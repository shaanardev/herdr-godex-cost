package codex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrUnsafeRolloutPath = errors.New("rollout path is outside Codex session directories")

// ResolveRolloutPath resolves rawPath and returns it only when it is beneath Codex's
// sessions or archived_sessions directories.
func ResolveRolloutPath(rawPath, homeDir string) (string, error) {
	if rawPath == "" {
		return "", ErrUnsafeRolloutPath
	}

	if !filepath.IsAbs(rawPath) {
		rawPath = filepath.Join(homeDir, rawPath)
	}

	path, err := filepath.EvalSymlinks(rawPath)
	if err != nil {
		return "", fmt.Errorf("resolve rollout path %q: %w", rawPath, err)
	}

	for _, name := range []string{"sessions", "archived_sessions"} {
		base, err := filepath.EvalSymlinks(filepath.Join(homeDir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("resolve session directory: %w", err)
		}

		rel, err := filepath.Rel(base, path)
		if err == nil && rel != ".." &&
			!filepath.IsAbs(rel) &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return path, nil
		}
	}

	return "", ErrUnsafeRolloutPath
}
