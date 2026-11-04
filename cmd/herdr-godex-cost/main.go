package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/herder"
	"github.com/sarora/herdr-godex-cost/internal/refresh"
	"golang.org/x/sys/unix"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("codex cost", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	mode := "refresh"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		mode, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("herdr-godex-cost "+mode, flag.ContinueOnError)
	pane := flags.String("pane", "", "Herdr pane ID (defaults to plugin context)")
	interval := flags.Duration("interval", 5*time.Second, "refresh interval")
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	codexHome := flags.String("codex-home", envOr("CODEX_HOME", filepath.Join(home, ".codex")), "Codex data directory")
	state := flags.String("state-dir", envOr("HERDR_PLUGIN_STATE_DIR", filepath.Join(os.TempDir(), fmt.Sprintf("herdr-codex-cost-%d", os.Getuid()))), "cache directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	switch mode {
	case "refresh", "watch", "event":
	default:
		return fmt.Errorf("unknown command %q: use refresh, watch, or event", mode)
	}
	if *pane == "" {
		var context struct {
			PaneID        string `json:"pane_id"`
			FocusedPaneID string `json:"focused_pane_id"`
			Data          struct {
				PaneID string `json:"pane_id"`
			} `json:"data"`
		}
		raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
		if mode == "event" {
			raw = os.Getenv("HERDR_PLUGIN_EVENT_JSON")
		}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &context); err != nil {
				return fmt.Errorf("decode plugin context: %w", err)
			}
		}
		*pane = context.FocusedPaneID
		if *pane == "" {
			*pane = context.PaneID
		}
		if *pane == "" {
			*pane = context.Data.PaneID
		}
	}
	if *pane == "" {
		if mode == "event" {
			return nil
		}
		return fmt.Errorf("pane is required: pass -pane or run from a Herdr plugin action")
	}
	runner := refresh.Runner{Herdr: herder.NewClient(envOr("HERDR_BIN_PATH", "herdr")), CodexHome: *codexHome, StateDir: *state, Interval: *interval}
	if mode == "refresh" {
		return runner.Refresh(ctx, *pane)
	}
	if mode == "event" {
		info, err := runner.Herdr.ReadPaneInfo(ctx, *pane)
		if err != nil {
			return err
		}
		if info.Agent != "codex" || info.AgentStatus != "working" {
			return runner.Refresh(ctx, *pane)
		}
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		command := exec.Command(binary, "watch", "-pane", *pane, "-codex-home", *codexHome, "-state-dir", *state, "-interval", interval.String())
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := os.MkdirAll(*state, 0700); err != nil {
			return err
		}
		logPath := filepath.Join(*state, fmt.Sprintf("watch-%x.log", sha256.Sum256([]byte(*pane))))
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("open worker log: %w", err)
		}
		defer logFile.Close()
		command.Stderr = logFile
		if err := command.Start(); err != nil {
			return fmt.Errorf("start refresh worker: %w", err)
		}
		return command.Process.Release()
	}
	// Kernel-owned locks are released even after a crash. Keep the lock file so
	// overlapping event workers always contend on the same inode.
	if err := os.MkdirAll(*state, 0700); err != nil {
		return err
	}
	lockName := fmt.Sprintf("watch-%x.lock", sha256.Sum256([]byte(*pane)))
	lock, err := os.OpenFile(filepath.Join(*state, lockName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		return fmt.Errorf("lock refresh worker: %w", err)
	}
	return runner.Watch(ctx, *pane)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
