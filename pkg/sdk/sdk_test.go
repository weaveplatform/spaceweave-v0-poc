package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/pkg/client/api"
)

func clientFor(t *testing.T, router http.Handler, options ...Option) *Client {
	t.Helper()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	c, err := New(server.URL, options...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func jsonReply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestPublicOperationsAndCredentialIsolation(t *testing.T) {
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			want := "Bearer product-secret"
			if strings.HasPrefix(r.URL.Path, OperatorPrefix) {
				want = "Bearer operator-secret"
			}
			if r.Header.Get("Authorization") != want {
				t.Errorf("%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
			}
			if r.Method != http.MethodGet && r.Header.Get(RequestHeader) != "1" {
				t.Errorf("%s lacks mutation guard", r.Method)
			}
			next.ServeHTTP(w, r)
		})
	})
	router.Get(APIPrefix+"/floorplans", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "7" {
			t.Error("list limit not passed")
		}
		jsonReply(w, map[string]any{"plans": []any{map[string]any{"id": "plan-one"}}})
	})
	router.Get(APIPrefix+"/floorplans/{id}", func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, map[string]any{"id": "plan-one", "status": "draft"})
	})
	for _, action := range []string{"approve", "reject", "retry", "retrace"} {
		router.Post(
			APIPrefix+"/floorplans/{id}/"+action,
			func(w http.ResponseWriter, r *http.Request) {
				var body api.ReviewRequest
				if err := json.NewDecoder(r.Body).
					Decode(&body); err != nil || body.Note == nil ||
					*body.Note != "review note" {
					t.Errorf("review body=%+v err=%v", body, err)
				}
				jsonReply(
					w,
					map[string]any{"plan": map[string]string{"id": "plan-one"}, "was": "draft"},
				)
			},
		)
	}
	router.Delete(
		APIPrefix+"/floorplans/{id}/files/{sha}",
		func(w http.ResponseWriter, _ *http.Request) {
			jsonReply(w, map[string]any{"plan": map[string]string{"id": "plan-one"}})
		},
	)
	router.Get(
		APIPrefix+"/floorplans/{id}/processing",
		func(w http.ResponseWriter, _ *http.Request) {
			jsonReply(w, map[string]any{"planId": "plan-one", "status": "tracing", "attempt": 2})
		},
	)
	router.Get(
		APIPrefix+"/floorplans/{id}/artifacts/{artifact}",
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "artifact bytes")
		},
	)
	router.Get(
		APIPrefix+"/floorplans/{id}/files/{sha}",
		func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "file bytes")
		},
	)
	router.Get(APIPrefix+"/layer-profiles/{name}", func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, map[string]any{"name": "acme", "profile": map[string]any{"rules": []any{}}})
	})
	router.Put(APIPrefix+"/layer-profiles/{name}", func(w http.ResponseWriter, r *http.Request) {
		var profile api.LayerProfile
		if err := json.NewDecoder(r.Body).Decode(&profile); err != nil || len(profile.Rules) != 1 {
			t.Errorf("profile=%+v err=%v", profile, err)
		}
		jsonReply(w, map[string]any{"name": "acme", "profile": profile})
	})
	router.Get(
		APIPrefix+"/info",
		func(w http.ResponseWriter, _ *http.Request) { jsonReply(w, map[string]any{"tracer": "auto"}) },
	)
	router.Get(
		OperatorPrefix+"/runtime",
		func(w http.ResponseWriter, _ *http.Request) { jsonReply(w, map[string]any{"workers": 2}) },
	)
	c := clientFor(t, router, WithToken("product-secret"), WithOperatorToken("operator-secret"))
	ctx := t.Context()
	limit := 7
	page, err := c.ListFloorplans(ctx, &api.ListFloorplansParams{Limit: &limit})
	if err != nil || len(page.Plans) != 1 || page.Plans[0].Id != "plan-one" {
		t.Fatalf("list=%+v err=%v", page, err)
	}
	plan, err := c.GetFloorplan(ctx, "plan-one")
	if err != nil || plan.Status != api.Draft {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	for _, action := range []ReviewAction{Approve, Reject, RetryTrace, Retrace} {
		if _, err := c.ReviewFloorplan(ctx, "plan-one", action, "review note"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ReviewFloorplan(ctx, "plan-one", "unknown", ""); !errors.Is(err, ErrInput) {
		t.Fatalf("unknown action: %v", err)
	}
	if _, err := c.DetachFile(ctx, "plan-one", "file-sha"); err != nil {
		t.Fatal(err)
	}
	processing, err := c.GetProcessing(ctx, "plan-one")
	if err != nil || processing.Attempt != 2 {
		t.Fatalf("processing=%+v err=%v", processing, err)
	}
	profile, err := c.GetProfile(ctx, "acme")
	if err != nil || profile.Name != "acme" {
		t.Fatalf("profile=%+v err=%v", profile, err)
	}
	if _, err := c.PutProfile(
		ctx,
		"acme",
		api.LayerProfile{Rules: []api.LayerRule{{Role: "wall"}}},
	); err != nil {
		t.Fatal(err)
	}
	info, err := c.GetInfo(ctx)
	if err != nil || info.Tracer != "auto" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	runtime, err := c.GetRuntime(ctx)
	if err != nil || runtime.Workers != 2 {
		t.Fatalf("runtime=%+v err=%v", runtime, err)
	}
	artifact, err := c.GetArtifact(ctx, "plan-one", api.Model)
	assertBody(t, artifact, err, "artifact bytes")
	file, err := c.DownloadFile(ctx, "plan-one", "file-sha")
	assertBody(t, file, err, "file bytes")
}

func assertBody(t *testing.T, response *http.Response, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != want {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestMultipartStreaming(t *testing.T) {
	router := chi.NewRouter()
	var calls atomic.Int32
	router.Post(APIPrefix+"/floorplans", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		checkMultipart(t, r, "image", "floor.dxf", "drawing bytes", "")
		jsonReply(w, map[string]any{"plan": map[string]any{"id": "uploaded"}, "duplicate": false})
	})
	router.Post(APIPrefix+"/floorplans/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		checkMultipart(t, r, "file", "spaces.dxf", "space bytes", "spaces")
		jsonReply(w, map[string]any{"plan": map[string]any{"id": "uploaded"}, "duplicate": true})
	})
	c := clientFor(t, router)
	result, err := c.UploadFloorplan(
		t.Context(),
		"/private/drawings/floor.dxf",
		strings.NewReader("drawing bytes"),
	)
	if err != nil || result.Plan.Id != "uploaded" || result.Duplicate {
		t.Fatalf("upload=%+v err=%v", result, err)
	}
	result, err = c.AttachFile(
		t.Context(),
		"uploaded",
		"spaces.dxf",
		api.AttachFloorplanFileMultipartBodyRoleSpaces,
		strings.NewReader("space bytes"),
	)
	if err != nil || !result.Duplicate {
		t.Fatalf("attachment=%+v err=%v", result, err)
	}
	if _, err := c.UploadFloorplan(t.Context(), "floor.dxf", nil); !errors.Is(err, ErrInput) {
		t.Fatalf("nil source=%v", err)
	}
	if _, err := c.AttachFile(
		t.Context(),
		"uploaded",
		"spaces.dxf",
		"architecture",
		strings.NewReader("x"),
	); !errors.Is(
		err,
		ErrInput,
	) {
		t.Fatalf("invalid role=%v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("invalid inputs reached server: %d", calls.Load())
	}
}

func checkMultipart(t *testing.T, r *http.Request, field, filename, contents, role string) {
	t.Helper()
	reader, err := r.MultipartReader()
	if err != nil {
		t.Error(err)
		return
	}
	found := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Error(err)
			return
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Error(err)
		}
		if part.FormName() == "role" {
			if string(body) != role {
				t.Errorf("role=%s", body)
			}
			continue
		}
		found = true
		if part.FormName() != field || part.FileName() != filename || string(body) != contents {
			t.Errorf("part=%s filename=%s body=%q", part.FormName(), part.FileName(), body)
		}
	}
	if !found {
		t.Error("file part absent")
	}
}

func TestProblemsAndDecodingFailures(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		wantTitle  string
	}{
		{
			"problem",
			`{"type":"urn:spaceweave:state","title":"Conflict","status":409,"detail":"already running","instance":"/run/1"}`,
			409,
			"Conflict",
		},
		{"plain fallback", "upstream failure", 502, "Bad Gateway"},
		{"partial problem", `{"detail":"missing plan"}`, 404, "Not Found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			router.Get(APIPrefix+"/info", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			c := clientFor(t, router, WithRetry(Retry{MaxAttempts: 1}))
			_, err := c.GetInfo(t.Context())
			var problem *Problem
			if !errors.As(err, &problem) || problem.Status != tc.status ||
				problem.Title != tc.wantTitle ||
				problem.Error() == "" {
				t.Fatalf("problem=%+v err=%v", problem, err)
			}
			if tc.name == "problem" &&
				(problem.Detail != "already running" || problem.Instance != "/run/1") {
				t.Fatalf("problem fields lost: %+v", problem)
			}
		})
	}
	router := chi.NewRouter()
	router.Get(
		APIPrefix+"/info",
		func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "not-json") },
	)
	c := clientFor(t, router)
	if _, err := c.GetInfo(t.Context()); err == nil {
		t.Fatal("malformed successful JSON accepted")
	}
}

func TestGetRetriesNeverRepeatMutations(t *testing.T) {
	router := chi.NewRouter()
	var gets, posts atomic.Int32
	router.Get(APIPrefix+"/info", func(w http.ResponseWriter, _ *http.Request) {
		if gets.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(503)
			return
		}
		jsonReply(w, map[string]any{"tracer": "auto"})
	})
	router.Post(APIPrefix+"/floorplans/{id}/approve", func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(503)
	})
	c := clientFor(t, router)
	if _, err := c.GetInfo(t.Context()); err != nil || gets.Load() != 3 {
		t.Fatalf("GET attempts=%d err=%v", gets.Load(), err)
	}
	if _, err := c.ReviewFloorplan(
		t.Context(),
		"one",
		Approve,
		"",
	); err == nil ||
		posts.Load() != 1 {
		t.Fatalf("write attempts=%d err=%v", posts.Load(), err)
	}
}

func TestRetryAfterBoundsAndContextCancellation(t *testing.T) {
	for _, value := range []string{"3600", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), "9223372036854775807"} {
		t.Run(value, func(t *testing.T) {
			var calls atomic.Int32
			router := chi.NewRouter()
			router.Get(APIPrefix+"/info", func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", value)
				w.WriteHeader(503)
			})
			c := clientFor(t, router)
			_, err := c.GetInfo(t.Context())
			var problem *Problem
			if !errors.As(err, &problem) || calls.Load() != 1 {
				t.Fatalf("calls=%d err=%v", calls.Load(), err)
			}
		})
	}
	router := chi.NewRouter()
	var calls atomic.Int32
	router.Get(APIPrefix+"/info", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
	})
	c := clientFor(t, router)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.GetInfo(ctx); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("deadline calls=%d err=%v", calls.Load(), err)
	}
}

func TestCursorIterationAndEarlyStop(t *testing.T) {
	var calls atomic.Int32
	router := chi.NewRouter()
	router.Get(APIPrefix+"/floorplans", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		cursor := r.URL.Query().Get("cursor")
		page := map[string]any{"plans": []any{map[string]any{"id": "first"}}, "nextCursor": "next"}
		if cursor == "next" {
			page = map[string]any{"plans": []any{map[string]any{"id": "second"}}}
		}
		if cursor == "loop" {
			page["nextCursor"] = "loop"
		}
		jsonReply(w, page)
	})
	c := clientFor(t, router)
	var ids []string
	for floorplan, err := range c.Floorplans(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, floorplan.Id)
	}
	if strings.Join(ids, ",") != "first,second" || calls.Load() != 2 {
		t.Fatalf("ids=%v calls=%d", ids, calls.Load())
	}
	for _, err := range c.Floorplans(t.Context(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if calls.Load() != 3 {
		t.Fatal("iterator requested another page after break")
	}
	cursor := "loop"
	var iterationErr error
	for _, err := range c.Floorplans(t.Context(), &api.ListFloorplansParams{Cursor: &cursor}) {
		iterationErr = err
	}
	if !errors.Is(iterationErr, ErrPagination) {
		t.Fatalf("repeated cursor=%v", iterationErr)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, err := range c.Floorplans(ctx, nil) {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation=%v", err)
		}
	}
}

func TestRedirectsConfineSecretsAndWrites(t *testing.T) {
	var leaked atomic.Int32
	outside := chi.NewRouter()
	outside.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("credential leaked")
		}
		jsonReply(w, map[string]any{})
	}))
	external := httptest.NewServer(outside)
	defer external.Close()
	for _, location := range []string{external.URL + APIPrefix + "/info", OperatorPrefix + "/runtime"} {
		t.Run(location, func(t *testing.T) {
			router := chi.NewRouter()
			router.Get(APIPrefix+"/info", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, location, http.StatusFound)
			})
			c := clientFor(t, router, WithToken("secret"))
			_, err := c.GetInfo(t.Context())
			var problem *Problem
			if !errors.As(err, &problem) || problem.Status != 302 {
				t.Fatalf("redirect=%v", err)
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("out-of-scope redirect was followed")
	}
	router := chi.NewRouter()
	var writes atomic.Int32
	router.Post(APIPrefix+"/floorplans/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		http.Redirect(w, r, APIPrefix+"/floorplans/second/approve", http.StatusTemporaryRedirect)
	})
	c := clientFor(t, router)
	if _, err := c.ReviewFloorplan(
		t.Context(),
		"one",
		Approve,
		"",
	); err == nil ||
		writes.Load() != 1 {
		t.Fatalf("redirect repeated mutation: writes=%d err=%v", writes.Load(), err)
	}
}

func TestClientConfigurationAndScope(t *testing.T) {
	for _, endpoint := range []string{"not a URL", "ftp://example.com", "http://name:secret@example.com", "http://example.com/path", "http://example.com?", "http://example.com#fragment"} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := New(endpoint); !errors.Is(err, ErrEndpoint) {
				t.Fatalf("endpoint accepted: %v", err)
			}
		})
	}
	if _, err := New("http://localhost", WithHTTPClient(nil)); !errors.Is(err, ErrInput) {
		t.Fatalf("nil client=%v", err)
	}
	if _, err := New("http://localhost", WithRetry(Retry{})); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid retry=%v", err)
	}
	router := chi.NewRouter()
	router.Get(APIPrefix+"/info", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("credential from other namespace was retained")
		}
		jsonReply(w, map[string]any{})
	})
	c := clientFor(t, router, WithOperatorToken("operator-only"))
	resp, err := c.API.GetInfo(t.Context(), func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer inappropriate")
		return nil
	})
	assertBody(t, resp, err, "{}\n")
	_, err = c.API.GetInfo(t.Context(), func(_ context.Context, r *http.Request) error {
		r.URL, _ = url.Parse("http://elsewhere.invalid/api/v1/info")
		return nil
	})
	if !errors.Is(err, ErrScope) {
		t.Fatalf("scope bypass=%v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestTransportFailureRetriesOnlyReads(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			var count int
			httpClient := &http.Client{
				Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
					count++
					return nil, io.ErrUnexpectedEOF
				}),
			}
			c, err := New(
				"http://spaceweave.invalid",
				WithHTTPClient(httpClient),
				WithRetry(Retry{MaxAttempts: 3}),
			)
			if err != nil {
				t.Fatal(err)
			}
			if method == http.MethodGet {
				_, err = c.GetInfo(t.Context())
			} else {
				_, err = c.ReviewFloorplan(t.Context(), "one", Approve, "")
			}
			want := 1
			if method == http.MethodGet {
				want = 3
			}
			if !errors.Is(err, io.ErrUnexpectedEOF) || count != want {
				t.Fatalf("attempts=%d want=%d err=%v", count, want, err)
			}
		})
	}
}

func ExampleClient_Floorplans() {
	// Use a server origin, then iterate lazily through the public REST API.
	client, err := New("https://spaceweave.example.com", WithToken("product-token"))
	if err != nil {
		panic(err)
	}
	for floorplan, err := range client.Floorplans(context.Background(), nil) {
		if err != nil {
			break
		}
		fmt.Println(floorplan.Id)
	}
}
