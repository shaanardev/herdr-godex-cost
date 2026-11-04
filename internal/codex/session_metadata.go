package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Holds session metadata fields used to associate panes and
// build session trees.
type SessionMetadata struct {
	Timestamp      string `json:"timestamp"`
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

// Reads the first session metadata event from a Codex JSONL
// rollout. It returns (nil, nil) when the file has no session_meta event.
func ReadSessionMetadata(ctx context.Context, path string) (*SessionMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Codex rollout %q: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var event rolloutEvent
			if err := json.Unmarshal(line, &event); err != nil {
				return nil, fmt.Errorf("decode Codex rollout %q: %w", path, err)
			}
			if event.Type == "session_meta" {
				metadata := new(SessionMetadata)
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
