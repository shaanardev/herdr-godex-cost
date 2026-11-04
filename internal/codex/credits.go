package codex

// Standard credits per million tokens, checked 2026-11-05 against
// https://learn.chatgpt.com/docs/pricing#token-rates . Credit billing has no
// separate cache-write charge. Models without published rates remain unpriced.
// Rates uses the same input/cache/output arithmetic for both units.
var creditRatesByModel = map[string]Rates{
	"gpt-6-astra":   {InputUSD: 250, CacheReadUSD: 25, OutputUSD: 1250},
	"gpt-6.1-sol":   {InputUSD: 50, CacheReadUSD: 2.5, OutputUSD: 250},
	"gpt-6.1-terra": {InputUSD: 25, CacheReadUSD: 1.25, OutputUSD: 125},
	"gpt-6-sol":     {InputUSD: 50, CacheReadUSD: 5, OutputUSD: 250},
	"gpt-6-luna":    {InputUSD: 2.5, CacheReadUSD: .25, OutputUSD: 12.5},
	"gpt-5.6-sol":   {InputUSD: 100, CacheReadUSD: 10, OutputUSD: 500},
	"gpt-5.6-terra": {InputUSD: 50, CacheReadUSD: 5, OutputUSD: 300},
	"gpt-5.6-luna":  {InputUSD: 5, CacheReadUSD: .5, OutputUSD: 30},
	"gpt-5.6-cyber": {InputUSD: 312.5, CacheReadUSD: 31.25, OutputUSD: 1875},
}
