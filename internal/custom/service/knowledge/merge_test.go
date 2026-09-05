package knowledge

import (
	"strings"
	"testing"
)

func TestMapSkillToKnowledgeTypeSupportsSkillOntology(t *testing.T) {
	tests := map[string]KnowledgeType{
		"methodology":  TypeMethodology,
		"case":         TypeCase,
		"concept":      TypeConcept,
		"insight":      TypeInsight,
		"entity":       TypeEntity,
		"person":       TypeEntity,
		"organization": TypeEntity,
		"product":      TypeEntity,
		"technology":   TypeEntity,
		"industry":     TypeEntity,
		"place":        TypeEntity,
	}
	for input, want := range tests {
		if got := MapSkillToKnowledgeType(input); got != want {
			t.Fatalf("MapSkillToKnowledgeType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMergeAnchorsDropsUnsupportedTypes(t *testing.T) {
	merged := MergeAnchors([]AnchorItem{
		{ID: "entity-1", Type: TypeEntity},
		{ID: "unknown-1", Type: KnowledgeType("unknown")},
	}, nil)

	if len(merged[TypeEntity]) != 1 || merged[TypeEntity][0].ID != "entity-1" {
		t.Fatalf("entity anchors = %#v", merged[TypeEntity])
	}
	if _, ok := merged[KnowledgeType("unknown")]; ok {
		t.Fatalf("unsupported type should not be returned: %#v", merged)
	}
}

func TestMapPageTypeToKnowledgeTypeUsesOnlySupportedWeKnoraPageTypes(t *testing.T) {
	if got := MapPageTypeToKnowledgeType("summary", "concept"); got != "" {
		t.Fatalf("unsupported page type mapped to %q", got)
	}
	if got := MapPageTypeToKnowledgeType("index", "concept"); got != TypeConcept {
		t.Fatalf("index concept mapped to %q", got)
	}
	for _, pageType := range []string{"case", "methodology", "insight"} {
		if got := MapPageTypeToKnowledgeType(pageType, pageType); got != "" {
			t.Fatalf("unsupported %s page mapped to %q", pageType, got)
		}
	}
}

func TestValidateWikiObjectPageEnforcesFiveTypeContract(t *testing.T) {
	content := `---
knowledge_object_id: object-1
type: methodology
primary_type: methodology
information_nature: 方法论
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.92
evidence_ids: [chunk-1]
source_refs: [source-document-1]
structure_fields:
  input: 留存曲线
  steps: 按渠道拆分并对比异常
---
# 异常归因方法

核心内容：通过异常数据定位业务原因。`

	result, err := ValidateWikiObjectPage(content, "index", "video-1", "generation-1")
	if err != nil {
		t.Fatalf("ValidateWikiObjectPage returned error: %v", err)
	}
	if result.KnowledgeType != TypeMethodology || result.KnowledgeObjectID != "object-1" {
		t.Fatalf("validation result = %#v", result)
	}

	for _, invalid := range []string{
		"---\ntype: method\n---\n# invalid",
		"---\nknowledge_object_id: object-mismatch\ntype: concept\nprimary_type: insight\nsource_video_id: video-1\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.9\nevidence_ids: [chunk-1]\nsource_refs: [chunk-1]\nstructure_fields:\n  claim: 判断\n  reasoning: 依据\n---\n# invalid",
		"---\ntype: methodology\nsource_video_id: video-1\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.9\nevidence_ids: [chunk-1]\nsource_refs: [chunk-1]\nstructure_fields:\n  input: only-one\n---\n# invalid",
		"---\nknowledge_object_id: object-2\ntype: concept\ntypes: [concept, insight]\nsource_video_id: video-1\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.9\nevidence_ids: [chunk-1]\nsource_refs: [chunk-1]\nstructure_fields:\n  definition: 概念\n  mechanism: 机制\n---\n# invalid",
		"---\nknowledge_object_id: object-3\ntype: concept\nsource_video_id: video-1\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.9\nevidence_ids: [chunk-1]\nsource_refs: [chunk-1]\nstructure_fields:\n  definition: 概念\n  mechanism: 机制\n  claim: 洞察字段\n---\n# invalid",
	} {
		if _, err := ValidateWikiObjectPage(invalid, "index", "video-1", "generation-1"); err == nil {
			t.Fatalf("expected invalid Wiki object to fail: %s", invalid)
		}
	}
}

func TestValidateWikiObjectPageSeparatesEvidenceIDsFromSourceRefs(t *testing.T) {
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.92
evidence_ids: [transcript-chunk-1]
source_refs: [source-document-1]
structure_fields:
  definition: 稳定定义
  mechanism: 运行机制
---
# 网络效应`
	result, err := ValidateWikiObjectPage(content, "index", "video-1", "generation-1")
	if err != nil {
		t.Fatalf("distinct source and evidence IDs must be accepted: %v", err)
	}
	if result.SourceRefs[0] != "source-document-1" {
		t.Fatalf("source refs = %v", result.SourceRefs)
	}
	missing := strings.Replace(content, "source_refs: [source-document-1]", "source_refs: []", 1)
	if _, err := ValidateWikiObjectPage(missing, "index", "video-1", "generation-1"); err == nil || !strings.Contains(err.Error(), "source document ID") {
		t.Fatalf("empty source_refs must be rejected, got %v", err)
	}
}

func TestValidateWikiObjectPageRejectsDeclaredNonIndexPageType(t *testing.T) {
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
page_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.92
evidence_ids: [chunk-1]
source_refs: [chunk-1]
structure_fields:
  definition: 稳定定义
  mechanism: 运行机制
---
# 网络效应`
	_, err := ValidateWikiObjectPage(content, "index", "video-1", "generation-1")
	if err == nil || !strings.Contains(err.Error(), "frontmatter page_type") {
		t.Fatalf("expected declared non-index page_type to fail, got %v", err)
	}
}

func TestValidateWikiObjectPageRejectsInformationNatureAndEntitySubtypeMismatch(t *testing.T) {
	content := `---
knowledge_object_id: object-entity
type: entity
primary_type: entity
entity_sub_type: person
information_nature: 产品
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.92
evidence_ids: [chunk-1]
source_refs: [chunk-1]
structure_fields:
  identity: 创业者
---
# 张三`
	if _, err := ValidateWikiObjectPage(content, "index", "video-1", "generation-1"); err == nil || !strings.Contains(err.Error(), "information_nature") {
		t.Fatalf("expected information_nature mismatch to fail, got %v", err)
	}
	invalidSubtype := strings.Replace(content, "entity_sub_type: person", "entity_sub_type: unknown", 1)
	invalidSubtype = strings.Replace(invalidSubtype, "information_nature: 产品", "information_nature: 人物", 1)
	if _, err := ValidateWikiObjectPage(invalidSubtype, "index", "video-1", "generation-1"); err == nil || !strings.Contains(err.Error(), "entity_sub_type") {
		t.Fatalf("expected invalid entity_sub_type to fail, got %v", err)
	}
}

func TestCompareIdentityDoesNotMergeSameNameAcrossTypesOrContexts(t *testing.T) {
	base := IdentityCandidate{
		KnowledgeObjectID:    "object-1",
		KnowledgeType:        TypeConcept,
		Title:                "网络效应",
		SourceVideoID:        "video-1",
		TranscriptGeneration: "generation-1",
		StructureFields:      map[string]string{"definition": "用户增加提升产品价值", "mechanism": "连接关系增加效用"},
		EvidenceIDs:          []string{"chunk-1"},
	}
	sameNameDifferentType := base
	sameNameDifferentType.KnowledgeType = TypeInsight
	if got := CompareIdentity(base, sameNameDifferentType); got.Decision != IdentityConflict {
		t.Fatalf("different types decision = %#v", got)
	}

	person := IdentityCandidate{
		KnowledgeType:        TypeEntity,
		EntitySubType:        "person",
		Title:                "Context",
		StructureFields:      map[string]string{"identity": "创业者"},
		EvidenceIDs:          []string{"chunk-1"},
		SourceVideoID:        "video-1",
		TranscriptGeneration: "generation-1",
	}
	product := person
	product.EntitySubType = "product"
	product.StructureFields = map[string]string{"product_type": "AI 产品"}
	if got := CompareIdentity(person, product); got.Decision != IdentityConflict {
		t.Fatalf("different entity subtype decision = %#v", got)
	}

	sameNameDifferentContext := base
	sameNameDifferentContext.StructureFields = map[string]string{"definition": "另一行业中的独立术语", "mechanism": "完全不同的运行机制"}
	sameNameDifferentContext.EvidenceIDs = []string{"chunk-9"}
	if got := CompareIdentity(base, sameNameDifferentContext); got.Decision != IdentitySeparate {
		t.Fatalf("different contexts decision = %#v", got)
	}

	sameObject := base
	if got := CompareIdentity(base, sameObject); got.Decision != IdentityReuse {
		t.Fatalf("same object decision = %#v", got)
	}
}
