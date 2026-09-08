package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

var _ interfaces.WikiPageRenamer = (*wikiPageService)(nil)

func (s *wikiPageService) RenamePage(
	ctx context.Context, req interfaces.WikiPageRenameRequest,
) (*interfaces.WikiPageRenameResult, error) {
	renamer, ok := s.repo.(interfaces.WikiPageRenamer)
	if !ok {
		return nil, interfaces.ErrWikiRenameUnsupported
	}
	result, err := renamer.RenamePage(ctx, req)
	if err != nil {
		return nil, err
	}
	// Retrieval is optional and not part of the Wiki transaction. A sync
	// failure must never turn a committed rename into a retryable rename error.
	if s.chunkRepo != nil {
		for _, page := range result.AffectedPages {
			if err := s.syncRenamedWikiChunk(ctx, page); err != nil {
				logger.Warnf(ctx, "wiki rename committed; chunk sync for %s failed: %v", page.ID, err)
				result.SyncWarnings = append(result.SyncWarnings,
					fmt.Sprintf("Rename committed; retrieval sync for [[%s]] needs attention: %v", page.Slug, err))
			}
		}
	}
	return result, nil
}

// syncRenamedWikiChunk refreshes only an existing, correctly scoped legacy Wiki
// projection. Never create a new retrieval document or touch source chunks.
func (s *wikiPageService) syncRenamedWikiChunk(ctx context.Context, renamed *types.WikiPage) error {
	page, err := s.repo.GetByID(ctx, renamed.ID)
	if err != nil {
		return err
	}
	if page.KnowledgeBaseID != renamed.KnowledgeBaseID || page.TenantID != renamed.TenantID {
		return errors.New("wiki page scope changed before retrieval sync")
	}
	chunkID := "wp-" + page.ID
	chunk, err := s.chunkRepo.GetChunkByID(ctx, page.TenantID, chunkID)
	if errors.Is(err, repository.ErrChunkNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if chunk == nil {
		return nil
	}
	if chunk.ID != chunkID || chunk.TenantID != page.TenantID ||
		chunk.KnowledgeBaseID != page.KnowledgeBaseID || chunk.ChunkType != types.ChunkTypeWikiPage {
		return errors.New("retrieval chunk does not belong to this wiki page")
	}
	updater, ok := s.chunkRepo.(interfaces.WikiPageRenameChunkUpdater)
	if !ok {
		return errors.New("conditional wiki chunk sync is not supported")
	}
	expectedUpdatedAt := chunk.UpdatedAt
	metadata := make(map[string]json.RawMessage)
	if len(chunk.Metadata) > 0 {
		if err := json.Unmarshal(chunk.Metadata, &metadata); err != nil {
			return fmt.Errorf("decode wiki chunk metadata: %w", err)
		}
	}
	if metadata == nil {
		metadata = make(map[string]json.RawMessage)
	}
	metadata["wiki_slug"], _ = json.Marshal(page.Slug)
	metadata["wiki_page_id"], _ = json.Marshal(page.ID)
	if _, exists := metadata["slug"]; exists {
		metadata["slug"], _ = json.Marshal(page.Slug)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	body := page.Content
	// Preserve the title-prefixed form used by some legacy projections.
	if prefix := "# " + page.Title + "\n\n"; strings.HasPrefix(chunk.Content, prefix) {
		body = prefix + body
	}
	bodyChanged := chunk.Content != body
	chunk.Content = body
	chunk.Metadata = encoded
	chunk.UpdatedAt = time.Now()
	if bodyChanged {
		hash := sha256.Sum256([]byte(body))
		chunk.ContentHash = hex.EncodeToString(hash[:])
		// This service has no vector-index dependency. Do not claim that the
		// old embedding reflects rewritten content or enable a disabled chunk.
		chunk.IndexStatus = "failed"
	}
	if err := updater.UpdateRenamedWikiChunk(ctx, page, chunk, expectedUpdatedAt); err != nil {
		return err
	}
	if bodyChanged {
		return errors.New("chunk content refreshed; external reindex is not configured")
	}
	return nil
}
