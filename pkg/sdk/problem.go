package sdk

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Problem is an RFC 9457 response and supports errors.As through wrapped calls.
// Status always reflects the HTTP response, including non-Problem fallbacks.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

func (p *Problem) Error() string {
	if p.Detail != "" {
		return fmt.Sprintf("SpaceWeave HTTP %d: %s: %s", p.Status, p.Title, p.Detail)
	}
	return fmt.Sprintf("SpaceWeave HTTP %d: %s", p.Status, p.Title)
}

func checked(resp *http.Response, err error) (*http.Response, error) {
	if err != nil {
		return nil, fmt.Errorf("SpaceWeave request: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close() //nolint:errcheck // no unread body escapes a failed response
	var problem Problem
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&problem)
	problem.Status = resp.StatusCode
	if problem.Type == "" {
		problem.Type = "about:blank"
	}
	if problem.Title == "" {
		problem.Title = http.StatusText(resp.StatusCode)
	}
	return nil, &problem
}

func decoded[T any](resp *http.Response, err error) (*T, error) {
	resp, err = checked(resp, err)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // JSON response is consumed here
	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode SpaceWeave response: %w", err)
	}
	return &result, nil
}
