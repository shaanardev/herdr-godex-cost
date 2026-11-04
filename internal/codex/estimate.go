package codex

import "fmt"

// Estimate retains known costs even when some records cannot be priced.
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

// Estimate prices each event using its active model and request counters.
func (usage *RolloutUsage) Estimate() Estimate {
	result := Estimate{HasUsage: usage.Totals.HasCounters(), Incomplete: !usage.Complete}
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
			rates = &Rates{InputUSD: 2.5, CacheReadUSD: .25, OutputUSD: 15}
		}
		if err != nil && !estimated {
			result.Incomplete = true
			continue
		}
		effectiveRates := *rates
		if longContextModels[model] && event.Last != nil && billable.ExceedsLongContextThreshold() {
			effectiveRates = effectiveRates.WithLongContextPremium()
		}
		cost, err := billable.CostUSD(effectiveRates)
		if err != nil {
			result.Incomplete = true
			continue
		}
		if estimated {
			result.EstimatedUSD += cost
		} else {
			result.KnownUSD += cost
		}
	}
	return result
}
