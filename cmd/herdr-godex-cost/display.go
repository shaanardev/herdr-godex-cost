package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/sarora/herdr-godex-cost/internal/refresh"
	"github.com/urfave/cli/v3"
)

func displayAction(ctx context.Context, command *cli.Command) error {
	mode := command.String("mode")
	if err := refresh.SetDisplayMode(command.String("state-dir"), mode); err != nil {
		return err
	}
	runner := newRunner(command)
	panes, err := runner.HerdrClient.ReadPanes(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, pane := range panes {
		if pane.Agent != "codex" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runner.Refresh(ctx, pane.PaneID); err != nil {
			if errors.Is(err, refresh.ErrSessionNotFound) || errors.Is(err, refresh.ErrAmbiguousSession) {
				slog.Warn("display mode saved; pane session unresolved", "mode", mode, "pane", pane.PaneID, "error", err)
				continue
			}
			failures = append(failures, fmt.Errorf("refresh pane %q: %w", pane.PaneID, err))
		}
	}
	return errors.Join(failures...)
}
