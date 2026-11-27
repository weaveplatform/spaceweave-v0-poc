package httpkit

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Bearer isolates surface credentials. An empty optional credential enables
// the local workbench but does not accept credentials from another surface.
func Bearer(token string, required bool, realm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" && required {
				WriteProblem(
					w,
					http.StatusServiceUnavailable,
					"operator-disabled",
					"Operator access is not configured.",
				)
				return
			}
			auth := r.Header.Get("Authorization")
			if token == "" && auth == "" {
				next.ServeHTTP(w, r)
				return
			}
			fields := strings.Fields(auth)
			if token != "" && len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") &&
				subtle.ConstantTimeCompare([]byte(fields[1]), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`"`)
			WriteProblem(
				w,
				http.StatusUnauthorized,
				"unauthorized",
				"A valid credential for this API surface is required.",
			)
		})
	}
}
