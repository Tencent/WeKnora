package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// LEARNING_TEST_POSTGRES_DSN must name a disposable test server/database. Tests
// create their own schema and never read or mutate the application's tables.
func learningTestDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	var err error
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	if dialect == "postgres" {
		dsn := os.Getenv("LEARNING_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("LEARNING_TEST_POSTGRES_DSN is not set")
		}
		cfg, parseErr := pgx.ParseConfig(dsn)
		if parseErr != nil {
			t.Fatal("invalid PostgreSQL test configuration")
		}
		admin := stdlib.OpenDB(*cfg)
		if err := admin.Ping(); err != nil {
			_ = admin.Close()
			t.Fatal("PostgreSQL test connection failed")
		}
		schema := "learning_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
			_ = admin.Close()
			t.Fatal("cannot create isolated PostgreSQL test schema")
		}
		t.Cleanup(func() {
			_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
			if err != nil {
				t.Error("cannot remove owned PostgreSQL test schema")
			}
			_ = admin.Close()
		})
		cfg.RuntimeParams["search_path"] = schema
		conn := stdlib.OpenDB(*cfg)
		conn.SetMaxOpenConns(12)
		t.Cleanup(func() { _ = conn.Close() })
		db, err = gorm.Open(postgres.New(postgres.Config{Conn: conn}), config)
	} else {
		db, err = gorm.Open(
			sqlite.Open(
				filepath.Join(t.TempDir(), "learning.db")+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=1",
			),
			config,
		)
		if err == nil {
			conn, err := db.DB()
			require.NoError(t, err)
			conn.SetMaxOpenConns(12)
			t.Cleanup(func() { _ = conn.Close() })
		}
	}
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.WikiPage{}, &types.Knowledge{}, &types.Chunk{}))
	file := "../../../migrations/sqlite/000015_learning.up.sql"
	if dialect == "postgres" {
		file = "../../../migrations/versioned/000094_learning.up.sql"
	}
	up, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up)).Error)
	return db
}

func learningSeed(t *testing.T, db *gorm.DB) (*types.WikiPage, *types.Chunk) {
	t.Helper()
	kb := &types.KnowledgeBase{
		ID:               uuid.NewString(),
		TenantID:         7,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
		SummaryModelID:   "model",
	}
	require.NoError(t, db.Create(kb).Error)
	doc := &types.Knowledge{
		ID:              uuid.NewString(),
		TenantID:        7,
		KnowledgeBaseID: kb.ID,
		EnableStatus:    "enabled",
		ParseStatus:     "completed",
		CustomMetadata:  types.JSON("{}"),
	}
	require.NoError(t, db.Create(doc).Error)
	c := &types.Chunk{
		ID:              uuid.NewString(),
		TenantID:        7,
		KnowledgeBaseID: kb.ID,
		KnowledgeID:     doc.ID,
		IsEnabled:       true,
		Content:         "Atomic writes commit together. Leases expire at deadlines. A primary key identifies a row.",
		ChunkType:       types.ChunkTypeText,
		IndexStatus:     "ready",
	}
	require.NoError(t, db.Create(c).Error)
	p := &types.WikiPage{
		ID:              uuid.NewString(),
		TenantID:        7,
		KnowledgeBaseID: kb.ID,
		Slug:            "concept/test",
		Title:           "Test",
		Content:         c.Content,
		PageType:        "concept",
		Status:          "published",
		SourceRefs:      types.StringArray{doc.ID},
		ChunkRefs:       types.StringArray{c.ID},
	}
	require.NoError(t, db.Create(p).Error)
	return p, c
}

func learningTestQuestions(c *types.Chunk) []types.LearningQuestion {
	qs := make([]types.LearningQuestion, 3)
	for i := range qs {
		qs[i] = types.LearningQuestion{
			Prompt:        fmt.Sprintf("Which source claim applies %d?", i),
			CorrectOption: "a",
			Explanation:   "PRIVATE_EXPLANATION",
			Options: []types.LearningOption{
				{ID: "a", Text: "Together"},
				{ID: "b", Text: "Never"},
				{ID: "c", Text: "Sometimes"},
				{ID: "d", Text: "Unknown"},
			},
			Evidence: []types.LearningEvidence{
				{ChunkID: c.ID, KnowledgeID: c.KnowledgeID, Quote: "Atomic writes commit together."},
			},
		}
	}
	return qs
}

func TestLearningRepositoryTransactions(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := learningTestDB(t, dialect)
			repo := NewLearningRepository(db)
			ctx := context.Background()
			scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}
			p, c := learningSeed(t, db)
			_, err := repo.SetEnabled(ctx, scope, true)
			require.NoError(t, err)
			_, wake, err := repo.PrepareQuiz(ctx, scope, p.ID)
			require.NoError(t, err)
			var wg sync.WaitGroup
			claims := make(chan *types.LearningClaim, 8)
			errs := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() { defer wg.Done(); claim, err := repo.Claim(ctx, *wake); claims <- claim; errs <- err }()
			}
			wg.Wait()
			close(claims)
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			var claim *types.LearningClaim
			for c := range claims {
				if c != nil {
					require.Nil(t, claim)
					claim = c
				}
			}
			require.NotNil(t, claim)
			require.NoError(t, repo.Publish(ctx, claim, learningTestQuestions(c)))
			q, err := repo.GetQuiz(ctx, scope, claim.Quiz.ID)
			require.NoError(t, err)
			require.Equal(t, "ready", q.Status)
			errs = make(chan error, 12)
			for i := range 12 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := repo.SubmitAnswer(
						ctx,
						scope,
						types.LearningAnswer{
							QuestionID: q.Questions[0].ID,
							OptionID:   "a",
							AttemptID:  fmt.Sprintf("attempt%d", i),
						},
					)
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			accepted := 0
			for err := range errs {
				if err == nil {
					accepted++
				} else {
					require.ErrorIs(t, err, types.ErrLearningConflict)
				}
			}
			require.Equal(t, 1, accepted)
			for i := 1; i < 3; i++ {
				_, err := repo.SubmitAnswer(
					ctx,
					scope,
					types.LearningAnswer{QuestionID: q.Questions[i].ID, OptionID: "a", AttemptID: fmt.Sprint(i)},
				)
				require.NoError(t, err)
			}
			_, wake, err = repo.PrepareQuiz(ctx, scope, p.ID)
			require.NoError(t, err)
			old, err := repo.Claim(ctx, *wake)
			require.NoError(t, err)
			require.NotNil(t, old)
			require.NoError(
				t,
				db.Model(&types.LearningQuiz{}).
					Where("id = ?", old.Quiz.ID).
					Update("lease_until", time.Now().UTC().Add(-time.Minute)).
					Error,
			)
			current, err := repo.Claim(ctx, *wake)
			require.NoError(t, err)
			require.NotNil(t, current)
			require.ErrorIs(t, repo.Publish(ctx, old, learningTestQuestions(c)), types.ErrLearningStale)
			_, err = repo.Clear(ctx, scope, "")
			require.NoError(t, err)
			_, err = repo.SetEnabled(ctx, scope, true)
			require.NoError(t, err)
			require.ErrorIs(t, repo.Publish(ctx, current, learningTestQuestions(c)), types.ErrLearningStale)
			for _, table := range []string{
				"learning_quizzes", "learning_questions", "learning_attempts", "learning_mastery",
			} {
				var n int64
				require.NoError(t, db.Table(table).Count(&n).Error)
				require.Zero(t, n)
			}
		})
	}
}

func TestLearningPostgresSourceUpdateSerializesWithPublication(t *testing.T) {
	db := learningTestDB(t, "postgres")
	repo := NewLearningRepository(db)
	p, c := learningSeed(t, db)
	scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}
	ctx := context.Background()
	_, err := repo.SetEnabled(ctx, scope, true)
	require.NoError(t, err)
	_, wake, err := repo.PrepareQuiz(ctx, scope, p.ID)
	require.NoError(t, err)
	claim, err := repo.Claim(ctx, *wake)
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, tx.Model(c).Update("content", c.Content+" Changed").Error)
	done := make(chan error, 1)
	go func() { done <- repo.Publish(ctx, claim, learningTestQuestions(c)) }()
	select {
	case err := <-done:
		t.Fatalf("publication bypassed source lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, tx.Commit().Error)
	require.ErrorIs(t, <-done, types.ErrLearningStale)
	var n int64
	require.NoError(t, db.Model(&types.LearningQuestion{}).Count(&n).Error)
	require.Zero(t, n)
}

func TestLearningOverlayBatchesTwoThousandNodes(t *testing.T) {
	db := learningTestDB(t, "sqlite")
	repo := NewLearningRepository(db)
	p, _ := learningSeed(t, db)
	scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:alice"}
	_, err := repo.SetEnabled(context.Background(), scope, true)
	require.NoError(t, err)
	pages := make([]types.WikiPage, 1999)
	slugs := []string{p.Slug}
	for i := range pages {
		pages[i] = *p
		pages[i].ID, pages[i].Slug = uuid.NewString(), fmt.Sprintf("concept/%d", i)
		slugs = append(slugs, pages[i].Slug)
	}
	require.NoError(t, db.CreateInBatches(&pages, 50).Error)
	var queries atomic.Int64
	require.NoError(
		t,
		db.Callback().Query().After("gorm:query").Register("learning_count_queries", func(*gorm.DB) { queries.Add(1) }),
	)
	nodes, err := repo.Overlay(context.Background(), scope, p.KnowledgeBaseID, slugs)
	require.NoError(t, err)
	require.Len(t, nodes, 2000)
	require.Less(t, queries.Load(), int64(10), "unassessed overlay must not perform per-node queries")
	// A batch freshness stamp must equal the transactional generation stamp.
	source, err := learningSource(db, scope.TenantID, p.ID)
	require.NoError(t, err)
	m := &types.LearningMastery{
		TenantID:        scope.TenantID,
		SubjectID:       scope.SubjectID,
		PageID:          p.ID,
		KnowledgeBaseID: p.KnowledgeBaseID,
		PMastery:        .7,
		Attempts:        2,
		SourceStamp:     source.Stamp,
	}
	require.NoError(t, db.Create(m).Error)
	queries.Store(0)
	nodes, err = repo.Overlay(context.Background(), scope, p.KnowledgeBaseID, slugs)
	require.NoError(t, err)
	for _, n := range nodes {
		if n.Page.ID == p.ID {
			require.Equal(t, source.Stamp, n.SourceStamp)
		}
	}
	require.Less(t, queries.Load(), int64(12))
	b, err := json.Marshal(types.LearningNodePublic(nodes[0], time.Now()))
	require.NoError(t, err)
	require.NotContains(t, string(b), "PRIVATE")
}
