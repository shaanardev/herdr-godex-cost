package codex

import (
	"fmt"
	"strings"
)

// Standard API prices in USD per million text tokens. Prompt-cache writes use
// the 30-minute cache-write rate. The pricing table was checked on
// 2026-11-04; GPT-5.6 Sol's promotional rates are currently listed through
// 2026-11-21.
type Rates struct {
	InputUSD         float64
	CacheReadUSD     float64
	CacheWrite30mUSD float64
	OutputUSD        float64
}

func NewRates(inputUSD, cacheReadUSD, cacheWrite30mUSD, outputUSD float64) *Rates {
	return &Rates{
		InputUSD:         inputUSD,
		CacheReadUSD:     cacheReadUSD,
		CacheWrite30mUSD: cacheWrite30mUSD,
		OutputUSD:        outputUSD,
	}
}

const (
	longContextInputMultiplier  = 2.0
	longContextOutputMultiplier = 1.5
)

// WithLongContextPremium returns adjusted rates without changing the original.
// Input, cache reads and cache writes share the input multiplier.
func (rates Rates) WithLongContextPremium() Rates {
	rates.InputUSD *= longContextInputMultiplier
	rates.CacheReadUSD *= longContextInputMultiplier
	rates.CacheWrite30mUSD *= longContextInputMultiplier
	rates.OutputUSD *= longContextOutputMultiplier
	return rates
}

// Cache-write rates are zero for models without a separate write premium.
var ratesByModel = map[string]Rates{
	"gpt-6-astra":           {10, 1, 12.5, 50},
	"gpt-6.1-sol":           {2, .1, 2.5, 10},
	"gpt-6.1-terra":         {1, .05, 1.25, 5},
	"gpt-6-sol":             {2, .2, 2.5, 10},
	"gpt-6-luna":            {.1, .01, .125, .5},
	"gpt-5.6-sol":           {4, .4, 5, 20},
	"gpt-5.6-terra":         {2, .2, 2.5, 12},
	"gpt-5.6-luna":          {.2, .02, .25, 1.2},
	"gpt-5.6-cyber":         {12.5, 1.25, 15.625, 75},
	"gpt-5.5-cyber":         {12.5, 1.25, 0, 75},
	"gpt-5.5":               {5, .5, 0, 30},
	"gpt-5.5-pro":           {30, 30, 0, 180},
	"gpt-5.4":               {2.5, .25, 0, 15},
	"gpt-5.4-mini":          {.75, .075, 0, 4.5},
	"gpt-5.4-nano":          {.2, .02, 0, 1.25},
	"gpt-5.4-pro":           {30, 30, 0, 180},
	"chat-latest":           {5, .5, 0, 30},
	"gpt-5.3-codex":         {1.75, .175, 0, 14},
	"o3-deep-research":      {10, 2.5, 0, 40},
	"o4-mini-deep-research": {2, .5, 0, 8},
	"computer-use-preview":  {3, 3, 0, 12},
	"o4-mini":               {1.1, .275, 0, 4.4},
	"o4-mini-2025-04-16":    {1.1, .275, 0, 4.4},
}

var longContextModels = map[string]bool{
	"gpt-6-astra": true, "gpt-6.1-sol": true, "gpt-6.1-terra": true,
	"gpt-6-sol": true, "gpt-6-luna": true, "gpt-5.6-sol": true,
	"gpt-5.6-terra": true, "gpt-5.6-luna": true, "gpt-5.6-cyber": true,
	"gpt-5.5": true, "gpt-5.5-pro": true, "gpt-5.4": true, "gpt-5.4-pro": true,
}

func normalizeModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch model {
	case "gpt-5.6", "gpt-daybreak-blue-latest":
		return "gpt-5.6-sol"
	case "gpt-daybreak-red-latest":
		return "gpt-5.6-cyber"
	default:
		return model
	}
}

func RatesForModel(model string) (*Rates, error) {
	rates, ok := ratesByModel[normalizeModel(model)]
	if !ok {
		return nil, fmt.Errorf("unsupported Codex model %q", model)
	}
	return &rates, nil
}
