package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// Tests pass arguments without an executable name, unlike cli.Command.Run.
func runCLI(ctx context.Context, args []string) error {
	return newCommand().Run(ctx, append([]string{"herdr-godex-cost"}, args...))
}

func TestCommandConfiguration(t *testing.T) {
	for _, test := range []struct {
		name                  string
		args                  []string
		actionJSON, eventJSON string
		wantCommand, wantPane string
	}{
		{"explicit pane", []string{"refresh", "--pane", "explicit"}, `{"focused_pane_id":"focused"}`, "", "refresh", "explicit"},
		{"focused action pane", []string{"refresh"}, `{"focused_pane_id":"focused","pane_id":"fallback"}`, "", "refresh", "focused"},
		{"action pane fallback", []string{"refresh"}, `{"pane_id":"fallback"}`, "", "refresh", "fallback"},
		{"default refresh", nil, `{"pane_id":"default"}`, "", "refresh", "default"},
		{"global pane before command", []string{"--pane", "global", "watch"}, "", "", "watch", "global"},
		{"legacy flags", []string{"watch", "-pane", "legacy", "-interval", "1s"}, "", "", "watch", "legacy"},
		{"event source", []string{"event"}, `{"focused_pane_id":"wrong"}`, `{"data":{"pane_id":"event"}}`, "event", "event"},
		{"event data precedence", []string{"event"}, "", `{"pane_id":"wrong","data":{"pane_id":"event"}}`, "event", "event"},
		{"explicit overrides invalid context", []string{"refresh", "--pane", "explicit"}, "invalid", "", "refresh", "explicit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", test.actionJSON)
			t.Setenv("HERDR_PLUGIN_EVENT_JSON", test.eventJSON)
			codexHome, stateDir := t.TempDir(), t.TempDir()
			t.Setenv("CODEX_HOME", codexHome)
			t.Setenv("HERDR_PLUGIN_STATE_DIR", stateDir)
			root := newCommand()
			root.Writer = io.Discard
			root.ErrWriter = io.Discard
			called := false
			root.Command(test.wantCommand).Action = func(ctx context.Context, command *cli.Command) error {
				called = true
				if got := command.String("pane"); got != test.wantPane {
					t.Errorf("pane=%q; want %q", got, test.wantPane)
				}
				if command.String("codex-home") != codexHome || command.String("state-dir") != stateDir {
					t.Error("environment paths were not applied")
				}
				return nil
			}
			if err := root.Run(context.Background(), append([]string{"test"}, test.args...)); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("action was not called")
			}
		})
	}
}

func TestCLIFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv("CODEX_HOME", "environment-home")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "environment-state")
	root := newCommand()
	root.Command("refresh").Action = func(_ context.Context, command *cli.Command) error {
		if command.String("codex-home") != "flag-home" || command.String("state-dir") != "flag-state" || command.Duration("interval") != time.Second {
			t.Error("flags did not override defaults and environment")
		}
		return nil
	}
	if err := root.Run(context.Background(), []string{"test", "refresh", "--pane", "pane", "--codex-home", "flag-home", "--state-dir", "flag-state", "--interval", "1s"}); err != nil {
		t.Fatal(err)
	}
}

func TestCLIUsageErrorsReturnWithoutPrinting(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", "")
	for _, args := range [][]string{
		{"refresh"}, {"watch", "--pane", "pane", "--interval", "0"},
		{"event", "--interval", "-1s"}, {"refresh", "--interval", "invalid"},
		{"refresh", "--unknown"}, {"bogus"}, {"refresh", "--pane", "pane", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			root := newCommand()
			root.Writer = &output
			root.ErrWriter = &output
			err := root.Run(context.Background(), append([]string{"test"}, args...))
			var exitCoder cli.ExitCoder
			if !errors.As(err, &exitCoder) || exitCoder.ExitCode() != 2 {
				t.Fatalf("expected usage error with exit code 2, got %v", err)
			}
			if output.Len() != 0 {
				t.Fatalf("CLI printed error before main could handle it: %s", output.String())
			}
		})
	}
}

func TestCLIContextErrorPreservesCause(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "broken")
	err := runCLI(context.Background(), []string{"refresh"})
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("lost JSON syntax error: %v", err)
	}
}

func TestCLIHelpDoesNotRequirePane(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "broken")
	for _, args := range [][]string{{"--help"}, {"refresh", "--help"}, {"watch", "--help"}, {"event", "--help"}, {"help", "watch"}} {
		root := newCommand()
		var output bytes.Buffer
		root.Writer = &output
		root.ErrWriter = &output
		if err := root.Run(context.Background(), append([]string{"test"}, args...)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "USAGE:") {
			t.Fatalf("help not shown: %s", output.String())
		}
		if strings.Contains(output.String(), "herdr-plugin-context") {
			t.Fatal("internal plugin JSON flag leaked into help")
		}
	}
}
