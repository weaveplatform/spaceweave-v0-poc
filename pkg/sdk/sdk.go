// Package sdk calls SpaceWeave's public product and operator APIs.
package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/weaveplatform/spaceweave/pkg/client/api"
	"github.com/weaveplatform/spaceweave/pkg/client/operator"
)

const (
	APIPrefix      = "/api/v1"
	OperatorPrefix = "/operator/v1"
	RequestHeader  = "X-Spaceweave"
)

var (
	ErrEndpoint = errors.New(
		"endpoint must be an HTTP or HTTPS origin without credentials, path, query or fragment",
	)
	ErrScope      = errors.New("request is outside the configured API origin or namespace")
	ErrInput      = errors.New("invalid SDK input")
	ErrPagination = errors.New("server repeated a pagination cursor")
)

// Client exposes generated wire clients and convenience methods. Product and
// operator credentials are separate even when both APIs share one listener.
type Client struct {
	API      *api.ClientWithResponses
	Operator *operator.ClientWithResponses
	base     *url.URL
	http     *http.Client
	token    string
	operator string
	retry    Retry
}

// Option configures a client before its HTTP clients are constructed.
type Option func(*Client)

// WithHTTPClient uses a copy of client, preserving its transport and timeout.
// Redirects remain confined to the selected API's origin and namespace.
func WithHTTPClient(client *http.Client) Option { return func(c *Client) { c.http = client } }

// WithToken sets the product API credential, never the operator credential.
func WithToken(token string) Option { return func(c *Client) { c.token = token } }

// WithOperatorToken sets the independent operator API credential.
func WithOperatorToken(token string) Option { return func(c *Client) { c.operator = token } }

// WithRetry configures bounded GET retries. Writes are never retried.
func WithRetry(retry Retry) Option { return func(c *Client) { c.retry = retry } }

// New constructs a client of an origin such as http://127.0.0.1:8000.
func New(baseURL string, options ...Option) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" ||
		base.User != nil || strings.Trim(base.Path, "/") != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, ErrEndpoint
	}
	base.Path = ""
	c := &Client{base: base, http: &http.Client{Timeout: 30 * time.Second}, retry: DefaultRetry}
	for _, option := range options {
		option(c)
	}
	if c.http == nil || c.retry.MaxAttempts < 1 || c.retry.InitialDelay < 0 ||
		c.retry.MaxDelay < c.retry.InitialDelay {
		return nil, fmt.Errorf("%w: HTTP client and retry settings", ErrInput)
	}
	c.API, err = api.NewClientWithResponses(base.String()+APIPrefix,
		api.WithHTTPClient(c.transport(APIPrefix, c.token)))
	if err != nil {
		return nil, fmt.Errorf("construct product client: %w", err)
	}
	c.Operator, err = operator.NewClientWithResponses(base.String()+OperatorPrefix,
		operator.WithHTTPClient(c.transport(OperatorPrefix, c.operator)))
	if err != nil {
		return nil, fmt.Errorf("construct operator client: %w", err)
	}
	return c, nil
}

func scoped(base, target *url.URL, prefix string) bool {
	return strings.EqualFold(base.Scheme, target.Scheme) &&
		strings.EqualFold(base.Host, target.Host) &&
		(target.Path == prefix || strings.HasPrefix(target.Path, prefix+"/"))
}

func (c *Client) transport(prefix, token string) transport {
	client := *c.http
	redirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !scoped(c.base, req.URL, prefix) ||
			(via[0].Method != http.MethodGet && via[0].Method != http.MethodHead) {
			return http.ErrUseLastResponse
		}
		if redirect != nil {
			if err := redirect(req, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		if !scoped(c.base, req.URL, prefix) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return transport{client: &client, base: c.base, prefix: prefix, token: token, retry: c.retry}
}

type transport struct {
	client *http.Client
	base   *url.URL
	prefix string
	token  string
	retry  Retry
}

func (t transport) Do(req *http.Request) (*http.Response, error) {
	if !scoped(t.base, req.URL, t.prefix) {
		return nil, ErrScope
	}
	req = req.Clone(req.Context())
	// A caller-supplied generated-client editor cannot accidentally use an
	// operator token on a product request, or vice versa.
	req.Header.Del("Authorization")
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		req.Header.Set(RequestHeader, "1")
	}
	return t.retry.do(req, t.client)
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
