// Command verify-generate checks the committed REST contract and wire outputs.
package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/weaveplatform/spaceweave/internal/devtools/generation"
)

func main() {
	err := generation.Verify(".", func() error {
		cmd := exec.Command("make", "generate")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
