package mcp

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
)

// EqualJSONValues compares JSON values decoded with UseNumber. Equivalent
// number spellings compare equal without rounding through float64.
func EqualJSONValues(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		other, ok := b.(json.Number)
		if !ok {
			return false
		}
		return a == other || canonicalNumber(string(a)) == canonicalNumber(string(other))
	case map[string]any:
		other, ok := b.(map[string]any)
		if !ok || len(a) != len(other) {
			return false
		}
		for key, value := range a {
			v, exists := other[key]
			if !exists || !EqualJSONValues(value, v) {
				return false
			}
		}
		return true
	case []any:
		other, ok := b.([]any)
		if !ok || len(a) != len(other) {
			return false
		}
		for i, value := range a {
			if !EqualJSONValues(value, other[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Normalize decimal tokens without expanding large exponents or rounding large
// integer IDs through float64. JSON decoding has already validated the grammar.
func canonicalNumber(raw string) string {
	mantissa, exponent := raw, new(big.Int)
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		mantissa = raw[:i]
		exponent.SetString(raw[i+1:], 10)
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(mantissa)-i-1)))
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0"
	}
	digits := strings.TrimRight(mantissa, "0")
	exponent.Add(exponent, big.NewInt(int64(len(mantissa)-len(digits))))
	return sign + digits + "e" + exponent.String()
}
