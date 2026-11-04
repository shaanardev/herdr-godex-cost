package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	pluginContextEnv = "HERDR_PLUGIN_CONTEXT_JSON"
	pluginEventEnv   = "HERDR_PLUGIN_EVENT_JSON"
)

// paneContext resolves an explicit pane or the appropriate Herdr action/event
// payload. It owns precedence and missing-pane behavior, without CLI dependencies.
type paneContext struct {
	Command      string
	ExplicitPane string
}

func NewPaneContext(command, explicitPane string) *paneContext {
	return &paneContext{Command: command, ExplicitPane: explicitPane}
}

func (pane paneContext) resolve() (string, error) {
	if pane.Command == "startup" {
		return "", nil
	}
	if id := strings.TrimSpace(pane.ExplicitPane); id != "" {
		return id, nil
	}
	environment := pluginContextEnv
	if pane.Command == "event" {
		environment = pluginEventEnv
	}
	var payload struct {
		PaneID        string `json:"pane_id"`
		FocusedPaneID string `json:"focused_pane_id"`
		Data          struct {
			PaneID string `json:"pane_id"`
		} `json:"data"`
	}
	if raw := os.Getenv(environment); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return "", fmt.Errorf("decode %s: %w", environment, err)
		}
	}
	candidates := []string{payload.FocusedPaneID, payload.PaneID, payload.Data.PaneID}
	if pane.Command == "event" {
		candidates = []string{payload.Data.PaneID, payload.PaneID}
	}
	for _, candidate := range candidates {
		if id := strings.TrimSpace(candidate); id != "" {
			return id, nil
		}
	}
	if pane.Command == "event" {
		return "", nil
	}
	return "", errors.New("pane is required: pass --pane or run from a Herdr plugin action")
}
