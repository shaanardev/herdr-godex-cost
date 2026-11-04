package refresh

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
)

// Holds the root session and its resolved descendants. The visited ID list
// also includes indexed descendants whose rollout file was
// missing, so fingerprints still reflect the complete indexed tree.
type SessionResolution struct {
	RootThreadID     string
	Source           ResolutionSource
	Sessions         []ResolvedSession
	VisitedThreadIDs []string
	Diagnostics      []error
}

// Incomplete reports whether any session-tree entries could not be resolved reliably.
func (resolution SessionResolution) Incomplete() bool {
	return len(resolution.Diagnostics) > 0
}

func (resolution *SessionResolution) addSession(threadID, rolloutPath string) {
	resolution.Sessions = append(resolution.Sessions, ResolvedSession{
		ThreadID:    threadID,
		RolloutPath: rolloutPath,
	})
}

func (resolution *SessionResolution) addVisitedThread(threadID string) {
	resolution.VisitedThreadIDs = append(resolution.VisitedThreadIDs, threadID)
}

func (resolution *SessionResolution) addDiagnostic(err error) {
	resolution.Diagnostics = append(resolution.Diagnostics, err)
}

// Computes a versioned fingerprint for the resolved tree.
func (resolution *SessionResolution) Fingerprint() (string, error) {
	paths := make([]string, 0, len(resolution.Sessions))
	for _, session := range resolution.Sessions {
		paths = append(paths, session.RolloutPath)
	}

	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "v%d\x00%s\x00", CacheVersion, resolution.RootThreadID)
	threadIDs := slices.Clone(resolution.VisitedThreadIDs)
	slices.Sort(threadIDs)
	threadIDs = slices.Compact(threadIDs)
	for _, id := range threadIDs {
		_, _ = fmt.Fprintf(hash, "id\x00%s\x00", id)
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("stat rollout %q for fingerprint: %w", path, err)
		}
		_, _ = fmt.Fprintf(hash, "path\x00%s\x00%d\x00%d\x00", path, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
