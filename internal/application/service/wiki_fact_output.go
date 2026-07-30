package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

const wikiFactMetadataKey = "fact_blocks_v1"

// wikiFactOutput is the only accepted shape for LLM-generated Wiki prose.
// Content and citations deliberately live in the same object so a later
// renderer cannot accidentally detach a sentence from its evidence.
type wikiFactOutput struct {
	SchemaVersion int             `json:"schema_version"`
	Summary       string          `json:"summary"`
	Blocks        []wikiFactBlock `json:"blocks"`
}

type wikiFactBlock struct {
	LogicalBlockID string              `json:"logical_block_id,omitempty"`
	Type           types.WikiBlockType `json:"type"`
	Content        string              `json:"content"`
	Citations      []wikiFactCitation  `json:"citations"`
}

type wikiFactCitation struct {
	ChunkID     string               `json:"chunk_id"`
	KnowledgeID string               `json:"knowledge_id,omitempty"`
	Role        types.WikiSourceRole `json:"role,omitempty"`
}

// wikiCitationEvidence is trusted backend data. The model supplies only a
// chunk ID and role; knowledge ownership is always filled from this map.
type wikiCitationEvidence struct {
	ChunkID     string
	KnowledgeID string
	Content     string
}

func parseWikiFactOutput(raw string, evidence map[string]wikiCitationEvidence) (*wikiFactOutput, error) {
	raw = cleanWikiFactJSON(raw)
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("wiki fact output is empty")
	}

	var output wikiFactOutput
	if err := json.Unmarshal([]byte(raw), &output); err != nil {
		return nil, fmt.Errorf("parse wiki fact output JSON: %w", err)
	}
	output.SchemaVersion = 1
	output.Summary = strings.TrimSpace(output.Summary)
	if output.Summary == "" {
		return nil, errors.New("wiki fact output requires summary")
	}
	if len(output.Blocks) == 0 {
		return nil, errors.New("wiki fact output requires at least one block")
	}
	if len(output.Blocks) > 300 {
		return nil, errors.New("wiki fact output exceeds 300 blocks")
	}

	logicalIDs := make(map[string]int, len(output.Blocks))
	for i := range output.Blocks {
		block := &output.Blocks[i]
		block.Content = strings.TrimSpace(block.Content)
		if block.Content == "" {
			return nil, fmt.Errorf("wiki fact block %d has empty content", i)
		}
		if !isLLMWikiFactBlockType(block.Type) {
			return nil, fmt.Errorf("wiki fact block %d has unsupported type %q", i, block.Type)
		}

		seenCitations := make(map[string]struct{}, len(block.Citations))
		normalizedCitations := make([]wikiFactCitation, 0, len(block.Citations))
		for _, citation := range block.Citations {
			citation.ChunkID = strings.TrimSpace(citation.ChunkID)
			trusted, ok := evidence[citation.ChunkID]
			if !ok || trusted.KnowledgeID == "" {
				return nil, fmt.Errorf("wiki fact block %d cites unknown chunk %q", i, citation.ChunkID)
			}
			citation.KnowledgeID = trusted.KnowledgeID
			if citation.Role == "" {
				citation.Role = types.WikiSourceSupporting
			}
			if !citation.Role.IsValid() {
				return nil, fmt.Errorf("wiki fact block %d has invalid citation role %q", i, citation.Role)
			}
			key := citation.ChunkID + "\x00" + string(citation.Role)
			if _, duplicate := seenCitations[key]; duplicate {
				continue
			}
			seenCitations[key] = struct{}{}
			normalizedCitations = append(normalizedCitations, citation)
		}
		block.Citations = normalizedCitations
		if block.Type != types.WikiBlockHeading && !hasSupportingCitation(block.Citations) {
			return nil, fmt.Errorf("wiki fact block %d requires at least one supporting citation", i)
		}

		baseID := deterministicWikiFactLogicalID(block.Type, block.Content)
		logicalIDs[baseID]++
		block.LogicalBlockID = baseID
		if logicalIDs[baseID] > 1 {
			block.LogicalBlockID = fmt.Sprintf("%s-%d", baseID, logicalIDs[baseID])
		}
	}
	return &output, nil
}

func cleanWikiFactJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		if newline := strings.IndexByte(raw, '\n'); newline >= 0 {
			raw = raw[newline+1:]
		}
		if end := strings.LastIndex(raw, "```"); end >= 0 {
			raw = raw[:end]
		}
	}
	return strings.TrimSpace(raw)
}

func isLLMWikiFactBlockType(blockType types.WikiBlockType) bool {
	switch blockType {
	case types.WikiBlockHeading,
		types.WikiBlockParagraph,
		types.WikiBlockFact,
		types.WikiBlockListItem,
		types.WikiBlockTableRow,
		types.WikiBlockQuote,
		types.WikiBlockCode:
		return true
	default:
		return false
	}
}

func hasSupportingCitation(citations []wikiFactCitation) bool {
	for _, citation := range citations {
		if citation.Role == types.WikiSourceSupporting {
			return true
		}
	}
	return false
}

func deterministicWikiFactLogicalID(blockType types.WikiBlockType, content string) string {
	digest := sha256.Sum256([]byte(string(blockType) + "\x00" + strings.TrimSpace(content)))
	return "fact-" + hex.EncodeToString(digest[:8])
}

// renderWikiFactOutput turns validated blocks into ordinary Markdown. Source
// badges are intentionally not embedded into Markdown: the source API/UI will
// render them from the structured ledger without exposing internal UUIDs.
func renderWikiFactOutput(output *wikiFactOutput) string {
	if output == nil {
		return ""
	}
	parts := make([]string, 0, len(output.Blocks))
	for _, block := range output.Blocks {
		content := strings.TrimSpace(block.Content)
		if content == "" {
			continue
		}
		switch block.Type {
		case types.WikiBlockHeading:
			if !strings.HasPrefix(content, "#") {
				content = "## " + content
			}
		case types.WikiBlockListItem:
			if !strings.HasPrefix(content, "- ") && !strings.HasPrefix(content, "* ") {
				content = "- " + content
			}
		case types.WikiBlockQuote:
			if !strings.HasPrefix(content, ">") {
				content = "> " + content
			}
		}
		parts = append(parts, content)
	}
	return strings.Join(parts, "\n\n")
}

func wikiFactChunkIDs(output *wikiFactOutput) types.StringArray {
	if output == nil {
		return types.StringArray{}
	}
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for _, block := range output.Blocks {
		for _, citation := range block.Citations {
			if citation.ChunkID == "" {
				continue
			}
			if _, duplicate := seen[citation.ChunkID]; duplicate {
				continue
			}
			seen[citation.ChunkID] = struct{}{}
			ids = append(ids, citation.ChunkID)
		}
	}
	return types.StringArray(ids)
}

func wikiFactKnowledgeIDs(output *wikiFactOutput) []string {
	if output == nil {
		return nil
	}
	seen := make(map[string]struct{})
	ids := make([]string, 0)
	for _, block := range output.Blocks {
		for _, citation := range block.Citations {
			if citation.KnowledgeID == "" {
				continue
			}
			if _, duplicate := seen[citation.KnowledgeID]; duplicate {
				continue
			}
			seen[citation.KnowledgeID] = struct{}{}
			ids = append(ids, citation.KnowledgeID)
		}
	}
	sort.Strings(ids)
	return ids
}

func sourceRefsForWikiFacts(current types.StringArray, newRefs map[string]string, output *wikiFactOutput) types.StringArray {
	byKnowledge := make(map[string]string)
	for _, ref := range current {
		knowledgeID := ref
		if separator := strings.Index(ref, "|"); separator > 0 {
			knowledgeID = ref[:separator]
		}
		if knowledgeID != "" {
			byKnowledge[knowledgeID] = ref
		}
	}
	for knowledgeID, ref := range newRefs {
		if knowledgeID != "" {
			if ref == "" {
				ref = knowledgeID
			}
			byKnowledge[knowledgeID] = ref
		}
	}
	ids := wikiFactKnowledgeIDs(output)
	refs := make(types.StringArray, 0, len(ids))
	for _, id := range ids {
		ref := byKnowledge[id]
		if ref == "" {
			ref = id
		}
		refs = append(refs, ref)
	}
	return refs
}

func setWikiFactMetadata(current types.JSON, output *wikiFactOutput) (types.JSON, error) {
	metadata := make(map[string]interface{})
	if len(current) > 0 && string(current) != "null" {
		if err := json.Unmarshal(current, &metadata); err != nil {
			return nil, fmt.Errorf("parse existing wiki page metadata: %w", err)
		}
	}
	metadata[wikiFactMetadataKey] = output
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode wiki fact metadata: %w", err)
	}
	return types.JSON(encoded), nil
}

func getWikiFactMetadata(metadata types.JSON) (*wikiFactOutput, bool) {
	if len(metadata) == 0 || string(metadata) == "null" {
		return nil, false
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return nil, false
	}
	raw, ok := envelope[wikiFactMetadataKey]
	if !ok {
		return nil, false
	}
	var output wikiFactOutput
	if err := json.Unmarshal(raw, &output); err != nil || output.SchemaVersion != 1 {
		return nil, false
	}
	return &output, true
}

func marshalWikiFactOutput(output *wikiFactOutput) string {
	if output == nil {
		return "(none)"
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		return "(none)"
	}
	return string(encoded)
}

func renderWikiEvidenceXML(evidence map[string]wikiCitationEvidence) string {
	if len(evidence) == 0 {
		return "(none)"
	}
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var builder strings.Builder
	for _, id := range ids {
		item := evidence[id]
		fmt.Fprintf(&builder, "<chunk id=%q>\n%s\n</chunk>\n", item.ChunkID, item.Content)
	}
	return builder.String()
}

// wikiEvidenceFromChunks creates a bounded, trusted citation allow-list. A
// partially included final chunk is still cited by its stable chunk ID; the
// model only sees the included prefix and therefore cannot claim unseen text.
func wikiEvidenceFromChunks(chunks []*types.Chunk, tenantID uint64, kbID string, maxRunes int) map[string]wikiCitationEvidence {
	evidence := make(map[string]wikiCitationEvidence)
	remaining := maxRunes
	for _, chunk := range chunks {
		if chunk == nil || chunk.ID == "" || chunk.KnowledgeID == "" || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if chunk.TenantID != tenantID || chunk.KnowledgeBaseID != kbID {
			continue
		}
		content := strings.TrimSpace(chunk.Content)
		if maxRunes > 0 {
			if remaining <= 0 {
				break
			}
			runes := []rune(content)
			if len(runes) > remaining {
				content = string(runes[:remaining])
			}
			remaining -= len([]rune(content))
		}
		evidence[chunk.ID] = wikiCitationEvidence{
			ChunkID:     chunk.ID,
			KnowledgeID: chunk.KnowledgeID,
			Content:     content,
		}
	}
	return evidence
}
