package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDatabaseIndex(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "sessions", "rollout-root.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"root","cwd":"/work"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		"CREATE TABLE threads (id TEXT, rollout_path TEXT)",
		"CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT)",
		"INSERT INTO thread_spawn_edges VALUES ('root','missing-child')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("INSERT INTO threads VALUES (?, ?)", "root", path); err != nil {
		t.Fatal(err)
	}
	index, err := NewSessionIndex(context.Background(), db, home)
	if err != nil {
		t.Fatal(err)
	}
	got, err := index.RolloutPath("root")
	if err != nil || got != path {
		t.Fatalf("path %q: %v", got, err)
	}
	children := index.ChildrenOf("root")
	if len(children) != 1 || children[0] != "missing-child" {
		t.Fatalf("lost indexed child: %v", children)
	}
	if ids := index.SessionIDsByCWD("/work"); len(ids) != 1 || ids[0] != "root" {
		t.Fatalf("cwd matches: %v", ids)
	}
}
