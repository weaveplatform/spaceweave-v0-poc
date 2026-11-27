package acceptance

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/dxf/dxftest"
)

const (
	featureProductToken  = "product-test-credential"
	featureOperatorToken = "operator-test-credential"
)

//go:embed features/*/*.feature
var acceptanceFeatures embed.FS

type featureWorld struct {
	t         *testing.T
	server    *serving
	status    int
	header    http.Header
	body      []byte
	id        string
	source    string
	duplicate bool
	cliCode   int
	cliError  string
}

func TestAcceptanceFeatures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stopping the server needs an interrupt signal")
	}
	// The existing real-process harness owns temporary sites and coverage.
	// Each scenario stops its server before the next scenario starts.
	var world *featureWorld
	suite := godog.TestSuite{
		Name: "spaceweave",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				world = &featureWorld{t: t}
				return ctx, nil
			})
			sc.After(
				func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
					if world.server != nil {
						world.server.stop()
					}
					return ctx, nil
				},
			)
			sc.Step(
				`^a running SpaceWeave server with separate product and operator credentials$`,
				func() { world.start(true) },
			)
			sc.Step(`^a running SpaceWeave server in local mode$`, func() { world.start(false) })
			sc.Step(
				`^an? "([^"]+)" client sends "([^"]+)" to "([^"]+)"$`,
				func(credential, method, path string) error {
					return world.request(credential, method, path, "", false)
				},
			)
			sc.Step(
				`^an anonymous client sends "([^"]+)" to "([^"]+)"$`,
				func(method, path string) error {
					return world.request("anonymous", method, path, "", false)
				},
			)
			sc.Step(
				`^a client sends guarded JSON '([^']*)' to "([^"]+)"$`,
				func(body, path string) error {
					return world.request("anonymous", http.MethodPost, path, body, true)
				},
			)
			sc.Step(
				`^the response is a (\d+) problem "([^"]+)"$`,
				func(status int, slug string) error { return world.problem(status, slug) },
			)
			sc.Step(
				`^the response has HTTP status (\d+)$`,
				func(status int) error { return world.hasStatus(status) },
			)
			sc.Step(`^the response challenges for a bearer credential$`, func() error {
				if !strings.HasPrefix(world.header.Get("WWW-Authenticate"), "Bearer ") {
					return fmt.Errorf("missing bearer challenge: %v", world.header)
				}
				return nil
			})
			sc.Step(
				`^runtime diagnostics identify the tracer and worker concurrency$`,
				func() error { return world.runtime() },
			)
			sc.Step(
				`^the remote CLI uploads an office drawing$`,
				func() error { return world.upload(false) },
			)
			sc.Step(
				`^the remote CLI uploads the same drawing again$`,
				func() error { return world.upload(true) },
			)
			sc.Step(`^the upload identifies the existing floorplan as a duplicate$`, func() error {
				if !world.duplicate {
					return fmt.Errorf("upload did not identify a duplicate")
				}
				return nil
			})
			sc.Step(
				`^the drawing reaches draft processing state$`,
				func() error { return world.waitForDraft() },
			)
			sc.Step(
				`^the remote CLI lists the imported floorplan$`,
				func() error { return world.catalogue(1) },
			)
			sc.Step(
				`^the product catalogue contains exactly (\d+) floorplan$`,
				func(count int) error { return world.catalogue(count) },
			)
			sc.Step(
				`^the remote CLI downloads its model through the product API$`,
				func() error { return world.model() },
			)
			sc.Step(
				`^the remote CLI approves the floorplan$`,
				func() error { _, err := world.cli("floorplans", "approve", world.id); return err },
			)
			sc.Step(
				`^the remote CLI reads an approved floorplan$`,
				func() error { return world.approved() },
			)
			sc.Step(
				`^the operator runtime reports (\d+) approved floorplan$`,
				func(count int) error { return world.approvedCount(count) },
			)
			sc.Step(`^server logs contain no credentials$`, func() error {
				if strings.Contains(world.server.log.String(), "test-credential") {
					return fmt.Errorf("credential leaked in server logs")
				}
				return nil
			})
			sc.Step(`^the remote CLI uses a product credential for operator runtime$`, func() {
				_, world.cliError, world.cliCode = control(
					t,
					world.server.base,
					featureProductToken,
					featureProductToken,
					"operator",
					"runtime",
				)
			})
			sc.Step(`^the CLI exits with a runtime failure and a 401 problem$`, func() error {
				if world.cliCode != 1 || !strings.Contains(world.cliError, "401") {
					return fmt.Errorf("CLI exit %d: %s", world.cliCode, world.cliError)
				}
				return nil
			})
		},
		Options: &godog.Options{
			Format: "pretty", Paths: []string{"features"}, Strict: true,
			TestingT: t, Concurrency: 1, FS: acceptanceFeatures,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance features failed")
	}
}

func (w *featureWorld) start(protected bool) {
	s := newAutoSite(w.t)
	if protected {
		s.env = append(
			s.env,
			"SPACEWEAVE_API_TOKEN="+featureProductToken,
			"SPACEWEAVE_OPERATOR_TOKEN="+featureOperatorToken,
		)
	}
	w.server = serve(w.t, s)
}

func (w *featureWorld) request(credential, method, path, body string, guarded bool) error {
	req, err := http.NewRequestWithContext(
		w.t.Context(),
		method,
		w.server.base+path,
		strings.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	switch credential {
	case "product":
		req.Header.Set("Authorization", "Bearer "+featureProductToken)
	case "operator":
		req.Header.Set("Authorization", "Bearer "+featureOperatorToken)
	case "anonymous":
	default:
		return fmt.Errorf("unknown credential %q", credential)
	}
	if guarded {
		req.Header.Set("X-Spaceweave", "1")
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only response
	w.status, w.header = res.StatusCode, res.Header.Clone()
	w.body, err = io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	return nil
}

func (w *featureWorld) hasStatus(want int) error {
	if w.status != want {
		return fmt.Errorf("HTTP %d, want %d: %s", w.status, want, w.body)
	}
	return nil
}

func (w *featureWorld) problem(status int, slug string) error {
	if err := w.hasStatus(status); err != nil {
		return err
	}
	var p struct {
		Type, Title string
		Status      int
	}
	if err := json.Unmarshal(w.body, &p); err != nil {
		return fmt.Errorf("decode problem: %w", err)
	}
	if w.header.Get("Content-Type") != "application/problem+json" ||
		p.Type != "urn:spaceweave:problem:"+slug ||
		p.Status != status ||
		p.Title == "" {
		return fmt.Errorf("invalid problem response: %v %s", w.header, w.body)
	}
	return nil
}

func (w *featureWorld) runtime() error {
	var r struct {
		Tracer  string
		Workers int
		Queue   map[string]int
	}
	if err := json.Unmarshal(w.body, &r); err != nil {
		return fmt.Errorf("decode runtime: %w", err)
	}
	if r.Tracer != "auto" || r.Workers < 1 || len(r.Queue) != 6 {
		return fmt.Errorf("invalid runtime: %s", w.body)
	}
	return nil
}

func (w *featureWorld) cli(args ...string) ([]byte, error) {
	out, stderr, code := control(
		w.t,
		w.server.base,
		featureProductToken,
		featureOperatorToken,
		args...)
	if code != 0 {
		return nil, fmt.Errorf("CLI %v exit %d: %s %s", args, code, out, stderr)
	}
	return out, nil
}

func (w *featureWorld) upload(again bool) error {
	if !again {
		w.source = filepath.Join(w.t.TempDir(), "office.dxf")
		if err := os.WriteFile(w.source, dxftest.Office(4).Bytes(), 0o600); err != nil {
			return fmt.Errorf("write drawing: %w", err)
		}
	}
	out, err := w.cli("floorplans", "upload", w.source)
	if err != nil {
		return err
	}
	var result struct {
		Plan      struct{ ID string }
		Duplicate bool
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("decode upload: %w", err)
	}
	if result.Plan.ID == "" || (again && result.Plan.ID != w.id) {
		return fmt.Errorf("unexpected uploaded plan: %s", out)
	}
	w.id, w.duplicate = result.Plan.ID, result.Duplicate
	return nil
}

func (w *featureWorld) waitForDraft() error {
	for deadline := time.Now().Add(time.Minute); ; {
		out, err := w.cli("floorplans", "processing", w.id)
		if err != nil {
			return err
		}
		var result struct{ Status string }
		if err := json.Unmarshal(out, &result); err != nil {
			return fmt.Errorf("decode processing: %w", err)
		}
		if result.Status == "draft" {
			return nil
		}
		if result.Status == "failed" || time.Now().After(deadline) {
			return fmt.Errorf("processing did not reach draft: %s\n%s", out, w.server.log)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (w *featureWorld) catalogue(count int) error {
	out, err := w.cli("floorplans", "list", "--all")
	if err != nil {
		return err
	}
	var plans []struct{ ID string }
	if err := json.Unmarshal(out, &plans); err != nil {
		return fmt.Errorf("decode catalogue: %w", err)
	}
	if len(plans) != count || (count == 1 && plans[0].ID != w.id) {
		return fmt.Errorf("unexpected catalogue: %s", out)
	}
	return nil
}

func (w *featureWorld) model() error {
	path := filepath.Join(w.t.TempDir(), "model.json")
	if _, err := w.cli("floorplans", "artifact", w.id, "model", "--output", path); err != nil {
		return err
	}
	model, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read downloaded model: %w", err)
	}
	if !json.Valid(model) || bytes.Contains(model, []byte("generated/")) ||
		!bytes.Contains(model, []byte("/api/v1/floorplans/")) {
		return fmt.Errorf("invalid model or source URL: %s", model)
	}
	return nil
}

func (w *featureWorld) approved() error {
	out, err := w.cli("floorplans", "get", w.id)
	if err != nil {
		return err
	}
	var plan struct{ ID, Status string }
	if err := json.Unmarshal(out, &plan); err != nil {
		return fmt.Errorf("decode floorplan: %w", err)
	}
	if plan.ID != w.id || plan.Status != "approved" {
		return fmt.Errorf("unexpected floorplan: %s", out)
	}
	return nil
}

func (w *featureWorld) approvedCount(count int) error {
	out, err := w.cli("operator", "runtime")
	if err != nil {
		return err
	}
	var result struct{ Queue struct{ Approved int } }
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("decode runtime: %w", err)
	}
	if result.Queue.Approved != count {
		return fmt.Errorf("unexpected runtime count: %s", out)
	}
	return nil
}
