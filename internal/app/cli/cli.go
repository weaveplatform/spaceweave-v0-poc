// Package cli is the spaceweave command line: the inbox daemon, one-off
// traces, and the review commands that approve or send back drafts.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/spf13/cobra"

	application "github.com/weaveplatform/spaceweave/internal/app"
	"github.com/weaveplatform/spaceweave/internal/app/config"
	"github.com/weaveplatform/spaceweave/internal/floorplans"
	server "github.com/weaveplatform/spaceweave/internal/floorplans/adapters/http"
	"github.com/weaveplatform/spaceweave/internal/floorplans/api"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/convert"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/plan"
	"github.com/weaveplatform/spaceweave/internal/floorplans/processing/vector"
	"github.com/weaveplatform/spaceweave/internal/platform/buildinfo"
)

// Errors. ErrUsage is a command used wrongly; Execute exits 2 for it.
var (
	ErrUsage       = errors.New("usage")
	ErrTraceFailed = errors.New("trace failed")
)

// App is what the commands need from outside. Tests replace the pieces that
// reach the network or the filesystem's node install.
type App struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) (string, bool)
	// ClientOptions are added to the Anthropic client's; tests point it at
	// a fake API here.
	ClientOptions []option.RequestOption
	// Validator, when set, replaces the node validator.
	Validator plan.Validator
}

type flags struct {
	config  string
	inbox   string
	output  string
	model   string
	maxUSD  float64
	workers int
	profile string
	verbose bool
	addr    string
	site    string
	hosts   []string
}

// Execute runs the command line and returns the process exit code.
func Execute(ctx context.Context, app App, args []string) int {
	//nolint:contextcheck // cobra hands ctx to each command through ExecuteContext
	root := New(app)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		_, _ = fmt.Fprintln(app.Stderr, "error:", err)
		return 2
	default:
		_, _ = fmt.Fprintln(app.Stderr, "error:", err)
		return 1
	}
}

// New builds the command tree.
func New(app App) *cobra.Command {
	f := &flags{}
	root := &cobra.Command{
		Use:   "spaceweave",
		Short: "Trace floor-plan images into the isometric microsite",
		Long: "spaceweave watches a folder for floor-plan images and drawings, traces each one into a plan, " +
			"and publishes it to the microsite as a draft for review.\n\n" +
			"Tracing runs here and gives the same plan for the same file every time; nothing is sent anywhere. " +
			"With `tracer: claude` a Claude agent traces instead: images are then sent to the Claude API, and the " +
			"API key comes from ANTHROPIC_API_KEY or an `ant auth login` profile.",
		Version:       buildinfo.Version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(app.Stdout)
	root.SetErr(app.Stderr)
	root.SetFlagErrorFunc(
		func(_ *cobra.Command, err error) error { return fmt.Errorf("%w: %w", ErrUsage, err) },
	)
	p := root.PersistentFlags()
	p.StringVar(&f.config, "config", "", "config file (default "+config.DefaultPath+" if present)")
	p.StringVar(&f.inbox, "inbox", "", "folder watched for new images")
	p.StringVar(&f.output, "output", "", "the microsite's generated/ folder")
	p.StringVar(&f.model, "model", "", "Claude model")
	p.Float64Var(&f.maxUSD, "max-usd", 0, "spending cap per plan, US dollars")
	p.IntVar(&f.workers, "workers", 0, "plans traced at once")
	p.StringVar(
		&f.profile,
		"profile",
		"",
		"client layer profile to read drawings with (profiles/<name>.yaml in the output folder)",
	)
	p.BoolVarP(&f.verbose, "verbose", "v", false, "debug logging")

	root.AddCommand(
		watchCmd(app, f),
		serveCmd(app, f),
		traceCmd(app, f),
		convertCmd(app),
		plansCmd(app, f),
	)
	return root
}

func usageArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return fmt.Errorf("%w: %w", ErrUsage, err)
		}
		return nil
	}
}

// load resolves the configuration: defaults, then the file, then the
// environment, then flags.
func (f *flags) load(cmd *cobra.Command, app App) (config.Config, error) {
	path, required := f.config, true
	if path == "" {
		path, required = config.DefaultPath, false
	}
	c, err := config.Load(path, required, app.Getenv)
	if err != nil {
		return c, err //nolint:wrapcheck // config errors name the file
	}
	set := cmd.Flags().Changed
	if set("inbox") {
		c.Inbox = f.inbox
	}
	if set("output") {
		c.Output = f.output
	}
	if set("model") {
		c.Model = f.model
	}
	if set("max-usd") {
		c.Limits.MaxUSD = f.maxUSD
	}
	if set("workers") {
		c.Workers = f.workers
	}
	if set("profile") {
		c.Profile = f.profile
	}
	if set("addr") {
		c.Serve.Addr = f.addr
	}
	if set("site") {
		c.Serve.Site = f.site
	}
	if set("allow-host") {
		c.Serve.Hosts = append(c.Serve.Hosts, f.hosts...)
	}
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	return c, nil
}

func (f *flags) logger(app App) *slog.Logger {
	level := slog.LevelInfo
	if f.verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(app.Stderr, &slog.HandlerOptions{Level: level}))
}

func openStore(c config.Config) (api.Service, error) {
	//nolint:wrapcheck // catalogue errors already name the path
	return floorplans.Open(c.Output)
}

func moduleOptions(c config.Config) floorplans.Options {
	return floorplans.Options{
		Inbox: c.Inbox, Output: c.Output, Node: c.Node, Validator: c.Validator,
		Profile: c.Profile, Workers: c.Workers, Settle: c.Settle, Tracer: c.Tracer,
		Model: c.Model, Effort: c.Effort, Limits: c.Limits, Prices: c.Prices,
	}
}

func newModule(app App, c config.Config, log *slog.Logger) (*floorplans.Module, error) {
	//nolint:wrapcheck // module errors already identify the failed prerequisite
	return floorplans.New(moduleOptions(c), floorplans.Dependencies{
		Log: log, Validator: app.Validator, ClientOptions: app.ClientOptions,
	})
}

// info is what the page is told about the tracer.
func info(c config.Config) server.Info {
	i := server.Info{Version: buildinfo.Version(), Tracer: c.Tracer}
	if c.Tracer == config.TracerClaude {
		i.Model, i.MaxUSD = c.Model, c.Limits.MaxUSD
	}
	return i
}

// daemon supplies process configuration around the Floorplans runtime.
type daemon struct {
	c      config.Config
	log    *slog.Logger
	module *floorplans.Module
}

func (f *flags) daemon(cmd *cobra.Command, app App) (*daemon, error) {
	c, err := f.load(cmd, app)
	if err != nil {
		return nil, err
	}
	log := f.logger(app)
	m, err := newModule(app, c, log)
	if err != nil {
		return nil, err
	}
	return &daemon{c: c, log: log, module: m}, nil
}

func watchCmd(app App, f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Watch the inbox and trace each new image",
		Long: "Watches the inbox folder. Each new image is copied into the output folder, queued, " +
			"traced and published as a draft. Runs until interrupted.",
		Args: usageArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := f.daemon(cmd, app)
			if err != nil {
				return err
			}
			return d.module.Run(cmd.Context())
		},
	}
}

func serveCmd(app App, f *flags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the microsite with uploads and review, and trace what arrives",
		Long: "Serves the microsite. Images dropped on the page (or into the inbox) are traced " +
			"and published as drafts, which can be approved or sent back from the page. " +
			"Runs until interrupted.\n\n" +
			"Only loopback host names are answered unless others are allowed, because any page in " +
			"the browser can reach a local server (and, with `tracer: claude`, every upload spends money).",
		Args: usageArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := f.daemon(cmd, app)
			if err != nil {
				return err
			}
			s := d.c.Serve
			if _, err := os.Stat(filepath.Join(s.Site, "index.html")); err != nil {
				return fmt.Errorf(
					"site folder: %w (run from the repository root, or set serve.site)",
					err,
				)
			}
			ln, err := (&net.ListenConfig{}).Listen(cmd.Context(), "tcp", s.Addr)
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			hosts := s.Hosts
			h, _, _ := net.SplitHostPort(ln.Addr().String())
			if ip := net.ParseIP(h); ip != nil && ip.IsUnspecified() {
				if len(s.Hosts) == 0 {
					d.log.Warn(
						"listening on every interface, but answering only to localhost names; add others with --allow-host",
					)
				}
			} else {
				hosts = append(
					slices.Clone(hosts),
					strings.ToLower(h),
				) // a named interface answers to its own address
			}
			srv := &server.Server{
				Store:    d.module.Plans(),
				Wake:     d.module.Wake,
				Log:      d.log,
				Info:     info(d.c),
				Profiles: d.module.Profiles(),
			}
			hs := &http.Server{
				Handler:           application.Router(s.Site, d.c.Output, hosts, srv.Handler()),
				ReadHeaderTimeout: 10 * time.Second,
			}
			d.log.Info("serving", "url", "http://"+ln.Addr().String()+"/", "site", s.Site)
			return d.module.Run(cmd.Context(), func(ctx context.Context) error {
				served := make(chan error, 1)
				go func() { served <- hs.Serve(ln) }()
				select {
				case err := <-served:
					return fmt.Errorf("serve: %w", err)
				case <-ctx.Done():
				}
				// Uploads in flight get a moment to land; a trace in progress
				// is requeued by the worker, not waited for.
				stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				if err := hs.Shutdown(stop); err != nil {
					return fmt.Errorf("shut down: %w", err)
				}
				return nil
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.addr, "addr", "", "listen address (default "+config.Default().Serve.Addr+")")
	fl.StringVar(&f.site, "site", "", "the microsite's folder")
	fl.StringSliceVar(&f.hosts, "allow-host", nil, "extra host name to answer to (repeatable)")
	return cmd
}

func traceCmd(app App, f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "trace <image or drawing>",
		Short: "Trace one image or drawing now, without the watcher",
		Long: "Adds the file to the output folder and traces it straight away, converting a drawing first. " +
			"A file already in the catalogue isn't traced again; use `plans reject` or `plans retry` for that.",
		Args: usageArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := f.load(cmd, app)
			if err != nil {
				return err
			}
			m, err := newModule(app, c, f.logger(app))
			if err != nil {
				return err
			}
			if _, err := checkFile(args[0]); err != nil {
				return err
			}
			e, created, err := m.Trace(cmd.Context(), args[0])
			if err != nil {
				return err //nolint:wrapcheck // preserve the processing error reported by the module
			}
			if !created {
				_, _ = fmt.Fprintf(
					app.Stdout,
					"%s is already plan %s (%s)\n",
					args[0],
					e.ID,
					e.Status,
				)
				return nil
			}
			printEntry(app.Stdout, e, c.Output)
			if e.Status == api.Failed {
				return fmt.Errorf("%w: %s", ErrTraceFailed, e.Error)
			}
			return nil
		},
	}
}

func printEntry(w io.Writer, e api.Entry, output string) {
	_, _ = fmt.Fprintf(
		w,
		"%s  %s  confidence %s  cost $%.2f\n  %s\n",
		e.ID,
		e.Status,
		confidence(e),
		e.CostUSD,
		filepath.Join(output, e.ID),
	)
}

func confidence(e api.Entry) string {
	if e.Confidence == nil {
		return "-"
	}
	return strconv.FormatFloat(*e.Confidence, 'f', 2, 64)
}

// checkFile identifies a file and checks its name agrees with its content.
func checkFile(path string) (convert.Format, error) {
	f, err := os.Open(path) //nolint:gosec // a path the user gave
	if err != nil {
		return convert.Format{}, fmt.Errorf("read input: %w", err) // the *PathError names the path
	}
	defer f.Close() //nolint:errcheck // read-only
	head := make([]byte, convert.SniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return convert.Format{}, fmt.Errorf("read input: %w", err)
	}
	format, err := convert.Check(filepath.Base(path), head[:n])
	if err != nil {
		return convert.Format{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	return format, nil
}

func convertCmd(app App) *cobra.Command {
	var out, svg string
	var maxSide int
	cmd := &cobra.Command{
		Use:   "convert <drawing>",
		Short: "Render a DXF, DWG or SVG drawing as the PNG the tracer reads, and as SVG",
		Long: "Renders the drawing the way the tracer will see it: black ink on white, with the scale " +
			"worked out from the drawing's units where it states them. Nothing is sent anywhere.",
		Args: usageArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			format, err := checkFile(src)
			if err != nil {
				return err
			}
			if !format.Drawing {
				return fmt.Errorf(
					"%w: %s is already a %s image; only drawings need converting",
					ErrUsage,
					src,
					format.Label,
				)
			}
			base := strings.TrimSuffix(src, filepath.Ext(src))
			if out == "" {
				out = base + ".png"
			}
			if svg == "" {
				svg = base + ".svg"
				if format.ID == convert.SVG.ID {
					svg = base + ".plan.svg" // don't overwrite the input
				}
			}
			tmp, err := os.MkdirTemp("", "spaceweave-convert-")
			if err != nil {
				return fmt.Errorf("convert: %w", err)
			}
			defer os.RemoveAll(tmp) //nolint:errcheck // temp dir
			res, err := convert.File(cmd.Context(), src, tmp, convert.Options{MaxSide: maxSide})
			if err != nil {
				return fmt.Errorf("convert %s: %w", src, err)
			}
			for from, to := range map[string]string{res.Image: out, res.SVG: svg} {
				b, err := os.ReadFile(from) //nolint:gosec // our temp dir
				if err != nil {
					return fmt.Errorf("convert: %w", err)
				}
				// Readable by others, like any image the user saves; the
				// path is the one they gave.
				//nolint:gosec // see above
				if err := os.WriteFile(to, b, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", to, err)
				}
			}
			w := app.Stdout
			_, _ = fmt.Fprintf(
				w,
				"%s: %s, %d × %d px\n",
				src,
				res.Format.Label,
				res.Width,
				res.Height,
			)
			_, _ = fmt.Fprintf(w, "scale: %s\n", res.Scale())
			for _, n := range res.Notes {
				_, _ = fmt.Fprintf(w, "note: %s\n", n)
			}
			_, _ = fmt.Fprintf(w, "wrote %s and %s\n", out, svg)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "PNG to write (default: next to the drawing)")
	cmd.Flags().StringVar(&svg, "svg", "", "SVG to write (default: next to the drawing)")
	cmd.Flags().IntVar(&maxSide, "max-px", vector.DefaultMaxSide, "the image's long edge in pixels")
	return cmd
}

func plansCmd(app App, f *flags) *cobra.Command {
	cmd := &cobra.Command{Use: "plans", Short: "Review traced plans"}
	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List plans with their status, confidence and cost",
		Args:  usageArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := f.store(cmd, app)
			if err != nil {
				return err
			}
			entries, err := store.List()
			if err != nil {
				return err //nolint:wrapcheck // names itself
			}
			if asJSON {
				enc := json.NewEncoder(app.Stdout)
				enc.SetIndent("", " ")
				return enc.Encode(entries) //nolint:wrapcheck // a write to stdout
			}
			tw := tabwriter.NewWriter(app.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tSTATUS\tCONFIDENCE\tCOST\tATTEMPTS\tTITLE")
			for _, e := range entries {
				_, _ = fmt.Fprintf(
					tw,
					"%s\t%s\t%s\t$%.2f\t%d\t%s\n",
					e.ID,
					e.Status,
					confidence(e),
					e.CostUSD,
					e.Attempts,
					e.Title,
				)
			}
			return tw.Flush() //nolint:wrapcheck // a write to stdout
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "print the catalogue entries as JSON")

	approve := &cobra.Command{
		Use:   "approve <id>",
		Short: "Approve a draft; the site drops its Draft badge",
		Args:  usageArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return f.review(cmd, app, args[0], api.Approve, "")
		},
	}

	var note string
	reject := &cobra.Command{
		Use:   "reject <id>",
		Short: "Send a draft (or approved plan) back for another pass",
		Long: "Re-queues the plan. The note goes to the tracer as reviewer feedback, " +
			"along with the rejected trace to correct.",
		Args: usageArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return f.review(cmd, app, args[0], api.Reject, note)
		},
	}
	reject.Flags().StringVar(&note, "note", "", "what to fix")

	retry := &cobra.Command{
		Use:   "retry <id>",
		Short: "Re-queue a failed plan",
		Args:  usageArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return f.review(cmd, app, args[0], api.Retry, "")
		},
	}
	cmd.AddCommand(list, approve, reject, retry)
	return cmd
}

func (f *flags) store(cmd *cobra.Command, app App) (api.Service, error) {
	c, err := f.load(cmd, app)
	if err != nil {
		return nil, err
	}
	return openStore(c)
}

func (f *flags) review(
	cmd *cobra.Command,
	app App,
	id string,
	a api.Action,
	note string,
) error {
	store, err := f.store(cmd, app)
	if err != nil {
		return err
	}
	was, e, err := store.Review(id, a, note)
	if err != nil {
		return err //nolint:wrapcheck // names the plan
	}
	_, _ = fmt.Fprintf(app.Stdout, "%s: %s → %s\n", id, was, e.Status)
	return nil
}
