package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/autotrace/profile"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/convert"
	transport "github.com/weaveplatform/spaceweave/internal/transport/gen/api"
)

const productPrefix = "/api/v1"

type productServer struct{ server *Server }

var _ transport.StrictServerInterface = productServer{}

// ProductHandler exposes the supported v1 contract through generated Chi routes.
// The existing Handler remains the local review-tool compatibility surface.
func (s *Server) ProductHandler() http.Handler {
	r := chi.NewRouter()
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		_ = productProblem(http.StatusNotFound, "Resource not found.").write(w)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		_ = productProblem(http.StatusMethodNotAllowed, "Method not allowed.").write(w)
	})
	strict := transport.NewStrictHandlerWithOptions(
		productServer{s},
		nil,
		transport.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
				_ = productProblem(
					http.StatusBadRequest,
					"The request does not match the API contract.",
				).write(w)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				s.log().Error("v1 response failed", "err", err)
				_ = productProblem(
					http.StatusInternalServerError,
					"The request could not be completed.",
				).write(w)
			},
		},
	)
	return transport.HandlerWithOptions(strict, transport.ChiServerOptions{
		BaseURL: productPrefix, BaseRouter: r,
		Middlewares: []transport.MiddlewareFunc{s.productGuard},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, _ error) {
			_ = productProblem(
				http.StatusBadRequest,
				"The request parameters are invalid.",
			).write(w)
		},
	})
}

type productResponse struct {
	status      int
	body        any
	contentType string
	reader      io.ReadCloser
	disposition string
}

func productJSON(status int, body any) productResponse {
	return productResponse{status: status, body: body, contentType: "application/json"}
}

func productProblem(status int, detail string) productResponse {
	slug := map[int]string{
		http.StatusBadRequest: "invalid-request", http.StatusUnauthorized: "unauthorized",
		http.StatusForbidden: "forbidden", http.StatusNotFound: "not-found",
		http.StatusConflict: "state-conflict", http.StatusRequestEntityTooLarge: "request-too-large",
		http.StatusUnsupportedMediaType: "unsupported-media-type", http.StatusUnprocessableEntity: "invalid-input",
		http.StatusInternalServerError: "internal-error", http.StatusServiceUnavailable: "temporarily-unavailable",
		http.StatusMethodNotAllowed: "method-not-allowed",
	}[status]
	return productResponse{
		status:      status,
		contentType: "application/problem+json",
		body: transport.Problem{
			Type:   "urn:spaceweave:problem:" + slug,
			Title:  http.StatusText(status),
			Status: status,
			Detail: &detail,
		},
	}
}

func (p productResponse) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", p.contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if p.disposition != "" {
		w.Header().Set("Content-Disposition", p.disposition)
	}
	if p.reader != nil {
		defer p.reader.Close() //nolint:errcheck // read-only artifact stream
		w.WriteHeader(p.status)
		_, err := io.Copy(w, p.reader)
		return err //nolint:wrapcheck // generated strict handler reports stream failures
	}
	data, err := json.Marshal(p.body)
	if err != nil {
		return fmt.Errorf("encode v1 response: %w", err)
	}
	w.WriteHeader(p.status)
	_, err = w.Write(append(data, '\n'))
	return err //nolint:wrapcheck // generated strict handler reports response failures
}

func (p productServer) failure(err error) productResponse {
	switch {
	case errors.Is(err, api.ErrNotFound), errors.Is(err, profile.ErrNotFound):
		return productProblem(http.StatusNotFound, "Resource not found.")
	case errors.Is(err, api.ErrRole), errors.Is(err, profile.ErrName):
		return productProblem(http.StatusBadRequest, "The request contains an invalid value.")
	case errors.Is(err, api.ErrFormat):
		return productProblem(http.StatusUnsupportedMediaType, "The file format is not supported.")
	case errors.Is(err, api.ErrPackage), errors.Is(err, profile.ErrInvalid):
		return productProblem(
			http.StatusUnprocessableEntity,
			"The supplied data cannot be processed.",
		)
	case errors.Is(err, api.ErrTransition), errors.Is(err, api.ErrPrimary):
		return productProblem(
			http.StatusConflict,
			"The operation is not allowed in the current state.",
		)
	case errors.Is(err, api.ErrLocked):
		return productProblem(
			http.StatusServiceUnavailable,
			"The resource is temporarily busy. Retry later.",
		)
	case errors.Is(err, profile.ErrBuiltin):
		return productProblem(http.StatusForbidden, "Built-in layer profiles cannot be changed.")
	default:
		p.server.log().Error("v1 request failed", "err", err)
		return productProblem(http.StatusInternalServerError, "The request could not be completed.")
	}
}

func productEntry(e api.Entry) api.Entry {
	base := productPrefix + "/floorplans/" + url.PathEscape(e.ID)
	if e.Source != "" {
		e.Source = base + "/artifacts/source"
	}
	if e.Upload != "" {
		e.Upload = base + "/artifacts/original"
	}
	if e.Drawing != "" {
		e.Drawing = base + "/artifacts/drawing"
	}
	e.Files = slices.Clone(e.Files)
	for i := range e.Files {
		e.Files[i].Upload = base + "/files/" + e.Files[i].SHA256
	}
	if e.Error != "" {
		e.Error = "Processing failed. Retry or contact an administrator."
	}
	return e
}

func (p productServer) ListFloorplans(
	_ context.Context,
	request transport.ListFloorplansRequestObject,
) (transport.ListFloorplansResponseObject, error) {
	limit := 50
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	if limit < 1 || limit > 200 {
		return productProblem(http.StatusBadRequest, "limit must be between 1 and 200."), nil
	}
	lastID := ""
	if request.Params.Cursor != nil {
		raw := *request.Params.Cursor
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(raw) > 1024 || !validProductID(string(decoded)) {
			return productProblem(http.StatusBadRequest, "The pagination cursor is invalid."), nil
		}
		lastID = string(decoded)
	}
	entries, err := p.server.Store.List()
	if err != nil {
		return p.failure(err), nil
	}
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b api.Entry) int { return strings.Compare(a.ID, b.ID) })
	plans := make([]api.Entry, 0, limit)
	next := ""
	for _, e := range entries {
		if e.ID <= lastID {
			continue
		}
		if len(plans) == limit {
			next = base64.RawURLEncoding.EncodeToString([]byte(plans[len(plans)-1].ID))
			break
		}
		plans = append(plans, productEntry(e))
	}
	return productJSON(http.StatusOK, struct {
		Plans      []api.Entry `json:"plans"`
		NextCursor string      `json:"nextCursor,omitempty"`
	}{plans, next}), nil
}

func (p productServer) GetFloorplan(
	_ context.Context,
	request transport.GetFloorplanRequestObject,
) (transport.GetFloorplanResponseObject, error) {
	e, err := p.server.Store.Get(request.Id)
	if err != nil {
		return p.failure(err), nil
	}
	return productJSON(http.StatusOK, productEntry(e)), nil
}

func (p productServer) GetFloorplanProcessing(
	_ context.Context,
	request transport.GetFloorplanProcessingRequestObject,
) (transport.GetFloorplanProcessingResponseObject, error) {
	e, err := p.server.Store.Get(request.Id)
	if err != nil {
		return p.failure(err), nil
	}
	e = productEntry(e)
	return productJSON(http.StatusOK, struct {
		PlanID    string        `json:"planId"`
		Status    api.Status    `json:"status"`
		Attempt   int           `json:"attempt"`
		Progress  *api.Progress `json:"progress,omitempty"`
		Error     string        `json:"error,omitempty"`
		UpdatedAt time.Time     `json:"updatedAt"`
	}{e.ID, e.Status, e.Attempts, e.Progress, e.Error, e.UpdatedAt}), nil
}

func (p productServer) review(
	id string,
	action api.Action,
	body *transport.ReviewRequest,
) productResponse {
	note := ""
	if body != nil && body.Note != nil {
		note = strings.TrimSpace(*body.Note)
	}
	was, e, err := p.server.Store.Review(id, action, note)
	if err != nil {
		return p.failure(err)
	}
	p.server.requeued(e)
	return productJSON(http.StatusOK, reviewReply{Plan: productEntry(e), Was: was})
}

func (p productServer) ApproveFloorplan(
	_ context.Context,
	request transport.ApproveFloorplanRequestObject,
) (transport.ApproveFloorplanResponseObject, error) {
	return p.review(request.Id, api.Approve, request.Body), nil
}

func (p productServer) RejectFloorplan(
	_ context.Context,
	request transport.RejectFloorplanRequestObject,
) (transport.RejectFloorplanResponseObject, error) {
	return p.review(request.Id, api.Reject, request.Body), nil
}

func (p productServer) RetraceFloorplan(
	_ context.Context,
	request transport.RetraceFloorplanRequestObject,
) (transport.RetraceFloorplanResponseObject, error) {
	return p.review(request.Id, api.Retrace, request.Body), nil
}

func (p productServer) RetryFloorplan(
	_ context.Context,
	request transport.RetryFloorplanRequestObject,
) (transport.RetryFloorplanResponseObject, error) {
	return p.review(request.Id, api.Retry, request.Body), nil
}

func (p productServer) GetInfo(
	_ context.Context,
	_ transport.GetInfoRequestObject,
) (transport.GetInfoResponseObject, error) {
	i := p.server.Info
	i.MaxUploadBytes, i.Formats = p.server.maxUpload(), convert.Formats
	if p.server.Profiles != nil {
		name, err := p.server.Profiles.Active()
		if err != nil {
			return p.failure(err), nil
		}
		i.Profile, i.Profiles = name, true
	}
	return productJSON(http.StatusOK, i), nil
}

func (p productServer) profile(name string, value *profile.Profile) productResponse {
	active, err := p.server.Profiles.Active()
	if err != nil {
		return p.failure(err)
	}
	response := profileReply{
		Name: name, Builtin: slices.Contains(profile.BuiltinNames, name), Exists: value != nil,
		Active: name == active, Profile: value, Roles: profile.Roles,
	}
	if response.Profile == nil {
		response.Profile = &profile.Profile{Rules: []profile.Rule{}}
	}
	return productJSON(http.StatusOK, response)
}

func (p productServer) GetLayerProfile(
	_ context.Context,
	request transport.GetLayerProfileRequestObject,
) (transport.GetLayerProfileResponseObject, error) {
	if p.server.Profiles == nil {
		return productProblem(http.StatusNotFound, "Layer profiles are not available."), nil
	}
	value, err := p.server.Profiles.Get(request.Name)
	if errors.Is(err, profile.ErrNotFound) {
		return p.profile(request.Name, nil), nil
	}
	if err != nil {
		return p.failure(err), nil
	}
	return p.profile(request.Name, value), nil
}

func (p productServer) PutLayerProfile(
	_ context.Context,
	request transport.PutLayerProfileRequestObject,
) (transport.PutLayerProfileResponseObject, error) {
	if p.server.Profiles == nil {
		return productProblem(http.StatusNotFound, "Layer profiles are not available."), nil
	}
	if err := profile.CheckName(request.Name); err != nil {
		return p.failure(err), nil
	}
	data, err := json.Marshal(request.Body)
	if err != nil {
		return p.failure(err), nil
	}
	value, err := profile.Parse(request.Name, data)
	if err != nil {
		return p.failure(err), nil
	}
	if err := p.server.Profiles.Put(request.Name, value); err != nil {
		return p.failure(err), nil
	}
	return p.profile(request.Name, value), nil
}
