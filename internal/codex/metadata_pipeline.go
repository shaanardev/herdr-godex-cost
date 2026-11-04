package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

const metadataReaders = 4

type metadataResult struct {
	path     string
	metadata *SessionMetadata
	err      error
}

// readMetadataPipeline replaces the sequential metadata-reading loop with:
// candidate paths -> bounded readers -> a single index collector.
// Only the collector mutates the index, so its maps need no mutexes.
func (index *SessionIndex) readMetadataPipeline(ctx context.Context, candidates []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}

	// The first fatal error cancels the other stages and becomes the return value.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	paths := make(chan string)
	results := make(chan metadataResult)
	var stages, readers sync.WaitGroup

	// Stage 1 owns paths. Unbuffered sends apply backpressure: the producer cannot
	// enqueue an unbounded number of reads ahead of the workers.
	stages.Go(func() {
		defer close(paths)
		for _, path := range candidates {
			select {
			case <-ctx.Done():
				return
			case paths <- path:
			}
		}
	})

	// Stage 2 has a fixed upper bound on concurrent file reads. Every blocking
	// channel operation also listens for cancellation, including result sends.
	for range min(metadataReaders, len(candidates)) {
		readers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case path, ok := <-paths:
					if !ok {
						return
					}
					result := readMetadataCandidate(ctx, index.home, path)
					select {
					case <-ctx.Done():
						return
					case results <- result:
					}
				}
			}
		})
	}

	// Multiple workers send results, so none may close results itself. This
	// coordinator closes it only after every reader has exited.
	stages.Go(func() {
		readers.Wait()
		close(results)
	})

	// Stage 3 owns all index mutations. Results arrive in completion order;
	// session lookups still sort IDs and reject duplicate rollout paths.
	for result := range results {
		if ctx.Err() != nil {
			continue
		}
		if result.err != nil {
			if errors.Is(result.err, os.ErrNotExist) || errors.Is(result.err, ErrUnsafeRolloutPath) {
				continue
			}
			var syntax *json.SyntaxError
			var fieldType *json.UnmarshalTypeError
			if errors.As(result.err, &syntax) || errors.As(result.err, &fieldType) {
				index.addDiagnostic(result.err)
				continue
			}
			// Keep draining until results closes, allowing all stages to finish.
			cancel(result.err)
			continue
		}
		metadata := result.metadata
		if metadata == nil || !ValidSessionID(metadata.ID) {
			continue
		}
		index.addRollout(metadata.ID, result.path, metadata)
		if validSessionID.MatchString(metadata.ParentThreadID) {
			index.children.addEdge(metadata.ParentThreadID, metadata.ID)
		}
	}
	stages.Wait()
	return context.Cause(ctx)
}

func readMetadataCandidate(ctx context.Context, home, path string) metadataResult {
	safe, err := ResolveRolloutPath(path, home)
	if err != nil {
		return metadataResult{err: err}
	}
	metadata, err := ReadSessionMetadata(ctx, safe)
	return metadataResult{path: safe, metadata: metadata, err: err}
}
