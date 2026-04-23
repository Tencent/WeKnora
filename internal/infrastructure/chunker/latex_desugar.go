// Package chunker - latex_desugar.go converts the LaTeX math encoding that
// MinerU VLM emits for units, numbers, and chemical formulas back into
// plain-text / Unicode so both the UI and the embedding model see human-
// readable content.
//
// Example transformations:
//
//	"$22 \sim 26 \mathrm{~g}$"          → "22 ~ 26 g"
//	"$(22 \pm 1) ^{\circ} \mathrm{C}$"  → "(22 ± 1) °C"
//	"$10 \%$"                            → "10 %"
//	"$\mathrm{Fe}^{2+}$"                → "Fe²⁺"
//	"$5\% \mathrm{CO}_{2}$"             → "5% CO₂"
//	"$40 \mathrm{mg} / \mathrm{kg}$"    → "40 mg / kg"
//
// Scope is intentionally narrow: we only target the "trivial" LaTeX patterns
// that represent ordinary text disguised as math. Genuine formulas (anything
// still containing backslashes or braces after all passes) are left as
// LaTeX, because touching them further would silently corrupt semantics.
package chunker

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Pass 1: plain Unicode substitutions
// ---------------------------------------------------------------------------

// latexPlainSubs maps LaTeX commands that are equivalent to a single Unicode
// rune (no argument). Longest keys are matched first via ordering in
// latexPlainOrder so "\rightarrow" beats a partial "\r".
var latexPlainSubs = map[string]string{
	// relations / operators commonly seen in medical papers
	`\sim`:       "~",
	`\pm`:        "±",
	`\mp`:        "∓",
	`\times`:     "×",
	`\div`:       "÷",
	`\cdot`:      "·",
	`\leq`:       "≤",
	`\geq`:       "≥",
	`\lt`:        "<",
	`\gt`:        ">",
	`\ne`:        "≠",
	`\neq`:       "≠",
	`\approx`:    "≈",
	`\circ`:      "°",
	`\prime`:     "′",
	`\infty`:     "∞",
	// arrows
	`\rightarrow`: "→",
	`\to`:         "→",
	`\leftarrow`:  "←",
	`\gets`:       "←",
	// escaped literals that must not stay backslashed
	`\%`: "%",
	`\#`: "#",
	`\&`: "&",
	`\$`: "$",
	`\_`: "_",
	// Greek letters (the lower set that actually appears in biomedical text)
	`\alpha`:   "α",
	`\beta`:    "β",
	`\gamma`:   "γ",
	`\delta`:   "δ",
	`\epsilon`: "ε",
	`\zeta`:    "ζ",
	`\eta`:     "η",
	`\theta`:   "θ",
	`\iota`:    "ι",
	`\kappa`:   "κ",
	`\lambda`:  "λ",
	`\mu`:      "μ",
	`\nu`:      "ν",
	`\xi`:      "ξ",
	`\pi`:      "π",
	`\rho`:     "ρ",
	`\sigma`:   "σ",
	`\tau`:     "τ",
	`\phi`:     "φ",
	`\chi`:     "χ",
	`\psi`:     "ψ",
	`\omega`:   "ω",
	`\Alpha`:   "Α",
	`\Beta`:    "Β",
	`\Gamma`:   "Γ",
	`\Delta`:   "Δ",
	`\Theta`:   "Θ",
	`\Lambda`:  "Λ",
	`\Sigma`:   "Σ",
	`\Phi`:     "Φ",
	`\Omega`:   "Ω",
}

// latexPlainOrder is the deterministic iteration order, longest first so
// "\rightarrow" is tried before "\r"-prefixed commands.
var latexPlainOrder = func() []string {
	keys := make([]string, 0, len(latexPlainSubs))
	for k := range latexPlainSubs {
		keys = append(keys, k)
	}
	// sort descending by length
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && len(keys[j]) > len(keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}()

// substitutePlainLatexCommands replaces single-token LaTeX commands with
// their Unicode equivalents. Uses a strings.Replacer-style scan but with
// word-boundary awareness so "\alpha1" → "α1" while "\alphabeta" stays
// untouched (it would not be a valid command).
func substitutePlainLatexCommands(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
	// strings.Replacer is fine here because LaTeX commands are terminated
	// by a non-letter; subsequent passes will catch any remnants. We
	// intentionally do not append a trailing " " to the pattern because
	// that would eat legitimate whitespace.
	args := make([]string, 0, len(latexPlainOrder)*2)
	for _, k := range latexPlainOrder {
		args = append(args, k, latexPlainSubs[k])
	}
	return strings.NewReplacer(args...).Replace(text)
}

// ---------------------------------------------------------------------------
// Pass 2: superscript / subscript groups
// ---------------------------------------------------------------------------

var superscriptMap = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴',
	'5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
	'+': '⁺', '-': '⁻', '(': '⁽', ')': '⁾',
	'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ', 'd': 'ᵈ', 'e': 'ᵉ',
	'f': 'ᶠ', 'g': 'ᵍ', 'h': 'ʰ', 'i': 'ⁱ', 'j': 'ʲ',
	'k': 'ᵏ', 'l': 'ˡ', 'm': 'ᵐ', 'n': 'ⁿ', 'o': 'ᵒ',
	'p': 'ᵖ', 'r': 'ʳ', 's': 'ˢ', 't': 'ᵗ', 'u': 'ᵘ',
	'v': 'ᵛ', 'w': 'ʷ', 'x': 'ˣ', 'y': 'ʸ', 'z': 'ᶻ',
}

var subscriptMap = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄',
	'5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
	'+': '₊', '-': '₋', '(': '₍', ')': '₎',
	'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ',
	'k': 'ₖ', 'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ',
	'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ',
	'v': 'ᵥ', 'x': 'ₓ',
}

var (
	scriptGroupSupRe = regexp.MustCompile(`\^\{([^{}]*)\}`)
	scriptGroupSubRe = regexp.MustCompile(`_\{([^{}]*)\}`)
	scriptSingleSup  = regexp.MustCompile(`\^([0-9a-zA-Z+\-()])`)
	scriptSingleSub  = regexp.MustCompile(`_([0-9a-zA-Z+\-()])`)
)

// translateScript maps each rune of s through m when present; runes not in
// the map are passed through unchanged so Unicode already produced by earlier
// passes (e.g. "°" from "\circ") survives untouched.
func translateScript(s string, m map[rune]rune) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if mapped, ok := m[r]; ok {
			b.WriteRune(mapped)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func substituteScriptGroups(text string) string {
	text = scriptGroupSupRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := scriptGroupSupRe.FindStringSubmatch(match)[1]
		return translateScript(inner, superscriptMap)
	})
	text = scriptGroupSubRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := scriptGroupSubRe.FindStringSubmatch(match)[1]
		return translateScript(inner, subscriptMap)
	})
	text = scriptSingleSup.ReplaceAllStringFunc(text, func(match string) string {
		r, _ := utf8.DecodeRuneInString(match[1:])
		if mapped, ok := superscriptMap[r]; ok {
			return string(mapped)
		}
		return match
	})
	text = scriptSingleSub.ReplaceAllStringFunc(text, func(match string) string {
		r, _ := utf8.DecodeRuneInString(match[1:])
		if mapped, ok := subscriptMap[r]; ok {
			return string(mapped)
		}
		return match
	})
	return text
}

// ---------------------------------------------------------------------------
// Pass 3: unwrap font/style commands
// ---------------------------------------------------------------------------

// fontCommandRe matches \mathrm, \mathsf, \mathbf, \mathit, \mathtt, \text,
// \textrm, \textsf, \textbf, \textit — essentially "render this argument as
// ordinary text". Useful because MinerU loves \mathrm{g}, \mathrm{CO}, etc.
var fontCommandRe = regexp.MustCompile(`\\(?:math(?:rm|sf|bf|it|tt)|text(?:rm|sf|bf|it)?)\{([^{}]*)\}`)

// unwrapFontCommands replaces \mathrm{X} with X, trimming LaTeX non-breaking
// spaces (literal '~' characters inserted by MinerU).
func unwrapFontCommands(text string) string {
	if !strings.Contains(text, `\`) {
		return text
	}
	return fontCommandRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := fontCommandRe.FindStringSubmatch(match)[1]
		// MinerU uses "~" as non-breaking space inside \mathrm{~g} to glue
		// the number and unit. The tilde is NOT a \sim here — it is a NBSP.
		// Drop it wherever it lives inside the argument.
		inner = strings.ReplaceAll(inner, "~", "")
		return inner
	})
}

// ---------------------------------------------------------------------------
// Pass 4: strip $...$ delimiters around fully-sanitized content
// ---------------------------------------------------------------------------

// inlineDollarRe matches a single-dollar inline math block. The content is
// captured greedy-free so we don't accidentally span two adjacent math
// blocks on the same line. Display math ($$...$$) is deliberately not
// targeted here — a dedicated pass handles it if ever needed.
var inlineDollarRe = regexp.MustCompile(`\$([^$\n]{1,200})\$`)

// looksMathy returns true when the candidate still contains backslash
// sequences or braces that indicate real (complex) LaTeX we should not
// strip. We also bail out if the content is very long, as that usually
// means a genuine formula.
func looksMathy(s string) bool {
	if len(s) > 200 {
		return true
	}
	return strings.ContainsRune(s, '\\') || strings.ContainsRune(s, '{')
}

// stripCleanInlineMath removes $...$ delimiters around chunks that already
// contain no LaTeX syntax after earlier passes. This is the final cosmetic
// pass that converts, e.g., "$22 ± 1$" into "22 ± 1".
func stripCleanInlineMath(text string) string {
	if !strings.Contains(text, "$") {
		return text
	}
	return inlineDollarRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[1 : len(match)-1]
		if looksMathy(inner) {
			return match
		}
		return inner
	})
}

// ---------------------------------------------------------------------------
// Public entry point
// ---------------------------------------------------------------------------

// SanitizeInlineLaTeX converts MinerU-style LaTeX-wrapped prose (numbers,
// units, chemical formulas, simple operators) back into plain Unicode text.
// Genuine formulas with complex macros are left untouched. Idempotent.
func SanitizeInlineLaTeX(text string) string {
	if text == "" || (!strings.Contains(text, `\`) && !strings.Contains(text, `$`) &&
		!strings.Contains(text, "^{") && !strings.Contains(text, "_{")) {
		return text
	}
	text = substitutePlainLatexCommands(text)
	text = substituteScriptGroups(text)
	text = unwrapFontCommands(text)
	text = stripCleanInlineMath(text)
	return text
}
