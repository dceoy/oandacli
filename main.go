// Command oandacli is a CLI and MCP server for the OANDA REST-V20 API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dceoy/oandacli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.New().Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
