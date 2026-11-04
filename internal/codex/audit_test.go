package codex

import (
	"strings"
	"testing"
)

func TestTokenUpdatesWithoutRequestUsage(t *testing.T) {
	const model = `{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n"
	const count = `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000},"last_token_usage":null}}}` + "\n"
	log := `{"type":"event_msg","payload":{"type":"token_count","info":null}}` + "\n" + model +
		`{"type":"turn_context","payload":{}}` + "\n" + count +
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":null}}}` + "\n"
	usage, err := ParseRollout(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	estimate := usage.Estimate()
	if estimate.Incomplete || estimate.KnownUSD != 3 {
		t.Fatalf("null/missing optional usage damaged valid accounting: %+v", estimate)
	}
}

func TestForkBaselineRequiresPricedFirstModel(t *testing.T) {
	for _, model := range []string{"gpt-6.1-sol", "unknown"} {
		log := `{"type":"session_meta","payload":{"id":"child","thread_source":"subagent","parent_thread_id":"parent","forked_from_id":"parent"}}` + "\n" +
			`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000}}}}` + "\n" +
			`{"type":"turn_context","payload":{"model":"` + model + `"}}` + "\n"
		usage, err := ParseRollout(strings.NewReader(log))
		if err != nil {
			t.Fatal(err)
		}
		estimate := usage.Estimate()
		if estimate.KnownUSD != 0 || estimate.Incomplete != (model == "unknown") {
			t.Fatalf("fork baseline model %s: %+v", model, estimate)
		}
	}
}

func TestEstimateDisplayStates(t *testing.T) {
	newEstimate := func(knownUSD, estimatedUSD float64, hasUsage, incomplete bool) Estimate {
		estimate := NewEstimate(incomplete)
		estimate.KnownUSD = knownUSD
		estimate.EstimatedUSD = estimatedUSD
		estimate.HasUsage = hasUsage
		return *estimate
	}
	for _, test := range []struct {
		estimate Estimate
		want     string
	}{
		{*NewEstimate(false), "…"}, {newEstimate(1.25, 0, true, false), "$1.25"},
		{newEstimate(1.25, 0, true, true), "~$1.25"},
		{newEstimate(1.25, .5, true, false), "$1.75"},
	} {
		if got := test.estimate.String(); got != test.want {
			t.Errorf("display %q; want %q", got, test.want)
		}
	}
}
