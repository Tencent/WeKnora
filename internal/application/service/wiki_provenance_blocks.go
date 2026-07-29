package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

// wikiRawMarkdownBlock is the parser's lossless intermediate form. Content
// retains the original line endings and inter-block whitespace so
// renderWikiMarkdownBlocks(splitWikiMarkdownBlocks(markdown)) is byte-for-byte
// stable. Matching and evidence prompts always use a trimmed/normalised view.
type wikiRawMarkdownBlock struct {
	BlockType   string
	SectionPath types.StringArray
	Content     string
}

// splitWikiMarkdownBlocks deterministically converts generated Markdown into
// independently sourceable blocks without adding a Markdown parser dependency.
// It preserves exact source bytes in Content and carries stable logical IDs and
// source rows forward for blocks whose normalised content is unchanged.
func splitWikiMarkdownBlocks(
	content string,
	previousSet *types.WikiPageBlockSet,
	author string,
) []*types.WikiPageBlock {
	rawBlocks := splitWikiMarkdownRawBlocks(content)
	if len(rawBlocks) == 0 {
		return nil
	}

	author = types.NormalizeWikiEditSource(author)
	blocks := make([]*types.WikiPageBlock, 0, len(rawBlocks))
	for i, raw := range rawBlocks {
		blockID := uuid.NewString()
		blocks = append(blocks, &types.WikiPageBlock{
			ID:               blockID,
			LogicalBlockID:   uuid.NewString(),
			BlockType:        raw.BlockType,
			SectionPath:      append(types.StringArray(nil), raw.SectionPath...),
			SortOrder:        i,
			Content:          raw.Content,
			ContentHash:      hashWikiProvenanceText(normalizeWikiBlockText(raw.Content)),
			AuthorType:       author,
			ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
		})
	}

	if previousSet != nil {
		carryForwardWikiLogicalBlocks(previousSet.Blocks, blocks)
	}
	return blocks
}

// buildWikiSummaryBlock creates the separately stored one-line summary block.
// Summary text is not part of wiki_pages.content, but it is factual generated
// prose and therefore goes through the same citation alignment as body blocks.
func buildWikiSummaryBlock(
	summary string,
	previousSet *types.WikiPageBlockSet,
	author string,
) *types.WikiPageBlock {
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	block := &types.WikiPageBlock{
		ID:               uuid.NewString(),
		LogicalBlockID:   uuid.NewString(),
		BlockType:        types.WikiBlockTypeSummary,
		SortOrder:        -1,
		Content:          summary,
		ContentHash:      hashWikiProvenanceText(normalizeWikiBlockText(summary)),
		AuthorType:       types.NormalizeWikiEditSource(author),
		ProvenanceStatus: types.WikiBlockProvenanceUnsupported,
	}
	if previousSet != nil {
		carryForwardWikiLogicalBlocks(previousSet.Blocks, []*types.WikiPageBlock{block})
	}
	return block
}

// renderWikiMarkdownBlocks reconstructs the body in SortOrder. Parser-produced
// blocks already contain their exact separators. The small separator fallback
// also makes manually assembled blocks render as valid Markdown.
func renderWikiMarkdownBlocks(blocks []*types.WikiPageBlock) string {
	ordered := make([]*types.WikiPageBlock, 0, len(blocks))
	for _, block := range blocks {
		if block != nil && block.BlockType != types.WikiBlockTypeSummary {
			ordered = append(ordered, block)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].SortOrder < ordered[j].SortOrder
	})

	var out strings.Builder
	previousEndedWithNewline := false
	for _, block := range ordered {
		if block.Content == "" {
			continue
		}
		if out.Len() > 0 && !previousEndedWithNewline && !strings.HasPrefix(block.Content, "\n") {
			out.WriteString("\n\n")
		}
		out.WriteString(block.Content)
		previousEndedWithNewline = strings.HasSuffix(block.Content, "\n")
	}
	return out.String()
}

func splitWikiMarkdownRawBlocks(content string) []wikiRawMarkdownBlock {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	lines := splitWikiMarkdownLines(content)
	if len(lines) == 0 {
		return nil
	}

	var blocks []wikiRawMarkdownBlock
	var section [6]string
	rawStart := 0
	for i := 0; i < len(lines); {
		for i < len(lines) && isWikiBlankLine(lines[i]) {
			i++
		}
		if i >= len(lines) {
			if len(blocks) > 0 && rawStart < len(lines) {
				blocks[len(blocks)-1].Content += strings.Join(lines[rawStart:], "")
			}
			break
		}

		blockType, end, headingLevel, headingText := consumeWikiMarkdownBlock(lines, i)
		for end < len(lines) && isWikiBlankLine(lines[end]) {
			end++
		}
		if headingLevel > 0 {
			section[headingLevel-1] = headingText
			for depth := headingLevel; depth < len(section); depth++ {
				section[depth] = ""
			}
		}
		path := make(types.StringArray, 0, len(section))
		for _, part := range section {
			if part != "" {
				path = append(path, part)
			}
		}

		blocks = append(blocks, wikiRawMarkdownBlock{
			BlockType:   blockType,
			SectionPath: path,
			Content:     strings.Join(lines[rawStart:end], ""),
		})
		rawStart = end
		i = end
	}
	return blocks
}

func splitWikiMarkdownLines(content string) []string {
	lines := make([]string, 0, strings.Count(content, "\n")+1)
	for len(content) > 0 {
		newline := strings.IndexByte(content, '\n')
		if newline < 0 {
			lines = append(lines, content)
			break
		}
		lines = append(lines, content[:newline+1])
		content = content[newline+1:]
	}
	return lines
}

func consumeWikiMarkdownBlock(lines []string, start int) (blockType string, end, headingLevel int, headingText string) {
	line := wikiMarkdownLineBody(lines[start])
	if marker, width, ok := wikiFenceMarker(line); ok {
		end = start + 1
		for end < len(lines) {
			if isWikiFenceClose(wikiMarkdownLineBody(lines[end]), marker, width) {
				end++
				break
			}
			end++
		}
		// Fenced code is factual content, so it intentionally uses paragraph
		// provenance until the storage model gains a dedicated code block type.
		return types.WikiBlockTypeParagraph, end, 0, ""
	}
	if isWikiThematicBreakLine(line) {
		// Keep the existing storage vocabulary and lossless bytes, but isolate
		// layout-only thematic breaks so they never inherit a neighboring claim.
		return types.WikiBlockTypeParagraph, start + 1, 0, ""
	}
	if level, text, ok := wikiATXHeading(line); ok {
		return types.WikiBlockTypeHeading, start + 1, level, text
	}
	if start+1 < len(lines) {
		if level, ok := wikiSetextHeadingLevel(wikiMarkdownLineBody(lines[start+1])); ok && strings.TrimSpace(line) != "" {
			return types.WikiBlockTypeHeading, start + 2, level, cleanWikiHeadingText(line)
		}
	}
	if isWikiListItem(line) {
		end = start + 1
		for end < len(lines) && !isWikiBlankLine(lines[end]) && !startsWikiMarkdownBlock(lines, end) {
			end++
		}
		return types.WikiBlockTypeListItem, end, 0, ""
	}
	if isWikiQuoteLine(line) {
		end = start + 1
		for end < len(lines) && isWikiQuoteLine(wikiMarkdownLineBody(lines[end])) {
			end++
		}
		return types.WikiBlockTypeQuote, end, 0, ""
	}
	if isWikiTableRow(line) {
		return types.WikiBlockTypeTableRow, start + 1, 0, ""
	}

	end = start + 1
	for end < len(lines) && !isWikiBlankLine(lines[end]) && !startsWikiMarkdownBlock(lines, end) {
		// A paragraph immediately followed by a setext underline is one heading.
		if end+1 < len(lines) {
			if _, ok := wikiSetextHeadingLevel(wikiMarkdownLineBody(lines[end+1])); ok {
				break
			}
		}
		end++
	}
	return types.WikiBlockTypeParagraph, end, 0, ""
}

func startsWikiMarkdownBlock(lines []string, index int) bool {
	if index < 0 || index >= len(lines) {
		return false
	}
	line := wikiMarkdownLineBody(lines[index])
	if _, _, ok := wikiFenceMarker(line); ok {
		return true
	}
	if isWikiThematicBreakLine(line) {
		return true
	}
	if _, _, ok := wikiATXHeading(line); ok {
		return true
	}
	if isWikiListItem(line) || isWikiQuoteLine(line) || isWikiTableRow(line) {
		return true
	}
	return false
}

func wikiMarkdownLineBody(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

func isWikiBlankLine(line string) bool {
	return strings.TrimSpace(wikiMarkdownLineBody(line)) == ""
}

func isWikiThematicBreakLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	marker := rune(0)
	count := 0
	for _, r := range trimmed {
		if unicode.IsSpace(r) {
			continue
		}
		if r != '-' && r != '*' && r != '_' {
			return false
		}
		if marker == 0 {
			marker = r
		} else if marker != r {
			return false
		}
		count++
	}
	return count >= 3
}

func wikiATXHeading(line string) (int, string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	indent := len(line) - len(trimmed)
	if indent > 3 || trimmed == "" || trimmed[0] != '#' {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && level < 6 && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level >= len(trimmed) || !unicode.IsSpace(rune(trimmed[level])) {
		return 0, "", false
	}
	return level, cleanWikiHeadingText(trimmed[level:]), true
}

func wikiSetextHeadingLevel(line string) (int, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 {
		return 0, false
	}
	marker := trimmed[0]
	if marker != '=' && marker != '-' {
		return 0, false
	}
	for i := 1; i < len(trimmed); i++ {
		if trimmed[i] != marker {
			return 0, false
		}
	}
	if marker == '=' {
		return 1, true
	}
	return 2, true
}

func cleanWikiHeadingText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimSpace(strings.TrimRight(text, "#"))
	return text
}

func isWikiListItem(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 2 {
		return false
	}
	if (trimmed[0] == '-' || trimmed[0] == '+' || trimmed[0] == '*') && unicode.IsSpace(rune(trimmed[1])) {
		return true
	}
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')') && unicode.IsSpace(rune(trimmed[i+1]))
}

func isWikiQuoteLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return len(line)-len(trimmed) <= 3 && strings.HasPrefix(trimmed, ">")
}

func isWikiTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.Count(trimmed, "|") < 2 {
		return false
	}
	return strings.HasPrefix(trimmed, "|") || strings.HasSuffix(trimmed, "|")
}

func wikiFenceMarker(line string) (byte, int, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0, false
	}
	marker := trimmed[0]
	width := 0
	for width < len(trimmed) && trimmed[width] == marker {
		width++
	}
	if width < 3 {
		return 0, 0, false
	}
	return marker, width, true
}

func isWikiFenceClose(line string, marker byte, width int) bool {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < width {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] != marker {
			return false
		}
	}
	return true
}

func carryForwardWikiLogicalBlocks(previous, current []*types.WikiPageBlock) {
	type oldBlockRef struct {
		block *types.WikiPageBlock
		used  bool
	}
	oldRefs := make([]*oldBlockRef, 0, len(previous))
	exact := make(map[string][]int)
	contentOnly := make(map[string][]int)
	for _, block := range previous {
		if block == nil {
			continue
		}
		index := len(oldRefs)
		oldRefs = append(oldRefs, &oldBlockRef{block: block})
		exact[wikiBlockMatchKey(block, true)] = append(exact[wikiBlockMatchKey(block, true)], index)
		contentOnly[wikiBlockMatchKey(block, false)] = append(contentOnly[wikiBlockMatchKey(block, false)], index)
	}

	pick := func(indices []int) *types.WikiPageBlock {
		for _, index := range indices {
			if index >= 0 && index < len(oldRefs) && !oldRefs[index].used {
				oldRefs[index].used = true
				return oldRefs[index].block
			}
		}
		return nil
	}
	for _, block := range current {
		if block == nil {
			continue
		}
		matched := pick(exact[wikiBlockMatchKey(block, true)])
		if matched == nil {
			matched = pick(contentOnly[wikiBlockMatchKey(block, false)])
		}
		if matched == nil {
			continue
		}
		if matched.LogicalBlockID != "" {
			block.LogicalBlockID = matched.LogicalBlockID
		}
		block.AuthorType = matched.AuthorType
		block.ProvenanceStatus = matched.ProvenanceStatus
		block.Sources = cloneWikiBlockSourcesForBlock(matched.Sources, block.ID)
	}
}

func wikiBlockMatchKey(block *types.WikiPageBlock, includeSection bool) string {
	if block == nil {
		return ""
	}
	key := block.BlockType + "\x00"
	if includeSection {
		key += strings.Join(block.SectionPath, "\x1f") + "\x00"
	}
	return key + normalizeWikiBlockText(block.Content)
}

func cloneWikiBlockSourcesForBlock(sources []*types.WikiBlockSource, blockID string) []*types.WikiBlockSource {
	out := make([]*types.WikiBlockSource, 0, len(sources))
	for _, source := range sources {
		if source == nil {
			continue
		}
		clone := *source
		clone.ID = uuid.NewString()
		clone.BlockID = blockID
		clone.CitationKey = ""
		clone.CreatedAt = time.Time{}
		out = append(out, &clone)
	}
	return out
}

func normalizeWikiBlockText(value string) string {
	return collapseWikiWhitespace(strings.TrimSpace(value))
}

func normalizeWikiEvidence(value string) string {
	return collapseWikiWhitespace(strings.TrimSpace(value))
}

func collapseWikiWhitespace(value string) string {
	var out strings.Builder
	spacePending := false
	for _, r := range value {
		if unicode.IsSpace(r) {
			spacePending = out.Len() > 0
			continue
		}
		if spacePending {
			out.WriteByte(' ')
			spacePending = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

func hashWikiProvenanceText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type wikiBlockCitationClaim struct {
	Chunk    string `json:"chunk"`
	Evidence string `json:"evidence"`
}

type wikiBlockCitationResult struct {
	Citations map[string][]wikiBlockCitationClaim `json:"citations"`
}

// wikiProvenanceClaim is an invocation-local, deterministic coverage unit
// derived from one rendered Markdown block. Claims are never persisted: their
// validated sources are merged back onto the owning block after every claim is
// covered. Keeping claims transient preserves the lossless Markdown block
// model while preventing one citation from making a multi-fact paragraph look
// fully sourced.
type wikiProvenanceClaim struct {
	ID        string
	BlockID   string
	BlockType string
	Text      string
	SortOrder int
}

type wikiValidatedCitationBatch struct {
	sourcesByBlock map[string][]*types.WikiBlockSource
	coveredClaims  map[string]struct{}
}

type wikiProvenanceSourceContext struct {
	KnowledgeAttempt int
	SourceTitle      string
}

// alignWikiBlockSources is the common single-document entry point used by
// Summary ingestion. Entity/concept reduce can call
// alignWikiBlockSourcesForContexts once with all contributing documents.
func (s *wikiIngestService) alignWikiBlockSources(
	ctx context.Context,
	chatModel chat.Chat,
	blocks []*types.WikiPageBlock,
	chunks []*types.Chunk,
	knowledgeID string,
	knowledgeAttempt int,
	lang string,
) (map[string][]*types.WikiBlockSource, error) {
	contexts := map[string]wikiProvenanceSourceContext{}
	if knowledgeID != "" {
		contexts[knowledgeID] = wikiProvenanceSourceContext{KnowledgeAttempt: knowledgeAttempt}
	}
	return s.alignWikiBlockSourcesForContexts(ctx, chatModel, blocks, chunks, contexts, lang)
}

// alignWikiBlockSourcesForContexts aligns every factual block to exact source
// quotes. It is fail-closed: one failed LLM batch, malformed/forged handle,
// evidence mismatch, or unsupported factual block rejects the complete result.
// No caller-owned block is mutated.
func (s *wikiIngestService) alignWikiBlockSourcesForContexts(
	ctx context.Context,
	chatModel chat.Chat,
	blocks []*types.WikiPageBlock,
	chunks []*types.Chunk,
	sourceContexts map[string]wikiProvenanceSourceContext,
	lang string,
) (map[string][]*types.WikiBlockSource, error) {
	if chatModel == nil {
		return nil, fmt.Errorf("wiki block provenance: chat model is nil")
	}

	citeable := make([]*types.WikiPageBlock, 0, len(blocks))
	blockHandles := modelcontext.NewHandleTable("b", 3, 0)
	seenBlockIDs := make(map[string]bool)
	for _, block := range blocks {
		if !wikiBlockNeedsSource(block) {
			continue
		}
		if block.ID == "" {
			return nil, fmt.Errorf("wiki block provenance: sourceable block has no ID")
		}
		if seenBlockIDs[block.ID] {
			return nil, fmt.Errorf("wiki block provenance: duplicate block ID %q", block.ID)
		}
		seenBlockIDs[block.ID] = true
		blockHandles.Register(block.ID)
		citeable = append(citeable, block)
	}
	if len(citeable) == 0 {
		return map[string][]*types.WikiBlockSource{}, nil
	}

	claims := buildWikiProvenanceClaims(citeable)
	if len(claims) == 0 {
		return nil, fmt.Errorf("wiki block provenance: factual blocks produced no sourceable claims")
	}
	claimHandles := modelcontext.NewHandleTable("q", 3, 0)
	claimByHandle := make(map[string]wikiProvenanceClaim, len(claims))
	for _, claim := range claims {
		handle := claimHandles.Register(claim.ID)
		claimByHandle[handle] = claim
	}

	chunkByID := make(map[string]*types.Chunk)
	chunkOrder := make(map[string]int)
	for _, chunk := range chunks {
		if chunk == nil || chunk.ID == "" || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if prior, exists := chunkByID[chunk.ID]; exists {
			if prior.KnowledgeID != chunk.KnowledgeID || prior.Content != chunk.Content {
				return nil, fmt.Errorf("wiki block provenance: conflicting duplicate chunk ID %q", chunk.ID)
			}
			continue
		}
		if len(sourceContexts) > 0 {
			if _, ok := sourceContexts[chunk.KnowledgeID]; !ok {
				return nil, fmt.Errorf("wiki block provenance: chunk %s belongs to unexpected knowledge %s", chunk.ID, chunk.KnowledgeID)
			}
		}
		chunkByID[chunk.ID] = chunk
		chunkOrder[chunk.ID] = chunk.ChunkIndex
	}
	batches := splitChunksIntoCitationBatches(chunks)
	if len(batches) == 0 {
		return nil, fmt.Errorf("wiki block provenance: factual blocks have no source chunks")
	}

	blocksXML := renderWikiProvenanceBlocksXML(citeable, claims, blockHandles, claimHandles)
	results := make([]wikiValidatedCitationBatch, len(batches))
	eg, ectx := errgroup.WithContext(ctx)
	eg.SetLimit(maxCitationBatchConcurrency)
	for batchIndex := range batches {
		batchIndex := batchIndex
		batch := batches[batchIndex]
		eg.Go(func() error {
			raw, err := s.generateWithTemplate(ectx, chatModel, agent.WikiBlockCitationPrompt, map[string]string{
				"BlocksXML": blocksXML,
				"ChunksXML": renderWikiProvenanceChunksXML(batch),
				"Language":  lang,
			})
			if err != nil {
				return fmt.Errorf("wiki block provenance: citation batch %d failed: %w", batchIndex, err)
			}
			raw = cleanLLMJSON(raw)
			var parsed wikiBlockCitationResult
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				return fmt.Errorf("wiki block provenance: parse citation batch %d: %w", batchIndex, err)
			}
			validated, err := validateWikiBlockCitationBatch(
				parsed, batch, claimByHandle, chunkByID, sourceContexts,
			)
			if err != nil {
				return fmt.Errorf("wiki block provenance: validate citation batch %d: %w", batchIndex, err)
			}
			results[batchIndex] = validated
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	merged := make(map[string][]*types.WikiBlockSource, len(citeable))
	seen := make(map[string]map[string]bool, len(citeable))
	coveredClaims := make(map[string]struct{}, len(claims))
	for _, batchResult := range results {
		for claimID := range batchResult.coveredClaims {
			coveredClaims[claimID] = struct{}{}
		}
		for blockID, sources := range batchResult.sourcesByBlock {
			if seen[blockID] == nil {
				seen[blockID] = make(map[string]bool)
			}
			for _, source := range sources {
				key := source.ChunkID + "\x00" + source.EvidenceHash
				if seen[blockID][key] {
					continue
				}
				seen[blockID][key] = true
				merged[blockID] = append(merged[blockID], source)
			}
		}
	}
	for _, claim := range claims {
		if _, covered := coveredClaims[claim.ID]; !covered {
			return nil, fmt.Errorf(
				"wiki block provenance: block %s (%s) claim %d has no validated source",
				claim.BlockID, claim.BlockType, claim.SortOrder+1,
			)
		}
	}

	for _, block := range citeable {
		sources := merged[block.ID]
		if len(sources) == 0 {
			return nil, fmt.Errorf("wiki block provenance: block %s (%s) has no validated source", block.ID, block.BlockType)
		}
		sort.SliceStable(sources, func(i, j int) bool {
			left, right := chunkOrder[sources[i].ChunkID], chunkOrder[sources[j].ChunkID]
			if left != right {
				return left < right
			}
			if sources[i].ChunkID != sources[j].ChunkID {
				return sources[i].ChunkID < sources[j].ChunkID
			}
			return sources[i].EvidenceHash < sources[j].EvidenceHash
		})
		for order, source := range sources {
			source.SortOrder = order
		}
	}
	return merged, nil
}

func wikiBlockNeedsSource(block *types.WikiPageBlock) bool {
	if block == nil || strings.TrimSpace(block.Content) == "" {
		return false
	}
	if block.BlockType == types.WikiBlockTypeHeading {
		return false
	}
	// Markdown's table delimiter row is layout metadata, not a factual claim.
	// It remains a table_row for lossless rendering but must not make an
	// otherwise well-sourced page fail closed.
	if block.BlockType == types.WikiBlockTypeTableRow && isWikiTableDelimiterRow(block.Content) {
		return false
	}
	// Syntax-only blocks (thematic breaks, empty fences, etc.) have no
	// deterministic claim units and are layout rather than factual prose.
	return len(splitWikiProvenanceClaimTexts(block)) > 0
}

func isWikiTableDelimiterRow(content string) bool {
	line := strings.TrimSpace(content)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	cells := strings.Split(line, "|")
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		cell = strings.TrimPrefix(cell, ":")
		cell = strings.TrimSuffix(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func buildWikiProvenanceClaims(blocks []*types.WikiPageBlock) []wikiProvenanceClaim {
	claims := make([]wikiProvenanceClaim, 0, len(blocks))
	for _, block := range blocks {
		if block == nil || !wikiBlockNeedsSource(block) {
			continue
		}
		for claimOrder, text := range splitWikiProvenanceClaimTexts(block) {
			claims = append(claims, wikiProvenanceClaim{
				ID:        fmt.Sprintf("%s:%d", block.ID, claimOrder),
				BlockID:   block.ID,
				BlockType: block.BlockType,
				Text:      text,
				SortOrder: claimOrder,
			})
		}
	}
	return claims
}

// splitWikiProvenanceClaimTexts creates deterministic, clause-sized coverage
// units. It deliberately does not ask the LLM to enumerate claims: otherwise
// an omitted claim would be indistinguishable from a fully covered block.
// Sentence punctuation, commas, semicolons, line boundaries and table cells
// are conservative boundaries. Over-splitting can reject a generation, but it
// cannot silently publish a multi-fact block with only one source attached.
func splitWikiProvenanceClaimTexts(block *types.WikiPageBlock) []string {
	if block == nil {
		return nil
	}
	if block.BlockType == types.WikiBlockTypeTableRow {
		var claims []string
		for _, cell := range splitWikiProvenanceTableCells(block.Content) {
			claims = append(claims, splitWikiProvenanceClauses(cell)...)
		}
		return claims
	}

	content := strings.ReplaceAll(block.Content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	claims := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Fence markers are layout syntax. The code lines between them remain
		// mandatory claims, one line at a time.
		if _, _, fence := wikiFenceMarker(trimmed); fence {
			continue
		}
		if block.BlockType == types.WikiBlockTypeQuote {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		}
		if block.BlockType == types.WikiBlockTypeListItem {
			trimmed = stripWikiProvenanceListMarker(trimmed)
		}
		claims = append(claims, splitWikiProvenanceClauses(trimmed)...)
	}
	return claims
}

func splitWikiProvenanceTableCells(content string) []string {
	line := strings.TrimSpace(content)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")

	var cells []string
	var cell strings.Builder
	escaped := false
	for _, r := range line {
		if escaped {
			cell.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			cell.WriteRune(r)
			escaped = true
			continue
		}
		if r == '|' {
			if value := strings.TrimSpace(cell.String()); hasWikiProvenanceClaimText(value) {
				cells = append(cells, value)
			}
			cell.Reset()
			continue
		}
		cell.WriteRune(r)
	}
	if value := strings.TrimSpace(cell.String()); hasWikiProvenanceClaimText(value) {
		cells = append(cells, value)
	}
	return cells
}

func stripWikiProvenanceListMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) >= 2 && (trimmed[0] == '-' || trimmed[0] == '+' || trimmed[0] == '*') &&
		unicode.IsSpace(rune(trimmed[1])) {
		return strings.TrimSpace(trimmed[2:])
	}
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')') &&
		unicode.IsSpace(rune(trimmed[i+1])) {
		return strings.TrimSpace(trimmed[i+2:])
	}
	return strings.TrimSpace(trimmed)
}

func splitWikiProvenanceClauses(value string) []string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return nil
	}
	claims := make([]string, 0, 2)
	start := 0
	flush := func(end int) {
		if end <= start {
			return
		}
		candidate := strings.TrimSpace(string(runes[start:end]))
		if hasWikiProvenanceClaimText(candidate) {
			claims = append(claims, splitWikiProvenanceConnectors(candidate)...)
		}
	}
	for i, r := range runes {
		if !isWikiProvenanceClaimBoundary(runes, i, r) {
			continue
		}
		flush(i + 1)
		start = i + 1
	}
	flush(len(runes))
	return claims
}

func isWikiProvenanceClaimBoundary(runes []rune, index int, value rune) bool {
	switch value {
	case ',', '，', '、', ';', '；', '。', '!', '！', '?', '？':
		return true
	case '.':
		// Do not split decimals or dotted identifiers such as v1.2. A normal
		// sentence-ending period is followed by whitespace or the end.
		if index+1 < len(runes) && !unicode.IsSpace(runes[index+1]) {
			return false
		}
		if index > 0 && index+1 < len(runes) && unicode.IsDigit(runes[index-1]) && unicode.IsDigit(runes[index+1]) {
			return false
		}
		return true
	default:
		return false
	}
}

// splitWikiProvenanceConnectors covers the common no-punctuation form
// "fact A and fact B". It is intentionally conservative and deterministic.
// A connector starts the following claim so the full parent context can still
// disambiguate fragments such as "and launched in 2021".
func splitWikiProvenanceConnectors(value string) []string {
	parts := []string{strings.TrimSpace(value)}
	connectors := []string{
		" as well as ", " whereas ", " while ", " and ", " but ",
		"并且", "而且", "以及", "同时", "但是",
	}
	for _, connector := range connectors {
		var next []string
		for _, part := range parts {
			remaining := part
			for {
				search := remaining
				if connector[0] == ' ' {
					search = strings.ToLower(remaining)
				}
				index := strings.Index(search, connector)
				if index == 0 {
					if later := strings.Index(search[len(connector):], connector); later >= 0 {
						index = len(connector) + later
					}
				}
				if index <= 0 {
					break
				}
				left := strings.TrimSpace(remaining[:index])
				right := strings.TrimSpace(remaining[index:])
				if !hasWikiProvenanceClaimText(left) || !hasWikiProvenanceClaimText(right) {
					break
				}
				next = append(next, left)
				remaining = right
			}
			if hasWikiProvenanceClaimText(remaining) {
				next = append(next, strings.TrimSpace(remaining))
			}
		}
		parts = next
	}
	return parts
}

func hasWikiProvenanceClaimText(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func renderWikiProvenanceBlocksXML(
	blocks []*types.WikiPageBlock,
	claims []wikiProvenanceClaim,
	blockHandles *modelcontext.HandleTable,
	claimHandles *modelcontext.HandleTable,
) string {
	claimsByBlock := make(map[string][]wikiProvenanceClaim, len(blocks))
	for _, claim := range claims {
		claimsByBlock[claim.BlockID] = append(claimsByBlock[claim.BlockID], claim)
	}
	var out strings.Builder
	for _, block := range blocks {
		if block == nil {
			continue
		}
		handle, ok := blockHandles.Handle(block.ID)
		if !ok {
			continue
		}
		fmt.Fprintf(&out, "<block id=%q type=%q section=%q>\n<context>%s</context>\n",
			handle,
			html.EscapeString(block.BlockType),
			html.EscapeString(strings.Join(block.SectionPath, " / ")),
			html.EscapeString(strings.TrimSpace(block.Content)),
		)
		for _, claim := range claimsByBlock[block.ID] {
			claimHandle, known := claimHandles.Handle(claim.ID)
			if !known {
				continue
			}
			fmt.Fprintf(&out, "<claim id=%q>%s</claim>\n",
				claimHandle, html.EscapeString(claim.Text))
		}
		out.WriteString("</block>\n")
	}
	return out.String()
}

func renderWikiProvenanceChunksXML(batch chunkBatch) string {
	var out strings.Builder
	for _, chunk := range batch.chunks {
		if chunk == nil {
			continue
		}
		handle, ok := batch.handles.Handle(chunk.ID)
		if !ok {
			continue
		}
		fmt.Fprintf(&out, "<c id=%q index=%q>\n%s\n</c>\n",
			handle,
			fmt.Sprintf("%d", chunk.ChunkIndex),
			html.EscapeString(chunk.Content),
		)
	}
	return out.String()
}

func validateWikiBlockCitationBatch(
	parsed wikiBlockCitationResult,
	batch chunkBatch,
	claimByHandle map[string]wikiProvenanceClaim,
	chunkByID map[string]*types.Chunk,
	sourceContexts map[string]wikiProvenanceSourceContext,
) (wikiValidatedCitationBatch, error) {
	out := wikiValidatedCitationBatch{
		sourcesByBlock: make(map[string][]*types.WikiBlockSource),
		coveredClaims:  make(map[string]struct{}),
	}
	for claimHandle, citations := range parsed.Citations {
		claim, known := claimByHandle[strings.TrimSpace(claimHandle)]
		if !known {
			return wikiValidatedCitationBatch{}, fmt.Errorf("unknown claim handle %q", claimHandle)
		}
		if len(citations) == 0 {
			return wikiValidatedCitationBatch{}, fmt.Errorf("claim handle %q has an empty citation list", claimHandle)
		}
		for _, citation := range citations {
			chunkID, known := batch.handles.Resolve(citation.Chunk)
			if !known {
				return wikiValidatedCitationBatch{}, fmt.Errorf(
					"unknown chunk handle %q for claim %q", citation.Chunk, claimHandle,
				)
			}
			chunk := chunkByID[chunkID]
			if chunk == nil {
				return wikiValidatedCitationBatch{}, fmt.Errorf("resolved chunk %q is outside the validated input", chunkID)
			}
			evidence, err := validateWikiEvidenceQuote(citation.Evidence, chunk.Content)
			if err != nil {
				return wikiValidatedCitationBatch{}, fmt.Errorf(
					"claim %q chunk %q: %w", claimHandle, citation.Chunk, err,
				)
			}
			context := sourceContexts[chunk.KnowledgeID]
			evidenceHash := hashWikiProvenanceText(normalizeWikiEvidence(evidence))
			out.sourcesByBlock[claim.BlockID] = append(out.sourcesByBlock[claim.BlockID], &types.WikiBlockSource{
				ID:               uuid.NewString(),
				TenantID:         chunk.TenantID,
				KnowledgeBaseID:  chunk.KnowledgeBaseID,
				BlockID:          claim.BlockID,
				KnowledgeID:      chunk.KnowledgeID,
				DocumentTitle:    context.SourceTitle,
				KnowledgeAttempt: context.KnowledgeAttempt,
				ChunkID:          chunk.ID,
				ChunkRevision:    chunk.ContentRevision,
				Evidence:         evidence,
				EvidenceHash:     evidenceHash,
				ChunkContentHash: hashWikiProvenanceText(chunk.Content),
				ValidationStatus: types.WikiSourceValidationLocated,
			})
		}
		out.coveredClaims[claim.ID] = struct{}{}
	}
	return out, nil
}

func validateWikiEvidenceQuote(evidence, chunkContent string) (string, error) {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return "", fmt.Errorf("empty evidence")
	}
	chunkNormal := normalizeWikiEvidence(chunkContent)
	if chunkNormal == "" {
		return "", fmt.Errorf("source chunk is empty")
	}

	// The prompt XML-escapes untrusted source text. Models usually return the
	// decoded quote, but some copy the entity form verbatim; accept either only
	// when it normalises to a contiguous substring of the original chunk.
	candidates := []string{evidence}
	if decoded := html.UnescapeString(evidence); decoded != evidence {
		candidates = append(candidates, decoded)
	}
	for _, candidate := range candidates {
		if normalized := normalizeWikiEvidence(candidate); normalized != "" && strings.Contains(chunkNormal, normalized) {
			return strings.TrimSpace(candidate), nil
		}
	}
	return "", fmt.Errorf("evidence is not a contiguous quote from the source chunk")
}
