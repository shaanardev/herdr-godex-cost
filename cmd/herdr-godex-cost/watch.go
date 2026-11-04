package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"
	"golang.org/x/sys/unix"
)

func watchAction(ctx context.Context, command *cli.Command) (err error) {
	paneID := command.String("pane")
	stateDir := command.String("state-dir")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("create worker state directory %q: %w", stateDir, err)
	}
	// Keep the lock file: overlapping workers must contend on the same inode.
	// The kernel releases the lock when its descriptor closes, including on exit.
	lockPath := filepath.Join(stateDir, fmt.Sprintf("watch-%x.lock", sha256.Sum256([]byte(paneID))))
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open worker lock %q: %w", lockPath, err)
	}
	defer func() {
		if closeErr := lock.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close worker lock: %w", closeErr))
		}
	}()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil
		}
		return fmt.Errorf("lock refresh worker for pane %q: %w", paneID, err)
	}
	if err := newRunner(command).Watch(ctx, paneID); err != nil {
		return fmt.Errorf("watch pane %q: %w", paneID, err)
	}
	return nil
}
