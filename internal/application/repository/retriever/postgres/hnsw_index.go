package postgres

import (
	"context"
	"fmt"
	"os"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/logger"
)

// maxHNSWHalfvecDimension is the widest halfvec pgvector can index (README,
// "Indexing": halfvec up to 4,000 dimensions). Wider embeddings are still
// stored and searched; they just cannot get an HNSW index.
const maxHNSWHalfvecDimension = 4000

// hnswAdvisoryLockKey is the first key of the session advisory lock that
// serializes index building across processes; the dimension is the second.
const hnswAdvisoryLockKey = 0x574B4E48

// hnswIndexName is the name migrations 000002 and 000059 give the partial
// HNSW index of one dimension. Keeping the shape lets a later migration for
// the same dimension recognise what the app built and stay a no-op.
func hnswIndexName(dimension int) string {
	return fmt.Sprintf("embeddings_embedding_idx_%d", dimension)
}

// autoHNSWIndexEnabled reports whether the app may build indexes itself.
// AUTO_MIGRATE=false means the schema is managed outside the app, and that
// covers this DDL as well.
func autoHNSWIndexEnabled() bool {
	return os.Getenv("AUTO_MIGRATE") != "false"
}

// ensureHNSWIndex schedules, once per dimension per process, a background
// check that the embeddings table has a partial HNSW index for that
// dimension, the way migrations 000002 and 000059 provide for 3584, 798 and
// 1024. Any other dimension fell through to a sequential scan on every
// vector query (#3298). It runs on every write and vector query, so it only
// touches a map; the request is never blocked and never fails because of
// the index.
func (g *pgRepository) ensureHNSWIndex(dimension int) {
	if dimension <= 0 || !g.autoIndex {
		return
	}
	if _, seen := g.hnswSeen.LoadOrStore(dimension, struct{}{}); seen {
		return
	}
	go g.buildHNSWIndex(dimension)
}

// buildHNSWIndexInBackground is the default buildHNSWIndex. The dimension
// stays marked as seen whatever happens, so a failure is reported once per
// process instead of on every request, and the log line says what to do.
func (g *pgRepository) buildHNSWIndexInBackground(dimension int) {
	if err := g.createHNSWIndex(dimension); err != nil {
		logger.GetLogger(context.Background()).Warnf("[Postgres] HNSW index for %d-dim embeddings: %v "+
			"(vector queries on this dimension keep scanning sequentially; "+
			"build it by hand with CREATE INDEX CONCURRENTLY, or restart to retry)", dimension, err)
	}
}

// hnswIndexRow is one HNSW index over a dimension's halfvec cast, whatever it
// is called: the migrations' name, or one an operator built by hand.
type hnswIndexRow struct {
	Name  string `gorm:"column:name"` // indexrelid::regclass::text, ready for DROP INDEX
	Valid bool   `gorm:"column:valid"`
}

// createHNSWIndex builds the partial HNSW index for one dimension unless a
// valid one already exists. An invalid index of the app's own name is the
// leftover of a build that did not finish and is dropped first, because IF
// NOT EXISTS would keep it forever; an invalid index of any other name is
// left alone. The whole sequence runs on one pinned connection under a
// session advisory lock, so replicas take turns instead of dropping each
// other's builds, and CREATE INDEX CONCURRENTLY runs outside a transaction
// as PostgreSQL requires. statement_timeout is lifted for the DDL and reset
// before the connection is returned to the pool.
func (g *pgRepository) createHNSWIndex(dimension int) error {
	ctx := context.Background()
	log := logger.GetLogger(ctx)
	if dimension > maxHNSWHalfvecDimension {
		log.Warnf("[Postgres] No HNSW index for %d-dim embeddings: pgvector indexes halfvec up to %d dimensions, "+
			"so vector queries on this dimension scan sequentially", dimension, maxHNSWHalfvecDimension)
		return nil
	}
	name := hnswIndexName(dimension)
	return g.db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		// Every statement gets its own session on the pinned connection: gorm
		// keeps one statement per instance here, so an error from one would
		// otherwise stick and silently skip the ones after it, the unlock
		// included.
		on := func() *gorm.DB { return conn.Session(&gorm.Session{NewDB: true}) }
		var locked bool
		if err := on().Raw("SELECT pg_try_advisory_lock(?, ?)", hnswAdvisoryLockKey, dimension).
			Scan(&locked).Error; err != nil {
			return fmt.Errorf("could not take the build lock: %w", err)
		}
		if !locked {
			log.Infof("[Postgres] Another process is building the HNSW index for %d-dim embeddings", dimension)
			return nil
		}
		defer on().Exec("SELECT pg_advisory_unlock(?, ?)", hnswAdvisoryLockKey, dimension)

		var rows []hnswIndexRow
		err := on().Raw(`
			SELECT i.indexrelid::regclass::text AS name, i.indisvalid AS valid
			FROM pg_index i
			WHERE i.indrelid = 'embeddings'::regclass
			  AND pg_get_indexdef(i.indexrelid) LIKE '%USING hnsw%'
			  AND pg_get_indexdef(i.indexrelid) LIKE ?
			  AND pg_get_indexdef(i.indexrelid) LIKE '%halfvec_cosine_ops%'`,
			fmt.Sprintf("%%halfvec(%d)%%", dimension)).Scan(&rows).Error
		if err != nil {
			return fmt.Errorf("could not read the catalog: %w", err)
		}
		for _, row := range rows {
			if row.Valid {
				log.Debugf("[Postgres] HNSW index %s already covers %d-dim embeddings", row.Name, dimension)
				return nil
			}
		}

		// Everything from here may run a CONCURRENTLY statement, which can take
		// a long time on a large table. A statement_timeout set on the role or
		// the database would cancel it and leave exactly the invalid index this
		// function exists to repair — which then waits for a restart to be
		// retried. Lift it for this session only. SET LOCAL is not an option:
		// CONCURRENTLY cannot run inside a transaction. The connection goes
		// back to the pool afterwards, so the RESET is not optional either.
		if err := on().Exec("SET statement_timeout = 0").Error; err != nil {
			return fmt.Errorf("could not lift statement_timeout for the build: %w", err)
		}
		defer func() {
			if err := on().Exec("RESET statement_timeout").Error; err != nil {
				log.Warnf("[Postgres] Could not reset statement_timeout after the HNSW build: %v "+
					"(this pooled connection may run without the configured timeout until it is recycled)", err)
			}
		}()
		for _, row := range rows {
			if row.Name != name {
				log.Warnf("[Postgres] Invalid HNSW index %s over %d-dim embeddings is not the app's; leaving it alone "+
					"(drop it by hand if no build is running)", row.Name, dimension)
				continue
			}
			// The app's own name, invalid: a build that died, or one still
			// running in a session that does not hold the lock (an older
			// version, an operator's psql). Only the first may be dropped.
			var building int64
			inFlight := "SELECT count(*) FROM pg_stat_progress_create_index WHERE relid = 'embeddings'::regclass"
			if err := on().Raw(inFlight).Scan(&building).Error; err != nil {
				return fmt.Errorf("could not tell whether %s is still being built: %w", name, err)
			}
			if building > 0 {
				log.Infof("[Postgres] HNSW index %s is being built by another session; leaving it to finish", name)
				return nil
			}
			log.Warnf("[Postgres] Dropping invalid HNSW index %s (an earlier build did not finish) "+
				"before rebuilding it", name)
			if err := on().Exec("DROP INDEX CONCURRENTLY IF EXISTS " + row.Name).Error; err != nil {
				return fmt.Errorf("could not drop invalid index %s: %w", name, err)
			}
		}

		log.Infof("[Postgres] Building HNSW index %s for %d-dim embeddings in the background; "+
			"vector queries on this dimension scan sequentially until it is ready", name, dimension)
		err = on().Exec(fmt.Sprintf(
			"CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON embeddings "+
				"USING hnsw ((embedding::halfvec(%d)) halfvec_cosine_ops) "+
				"WITH (m = 16, ef_construction = 64) WHERE (dimension = %d)",
			name, dimension, dimension)).Error
		if err != nil {
			return fmt.Errorf("could not build %s: %w", name, err)
		}
		// IF NOT EXISTS says nothing about the index it found, so read back
		// what is there before calling it ready.
		var valid bool
		result := on().Raw("SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass(?)", name).Scan(&valid)
		if result.Error != nil {
			return fmt.Errorf("could not verify %s: %w", name, result.Error)
		}
		if result.RowsAffected != 1 || !valid {
			return fmt.Errorf("%s is missing or invalid after the build", name)
		}
		log.Infof("[Postgres] HNSW index %s for %d-dim embeddings is ready", name, dimension)
		return nil
	})
}
