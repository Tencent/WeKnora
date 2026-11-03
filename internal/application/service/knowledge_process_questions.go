package service

import (
	"strings"
	"unicode"
)

// keepQuestionsGroundedInChunk drops a generated question only when its
// evidence appears in the surrounding chunks and not in the current chunk.
// Surrounding text stays in the prompt so the model can resolve a boundary;
// a question that merely paraphrases the current chunk, or overlaps it, stays.
func keepQuestionsGroundedInChunk(questions []string, current, prev, next string) []string {
	if len(questions) == 0 || (prev == "" && next == "") {
		return questions
	}
	currentEvidence := questionEvidence(current)
	neighborEvidence := questionEvidence(prev)
	for token := range questionEvidence(next) {
		neighborEvidence[token] = struct{}{}
	}
	for token := range currentEvidence {
		delete(neighborEvidence, token)
	}
	if len(neighborEvidence) == 0 {
		return questions
	}
	kept := make([]string, 0, len(questions))
	for _, question := range questions {
		evidence := questionEvidence(question)
		if evidenceOverlaps(evidence, neighborEvidence) && !evidenceOverlaps(evidence, currentEvidence) {
			continue
		}
		kept = append(kept, question)
	}
	return kept
}

func evidenceOverlaps(left, right map[string]struct{}) bool {
	for token := range left {
		if _, ok := right[token]; ok {
			return true
		}
	}
	return false
}

// questionEvidence is CJK bigrams plus ASCII or digit tokens of length >= 4.
// Shared boundary words that also occur in the current chunk are removed by
// the caller, so they cannot by themselves mark a question as neighbor-only.
func questionEvidence(text string) map[string]struct{} {
	evidence := make(map[string]struct{})
	var han []rune
	var ascii []rune
	flushHan := func() {
		if len(han) >= 2 {
			for i := 0; i < len(han)-1; i++ {
				evidence[string(han[i:i+2])] = struct{}{}
			}
		}
		han = han[:0]
	}
	flushASCII := func() {
		if len(ascii) >= 4 {
			evidence[strings.ToLower(string(ascii))] = struct{}{}
		}
		ascii = ascii[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushASCII()
			han = append(han, r)
		case r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			flushHan()
			ascii = append(ascii, r)
		default:
			flushHan()
			flushASCII()
		}
	}
	flushHan()
	flushASCII()
	return evidence
}
