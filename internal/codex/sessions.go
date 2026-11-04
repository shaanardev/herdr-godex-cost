package codex

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var FullUUIDPattern = regexp.MustCompile(`(^|[^0-9a-f])([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})($|[^0-9a-f])`)

var TruncatedUUIDPattern = regexp.MustCompile(`(^|[^0-9a-f])([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{0,11})(…|\.{3})`)

type Client struct {
	codexHome string
}

func NewClient(codexHome string) *Client {
	return &Client{codexHome: codexHome}
}

func (c Client) FindRolloutSessionByID(threadID string) (string, bool, error) {
	if !validSessionID.MatchString(threadID) {
		return "", false, nil
	}
	suffix := "-" + threadID + ".jsonl"
	var found string

	err := walkRolloutFiles(c.codexHome, func(path string) (bool, error) {
		if !strings.HasSuffix(filepath.Base(path), suffix) {
			return true, nil
		}
		if safe, ok := isSafePath(path, c.codexHome); ok {
			found = safe
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return "", false, err
	}
	return found, found != "", nil
}

// walkRolloutFiles visits rollout JSONL files under both Codex session roots.
// Returning false from visit stops the search after the current file.
func walkRolloutFiles(home string, visit func(path string) (keepWalking bool, err error)) error {
	for _, baseName := range []string{"sessions", "archived_sessions"} {
		base := filepath.Join(home, baseName)
		info, err := os.Stat(base)
		if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
			continue
		}
		if err != nil {
			return err
		}

		stopped := false
		err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
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

			keepWalking, err := visit(path)
			if err != nil {
				return err
			}
			if !keepWalking {
				stopped = true
				return fs.SkipAll
			}
			return nil
		})
		if stopped {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}
