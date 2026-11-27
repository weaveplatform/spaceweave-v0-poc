package app

import (
	"net/http"

	"github.com/weaveplatform/spaceweave/internal/gateway/portal"
)

// Router assembles module HTTP routes with the portal's host policy and assets.
func Router(site, generated string, hosts []string, api http.Handler) http.Handler {
	return portal.Handler(site, generated, hosts, api)
}
