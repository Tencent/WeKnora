package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sync/errgroup"
)

const (
	// wikiLeafRelateBatchSize caps how many leaves go into one relatedness
	// call. Each card now carries keywords plus a one-line summary; 60
	// items in a single JSON judgement tends to skip middle rows and
	// treat similar names as related. Larger catalogs are split across
	// parallel batches.
	wikiLeafRelateBatchSize = 30
	// wikiLeafRelateMaxParallel caps concurrent relatedness calls so a
	// large catalog cannot stampede the synthesis model.
	wikiLeafRelateMaxParallel  = 4
	wikiAssocDefaultLimit      = 20
	wikiAssocMaxLimit          = 50
	wikiAssocContentMaxRunes   = 6000
	wikiAssocChunkMaxRunes     = 2000
	wikiAssocMaxChunksPerLeaf  = 8
	wikiLeafRelateMaxTokens            = 2048
	wikiLeafRelateDescMaxRunes         = 200
	wikiLeafRelateCallerPromptMaxRunes = 4000
)

// AssociateLeaves asks the wiki synthesis model which entity/concept leaves
// match the question, then assembles directory, sources and cited chunks.
// relatePrompt is optional MCP judging text; empty keeps the default
// writing-strategy criterion. The model sees slug, keywords, and the first
// markdown paragraph under the title when present.
func (s *wikiPageService) AssociateLeaves(
	ctx context.Context,
	kbID string,
	query string,
	limit int,
	relatePrompt string,
) (*types.WikiKnowledgeAssocResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return &types.WikiKnowledgeAssocResult{Query: query, Tree: nil}, nil
	}
	if limit <= 0 {
		limit = wikiAssocDefaultLimit
	}
	if limit > wikiAssocMaxLimit {
		limit = wikiAssocMaxLimit
	}

	chatModel, err := s.wikiAssocChatModel(ctx, kbID)
	if err != nil {
		return nil, err
	}

	cards, err := s.repo.ListLeafKeywordCards(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("list wiki leaf keywords: %w", err)
	}

	related, err := s.relateLeafCards(ctx, chatModel, query, cards, relatePrompt)
	if err != nil {
		return nil, err
	}
	if len(related) > limit {
		related = related[:limit]
	}

	pages, err := s.repo.ListFullPagesBySlugs(ctx, kbID, related)
	if err != nil {
		return nil, fmt.Errorf("load related wiki pages: %w", err)
	}

	chunks, err := s.loadAssocChunks(ctx, pages)
	if err != nil {
		logger.Warnf(ctx, "wiki associate: load source chunks failed: %v", err)
		chunks = nil
	}

	return buildWikiAssocResult(query, pages, chunks), nil
}

func (s *wikiPageService) wikiAssocChatModel(ctx context.Context, kbID string) (chat.Chat, error) {
	if s.modelService == nil {
		return nil, errors.New("wiki association requires a chat model service")
	}
	if s.kbService == nil {
		return nil, errors.New("wiki association requires a knowledge base")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge base: %w", err)
	}
	if kb == nil {
		return nil, errors.New("knowledge base not found")
	}
	modelID := ""
	if kb.WikiConfig != nil {
		modelID = strings.TrimSpace(kb.WikiConfig.SynthesisModelID)
	}
	if modelID == "" {
		modelID = strings.TrimSpace(kb.SummaryModelID)
	}
	if modelID == "" {
		return nil, errors.New("wiki synthesis model is not configured")
	}
	return s.modelService.GetChatModel(ctx, modelID)
}

func (s *wikiPageService) relateLeafCards(
	ctx context.Context,
	chatModel chat.Chat,
	query string,
	cards []types.WikiLeafKeywordCard,
	relatePrompt string,
) ([]string, error) {
	if len(cards) == 0 {
		return nil, nil
	}

	allowed := make(map[string]struct{}, len(cards))
	for _, c := range cards {
		if c.Slug != "" {
			allowed[c.Slug] = struct{}{}
		}
	}

	eg, ectx := errgroup.WithContext(ctx)
	eg.SetLimit(wikiLeafRelateMaxParallel)

	var mu sync.Mutex
	seen := make(map[string]struct{})
	var related []string
	var lastErr error
	success := 0

	for start := 0; start < len(cards); start += wikiLeafRelateBatchSize {
		end := start + wikiLeafRelateBatchSize
		if end > len(cards) {
			end = len(cards)
		}
		batch := cards[start:end]
		batchStart, batchEnd := start, end
		eg.Go(func() error {
			slugs, err := s.relateLeafBatch(ectx, chatModel, query, batch, relatePrompt)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				lastErr = err
				logger.Warnf(ectx, "wiki associate: leaf batch %d-%d failed: %v", batchStart, batchEnd, err)
				return nil
			}
			success++
			for _, slug := range slugs {
				if _, ok := allowed[slug]; !ok {
					continue
				}
				if _, dup := seen[slug]; dup {
					continue
				}
				seen[slug] = struct{}{}
				related = append(related, slug)
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if success == 0 && lastErr != nil {
		return nil, lastErr
	}
	sort.Strings(related)
	return related, nil
}

func (s *wikiPageService) relateLeafBatch(
	ctx context.Context,
	chatModel chat.Chat,
	query string,
	batch []types.WikiLeafKeywordCard,
	relatePrompt string,
) ([]string, error) {
	raw, err := generateLeafRelate(ctx, chatModel, query, formatLeafCardsForPrompt(batch), relatePrompt)
	if err != nil {
		return nil, err
	}
	return parseWikiRelatedSlugs(raw), nil
}

func generateLeafRelate(ctx context.Context, chatModel chat.Chat, query, leaves, relatePrompt string) (string, error) {
	tmplSrc := agent.WikiLeafRelatePrompt
	data := map[string]string{
		"Question": query,
		"Leaves":   leaves,
	}
	if criteria := strings.TrimSpace(relatePrompt); criteria != "" {
		truncated, _ := truncateAssocText(criteria, wikiLeafRelateCallerPromptMaxRunes)
		tmplSrc = agent.WikiLeafRelateCallerPrompt
		data["Criteria"] = truncated
	}
	tmpl, err := template.New("wiki-leaf-relate").Parse(tmplSrc)
	if err != nil {
		return "", fmt.Errorf("parse leaf-relate prompt: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute leaf-relate prompt: %w", err)
	}

	thinking := false
	opts := &chat.ChatOptions{Temperature: 0, Thinking: &thinking, MaxTokens: wikiLeafRelateMaxTokens}
	ctx = types.WithLLMCallMetadata(ctx, "wiki_leaf_relate", "")
	resp, err := chatModel.Chat(ctx, []chat.Message{{Role: "user", Content: buf.String()}}, opts)
	if err != nil {
		return "", fmt.Errorf("leaf-relate LLM call: %w", err)
	}
	if resp == nil {
		return "", errors.New("leaf-relate LLM returned nil response")
	}
	return resp.Content, nil
}

func (s *wikiPageService) loadAssocChunks(ctx context.Context, pages []*types.WikiPage) (map[string]*types.Chunk, error) {
	if s.chunkRepo == nil || len(pages) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var ids []string
	for _, p := range pages {
		if p == nil {
			continue
		}
		for _, id := range p.ChunkRefs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := s.chunkRepo.ListChunksByIDOnly(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*types.Chunk, len(rows))
	for _, c := range rows {
		if c != nil && c.ID != "" {
			out[c.ID] = c
		}
	}
	return out, nil
}

func formatLeafCardsForPrompt(cards []types.WikiLeafKeywordCard) string {
	var b strings.Builder
	for _, c := range cards {
		line := fmt.Sprintf("- id: %s | keywords: %s", c.Slug, leafKeywords(c))
		if desc := leafDescription(c); desc != "" {
			line += " | desc: " + desc
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func leafDescription(c types.WikiLeafKeywordCard) string {
	s := firstMarkdownParagraph(c.Content)
	if s == "" {
		s = strings.TrimSpace(c.Summary)
		s = strings.TrimSpace(strings.TrimPrefix(s, "SUMMARY:"))
	}
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	truncated, _ := truncateAssocText(s, wikiLeafRelateDescMaxRunes)
	return truncated
}

var (
	mdATXHeading  = regexp.MustCompile(`^#{1,6}\s+`)
	mdWikiLink    = regexp.MustCompile(`\[\[([^\[\]|]+)(?:\|([^\]]+))?\]\]`)
	mdEmphasis    = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`)
	mdInlineImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
)

func firstMarkdownParagraph(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || isMarkdownSkipLine(t) {
			i++
			continue
		}
		break
	}
	if i >= len(lines) {
		return ""
	}
	var para []string
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || isMarkdownSkipLine(t) {
			break
		}
		para = append(para, t)
		i++
	}
	s := strings.Join(para, " ")
	s = mdInlineImage.ReplaceAllString(s, "")
	s = mdWikiLink.ReplaceAllStringFunc(s, func(match string) string {
		sub := mdWikiLink.FindStringSubmatch(match)
		if len(sub) >= 3 && strings.TrimSpace(sub[2]) != "" {
			return strings.TrimSpace(sub[2])
		}
		if len(sub) >= 2 {
			slug := strings.TrimSpace(sub[1])
			if idx := strings.LastIndex(slug, "/"); idx >= 0 {
				return slug[idx+1:]
			}
			return slug
		}
		return ""
	})
	s = mdEmphasis.ReplaceAllString(s, "$1")
	return strings.Join(strings.Fields(s), " ")
}

func isMarkdownSkipLine(t string) bool {
	if mdATXHeading.MatchString(t) {
		return true
	}
	if strings.HasPrefix(t, "![") {
		return true
	}
	if t == "---" || t == "***" || t == "___" {
		return true
	}
	return false
}

func leafKeywords(c types.WikiLeafKeywordCard) string {
	parts := make([]string, 0, 1+len(c.Aliases))
	if t := strings.TrimSpace(c.Title); t != "" {
		parts = append(parts, t)
	}
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		seen[strings.ToLower(p)] = struct{}{}
	}
	for _, alias := range c.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, alias)
	}
	if len(parts) == 0 {
		return c.Slug
	}
	return strings.Join(parts, ", ")
}

func parseWikiRelatedSlugs(raw string) []string {
	raw = cleanLLMJSON(raw)
	if raw == "" {
		return nil
	}
	var envelope struct {
		Related []json.RawMessage `json:"related"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil
	}
	out := make([]string, 0, len(envelope.Related))
	seen := make(map[string]struct{}, len(envelope.Related))
	for _, item := range envelope.Related {
		slug := parseWikiRelatedSlug(item)
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		out = append(out, slug)
	}
	return out
}

func parseWikiRelatedSlug(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var obj struct {
		Slug string `json:"slug"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if slug := strings.TrimSpace(obj.Slug); slug != "" {
		return slug
	}
	return strings.TrimSpace(obj.ID)
}

func buildWikiAssocResult(query string, pages []*types.WikiPage, chunks map[string]*types.Chunk) *types.WikiKnowledgeAssocResult {
	leavesByPath := make(map[string][]*types.WikiKnowledgeAssocLeaf)
	var pathOrder []string
	flat := make([]*types.WikiPage, 0, len(pages))

	for _, p := range pages {
		if p == nil {
			continue
		}
		flat = append(flat, p)
		leaf := wikiPageToAssocLeaf(p, chunks)
		key := strings.Join([]string(p.CategoryPath), "\x00")
		if _, ok := leavesByPath[key]; !ok {
			pathOrder = append(pathOrder, key)
		}
		leavesByPath[key] = append(leavesByPath[key], leaf)
	}

	sort.SliceStable(flat, func(i, j int) bool {
		if flat[i].WikiPath != flat[j].WikiPath {
			return flat[i].WikiPath < flat[j].WikiPath
		}
		return flat[i].Slug < flat[j].Slug
	})

	root := &assocTrieNode{children: map[string]*assocTrieNode{}}
	for _, key := range pathOrder {
		var path []string
		if key != "" {
			path = strings.Split(key, "\x00")
		}
		node := root
		for i, seg := range path {
			child, ok := node.children[seg]
			if !ok {
				child = &assocTrieNode{
					name:     seg,
					path:     append(append([]string{}, path[:i]...), seg),
					children: map[string]*assocTrieNode{},
				}
				node.children[seg] = child
			}
			node = child
		}
		node.leaves = append(node.leaves, leavesByPath[key]...)
	}

	return &types.WikiKnowledgeAssocResult{
		Query: query,
		Tree:  root.toAssocNodes(),
		Pages: flat,
	}
}

type assocTrieNode struct {
	name     string
	path     []string
	children map[string]*assocTrieNode
	leaves   []*types.WikiKnowledgeAssocLeaf
}

func (n *assocTrieNode) toAssocNodes() []*types.WikiKnowledgeAssocNode {
	if n == nil {
		return nil
	}
	out := make([]*types.WikiKnowledgeAssocNode, 0, len(n.children)+1)
	if len(n.leaves) > 0 {
		out = append(out, &types.WikiKnowledgeAssocNode{Leaves: sortAssocLeaves(n.leaves)})
	}
	names := sortedChildNames(n.children)
	for _, name := range names {
		out = append(out, n.children[name].toAssocNode())
	}
	return out
}

func (n *assocTrieNode) toAssocNode() *types.WikiKnowledgeAssocNode {
	names := sortedChildNames(n.children)
	children := make([]*types.WikiKnowledgeAssocNode, 0, len(names))
	for _, name := range names {
		children = append(children, n.children[name].toAssocNode())
	}
	return &types.WikiKnowledgeAssocNode{
		Name:     n.name,
		Path:     n.path,
		Children: children,
		Leaves:   sortAssocLeaves(n.leaves),
	}
}

func sortedChildNames(children map[string]*assocTrieNode) []string {
	names := make([]string, 0, len(children))
	for name := range children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortAssocLeaves(leaves []*types.WikiKnowledgeAssocLeaf) []*types.WikiKnowledgeAssocLeaf {
	sort.SliceStable(leaves, func(i, j int) bool {
		if leaves[i].WikiPath != leaves[j].WikiPath {
			return leaves[i].WikiPath < leaves[j].WikiPath
		}
		return leaves[i].Slug < leaves[j].Slug
	})
	return leaves
}

func wikiPageToAssocLeaf(p *types.WikiPage, chunks map[string]*types.Chunk) *types.WikiKnowledgeAssocLeaf {
	content, truncated := truncateAssocText(p.Content, wikiAssocContentMaxRunes)
	return &types.WikiKnowledgeAssocLeaf{
		Slug:             p.Slug,
		Title:            p.Title,
		PageType:         p.PageType,
		Aliases:          p.Aliases,
		Summary:          p.Summary,
		Content:          content,
		ContentTruncated: truncated,
		CategoryPath:     p.CategoryPath,
		WikiPath:         p.WikiPath,
		FolderID:         p.FolderID,
		Sources:          parseWikiAssocSources(p.SourceRefs),
		Chunks:           attachWikiAssocChunks(p.ChunkRefs, chunks),
		InLinks:          p.InLinks,
		OutLinks:         p.OutLinks,
	}
}

func parseWikiAssocSources(refs types.StringArray) []types.WikiAssocSource {
	if len(refs) == 0 {
		return nil
	}
	out := make([]types.WikiAssocSource, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		id, title := splitWikiSourceRef(ref)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, types.WikiAssocSource{KnowledgeID: id, Title: title})
	}
	return out
}

func splitWikiSourceRef(ref string) (id, title string) {
	if i := strings.Index(ref, "|"); i >= 0 {
		return strings.TrimSpace(ref[:i]), strings.TrimSpace(ref[i+1:])
	}
	return ref, ""
}

func attachWikiAssocChunks(refs types.StringArray, chunks map[string]*types.Chunk) []types.WikiAssocChunk {
	if len(refs) == 0 {
		return nil
	}
	out := make([]types.WikiAssocChunk, 0, min(len(refs), wikiAssocMaxChunksPerLeaf))
	for _, id := range refs {
		if len(out) >= wikiAssocMaxChunksPerLeaf {
			break
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		item := types.WikiAssocChunk{ID: id}
		if c, ok := chunks[id]; ok && c != nil {
			item.KnowledgeID = c.KnowledgeID
			item.Content, item.Truncated = truncateAssocText(c.Content, wikiAssocChunkMaxRunes)
		}
		out = append(out, item)
	}
	return out
}

func truncateAssocText(s string, maxRunes int) (string, bool) {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s, false
	}
	return string([]rune(s)[:maxRunes]) + "...", true
}
