package driver

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

// Limit each refresh pass to one minute, including pane lookup, parsing,
// and metadata publication.
const refreshTimeout = time.Minute

// Runner coordinates session resolution, usage estimates, pane metadata, and
// polling. Each pass rebuilds its index to discover new sessions and subagents.
type Runner struct {
	HerdrClient *herder.Client
	CodexHome   string
	StateDir    string
	Interval    time.Duration
	// A watch worker remembers the last diagnostic set until resolution recovers.
	lastDiagnostics map[string]string
}

func NewRunner(herdrClient *herder.Client, codexHome string, stateDir string, interval time.Duration) *Runner {
	return &Runner{HerdrClient: herdrClient, CodexHome: codexHome, StateDir: stateDir, Interval: interval}
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
	// Codex upgrades can leave useful threads and edges in several databases.
	var databasesToIndex []*sql.DB
	for _, database := range databases {
		uri := url.URL{Scheme: "file", Path: database, RawQuery: "mode=ro"}
		db, err := sql.Open("sqlite", uri.String())
		if err != nil {
			slog.Warn("open state database", "path", database, "error", err)
			continue
		}
		defer func() {
			if err := db.Close(); err != nil {
				slog.Warn("close state database", "path", database, "error", err)
			}
		}()
		databasesToIndex = append(databasesToIndex, db)
	}
	return codex.NewSessionIndex(ctx, runner.CodexHome, databasesToIndex...)
}

// Refresh updates a single pane, preserving its last value during a restart.
func (runner Runner) Refresh(ctx context.Context, paneID string) error {
	_, err := runner.refreshPass(ctx, paneID)
	return err
}

func (runner Runner) refreshPass(ctx context.Context, paneID string) (*herder.PaneInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	pane, err := runner.HerdrClient.ReadPaneInfo(ctx, paneID)
	if err != nil {
		return nil, err
	}
	return pane, runner.refreshPane(ctx, paneID, pane)
}

// refreshPane uses the status snapshot already fetched by the caller.
func (runner Runner) refreshPane(ctx context.Context, paneID string, pane *herder.PaneInfo) error {
	if pane.Agent != "codex" {
		return nil
	}
	mode, err := ReadDisplayMode(runner.StateDir)
	if err != nil {
		return err
	}
	resolution, err := runner.resolvePaneSession(ctx, paneID, pane)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrAmbiguousSession) {
			if pane.Tokens["cost"] == "" || pane.Tokens["cost"] == "…" {
				if publishErr := runner.HerdrClient.UpdatePane(ctx, paneID, "session?"); publishErr != nil {
					return publishErr
				}
			}
		}
		return err
	}
	runner.logDiagnostics(paneID, "resolve session tree", resolution.Diagnostics)
	fingerprint, err := resolution.Fingerprint(ctx)
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
			usage, readErr := readUsage(ctx, session.RolloutPath)
			if readErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				estimate.Incomplete = true
				slog.Warn("read rollout", "path", session.RolloutPath, "error", readErr)
				continue
			}
			estimate.Add(usage.Estimate())
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Retry damaged files on the next pass, even if their stat data is unchanged.
		if !estimate.Incomplete {
			if err := cache.WriteAtomic(estimate); err != nil {
				slog.Warn("write cache", "error", err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	display := estimate.String()
	if mode == "credit" {
		display = estimate.CreditString()
	}
	if display == "…" && pane.Tokens["cost"] != "" && pane.Tokens["cost"] != "…" {
		return nil
	}
	if pane.Tokens["cost"] == display {
		return nil
	}
	return runner.HerdrClient.UpdatePane(ctx, paneID, display)
}

// Logs changed warnings once per watch worker. Clearing
// the remembered set on recovery lets a later recurrence be reported again.
func (runner Runner) logDiagnostics(paneID, stage string, diagnostics []error) {
	key := stage + ":" + paneID
	combined := errors.Join(diagnostics...)
	if combined == nil {
		delete(runner.lastDiagnostics, key)
		return
	}
	if runner.lastDiagnostics != nil {
		message := combined.Error()
		if runner.lastDiagnostics[key] == message {
			return
		}
		runner.lastDiagnostics[key] = message
	}
	slog.Warn(stage, "pane", paneID, "error", combined)
}

func readUsage(ctx context.Context, path string) (*codex.RolloutUsage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return codex.ParseRollout(ctx, file)
}

// Watch reads each pane snapshot once and refreshes it, including the first
// non-working snapshot. It waits a full interval after each working pass.
func (runner Runner) Watch(ctx context.Context, paneID string) error {
	if runner.Interval <= 0 {
		return errors.New("refresh interval must be positive")
	}

	runner.lastDiagnostics = make(map[string]string)
	// The first pass runs immediately; subsequent waits start after the work.
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastError string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		pane, err := runner.refreshPass(ctx, paneID)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if pane == nil {
			return err
		}
		if pane.Agent != "codex" {
			return nil
		}
		if pane.AgentStatus != "working" {
			return err
		}
		if err != nil {
			if message := err.Error(); message != lastError {
				slog.Warn("refresh pane", "pane", paneID, "error", err)
				lastError = message
			}
		} else {
			lastError = ""
		}
		timer.Reset(runner.Interval)
	}
}

func (runner Runner) resolvePaneSession(ctx context.Context, paneID string, pane *herder.PaneInfo) (*SessionResolution, error) {
	index, err := runner.sessionIndex(ctx)
	if err != nil {
		return nil, err
	}
	runner.logDiagnostics(paneID, "index rollout", index.Diagnostics)
	resolver, err := NewSessionResolver(index, codex.NewRolloutFiles(runner.CodexHome))
	if err != nil {
		return nil, err
	}
	input := PaneSessionInput{PaneID: paneID, CWD: pane.CWD, TerminalTitle: pane.Title}
	if pane.AgentSession != nil && pane.AgentSession.Agent == "codex" && pane.AgentSession.Kind == "id" {
		input.KnownSessionID = pane.AgentSession.Value
	}
	// Read visible text only when the reported association cannot be verified.
	resolution, err := resolver.Resolve(ctx, input)
	if errors.Is(err, ErrSessionNotFound) || errors.Is(err, ErrAmbiguousSession) {
		text, readErr := runner.HerdrClient.ReadPane(ctx, paneID)
		if readErr != nil {
			return nil, readErr
		}
		input.TerminalText = string(text)
		resolution, err = resolver.Resolve(ctx, input)
	}
	if errors.Is(err, ErrSessionNotFound) {
		started, processErr := runner.HerdrClient.CodexProcessStart(ctx, paneID)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if processErr == nil {
			resolution, err = resolver.ResolveByProcessStart(ctx, pane.CWD, started)
		}
	}
	return resolution, err
}
