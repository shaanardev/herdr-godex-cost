package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/urfave/cli/v3"
)

func eventAction(ctx context.Context, command *cli.Command) (err error) {
	paneID := command.String("pane")
	if paneID == "" {
		return nil
	}
	runner := newRunner(command)
	info, err := runner.HerdrClient.ReadPaneInfo(ctx, paneID)
	if err != nil {
		return fmt.Errorf("read event pane %q: %w", paneID, err)
	}
	if info.Agent != "codex" {
		return nil
	}
	if info.AgentStatus != "working" {
		if err := runner.Refresh(ctx, paneID); err != nil {
			return fmt.Errorf("refresh event pane %q: %w", paneID, err)
		}
		return nil
	}
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find worker executable: %w", err)
	}
	stateDir := command.String("state-dir")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("create worker state directory %q: %w", stateDir, err)
	}
	logPath := filepath.Join(stateDir, fmt.Sprintf("watch-%x.log", sha256.Sum256([]byte(paneID))))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open worker log %q: %w", logPath, err)
	}
	defer func() {
		if closeErr := logFile.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close worker log: %w", closeErr))
		}
	}()
	// A detached worker must outlive this event's context. It handles its own
	// signals and uses the same pane lock as foreground watch commands.
	worker := exec.Command(binary, "watch", "--pane", paneID, "--codex-home", command.String("codex-home"), "--state-dir", stateDir, "--interval", command.Duration("interval").String())
	worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	worker.Stderr = logFile
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := worker.Start(); err != nil {
		return fmt.Errorf("start refresh worker for pane %q: %w", paneID, err)
	}
	if err := worker.Process.Release(); err != nil {
		return fmt.Errorf("release refresh worker process: %w", err)
	}
	return nil
}
