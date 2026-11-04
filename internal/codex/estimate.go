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
	Credits           float64
	CreditsIncomplete bool
	KnownUSD          float64
	EstimatedUSD      float64
	HasUsage          bool
	Incomplete        bool
}

func NewEstimate(incomplete bool) *Estimate {
	return &Estimate{
		Incomplete: incomplete,
	}
}

// Add combines another session's amounts and preserves usage and completeness flags.
func (estimate *Estimate) Add(other Estimate) {
	estimate.Credits += other.Credits
	estimate.CreditsIncomplete = estimate.CreditsIncomplete || other.CreditsIncomplete
	estimate.KnownUSD += other.KnownUSD
	estimate.EstimatedUSD += other.EstimatedUSD
	estimate.HasUsage = estimate.HasUsage || other.HasUsage
	estimate.Incomplete = estimate.Incomplete || other.Incomplete
}

// TotalUSD includes both normally priced usage and estimated auto-review costs.
func (estimate Estimate) TotalUSD() float64 {
	return estimate.KnownUSD + estimate.EstimatedUSD
}

func (estimate Estimate) String() string {
	if !estimate.HasUsage {
		return "…"
	}
	prefix := ""
	if estimate.Incomplete {
		prefix = "~"
	}
	return fmt.Sprintf("%s$%.2f", prefix, estimate.TotalUSD())
}

// CreditString estimates Standard-speed credit usage; it is not an account balance.
func (estimate Estimate) CreditString() string {
	if !estimate.HasUsage {
		return "…"
	}
	suffix := ""
	if estimate.Incomplete || estimate.CreditsIncomplete {
		suffix = " (partial)"
	}
	return fmt.Sprintf("~%.2f credits%s", estimate.Credits, suffix)
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
		if creditRates, ok := creditRatesByModel[model]; ok {
			credits, creditErr := billable.CostUSD(creditRates)
			if creditErr != nil {
				result.CreditsIncomplete = true
			} else {
				result.Credits += credits
			}
		} else {
			result.CreditsIncomplete = true
		}
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
