package trainingorchestration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

type staticInputCollector struct{ input InputPackage }

func (c staticInputCollector) Collect(context.Context) (InputPackage, error) { return c.input, nil }

type staticCatalogCollector struct {
	snapshot CatalogSnapshot
	calls    int
}

func (c *staticCatalogCollector) CollectCatalog(context.Context) (CatalogSnapshot, error) {
	c.calls++
	return c.snapshot, nil
}

type sequenceInputCollector struct {
	inputs []InputPackage
	index  int
}

func (c *sequenceInputCollector) Collect(context.Context) (InputPackage, error) {
	if len(c.inputs) == 0 {
		return InputPackage{}, nil
	}
	index := c.index
	if index >= len(c.inputs) {
		index = len(c.inputs) - 1
	}
	c.index++
	return c.inputs[index], nil
}

type staticProjectionGenerator struct{ doc ProjectionDocument }

func (g staticProjectionGenerator) Generate(context.Context, InputPackage) (ProjectionDocument, error) {
	return g.doc, nil
}

type staticStageFourRunner struct {
	doc   ProjectionDocument
	calls int
}

func (r *staticStageFourRunner) Run(context.Context, CatalogSnapshot) (ProjectionDocument, error) {
	r.calls++
	return r.doc, nil
}

type memoryProjectionWiki struct {
	mu     sync.Mutex
	page   weknora.WikiPage
	writes int
}

func (w *memoryProjectionWiki) EnsurePage(_ context.Context, _ string, input weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.page.ID == "" {
		w.page = weknora.WikiPage{ID: "training-wiki-1", Slug: input.Slug, Content: input.Content, Version: 1}
	}
	page := w.page
	return &page, nil
}
func (w *memoryProjectionWiki) GetPageByID(context.Context, string, string) (*weknora.WikiPage, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.page.ID == "" {
		return nil, nil
	}
	page := w.page
	return &page, nil
}

func TestServicePublishesCurrentVersionAndReusesFingerprint(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modelOutput := validModelOutput(input)
	raw, _ := json.Marshal(modelOutput)
	llm := &fakeCompletionClient{output: string(raw)}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "service.db")+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TrainingOrchestrationJob{}, &model.TrainingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	wiki := &memoryProjectionWiki{}
	service := &Service{DB: db, Collector: staticInputCollector{input}, Generator: &Generator{LLM: llm, PromptVersion: "training-v1"}, Wiki: wiki, KnowledgeBaseID: testKBID, OwnerScopeID: input.OwnerScopeID, Model: llm.Model(), PromptVersion: "training-v1", RunTimeout: time.Second}

	first, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitForTrainingJob(t, service, first.ID)
	if first.Status != JobSucceeded || first.Stage != StagePublishing || first.Reused || first.ResultWikiPageID == "" || llm.calls != 1 {
		t.Fatalf("unexpected first job: %#v, calls=%d", first, llm.calls)
	}
	doc, current, err := service.GetCurrent(t.Context())
	if err != nil || doc == nil || current == nil || current.JobID != first.ID {
		t.Fatalf("unexpected current result: doc=%#v current=%#v err=%v", doc, current, err)
	}

	second, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second = waitForTrainingJob(t, service, second.ID)
	if second.Status != JobSucceeded || !second.Reused || llm.calls != 1 || wiki.writes != 1 {
		t.Fatalf("fingerprint was not reused: %#v calls=%d writes=%d", second, llm.calls, wiki.writes)
	}

	doc.TrainingPathProjection.TopicClusters[0].KnowledgeObjectIDs = []string{"tampered-object"}
	tampered, _ := json.Marshal(doc)
	wiki.mu.Lock()
	wiki.page.Content = string(tampered)
	wiki.mu.Unlock()
	currentDoc, _, err := service.GetCurrent(t.Context())
	if err != nil || len(currentDoc.TrainingPathProjection.TopicClusters[0].KnowledgeObjectIDs) != 0 {
		t.Fatalf("knowledge metadata blocked or leaked from current Wiki: doc=%#v err=%v", currentDoc, err)
	}
}

func TestServiceStopsBeforeModelWhenInputChangesDuringPreflight(t *testing.T) {
	input, _ := validServiceInputAndDocument(t)
	db := newTrainingServiceDB(t)
	changed := input
	changed.QualifiedVideos = append([]VideoTopicProfile(nil), input.QualifiedVideos...)
	changed.QualifiedVideos[0].TranscriptGeneration = "new-generation"
	collector := &sequenceInputCollector{inputs: []InputPackage{input, changed}}
	llm := &fakeCompletionClient{output: "should-not-be-called"}
	service := &Service{
		DB: db, Collector: collector, Generator: &Generator{LLM: llm, PromptVersion: "training-v1"},
		Wiki: &memoryProjectionWiki{}, KnowledgeBaseID: testKBID, OwnerScopeID: input.OwnerScopeID,
		Model: llm.Model(), PromptVersion: "training-v1", RunTimeout: time.Second,
	}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobFailed || job.ErrorCode != "source_not_stable" {
		t.Fatalf("unexpected unstable-input job: %#v", job)
	}
	if llm.calls != 0 {
		t.Fatalf("model was called despite unstable preflight: %d", llm.calls)
	}
}

func TestServiceUsesStageFourRunnerWhenConfigured(t *testing.T) {
	video := validCatalogVideo("video-1")
	video.Title = "视频一"
	snapshot := CatalogSnapshot{ContractVersion: PlanningContractVersion, OwnerScopeID: "scope-1", Videos: []CatalogVideo{video}}
	plan := validAcceptedPlan(snapshot)
	plan.TopicClusters[0].Summary = "主题摘要"
	modelName, promptVersion := "planner-model", "planning-v1"
	fingerprint, err := CatalogFingerprint(snapshot, modelName, promptVersion)
	if err != nil {
		t.Fatal(err)
	}
	plan.SourceFingerprint = fingerprint
	material := validStageFourMaterial(plan.TopicClusters[0].ClusterKey)
	draft := validStageFourDraft(plan.TopicClusters[0].ClusterKey, material.Evidence[0])
	doc, err := (&ProjectionAssembler{Fingerprint: stageFourFingerprint(modelName, promptVersion)}).Assemble(StageFourAssemblyInput{
		InitialCatalog: snapshot, LatestCatalog: snapshot, Plan: plan,
		Materials: []ClusterMaterial{material}, Drafts: []ClusterGenerationDraft{draft},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc.TrainingPathProjection.RetrievalDegraded = true
	doc.TrainingPathProjection.RetrievalDegradationReason = "search_unavailable"
	catalog := &staticCatalogCollector{snapshot: snapshot}
	runner := &staticStageFourRunner{doc: doc}
	wiki := &memoryProjectionWiki{}
	service := &Service{
		DB: newTrainingServiceDB(t), CatalogCollector: catalog, StageFour: runner,
		Wiki: wiki, KnowledgeBaseID: testKBID, OwnerScopeID: snapshot.OwnerScopeID,
		Model: modelName, PromptVersion: promptVersion, RunTimeout: time.Second,
	}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobSucceeded || job.WarningCode != WarningEvidenceRetrievalDegraded || job.WarningMessage == "" || runner.calls != 1 || catalog.calls != 2 || wiki.writes != 1 {
		t.Fatalf("stage-four runner was not used: job=%#v runner_calls=%d catalog_calls=%d wiki_writes=%d", job, runner.calls, catalog.calls, wiki.writes)
	}
}

func TestServiceIgnoresGeneratorKnowledgeBeforePublishing(t *testing.T) {
	input, doc := validServiceInputAndDocument(t)
	doc.TrainingPathProjection.TopicClusters[0].KnowledgeObjectIDs = []string{"injected-object"}
	db := newTrainingServiceDB(t)
	wiki := &memoryProjectionWiki{}
	service := &Service{DB: db, Collector: staticInputCollector{input}, Generator: staticProjectionGenerator{doc}, Wiki: wiki, KnowledgeBaseID: testKBID, OwnerScopeID: input.OwnerScopeID, Model: "real-model", PromptVersion: "training-v1", RunTimeout: time.Second}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobSucceeded || wiki.writes != 1 {
		t.Fatalf("knowledge metadata blocked publishing: job=%#v writes=%d", job, wiki.writes)
	}
}

func TestServiceIgnoresKnowledgeMetadataInExistingWiki(t *testing.T) {
	input, doc := validServiceInputAndDocument(t)
	validContent, _ := json.Marshal(doc)
	var polluted ProjectionDocument
	if err := json.Unmarshal(validContent, &polluted); err != nil {
		t.Fatal(err)
	}
	polluted.TrainingPathProjection.TopicClusters[0].KnowledgeObjectIDs = []string{"polluted-object"}
	pollutedContent, _ := json.Marshal(polluted)
	wiki := &memoryProjectionWiki{page: weknora.WikiPage{ID: "training-wiki-1", Content: string(pollutedContent), Version: 1}}
	db := newTrainingServiceDB(t)
	service := &Service{DB: db, Collector: staticInputCollector{input}, Generator: staticProjectionGenerator{doc}, Wiki: wiki, KnowledgeBaseID: testKBID, OwnerScopeID: input.OwnerScopeID, Model: "real-model", PromptVersion: "training-v1", RunTimeout: time.Second}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobSucceeded {
		t.Fatalf("knowledge metadata blocked existing Wiki reuse: %#v", job)
	}
	var currentCount int64
	if err := db.Model(&model.TrainingOrchestrationCurrent{}).Count(&currentCount).Error; err != nil || currentCount != 1 {
		t.Fatalf("existing Wiki was not switched current: count=%d err=%v", currentCount, err)
	}
}

func TestServiceDoesNotSwitchCurrentWhenSuccessUpdateFails(t *testing.T) {
	input, doc := validServiceInputAndDocument(t)
	db := newTrainingServiceDB(t)
	if err := db.Exec(`CREATE TRIGGER reject_training_success BEFORE UPDATE ON training_orchestration_jobs WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'reject success'); END`).Error; err != nil {
		t.Fatal(err)
	}
	service := &Service{DB: db, Collector: staticInputCollector{input}, Generator: staticProjectionGenerator{doc}, Wiki: &memoryProjectionWiki{}, KnowledgeBaseID: testKBID, OwnerScopeID: input.OwnerScopeID, Model: "real-model", PromptVersion: "training-v1", RunTimeout: time.Second}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobFailed || job.ErrorCode != "current_switch_failed" {
		t.Fatalf("unexpected failed publish job: %#v", job)
	}
	var currentCount int64
	if err := db.Model(&model.TrainingOrchestrationCurrent{}).Count(&currentCount).Error; err != nil {
		t.Fatal(err)
	}
	if currentCount != 0 {
		t.Fatalf("current result switched despite failed success update: %d", currentCount)
	}
}

func TestServicePersistsSpecificModelOutputFailureCode(t *testing.T) {
	input, _ := validServiceInputAndDocument(t)
	db := newTrainingServiceDB(t)
	wiki := &memoryProjectionWiki{}
	llm := &fakeCompletionClient{output: "<think>sensitive reasoning must not be persisted"}
	service := &Service{DB: db, Collector: staticInputCollector{input}, Generator: &Generator{LLM: llm, PromptVersion: "training-v1"}, Wiki: wiki, KnowledgeBaseID: testKBID, OwnerScopeID: input.OwnerScopeID, Model: llm.Model(), PromptVersion: "training-v1", RunTimeout: time.Second}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobFailed || job.ErrorCode != "model_output_truncated" {
		t.Fatalf("unexpected failed job: %#v", job)
	}
	if strings.Contains(job.ErrorMessage, "sensitive reasoning") || !strings.Contains(job.ErrorMessage, "output_bytes=") {
		t.Fatalf("unsafe or incomplete diagnostic: %q", job.ErrorMessage)
	}
}

func TestServicePersistsSafeModelOutputReason(t *testing.T) {
	input, _ := validServiceInputAndDocument(t)
	db := newTrainingServiceDB(t)
	service := &Service{
		DB: db, Collector: staticInputCollector{input},
		Generator: &Generator{
			LLM:           &fakeCompletionClient{output: `{"topic_clusters":[{"unknown":true}]}`},
			PromptVersion: "training-v1",
		},
		Wiki: &memoryProjectionWiki{}, KnowledgeBaseID: testKBID,
		OwnerScopeID: input.OwnerScopeID, Model: "real-model",
		PromptVersion: "training-v1", RunTimeout: time.Second,
	}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobFailed || job.ErrorCode != "model_output_invalid" {
		t.Fatalf("unexpected failed job: %#v", job)
	}
	if !strings.Contains(job.ErrorMessage, "reason=json_unknown_field") {
		t.Fatalf("safe model output reason was not persisted: %q", job.ErrorMessage)
	}
	if strings.Contains(job.ErrorMessage, "true") {
		t.Fatalf("raw provider/model output leaked into task message: %q", job.ErrorMessage)
	}
}

func TestServicePersistsSafeEvidenceReferenceReason(t *testing.T) {
	input, _ := validServiceInputAndDocument(t)
	modelOutput := validModelOutput(input)
	modelOutput.TopicClusters[0].Path.Stages[0].Units[0].EvidenceRefs[0].EvidenceID = "invented-evidence"
	raw, err := json.Marshal(modelOutput)
	if err != nil {
		t.Fatal(err)
	}
	db := newTrainingServiceDB(t)
	service := &Service{
		DB: db, Collector: staticInputCollector{input},
		Generator: &Generator{
			LLM:           &fakeCompletionClient{output: string(raw)},
			PromptVersion: "training-v1",
		},
		Wiki: &memoryProjectionWiki{}, KnowledgeBaseID: testKBID,
		OwnerScopeID: input.OwnerScopeID, Model: "real-model",
		PromptVersion: "training-v1", RunTimeout: time.Second,
	}

	job, err := service.Start(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitForTrainingJob(t, service, job.ID)
	if job.Status != JobFailed || job.ErrorCode != "model_output_invalid" {
		t.Fatalf("unexpected failed job: %#v", job)
	}
	if !strings.Contains(job.ErrorMessage, "reason=evidence_reference_outside_whitelist") {
		t.Fatalf("safe evidence reference reason was not persisted: %q", job.ErrorMessage)
	}
	if strings.Contains(job.ErrorMessage, "invented-evidence") {
		t.Fatalf("raw evidence identifier leaked into task message: %q", job.ErrorMessage)
	}
}

func TestProjectionSourceRefsIncludeNormalizedTranscript(t *testing.T) {
	input := InputPackage{QualifiedVideos: []VideoTopicProfile{{
		Transcript:       &TranscriptInput{KnowledgeID: "normalized-transcript-1"},
		KnowledgeIndex:   WikiReference{WikiPageID: "ignored-index"},
		KnowledgeSignals: []KnowledgeSignal{{WikiPageID: "ignored-object"}},
	}}}

	refs := projectionSourceRefs(input)
	if len(refs) != 1 || refs[0] != "normalized-transcript-1" {
		t.Fatalf("unexpected projection source refs: %#v", refs)
	}
}

func validServiceInputAndDocument(t *testing.T) (InputPackage, ProjectionDocument) {
	t.Helper()
	collector, _, _ := testCollector(t, true)
	input, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(validModelOutput(input))
	doc, err := (&Generator{LLM: &fakeCompletionClient{output: string(raw)}, PromptVersion: "training-v1"}).Generate(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	return input, doc
}

func newTrainingServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "service.db")+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.TrainingOrchestrationJob{}, &model.TrainingOrchestrationCurrent{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func waitForTrainingJob(t *testing.T, service *Service, id string) model.TrainingOrchestrationJob {
	t.Helper()
	for i := 0; i < 100; i++ {
		job, err := service.GetJob(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == JobSucceeded || job.Status == JobFailed {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("training orchestration job did not finish")
	return model.TrainingOrchestrationJob{}
}
