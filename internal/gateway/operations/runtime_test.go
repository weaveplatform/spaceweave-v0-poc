package operations

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/weaveplatform/spaceweave/api"
	domain "github.com/weaveplatform/spaceweave/internal/floorplans/api"
)

type reader struct {
	entries []domain.Entry
	err     error
}

func (r reader) List() ([]domain.Entry, error) { return r.entries, r.err }

func TestRuntimeSurface(t *testing.T) {
	statuses := []domain.Status{
		domain.Queued,
		domain.Converting,
		domain.Tracing,
		domain.Draft,
		domain.Approved,
		domain.Failed,
	}
	var entries []domain.Entry
	for _, status := range statuses {
		entries = append(entries, domain.Entry{Status: status})
	}
	s := &Server{
		Plans:   reader{entries: entries},
		Version: "test",
		Tracer:  "auto",
		Workers: 2,
		Token:   "operator-secret",
	}
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"missing", "", 401}, {"product cannot operate", "product-secret", 401}, {"operator", "operator-secret", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/operator/v1/runtime", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if w.Code == 200 {
				var value map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				if value["version"] != "test" || value["workers"] != float64(2) {
					t.Fatal(value)
				}
				queue := value["queue"].(map[string]any)
				for _, status := range statuses {
					if queue[string(status)] != float64(1) {
						t.Fatal(queue)
					}
				}
				doc, err := openapi3.NewLoader().LoadFromData(api.Operator)
				if err != nil {
					t.Fatal(err)
				}
				if err := doc.Components.Schemas["Runtime"].Value.VisitJSON(value); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	s.Plans = reader{err: errors.New("/private/state: credential")}
	req := httptest.NewRequest(http.MethodGet, "/operator/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer operator-secret")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	s.Token = ""
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
