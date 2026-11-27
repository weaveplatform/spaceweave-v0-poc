package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/autotrace/profile"
	transport "github.com/weaveplatform/spaceweave/internal/transport/gen/api"
)

var productSchema = sync.OnceValues(transport.GetSpec)

func validProductID(id string) bool {
	return id != "" && len(id) <= 255 && id != "." && id != ".." &&
		!strings.ContainsAny(id, "/\\\x00\r\n")
}

func (s *Server) productGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if problem := s.validateProductRequest(w, r); problem.status != 0 {
			_ = problem.write(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) validateProductRequest(w http.ResponseWriter, r *http.Request) productResponse {
	if id := chi.URLParam(r, "id"); id != "" && !validProductID(id) {
		return productProblem(http.StatusBadRequest, "The floorplan identifier is invalid.")
	}
	if sha := chi.URLParam(r, "sha"); sha != "" {
		if _, err := hex.DecodeString(
			sha,
		); err != nil || len(sha) != 64 ||
			strings.ToLower(sha) != sha {
			return productProblem(http.StatusBadRequest, "The attachment identifier is invalid.")
		}
	}
	if name := chi.URLParam(r, "name"); name != "" {
		if err := profile.CheckName(name); err != nil && !errors.Is(err, profile.ErrBuiltin) {
			return productProblem(http.StatusBadRequest, "The layer profile name is invalid.")
		}
	}
	for _, values := range r.URL.Query() {
		if len(values) > 1 {
			return productProblem(
				http.StatusBadRequest,
				"Repeated query parameters are not supported.",
			)
		}
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return productResponse{}
	}
	if r.Header.Get(RequestHeader) != "1" {
		return productProblem(
			http.StatusForbidden,
			"The request must include the X-Spaceweave header.",
		)
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host {
			return productProblem(http.StatusForbidden, "Cross-origin writes are not permitted.")
		}
	}
	if r.Method == http.MethodDelete {
		return productResponse{}
	}
	if r.Method == http.MethodPost &&
		(r.URL.Path == productPrefix+"/floorplans" || strings.HasSuffix(r.URL.Path, "/files")) {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload())
		return productResponse{}
	}
	return validateProductJSON(w, r)
}

func validateProductJSON(w http.ResponseWriter, r *http.Request) productResponse {
	profileBody := r.Method == http.MethodPut
	limit := int64(4*maxNote + 1024)
	if profileBody {
		limit = maxProfile
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	defer body.Close() //nolint:errcheck // the bounded request has been consumed
	data, err := io.ReadAll(body)
	if err != nil {
		return productReadFailure(err)
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 && !profileBody {
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return productResponse{}
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return productProblem(
			http.StatusUnsupportedMediaType,
			"Send this request as application/json.",
		)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil || shape == nil || containsJSONNull(data) {
		return productProblem(
			http.StatusBadRequest,
			"The body must be one JSON object with non-null fields.",
		)
	}
	var target any = &transport.ReviewRequest{}
	if profileBody {
		target = &transport.LayerProfile{}
		if _, ok := shape["rules"]; !ok || !validProfileRules(shape["rules"]) {
			return productProblem(
				http.StatusBadRequest,
				"A layer profile requires rules, each with a role.",
			)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return productProblem(
			http.StatusBadRequest,
			"The JSON fields do not match the API contract.",
		)
	}
	if review, ok := target.(*transport.ReviewRequest); ok && review.Note != nil &&
		utf8.RuneCountInString(*review.Note) > maxNote {
		return productProblem(http.StatusBadRequest, "The review note is too long.")
	}
	if problem := validateProductSchema(data, profileBody); problem.status != 0 {
		return problem
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return productResponse{}
}

// Struct decoding accepts case-insensitive field names, while the published
// schemas require exact property names. Validate before generated binding so
// variants such as NOTE and Layer cannot bypass additionalProperties: false.
func validateProductSchema(data []byte, profileBody bool) productResponse {
	spec, err := productSchema()
	if err != nil {
		return productProblem(http.StatusInternalServerError, "Request validation is unavailable.")
	}
	name := "ReviewRequest"
	if profileBody {
		name = "LayerProfile"
	}
	schema := spec.Components.Schemas[name]
	if schema == nil || schema.Value == nil {
		return productProblem(http.StatusInternalServerError, "Request validation is unavailable.")
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return productProblem(http.StatusBadRequest, "The body must be one JSON object.")
	}
	if err := schema.Value.VisitJSON(value); err != nil {
		return productProblem(
			http.StatusBadRequest,
			"The JSON fields do not match the API contract.",
		)
	}
	return productResponse{}
}

func containsJSONNull(data []byte) bool {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return true
	}
	var hasNull func(any) bool
	hasNull = func(value any) bool {
		switch item := value.(type) {
		case nil:
			return true
		case map[string]any:
			for _, nested := range item {
				if hasNull(nested) {
					return true
				}
			}
		case []any:
			for _, nested := range item {
				if hasNull(nested) {
					return true
				}
			}
		}
		return false
	}
	return hasNull(value)
}

func validProfileRules(data json.RawMessage) bool {
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rules); err != nil || rules == nil {
		return false
	}
	for _, rule := range rules {
		if _, ok := rule["role"]; !ok {
			return false
		}
	}
	return true
}

func productReadFailure(err error) productResponse {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return productProblem(
			http.StatusRequestEntityTooLarge,
			"The request exceeds the allowed size.",
		)
	}
	return productProblem(http.StatusBadRequest, "The request body could not be read.")
}
