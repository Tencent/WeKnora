package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This integration uses the checked-in request body, real SQLite migrations,
// repository and registry. No knowledge service or model provider is installed.
func TestSyntheticCampusRegistryExecutionIntegration(t *testing.T) {
	corpusDir := filepath.Join("..", "..", "..", "dataset", "synthetic-campus", "v1")
	readJSON := func(name string, target any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(corpusDir, name))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), target))
	}
	var input types.EvaluationDatasetVersionInput
	readJSON("registry-input.json", &input)
	var sourceQuestions []struct {
		QID         string   `json:"question_id"`
		Question    string   `json:"question"`
		Answer      string   `json:"reference_answer"`
		Answerable  bool     `json:"answerable"`
		Evidence    []string `json:"evidence_passage_ids"`
		LabelOrigin string   `json:"label_origin"`
		Review      string   `json:"human_review_status"`
	}
	readJSON("questions.json", &sourceQuestions)
	var sourcePassages map[string]struct {
		DocumentID string `json:"document_id"`
		Heading    string `json:"heading"`
		Content    string `json:"content"`
	}
	readJSON("passages.json", &sourcePassages)
	var manifest struct {
		Source    string `json:"source"`
		Documents []struct {
			ID         string   `json:"document_id"`
			File       string   `json:"file"`
			SHA256     string   `json:"sha256"`
			PassageIDs []string `json:"passage_ids"`
		} `json:"documents"`
	}
	readJSON("manifest.json", &manifest)
	require.Equal(t, "ai_synthetic", manifest.Source)
	require.Len(t, manifest.Documents, 6)
	require.Len(t, input.Passages, 24)
	require.Len(t, input.Questions, 20)
	require.Len(t, input.Relevance, 97)
	require.Len(t, sourceQuestions, 20)
	require.Len(t, sourcePassages, 24)

	expectedMetadata := make(map[string]map[string]string)
	for _, document := range manifest.Documents {
		data, err := os.ReadFile(filepath.Join(corpusDir, filepath.FromSlash(document.File)))
		require.NoError(t, err)
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		digest := sha256.Sum256(data)
		require.Equal(t, document.SHA256, hex.EncodeToString(digest[:]), document.File)
		for _, pid := range document.PassageIDs {
			passage, ok := sourcePassages[pid]
			require.True(t, ok, pid)
			require.Equal(t, document.ID, passage.DocumentID)
			require.Contains(t, string(data), passage.Content)
			require.NotContains(t, expectedMetadata, pid)
			expectedMetadata[pid] = map[string]string{
				"source": "ai_synthetic", "document_id": document.ID,
				"heading": passage.Heading, "source_file": document.File,
				"source_sha256": document.SHA256, "human_review_status": "pending",
			}
		}
	}
	require.Len(t, expectedMetadata, 24)

	db := setupEvaluationDatasetServiceTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	registry := newEvaluationDatasetRegistryService(db)
	ctx := context.Background()
	const tenantID uint64 = 7
	dataset, err := registry.CreateDataset(ctx, tenantID, "AI synthetic campus v1", "Human review pending")
	require.NoError(t, err)
	version, err := registry.CreateVersion(ctx, tenantID, dataset.ID, &input)
	require.NoError(t, err)
	require.Equal(t, 1, version.VersionNumber)
	require.Equal(t, 24, version.PassageCount)
	require.Equal(t, 20, version.QuestionCount)
	require.Equal(t, 97, version.RelevanceCount)

	stored, err := registry.GetVersionContent(ctx, tenantID, version.ID)
	require.NoError(t, err)
	require.Equal(t, types.CanonicalEvaluationDatasetContentSHA256(&input), stored.Version.ContentSHA256)
	for index, passage := range stored.Passages {
		require.Equal(t, input.Passages[index].PID, passage.PID)
		require.Equal(t, sourcePassages[passage.PID].Content, passage.Content)
		var metadata map[string]string
		require.NoError(t, json.Unmarshal(passage.Metadata, &metadata))
		require.Equal(t, expectedMetadata[passage.PID], metadata)
	}
	for index, question := range stored.Questions {
		require.Equal(t, index, question.SampleIndex)
		require.Equal(t, sourceQuestions[index].QID, question.QID)
	}
	storedZeroGrades := 0
	for _, edge := range stored.Relevance {
		if edge.Grade == 0 {
			storedZeroGrades++
		}
	}
	require.Equal(t, 72, storedZeroGrades, "explicit grade 0 must survive ORM insertion")
	require.Equal(t, version.ContentSHA256,
		types.CanonicalEvaluationDatasetContentSHA256(evaluationDatasetVersionInput(stored)),
		"frozen hash must match content read from the real registry")

	detail := &types.EvaluationDetail{
		Task: &types.EvaluationTask{TenantID: tenantID, DatasetID: dataset.ID},
		Experiment: &types.EvaluationExperimentSnapshot{Dataset: types.EvaluationDatasetSnapshot{
			DatasetID: dataset.ID, DatasetVersionID: version.ID, ContentSHA256: version.ContentSHA256,
		}},
	}
	execution := &EvaluationService{datasetRegistry: registry}
	pairs, err := execution.loadEvaluationDataset(ctx, detail)
	require.NoError(t, err)
	require.Len(t, pairs, 20)
	corpus := getPassageList(pairs)
	require.Len(t, corpus, 24)
	pidIndex := make(map[string]int)
	for index, passage := range input.Passages {
		pidIndex[passage.PID] = index
		require.Equal(t, passage.Content, corpus[index])
	}
	answerable, unanswerable, gradeZeroEdges, positiveEdges := 0, 0, 0, 0
	for index, question := range sourceQuestions {
		pair := pairs[index]
		require.Equal(t, index, pair.QID)
		require.Equal(t, question.QID, pair.DatasetQID)
		require.Equal(t, question.Question, pair.Question)
		require.Equal(t, question.Answer, pair.Answer)
		require.Equal(t, "ai_authored_draft", question.LabelOrigin)
		require.Equal(t, "pending", question.Review)
		require.True(t, pair.RetrievalLabelsAvailable, "grade 0 is observed relevance, not missing labels")
		require.Equal(t, corpus, pair.Corpus)
		expectedGrades := make(map[int]int)
		if question.Answerable {
			answerable++
			expectedPIDs := make([]int, 0, len(question.Evidence))
			for _, pid := range question.Evidence {
				index, ok := pidIndex[pid]
				require.True(t, ok, pid)
				expectedPIDs = append(expectedPIDs, index)
				expectedGrades[index] = 1
				positiveEdges++
			}
			assert.ElementsMatch(t, expectedPIDs, pair.PIDs, question.QID)
			require.Len(t, pair.Passages, len(pair.PIDs))
			for pos, pid := range pair.PIDs {
				require.Equal(t, corpus[pid], pair.Passages[pos])
			}
		} else {
			unanswerable++
			for pid := range corpus {
				expectedGrades[pid] = 0
				gradeZeroEdges++
			}
			assert.Empty(t, pair.PIDs)
			assert.Empty(t, pair.Passages)
		}
		require.Equal(t, expectedGrades, pair.PIDGrades, question.QID)
	}
	require.Equal(t, 17, answerable)
	require.Equal(t, 3, unanswerable)
	require.Equal(t, 25, positiveEdges)
	require.Equal(t, 72, gradeZeroEdges)
	t.Logf("SQLite registry → frozen execution: documents=6 passages=24 questions=20 "+
		"answerable=%d unanswerable=%d relevance=%d content_sha256=%s artifact_sha256=%s",
		answerable, unanswerable, positiveEdges+gradeZeroEdges, version.ContentSHA256, version.ArtifactSHA256)

	t.Run("new current version preserves the frozen question sequence and answers", func(t *testing.T) {
		var nextInput types.EvaluationDatasetVersionInput
		readJSON("registry-input.json", &nextInput)
		nextInput.Questions[0].Answer += " (second version)"
		nextVersion, err := registry.CreateVersion(ctx, tenantID, dataset.ID, &nextInput)
		require.NoError(t, err)
		require.Equal(t, 2, nextVersion.VersionNumber)
		require.NotEqual(t, version.ContentSHA256, nextVersion.ContentSHA256)
		current, err := registry.GetDataset(ctx, tenantID, dataset.ID)
		require.NoError(t, err)
		require.Equal(t, nextVersion.ID, current.CurrentVersionID)
		frozen, err := execution.loadEvaluationDataset(ctx, detail)
		require.NoError(t, err)
		require.Equal(t, pairs, frozen)
	})
	t.Run("cross tenant loading is rejected", func(t *testing.T) {
		foreign := *detail
		foreign.Task = &types.EvaluationTask{TenantID: tenantID + 1, DatasetID: dataset.ID}
		_, err := execution.loadEvaluationDataset(ctx, &foreign)
		require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetVersionNotFound)
	})
	t.Run("tampered frozen hash is rejected", func(t *testing.T) {
		invalid := *detail
		snapshot := *detail.Experiment
		snapshot.Dataset.ContentSHA256 = strings.Repeat("0", 64)
		invalid.Experiment = &snapshot
		_, err := execution.loadEvaluationDataset(ctx, &invalid)
		require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetInvalid)
		require.Contains(t, err.Error(), "frozen identity mismatch")
	})
	t.Run("persisted passage tampering is rejected by frozen content hash", func(t *testing.T) {
		update := db.Model(&types.EvaluationDatasetPassage{}).
			Where("dataset_version_id = ? AND pid = ?", version.ID, input.Passages[0].PID).
			Update("content", "tampered content inside the isolated test database")
		require.NoError(t, update.Error)
		require.EqualValues(t, 1, update.RowsAffected)
		loaded, err := execution.loadEvaluationDataset(ctx, detail)
		require.ErrorIs(t, err, interfaces.ErrEvaluationDatasetInvalid)
		require.Contains(t, err.Error(), "content SHA-256 mismatch")
		require.Nil(t, loaded)
	})
}
