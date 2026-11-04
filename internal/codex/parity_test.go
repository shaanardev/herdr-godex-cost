package codex

import (
	"context"
	_ "embed"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

//go:embed testdata/pricing.json
var pricingJSON []byte

func TestUpstreamPricingParity(t *testing.T) {
	var golden struct {
		Source   string
		Fixtures []struct {
			Log              string
			Known, Estimated float64
			Complete         bool
			HasUsage         bool `json:"has_usage"`
		}
	}
	if err := json.Unmarshal(pricingJSON, &golden); err != nil {
		t.Fatal(err)
	}
	if golden.Source == "" || len(golden.Fixtures) == 0 {
		t.Fatal("upstream fixture set is empty or has no provenance")
	}
	for i, f := range golden.Fixtures {
		u, err := ParseRollout(context.Background(), strings.NewReader(f.Log))
		if err != nil {
			t.Fatal(err)
		}
		got := u.Estimate()
		if math.Abs(got.KnownUSD-f.Known) > 1e-8 || math.Abs(got.EstimatedUSD-f.Estimated) > 1e-8 || got.Incomplete == f.Complete || got.HasUsage != f.HasUsage {
			t.Errorf("fixture %d: got %+v; want known=%g estimated=%g complete=%v hasUsage=%v; log=%s", i, got, f.Known, f.Estimated, f.Complete, f.HasUsage, f.Log)
		}
	}
}
