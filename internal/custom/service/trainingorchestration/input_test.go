package trainingorchestration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

const (
	testKBID       = "knowledge-kb"
	testVideoID    = "video-1"
	testGeneration = "generation-1"
	testEvidenceID = "ev-1"
	testChunkID    = "chunk-1"
)

type fakeWiki struct {
	pages    map[string]weknora.WikiPage
	getCount int
	lastSlug string
	err      error
}

func (f *fakeWiki) GetPage(_ context.Context, _, slug string) (*weknora.WikiPage, error) {
	f.getCount++
	f.lastSlug = slug
	if f.err != nil {
		return nil, f.err
	}
	for _, page := range f.pages {
		if page.Slug == slug {
			copy := page
			return &copy, nil
		}
	}
	return nil, nil
}

type fakeVideoAccess struct {
	accessible map[string]bool
	readCount  int
}

func (f *fakeVideoAccess) CheckAccessible(_ context.Context, videos []model.Video) (map[string]bool, error) {
	f.readCount++
	result := make(map[string]bool, len(videos))
	for _, video := range videos {
		result[video.ID] = f.accessible[video.ID]
	}
	return result, nil
}

type fakeKnowledgeReader struct {
	items     map[string]weknora.ManualKnowledgeResult
	readCount int
}

func (f *fakeKnowledgeReader) GetKnowledge(_ context.Context, id string) (weknora.ManualKnowledgeResult, error) {
	f.readCount++
	item, ok := f.items[id]
	if !ok {
		return weknora.ManualKnowledgeResult{}, errors.New("knowledge not found")
	}
	return item, nil
}

type fakeTranscriptReader struct {
	chunks    map[string][]transcript.Chunk
	err       error
	readCount int
}

func (f *fakeTranscriptReader) Read(_ context.Context, videoID, generation string) ([]transcript.Chunk, error) {
	f.readCount++
	if f.err != nil {
		return nil, f.err
	}
	return append([]transcript.Chunk(nil), f.chunks[videoID+"\x00"+generation]...), nil
}

func TestCollectReturnsTranscriptReadError(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	readErr := errors.New("transcript backend unavailable")
	collector.TranscriptReader.(*fakeTranscriptReader).err = readErr

	_, err := collector.Collect(context.Background())
	if err == nil || !errors.Is(err, readErr) {
		t.Fatalf("expected transcript read error, got %v", err)
	}
	if strings.Contains(err.Error(), string(SkipEvidenceMissing)) {
		t.Fatalf("transcript read error was misclassified as evidence missing: %v", err)
	}
}

func TestCollectUsesFinalSummaryWithoutReadingNormalizedTranscript(t *testing.T) {
	collector, wiki, sourceReader := testCollector(t, true)
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedVideos != 1 || len(result.QualifiedVideos) != 1 || len(result.SkippedVideos) != 0 {
		t.Fatalf("unexpected counts: %#v", result)
	}
	profile := result.QualifiedVideos[0]
	if profile.TopicSource != TopicSourceFinalSummary || profile.Summary == nil || profile.Transcript != nil {
		t.Fatalf("unexpected topic source: %#v", profile)
	}
	if sourceReader.readCount != 0 {
		t.Fatalf("normalized transcript was read %d times for a valid final summary", sourceReader.readCount)
	}
	if profile.KnowledgeIndex.WikiPageID != "" || len(profile.KnowledgeSignals) != 0 {
		t.Fatalf("knowledge objects leaked into the summary-first input: %#v", profile)
	}
	for _, signal := range profile.Summary.Signals {
		if len(signal.KnowledgeRefs) != 0 {
			t.Fatalf("summary knowledge references leaked into the input: %#v", signal)
		}
	}
	if len(profile.EvidenceSignals) != 1 || profile.EvidenceSignals[0].VideoID != testVideoID ||
		profile.EvidenceSignals[0].TranscriptGeneration != testGeneration || profile.EvidenceSignals[0].EvidenceID != testEvidenceID {
		t.Fatalf("unexpected evidence signals: %#v", profile.EvidenceSignals)
	}
	if wiki.getCount != 1 || wiki.lastSlug != "typed-summary/"+testVideoID {
		t.Fatalf("unexpected Wiki summary read: count=%d slug=%q", wiki.getCount, wiki.lastSlug)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "raw-mps-result-must-not-leak") {
		t.Fatal("processing job result payload leaked into the input package")
	}
}

func TestCollectFallsBackToValidatedNormalizedTranscript(t *testing.T) {
	collector, _, sourceReader := testCollector(t, false)
	extraChunk := addSecondEvidence(t, collector)
	document := transcriptDocument(t, []transcript.InputSentence{
		{SourceSentenceID: "source-sentence-1", EvidenceSentenceID: testEvidenceID, Text: "当前代次证据原文", StartMs: 1000, EndMs: 2000},
		{SourceSentenceID: extraChunk.SourceSentenceID, EvidenceSentenceID: extraChunk.EvidenceSentenceID, Text: "未被知识页覆盖的新主题", StartMs: extraChunk.StartMs, EndMs: extraChunk.EndMs},
	})
	documentJSON, err := document.JSON()
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(documentJSON)))
	binding := model.VideoTranscriptSource{
		ID: "source-binding", VideoID: testVideoID, TranscriptGeneration: testGeneration,
		KnowledgeBaseID: testKBID, KnowledgeID: "source-1", ContentHash: hash, Status: transcript.SourceStatusCreated,
	}
	if err := collector.DB.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	sourceReader.items["source-1"] = weknora.ManualKnowledgeResult{
		ID: "source-1", KnowledgeBaseID: testKBID,
		Content: transcript.SourceContent(document, documentJSON, hash),
	}

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	profile := result.QualifiedVideos[0]
	if profile.TopicSource != TopicSourceNormalizedTranscript || profile.Transcript == nil || profile.Summary != nil {
		t.Fatalf("unexpected fallback profile: %#v", profile)
	}
	if sourceReader.readCount != 1 || len(profile.Transcript.Signals) != 1 ||
		profile.Transcript.Signals[0].Text != "当前代次证据原文未被知识页覆盖的新主题" ||
		len(profile.Transcript.Signals[0].EvidenceRefs) != 2 || profile.Transcript.Signals[0].EvidenceRefs[1].EvidenceID != extraChunk.EvidenceSentenceID ||
		len(profile.EvidenceSignals) != 2 {
		t.Fatalf("unexpected normalized transcript reads/signals: reads=%d input=%#v", sourceReader.readCount, profile.Transcript)
	}
}

func TestCollectQualifiesNormalizedTranscriptWithoutKnowledgeObjects(t *testing.T) {
	collector, wiki, sourceReader := testCollector(t, false)
	if err := collector.DB.Model(&model.Video{}).Where("id = ?", testVideoID).Updates(map[string]any{
		"knowledge_base_wiki_page_id": "",
		"knowledge_audit_status":      "failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	delete(wiki.pages, "index-page")
	delete(wiki.pages, "knowledge-page")
	installTranscriptSource(t, collector, sourceReader, validTranscriptDocument(t))

	result, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 || len(result.SkippedVideos) != 0 {
		t.Fatalf("video with complete transcript evidence was not qualified: %#v", result)
	}
	profile := result.QualifiedVideos[0]
	if profile.TopicSource != TopicSourceNormalizedTranscript || profile.Transcript == nil {
		t.Fatalf("unexpected transcript-first profile: %#v", profile)
	}
	if profile.KnowledgeIndex.WikiPageID != "" || len(profile.KnowledgeSignals) != 0 {
		t.Fatalf("knowledge objects leaked into transcript-first input: %#v", profile)
	}
}

func TestCollectQualifiesFinalSummaryWithoutKnowledgeObjects(t *testing.T) {
	collector, wiki, sourceReader := testCollector(t, true)
	if err := collector.DB.Model(&model.Video{}).Where("id = ?", testVideoID).Updates(map[string]any{
		"knowledge_base_wiki_page_id": "",
		"knowledge_audit_status":      "failed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	delete(wiki.pages, "index-page")
	delete(wiki.pages, "knowledge-page")

	result, err := collector.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 || len(result.SkippedVideos) != 0 {
		t.Fatalf("video with valid summary evidence was not qualified: %#v", result)
	}
	profile := result.QualifiedVideos[0]
	if profile.TopicSource != TopicSourceFinalSummary || profile.Summary == nil || profile.Transcript != nil {
		t.Fatalf("unexpected summary-first profile: %#v", profile)
	}
	if sourceReader.readCount != 0 || profile.KnowledgeIndex.WikiPageID != "" || len(profile.KnowledgeSignals) != 0 {
		t.Fatalf("knowledge objects affected final-summary input: %#v", profile)
	}
}

func TestCollectRejectsStaleFinalSummaryWithoutFallback(t *testing.T) {
	collector, wiki, sourceReader := testCollector(t, true)
	stale := wiki.pages["summary-page"]
	stale.Content = strings.Replace(stale.Content, "transcript_generation: "+testGeneration, "transcript_generation: generation-old", 1)
	wiki.pages["summary-page"] = stale

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipFormalContentNotReady] != 1 {
		t.Fatalf("stale final summary was not rejected: %#v", result)
	}
	if sourceReader.readCount != 0 {
		t.Fatal("stale final summary silently fell back to the normalized transcript")
	}
}

func TestCollectIgnoresFinalSummaryKnowledgeReferences(t *testing.T) {
	collector, wiki, sourceReader := testCollector(t, true)
	page := wiki.pages["summary-page"]
	page.Content = strings.ReplaceAll(page.Content, "knowledge-page", "stale-knowledge-page")
	wiki.pages["summary-page"] = page

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 || len(result.QualifiedVideos[0].Summary.Signals) == 0 {
		t.Fatalf("valid summary evidence was rejected because of a knowledge reference: %#v", result)
	}
	if sourceReader.readCount != 0 || len(result.QualifiedVideos[0].Summary.Signals[0].KnowledgeRefs) != 0 {
		t.Fatalf("summary knowledge references were not isolated: %#v", result.QualifiedVideos[0])
	}
}

func TestCollectIgnoresInvalidKnowledgeObjectEvidence(t *testing.T) {
	collector, wiki, _ := testCollector(t, true)
	page := wiki.pages["knowledge-page"]
	page.Content = strings.ReplaceAll(page.Content, testEvidenceID, testChunkID)
	wiki.pages[page.ID] = page

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 || len(result.QualifiedVideos[0].KnowledgeSignals) != 0 {
		t.Fatalf("knowledge object affected summary-based qualification: %#v", result)
	}
}

func TestCollectRejectsSummaryWhoseEvidencePointsAtWrongChunk(t *testing.T) {
	collector, wiki, _ := testCollector(t, true)
	page := wiki.pages["summary-page"]
	page.Content = strings.ReplaceAll(page.Content, testChunkID, "chunk-other")
	wiki.pages[page.ID] = page

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipFormalContentNotReady] != 1 {
		t.Fatalf("summary with a mismatched chunk was accepted: %#v", result)
	}
}

func TestCollectRejectsSummaryProfileEvidenceOutsideCurrentGeneration(t *testing.T) {
	collector, wiki, _ := testCollector(t, true)
	page := wiki.pages["summary-page"]
	document, err := summary.ParseStored(page.Content)
	if err != nil {
		t.Fatal(err)
	}
	document.OrchestrationProfile = &summary.OrchestrationProfile{
		SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []summary.OrchestrationTopicUnit{{
			Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
			SummaryBlockIDs: []string{"block-1"}, EvidenceChunkIDs: []string{"chunk-from-old-generation"},
		}},
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	jsonStart := strings.Index(page.Content, "{")
	if jsonStart < 0 {
		t.Fatal("summary page JSON payload not found")
	}
	page.Content = page.Content[:jsonStart] + string(payload)
	wiki.pages["summary-page"] = page

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipFormalContentNotReady] != 1 {
		t.Fatalf("summary with stale orchestration evidence was accepted: %#v", result)
	}
}

func TestCollectAcceptsFinalSummaryWithoutKnowledgeReferences(t *testing.T) {
	collector, wiki, _ := testCollector(t, true)
	page := wiki.pages["summary-page"]
	page.Content = strings.ReplaceAll(page.Content, `"knowledge_refs":["knowledge-page"]`, `"knowledge_refs":[]`)
	wiki.pages[page.ID] = page

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 || len(result.QualifiedVideos[0].Summary.Signals) == 0 {
		t.Fatalf("summary without knowledge references was rejected: %#v", result)
	}
}

func TestCollectIgnoresFailedKnowledgeObjectContribution(t *testing.T) {
	collector, wiki, _ := testCollector(t, true)
	page := wiki.pages["knowledge-page"]
	canonical, err := knowledge.EnsureEvidenceContributions(page.Content)
	if err != nil {
		t.Fatal(err)
	}
	page.Content = strings.Replace(canonical, "quality_status: passed", "quality_status: failed", 1)
	wiki.pages[page.ID] = page

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 1 || len(result.QualifiedVideos[0].KnowledgeSignals) != 0 {
		t.Fatalf("knowledge object contribution affected summary-based qualification: %#v", result)
	}
}

func TestCollectSkipsInaccessibleVideoBeforeReadingItsContent(t *testing.T) {
	collector, _, sourceReader := testCollector(t, true)
	collector.VideoAccess.(*fakeVideoAccess).accessible[testVideoID] = false

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipInaccessible] != 1 {
		t.Fatalf("inaccessible video was not skipped: %#v", result)
	}
	if sourceReader.readCount != 0 || collector.TranscriptReader.(*fakeTranscriptReader).readCount != 0 {
		t.Fatal("inaccessible video content was read")
	}
}

func TestCollectRejectsSourceContentHashMismatch(t *testing.T) {
	collector, _, sourceReader := testCollector(t, false)
	document := validTranscriptDocument(t)
	documentJSON, err := document.JSON()
	if err != nil {
		t.Fatal(err)
	}
	validHash := fmt.Sprintf("%x", sha256.Sum256([]byte(documentJSON)))
	if err := collector.DB.Create(&model.VideoTranscriptSource{
		ID: "source-binding", VideoID: testVideoID, TranscriptGeneration: testGeneration,
		KnowledgeBaseID: testKBID, KnowledgeID: "source-1", ContentHash: "wrong-hash", Status: transcript.SourceStatusCreated,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sourceReader.items["source-1"] = weknora.ManualKnowledgeResult{
		ID: "source-1", KnowledgeBaseID: testKBID,
		Content: transcript.SourceContent(document, documentJSON, validHash),
	}

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipFormalContentNotReady] != 1 {
		t.Fatalf("source hash mismatch was not rejected: %#v", result)
	}
}

func TestCollectRejectsNormalizedTranscriptThatOmitsCurrentEvidence(t *testing.T) {
	collector, _, sourceReader := testCollector(t, false)
	addSecondEvidence(t, collector)
	installTranscriptSource(t, collector, sourceReader, validTranscriptDocument(t))

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipFormalContentNotReady] != 1 {
		t.Fatalf("incomplete normalized transcript was accepted: %#v", result)
	}
}

func TestCollectRejectsNormalizedTranscriptWhoseTextDiffersFromEvidence(t *testing.T) {
	collector, _, sourceReader := testCollector(t, false)
	document := transcriptDocument(t, []transcript.InputSentence{{
		SourceSentenceID: "source-sentence-1", EvidenceSentenceID: testEvidenceID,
		Text: "与证据原文不一致", StartMs: 1000, EndMs: 2000,
	}})
	installTranscriptSource(t, collector, sourceReader, document)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipFormalContentNotReady] != 1 {
		t.Fatalf("normalized transcript with drifted text was accepted: %#v", result)
	}
}

func TestCollectRejectsIncompleteCurrentEvidenceManifest(t *testing.T) {
	collector, _, _ := testCollector(t, true)
	if err := collector.DB.Model(&model.VideoTranscriptChunk{}).
		Where("video_id = ? AND generation = ?", testVideoID, testGeneration).
		Update("revision", 2).Error; err != nil {
		t.Fatal(err)
	}
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.QualifiedVideos) != 0 || result.SkipReasonCounts[SkipEvidenceMissing] != 1 {
		t.Fatalf("stale evidence revision was not rejected: %#v", result)
	}
}

func TestCollectRejectsMoreThanOneHundredCandidatesBeforeContentReads(t *testing.T) {
	db := testDB(t)
	now := time.Now().UTC()
	videos := make([]model.Video, 0, MaxCandidateVideos+1)
	for index := 0; index <= MaxCandidateVideos; index++ {
		videos = append(videos, model.Video{
			ID: fmt.Sprintf("video-%03d", index), Title: "候选视频", VideoType: "training",
			FileURL: "https://example.invalid/video.mp4", Status: model.VideoStatusProcessing, UploadedAt: &now,
		})
	}
	if err := db.Create(&videos).Error; err != nil {
		t.Fatal(err)
	}
	wiki := &fakeWiki{pages: map[string]weknora.WikiPage{}}
	sourceReader := &fakeKnowledgeReader{items: map[string]weknora.ManualKnowledgeResult{}}
	transcriptReader := &fakeTranscriptReader{chunks: map[string][]transcript.Chunk{}}
	collector := Collector{DB: db, Wiki: wiki, SourceReader: sourceReader, TranscriptReader: transcriptReader, KnowledgeBaseID: testKBID, OwnerScopeID: "single-tenant"}
	collector.VideoAccess = &fakeVideoAccess{accessible: map[string]bool{}}

	result, err := collector.Collect(context.Background())
	var capacityErr *CapacityError
	if !errors.As(err, &capacityErr) || capacityErr.CandidateVideos != MaxCandidateVideos+1 {
		t.Fatalf("expected capacity error, got result=%#v err=%v", result, err)
	}
	if wiki.getCount != 0 || sourceReader.readCount != 0 || transcriptReader.readCount != 0 {
		t.Fatal("content was read before the candidate capacity gate")
	}
}

func TestCollectorRequiresTrustedOwnerScope(t *testing.T) {
	var missing *Collector
	if _, err := missing.Collect(context.Background()); err == nil {
		t.Fatal("nil collector was accepted")
	}
	collector, _, _ := testCollector(t, true)
	collector.OwnerScopeID = ""
	if _, err := collector.Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "owner scope") {
		t.Fatalf("expected missing owner scope error, got %v", err)
	}
	if got := (&CapacityError{CandidateVideos: 101}).Error(); !strings.Contains(got, "101") {
		t.Fatalf("capacity error does not report the candidate count: %q", got)
	}
}

func TestCollectReturnsEmptyPackageWithoutExternalReads(t *testing.T) {
	db := testDB(t)
	wiki := &fakeWiki{pages: map[string]weknora.WikiPage{}, err: errors.New("Wiki unavailable")}
	access := &fakeVideoAccess{accessible: map[string]bool{}}
	collector := Collector{
		DB: db, Wiki: wiki, SourceReader: &fakeKnowledgeReader{items: map[string]weknora.ManualKnowledgeResult{}},
		TranscriptReader: &fakeTranscriptReader{chunks: map[string][]transcript.Chunk{}}, VideoAccess: access,
		KnowledgeBaseID: testKBID, OwnerScopeID: "single-tenant",
	}

	result, err := collector.Collect(context.Background())
	if err != nil || result.ScannedVideos != 0 || len(result.QualifiedVideos) != 0 || len(result.SkippedVideos) != 0 {
		t.Fatalf("empty input failed: result=%#v err=%v", result, err)
	}
	if wiki.getCount != 0 || access.readCount != 0 {
		t.Fatalf("empty input performed external reads: wiki=%d access=%d", wiki.getCount, access.readCount)
	}
}

func testCollector(t *testing.T, withSummary bool) (Collector, *fakeWiki, *fakeKnowledgeReader) {
	t.Helper()
	db := testDB(t)
	now := time.Now().UTC()
	video := model.Video{
		ID: testVideoID, Title: "培训视频", VideoType: "training", DurationSeconds: 10,
		FileURL: "https://example.invalid/video.mp4", Status: model.VideoStatusCompleted, UploadedAt: &now,
		TranscriptGeneration: testGeneration, TranscriptRevision: 1, TranscriptActiveRevision: 1,
		KnowledgeBaseWikiPageID: "index-page", KnowledgeAuditStatus: "passed",
	}
	if withSummary {
		video.SummaryWikiPageID = "summary-page"
		video.SummaryResultStage = "final_ready"
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatal(err)
	}
	checkpoint := model.VideoTranscriptChunk{
		VideoID: testVideoID, Generation: testGeneration, Revision: 1, ChunkIndex: 0,
		EvidenceSentenceID: testEvidenceID, SourceSegmentID: "source-sentence-1",
		StartMs: 1000, EndMs: 2000, KnowledgeID: testChunkID, ContentHash: "chunk-hash", Status: "completed",
	}
	if err := db.Create(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VideoProcessingJob{
		ID: "mps-job", VideoID: testVideoID, JobType: "transcription", Status: "succeeded",
		IdempotencyKey: "mps-job", ResultPayload: "raw-mps-result-must-not-leak",
	}).Error; err != nil {
		t.Fatal(err)
	}
	indexPage := validIndexPage()
	objectPage := validKnowledgePage(t)
	wiki := &fakeWiki{
		pages: map[string]weknora.WikiPage{
			indexPage.ID:   indexPage,
			objectPage.ID:  objectPage,
			"summary-page": validSummaryPage(t),
		},
	}
	sourceReader := &fakeKnowledgeReader{items: map[string]weknora.ManualKnowledgeResult{}}
	transcriptReader := &fakeTranscriptReader{chunks: map[string][]transcript.Chunk{
		testVideoID + "\x00" + testGeneration: {{
			ID: testChunkID, EvidenceSentenceID: testEvidenceID, SourceSentenceID: "source-sentence-1",
			Index: 0, Content: "## 原文\n\n当前代次证据原文", StartMs: 1000, EndMs: 2000,
		}},
	}}
	return Collector{
		DB: db, Wiki: wiki, SourceReader: sourceReader, TranscriptReader: transcriptReader,
		VideoAccess:     &fakeVideoAccess{accessible: map[string]bool{testVideoID: true}},
		KnowledgeBaseID: testKBID, OwnerScopeID: "single-tenant",
	}, wiki, sourceReader
}

func addSecondEvidence(t *testing.T, collector Collector) transcript.Chunk {
	t.Helper()
	extraChunk := transcript.Chunk{
		ID: "chunk-2", EvidenceSentenceID: "ev-2", SourceSentenceID: "source-sentence-2",
		Index: 1, Content: "## 原文\n\n未被知识页覆盖的新主题", StartMs: 2000, EndMs: 3000,
	}
	reader := collector.TranscriptReader.(*fakeTranscriptReader)
	reader.chunks[testVideoID+"\x00"+testGeneration] = append(reader.chunks[testVideoID+"\x00"+testGeneration], extraChunk)
	if err := collector.DB.Create(&model.VideoTranscriptChunk{
		VideoID: testVideoID, Generation: testGeneration, Revision: 1, ChunkIndex: 1,
		EvidenceSentenceID: extraChunk.EvidenceSentenceID, SourceSegmentID: extraChunk.SourceSentenceID,
		StartMs: extraChunk.StartMs, EndMs: extraChunk.EndMs, KnowledgeID: extraChunk.ID, ContentHash: "chunk-2-hash", Status: "completed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	return extraChunk
}

func installTranscriptSource(t *testing.T, collector Collector, sourceReader *fakeKnowledgeReader, document transcript.FullVideoDocument) {
	t.Helper()
	documentJSON, err := document.JSON()
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(documentJSON)))
	if err := collector.DB.Create(&model.VideoTranscriptSource{
		ID: "source-binding", VideoID: testVideoID, TranscriptGeneration: testGeneration,
		KnowledgeBaseID: testKBID, KnowledgeID: "source-1", ContentHash: hash, Status: transcript.SourceStatusCreated,
	}).Error; err != nil {
		t.Fatal(err)
	}
	sourceReader.items["source-1"] = weknora.ManualKnowledgeResult{
		ID: "source-1", KnowledgeBaseID: testKBID, Content: transcript.SourceContent(document, documentJSON, hash),
	}
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "training.db")+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}, &model.VideoTranscriptSource{}, &model.VideoProcessingJob{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func validIndexPage() weknora.WikiPage {
	return weknora.WikiPage{
		ID: "index-page", Slug: "video/" + testVideoID, Title: "培训视频_知识底座", PageType: "index", Version: 3,
		Content: "---\npage_type: index\ntype: knowledge_base\nsource_video_id: " + testVideoID + "\ntranscript_generation: " + testGeneration + "\naudit_status: aligned\ntitle: 培训视频_知识底座\n---\n\n# 培训视频_知识底座\n",
	}
}

func validKnowledgePage(t *testing.T) weknora.WikiPage {
	t.Helper()
	object := knowledge.ClassifiedKnowledge{
		CandidateID: "object-1", SourceDocumentID: "source-1", SourceVideoID: testVideoID,
		TranscriptGeneration: testGeneration, PrimaryType: knowledge.TypeConcept, Title: "按需学习",
		CoreContent:     "按真实问题选择当前需要学习的内容。",
		StructureFields: map[string]string{"definition": "按任务需要选择学习内容", "mechanism": "用实际问题暴露知识缺口"},
		EvidenceIDs:     []string{testEvidenceID}, ClassificationConfidence: 0.92, AuditStatus: "passed",
	}
	rendered, err := knowledge.RenderFirstStageObjectPage(knowledge.FirstStagePageInput{
		Object: object, TimeRange: "00:01-00:02", ChunkRefs: []string{testChunkID},
		FieldEvidence: map[string][]string{"definition": {testEvidenceID}, "mechanism": {testEvidenceID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return weknora.WikiPage{ID: "knowledge-page", Slug: "concept/on-demand-learning", Title: rendered.Title, PageType: rendered.PageType, Version: 2, Content: rendered.Content}
}

func validSummaryPage(t *testing.T) weknora.WikiPage {
	t.Helper()
	framework, ok := summary.Framework("training")
	if !ok {
		t.Fatal("training summary framework missing")
	}
	document := summary.Document{
		SchemaVersion: summary.SchemaVersion, VideoType: "training",
		Classification: &summary.Classification{Confidence: 0.9, Reason: "转写内容属于培训", EvidenceChunkIDs: []string{testChunkID}},
	}
	for index, section := range framework {
		document.Sections = append(document.Sections, summary.Section{
			ID: section.ID, Title: section.Title,
			Blocks: []summary.Block{{
				ID: fmt.Sprintf("block-%d", index+1), Kind: summary.BlockKindParagraph, Text: "可验证的培训总结内容",
				EvidenceChunkIDs: []string{testChunkID}, KnowledgeRefs: []string{"knowledge-page"},
				EvidenceRefs: []summary.EvidenceRef{{ChunkID: testChunkID, EvidenceSentenceID: testEvidenceID, StartMs: 1000, EndMs: 2000}},
				Evidence:     []summary.Evidence{{ChunkID: testChunkID, EvidenceSentenceID: testEvidenceID, StartSeconds: 1, EndSeconds: 2, Timestamp: "00:01-00:02", TranscriptSnippet: "当前代次证据原文"}},
			}},
		})
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return weknora.WikiPage{
		ID: "summary-page", Slug: "typed-summary/" + testVideoID, Title: "培训视频总结", PageType: "index", Version: 4,
		Content: "---\ntype: typed_summary\nsource_video_id: " + testVideoID + "\ntranscript_generation: " + testGeneration + "\n---\n" + string(payload),
	}
}

func validTranscriptDocument(t *testing.T) transcript.FullVideoDocument {
	t.Helper()
	return transcriptDocument(t, []transcript.InputSentence{{
		SourceSentenceID: "source-sentence-1", EvidenceSentenceID: testEvidenceID,
		Text: "当前代次证据原文", StartMs: 1000, EndMs: 2000,
	}})
}

func transcriptDocument(t *testing.T, sentences []transcript.InputSentence) transcript.FullVideoDocument {
	t.Helper()
	document, err := transcript.Build(transcript.Input{
		VideoID: testVideoID, TranscriptGeneration: testGeneration, Title: "培训视频", DurationSeconds: 10,
		Chapters: []transcript.InputChapter{{Index: 0, Title: "第一章", Paragraphs: []transcript.InputParagraph{{
			ParagraphID: "paragraph-1", Index: 0, Sentences: sentences,
		}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}
