// Package langdetect identifies the reply language of an IM message. It is
// deliberately independent of the IM service and its storage dependencies.
package langdetect

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

var urlPattern = regexp.MustCompile(`(?i)https?://\S+|www\.\S+`)

// These precomposed letters are distinctive to Vietnamese. Do not include
// shared letters such as ă, đ, â, ê or ô, which also occur in other languages,
// or dot-below letters (ạ ẹ ị ọ ụ ỵ), which also occur in Yoruba and Igbo.
const vietnameseOnly = "ơưƠƯấầẩẫậắằẳẵặếềểễệốồổỗộớờởỡợứừửữự" +
	"ẤẦẨẪẬẮẰẲẴẶẾỀỂỄỆỐỒỔỖỘỚỜỞỠỢỨỪỬỮỰảẻỉỏủỷẢẺỈỎỦỶ"

// localeOverrides maps whatlanggo languages whose locale tag differs from the
// bare ISO 639-3 primary tag onto the locale codes WeKnora already uses
// (internal/types/locale.go).
var localeOverrides = map[whatlanggo.Lang]string{
	whatlanggo.Cmn: "zh-CN",
	whatlanggo.Eng: "en-US",
	whatlanggo.Jpn: "ja-JP",
	whatlanggo.Kor: "ko-KR",
	whatlanggo.Rus: "ru-RU",
}

// localeTag returns the locale code for a detected language: the known
// override when one exists, otherwise the BCP-47 primary subtag.
func localeTag(lang whatlanggo.Lang) string {
	if tag, ok := localeOverrides[lang]; ok {
		return tag
	}
	base, confidence := language.Make(lang.Iso6393()).Base()
	if confidence == language.No {
		return ""
	}
	return base.String()
}

// Detect returns the detected locale code (e.g. "zh-CN", "vi") for the reply
// pipeline, or empty when the message is too short or uncertain. The
// detector's closed language table prevents message text from entering a
// prompt instruction.
func Detect(content string) string {
	text := norm.NFC.String(strings.TrimSpace(urlPattern.ReplaceAllString(content, " ")))
	letters := 0
	vietnameseMarks := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
		if strings.ContainsRune("ăâđêôơưĂÂĐÊÔƠƯấầẩẫậắằẳẵặếềểễệốồổỗộớờởỡợứừửữự", r) {
			vietnameseMarks++
		}
	}
	if letters < 20 {
		if strings.ContainsAny(text, vietnameseOnly) {
			return "vi"
		}
		return ""
	}
	info := whatlanggo.Detect(text)
	if !info.IsReliable() {
		if strings.ContainsAny(text, vietnameseOnly) {
			return "vi"
		}
		return ""
	}
	// A mixed Vietnamese/English turn can receive a falsely high English
	// score. Keep the conversation's prior language when those cues disagree.
	if vietnameseMarks >= 2 && info.Lang != whatlanggo.Vie {
		return ""
	}
	return localeTag(info.Lang)
}
