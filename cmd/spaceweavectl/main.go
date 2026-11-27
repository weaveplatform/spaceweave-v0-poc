// Command spaceweavectl calls a remote SpaceWeave server through its public SDK.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/weaveplatform/spaceweave/internal/ctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := ctl.Execute(
		ctx,
		ctl.App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.LookupEnv},
		os.Args[1:],
	)
	stop()
	os.Exit(code)
}
