package metric

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func sum(m map[string]int) int {
	s := 0
	for _, v := range m {
		s += v
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func splitSentences(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '。' || r == '.'
	})
	var sentences []string
	for _, part := range parts {
		if sentence := strings.TrimSpace(part); sentence != "" {
			sentences = append(sentences, sentence)
		}
	}
	return sentences
}

func splitIntoWords(sentences []string) []string {
	// 正则匹配中英文段落（中文块、英文块、其他字符）
	re := regexp.MustCompile(`([\p{Han}]+)|([a-zA-Z0-9_.,!?]+)|(\p{P})`)

	var tokens []string
	for _, text := range sentences {
		matches := re.FindAllStringSubmatch(text, -1)

		for _, groups := range matches {
			chineseBlock := groups[1]
			englishBlock := groups[2]
			punctuation := groups[3]

			switch {
			case chineseBlock != "": // 处理中文部分
				words := types.Jieba.Cut(chineseBlock, true)
				tokens = append(tokens, words...)
			case englishBlock != "": // 处理英文部分
				engTokens := strings.Fields(englishBlock)
				tokens = append(tokens, engTokens...)
			case punctuation != "": // 保留标点符号
				tokens = append(tokens, punctuation)
			}
		}
	}
	return tokens
}

func ToSet[T comparable](li []T) map[T]struct{} {
	res := make(map[T]struct{}, len(li))
	for _, v := range li {
		res[v] = struct{}{}
	}
	return res
}

func SliceMap[T any, Y any](li []T, fn func(T) Y) []Y {
	res := make([]Y, len(li))
	for i, v := range li {
		res[i] = fn(v)
	}
	return res
}

func Hit[T comparable](li []T, set map[T]struct{}) int {
	count := 0
	for _, v := range li {
		if _, exist := set[v]; exist {
			count++
		}
	}
	return count
}

func Fold[T any, Y any](slice []T, initial Y, f func(Y, T) Y) Y {
	accumulator := initial
	for _, item := range slice {
		accumulator = f(accumulator, item)
	}
	return accumulator
}
