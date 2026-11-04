package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWatchLockPreventsDuplicateWorkerAndReleasesOnError(t *testing.T) {
	state := t.TempDir()
	path := filepath.Join(state, fmt.Sprintf("watch-%x.lock", sha256.Sum256([]byte("pane"))))
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	// A duplicate must exit before ever invoking this nonexistent executable.
	t.Setenv("HERDR_BIN_PATH", filepath.Join(t.TempDir(), "missing-herdr"))
	args := []string{"watch", "--pane", "pane", "--state-dir", state}
	if err := runCLI(context.Background(), args); err != nil {
		t.Fatalf("duplicate worker attempted work: %v", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(context.Background(), args); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unlocked worker did not invoke Herdr: %v", err)
	}
	// The failed watch must release its descriptor and lock.
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("failed worker retained its lock: %v", err)
	}
}

func TestCancelledWatchDoesNotCreateState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCLI(ctx, []string{"watch", "--pane", "pane", "--state-dir", state})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("cancelled watch created state: %v", err)
	}
}
