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
	binaryPath    string
	usageInterval time.Duration
}

func NewClient(binary string, usageInterval time.Duration) *Client {
	return &Client{binaryPath: binary, usageInterval: usageInterval}
}

func (c Client) runCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	out, err := cmd.Output()
	if err != nil {
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
	return response, nil
}

func (c Client) ReadPane(ctx context.Context, pane string) ([]byte, error) {
	return c.runCommand(ctx, "agent", "read", pane, "--source", "visible", "--lines", "100")
}

func (c Client) UpdatePane(ctx context.Context, pane string, cost *float32, seq int64) error {
	var formattedCost string
	if cost == nil {
		formattedCost = "unpriced"
	} else {
		formattedCost = fmt.Sprintf("%.2f", *cost)
	}

	creditDisplay := fmt.Sprintf("cost=%s", formattedCost)
	totalInterval := strconv.FormatInt((c.usageInterval * 3).Milliseconds(), 10)

	// herdr pane report-metadata is a quiet command so we can ignore the stdout result
	_, err := c.runCommand(ctx, "pane", "report-metadata", pane, "--source", Source,
		"--token", creditDisplay, "--seq", fmt.Sprintf("%d", seq), "--ttl-ms", totalInterval)

	return err
}

func (c Client) ReadPaneProcessInfo(ctx context.Context, pane string) (*ProcessInfoResult, error) {
	response, err := c.runTypedCommand[ProcessInfoResult](ctx, "pane", "process-info", "--pane", pane)
	if err != nil {
		return nil, err
	}

	return &response.Result, nil
}

func (c Client) ReadAgentList(ctx context.Context) (*AgentListResult, error) {
	response, err := c.runTypedCommand[AgentListResult](ctx, "agent", "list")
	if err != nil {
		return nil, err
	}

	return &response.Result, nil
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
