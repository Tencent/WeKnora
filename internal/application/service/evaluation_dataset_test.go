package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEvaluationDatasetServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	previousDir, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(repoRoot))
	dbPath := filepath.Join(t.TempDir(), "evaluation-dataset.db")
	migrationErr := database.RunMigrationsWithOptions(
		"sqlite3://unused",
		database.MigrationOptions{SQLiteDBPath: dbPath},
	)
	require.NoError(t, os.Chdir(previousDir))
	require.NoError(t, migrationErr)

	db, err := gorm.Open(sqlite.Open(dbPath+"?_foreign_keys=on"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

func newEvaluationDatasetRegistryService(db *gorm.DB) interfaces.EvaluationDatasetRegistryService {
	return NewEvaluationDatasetRegistryService(&config.Config{}, repository.NewEvaluationDatasetRepository(db))
}

func evaluationDatasetServiceFixture() *types.EvaluationDatasetVersionInput {
	return &types.EvaluationDatasetVersionInput{
		Passages: []types.EvaluationDatasetPassageInput{
			{PID: "p1", Content: "passage one"},
			{PID: "p2", Content: "passage two"},
		},
		Questions: []types.EvaluationDatasetQuestionInput{
			{QID: "q1", Question: "question one?", Answer: "answer one"},
		},
		Relevance: []types.EvaluationDatasetRelevanceInput{
			{QID: "q1", PID: "p1", Grade: 1},
		},
	}
}

func TestEvaluationDatasetRegistryServiceCreateDatasetAndVersion(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	service := newEvaluationDatasetRegistryService(db)
	ctx := context.Background()

	dataset, err := service.CreateDataset(ctx, 7, "Tenant dataset", "for tests")
	require.NoError(t, err)
	require.NotNil(t, dataset.OwnerTenantID)
	assert.Equal(t, uint64(7), *dataset.OwnerTenantID)

	version, err := service.CreateVersion(ctx, 7, dataset.ID, evaluationDatasetServiceFixture())
	require.NoError(t, err)
	assert.Equal(t, 1, version.VersionNumber)
	assert.Len(t, version.ContentSHA256, 64)
	assert.Len(t, version.ArtifactSHA256, 64)
	assert.NotEqual(t, version.ContentSHA256, version.ArtifactSHA256)

	// Second version increments the human-readable number; ids stay global.
	second, err := service.CreateVersion(ctx, 7, dataset.ID, &types.EvaluationDatasetVersionInput{
		Passages: []types.EvaluationDatasetPassageInput{{PID: "p1", Content: "changed"}},
		Questions: []types.EvaluationDatasetQuestionInput{
			{QID: "q1", Question: "question one?", Answer: "answer one"},
		},
		Relevance: []types.EvaluationDatasetRelevanceInput{{QID: "q1", PID: "p1", Grade: 1}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, second.VersionNumber)
	assert.NotEqual(t, version.ID, second.ID)

	versions, err := service.ListVersions(ctx, 7, dataset.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 2)
}

func TestEvaluationDatasetRegistryServiceUnknownDatasetFails(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	service := newEvaluationDatasetRegistryService(db)
	ctx := context.Background()

	// Unknown dataset ids fail explicitly; no fallback to a default dataset.
	_, err := service.CreateVersion(ctx, 7, "dataset-missing", evaluationDatasetServiceFixture())
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetNotFound)
	_, err = service.GetVersionContent(ctx, 7, "dataset-version-missing")
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetVersionNotFound)
	_, err = service.ListVersions(ctx, 7, "dataset-missing")
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetNotFound)
}

func TestEvaluationDatasetRegistryServiceCrossTenantDenied(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	service := newEvaluationDatasetRegistryService(db)
	ctx := context.Background()

	dataset, err := service.CreateDataset(ctx, 7, "Tenant 7 dataset", "")
	require.NoError(t, err)

	// Cross-tenant version creation is denied with the same NotFound used
	// for missing datasets.
	_, err = service.CreateVersion(ctx, 8, dataset.ID, evaluationDatasetServiceFixture())
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetNotFound)
}

func TestEvaluationDatasetRegistryServiceEnforcesLimits(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	limits := config.DefaultEvaluationDatasetLimits()
	limits.MaxPassages = 2
	limits.MaxQuestions = 1
	limits.MaxRelevance = 1
	limits.MaxQuestionBytes = 8
	limits.MaxPassageBytes = 16
	service := NewEvaluationDatasetRegistryService(
		&config.Config{Evaluation: &config.EvaluationConfig{Dataset: &limits}},
		repository.NewEvaluationDatasetRepository(db),
	)
	ctx := context.Background()

	dataset, err := service.CreateDataset(ctx, 7, "Limited dataset", "")
	require.NoError(t, err)

	cases := []struct {
		name    string
		content *types.EvaluationDatasetVersionInput
	}{
		{"too many passages", &types.EvaluationDatasetVersionInput{
			Passages: []types.EvaluationDatasetPassageInput{
				{PID: "p1", Content: "a"}, {PID: "p2", Content: "b"}, {PID: "p3", Content: "c"},
			},
		}},
		{"too many questions", &types.EvaluationDatasetVersionInput{
			Questions: []types.EvaluationDatasetQuestionInput{
				{QID: "q1", Question: "q1"}, {QID: "q2", Question: "q2"},
			},
		}},
		{"oversized question", &types.EvaluationDatasetVersionInput{
			Questions: []types.EvaluationDatasetQuestionInput{
				{QID: "q1", Question: strings.Repeat("q", 9)},
			},
		}},
		{"oversized passage", &types.EvaluationDatasetVersionInput{
			Passages: []types.EvaluationDatasetPassageInput{
				{PID: "p1", Content: strings.Repeat("p", 17)},
			},
		}},
		{"oversized answer", &types.EvaluationDatasetVersionInput{
			Questions: []types.EvaluationDatasetQuestionInput{
				{QID: "q1", Question: "q1", Answer: strings.Repeat("a", 17)},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.CreateVersion(ctx, 7, dataset.ID, tc.content)
			require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetLimitExceeded)
		})
	}

	// Nothing was persisted by the rejected versions.
	versions, err := service.ListVersions(ctx, 7, dataset.ID)
	require.NoError(t, err)
	assert.Empty(t, versions)
}

func TestEvaluationDatasetRegistryServiceBuiltinRegistration(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	service := newEvaluationDatasetRegistryService(db)
	ctx := context.Background()

	content := evaluationDatasetServiceFixture()
	artifact := []byte(`{"bundle":"samples-v1"}`)
	registration := &types.EvaluationBuiltinDatasetRegistration{
		DatasetID:              "default",
		Name:                   "Built-in samples",
		Description:            "Parquet sample bundle",
		Content:                content,
		ArtifactBytes:          artifact,
		ExpectedArtifactSHA256: types.EvaluationDatasetArtifactSHA256(artifact),
	}

	version, err := service.RegisterBuiltinDataset(ctx, registration)
	require.NoError(t, err)
	assert.Equal(t, 1, version.VersionNumber)
	assert.Equal(t, registration.ExpectedArtifactSHA256, version.ArtifactSHA256)

	// Re-registering identical content is idempotent and returns the same version.
	again, err := service.RegisterBuiltinDataset(ctx, registration)
	require.NoError(t, err)
	assert.Equal(t, version.ID, again.ID)

	// System datasets are readable by any tenant.
	readable, err := service.GetVersionContent(ctx, 42, version.ID)
	require.NoError(t, err)
	require.Len(t, readable.Questions, 1)

	// A tampered bundle fails the pinned artifact check before any write.
	tampered := &types.EvaluationBuiltinDatasetRegistration{
		DatasetID:              "default-tampered",
		Name:                   "Tampered",
		Content:                content,
		ArtifactBytes:          []byte(`{"bundle":"evil"}`),
		ExpectedArtifactSHA256: registration.ExpectedArtifactSHA256,
	}
	_, err = service.RegisterBuiltinDataset(ctx, tampered)
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetArtifactMismatch)

	// Tenant APIs cannot create versions on the system dataset.
	_, err = service.CreateVersion(ctx, 7, "default", evaluationDatasetServiceFixture())
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetNotFound)
}
