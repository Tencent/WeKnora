package regression

import (
	"fmt"
	"strings"
)

// RenderText renders a human-readable regression report. It is deliberately
// terse: it names the failing metrics and their baseline/current/delta/threshold
// values instead of dumping raw benchmark data.
func (r *Report) RenderText() string {
	var b strings.Builder
	b.WriteString("Regression Report\n\n")
	writeBuilder(&b, "Overall: %s\n", r.OverallStatus)
	writeBuilder(&b, "Baseline: %s\n", r.BaselineID)
	writeBuilder(&b, "Current:  %s\n", r.CurrentID)

	if r.OverallStatus == StatusPass {
		b.WriteString("\nAll monitored quality metrics are within regression thresholds.\n")
		return b.String()
	}

	b.WriteString("\nFailed Metrics:\n")
	for _, mc := range r.Comparisons {
		if mc.Status != StatusFail {
			continue
		}
		writeBuilder(&b, "\n%s\n", mc.DisplayName)
		switch {
		case mc.Missing:
			b.WriteString("baseline or current metric is missing\n")
		case mc.NonFinite:
			b.WriteString("baseline or current metric is not a finite number\n")
		default:
			writeBuilder(&b, "baseline = %.4f\n", *mc.Baseline)
			writeBuilder(&b, "current  = %.4f\n", *mc.Current)
			writeBuilder(&b, "delta    = %+.4f\n", *mc.Delta)
			writeBuilder(&b, "threshold = %+.4f\n", -mc.AllowedDrop)
		}
	}
	b.WriteString("\n")
	b.WriteString(r.Summary)
	b.WriteString("\n")
	return b.String()
}

func writeBuilder(b *strings.Builder, format string, args ...any) {
	_, _ = fmt.Fprintf(b, format, args...)
}
