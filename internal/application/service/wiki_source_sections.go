package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	wikiSourcePageMarker = "<!-- weknora-wiki-sources -->\n"
	wikiSourceOpen       = "<!-- weknora-source "
	wikiSourceClose      = "<!-- /weknora-source -->"
)

// Unattributed parts belong to the editor. Attributed parts can have multiple
// supporting sources, each with its own chunk IDs.
type wikiSourcePart struct {
	text    string
	sources map[string][]string
}

const wikiSourceModeKey = "source_sections"

func wikiUsesSourceSections(page *types.WikiPage) bool {
	if len(page.PageMetadata) == 0 {
		return false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(page.PageMetadata, &metadata) != nil {
		return false
	}
	var enabled bool
	return json.Unmarshal(metadata[wikiSourceModeKey], &enabled) == nil && enabled
}

func setWikiSourceSectionMode(page *types.WikiPage, enabled bool) error {
	var metadata map[string]json.RawMessage
	if len(page.PageMetadata) > 0 {
		if err := json.Unmarshal(page.PageMetadata, &metadata); err != nil {
			return fmt.Errorf("read Wiki page metadata: %w", err)
		}
	}
	if enabled {
		if metadata == nil {
			metadata = make(map[string]json.RawMessage)
		}
		metadata[wikiSourceModeKey] = json.RawMessage("true")
	} else {
		delete(metadata, wikiSourceModeKey)
	}
	if len(metadata) == 0 {
		page.PageMetadata = nil
		return nil
	}
	newMetadata, err := json.Marshal(metadata)
	page.PageMetadata = newMetadata
	return err
}

func wikiHasSourceSections(content string) bool {
	return strings.HasPrefix(content, wikiSourcePageMarker)
}

func parseWikiSourceParts(content string) ([]wikiSourcePart, error) {
	if !wikiHasSourceSections(content) {
		return nil, errors.New("missing Wiki source page marker")
	}
	var parts []wikiSourcePart
	var text strings.Builder
	var sources map[string][]string
	for line := range strings.SplitAfterSeq(strings.TrimPrefix(content, wikiSourcePageMarker), "\n") {
		marker := strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(marker, wikiSourceOpen):
			if sources != nil || !strings.HasSuffix(marker, " -->") {
				return nil, errors.New("malformed Wiki source section")
			}
			raw := strings.TrimSuffix(strings.TrimPrefix(marker, wikiSourceOpen), " -->")
			if err := json.Unmarshal([]byte(raw), &sources); err != nil || len(sources) == 0 {
				return nil, errors.New("invalid Wiki source section attribution")
			}
			if text.Len() != 0 {
				parts = append(parts, wikiSourcePart{text: text.String()})
				text.Reset()
			}
		case marker == wikiSourceClose:
			if sources == nil {
				return nil, errors.New("unexpected Wiki source section end")
			}
			parts = append(parts, wikiSourcePart{text: text.String(), sources: sources})
			text.Reset()
			sources = nil
		default:
			text.WriteString(line)
		}
	}
	if sources != nil {
		return nil, errors.New("unclosed Wiki source section")
	}
	if text.Len() != 0 {
		parts = append(parts, wikiSourcePart{text: text.String()})
	}
	return parts, nil
}

func renderWikiSourceParts(parts []wikiSourcePart) string {
	var body strings.Builder
	body.WriteString(wikiSourcePageMarker)
	for _, part := range parts {
		if len(part.sources) == 0 {
			body.WriteString(part.text)
			if part.text != "" && !strings.HasSuffix(part.text, "\n") {
				body.WriteByte('\n')
			}
			continue
		}
		sources, _ := json.Marshal(part.sources)
		body.WriteString(wikiSourceOpen)
		body.WriteString(string(sources))
		body.WriteString(" -->\n")
		body.WriteString(part.text)
		body.WriteString(wikiSourceClose)
		body.WriteByte('\n')
	}
	return body.String()
}

func hasWikiSourceMarkers(text string) bool {
	return strings.Contains(text, wikiSourceOpen) || strings.Contains(text, wikiSourceClose) ||
		strings.Contains(text, strings.TrimSpace(wikiSourcePageMarker))
}

func wikiSourcePartText(text string) (string, error) {
	text = strings.TrimSpace(stripWikiInlineChunkCitations(text))
	if text == "" || hasWikiSourceMarkers(text) {
		return "", errors.New("a Wiki source section needs content without source markers")
	}
	return text + "\n", nil
}

func wikiSourcePartRefs(parts []wikiSourcePart) (map[string]bool, types.StringArray) {
	sources := make(map[string]bool)
	chunkSet := make(map[string]bool)
	var chunks types.StringArray
	for _, part := range parts {
		for source, ids := range part.sources {
			sources[source] = true
			for _, id := range ids {
				if !chunkSet[id] {
					chunks = append(chunks, id)
					chunkSet[id] = true
				}
			}
		}
	}
	slices.Sort(chunks)
	return sources, chunks
}

func (s *wikiIngestService) updateWikiSourceSections(ctx context.Context, page *types.WikiPage,
	updates, additions, retracts []SlugUpdate, tenantID uint64,
) (bool, error) {
	parts, err := parseWikiSourceParts(page.Content)
	if err != nil {
		return false, err
	}
	changedSources := make(map[string]bool)
	for _, retract := range retracts {
		changedSources[retract.KnowledgeID] = true
	}
	for _, add := range additions {
		changedSources[add.KnowledgeID] = true
	}
	kept := removeWikiSourceParts(parts, changedSources)

	chunkContent := s.resolveCitedChunks(ctx, tenantID, additions)
	refs := make(map[string]string)
	for _, ref := range page.SourceRefs {
		refs[types.WikiSourceKnowledgeID(ref)] = ref
	}
	prevAliases := len(page.Aliases)
	for _, add := range additions {
		text := add.Item.Details
		if strings.TrimSpace(text) == "" {
			text = add.Item.Description
		}
		if strings.TrimSpace(text) == "" {
			text = collectCitedChunkContent(add.SourceChunks, chunkContent)
		}
		body, err := wikiSourcePartText(text)
		if err != nil {
			return false, fmt.Errorf("wiki source %s: %w", add.KnowledgeID, err)
		}
		var chunkIDs []string
		for _, id := range add.SourceChunks {
			if chunkContent[id] != "" && !slices.Contains(chunkIDs, id) {
				chunkIDs = append(chunkIDs, id)
			}
		}
		kept = append(kept, wikiSourcePart{text: body, sources: map[string][]string{add.KnowledgeID: chunkIDs}})
		refs[add.KnowledgeID] = add.SourceRef
		for _, alias := range add.Item.Aliases {
			page.Aliases = appendUnique(page.Aliases, alias)
		}
	}

	sources, chunkRefs := wikiSourcePartRefs(kept)
	var sourceRefs types.StringArray
	for source := range sources {
		ref := refs[source]
		if ref == "" {
			ref = source
		}
		sourceRefs = append(sourceRefs, ref)
	}
	slices.Sort(sourceRefs)
	content := renderWikiSourceParts(kept)
	if content == page.Content && slices.Equal(sourceRefs, page.SourceRefs) &&
		slices.Equal(chunkRefs, page.ChunkRefs) && prevAliases == len(page.Aliases) {
		return false, nil
	}
	if err := s.checkWikiPageRetrySources(ctx, page.KnowledgeBaseID, updates); err != nil {
		return false, err
	}
	// Replacing attributed text can intentionally remove old rows.
	ctx = context.WithValue(ctx, wikiSourceRebuildKey{}, true)
	page.Content, page.SourceRefs, page.ChunkRefs = content, sourceRefs, chunkRefs
	_, err = s.wikiService.UpdatePage(types.WithWikiShrinkAllowed(ctx), page)
	return err == nil, err
}

func removeWikiSourceParts(parts []wikiSourcePart, removed map[string]bool) []wikiSourcePart {
	kept := parts[:0]
	for _, part := range parts {
		if len(part.sources) != 0 {
			for source := range part.sources {
				if removed[source] {
					delete(part.sources, source)
				}
			}
			if len(part.sources) == 0 {
				continue
			}
		}
		kept = append(kept, part)
	}
	return kept
}

type wikiSourceRebuildKey struct{}

// ErrWikiSourceSectionsEdited prompts callers to use a source rebuild for attributed passages.
var ErrWikiSourceSectionsEdited = errors.New("edit attributed Wiki passages through the source rebuild preview")

func checkWikiSourceManualEdit(ctx context.Context, existing *types.WikiPage, incoming string) error {
	if !wikiUsesSourceSections(existing) || incoming == existing.Content || ctx.Value(wikiSourceRebuildKey{}) == true {
		return nil
	}
	if !wikiHasSourceSections(incoming) {
		return ErrWikiSourceSectionsEdited
	}
	previous, err := parseWikiSourceParts(existing.Content)
	if err != nil {
		return err
	}
	next, err := parseWikiSourceParts(incoming)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWikiSourceSectionsEdited, err)
	}
	previous = slices.DeleteFunc(previous, func(part wikiSourcePart) bool { return part.sources == nil })
	next = slices.DeleteFunc(next, func(part wikiSourcePart) bool { return part.sources == nil })
	if !slices.EqualFunc(previous, next, func(left, right wikiSourcePart) bool {
		return left.text == right.text && maps.EqualFunc(left.sources, right.sources, slices.Equal[[]string])
	}) {
		return ErrWikiSourceSectionsEdited
	}
	return nil
}

func (s *knowledgeService) retractWikiSourceSection(ctx context.Context, page *types.WikiPage, source string) error {
	parts, err := parseWikiSourceParts(page.Content)
	if err != nil {
		return err
	}
	parts = removeWikiSourceParts(parts, map[string]bool{source: true})
	_, chunks := wikiSourcePartRefs(parts)
	ctx = types.WithWikiPageWriteSnapshot(ctx, page)
	ctx = context.WithValue(ctx, wikiSourceRebuildKey{}, true)
	ctx = types.WithWikiShrinkAllowed(ctx)
	page.Content = renderWikiSourceParts(parts)
	page.ChunkRefs = chunks
	page.SourceRefs = removeSourceRef(page.SourceRefs, source)
	_, err = s.wikiService.UpdatePage(ctx, page)
	return err
}
