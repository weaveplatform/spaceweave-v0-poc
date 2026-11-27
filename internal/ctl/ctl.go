// Package ctl implements the remote spaceweavectl client using only the public SDK.
package ctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/spaceweave/internal/platform/buildinfo"
	"github.com/weaveplatform/spaceweave/pkg/client/api"
	"github.com/weaveplatform/spaceweave/pkg/sdk"
)

// ErrUsage distinguishes invalid commands from failed remote operations.
var ErrUsage = errors.New("invalid command input")

// App supplies process I/O and optional client substitutions.
type App struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Getenv     func(string) (string, bool)
	HTTPClient *http.Client
}

// Execute returns zero for success, one for runtime failures, two for usage.
func Execute(ctx context.Context, app App, args []string) int {
	//nolint:contextcheck // Cobra passes ctx to callbacks through ExecuteContext below.
	root := New(app)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(root.ErrOrStderr(), err)
	if errors.Is(err, ErrUsage) || errors.Is(err, sdk.ErrEndpoint) ||
		errors.Is(err, sdk.ErrInput) ||
		strings.HasPrefix(err.Error(), "unknown command ") {
		return 2
	}
	return 1
}

// New constructs a fresh command tree. Secrets are accepted through environment
// variables only, so they do not appear in shell history or process arguments.
func New(app App) *cobra.Command {
	getenv := app.Getenv
	if getenv == nil {
		getenv = os.LookupEnv
	}
	endpoint, _ := getenv("SPACEWEAVECTL_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8000"
	}
	root := &cobra.Command{
		Use: "spaceweavectl", Short: "Call a SpaceWeave server through its public APIs",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.SetIn(app.Stdin)
	root.SetOut(app.Stdout)
	root.SetErr(app.Stderr)
	root.SetFlagErrorFunc(
		func(_ *cobra.Command, err error) error { return fmt.Errorf("%w: %w", ErrUsage, err) },
	)
	root.PersistentFlags().
		StringVar(&endpoint, "endpoint", endpoint, "server origin (SPACEWEAVECTL_ENDPOINT)")
	client := func() (*sdk.Client, error) {
		product, _ := getenv("SPACEWEAVECTL_TOKEN")
		operator, _ := getenv("SPACEWEAVECTL_OPERATOR_TOKEN")
		options := []sdk.Option{sdk.WithToken(product), sdk.WithOperatorToken(operator)}
		if app.HTTPClient != nil {
			options = append(options, sdk.WithHTTPClient(app.HTTPClient))
		}
		return sdk.New(
			endpoint,
			options...) //nolint:wrapcheck // SDK errors are the command's public errors
	}
	root.AddCommand(
		command(
			"version",
			"Print this client's build version",
			0,
			func(cmd *cobra.Command, _ []string) error {
				return show(cmd, map[string]string{"version": buildinfo.Version()})
			},
		),
		remote(
			"info",
			"Read product capabilities",
			0,
			client,
			func(cmd *cobra.Command, c *sdk.Client, _ []string) (any, error) {
				return c.GetInfo(cmd.Context())
			},
		),
		floorplans(client),
		profiles(client),
		operator(client),
	)
	return root
}

type clientFunc func() (*sdk.Client, error)

func command(
	use, short string,
	nargs int,
	run func(*cobra.Command, []string) error,
) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(nargs)(cmd, args); err != nil {
				return fmt.Errorf("%w: %w", ErrUsage, err)
			}
			return nil
		},
		RunE: run,
	}
}

func remote(use, short string, nargs int, client clientFunc,
	call func(*cobra.Command, *sdk.Client, []string) (any, error),
) *cobra.Command {
	return command(use, short, nargs, func(cmd *cobra.Command, args []string) error {
		c, err := client()
		if err != nil {
			return err
		}
		value, err := call(cmd, c, args)
		if err != nil {
			return err
		}
		return show(cmd, value)
	})
}

func show(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func floorplans(client clientFunc) *cobra.Command {
	root := &cobra.Command{Use: "floorplans", Short: "Import, query and review floorplans"}
	var limit int
	var cursor string
	var all bool
	list := remote("list", "List one page or all floorplans", 0, client,
		func(cmd *cobra.Command, c *sdk.Client, _ []string) (any, error) {
			params := &api.ListFloorplansParams{}
			if cmd.Flags().Changed("limit") {
				params.Limit = &limit
			}
			if cmd.Flags().Changed("cursor") {
				params.Cursor = &cursor
			}
			if !all {
				return c.ListFloorplans(cmd.Context(), params)
			}
			plans := []api.Floorplan{}
			for plan, err := range c.Floorplans(cmd.Context(), params) {
				if err != nil {
					return nil, err
				}
				plans = append(plans, plan)
			}
			return plans, nil
		})
	list.Flags().IntVar(&limit, "limit", 50, "maximum floorplans per page")
	list.Flags().StringVar(&cursor, "cursor", "", "opaque continuation cursor")
	list.Flags().BoolVar(&all, "all", false, "follow pages until completion")
	root.AddCommand(
		list,
		remote(
			"get ID",
			"Read a floorplan",
			1,
			client,
			func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
				return c.GetFloorplan(cmd.Context(), args[0])
			},
		),
		upload(client),
		remote("processing ID", "Read floorplan processing status", 1, client,
			func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
				return c.GetProcessing(cmd.Context(), args[0])
			}),
		files(client),
		download(client, false),
		download(client, true),
	)
	for _, action := range []sdk.ReviewAction{sdk.Approve, sdk.Reject, sdk.RetryTrace, sdk.Retrace} {
		var note string
		cmd := remote(string(action)+" ID", "Apply an explicit floorplan review action", 1, client,
			func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
				return c.ReviewFloorplan(cmd.Context(), args[0], action, note)
			})
		cmd.Flags().StringVar(&note, "note", "", "review note")
		root.AddCommand(cmd)
	}
	return root
}

func input(cmd *cobra.Command, file, name string) (io.ReadCloser, string, error) {
	if file == "-" {
		if name == "" {
			return nil, "", fmt.Errorf("%w: --name is required for an upload from stdin", ErrUsage)
		}
		return io.NopCloser(cmd.InOrStdin()), name, nil
	}
	f, err := os.Open(file) //nolint:gosec // explicit local input path supplied by the CLI user
	if err != nil {
		return nil, "", fmt.Errorf("read input: %w", err)
	}
	if name == "" {
		name = filepath.Base(file)
	}
	return f, name, nil
}

func upload(client clientFunc) *cobra.Command {
	var name string
	cmd := remote("upload FILE", "Stream a drawing to the server ('-' for stdin)", 1, client,
		func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
			file, filename, err := input(cmd, args[0], name)
			if err != nil {
				return nil, err
			}
			defer file.Close() //nolint:errcheck // read-only input
			return c.UploadFloorplan(cmd.Context(), filename, file)
		})
	cmd.Flags().StringVar(&name, "name", "", "uploaded file name (required for stdin)")
	return cmd
}

func files(client clientFunc) *cobra.Command {
	root := &cobra.Command{Use: "files", Short: "Manage attached floorplan files"}
	var name, role string
	attach := remote("attach ID FILE", "Stream a space plan or reference attachment", 2, client,
		func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
			file, filename, err := input(cmd, args[1], name)
			if err != nil {
				return nil, err
			}
			defer file.Close() //nolint:errcheck // read-only input
			return c.AttachFile(
				cmd.Context(),
				args[0],
				filename,
				api.AttachFloorplanFileMultipartBodyRole(role),
				file,
			)
		})
	attach.Flags().StringVar(&name, "name", "", "uploaded file name (required for stdin)")
	attach.Flags().StringVar(&role, "role", "reference", "reference or spaces")
	root.AddCommand(attach,
		remote("detach ID SHA", "Detach an attached file", 2, client,
			func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
				return c.DetachFile(cmd.Context(), args[0], args[1])
			}), download(client, false),
	)
	return root
}

func download(client clientFunc, artifact bool) *cobra.Command {
	var output string
	use := "download ID SHA"
	if artifact {
		use = "artifact ID KIND"
	}
	cmd := command(use, "Download bytes to stdout or --output FILE", 2,
		func(cmd *cobra.Command, args []string) error {
			c, err := client()
			if err != nil {
				return err
			}
			var response *http.Response
			if artifact {
				kind := api.GetFloorplanArtifactParamsArtifact(args[1])
				if !kind.Valid() {
					return fmt.Errorf("%w: unknown artifact %q", ErrUsage, args[1])
				}
				response, err = c.GetArtifact(cmd.Context(), args[0], kind)
			} else {
				response, err = c.DownloadFile(cmd.Context(), args[0], args[1])
			}
			if err != nil {
				return fmt.Errorf("download: %w", err)
			}
			defer response.Body.Close() //nolint:errcheck // response consumed below
			if output == "-" {
				if _, err := io.Copy(cmd.OutOrStdout(), response.Body); err != nil {
					return fmt.Errorf("download output: %w", err)
				}
				return nil
			}
			file, err := os.OpenFile(
				output,
				os.O_CREATE|os.O_EXCL|os.O_WRONLY,
				0o600,
			) //nolint:gosec // explicit user destination
			if err != nil {
				return fmt.Errorf("create download: %w", err)
			}
			_, copyErr := io.Copy(file, response.Body)
			closeErr := file.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				_ = os.Remove(output)
				return fmt.Errorf("save download: %w", err)
			}
			return show(cmd, map[string]string{"path": output})
		})
	cmd.Flags().StringVar(&output, "output", "-", "destination; '-' writes bytes to stdout")
	return cmd
}

func profiles(client clientFunc) *cobra.Command {
	root := &cobra.Command{Use: "layer-profiles", Short: "Read and replace drawing layer profiles"}
	root.AddCommand(
		remote("get NAME", "Read a layer profile", 1, client,
			func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
				return c.GetProfile(cmd.Context(), args[0])
			}),
		remote("put NAME FILE", "Replace a profile from JSON ('-' for stdin)", 2, client,
			func(cmd *cobra.Command, c *sdk.Client, args []string) (any, error) {
				file, _, err := input(cmd, args[1], "profile.json")
				if err != nil {
					return nil, err
				}
				defer file.Close() //nolint:errcheck // read-only profile
				var profile api.LayerProfile
				decoder := json.NewDecoder(file)
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&profile); err != nil {
					return nil, fmt.Errorf("%w: profile JSON: %w", ErrUsage, err)
				}
				var extra any
				if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
					return nil, fmt.Errorf(
						"%w: profile must contain exactly one JSON object",
						ErrUsage,
					)
				}
				return c.PutProfile(cmd.Context(), args[0], profile)
			}),
	)
	return root
}

func operator(client clientFunc) *cobra.Command {
	root := &cobra.Command{
		Use:   "operator",
		Short: "Installation operations with independent operator authority",
	}
	root.AddCommand(remote(
		"runtime",
		"Read installation tracing runtime",
		0,
		client,
		func(cmd *cobra.Command, c *sdk.Client, _ []string) (any, error) { return c.GetRuntime(cmd.Context()) },
	))
	return root
}
