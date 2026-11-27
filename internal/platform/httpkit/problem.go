// Package httpkit contains REST transport conventions shared by API surfaces.
package httpkit

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Problem is the stable error representation returned by REST APIs.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Error is a deliberate public failure; arbitrary internal errors are redacted.
type Error struct {
	Status       int
	Slug, Detail string
}

func (e *Error) Error() string { return e.Detail }

// WriteProblem returns a machine-readable problem without internal error text.
func WriteProblem(w http.ResponseWriter, status int, slug, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).
		Encode(Problem{Type: "urn:spaceweave:problem:" + slug, Title: http.StatusText(status), Status: status, Detail: detail})
}

// ResponseError maps explicit public errors and redacts unexpected failures.
func ResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	var public *Error
	if errors.As(err, &public) {
		WriteProblem(w, public.Status, public.Slug, public.Detail)
		return
	}
	WriteProblem(
		w,
		http.StatusInternalServerError,
		"internal-error",
		"The request could not be completed.",
	)
}

// RequestError handles request binding/validation failures.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		WriteProblem(
			w,
			http.StatusRequestEntityTooLarge,
			"request-too-large",
			"The request exceeds the upload limit.",
		)
		return
	}
	WriteProblem(
		w,
		http.StatusBadRequest,
		"invalid-request",
		"The request does not match the API contract.",
	)
}
