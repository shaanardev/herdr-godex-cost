package herder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const Source = "herdr-godex-cost"

type Client struct {
	binaryPath string
}

func NewClient(binary string) *Client {
	return &Client{binaryPath: binary}
}

func (c Client) runCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("herdr %q: %w", args[0], ctx.Err())
		}
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			detail := strings.TrimSpace(string(exitErr.Stderr))
			if detail != "" {
				return nil, fmt.Errorf(
					"herdr %q exited %d: %s: %w",
					args[0], exitErr.ExitCode(), detail, err,
				)
			}
		}
		return nil, fmt.Errorf("herdr failed %q: %w", args[0], err)
	}

	return out, nil
}

func (c Client) runTypedCommand[T any](ctx context.Context, args ...string) (*CLIResponse[T], error) {
	res, err := c.runCommand(ctx, args...)
	if err != nil {
		return nil, err
	}

	response := new(CLIResponse[T])
	if err := json.Unmarshal(res, response); err != nil {
		return nil, fmt.Errorf("invalid herdr output %q: %w", args[0], err)
	}
	if response.Result == nil {
		return nil, fmt.Errorf("herdr %q returned no result", args[0])
	}
	return response, nil
}

func (c Client) ReadPane(ctx context.Context, pane string) ([]byte, error) {
	return c.runCommand(ctx, "pane", "read", pane, "--source", "visible", "--lines", "16", "--format", "text")
}

// UpdatePane publishes an already formatted estimate. Formatting belongs to pricing.
func (c Client) UpdatePane(ctx context.Context, pane, display string) error {
	_, err := c.runCommand(ctx, "pane", "report-metadata", pane, "--source", Source,
		"--token", "cost="+display, "--seq", strconv.FormatInt(time.Now().UnixNano(), 10))
	return err
}

func (c Client) ReadPaneInfo(ctx context.Context, pane string) (*PaneInfo, error) {
	response, err := c.runTypedCommand[struct {
		Pane PaneInfo `json:"pane"`
	}](ctx, "pane", "get", pane)
	if err != nil {
		return nil, err
	}
	return &response.Result.Pane, nil
}

func (c Client) ReadPaneProcessInfo(ctx context.Context, pane string) (*ProcessInfoResult, error) {
	response, err := c.runTypedCommand[ProcessInfoResult](ctx, "pane", "process-info", "--pane", pane)
	if err != nil {
		return nil, err
	}

	return response.Result, nil
}

func (c Client) ReadAgentList(ctx context.Context) (*AgentListResult, error) {
	response, err := c.runTypedCommand[AgentListResult](ctx, "agent", "list")
	if err != nil {
		return nil, err
	}

	return response.Result, nil
}

func (c Client) ReadApiSnapshot(ctx context.Context) (*SessionSnapshotResult, error) {
	response, err := c.runTypedCommand[SessionSnapshotResult](ctx, "api", "snapshot")
	if err != nil {
		return nil, err
	}

	return &SessionSnapshotResult{
		Type:     response.Result.Type,
		Snapshot: response.Result.Snapshot,
	}, nil
}

// ReadPanes returns the pane snapshots used to initialize the plugin at startup.
func (c Client) ReadPanes(ctx context.Context) ([]PaneInfo, error) {
	response, err := c.runTypedCommand[struct {
		Panes []PaneInfo `json:"panes"`
	}](ctx, "pane", "list")
	if err != nil {
		return nil, err
	}
	return response.Result.Panes, nil
}
