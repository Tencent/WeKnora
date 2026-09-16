package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestEvaluationDatasetRepositoryPostgresContract verifies the registry
// against the real PostgreSQL 000005 migration in an isolated schema with
// transactional rollback.
func TestEvaluationDatasetRepositoryPostgresContract(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() { _ = tx.Rollback().Error })

	schema := fmt.Sprintf("m3_dataset_registry_%d", time.Now().UnixNano())
	require.NoError(t, tx.Exec("CREATE SCHEMA "+schema).Error)
	require.NoError(t, tx.Exec("SET LOCAL search_path = "+schema+", pg_catalog").Error)

	migrationPath := filepath.Join(
		"..", "..", "..", "migrations", "topic3", "postgres", "000005_evaluation_datasets.up.sql",
	)
	migrationSQL, err := os.ReadFile(migrationPath)
	require.NoError(t, err)
	require.NoError(t, tx.Exec(string(migrationSQL)).Error)

	repo := NewEvaluationDatasetRepository(tx)
	ctx := context.Background()

	tenant := uint64(7)
	require.NoError(t, repo.CreateDataset(ctx, &types.EvaluationDataset{
		ID: "dataset-postgres", Scope: types.EvaluationDatasetScopeTenant,
		OwnerTenantID: &tenant, Name: "Postgres fixture",
	}))
	version, content := newEvaluationDatasetVersionFixture("dataset-postgres", "postgres-v1")
	content.Relevance = append(content.Relevance, types.EvaluationDatasetRelevanceInput{QID: "q2", PID: "p1", Grade: 0})
	version.ContentSHA256 = types.CanonicalEvaluationDatasetContentSHA256(content)
	version.RelevanceCount = len(content.Relevance)
	require.NoError(t, repo.CreateVersion(ctx, version, content))

	got, err := repo.GetVersion(ctx, tenant, version.ID)
	require.NoError(t, err)
	assert.Equal(t, version.ContentSHA256, got.ContentSHA256)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())

	full, err := repo.GetVersionContent(ctx, tenant, version.ID)
	require.NoError(t, err)
	require.Len(t, full.Questions, 2)
	assert.Equal(t, "q1", full.Questions[0].QID)
	foundZero := false
	for _, relevance := range full.Relevance {
		if relevance.QID == "q2" && relevance.PID == "p1" {
			assert.Zero(t, relevance.Grade, "explicit zero relevance must survive PostgreSQL persistence")
			foundZero = true
		}
	}
	require.True(t, foundZero)

	// Unique constraints hold on PostgreSQL as on SQLite.
	duplicate, sameContent := newEvaluationDatasetVersionFixture("dataset-postgres", "postgres-dup")
	err = repo.CreateVersion(ctx, duplicate, sameContent)
	require.ErrorIs(t, err, ErrEvaluationDatasetVersionConflict)

	// Foreign keys reject dangling relevance even if the caller bypasses
	// service-side validation.
	dangling := `INSERT INTO evaluation_dataset_relevance (dataset_version_id, qid, pid, grade)
		VALUES ($1, 'q-missing', 'p1', 1)`
	require.Error(t, tx.Exec(dangling, version.ID).Error)
}
