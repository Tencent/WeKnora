package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderInsightPageUsesFrameworkOrderAndChineseLabels(t *testing.T) {
	object := insightTemplateObject(map[string]string{
		"claim":          "应用层仍被低估",
		"reasoning":      "应用层收入增长快于估值增长",
		"qualifications": "仅适用于早期市场",
		"implications":   "优先关注有收入模型的应用",
	}, "ev-insight")
	render, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{
		"claim": {EvidenceIDs: []string{"ev-insight"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}}, "qualifications": {EvidenceIDs: []string{"ev-insight"}}, "implications": {EvidenceIDs: []string{"ev-insight"}},
	}})
	if err != nil {
		t.Fatalf("render insight page: %v", err)
	}
	if err := ValidateInsightPageRender(render, object.Title); err != nil {
		t.Fatalf("validate render: %v", err)
	}
	positions := []int{strings.Index(render.Content, "- 核心判断："), strings.Index(render.Content, "- 推导依据："), strings.Index(render.Content, "- 限定条件："), strings.Index(render.Content, "- 影响建议：")}
	for i, position := range positions {
		if position < 0 || (i > 0 && positions[i-1] >= position) {
			t.Fatalf("framework field order is wrong: %s", render.Content)
		}
	}
	if !strings.Contains(render.Content, "\n洞察\n") {
		t.Fatalf("information nature missing: %s", render.Content)
	}
}

func TestRenderInsightPageSkipsEmptyUnsupportedAndUnevidencedFields(t *testing.T) {
	object := insightTemplateObject(map[string]string{"claim": "应用层仍被低估", "reasoning": "有明确收入模型的应用更稳健", "implications": "不应追逐无收入项目", "context": "案例背景不应进入洞察"}, "ev-insight")
	delete(object.StructureFields, "context")
	render, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{
		"claim": {EvidenceIDs: []string{"ev-insight"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}},
	}})
	if err != nil {
		t.Fatalf("render insight page: %v", err)
	}
	for _, unexpected := range []string{"限定条件：", "影响建议：", "案例背景"} {
		if strings.Contains(render.Content, unexpected) {
			t.Fatalf("unexpected field rendered %q: %s", unexpected, render.Content)
		}
	}
}

func TestRenderInsightPageRequiresClaimAndReasoningOrQualificationsWithEvidence(t *testing.T) {
	object := insightTemplateObject(map[string]string{"claim": "一个判断", "reasoning": "一个依据"}, "ev-insight")
	_, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"claim": {EvidenceIDs: []string{"ev-insight"}}}})
	if err == nil || !strings.Contains(err.Error(), "reasoning or qualifications") {
		t.Fatalf("minimum field error = %v", err)
	}
	_, err = RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"reasoning": {EvidenceIDs: []string{"ev-insight"}}}})
	if err == nil || !strings.Contains(err.Error(), "core claim") {
		t.Fatalf("claim evidence error = %v", err)
	}
}

func TestRenderInsightPageRejectsCaseFixture(t *testing.T) {
	object := insightTemplateObject(map[string]string{"claim": "一个判断", "reasoning": "一个依据", "context": "团队在某季度做了一个具体决策"}, "ev-insight")
	_, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"claim": {EvidenceIDs: []string{"ev-insight"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}}}})
	if err == nil || !strings.Contains(err.Error(), "case field") {
		t.Fatalf("case fixture should be rejected, error = %v", err)
	}
}

func TestRenderInsightPageRejectsForeignEvidenceAndKeepsDescription(t *testing.T) {
	object := insightTemplateObject(map[string]string{"claim": "一个判断", "reasoning": "一个依据"}, "ev-insight")
	_, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"claim": {EvidenceIDs: []string{"foreign"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}}}})
	if err == nil || !strings.Contains(err.Error(), "not present on insight") {
		t.Fatalf("foreign evidence error = %v", err)
	}
	render, err := RenderInsightPage(InsightTemplateInput{Object: object, Description: "这是已提炼的洞察描述。", Aliases: []string{"应用层判断"}, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"claim": {EvidenceIDs: []string{"ev-insight"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}}}, SourceParagraphs: []InsightSourceParagraph{{Text: "来源段落", EvidenceIDs: []string{"ev-insight"}}}})
	if err != nil {
		t.Fatalf("render insight page: %v", err)
	}
	if !strings.Contains(render.Content, "一句话概述：这是已提炼的洞察描述。") || !strings.Contains(render.Content, "来源段落") {
		t.Fatalf("description/source missing: %s", render.Content)
	}
}

func TestRenderInsightPagePassesWikiContractAndIsByteStable(t *testing.T) {
	object := insightTemplateObject(map[string]string{"claim": "一个判断", "reasoning": "一个依据"}, "ev-insight")
	input := InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"claim": {EvidenceIDs: []string{"ev-insight"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}}}}
	var previous []byte
	for i := 0; i < 3; i++ {
		render, err := RenderInsightPage(input)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateInsightPageRender(render, object.Title); err != nil {
			t.Fatal(err)
		}
		validation, err := ValidateWikiObjectPage(render.Content, render.PageType, object.SourceVideoID, object.TranscriptGeneration)
		if err != nil {
			t.Fatalf("rendered insight page failed Wiki contract: %v\n%s", err, render.Content)
		}
		if validation.Title != object.Title || validation.KnowledgeType != TypeInsight {
			t.Fatalf("validation = %#v", validation)
		}
		encoded, err := json.Marshal(render)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && !bytes.Equal(previous, encoded) {
			t.Fatalf("run %d changed bytes", i)
		}
		previous = encoded
	}
}

func TestRenderInsightPageNormalizesFieldKeyCasing(t *testing.T) {
	object := insightTemplateObject(map[string]string{"Claim": "一个判断", "REASONING": "一个依据"}, "ev-insight")
	render, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: "00:10:00-00:10:50", FieldEvidence: map[string]InsightFieldEvidence{"CLAIM": {EvidenceIDs: []string{"ev-insight"}}, "reasoning": {EvidenceIDs: []string{"ev-insight"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(render.Content, "核心判断：一个判断") || !strings.Contains(render.Content, "推导依据：一个依据") {
		t.Fatalf("case-insensitive fields missing: %s", render.Content)
	}
}

func insightTemplateObject(fields map[string]string, evidenceID string) ClassifiedKnowledge {
	return ClassifiedKnowledge{CandidateID: "insight-template", SourceDocumentID: "doc-insight", SourceVideoID: "video-insight", TranscriptGeneration: "generation-insight", PrimaryType: TypeInsight, Title: "应用层估值判断", CoreContent: "应用层仍被低估。", StructureFields: fields, EvidenceIDs: []string{evidenceID}, ClassificationConfidence: 0.93, AuditStatus: "passed"}
}
