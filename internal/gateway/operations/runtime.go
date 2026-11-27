// Package operations exposes installation diagnostics through the operator API.
package operations

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	"github.com/weaveplatform/spaceweave/internal/platform/httpkit"
	operator "github.com/weaveplatform/spaceweave/internal/transport/gen/operator"
)

// Plans is the read-only query authority required by installation diagnostics.
type Plans interface{ List() ([]api.Entry, error) }

// Server reads platform state without acquiring business mutation authority.
type Server struct {
	Plans                  Plans
	Version, Tracer, Token string
	Workers                int
}

var _ operator.StrictServerInterface = (*Server)(nil)

// Handler requires the installation's separate operator credential.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(httpkit.Bearer(s.Token, true, "spaceweave-operator"))
	strict := operator.NewStrictHandlerWithOptions(
		s,
		nil,
		operator.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  httpkit.RequestError,
			ResponseErrorHandlerFunc: httpkit.ResponseError,
		},
	)
	return operator.HandlerWithOptions(
		strict,
		operator.ChiServerOptions{
			BaseURL:          "/operator/v1",
			BaseRouter:       r,
			ErrorHandlerFunc: httpkit.RequestError,
		},
	)
}

// GetRuntime reports configured concurrency and actual catalogue state counts.
func (s *Server) GetRuntime(
	_ context.Context,
	_ operator.GetRuntimeRequestObject,
) (operator.GetRuntimeResponseObject, error) {
	entries, err := s.Plans.List()
	if err != nil {
		return nil, &httpkit.Error{
			Status: http.StatusServiceUnavailable,
			Slug:   "runtime-unavailable",
			Detail: "Processing state is temporarily unavailable.",
		}
	}
	var out operator.Runtime
	out.Version, out.Tracer, out.Workers = s.Version, s.Tracer, s.Workers
	for _, entry := range entries {
		switch entry.Status {
		case api.Queued:
			out.Queue.Queued++
		case api.Converting:
			out.Queue.Converting++
		case api.Tracing:
			out.Queue.Tracing++
		case api.Draft:
			out.Queue.Draft++
		case api.Approved:
			out.Queue.Approved++
		case api.Failed:
			out.Queue.Failed++
		}
	}
	return operator.GetRuntime200JSONResponse(out), nil
}
