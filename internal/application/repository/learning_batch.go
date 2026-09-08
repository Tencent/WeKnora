package repository

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// Bulk display paths do not acquire content row locks. Their state describes
// the read snapshot; grading and publication independently recheck live input
// under locks. Hash chunks in batches to bound resident source content.
func learningNodes(tx *gorm.DB, scope interfaces.LearningScope, pages []*types.WikiPage, kb *types.KnowledgeBase) ([]*types.LearningNode, error) {
	result := make([]*types.LearningNode, 0, len(pages))
	if len(pages) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(pages))
	for _, p := range pages {
		ids = append(ids, p.ID)
	}
	var masters []*types.LearningMastery
	if err := learningScope(tx, scope).Where("page_id IN ?", ids).Find(&masters).Error; err != nil {
		return nil, err
	}
	byPage := map[string]*types.LearningMastery{}
	for _, m := range masters {
		byPage[m.PageID] = m
	}
	docSet, chunkSet := map[string]bool{}, map[string]bool{}
	for _, p := range pages {
		m := byPage[p.ID]
		if m == nil || m.Attempts == 0 || len(p.SourceRefs) > 64 || len(p.ChunkRefs) > 256 {
			continue
		}
		for _, id := range p.SourceKnowledgeIDs() {
			docSet[id] = true
		}
		refs := append([]string(nil), p.ChunkRefs...)
		sort.Strings(refs)
		for _, id := range refs[:min(len(refs), types.LearningMaxChunks)] {
			chunkSet[id] = true
		}
	}
	sortedKeys := func(set map[string]bool) []string {
		out := make([]string, 0, len(set))
		for id := range set {
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}
	docIDs, chunkIDs := sortedKeys(docSet), sortedKeys(chunkSet)
	validDocs := map[string]bool{}
	for start := 0; start < len(docIDs); start += 500 {
		var found []string
		if err := tx.Model(&types.Knowledge{}).Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ? AND enable_status = ? AND parse_status = ?",
			scope.TenantID, kb.ID, docIDs[start:min(start+500, len(docIDs))], "enabled", types.ParseStatusCompleted).Pluck("id", &found).Error; err != nil {
			return nil, err
		}
		for _, id := range found {
			validDocs[id] = true
		}
	}
	stamps := map[string]learningChunkStamp{}
	for start := 0; start < len(chunkIDs); start += 200 {
		var chunks []*types.Chunk
		if err := tx.Select("id", "knowledge_id", "content", "content_revision").Where("tenant_id = ? AND knowledge_base_id = ? AND id IN ? AND is_enabled = ? AND index_status = ? AND chunk_type IN ?",
			scope.TenantID, kb.ID, chunkIDs[start:min(start+200, len(chunkIDs))], true, "ready",
			[]string{types.ChunkTypeText, types.ChunkTypeParentText, types.ChunkTypeImageOCR}).Find(&chunks).Error; err != nil {
			return nil, err
		}
		for _, c := range chunks {
			if validDocs[c.KnowledgeID] && strings.TrimSpace(c.Content) != "" && len(c.Content) <= 1<<20 {
				stamps[c.ID] = learningChunkHash(c)
			}
		}
	}
	for _, p := range pages {
		n := &types.LearningNode{Page: p, Mastery: byPage[p.ID]}
		result = append(result, n)
		if n.Mastery == nil || n.Mastery.Attempts == 0 || len(p.ChunkRefs) == 0 || len(p.ChunkRefs) > 256 || len(p.SourceRefs) == 0 || len(p.SourceRefs) > 64 {
			continue
		}
		members := map[string]bool{}
		valid := true
		for _, id := range p.SourceKnowledgeIDs() {
			members[id] = true
			if !validDocs[id] {
				valid = false
			}
		}
		refs := append([]string(nil), p.ChunkRefs...)
		sort.Strings(refs)
		chunks := make([]learningChunkStamp, 0, types.LearningMaxChunks)
		for _, id := range refs[:min(len(refs), types.LearningMaxChunks)] {
			c, ok := stamps[id]
			if !ok || !members[c.KnowledgeID] {
				valid = false
				break
			}
			chunks = append(chunks, c)
		}
		if valid {
			n.SourceStamp = learningSourceStamp(p, kb, chunks)
		}
	}
	return result, nil
}
