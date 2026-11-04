package codex

import (
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
func (files RolloutFiles) Read(path string) (*Rollout, error) {
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
	metadata, err := ReadSessionMetadata(safe)
	if err != nil {
		return nil, err
	}
	if metadata == nil || !ValidSessionID(metadata.ID) {
		return nil, ErrRolloutNotFound
	}
	return &Rollout{Path: safe, Metadata: *metadata}, nil
}

// FindByID matches the filename and metadata ID, rejecting distinct matching files.
func (files RolloutFiles) FindByID(id string) (*Rollout, error) {
	if !ValidSessionID(id) {
		return nil, fmt.Errorf("invalid Codex session ID %q", id)
	}
	suffix := "-" + id + ".jsonl"
	var found *Rollout
	err := walkRolloutFiles(files.Home, func(path string) error {
		if !strings.HasSuffix(filepath.Base(path), suffix) {
			return nil
		}
		rollout, err := files.Read(path)
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
func (files RolloutFiles) FindByPrefix(prefix string) (*Rollout, error) {
	matches, err := files.FindByPrefixes([]string{prefix})
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
// prefixes are omitted; an ambiguous prefix fails the lookup.
func (files RolloutFiles) FindByPrefixes(prefixes []string) (map[string]*Rollout, error) {
	matches := make(map[string]*Rollout)
	if len(prefixes) == 0 {
		return matches, nil
	}
	for _, prefix := range prefixes {
		if len(prefix) < 8 || !validUUIDPrefix.MatchString(prefix) {
			return nil, fmt.Errorf("invalid Codex session ID prefix %q", prefix)
		}
	}
	err := walkRolloutFiles(files.Home, func(path string) error {
		rollout, err := files.Read(path)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrUnsafeRolloutPath) || errors.Is(err, ErrRolloutNotFound) {
			return nil
		}
		if err != nil {
			return err
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
func walkRolloutFiles(home string, visit func(path string) error) error {
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
