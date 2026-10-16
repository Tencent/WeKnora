package vastbase

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Integration test against a real Vastbase G100 instance. Skipped unless
// WEKNORA_VASTBASE_IT=1. Connection env vars (with local defaults for dev
// boxes):
//
//	VASTBASE_HOST / VASTBASE_PORT / VASTBASE_USER / VASTBASE_PASSWORD / VASTBASE_DATABASE
func itDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("WEKNORA_VASTBASE_IT") != "1" {
		t.Skip("set WEKNORA_VASTBASE_IT=1 to run Vastbase integration tests")
	}
	getEnv := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		getEnv("VASTBASE_HOST", "127.0.0.1"),
		getEnv("VASTBASE_PORT", "5432"),
		getEnv("VASTBASE_USER", "tom"),
		getEnv("VASTBASE_PASSWORD", ""),
		getEnv("VASTBASE_DATABASE", "weknora_vastbase_it"),
		getEnv("VASTBASE_SSLMODE", "disable"),
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		t.Fatalf("connect vastbase: %v", err)
	}
	// Clean slate for a deterministic run.
	if err := db.Exec("DROP TABLE IF EXISTS embeddings CASCADE").Error; err != nil {
		t.Fatalf("drop table: %v", err)
	}
	return db
}

func makeIndexInfos(prefix string, n, dim int, rng *rand.Rand) ([]*types.IndexInfo, map[string][]float32) {
	infos := make([]*types.IndexInfo, n)
	embs := make(map[string][]float32, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%d", prefix, i)
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()
		}
		infos[i] = &types.IndexInfo{
			Content:         fmt.Sprintf("content %s", id),
			SourceID:        id,
			SourceType:      0,
			ChunkID:         "chunk-" + id,
			KnowledgeID:     "know-" + prefix,
			KnowledgeBaseID: "kb-" + prefix,
			IsEnabled:       true,
		}
		embs[id] = v
	}
	return infos, embs
}

func TestVastbaseEndToEnd(t *testing.T) {
	db := itDB(t)
	ctx := context.Background()

	repoIface, err := NewVastbaseRetrieveEngineRepository(db, ConfigFromEnv())
	if err != nil {
		t.Fatalf("init repository: %v", err)
	}
	repo := repoIface.(*vastbaseRepository)

	if got := repo.EngineType(); got != types.VastbaseRetrieverEngineType {
		t.Fatalf("engine type = %s", got)
	}
	if got := repo.Support(); len(got) != 1 || got[0] != types.VectorRetrieverType {
		t.Fatalf("support = %v", got)
	}

	const dim = 128
	rng := rand.New(rand.NewSource(42))

	// --- BatchSave + lazy Graph_Index creation ---
	infos, embs := makeIndexInfos("src", 200, dim, rng)
	if err := repo.BatchSave(ctx, infos, map[string]any{"embedding": embs}); err != nil {
		t.Fatalf("batch save: %v", err)
	}
	var count int64
	db.Table("embeddings").Count(&count)
	if count != 200 {
		t.Fatalf("row count = %d, want 200", count)
	}
	var idxCount int64
	db.Raw("SELECT count(*) FROM pg_indexes WHERE tablename='embeddings' AND indexdef LIKE '%graph_index%'").Scan(&idxCount)
	if idxCount != 1 {
		t.Fatalf("graph_index count = %d, want 1", idxCount)
	}

	// --- Idempotent re-save (ON CONFLICT (source_id, source_type) DO NOTHING) ---
	if err := repo.BatchSave(ctx, infos, map[string]any{"embedding": embs}); err != nil {
		t.Fatalf("duplicate batch save: %v", err)
	}
	db.Table("embeddings").Count(&count)
	if count != 200 {
		t.Fatalf("row count after dup save = %d, want 200", count)
	}

	// --- VectorRetrieve: exact vector must rank first with score ~1 ---
	target := infos[7]
	res, err := repo.Retrieve(ctx, types.RetrieveParams{
		RetrieverType:    types.VectorRetrieverType,
		KnowledgeBaseIDs: []string{"kb-src"},
		Embedding:        embs[target.SourceID],
		TopK:             10,
		Threshold:        0.0,
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(res) != 1 || len(res[0].Results) != 10 {
		t.Fatalf("unexpected result shape: %+v", res)
	}
	top := res[0].Results[0]
	if top.SourceID != target.SourceID {
		t.Fatalf("top result = %s, want %s", top.SourceID, target.SourceID)
	}
	if top.Score < 0.999 {
		t.Fatalf("top score = %f, want ~1", top.Score)
	}
	if res[0].RetrieverEngineType != types.VastbaseRetrieverEngineType {
		t.Fatalf("result engine type = %s", res[0].RetrieverEngineType)
	}
	// Scores must be sorted descending and within [0,1].
	for i := 1; i < len(res[0].Results); i++ {
		if res[0].Results[i].Score > res[0].Results[i-1].Score {
			t.Fatalf("results not sorted at %d", i)
		}
	}

	// --- Threshold filtering ---
	res, err = repo.Retrieve(ctx, types.RetrieveParams{
		RetrieverType:    types.VectorRetrieverType,
		KnowledgeBaseIDs: []string{"kb-src"},
		Embedding:        embs[target.SourceID],
		TopK:             200,
		Threshold:        0.999,
	})
	if err != nil {
		t.Fatalf("threshold retrieve: %v", err)
	}
	if len(res[0].Results) == 0 || res[0].Results[0].SourceID != target.SourceID {
		t.Fatalf("threshold retrieve lost the exact match")
	}
	for _, r := range res[0].Results {
		if r.Score < 0.999 {
			t.Fatalf("threshold not applied: score %f", r.Score)
		}
	}

	// --- Unsupported retriever type ---
	if _, err := repo.Retrieve(ctx, types.RetrieveParams{RetrieverType: types.KeywordsRetrieverType}); err == nil {
		t.Fatalf("expected error for keywords retrieval")
	}

	// --- Enable/disable filter ---
	if err := repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{target.ChunkID: false}); err != nil {
		t.Fatalf("disable chunk: %v", err)
	}
	res, err = repo.Retrieve(ctx, types.RetrieveParams{
		RetrieverType:    types.VectorRetrieverType,
		KnowledgeBaseIDs: []string{"kb-src"},
		Embedding:        embs[target.SourceID],
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("retrieve after disable: %v", err)
	}
	for _, r := range res[0].Results {
		if r.SourceID == target.SourceID {
			t.Fatalf("disabled chunk still returned")
		}
	}
	if err := repo.BatchUpdateChunkEnabledStatus(ctx, map[string]bool{target.ChunkID: true}); err != nil {
		t.Fatalf("re-enable chunk: %v", err)
	}

	// --- Tag update + tag filter ---
	if err := repo.BatchUpdateChunkTagID(ctx, map[string]string{target.ChunkID: "tag-1"}); err != nil {
		t.Fatalf("tag update: %v", err)
	}
	res, err = repo.Retrieve(ctx, types.RetrieveParams{
		RetrieverType:    types.VectorRetrieverType,
		KnowledgeBaseIDs: []string{"kb-src"},
		TagIDs:           []string{"tag-1"},
		Embedding:        embs[target.SourceID],
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("tag retrieve: %v", err)
	}
	if len(res[0].Results) != 1 || res[0].Results[0].SourceID != target.SourceID {
		t.Fatalf("tag filter returned %d results", len(res[0].Results))
	}

	// --- CopyIndices ---
	chunkMap := make(map[string]string, len(infos))
	knowMap := map[string]string{"know-src": "know-dst"}
	for _, info := range infos {
		chunkMap[info.ChunkID] = "dst-" + info.ChunkID
	}
	if err := repo.CopyIndices(ctx, "kb-src", knowMap, chunkMap, "kb-dst", dim, ""); err != nil {
		t.Fatalf("copy indices: %v", err)
	}
	db.Table("embeddings").Where("knowledge_base_id = ?", "kb-dst").Count(&count)
	if count != 200 {
		t.Fatalf("copied rows = %d, want 200", count)
	}

	// --- MoveKnowledgeIndices (KnowledgeIndexMover) ---
	mover, ok := repoIface.(interface {
		MoveKnowledgeIndices(ctx context.Context, sourceKB, targetKB, knowledgeID string, _ []string, _ int, _ string) error
	})
	if !ok {
		t.Fatalf("repository does not implement KnowledgeIndexMover")
	}
	if err := mover.MoveKnowledgeIndices(ctx, "kb-dst", "kb-moved", "know-dst", nil, dim, ""); err != nil {
		t.Fatalf("move indices: %v", err)
	}
	db.Table("embeddings").Where("knowledge_base_id = ?", "kb-moved").Count(&count)
	if count != 200 {
		t.Fatalf("moved rows = %d, want 200", count)
	}

	// --- Deletes ---
	if err := repo.DeleteBySourceIDList(ctx, []string{"src-0", "src-1"}, dim, ""); err != nil {
		t.Fatalf("delete by source: %v", err)
	}
	if err := repo.DeleteByChunkIDList(ctx, []string{"dst-chunk-src-2"}, dim, ""); err != nil {
		t.Fatalf("delete by chunk: %v", err)
	}
	if err := repo.DeleteByKnowledgeIDList(ctx, []string{"know-src"}, dim, ""); err != nil {
		t.Fatalf("delete by knowledge: %v", err)
	}
	db.Table("embeddings").Where("knowledge_base_id = ?", "kb-src").Count(&count)
	if count != 0 {
		t.Fatalf("kb-src rows left = %d, want 0", count)
	}
	db.Table("embeddings").Where("knowledge_base_id = ?", "kb-moved").Count(&count)
	if count != 199 {
		t.Fatalf("kb-moved rows = %d, want 199", count)
	}

	// --- EstimateStorageSize sanity ---
	if size := repo.EstimateStorageSize(ctx, infos[:10], map[string]any{"embedding": embs}); size <= 0 {
		t.Fatalf("estimate storage size = %d", size)
	}
}

// TestVastbaseGraphIndexPerformance loads 20k vectors, verifies the planner
// uses the Graph_Index ANN scan, and checks query latency and recall of the
// exact-match vector.
func TestVastbaseGraphIndexPerformance(t *testing.T) {
	db := itDB(t)
	ctx := context.Background()

	repoIface, err := NewVastbaseRetrieveEngineRepository(db, ConfigFromEnv())
	if err != nil {
		t.Fatalf("init repository: %v", err)
	}
	repo := repoIface.(*vastbaseRepository)

	const dim = 1024
	const total = 20000
	rng := rand.New(rand.NewSource(7))

	start := time.Now()
	batch := 1000
	for off := 0; off < total; off += batch {
		infos, embs := makeIndexInfos(fmt.Sprintf("perf%d", off), batch, dim, rng)
		if err := repo.BatchSave(ctx, infos, map[string]any{"embedding": embs}); err != nil {
			t.Fatalf("batch save at %d: %v", off, err)
		}
		if off == 0 {
			// Keep one known vector for the recall check below.
			defer func() {
				res, err := repo.Retrieve(ctx, types.RetrieveParams{
					RetrieverType:    types.VectorRetrieverType,
					KnowledgeBaseIDs: []string{"kb-perf0"},
					Embedding:        embs[infos[3].SourceID],
					TopK:             10,
				})
				if err != nil {
					t.Errorf("final retrieve: %v", err)
					return
				}
				if res[0].Results[0].SourceID != infos[3].SourceID {
					t.Errorf("exact match not ranked first: got %s", res[0].Results[0].SourceID)
				}
			}()
		}
	}
	t.Logf("inserted %d x %d-dim vectors in %s", total, dim, time.Since(start))

	if err := db.Exec("ANALYZE embeddings").Error; err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// Planner must choose the ANN Index Scan on the partial Graph_Index when
	// no selective scalar filter competes with it.
	litVec := strings.TrimSuffix(strings.Repeat("0.5,", dim), ",")
	explainSQL := fmt.Sprintf(`EXPLAIN SELECT id FROM embeddings
		WHERE dimension = %d
		ORDER BY embedding::halfvector(%d) <=> '[%s]'::halfvector(%d) LIMIT 10`,
		dim, dim, litVec, dim)
	rows, err := db.Raw(explainSQL).Rows()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var planLines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain row: %v", err)
		}
		planLines = append(planLines, line)
	}
	rows.Close()
	plan := strings.Join(planLines, "\n")
	if !strings.Contains(plan, "ANN Index Scan") {
		t.Fatalf("query plan does not use the Graph_Index:\n%s", plan)
	}

	// Latency check.
	q := make([]float32, dim)
	for j := range q {
		q[j] = rng.Float32()
	}
	qStart := time.Now()
	const runs = 20
	for i := 0; i < runs; i++ {
		// No KB filter: exercises the Graph_Index ANN scan over the full table.
		if _, err := repo.Retrieve(ctx, types.RetrieveParams{
			RetrieverType: types.VectorRetrieverType,
			Embedding:     q,
			TopK:          10,
		}); err != nil {
			t.Fatalf("retrieve: %v", err)
		}
	}
	avg := time.Since(qStart) / runs
	t.Logf("average retrieve latency over %d runs: %s", runs, avg)
	if avg > 500*time.Millisecond {
		t.Fatalf("average latency %s too high — Graph_Index likely not used", avg)
	}
}

// sqlRecorder captures the SQL that GORM logs so a test can pin query shape.
type sqlRecorder struct {
	mu    sync.Mutex
	stmts []string
}

func (r *sqlRecorder) Printf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, fmt.Sprintf(format, args...))
}

// matching returns the logged statements whose upper-cased text contains substr.
func (r *sqlRecorder) matching(substr string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, stmt := range r.stmts {
		if strings.Contains(strings.ToUpper(stmt), substr) {
			out = append(out, stmt)
		}
	}
	return out
}

// TestVastbaseCopyIndicesKeysetPaging pins the paging contract of CopyIndices:
// more than one 500-row page of mapped source data must be copied exactly once
// each, while rows that are not in the knowledge mapping (same source KB) or
// that live in another KB are left alone. The page queries are also asserted to
// be keyset (id > lastID ORDER BY id, no OFFSET): offset paging over an
// unordered scan can skip or repeat rows when the physical order differs
// between pages, which is not observable on freshly inserted test data.
func TestVastbaseCopyIndicesKeysetPaging(t *testing.T) {
	db := itDB(t)
	ctx := context.Background()

	rec := &sqlRecorder{}
	repoIface, err := NewVastbaseRetrieveEngineRepository(
		db.Session(&gorm.Session{Logger: gormlogger.New(rec, gormlogger.Config{LogLevel: gormlogger.Info})}),
		ConfigFromEnv(),
	)
	if err != nil {
		t.Fatalf("init repository: %v", err)
	}
	repo := repoIface.(*vastbaseRepository)

	const dim = 8
	rng := rand.New(rand.NewSource(7))

	build := func(kbID, knowledgeID, prefix string, n int) ([]*types.IndexInfo, map[string][]float32) {
		infos := make([]*types.IndexInfo, n)
		embs := make(map[string][]float32, n)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s-%d", prefix, i)
			v := make([]float32, dim)
			for j := range v {
				v[j] = rng.Float32()
			}
			infos[i] = &types.IndexInfo{
				Content:         "content " + id,
				SourceID:        id,
				SourceType:      0,
				ChunkID:         "chunk-" + id,
				KnowledgeID:     knowledgeID,
				KnowledgeBaseID: kbID,
				IsEnabled:       true,
			}
			embs[id] = v
		}
		return infos, embs
	}

	// 600 mapped rows: two pages of the 500-row batch loop.
	mapped, embs := build("kb-src", "know-a", "a", 600)
	// Unrelated knowledge inside the same source KB: excluded by the
	// knowledge_id filter.
	unrelated, unrelatedEmbs := build("kb-src", "know-other", "other", 120)
	// Mapped knowledge id in a different KB: excluded by the KB filter. The
	// knowledge id is deliberately reused here (it does not happen in
	// production, where a knowledge belongs to one KB) so that the test fails
	// if the source knowledge-base filter is ever dropped.
	elsewhere, elsewhereEmbs := build("kb-elsewhere", "know-a", "else", 30)

	all := append(append([]*types.IndexInfo{}, mapped...), append(unrelated, elsewhere...)...)
	for k, v := range unrelatedEmbs {
		embs[k] = v
	}
	for k, v := range elsewhereEmbs {
		embs[k] = v
	}
	if err := repo.BatchSave(ctx, all, map[string]any{"embedding": embs}); err != nil {
		t.Fatalf("batch save: %v", err)
	}

	chunkMap := make(map[string]string, len(mapped))
	for _, info := range mapped {
		chunkMap[info.ChunkID] = "dst-" + info.ChunkID
	}
	if err := repo.CopyIndices(ctx, "kb-src", map[string]string{"know-a": "know-dst"},
		chunkMap, "kb-dst", dim, ""); err != nil {
		t.Fatalf("copy indices: %v", err)
	}

	// The 600 mapped rows must be read in exactly two keyset pages of 500.
	var pageQueries []string
	for _, stmt := range rec.matching("KNOWLEDGE_BASE_ID") {
		if strings.Contains(strings.ToUpper(stmt), "SELECT") {
			pageQueries = append(pageQueries, stmt)
		}
	}
	if len(pageQueries) != 2 {
		t.Fatalf("copy issued %d page queries, want 2: %v", len(pageQueries), pageQueries)
	}
	for i, stmt := range pageQueries {
		upper := strings.ToUpper(stmt)
		for _, want := range []string{"ID >", "ORDER BY", "KNOWLEDGE_ID IN"} {
			if !strings.Contains(upper, want) {
				t.Errorf("page query %d lacks %q: %s", i, want, stmt)
			}
		}
		if strings.Contains(upper, "OFFSET") {
			t.Errorf("page query %d pages by OFFSET: %s", i, stmt)
		}
	}

	var rows []struct {
		ChunkID     string
		KnowledgeID string
		N           int
	}
	if err := db.Table("embeddings").
		Select("chunk_id, knowledge_id, count(*) as n").
		Where("knowledge_base_id = ?", "kb-dst").
		Group("chunk_id, knowledge_id").
		Scan(&rows).Error; err != nil {
		t.Fatalf("query target rows: %v", err)
	}

	copied := make(map[string]int, len(rows))
	for _, row := range rows {
		if row.KnowledgeID != "know-dst" {
			t.Errorf("target row %s has knowledge id %s, want know-dst", row.ChunkID, row.KnowledgeID)
		}
		if row.N != 1 {
			t.Errorf("target chunk %s copied %d times, want exactly 1", row.ChunkID, row.N)
		}
		copied[row.ChunkID] += row.N
	}
	if len(copied) != len(mapped) {
		t.Errorf("copied %d distinct chunks, want %d", len(copied), len(mapped))
	}
	for _, info := range mapped {
		want := "dst-" + info.ChunkID
		if _, ok := copied[want]; !ok {
			t.Errorf("target chunk %s was not copied", want)
		}
	}

	var total int64
	db.Table("embeddings").Where("knowledge_base_id = ?", "kb-dst").Count(&total)
	if total != int64(len(mapped)) {
		t.Errorf("target row count = %d, want %d", total, len(mapped))
	}

	// Source data untouched: the unmapped rows must not have moved or been
	// consumed by the copy.
	db.Table("embeddings").
		Where("knowledge_base_id = ? AND knowledge_id = ?", "kb-src", "know-other").Count(&total)
	if total != int64(len(unrelated)) {
		t.Errorf("unmapped source rows = %d, want %d", total, len(unrelated))
	}
	db.Table("embeddings").Where("knowledge_base_id = ?", "kb-elsewhere").Count(&total)
	if total != int64(len(elsewhere)) {
		t.Errorf("other-KB rows = %d, want %d", total, len(elsewhere))
	}

	// Copied content is the source content, not a re-embedding artifact.
	var content string
	db.Table("embeddings").
		Where("knowledge_base_id = ? AND chunk_id = ?", "kb-dst", "dst-chunk-a-599").
		Pluck("content", &content)
	if content != "content a-599" {
		t.Errorf("copied content = %q, want %q", content, "content a-599")
	}
}
