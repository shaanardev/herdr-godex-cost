package codex

import "strings"

// Rates are Standard API USD per million text tokens. CacheWrite30mUSD is the
// 30-minute prompt-cache write rate. The pricing table was checked on
// 2026-11-04; GPT-5.6 Sol's promotional rates are currently listed through
// 2026-11-21.
type Rates struct {
	InputUSD         float64
	CacheReadUSD     float64
	CacheWrite30mUSD float64
	OutputUSD        float64
}

var GPT6AstraRates = Rates{
	InputUSD:         10.00,
	CacheReadUSD:     1.00,
	CacheWrite30mUSD: 12.50,
	OutputUSD:        50.00,
}

var GPT61SolRates = Rates{
	InputUSD:         2.00,
	CacheReadUSD:     0.10,
	CacheWrite30mUSD: 2.50,
	OutputUSD:        10.00,
}

var GPT61TerraRates = Rates{
	InputUSD:         1.00,
	CacheReadUSD:     0.05,
	CacheWrite30mUSD: 1.25,
	OutputUSD:        5.00,
}

var GPT6SolRates = Rates{
	InputUSD:         2.00,
	CacheReadUSD:     0.20,
	CacheWrite30mUSD: 2.50,
	OutputUSD:        10.00,
}

var GPT6LunaRates = Rates{
	InputUSD:         0.10,
	CacheReadUSD:     0.01,
	CacheWrite30mUSD: 0.125,
	OutputUSD:        0.50,
}

var GPT56SolRates = Rates{
	InputUSD:         4.00,
	CacheReadUSD:     0.40,
	CacheWrite30mUSD: 5.00,
	OutputUSD:        20.00,
}

var GPT56CyberRates = Rates{
	InputUSD:         12.50,
	CacheReadUSD:     1.25,
	CacheWrite30mUSD: 15.625,
	OutputUSD:        75.00,
}

var ratesByModel = map[string]Rates{
	"gpt-6-astra":              GPT6AstraRates,
	"gpt-6.1-sol":              GPT61SolRates,
	"gpt-6.1-terra":            GPT61TerraRates,
	"gpt-6-sol":                GPT6SolRates,
	"gpt-6-luna":               GPT6LunaRates,
	"gpt-5.6-sol":              GPT56SolRates,
	"gpt-5.6-cyber":            GPT56CyberRates,
	"gpt-daybreak-blue-latest": GPT56SolRates,
	"gpt-daybreak-red-latest":  GPT56CyberRates,
}

func RatesForModel(model string) (Rates, bool) {
	rates, ok := ratesByModel[strings.ToLower(strings.TrimSpace(model))]
	return rates, ok
}
