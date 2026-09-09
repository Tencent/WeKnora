package service

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestHookMetricBuildsOrderedSecretFreeSampleEvidence(t *testing.T) {
	hook := NewHookMetric(1)
	hook.recordInit(0)
	hook.recordQaPair(0, &types.QAPair{
		QID: 7, Question: "private question", PIDs: []int{11},
		Passages: []string{"grounded passage"}, AID: 13, Answer: "reference answer",
	})
	hook.recordSearchResult(0, []*types.SearchResult{{
		ID: "chunk-1", KnowledgeID: "knowledge-1", ChunkIndex: 3,
		Content: "grounded passage", Score: 0.92,
	}})
	hook.recordChatResponse(0, &types.ChatResponse{Content: "generated answer"})
	hook.recordFinish(0)

	result := hook.MetricResult()
	require.Len(t, result.Samples, 1)
	sample := result.Samples[0]
	require.Equal(t, 7, sample.QuestionID)
	require.Equal(t, 13, sample.AnswerID)
	require.Equal(t, 11, *sample.Retrieved[0].DatasetPassageID)
	require.Equal(t, 1, sample.Retrieved[0].Rank)
	require.Equal(t, sha256Text("generated answer"), sample.ResponseSHA256)
	require.Equal(t, len([]byte("generated answer")), sample.ResponseBytes)

	encoded, err := json.Marshal(sample)
	require.NoError(t, err)
	for _, plaintext := range []string{"private question", "grounded passage", "reference answer", "generated answer"} {
		require.NotContains(t, string(encoded), plaintext)
	}
}

func TestEvidenceReportHashIsDeterministicAndExcludesErrorsAndParams(t *testing.T) {
	detail := &types.EvaluationDetail{
		Task:      &types.EvaluationTask{ID: "task-1", TenantID: 9, DatasetID: "fixed", StartTime: time.Unix(100, 0).UTC()},
		Params:    &types.ChatManage{PipelineRequest: types.PipelineRequest{Query: "secret query"}},
		RunConfig: &types.EvaluationRunConfig{CodeVersion: "commit-abc"},
		Metric:    &types.MetricResult{Samples: []types.EvaluationSampleEvidence{{QuestionID: 1}}},
		ModelCalls: []types.EvaluationModelCall{{
			ID: "call-1", ModelID: "model-1", Error: "provider secret error", CreatedAt: time.Unix(101, 0).UTC(),
		}},
	}
	first := buildEvaluationEvidenceReport(detail)
	second := buildEvaluationEvidenceReport(detail)
	require.NoError(t, sealEvaluationEvidenceReport(first))
	require.NoError(t, sealEvaluationEvidenceReport(second))
	require.Equal(t, first.ReportSHA256, second.ReportSHA256)

	want := first.ReportSHA256
	first.ReportSHA256 = ""
	encoded, err := json.Marshal(first)
	require.NoError(t, err)
	require.Equal(t, want, fmt.Sprintf("sha256:%x", sha256.Sum256(encoded)))
	require.False(t, strings.Contains(string(encoded), "secret query"))
	require.False(t, strings.Contains(string(encoded), "provider secret error"))
}

func TestPipelineSnapshotHashesPromptBodiesAndFingerprintsControls(t *testing.T) {
	pipeline := types.PipelineRequest{
		ChatModelID: "chat-a", EmbeddingTopK: 8,
		SummaryConfig: types.SummaryConfig{Prompt: "private system prompt", ContextTemplate: "private context"},
	}
	snapshot := snapshotEvaluationPipeline(pipeline)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private system prompt")
	require.NotContains(t, string(encoded), "private context")
	require.Equal(t, sha256Text("private system prompt"), snapshot.Summary.PromptSHA256)

	config := &types.EvaluationRunConfig{
		SchemaVersion: 1, DatasetFingerprint: "sha256:data", DatasetSamples: 4,
		Pipeline: snapshot, Models: []types.EvaluationModelSnapshot{{Role: "chat", ID: "chat-a"}},
		CodeVersion: "commit-abc",
	}
	fullA := fingerprintEvaluationConfig(config, false)
	controlledA := fingerprintEvaluationConfig(config, true)
	config.Pipeline.ChatModelID = "chat-b"
	config.Models[0].ID = "chat-b"
	require.NotEqual(t, fullA, fingerprintEvaluationConfig(config, false))
	require.Equal(t, controlledA, fingerprintEvaluationConfig(config, true))
}
