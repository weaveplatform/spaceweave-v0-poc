// Package portal hosts the floor-plan microsite and mounts module HTTP handlers.
package portal

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Handler serves the portal and generated assets under the same local host
// policy as its API. The generated directory may live outside the site folder.
func Handler(site, generated string, hosts []string, api http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler { return allowHost(hosts, next) }, noStore)
	r.Handle("/api", api)
	r.Handle("/api/*", api)
	r.Handle("/generated/*", http.StripPrefix("/generated", files(generated)))
	r.Handle("/*", files(site))
	return r
}

// files serves a folder, hiding dotfiles: the catalogue's lock and the
// temporary files it renames into place.
func files(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for seg := range strings.SplitSeq(r.URL.Path, "/") {
			if strings.HasPrefix(seg, ".") {
				http.NotFound(w, r)
				return
			}
		}
		fs.ServeHTTP(w, r)
	})
}

// noStore stops the browser caching modules and the catalogue, which change
// while the page is open (tools/serve.py does the same).
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func allowHost(hosts []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		ip := net.ParseIP(host)
		ok := host == "localhost" || strings.HasSuffix(host, ".localhost") ||
			(ip != nil && ip.IsLoopback()) || slices.Contains(hosts, host)
		if !ok {
			writeError(w, http.StatusMisdirectedRequest,
				fmt.Sprintf("this server doesn't answer to %q; add it with --allow-host", host))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
