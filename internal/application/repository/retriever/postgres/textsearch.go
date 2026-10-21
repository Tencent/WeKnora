package postgres

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// Preserve repeated tokens for BM25 term frequency, using the same Jieba dictionary for writes and queries.
func keywordText(content string) string {
	for _, r := range content {
		if unicode.Is(unicode.Han, r) {
			return strings.Join(types.Jieba.CutForSearch(content, true), " ")
		}
	}
	return content
}

// BackfillTextSearch fills only missing derived text; original content and vectors are retained.
func BackfillTextSearch(ctx context.Context, db *gorm.DB) error {
	var ready bool
	if err := db.WithContext(ctx).Raw(`SELECT
		EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_textsearch') AND
		EXISTS (SELECT 1 FROM pg_index WHERE indexrelid = to_regclass('embeddings_textsearch_idx') AND indisvalid)`).
		Scan(&ready).Error; err != nil {
		return fmt.Errorf("check pg_textsearch index: %w", err)
	}
	if !ready {
		return fmt.Errorf("pg_textsearch extension and a valid embeddings_textsearch_idx are required")
	}
	for {
		var rows []pgVector
		if err := db.WithContext(ctx).Select("id, content").
			Where("search_content IS NULL").Order("id").Limit(500).Find(&rows).Error; err != nil {
			return fmt.Errorf("read pg_textsearch backfill: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		values := make([]string, len(rows))
		args := make([]any, 0, len(rows)*2)
		for i, row := range rows {
			values[i] = "(?::bigint, ?)"
			args = append(args, row.ID, keywordText(row.Content))
		}
		query := "UPDATE embeddings AS e SET search_content = v.content FROM (VALUES " +
			strings.Join(values, ",") + ") AS v(id, content) WHERE e.id = v.id AND e.search_content IS NULL"
		if err := db.WithContext(ctx).Exec(query, args...).Error; err != nil {
			return fmt.Errorf("write pg_textsearch backfill: %w", err)
		}
		logger.Infof(ctx, "[Postgres] Backfilled pg_textsearch text for %d records", len(rows))
	}
}
