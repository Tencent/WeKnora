package summary

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

func TestCurrentSchemaVersionIsTwo(t *testing.T) {
	if SchemaVersion != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", SchemaVersion)
	}
}

func TestValidateRequiresClassificationForCurrentSchema(t *testing.T) {
	document := emptyFrameworkDocument(SchemaVersion, "meeting", frameworks["meeting"])
	if err := Validate(document, "meeting", nil); err == nil || !strings.Contains(err.Error(), "classification") {
		t.Fatalf("Validate error = %v, want missing classification error", err)
	}
}

func TestValidateAcceptsCurrentEightSectionMeeting(t *testing.T) {
	document := emptyFrameworkDocument(SchemaVersion, "meeting", frameworks["meeting"])
	document.Classification = &Classification{
		Confidence: 0.9, Reason: "会议形成了明确共识和后续行动", EvidenceChunkIDs: []string{"chunk-1", "chunk-2"},
	}
	if err := Validate(document, "meeting", map[string]struct{}{"chunk-1": {}, "chunk-2": {}}); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateStoredAcceptsLegacySevenSectionMeeting(t *testing.T) {
	document := emptyFrameworkDocument(1, "meeting", legacyMeetingFramework)
	if err := ValidateStored(document, "meeting"); err != nil {
		t.Fatalf("ValidateStored returned error: %v", err)
	}
	if len(document.Sections) != 7 || document.Sections[0].Title != "一、会议目标与参与者" {
		t.Fatalf("legacy meeting document was unexpectedly transformed: %+v", document.Sections)
	}
}

func TestValidateRejectsLegacySchemaForNewGeneration(t *testing.T) {
	document := emptyFrameworkDocument(1, "meeting", legacyMeetingFramework)
	if err := Validate(document, "meeting", nil); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("Validate error = %v, want unsupported schema version", err)
	}
}

func TestValidateStoredRejectsUnknownSchemaVersion(t *testing.T) {
	document := emptyFrameworkDocument(99, "meeting", frameworks["meeting"])
	if err := ValidateStored(document, "meeting"); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("ValidateStored error = %v, want unsupported schema version", err)
	}
}

func emptyFrameworkDocument(schemaVersion int, videoType string, framework []FrameworkSection) Document {
	document := Document{SchemaVersion: schemaVersion, VideoType: videoType, Sections: make([]Section, 0, len(framework))}
	for _, section := range framework {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{}})
	}
	return document
}

func validClassification(videoType string) *Classification {
	evidenceChunkIDs := []string{"chunk-1"}
	if videoType == "meeting" {
		evidenceChunkIDs = append(evidenceChunkIDs, "chunk-2")
	}
	return &Classification{Confidence: 0.9, Reason: "转写内容符合该视频类型", EvidenceChunkIDs: evidenceChunkIDs}
}

func TestValidateRequiresExactFramework(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "general", Classification: validClassification("general"), Sections: []Section{
		{ID: "positioning-problem", Title: "一、定位与问题", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "问题", EvidenceChunkIDs: []string{"chunk-1"}}}},
		{ID: "claims-reasoning", Title: "二、主张与论证", Blocks: []Block{{ID: "b2", Kind: BlockKindParagraph, Text: "主张", EvidenceChunkIDs: []string{"chunk-1"}}}},
		{ID: "evidence-cases", Title: "三、证据与案例", Blocks: []Block{{ID: "b3", Kind: BlockKindParagraph, Text: "证据", EvidenceChunkIDs: []string{"chunk-1"}}}},
		{ID: "limitations-counterarguments", Title: "四、限定与反方", Blocks: []Block{{ID: "b4", Kind: BlockKindParagraph, Text: "限定", EvidenceChunkIDs: []string{"chunk-1"}}}},
		{ID: "impact-recommendations", Title: "五、影响与建议", Blocks: []Block{{ID: "b5", Kind: BlockKindParagraph, Text: "建议", EvidenceChunkIDs: []string{"chunk-1"}}}},
	}}
	if err := Validate(document, "general", map[string]struct{}{"chunk-1": {}}); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
	document.Sections[0].Title = "自定义标题"
	if err := Validate(document, "general", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("Validate accepted a non-canonical section title")
	}
}

func TestValidateAllowsSectionsWithoutSupportedEvidence(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "training", Classification: validClassification("training"), Sections: make([]Section, 0, len(frameworks["training"]))}
	for _, section := range frameworks["training"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{}})
	}
	if err := Validate(document, "training", nil); err != nil {
		t.Fatalf("Validate rejected empty evidence-free sections: %v", err)
	}
}

func TestParseStoredMigratesLegacyTrainingFramework(t *testing.T) {
	legacy := Document{SchemaVersion: legacySchemaVersion, VideoType: "training", Sections: make([]Section, 0, len(legacyTrainingFramework))}
	for _, section := range legacyTrainingFramework {
		legacy.Sections = append(legacy.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{}})
	}
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy training summary: %v", err)
	}
	document, err := ParseStored(string(payload))
	if err != nil {
		t.Fatalf("ParseStored returned error: %v", err)
	}
	if document.Sections[0].Title != "一、学习目标、适用对象与前置知识" || document.Sections[5].Title != "六、练习、自测与应用清单" {
		t.Fatalf("legacy training framework was not migrated: %+v", document.Sections)
	}
	if err := ValidateStored(document, "training"); err != nil {
		t.Fatalf("migrated legacy training summary is invalid: %v", err)
	}
}

func TestResolveEvidenceReturnsOriginalTextAndTimestamp(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "general", Sections: []Section{{
		ID:     "positioning-problem",
		Title:  "一、定位与问题",
		Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "观点", EvidenceChunkIDs: []string{"chunk-1"}}},
	}}}
	for _, section := range frameworks["general"][1:] {
		document.Sections = append(document.Sections, Section{
			ID:    section.ID,
			Title: section.Title,
			Blocks: []Block{{
				ID:               section.ID,
				Kind:             BlockKindParagraph,
				Text:             "内容",
				EvidenceChunkIDs: []string{"chunk-1"},
			}},
		})
	}
	chunks := []transcript.Chunk{{ID: "chunk-1", EvidenceSentenceID: "evs:v1:abc", StartMs: 605000, EndMs: 620500, Content: "## 视频定位信息\n\n## 原文\n\n真实原文"}}
	if err := ResolveEvidence(&document, chunks); err != nil {
		t.Fatalf("ResolveEvidence returned error: %v", err)
	}
	evidence := document.Sections[0].Blocks[0].Evidence[0]
	if evidence.Timestamp != "10:05–10:20" || evidence.TranscriptSnippet != "真实原文" {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}
	if evidence.EvidenceSentenceID != "evs:v1:abc" {
		t.Fatalf("unexpected evidence sentence ID: %s", evidence.EvidenceSentenceID)
	}
	ref := document.Sections[0].Blocks[0].EvidenceRefs[0]
	if ref.ChunkID != "chunk-1" || ref.EvidenceSentenceID != "evs:v1:abc" || ref.StartMs != 605000 || ref.EndMs != 620500 {
		t.Fatalf("unexpected evidence reference: %+v", ref)
	}
}

func TestValidateEnhancementPreservesStructureAndEvidence(t *testing.T) {
	base := Document{SchemaVersion: 1, VideoType: "general", Sections: []Section{{ID: "s1", Title: "一", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "初版", EvidenceChunkIDs: []string{"c1"}}}}}}
	enhanced := base
	enhanced.Sections = []Section{{ID: "s1", Title: "一", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "增强", EvidenceChunkIDs: []string{"c1"}, KnowledgeRefs: []string{"wiki-1"}}}}}
	if err := ValidateEnhancement(base, enhanced); err != nil {
		t.Fatalf("ValidateEnhancement returned error: %v", err)
	}
}

func TestValidateEnhancementRejectsChangedEvidenceAnchor(t *testing.T) {
	base := Document{SchemaVersion: 1, VideoType: "general", Sections: []Section{{ID: "s1", Title: "一", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "初版", EvidenceChunkIDs: []string{"c1"}}}}}}
	enhanced := base
	enhanced.Sections = []Section{{ID: "s1", Title: "一", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "增强", EvidenceChunkIDs: []string{"c2"}}}}}
	if err := ValidateEnhancement(base, enhanced); err == nil {
		t.Fatal("changed evidence anchor should be rejected")
	}
}

func TestValidateOrchestrationProfileAcceptsBoundedReferences(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "training", Classification: validClassification("training"), Sections: make([]Section, 0, len(frameworks["training"]))}
	for index, section := range frameworks["training"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: fmt.Sprintf("block-%d", index+1), Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}}}})
	}
	document.OrchestrationProfile = &OrchestrationProfile{
		SchemaVersion: 1,
		PrimaryTopic:  "MCP 基础与使用",
		TopicUnits: []OrchestrationTopicUnit{{
			Title: "基础概念", Abstract: "说明核心角色和适用范围", ContentForms: []string{"concept_cognition"},
			LearningOutcomes: []string{"能够说明核心作用"}, SummaryBlockIDs: []string{"block-1"}, EvidenceChunkIDs: []string{"chunk-1"},
		}},
	}
	if err := Validate(document, "training", map[string]struct{}{"chunk-1": {}}); err != nil {
		t.Fatalf("Validate returned error: %v", err)
	}
}

func TestValidateGeneratedRequiresOrchestrationProfile(t *testing.T) {
	document := emptyFrameworkDocument(SchemaVersion, "general", frameworks["general"])
	document.Classification = validClassification("general")
	if err := ValidateGenerated(document, "general", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("ValidateGenerated accepted a missing orchestration profile")
	}
	document.OrchestrationProfile = &OrchestrationProfile{
		SchemaVersion: 1, PrimaryTopic: "主题",
		TopicUnits: []OrchestrationTopicUnit{{
			Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"},
			LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{}, EvidenceChunkIDs: []string{},
		}},
	}
	// The profile remains subject to the normal completeness checks; this
	// assertion only verifies the missing-profile gate above.
	if err := ValidateGenerated(document, "general", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("ValidateGenerated accepted an incomplete orchestration profile")
	}
}

func TestNormalizeOrchestrationProfileReferencesProjectsEvidenceFromBlocks(t *testing.T) {
	document := Document{
		SchemaVersion: SchemaVersion,
		VideoType:     "general",
		Sections: []Section{
			{ID: "positioning-problem", Title: "一、定位与问题", Blocks: []Block{{
				ID: "block-1", Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1", "chunk-2"},
			}}},
		},
		OrchestrationProfile: &OrchestrationProfile{
			SchemaVersion: OrchestrationProfileSchemaVersion, PrimaryTopic: "主题",
			TopicUnits: []OrchestrationTopicUnit{{
				Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"},
				LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{"block-1"},
				EvidenceChunkIDs: []string{"chunk-1", "chunk-not-in-block"},
				EvidenceRefs:     []EvidenceRef{{ChunkID: "chunk-1", EvidenceSentenceID: "evs:old", StartMs: 1, EndMs: 2}},
			}},
		},
	}
	if err := NormalizeOrchestrationProfileReferences(&document, map[string]struct{}{"chunk-1": {}, "chunk-2": {}, "chunk-not-in-block": {}}); err != nil {
		t.Fatalf("NormalizeOrchestrationProfileReferences returned error: %v", err)
	}
	unit := document.OrchestrationProfile.TopicUnits[0]
	if len(unit.EvidenceChunkIDs) != 1 || unit.EvidenceChunkIDs[0] != "chunk-1" {
		t.Fatalf("unexpected normalized evidence IDs: %#v", unit.EvidenceChunkIDs)
	}
	if unit.EvidenceRefs != nil {
		t.Fatalf("expected evidence refs to be deferred to ResolveEvidence: %#v", unit.EvidenceRefs)
	}
}

func TestNormalizeOrchestrationProfileReferencesClosesMeetingEvidenceMismatch(t *testing.T) {
	sections := make([]Section, 0, len(frameworks["meeting"]))
	for index, frameworkSection := range frameworks["meeting"] {
		section := Section{ID: frameworkSection.ID, Title: frameworkSection.Title}
		if index == 0 {
			section.Blocks = []Block{{
				ID: "block-meeting-summary-1", Kind: BlockKindParagraph, Text: "会议形成了共识。",
				EvidenceChunkIDs: []string{"chunk-1"},
			}}
		}
		sections = append(sections, section)
	}
	document := Document{
		SchemaVersion: SchemaVersion, VideoType: "meeting", Sections: sections,
		Classification: &Classification{Confidence: 0.91, Reason: "会议形成了明确共识", EvidenceChunkIDs: []string{"chunk-1", "chunk-2"}},
		OrchestrationProfile: &OrchestrationProfile{
			SchemaVersion: OrchestrationProfileSchemaVersion, PrimaryTopic: "会议共识",
			TopicUnits: []OrchestrationTopicUnit{{
				Title: "共识", Abstract: "会议形成了共识", ContentForms: []string{"concept_cognition"},
				LearningOutcomes: []string{"能够说明会议共识"}, SummaryBlockIDs: []string{"block-meeting-summary-1"},
				// chunk-2 is valid transcript evidence but is not attached to the
				// selected block, matching the production failure shape.
				EvidenceChunkIDs: []string{"chunk-1", "chunk-2"},
			}},
		},
	}

	if err := NormalizeOrchestrationProfileReferences(&document, map[string]struct{}{"chunk-1": {}, "chunk-2": {}}); err != nil {
		t.Fatalf("NormalizeOrchestrationProfileReferences returned error: %v", err)
	}
	if err := ValidateGenerated(document, "meeting", map[string]struct{}{"chunk-1": {}, "chunk-2": {}}); err != nil {
		t.Fatalf("normalized meeting summary was rejected: %v", err)
	}
	if got := document.OrchestrationProfile.TopicUnits[0].EvidenceChunkIDs; len(got) != 1 || got[0] != "chunk-1" {
		t.Fatalf("unexpected normalized meeting evidence IDs: %#v", got)
	}
}

func TestNormalizeOrchestrationProfileReferencesFailsClosed(t *testing.T) {
	document := Document{
		Sections: []Section{{ID: "positioning-problem", Title: "一、定位与问题", Blocks: []Block{{
			ID: "block-1", EvidenceChunkIDs: []string{"chunk-1"},
		}}}},
		OrchestrationProfile: &OrchestrationProfile{TopicUnits: []OrchestrationTopicUnit{
			{SummaryBlockIDs: []string{"unknown-block"}, EvidenceChunkIDs: []string{"chunk-1"}},
		}},
	}
	if err := NormalizeOrchestrationProfileReferences(&document, map[string]struct{}{"chunk-1": {}}); err == nil || !strings.Contains(err.Error(), "unknown summary block") {
		t.Fatalf("expected unknown block error, got %v", err)
	}

	document.OrchestrationProfile.TopicUnits[0].SummaryBlockIDs = []string{"block-1"}
	document.OrchestrationProfile.TopicUnits[0].EvidenceChunkIDs = []string{"chunk-not-in-block"}
	if err := NormalizeOrchestrationProfileReferences(&document, map[string]struct{}{"chunk-1": {}, "chunk-not-in-block": {}}); err == nil || !strings.Contains(err.Error(), "no evidence") {
		t.Fatalf("expected empty projected evidence error, got %v", err)
	}

	document.OrchestrationProfile.TopicUnits[0].EvidenceChunkIDs = []string{"unknown-chunk"}
	if err := NormalizeOrchestrationProfileReferences(&document, map[string]struct{}{"chunk-1": {}}); err == nil || !strings.Contains(err.Error(), "unknown evidence chunk") {
		t.Fatalf("expected unknown evidence error, got %v", err)
	}
}

func TestResolveEvidencePopulatesOrchestrationEvidenceRefs(t *testing.T) {
	document := Document{
		SchemaVersion: SchemaVersion, VideoType: "general",
		Sections: []Section{{ID: "positioning-problem", Title: "一、定位与问题", Blocks: []Block{{ID: "block-1", Kind: BlockKindParagraph, Text: "观点", EvidenceChunkIDs: []string{"chunk-1"}}}}},
		OrchestrationProfile: &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{{
			Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
			SummaryBlockIDs: []string{"block-1"}, EvidenceChunkIDs: []string{"chunk-1"},
		}}},
	}
	for _, section := range frameworks["general"][1:] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title})
	}
	if err := ResolveEvidence(&document, []transcript.Chunk{{ID: "chunk-1", EvidenceSentenceID: "evs:v1:one", StartMs: 100, EndMs: 1200, Content: "## 原文\n\n观点"}}); err != nil {
		t.Fatalf("ResolveEvidence returned error: %v", err)
	}
	refs := document.OrchestrationProfile.TopicUnits[0].EvidenceRefs
	if len(refs) != 1 || refs[0].EvidenceSentenceID != "evs:v1:one" || refs[0].StartMs != 100 || refs[0].EndMs != 1200 {
		t.Fatalf("unexpected orchestration evidence refs: %+v", refs)
	}
}

func TestValidateOrchestrationProfileRejectsUnknownReferences(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "training", Classification: validClassification("training"), Sections: make([]Section, 0, len(frameworks["training"]))}
	for _, section := range frameworks["training"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: section.ID, Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}}}})
	}
	document.OrchestrationProfile = &OrchestrationProfile{
		SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{{
			Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
			SummaryBlockIDs: []string{"unknown-block"}, EvidenceChunkIDs: []string{"unknown-chunk"},
		}},
	}
	if err := Validate(document, "training", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("Validate accepted orchestration profile references outside the summary")
	}
}

func TestValidateOrchestrationProfileRejectsAmbiguousReferences(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "training", Classification: validClassification("training"), Sections: make([]Section, 0, len(frameworks["training"]))}
	for _, section := range frameworks["training"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: section.ID, Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}}}})
	}
	document.OrchestrationProfile = &OrchestrationProfile{
		SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{
			{Title: "单元一", Abstract: "摘要一", ContentForms: []string{"concept_cognition", "concept_cognition"}, LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{"training-content-system"}, EvidenceChunkIDs: []string{"chunk-1"}},
		},
	}
	if err := Validate(document, "training", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("Validate accepted duplicate content form")
	}

	document.OrchestrationProfile.TopicUnits[0].ContentForms = []string{"concept_cognition"}
	document.OrchestrationProfile.TopicUnits[0].EvidenceChunkIDs = []string{"unknown-chunk"}
	if err := Validate(document, "training", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("Validate accepted evidence not owned by summary blocks")
	}
}

func TestValidateOrchestrationProfileRejectsDuplicateSummaryBlockIDs(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "training", Classification: validClassification("training"), Sections: make([]Section, 0, len(frameworks["training"]))}
	for _, section := range frameworks["training"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: "same-block", Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}}}})
	}
	document.OrchestrationProfile = &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{{
		Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{"same-block"}, EvidenceChunkIDs: []string{"chunk-1"},
	}}}
	if err := Validate(document, "training", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("Validate accepted duplicate summary block IDs")
	}
}

func TestValidateEnhancementPreservesOrchestrationProfile(t *testing.T) {
	base := Document{SchemaVersion: 1, VideoType: "general", Sections: []Section{{ID: "s1", Title: "一", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "初版", EvidenceChunkIDs: []string{"c1"}}}}}}
	base.OrchestrationProfile = &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "原主题", TopicUnits: []OrchestrationTopicUnit{{Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{"b1"}, EvidenceChunkIDs: []string{"c1"}}}}
	enhanced := base
	enhanced.Sections = []Section{{ID: "s1", Title: "一", Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "增强正文", EvidenceChunkIDs: []string{"c1"}, KnowledgeRefs: []string{"wiki-1"}}}}}
	if err := ValidateEnhancement(base, enhanced); err != nil {
		t.Fatalf("ValidateEnhancement rejected unchanged orchestration profile: %v", err)
	}
}

func TestValidateEnhancementRejectsChangedOrchestrationProfile(t *testing.T) {
	base := Document{
		SchemaVersion: 1,
		VideoType:     "general",
		Sections: []Section{{
			ID: "s1", Title: "一",
			Blocks: []Block{{ID: "b1", Kind: BlockKindParagraph, Text: "初版", EvidenceChunkIDs: []string{"c1"}}},
		}},
	}
	base.OrchestrationProfile = &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "原主题", TopicUnits: []OrchestrationTopicUnit{{Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"}, SummaryBlockIDs: []string{"b1"}, EvidenceChunkIDs: []string{"c1"}}}}
	enhanced := base
	enhanced.OrchestrationProfile = &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "新主题", TopicUnits: base.OrchestrationProfile.TopicUnits}
	if err := ValidateEnhancement(base, enhanced); err == nil {
		t.Fatal("ValidateEnhancement accepted changed orchestration profile")
	}
}

func TestPreserveOrchestrationProfileRestoresResolvedEvidenceRefs(t *testing.T) {
	base := Document{OrchestrationProfile: &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{{
		Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
		SummaryBlockIDs: []string{"b1"}, EvidenceChunkIDs: []string{"c1"}, EvidenceRefs: []EvidenceRef{{ChunkID: "c1", EvidenceSentenceID: "evs:1", StartMs: 1, EndMs: 2}},
	}}}}
	enhanced := Document{OrchestrationProfile: &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{{
		Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
		SummaryBlockIDs: []string{"b1"}, EvidenceChunkIDs: []string{"c1"},
	}}}}
	if err := PreserveOrchestrationProfile(&base, &enhanced); err != nil {
		t.Fatalf("PreserveOrchestrationProfile returned error: %v", err)
	}
	if len(enhanced.OrchestrationProfile.TopicUnits[0].EvidenceRefs) != 1 {
		t.Fatalf("resolved evidence refs were not restored: %+v", enhanced.OrchestrationProfile)
	}
}

func TestParseAcceptsCamelCaseDoubleReferenceAliases(t *testing.T) {
	parsed, err := Parse(`{"schemaVersion":1,"videoType":"general","sections":[{"id":"positioning-problem","title":"一、定位与问题","blocks":[{"id":"block-1","kind":"paragraph","text":"内容","evidenceChunkIds":["chunk-1"],"knowledgeRefs":["wiki-1"],"evidenceRefs":[{"chunk_id":"chunk-1","evidence_sentence_id":"evs:v1:one","start_ms":100,"end_ms":1200}],"evidence":[{"chunkId":"chunk-1","evidenceSentenceId":"evs:v1:one","startSeconds":0.1,"endSeconds":1.2,"timestamp":"00:00–00:01","transcriptSnippet":"原文"}]}]},{"id":"claims-reasoning","title":"二、主张与论证","blocks":[{"id":"block-2","kind":"paragraph","text":"内容","evidenceChunkIds":["chunk-1"]}]},{"id":"evidence-cases","title":"三、证据与案例","blocks":[{"id":"block-3","kind":"paragraph","text":"内容","evidenceChunkIds":["chunk-1"]}]},{"id":"limitations-counterarguments","title":"四、限定与反方","blocks":[{"id":"block-4","kind":"paragraph","text":"内容","evidenceChunkIds":["chunk-1"]}]},{"id":"impact-recommendations","title":"五、影响与建议","blocks":[{"id":"block-5","kind":"paragraph","text":"内容","evidenceChunkIds":["chunk-1"]}]}]}`)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	block := parsed.Sections[0].Blocks[0]
	if len(block.KnowledgeRefs) != 1 || block.KnowledgeRefs[0] != "wiki-1" || len(block.EvidenceRefs) != 1 {
		t.Fatalf("double references were not parsed: %+v", block)
	}
}

func TestValidateStoredRejectsMismatchedEvidenceReference(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "general", Classification: validClassification("general"), Sections: make([]Section, 0, len(frameworks["general"]))}
	for _, section := range frameworks["general"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: section.ID, Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}, Evidence: []Evidence{{ChunkID: "chunk-1", EvidenceSentenceID: "evs:v1:one", StartSeconds: 0.1, EndSeconds: 1.2, Timestamp: "00:00–00:01", TranscriptSnippet: "原文"}}, EvidenceRefs: []EvidenceRef{{ChunkID: "chunk-1", EvidenceSentenceID: "evs:v1:one", StartMs: 999, EndMs: 1200}}}}})
	}
	if err := ValidateStored(document, "general"); err == nil {
		t.Fatal("ValidateStored accepted mismatched evidence reference")
	}
}

func TestParseStoredSkipsWikiFrontmatter(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "general", Classification: validClassification("general"), Sections: make([]Section, 0, len(frameworks["general"]))}
	for _, section := range frameworks["general"] {
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: section.ID, Kind: BlockKindParagraph, Text: "内容", EvidenceChunkIDs: []string{"chunk-1"}, Evidence: []Evidence{{ChunkID: "chunk-1", EvidenceSentenceID: "evs:v1:chunk-1", StartSeconds: 1, EndSeconds: 2, Timestamp: "00:01–00:02", TranscriptSnippet: "原文"}}}}})
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	parsed, err := ParseStored("---\ntype: typed_summary\nsource_video_id: video-1\n---\n\n" + string(payload))
	if err != nil {
		t.Fatalf("ParseStored returned error: %v", err)
	}
	if err := ValidateStored(parsed, "general"); err != nil {
		t.Fatalf("ValidateStored returned error: %v", err)
	}
	parsed.Sections[0].Blocks[0].Evidence[0].EvidenceSentenceID = ""
	if err := ValidateStored(parsed, "general"); err == nil {
		t.Fatal("ValidateStored accepted evidence without immutable sentence ID")
	}
}

func TestParseAcceptsLegacySnakeCaseSchemaVersion(t *testing.T) {
	parsed, err := Parse(`{"schema_version":1,"videoType":"general","sections":[]}`)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.SchemaVersion != legacySchemaVersion {
		t.Fatalf("expected schema version %d, got %d", legacySchemaVersion, parsed.SchemaVersion)
	}
	encoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("marshal parsed summary: %v", err)
	}
	if strings.Contains(string(encoded), `"schema_version"`) || !strings.Contains(string(encoded), `"schemaVersion":1`) {
		t.Fatalf("legacy schema field leaked into canonical output: %s", encoded)
	}
}

func TestCanonicalJSONUsesFrontendWireContractForEveryVideoType(t *testing.T) {
	chunk := transcript.Chunk{ID: "chunk-1", EvidenceSentenceID: "evs:v1:chunk-1", StartMs: 1000, EndMs: 2500, Content: "真实原文"}
	for videoType, framework := range frameworks {
		document := Document{SchemaVersion: SchemaVersion, VideoType: videoType, Classification: validClassification(videoType), Sections: make([]Section, 0, len(framework))}
		for _, section := range framework {
			document.Sections = append(document.Sections, Section{
				ID: section.ID, Title: section.Title,
				Blocks: []Block{{
					ID: "block-1", Kind: BlockKindBullet, Text: "可直接展示的内容",
					EvidenceChunkIDs: []string{chunk.ID},
				}},
			})
		}
		knownChunkIDs := map[string]struct{}{chunk.ID: {}}
		if videoType == "meeting" {
			knownChunkIDs["chunk-2"] = struct{}{}
		}
		if err := Validate(document, videoType, knownChunkIDs); err != nil {
			t.Fatalf("Validate(%s) returned error: %v", videoType, err)
		}
		if err := ResolveEvidence(&document, []transcript.Chunk{chunk}); err != nil {
			t.Fatalf("ResolveEvidence(%s) returned error: %v", videoType, err)
		}
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal(%s) returned error: %v", videoType, err)
		}
		wire := string(payload)
		for _, field := range []string{"schemaVersion", "videoType", "evidenceChunkIds", "knowledge_refs", "evidence_refs", "evidence_sentence_id", "start_ms", "end_ms", "chunkId", "startSeconds", "endSeconds", "transcriptSnippet"} {
			if !strings.Contains(wire, `"`+field+`"`) {
				t.Fatalf("%s payload is missing frontend field %q: %s", videoType, field, wire)
			}
		}
		if strings.Contains(wire, `"schema_version"`) || strings.Contains(wire, `"evidence_chunk_ids"`) {
			t.Fatalf("%s payload contains backend-only field names: %s", videoType, wire)
		}
	}
}

func TestMeetingClassificationRequiresTwoTranscriptChunks(t *testing.T) {
	document := Document{
		SchemaVersion: SchemaVersion,
		VideoType:     "meeting",
		Classification: &Classification{
			Confidence:       0.91,
			Reason:           "围绕工作事项形成决策和行动安排",
			EvidenceChunkIDs: []string{"chunk-1"},
		},
	}
	if err := ValidateClassification(document, map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("expected meeting classification to require evidence from two chunks")
	}
	document.Classification.EvidenceChunkIDs = []string{"chunk-1", "chunk-2"}
	if err := ValidateClassification(document, map[string]struct{}{"chunk-1": {}, "chunk-2": {}}); err != nil {
		t.Fatalf("ValidateClassification returned error: %v", err)
	}
}

func TestMeetingFrameworkMatchesEightSectionSkillContract(t *testing.T) {
	framework, ok := Framework("meeting")
	if !ok {
		t.Fatal("meeting framework is missing")
	}
	want := []FrameworkSection{
		{ID: "meeting-summary", Title: "一、会议总结"},
		{ID: "meeting-basic-information", Title: "二、会议基本信息"},
		{ID: "key-topics-consensus", Title: "三、关键议题和共识"},
		{ID: "meeting-disagreements", Title: "四、会议分歧点"},
		{ID: "discussion-details", Title: "五、会议讨论详情"},
		{ID: "action-items", Title: "六、待办事项"},
		{ID: "deferred-topics", Title: "七、遗留和搁置议题"},
		{ID: "other", Title: "八、其他"},
	}
	if len(framework) != len(want) {
		t.Fatalf("meeting framework section count = %d, want %d", len(framework), len(want))
	}
	for index := range want {
		if framework[index] != want[index] {
			t.Fatalf("meeting framework section %d = %+v, want %+v", index+1, framework[index], want[index])
		}
	}
}

func TestNormalizeEvidenceChunkIDsAcceptsPromptAliases(t *testing.T) {
	document := Document{
		SchemaVersion: SchemaVersion,
		VideoType:     "general",
		OrchestrationProfile: &OrchestrationProfile{SchemaVersion: 1, PrimaryTopic: "主题", TopicUnits: []OrchestrationTopicUnit{{
			Title: "单元", Abstract: "摘要", ContentForms: []string{"concept_cognition"}, LearningOutcomes: []string{"结果"},
			SummaryBlockIDs: []string{"block-1"}, EvidenceChunkIDs: []string{"chunk-1|000004"},
		}}},
		Sections: []Section{{
			ID: "positioning-problem", Title: "一、定位与问题",
			Blocks: []Block{{ID: "block-1", EvidenceChunkIDs: []string{"chunk-1|000004", "unknown|000004"}}},
		}},
	}
	NormalizeEvidenceChunkIDs(&document, []transcript.Chunk{{ID: "chunk-1", Index: 4}})
	if got := document.Sections[0].Blocks[0].EvidenceChunkIDs[0]; got != "chunk-1" {
		t.Fatalf("normalized evidence chunk ID = %q", got)
	}
	if got := document.Sections[0].Blocks[0].EvidenceChunkIDs[1]; got != "unknown|000004" {
		t.Fatalf("unknown evidence chunk ID should remain unchanged, got %q", got)
	}
	if got := document.OrchestrationProfile.TopicUnits[0].EvidenceChunkIDs[0]; got != "chunk-1" {
		t.Fatalf("profile evidence chunk ID = %q", got)
	}
}

func TestValidateRejectsMarkdownBlockText(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, VideoType: "general", Classification: validClassification("general"), Sections: make([]Section, 0, len(frameworks["general"]))}
	for index, section := range frameworks["general"] {
		text := "内容"
		if index == 0 {
			text = "**不应渲染为 Markdown**"
		}
		document.Sections = append(document.Sections, Section{ID: section.ID, Title: section.Title, Blocks: []Block{{ID: section.ID, Kind: BlockKindParagraph, Text: text, EvidenceChunkIDs: []string{"chunk-1"}}}})
	}
	if err := Validate(document, "general", map[string]struct{}{"chunk-1": {}}); err == nil {
		t.Fatal("Validate accepted Markdown block text")
	}
}
