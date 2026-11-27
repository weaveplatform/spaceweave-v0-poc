package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/internal/gateway/portal"
	"github.com/weaveplatform/spaceweave/internal/platform/httpkit"
)

// Surfaces mounts independently versioned product and installation contracts.
// ProductToken is optional for the local workbench; operator auth is mandatory
// and implemented by its own handler.
type Surfaces struct {
	Product, Operator http.Handler
	ProductToken      string
}

// Router assembles module routes, local compatibility routes and portal assets.
func Router(
	site, generated string,
	hosts []string,
	legacy http.Handler,
	surfaces ...Surfaces,
) http.Handler {
	r := chi.NewRouter()
	var config Surfaces
	if len(surfaces) > 0 {
		config = surfaces[0]
	}
	productGuard := httpkit.Bearer(config.ProductToken, false, "spaceweave-product")
	r.Handle("/api", productGuard(legacy))
	r.Handle("/api/*", productGuard(legacy))
	if config.Product != nil {
		r.Handle("/api/v1", productGuard(config.Product))
		r.Handle("/api/v1/*", productGuard(config.Product))
	}
	if config.Operator != nil {
		r.Handle("/operator/v1", config.Operator)
		r.Handle("/operator/v1/*", config.Operator)
	}
	return portal.Handler(
		site,
		generated,
		hosts,
		r,
		portal.Options{FileMiddleware: productGuard},
	)
}
