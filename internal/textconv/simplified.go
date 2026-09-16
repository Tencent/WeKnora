// Package textconv provides the text conversion used by FAQ normalization.
package textconv

import (
	_ "embed"
	"strings"
	"unicode/utf8"
)

// These Apache-2.0 dictionaries are pinned to the previous converter's data.
// See data/README.md for provenance and licenses/OpenCC-Apache-2.0.txt.
//
//go:embed data/TSPhrases.txt
var phrasesText string

//go:embed data/TSCharacters.txt
var charactersText string

var traditionalToSimplified = newConverter(phrasesText, charactersText)

type dictionary struct {
	values   map[string]string
	maxRunes int
}

type converter []dictionary

func newConverter(texts ...string) converter {
	result := make(converter, 0, len(texts))
	for _, text := range texts {
		d := dictionary{values: make(map[string]string)}
		for line := range strings.SplitSeq(text, "\n") {
			key, alternatives, ok := strings.Cut(strings.TrimSpace(line), "\t")
			values := strings.Fields(alternatives)
			if !ok || key == "" || len(values) == 0 {
				continue
			}
			d.values[key] = values[0]
			d.maxRunes = max(d.maxRunes, utf8.RuneCountInString(key))
		}
		result = append(result, d)
	}
	return result
}

// ToSimplified preserves the historical FAQ conversion: phrase dictionary
// before character dictionary, longest match within each, first alternative.
// Dictionaries are read-only after initialization, so conversion is concurrent-safe.
func ToSimplified(text string) string {
	return traditionalToSimplified.convert(text)
}

func (c converter) convert(text string) string {
	runes := []rune(text)
	var result strings.Builder
	result.Grow(len(text))
	for pos := 0; pos < len(runes); {
		matched := false
		for _, d := range c {
			// The previous converter searched at most ten input runes.
			limit := min(10, d.maxRunes, len(runes)-pos)
			for size := limit; size > 0; size-- {
				if replacement, ok := d.values[string(runes[pos:pos+size])]; ok {
					result.WriteString(replacement)
					pos += size
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			result.WriteRune(runes[pos])
			pos++
		}
	}
	return result.String()
}
