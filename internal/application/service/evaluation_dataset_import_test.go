package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	publicdata "github.com/Tencent/WeKnora/dataset/public"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func datasetImportFixture() *types.EvaluationDatasetImportInput {
	return &types.EvaluationDatasetImportInput{
		RequestID: uuid.NewString(), Name: "Imported fixture", Description: "test",
		Content: evaluationDatasetServiceFixture(),
	}
}

func TestEvaluationDatasetImportSQLiteContract(t *testing.T) {
	db := setupEvaluationDatasetServiceTestDB(t)
	runEvaluationDatasetImportContract(t, db)
}

func TestEvaluationDatasetImportPostgresContract(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("REQUIRE_POSTGRES_TESTS") == "1" {
			t.Fatal("TEST_POSTGRES_DSN is required")
		}
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = adminSQL.Close() })
	schema := "dataset_import_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	} else {
		dsn += " search_path=" + schema
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migration, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "topic3", "postgres",
		"000005_evaluation_datasets.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	runEvaluationDatasetImportContract(t, db)
}

func runEvaluationDatasetImportContract(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	registry := newEvaluationDatasetRegistryService(db)
	for name, mutate := range map[string]func(*types.EvaluationDatasetImportInput){
		"missing uuid":    func(input *types.EvaluationDatasetImportInput) { input.RequestID = "" },
		"nil uuid":        func(input *types.EvaluationDatasetImportInput) { input.RequestID = uuid.Nil.String() },
		"blank name":      func(input *types.EvaluationDatasetImportInput) { input.Name = "  " },
		"name length":     func(input *types.EvaluationDatasetImportInput) { input.Name = strings.Repeat("中", 256) },
		"empty passages":  func(input *types.EvaluationDatasetImportInput) { input.Content.Passages = nil },
		"empty questions": func(input *types.EvaluationDatasetImportInput) { input.Content.Questions = nil },
		"nil relevance":   func(input *types.EvaluationDatasetImportInput) { input.Content.Relevance = nil },
		"duplicate pid": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Passages[1].PID = input.Content.Passages[0].PID
		},
		"dangling qid": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Relevance[0].QID = "missing"
		},
		"dangling pid": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Relevance[0].PID = "missing"
		},
		"negative grade": func(input *types.EvaluationDatasetImportInput) { input.Content.Relevance[0].Grade = -1 },
		"grade overflow": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Relevance[0].Grade = 2147483648
		},
		"empty text": func(input *types.EvaluationDatasetImportInput) { input.Content.Passages[0].Content = " " },
		"NUL text": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Questions[0].Answer = "a\x00b"
		},
		"invalid metadata": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Passages[0].Metadata = types.JSON(`{"bad":`)
		},
		"array metadata": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Passages[0].Metadata = types.JSON(`[]`)
		},
		"nested NUL metadata": func(input *types.EvaluationDatasetImportInput) {
			input.Content.Passages[0].Metadata = types.JSON(`{"valid":{},"bad":["a\u0000b"]}`)
		},
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			input := datasetImportFixture()
			mutate(input)
			_, err := registry.ImportDataset(ctx, 7, input)
			require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetInvalid)
			assertDatasetImportCounts(t, db, 0, 0, 0, 0, 0)
		})
	}
	t.Run("limits", func(t *testing.T) {
		type configureLimits func(*config.EvaluationDatasetLimits, *types.EvaluationDatasetImportInput)
		for name, configure := range map[string]configureLimits{
			"request bytes": func(l *config.EvaluationDatasetLimits, _ *types.EvaluationDatasetImportInput) {
				l.MaxRequestBodyBytes = 32
			},
			"passage count": func(l *config.EvaluationDatasetLimits, _ *types.EvaluationDatasetImportInput) {
				l.MaxPassages = 1
			},
			"question count": func(l *config.EvaluationDatasetLimits, i *types.EvaluationDatasetImportInput) {
				l.MaxQuestions = 1
				i.Content.Questions = append(i.Content.Questions,
					types.EvaluationDatasetQuestionInput{QID: "q2", Question: "q"})
			},
			"relevance count": func(l *config.EvaluationDatasetLimits, i *types.EvaluationDatasetImportInput) {
				l.MaxRelevance = 1
				i.Content.Relevance = append(i.Content.Relevance,
					types.EvaluationDatasetRelevanceInput{QID: "q1", PID: "p2"})
			},
			"question bytes": func(l *config.EvaluationDatasetLimits, i *types.EvaluationDatasetImportInput) {
				l.MaxQuestionBytes = 1
				i.Description = ""
			},
			"passage bytes": func(l *config.EvaluationDatasetLimits, _ *types.EvaluationDatasetImportInput) {
				l.MaxPassageBytes = 1
			},
			"metadata bytes": func(l *config.EvaluationDatasetLimits, i *types.EvaluationDatasetImportInput) {
				l.MaxPassageBytes = 32
				i.Content.Passages[0].Metadata = types.JSON(`{"source":"` + strings.Repeat("x", 40) + `"}`)
			},
			"description bytes": func(l *config.EvaluationDatasetLimits, i *types.EvaluationDatasetImportInput) {
				l.MaxQuestionBytes = 32
				i.Description = strings.Repeat("中", 11)
			},
		} {
			t.Run(name, func(t *testing.T) {
				limits, input := config.DefaultEvaluationDatasetLimits(), datasetImportFixture()
				configure(&limits, input)
				limited := NewEvaluationDatasetRegistryService(
					&config.Config{Evaluation: &config.EvaluationConfig{Dataset: &limits}},
					repository.NewEvaluationDatasetRepository(db))
				_, err := limited.ImportDataset(ctx, 7, input)
				require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetLimitExceeded)
				assertDatasetImportCounts(t, db, 0, 0, 0, 0, 0)
			})
		}
	})
	t.Run("database failure rolls back identity and version", func(t *testing.T) {
		input := datasetImportFixture()
		for index := 2; index < 250; index++ {
			input.Content.Passages = append(input.Content.Passages,
				types.EvaluationDatasetPassageInput{PID: fmt.Sprintf("extra-%d", index), Content: "batch fixture"})
		}
		tenant := uint64(7)
		dataset := &types.EvaluationDataset{
			ID: uuid.NewString(), Name: "rollback",
			Scope: types.EvaluationDatasetScopeTenant, OwnerTenantID: &tenant,
		}
		version := &types.EvaluationDatasetVersion{
			ID: uuid.NewString(), DatasetID: dataset.ID, VersionNumber: 1, SchemaVersion: 1,
			ArtifactSHA256: strings.Repeat("a", 64), ContentSHA256: strings.Repeat("b", 64),
			Manifest: types.JSON(`{"source":"rollback-test"}`), PassageCount: 250, QuestionCount: 1, RelevanceCount: 1,
		}
		// Bypass the service to provoke a database JSON constraint after the
		// identity, version and first passage batch have been inserted.
		input.Content.Passages[249].Metadata = types.JSON(`{"broken":`)
		_, err := repository.NewEvaluationDatasetRepository(db).ImportDataset(ctx, dataset, version, input.Content)
		require.Error(t, err)
		assertDatasetImportCounts(t, db, 0, 0, 0, 0, 0)
	})
	input := datasetImportFixture()
	first, err := registry.ImportDataset(ctx, 7, input)
	require.NoError(t, err)
	assert.False(t, first.Replayed)
	assert.Equal(t, first.Version.ID, first.Dataset.CurrentVersionID)
	assertDatasetImportCounts(t, db, 1, 1, 2, 1, 1)
	retry, err := registry.ImportDataset(ctx, 7, input)
	require.NoError(t, err)
	assert.True(t, retry.Replayed)
	assert.Equal(t, first.Dataset.ID, retry.Dataset.ID)
	assert.Equal(t, first.Version.ID, retry.Version.ID)
	assert.Equal(t, types.CanonicalEvaluationDatasetContentSHA256(input.Content), first.Version.ContentSHA256)

	for _, field := range []string{"name", "description", "content", "order"} {
		t.Run("conflict/"+field, func(t *testing.T) {
			changed := datasetImportFixture()
			changed.RequestID = input.RequestID
			switch field {
			case "name":
				changed.Name += " different"
			case "description":
				changed.Description += " different"
			case "content":
				changed.Content.Passages[0].Content += " different"
			case "order":
				passages := changed.Content.Passages
				passages[0], passages[1] = passages[1], passages[0]
			}
			_, err := registry.ImportDataset(ctx, 7, changed)
			require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetVersionConflict)
			assertDatasetImportCounts(t, db, 1, 1, 2, 1, 1)
		})
	}
	_, err = registry.GetDataset(ctx, 8, first.Dataset.ID)
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetNotFound)
	_, err = registry.GetVersion(ctx, 8, first.Version.ID)
	require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetVersionNotFound)
	otherTenant, err := registry.ImportDataset(ctx, 8, input)
	require.NoError(t, err)
	assert.NotEqual(t, first.Dataset.ID, otherTenant.Dataset.ID)
	assertDatasetImportCounts(t, db, 2, 2, 4, 2, 2)

	t.Run("concurrent retries", func(t *testing.T) {
		concurrentInput := datasetImportFixture()
		results := make(chan *types.EvaluationDatasetImportResult, 2)
		errors := make(chan error, 2)
		start := make(chan struct{})
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				<-start
				result, err := registry.ImportDataset(ctx, 7, concurrentInput)
				results <- result
				errors <- err
			})
		}
		close(start)
		workers.Wait()
		require.NoError(t, <-errors)
		require.NoError(t, <-errors)
		a, b := <-results, <-results
		assert.Equal(t, a.Dataset.ID, b.Dataset.ID)
		assert.Equal(t, a.Version.ID, b.Version.ID)
		assert.NotEqual(t, a.Replayed, b.Replayed)
		assertDatasetImportCounts(t, db, 3, 3, 6, 3, 3)
	})

	changedContent := evaluationDatasetServiceFixture()
	changedContent.Passages[0].Content += " updated"
	next, err := registry.CreateVersion(ctx, 7, first.Dataset.ID, changedContent)
	require.NoError(t, err)
	retry, err = registry.ImportDataset(ctx, 7, input)
	require.NoError(t, err)
	assert.Equal(t, first.Version.ID, retry.Version.ID, "retry returns the immutable initial version")
	assert.Equal(t, next.ID, retry.Dataset.CurrentVersionID, "retry preserves the current version pointer")

	t.Run("actual public bundles", func(t *testing.T) {
		items, err := publicdata.List()
		require.NoError(t, err)
		require.Len(t, items, 2)
		for _, item := range items {
			bundle, err := publicdata.Get(item.ID)
			require.NoError(t, err)
			result, err := registry.ImportDataset(ctx, 7, &types.EvaluationDatasetImportInput{
				RequestID: uuid.NewString(), Name: bundle.Name,
				Description: bundle.Description, Content: bundle.Content,
			})
			require.NoError(t, err)
			stored, err := registry.GetVersionContent(ctx, 7, result.Version.ID)
			require.NoError(t, err)
			assert.Len(t, stored.Passages, item.Counts["passages"])
			assert.Len(t, stored.Questions, item.Counts["questions"])
			assert.Len(t, stored.Relevance, item.Counts["relevance"])
			zeroGrades := 0
			for _, edge := range stored.Relevance {
				if edge.Grade == 0 {
					zeroGrades++
				}
			}
			if item.ID == "squad2-dev" {
				assert.Equal(t, 8, zeroGrades)
			}
		}
	})
}

func assertDatasetImportCounts(t *testing.T, db *gorm.DB, expected ...int64) {
	t.Helper()
	for index, table := range []string{
		"evaluation_datasets", "evaluation_dataset_versions",
		"evaluation_dataset_passages", "evaluation_dataset_questions", "evaluation_dataset_relevance",
	} {
		var count int64
		require.NoError(t, db.Table(table).Count(&count).Error)
		assert.Equal(t, expected[index], count, fmt.Sprintf("row count for %s", table))
	}
}
