// Package chunker - tokens.go provides language-aware token count approximation.
//
// We avoid pulling in a tokenizer dependency (e.g. tiktoken) and instead use
// per-language chars-per-token ratios derived from common embedding model
// vocabularies. The numbers are conservative — they tend to slightly
// over-estimate token counts so that chunks stay safely under model limits.
package chunker

import "unicode"

// Language identifiers used by the token estimator and the heuristic splitter.
const (
	LangEnglish = "en"
	LangGerman  = "de"
	LangChinese = "zh"
	LangMixed   = "mixed"
)

// charsPerToken holds approximate chars/token ratios per language.
// Numbers err on the conservative side so estimates over-shoot a little.
var charsPerToken = map[string]float64{
	LangEnglish: 4.0,
	LangGerman:  4.5,
	LangChinese: 1.7,
	LangMixed:   3.0,
}

// ApproxTokenCount returns a conservative token estimate for s in the given
// language. An empty or unknown lang falls back to "mixed".
func ApproxTokenCount(s string, lang string) int {
	if s == "" {
		return 0
	}
	ratio, ok := charsPerToken[lang]
	if !ok {
		ratio = charsPerToken[LangMixed]
	}
	runes := []rune(s)
	approx := float64(len(runes)) / ratio
	if approx < 1 {
		return 1
	}
	return int(approx + 0.5)
}

// DetectLanguage returns a coarse language label by counting CJK runes vs.
// Latin runes. The result is one of LangChinese, LangGerman, LangEnglish or
// LangMixed. Detection is cheap and meant only for heuristic dispatch — it
// is NOT a replacement for proper language identification.
func DetectLanguage(s string) string {
	if s == "" {
		return LangMixed
	}
	var cjk, latin, umlaut int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			cjk++
		case isGermanUmlaut(r):
			umlaut++
			latin++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			latin++
		}
	}
	total := cjk + latin
	if total == 0 {
		return LangMixed
	}
	cjkRatio := float64(cjk) / float64(total)
	latinRatio := float64(latin) / float64(total)
	// Mixed: meaningful presence of both scripts (>=15% each).
	if cjkRatio >= 0.15 && latinRatio >= 0.15 {
		return LangMixed
	}
	if cjkRatio > 0.3 {
		return LangChinese
	}
	if umlaut > 0 || hasGermanWords(s) {
		return LangGerman
	}
	return LangEnglish
}

func isGermanUmlaut(r rune) bool {
	switch r {
	case 'ä', 'ö', 'ü', 'Ä', 'Ö', 'Ü', 'ß':
		return true
	}
	return false
}

// hasGermanWords does a tiny stop-word check to bias towards "de" when the
// text uses common German function words. Cheap heuristic — false positives
// on borrowed terms are acceptable.
func hasGermanWords(s string) bool {
	const sample = 512
	if len(s) > sample {
		s = s[:sample]
	}
	for _, w := range []string{" der ", " die ", " das ", " und ", " ist ", " nicht ", " mit ", " auf "} {
		if containsLower(s, w) {
			return true
		}
	}
	return false
}

func containsLower(haystack, needle string) bool {
	if len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			h := haystack[i+j]
			if h >= 'A' && h <= 'Z' {
				h += 'a' - 'A'
			}
			if h != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// CharsForTokenLimit converts a token limit into an approximate character
// budget for a given language. Used to size chunks so they fit within an
// embedding model's max-token window with a small safety margin.
func CharsForTokenLimit(tokens int, lang string) int {
	if tokens <= 0 {
		return 0
	}
	ratio, ok := charsPerToken[lang]
	if !ok {
		ratio = charsPerToken[LangMixed]
	}
	// 0.9 safety factor so we under-shoot the model limit instead of overshooting.
	return int(float64(tokens) * ratio * 0.9)
}
