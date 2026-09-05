package knowledge

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestClassifyAssignsOnePrimaryTypeForFiveTypesAndSixEntitySubtypes(t *testing.T) {
	tests := []struct {
		name          string
		fields        map[string]string
		primaryType   KnowledgeType
		entitySubType string
	}{
		{name: "person", fields: map[string]string{"identity": "投资人", "standpoint": "强调长期价值"}, primaryType: TypeEntity, entitySubType: "person"},
		{name: "organization", fields: map[string]string{"org_type": "投资机构", "core_business": "早期科技投资"}, primaryType: TypeEntity, entitySubType: "organization"},
		{name: "product", fields: map[string]string{"product_type": "可穿戴硬件", "core_function": "记录上下文"}, primaryType: TypeEntity, entitySubType: "product"},
		{name: "technology", fields: map[string]string{"tech_category": "人工智能", "application_area": "任务执行"}, primaryType: TypeEntity, entitySubType: "technology"},
		{name: "industry", fields: map[string]string{"scope": "一级市场早期投资", "key_trends": "AI 原生组织"}, primaryType: TypeEntity, entitySubType: "industry"},
		{name: "place", fields: map[string]string{"place_type": "城市", "associated_activity": "项目所在地"}, primaryType: TypeEntity, entitySubType: "place"},
		{name: "concept", fields: map[string]string{"definition": "用户增加提升产品价值", "mechanism": "连接关系增加效用"}, primaryType: TypeConcept},
		{name: "methodology", fields: map[string]string{"input": "项目材料", "steps": "比较团队与产品"}, primaryType: TypeMethodology},
		{name: "case", fields: map[string]string{"context": "早期项目评估", "actors": "投资团队", "actions": "评估产品"}, primaryType: TypeCase},
		{name: "insight", fields: map[string]string{"claim": "应用层仍被低估", "reasoning": "投入未转化为收入"}, primaryType: TypeInsight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate, context := classifierInput(tt.name, tt.fields)
			classified, err := Classify(candidate, context)
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			if classified.PrimaryType != tt.primaryType || classified.EntitySubType != tt.entitySubType {
				t.Fatalf("got type=%q subtype=%q, want type=%q subtype=%q", classified.PrimaryType, classified.EntitySubType, tt.primaryType, tt.entitySubType)
			}
			if err := classified.Validate(); err != nil {
				t.Fatalf("invalid classified result: %v", err)
			}
		})
	}
}

func TestClassifyIsByteStableAcrossRepeatedRuns(t *testing.T) {
	candidate, context := classifierInput("stable", map[string]string{
		"definition": "用户增加提升产品价值", "mechanism": "连接关系增加效用", "ignored": "保留但不参与分类",
	})

	var previous []byte
	for run := 0; run < 50; run++ {
		classified, err := Classify(candidate, context)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		encoded, err := json.Marshal(classified)
		if err != nil {
			t.Fatal(err)
		}
		if run > 0 && !bytes.Equal(previous, encoded) {
			t.Fatalf("run %d produced different bytes:\nprevious=%s\ncurrent=%s", run, previous, encoded)
		}
		previous = encoded
	}
}

func TestClassifyUsesContextOnlyForIdentityAndEvidenceValidation(t *testing.T) {
	candidate, context := classifierInput("context-isolation", map[string]string{"claim": "应用层仍被低估", "reasoning": "收入未同步增长"})
	first, err := Classify(candidate, context)
	if err != nil {
		t.Fatal(err)
	}

	context.Summary = "与候选无关的文档摘要变化，不应改变分类。"
	second, err := Classify(candidate, context)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("document summary changed classification:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
}

func TestClassifyResolvesConflictingSchemasWithStableSingleType(t *testing.T) {
	candidate, context := classifierInput("conflict", map[string]string{
		"context": "具体项目", "actors": "团队", "claim": "应调整策略", "reasoning": "复盘数据",
	})
	classified, err := Classify(candidate, context)
	if err != nil {
		t.Fatal(err)
	}
	if classified.PrimaryType != TypeCase || classified.EntitySubType != "" {
		t.Fatalf("got type=%q subtype=%q, want case without subtype", classified.PrimaryType, classified.EntitySubType)
	}
	if classified.ClassificationConfidence != 0.5 {
		t.Fatalf("got confidence=%v, want 0.5", classified.ClassificationConfidence)
	}
}

func TestClassifyRejectsCandidatesWithoutRecognizedStructure(t *testing.T) {
	candidate, context := classifierInput("unclassified", map[string]string{"note": "无可识别结构"})
	if _, err := Classify(candidate, context); err == nil {
		t.Fatal("expected unclassified candidate to be rejected")
	}
}

func classifierInput(id string, fields map[string]string) (Candidate, DocumentContext) {
	evidenceID := "ev-" + id
	candidate := Candidate{
		ID: id, SourceDocumentID: "doc-1", SourceVideoID: "video-1", TranscriptGeneration: "gen-1",
		Title: "稳定候选", CoreContent: "这是可独立理解的候选摘要。", StructureFields: fields,
		Citations: []CandidateCitation{{CitationID: "cite-" + id, EvidenceIDs: []string{evidenceID}}}, EvidenceIDs: []string{evidenceID},
	}
	context := DocumentContext{
		SourceDocumentID: "doc-1", SourceVideoID: "video-1", TranscriptGeneration: "gen-1", Summary: "整篇源文档摘要。",
		CandidateCitations: []CandidateCitation{{CitationID: "cite-" + id, EvidenceIDs: []string{evidenceID}}}, EvidenceIDs: []string{evidenceID},
	}
	return candidate, context
}
