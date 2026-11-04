package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeMetadata(t *testing.T, home, relativePath, id string) string {
	t.Helper()
	path := filepath.Join(home, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(rolloutEvent{Type: "session_meta", Payload: json.RawMessage(`{"id":"` + id + `","cwd":"/project"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRolloutFilesFindVerifiedRollouts(t *testing.T) {
	const id = "12345678-1234-1234-1234-123456789abc"
	for _, root := range []string{"sessions", "archived_sessions"} {
		t.Run(root, func(t *testing.T) {
			home := t.TempDir()
			path := writeMetadata(t, home, filepath.Join(root, "nested", "rollout-"+id+".jsonl"), id)
			files := NewRolloutFiles(home)
			exact, err := files.FindByID(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			prefix, err := files.FindByPrefix(context.Background(), "12345678-1234")
			if err != nil {
				t.Fatal(err)
			}
			if exact == nil || prefix == nil || *exact != *prefix || exact.Path != path || exact.Metadata.ID != id || exact.Metadata.CWD != "/project" {
				t.Fatalf("unexpected verified rollouts: %+v %+v", exact, prefix)
			}
		})
	}
}

func TestRolloutFilesBatchPrefixes(t *testing.T) {
	home := t.TempDir()
	first := writeMetadata(t, home, "sessions/rollout-first.jsonl", "12345678-1234-1234-1234-123456789abc")
	second := writeMetadata(t, home, "archived_sessions/rollout-second.jsonl", "abcdefab-1234-1234-1234-123456789abc")
	files := NewRolloutFiles(home)
	matches, err := files.FindByPrefixes(context.Background(), []string{"12345678", "abcdefab", "12345678", "deadbeef"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches["12345678"].Path != first || matches["abcdefab"].Path != second {
		t.Fatalf("batch results: %+v", matches)
	}
	if _, err := files.FindByPrefix(context.Background(), "deadbeef"); !errors.Is(err, ErrRolloutNotFound) {
		t.Fatalf("missing prefix: %v", err)
	}
	for _, prefix := range []string{"", "1234", "not-a-uuid"} {
		if _, err := files.FindByPrefix(context.Background(), prefix); err == nil {
			t.Errorf("accepted invalid prefix %q", prefix)
		}
	}
	// An empty batch does not touch the filesystem.
	empty, err := NewRolloutFiles("/does/not/exist").FindByPrefixes(context.Background(), nil, "")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty batch: %+v %v", empty, err)
	}
}

func TestRolloutFilesRejectAmbiguousMatches(t *testing.T) {
	const id = "12345678-1234-1234-1234-123456789abc"
	home := t.TempDir()
	writeMetadata(t, home, "sessions/rollout-"+id+".jsonl", id)
	writeMetadata(t, home, "archived_sessions/rollout-"+id+".jsonl", id)
	files := NewRolloutFiles(home)
	if _, err := files.FindByID(context.Background(), id); !errors.Is(err, ErrAmbiguousRollout) {
		t.Fatalf("duplicate ID: %v", err)
	}
	if _, err := files.FindByPrefixes(context.Background(), []string{"12345678", "deadbeef"}, ""); !errors.Is(err, ErrAmbiguousRollout) {
		t.Fatalf("duplicate prefix: %v", err)
	}
}

func TestRolloutFilesVerifyMetadataAndFileType(t *testing.T) {
	home := t.TempDir()
	files := NewRolloutFiles(home)
	// A filename alone cannot establish a session association.
	writeMetadata(t, home, "sessions/rollout-requested.jsonl", "different")
	if _, err := files.FindByID(context.Background(), "requested"); !errors.Is(err, ErrRolloutNotFound) {
		t.Fatalf("accepted mismatched metadata: %v", err)
	}
	path := writeMetadata(t, home, "sessions/rollout-broken.jsonl", "broken")
	if err := os.WriteFile(path, []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := files.FindByID(context.Background(), "broken")
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("lost metadata decoding failure: %v", err)
	}
	directory := filepath.Join(home, "sessions", "rollout-directory.jsonl")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(context.Background(), directory); !errors.Is(err, ErrRolloutNotFound) {
		t.Fatalf("accepted directory: %v", err)
	}
	outside := writeMetadata(t, t.TempDir(), "outside.jsonl", "outside")
	if err := os.Symlink(outside, filepath.Join(home, "sessions", "rollout-outside.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(context.Background(), "sessions/rollout-outside.jsonl"); !errors.Is(err, ErrUnsafeRolloutPath) {
		t.Fatalf("accepted escaping symlink: %v", err)
	}
}

func TestRolloutLookupErrorsReturnNil(t *testing.T) {
	const id = "12345678-1234-1234-1234-123456789abc"
	home := t.TempDir()
	writeMetadata(t, home, "sessions/rollout-"+id+".jsonl", id)
	writeMetadata(t, home, "archived_sessions/rollout-"+id+".jsonl", id)
	files := NewRolloutFiles(home)
	for _, test := range []struct {
		name   string
		lookup func() (*Rollout, error)
		want   error
	}{
		{"missing ID", func() (*Rollout, error) { return files.FindByID(context.Background(), "missing") }, ErrRolloutNotFound},
		{"ambiguous ID", func() (*Rollout, error) { return files.FindByID(context.Background(), id) }, ErrAmbiguousRollout},
		{"missing prefix", func() (*Rollout, error) { return files.FindByPrefix(context.Background(), "deadbeef") }, ErrRolloutNotFound},
		{"ambiguous prefix", func() (*Rollout, error) { return files.FindByPrefix(context.Background(), "12345678") }, ErrAmbiguousRollout},
		{"invalid prefix", func() (*Rollout, error) { return files.FindByPrefix(context.Background(), "short") }, nil},
		{"missing file", func() (*Rollout, error) { return files.Read(context.Background(), "sessions/missing.jsonl") }, os.ErrNotExist},
	} {
		t.Run(test.name, func(t *testing.T) {
			rollout, err := test.lookup()
			if rollout != nil || err == nil {
				t.Fatalf("expected nil rollout and error, got %+v, %v", rollout, err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("got %v; want %v", err, test.want)
			}
		})
	}
}

func TestPrefixAliasesShareVerifiedRollout(t *testing.T) {
	home := t.TempDir()
	const id = "12345678-1234-1234-1234-123456789abc"
	writeMetadata(t, home, "sessions/rollout-"+id+".jsonl", id)
	matches, err := NewRolloutFiles(home).FindByPrefixes(context.Background(), []string{"12345678", "12345678-1234"}, "")
	if err != nil {
		t.Fatal(err)
	}
	short, long := matches["12345678"], matches["12345678-1234"]
	if short == nil || long == nil || short != long {
		t.Fatal("prefixes for the same file should share the verified rollout")
	}
	if short.Metadata.ID != id {
		t.Fatalf("wrong shared rollout: %+v", short)
	}
}

func TestRolloutLookupHonorsExpiredContext(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	files := NewRolloutFiles(t.TempDir())
	for _, lookup := range []func() error{
		func() error { _, err := files.Read(ctx, "missing.jsonl"); return err },
		func() error { _, err := files.FindByID(ctx, "root"); return err },
		func() error { _, err := files.FindByPrefixes(ctx, nil, ""); return err },
		func() error { _, err := ReadSessionMetadata(ctx, "missing.jsonl"); return err },
	} {
		if err := lookup(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline replaced by lookup error: %v", err)
		}
	}
}
