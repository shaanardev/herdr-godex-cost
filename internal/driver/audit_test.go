package driver

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/codex"
	"github.com/sarora/herdr-godex-cost/internal/herder"
)

func TestVisibleSessionResolutionRequiresCWDAndDetectsAdjacentIDs(t *testing.T) {
	const first = "12345678-1234-1234-1234-123456789abc"
	const second = "abcdefab-1234-1234-1234-123456789abc"
	home := t.TempDir()
	writeRollout(t, home, first, "")
	writeRollout(t, home, second, "")
	index, err := codex.NewSessionIndex(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text, cwd string
		want      error
	}{
		{first, "", ErrSessionNotFound}, {first, "/different", ErrSessionNotFound},
		{first + " " + second, "/work", ErrAmbiguousSession},
		{"a" + first, "/work", ErrSessionNotFound},
		{"ID: " + first, "/work", nil},
		{"ID: 12345678-1234-1234-1234-…", "/work", nil},
	} {
		resolution, err := resolver.Resolve(context.Background(), PaneSessionInput{TerminalText: test.text, CWD: test.cwd})
		if !errors.Is(err, test.want) {
			t.Fatalf("text=%q cwd=%q: %v; want %v", test.text, test.cwd, err, test.want)
		}
		if err == nil && resolution.RootThreadID != first {
			t.Fatalf("wrong visible association: %+v", resolution)
		}
	}
}

func TestTerminalTitleMatchesExactIndexedName(t *testing.T) {
	home := t.TempDir()
	db := treeDatabase(t, home, []string{"root", "other"}, nil)
	for _, query := range []string{"ALTER TABLE threads ADD COLUMN name TEXT", "ALTER TABLE threads ADD COLUMN cwd TEXT", "UPDATE threads SET cwd='/work'", "UPDATE threads SET name='Audit queue' WHERE id='root'", "UPDATE threads SET name='Different session' WHERE id='other'"} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	index, err := codex.NewSessionIndex(context.Background(), home, db)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(home))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.Resolve(context.Background(), PaneSessionInput{CWD: "/work", TerminalTitle: "Audit queue | work"})
	if err != nil || resolution.RootThreadID != "root" {
		t.Fatalf("title fallback: %+v %v", resolution, err)
	}
	for _, title := range []string{"Codex", "Unknown | work", "Audit queue | different"} {
		if _, err := resolver.Resolve(context.Background(), PaneSessionInput{CWD: "/work", TerminalTitle: title}); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("guessed from title %q: %v", title, err)
		}
	}
}

func TestSessionIndexCombinesDatabasesAndFallsBackFromCorruption(t *testing.T) {
	home := t.TempDir()
	treeDatabase(t, home, []string{"root"}, nil)
	writeRollout(t, home, "child", "")
	path := filepath.Join(home, "state_2.sqlite")
	second, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, query := range []string{"CREATE TABLE threads (id TEXT, rollout_path TEXT)", "CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT)", "INSERT INTO thread_spawn_edges VALUES ('root','child')"} {
		if _, err := second.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := second.Exec("INSERT INTO threads VALUES ('child',?)", filepath.Join(home, "sessions", "rollout-child.jsonl")); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(nil, home, "", 0)
	index, err := runner.sessionIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	children := index.ChildrenOf("root")
	if len(children) != 1 || children[0] != "child" {
		t.Fatalf("lost cross-database edge: %v", children)
	}
	if _, err := index.RolloutPath("root"); err != nil {
		t.Fatalf("lost older database root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "state_3.sqlite"), []byte("broken database"), 0600); err != nil {
		t.Fatal(err)
	}
	index, err = runner.sessionIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Diagnostics) == 0 {
		t.Fatal("corrupt database failure was swallowed")
	}
	if _, err := index.RolloutPath("root"); err != nil {
		t.Fatal(err)
	}
}

func TestRestartPreservesPublishedCost(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	path := filepath.Join(home, "sessions", "rollout-root.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"root","cwd":"/work"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
case "$1 $2" in
 "pane get") printf '%s\n' '{"result":{"pane":{"agent":"codex","tokens":{"cost":"$9.00"},"agent_session":{"agent":"codex","kind":"id","value":"root"}}}}' ;;
 *) echo "unexpected metadata overwrite" >&2; exit 7 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(herder.NewClient(binary), home, t.TempDir(), 0)
	if err := runner.Refresh(context.Background(), "pane"); err != nil {
		t.Fatalf("empty restarted rollout overwrote existing cost: %v", err)
	}
}

func TestPrefixAmbiguityIsScopedToPaneCWD(t *testing.T) {
	home := t.TempDir()
	first := "12345678-1234-1234-1234-123456789abc"
	second := "12345678-1234-1234-1234-123456789def"
	writeRollout(t, home, first, "")
	writeRollout(t, home, second, "")
	path := filepath.Join(home, "sessions", "rollout-"+second+".jsonl")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(contents), "/work", "/other")), 0600); err != nil {
		t.Fatal(err)
	}
	index, err := codex.NewSessionIndex(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(home))
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.Resolve(context.Background(), PaneSessionInput{CWD: "/work", TerminalText: "12345678-1234-1234-1234-…"})
	if err != nil || result.RootThreadID != first {
		t.Fatalf("unrelated cwd made a valid prefix ambiguous: %+v %v", result, err)
	}
}

func TestWatchLogsRefreshFailureAgainOnlyAfterRecovery(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	directory := t.TempDir()
	binary := filepath.Join(directory, "herdr")
	script := `#!/bin/sh
case "$1 $2" in
 "pane get")
  n=0
  if [ -e "$TEST_WATCH_ERRORS/count" ]; then n=$(cat "$TEST_WATCH_ERRORS/count"); fi
  n=$((n+1));printf '%s' "$n" > "$TEST_WATCH_ERRORS/count"
  agent=codex;session=missing
  if [ "$n" -eq 3 ]; then session=root; fi
  if [ "$n" -ge 5 ]; then agent=other; fi
  printf '{"result":{"pane":{"agent":"%s","agent_status":"working","tokens":{"cost":"$3.00"},"agent_session":{"agent":"codex","kind":"id","value":"%s"}}}}\n' "$agent" "$session" ;;
 "pane read"|"pane report-metadata") ;;
 *) exit 9 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_WATCH_ERRORS", directory)
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	runner := NewRunner(herder.NewClient(binary), home, t.TempDir(), time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runner.Watch(ctx, "pane"); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(output.String(), `msg="refresh pane"`); count != 2 {
		t.Fatalf("expected initial and recurring warnings, got %d: %s", count, output.String())
	}
}
