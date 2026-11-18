package compaction

import "strings"

const fallbackOmission = "[Earlier context omitted to keep the fallback summary within its token budget.]"

// fallbackHistory shares one text budget between the previous summary and the
// new archive. Repeated failures must not append another full budget each time.
// File tracking has already been extracted by Prepare and is appended separately;
// removing its old blocks here avoids truncating tags or accumulating stale copies.
func (c *Compactor) fallbackHistory(p *Preparation) string {
	budget := c.settings.summaryBudget()
	previous := withoutFileOps(p.PreviousSummary)
	archive := rawArchive(p.MessagesToSummarize, budget, c.estimator)
	if combined := joinNonEmpty(previous, archive); c.estimator.EstimateString(combined) <= budget {
		return combined
	}

	// Preserve the start of the prior summary (normally its goal/constraints),
	// using at most half the budget. The rest holds the newest archived work
	// and the omission notice; a short previous summary leaves more room.
	previous = fitFallbackText(previous, func(text string) bool {
		return c.estimator.EstimateString(text) <= budget/2
	}, false)
	prefix := joinNonEmpty(previous, fallbackOmission)
	archive = fitFallbackText(archive, func(text string) bool {
		return c.estimator.EstimateString(joinNonEmpty(prefix, text)) <= budget
	}, true)
	return joinNonEmpty(prefix, archive)
}

// fitFallbackText retains a rune-aligned prefix or suffix. Measure the complete
// candidate supplied by fits so separators and the omission marker count too.
func fitFallbackText(text string, fits func(string) bool, keepEnd bool) string {
	if fits(text) {
		return text
	}
	runes := []rune(text)
	part := func(n int) string {
		if keepEnd {
			return string(runes[len(runes)-n:])
		}
		return string(runes[:n])
	}
	low, high := 0, len(runes)
	for low < high {
		mid := low + (high-low+1)/2
		if fits(part(mid)) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return part(low)
}

func withoutFileOps(summary string) string {
	for _, tag := range []string{readFilesTag, modifiedFilesTag} {
		open, closeTag := "<"+tag+">", "</"+tag+">"
		for {
			start := strings.Index(summary, open)
			if start < 0 {
				break
			}
			end := strings.Index(summary[start+len(open):], closeTag)
			if end < 0 {
				break
			}
			end += start + len(open) + len(closeTag)
			summary = summary[:start] + summary[end:]
		}
	}
	return strings.TrimSpace(summary)
}
