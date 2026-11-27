package portal

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestStaticFilesHostsAndAPIMount(t *testing.T) {
	site, generated := t.TempDir(), t.TempDir()
	for path, body := range map[string]string{
		filepath.Join(site, "index.html"):        "<title>site</title>",
		filepath.Join(generated, "catalog.json"): `{"version":1}`,
		filepath.Join(generated, "catalog.lock"): "",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	api := chi.NewRouter()
	api.Get("/api/info", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tracer":"auto"}`))
	})
	h := Handler(site, generated, []string{"studio.local"}, api)
	cases := []struct {
		path, host string
		code       int
		body       string
	}{
		{"/", "localhost:8000", 200, "<title>site</title>"},
		{"/index.html", "127.0.0.1:8000", 301, ""},
		{"/generated/catalog.json", "[::1]:8000", 200, `{"version":1}`},
		{"/generated/catalog.lock", "localhost", 200, ""},
		{"/generated/.catalog.json.123", "app.localhost:8000", 404, ""},
		{"/.git/config", "localhost", 404, ""},
		{"/", "studio.local:8000", 200, "site"},
		{"/", "evil.example:8000", 421, "--allow-host"},
		{"/", "169.254.1.1", 421, ""},
		{"/api/info", "localhost:8000", 200, `{"tracer":"auto"}`},
		{"/api/info", "evil.example:8000", 421, "--allow-host"},
	}
	for _, c := range cases {
		t.Run(c.path+"@"+c.host, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, c.path, nil)
			r.Host = c.host
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.code || !strings.Contains(w.Body.String(), c.body) {
				t.Fatalf("GET %s (Host %s): %d %q", c.path, c.host, w.Code, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" && w.Code != 421 {
				t.Fatal("caching not disabled")
			}
			if w.Code == 421 && w.Header().Get("Content-Type") != "application/json" {
				t.Fatal("host refusal lost its JSON response")
			}
		})
	}
}
