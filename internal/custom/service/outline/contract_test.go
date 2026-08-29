package outline

import "testing"

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
