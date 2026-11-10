package tools

import "context"

type outputBudgetKey struct{}

// WithOutputBudget publishes the registry's output ceiling to the tool being
// executed. Tools that render several independent records in one result use it
// to shape their own output: the registry's fallback truncation keeps the head
// and tail of the whole blob, which for a batched result deletes the records in
// the middle and leaves the surviving ones cut mid-tag.
func WithOutputBudget(ctx context.Context, maxChars int) context.Context {
	if ctx == nil || maxChars <= 0 {
		return ctx
	}
	return context.WithValue(ctx, outputBudgetKey{}, maxChars)
}

// OutputBudget returns the ceiling published by the registry, in runes, falling
// back to DefaultMaxToolOutput for callers that execute a tool directly.
func OutputBudget(ctx context.Context) int {
	if ctx != nil {
		if budget, ok := ctx.Value(outputBudgetKey{}).(int); ok && budget > 0 {
			return budget
		}
	}
	return DefaultMaxToolOutput
}

// splitBudgetFairly performs max-min fair (water-filling) allocation of total
// across sizes: entries smaller than an equal share keep their full size and
// donate the slack to the larger entries. A batched result therefore degrades
// by trimming its biggest records rather than by dropping whole ones.
//
// The returned caps are never larger than the corresponding size, and their sum
// never exceeds total.
func splitBudgetFairly(total int, sizes []int) []int {
	caps := make([]int, len(sizes))
	if len(sizes) == 0 || total <= 0 {
		return caps
	}
	settled := make([]bool, len(sizes))
	remaining, unsettled := total, len(sizes)
	for unsettled > 0 {
		share := remaining / unsettled
		if share <= 0 {
			break
		}
		progressed := false
		for i, size := range sizes {
			if settled[i] || size > share {
				continue
			}
			caps[i], settled[i] = size, true
			remaining -= size
			unsettled--
			progressed = true
		}
		if !progressed {
			// Every entry still unsettled exceeds the fair share, so the
			// remainder splits evenly and the allocation is complete.
			for i := range sizes {
				if !settled[i] {
					caps[i] = share
				}
			}
			break
		}
	}
	return caps
}
