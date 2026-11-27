package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/pkg/sdk"
)

type request struct {
	method, path, query, token, guard, body, filename, role string
}

type fixture struct {
	endpoint string
	requests chan request
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{requests: make(chan request, 30)}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen := request{
				method: r.Method, path: r.URL.Path, query: r.URL.RawQuery,
				token: r.Header.Get("Authorization"), guard: r.Header.Get(sdk.RequestHeader),
			}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				defer r.MultipartForm.RemoveAll()
				seen.role = r.FormValue("role")
				field := "image"
				if strings.HasSuffix(r.URL.Path, "/files") {
					field = "file"
				}
				file, header, err := r.FormFile(field)
				if err != nil {
					t.Error(err)
				} else {
					body, _ := io.ReadAll(file)
					_ = file.Close()
					seen.filename, seen.body = header.Filename, string(body)
				}
			} else if r.Body != nil {
				body, _ := io.ReadAll(r.Body)
				seen.body = string(body)
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
			f.requests <- seen
			next.ServeHTTP(w, r)
		})
	})
	jsonResponse := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
	router.Get(sdk.APIPrefix+"/info", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, `{"tracer":"auto","version":"server-test"}`)
	})
	router.Get(sdk.APIPrefix+"/floorplans", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "next" {
			jsonResponse(w, `{"plans":[{"id":"second"}]}`)
			return
		}
		jsonResponse(w, `{"plans":[{"id":"first"}],"nextCursor":"next"}`)
	})
	router.Get(sdk.APIPrefix+"/floorplans/{id}", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, `{"id":"first","status":"draft"}`)
	})
	router.Get(
		sdk.APIPrefix+"/floorplans/{id}/processing",
		func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, `{"planId":"first","status":"tracing"}`)
		},
	)
	router.Post(sdk.APIPrefix+"/floorplans", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, `{"plan":{"id":"first"},"duplicate":false}`)
	})
	router.Post(
		sdk.APIPrefix+"/floorplans/{id}/{action}",
		func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, `{"plan":{"id":"first"},"was":"draft"}`)
		},
	)
	router.Delete(
		sdk.APIPrefix+"/floorplans/{id}/files/{sha}",
		func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, `{"plan":{"id":"first"}}`)
		},
	)
	router.Get(
		sdk.APIPrefix+"/floorplans/{id}/files/{sha}",
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "file-content")
		},
	)
	router.Get(
		sdk.APIPrefix+"/floorplans/{id}/artifacts/{artifact}",
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "artifact-content")
		},
	)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		router.Method(
			method,
			sdk.APIPrefix+"/layer-profiles/{name}",
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				jsonResponse(w, `{"name":"acme","profile":{"rules":[]}}`)
			}),
		)
	}
	router.Get(sdk.OperatorPrefix+"/runtime", func(w http.ResponseWriter, _ *http.Request) {
		jsonResponse(w, `{"workers":2,"tracer":"auto"}`)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	f.endpoint = server.URL
	return f
}

func (f *fixture) run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := App{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(key string) (string, bool) {
			values := map[string]string{
				"SPACEWEAVECTL_ENDPOINT":       f.endpoint,
				"SPACEWEAVECTL_TOKEN":          "product-token",
				"SPACEWEAVECTL_OPERATOR_TOKEN": "operator-token",
			}
			value, ok := values[key]
			return value, ok
		},
		HTTPClient: &http.Client{},
	}
	code := Execute(t.Context(), app, args)
	return code, stdout.String(), stderr.String()
}

func TestRemoteCommandsUsePublicAPIs(t *testing.T) {
	cases := []struct {
		name, want, method, path, stdin string
		args                            []string
	}{
		{"info", "server-test", "GET", "/info", "", []string{"info"}},
		{
			"list",
			"first",
			"GET",
			"/floorplans",
			"",
			[]string{"floorplans", "list", "--limit", "7", "--cursor", "start"},
		},
		{"get", "draft", "GET", "/floorplans/first", "", []string{"floorplans", "get", "first"}},
		{
			"processing",
			"tracing",
			"GET",
			"/floorplans/first/processing",
			"",
			[]string{"floorplans", "processing", "first"},
		},
		{
			"approve",
			"first",
			"POST",
			"/floorplans/first/approve",
			"",
			[]string{"floorplans", "approve", "first"},
		},
		{
			"reject",
			"first",
			"POST",
			"/floorplans/first/reject",
			"",
			[]string{"floorplans", "reject", "first", "--note", "fix walls"},
		},
		{
			"retry",
			"first",
			"POST",
			"/floorplans/first/retry",
			"",
			[]string{"floorplans", "retry", "first"},
		},
		{
			"retrace",
			"first",
			"POST",
			"/floorplans/first/retrace",
			"",
			[]string{"floorplans", "retrace", "first"},
		},
		{
			"detach",
			"first",
			"DELETE",
			"/floorplans/first/files/sha",
			"",
			[]string{"floorplans", "files", "detach", "first", "sha"},
		},
		{
			"get profile",
			"acme",
			"GET",
			"/layer-profiles/acme",
			"",
			[]string{"layer-profiles", "get", "acme"},
		},
		{
			"put profile",
			"acme",
			"PUT",
			"/layer-profiles/acme",
			`{"rules":[{"role":"wall","layer":"walls"}]}`,
			[]string{"layer-profiles", "put", "acme", "-"},
		},
		{
			"upload",
			"first",
			"POST",
			"/floorplans",
			"drawing",
			[]string{"floorplans", "upload", "-", "--name", "floor.dxf"},
		},
		{
			"attach",
			"first",
			"POST",
			"/floorplans/first/files",
			"drawing",
			[]string{
				"floorplans",
				"files",
				"attach",
				"first",
				"-",
				"--name",
				"spaces.dxf",
				"--role",
				"spaces",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			code, out, stderr := f.run(t, tc.stdin, tc.args...)
			if code != 0 || !strings.Contains(out, tc.want) || stderr != "" ||
				!json.Valid([]byte(out)) {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, out, stderr)
			}
			seen := <-f.requests
			if seen.path != sdk.APIPrefix+tc.path || seen.method != tc.method ||
				seen.token != "Bearer product-token" {
				t.Fatalf("request=%+v", seen)
			}
			if tc.method != "GET" && seen.guard != "1" {
				t.Fatal("missing SDK write guard")
			}
			if tc.name == "list" &&
				(!strings.Contains(seen.query, "limit=7") || !strings.Contains(seen.query, "cursor=start")) {
				t.Fatalf("query=%s", seen.query)
			}
			if tc.name == "reject" && !strings.Contains(seen.body, "fix walls") {
				t.Fatal("review note lost")
			}
			if tc.name == "attach" &&
				(seen.filename != "spaces.dxf" || seen.body != "drawing" || seen.role != "spaces") {
				t.Fatalf("attachment=%+v", seen)
			}
		})
	}
}

func TestListAllAndOperatorCredential(t *testing.T) {
	f := newFixture(t)
	code, out, stderr := f.run(t, "", "floorplans", "list", "--all")
	if code != 0 || !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, stderr)
	}
	<-f.requests
	next := <-f.requests
	if !strings.Contains(next.query, "cursor=next") {
		t.Fatal("next cursor not followed")
	}
	code, out, stderr = f.run(t, "", "operator", "runtime")
	if code != 0 || !strings.Contains(out, `"workers": 2`) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, stderr)
	}
	seen := <-f.requests
	if seen.token != "Bearer operator-token" || seen.path != sdk.OperatorPrefix+"/runtime" {
		t.Fatalf("operator request=%+v", seen)
	}
}

func TestLocalInputAndDownloadDestinations(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "office.dxf")
	if err := os.WriteFile(file, []byte("drawing bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := f.run(t, "", "floorplans", "upload", file)
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, stderr)
	}
	if seen := <-f.requests; seen.filename != "office.dxf" || seen.body != "drawing bytes" {
		t.Fatalf("upload=%+v", seen)
	}
	for _, args := range [][]string{
		{"floorplans", "download", "first", "sha"},
		{"floorplans", "files", "download", "first", "sha"},
		{"floorplans", "artifact", "first", "model"},
	} {
		code, out, stderr = f.run(t, "", args...)
		if code != 0 || !strings.HasSuffix(out, "-content") || stderr != "" {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, out, stderr)
		}
		<-f.requests
	}
	destination := filepath.Join(dir, "model.json")
	code, out, stderr = f.run(
		t,
		"",
		"floorplans",
		"artifact",
		"first",
		"model",
		"--output",
		destination,
	)
	if code != 0 || !strings.Contains(out, "model.json") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out, stderr)
	}
	<-f.requests
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "artifact-content" {
		t.Fatalf("saved=%q err=%v", contents, err)
	}
	code, _, stderr = f.run(
		t,
		"",
		"floorplans",
		"artifact",
		"first",
		"model",
		"--output",
		destination,
	)
	if code != 1 || !strings.Contains(stderr, "create download") {
		t.Fatalf("existing destination code=%d stderr=%s", code, stderr)
	}
	<-f.requests
	contents, _ = os.ReadFile(destination)
	if string(contents) != "artifact-content" {
		t.Fatal("existing file overwritten")
	}
}

func TestUsageFailuresDoNotSubmitWork(t *testing.T) {
	cases := []struct {
		name, stdin string
		args        []string
	}{
		{"unknown command", "", []string{"oops"}},
		{"unknown flag", "", []string{"--token", "secret", "info"}},
		{"missing ID", "", []string{"floorplans", "get"}},
		{"extra arg", "", []string{"info", "extra"}},
		{"stdin without name", "x", []string{"floorplans", "upload", "-"}},
		{"invalid artifact", "", []string{"floorplans", "artifact", "first", "credentials"}},
		{
			"invalid role",
			"x",
			[]string{
				"floorplans",
				"files",
				"attach",
				"first",
				"-",
				"--name",
				"floor.dxf",
				"--role",
				"architecture",
			},
		},
		{"invalid profile", `{"unknown":1}`, []string{"layer-profiles", "put", "acme", "-"}},
		{
			"extra profile object",
			`{"rules":[]} {"rules":[]}`,
			[]string{"layer-profiles", "put", "acme", "-"},
		},
		{
			"malformed extra profile",
			`{"rules":[]} malformed`,
			[]string{"layer-profiles", "put", "acme", "-"},
		},
		{"invalid endpoint", "", []string{"--endpoint", "https://user:secret@example.com", "info"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			code, out, stderr := f.run(t, tc.stdin, tc.args...)
			if code != 2 || out != "" || stderr == "" {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, out, stderr)
			}
			if len(f.requests) != 0 {
				t.Fatal("invalid command submitted remote work")
			}
		})
	}
}

func TestEndpointPrecedenceAndVersion(t *testing.T) {
	f := newFixture(t)
	code, _, stderr := (&fixture{endpoint: "http://unreachable.invalid"}).run(
		t,
		"",
		"--endpoint",
		f.endpoint,
		"info",
	)
	if code != 0 {
		t.Fatalf("endpoint flag did not override environment: %s", stderr)
	}
	<-f.requests
	code, out, stderr := f.run(t, "", "version")
	if code != 0 || !json.Valid([]byte(out)) || !strings.Contains(out, "version") || stderr != "" ||
		len(f.requests) != 0 {
		t.Fatalf("version code=%d stdout=%s stderr=%s", code, out, stderr)
	}
	t.Setenv("SPACEWEAVECTL_ENDPOINT", "")
	root := New(App{})
	if value, err := root.PersistentFlags().
		GetString("endpoint"); err != nil ||
		value != "http://127.0.0.1:8000" {
		t.Fatalf("default endpoint=%q err=%v", value, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRuntimeAndOutputFailures(t *testing.T) {
	f := newFixture(t)
	code, out, stderr := f.run(
		t,
		"",
		"floorplans",
		"upload",
		filepath.Join(t.TempDir(), "missing.dxf"),
	)
	if code != 1 || out != "" || !strings.Contains(stderr, "read input") {
		t.Fatalf("missing input code=%d stdout=%s stderr=%s", code, out, stderr)
	}
	var errorsOut bytes.Buffer
	app := App{
		Stdout: failingWriter{},
		Stderr: &errorsOut,
		Getenv: func(string) (string, bool) { return "", false },
	}
	if code := Execute(
		t.Context(),
		app,
		[]string{"version"},
	); code != 1 ||
		!strings.Contains(errorsOut.String(), "write output") {
		t.Fatalf("output code=%d stderr=%s", code, errorsOut.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	app = App{Stdout: &bytes.Buffer{}, Stderr: &errorsOut, Getenv: func(key string) (string, bool) {
		if key == "SPACEWEAVECTL_ENDPOINT" {
			return f.endpoint, true
		}
		return "", false
	}}
	if code := Execute(
		ctx,
		app,
		[]string{"info"},
	); code != 1 ||
		!strings.Contains(errorsOut.String(), "context canceled") {
		t.Fatalf("canceled code=%d stderr=%s", code, errorsOut.String())
	}
}

func TestRemoteProblemFailureAndIteratorFailure(t *testing.T) {
	router := chi.NewRouter()
	router.Get(sdk.APIPrefix+"/floorplans", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(403)
		_, _ = io.WriteString(
			w,
			`{"type":"about:blank","title":"Forbidden","status":403,"detail":"access denied"}`,
		)
	})
	router.Get(sdk.APIPrefix+"/floorplans/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	})
	server := httptest.NewServer(router)
	defer server.Close()
	f := &fixture{endpoint: server.URL}
	for _, args := range [][]string{{"floorplans", "list", "--all"}, {"floorplans", "get", "missing"}, {"floorplans", "artifact", "missing", "model"}} {
		code, out, stderr := f.run(t, "", args...)
		if code != 1 || out != "" || !strings.Contains(stderr, "HTTP") {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, out, stderr)
		}
	}
}

func TestProfileFileAndDownloadOutputFailure(t *testing.T) {
	f := newFixture(t)
	file := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(file, []byte(`{"rules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := f.run(t, "", "layer-profiles", "put", "acme", file)
	if code != 0 {
		t.Fatal(stderr)
	}
	<-f.requests
	var errorOut bytes.Buffer
	app := App{Stdout: failingWriter{}, Stderr: &errorOut, Getenv: func(key string) (string, bool) {
		if key == "SPACEWEAVECTL_ENDPOINT" {
			return f.endpoint, true
		}
		return "", false
	}}
	if code := Execute(
		t.Context(),
		app,
		[]string{"floorplans", "download", "first", "sha"},
	); code != 1 ||
		!strings.Contains(errorOut.String(), "download output") {
		t.Fatalf("output code=%d stderr=%s", code, errorOut.String())
	}
	<-f.requests
}
