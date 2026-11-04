package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/herder"
	"github.com/sarora/herdr-godex-cost/internal/refresh"
	"github.com/urfave/cli/v3"
)

func newCommand() *cli.Command {
	command := &cli.Command{
		Name:           "herdr-godex-cost",
		Usage:          "Estimate Codex session costs in the Herdr sidebar",
		DefaultCommand: "refresh",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "pane",
				Usage: "Herdr pane ID (defaults to plugin context)",
			},
			&cli.DurationFlag{
				Name:  "interval",
				Value: 5 * time.Second,
				Usage: "wait between refreshes",
			},
			&cli.StringFlag{
				Name:    "codex-home",
				Usage:   "Codex data directory (defaults to ~/.codex)",
				Sources: cli.EnvVars("CODEX_HOME"),
			},
			&cli.StringFlag{
				Name:    "state-dir",
				Value:   filepath.Join(os.TempDir(), fmt.Sprintf("herdr-codex-cost-%d", os.Getuid())),
				Usage:   "cache, worker lock and log directory",
				Sources: cli.EnvVars("HERDR_PLUGIN_STATE_DIR"),
			},
		},
		// Return errors to main instead of letting the CLI print or exit itself.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   usageError,
		Commands: []*cli.Command{
			{Name: "refresh", Usage: "Refresh the cost for one pane", Action: refreshAction},
			{Name: "watch", Usage: "Refresh while a Codex pane is working", Action: watchAction},
			{Name: "event", Usage: "Handle a Herdr agent-status event", Action: eventAction},
			{Name: "startup", Usage: "Refresh existing panes and start active workers", Action: startupAction},
		},
	}
	for _, subcommand := range command.Commands {
		subcommand.Before = prepareCommand
		subcommand.OnUsageError = usageError
	}
	return command
}

func usageError(_ context.Context, command *cli.Command, err error, _ bool) error {
	return cli.Exit(fmt.Errorf("%s arguments: %w", command.Name, err), 2)
}

// prepareCommand validates configuration before an action can create files or
// invoke Herdr. An explicit pane takes precedence over plugin context.
func prepareCommand(ctx context.Context, command *cli.Command) (context.Context, error) {
	if err := ctx.Err(); err != nil {
		return ctx, err
	}
	if command.Args().Present() {
		return ctx, cli.Exit(fmt.Errorf("unexpected arguments: %v", command.Args().Slice()), 2)
	}
	if command.Duration("interval") <= 0 {
		return ctx, cli.Exit("interval must be positive", 2)
	}
	pane := paneContext{Command: command.Name, ExplicitPane: command.String("pane")}
	paneID, err := pane.resolve()
	if err != nil {
		return ctx, cli.Exit(err, 2)
	}
	if paneID == "" && command.Name == "event" {
		return ctx, nil
	}
	if err := command.Set("pane", paneID); err != nil {
		return ctx, fmt.Errorf("set pane: %w", err)
	}
	if command.String("codex-home") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ctx, fmt.Errorf("find home directory: %w", err)
		}
		if err := command.Set("codex-home", filepath.Join(home, ".codex")); err != nil {
			return ctx, fmt.Errorf("set Codex home: %w", err)
		}
	}
	if command.String("state-dir") == "" {
		return ctx, cli.Exit("state directory must not be empty", 2)
	}
	return ctx, nil
}

func newRunner(command *cli.Command) *refresh.Runner {
	binary := os.Getenv("HERDR_BIN_PATH")
	if binary == "" {
		binary = "herdr"
	}
	return refresh.NewRunner(herder.NewClient(binary), command.String("codex-home"), command.String("state-dir"), command.Duration("interval"))
}
