// Command kestrel is a local-first debugging intelligence CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Aareevs/Kestrel-CLI/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, cli.DefaultStreams(), os.Args[1:])
	stop()
	os.Exit(code)
}
