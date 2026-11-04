package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStartupContinuesAfterPaneFailure(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "herdr")
	calls := filepath.Join(directory, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TEST_STARTUP_CALLS"
case "$1 $2" in
 "pane list") printf '%s\n' '{"result":{"panes":[{"pane_id":"bad","agent":"codex"},{"pane_id":"good","agent":"codex"},{"pane_id":"other","agent":"other"}]}}' ;;
 "pane get")
  if [ "$3" = bad ]; then echo "missing pane" >&2; exit 7; fi
  printf '%s\n' '{"result":{"pane":{"agent":"other"}}}' ;;
 *) exit 9 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", binary)
	t.Setenv("TEST_STARTUP_CALLS", calls)
	t.Setenv(pluginContextEnv, "broken")
	err := runCLI(context.Background(), []string{"startup"})
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("startup lost the individual pane failure: %v", err)
	}
	content, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "pane list\npane get bad\npane get good\n" {
		t.Fatalf("startup skipped healthy panes or included non-Codex panes: %s", content)
	}
}

func TestPluginWorkerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the plugin binary")
	}
	binary := filepath.Join(t.TempDir(), "plugin")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, output)
	}
	for _, mode := range []string{"startup", "event"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			home := filepath.Join(directory, "codex home")
			sessions := filepath.Join(home, "sessions")
			if err := os.MkdirAll(sessions, 0700); err != nil {
				t.Fatal(err)
			}
			rollout := filepath.Join(sessions, "rollout-root.jsonl")
			contents := `{"type":"session_meta","payload":{"id":"root"}}` + "\n" +
				`{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n" +
				`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}` + "\n"
			if err := os.WriteFile(rollout, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			fake := filepath.Join(directory, "fake herdr")
			script := `#!/bin/sh
case "$1 $2" in
 "pane list") printf '%s\n' '{"result":{"panes":[{"pane_id":"pane","agent":"codex","agent_status":"working"}]}}' ;;
 "pane get")
  status=working
  if [ -e "$TEST_PLUGIN_DIR/idle" ]; then status=idle; fi
  cost=""
  if [ -e "$TEST_PLUGIN_DIR/cost" ]; then cost=$(cat "$TEST_PLUGIN_DIR/cost"); fi
  printf '{"result":{"pane":{"agent":"codex","agent_status":"%s","tokens":{"cost":"%s"},"agent_session":{"agent":"codex","kind":"id","value":"root"}}}}\n' "$status" "$cost" ;;
 "pane report-metadata")
  for arg in "$@"; do
   case "$arg" in cost=*) printf '%s' "${arg#cost=}" > "$TEST_PLUGIN_DIR/cost"; printf '%s\n' "$arg" >> "$TEST_PLUGIN_DIR/reports" ;; esac
  done ;;
 *) echo "unexpected command: $*" >&2; exit 7 ;;
esac
`
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(directory, "state dir")
			lockPath := filepath.Join(state, fmt.Sprintf("watch-%x.lock", sha256.Sum256([]byte("pane"))))
			t.Setenv("TEST_PLUGIN_DIR", directory)
			t.Setenv("HERDR_BIN_PATH", fake)
			t.Setenv("CODEX_HOME", home)
			t.Setenv("HERDR_PLUGIN_STATE_DIR", state)
			t.Setenv(pluginEventEnv, `{"data":{"pane_id":"pane"}}`)
			released := func() bool {
				lock, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
				if os.IsNotExist(err) {
					return true
				}
				if err != nil {
					return false
				}
				defer lock.Close()
				return unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
			}
			t.Cleanup(func() {
				_ = os.WriteFile(filepath.Join(directory, "idle"), nil, 0600)
				deadline := time.Now().Add(3 * time.Second)
				for !released() && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
			})
			launch := func(mode string) {
				command := exec.Command(binary, mode, "--interval", "10ms")
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("launch %s: %v\n%s", mode, err, output)
				}
			}
			waitFor := func(description string, condition func() bool) {
				deadline := time.NewTimer(5 * time.Second)
				defer deadline.Stop()
				tick := time.NewTicker(5 * time.Millisecond)
				defer tick.Stop()
				for !condition() {
					select {
					case <-deadline.C:
						t.Fatalf("timed out waiting for %s", description)
					case <-tick.C:
					}
				}
			}
			launch(mode)
			waitFor("initial cost", func() bool {
				value, _ := os.ReadFile(filepath.Join(directory, "cost"))
				return string(value) == "$3.00"
			})
			launch("event") // A repeated status event must not create another worker.
			updated := strings.ReplaceAll(strings.ReplaceAll(contents, "1000000", "2000000"), "100000", "200000")
			if err := os.WriteFile(rollout+".tmp", []byte(updated), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(rollout+".tmp", rollout); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "idle"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			waitFor("final cost", func() bool {
				value, _ := os.ReadFile(filepath.Join(directory, "cost"))
				return string(value) == "$6.00"
			})
			waitFor("worker lock release", released)
			reports, err := os.ReadFile(filepath.Join(directory, "reports"))
			if err != nil {
				t.Fatal(err)
			}
			if string(reports) != "cost=$3.00\ncost=$6.00\n" {
				t.Fatalf("duplicate or missing publications: %s", reports)
			}
			logs, err := filepath.Glob(filepath.Join(state, "*.log"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range logs {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(content) != 0 {
					t.Fatalf("worker errors: %s", content)
				}
			}
		})
	}
}
