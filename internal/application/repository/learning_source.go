package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Only explicit page citations supply evidence. Missing chunk citations are
// insufficient evidence, never permission to search an unrelated document.
func learningSource(tx *gorm.DB, tenant uint64, pageID string) (*types.LearningSource, error) {
	p, kb, err := learningPage(tx, tenant, pageID)
	if err != nil {
		return nil, err
	}
	return learningSourceForPage(tx, p, kb)
}

func learningSourceForPage(tx *gorm.DB, p *types.WikiPage, kb *types.KnowledgeBase) (*types.LearningSource, error) {
	if len(p.ChunkRefs) == 0 || len(p.ChunkRefs) > 256 || len(p.SourceRefs) == 0 || len(p.SourceRefs) > 64 {
		return nil, types.ErrLearningEvidence
	}
	docIDs := p.SourceKnowledgeIDs()
	sort.Strings(docIDs)
	var docs []*types.Knowledge
	err := learningShare(tx).Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ? AND enable_status = ? AND parse_status = ?",
		p.TenantID, p.KnowledgeBaseID, docIDs, "enabled", types.ParseStatusCompleted).Order("id").Find(&docs).Error
	if err != nil {
		return nil, err
	}
	if len(docs) != len(docIDs) {
		return nil, types.ErrLearningEvidence
	}
	refs := append([]string(nil), p.ChunkRefs...)
	sort.Strings(refs)
	selected := refs[:min(len(refs), types.LearningMaxChunks)]
	var chunks []*types.Chunk
	err = learningShare(tx).Where("tenant_id = ? AND knowledge_base_id = ? AND knowledge_id IN ? AND id IN ? AND is_enabled = ? AND index_status = ? AND chunk_type IN ?",
		p.TenantID, p.KnowledgeBaseID, docIDs, selected, true, "ready", []string{types.ChunkTypeText, types.ChunkTypeParentText, types.ChunkTypeImageOCR}).Order("id").Find(&chunks).Error
	if err != nil {
		return nil, err
	}
	if len(chunks) != len(selected) {
		return nil, types.ErrLearningEvidence
	}
	for _, c := range chunks {
		if strings.TrimSpace(c.Content) == "" || len(c.Content) > 1<<20 {
			return nil, types.ErrLearningEvidence
		}
	}
	// Hash actual content, not the cached content_hash or timestamps. Link
	// bookkeeping does not invalidate mastery, but every quiz input does.
	stamps := make([]learningChunkStamp, 0, len(chunks))
	for _, c := range chunks {
		stamps = append(stamps, learningChunkHash(c))
	}
	return &types.LearningSource{Page: p, Chunks: chunks, Stamp: learningSourceStamp(p, kb, stamps), ModelID: learningModelID(kb)}, nil
}

type learningChunkStamp struct {
	ID, KnowledgeID, ContentHash string
	Revision                     int
}

func learningChunkHash(c *types.Chunk) learningChunkStamp {
	h := sha256.Sum256([]byte(c.Content))
	return learningChunkStamp{c.ID, c.KnowledgeID, hex.EncodeToString(h[:]), c.ContentRevision}
}

func learningModelID(kb *types.KnowledgeBase) string {
	if kb.WikiConfig != nil && kb.WikiConfig.SynthesisModelID != "" {
		return kb.WikiConfig.SynthesisModelID
	}
	return kb.SummaryModelID
}

func learningSourceStamp(p *types.WikiPage, kb *types.KnowledgeBase, chunks []learningChunkStamp) string {
	refs := append([]string(nil), p.ChunkRefs...)
	sort.Strings(refs)
	stamp := struct {
		Tenant                                        uint64
		ModelID, PromptVersion                        string
		PageID, KB, Title, Summary, Content, PageType string
		Version                                       int
		Aliases, Sources, ChunkRefs                   []string
		Chunks                                        []learningChunkStamp
	}{Tenant: p.TenantID, ModelID: learningModelID(kb), PromptVersion: types.LearningPromptVersion,
		PageID: p.ID, KB: p.KnowledgeBaseID, Title: p.Title, Summary: p.Summary, Content: p.Content,
		PageType: p.PageType, Version: p.Version, Aliases: p.Aliases,
		Sources: append([]string(nil), p.SourceRefs...), ChunkRefs: refs, Chunks: chunks}
	sort.Strings(stamp.Sources)
	b, _ := json.Marshal(stamp)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func learningNode(tx *gorm.DB, scope interfaces.LearningScope, p *types.WikiPage, kb *types.KnowledgeBase) (*types.LearningNode, error) {
	m, err := learningMastery(tx, scope, p)
	if err != nil {
		return nil, err
	}
	n := &types.LearningNode{Page: p, Mastery: m}
	if m.Attempts > 0 {
		source, err := learningSourceForPage(tx, p, kb)
		if err == nil {
			n.SourceStamp = source.Stamp
		} else if !errors.Is(err, types.ErrLearningEvidence) {
			return nil, err
		}
	}
	return n, nil
}
