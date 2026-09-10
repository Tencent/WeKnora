package knowledgegraph

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
)

func TestRelationMatrixRejectsOutOfContractPairs(t *testing.T) {
	if !IsRelationAllowedForTypes("explains", knowledge.TypeConcept, knowledge.TypeMethodology) {
		t.Fatal("concept -> methodology explains should be allowed")
	}
	if IsRelationAllowedForTypes("explains", knowledge.TypeEntity, knowledge.TypeConcept) {
		t.Fatal("entity -> concept explains should be rejected")
	}
	if IsRelationAllowedForTypes("related_to", knowledge.TypeConcept, knowledge.TypeConcept) {
		t.Fatal("legacy related_to should be rejected")
	}
}

func TestBuildCrossVideoAssociationsUsesSharedObjectIDAndCurrentEvidence(t *testing.T) {
	videoA := CrossVideoVideo{ID: "video-a", Title: "来源视频", TranscriptGeneration: "gen-a", DurationSeconds: 10}
	videoB := CrossVideoVideo{ID: "video-b", Title: "目标视频", TranscriptGeneration: "gen-b", DurationSeconds: 10}
	pages := []CrossVideoPage{
		{ID: "10000000-0000-4000-8000-000000000001", KnowledgeObjectID: "object-shared", KnowledgeType: knowledge.TypeConcept, VideoID: videoA.ID, TranscriptGeneration: videoA.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-a"}},
		{ID: "10000000-0000-4000-8000-000000000002", KnowledgeObjectID: "object-shared", KnowledgeType: knowledge.TypeConcept, VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-b"}},
		{ID: "10000000-0000-4000-8000-000000000003", KnowledgeObjectID: "title-only-match", KnowledgeType: knowledge.TypeConcept, VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-other"}},
	}
	result := BuildCrossVideoAssociations(videoA.ID, pages, map[string]CrossVideoVideo{videoA.ID: videoA, videoB.ID: videoB}, map[string][]CrossVideoEvidence{
		videoA.ID: {{ID: "ev-a", VideoID: videoA.ID, TranscriptGeneration: videoA.TranscriptGeneration, StartMs: 1000, EndMs: 2000}},
		videoB.ID: {{ID: "ev-b", VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, StartMs: 3000, EndMs: 4000}, {ID: "ev-other", VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, StartMs: 4000, EndMs: 5000}},
	})
	if result.Status != CrossVideoStatusReady || len(result.Associations) != 1 {
		t.Fatalf("result = %#v", result)
	}
	association := result.Associations[0]
	if association.TargetVideoID != videoB.ID || association.RelationType != "shared_object" || association.SourceEvidence.StartMs != 1000 || association.TargetEvidence.StartMs != 3000 {
		t.Fatalf("association = %#v", association)
	}
}

func TestBuildCrossVideoAssociationsRejectsStaleOrOutOfRangeEvidence(t *testing.T) {
	videoA := CrossVideoVideo{ID: "video-a", Title: "A", TranscriptGeneration: "gen-a", DurationSeconds: 5}
	videoB := CrossVideoVideo{ID: "video-b", Title: "B", TranscriptGeneration: "gen-b", DurationSeconds: 5}
	pages := []CrossVideoPage{
		{ID: "20000000-0000-4000-8000-000000000001", KnowledgeObjectID: "object-shared", KnowledgeType: knowledge.TypeConcept, VideoID: videoA.ID, TranscriptGeneration: videoA.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-a"}},
		{ID: "20000000-0000-4000-8000-000000000002", KnowledgeObjectID: "object-shared", KnowledgeType: knowledge.TypeConcept, VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-b"}},
	}
	result := BuildCrossVideoAssociations(videoA.ID, pages, map[string]CrossVideoVideo{videoA.ID: videoA, videoB.ID: videoB}, map[string][]CrossVideoEvidence{
		videoA.ID: {{ID: "ev-a", VideoID: videoA.ID, TranscriptGeneration: "old", StartMs: 1000, EndMs: 2000}},
		videoB.ID: {{ID: "ev-b", VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, StartMs: 1000, EndMs: 6000}},
	})
	if result.Status != CrossVideoStatusFilterEmpty || len(result.Associations) != 0 || len(result.Rejected) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Rejected[0].Reason != "evidence_invalid" {
		t.Fatalf("rejected = %#v", result.Rejected)
	}
}

func TestBuildCrossVideoAssociationsIsStableAndDeduplicated(t *testing.T) {
	videoA := CrossVideoVideo{ID: "video-a", Title: "A", TranscriptGeneration: "gen-a", DurationSeconds: 5}
	videoB := CrossVideoVideo{ID: "video-b", Title: "B", TranscriptGeneration: "gen-b", DurationSeconds: 5}
	pageA := CrossVideoPage{ID: "30000000-0000-4000-8000-000000000001", KnowledgeObjectID: "object", KnowledgeType: knowledge.TypeConcept, VideoID: videoA.ID, TranscriptGeneration: videoA.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-a"}}
	pageB := CrossVideoPage{ID: "30000000-0000-4000-8000-000000000002", KnowledgeObjectID: "object", KnowledgeType: knowledge.TypeConcept, VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, AuditStatus: "passed", EvidenceIDs: []string{"ev-b"}}
	inputs := []CrossVideoPage{pageB, pageA, pageB}
	evidence := map[string][]CrossVideoEvidence{videoA.ID: {{ID: "ev-a", VideoID: videoA.ID, TranscriptGeneration: videoA.TranscriptGeneration, StartMs: 0, EndMs: 1}}, videoB.ID: {{ID: "ev-b", VideoID: videoB.ID, TranscriptGeneration: videoB.TranscriptGeneration, StartMs: 1, EndMs: 2}}}
	result := BuildCrossVideoAssociations(videoA.ID, inputs, map[string]CrossVideoVideo{videoA.ID: videoA, videoB.ID: videoB}, evidence)
	if len(result.Associations) != 1 || result.Associations[0].ID != strings.Join([]string{pageA.ID, "shared_object", pageB.ID, videoB.ID, videoB.TranscriptGeneration}, ":") {
		t.Fatalf("associations = %#v", result.Associations)
	}
}

func TestBuildCrossVideoAssociationsKeepsIDsUniqueForCanonicalPageContributions(t *testing.T) {
	pageID := "40000000-0000-4000-8000-000000000001"
	videos := map[string]CrossVideoVideo{
		"video-a": {ID: "video-a", TranscriptGeneration: "gen-a", DurationSeconds: 10},
		"video-b": {ID: "video-b", TranscriptGeneration: "gen-b", DurationSeconds: 10},
		"video-c": {ID: "video-c", TranscriptGeneration: "gen-c", DurationSeconds: 10},
	}
	pages := []CrossVideoPage{
		{ID: pageID, KnowledgeObjectID: "object", KnowledgeType: knowledge.TypeConcept, VideoID: "video-a", TranscriptGeneration: "gen-a", AuditStatus: "passed", EvidenceIDs: []string{"ev-a"}},
		{ID: pageID, KnowledgeObjectID: "object", KnowledgeType: knowledge.TypeConcept, VideoID: "video-b", TranscriptGeneration: "gen-b", AuditStatus: "passed", EvidenceIDs: []string{"ev-b"}},
		{ID: pageID, KnowledgeObjectID: "object", KnowledgeType: knowledge.TypeConcept, VideoID: "video-c", TranscriptGeneration: "gen-c", AuditStatus: "passed", EvidenceIDs: []string{"ev-c"}},
	}
	evidence := map[string][]CrossVideoEvidence{
		"video-a": {{ID: "ev-a", VideoID: "video-a", TranscriptGeneration: "gen-a", StartMs: 0, EndMs: 1}},
		"video-b": {{ID: "ev-b", VideoID: "video-b", TranscriptGeneration: "gen-b", StartMs: 1, EndMs: 2}},
		"video-c": {{ID: "ev-c", VideoID: "video-c", TranscriptGeneration: "gen-c", StartMs: 2, EndMs: 3}},
	}

	result := BuildCrossVideoAssociations("video-a", pages, videos, evidence)
	if len(result.Associations) != 2 || result.Associations[0].ID == result.Associations[1].ID {
		t.Fatalf("associations = %#v", result.Associations)
	}
}
