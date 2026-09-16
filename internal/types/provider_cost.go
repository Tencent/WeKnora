package types

import (
	"encoding/json"
	"math/big"
	"regexp"
)

var providerDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]{1,2})?$`)

// CaptureReportedCost retains a nonnegative JSON usage.cost without float rounding.
func CaptureReportedCost(body []byte, usage *TokenUsage) {
	if usage == nil {
		return
	}
	var raw struct {
		Usage *struct {
			Cost *json.Number `json:"cost"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &raw) != nil || raw.Usage == nil || raw.Usage.Cost == nil {
		return
	}
	value := raw.Usage.Cost.String()
	if _, ok := DecimalCostMicrounits(value); ok {
		usage.ReportedCost = &value
	}
}

// DecimalCostMicrounits rounds a nonnegative decimal amount half up to microcurrency.
func DecimalCostMicrounits(value string) (int64, bool) {
	if len(value) == 0 || len(value) > 64 || !providerDecimal.MatchString(value) {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 || r.Cmp(big.NewRat(1_000_000_000, 1)) > 0 {
		return 0, false
	}
	r.Mul(r, big.NewRat(1_000_000, 1))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	return n.Int64(), n.IsInt64()
}
