package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sarora/herdr-godex-cost/internal/herder"
)

func TestRefreshDeadlineCoversMetadataPublication(t *testing.T) {
	home := t.TempDir()
	writeRollout(t, home, "root", "")
	directory := t.TempDir()
	executable := filepath.Join(directory, "herdr")
	script := `#!/bin/sh
case "$1 $2" in
 "pane get") printf '%s' '{"result":{"pane":{"agent":"codex","agent_session":{"agent":"codex","kind":"id","value":"root"}}}}' ;;
 "pane report-metadata") exec /bin/sleep 30 ;;
 *) exit 7 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(herder.NewClient(executable), home, t.TempDir(), time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runner.Refresh(ctx, "pane")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost publication deadline: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("publication did not stop promptly")
	}
}
