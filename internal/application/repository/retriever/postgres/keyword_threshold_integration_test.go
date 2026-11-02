package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// WEKNORA_KEYWORD_TEST_DSN must point to a disposable local ParadeDB database.
// The test creates its own schema and never reads existing application data.
func TestKeywordsRetrieveThresholdParadeDB(t *testing.T) {
	dsn := os.Getenv("WEKNORA_KEYWORD_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_KEYWORD_TEST_DSN to run against real pg_search")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, []string{"localhost", "127.0.0.1", "::1"}, u.Hostname(), "local test database only")
	admin, err := gorm.Open(pgdriver.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminSQL.Close() })
	require.NoError(t, admin.Exec("CREATE EXTENSION IF NOT EXISTS pg_search").Error)
	schema := fmt.Sprintf("keyword_threshold_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	db, err := gorm.Open(pgdriver.Open(u.String()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(`CREATE TABLE embeddings (
		id SERIAL PRIMARY KEY, content TEXT, source_id TEXT, source_type INTEGER,
		chunk_id TEXT, knowledge_id TEXT, knowledge_base_id TEXT, tag_id TEXT, is_enabled BOOLEAN
	)`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX keyword_threshold_idx ON embeddings USING bm25
		(id, content, knowledge_base_id, knowledge_id, tag_id)
		WITH (key_field='id', text_fields='{"content":{"tokenizer":{"type":"chinese_lindera"}}}')`).Error)
	for i, text := range []string{
		"少女 樱花 少女 少女",
		"少女 春天 花园",
		"少女 背景 描述 天空 山川 湖泊 公园 城市 音乐 交通 建筑",
	} {
		require.NoError(t, db.Exec(`INSERT INTO embeddings(content,source_id,source_type,chunk_id,knowledge_id,knowledge_base_id,is_enabled)
			VALUES (?, ?, 0, ?, 'doc', 'kb', true)`, text, fmt.Sprint(i), fmt.Sprint(i)).Error)
	}
	require.NoError(t, db.Exec(`INSERT INTO embeddings(content,source_id,source_type,chunk_id,knowledge_id,knowledge_base_id,is_enabled)
		SELECT '数据库 索引 网络 存储', n::text, 0, n::text, 'other', 'kb', true FROM generate_series(10,100) n`).Error)
	// A disabled row and a better match outside the requested KB must not
	// influence either the candidate set or its normalization denominator.
	require.NoError(t, db.Exec(`INSERT INTO embeddings(content,source_id,source_type,chunk_id,knowledge_id,knowledge_base_id,is_enabled)
		VALUES ('少女','disabled',0,'disabled','doc','kb',false), ('少女','foreign',0,'foreign','doc','foreign',true)`).Error)
	repo := &pgRepository{db: db}
	params := types.RetrieveParams{Query: "少女", KnowledgeBaseIDs: []string{"kb"}, TopK: 10}
	baseline, err := repo.KeywordsRetrieve(context.Background(), params)
	require.NoError(t, err)
	require.Len(t, baseline[0].Results, 3)
	scale := baseline[0].Results[0].Score
	require.Greater(t, scale, 1.0)
	thresholds := []float64{0, 0.5, 0.8, 1, 100}
	for _, hit := range baseline[0].Results {
		thresholds = append(thresholds, hit.Score/scale)
	}
	for _, threshold := range thresholds {
		params.Threshold = threshold
		got, err := repo.KeywordsRetrieve(context.Background(), params)
		require.NoError(t, err)
		want := make([]string, 0)
		for _, hit := range baseline[0].Results {
			if hit.Score/scale >= threshold {
				want = append(want, hit.ChunkID)
			}
		}
		actual := make([]string, 0)
		for _, hit := range got[0].Results {
			actual = append(actual, hit.ChunkID)
		}
		require.Equal(t, want, actual, "threshold=%v", threshold)
	}
}
