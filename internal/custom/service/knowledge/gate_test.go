package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestGateAcceptsEntityWithTwoValidFieldsAndMarksPassed(t *testing.T) {
	object := splitInput("entity-pass", "Context Machine", TypeEntity, map[string]string{
		"product_type": "可穿戴硬件", "core_function": "记录上下文", "claim": "不得计入",
	}, "ev-entity")
	object.EntitySubType = "product"
	decision := GateClassifiedKnowledge(object)
	if decision.Status != PublishGatePassed || decision.Object == nil || decision.Rejected != nil {
		t.Fatalf("decision = %#v, want passed object", decision)
	}
	if decision.Object.AuditStatus != "passed" {
		t.Fatalf("audit_status = %q, want passed", decision.Object.AuditStatus)
	}
}

func TestGateRequiresTwoFieldsFromTheTypeFramework(t *testing.T) {
	tests := []struct {
		name   string
		kind   KnowledgeType
		fields map[string]string
	}{
		{"concept", TypeConcept, map[string]string{"definition": "是什么"}},
		{"methodology", TypeMethodology, map[string]string{"steps": "执行"}},
		{"case", TypeCase, map[string]string{"context": "背景"}},
		{"insight", TypeInsight, map[string]string{"claim": "判断"}},
		{"entity", TypeEntity, map[string]string{"identity": "人物"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			object := splitInput("gate-"+tt.name, tt.name, tt.kind, tt.fields, "ev-"+tt.name)
			if tt.kind == TypeEntity {
				object.EntitySubType = "person"
			}
			decision := GateClassifiedKnowledge(object)
			if decision.Status != PublishGateRejected || decision.Rejected == nil {
				t.Fatalf("decision = %#v, want rejected", decision)
			}
			if !strings.Contains(decision.Reason, "at least 2 valid structure fields") {
				t.Fatalf("reason = %q", decision.Reason)
			}
		})
	}
}

func TestGateRejectsConfidenceOutsideRange(t *testing.T) {
	object := splitInput("confidence-zero", "网络效应", TypeConcept, map[string]string{
		"definition": "用户增加提升产品价值", "mechanism": "连接关系增加效用",
	}, "ev-confidence")
	object.ClassificationConfidence = 0
	decision := GateClassifiedKnowledge(object)
	if decision.Status != PublishGateRejected || decision.Rejected == nil {
		t.Fatalf("decision = %#v, want rejected", decision)
	}
}

func TestApplyPublishGateSeparatesResultsAndIsByteStable(t *testing.T) {
	valid := splitInput("b-pass", "概念", TypeConcept, map[string]string{
		"definition": "定义", "mechanism": "机制",
	}, "ev-pass")
	invalid := splitInput("a-reject", "普通词", TypeConcept, map[string]string{
		"definition": "只有一句",
	}, "ev-reject")
	first := ApplyPublishGate([]ClassifiedKnowledge{valid, invalid})
	second := ApplyPublishGate([]ClassifiedKnowledge{valid, invalid})
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("gate result changed bytes:\nfirst=%s\nsecond=%s", firstJSON, secondJSON)
	}
	if len(first.Passed) != 1 || len(first.Rejected) != 1 || first.Passed[0].AuditStatus != "passed" {
		t.Fatalf("batch = %#v, want one passed and one rejected", first)
	}
	if strings.Contains(string(firstJSON), "wiki_page_id") {
		t.Fatal("P3-04 gate artifact must not contain Wiki page IDs")
	}
}

func TestGateDoesNotCountForeignFields(t *testing.T) {
	object := splitInput("foreign-only", "概念", TypeConcept, map[string]string{
		"identity": "人物", "context": "背景", "definition": "定义",
	}, "ev-foreign")
	decision := GateClassifiedKnowledge(object)
	if decision.Status != PublishGateRejected {
		t.Fatalf("decision = %#v, foreign fields should not satisfy concept gate", decision)
	}
}

func TestGateResultJSONHasExplicitStatus(t *testing.T) {
	object := splitInput("json-pass", "概念", TypeConcept, map[string]string{
		"definition": "定义", "mechanism": "机制",
	}, "ev-json")
	encoded, err := json.Marshal(GateClassifiedKnowledge(object))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"status":"passed"`)) {
		t.Fatalf("encoded gate result = %s", encoded)
	}
}
