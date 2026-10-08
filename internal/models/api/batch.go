package api

import (
	"fmt"
	"unicode/utf8"
)

// BatchLimits are the per-request ceilings a vendor documents. A zero field
// means the vendor states no limit, and nothing is enforced for it.
type BatchLimits struct {
	// MaxItems caps how many documents one request may carry.
	MaxItems int
	// MaxItemRunes caps a single document. Vendors state these in characters
	// or tokens; runes are the closest thing we can count without a
	// tokenizer, and undercounting is the safe direction.
	MaxItemRunes int
	// MaxTotalRunes caps the whole request, query included.
	MaxTotalRunes int
}

// Batch is one slice of the input, and where it started.
type Batch struct {
	Start int
	Items []string
}

// SplitBatches divides items into requests that satisfy limits. fixedRunes is
// charged to every batch (the query, which each request repeats).
//
// A single item that cannot fit on its own is an error rather than a silent
// truncation: the caller asked for this document to be scored, and scoring a
// prefix of it returns a number for something the user never sent.
func SplitBatches(items []string, fixedRunes int, limits BatchLimits) ([]Batch, error) {
	if limits.MaxTotalRunes > 0 && fixedRunes >= limits.MaxTotalRunes {
		return nil, fmt.Errorf(
			"query is %d characters; the request budget is %d and must also fit a document",
			fixedRunes, limits.MaxTotalRunes,
		)
	}

	sizes := make([]int, len(items))
	for i, item := range items {
		sizes[i] = utf8.RuneCountInString(item)
		if limits.MaxItemRunes > 0 && sizes[i] > limits.MaxItemRunes {
			return nil, fmt.Errorf(
				"document at index %d is %d characters; the per-document limit is %d",
				i, sizes[i], limits.MaxItemRunes,
			)
		}
		if limits.MaxTotalRunes > 0 && fixedRunes+sizes[i] > limits.MaxTotalRunes {
			return nil, fmt.Errorf(
				"document at index %d is %d characters; it does not fit beside a %d-character query "+
					"in the %d-character request budget",
				i, sizes[i], fixedRunes, limits.MaxTotalRunes,
			)
		}
	}

	if len(items) == 0 {
		return nil, nil
	}
	if limits.MaxItems <= 0 && limits.MaxTotalRunes <= 0 {
		return []Batch{{Start: 0, Items: items}}, nil
	}

	batches := make([]Batch, 0, 1)
	start, total := 0, fixedRunes
	for i := range items {
		full := limits.MaxItems > 0 && i-start == limits.MaxItems
		over := limits.MaxTotalRunes > 0 && total+sizes[i] > limits.MaxTotalRunes
		if i > start && (full || over) {
			batches = append(batches, Batch{Start: start, Items: items[start:i]})
			start, total = i, fixedRunes
		}
		total += sizes[i]
	}
	batches = append(batches, Batch{Start: start, Items: items[start:]})
	return batches, nil
}
