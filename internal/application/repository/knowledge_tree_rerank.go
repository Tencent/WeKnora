package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var _ interfaces.KnowledgeRerankRepository = (*knowledgeRepository)(nil)

// KnowledgeRerankPassages ascends one direct parent, then expands descendants.
// Unlike detail reads, reaching a retrieval budget truncates rather than failing
// the query. Only enabled, completed knowledge with ready passages is eligible.
func (r *knowledgeRepository) KnowledgeRerankPassages(
	ctx context.Context, roots []types.KnowledgeTreeRoot, opts types.KnowledgeChildrenOptions,
) ([]types.KnowledgeTreePassage, *types.KnowledgeTreeDiagnostics, error) {
	diag := &types.KnowledgeTreeDiagnostics{}
	if opts.Depth == 0 || len(roots) == 0 {
		return nil, diag, nil
	}
	if _, err := types.ParseKnowledgeChildrenOptions(fmt.Sprint(opts.Depth), fmt.Sprint(opts.Limit)); err != nil {
		return nil, nil, apperrors.NewBadRequestError(err.Error())
	}
	var result []types.KnowledgeTreePassage
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		base := map[string]bool{}
		groups := map[types.KnowledgeTreeRoot][]string{}
		var scopes []types.KnowledgeTreeRoot
		for _, root := range roots {
			base[root.KnowledgeID] = true
			scope := types.KnowledgeTreeRoot{TenantID: root.TenantID, KBID: root.KBID}
			if _, ok := groups[scope]; !ok {
				scopes = append(scopes, scope)
			}
			groups[scope] = append(groups[scope], root.KnowledgeID)
		}
		var selected []types.KnowledgeTreePassage
		for _, scope := range scopes {
			if len(selected) >= opts.Limit {
				diag.Truncated = true
				break
			}
			rows, err := collectRerankTree(tx, scope, groups[scope], base, opts, diag)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if len(selected) == opts.Limit {
					diag.Truncated = true
					break
				}
				selected = append(selected, row)
			}
		}
		if len(selected) == 0 {
			return nil
		}
		ids := make([]string, 0, len(selected))
		byID := map[string]types.KnowledgeTreePassage{}
		order := map[string]int{}
		for i, row := range selected {
			ids = append(ids, row.Knowledge.ID)
			byID[row.Knowledge.ID], order[row.Knowledge.ID] = row, i
		}
		// Round-robin passages by position within each knowledge: one long
		// parent must not consume the budget before its siblings are scored.
		var chunks []struct {
			types.Chunk `gorm:"embedded"`
			TreeRank    int `gorm:"column:tree_rank"`
		}
		q := tx.Model(&types.Chunk{}).Select("chunks.*, ROW_NUMBER() OVER "+
			"(PARTITION BY chunks.knowledge_id ORDER BY chunks.chunk_index, chunks.id) AS tree_rank").
			Joins("JOIN knowledges tree_k ON tree_k.id = chunks.knowledge_id "+
				"AND tree_k.tenant_id = chunks.tenant_id AND tree_k.knowledge_base_id = chunks.knowledge_base_id").
			Where("chunks.knowledge_id IN ? AND chunks.is_enabled = ? AND chunks.index_status = ? "+
				"AND chunks.chunk_type IN ?", ids, true, "ready", rerankTreeChunkTypes())
		if err := tx.Table("(?) AS tree_passages", q).Order("tree_rank, knowledge_id, id").
			Limit(types.MaxKnowledgeTreePassages + 1).Scan(&chunks).Error; err != nil {
			return err
		}
		if len(chunks) > types.MaxKnowledgeTreePassages {
			diag.Truncated = true
			chunks = chunks[:types.MaxKnowledgeTreePassages]
		}
		// Restore anchor order inside each round, independent of UUID ordering.
		sort.SliceStable(chunks, func(i, j int) bool {
			a, b := chunks[i], chunks[j]
			if a.TreeRank != b.TreeRank {
				return a.TreeRank < b.TreeRank
			}
			if order[a.KnowledgeID] != order[b.KnowledgeID] {
				return order[a.KnowledgeID] < order[b.KnowledgeID]
			}
			return a.ID < b.ID
		})
		added := map[string]bool{}
		for i := range chunks {
			chunk := &chunks[i].Chunk
			row := byID[chunk.KnowledgeID]
			if row.Knowledge == nil || chunk.TenantID != row.Knowledge.TenantID ||
				chunk.KnowledgeBaseID != row.Knowledge.KnowledgeBaseID {
				continue
			}
			row.Chunk = chunk
			result = append(result, row)
			added[chunk.KnowledgeID] = true
		}
		diag.AddedKnowledge, diag.AddedPassages = len(added), len(result)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, diag, err
}

func rerankTreeRows(tx *gorm.DB, scope types.KnowledgeTreeRoot) *gorm.DB {
	return tx.Model(&types.Knowledge{}).Where(
		"tenant_id = ? AND knowledge_base_id = ? AND parse_status = ? AND enable_status = ? AND NOT deletion_pending",
		scope.TenantID, scope.KBID, types.ParseStatusCompleted, "enabled",
	).Where("EXISTS (SELECT 1 FROM chunks c WHERE c.knowledge_id = knowledges.id "+
		"AND c.tenant_id = knowledges.tenant_id AND c.knowledge_base_id = knowledges.knowledge_base_id "+
		"AND c.deleted_at IS NULL AND c.is_enabled = ? AND c.index_status = ? AND c.chunk_type IN ?)",
		true, "ready", rerankTreeChunkTypes())
}

func rerankTreeChunkTypes() []types.ChunkType {
	return []types.ChunkType{
		types.ChunkTypeText, types.ChunkTypeSummary, types.ChunkTypeTableColumn, types.ChunkTypeTableSummary,
		types.ChunkTypeFAQ, types.ChunkTypeImageOCR, types.ChunkTypeImageCaption, types.ChunkTypeImageVector,
	}
}

func collectRerankTree(
	tx *gorm.DB, scope types.KnowledgeTreeRoot, hits []string, base map[string]bool,
	opts types.KnowledgeChildrenOptions, diag *types.KnowledgeTreeDiagnostics,
) ([]types.KnowledgeTreePassage, error) {
	var seeds []*types.Knowledge
	if err := rerankTreeRows(tx, scope).Where("id IN ?", hits).Find(&seeds).Error; err != nil {
		return nil, err
	}
	seedByID := map[string]*types.Knowledge{}
	var parents []string
	for _, seed := range seeds {
		seedByID[seed.ID] = seed
		if seed.ParentKnowledgeID != "" {
			parents = append(parents, seed.ParentKnowledgeID)
		}
	}
	var parentRows []*types.Knowledge
	if len(parents) > 0 {
		if err := rerankTreeRows(tx, scope).Where("id IN ?", parents).Find(&parentRows).Error; err != nil {
			return nil, err
		}
	}
	parentByID := map[string]*types.Knowledge{}
	for _, parent := range parentRows {
		if access.RejectMovingKnowledge(parent) == nil {
			parentByID[parent.ID] = parent
		}
	}
	var result []types.KnowledgeTreePassage
	frontier := map[string]string{}
	seen := map[string]bool{}
	add := func(k *types.Knowledge, anchor string, depth int) {
		if !base[k.ID] {
			result = append(result, types.KnowledgeTreePassage{
				Knowledge: k, AnchorKnowledgeID: anchor, Depth: depth,
			})
		}
	}
	for _, id := range hits {
		seed := seedByID[id]
		if seed == nil || access.RejectMovingKnowledge(seed) != nil {
			continue
		}
		anchor := seed
		if parent := parentByID[seed.ParentKnowledgeID]; parent != nil {
			anchor = parent
		}
		if !seen[anchor.ID] {
			seen[anchor.ID] = true
			frontier[anchor.ID] = anchor.ID
			add(anchor, anchor.ID, 0)
		}
	}
	maxDepth := opts.Depth
	if maxDepth == -1 {
		maxDepth = types.MaxKnowledgeDepth
	}
	for depth := 1; len(frontier) > 0 && depth <= maxDepth; depth++ {
		if len(result) >= opts.Limit {
			diag.Truncated = true
			break
		}
		ids := make([]string, 0, len(frontier))
		for id := range frontier {
			ids = append(ids, id)
		}
		var children []*types.Knowledge
		if err := rerankTreeRows(tx, scope).Where("parent_knowledge_id IN ?", ids).
			Order("created_at, id").Limit(types.MaxKnowledgeChildren + len(base) + 1).
			Find(&children).Error; err != nil {
			return nil, err
		}
		next := map[string]string{}
		for _, child := range children {
			if seen[child.ID] || access.RejectMovingKnowledge(child) != nil {
				continue
			}
			seen[child.ID] = true
			anchor := frontier[child.ParentKnowledgeID]
			next[child.ID] = anchor
			add(child, anchor, depth)
			if len(result) > opts.Limit {
				diag.Truncated = true
				return result[:opts.Limit], nil
			}
		}
		frontier = next
		if depth == maxDepth && opts.Depth == -1 && len(next) > 0 {
			diag.Truncated = true
		}
	}
	return result, nil
}
