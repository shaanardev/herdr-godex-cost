package herder

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandErrors(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho 'pane disappeared' >&2\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client := NewClient(executable)
	_, err := client.ReadPaneInfo(context.Background(), "p")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(err.Error(), "pane disappeared") {
		t.Fatalf("lost command failure: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ReadPaneInfo(ctx, "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestMissingResult(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s' '{\"error\":\"unknown pane\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(executable).ReadPaneInfo(context.Background(), "p"); err == nil {
		t.Fatal("accepted error envelope as empty pane")
	}
}

func TestPaneTitleAndVisibleTextContract(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "herdr")
	script := `#!/bin/sh
case "$*" in
 "pane get pane") printf '%s\n' '{"result":{"pane":{"terminal_title_stripped":"Audit queue | work"}}}' ;;
 "pane read pane --source visible --lines 16 --format text") printf '%s' 'session text' ;;
 *) echo "incorrect Herdr arguments: $*" >&2; exit 7 ;;
esac
`
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	client := NewClient(executable)
	pane, err := client.ReadPaneInfo(context.Background(), "pane")
	if err != nil {
		t.Fatal(err)
	}
	if pane.Title != "Audit queue | work" {
		t.Fatalf("stripped title was lost: %+v", pane)
	}
	text, err := client.ReadPane(context.Background(), "pane")
	if err != nil || string(text) != "session text" {
		t.Fatalf("pane text=%q error=%v", text, err)
	}
}
