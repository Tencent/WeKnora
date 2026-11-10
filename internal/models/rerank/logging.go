package rerank

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxLogDocuments = 3
	maxLogTextRunes = 120
)

func buildRerankRequestDebug(model, endpoint, query string, documents []string) string {
	previews := make([]string, 0, maxLogDocuments)
	for i, doc := range documents {
		if i >= maxLogDocuments {
			break
		}
		previews = append(previews, compactForLog(doc, maxLogTextRunes))
	}

	previewJSON, _ := json.Marshal(previews)
	return fmt.Sprintf(
		"rerank request endpoint=%s model=%s query_preview=%q query_runes=%d documents=%d preview_docs=%s",
		endpoint,
		model,
		compactForLog(query, maxLogTextRunes),
		utf8.RuneCountInString(query),
		len(documents),
		string(previewJSON),
	)
}

func compactForLog(text string, maxRunes int) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if utf8.RuneCountInString(normalized) <= maxRunes {
		return normalized
	}
	return string([]rune(normalized)[:maxRunes]) + "...(truncated)"
}
