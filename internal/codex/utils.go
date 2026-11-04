package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var validSessionID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// sessionMetadata contains the rollout fields used to associate sessions and
// their child threads.
type sessionMetadata struct {
	ID             string `json:"id"`
	ParentThreadID string `json:"parent_thread_id"`
	ForkedFromID   string `json:"forked_from_id"`
	ThreadSource   string `json:"thread_source"`
	RolloutPath    string `json:"rollout_path"`
	CWD            string `json:"cwd"`
}

type rolloutEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// readSessionMetadata reads the first session_meta event from a Codex JSONL
// rollout. It returns (nil, nil) when the file has no session_meta event.
func readSessionMetadata(path string) (*sessionMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Codex rollout %q: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var event rolloutEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return nil, fmt.Errorf("decode Codex rollout %q: %w", path, err)
			}
			if event.Type == "session_meta" {
				metadata := new(sessionMetadata)
				if len(event.Payload) != 0 && string(event.Payload) != "null" {
					if err := json.Unmarshal(event.Payload, metadata); err != nil {
						return nil, fmt.Errorf("decode session metadata in %q: %w", path, err)
					}
				}
				return metadata, nil
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				return nil, nil
			}
			return nil, fmt.Errorf("read Codex rollout %q: %w", path, readErr)
		}
	}
}

func isSafePath(rawPath, homeDir string) (string, bool) {
	if rawPath == "" {
		return "", false
	}

	if !filepath.IsAbs(rawPath) {
		rawPath = filepath.Join(homeDir, rawPath)
	}

	path, err := filepath.EvalSymlinks(rawPath)
	if err != nil {
		return "", false // Missing path, or unable to resolve it
	}

	for _, name := range []string{"sessions", "archived_sessions"} {
		base, err := filepath.EvalSymlinks(filepath.Join(homeDir, name))
		if err != nil {
			continue
		}

		rel, err := filepath.Rel(base, path)
		if err == nil && rel != ".." &&
			!filepath.IsAbs(rel) &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return path, true
		}
	}

	return "", false
}
