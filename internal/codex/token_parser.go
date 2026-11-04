package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Holds Codex's cumulative counters and optional per-request counters together.
// A reset marker records cumulative counter resets; the
// event remains available so a later stage can reset its delta baseline.
type TokenCountEvent struct {
	Model            string
	Total            TokenUsage
	Last             *TokenUsage
	LastUsageInvalid bool
	CounterReset     bool
}

// Contains session-tree metadata and normalized token events
// parsed from one Codex rollout JSONL file. Complete describes whether the
// rollout could be parsed and its counters remained coherent. Unsupported
// models are a pricing concern and are deliberately not interpreted here.
type RolloutUsage struct {
	SessionID            string
	FirstModel           string
	ParentThreadID       string
	ForkedFromID         string
	ThreadSource         string
	TokenCounts          []TokenCountEvent
	Totals               TokenUsage
	HasTokenUsage        bool
	Complete             bool
	UsageBeforeModel     bool
	ForkBaselineEligible bool
}

type rolloutParser struct {
	usage                RolloutUsage
	model                string
	modelSeen            bool
	sessionMetaSeen      bool
	pendingPreModelUsage bool
	previous             *TokenUsage
}

// Reads session metadata, turn context, and token count events from a
// Codex rollout. It normalizes counters but does not estimate or price usage.
func ParseRollout(ctx context.Context, input io.Reader) (*RolloutUsage, error) {
	parser := rolloutParser{usage: RolloutUsage{Complete: true}}
	reader := bufio.NewReader(input)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := reader.ReadBytes('\n')
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(line) != 0 {
			var event rolloutEvent
			if err := json.Unmarshal(line, &event); err != nil {
				// Codex may be writing the final record when the reader opens the
				// file. An invalid newline-terminated record is damaged data; an
				// invalid unterminated tail is an incomplete append and is ignored.
				if line[len(line)-1] == '\n' {
					parser.usage.Complete = false
				}
			} else {
				parser.processEvent(event)
			}
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read Codex rollout: %w", readErr)
		}
	}

	if !parser.usage.HasTokenUsage || (parser.pendingPreModelUsage && !parser.modelSeen) {
		parser.usage.Complete = false
	}
	return &parser.usage, nil
}

func (parser *rolloutParser) processEvent(event rolloutEvent) {
	switch event.Type {
	case "session_meta":
		if parser.sessionMetaSeen {
			return
		}
		var metadata SessionMetadata
		if err := decodeUsageObject(event.Payload, &metadata); err != nil {
			parser.usage.Complete = false
			return
		}
		parser.sessionMetaSeen = true
		parser.usage.SessionID = metadata.ID
		parser.usage.ParentThreadID = metadata.ParentThreadID
		parser.usage.ForkedFromID = metadata.ForkedFromID
		parser.usage.ThreadSource = metadata.ThreadSource
		parser.usage.ForkBaselineEligible = metadata.ThreadSource == "subagent" &&
			ValidSessionID(metadata.ParentThreadID) && metadata.ForkedFromID == metadata.ParentThreadID
	case "turn_context":
		var context struct {
			Model string `json:"model"`
		}
		if err := decodeUsageObject(event.Payload, &context); err != nil {
			parser.usage.Complete = false
			return
		}
		if context.Model == "" {
			return
		}
		if !parser.modelSeen {
			parser.usage.FirstModel = context.Model
		}
		parser.model = context.Model
		parser.modelSeen = true
		parser.pendingPreModelUsage = false
	case "event_msg":
		var message struct {
			Type string          `json:"type"`
			Info json.RawMessage `json:"info"`
		}
		if err := decodeUsageObject(event.Payload, &message); err != nil {
			parser.usage.Complete = false
			return
		}
		if message.Type != "token_count" {
			return
		}
		if len(message.Info) == 0 || bytes.Equal(bytes.TrimSpace(message.Info), []byte("null")) {
			return
		}
		var info struct {
			Total json.RawMessage `json:"total_token_usage"`
			Last  json.RawMessage `json:"last_token_usage"`
		}
		if err := decodeUsageObject(message.Info, &info); err != nil {
			parser.usage.Complete = false
			return
		}
		if len(info.Total) == 0 || bytes.Equal(bytes.TrimSpace(info.Total), []byte("null")) {
			return
		}
		total, err := ParseTokenUsage(info.Total)
		if err != nil {
			parser.usage.Complete = false
			return
		}
		reset := parser.previous != nil && total.RegressedFrom(*parser.previous)
		if reset {
			parser.usage.Complete = false
		}
		hasUsage := (parser.previous == nil && total.HasCounters()) ||
			(parser.previous != nil && !reset && total.ChangedFrom(*parser.previous))
		if hasUsage && parser.model == "" {
			parser.usage.UsageBeforeModel = true
			if !parser.modelSeen && parser.usage.ForkBaselineEligible {
				parser.pendingPreModelUsage = true
			} else {
				parser.usage.Complete = false
			}
		}
		var last *TokenUsage
		var lastErr error
		if len(info.Last) > 0 && !bytes.Equal(bytes.TrimSpace(info.Last), []byte("null")) {
			last, lastErr = ParseTokenUsage(info.Last)
		}
		if lastErr != nil {
			parser.usage.Complete = false
		}
		parser.usage.TokenCounts = append(parser.usage.TokenCounts, TokenCountEvent{
			Model: parser.model, Total: *total, Last: last,
			LastUsageInvalid: lastErr != nil, CounterReset: reset,
		})
		parser.usage.Totals = *total
		parser.usage.HasTokenUsage = true
		parser.previous = total
	}
}
