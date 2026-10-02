package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func waitGenerationJob(t *testing.T, store *projectStore, projectID, jobID string) *GenerationJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := store.getGenerationJob(projectID, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "succeeded" || job.Status == "failed" || job.Status == "canceled" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("generation job did not finish")
	return nil
}

func TestGenerationP0WorkflowAndCache(t *testing.T) {
	var calls atomic.Int32
	originalClient := generationHTTPClient
	generationHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("unexpected generation request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-generation-test" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		calls.Add(1)
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		content := `{"candidates":[{"question":"恢复设备出厂设置需要执行什么操作？","answer":"长按重置键十秒，指示灯闪烁后设备恢复出厂设置。","answer_key_points":["长按重置键十秒","等待指示灯闪烁"],"category":"操作指导类","difficulty":"easy","tags":["重置"],"answerable":true,"evidence_quotes":["长按重置键十秒","指示灯闪烁后设备恢复出厂设置"]}]}`
		if messages, ok := request["messages"].([]any); ok && len(messages) > 0 {
			message, _ := messages[0].(map[string]any)
			if strings.Contains(message["content"].(string), "回复1") {
				if request["max_tokens"] != float64(1) {
					t.Errorf("test max_tokens = %#v", request["max_tokens"])
				}
				// Some reasoning models return a successful response with empty content
				// when the probe is limited to one output token. Connectivity testing
				// must accept that response, while real generation still requires text.
				content = ""
			}
		}
		data, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 80, "total_tokens": 180},
		})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	defer func() { generationHTTPClient = originalClient }()

	store, err := newProjectStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := validProject()
	if err := store.importProject(project); err != nil {
		t.Fatal(err)
	}
	profile, err := store.saveGenerationConnection("generator-test", &GenerationConnection{
		ID: "generator-test", Name: "测试生成器", BaseURL: "https://generator.test/v1", APIKey: " sk-generation-test ", Model: "mock-model",
		TimeoutSeconds: 10, QuestionsPerPassage: 1, MaxOutputTokens: 20000, MaxTotalTokens: 2000000, InputPricePerMillion: 1, OutputPricePerMillion: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.APIKey != "sk-generation-test" {
		t.Fatalf("trimmed API key = %q", profile.APIKey)
	}
	if profile.TestPrompt != "回复1" {
		t.Fatalf("default test prompt = %q", profile.TestPrompt)
	}
	info, err := os.Stat(filepath.Join(store.generationConnections, profile.ID+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("profile permissions = %v, err = %v", info.Mode().Perm(), err)
	}
	if err := store.testGenerationConnection(profile.ID); err != nil {
		t.Fatal(err)
	}

	job, err := store.startGenerationJob(project.ID, generationStartRequest{ConnectionID: profile.ID, PassageIDs: []int64{3}, QuestionsPerPassage: 1})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitGenerationJob(t, store, project.ID, job.ID)
	if completed.Status != "succeeded" || completed.CompletedPassages != 1 || len(completed.Candidates) != 1 || completed.Usage.TotalTokens != 180 {
		t.Fatalf("completed job = %#v", completed)
	}
	candidate := completed.Candidates[0]
	if candidate.ReviewStatus != "pending" || len(candidate.Issues) != 0 || candidate.PassageIDs[0] != 3 {
		t.Fatalf("candidate = %#v", candidate)
	}
	jobData, err := os.ReadFile(filepath.Join(store.generationJobs, project.ID, job.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(jobData), profile.APIKey) {
		t.Fatal("generation job persisted API key")
	}

	second, err := store.startGenerationJob(project.ID, generationStartRequest{ConnectionID: profile.ID, PassageIDs: []int64{3}, QuestionsPerPassage: 1})
	if err != nil {
		t.Fatal(err)
	}
	second = waitGenerationJob(t, store, project.ID, second.ID)
	if second.CacheHits != 1 || second.Usage.TotalTokens != 0 || calls.Load() != 2 {
		t.Fatalf("cache job = %#v, model calls = %d", second, calls.Load())
	}

	reviewed, updated, err := store.reviewGenerationCandidates(project.ID, job.ID, generationReviewRequest{Items: []generationReviewItem{{
		CandidateID: candidate.ID, Action: "approve", Question: candidate.Question, Answer: candidate.Answer,
		AnswerKeyPoints: candidate.AnswerKeyPoints, Category: candidate.Category, Difficulty: candidate.Difficulty,
		Tags: candidate.Tags, EvidenceQuotes: candidate.EvidenceQuotes,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Candidates[0].ReviewStatus != "approved" || len(updated.Questions) != 3 {
		t.Fatalf("reviewed = %#v, questions = %#v", reviewed.Candidates[0], updated.Questions)
	}
	approved := updated.Questions[2]
	if approved.ReviewState != "approved" || approved.AnnotationSource != "ai:mock-model:"+generationPromptVersion || len(approved.RelevantPassageIDs) != 1 || approved.RelevantPassageIDs[0] != 3 {
		t.Fatalf("approved question = %#v", approved)
	}
	if report := validateProject(updated); !report.Valid {
		t.Fatalf("generated project should remain exportable: %#v", report.Issues)
	}
	rows, err := buildExportRows(updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Queries) != 3 || len(rows.Answers) != 3 || len(rows.Qrels) != 3 || len(rows.QAs) != 3 {
		t.Fatalf("generated export rows = %#v", rows)
	}
	zipPath, _, err := store.exportWeKnora(updated)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(zipPath); err != nil || info.Size() == 0 {
		t.Fatalf("generated ZIP = %q, info = %#v, err = %v", zipPath, info, err)
	}
}

func TestGenerationCandidateRejectsMissingEvidence(t *testing.T) {
	passage := Passage{ID: 1, Text: "系统支持创建和编辑产品模型。"}
	answerable := true
	candidate := buildGenerationCandidate(generatedCandidatePayload{
		Question: "如何删除产品模型？", Answer: "点击删除。", AnswerKeyPoints: []string{"点击删除"},
		Category: "操作指导类", Difficulty: "easy", Answerable: &answerable, EvidenceQuotes: []string{"点击删除"},
	}, passage, nil, nil)
	found := false
	for _, issue := range candidate.Issues {
		if issue.Code == "evidence_not_found" && issue.Severity == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("candidate issues = %#v", candidate.Issues)
	}
}
