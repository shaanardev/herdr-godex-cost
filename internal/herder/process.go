package herder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CodexProcessStart reads process identity from Herdr and the local start time
// using ps, available on both supported platforms (Linux and macOS).
func (c Client) CodexProcessStart(ctx context.Context, paneID string) (time.Time, error) {
	info, err := c.ReadPaneProcessInfo(ctx, paneID)
	if err != nil {
		return time.Time{}, err
	}
	pid := uint32(0)
	for _, process := range info.ProcessInfo.ForegroundProcesses {
		if !strings.EqualFold(process.Name, "codex") {
			continue
		}
		if pid != 0 {
			return time.Time{}, fmt.Errorf("multiple foreground Codex processes")
		}
		pid = process.PID
	}
	if pid == 0 {
		return time.Time{}, fmt.Errorf("no foreground Codex process")
	}
	command := exec.CommandContext(ctx, "ps", "-p", strconv.FormatUint(uint64(pid), 10), "-o", "lstart=")
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return time.Time{}, ctx.Err()
		}
		return time.Time{}, fmt.Errorf("read Codex process start: %w", err)
	}
	stamp, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(strings.Fields(string(output)), " "), time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Codex process start: %w", err)
	}
	return stamp, nil
}
