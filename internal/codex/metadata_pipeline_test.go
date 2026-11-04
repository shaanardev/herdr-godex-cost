package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetadataPipelineStopsAllStages(t *testing.T) {
	for _, scenario := range []string{"cancelled", "fatal read"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			directory := filepath.Join(home, "sessions")
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "rollout-root.jsonl")
			if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"root"}}`+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			candidates := make([]string, 512)
			for i := range candidates {
				candidates[i] = path
			}
			if scenario == "fatal read" {
				// A directory passes containment checks but cannot be read as a JSONL file.
				candidates[0] = directory
			}
			index := &SessionIndex{home: home, files: map[string]string{}, ambiguous: map[string]struct{}{}, metadata: map[string]SessionMetadata{}, children: sessionGraph{}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- index.readMetadataPipeline(ctx, candidates) }()
			if scenario == "cancelled" {
				cancel()
			}
			select {
			case err := <-done:
				if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
				if scenario == "fatal read" {
					var pathErr *os.PathError
					if !errors.As(err, &pathErr) {
						t.Fatalf("lost read failure: %v", err)
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("pipeline did not finish joining its stages")
			}
		})
	}
}
