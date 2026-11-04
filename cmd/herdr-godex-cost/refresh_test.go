package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarora/herdr-godex-cost/internal/driver"
)

func TestRefreshAction(t *testing.T) {
	for _, test := range []struct {
		name                           string
		args                           []string
		pluginJSON                     string
		agent, sessionID, existingCost string
		wantPane, wantToken            string
		wantError                      error
	}{
		{"explicit pane overrides context", []string{"refresh", "--pane", "explicit"}, "broken", "codex", "root", "", "explicit", "cost=$6.00", nil},
		{"focused pane from context", []string{"refresh"}, `{"focused_pane_id":"focused","pane_id":"fallback"}`, "codex", "root", "", "focused", "cost=$6.00", nil},
		{"default refresh", nil, `{"pane_id":"default"}`, "codex", "root", "", "default", "cost=$6.00", nil},
		{"unchanged cost", []string{"refresh", "--pane", "same"}, "", "codex", "root", "$6.00", "same", "", nil},
		{"non-Codex pane", []string{"refresh", "--pane", "other"}, "", "other", "", "", "other", "", nil},
		{"unresolved session", []string{"refresh", "--pane", "unknown"}, "", "codex", "missing", "", "unknown", "cost=session?", driver.ErrSessionNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			home := filepath.Join(directory, "codex")
			sessions := filepath.Join(home, "sessions")
			if err := os.MkdirAll(sessions, 0700); err != nil {
				t.Fatal(err)
			}
			for _, session := range []struct{ id, parent string }{{"root", ""}, {"child", "root"}} {
				contents := `{"type":"session_meta","payload":{"id":"` + session.id + `","parent_thread_id":"` + session.parent + `"}}` + "\n" +
					`{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n" +
					`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}` + "\n"
				if err := os.WriteFile(filepath.Join(sessions, "rollout-"+session.id+".jsonl"), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reply, err := json.Marshal(map[string]any{"result": map[string]any{"pane": map[string]any{
				"agent": test.agent, "tokens": map[string]string{"cost": test.existingCost},
				"agent_session": map[string]string{"agent": "codex", "kind": "id", "value": test.sessionID},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(directory, "herdr")
			calls := filepath.Join(directory, "calls")
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_REFRESH_CALLS"
case "$1 $2" in
 "pane get") printf '%s\n' "$TEST_REFRESH_REPLY" ;;
 "pane report-metadata"|"pane read") ;;
 *) echo "unexpected command: $*" >&2; exit 1 ;;
esac
`
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_BIN_PATH", binary)
			t.Setenv("TEST_REFRESH_CALLS", calls)
			t.Setenv("TEST_REFRESH_REPLY", string(reply))
			t.Setenv("CODEX_HOME", home)
			state := filepath.Join(directory, "state")
			t.Setenv("HERDR_PLUGIN_STATE_DIR", state)
			t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", test.pluginJSON)
			err = runCLI(context.Background(), test.args)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("got %v; want %v", err, test.wantError)
			}
			if err != nil && !strings.Contains(err.Error(), `refresh pane "`+test.wantPane+`"`) {
				t.Fatalf("error lacks action and pane context: %v", err)
			}
			content, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(content)), "\n")
			if lines[0] != "pane get "+test.wantPane {
				t.Fatalf("wrong target pane: %s", content)
			}
			if strings.Count(string(content), "pane get ") != 1 {
				t.Fatalf("refresh fetched pane more than once: %s", content)
			}
			publications := 0
			for _, line := range lines {
				if strings.HasPrefix(line, "pane report-metadata ") {
					publications++
					if test.wantToken == "" || !strings.HasPrefix(line, "pane report-metadata "+test.wantPane+" ") || !strings.Contains(line, "--token "+test.wantToken+" ") {
						t.Fatalf("unexpected publication: %s", line)
					}
				}
			}
			wantPublications := 0
			if test.wantToken != "" {
				wantPublications = 1
			}
			if publications != wantPublications {
				t.Fatalf("got %d publications; want %d: %s", publications, wantPublications, content)
			}
			locks, err := filepath.Glob(filepath.Join(state, "*.lock"))
			if err != nil {
				t.Fatal(err)
			}
			if len(locks) != 0 {
				t.Fatalf("one-shot refresh created worker locks: %v", locks)
			}
		})
	}
}

func TestRefreshErrorPreservesCause(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'pane disappeared' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", binary)
	err := runCLI(context.Background(), []string{"refresh", "--pane", "pane"})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "pane disappeared") || !strings.Contains(err.Error(), `refresh pane "pane"`) {
		t.Fatalf("lost refresh failure details: %v", err)
	}
}

func TestCancelledRefreshDoesNotInvokeHerdr(t *testing.T) {
	directory := t.TempDir()
	called := filepath.Join(directory, "called")
	binary := filepath.Join(directory, "herdr")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch \"$TEST_REFRESH_CALLED\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_REFRESH_CALLED", called)
	t.Setenv("HERDR_BIN_PATH", binary)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCLI(ctx, []string{"refresh", "--pane", "pane"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatalf("cancelled refresh invoked Herdr: %v", err)
	}
}
