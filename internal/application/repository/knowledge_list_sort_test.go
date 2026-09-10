package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type knowledgeSortSeed struct {
	id        string
	fileName  string
	title     string
	createdAt time.Time
	updatedAt time.Time
}

func insertKnowledgeSortSeed(t *testing.T, db *gorm.DB, seed knowledgeSortSeed) {
	t.Helper()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, type, title, source, file_name, parse_status, created_at, updated_at)
		VALUES (?, 1, 'kb-sort', 'file', ?, 'manual', ?, 'completed', ?, ?)
	`, seed.id, seed.title, seed.fileName, seed.createdAt, seed.updatedAt).Error)
}

func knowledgeIDs(rows []*types.Knowledge) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestListPagedKnowledgeSortModes(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()

	jan := time.Date(2024, time.January, 1, 8, 0, 0, 0, time.UTC)
	feb := time.Date(2024, time.February, 1, 8, 0, 0, 0, time.UTC)
	mar := time.Date(2024, time.March, 1, 8, 0, 0, 0, time.UTC)
	insertKnowledgeSortSeed(t, db, knowledgeSortSeed{
		id: "doc-a", fileName: "Zulu.txt", title: "Zulu", createdAt: jan, updatedAt: mar,
	})
	insertKnowledgeSortSeed(t, db, knowledgeSortSeed{
		id: "doc-b", fileName: "alpha.txt", title: "Alpha", createdAt: feb, updatedAt: jan,
	})
	// file_name 为空时，名称排序应使用前端同样会展示的 title。
	insertKnowledgeSortSeed(t, db, knowledgeSortSeed{
		id: "doc-c", fileName: "", title: "Bravo note", createdAt: mar, updatedAt: feb,
	})

	tests := []struct {
		name   string
		filter types.KnowledgeListFilter
		want   []string
	}{
		{
			name: "更新时间从新到旧",
			filter: types.KnowledgeListFilter{
				SortBy: types.KnowledgeListSortByUpdatedAt, SortOrder: types.KnowledgeListSortDescending,
			},
			want: []string{"doc-a", "doc-c", "doc-b"},
		},
		{
			name: "更新时间从旧到新",
			filter: types.KnowledgeListFilter{
				SortBy: types.KnowledgeListSortByUpdatedAt, SortOrder: types.KnowledgeListSortAscending,
			},
			want: []string{"doc-b", "doc-c", "doc-a"},
		},
		{
			name: "创建时间从新到旧",
			filter: types.KnowledgeListFilter{
				SortBy: types.KnowledgeListSortByCreatedAt, SortOrder: types.KnowledgeListSortDescending,
			},
			want: []string{"doc-c", "doc-b", "doc-a"},
		},
		{
			name: "创建时间从旧到新",
			filter: types.KnowledgeListFilter{
				SortBy: types.KnowledgeListSortByCreatedAt, SortOrder: types.KnowledgeListSortAscending,
			},
			want: []string{"doc-a", "doc-b", "doc-c"},
		},
		{
			name: "文件名称从 A 到 Z 且忽略大小写",
			filter: types.KnowledgeListFilter{
				SortBy: types.KnowledgeListSortByFileName, SortOrder: types.KnowledgeListSortAscending,
			},
			want: []string{"doc-b", "doc-c", "doc-a"},
		},
		{
			name: "文件名称从 Z 到 A 且忽略大小写",
			filter: types.KnowledgeListFilter{
				SortBy: types.KnowledgeListSortByFileName, SortOrder: types.KnowledgeListSortDescending,
			},
			want: []string{"doc-a", "doc-c", "doc-b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
				ctx,
				1,
				"kb-sort",
				&types.Pagination{Page: 1, PageSize: 100},
				tt.filter,
			)
			require.NoError(t, err)
			assert.Equal(t, int64(3), total)
			assert.Equal(t, tt.want, knowledgeIDs(rows))
		})
	}
}

func TestListPagedKnowledgeSortUsesStableIDTieBreaker(t *testing.T) {
	db := setupKnowledgeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	sameTime := time.Date(2024, time.April, 1, 8, 0, 0, 0, time.UTC)

	insertKnowledgeSortSeed(t, db, knowledgeSortSeed{
		id: "doc-b", fileName: "same.txt", title: "Same", createdAt: sameTime, updatedAt: sameTime,
	})
	insertKnowledgeSortSeed(t, db, knowledgeSortSeed{
		id: "doc-a", fileName: "same.txt", title: "Same", createdAt: sameTime, updatedAt: sameTime,
	})

	rows, _, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
		context.Background(),
		1,
		"kb-sort",
		&types.Pagination{Page: 1, PageSize: 100},
		types.KnowledgeListFilter{
			SortBy: types.KnowledgeListSortByFileName, SortOrder: types.KnowledgeListSortAscending,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"doc-a", "doc-b"}, knowledgeIDs(rows))
}
