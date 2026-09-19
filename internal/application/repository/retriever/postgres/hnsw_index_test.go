package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Tencent/WeKnora/internal/types"
)

func newHNSWTestRepo(t *testing.T) (*pgRepository, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               gormlogger.Discard,
	})
	require.NoError(t, err)

	repo := NewPostgresRetrieveEngineRepository(db).(*pgRepository)
	return repo, mock
}

// recordBuilds swaps the background build for a recorder and returns a
// function that waits for the expected number of scheduled builds and
// fails on any extra one.
func recordBuilds(repo *pgRepository) func(t *testing.T, want int) []int {
	built := make(chan int, 16)
	repo.buildHNSWIndex = func(dimension int) { built <- dimension }
	return func(t *testing.T, want int) []int {
		t.Helper()
		var got []int
		for len(got) < want {
			select {
			case dim := <-built:
				got = append(got, dim)
			case <-time.After(2 * time.Second):
				t.Fatalf("expected %d scheduled builds, got %v", want, got)
			}
		}
		select {
		case dim := <-built:
			t.Fatalf("unexpected extra build for dimension %d", dim)
		case <-time.After(50 * time.Millisecond):
		}
		sort.Ints(got)
		return got
	}
}

// The statements createHNSWIndex may issue, in the order they can appear.
const (
	lockSQL    = `SELECT pg_try_advisory_lock\(\$1, \$2\)`
	catalogSQL = `SELECT i\.indexrelid::regclass::text AS name, i\.indisvalid AS valid[\s\S]*` +
		`WHERE i\.indrelid = 'embeddings'::regclass[\s\S]*LIKE '%USING hnsw%'[\s\S]*` +
		`LIKE \$1[\s\S]*LIKE '%halfvec_cosine_ops%'`
	progressSQL = `SELECT count\(\*\) FROM pg_stat_progress_create_index WHERE relid = 'embeddings'::regclass`
	createSQL   = `CREATE INDEX CONCURRENTLY IF NOT EXISTS embeddings_embedding_idx_2560 ON embeddings ` +
		`USING hnsw \(\(embedding::halfvec\(2560\)\) halfvec_cosine_ops\) ` +
		`WITH \(m = 16, ef_construction = 64\) WHERE \(dimension = 2560\)`
	verifySQL = `SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass\(\$1\)`
	unlockSQL = `SELECT pg_advisory_unlock\(\$1, \$2\)`
)

func expectLock(mock sqlmock.Sqlmock, dimension int, granted bool) {
	mock.ExpectQuery(lockSQL).WithArgs(hnswAdvisoryLockKey, dimension).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(granted))
}

func expectCatalog(mock sqlmock.Sqlmock, dimension int, rows *sqlmock.Rows) {
	mock.ExpectQuery(catalogSQL).WithArgs(fmt.Sprintf("%%halfvec(%d)%%", dimension)).WillReturnRows(rows)
}

func catalogRows(entries ...hnswIndexRow) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{"name", "valid"})
	for _, e := range entries {
		rows.AddRow(e.Name, e.Valid)
	}
	return rows
}

func expectUnlock(mock sqlmock.Sqlmock, dimension int) {
	mock.ExpectExec(unlockSQL).WithArgs(hnswAdvisoryLockKey, dimension).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func expectVerify(mock sqlmock.Sqlmock, valid bool) {
	mock.ExpectQuery(verifySQL).WithArgs("embeddings_embedding_idx_2560").
		WillReturnRows(sqlmock.NewRows([]string{"indisvalid"}).AddRow(valid))
}

func TestEnsureHNSWIndexSchedulesOncePerDimension(t *testing.T) {
	repo, _ := newHNSWTestRepo(t)
	wait := recordBuilds(repo)

	for range 3 {
		repo.ensureHNSWIndex(2560)
	}
	repo.ensureHNSWIndex(1024)
	repo.ensureHNSWIndex(0)
	repo.ensureHNSWIndex(-1)

	require.Equal(t, []int{1024, 2560}, wait(t, 2))
}

func TestEnsureHNSWIndexStaysOutWhenMigrationsAreManagedOutside(t *testing.T) {
	t.Setenv("AUTO_MIGRATE", "false")
	repo, _ := newHNSWTestRepo(t)
	wait := recordBuilds(repo)

	repo.ensureHNSWIndex(2560)

	require.Empty(t, wait(t, 0))
}

func TestCreateHNSWIndexBuildsWhenMissing(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows())
	mock.ExpectExec(createSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	expectVerify(mock, true)
	expectUnlock(mock, 2560)

	require.NoError(t, repo.createHNSWIndex(2560))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexKeepsAnExistingValidIndexWhateverItsName(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows(hnswIndexRow{Name: "hand_built_hnsw_2560", Valid: true}))
	expectUnlock(mock, 2560)

	require.NoError(t, repo.createHNSWIndex(2560))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexReplacesItsOwnInvalidIndexWhenNoBuildIsRunning(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows(hnswIndexRow{Name: "embeddings_embedding_idx_2560", Valid: false}))
	mock.ExpectQuery(progressSQL).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec(`DROP INDEX CONCURRENTLY IF EXISTS embeddings_embedding_idx_2560`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(createSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	expectVerify(mock, true)
	expectUnlock(mock, 2560)

	require.NoError(t, repo.createHNSWIndex(2560))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexLeavesAnIndexAnotherSessionIsStillBuilding(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows(hnswIndexRow{Name: "embeddings_embedding_idx_2560", Valid: false}))
	mock.ExpectQuery(progressSQL).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	expectUnlock(mock, 2560)

	require.NoError(t, repo.createHNSWIndex(2560))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexLeavesAForeignInvalidIndexAloneAndBuildsItsOwn(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows(hnswIndexRow{Name: "hand_built_hnsw_2560", Valid: false}))
	mock.ExpectExec(createSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	expectVerify(mock, true)
	expectUnlock(mock, 2560)

	require.NoError(t, repo.createHNSWIndex(2560))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexYieldsWhenAnotherProcessHoldsTheLock(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, false)

	require.NoError(t, repo.createHNSWIndex(2560))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexSkipsDimensionsHalfvecCannotIndex(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)

	require.NoError(t, repo.createHNSWIndex(4096))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexReportsABuildFailureAndReleasesTheLock(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows())
	mock.ExpectExec(createSQL).WillReturnError(errors.New("ERROR: permission denied for schema public"))
	expectUnlock(mock, 2560)

	err := repo.createHNSWIndex(2560)

	require.ErrorContains(t, err, "permission denied")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateHNSWIndexReportsAnIndexLeftInvalid(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	expectCatalog(mock, 2560, catalogRows())
	mock.ExpectExec(createSQL).WillReturnResult(sqlmock.NewResult(0, 0))
	expectVerify(mock, false)
	expectUnlock(mock, 2560)

	err := repo.createHNSWIndex(2560)

	require.ErrorContains(t, err, "missing or invalid")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBuildHNSWIndexInBackgroundOnlyLogsAFailure(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	expectLock(mock, 2560, true)
	mock.ExpectQuery(catalogSQL).WillReturnError(errors.New("connection reset"))
	expectUnlock(mock, 2560)

	repo.buildHNSWIndexInBackground(2560)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSaveSchedulesTheIndexForTheStoredDimension(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	wait := recordBuilds(repo)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "embeddings"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectCommit()

	err := repo.Save(context.Background(),
		&types.IndexInfo{SourceID: "s1", ChunkID: "c1", KnowledgeID: "k1", KnowledgeBaseID: "kb1", Content: "x"},
		map[string]any{"embedding": map[string][]float32{"s1": make([]float32, 1536)}})

	require.NoError(t, err)
	require.Equal(t, []int{1536}, wait(t, 1))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchSaveSchedulesTheIndexForEveryStoredDimension(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	wait := recordBuilds(repo)
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "embeddings"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
	mock.ExpectCommit()

	err := repo.BatchSave(context.Background(),
		[]*types.IndexInfo{
			{SourceID: "s1", ChunkID: "c1", KnowledgeID: "k1", KnowledgeBaseID: "kb1", Content: "x"},
			{SourceID: "s2", ChunkID: "c2", KnowledgeID: "k1", KnowledgeBaseID: "kb1", Content: "y"},
		},
		map[string]any{"embedding": map[string][]float32{"s1": make([]float32, 1536), "s2": make([]float32, 2560)}})

	require.NoError(t, err)
	require.Equal(t, []int{1536, 2560}, wait(t, 2))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestVectorRetrieveSchedulesTheIndexForTheQueryDimension(t *testing.T) {
	repo, mock := newHNSWTestRepo(t)
	wait := recordBuilds(repo)
	// The query runs inside the SET LOCAL transaction VectorRetrieve opens.
	mock.ExpectBegin()
	mock.ExpectExec(`SET LOCAL hnsw.ef_search = \d+`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`SET LOCAL hnsw.iterative_scan = strict_order`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`SELECT`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectCommit()

	_, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding: make([]float32, 2560), TopK: 5, KnowledgeBaseIDs: []string{"kb1"},
	})

	require.NoError(t, err)
	require.Equal(t, []int{2560}, wait(t, 1))
	require.NoError(t, mock.ExpectationsWereMet())
}
