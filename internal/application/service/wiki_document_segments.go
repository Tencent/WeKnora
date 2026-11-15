package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// Bound each model input, not the document. Preserve every rune, with overlap
// so an entity whose name crosses a segment boundary can still be extracted.
func splitWikiDocument(content string) []string {
	runes := []rune(content)
	if len(runes) <= maxContentForWiki {
		return []string{content}
	}
	var parts []string
	for start := 0; start < len(runes); {
		end := start + maxContentForWiki
		if end >= len(runes) {
			parts = append(parts, string(runes[start:]))
			break
		}
		for cut := end; cut > end-2048; cut-- {
			if runes[cut-1] == '\n' {
				end = cut
				break
			}
		}
		parts = append(parts, string(runes[start:end]))
		start = end - 512
	}
	return parts
}

// A failed segment fails the extraction; a successful prefix is never reported
// as complete. Deduplicate canonical slugs before the existing KB-level resolver.
func extractWikiSegments(content string, extract func(string) (combinedExtraction, error)) (combinedExtraction, error) {
	var result combinedExtraction
	entities, concepts := make(map[string]int), make(map[string]int)
	merge := func(target *[]extractedItem, positions map[string]int, incoming []extractedItem) {
		for _, item := range incoming {
			if item.Slug == "" || item.Name == "" {
				continue
			}
			if index, ok := positions[item.Slug]; ok {
				current := &(*target)[index]
				for _, alias := range item.Aliases {
					current.Aliases = appendUnique(current.Aliases, alias)
				}
				if item.Details != "" && !strings.Contains(current.Details, item.Details) {
					current.Details += "\n" + item.Details
				}
			} else {
				positions[item.Slug] = len(*target)
				*target = append(*target, item)
			}
		}
	}
	for i, segment := range splitWikiDocument(content) {
		partial, err := extract(segment)
		if err != nil {
			return combinedExtraction{}, fmt.Errorf("wiki extraction segment %d: %w", i+1, err)
		}
		merge(&result.Entities, entities, partial.Entities)
		merge(&result.Concepts, concepts, partial.Concepts)
	}
	return result, nil
}

func (s *wikiIngestService) generateWikiDocumentSummary(
	ctx context.Context, model chat.Chat, variables map[string]string,
) (string, error) {
	content := variables["Content"]
	for {
		segments := splitWikiDocument(content)
		summaries := make([]string, 0, len(segments))
		for i, segment := range segments {
			input := make(map[string]string, len(variables))
			for key, value := range variables {
				input[key] = value
			}
			input["Content"] = segment
			summary, err := s.generateWithTemplate(ctx, model, agent.WikiSummaryPrompt, input)
			if err != nil {
				return "", fmt.Errorf("wiki summary segment %d: %w", i+1, err)
			}
			if strings.TrimSpace(summary) == "" {
				return "", fmt.Errorf("empty wiki summary segment %d", i+1)
			}
			summaries = append(summaries, summary)
		}
		if len(summaries) == 1 {
			return summaries[0], nil
		}
		combined := strings.Join(summaries, "\n\n")
		if len([]rune(combined)) >= len([]rune(content)) {
			return "", fmt.Errorf("wiki section summaries exceed document budget")
		}
		content = combined
	}
}
