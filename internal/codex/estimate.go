package codex

import (
	"fmt"
	"log/slog"
)

const (
	autoReviewInputRateUSD     = 2.5
	autoReviewCacheReadRateUSD = 0.25
	autoReviewOutputRateUSD    = 15.0
)

// Retains known costs even when some records cannot be priced.
type Estimate struct {
	KnownUSD     float64
	EstimatedUSD float64
	HasUsage     bool
	Incomplete   bool
}

func NewEstimate(incomplete bool) *Estimate {
	return &Estimate{
		Incomplete: incomplete,
	}
}

func (estimate Estimate) String() string {
	if !estimate.HasUsage {
		return "…"
	}
	prefix := ""
	if estimate.Incomplete {
		prefix = "~"
	}
	if estimate.EstimatedUSD != 0 {
		return fmt.Sprintf("%s$%.2f(+$%.2f)", prefix, estimate.KnownUSD, estimate.EstimatedUSD)
	}
	return fmt.Sprintf("%s$%.2f", prefix, estimate.KnownUSD)
}

// Prices each event using its active model and request counters.
func (usage *RolloutUsage) Estimate() Estimate {
	result := NewEstimate(!usage.Complete)
	result.HasUsage = usage.Totals.HasCounters()
	if usage.UsageBeforeModel && usage.ForkBaselineEligible {
		if _, err := RatesForModel(usage.FirstModel); err != nil && normalizeModel(usage.FirstModel) != "codex-auto-review" {
			slog.Warn("resolve rates for first Codex model", "model", usage.FirstModel, "error", err)
			result.Incomplete = true
		}
	}
	var previous TokenUsage
	for _, event := range usage.TokenCounts {
		prior := previous
		previous = event.Total
		if event.CounterReset || event.LastUsageInvalid {
			continue
		}
		billable := event.Total.Subtract(prior)
		if event.Last != nil {
			billable = *event.Last
		}
		if !billable.HasCounters() {
			continue
		}
		if event.Model == "" && usage.ForkBaselineEligible {
			continue
		}
		model := normalizeModel(event.Model)
		rates, err := RatesForModel(model)
		estimated := model == "codex-auto-review"
		if estimated {
			rates = NewRates(autoReviewInputRateUSD, autoReviewCacheReadRateUSD, 0, autoReviewOutputRateUSD)
		}
		if err != nil && !estimated {
			slog.Warn("resolve rates for Codex model", "model", model, "error", err)
			result.Incomplete = true
			continue
		}
		effectiveRates := *rates
		if longContextModels[model] && event.Last != nil && billable.ExceedsLongContextThreshold() {
			effectiveRates = effectiveRates.WithLongContextPremium()
		}
		cost, err := billable.CostUSD(effectiveRates)
		if err != nil {
			slog.Warn("calculate Codex token cost", "model", model, "error", err)
			result.Incomplete = true
			continue
		}
		if estimated {
			result.EstimatedUSD += cost
		} else {
			result.KnownUSD += cost
		}
	}
	return *result
}
