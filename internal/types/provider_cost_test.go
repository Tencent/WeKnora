package types

import "testing"

func TestProviderCostDecimalAndMissing(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    int64
		present bool
	}{
		{`{"usage":{"cost":0}}`, 0, true},
		{`{"usage":{"cost":0.0000005}}`, 1, true},
		{`{"usage":{"cost":4.87333e-6}}`, 5, true},
		{`{"usage":{"cost":null}}`, 0, false},
		{`{"usage":{}}`, 0, false},
		{`{"usage":{"cost":-1}}`, 0, false},
		{`{"usage":{"cost":1e20}}`, 0, false},
	} {
		u := &TokenUsage{}
		CaptureReportedCost([]byte(tc.raw), u)
		if (u.ReportedCost != nil) != tc.present {
			t.Fatalf("presence: %s", tc.raw)
		}
		if tc.present {
			got, ok := DecimalCostMicrounits(*u.ReportedCost)
			if !ok || got != tc.want {
				t.Fatalf("amount: %s = %d", tc.raw, got)
			}
		}
	}
}
