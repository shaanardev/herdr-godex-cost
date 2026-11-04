package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/codex"
)

const (
	processStartMatchWindow = time.Minute
	maxResolvedSessions     = 512
	sessionIDHexDigits      = "0123456789abcdefABCDEF"
)

var (
	ErrSessionNotFound  = errors.New("Codex session could not be resolved")
	ErrAmbiguousSession = errors.New("Codex session resolution is ambiguous")
)

// Holds pane metadata and an optional session ID already
// associated by the caller. The caller translates integration-specific data
// into this Codex-oriented input.
type PaneSessionInput struct {
	PaneID         string
	CWD            string
	TerminalText   string
	TerminalTitle  string
	KnownSessionID string
}

// Identifies the evidence that matched a Codex session.
type ResolutionSource string

const (
	ResolutionFromReportedID   ResolutionSource = "reported_session_id"
	ResolutionFromPaneText     ResolutionSource = "pane_text"
	ResolutionFromTerminalInfo ResolutionSource = "terminal_info"
	ResolutionFromProcessStart ResolutionSource = "process_start"
)

// Identifies one rollout in a pane's session tree.
type ResolvedSession struct {
	ThreadID    string
	RolloutPath string
}

// Combines normalized pane input with Codex's local session
// index and rollout files. Integration-specific response types belong at the
// application boundary, not in this package.
type SessionResolver struct {
	index *codex.SessionIndex
	files *codex.RolloutFiles
}

func NewSessionResolver(index *codex.SessionIndex, files *codex.RolloutFiles) (*SessionResolver, error) {
	if index == nil {
		return nil, errors.New("session index is required")
	}
	if files == nil {
		return nil, errors.New("rollout files are required")
	}
	return &SessionResolver{index: index, files: files}, nil
}

// Tries the caller's session association first, then IDs visible in
// the pane, and finally terminal-title plus working-directory evidence. Every
// candidate must resolve to a rollout whose session_meta ID agrees.
func (resolver *SessionResolver) Resolve(ctx context.Context, input PaneSessionInput) (*SessionResolution, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id := strings.TrimSpace(input.KnownSessionID); codex.ValidSessionID(id) {
		root, err := resolver.verifiedRollout(ctx, id)
		if err != nil && !errors.Is(err, ErrSessionNotFound) {
			return nil, fmt.Errorf("verify reported Codex session %q: %w", id, err)
		}
		if err == nil {
			return resolver.resolveTree(ctx, root, ResolutionFromReportedID)
		}
	}

	if strings.TrimSpace(input.CWD) == "" {
		return nil, ErrSessionNotFound
	}
	candidates := extractCandidates(input.TerminalText)
	visibleMatches, err := resolver.matchesFromCandidates(ctx, candidates, input.CWD)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex session from pane text: %w", err)
	}
	if len(visibleMatches) == 1 {
		return resolver.resolveTree(ctx, visibleMatches[0], ResolutionFromPaneText)
	}

	terminalMatches, err := resolver.matchesFromTerminalInfo(ctx, input.TerminalTitle, input.CWD)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex session from terminal info: %w", err)
	}
	if len(terminalMatches) == 1 {
		return resolver.resolveTree(ctx, terminalMatches[0], ResolutionFromTerminalInfo)
	}
	if len(visibleMatches) > 1 || len(terminalMatches) > 1 {
		return nil, fmt.Errorf("resolve pane session: %w", ErrAmbiguousSession)
	}
	return nil, fmt.Errorf("resolve pane session: %w", ErrSessionNotFound)
}

func (resolver *SessionResolver) resolveTree(ctx context.Context, root *codex.Rollout, source ResolutionSource) (*SessionResolution, error) {
	rootID := root.Metadata.ID
	resolution := &SessionResolution{RootThreadID: rootID, Source: source}
	queue := []string{rootID}
	visited := make(map[string]struct{})

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		threadID := queue[0]
		queue = queue[1:]
		if _, exists := visited[threadID]; exists {
			resolution.addDiagnostic(fmt.Errorf("session %q reached more than once in the session tree", threadID))
			continue
		}
		if len(visited) == maxResolvedSessions {
			resolution.addDiagnostic(fmt.Errorf("session tree exceeds the limit of %d sessions; next session is %q", maxResolvedSessions, threadID))
			break
		}
		visited[threadID] = struct{}{}
		resolution.addVisitedThread(threadID)

		rollout := root
		var err error
		if threadID != rootID {
			rollout, err = resolver.verifiedRollout(ctx, threadID)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			resolution.addDiagnostic(fmt.Errorf("verify child rollout %q: %w", threadID, err))
		} else {
			resolution.addSession(threadID, rollout.Path)
		}
		queue = append(queue, resolver.index.ChildrenOf(threadID)...)
	}
	return resolution, nil
}

func (resolver *SessionResolver) matchesFromTerminalInfo(ctx context.Context, title, cwd string) ([]*codex.Rollout, error) {
	var candidates []sessionIDCandidate
	for _, id := range resolver.index.SessionIDsByTitle(title, cwd) {
		candidates = append(candidates, sessionIDCandidate{ID: id})
	}
	return resolver.matchesFromCandidates(ctx, candidates, cwd)
}

func (resolver *SessionResolver) matchesFromCandidates(ctx context.Context, candidates []sessionIDCandidate, cwd string) ([]*codex.Rollout, error) {
	var prefixes []string
	for _, candidate := range candidates {
		if candidate.Prefix != "" {
			prefixes = append(prefixes, candidate.Prefix)
		}
	}
	prefixMatches, err := resolver.files.FindByPrefixes(ctx, prefixes, cwd)
	if err != nil {
		if errors.Is(err, codex.ErrAmbiguousRollout) {
			return nil, ErrAmbiguousSession
		}
		return nil, err
	}
	matches := make(map[string]*codex.Rollout)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var rollout *codex.Rollout
		if candidate.ID != "" {
			rollout, err = resolver.verifiedRollout(ctx, candidate.ID)
			if errors.Is(err, ErrSessionNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
		} else {
			var ok bool
			rollout, ok = prefixMatches[candidate.Prefix]
			if !ok {
				continue
			}
			// An indexed association remains authoritative; reuse the verified
			// metadata from the scan when the paths agree.
			indexedPath, indexErr := resolver.index.RolloutPath(rollout.Metadata.ID)
			if errors.Is(indexErr, codex.ErrAmbiguousRollout) {
				return nil, ErrAmbiguousSession
			}
			if indexErr != nil && !errors.Is(indexErr, codex.ErrRolloutNotFound) {
				return nil, indexErr
			}
			if indexErr == nil && indexedPath != rollout.Path {
				continue
			}
		}
		if cwd != "" {
			candidateCWD := strings.TrimSpace(rollout.Metadata.CWD)
			wantedCWD := strings.TrimSpace(cwd)
			if candidateCWD == "" || wantedCWD == "" {
				continue
			}
			candidateCWD = filepath.Clean(candidateCWD)
			if resolved, err := filepath.EvalSymlinks(candidateCWD); err == nil {
				candidateCWD = resolved
			}
			wantedCWD = filepath.Clean(wantedCWD)
			if resolved, err := filepath.EvalSymlinks(wantedCWD); err == nil {
				wantedCWD = resolved
			}
			if candidateCWD != wantedCWD {
				continue
			}
		}
		matches[rollout.Metadata.ID] = rollout
	}
	rollouts := make([]*codex.Rollout, 0, len(matches))
	for _, rollout := range matches {
		rollouts = append(rollouts, rollout)
	}
	slices.SortFunc(rollouts, func(left, right *codex.Rollout) int {
		return strings.Compare(left.Metadata.ID, right.Metadata.ID)
	})
	return rollouts, nil
}

type sessionIDCandidate struct {
	ID     string
	Prefix string
}

func extractCandidates(text string) []sessionIDCandidate {
	seen := make(map[string]struct{})
	var candidates []sessionIDCandidate
	for _, location := range codex.FullUUIDPattern.FindAllStringIndex(text, -1) {
		start, end := location[0], location[1]
		if (start > 0 && strings.ContainsRune(sessionIDHexDigits, rune(text[start-1]))) ||
			(end < len(text) && strings.ContainsRune(sessionIDHexDigits, rune(text[end]))) {
			continue
		}
		id := strings.ToLower(text[start:end])
		if _, exists := seen["id:"+id]; !exists {
			seen["id:"+id] = struct{}{}
			candidates = append(candidates, sessionIDCandidate{ID: id})
		}
	}
	for _, location := range codex.TruncatedUUIDPattern.FindAllStringSubmatchIndex(text, -1) {
		start := location[0]
		if start > 0 && strings.ContainsRune(sessionIDHexDigits, rune(text[start-1])) {
			continue
		}
		prefix := strings.ToLower(text[location[2]:location[3]])
		if _, exists := seen["prefix:"+prefix]; !exists {
			seen["prefix:"+prefix] = struct{}{}
			candidates = append(candidates, sessionIDCandidate{Prefix: prefix})
		}
	}
	return candidates
}

func (resolver *SessionResolver) verifiedRollout(ctx context.Context, id string) (*codex.Rollout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := resolver.index.RolloutPath(id)
	var rollout *codex.Rollout
	if errors.Is(err, codex.ErrRolloutNotFound) {
		rollout, err = resolver.files.FindByID(ctx, id)
	} else if err == nil {
		rollout, err = resolver.files.Read(ctx, path)
		if err == nil && rollout.Metadata.ID != id {
			err = codex.ErrRolloutNotFound
		}
	}
	if err != nil {
		if errors.Is(err, codex.ErrRolloutNotFound) || errors.Is(err, os.ErrNotExist) || errors.Is(err, codex.ErrUnsafeRolloutPath) {
			return nil, ErrSessionNotFound
		}
		if errors.Is(err, codex.ErrAmbiguousRollout) {
			return nil, ErrAmbiguousSession
		}
		return nil, err
	}
	return rollout, nil
}

// ResolveByProcessStart is a fallback for panes without a usable session reference.
// Require one top-level session in the same directory within one minute.
func (resolver *SessionResolver) ResolveByProcessStart(ctx context.Context, cwd string, started time.Time) (*SessionResolution, error) {
	candidate, err := resolver.index.SessionByProcessStart(ctx, cwd, started, processStartMatchWindow)
	switch {
	case errors.Is(err, codex.ErrRolloutNotFound):
		return nil, ErrSessionNotFound
	case errors.Is(err, codex.ErrAmbiguousRollout):
		return nil, ErrAmbiguousSession
	case err != nil:
		return nil, err
	}
	root, err := resolver.verifiedRollout(ctx, candidate.ID)
	if err != nil {
		return nil, err
	}
	// Reject metadata replaced since indexing; retry from a fresh index next pass.
	if root.Metadata != *candidate {
		return nil, ErrSessionNotFound
	}
	return resolver.resolveTree(ctx, root, ResolutionFromProcessStart)
}
