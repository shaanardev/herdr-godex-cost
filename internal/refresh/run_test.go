package refresh

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/codex"
	"github.com/sarora/herdr-godex-cost/internal/herder"
)

func writeRollout(t *testing.T, home, id, parent string) {
	t.Helper()
	directory := filepath.Join(home, "sessions")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"session_meta","payload":{"id":"` + id + `","parent_thread_id":"` + parent + `","cwd":"/work"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n" +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, "rollout-"+id+".jsonl"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshPublishesTreeAndUsesCache(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	writeRollout(t, home, "root", "")
	writeRollout(t, home, "child", "root")
	executable := filepath.Join(t.TempDir(), "herdr")
	reports := filepath.Join(t.TempDir(), "reports")
	t.Setenv("TEST_REPORTS", reports)
	script := `#!/bin/sh
case "$1 $2" in
 "pane get") printf '%s\n' '{"result":{"pane":{"pane_id":"pane1","agent":"codex","agent_status":"idle","agent_session":{"agent":"codex","kind":"id","value":"root"}}}}' ;;
 "pane report-metadata") printf '%s\n' "$*" >> "$TEST_REPORTS" ;;
 *) echo "unexpected command: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(herder.NewClient(executable), home, state, time.Second)
	for range 2 {
		if err := runner.Refresh(context.Background(), "pane1"); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(reports)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "cost=$6.00") != 2 {
		t.Fatalf("unexpected reports: %s", content)
	}
	files, err := filepath.Glob(filepath.Join(state, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cache files %v: %v", files, err)
	}
}

func TestResolutionAndPathErrors(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	writeRollout(t, home, "child", "root")
	index, err := codex.NewSessionIndex(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(home))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.Resolve(PaneSessionInput{KnownSessionID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Sessions) != 2 || resolution.Incomplete() {
		t.Fatalf("unexpected tree: %+v", resolution)
	}
	_, err = resolver.Resolve(PaneSessionInput{CWD: "/work", TerminalTitle: "Codex"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("title without an exact indexed name must not match: %v", err)
	}
	_, err = codex.ResolveRolloutPath(filepath.Join(home, "sessions", "missing.jsonl"), home)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost missing-file cause: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "sessions", "escape.jsonl")); err != nil {
		t.Fatal(err)
	}
	_, err = codex.ResolveRolloutPath("sessions/escape.jsonl", home)
	if !errors.Is(err, codex.ErrUnsafeRolloutPath) {
		t.Fatalf("accepted escaping symlink: %v", err)
	}
}

func TestCacheMissAndReplacement(t *testing.T) {
	cache := NewCache(filepath.Join(t.TempDir(), "cost.json"), "root", "one")
	if _, err := cache.Read(); !errors.Is(err, ErrCacheMiss) {
		t.Fatal(err)
	}
	value := *codex.NewEstimate(false)
	value.KnownUSD = 3
	value.HasUsage = true
	if err := cache.WriteAtomic(value); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Read()
	if err != nil || got == nil || *got != value {
		t.Fatalf("got %+v: %v", got, err)
	}
	cache.Fingerprint = "two"
	if _, err := cache.Read(); !errors.Is(err, ErrCacheMiss) {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.Path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Read(); !errors.Is(err, ErrCacheMiss) {
		t.Fatal(err)
	}
}

func TestMissingChildIsPartial(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	writeRollout(t, home, "child", "root")
	index, err := codex.NewSessionIndex(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "sessions", "rollout-child.jsonl")); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(home))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.Resolve(PaneSessionInput{KnownSessionID: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if !resolution.Incomplete() || len(resolution.Sessions) != 1 {
		t.Fatalf("missing child looked complete: %+v", resolution)
	}
}

func TestIncompleteResolutionDoesNotReuseCompleteCache(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	db := treeDatabase(t, home, []string{"root", "child"}, [][2]string{{"root", "child"}})
	executable := filepath.Join(t.TempDir(), "herdr")
	reports := filepath.Join(t.TempDir(), "reports")
	t.Setenv("TEST_REPORTS", reports)
	script := `#!/bin/sh
case "$1 $2" in
 "pane get") printf '%s\n' '{"result":{"pane":{"agent":"codex","agent_session":{"agent":"codex","kind":"id","value":"root"}}}}' ;;
 "pane report-metadata") printf '%s\n' "$*" >> "$TEST_REPORTS" ;;
 *) exit 1 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(herder.NewClient(executable), home, state, 0)
	if err := runner.Refresh(context.Background(), "pane"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(state, "*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cache files %v: %v", files, err)
	}
	before, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	// A cycle changes completeness without changing the visited IDs, rollout
	// paths, or stat data used in the fingerprint.
	if _, err := db.Exec("INSERT INTO thread_spawn_edges VALUES ('child', 'root')"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Refresh(context.Background(), "pane"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(reports)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "cost=~$6.00") {
		t.Fatalf("cached total concealed incomplete resolution: %s", content)
	}
	after, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("incomplete estimate overwrote complete cache")
	}
}
