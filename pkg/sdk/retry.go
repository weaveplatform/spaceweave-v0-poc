package sdk

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Retry bounds safe GET attempts and their backoff. A Retry-After beyond
// MaxDelay returns the server's response instead of retrying too early.
type Retry struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// DefaultRetry permits at most three GET attempts; mutations get one attempt.
var DefaultRetry = Retry{
	MaxAttempts:  3,
	InitialDelay: 100 * time.Millisecond,
	MaxDelay:     2 * time.Second,
}

func (r Retry) do(req *http.Request, client *http.Client) (*http.Response, error) {
	delay := r.InitialDelay
	for attempt := 1; ; attempt++ {
		resp, err := client.Do(req)
		if req.Method != http.MethodGet || attempt >= r.MaxAttempts || req.Context().Err() != nil {
			return resp, err //nolint:wrapcheck // preserve the HTTP response and context error
		}
		if err == nil && resp.StatusCode != http.StatusTooManyRequests &&
			resp.StatusCode != http.StatusBadGateway &&
			resp.StatusCode != http.StatusServiceUnavailable &&
			resp.StatusCode != http.StatusGatewayTimeout {
			return resp, nil
		}
		pause := delay
		if resp != nil {
			if value := resp.Header.Get("Retry-After"); value != "" {
				pause = retryAfter(value, delay)
			}
		}
		if pause > r.MaxDelay {
			return resp, err //nolint:wrapcheck // caller handles the final server response
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err := wait(req.Context(), pause); err != nil {
			return nil, fmt.Errorf("retry interrupted: %w", err)
		}
		if delay < r.MaxDelay/2 {
			delay *= 2
		} else {
			delay = r.MaxDelay
		}
	}
}

func retryAfter(value string, fallback time.Duration) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		const maxSeconds = int64((1<<63 - 1) / time.Second)
		if seconds > maxSeconds {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(until))
	}
	return fallback
}
