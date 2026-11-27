package service

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"golang.org/x/sync/errgroup"
)

const (
	wikiExtractionTargetRunes = 4096
	wikiExtractionMaxRunes    = 8192
	wikiExtractionOverlap     = 512
)

// Headings and paragraph boundaries approximate topic boundaries without an
// extra model call. Keep summary synthesis on its separate, larger budget.
func splitWikiExtractionDocument(content string) []string {
	runes := []rune(content)
	if len(runes) <= wikiExtractionMaxRunes {
		return []string{content}
	}
	var headings, paragraphs, lines []int
	var fence rune
	fenceWidth := 0
	for start := 0; start < len(runes); {
		end := start
		for end < len(runes) && runes[end] != '\n' {
			end++
		}
		line := strings.TrimSpace(string(runes[start:end]))
		if end < len(runes) {
			end++
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			chars := []rune(line)
			width := 0
			for width < len(chars) && chars[width] == chars[0] {
				width++
			}
			if fence == 0 {
				fence, fenceWidth = chars[0], width
			} else if fence == chars[0] && width >= fenceWidth && strings.TrimSpace(string(chars[width:])) == "" {
				fence = 0
			}
		} else if fence == 0 {
			n := 0
			for n < len(line) && line[n] == '#' {
				n++
			}
			if n > 0 && n <= 6 && n < len(line) && (line[n] == ' ' || line[n] == '\t') {
				headings = append(headings, start)
			}
			if line == "" {
				paragraphs = append(paragraphs, end)
			}
			lines = append(lines, end)
		}
		start = end
	}
	var parts []string
	for start := 0; start < len(runes); {
		if len(runes)-start <= wikiExtractionMaxRunes {
			parts = append(parts, string(runes[start:]))
			break
		}
		minimum := start + wikiExtractionTargetRunes/2
		target := start + wikiExtractionTargetRunes
		maximum := start + wikiExtractionMaxRunes
		cut, topicBoundary := 0, false
		for _, pos := range headings {
			if pos >= minimum && pos <= maximum {
				cut, topicBoundary = pos, true
				break
			}
		}
		if cut == 0 {
			for _, pos := range paragraphs {
				if pos >= minimum && pos <= target {
					cut = pos
				} else if pos > target && pos <= maximum {
					if cut == 0 {
						cut = pos
					}
					break
				}
			}
		}
		if cut == 0 {
			for _, pos := range lines {
				if pos >= minimum && pos <= maximum {
					cut = pos
				}
			}
		}
		if cut == 0 {
			cut = maximum
			for pos := maximum; pos > target; pos-- {
				if strings.ContainsRune("。！？.!?；;", runes[pos-1]) {
					cut = pos
					break
				}
			}
		}
		parts = append(parts, string(runes[start:cut]))
		start = cut
		if !topicBoundary {
			start -= wikiExtractionOverlap
			// Avoid starting halfway through an ASCII identifier where possible.
			for start < cut && runes[start] < 128 && !unicode.IsSpace(runes[start]) {
				start++
			}
		}
	}
	return parts
}

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
func extractWikiSegments(
	ctx context.Context, content string, parallel int,
	extract func(context.Context, string) (combinedExtraction, error),
) (combinedExtraction, error) {
	segments := splitWikiExtractionDocument(content)
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(segments) {
		parallel = len(segments)
	}
	partials := make([]combinedExtraction, len(segments))
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(parallel)
	for i, segment := range segments {
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			partial, err := extract(groupCtx, segment)
			if err != nil {
				return fmt.Errorf("wiki extraction segment %d: %w", i+1, err)
			}
			partials[i] = partial
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return combinedExtraction{}, err
	}
	if err := ctx.Err(); err != nil {
		return combinedExtraction{}, err
	}
	// Only merge after every segment succeeds, in original document order.
	// Completion order must not choose canonical slugs or reorder source facts.
	var result combinedExtraction
	entities, concepts := make(map[string]int), make(map[string]int)
	usedSlugs := make(map[string]string)
	merge := func(target *[]extractedItem, positions map[string]int, incoming []extractedItem) {
		for _, item := range incoming {
			if item.Slug == "" || item.Name == "" {
				continue
			}
			identity := normalizeWikiIdentityTitle(item.Name)
			index, found := positions[identity]
			if !found {
				for i, previous := range *target {
					// A shared generic alias is insufficient. Both segments must
					// explicitly name the other candidate as the same subject.
					if wikiExtractionNamesAliasEachOther(previous, item) {
						index, found = i, true
						positions[identity] = i
						break
					}
				}
			}
			if found {
				(*target)[index] = mergeExtractedIdentity((*target)[index], item)
			} else {
				item.Slug = unoccupiedWikiExtractionSlug(item, usedSlugs)
				usedSlugs[item.Slug] = identity
				positions[identity] = len(*target)
				*target = append(*target, item)
			}
		}
	}
	for _, partial := range partials {
		merge(&result.Entities, entities, partial.Entities)
		merge(&result.Concepts, concepts, partial.Concepts)
	}
	return result, nil
}

func wikiExtractionNamesAliasEachOther(a, b extractedItem) bool {
	has := func(aliases []string, name string) bool {
		for _, alias := range aliases {
			if normalizeWikiIdentityTitle(alias) == normalizeWikiIdentityTitle(name) {
				return true
			}
		}
		return false
	}
	return has(a.Aliases, b.Name) && has(b.Aliases, a.Name)
}

// Pinyin is not identity: 伊犁 and 仪礼 can both arrive as entity/yi-li.
// Keep a distinct provisional slug so KB-level semantic resolution sees both.
func unoccupiedWikiExtractionSlug(item extractedItem, occupied map[string]string) string {
	identity := normalizeWikiIdentityTitle(item.Name)
	if other, exists := occupied[item.Slug]; !exists || other == identity {
		return item.Slug
	}
	prefix, _, _ := strings.Cut(item.Slug, "/")
	base := prefix + "/" + slugify(item.Name)
	for candidate, n := base, 2; ; n++ {
		if other, exists := occupied[candidate]; !exists || other == identity {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
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
