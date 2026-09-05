package knowledge

import (
	"encoding/json"
	"testing"
)

func validCandidatePayload() []byte {
	return []byte(`{"candidate_id":"cand-1","source_document_id":"doc-1","source_video_id":"video-1","transcript_generation":"gen-1","title":"网络效应","core_content":"用户增加会提升产品价值。","structure_fields":{"definition":"用户增加提升产品价值","mechanism":"连接关系增加效用"},"citations":[{"citation_id":"cite-1","evidence_ids":["ev-1"]}],"evidence_ids":["ev-1"]}`)
}

func validContextPayload() []byte {
	return []byte(`{"source_document_id":"doc-1","source_video_id":"video-1","transcript_generation":"gen-1","summary":"整篇视频源文档摘要。","sections":[{"section_id":"s-1","title":"核心概念","start_ms":0,"end_ms":1000,"evidence_ids":["ev-1"]}],"candidate_citations":[{"citation_id":"cite-1","evidence_ids":["ev-1"]}],"evidence_ids":["ev-1"]}`)
}

func TestClassificationContractsAcceptValidPayloads(t *testing.T) {
	candidate, err := DecodeCandidate(validCandidatePayload())
	if err != nil {
		t.Fatalf("candidate rejected: %v", err)
	}
	context, err := DecodeDocumentContext(validContextPayload())
	if err != nil {
		t.Fatalf("context rejected: %v", err)
	}
	if err := ValidateClassificationInput(candidate, context); err != nil {
		t.Fatalf("classification input rejected: %v", err)
	}
	classified := ClassifiedKnowledge{
		CandidateID: "cand-1", SourceDocumentID: "doc-1", SourceVideoID: "video-1", TranscriptGeneration: "gen-1",
		PrimaryType: TypeConcept, Title: "网络效应", CoreContent: "用户增加会提升产品价值。",
		StructureFields: map[string]string{"definition": "用户增加提升产品价值", "mechanism": "连接关系增加效用"}, EvidenceIDs: []string{"ev-1"}, ClassificationConfidence: 0.9, AuditStatus: "pending",
	}
	encoded, err := json.Marshal(classified)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeClassifiedKnowledge(encoded); err != nil {
		t.Fatalf("classified knowledge rejected: %v", err)
	}
	var decoded ClassifiedKnowledge
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateClassifiedKnowledge(candidate, context, decoded); err != nil {
		t.Fatalf("classified knowledge identity/evidence rejected: %v", err)
	}
}

func TestClassificationContractsRejectMissingIdentityAndEvidence(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing candidate identity": func(value map[string]any) { delete(value, "source_document_id") },
		"empty candidate evidence":   func(value map[string]any) { value["evidence_ids"] = []string{} },
		"missing context identity":   func(value map[string]any) { delete(value, "transcript_generation") },
		"empty context evidence":     func(value map[string]any) { value["evidence_ids"] = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			payload := validCandidatePayload()
			decode := DecodeCandidate
			if name == "missing context identity" || name == "empty context evidence" {
				payload = validContextPayload()
				decode = func(data []byte) (Candidate, error) { _, err := DecodeDocumentContext(data); return Candidate{}, err }
			}
			var value map[string]any
			if err := json.Unmarshal(payload, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			payload, _ = json.Marshal(value)
			if _, err := decode(payload); err == nil {
				t.Fatal("expected contract rejection")
			}
		})
	}
}

func TestClassificationContractsRejectUnknownAndSubtitleFields(t *testing.T) {
	for _, field := range []string{"unknown_field", "subtitle_blocks", "transcript_blocks"} {
		payload := map[string]any{}
		if err := json.Unmarshal(validContextPayload(), &payload); err != nil {
			t.Fatal(err)
		}
		payload[field] = []any{}
		encoded, _ := json.Marshal(payload)
		if _, err := DecodeDocumentContext(encoded); err == nil {
			t.Fatalf("expected %s to be rejected", field)
		}
	}
	var candidate map[string]any
	if err := json.Unmarshal(validCandidatePayload(), &candidate); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"subtitle", "wiki_page_id", "target_wiki_page_id", "graph_id", "page_type", "write_status"} {
		candidate["structure_fields"] = map[string]string{field: "forbidden"}
		encoded, _ := json.Marshal(candidate)
		if _, err := DecodeCandidate(encoded); err == nil {
			t.Fatalf("expected %s structure field to be rejected", field)
		}
	}
	classified := ClassifiedKnowledge{
		CandidateID: "cand-1", SourceDocumentID: "doc-1", SourceVideoID: "video-1", TranscriptGeneration: "gen-1",
		PrimaryType: TypeConcept, Title: "网络效应", CoreContent: "用户增加会提升产品价值。", EvidenceIDs: []string{"ev-1"}, ClassificationConfidence: 0.9, AuditStatus: "pending",
	}
	classifiedPayload, err := json.Marshal(classified)
	if err != nil {
		t.Fatal(err)
	}
	var unknown map[string]any
	if err := json.Unmarshal(classifiedPayload, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["subtitle_text"] = "forbidden"
	classifiedPayload, _ = json.Marshal(unknown)
	if _, err := DecodeClassifiedKnowledge(classifiedPayload); err == nil {
		t.Fatal("expected classified knowledge subtitle field to be rejected")
	}
}

func TestClassifiedKnowledgeRejectsInvalidTypeAndEntitySubtype(t *testing.T) {
	base := ClassifiedKnowledge{CandidateID: "c", SourceDocumentID: "d", SourceVideoID: "v", TranscriptGeneration: "g", Title: "t", CoreContent: "c", EvidenceIDs: []string{"e"}, ClassificationConfidence: 0.8, AuditStatus: "pending", StructureFields: map[string]string{"definition": "x", "mechanism": "y"}}
	base.PrimaryType = KnowledgeType("unknown")
	if err := base.Validate(); err == nil {
		t.Fatal("unknown type should be rejected")
	}
	base.PrimaryType = TypeEntity
	base.EntitySubType = "not-a-subtype"
	if err := base.Validate(); err == nil {
		t.Fatal("unknown entity subtype should be rejected")
	}
}

func TestValidateClassificationInputRejectsCrossGenerationAndUnknownEvidence(t *testing.T) {
	candidate, err := DecodeCandidate(validCandidatePayload())
	if err != nil {
		t.Fatal(err)
	}
	context, err := DecodeDocumentContext(validContextPayload())
	if err != nil {
		t.Fatal(err)
	}
	context.TranscriptGeneration = "other-generation"
	if err := ValidateClassificationInput(candidate, context); err == nil {
		t.Fatal("cross-generation input should be rejected")
	}
	context.TranscriptGeneration = candidate.TranscriptGeneration
	context.SourceVideoID = "other-video"
	if err := ValidateClassificationInput(candidate, context); err == nil {
		t.Fatal("cross-video input should be rejected")
	}
	context.SourceVideoID = candidate.SourceVideoID
	candidate.EvidenceIDs = []string{"ev-not-in-context"}
	if err := ValidateClassificationInput(candidate, context); err == nil {
		t.Fatal("evidence outside context should be rejected")
	}
}

func TestValidateClassifiedKnowledgeRejectsEvidenceOutsideContext(t *testing.T) {
	candidate, err := DecodeCandidate(validCandidatePayload())
	if err != nil {
		t.Fatal(err)
	}
	context, err := DecodeDocumentContext(validContextPayload())
	if err != nil {
		t.Fatal(err)
	}
	classified := ClassifiedKnowledge{
		CandidateID: candidate.ID, SourceDocumentID: context.SourceDocumentID, SourceVideoID: context.SourceVideoID, TranscriptGeneration: context.TranscriptGeneration,
		PrimaryType: TypeConcept, Title: candidate.Title, CoreContent: candidate.CoreContent, EvidenceIDs: []string{"ev-not-in-context"}, ClassificationConfidence: 0.8, AuditStatus: "pending",
	}
	if err := ValidateClassifiedKnowledge(candidate, context, classified); err == nil {
		t.Fatal("classified evidence outside context should be rejected")
	}
}

func TestValidateClassifiedKnowledgeRejectsEvidenceOutsideCandidate(t *testing.T) {
	candidate, err := DecodeCandidate(validCandidatePayload())
	if err != nil {
		t.Fatal(err)
	}
	context, err := DecodeDocumentContext(validContextPayload())
	if err != nil {
		t.Fatal(err)
	}
	context.EvidenceIDs = append(context.EvidenceIDs, "ev-2")
	classified := ClassifiedKnowledge{
		CandidateID: candidate.ID, SourceDocumentID: context.SourceDocumentID, SourceVideoID: context.SourceVideoID, TranscriptGeneration: context.TranscriptGeneration,
		PrimaryType: TypeConcept, Title: candidate.Title, CoreContent: candidate.CoreContent, EvidenceIDs: []string{"ev-2"}, ClassificationConfidence: 0.8, AuditStatus: "pending",
	}
	if err := ValidateClassifiedKnowledge(candidate, context, classified); err == nil {
		t.Fatal("classified evidence outside candidate should be rejected")
	}
}

func TestDocumentContextRejectsSectionAndCitationEvidenceOutsideManifest(t *testing.T) {
	for name, mutate := range map[string]func(*DocumentContext){
		"section": func(context *DocumentContext) { context.Sections[0].EvidenceIDs = []string{"ev-not-in-manifest"} },
		"citation": func(context *DocumentContext) {
			context.CandidateCitations[0].EvidenceIDs = []string{"ev-not-in-manifest"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			context, err := DecodeDocumentContext(validContextPayload())
			if err != nil {
				t.Fatal(err)
			}
			mutate(&context)
			if err := context.Validate(); err == nil {
				t.Fatal("context evidence outside manifest should be rejected")
			}
		})
	}
}

func TestClassificationContractsRejectCitationEvidenceOutsideCandidate(t *testing.T) {
	var candidate map[string]any
	if err := json.Unmarshal(validCandidatePayload(), &candidate); err != nil {
		t.Fatal(err)
	}
	candidate["citations"] = []map[string]any{{"citation_id": "cite-1", "evidence_ids": []string{"ev-not-in-candidate"}}}
	encoded, _ := json.Marshal(candidate)
	if _, err := DecodeCandidate(encoded); err == nil {
		t.Fatal("citation evidence outside candidate should be rejected")
	}
}
