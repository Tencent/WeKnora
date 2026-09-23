// Package builtin implements mainland-China regex and checksum PII detectors.
package builtin

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// Mask applies mainland-China preset detectors and custom regexps.
func Mask(ctx context.Context, text string, cfg types.DesensitizationConfig) (string, types.JSONMap, error) {
	_ = ctx
	cfg.Normalize()
	if !cfg.Enabled {
		return text, types.JSONMap{"masked": false}, nil
	}

	spans := make([]span, 0, 8)
	for _, entity := range cfg.EntityTypes {
		found, err := findPreset(text, entity)
		if err != nil {
			return "", nil, err
		}
		spans = append(spans, found...)
	}
	custom, err := findCustom(text, cfg.Rules)
	if err != nil {
		return "", nil, err
	}
	spans = append(spans, custom...)

	merged := mergeSpans(spans)
	out := applySpans(text, merged, cfg.MaskStyle)
	return out, types.JSONMap{
		"masked":     len(merged) > 0,
		"span_count": len(merged),
		"engine":     types.DesensitizationEngineBuiltin,
	}, nil
}

type span struct {
	start       int
	end         int
	entity      string
	replacement string
	original    string
}

func findPreset(text, entity string) ([]span, error) {
	switch entity {
	case types.DesensitizationEntityCNIDCard:
		return findCNIDCards(text), nil
	case types.DesensitizationEntityCNMobile:
		return findCNMobiles(text), nil
	case types.DesensitizationEntityCNLandline:
		return findCNLandlines(text), nil
	case types.DesensitizationEntityCNBankCard:
		return findCNBankCards(text), nil
	case types.DesensitizationEntityCNUSCC:
		return findCNUSCC(text), nil
	case types.DesensitizationEntityCNPlate:
		return findCNPlates(text), nil
	case types.DesensitizationEntityEmail:
		return findEmails(text), nil
	default:
		return nil, nil
	}
}

var (
	// 18-digit mainland ID; digits may be grouped with spaces.
	reDigits18 = regexp.MustCompile(`[1-9](?:\s*\d){16}\s*[\dXx]`)
	// MIIT-like 11-digit segments, optional +86/0086 and space/hyphen grouping.
	reMobile = regexp.MustCompile( //nolint:lll // regex
		`(?:(?:\+86|0086)\s*)?1(?:3\d|4[5-9]|5[0-35-9]|6[2567]|7\d|8\d|9\d)(?:[\s-]?\d){8}`,
	)
	reMobileBare = regexp.MustCompile(`^1(?:3\d|4[5-9]|5[0-35-9]|6[2567]|7\d|8\d|9\d)\d{8}$`)
	reLandline   = regexp.MustCompile(`0\d{2,3}-?\d{7,8}`)
	// UnionPay 16–19 digits; consecutive whitespace and/or hyphens between digits.
	reUnionPay = regexp.MustCompile(`62(?:[\s\-]*\d){14,17}`)
	reUSCC     = regexp.MustCompile(`[0-9A-HJ-NPQRTUWXY]{2}\d{6}[0-9A-HJ-NPQRTUWXY]{10}`)
	rePlateStd = regexp.MustCompile(
		`[京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤川青藏琼宁][A-Z][A-HJ-NP-Z0-9]{4}[A-HJ-NP-Z0-9挂学警港澳]`, //nolint:lll // CJK
	)
	rePlateNEV = regexp.MustCompile(
		`[京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤川青藏琼宁][A-Z](?:[0-9A-HJ-NP-Z]{5}[DF]|[DF][0-9A-HJ-NP-Z]{5})`, //nolint:lll // CJK
	)
	reEmail = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
)

func findCNIDCards(text string) []span {
	return collect(text, reDigits18, types.DesensitizationEntityCNIDCard, true, validCNIDCard)
}

func findCNMobiles(text string) []span {
	return collect(text, reMobile, types.DesensitizationEntityCNMobile, true, func(raw string) bool {
		_, ok := normalizeCNMobile(raw)
		return ok
	})
}

func findCNLandlines(text string) []span {
	return collect(text, reLandline, types.DesensitizationEntityCNLandline, true, nil)
}

func findCNBankCards(text string) []span {
	return collect(text, reUnionPay, types.DesensitizationEntityCNBankCard, true, func(raw string) bool {
		digits := stripCardSeparators(raw)
		return !validCNIDCard(digits) && validLuhn(digits)
	})
}

func stripCardSeparators(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func stripSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func normalizeCNMobile(raw string) (string, bool) {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if r == '+' || r == '-' || unicode.IsSpace(r) {
			continue
		}
		if r < '0' || r > '9' {
			return "", false
		}
		b.WriteRune(r)
	}
	digits := b.String()
	switch {
	case strings.HasPrefix(digits, "0086"):
		digits = digits[4:]
	case strings.HasPrefix(digits, "86") && len(digits) > 11:
		digits = digits[2:]
	}
	if !reMobileBare.MatchString(digits) {
		return "", false
	}
	return digits, true
}

func findCNUSCC(text string) []span {
	return collect(text, reUSCC, types.DesensitizationEntityCNUSCC, false, validUSCC)
}

func findCNPlates(text string) []span {
	out := collect(text, rePlateNEV, types.DesensitizationEntityCNPlate, false, nil)
	out = append(out, collect(text, rePlateStd, types.DesensitizationEntityCNPlate, false, nil)...)
	return out
}

func findEmails(text string) []span {
	return collect(text, reEmail, types.DesensitizationEntityEmail, false, validEmail)
}

func collect(text string, re *regexp.Regexp, entity string, digits bool, ok func(string) bool) []span {
	locs := re.FindAllStringIndex(text, -1)
	out := make([]span, 0, len(locs))
	for _, loc := range locs {
		if digits && !digitBounded(text, loc[0], loc[1]) {
			continue
		}
		raw := text[loc[0]:loc[1]]
		if ok != nil && !ok(raw) {
			continue
		}
		out = append(out, span{start: loc[0], end: loc[1], entity: entity, original: raw})
	}
	return out
}

// GB/T 2260 province / region codes used as the first two digits of a
// mainland resident ID. 71/81/82 cover Taiwan / Hong Kong / Macao.
var cnProvinceCodes = map[string]struct{}{
	"11": {}, "12": {}, "13": {}, "14": {}, "15": {},
	"21": {}, "22": {}, "23": {},
	"31": {}, "32": {}, "33": {}, "34": {}, "35": {}, "36": {}, "37": {},
	"41": {}, "42": {}, "43": {}, "44": {}, "45": {}, "46": {},
	"50": {}, "51": {}, "52": {}, "53": {}, "54": {},
	"61": {}, "62": {}, "63": {}, "64": {}, "65": {},
	"71": {}, "81": {}, "82": {},
}

func validCNIDCard(id string) bool {
	id = stripSpaces(id)
	if len(id) != 18 {
		return false
	}
	for i := 0; i < 17; i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	if _, ok := cnProvinceCodes[id[0:2]]; !ok {
		return false
	}
	if _, err := time.Parse("20060102", id[6:14]); err != nil {
		return false
	}
	if id[6:14] < "19000101" || id[6:14] > time.Now().Format("20060102") {
		return false
	}
	weights := []int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := []byte{'1', '0', 'X', '9', '8', '7', '6', '5', '4', '3', '2'}
	sum := 0
	for i, w := range weights {
		sum += int(id[i]-'0') * w
	}
	want := codes[sum%11]
	got := id[17]
	if got == 'x' {
		got = 'X'
	}
	return got == want
}

func validEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return false
	}
	local := s[:at]
	domain := s[at+1:]
	if len(local) < 1 || len(local) > 64 {
		return false
	}
	if len(domain) < 1 || len(domain) > 255 {
		return false
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return false
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return false
	}
	return true
}

func validLuhn(num string) bool {
	if len(num) < 16 || len(num) > 19 {
		return false
	}
	sum := 0
	alt := false
	for i := len(num) - 1; i >= 0; i-- {
		if num[i] < '0' || num[i] > '9' {
			return false
		}
		n := int(num[i] - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0
}

const usccCharset = "0123456789ABCDEFGHJKLMNPQRTUWXY"

func validUSCC(code string) bool {
	if len(code) != 18 {
		return false
	}
	index := [256]int{}
	for i := range index {
		index[i] = -1
	}
	for i := 0; i < len(usccCharset); i++ {
		index[usccCharset[i]] = i
	}
	weights := []int{1, 3, 9, 27, 19, 26, 16, 17, 20, 29, 25, 13, 8, 24, 10, 30, 28}
	sum := 0
	for i, w := range weights {
		pos := index[code[i]]
		if pos < 0 {
			return false
		}
		sum += pos * w
	}
	want := usccCharset[(31-(sum%31))%31]
	return code[17] == want
}

// ValidateRules compiles user-supplied regexps so invalid patterns fail at save time.
func ValidateRules(rules []types.DesensitizationRule) error {
	for _, rule := range rules {
		if strings.TrimSpace(rule.Pattern) == "" {
			continue
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return fmt.Errorf("custom desensitization rule %q: %w", rule.Name, err)
		}
	}
	return nil
}

func findCustom(text string, rules []types.DesensitizationRule) ([]span, error) {
	if err := ValidateRules(rules); err != nil {
		return nil, err
	}
	out := make([]span, 0)
	for i, rule := range rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, fmt.Errorf("custom desensitization rule %q: %w", rule.Name, err)
		}
		locs := re.FindAllStringIndex(text, -1)
		for _, loc := range locs {
			raw := text[loc[0]:loc[1]]
			repl := rule.Replacement
			if repl == "" {
				repl = "<" + rule.Name + ">"
			}
			out = append(out, span{
				start:       loc[0],
				end:         loc[1],
				entity:      fmt.Sprintf("custom:%d", i),
				replacement: repl,
				original:    raw,
			})
		}
	}
	return out, nil
}

func digitBounded(text string, start, end int) bool {
	if start > 0 && isASCIIDigit(text[start-1]) {
		return false
	}
	if end < len(text) && isASCIIDigit(text[end]) {
		return false
	}
	return true
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func mergeSpans(in []span) []span {
	if len(in) == 0 {
		return in
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].start != in[j].start {
			return in[i].start < in[j].start
		}
		return (in[i].end - in[i].start) > (in[j].end - in[j].start)
	})
	out := make([]span, 0, len(in))
	lastEnd := -1
	for _, s := range in {
		if s.start < lastEnd {
			continue
		}
		out = append(out, s)
		lastEnd = s.end
	}
	return out
}

func applySpans(text string, spans []span, style string) string {
	if len(spans) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	cursor := 0
	for _, s := range spans {
		if s.start > cursor {
			b.WriteString(text[cursor:s.start])
		}
		b.WriteString(replacementFor(s, style))
		cursor = s.end
	}
	if cursor < len(text) {
		b.WriteString(text[cursor:])
	}
	return b.String()
}

func replacementFor(s span, style string) string {
	if s.replacement != "" {
		return s.replacement
	}
	if style == types.DesensitizationMaskPartial {
		if partial := partialMask(s.entity, s.original); partial != "" {
			return partial
		}
	}
	return "<" + entityLabel(s.entity) + ">"
}

func entityLabel(entity string) string {
	switch entity {
	case types.DesensitizationEntityCNIDCard:
		return "身份证"
	case types.DesensitizationEntityCNMobile:
		return "手机号"
	case types.DesensitizationEntityCNLandline:
		return "固定电话"
	case types.DesensitizationEntityCNBankCard:
		return "银行卡"
	case types.DesensitizationEntityCNUSCC:
		return "统一社会信用代码"
	case types.DesensitizationEntityCNPlate:
		return "车牌号"
	case types.DesensitizationEntityEmail:
		return "邮箱"
	default:
		if strings.HasPrefix(entity, "custom:") {
			return "脱敏"
		}
		return entity
	}
}

func partialMask(entity, original string) string {
	switch entity {
	case types.DesensitizationEntityCNIDCard:
		return partialMaskKeepingSeps(original, 6, 4)
	case types.DesensitizationEntityCNMobile:
		return partialMaskMobile(original)
	case types.DesensitizationEntityCNBankCard:
		return partialMaskBankCard(original)
	case types.DesensitizationEntityCNUSCC:
		return keepHeadTail(original, 4, 4)
	case types.DesensitizationEntityEmail:
		return maskEmail(original)
	case types.DesensitizationEntityCNLandline, types.DesensitizationEntityCNPlate:
		return keepHeadTail(original, 2, 2)
	default:
		return keepHeadTail(original, 1, 1)
	}
}

func partialMaskBankCard(original string) string {
	digits := stripCardSeparators(original)
	masked := keepHeadTail(digits, 4, 4)
	if masked == "" || len(masked) != len(digits) {
		return keepHeadTail(original, 4, 4)
	}
	return rewriteKeepingNonDigits(original, masked)
}

func partialMaskMobile(original string) string {
	digits, ok := normalizeCNMobile(original)
	if !ok {
		return keepHeadTail(original, 3, 4)
	}
	masked := keepHeadTail(digits, 3, 4)
	return rewriteLastNDigits(original, masked)
}

func rewriteLastNDigits(original, maskedDigits string) string {
	runes := []rune(original)
	digitIdx := make([]int, 0, len(runes))
	for i, r := range runes {
		if r >= '0' && r <= '9' {
			digitIdx = append(digitIdx, i)
		}
	}
	if len(digitIdx) < len(maskedDigits) {
		return keepHeadTail(original, 3, 4)
	}
	start := len(digitIdx) - len(maskedDigits)
	for i := 0; i < len(maskedDigits); i++ {
		runes[digitIdx[start+i]] = rune(maskedDigits[i])
	}
	return string(runes)
}

func partialMaskKeepingSeps(original string, head, tail int) string {
	compact := stripSpaces(original)
	masked := keepHeadTail(compact, head, tail)
	if masked == "" || utf8.RuneCountInString(masked) != utf8.RuneCountInString(compact) {
		return keepHeadTail(original, head, tail)
	}
	return rewriteKeepingNonDigits(original, masked)
}

func rewriteKeepingNonDigits(original, maskedDigits string) string {
	var b strings.Builder
	b.Grow(len(original))
	di := 0
	for _, r := range original {
		if r == '+' || r == '-' || unicode.IsSpace(r) {
			b.WriteRune(r)
			continue
		}
		if di < len(maskedDigits) {
			b.WriteByte(maskedDigits[di])
			di++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func keepHeadTail(s string, head, tail int) string {
	n := utf8.RuneCountInString(s)
	if n <= head+tail {
		return strings.Repeat("*", n)
	}
	runes := []rune(s)
	return string(runes[:head]) + strings.Repeat("*", n-head-tail) + string(runes[n-tail:])
}

func maskEmail(s string) string {
	at := strings.IndexByte(s, '@')
	if at <= 0 {
		return keepHeadTail(s, 1, 0)
	}
	local := s[:at]
	domain := s[at+1:]
	return keepHeadTail(local, 2, 0) + "@" + strings.Repeat("*", len(domain))
}
