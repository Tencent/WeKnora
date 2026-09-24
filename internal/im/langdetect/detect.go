// Package langdetect identifies the reply language of an IM message. It is
// deliberately independent of the IM service and its storage dependencies.
package langdetect

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/abadojack/whatlanggo"
)

var urlPattern = regexp.MustCompile(`(?i)https?://\S+|www\.\S+`)

// Detect returns a trusted language name for prompt templates, or empty when
// the message is too short or uncertain. The detector's closed language table
// prevents message text from entering a prompt instruction.
func Detect(content string) string {
	text := strings.TrimSpace(urlPattern.ReplaceAllString(content, " "))
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
		return ""
	}
	info := whatlanggo.Detect(text)
	if !info.IsReliable() {
		return ""
	}
	// A mixed Vietnamese/English turn can receive a falsely high English
	// score. Keep the conversation's prior language when those cues disagree.
	if vietnameseMarks >= 2 && info.Lang != whatlanggo.Vie {
		return ""
	}
	return whatlanggo.Langs[info.Lang]
}
