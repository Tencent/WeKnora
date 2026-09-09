package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/stretchr/testify/require"
)

func TestDatasetServiceLoadsRequestedDatasetAndSortsQuestions(t *testing.T) {
	rootDir := t.TempDir()
	writeDatasetFixture(t, rootDir, "support-zh", "support-zh", []TextInfo{
		{ID: 2, Text: "second question"},
		{ID: 1, Text: "first question"},
	}, []RelsInfo{{QID: 2, PID: 20}, {QID: 1, PID: 10}}, []QaInfo{{QID: 2, AID: 200}, {QID: 1, AID: 100}})

	service := &DatasetService{rootDir: rootDir}
	pairs, err := service.GetDatasetByID(context.Background(), "support-zh")

	require.NoError(t, err)
	require.Len(t, pairs, 2)
	require.Equal(t, 1, pairs[0].QID)
	require.Equal(t, "first question", pairs[0].Question)
	require.Equal(t, []int{10}, pairs[0].PIDs)
	require.Equal(t, "answer 100", pairs[0].Answer)
	require.Equal(t, 2, pairs[1].QID)
}

func TestDatasetServiceLoadsBuiltInDefaultDataset(t *testing.T) {
	service := &DatasetService{rootDir: filepath.Join("..", "..", "..", "dataset")}
	pairs, err := service.GetDatasetByID(context.Background(), "default")

	require.NoError(t, err)
	require.NotEmpty(t, pairs)
}

func TestDatasetServiceTreatsEmptyIDAsDefault(t *testing.T) {
	service := &DatasetService{rootDir: filepath.Join("..", "..", "..", "dataset")}
	pairs, err := service.GetDatasetByID(context.Background(), "  ")

	require.NoError(t, err)
	require.NotEmpty(t, pairs)
}

func TestDatasetServiceListsReadyAndInvalidDatasets(t *testing.T) {
	rootDir := t.TempDir()
	writeDatasetFixture(t, rootDir, "support-zh", "support-zh",
		[]TextInfo{{ID: 1, Text: "question"}},
		[]RelsInfo{{QID: 1, PID: 10}},
		[]QaInfo{{QID: 1, AID: 100}},
	)
	require.NoError(t, os.Mkdir(filepath.Join(rootDir, "broken"), 0o755))

	service := &DatasetService{rootDir: rootDir}
	datasets, err := service.ListDatasets(context.Background())

	require.NoError(t, err)
	require.Len(t, datasets, 2)
	require.Equal(t, "broken", datasets[0].ID)
	require.False(t, datasets[0].Available)
	require.Contains(t, datasets[0].ValidationError, "manifest.json")
	require.Equal(t, "support-zh", datasets[1].ID)
	require.True(t, datasets[1].Available)
	require.Empty(t, datasets[1].ValidationError)
}

func TestDatasetServiceRejectsPathTraversal(t *testing.T) {
	service := &DatasetService{rootDir: t.TempDir()}
	_, err := service.GetDatasetByID(context.Background(), "../samples")

	require.ErrorContains(t, err, "invalid dataset ID")
}

func TestDatasetServiceRequiresMatchingManifest(t *testing.T) {
	rootDir := t.TempDir()
	writeDatasetFixture(t, rootDir, "support-zh", "another-dataset",
		[]TextInfo{{ID: 1, Text: "question"}},
		[]RelsInfo{{QID: 1, PID: 10}},
		[]QaInfo{{QID: 1, AID: 100}},
	)

	service := &DatasetService{rootDir: rootDir}
	_, err := service.GetDatasetByID(context.Background(), "support-zh")

	require.ErrorContains(t, err, `manifest ID "another-dataset" does not match requested ID "support-zh"`)
}

func TestDatasetServiceRejectsDanglingRelationBeforeEvaluation(t *testing.T) {
	rootDir := t.TempDir()
	writeDatasetFixture(t, rootDir, "support-zh", "support-zh",
		[]TextInfo{{ID: 1, Text: "question"}},
		[]RelsInfo{{QID: 1, PID: 999}},
		[]QaInfo{{QID: 1, AID: 100}},
	)

	service := &DatasetService{rootDir: rootDir}
	_, err := service.GetDatasetByID(context.Background(), "support-zh")

	require.ErrorContains(t, err, "pid 999 is missing from corpus.parquet")
}

func TestLoadDatasetFromDirReturnsErrorInsteadOfPanicking(t *testing.T) {
	_, err := loadDatasetFromDir(t.TempDir())
	require.ErrorContains(t, err, "load queries.parquet")
}

func writeDatasetFixture(
	t *testing.T,
	rootDir, datasetID, manifestID string,
	queries []TextInfo,
	qrels []RelsInfo,
	qas []QaInfo,
) {
	t.Helper()
	datasetDir := filepath.Join(rootDir, datasetID)
	require.NoError(t, os.MkdirAll(datasetDir, 0o755))
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "id": %q,
  "name": "Dataset fixture",
  "language": "en",
  "scenario": "test"
}`, manifestID)
	require.NoError(t, os.WriteFile(filepath.Join(datasetDir, "manifest.json"), []byte(manifest), 0o644))
	require.NoError(t, parquet.WriteFile(filepath.Join(datasetDir, "queries.parquet"), queries))
	require.NoError(t, parquet.WriteFile(filepath.Join(datasetDir, "corpus.parquet"), []TextInfo{
		{ID: 10, Text: "passage 10"}, {ID: 20, Text: "passage 20"},
	}))
	require.NoError(t, parquet.WriteFile(filepath.Join(datasetDir, "answers.parquet"), []TextInfo{
		{ID: 100, Text: "answer 100"}, {ID: 200, Text: "answer 200"},
	}))
	require.NoError(t, parquet.WriteFile(filepath.Join(datasetDir, "qrels.parquet"), qrels))
	require.NoError(t, parquet.WriteFile(filepath.Join(datasetDir, "qas.parquet"), qas))
}
