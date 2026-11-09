package repository

import (
	"context"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWikiSQLiteSimilarityLiteralAndScope(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	for _, x := range []struct{ id, kb, title, status string }{{"a", "kb", "CL-200", "published"}, {"b", "kb", "CL-200系列", "published"}, {"c", "other", "CL-200", "published"}, {"d", "kb", "CL-200", "archived"}, {"e", "kb", "100%", "published"}, {"f", "kb", "100ABC", "published"}} {
		require.NoError(t, db.Exec("INSERT INTO wiki_pages(id,tenant_id,knowledge_base_id,slug,title,page_type,status) VALUES(?,1,?,?,?,?,?)", x.id, x.kb, x.id, x.title, types.WikiPageTypeEntity, x.status).Error)
	}
	r := NewWikiPageRepository(db)
	rows, err := r.FindSimilarPages(context.Background(), "kb", "CL-200", nil, 5)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "CL-200", rows[0].Title)
	rows, err = r.FindSimilarPages(context.Background(), "kb", "100%", nil, 5)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "100%", rows[0].Title)
}
