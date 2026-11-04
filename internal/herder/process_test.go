package herder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexProcessStartDeadline(t *testing.T) {
	directory := t.TempDir()
	herdr := filepath.Join(directory, "herdr")
	script := `#!/bin/sh
printf '%s' '{"result":{"process_info":{"foreground_processes":[{"name":"codex","pid":123}]}}}'
`
	if err := os.WriteFile(herdr, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// Simulate a stuck ps. exec lets CommandContext terminate the actual process.
	if err := os.WriteFile(filepath.Join(directory, "ps"), []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := NewClient(herdr).CodexProcessStart(ctx, "pane")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline: %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("process lookup did not stop promptly")
	}
}
