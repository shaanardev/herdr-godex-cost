package refresh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/herder"
)

// The fake exposes each subprocess call and appends final usage when idle is
// observed. Its token file mimics Herdr retaining the last published value.
func watchHerdr(t *testing.T, home, firstStatus, nextAgent string) (*herder.Client, string) {
	t.Helper()
	directory := t.TempDir()
	t.Setenv("WATCH_TEST_DIR", directory)
	t.Setenv("WATCH_TEST_ROLLOUT", filepath.Join(home, "sessions", "rollout-root.jsonl"))
	t.Setenv("WATCH_TEST_FIRST_STATUS", firstStatus)
	t.Setenv("WATCH_TEST_NEXT_AGENT", nextAgent)
	script := `#!/bin/sh
printf '%s\n' "$1 $2" >> "$WATCH_TEST_DIR/calls"
case "$1 $2" in
 "pane get")
  agent=codex
  status="$WATCH_TEST_FIRST_STATUS"
  if [ -e "$WATCH_TEST_DIR/seen" ]; then
   touch "$WATCH_TEST_DIR/second-read"
   agent="$WATCH_TEST_NEXT_AGENT"
   status=idle
   printf '%s\n' '{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2000000,"output_tokens":200000}}}}' >> "$WATCH_TEST_ROLLOUT"
  fi
  touch "$WATCH_TEST_DIR/seen"
  cost=""
  if [ -e "$WATCH_TEST_DIR/cost" ]; then cost=$(cat "$WATCH_TEST_DIR/cost"); fi
  printf '{"result":{"pane":{"agent":"%s","agent_status":"%s","tokens":{"cost":"%s"},"agent_session":{"agent":"codex","kind":"id","value":"root"}}}}\n' "$agent" "$status" "$cost"
  ;;
 "pane report-metadata")
  if [ -n "$WATCH_TEST_PUBLISH_DELAY" ] && [ ! -e "$WATCH_TEST_DIR/first-publish" ]; then
   sleep "$WATCH_TEST_PUBLISH_DELAY"
  fi
  for argument in "$@"; do
   case "$argument" in cost=*) printf '%s' "${argument#cost=}" > "$WATCH_TEST_DIR/cost"; printf '%s\n' "$argument" >> "$WATCH_TEST_DIR/reports" ;; esac
  done
  if [ ! -e "$WATCH_TEST_DIR/first-publish" ]; then touch "$WATCH_TEST_DIR/first-publish"; fi
  ;;
 *) echo "unexpected command: $*" >&2; exit 1 ;;
esac
`
	executable := filepath.Join(directory, "herdr")
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return herder.NewClient(executable), directory
}

func TestWatchReadsPaneOncePerPass(t *testing.T) {
	for _, test := range []struct {
		name, status, nextAgent string
		reads                   int
		reports                 string
	}{
		{"final idle usage", "working", "codex", 2, "cost=$3.00\ncost=$6.00\n"},
		{"already idle", "idle", "codex", 1, "cost=$3.00\n"},
		{"agent changed", "working", "other", 2, "cost=$3.00\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			writeRollout(t, home, "root", "")
			client, directory := watchHerdr(t, home, test.status, test.nextAgent)
			runner := NewRunner(client, home, t.TempDir(), time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := runner.Watch(ctx, "pane"); err != nil {
				t.Fatal(err)
			}
			calls, err := os.ReadFile(filepath.Join(directory, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(calls), "pane get\n"); got != test.reads {
				t.Fatalf("got %d pane reads, want %d: %s", got, test.reads, calls)
			}
			reports, err := os.ReadFile(filepath.Join(directory, "reports"))
			if err != nil {
				t.Fatal(err)
			}
			if string(reports) != test.reports {
				t.Fatalf("reports %q; want %q", reports, test.reports)
			}
		})
	}
}

func TestWatchCancellationInterruptsWait(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	client, directory := watchHerdr(t, home, "working", "codex")
	runner := NewRunner(client, home, t.TempDir(), time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Watch(ctx, "pane") }()
	// Let Watch publish its first update before cancelling.
	// Bound the test so a stuck refresh or shutdown cannot hang it.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		if _, err := os.Stat(filepath.Join(directory, "reports")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("watch exited before publishing: %v", err)
		case <-deadline.C:
			t.Fatal("initial refresh did not finish")
		case <-poll.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-deadline.C:
		t.Fatal("watch did not cancel its wait")
	}
	calls, err := os.ReadFile(filepath.Join(directory, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(calls), "pane get\n"); got != 1 {
		t.Fatalf("unexpected pane reads: %s", calls)
	}
}

func TestWatchWaitsFullIntervalAfterSlowRefresh(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	client, directory := watchHerdr(t, home, "working", "codex")
	// Make the refresh longer than the interval. A fixed-cadence ticker would
	// have a pending tick and start the next pass immediately after publishing.
	t.Setenv("WATCH_TEST_PUBLISH_DELAY", "0.1")
	const interval = 40 * time.Millisecond
	runner := NewRunner(client, home, t.TempDir(), interval)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Watch(ctx, "pane"); err != nil {
		t.Fatal(err)
	}
	published, err := os.Stat(filepath.Join(directory, "first-publish"))
	if err != nil {
		t.Fatal(err)
	}
	nextRead, err := os.Stat(filepath.Join(directory, "second-read"))
	if err != nil {
		t.Fatal(err)
	}
	// File timestamps use wall-clock time, unlike Go timers. Allow 1ms for
	// timestamp precision and clock adjustments when comparing the two.
	if elapsed := nextRead.ModTime().Sub(published.ModTime()); elapsed < interval-time.Millisecond {
		t.Fatalf("next pass started %s after publishing; want at least %s", elapsed, interval)
	}
}
