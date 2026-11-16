package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	temporaryDocumentInlineTokens   = 12000
	temporaryDocumentPromptBudget   = 12000
	temporaryDocumentMaxPromptParts = 16
	// agentAttachmentInlineBudget caps the tokens of small attachments an
	// agent turn inlines in total; the rest stay on demand.
	agentAttachmentInlineBudget         = 6000
	temporaryDocumentOutlineMaxHeadings = 30
	temporaryDocumentOutlineMaxRunes    = 1500
)

func (s *temporaryDocumentService) ResolveForPrompt(
	ctx context.Context, tenantID uint64, sessionID string, documentIDs []string, query string,
) (*types.TemporaryDocumentPromptResult, error) {
	result := &types.TemporaryDocumentPromptResult{}
	if len(documentIDs) > types.MaxTemporaryAttachmentsPerMessage {
		return nil, fmt.Errorf("a message can use at most %d attachments", types.MaxTemporaryAttachmentsPerMessage)
	}
	perDocumentBudget := temporaryDocumentPromptBudget
	if len(documentIDs) > 0 {
		perDocumentBudget = temporaryDocumentPromptBudget / len(documentIDs)
	}
	seen := make(map[string]struct{}, len(documentIDs))
	for _, documentID := range documentIDs {
		if _, duplicate := seen[documentID]; duplicate {
			continue
		}
		seen[documentID] = struct{}{}
		document, err := s.resolveInSession(ctx, tenantID, sessionID, documentID)
		if err != nil {
			return nil, err
		}
		if document == nil {
			return nil, fmt.Errorf("attachment %s was not found in this session", documentID)
		}
		if document.Status != types.TemporaryDocumentStatusReady {
			if document.Status == types.TemporaryDocumentStatusFailed {
				return nil, fmt.Errorf("attachment %s failed to parse: %s", document.FileName, document.ErrorMessage)
			}
			return nil, fmt.Errorf("attachment %s is still being processed", document.FileName)
		}
		content, selected, total := selectTemporaryDocumentContentWithBudget(document, query, perDocumentBudget)
		result.Attachments = append(result.Attachments, types.MessageAttachment{
			ID: document.ID, URL: document.ResourceRef, FileName: document.FileName,
			FileType: document.FileType, FileSize: document.FileSize, Content: content,
			ContentMode: map[bool]string{true: "full", false: "selected_chunks"}[selected == total],
			TokenCount:  document.TokenCount, SelectedChunks: selected, TotalChunks: total,
		})
		// Only standalone image attachments automatically enter Quick Answer's
		// visual context. Document figures are read on demand in Agent mode.
		if docparser.IsImageFormat(document.FileType) {
			for _, image := range temporaryDocumentImageRefs(document.ImageRefs) {
				if image.URL != "" && len(result.ImageURLs) < 4 {
					result.ImageURLs = append(result.ImageURLs, image.URL)
				}
			}
		}
	}
	return result, nil
}

// DescribeForAgent returns a manifest view of the given documents for an
// agent turn. Unlike ResolveForPrompt it neither waits for parsing nor
// selects content by the question: the agent reads what it needs through
// read_file, so only small, ready documents are inlined in full.
func (s *temporaryDocumentService) DescribeForAgent(
	ctx context.Context, tenantID uint64, sessionID string, documentIDs []string,
) (*types.TemporaryDocumentPromptResult, error) {
	result := &types.TemporaryDocumentPromptResult{}
	if len(documentIDs) > types.MaxTemporaryAttachmentsPerMessage {
		return nil, fmt.Errorf("a message can use at most %d attachments", types.MaxTemporaryAttachmentsPerMessage)
	}
	inlineBudget := agentAttachmentInlineBudget
	seen := make(map[string]struct{}, len(documentIDs))
	for _, documentID := range documentIDs {
		if _, duplicate := seen[documentID]; duplicate {
			continue
		}
		seen[documentID] = struct{}{}
		document, err := s.resolveInSession(ctx, tenantID, sessionID, documentID)
		if err != nil {
			return nil, err
		}
		if document == nil {
			continue
		}
		attachment := types.MessageAttachment{
			ID: document.ID, URL: document.ResourceRef, FileName: document.FileName,
			FileType: document.FileType, FileSize: document.FileSize,
			Status: document.Status, ContentMode: types.AttachmentContentModeOnDemand,
		}
		if document.Status == types.TemporaryDocumentStatusReady {
			images := document.ViewableImages()
			attachment.TokenCount = document.TokenCount
			attachment.TotalChunks = document.ChunkCount
			attachment.ImageCount = len(images)
			attachment.Outline = temporaryDocumentOutline(document.Content)
			if document.TokenCount <= types.AgentAttachmentInlineTokens && document.TokenCount <= inlineBudget {
				attachment.Content = types.NumberAttachmentImages(document.Content, images)
				attachment.ContentMode = types.AttachmentContentModeFull
				attachment.SelectedChunks = document.ChunkCount
				inlineBudget -= document.TokenCount
			}
			if docparser.IsImageFormat(document.FileType) {
				for _, image := range temporaryDocumentImageRefs(document.ImageRefs) {
					if image.URL != "" && len(result.ImageURLs) < 4 {
						result.ImageURLs = append(result.ImageURLs, image.URL)
					}
				}
			}
		}
		result.Attachments = append(result.Attachments, attachment)
	}
	return result, nil
}

var markdownHeadingPattern = regexp.MustCompile(`^#{1,4}\s+\S`)

// temporaryDocumentOutline lists the document's leading markdown headings so
// the agent can decide where to read before fetching any content.
func temporaryDocumentOutline(content string) string {
	var lines []string
	runes := 0
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !markdownHeadingPattern.MatchString(line) {
			continue
		}
		runes += utf8.RuneCountInString(line)
		if len(lines) >= temporaryDocumentOutlineMaxHeadings || runes > temporaryDocumentOutlineMaxRunes {
			lines = append(lines, "…")
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func selectTemporaryDocumentContent(document *types.TemporaryDocument, query string) (string, int, int) {
	return selectTemporaryDocumentContentWithBudget(document, query, temporaryDocumentPromptBudget)
}

func selectTemporaryDocumentContentWithBudget(
	document *types.TemporaryDocument, query string, budget int,
) (string, int, int) {
	var chunks []types.TemporaryDocumentChunk
	_ = json.Unmarshal(document.Chunks, &chunks)
	if budget <= 0 {
		budget = temporaryDocumentPromptBudget
	}
	if len(chunks) == 0 || (document.TokenCount <= temporaryDocumentInlineTokens && document.TokenCount <= budget) {
		return document.Content, len(chunks), len(chunks)
	}
	terms := temporaryDocumentQueryTerms(query)
	type rankedChunk struct {
		chunk types.TemporaryDocumentChunk
		score int
	}
	ranked := make([]rankedChunk, 0, len(chunks))
	for _, part := range chunks {
		text := strings.ToLower(part.ContextHeader + "\n" + part.Content)
		score := 0
		for _, term := range terms {
			score += strings.Count(text, term) * (1 + len([]rune(term))/2)
		}
		ranked = append(ranked, rankedChunk{chunk: part, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].chunk.Seq < ranked[j].chunk.Seq
		}
		return ranked[i].score > ranked[j].score
	})
	selected := make([]types.TemporaryDocumentChunk, 0, temporaryDocumentMaxPromptParts)
	tokens := 0
	for _, candidate := range ranked {
		if len(selected) >= temporaryDocumentMaxPromptParts {
			break
		}
		if tokens > 0 && tokens+candidate.chunk.TokenCount > budget {
			continue
		}
		selected = append(selected, candidate.chunk)
		tokens += candidate.chunk.TokenCount
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Seq < selected[j].Seq })
	var builder strings.Builder
	for _, part := range selected {
		if builder.Len() > 0 {
			builder.WriteString("\n\n---\n\n")
		}
		if part.ContextHeader != "" {
			builder.WriteString(part.ContextHeader)
			builder.WriteString("\n\n")
		}
		builder.WriteString(strings.TrimSpace(part.Content))
	}
	return builder.String(), len(selected), len(chunks)
}

func temporaryDocumentQueryTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	seen := make(map[string]struct{})
	var terms []string
	for _, field := range strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) {
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ok := seen[field]; !ok {
			seen[field] = struct{}{}
			terms = append(terms, field)
		}
	}
	runes := []rune(query)
	for i := 0; i+1 < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) {
			term := string(runes[i : i+2])
			if _, ok := seen[term]; !ok {
				seen[term] = struct{}{}
				terms = append(terms, term)
			}
		}
	}
	return terms
}
