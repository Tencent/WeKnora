package evidence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestIndexReadReturnsOrderedEvidenceWithTiming(t *testing.T) {
	db := testEvidenceDB(t)
	videoID, generation := "video-1", "generation-1"
	if err := db.Create(&model.Video{ID: videoID, Title: "视频", TranscriptGeneration: generation}).Error; err != nil {
		t.Fatal(err)
	}
	first := evidenceContent("evs:v1:first", "s1", "speaker-a", generation, 100, 1200, "第一句")
	second := evidenceContent("evs:v1:second", "s2", "speaker-b", generation, 1200, 2400, "第二句")
	if err := db.Create([]model.VideoTranscriptChunk{
		{VideoID: videoID, Generation: generation, Revision: 1, ChunkIndex: 0, KnowledgeID: "k-1", EvidenceSentenceID: "evs:v1:first", SourceSegmentID: "s1", SpeakerID: "speaker-a", StartMs: 100, EndMs: 1200, ContentHash: "hash-1", Status: "completed"},
		{VideoID: videoID, Generation: generation, Revision: 1, ChunkIndex: 1, KnowledgeID: "k-2", EvidenceSentenceID: "evs:v1:second", SourceSegmentID: "s2", SpeakerID: "speaker-b", StartMs: 1200, EndMs: 2400, ContentHash: "hash-2", Status: "completed"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	client := testEvidenceClient(t, map[string]string{"k-1": first, "k-2": second}, nil)
	records, err := NewIndex(db, client).Read(context.Background(), videoID, generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Text != "第一句" || records[1].StartMs != 1200 || records[1].EvidenceSentenceID != "evs:v1:second" {
		t.Fatalf("records = %#v", records)
	}
}

func TestIndexReadBackfillsLegacyEvidenceSentenceID(t *testing.T) {
	db := testEvidenceDB(t)
	videoID, generation := "video-legacy", "generation-legacy"
	if err := db.Create(&model.Video{ID: videoID, Title: "视频", TranscriptGeneration: generation}).Error; err != nil {
		t.Fatal(err)
	}
	content := legacyEvidenceContent("s-legacy", "speaker-a", generation, 100, 1200, "历史原文")
	checkpoint := model.VideoTranscriptChunk{
		VideoID: videoID, Generation: generation, Revision: 1, ChunkIndex: 0,
		KnowledgeID: "k-legacy", SourceSegmentID: "s-legacy", SpeakerID: "speaker-a",
		StartMs: 100, EndMs: 1200, ContentHash: "hash", Status: "completed",
	}
	if err := db.Create(&checkpoint).Error; err != nil {
		t.Fatal(err)
	}
	client := testEvidenceClient(t, map[string]string{"k-legacy": content}, nil)
	records, err := NewIndex(db, client).Read(context.Background(), videoID, generation)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := BuildEvidenceSentenceID(Input{VideoID: videoID, TranscriptGeneration: generation, Ordinal: 0, SourceSentenceID: "s-legacy", Text: "历史原文", SpeakerID: "speaker-a", StartMs: 100, EndMs: 1200})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].EvidenceSentenceID != expected {
		t.Fatalf("records = %#v, expected ID %q", records, expected)
	}
	var stored model.VideoTranscriptChunk
	if err := db.First(&stored, "video_id = ? AND generation = ? AND chunk_index = 0", videoID, generation).Error; err != nil {
		t.Fatal(err)
	}
	if stored.EvidenceSentenceID != expected {
		t.Fatalf("legacy evidence ID was not persisted: %q", stored.EvidenceSentenceID)
	}
}

func TestIndexSearchScopesResultsAndRestoresSentenceMapping(t *testing.T) {
	db := testEvidenceDB(t)
	videoID, generation := "video-1", "generation-1"
	if err := db.Create(&model.Video{ID: videoID, Title: "视频", TranscriptGeneration: generation}).Error; err != nil {
		t.Fatal(err)
	}
	content := evidenceContent("evs:v1:first", "s1", "speaker-a", generation, 100, 1200, "检索命中")
	if err := db.Create(&model.VideoTranscriptChunk{VideoID: videoID, Generation: generation, Revision: 1, ChunkIndex: 0, KnowledgeID: "k-1", EvidenceSentenceID: "evs:v1:first", SourceSegmentID: "s1", SpeakerID: "speaker-a", StartMs: 100, EndMs: 1200, ContentHash: "hash-1", Status: "completed"}).Error; err != nil {
		t.Fatal(err)
	}
	client := testEvidenceClient(t, nil, []weknora.SearchResult{{ID: "result-1", KnowledgeID: "k-1", Content: content}})
	records, err := NewIndex(db, client).Search(context.Background(), videoID, generation, "命中", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].EvidenceSentenceID != "evs:v1:first" || records[0].StartMs != 100 || records[0].Text != "检索命中" {
		t.Fatalf("records = %#v", records)
	}
}

func TestIndexSearchRejectsCrossGenerationAndMalformedResults(t *testing.T) {
	db := testEvidenceDB(t)
	if err := db.Create(&model.Video{ID: "video-1", Title: "视频", TranscriptGeneration: "generation-current"}).Error; err != nil {
		t.Fatal(err)
	}
	client := testEvidenceClient(t, nil, nil)
	if _, err := NewIndex(db, client).Search(context.Background(), "video-1", "generation-old", "词", 5); err == nil {
		t.Fatal("expected active generation mismatch")
	}
	if _, err := NewIndex(db, client).Search(context.Background(), "video-1", "generation-current", " ", 5); err == nil {
		t.Fatal("expected empty query error")
	}
}

func TestIndexSearchRejectsResultOutsideManifest(t *testing.T) {
	db := testEvidenceDB(t)
	if err := db.Create(&model.Video{ID: "video-1", Title: "视频", TranscriptGeneration: "generation-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{VideoID: "video-1", Generation: "generation-1", Revision: 1, ChunkIndex: 0, KnowledgeID: "k-1", EvidenceSentenceID: "evs:v1:first", SourceSegmentID: "s1", StartMs: 0, EndMs: 1000, ContentHash: "hash", Status: "completed"}).Error; err != nil {
		t.Fatal(err)
	}
	client := testEvidenceClient(t, nil, []weknora.SearchResult{{KnowledgeID: "k-other", Content: "## 视频定位信息"}})
	if _, err := NewIndex(db, client).Search(context.Background(), "video-1", "generation-1", "词", 5); err == nil {
		t.Fatal("expected cross-video result to be rejected")
	}
}

func TestIndexSearchRejectsDuplicateOrMissingEvidenceMetadata(t *testing.T) {
	db := testEvidenceDB(t)
	if err := db.Create(&model.Video{ID: "video-1", Title: "视频", TranscriptGeneration: "generation-1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{VideoID: "video-1", Generation: "generation-1", Revision: 1, ChunkIndex: 0, KnowledgeID: "k-1", EvidenceSentenceID: "evs:v1:first", SourceSegmentID: "s1", StartMs: 0, EndMs: 1000, ContentHash: "hash", Status: "completed"}).Error; err != nil {
		t.Fatal(err)
	}
	t.Run("duplicate", func(t *testing.T) {
		content := evidenceContent("evs:v1:first", "s1", "", "generation-1", 0, 1000, "原文")
		client := testEvidenceClient(t, nil, []weknora.SearchResult{{KnowledgeID: "k-1", Content: content}, {KnowledgeID: "k-1", Content: content}})
		if _, err := NewIndex(db, client).Search(context.Background(), "video-1", "generation-1", "词", 5); err == nil {
			t.Fatal("expected duplicate search result to be rejected")
		}
	})
	t.Run("missing metadata", func(t *testing.T) {
		client := testEvidenceClient(t, nil, []weknora.SearchResult{{KnowledgeID: "k-1", Content: "只有原文，没有定位"}})
		if _, err := NewIndex(db, client).Search(context.Background(), "video-1", "generation-1", "词", 5); err == nil {
			t.Fatal("expected missing metadata to be rejected")
		}
	})
}

func TestIndexSearchUsesFixedEvidenceKnowledgeBase(t *testing.T) {
	db := testEvidenceDB(t)
	videoID, generation := "video-routing", "generation-routing"
	if err := db.Create(&model.Video{ID: videoID, Title: "视频", TranscriptGeneration: generation}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: videoID, Generation: generation, Revision: 1, ChunkIndex: 0,
		KnowledgeID: "evidence-1", EvidenceSentenceID: "evs:v1:routing", SourceSegmentID: "s1",
		StartMs: 0, EndMs: 1000, ContentHash: "hash", Status: "completed",
	}).Error; err != nil {
		t.Fatal(err)
	}

	seenPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		content := evidenceContent("evs:v1:routing", "s1", "", generation, 0, 1000, "原文")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": []weknora.SearchResult{{KnowledgeID: "evidence-1", Content: content}}})
	}))
	defer server.Close()
	base := config.WeKnoraConfig{BaseURL: server.URL, EvidenceKBID: "evidence-kb", KnowledgeKBID: "knowledge-kb"}
	client := weknora.New(base.ForKnowledgeBase(base.EvidenceKBID))
	if _, err := NewIndex(db, client).Search(t.Context(), videoID, generation, "原文", 5); err != nil {
		t.Fatal(err)
	}
	if seenPath != "/api/v1/knowledge-bases/evidence-kb/hybrid-search" {
		t.Fatalf("search path = %q, want evidence KB", seenPath)
	}
}

func testEvidenceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Video{}, &model.VideoTranscriptChunk{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func testEvidenceClient(t *testing.T, knowledge map[string]string, results []weknora.SearchResult) *weknora.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/hybrid-search" || r.URL.Path == "/api/v1/knowledge-bases/kb-1/hybrid-search" {
			payload, _ := json.Marshal(map[string]any{"success": true, "data": results})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		const prefix = "/api/v1/chunks/"
		if len(r.URL.Path) >= len(prefix) && r.URL.Path[:len(prefix)] == prefix {
			id := r.URL.Path[len(prefix):]
			content, ok := knowledge[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			payload, _ := json.Marshal(map[string]any{"success": true, "data": []weknora.KnowledgeChunk{{ID: "chunk-" + id, KnowledgeID: id, Content: content, ChunkIndex: 0}}, "total": 1})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return weknora.New(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1"})
}

func legacyEvidenceContent(source, speaker, generation string, start, end int, text string) string {
	return "## 视频定位信息\n\n```json\n" +
		`{"start_ms":` + strconv.Itoa(start) + `,"end_ms":` + strconv.Itoa(end) + `,"sentence_id":"` + source + `","speaker_id":"` + speaker + `","transcript_generation":"` + generation + `"}` +
		"\n```\n\n## 原文\n\n" + text
}

func evidenceContent(id, source, speaker, generation string, start, end int, text string) string {
	return `## 视频定位信息

` + "```json\n" + `{"start_ms":` + jsonInt(start) + `,"end_ms":` + jsonInt(end) + `,"sentence_id":"` + source + `","speaker_id":"` + speaker + `","evidence_sentence_id":"` + id + `","transcript_generation":"` + generation + `"}` + "\n```\n\n## 原文\n\n" + text
}

func jsonInt(value int) string { return strconv.Itoa(value) }
