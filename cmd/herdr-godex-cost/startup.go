package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/urfave/cli/v3"
)

func startupAction(ctx context.Context, command *cli.Command) error {
	runner := newRunner(command)
	panes, err := runner.HerdrClient.ReadPanes(ctx)
	if err != nil {
		return fmt.Errorf("list startup panes: %w", err)
	}
	var failures []error
	for _, pane := range panes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if pane.Agent != "codex" {
			continue
		}
		if pane.AgentStatus == "working" {
			err = startWorker(ctx, command, pane.PaneID)
		} else {
			err = runner.Refresh(ctx, pane.PaneID)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("initialize pane %q: %w", pane.PaneID, err))
		}
	}
	return errors.Join(failures...)
}
