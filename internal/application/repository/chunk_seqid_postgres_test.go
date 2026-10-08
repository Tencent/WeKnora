package repository

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// pgChunkSeqIDSequenceStart mirrors CREATE SEQUENCE chunks_seq_id_seq START WITH
// 100000000 in migrations/versioned/000010_add_seq_id.up.sql. It is deliberately
// spelled out here instead of referring to types.ChunkSeqIDSequenceStart so
// that these tests keep compiling (and keep asserting real behaviour) when the
// source change they guard is reverted.
const pgChunkSeqIDSequenceStart int64 = 100000000

// setupChunkSeqIDPostgresDB returns a *gorm.DB bound to a throwaway schema on a
// disposable PostgreSQL, with a chunks table equivalent to migrations
// 000010: seq_id defaults to nextval('chunks_seq_id_seq') and the sequence
// starts at 100000000. Nothing outside the schema is touched and the schema is
// dropped when the test ends, so the tests do not depend on (or disturb) any
// existing data.
//
// Opt in with a disposable database, for example:
//
//	WEKNORA_CHUNK_TEST_POSTGRES_DSN=postgres://postgres:pg@127.0.0.1:5432/weknora_seqtest?sslmode=disable
func setupChunkSeqIDPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WEKNORA_CHUNK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_CHUNK_TEST_POSTGRES_DSN to run PostgreSQL seq_id tests")
	}

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminSQL.Close() })

	schema := "chunk_seqid_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })

	db, err := gorm.Open(postgres.Open(withSearchPath(t, dsn, schema)), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	// AutoMigrate gives seq_id a private bigserial; replace it with the exact
	// shape migration 000010 installs (own sequence, explicit start).
	require.NoError(t, db.Exec("ALTER TABLE chunks ALTER COLUMN seq_id DROP DEFAULT").Error)
	require.NoError(t, db.Exec("DROP SEQUENCE IF EXISTS chunks_seq_id_seq").Error)
	require.NoError(t, db.Exec("CREATE SEQUENCE chunks_seq_id_seq START WITH 100000000").Error)
	require.NoError(t, db.Exec("ALTER TABLE chunks ALTER COLUMN seq_id SET DEFAULT nextval('chunks_seq_id_seq')").Error)
	require.NoError(t, db.Exec("ALTER SEQUENCE chunks_seq_id_seq OWNED BY chunks.seq_id").Error)
	return db
}

// withSearchPath pins the connection to the per-test schema. The DSN is a URL
// in every documented invocation, so the parameter is added as a query value
// (pgx forwards unknown query values as runtime parameters); keyword/value DSNs
// get the equivalent suffix.
func withSearchPath(t *testing.T, dsn, schema string) string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		return dsn + " search_path=" + schema
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// seedChunkWithSeqID inserts a row with an explicit seq_id through raw SQL, the
// way a legacy installation (or an older release running in another instance)
// would have left it in the table. It bypasses CreateChunks on purpose so the
// test can set up "this id is already taken" independently of the code under
// test.
func seedChunkWithSeqID(t *testing.T, db *gorm.DB, seqID int64) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, db.Exec(
		`INSERT INTO chunks (id, seq_id, tenant_id, knowledge_id, knowledge_base_id, `+
			`content, chunk_type, is_enabled, created_at, updated_at)
		 VALUES (?, ?, 1, 'k-seed', 'kb-seed', 'seed', 'faq', true, now(), now())`, id, seqID).Error)
	return id
}

// storedChunkSeqID reads the persisted seq_id, so assertions do not depend on
// GORM writing the allocated value back into the struct.
func storedChunkSeqID(t *testing.T, db *gorm.DB, id string) int64 {
	t.Helper()
	var seqID int64
	require.NoError(t, db.Unscoped().Model(&types.Chunk{}).
		Where("id = ?", id).Pluck("seq_id", &seqID).Error)
	return seqID
}

func requireChunkSeqIDsUnique(t *testing.T, db *gorm.DB) {
	t.Helper()
	var duplicates []int64
	require.NoError(t, db.Raw(
		"SELECT seq_id FROM chunks GROUP BY seq_id HAVING count(*) > 1").Scan(&duplicates).Error)
	require.Empty(t, duplicates, "chunks.seq_id 必须全局唯一")
}

// Scenario 1: same instance, an import that has to reassign a taken id, and
// then an ordinary document chunk. Before the fix PG entered the MAX(seq_id)+1
// branch for the reassigned row: the explicit value equalled the sequence's
// next number, so the next ordinary insert died on idx_chunks_seq_id.
func TestCreateChunks_Postgres_RemapThenOrdinaryChunk(t *testing.T) {
	db := setupChunkSeqIDPostgresDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	// A healthy instance: 100000000 is taken by a real row and the sequence has
	// been advanced past it, i.e. seq_last == max(seq_id).
	seedChunkWithSeqID(t, db, pgChunkSeqIDSequenceStart)
	require.NoError(t, db.Exec("SELECT setval('chunks_seq_id_seq', 100000000)").Error)

	// Re-importing that same entry into another KB of the same instance: the
	// requested id is taken (the original row is live here), so CreateChunks has
	// to reassign it.
	imported := makeChunk("kb-import", "k-import", types.ChunkTypeFAQ)
	imported.SeqID = pgChunkSeqIDSequenceStart
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{imported}))
	require.NotEqual(t, pgChunkSeqIDSequenceStart, storedChunkSeqID(t, db, imported.ID),
		"被占用的 seq_id 必须重新分配")
	require.GreaterOrEqual(t, imported.SeqID, pgChunkSeqIDSequenceStart,
		"CreateChunks 必须把序列分配的 id 写回入参："+
			"导入端按 chunks[i].SeqID != 请求值 统计 seq_id_remapped_count")

	// The regression: an ordinary document chunk takes its id from the
	// sequence. The reassigned row must not have stolen the sequence's next
	// number.
	ordinary := []*types.Chunk{
		makeChunk("kb-doc", "k-doc", types.ChunkTypeText),
		makeChunk("kb-doc", "k-doc", types.ChunkTypeText),
		makeChunk("kb-doc", "k-doc", types.ChunkTypeText),
	}
	require.NoError(t, repo.CreateChunks(ctx, ordinary),
		"重分配之后普通文档 chunk 仍必须能从序列取到未被占用的 seq_id")

	for _, chunk := range ordinary {
		require.GreaterOrEqual(t, storedChunkSeqID(t, db, chunk.ID), pgChunkSeqIDSequenceStart,
			"普通 chunk 的 seq_id 应来自序列")
	}
	requireChunkSeqIDsUnique(t, db)
}

// Scenario 2: cross-instance import. The exported id is >= 100000000 and still
// free on the target instance — it is in the sequence's future range, so it
// must be reallocated rather than written as-is.
func TestCreateChunks_Postgres_CrossInstanceHighSeqIDRemapped(t *testing.T) {
	db := setupChunkSeqIDPostgresDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	// The target instance already handed out ids up to 100000041.
	require.NoError(t, db.Exec("SELECT setval('chunks_seq_id_seq', 100000041)").Error)

	imported := makeChunk("kb-import", "k-import", types.ChunkTypeFAQ)
	imported.SeqID = 100000050 // free here, but inside the sequence's range
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{imported}))

	stored := storedChunkSeqID(t, db, imported.ID)
	require.NotEqual(t, int64(100000050), stored,
		"PG 上 >= 100000000 的导入 id 必须重新分配，否则后续 nextval 会再发一次同一个号")
	require.GreaterOrEqual(t, stored, pgChunkSeqIDSequenceStart)
	require.Equal(t, stored, imported.SeqID,
		"CreateChunks 必须把序列分配的 id 写回入参，导入端才能上报真实 id 和重分配计数")

	// Walk the sequence past the value the export wanted: every ordinary chunk
	// must insert cleanly.
	ordinary := make([]*types.Chunk, 0, 20)
	for i := 0; i < 20; i++ {
		ordinary = append(ordinary, makeChunk("kb-doc", "k-doc", types.ChunkTypeText))
	}
	require.NoError(t, repo.CreateChunks(ctx, ordinary),
		"跨实例导入高位 id 后，普通插入不能与它冲突")
	requireChunkSeqIDsUnique(t, db)
}

// Scenario 3: concurrent reassignment. Several transactions release/allocate
// ids at the same time; the database sequence serialises them, so no duplicate
// seq_id and no failed insert is allowed.
func TestCreateChunks_Postgres_ConcurrentRemapKeepsSeqIDsUnique(t *testing.T) {
	db := setupChunkSeqIDPostgresDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	seedChunkWithSeqID(t, db, pgChunkSeqIDSequenceStart)
	require.NoError(t, db.Exec("SELECT setval('chunks_seq_id_seq', 100000000)").Error)

	const workers = 8
	chunks := make([]*types.Chunk, workers)
	for i := range chunks {
		chunks[i] = makeChunk("kb-concurrent", "k-concurrent", types.ChunkTypeFAQ)
		chunks[i].SeqID = pgChunkSeqIDSequenceStart // every worker asks for the taken id
	}

	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range chunks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = repo.CreateChunks(ctx, []*types.Chunk{chunks[i]})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "并发重分配 worker %d 不应失败", i)
	}

	var total, distinct int64
	require.NoError(t, db.Unscoped().Model(&types.Chunk{}).Count(&total).Error)
	require.NoError(t, db.Unscoped().Model(&types.Chunk{}).Distinct("seq_id").Count(&distinct).Error)
	require.Equal(t, total, distinct, "并发重分配产生的 seq_id 不得重复")
	require.EqualValues(t, workers+1, total, "所有并发导入都应落库")
	requireChunkSeqIDsUnique(t, db)
}

// Scenario 4 (documented rule): a low-range id that is free is preserved
// verbatim, and a low-range id that is taken is reallocated from the sequence
// instead of MAX(seq_id)+1. Both sub-cases must leave the sequence usable for
// ordinary chunks.
func TestCreateChunks_Postgres_LowRangeSeqIDRules(t *testing.T) {
	db := setupChunkSeqIDPostgresDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	// 42 migrated from an older installation and is still free: keep it.
	kept := makeChunk("kb-migrate", "k-migrate", types.ChunkTypeFAQ)
	kept.SeqID = 42
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{kept}))
	require.Equal(t, int64(42), storedChunkSeqID(t, db, kept.ID),
		"低于 100000000 且未被占用的 id 必须原样保留")

	// 43 is already taken by a migrated row: reassign it from the sequence.
	seedChunkWithSeqID(t, db, 43)
	reassigned := makeChunk("kb-migrate", "k-migrate", types.ChunkTypeFAQ)
	reassigned.SeqID = 43
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{reassigned}))
	stored := storedChunkSeqID(t, db, reassigned.ID)
	require.NotEqual(t, int64(43), stored, "已被占用的低位 id 必须重新分配")
	require.GreaterOrEqual(t, stored, pgChunkSeqIDSequenceStart,
		"PG 上重新分配必须走序列，而不是 MAX(seq_id)+1")

	ordinary := []*types.Chunk{
		makeChunk("kb-doc", "k-doc", types.ChunkTypeText),
		makeChunk("kb-doc", "k-doc", types.ChunkTypeText),
	}
	require.NoError(t, repo.CreateChunks(ctx, ordinary))
	requireChunkSeqIDsUnique(t, db)
}
