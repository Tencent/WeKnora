package utils

import (
	"strings"
	"unicode"
)

// DetectLanguageCode detects the language code of the given text
func DetectLanguageCode(text string) string {
	var hasHan bool
	var hasHiragana bool
	var hasKatakana bool
	var hasHangul bool
	var hasCyrillic bool
	var hasLatin bool

	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Han):
			hasHan = true
		case unicode.In(r, unicode.Hiragana):
			hasHiragana = true
		case unicode.In(r, unicode.Katakana):
			hasKatakana = true
		case unicode.In(r, unicode.Hangul):
			hasHangul = true
		case unicode.In(r, unicode.Cyrillic):
			hasCyrillic = true
		case unicode.IsLetter(r):
			hasLatin = true
		}
	}

	if hasHiragana || hasKatakana {
		return "ja"
	}
	if hasHangul {
		return "ko"
	}
	if hasHan {
		return "zh"
	}
	if hasCyrillic {
		return "ru"
	}
	if hasLatin {
		return "en"
	}
	return ""
}

// BuildLanguageDirectiveFromText builds a language directive based on the detected language of the text
func BuildLanguageDirectiveFromText(text string) string {
	language := DetectLanguageCode(text)
	switch language {
	case "zh":
		return "请使用与用户初始消息相同的语言作答（简体中文）。"
	case "ja":
		return "ユーザーの最初のメッセージと同じ言語で回答してください（日本語）。"
	case "ko":
		return "사용자의 첫 메시지와 동일한 언어로 답변해주세요(한국어)."
	case "ru":
		return "Отвечай на том же языке, что и первое сообщение пользователя (русский)."
	case "en":
		return "Respond in the same language as the user's initial message (English)."
	default:
		return "Respond in the same language as the user's initial message."
	}
}

// AppendLanguageDirective appends the language directive to the prompt
func AppendLanguageDirective(prompt string, directive string) string {
	if strings.TrimSpace(directive) == "" {
		return prompt
	}
	if strings.Contains(prompt, directive) {
		return prompt
	}
	return strings.TrimRight(prompt, "\n") + "\n\n" + directive
}
