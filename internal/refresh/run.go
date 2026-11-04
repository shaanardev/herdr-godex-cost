package refresh

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/codex"
	"github.com/sarora/herdr-godex-cost/internal/herder"
	_ "modernc.org/sqlite"
)

// Runner holds the dependencies for a refresh. Each pass rebuilds its index
// so newly spawned descendants and newly created rollouts are discovered.
type Runner struct {
	Herdr     *herder.Client
	CodexHome string
	StateDir  string
	Interval  time.Duration
	// A watch worker remembers the last diagnostic set until resolution recovers.
	lastResolutionDiagnostics map[string]string
}

func (runner Runner) sessionIndex(ctx context.Context) (*codex.SessionIndex, error) {
	databases, err := filepath.Glob(filepath.Join(runner.CodexHome, "state_*.sqlite"))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(databases, func(left, right string) int {
		version := func(path string) int {
			n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "state_"), ".sqlite"))
			return n
		}
		return version(left) - version(right)
	})
	// Prefer the newest schema. Older files can remain after Codex upgrades.
	for _, database := range slices.Backward(databases) {
		uri := url.URL{Scheme: "file", Path: database, RawQuery: "mode=ro"}
		db, err := sql.Open("sqlite", uri.String())
		if err != nil {
			slog.Warn("open state database", "path", database, "error", err)
			continue
		}
		index, indexErr := codex.NewSessionIndex(ctx, db, runner.CodexHome)
		closeErr := db.Close()
		if closeErr != nil {
			slog.Warn("close state database", "error", closeErr)
		}
		if indexErr == nil {
			return index, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("read state database; trying fallback", "path", database, "error", indexErr)
	}
	return codex.NewSessionIndex(ctx, nil, runner.CodexHome)
}

// Refresh updates a single pane, preserving its last value during a restart.
func (runner Runner) Refresh(ctx context.Context, paneID string) error {
	pane, err := runner.Herdr.ReadPaneInfo(ctx, paneID)
	if err != nil {
		return err
	}
	return runner.refreshPane(ctx, paneID, pane)
}

// refreshPane uses the status snapshot already fetched by the caller.
func (runner Runner) refreshPane(ctx context.Context, paneID string, pane *herder.PaneInfo) error {
	if pane.Agent != "codex" {
		return nil
	}
	index, err := runner.sessionIndex(ctx)
	if err != nil {
		return err
	}
	for _, diagnostic := range index.Diagnostics {
		slog.Warn("index rollout", "error", diagnostic)
	}
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(runner.CodexHome))
	if err != nil {
		return err
	}
	input := PaneSessionInput{PaneID: paneID, CWD: pane.CWD, TerminalTitle: pane.Title}
	if pane.AgentSession != nil && pane.AgentSession.Agent == "codex" && pane.AgentSession.Kind == "id" {
		input.KnownSessionID = pane.AgentSession.Value
	}
	// Read visible text only when the reported association cannot be verified.
	resolution, err := resolver.Resolve(input)
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrAmbiguousSession) {
		text, readErr := runner.Herdr.ReadPane(ctx, paneID)
		if readErr != nil {
			return readErr
		}
		input.TerminalText = string(text)
		resolution, err = resolver.Resolve(input)
	}
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrAmbiguousSession) {
			if pane.Tokens["cost"] == "" || pane.Tokens["cost"] == "…" {
				if publishErr := runner.Herdr.UpdatePane(ctx, paneID, "session?"); publishErr != nil {
					return publishErr
				}
			}
		}
		return err
	}
	runner.logResolutionDiagnostics(paneID, resolution.Diagnostics)
	fingerprint, err := resolution.Fingerprint()
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(paneID)))
	cache := NewCache(filepath.Join(runner.StateDir, key+".json"), resolution.RootThreadID, fingerprint)
	var estimate codex.Estimate
	cacheErr := ErrCacheMiss
	if !resolution.Incomplete() {
		var cached *codex.Estimate
		cached, cacheErr = cache.Read()
		if cacheErr == nil {
			estimate = *cached
		}
	}
	if cacheErr != nil {
		if !errors.Is(cacheErr, ErrCacheMiss) {
			slog.Warn("read cache", "pane", paneID, "error", cacheErr)
		}
		estimate = *codex.NewEstimate(resolution.Incomplete())
		for _, session := range resolution.Sessions {
			usage, readErr := readUsage(session.RolloutPath)
			if readErr != nil {
				estimate.Incomplete = true
				slog.Warn("read rollout", "path", session.RolloutPath, "error", readErr)
				continue
			}
			item := usage.Estimate()
			estimate.KnownUSD += item.KnownUSD
			estimate.EstimatedUSD += item.EstimatedUSD
			estimate.HasUsage = estimate.HasUsage || item.HasUsage
			estimate.Incomplete = estimate.Incomplete || item.Incomplete
		}
		// Retry damaged files on the next pass, even if their stat data is unchanged.
		if !estimate.Incomplete {
			if err := cache.WriteAtomic(estimate); err != nil {
				slog.Warn("write cache", "error", err)
			}
		}
	}
	display := estimate.String()
	if display == "..." && pane.Tokens["cost"] != "" && pane.Tokens["cost"] != "..." {
		return nil
	}
	if pane.Tokens["cost"] == display {
		return nil
	}
	return runner.Herdr.UpdatePane(ctx, paneID, display)
}

// Logs changed warnings once per watch worker. Clearing
// the remembered set on recovery lets a later recurrence be reported again.
func (runner Runner) logResolutionDiagnostics(paneID string, diagnostics []error) {
	combined := errors.Join(diagnostics...)
	if combined == nil {
		delete(runner.lastResolutionDiagnostics, paneID)
		return
	}
	if runner.lastResolutionDiagnostics != nil {
		message := combined.Error()
		if runner.lastResolutionDiagnostics[paneID] == message {
			return
		}
		runner.lastResolutionDiagnostics[paneID] = message
	}
	slog.Warn("resolve session tree", "pane", paneID, "error", combined)
}

func readUsage(path string) (*codex.RolloutUsage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return codex.ParseRollout(file)
}

// Watch reads each pane snapshot once and refreshes it, including the first
// non-working snapshot. It waits a full interval after each working pass.
func (runner Runner) Watch(ctx context.Context, paneID string) error {
	if runner.Interval <= 0 {
		return errors.New("refresh interval must be positive")
	}

	runner.lastResolutionDiagnostics = make(map[string]string)
	// The first pass runs immediately; subsequent waits start after the work.
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pane, err := runner.Herdr.ReadPaneInfo(ctx, paneID)
		if err != nil {
			return err
		}
		if pane.Agent != "codex" {
			return nil
		}
		err = runner.refreshPane(ctx, paneID, pane)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if pane.AgentStatus != "working" {
			return err
		}
		if err != nil {
			slog.Warn("refresh pane", "pane", paneID, "error", err)
		}
		timer.Reset(runner.Interval)
	}
}
