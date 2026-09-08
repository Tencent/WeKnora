package knowledge

import (
	"strings"
	"testing"
)

func TestMergeCanonicalWikiObjectCreatesContribution(t *testing.T) {
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
title: 第二大脑（概念）
source_video_id: video-1
source_document_id: doc-1
transcript_generation: generation-1
audit_status: passed
evidence_ids: [ev-1]
source_refs: [doc-1]
core_content: 用于组织知识和行动的系统。
---

# 第二大脑（概念）
`
	merged, err := MergeCanonicalWikiObject("", content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(merged, "第二大脑（概念）") || !strings.Contains(merged, "evidence_contributions:") {
		t.Fatalf("canonical title/contribution normalization failed:\n%s", merged)
	}
	contributions, err := ParseEvidenceContributions(merged)
	if err != nil || len(contributions) != 1 {
		t.Fatalf("contributions = %#v, err=%v", contributions, err)
	}
	if contributions[0].VideoID != "video-1" || contributions[0].SourceDocumentID != "doc-1" {
		t.Fatalf("contribution = %#v", contributions[0])
	}
}

func TestMergeCanonicalWikiObjectAppendsAndReplacesByVideoGeneration(t *testing.T) {
	canonical := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
title: 第二大脑
source_video_id: video-1
transcript_generation: generation-1
evidence_ids: [ev-1]
source_refs: [doc-1]
evidence_contributions:
  - video_id: video-1
    source_document_id: doc-1
    transcript_generation: generation-1
    evidence_ids: [ev-1]
    quality_status: passed
---

# 第二大脑
`
	incoming := `---
knowledge_object_id: object-generated
type: concept
primary_type: concept
title: 第二大脑（概念）
source_video_id: video-2
source_document_id: doc-2
transcript_generation: generation-2
evidence_ids: [ev-2]
source_refs: [doc-2]
---

# 第二大脑（概念）
`
	merged, err := MergeCanonicalWikiObject(canonical, incoming)
	if err != nil {
		t.Fatal(err)
	}
	contributions, err := ParseEvidenceContributions(merged)
	if err != nil || len(contributions) != 2 {
		t.Fatalf("cross-video contributions = %#v, err=%v", contributions, err)
	}
	if !strings.Contains(merged, "doc-1") || !strings.Contains(merged, "doc-2") {
		t.Fatalf("source refs were not unioned:\n%s", merged)
	}
	updatedIncoming := strings.ReplaceAll(incoming, "ev-2", "ev-2b")
	updated, err := MergeCanonicalWikiObject(merged, updatedIncoming)
	if err != nil {
		t.Fatal(err)
	}
	updatedContributions, err := ParseEvidenceContributions(updated)
	if err != nil || len(updatedContributions) != 2 {
		t.Fatalf("same-generation contribution was duplicated: %#v, err=%v", updatedContributions, err)
	}
	if !strings.Contains(updated, "ev-2b") || strings.Contains(updated, "- ev-2\n") {
		t.Fatalf("same-generation contribution was not replaced:\n%s", updated)
	}
}
