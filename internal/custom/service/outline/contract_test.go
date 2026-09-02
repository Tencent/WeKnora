package outline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestResolveEvidenceProjectsImmutableSentenceIDs(t *testing.T) {
	document := validDocument()
	chunks := []transcript.Chunk{{ID: "chunk-1", EvidenceSentenceID: "evs:v1:abc", StartMs: 0, EndMs: 1000, Content: "原文"}}
	if err := ResolveEvidence(&document, chunks); err != nil {
		t.Fatalf("ResolveEvidence returned error: %v", err)
	}
	if got := document.Chapters[0].EvidenceSentenceIDs; len(got) != 1 || got[0] != "evs:v1:abc" {
		t.Fatalf("chapter evidence sentence IDs = %#v", got)
	}
	if got := document.Chapters[0].KnowledgePoints[0].EvidenceSentenceIDs; len(got) != 1 || got[0] != "evs:v1:abc" {
		t.Fatalf("point evidence sentence IDs = %#v", got)
	}
}

func TestValidateAndResolveBindsCurrentEvidenceAndTranscriptEnd(t *testing.T) {
	document := validDocument()
	document.Chapters[0].EndSeconds = 13
	chunks := []transcript.Chunk{
		{ID: "chunk-1", EvidenceSentenceID: "evs:v1:abc", SourceSentenceID: "source-1", StartMs: 0, EndMs: 12400, Content: "原文"},
	}
	if err := ValidateAndResolve(&document, 20, chunks); err != nil {
		t.Fatalf("ValidateAndResolve returned error: %v", err)
	}
	if got := document.Chapters[0].EvidenceSentenceIDs; len(got) != 1 || got[0] != "evs:v1:abc" {
		t.Fatalf("chapter evidence sentence IDs = %#v", got)
	}
	if got := document.Chapters[0].KnowledgePoints[0].EvidenceSentenceIDs; len(got) != 1 || got[0] != "evs:v1:abc" {
		t.Fatalf("point evidence sentence IDs = %#v", got)
	}
}

func TestValidateAndResolveRejectsIncompleteEvidenceMapping(t *testing.T) {
	document := validDocument()
	chunks := []transcript.Chunk{{ID: "chunk-1", StartMs: 0, EndMs: 1000, Content: "原文"}}
	if err := ValidateAndResolve(&document, 10, chunks); err == nil || !contains(err.Error(), "no evidence sentence ID") {
		t.Fatalf("expected missing evidence sentence ID error, got %v", err)
	}
}

func TestValidateAndResolveRejectsDuplicateEvidenceSentenceIDs(t *testing.T) {
	document := validDocument()
	chunks := []transcript.Chunk{
		{ID: "chunk-1", EvidenceSentenceID: "evs:v1:duplicate", StartMs: 0, EndMs: 1000, Content: "第一句"},
		{ID: "chunk-2", EvidenceSentenceID: "evs:v1:duplicate", StartMs: 1000, EndMs: 2000, Content: "第二句"},
	}
	if err := ValidateAndResolve(&document, 10, chunks); err == nil || !contains(err.Error(), "duplicate evidence sentence ID") {
		t.Fatalf("expected duplicate evidence sentence ID error, got %v", err)
	}
}

func validDocument() Document {
	return Document{
		SchemaVersion: SchemaVersion,
		Chapters: []Chapter{
			{
				ChapterIndex:     1,
				ChapterTitle:     "视频引入",
				StartSeconds:     0,
				EndSeconds:       60,
				ChapterSummary:   "本章介绍视频主题。",
				EvidenceChunkIDs: []string{"chunk-1"},
				KnowledgePoints: []KnowledgePoint{{
					Title:            "观察场景",
					Seconds:          12,
					EvidenceChunkIDs: []string{"chunk-1"},
				}},
			},
		},
	}
}

func TestValidateRejectsPlaceholderContent(t *testing.T) {
	document := validDocument()
	document.Chapters[0].ChapterSummary = "..."
	if err := Validate(document, 60, map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("expected placeholder summary to be rejected")
	}
}

func TestValidateRejectsMissingEvidence(t *testing.T) {
	document := validDocument()
	document.Chapters[0].EvidenceChunkIDs = nil
	if err := Validate(document, 60, map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("expected missing chapter evidence to be rejected")
	}
}

func TestValidateWithTranscriptEndAllowsTrailingVideoContentWithoutTranscript(t *testing.T) {
	document := validDocument()
	document.Chapters[0].EndSeconds = 386
	if err := ValidateWithTranscriptEnd(document, 535, 386, map[string]struct{}{"chunk-1": {}}); err != nil {
		t.Fatalf("ValidateWithTranscriptEnd returned error: %v", err)
	}
}

func TestValidateWithTranscriptEndRejectsUncoveredTranscript(t *testing.T) {
	document := validDocument()
	document.Chapters[0].EndSeconds = 300
	if err := ValidateWithTranscriptEnd(document, 535, 386, map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("expected effective transcript end coverage error")
	}
}

func TestValidateWithTranscriptEndStillRejectsChapterBeyondVideo(t *testing.T) {
	document := validDocument()
	document.Chapters[0].EndSeconds = 536
	if err := ValidateWithTranscriptEnd(document, 535, 386, map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("expected video duration overflow error")
	}
}

func TestValidateRejectsOverlappingChapters(t *testing.T) {
	document := validDocument()
	document.Chapters = append(document.Chapters, Chapter{
		ChapterIndex:     2,
		ChapterTitle:     "第二章",
		StartSeconds:     30,
		EndSeconds:       90,
		ChapterSummary:   "本章继续介绍视频主题。",
		EvidenceChunkIDs: []string{"chunk-2"},
		KnowledgePoints:  []KnowledgePoint{{Title: "延伸内容", Seconds: 45, EvidenceChunkIDs: []string{"chunk-2"}}},
	})
	if err := Validate(document, 90, map[string]struct{}{"chunk-1": {}, "chunk-2": {}}); err == nil {
		t.Fatal("expected overlapping chapter ranges to be rejected")
	}
}

func TestMarshalAndParseRoundTrip(t *testing.T) {
	document := validDocument()
	content, err := Marshal(document)
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	parsed, err := Parse("---\ntype: outline\n---\n\n" + content)
	if err != nil {
		t.Fatalf("parse returned error: %v", err)
	}
	if parsed.SchemaVersion != SchemaVersion || len(parsed.Chapters) != 1 || parsed.Chapters[0].KnowledgePoints[0].Seconds != 12 {
		t.Fatalf("unexpected parsed document: %+v", parsed)
	}
}

func TestRenderOrLegacyRendersCanonicalDocument(t *testing.T) {
	document := validDocument()
	content, err := Marshal(document)
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	rendered := RenderOrLegacy(content)
	if rendered == content || !containsAll(rendered, "## 视频引入", "### 本章核心内容", "- 观察场景（00:12）") {
		t.Fatalf("unexpected rendered content: %s", rendered)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !contains(value, part) {
			return false
		}
	}
	return true
}

func contains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
