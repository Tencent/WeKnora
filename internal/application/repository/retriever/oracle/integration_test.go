package oracle

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// TestOracleIntegration runs the repository against a real Oracle AI Database.
// Skipped unless ORACLE_TEST_ADDR is set, e.g.
//
//	ORACLE_TEST_ADDR=host:1521 ORACLE_TEST_SERVICE=FREEPDB1 \
//	ORACLE_TEST_USER=weknora ORACLE_TEST_PASSWORD=... \
//	[ORACLE_TEST_WALLET_ZIP=/path/to/Wallet_db.zip] go test ./internal/application/repository/retriever/oracle/
func TestOracleIntegration(t *testing.T) {
	addr := os.Getenv("ORACLE_TEST_ADDR")
	if addr == "" {
		t.Skip("ORACLE_TEST_ADDR not set")
	}
	// Test-only DBs live on private networks; the SSRF dialer would block them.
	t.Setenv("SSRF_WHITELIST", strings.Split(addr, ":")[0])
	// ORACLE_TEST_WALLET_ZIP exercises the UI upload path (ZIP -> normalized
	// cwallet.sso -> materialized wallet dir), e.g. an ADB Wallet_*.zip.
	var wallet string
	if zipPath := os.Getenv("ORACLE_TEST_WALLET_ZIP"); zipPath != "" {
		raw, err := os.ReadFile(zipPath)
		require.NoError(t, err)
		wallet, err = utils.NormalizeOracleWallet(base64.StdEncoding.EncodeToString(raw))
		require.NoError(t, err)
	}
	db, err := utils.OpenOracleDB(utils.OracleConnParams{
		Addr: addr, Service: os.Getenv("ORACLE_TEST_SERVICE"),
		Username: os.Getenv("ORACLE_TEST_USER"), Password: os.Getenv("ORACLE_TEST_PASSWORD"),
		UseTLS: wallet != "", Wallet: wallet,
	}, true)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	base := "WKTEST_ORA"
	repo := NewOracleRetrieveEngineRepository(db, &types.IndexConfig{CollectionPrefix: base}).(*oracleRepository)
	table := tableName(base, 3)
	drop := func() {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table+" PURGE")
		_, _ = db.ExecContext(ctx,
			"BEGIN CTX_DDL.DROP_PREFERENCE('"+table+"_LEX'); EXCEPTION WHEN OTHERS THEN NULL; END;")
	}
	drop()
	defer drop()

	infos := []*types.IndexInfo{
		{
			SourceID: "c1", ChunkID: "c1", KnowledgeID: "k1", KnowledgeBaseID: "kb1", IsEnabled: true,
			Content: "東京の天気は晴れです。WeKnora deploy guide",
		},
		{
			SourceID: "c2", ChunkID: "c2", KnowledgeID: "k1", KnowledgeBaseID: "kb1", IsEnabled: true, TagID: "tagA",
			Content: "大阪でラーメンを食べました",
		},
		{
			SourceID: "c3", ChunkID: "c3", KnowledgeID: "k2", KnowledgeBaseID: "kb2", IsEnabled: true,
			Content: "another knowledge base",
		},
	}
	emb := map[string]any{fieldEmbedding: map[string][]float32{
		"c1": {1, 0, 0}, "c2": {0, 1, 0}, "c3": {0.9, 0.1, 0},
	}}
	require.NoError(t, repo.BatchSave(ctx, infos, emb))
	// Saving again must upsert, not duplicate.
	require.NoError(t, repo.BatchSave(ctx, infos[:1], emb))

	vector := func(kbs ...string) []*types.IndexWithScore {
		res, err := repo.Retrieve(ctx, types.RetrieveParams{
			RetrieverType: types.VectorRetrieverType,
			Embedding:     []float32{1, 0, 0}, TopK: 10, KnowledgeBaseIDs: kbs,
		})
		require.NoError(t, err)
		return res[0].Results
	}
	got := vector("kb1")
	require.Len(t, got, 2)
	require.Equal(t, "c1", got[0].ChunkID)
	require.InDelta(t, 1.0, got[0].Score, 1e-5)
	require.Equal(t, "東京の天気は晴れです。WeKnora deploy guide", got[0].Content)
	require.Equal(t, "tagA", got[1].TagID)

	keywords := func(q string) []*types.IndexWithScore {
		res, err := repo.Retrieve(ctx, types.RetrieveParams{
			RetrieverType: types.KeywordsRetrieverType,
			Query:         q, TopK: 10, KnowledgeBaseIDs: []string{"kb1"},
		})
		require.NoError(t, err)
		return res[0].Results
	}
	require.Equal(t, "c1", keywords("東京の天気")[0].ChunkID)
	require.Equal(t, "c2", keywords("ラーメン")[0].ChunkID)
	require.Equal(t, "c1", keywords("deploy")[0].ChunkID)

	require.NoError(t, repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{"c1": false}))
	require.Len(t, vector("kb1"), 1)
	require.NoError(t, repo.BatchUpdateChunkTagID(ctx, map[string]string{"c2": "tagB"}))
	require.Equal(t, "tagB", vector("kb1")[0].TagID)

	require.NoError(t, repo.CopyIndices(ctx, "kb1", map[string]string{"k1": "k9"},
		map[string]string{"c1": "c1x", "c2": "c2x"}, "kb9", 3, ""))
	copied := vector("kb9")
	require.Len(t, copied, 1) // c1x is disabled, like its source
	require.Equal(t, "c2x", copied[0].ChunkID)
	require.Equal(t, "k9", copied[0].KnowledgeID)

	require.NoError(t, repo.MoveKnowledgeIndices(ctx, "kb2", "kb3", "k2", nil, 3, ""))
	require.Len(t, vector("kb3"), 1)

	require.NoError(t, repo.DeleteByKnowledgeIDList(ctx, []string{"k9"}, 3, ""))
	require.Empty(t, vector("kb9"))
	require.NoError(t, repo.DeleteByChunkIDList(ctx, []string{"c2"}, 3, ""))
	require.Empty(t, vector("kb1"))

	// Multi-batch writes/copies (more rows than writeBatchSize), with a
	// duplicated ID inside one BatchSave call (last one wins).
	n := writeBatchSize*2 + 7
	bulk := make([]*types.IndexInfo, 0, n+1)
	bulkEmb := map[string][]float32{}
	knowledgeMap, chunkMap := map[string]string{"kb": "kb-copy"}, map[string]string{}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("b%d", i)
		bulk = append(bulk, &types.IndexInfo{
			SourceID: id, ChunkID: id, KnowledgeID: "kb", KnowledgeBaseID: "bulk", Content: id, IsEnabled: true,
		})
		bulkEmb[id] = []float32{1, float32(i), 0}
		chunkMap[id] = id + "-copy"
	}
	bulk = append(bulk, &types.IndexInfo{
		SourceID: "b0", ChunkID: "b0", KnowledgeID: "kb", KnowledgeBaseID: "bulk", Content: "dup", IsEnabled: true,
	})
	require.NoError(t, repo.BatchSave(ctx, bulk, map[string]any{fieldEmbedding: bulkEmb}))
	count := func(kb string) (c int) {
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE knowledge_base_id = :1", kb).Scan(&c))
		return c
	}
	require.Equal(t, n, count("bulk"))
	var content string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT content FROM "+table+" WHERE id = 'b0'").Scan(&content))
	require.Equal(t, "dup", content)

	require.NoError(t, repo.CopyIndices(ctx, "bulk", knowledgeMap, chunkMap, "bulk-copy", 3, ""))
	require.Equal(t, n, count("bulk-copy"))

	// Realistic sizes: a 1024-dim vector and multi-byte content, both longer
	// than one VARCHAR2 bind, go through the split TO_CLOB(...) || ... path.
	table1024 := tableName(base, 1024)
	defer func() {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table1024+" PURGE")
		_, _ = db.ExecContext(ctx,
			"BEGIN CTX_DDL.DROP_PREFERENCE('"+table1024+"_LEX'); EXCEPTION WHEN OTHERS THEN NULL; END;")
	}()
	vec := make([]float32, 1024)
	for i := range vec {
		vec[i] = float32(i%7) / 7
	}
	longContent := strings.Repeat("長い本文テキスト。", 600) // ~16 KB
	require.NoError(t, repo.BatchSave(ctx, []*types.IndexInfo{{
		SourceID: "big", ChunkID: "big", KnowledgeID: "kbig", KnowledgeBaseID: "kbig",
		Content: longContent, IsEnabled: true,
	}}, map[string]any{fieldEmbedding: map[string][]float32{"big": vec}}))
	res, err := repo.Retrieve(ctx, types.RetrieveParams{
		RetrieverType: types.VectorRetrieverType, Embedding: vec, TopK: 1, KnowledgeBaseIDs: []string{"kbig"},
	})
	require.NoError(t, err)
	require.Len(t, res[0].Results, 1)
	require.InDelta(t, 1.0, res[0].Results[0].Score, 1e-5)
	require.Equal(t, longContent, res[0].Results[0].Content)

	// Concurrent first writes from separate repositories (≈ separate app
	// processes, so the in-process mutex does not help) must still end with
	// every index created: DDL waits on DDL_LOCK_TIMEOUT instead of failing.
	table4 := tableName(base, 4)
	defer func() {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table4+" PURGE")
		_, _ = db.ExecContext(ctx,
			"BEGIN CTX_DDL.DROP_PREFERENCE('"+table4+"_LEX'); EXCEPTION WHEN OTHERS THEN NULL; END;")
	}()
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := NewOracleRetrieveEngineRepository(db, &types.IndexConfig{CollectionPrefix: base})
			id := fmt.Sprintf("p%d", i)
			errs <- r.BatchSave(ctx, []*types.IndexInfo{{
				SourceID: id, ChunkID: id, KnowledgeID: "k", KnowledgeBaseID: "kb4", Content: id, IsEnabled: true,
			}}, map[string]any{fieldEmbedding: map[string][]float32{id: {1, 2, 3, float32(i)}}})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var indexes int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_indexes WHERE table_name = :1
		AND index_name IN (:2, :3, :4, :5, :6, :7)`, table4,
		table4+"_CHK", table4+"_KNW", table4+"_KB", table4+"_SRC", table4+"_TXT", table4+"_VEC").Scan(&indexes))
	require.Equal(t, 6, indexes)
}
