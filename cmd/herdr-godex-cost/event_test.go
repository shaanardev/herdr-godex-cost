package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventIgnoresNonCodexPane(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "herdr")
	calls := filepath.Join(directory, "calls")
	t.Setenv("TEST_EVENT_CALLS", calls)
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_EVENT_CALLS"
printf '%s\n' '{"result":{"pane":{"agent":"other","agent_status":"working"}}}'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", binary)
	t.Setenv("HERDR_PLUGIN_EVENT_JSON", `{"data":{"pane_id":"event-pane"}}`)
	state := filepath.Join(directory, "state")
	if err := runCLI(context.Background(), []string{"event", "--state-dir", state}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != "pane get event-pane" {
		t.Fatalf("unexpected event calls: %s", content)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("non-Codex event created worker state: %v", err)
	}
}
