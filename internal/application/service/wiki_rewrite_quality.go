package service

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// trimWikiContinuationOverlap removes only an exact, substantial overlap at
// the continuation boundary. Short matches may be legitimate prose or tables.
func trimWikiContinuationOverlap(previous, next string) string {
	r := []rune(next)
	if len(r) < 40 {
		return next
	}
	prefix := string(r[:40])
	for start := strings.Index(previous, prefix); start >= 0; {
		suffix := previous[start:]
		if strings.HasPrefix(next, suffix) {
			return next[len(suffix):]
		}
		offset := start + len(prefix)
		i := strings.Index(previous[offset:], prefix)
		if i < 0 {
			break
		}
		start = offset + i
	}
	return next
}

// validateWikiRewrite rejects pathological output before it can become the
// context of the next rewrite. It never silently discards generated facts.
func validateWikiRewrite(content string, data map[string]string) error {
	total := utf8.RuneCountInString(content)
	seen := make(map[string]bool)
	shortLines := make(map[string]int)
	repeatedShort := 0
	seenSentences := make(map[string]int)
	repeatedSentences := 0
	repeated := 0
	fenced := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(line, "|") || strings.HasPrefix(line, "#") {
			continue
		}
		// Degeneration can repeat a phrase without punctuation or newlines.
		runes := []rune(line)
		if len(runes) >= 1024 {
			blocks := make(map[string]int)
			duplicateChars := 0
			for i := 0; i+64 <= len(runes); i += 64 {
				block := string(runes[i : i+64])
				blocks[block]++
				if blocks[block] > 2 {
					duplicateChars += 64
				}
			}
			if duplicateChars >= 512 && duplicateChars*2 > len(runes) {
				return fmt.Errorf("wiki rewrite quality: looping text within a paragraph")
			}
		}
		for _, sentence := range strings.FieldsFunc(line, func(r rune) bool { return r == '。' || r == '！' || r == '？' }) {
			sentence = strings.TrimSpace(sentence)
			n := utf8.RuneCountInString(sentence)
			if n >= 40 {
				seenSentences[sentence]++
				if seenSentences[sentence] > 2 {
					repeatedSentences += n
				}
			}
		}
		key := strings.Join(strings.Fields(line), " ")
		n := utf8.RuneCountInString(key)
		if n < 40 {
			if n >= 8 {
				shortLines[key]++
				if shortLines[key] > 8 {
					repeatedShort += n
				}
			}
			continue
		}
		if seen[key] {
			repeated += n
		}
		seen[key] = true
	}
	if repeatedShort >= 200 && repeatedShort*5 >= total {
		return fmt.Errorf("wiki rewrite quality: looping short lines")
	}
	if repeated >= 200 && repeated*20 >= total {
		return fmt.Errorf("wiki rewrite quality: repeated prose (%d of %d characters)", repeated, total)
	}
	if repeatedSentences >= 200 && repeatedSentences*10 >= total {
		return fmt.Errorf("wiki rewrite quality: repeated sentences (%d of %d characters)", repeatedSentences, total)
	}
	source := 0
	for _, key := range []string{"ExistingContent", "NewContent", "SharedSourceContexts", "RemainingSourcesContent"} {
		source += utf8.RuneCountInString(data[key])
	}
	if total > 16000 && total > 4*source {
		return fmt.Errorf("wiki rewrite quality: abnormal growth (%d characters from %d source characters)", total, source)
	}
	return nil
}
