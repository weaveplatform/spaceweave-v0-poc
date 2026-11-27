// Package floorplans assembles the drawing import, tracing and review module.
package floorplans

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/weaveplatform/spaceweave/internal/floorplans/adapters/inbox"
	worker "github.com/weaveplatform/spaceweave/internal/floorplans/adapters/jobs"
	"github.com/weaveplatform/spaceweave/internal/floorplans/adapters/localimport/catalog"
	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/autotrace/profile"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/plan"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/trace"
)

// Options are resolved deployment settings, independent of CLI/YAML parsing.
type Options struct {
	Inbox, Output, Node, Validator, Profile string
	Workers                                 int
	Settle                                  time.Duration
	Tracer, Model, Effort                   string
	Limits                                  trace.Limits
	Prices                                  trace.Prices
}

// Dependencies supplies process resources and optional test substitutions.
type Dependencies struct {
	Log           *slog.Logger
	Validator     plan.Validator
	ClientOptions []option.RequestOption
}

// Module owns the local import/review lifecycle and its processing runtime.
// Local catalogue locking is retained; it is not a distributed job lease.
type Module struct {
	options Options
	log     *slog.Logger
	store   *catalog.Store
	worker  *worker.Worker
	watcher *inbox.Watcher
	wake    chan struct{}
}

// Open exposes review operations without requiring a tracing runtime or Node.
func Open(output string) (api.Service, error) {
	return catalog.Open(output) //nolint:wrapcheck // catalogue errors name their source
}

// New constructs the module's adapters and validates tracing prerequisites.
func New(opts Options, deps Dependencies) (*Module, error) {
	store, err := catalog.Open(opts.Output)
	if err != nil {
		return nil, err //nolint:wrapcheck // catalogue errors name their source
	}
	if deps.Log == nil {
		deps.Log = slog.New(slog.DiscardHandler)
	}
	d := &Module{options: opts, log: deps.Log, store: store, wake: make(chan struct{}, 1)}
	if d.worker, err = newWorker(deps, opts, store, deps.Log); err != nil {
		return nil, err
	}
	d.watcher = &inbox.Watcher{
		Dir: opts.Inbox, Settle: opts.Settle, Log: d.log,
		Ingest: func(path string) error {
			e, created, err := d.store.Ingest(path)
			if err != nil {
				return err //nolint:wrapcheck // logged by the watcher with the file name
			}
			if !created {
				d.log.Info("already seen", "file", path, "plan", e.ID, "status", e.Status)
				return nil
			}
			d.log.Info("queued", "file", path, "plan", e.ID)
			d.Wake()
			return nil
		},
		Attach: d.attach,
	}
	return d, nil
}

// newWorker wires the tracer to the configuration. It checks node and the
// validator script up front: without them every trace would fail at the
// end, and a Claude trace would spend money first.
//
// Only the Claude tracer gets an API client, so the automatic tracer never
// reads ANTHROPIC_API_KEY and can't send a plan anywhere by mistake.
func newWorker(
	deps Dependencies,
	c Options,
	store *catalog.Store,
	log *slog.Logger,
) (*worker.Worker, error) {
	v := deps.Validator
	if v == nil {
		if _, err := exec.LookPath(c.Node); err != nil {
			return nil, fmt.Errorf("node is needed to validate plans: %w", err)
		}
		if _, err := os.Stat(c.Validator); err != nil {
			return nil, fmt.Errorf(
				"validator script: %w (run from the repository root, or set validator)",
				err,
			)
		}
		v = plan.NodeValidator{Node: c.Node, Script: c.Validator}
	}
	w := &worker.Worker{
		Store:    store,
		Log:      log,
		Tracer:   c.Tracer,
		Options:  trace.Options{Validator: v},
		Profiles: profiles(c, store),
	}
	if c.Tracer == trace.TracerClaude {
		w.Trace = trace.Run
		w.Options.Client = anthropic.NewClient(deps.ClientOptions...)
		w.Options.Model, w.Options.Effort = c.Model, c.Effort
		w.Options.Limits, w.Options.Prices = c.Limits, c.Prices
	}
	return w, nil
}

// profiles are the client layer profiles, beside the plans in the output
// folder. The worker and the server each get one; they share only the
// folder, so a profile saved from the page reaches the next trace however
// the two are run.
func profiles(c Options, store *catalog.Store) *profile.Library {
	return &profile.Library{Dir: filepath.Join(store.Dir, ProfilesDir), Fixed: c.Profile}
}

// ProfilesDir is the client profiles' folder in the output folder. No
// plan's folder can clash with it: a plan's name always ends in a dash and
// six hex digits of its hash.
const ProfilesDir = "profiles"

// attach adds an inbox file to the plan traced from the drawing beside it.
// A plan being converted or traced can't take it yet; the watcher offers it
// again until the run ends, and the plan is traced again with it.
func (d *Module) attach(drawing, path, role string) error {
	plan, err := d.store.PlanOf(drawing)
	if err != nil {
		return err //nolint:wrapcheck // logged by the watcher with the file names
	}
	e, added, err := d.store.Attach(plan.ID, path, role)
	if errors.Is(err, catalog.ErrTransition) {
		// An approved plan waits for a reviewer to send it back; one being
		// converted or traced only for the run to end.
		if now, getErr := d.store.Get(plan.ID); getErr == nil && now.Status != catalog.Approved {
			return inbox.ErrBusy
		}
	}
	if err != nil {
		return err //nolint:wrapcheck // logged by the watcher with the file names
	}
	if !added {
		d.log.Info("already attached", "file", path, "plan", e.ID)
		return nil
	}
	d.log.Info("attached", "file", path, "plan", e.ID, "role", role)
	if e.Status == catalog.Queued {
		d.Wake()
	}
	return nil
}

// Wake wakes an idle worker. The channel holds one wake-up; more would only
// say the same thing.
func (d *Module) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run runs the watcher, the workers and any extra services until ctx is done
// or one of them stops, which stops the rest.
func (d *Module) Run(ctx context.Context, extra ...func(context.Context) error) error {
	args := []any{
		"inbox",
		d.options.Inbox,
		"output",
		d.options.Output,
		"workers",
		d.options.Workers,
		"tracer",
		d.options.Tracer,
	}
	if d.options.Tracer == trace.TracerClaude {
		args = append(args, "model", d.options.Model, "maxUsd", d.options.Limits.MaxUSD)
	}
	d.log.Info("watching", args...)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	services := append([]func(context.Context) error{
		d.watcher.Run,
		func(ctx context.Context) error { return d.worker.Run(ctx, d.options.Workers, d.wake) },
	}, extra...)
	errs := make([]error, len(services))
	var wg sync.WaitGroup
	for i, run := range services {
		wg.Go(func() {
			errs[i] = run(ctx)
			cancel()
		})
	}
	wg.Wait()
	d.log.Info("stopped")
	return errors.Join(errs...)
}

// Plans exposes the supported import/review interface, not the storage adapter.
func (d *Module) Plans() api.Service { return d.store }

// Profiles returns the processing profiles shared by HTTP review and tracing.
func (d *Module) Profiles() *profile.Library { return profiles(d.options, d.store) }

// Trace imports and processes one plan, preserving duplicate-upload semantics.
func (d *Module) Trace(ctx context.Context, path string) (api.Entry, bool, error) {
	e, created, err := d.store.Ingest(path)
	if err != nil || !created {
		return e, created, err //nolint:wrapcheck // catalogue errors name their source
	}
	first := api.Tracing
	if e.Source == "" {
		first = api.Converting
	}
	if err := d.store.Transition(e.ID, first, func(x *api.Entry) { x.Attempts++ }); err != nil {
		return e, true, err //nolint:wrapcheck // catalogue errors name their source
	}
	e, err = d.store.Get(e.ID)
	if err != nil {
		return e, true, err //nolint:wrapcheck // catalogue errors name their source
	}
	if err := d.worker.Process(ctx, e); err != nil {
		return e, true, err //nolint:wrapcheck // worker errors name their source
	}
	e, err = d.store.Get(e.ID)
	return e, true, err //nolint:wrapcheck // catalogue errors name their source
}
