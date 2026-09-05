package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestSplitCaseAndInsightPreservesEvidenceAndOnlyCreatesPendingRelations(t *testing.T) {
	candidate := splitInput("mixed-case-insight", "Looki 投资案例与应用层仍被低估", TypeCase, map[string]string{
		"context":   "早期项目评估",
		"actors":    "投资团队与创业团队",
		"actions":   "评估产品并作出投资选择",
		"outcome":   "形成投资结果",
		"claim":     "应用层仍被低估",
		"reasoning": "基础设施投入增长未同步转化为应用收入",
	}, "ev-mixed-2", "ev-mixed-1")

	result, err := SplitClassifiedKnowledge(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 2 {
		t.Fatalf("status=%q objects=%d, want split/2", result.Status, len(result.Objects))
	}
	if result.Objects[0].PrimaryType != TypeCase || result.Objects[1].PrimaryType != TypeInsight {
		t.Fatalf("object types = %q, %q", result.Objects[0].PrimaryType, result.Objects[1].PrimaryType)
	}
	if len(result.Relations) != 1 || result.Relations[0].RelationType != "derived_from" {
		t.Fatalf("pending relations = %#v", result.Relations)
	}
	assertEvidenceSet(t, result.OriginalEvidenceIDs, result.Objects[0].EvidenceIDs, result.Objects[1].EvidenceIDs)
	if strings.Contains(string(mustJSON(t, result)), "wiki_page_id") {
		t.Fatal("P3 relation artifact must not contain Wiki page IDs")
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("result validation: %v", err)
	}
}

func TestSplitConceptAndMethodologyUsesTypedChildren(t *testing.T) {
	candidate := splitInput("mixed-concept-method", "三非理论与早期项目判断框架", TypeConcept, map[string]string{
		"definition": "三种非典型特征的组合",
		"components": "非共识、非连续、非线性",
		"input":      "项目材料",
		"steps":      "比较团队与产品",
	}, "ev-mixed")

	result, err := Split(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 2 {
		t.Fatalf("status=%q objects=%d, want split/2", result.Status, len(result.Objects))
	}
	if result.Objects[0].PrimaryType != TypeConcept || result.Objects[1].PrimaryType != TypeMethodology {
		t.Fatalf("object types = %q, %q", result.Objects[0].PrimaryType, result.Objects[1].PrimaryType)
	}
	if result.Relations[0].RelationType != "explains" {
		t.Fatalf("relation type = %q, want explains", result.Relations[0].RelationType)
	}
	if result.Objects[0].Title != "三非理论" || result.Objects[1].Title != "早期项目判断框架" {
		t.Fatalf("child titles = %q, %q", result.Objects[0].Title, result.Objects[1].Title)
	}
}

func TestSplitIndependentCasesFromOneTitle(t *testing.T) {
	candidate := splitInput("two-cases", "投资 Looki 与投资 Context Machine", TypeCase, map[string]string{
		"context": "早期项目评估",
		"actors":  "投资团队",
		"actions": "评估产品并作出选择",
		"outcome": "形成投资结果",
	}, "ev-case")

	result, err := SplitPropositions(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 2 {
		t.Fatalf("status=%q objects=%d, want split/2", result.Status, len(result.Objects))
	}
	if result.Objects[0].Title != "投资 Looki" || result.Objects[1].Title != "投资 Context Machine" {
		t.Fatalf("child titles = %q, %q", result.Objects[0].Title, result.Objects[1].Title)
	}
	if result.Relations[0].RelationType != "complements" {
		t.Fatalf("same-type relation = %q, want complements", result.Relations[0].RelationType)
	}
}

func TestSplitTitleScansMixedConnectors(t *testing.T) {
	candidate := splitInput("mixed-connectors", "投资 Looki 与投资 Context Machine 和投资 Nova", TypeCase, map[string]string{
		"context": "早期项目评估",
		"actors":  "投资团队",
		"actions": "评估产品并作出选择",
		"outcome": "形成投资结果",
	}, "ev-mixed-connectors")

	result, err := Split(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 3 {
		t.Fatalf("status=%q objects=%d, want split/3", result.Status, len(result.Objects))
	}
}

func TestSplitDoesNotSplitWordInternalHeheConnector(t *testing.T) {
	candidate := splitInput("internal-he", "机构和谐发展", TypeConcept, map[string]string{
		"definition": "机构和谐发展的概念定义",
		"components": "协作与稳定",
	}, "ev-internal-he")

	result, err := Split(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusUnchanged || len(result.Objects) != 1 {
		t.Fatalf("status=%q objects=%d, want unchanged/1", result.Status, len(result.Objects))
	}
}

func TestSplitDoesNotSplitUnifiedInsight(t *testing.T) {
	candidate := splitInput("unified-insight", "智能会通胀且智慧仍然稀缺", TypeInsight, map[string]string{
		"claim":     "智能会通胀且智慧仍然稀缺",
		"reasoning": "工具能力普及降低差异化，但经验与反思仍然稀缺",
	}, "ev-insight")
	parts, ok := splitTitleByConnector(candidate.Title, candidate.PrimaryType)
	if ok || !isUnifiedInsight(candidate.Title, "且", []string{"智能会通胀", "智慧仍然稀缺"}) {
		t.Fatalf("unified insight guard failed: ok=%v parts=%v", ok, parts)
	}

	result, err := SplitClassifiedKnowledge(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusUnchanged || len(result.Objects) != 1 || len(result.Relations) != 0 {
		t.Fatalf("result = %#v, want unchanged single object", result)
	}
}

func TestSplitDoesNotSplitCompositeMethodologyStrategies(t *testing.T) {
	candidate := splitInput("composite-method", "上下文压缩且 Agent 接力策略", TypeMethodology, map[string]string{
		"input":  "长任务上下文",
		"steps":  "先压缩上下文，再交接 Agent",
		"output": "连续完成任务",
	}, "ev-method")

	result, err := SplitClassifiedKnowledge(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusUnchanged || len(result.Objects) != 1 {
		t.Fatalf("status=%q objects=%d, want unchanged/1", result.Status, len(result.Objects))
	}
}

func TestSplitIndependentMethodologiesWithSeparateStrategies(t *testing.T) {
	candidate := splitInput("separate-methods", "用户留存归因策略与渠道优化框架", TypeMethodology, map[string]string{
		"input":    "用户与渠道数据",
		"steps":    "分别分析留存和渠道",
		"criteria": "归因准确且可执行",
		"output":   "两个优化方案",
	}, "ev-separate-methods")

	result, err := Split(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 2 {
		t.Fatalf("status=%q objects=%d, want split/2", result.Status, len(result.Objects))
	}
}

func TestSplitInsightSameSubjectDifferentClaims(t *testing.T) {
	candidate := splitInput("same-subject-insight", "市场增长且市场份额仍然下降", TypeInsight, map[string]string{
		"claim":     "市场增长且市场份额仍然下降",
		"reasoning": "总量增长不代表份额提升",
	}, "ev-same-subject")

	result, err := Split(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 2 {
		t.Fatalf("status=%q objects=%d, want split/2", result.Status, len(result.Objects))
	}
}

func TestSplitChildrenDoNotRetainParentMixedCoreContent(t *testing.T) {
	candidate := splitInput("child-content", "投资 Looki 案例与应用层仍被低估", TypeCase, map[string]string{
		"context": "早期项目评估",
		"actors":  "投资团队",
		"actions": "评估产品并作出选择",
		"outcome": "形成投资结果",
		"claim":   "应用层仍被低估",
	}, "ev-child-content")
	candidate.CoreContent = "父级混合命题：投资过程与应用层判断"

	result, err := Split(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusSplit || len(result.Objects) != 2 {
		t.Fatalf("status=%q objects=%d, want split/2", result.Status, len(result.Objects))
	}
	for _, object := range result.Objects {
		if object.CoreContent == candidate.CoreContent {
			t.Fatalf("child %q retained mixed parent core_content", object.CandidateID)
		}
	}
}

func TestSplitRejectsOrdinaryWordMeaning(t *testing.T) {
	candidate := splitInput("ordinary-word", "实", TypeConcept, map[string]string{
		"definition": "知道其内容的普通词义解释",
	}, "ev-ordinary")

	result, err := SplitClassifiedKnowledge(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusRejected || len(result.Objects) != 0 || len(result.Rejected) != 1 {
		t.Fatalf("result = %#v, want rejected without object", result)
	}
	if result.Rejected[0].Reason != "ordinary_word_meaning" {
		t.Fatalf("reject reason = %q", result.Rejected[0].Reason)
	}
	assertEvidenceSet(t, result.OriginalEvidenceIDs, result.Rejected[0].EvidenceIDs)
}

func TestSplitRejectsOrdinaryWordEvenWhenASecondFieldIsPresent(t *testing.T) {
	candidate := splitInput("ordinary-word-extra", "知道", TypeConcept, map[string]string{
		"definition": "理解内容",
		"components": "字面解释",
	}, "ev-ordinary-extra")

	result, err := SplitClassifiedKnowledge(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if result.Status != SplitStatusRejected || len(result.Objects) != 0 {
		t.Fatalf("status=%q objects=%d, want rejected/0", result.Status, len(result.Objects))
	}
}

func TestSplitOutputIsByteStable(t *testing.T) {
	candidate := splitInput("stable-split", "模型才是 agent，且 prompt flow 会消亡", TypeInsight, map[string]string{
		"claim":     "模型才是 agent，且 prompt flow 会消亡",
		"reasoning": "模型负责执行，流程编排逐渐失去中心位置",
	}, "ev-stable-2", "ev-stable-1")

	var previous []byte
	for run := 0; run < 50; run++ {
		result, err := SplitClassifiedKnowledge(candidate)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		encoded := mustJSON(t, result)
		if run > 0 && !bytes.Equal(previous, encoded) {
			t.Fatalf("run %d changed bytes:\nprevious=%s\ncurrent=%s", run, previous, encoded)
		}
		previous = encoded
	}
}

func TestSplitRelationsDoNotAcceptGraphRelationShape(t *testing.T) {
	result := SplitResult{
		CandidateID:         "candidate",
		OriginalTitle:       "概念与方法",
		OriginalPrimaryType: TypeConcept,
		OriginalEvidenceIDs: []string{"ev-1"},
		Status:              SplitStatusSplit,
		Objects: []ClassifiedKnowledge{
			{CandidateID: "candidate#split-1", SourceDocumentID: "doc", SourceVideoID: "video", TranscriptGeneration: "generation", PrimaryType: TypeConcept, Title: "概念", CoreContent: "内容", StructureFields: map[string]string{"definition": "定义"}, EvidenceIDs: []string{"ev-1"}, ClassificationConfidence: 0.8, AuditStatus: "pending"},
			{CandidateID: "candidate#split-2", SourceDocumentID: "doc", SourceVideoID: "video", TranscriptGeneration: "generation", PrimaryType: TypeMethodology, Title: "方法", CoreContent: "内容", StructureFields: map[string]string{"steps": "步骤"}, EvidenceIDs: []string{"ev-1"}, ClassificationConfidence: 0.8, AuditStatus: "pending"},
		},
		Relations: []PendingRelationDescription{{RelationID: "relation", SourceCandidateID: "candidate#split-1", TargetCandidateID: "candidate#split-2", RelationType: "explains", Description: "待 P4", EvidenceIDs: []string{"ev-1"}}},
	}
	encoded := mustJSON(t, result)
	if strings.Contains(string(encoded), "target_wiki_page_id") || strings.Contains(string(encoded), "wiki_page_id") {
		t.Fatal("P3 relation result must not encode Graph/Wiki page IDs")
	}
}

func TestDecodeSplitResultRejectsUnknownWikiFields(t *testing.T) {
	candidate := splitInput("decode-split", "实", TypeConcept, map[string]string{"definition": "知道其内容"}, "ev-decode")
	result, err := SplitClassifiedKnowledge(candidate)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	payload := mustJSON(t, result)
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	value["target_wiki_page_id"] = "should-not-be-here"
	if _, err := DecodeSplitResult(mustJSON(t, value)); err == nil {
		t.Fatal("expected unknown Wiki relation field to be rejected")
	}
}

func splitInput(id, title string, primaryType KnowledgeType, fields map[string]string, evidence ...string) ClassifiedKnowledge {
	return ClassifiedKnowledge{
		CandidateID:              id,
		SourceDocumentID:         "doc-1",
		SourceVideoID:            "video-1",
		TranscriptGeneration:     "generation-1",
		PrimaryType:              primaryType,
		Title:                    title,
		CoreContent:              "这是来自同一候选的可审计摘要。",
		StructureFields:          fields,
		EvidenceIDs:              evidence,
		ClassificationConfidence: 0.8,
		AuditStatus:              "pending",
	}
}

func assertEvidenceSet(t *testing.T, original []string, children ...[]string) {
	t.Helper()
	want := make(map[string]struct{}, len(original))
	for _, id := range original {
		want[id] = struct{}{}
	}
	got := make(map[string]struct{}, len(original))
	for _, evidence := range children {
		for _, id := range evidence {
			got[id] = struct{}{}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("evidence union = %#v, want %#v", got, want)
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("evidence %q missing from children", id)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
