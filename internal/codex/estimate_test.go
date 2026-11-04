package codex

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

func TestRolloutEstimates(t *testing.T) {
	const model = `{"type":"turn_context","payload":{"model":"gpt-6.1-sol"}}` + "\n"
	const first = `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"output_tokens":100000}}}}` + "\n"
	tests := []struct {
		name, log        string
		known, estimated float64
		incomplete       bool
	}{
		{"ordinary", model + first, 3, 0, false},
		{"model switch", model + first + `{"type":"turn_context","payload":{"model":"gpt-6.1-terra"}}` + "\n" + `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2000000,"output_tokens":200000}}}}` + "\n", 4.5, 0, false},
		{"unknown", strings.ReplaceAll(model, "gpt-6.1-sol", "unknown") + first, 0, 0, true},
		{"auto review", strings.ReplaceAll(model, "gpt-6.1-sol", "codex-auto-review") + first, 0, 4, false},
		{"bad total", model + first + `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":[]}}}` + "\n", 3, 0, true},
		{"inconsistent delta", model + first + `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"cached_input_tokens":1,"output_tokens":100000}}}}` + "\n", 3, 0, true},
		{"bad line", model + first + "bad\n", 3, 0, true},
		{"unfinished tail", model + first + `{"type":`, 3, 0, false},
		{"reset", model + first + `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1}}}}` + "\n", 3, 0, true},
		{"long context mixed writes", model + `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":300000,"cached_input_tokens":10000,"cache_write_input_tokens":20000},"last_token_usage":{"input_tokens":300000,"input_tokens_details":{"cached_tokens":10000,"cache_write_tokens":20000,"cache_write_tokens_details":{"ttl_30m_tokens":10000,"ttl_12h_tokens":10000}}}}}}` + "\n", 1.232, 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usage, err := ParseRollout(context.Background(), strings.NewReader(test.log))
			if err != nil {
				t.Fatal(err)
			}
			got := usage.Estimate()
			if math.Abs(got.KnownUSD-test.known) > 1e-9 || math.Abs(got.EstimatedUSD-test.estimated) > 1e-9 || got.Incomplete != test.incomplete {
				t.Fatalf("got %+v; want known=%g estimated=%g incomplete=%v", got, test.known, test.estimated, test.incomplete)
			}
		})
	}
}

func TestTokenUsageValidation(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"input_tokens":null}`, `{"input_tokens":-1}`, `{"input_tokens":1.2}`, `{"input_tokens":true}`, `{"input_tokens":18446744073709551616}`, `{"input_tokens":2,"cached_input_tokens":3}`, `{"cached_input_tokens":1,"input_tokens_details":{"cached_tokens":2}}`} {
		if _, err := ParseTokenUsage([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func TestRolloutReadError(t *testing.T) {
	_, err := ParseRollout(context.Background(), brokenReader{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lost read error: %v", err)
	}
}

func TestLongContextPricingBoundaries(t *testing.T) {
	for _, test := range []struct {
		name      string
		model     string
		input     uint64
		lastUsage bool
		want      float64
	}{
		{"below threshold", "gpt-6.1-sol", 271999, true, .553998},
		{"at threshold", "gpt-6.1-sol", 272000, true, .554},
		{"above threshold", "gpt-6.1-sol", 272001, true, 1.103004},
		{"ineligible model", "gpt-5.4-mini", 272001, true, .20850075},
		{"cumulative total alone", "gpt-6.1-sol", 272001, false, .554002},
	} {
		t.Run(test.name, func(t *testing.T) {
			counters := fmt.Sprintf(`{"input_tokens":%d,"output_tokens":1000}`, test.input)
			last := ""
			if test.lastUsage {
				last = `,"last_token_usage":` + counters
			}
			log := fmt.Sprintf(`{"type":"turn_context","payload":{"model":%q}}`+"\n"+
				`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":%s%s}}}`+"\n", test.model, counters, last)
			usage, err := ParseRollout(context.Background(), strings.NewReader(log))
			if err != nil {
				t.Fatal(err)
			}
			got := usage.Estimate()
			if got.Incomplete || math.Abs(got.KnownUSD-test.want) > 1e-9 {
				t.Fatalf("got %+v; want $%g", got, test.want)
			}
		})
	}
}

func TestEstimateDisplaysOneTotalIncludingAutoReview(t *testing.T) {
	for _, test := range []struct {
		name     string
		estimate Estimate
		want     string
		total    float64
	}{
		{"session plus review", Estimate{KnownUSD: 3, EstimatedUSD: 1, HasUsage: true}, "$4.00", 4},
		{"review alone", Estimate{EstimatedUSD: 1, HasUsage: true}, "$1.00", 1},
		{"partial total", Estimate{KnownUSD: 3, EstimatedUSD: 1, HasUsage: true, Incomplete: true}, "~$4.00", 4},
		{"no usage", Estimate{}, "…", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.estimate.TotalUSD(); got != test.total {
				t.Fatalf("total=%g; want %g", got, test.total)
			}
			if got := test.estimate.String(); got != test.want {
				t.Fatalf("display=%q; want %q", got, test.want)
			}
		})
	}
}
