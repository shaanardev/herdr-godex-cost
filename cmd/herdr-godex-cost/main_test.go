package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidCommands(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
	for _, args := range [][]string{{"refresh"}, {"watch", "-pane", "p", "-interval", "0"}, {"bogus"}, {"refresh", "-pane", "p", "extra"}} {
		if err := runCLI(context.Background(), args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestEventWithoutPane(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", "{}")
	if err := runCLI(context.Background(), []string{"event"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", "broken")
	if err := runCLI(context.Background(), []string{"event"}); err == nil {
		t.Fatal("accepted broken event")
	}
}

// Exercise the real main in a subprocess so exit codes and duplicate error
// output can be checked without terminating the test runner.
func TestMainProcess(t *testing.T) {
	if os.Getenv("HERDR_CLI_MAIN_TEST") == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"herdr-godex-cost"}, os.Args[i+1:]...)
				break
			}
		}
		main()
		os.Exit(0)
	}
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "missing-herdr"))
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{"help", []string{"--help"}, 0},
		{"usage", []string{"refresh", "--unknown"}, 2},
		{"operational", []string{"refresh", "--pane", "pane"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"-test.run=^TestMainProcess$", "--"}, test.args...)
			command := exec.Command(os.Args[0], args...)
			command.Env = append(os.Environ(), "HERDR_CLI_MAIN_TEST=1")
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != test.code {
				t.Fatalf("exit=%d, want %d; output: %s", code, test.code, output)
			}
			if code != 0 && strings.Count(string(output), "codex cost") != 1 {
				t.Fatalf("error was not reported exactly once: %s", output)
			}
		})
	}
}
