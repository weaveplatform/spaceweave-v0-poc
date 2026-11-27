package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/internal/platform/httpkit"
)

func testRoutes(path string) http.Handler {
	r := chi.NewRouter()
	r.Get(path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return r
}

func TestRouterSeparatesAuthoritiesAndProtectsLegacyAssets(t *testing.T) {
	site := t.TempDir()
	generated := filepath.Join(site, "generated")
	if err := os.Mkdir(generated, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(site, "index.html"),
		[]byte("public site"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(generated, "catalog.json"),
		[]byte(`{"plans":[]}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	operator := httpkit.Bearer(
		"operator-secret",
		true,
		"operator",
	)(
		testRoutes("/operator/v1/runtime"),
	)
	router := Router(
		site,
		generated,
		nil,
		testRoutes("/api/info"),
		Surfaces{
			Product:      testRoutes("/api/v1/info"),
			Operator:     operator,
			ProductToken: "product-secret",
		},
	)
	for _, tc := range []struct {
		name, path, credential string
		want                   int
	}{
		{"product requires credentials", "/api/v1/info", "", 401},
		{"product", "/api/v1/info", "product-secret", 200},
		{"operator cannot call product", "/api/v1/info", "operator-secret", 401},
		{"product cannot operate", "/operator/v1/runtime", "product-secret", 401},
		{"operator", "/operator/v1/runtime", "operator-secret", 200},
		{"legacy cannot bypass product auth", "/api/info", "", 401},
		{"legacy authenticated", "/api/info", "product-secret", 200},
		{"generated cannot bypass product auth", "/generated/catalog.json", "", 401},
		{"generated authenticated", "/generated/catalog.json", "product-secret", 200},
		{"portal fallback protected", "/", "", 401},
		{"encoded directory cannot bypass auth", "/%67enerated/catalog.json", "", 401},
		{"encoded slash cannot bypass auth", "/generated%2Fcatalog.json", "", 401},
		{"case alias cannot bypass auth", "/GENERATED/catalog.json", "", 401},
		{"duplicate slash cannot bypass auth", "//generated/catalog.json", "", 401},
		{"portal authenticated", "/", "product-secret", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Host = "localhost"
			if tc.credential != "" {
				req.Header.Set("Authorization", "Bearer "+tc.credential)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	local := Router(site, generated, nil, testRoutes("/api/info"))
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.Host = "localhost"
	w := httptest.NewRecorder()
	local.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
