package codex

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestCreditEstimateUsesCreditRates(t *testing.T) {
	// Credits have no cache-write premium or API long-context multiplier.
	log := `{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}
{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"cached_input_tokens":200000,"cache_write_input_tokens":100000,"output_tokens":100000},"last_token_usage":{"input_tokens":1000000,"cached_input_tokens":200000,"cache_write_input_tokens":100000,"output_tokens":100000}}}}
`
	usage, err := ParseRollout(context.Background(), strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	got := usage.Estimate()
	// 800k ordinary input + 200k cached + 100k output = 65.5 credits.
	if math.Abs(got.Credits-65.5) > 1e-9 || got.CreditsIncomplete || got.CreditString() != "~65.50 credits" {
		t.Fatalf("unexpected credits: %+v (%s)", got, got.CreditString())
	}
	usage.TokenCounts[0].Model = "codex-auto-review"
	got = usage.Estimate()
	if !got.CreditsIncomplete || got.CreditString() != "~0.00 credits (partial)" {
		t.Fatalf("unpublished credit rate accepted: %+v", got)
	}
}
