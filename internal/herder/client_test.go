package herder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandErrors(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho 'pane disappeared' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client := NewClient(executable)
	_, err := client.ReadPaneInfo(context.Background(), "p")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "pane disappeared") {
		t.Fatalf("lost command failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ReadPaneInfo(ctx, "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestMissingResult(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s' '{\"error\":\"unknown pane\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(executable).ReadPaneInfo(context.Background(), "p"); err == nil {
		t.Fatal("accepted error envelope as empty pane")
	}
}
