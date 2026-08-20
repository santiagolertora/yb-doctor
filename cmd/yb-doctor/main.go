// Command yb-doctor is the YugabyteDB cluster diagnostic CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/santiagolertora/yb-doctor/internal/adapter/inbound/cli"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.Execute(ctx, cli.Deps{
		Version:   version,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Args:      args,
		LookupEnv: os.Getenv,
	})
}
