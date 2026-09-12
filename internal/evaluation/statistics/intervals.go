// Package statistics computes deterministic uncertainty intervals for evaluation results.
package statistics

import (
	"errors"
	"math"
	"math/rand"
	"sort"
)

// Interval is a two-sided confidence interval with explicit sample metadata.
type Interval struct {
	Estimate   float64 `json:"estimate"`
	Lower      float64 `json:"lower"`
	Upper      float64 `json:"upper"`
	Confidence float64 `json:"confidence"`
	Method     string  `json:"method"`
	Samples    int     `json:"samples"`
	Iterations int     `json:"iterations,omitempty"`
	Seed       int64   `json:"seed,omitempty"`
}

// BootstrapMean returns a deterministic percentile interval for the arithmetic mean.
func BootstrapMean(values []float64, confidence float64, iterations int, seed int64) (*Interval, error) {
	if len(values) == 0 {
		return nil, errors.New("bootstrap mean: at least one sample is required")
	}
	if confidence <= 0 || confidence >= 1 || iterations < 100 {
		return nil, errors.New("bootstrap mean: confidence must be between zero and one and iterations at least 100")
	}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, errors.New("bootstrap mean: samples must be finite")
		}
	}
	estimate := mean(values)
	if len(values) == 1 {
		return &Interval{
			Estimate: estimate, Lower: estimate, Upper: estimate, Confidence: confidence,
			Method: "percentile_bootstrap", Samples: 1, Iterations: iterations, Seed: seed,
		}, nil
	}
	random := rand.New(rand.NewSource(seed))
	means := make([]float64, iterations)
	for iteration := range means {
		var sum float64
		for range values {
			sum += values[random.Intn(len(values))]
		}
		means[iteration] = sum / float64(len(values))
	}
	sort.Float64s(means)
	alpha := (1 - confidence) / 2
	return &Interval{
		Estimate: estimate,
		Lower:    quantile(means, alpha), Upper: quantile(means, 1-alpha), Confidence: confidence,
		Method: "percentile_bootstrap", Samples: len(values), Iterations: iterations, Seed: seed,
	}, nil
}

// Wilson returns the Wilson score interval for a binomial proportion.
func Wilson(successes, trials int, confidence float64) (*Interval, error) {
	if trials < 2 || successes < 0 || successes > trials || confidence <= 0 || confidence >= 1 {
		return nil, errors.New("wilson interval: valid counts and confidence are required")
	}
	n := float64(trials)
	proportion := float64(successes) / n
	z := math.Sqrt2 * math.Erfinv(confidence)
	z2 := z * z
	denominator := 1 + z2/n
	center := (proportion + z2/(2*n)) / denominator
	margin := z * math.Sqrt((proportion*(1-proportion)+z2/(4*n))/n) / denominator
	return &Interval{
		Estimate: proportion, Lower: math.Max(0, center-margin), Upper: math.Min(1, center+margin),
		Confidence: confidence, Method: "wilson_score", Samples: trials,
	}, nil
}

// Percentiles returns deterministic nearest-rank interpolated percentiles.
func Percentiles(values []float64) (p50, p95, p99 float64, err error) {
	if len(values) == 0 {
		return 0, 0, 0, errors.New("percentiles: at least one sample is required")
	}
	ordered := append([]float64(nil), values...)
	for _, value := range ordered {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, 0, 0, errors.New("percentiles: samples must be finite")
		}
	}
	sort.Float64s(ordered)
	return quantile(ordered, 0.50), quantile(ordered, 0.95), quantile(ordered, 0.99), nil
}

func mean(values []float64) float64 {
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func quantile(sorted []float64, probability float64) float64 {
	position := probability * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
