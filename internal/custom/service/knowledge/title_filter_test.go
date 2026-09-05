package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanTitleRemovesWikiMarkdownTemplateAndQuestionMarkers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "heading-template-question", raw: "## 知识点：**什么是网络效应？**", want: "网络效应"},
		{name: "chapter-and-link", raw: "第 3 章： [早期项目判断框架](https://example.com)", want: "早期项目判断框架"},
		{name: "chinese-number-and-transition", raw: "一、由此可见：AI 智能体的应用领域", want: "AI 智能体的应用领域"},
		{name: "wiki-link-display", raw: "标题：[[Context Machine|Context Machine]]", want: "Context Machine"},
		{name: "question-suffix", raw: "网络效应是什么？", want: "网络效应"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := CleanTitle(tt.raw)
			if reason != "" {
				t.Fatalf("CleanTitle rejected %q: %s", tt.raw, reason)
			}
			if got != tt.want {
				t.Fatalf("CleanTitle(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if got != strings.TrimSpace(got) || strings.ContainsAny(got, "\r\n\t？?") {
				t.Fatalf("cleaned title still contains unstable whitespace/question marker: %q", got)
			}
		})
	}
}

func TestFilterClassifiedKnowledgeKeepsStableTitleWithoutChangingEvidenceOrFields(t *testing.T) {
	object := splitInput("title-clean", "### 概念：**网络效应？**", TypeConcept, map[string]string{
		"definition": "用户增加提升产品价值",
		"mechanism":  "连接关系增加效用",
	}, "ev-title-clean")
	object.AuditStatus = "passed"
	beforeFields := mustJSON(t, object.StructureFields)
	beforeEvidence := mustJSON(t, object.EvidenceIDs)
	beforeCoreContent := object.CoreContent

	decision := FilterClassifiedKnowledge(object)
	if decision.Status != TitleFilterPassed || decision.Object == nil || decision.Rejected != nil {
		t.Fatalf("decision = %#v, want passed object", decision)
	}
	if decision.Object.Title != "网络效应" {
		t.Fatalf("cleaned title = %q, want %q", decision.Object.Title, "网络效应")
	}
	if decision.Object.CoreContent != beforeCoreContent {
		t.Fatalf("title filter changed core_content: %q", decision.Object.CoreContent)
	}
	if !bytes.Equal(beforeFields, mustJSON(t, decision.Object.StructureFields)) {
		t.Fatalf("title filter changed structure_fields")
	}
	if !bytes.Equal(beforeEvidence, mustJSON(t, decision.Object.EvidenceIDs)) {
		t.Fatalf("title filter changed evidence_ids")
	}
	if object.Title != "### 概念：**网络效应？**" {
		t.Fatalf("title filter mutated input object title: %q", object.Title)
	}
}

func TestFilterClassifiedKnowledgeRejectsNonObjectsAndUnpublishableStatus(t *testing.T) {
	tests := []struct {
		name   string
		title  string
		reason string
		status string
	}{
		{name: "chapter-marker", title: "第 3 章", reason: "chapter_marker", status: "passed"},
		{name: "transition-only", title: "下面我们来看看", reason: "transition_only", status: "passed"},
		{name: "context-dependent", title: "本视频内容", reason: "context_dependent_title", status: "passed"},
		{name: "ordinary-word", title: "实", reason: "ordinary_word_meaning", status: "passed"},
		{name: "placeholder", title: "待定", reason: "temporary_title", status: "passed"},
		{name: "generic-label", title: "知识点", reason: "generic_title", status: "passed"},
		{name: "generic-type-label", title: "产品介绍", reason: "generic_title", status: "passed"},
		{name: "english-question", title: "What is network effect?", reason: "question_title", status: "passed"},
		{name: "pending-object", title: "网络效应", reason: "audit_status_not_passed", status: "pending"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			object := splitInput("reject-"+tt.name, tt.title, TypeConcept, map[string]string{
				"definition": "用户增加提升产品价值",
				"mechanism":  "连接关系增加效用",
			}, "ev-"+tt.name)
			object.AuditStatus = tt.status

			decision := FilterClassifiedKnowledge(object)
			if decision.Status != TitleFilterRejected || decision.Object != nil || decision.Rejected == nil {
				t.Fatalf("decision = %#v, want rejected record", decision)
			}
			if decision.Reason != tt.reason || decision.Rejected.Reason != tt.reason {
				t.Fatalf("reason = %q/%q, want %q", decision.Reason, decision.Rejected.Reason, tt.reason)
			}
			if len(decision.Rejected.EvidenceIDs) != 1 || decision.Rejected.EvidenceIDs[0] != "ev-"+tt.name {
				t.Fatalf("rejected evidence = %#v", decision.Rejected.EvidenceIDs)
			}
		})
	}
}

func TestFilterClassifiedKnowledgeRechecksPublishGate(t *testing.T) {
	object := splitInput("bypass-gate", "网络效应", TypeConcept, map[string]string{
		"definition": "用户增加提升产品价值",
	}, "ev-bypass-gate")
	object.AuditStatus = "passed"

	decision := FilterClassifiedKnowledge(object)
	if decision.Status != TitleFilterRejected || decision.Rejected == nil {
		t.Fatalf("decision = %#v, want rejected object that bypasses P3-04", decision)
	}
	if !strings.Contains(decision.Reason, "at least 2 valid structure fields") {
		t.Fatalf("reason = %q, want minimum structure rejection", decision.Reason)
	}
}

func TestFilterClassifiedKnowledgePreservesNonTitleFieldsExactly(t *testing.T) {
	object := splitInput("preserve-fields", "网络效应", TypeConcept, map[string]string{
		"definition": "  用户增加提升产品价值  ",
		"mechanism":  "连接关系增加效用",
	}, "ev-b", "ev-a")
	object.CoreContent = "  核心内容保留空白  "
	object.AuditStatus = "passed"
	before := object
	beforeFields := mustJSON(t, before.StructureFields)
	beforeEvidence := mustJSON(t, before.EvidenceIDs)

	decision := FilterClassifiedKnowledge(object)
	if decision.Status != TitleFilterPassed || decision.Object == nil {
		t.Fatalf("decision = %#v, want passed object", decision)
	}
	if decision.Object.CoreContent != before.CoreContent {
		t.Fatalf("core_content changed from %q to %q", before.CoreContent, decision.Object.CoreContent)
	}
	if !bytes.Equal(beforeFields, mustJSON(t, decision.Object.StructureFields)) {
		t.Fatal("structure_fields changed")
	}
	if !bytes.Equal(beforeEvidence, mustJSON(t, decision.Object.EvidenceIDs)) {
		t.Fatal("evidence_ids changed or reordered")
	}
}

func TestFilterClassifiedKnowledgeRejectsWikiAndGraphFieldsInsideStructure(t *testing.T) {
	object := splitInput("nested-page-field", "网络效应", TypeConcept, map[string]string{
		"definition":   "用户增加提升产品价值",
		"mechanism":    "连接关系增加效用",
		"wiki_page_id": "page-1",
	}, "ev-nested-page")
	object.AuditStatus = "passed"

	decision := FilterClassifiedKnowledge(object)
	if decision.Status != TitleFilterRejected || decision.Rejected == nil {
		t.Fatalf("decision = %#v, want rejected object", decision)
	}
	if !strings.Contains(decision.Reason, "wiki_page_id") {
		t.Fatalf("reason = %q, want reserved page field rejection", decision.Reason)
	}
}

func TestCleanTitlePreservesStableNamesWithDescriptorWords(t *testing.T) {
	tests := []string{"语法解析", "产品说明书", "方法论概述模型"}
	for _, raw := range tests {
		got, reason := CleanTitle(raw)
		if reason != "" || got != raw {
			t.Fatalf("CleanTitle(%q) = %q/%q, want unchanged stable name", raw, got, reason)
		}
	}
}

func TestFilterPublishGateBatchKeepsOnlyCleanedPassedObjects(t *testing.T) {
	valid := splitInput("b-valid", "## 标题：什么是网络效应？", TypeConcept, map[string]string{
		"definition": "用户增加提升产品价值",
		"mechanism":  "连接关系增加效用",
	}, "ev-valid")
	valid.AuditStatus = "passed"
	filtered := splitInput("a-filtered", "本章内容", TypeConcept, map[string]string{
		"definition": "只是章节占位",
		"mechanism":  "没有独立对象",
	}, "ev-filtered")
	filtered.AuditStatus = "passed"
	priorRejected := RejectedProposition{
		CandidateID: "c-prior-rejected",
		Title:       "缺字段",
		PrimaryType: TypeConcept,
		Reason:      "minimum_structure",
		EvidenceIDs: []string{"ev-prior"},
	}

	result := FilterPublishGateBatch(PublishGateBatch{
		Passed:   []ClassifiedKnowledge{valid, filtered},
		Rejected: []RejectedProposition{priorRejected},
	})
	if len(result.Passed) != 1 || result.Passed[0].CandidateID != "b-valid" {
		t.Fatalf("passed = %#v, want only cleaned valid object", result.Passed)
	}
	if result.Passed[0].Title != "网络效应" {
		t.Fatalf("passed title = %q, want %q", result.Passed[0].Title, "网络效应")
	}
	if len(result.Rejected) != 2 {
		t.Fatalf("rejected count = %d, want 2", len(result.Rejected))
	}
	if result.Rejected[0].CandidateID != "a-filtered" || result.Rejected[1].CandidateID != "c-prior-rejected" {
		t.Fatalf("rejected order = %#v", result.Rejected)
	}
	if strings.Contains(string(mustJSON(t, result)), "wiki_page_id") {
		t.Fatal("P3-05 artifact must not contain Wiki page IDs")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("title filter batch validation: %v", err)
	}
}

func TestTitleFilterOutputIsByteStable(t *testing.T) {
	object := splitInput("stable-title", "## 知识点：[[什么是网络效应？]]", TypeConcept, map[string]string{
		"definition": "用户增加提升产品价值",
		"mechanism":  "连接关系增加效用",
	}, "ev-stable-title")
	object.AuditStatus = "passed"

	var previous []byte
	for run := 0; run < 50; run++ {
		result := ApplyTitleFilter([]ClassifiedKnowledge{object})
		encoded := mustJSON(t, result)
		if run > 0 && !bytes.Equal(previous, encoded) {
			t.Fatalf("run %d changed bytes:\nprevious=%s\ncurrent=%s", run, previous, encoded)
		}
		previous = encoded
	}
}

func TestDecodeTitleFilterBatchRejectsUnknownWikiFields(t *testing.T) {
	object := splitInput("decode-title", "网络效应", TypeConcept, map[string]string{
		"definition": "用户增加提升产品价值",
		"mechanism":  "连接关系增加效用",
	}, "ev-decode-title")
	object.AuditStatus = "passed"
	batch := ApplyTitleFilter([]ClassifiedKnowledge{object})
	payload := mustJSON(t, batch)
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	value["wiki_page_id"] = "should-not-be-here"
	if _, err := DecodeTitleFilterBatch(mustJSON(t, value)); err == nil {
		t.Fatal("expected unknown Wiki field to be rejected")
	}
}
