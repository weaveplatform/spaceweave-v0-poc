// Command openapi-bundle builds the committed REST contract bundles.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/weaveplatform/spaceweave/internal/devtools/openapibundle"
)

func main() {
	var src, out string
	cmd := &cobra.Command{
		Use: "openapi-bundle", Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			names, err := openapibundle.BundleAll(src, out)
			if err != nil {
				return err
			} //nolint:wrapcheck // bundler errors identify their source
			for _, name := range names {
				cmd.Println("bundled", name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&src, "src", "api/src", "contract fragment root")
	cmd.Flags().StringVar(&out, "out", "api", "bundle output directory")
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
