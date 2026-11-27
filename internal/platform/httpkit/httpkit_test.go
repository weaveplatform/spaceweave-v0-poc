package httpkit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestSurfaceCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, configured, authorization string
		required                        bool
		want                            int
	}{
		{"local anonymous", "", "", false, 204},
		{"local rejects another surface", "", "Bearer operator-secret", false, 401},
		{"operator disabled", "", "", true, 503},
		{"missing credential", "product-secret", "", false, 401},
		{"wrong credential", "product-secret", "Bearer operator-secret", false, 401},
		{"wrong scheme", "product-secret", "Basic product-secret", false, 401},
		{"extra value", "product-secret", "Bearer product-secret extra", false, 401},
		{"product credential", "product-secret", "Bearer product-secret", false, 204},
		{"operator credential", "operator-secret", "bearer operator-secret", true, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := chi.NewRouter()
			router.Use(Bearer(tc.configured, tc.required, "test"))
			router.Get("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tc.want >= 400 {
				var p Problem
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				if p.Status != tc.want || !strings.HasPrefix(p.Type, "urn:spaceweave:problem:") {
					t.Fatalf("bad problem: %+v", p)
				}
				if strings.Contains(w.Body.String(), "-secret") {
					t.Fatal("credential leaked")
				}
			}
			if tc.want == 401 && w.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing authentication challenge")
			}
		})
	}
}

func TestProblemHandlers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request, error)
		err     error
		want    int
		detail  string
	}{
		{"explicit", ResponseError, &Error{Status: 409, Slug: "state-conflict", Detail: "Changed state"}, 409, "Changed state"},
		{"internal", ResponseError, errors.New("/private/secret token"), 500, "The request could not be completed."},
		{"invalid", RequestError, errors.New("bad binding"), 400, "The request does not match the API contract."},
		{"large", RequestError, &http.MaxBytesError{Limit: 4}, 413, "The request exceeds the upload limit."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, httptest.NewRequest(http.MethodGet, "/", nil), tc.err)
			var p Problem
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.want || p.Detail != tc.detail ||
				w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("%d %+v", w.Code, p)
			}
		})
	}
	if got := (&Error{Detail: "public error"}).Error(); got != "public error" {
		t.Fatal(got)
	}
}
