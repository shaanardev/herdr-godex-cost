package refresh

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarora/herdr-godex-cost/internal/codex"
)

// Use real indexed edges so cycles and shared descendants don't depend on
// the single-parent representation in rollout metadata.
func treeDatabase(t *testing.T, home string, ids []string, edges [][2]string) *sql.DB {
	t.Helper()
	for _, id := range ids {
		writeRollout(t, home, id, "")
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		"CREATE TABLE threads (id TEXT, rollout_path TEXT)",
		"CREATE TABLE thread_spawn_edges (parent_thread_id TEXT, child_thread_id TEXT)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec("INSERT INTO threads VALUES (?, ?)", id, filepath.Join(home, "sessions", "rollout-"+id+".jsonl")); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range edges {
		if _, err := tx.Exec("INSERT INTO thread_spawn_edges VALUES (?, ?)", edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTreeDiagnostics(t *testing.T) {
	for _, scenario := range []string{"healthy", "missing child", "damaged child", "cycle", "shared descendant", "missing root", "damaged root"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			ids := []string{"root", "child"}
			edges := [][2]string{{"root", "child"}}
			if scenario == "cycle" {
				edges = append(edges, [2]string{"child", "root"})
			}
			if scenario == "shared descendant" {
				ids = append(ids, "other", "shared")
				edges = append(edges, [2]string{"root", "other"}, [2]string{"child", "shared"}, [2]string{"other", "shared"})
			}
			db := treeDatabase(t, home, ids, edges)
			index, err := codex.NewSessionIndex(context.Background(), home, db)
			if err != nil {
				t.Fatal(err)
			}
			resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(home))
			if err != nil {
				t.Fatal(err)
			}
			// Mutate after indexing to exercise errors encountered during resolution.
			target := "child"
			if strings.HasSuffix(scenario, "root") {
				target = "root"
			}
			path := filepath.Join(home, "sessions", "rollout-"+target+".jsonl")
			if strings.HasPrefix(scenario, "missing") {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(scenario, "damaged") {
				if err := os.WriteFile(path, []byte("broken\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			resolution, err := resolver.Resolve(PaneSessionInput{KnownSessionID: "root"})
			if target == "root" {
				if err == nil || resolution != nil {
					t.Fatalf("root failure returned partial success: %+v, %v", resolution, err)
				}
				if scenario == "missing root" && !errors.Is(err, ErrSessionNotFound) {
					t.Fatalf("lost missing-root error: %v", err)
				}
				if scenario == "damaged root" {
					var syntax *json.SyntaxError
					if !errors.As(err, &syntax) {
						t.Fatalf("lost syntax error: %v", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "healthy" {
				if resolution.Incomplete() || len(resolution.Diagnostics) != 0 || len(resolution.Sessions) != 2 {
					t.Fatalf("healthy tree: %+v", resolution)
				}
				return
			}
			if !resolution.Incomplete() || len(resolution.Diagnostics) != 1 {
				t.Fatalf("incomplete tree must explain its failure: %+v", resolution)
			}
			diagnostic := resolution.Diagnostics[0]
			switch scenario {
			case "missing child":
				if !errors.Is(diagnostic, ErrSessionNotFound) || !strings.Contains(diagnostic.Error(), `"child"`) {
					t.Fatalf("missing child diagnostic: %v", diagnostic)
				}
			case "damaged child":
				var syntax *json.SyntaxError
				if !errors.As(diagnostic, &syntax) {
					t.Fatalf("lost child syntax error: %v", diagnostic)
				}
			case "cycle", "shared descendant":
				if !strings.Contains(diagnostic.Error(), "reached more than once") {
					t.Fatalf("repeated session diagnostic: %v", diagnostic)
				}
			}
			seen := make(map[string]bool)
			for _, session := range resolution.Sessions {
				if seen[session.ThreadID] {
					t.Fatalf("session counted twice: %s", session.ThreadID)
				}
				seen[session.ThreadID] = true
			}
		})
	}
}

func TestTreeTraversalLimit(t *testing.T) {
	for _, count := range []int{maxResolvedSessions, maxResolvedSessions + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			home := t.TempDir()
			ids := []string{"root"}
			var edges [][2]string
			for i := 1; i < count; i++ {
				id := fmt.Sprintf("child-%03d", i)
				edges = append(edges, [2]string{ids[len(ids)-1], id})
				ids = append(ids, id)
			}
			db := treeDatabase(t, home, ids, edges)
			index, err := codex.NewSessionIndex(context.Background(), home, db)
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
			if len(resolution.Sessions) != maxResolvedSessions {
				t.Fatalf("resolved %d sessions", len(resolution.Sessions))
			}
			if count == maxResolvedSessions {
				if resolution.Incomplete() || len(resolution.Diagnostics) != 0 {
					t.Fatalf("exactly at limit should be complete: %+v", resolution)
				}
			} else {
				if !resolution.Incomplete() || len(resolution.Diagnostics) != 1 || !strings.Contains(resolution.Diagnostics[0].Error(), "limit of 512 sessions") {
					t.Fatalf("missing limit diagnostic: %+v", resolution)
				}
			}
		})
	}
}

func TestResolutionDiagnosticsLogChangesAndRecovery(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	runner := NewRunner(nil, "", "", 0)
	runner.lastDiagnostics = make(map[string]string)
	warning := []error{fmt.Errorf("child rollout: %w", ErrSessionNotFound)}
	runner.logDiagnostics("pane", "resolve session tree", warning)
	runner.logDiagnostics("pane", "resolve session tree", warning)
	if got := strings.Count(output.String(), "level=WARN"); got != 1 {
		t.Fatalf("unchanged warnings logged %d times", got)
	}
	runner.logDiagnostics("pane", "resolve session tree", []error{errors.New("different problem")})
	runner.logDiagnostics("pane", "resolve session tree", nil)
	if len(runner.lastDiagnostics) != 0 {
		t.Fatal("recovery did not clear remembered warnings")
	}
	runner.logDiagnostics("pane", "resolve session tree", warning)
	if got := strings.Count(output.String(), "level=WARN"); got != 3 {
		t.Fatalf("changed or recurring warnings lost: %s", output.String())
	}
}

func TestResolveTreeReusesVerifiedRoot(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	index, err := codex.NewSessionIndex(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	files := codex.NewRolloutFiles(home)
	root, err := files.FindByID("root")
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSessionResolver(index, files)
	if err != nil {
		t.Fatal(err)
	}
	// The root has already been verified for this operation. Tree construction
	// must use that result rather than reopen it. Subsequent resolutions must
	// still read fresh metadata.
	if err := os.WriteFile(root.Path, []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.resolveTree(root, ResolutionFromReportedID)
	if err != nil || resolution == nil || resolution.Incomplete() || len(resolution.Sessions) != 1 {
		t.Fatalf("verified root was reread: %+v %v", resolution, err)
	}
	_, err = resolver.Resolve(PaneSessionInput{KnownSessionID: "root"})
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("later resolution reused stale metadata: %v", err)
	}
}

func TestResolveBatchPrefixCandidates(t *testing.T) {
	home := t.TempDir()
	const first = "12345678-1234-1234-1234-123456789abc"
	const second = "abcdefab-1234-1234-1234-123456789abc"
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
	candidates := []sessionIDCandidate{{Prefix: "12345678"}, {Prefix: "abcdefab"}, {ID: first}}
	matches, err := resolver.matchesFromCandidates(candidates, "/work")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Metadata.ID != first || matches[1].Metadata.ID != second {
		t.Fatalf("batch candidates not deduplicated and sorted: %+v", matches)
	}
	// Prefix results must not bypass an ambiguous indexed ID.
	directory := filepath.Join(home, "archived_sessions")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(home, "sessions", "rollout-"+first+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "rollout-"+first+".jsonl"), contents, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = resolver.matchesFromCandidates(candidates, "/work")
	if !errors.Is(err, ErrAmbiguousSession) {
		t.Fatalf("ambiguous prefix became a match: %v", err)
	}
	// Even if one duplicate disappears during the pass, a uniquely scanned
	// prefix must not override the ambiguity already recorded by the index.
	ambiguousIndex, err := codex.NewSessionIndex(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "rollout-"+first+".jsonl")); err != nil {
		t.Fatal(err)
	}
	resolver, err = NewSessionResolver(ambiguousIndex, codex.NewRolloutFiles(home))
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.matchesFromCandidates(candidates[:2], "/work")
	if !errors.Is(err, ErrAmbiguousSession) {
		t.Fatalf("prefix bypassed indexed ambiguity: %v", err)
	}
}
