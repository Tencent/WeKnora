package types

import "strings"

var supportedLocales = map[string]struct{}{
	"zh-CN": {},
	"en-US": {},
	"ko-KR": {},
	"ja-JP": {},
	"ru-RU": {},
}

// NormalizeSupportedLocale returns a trimmed, supported locale tag or an empty
// string when the value is blank or unsupported.
func NormalizeSupportedLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if _, ok := supportedLocales[locale]; ok {
		return locale
	}
	return ""
}
