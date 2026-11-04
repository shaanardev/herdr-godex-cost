package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newCommand().Run(ctx, os.Args)
	// Release signal resources before os.Exit, which does not run deferred calls.
	stop()
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	slog.Error("herdr codex cost", "error", err)
	code := 1
	var exitCoder cli.ExitCoder
	if errors.As(err, &exitCoder) {
		code = exitCoder.ExitCode()
	}
	os.Exit(code)
}
