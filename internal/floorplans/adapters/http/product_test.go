package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/weaveplatform/spaceweave/internal/floorplans/adapters/localimport/catalog"
	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/autotrace/profile"
	transport "github.com/weaveplatform/spaceweave/internal/transport/gen/api"
)

func productFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.srv.Assets = f.store
	f.srv.Profiles = &profile.Library{Dir: filepath.Join(f.store.Dir, "profiles")}
	f.h = f.srv.ProductHandler()
	return f
}

func productForm(
	t *testing.T,
	field, filename string,
	data []byte,
	fields map[string]string,
) (string, *bytes.Buffer) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if field != "" {
		part, err := writer.CreateFormFile(field, filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return writer.FormDataContentType(), &body
}

func productUpload(t *testing.T, f *fixture, name string, mark uint8) api.Entry {
	t.Helper()
	ct, body := productForm(t, "image", name, pngBytes(t, 30, 20, mark), nil)
	w := f.post("/api/v1/floorplans", ct, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var reply uploadReply
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Plan
}

func assertProblem(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	var body struct {
		Type, Title string
		Status      int
	}
	if w.Code != status || w.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("expected problem %d: got %d %s", status, w.Code, w.Body.String())
	}
	if err := json.Unmarshal(
		w.Body.Bytes(),
		&body,
	); err != nil || body.Status != status || body.Type == "" ||
		body.Title == "" {
		t.Fatalf("malformed problem: %s %v", w.Body.String(), err)
	}
}

func TestProductUploadPaginationAndReview(t *testing.T) {
	f := productFixture(t)
	first := productUpload(t, f, "alpha.png", 1)
	second := productUpload(t, f, "zulu.png", 2)
	if first.Source != "/api/v1/floorplans/"+first.ID+"/artifacts/source" {
		t.Fatalf("source is not a supported API URL: %s", first.Source)
	}
	ct, body := productForm(t, "image", "alpha.png", pngBytes(t, 30, 20, 1), nil)
	duplicate := f.post("/api/v1/floorplans", ct, body)
	if duplicate.Code != http.StatusOK ||
		!strings.Contains(duplicate.Body.String(), `"duplicate":true`) {
		t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body.String())
	}
	page := f.do(httptest.NewRequest(http.MethodGet, "/api/v1/floorplans?limit=1", nil))
	var listed struct {
		Plans      []api.Entry `json:"plans"`
		NextCursor string      `json:"nextCursor"`
	}
	if err := json.Unmarshal(
		page.Body.Bytes(),
		&listed,
	); err != nil || page.Code != http.StatusOK || len(listed.Plans) != 1 || listed.Plans[0].ID != first.ID ||
		listed.NextCursor == "" {
		t.Fatalf("page one: %s %v", page.Body.String(), err)
	}
	page = f.do(
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/floorplans?limit=1&cursor="+listed.NextCursor,
			nil,
		),
	)
	if err := json.Unmarshal(
		page.Body.Bytes(),
		&listed,
	); err != nil || len(listed.Plans) != 1 ||
		listed.Plans[0].ID != second.ID {
		t.Fatalf("page two: %s %v", page.Body.String(), err)
	}
	get := f.do(httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/"+first.ID, nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), first.ID) {
		t.Fatalf("get: %d %s", get.Code, get.Body.String())
	}
	assertProblem(
		t,
		f.post("/api/v1/floorplans/"+first.ID+"/approve", "", nil),
		http.StatusConflict,
	)
	if err := f.store.Transition(first.ID, api.Tracing, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Transition(first.ID, api.Draft, nil); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"approve", "reject"} {
		w := f.post(
			"/api/v1/floorplans/"+first.ID+"/"+action,
			"application/json",
			strings.NewReader(`{"note":"review note"}`),
		)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	if err := f.store.Transition(first.ID, api.Tracing, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Transition(
		first.ID,
		api.Failed,
		func(e *api.Entry) { e.Error = "/private/provider/key: failure" },
	); err != nil {
		t.Fatal(err)
	}
	processing := f.do(
		httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/"+first.ID+"/processing", nil),
	)
	if processing.Code != http.StatusOK ||
		strings.Contains(processing.Body.String(), "/private/") ||
		!strings.Contains(processing.Body.String(), `"status":"failed"`) {
		t.Fatalf("processing: %d %s", processing.Code, processing.Body.String())
	}
	if w := f.post("/api/v1/floorplans/"+first.ID+"/retry", "", nil); w.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	if err := f.store.Transition(first.ID, api.Tracing, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Transition(first.ID, api.Draft, nil); err != nil {
		t.Fatal(err)
	}
	if w := f.post("/api/v1/floorplans/"+first.ID+"/retrace", "", nil); w.Code != http.StatusOK {
		t.Fatalf("retrace: %d %s", w.Code, w.Body.String())
	}
}

func TestProductProfilesAndContractValidation(t *testing.T) {
	f := productFixture(t)
	entry := productUpload(t, f, "office.png", 1)
	for _, bad := range []string{
		`{"unknown":true}`, `{} {}`, `null`, `{"note":null}`, `{"note":10}`, "broken",
		`{"NOTE":"review note"}`, `{"Note":"review note"}`,
	} {
		assertProblem(
			t,
			f.post(
				"/api/v1/floorplans/"+entry.ID+"/approve",
				"application/json",
				strings.NewReader(bad),
			),
			http.StatusBadRequest,
		)
	}
	for _, bad := range []string{
		`{}`, `{"rules":null}`, `{"rules":[{}]}`, `{"rules":[{"role":"wall","unknown":1}]}`, `{"rules":[]} {}`,
		`{"rules":[],"Description":"variant"}`, `{"rules":[{"role":"wall","Layer":"A-WALL"}]}`,
	} {
		r := httptest.NewRequest(
			http.MethodPut,
			"/api/v1/layer-profiles/acme",
			strings.NewReader(bad),
		)
		r.Header.Set(RequestHeader, "1")
		r.Header.Set("Content-Type", "application/json")
		assertProblem(t, f.do(r), http.StatusBadRequest)
	}
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/layer-profiles/acme",
		strings.NewReader(`{"rules":[{"role":"wall","layer":"A-WALL"}]}`),
	)
	r.Header.Set(RequestHeader, "1")
	r.Header.Set("Content-Type", "application/json")
	w := f.do(r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"active":true`) {
		t.Fatalf("profile update: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"acme", "generic", "new-client"} {
		w := f.do(httptest.NewRequest(http.MethodGet, "/api/v1/layer-profiles/"+name, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"roles":`) {
			t.Fatalf("profile %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	info := f.do(httptest.NewRequest(http.MethodGet, "/api/v1/info", nil))
	if info.Code != http.StatusOK || !strings.Contains(info.Body.String(), `"profile":"acme"`) {
		t.Fatalf("info: %d %s", info.Code, info.Body.String())
	}
	for _, path := range []string{
		"/api/v1/floorplans?limit=0", "/api/v1/floorplans?limit=201", "/api/v1/floorplans?limit=no",
		"/api/v1/floorplans?limit=1&limit=2", "/api/v1/floorplans?cursor=%25", "/api/v1/floorplans?cursor=" + base64.RawURLEncoding.EncodeToString([]byte("../x")),
		"/api/v1/layer-profiles/UPPER", "/api/v1/floorplans/" + entry.ID + "/files/not-a-sha",
		"/api/v1/floorplans/" + entry.ID + "/artifacts/unknown",
	} {
		assertProblem(
			t,
			f.do(httptest.NewRequest(http.MethodGet, path, nil)),
			http.StatusBadRequest,
		)
	}
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/missing", nil)),
		http.StatusNotFound,
	)
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodPost, "/api/v1/floorplans/"+entry.ID+"/approve", nil)),
		http.StatusBadRequest,
	)
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil)),
		http.StatusNotFound,
	)
}

func TestProductMultipartBoundsAndAssets(t *testing.T) {
	f := productFixture(t)
	entry := productUpload(t, f, "office.png", 1)
	ct, data := productForm(
		t,
		"file",
		"reference.png",
		pngBytes(t, 30, 20, 2),
		map[string]string{"role": "reference"},
	)
	w := f.post("/api/v1/floorplans/"+entry.ID+"/files", ct, data)
	if w.Code != http.StatusCreated {
		t.Fatalf("attachment: %d %s", w.Code, w.Body.String())
	}
	var reply attachReply
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	attachment := reply.Plan.Attached(api.RoleReference)[0]
	download := f.do(httptest.NewRequest(http.MethodGet, attachment.Upload, nil))
	if download.Code != http.StatusOK ||
		!strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") ||
		!bytes.Equal(download.Body.Bytes(), pngBytes(t, 30, 20, 2)) {
		t.Fatalf("download: %d %s", download.Code, download.Body.String())
	}
	for _, kind := range []string{"source", "original"} {
		w := f.do(
			httptest.NewRequest(
				http.MethodGet,
				"/api/v1/floorplans/"+entry.ID+"/artifacts/"+kind,
				nil,
			),
		)
		if w.Code != http.StatusOK || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("asset %s: %d %s", kind, w.Code, w.Body.String())
		}
	}
	if err := os.WriteFile(
		filepath.Join(f.store.PlanDir(entry.ID), "plan.json"),
		[]byte(`{"SOURCE":{"url":"generated/private/source.png"},"WIDTH":10}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	model := f.do(
		httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/"+entry.ID+"/artifacts/model", nil),
	)
	if model.Code != http.StatusOK || strings.Contains(model.Body.String(), "generated/") ||
		!strings.Contains(model.Body.String(), "/artifacts/source") {
		t.Fatalf("model source: %d %s", model.Code, model.Body.String())
	}
	unchanged, err := os.ReadFile(filepath.Join(f.store.PlanDir(entry.ID), "plan.json"))
	if err != nil || !strings.Contains(string(unchanged), "generated/private/source.png") {
		t.Fatalf("API projection rewrote the stored model: %s %v", unchanged, err)
	}
	if err := os.WriteFile(
		filepath.Join(f.store.PlanDir(entry.ID), "report.json"),
		[]byte(`{"error":"provider /private/token failed","layers":[]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	report := f.do(
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/floorplans/"+entry.ID+"/artifacts/report",
			nil,
		),
	)
	if report.Code != http.StatusOK || strings.Contains(report.Body.String(), "/private") ||
		!strings.Contains(report.Body.String(), `"layers":[]`) {
		t.Fatalf("report leaked internal details: %d %s", report.Code, report.Body.String())
	}
	r := httptest.NewRequest(http.MethodDelete, attachment.Upload, nil)
	r.Header.Set(RequestHeader, "1")
	if w := f.do(r); w.Code != http.StatusOK {
		t.Fatalf("detach: %d %s", w.Code, w.Body.String())
	}
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodGet, attachment.Upload, nil)),
		http.StatusNotFound,
	)
	f.srv.MaxUpload = 64
	ct, data = productForm(t, "image", "big.png", pngBytes(t, 30, 20, 3), nil)
	assertProblem(t, f.post("/api/v1/floorplans", ct, data), http.StatusRequestEntityTooLarge)
	f.srv.MaxUpload = 0
	ct, data = productForm(
		t,
		"image",
		"office.png",
		pngBytes(t, 30, 20, 3),
		map[string]string{"unexpected": "field"},
	)
	assertProblem(t, f.post("/api/v1/floorplans", ct, data), http.StatusBadRequest)
	ct, data = productForm(t, "image", "office.png", []byte("not png"), nil)
	assertProblem(t, f.post("/api/v1/floorplans", ct, data), http.StatusUnprocessableEntity)
}

type failingProductStore struct{ api.Service }

var errProductPrivate = errors.New("provider token /private/tenant/secret")

func (failingProductStore) List() ([]api.Entry, error) { return nil, errProductPrivate }

func TestProductRedactsUnexpectedFailures(t *testing.T) {
	f := productFixture(t)
	f.srv.Store = failingProductStore{f.srv.Store}
	w := f.do(httptest.NewRequest(http.MethodGet, "/api/v1/floorplans", nil))
	assertProblem(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "secret") ||
		strings.Contains(w.Body.String(), "/private") {
		t.Fatalf("internal detail leaked: %s", w.Body.String())
	}
}

func TestProductResponsesMatchPublishedSchemas(t *testing.T) {
	f := productFixture(t)
	entry := productUpload(t, f, "office.png", 1)
	spec, err := transport.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ path, route string }{
		{"/info", "/info"},
		{"/floorplans", "/floorplans"},
		{"/floorplans/" + entry.ID, "/floorplans/{id}"},
		{"/floorplans/" + entry.ID + "/processing", "/floorplans/{id}/processing"},
		{"/layer-profiles/generic", "/layer-profiles/{name}"},
		{"/floorplans/missing", "/floorplans/{id}"},
		{"/floorplans?limit=201", "/floorplans"},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, productPrefix+test.path, nil)
			response := f.do(request)
			item := spec.Paths.Value(test.route)
			input := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{
					Request: request,
					Route: &routers.Route{
						Spec:      spec,
						Path:      test.route,
						PathItem:  item,
						Method:    http.MethodGet,
						Operation: item.Get,
					},
				},
				Status: response.Code, Header: response.Header(),
			}
			if err := openapi3filter.ValidateResponse(
				t.Context(),
				input.SetBodyBytes(response.Body.Bytes()),
			); err != nil {
				t.Fatalf("response violates published schema: %s: %v", response.Body.String(), err)
			}
		})
	}
}

func TestProductRejectsMalformedAndProtectedMutations(t *testing.T) {
	f := productFixture(t)
	entry := productUpload(t, f, "office.png", 1)
	for _, request := range []struct {
		method, path, contentType, body string
		status                          int
	}{
		{http.MethodPost, "/floorplans", "multipart/form-data; boundary=broken", "broken multipart", http.StatusBadRequest},
		{http.MethodPost, "/floorplans", "application/json", "{}", http.StatusBadRequest},
		{http.MethodPost, "/floorplans/" + entry.ID + "/approve", "text/plain", "{}", http.StatusUnsupportedMediaType},
		{http.MethodPut, "/layer-profiles/generic", "application/json", `{"rules":[]}`, http.StatusForbidden},
		{http.MethodPut, "/layer-profiles/acme", "application/json", `{"rules":[{"role":"not-real"}]}`, http.StatusUnprocessableEntity},
		{http.MethodPut, "/layer-profiles/acme", "application/json", `{"rules":[null]}`, http.StatusBadRequest},
		{http.MethodPut, "/layer-profiles/acme", "application/json", `{"rules":{}}`, http.StatusBadRequest},
		{http.MethodPost, "/floorplans/" + entry.ID + "/approve", "application/json", `{"note":"` + strings.Repeat("a", maxNote+1) + `"}`, http.StatusBadRequest},
		{http.MethodPut, "/layer-profiles/acme", "application/json", `{"rules":[],"description":"` + strings.Repeat("a", maxProfile) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		r := httptest.NewRequest(
			request.method,
			productPrefix+request.path,
			strings.NewReader(request.body),
		)
		r.Header.Set(RequestHeader, "1")
		r.Header.Set("Content-Type", request.contentType)
		assertProblem(t, f.do(r), request.status)
	}
	for _, test := range []struct {
		field, filename, role string
		data                  []byte
		status                int
	}{
		{"", "", "", nil, http.StatusBadRequest},
		{"image", ".hidden.png", "", pngBytes(t, 30, 20, 2), http.StatusBadRequest},
		{"image", "unsupported.pdf", "", []byte("%PDF-1.4"), http.StatusUnsupportedMediaType},
		{"file", "reference.png", "", pngBytes(t, 30, 20, 2), http.StatusBadRequest},
		{"file", "reference.png", "unknown", pngBytes(t, 30, 20, 2), http.StatusBadRequest},
		{"file", "reference.png", strings.Repeat("a", maxField+1), pngBytes(t, 30, 20, 2), http.StatusRequestEntityTooLarge},
	} {
		fields := map[string]string{}
		path := "/api/v1/floorplans"
		if test.field == "file" {
			fields["role"] = test.role
			path += "/" + entry.ID + "/files"
		}
		ct, body := productForm(t, test.field, test.filename, test.data, fields)
		assertProblem(t, f.post(path, ct, body), test.status)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/floorplans/"+entry.ID+"/approve", nil)
	r.Header.Set(RequestHeader, "1")
	r.Header.Set("Origin", "https://untrusted.example")
	assertProblem(t, f.do(r), http.StatusForbidden)
	assertProblem(
		t,
		f.do(
			httptest.NewRequest(
				http.MethodGet,
				"/api/v1/floorplans/"+strings.Repeat("a", 256),
				nil,
			),
		),
		http.StatusBadRequest,
	)
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/missing/processing", nil)),
		http.StatusNotFound,
	)
	r = httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/floorplans/"+entry.ID+"/files/"+strings.Repeat("a", 64),
		nil,
	)
	r.Header.Set(RequestHeader, "1")
	assertProblem(t, f.do(r), http.StatusNotFound)
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodPatch, "/api/v1/info", nil)),
		http.StatusMethodNotAllowed,
	)
}

func TestProductArtifactsCannotEscapeTheirPlan(t *testing.T) {
	f := productFixture(t)
	entry := productUpload(t, f, "office.png", 1)
	assertProblem(
		t,
		f.do(
			httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/%2e%2e/artifacts/source", nil),
		),
		http.StatusNotFound,
	)
	if err := f.store.Update(func(c *catalog.Catalog) error {
		c.Plans[0].Source = "../catalog.json"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertProblem(
		t,
		f.do(
			httptest.NewRequest(
				http.MethodGet,
				"/api/v1/floorplans/"+entry.ID+"/artifacts/source",
				nil,
			),
		),
		http.StatusNotFound,
	)
	t.Run("symlink", func(t *testing.T) {
		secret := filepath.Join(t.TempDir(), "private.txt")
		if err := os.WriteFile(secret, []byte("private-secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(
			secret,
			filepath.Join(f.store.PlanDir(entry.ID), "trace.json"),
		); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		w := f.do(
			httptest.NewRequest(
				http.MethodGet,
				"/api/v1/floorplans/"+entry.ID+"/artifacts/trace",
				nil,
			),
		)
		assertProblem(t, w, http.StatusNotFound)
		if strings.Contains(w.Body.String(), "private-secret") {
			t.Fatal("artifact request followed an escaping symlink")
		}
	})
}

func TestProductUnavailableProfilesAndCorruptArtifacts(t *testing.T) {
	f := productFixture(t)
	entry := productUpload(t, f, "office.png", 1)
	f.srv.Profiles = nil
	assertProblem(
		t,
		f.do(httptest.NewRequest(http.MethodGet, "/api/v1/layer-profiles/acme", nil)),
		http.StatusNotFound,
	)
	r := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/layer-profiles/acme",
		strings.NewReader(`{"rules":[]}`),
	)
	r.Header.Set(RequestHeader, "1")
	r.Header.Set("Content-Type", "application/json")
	assertProblem(t, f.do(r), http.StatusNotFound)
	f.srv.Assets = nil
	for _, path := range []string{"/artifacts/model", "/files/" + entry.SHA256} {
		assertProblem(
			t,
			f.do(httptest.NewRequest(http.MethodGet, "/api/v1/floorplans/"+entry.ID+path, nil)),
			http.StatusNotFound,
		)
	}
	f.srv.Assets = f.store
	for _, model := range []string{"not json", `{"WIDTH":10}`} {
		if err := os.WriteFile(
			filepath.Join(f.store.PlanDir(entry.ID), "plan.json"),
			[]byte(model),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		assertProblem(
			t,
			f.do(
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/floorplans/"+entry.ID+"/artifacts/model",
					nil,
				),
			),
			http.StatusInternalServerError,
		)
	}
	assertProblem(
		t,
		f.do(
			httptest.NewRequest(
				http.MethodGet,
				"/api/v1/floorplans/"+entry.ID+"/artifacts/drawing",
				nil,
			),
		),
		http.StatusNotFound,
	)
}
