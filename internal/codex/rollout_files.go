package codex

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrRolloutNotFound  = errors.New("Codex rollout not found")
	ErrAmbiguousRollout = errors.New("Codex rollout match is ambiguous")
)

// RolloutFiles finds and reads local rollouts under a Codex home directory.
type RolloutFiles struct{ Home string }

func NewRolloutFiles(home string) *RolloutFiles { return &RolloutFiles{Home: home} }

// Rollout contains a verified path and the metadata read from that file.
type Rollout struct {
	Path     string
	Metadata SessionMetadata
}

// Read verifies containment, file type and session metadata before returning a rollout.
// Filesystem and decoding errors retain their original causes.
func (files RolloutFiles) Read(ctx context.Context, path string) (*Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	safe, err := ResolveRolloutPath(path, files.Home)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(safe)
	if err != nil {
		return nil, fmt.Errorf("stat rollout %q: %w", safe, err)
	}
	if !info.Mode().IsRegular() {
		return nil, ErrRolloutNotFound
	}
	metadata, err := ReadSessionMetadata(ctx, safe)
	if err != nil {
		return nil, err
	}
	if metadata == nil || !ValidSessionID(metadata.ID) {
		return nil, ErrRolloutNotFound
	}
	return &Rollout{Path: safe, Metadata: *metadata}, nil
}

// FindByID matches the filename and metadata ID, rejecting distinct matching files.
func (files RolloutFiles) FindByID(ctx context.Context, id string) (*Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ValidSessionID(id) {
		return nil, fmt.Errorf("invalid Codex session ID %q", id)
	}
	suffix := "-" + id + ".jsonl"
	var found *Rollout
	err := walkRolloutFiles(ctx, files.Home, func(path string) error {
		if !strings.HasSuffix(filepath.Base(path), suffix) {
			return nil
		}
		rollout, err := files.Read(ctx, path)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrUnsafeRolloutPath) || errors.Is(err, ErrRolloutNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if rollout.Metadata.ID != id {
			return nil
		}
		if found != nil && found.Path != rollout.Path {
			return ErrAmbiguousRollout
		}
		found = rollout
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrRolloutNotFound
	}
	return found, nil
}

// FindByPrefix returns one unambiguous metadata match. Prefixes must contain
// at least eight hexadecimal characters to avoid matching ordinary pane text.
func (files RolloutFiles) FindByPrefix(ctx context.Context, prefix string) (*Rollout, error) {
	matches, err := files.FindByPrefixes(ctx, []string{prefix}, "")
	if err != nil {
		return nil, err
	}
	rollout, ok := matches[prefix]
	if !ok {
		return nil, ErrRolloutNotFound
	}
	return rollout, nil
}

// FindByPrefixes resolves all prefixes during one directory scan. Unmatched
// prefixes are omitted; an ambiguous prefix fails the lookup. A nonempty cwd
// filters candidates before ambiguity is determined.
func (files RolloutFiles) FindByPrefixes(ctx context.Context, prefixes []string, cwd string) (map[string]*Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	matches := make(map[string]*Rollout)
	if len(prefixes) == 0 {
		return matches, nil
	}
	for _, prefix := range prefixes {
		if len(prefix) < 8 || !validUUIDPrefix.MatchString(prefix) {
			return nil, fmt.Errorf("invalid Codex session ID prefix %q", prefix)
		}
	}
	expectedCWD := filepath.Clean(strings.TrimSpace(cwd))
	if resolved, err := filepath.EvalSymlinks(expectedCWD); err == nil {
		expectedCWD = resolved
	}
	err := walkRolloutFiles(ctx, files.Home, func(path string) error {
		rollout, err := files.Read(ctx, path)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrUnsafeRolloutPath) || errors.Is(err, ErrRolloutNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if cwd != "" {
			if strings.TrimSpace(rollout.Metadata.CWD) == "" {
				return nil
			}
			actualCWD := filepath.Clean(strings.TrimSpace(rollout.Metadata.CWD))
			if resolved, err := filepath.EvalSymlinks(actualCWD); err == nil {
				actualCWD = resolved
			}
			if actualCWD != expectedCWD {
				return nil
			}
		}
		for _, prefix := range prefixes {
			if !strings.HasPrefix(rollout.Metadata.ID, prefix) {
				continue
			}
			if found, ok := matches[prefix]; ok && found.Path != rollout.Path {
				return fmt.Errorf("prefix %q: %w", prefix, ErrAmbiguousRollout)
			}
			matches[prefix] = rollout
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// Visits rollout JSONL files under both Codex session roots.
func walkRolloutFiles(ctx context.Context, home string, visit func(path string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, baseName := range []string{"sessions", "archived_sessions"} {
		base := filepath.Join(home, baseName)
		info, err := os.Stat(base)
		if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
			continue
		}
		if err != nil {
			return err
		}

		err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				return nil
			}

			return visit(path)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
