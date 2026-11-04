package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

func refreshAction(ctx context.Context, command *cli.Command) error {
	paneID := command.String("pane")
	if err := newRunner(command).Refresh(ctx, paneID); err != nil {
		return fmt.Errorf("refresh pane %q: %w", paneID, err)
	}
	return nil
}
