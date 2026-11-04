package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Per-request input count above which
// supported models apply long-context pricing. A request at the threshold
// still uses standard rates; cumulative session totals must not be used.
const LongContextInputTokenThreshold = 272_000

// Holds the normalized token count reported by one Codex token_count event.
// Cache writes are tracked as a total and split across the 30-minute and
// 12-hour windows.
// Rollouts with only the legacy flat counter are treated as 30-minute writes, because no lifetime split was recorded.
type TokenUsage struct {
	InputTokens           uint64 `json:"input_tokens"`
	CachedInputTokens     uint64 `json:"cached_input_tokens"`
	CacheWriteInputTokens uint64 `json:"cache_write_input_tokens"`
	CacheWrite30mTokens   uint64 `json:"cache_write_30m_tokens"`
	CacheWrite12hTokens   uint64 `json:"cache_write_12h_tokens"`
	OutputTokens          uint64 `json:"output_tokens"`
}

// Reports whether these request counters exceed
// the long-context input threshold. Model eligibility is checked separately.
func (usage TokenUsage) ExceedsLongContextThreshold() bool {
	return usage.InputTokens > LongContextInputTokenThreshold
}

// Reports whether any cumulative counter decreased from prior.
func (usage TokenUsage) RegressedFrom(prior TokenUsage) bool {
	return usage.InputTokens < prior.InputTokens ||
		usage.CachedInputTokens < prior.CachedInputTokens ||
		usage.CacheWriteInputTokens < prior.CacheWriteInputTokens ||
		usage.CacheWrite12hTokens < prior.CacheWrite12hTokens ||
		usage.OutputTokens < prior.OutputTokens
}

// Reports whether any cumulative counter differs from prior.
func (usage TokenUsage) ChangedFrom(prior TokenUsage) bool {
	return usage.InputTokens != prior.InputTokens ||
		usage.CachedInputTokens != prior.CachedInputTokens ||
		usage.CacheWriteInputTokens != prior.CacheWriteInputTokens ||
		usage.CacheWrite12hTokens != prior.CacheWrite12hTokens ||
		usage.OutputTokens != prior.OutputTokens
}

// Reports whether at least one cumulative counter is nonzero.
func (usage TokenUsage) HasCounters() bool {
	return usage.InputTokens != 0 || usage.CachedInputTokens != 0 ||
		usage.CacheWriteInputTokens != 0 || usage.CacheWrite12hTokens != 0 ||
		usage.OutputTokens != 0
}

type tokenUsageWire struct {
	InputTokens           json.RawMessage `json:"input_tokens"`
	CachedInputTokens     json.RawMessage `json:"cached_input_tokens"`
	CacheWriteInputTokens json.RawMessage `json:"cache_write_input_tokens"`
	InputTokensDetails    json.RawMessage `json:"input_tokens_details"`
	OutputTokens          json.RawMessage `json:"output_tokens"`
}

type inputTokensDetailsWire struct {
	CachedTokens            json.RawMessage `json:"cached_tokens"`
	CacheWriteTokens        json.RawMessage `json:"cache_write_tokens"`
	CacheWriteTokensDetails json.RawMessage `json:"cache_write_tokens_details"`
}

type cacheWriteTokensDetailsWire struct {
	TTL30mTokens json.RawMessage `json:"ttl_30m_tokens"`
	TTL12hTokens json.RawMessage `json:"ttl_12h_tokens"`
}

// Decodes a flat or Responses-style token usage object and
// validates the counter relationships. Missing counters default to zero;
// explicit nulls and non-integer values are invalid.
func ParseTokenUsage(raw json.RawMessage) (*TokenUsage, error) {
	var wire tokenUsageWire
	if err := decodeUsageObject(raw, &wire); err != nil {
		return nil, fmt.Errorf("decode token usage: %w", err)
	}

	result := &TokenUsage{}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
		dst  *uint64
	}{
		{"input_tokens", wire.InputTokens, &result.InputTokens}, {"cached_input_tokens", wire.CachedInputTokens, &result.CachedInputTokens},
		{"cache_write_input_tokens", wire.CacheWriteInputTokens, &result.CacheWriteInputTokens}, {"output_tokens", wire.OutputTokens, &result.OutputTokens},
	} {
		if len(field.raw) == 0 {
			continue
		}
		value, err := parseCounter(field.raw)
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", field.name, err)
		}
		*field.dst = value
	}

	if len(wire.InputTokensDetails) == 0 {
		result.CacheWrite30mTokens = result.CacheWriteInputTokens
	} else {
		var details inputTokensDetailsWire
		if err := decodeUsageObject(wire.InputTokensDetails, &details); err != nil {
			return nil, fmt.Errorf("decode input_tokens_details: %w", err)
		}
		if len(details.CachedTokens) != 0 {
			nestedValue, err := parseCounter(details.CachedTokens)
			if err != nil {
				return nil, fmt.Errorf("decode cached_tokens: %w", err)
			}
			if len(wire.CachedInputTokens) != 0 && nestedValue != result.CachedInputTokens {
				return nil, errors.New("cached_tokens does not match cached_input_tokens")
			}
			result.CachedInputTokens = nestedValue
		}
		if len(details.CacheWriteTokens) != 0 {
			nestedValue, err := parseCounter(details.CacheWriteTokens)
			if err != nil {
				return nil, fmt.Errorf("decode cache_write_tokens: %w", err)
			}
			if len(wire.CacheWriteInputTokens) != 0 && nestedValue != result.CacheWriteInputTokens {
				return nil, errors.New("cache_write_tokens does not match cache_write_input_tokens")
			}
			result.CacheWriteInputTokens = nestedValue
		}

		if len(details.CacheWriteTokensDetails) == 0 {
			result.CacheWrite30mTokens = result.CacheWriteInputTokens
		} else {
			if len(details.CacheWriteTokens) == 0 {
				return nil, errors.New("cache_write_tokens_details requires cache_write_tokens")
			}
			var split cacheWriteTokensDetailsWire
			if err := decodeUsageObject(details.CacheWriteTokensDetails, &split); err != nil {
				return nil, fmt.Errorf("decode cache_write_tokens_details: %w", err)
			}
			writeTotal := result.CacheWriteInputTokens
			write30m, err := parseCounter(split.TTL30mTokens)
			if err != nil {
				return nil, fmt.Errorf("decode 30-minute cache writes: %w", err)
			}
			write12h, err := parseCounter(split.TTL12hTokens)
			if err != nil {
				return nil, fmt.Errorf("decode 12-hour cache writes: %w", err)
			}
			if writeTotal != result.CacheWriteInputTokens || write30m > writeTotal || write12h != writeTotal-write30m {
				return nil, errors.New("cache write counter relationships are inconsistent")
			}
			result.CacheWrite30mTokens = write30m
			result.CacheWrite12hTokens = write12h
		}
	}

	if err := result.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

func decodeUsageObject(raw json.RawMessage, destination any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("expected JSON object")
	}
	if err := json.Unmarshal(trimmed, destination); err != nil {
		return fmt.Errorf("decode JSON object: %w", err)
	}
	return nil
}

func parseCounter(raw json.RawMessage) (uint64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, errors.New("counter is missing or null")
	}
	var value uint64
	// encoding/json rejects negative, fractional, exponent and nonnumeric values.
	if bytes.Equal(bytes.TrimSpace(raw), []byte("-0")) {
		return 0, nil
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("decode unsigned token counter: %w", err)
	}
	return value, nil
}

// Validate checks the relationships between normalized counters. It also applies
// to deltas, which can be inconsistent even when both cumulative totals are valid.
func (usage TokenUsage) Validate() error {
	if usage.CachedInputTokens > usage.InputTokens ||
		usage.CacheWriteInputTokens > usage.InputTokens-usage.CachedInputTokens {
		return errors.New("cached and cache-write tokens exceed input tokens")
	}
	if usage.CacheWrite12hTokens > usage.CacheWriteInputTokens {
		return errors.New("12-hour cache writes exceed total cache writes")
	}
	return nil
}

// CostUSD prices these counters using the supplied effective rates. Model and
// long-context policies are applied by the caller before supplying rates.
func (usage TokenUsage) CostUSD(rates Rates) (float64, error) {
	if err := usage.Validate(); err != nil {
		return 0, err
	}
	cost := float64(usage.InputTokens-usage.CachedInputTokens)*rates.InputUSD +
		float64(usage.CachedInputTokens)*rates.CacheReadUSD +
		float64(usage.OutputTokens)*rates.OutputUSD
	if rates.CacheWrite30mUSD > 0 {
		cost += float64(usage.CacheWriteInputTokens)*(rates.CacheWrite30mUSD-rates.InputUSD) +
			float64(usage.CacheWrite12hTokens)*1.25*rates.InputUSD
	}
	return cost / 1_000_000, nil
}

// Subtract requires monotonically increasing cumulative counters.
func (usage TokenUsage) Subtract(prior TokenUsage) TokenUsage {
	return TokenUsage{
		InputTokens:           usage.InputTokens - prior.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens - prior.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens - prior.CacheWriteInputTokens,
		CacheWrite12hTokens:   usage.CacheWrite12hTokens - prior.CacheWrite12hTokens,
		OutputTokens:          usage.OutputTokens - prior.OutputTokens,
	}
}
