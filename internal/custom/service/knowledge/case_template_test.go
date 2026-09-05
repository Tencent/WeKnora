package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderCasePageUsesFrameworkOrderAndChineseLabels(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context":       "团队在 2024 年 Q3 评估多个增长机会。",
		"actors":        "投资团队与创始团队",
		"choices":       "优先推进 Looki 的投资决策",
		"actions":       "完成尽调并进入投资流程",
		"outcome":       "形成最终投资决定",
		"retrospective": "后续应继续跟踪产品落地效果",
	}, "ev-case")
	object.CoreContent = "投资团队与创始团队在 2024 年 Q3 评估多个增长机会时，优先推进 Looki 的投资决策，通过尽调进入投资流程并最终形成投资决定；后续继续跟踪产品落地效果。"
	render, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context":       {EvidenceIDs: []string{"ev-case"}},
			"actors":        {EvidenceIDs: []string{"ev-case"}},
			"choices":       {EvidenceIDs: []string{"ev-case"}},
			"actions":       {EvidenceIDs: []string{"ev-case"}},
			"outcome":       {EvidenceIDs: []string{"ev-case"}},
			"retrospective": {EvidenceIDs: []string{"ev-case"}},
		},
	})
	if err != nil {
		t.Fatalf("render case page: %v", err)
	}
	if render.PageType != "index" || render.Title != object.Title {
		t.Fatalf("render metadata = %#v", render)
	}
	if err := ValidateCasePageRender(render, object.Title); err != nil {
		t.Fatalf("validate render: %v", err)
	}
	positions := []int{
		strings.Index(render.Content, "- 背景："),
		strings.Index(render.Content, "- 参与对象："),
		strings.Index(render.Content, "- 选择："),
		strings.Index(render.Content, "- 行动："),
		strings.Index(render.Content, "- 结果："),
		strings.Index(render.Content, "- 复盘判断："),
	}
	for index, position := range positions {
		if position < 0 || (index > 0 && positions[index-1] >= position) {
			t.Fatalf("framework field order is wrong: %s", render.Content)
		}
	}
	if !strings.Contains(render.Content, "\n案例\n") {
		t.Fatalf("information nature is missing: %s", render.Content)
	}
	if !strings.Contains(render.Content, "一句话概述："+object.CoreContent) {
		t.Fatalf("core content was not rendered as the case summary: %s", render.Content)
	}
	if strings.Contains(render.Content, "该案例发生在") ||
		strings.Contains(render.Content, "选择优先推进 Looki 的投资决策") {
		t.Fatalf("case summary was mechanically rebuilt from structure fields: %s", render.Content)
	}
}

func TestRenderCasePageSkipsEmptyUnsupportedAndUnevidencedFields(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context":   "项目进入收缩期，团队重新评估增长路径。",
		"actors":    "产品团队",
		"actions":   "调整投放策略并重排优先级",
		"claim":     "外来判断字段",
		"reasoning": "外来判断字段",
	}, "ev-case")
	render, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
			"choices": {},
		},
	})
	if err != nil {
		t.Fatalf("render case page: %v", err)
	}
	for _, unexpected := range []string{"复盘判断：", "外来判断字段", "claim", "reasoning"} {
		if strings.Contains(render.Content, unexpected) {
			t.Fatalf("unexpected field rendered %q: %s", unexpected, render.Content)
		}
	}
	if !strings.Contains(render.Content, "背景：项目进入收缩期，团队重新评估增长路径。") ||
		!strings.Contains(render.Content, "参与对象：产品团队") ||
		!strings.Contains(render.Content, "行动：调整投放策略并重排优先级") {
		t.Fatalf("supported evidenced fields are missing: %s", render.Content)
	}
}

func TestRenderCasePageRequiresContextActorsAndActionOrOutcomeWithEvidence(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "项目进入收缩期，团队重新评估增长路径。",
		"actors":  "产品团队",
		"choices": "优先保留现有投放",
	}, "ev-case")
	_, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"choices": {EvidenceIDs: []string{"ev-case"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "context, actors, and at least one of actions or outcome") {
		t.Fatalf("case minimum field error = %v", err)
	}
}

func TestRenderCasePageRejectsInsightFixture(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"claim":     "应用层仍被低估",
		"reasoning": "基础设施投入增长未同步转化为应用收入",
	}, "ev-insight")
	_, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
	})
	if err == nil || !strings.Contains(err.Error(), "at least 2 valid structure fields") {
		t.Fatalf("insight fixture should be rejected, error = %v", err)
	}
}

func TestRenderCasePageRejectsFieldOrSourceEvidenceOutsideCase(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "项目进入收缩期。",
		"actors":  "产品团队",
		"actions": "调整投放策略",
	}, "ev-case")
	_, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"foreign"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on case") {
		t.Fatalf("field evidence error = %v", err)
	}

	_, err = RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
		},
		SourceParagraphs: []CaseSourceParagraph{{
			Text:        "无效来源",
			EvidenceIDs: []string{"foreign"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on case") {
		t.Fatalf("source evidence error = %v", err)
	}
}

func TestRenderCasePageKeepsDescriptionAliasesAndSourceParagraph(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "团队在增长拐点重新评估案例。",
		"actors":  "投资团队",
		"actions": "完成尽调并进入投资流程",
	}, "ev-case")
	render, err := RenderCasePage(CaseTemplateInput{
		Object:      object,
		Aliases:     []string{"Looki 投资决策", "Investment Decision", " Investment Decision "},
		Description: "围绕 Looki 投资做出的决策案例。",
		TimeRange:   "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
		},
		SourceParagraphs: []CaseSourceParagraph{{
			Text:        "团队先评估机会，再决定推进投资。",
			EvidenceIDs: []string{"ev-case"},
		}},
	})
	if err != nil {
		t.Fatalf("render case page: %v", err)
	}
	for _, expected := range []string{
		"围绕 Looki 投资做出的决策案例。",
		"Investment Decision",
		"团队先评估机会，再决定推进投资。",
		"背景：团队在增长拐点重新评估案例。",
		"参与对象：投资团队",
		"行动：完成尽调并进入投资流程",
	} {
		if !strings.Contains(render.Content, expected) {
			t.Fatalf("rendered page missing %q: %s", expected, render.Content)
		}
	}
	if strings.Contains(render.Content, "- Looki 投资决策\n") {
		t.Fatal("title duplicate should not be rendered as alias")
	}
}

func TestRenderCasePagePassesWikiObjectContract(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "团队在增长拐点重新评估案例。",
		"actors":  "投资团队",
		"actions": "完成尽调并进入投资流程",
	}, "ev-case")
	render, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
		},
		SourceParagraphs: []CaseSourceParagraph{{
			Text:        "案例来源段落。",
			EvidenceIDs: []string{"ev-case"},
		}},
	})
	if err != nil {
		t.Fatalf("render case page: %v", err)
	}
	validation, err := ValidateWikiObjectPage(render.Content, render.PageType, object.SourceVideoID, object.TranscriptGeneration)
	if err != nil {
		t.Fatalf("rendered case page failed Wiki contract: %v\n%s", err, render.Content)
	}
	if validation.Title != object.Title || validation.KnowledgeType != TypeCase {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestRenderCasePageIsByteStableAcrossRepeatedRuns(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "团队在增长拐点重新评估案例。",
		"actors":  "投资团队",
		"actions": "完成尽调并进入投资流程",
		"outcome": "形成最终投资决定",
	}, "ev-case")
	input := CaseTemplateInput{
		Object:    object,
		Aliases:   []string{"Investment Decision", "Looki 投资决策"},
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
			"outcome": {EvidenceIDs: []string{"ev-case"}},
		},
		SourceParagraphs: []CaseSourceParagraph{{
			Text:        "案例来源段落。",
			EvidenceIDs: []string{"ev-case"},
		}},
	}
	var previous []byte
	for run := 0; run < 50; run++ {
		render, err := RenderCasePage(input)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		encoded, err := json.Marshal(render)
		if err != nil {
			t.Fatal(err)
		}
		if run > 0 && !bytes.Equal(previous, encoded) {
			t.Fatalf("run %d changed bytes:\nprevious=%s\ncurrent=%s", run, previous, encoded)
		}
		previous = encoded
	}
}

func TestRenderCasePageNormalizesFrameworkFieldKeyCasing(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"Context": "团队在增长拐点重新评估案例。",
		"ACTORS":  "投资团队",
		"Actions": "完成尽调并进入投资流程",
	}, "ev-case")
	render, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
		},
	})
	if err != nil {
		t.Fatalf("render case page: %v", err)
	}
	if !strings.Contains(render.Content, "背景：团队在增长拐点重新评估案例。") ||
		!strings.Contains(render.Content, "参与对象：投资团队") ||
		!strings.Contains(render.Content, "行动：完成尽调并进入投资流程") {
		t.Fatalf("case-insensitive framework fields were not rendered: %s", render.Content)
	}
}

func TestValidateCasePageRenderRejectsMismatchedFrontmatterTitleAndType(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "团队在增长拐点重新评估案例。",
		"actors":  "投资团队",
		"actions": "完成尽调并进入投资流程",
	}, "ev-case")
	render, err := RenderCasePage(CaseTemplateInput{
		Object:    object,
		TimeRange: "00:10:00-00:10:50",
		FieldEvidence: map[string]CaseFieldEvidence{
			"context": {EvidenceIDs: []string{"ev-case"}},
			"actors":  {EvidenceIDs: []string{"ev-case"}},
			"actions": {EvidenceIDs: []string{"ev-case"}},
		},
	})
	if err != nil {
		t.Fatalf("render case page: %v", err)
	}
	for _, replacement := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "title", old: "title: Looki 投资决策", new: "title: 另一个案例", want: "title and frontmatter title"},
		{name: "type", old: "type: case", new: "type: concept", want: "type and primary_type"},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			mutated := strings.Replace(render.Content, replacement.old, replacement.new, 1)
			if mutated == render.Content {
				t.Fatalf("fixture replacement %q did not apply", replacement.name)
			}
			if err := ValidateCasePageRender(CasePageRender{
				PageType: render.PageType,
				Title:    render.Title,
				Content:  mutated,
			}, object.Title); err == nil || !strings.Contains(err.Error(), replacement.want) {
				t.Fatalf("validation error = %v, want %q", err, replacement.want)
			}
		})
	}
}

func TestRenderCasePageRejectsPlaceholderTimeRange(t *testing.T) {
	object := caseTemplateObject(map[string]string{
		"context": "团队在增长拐点重新评估案例。",
		"actors":  "投资团队",
		"actions": "完成尽调并进入投资流程",
	}, "ev-case")
	for _, timeRange := range []string{"TBD", "待定-待定", "placeholder"} {
		t.Run(timeRange, func(t *testing.T) {
			_, err := RenderCasePage(CaseTemplateInput{
				Object:    object,
				TimeRange: timeRange,
				FieldEvidence: map[string]CaseFieldEvidence{
					"context": {EvidenceIDs: []string{"ev-case"}},
					"actors":  {EvidenceIDs: []string{"ev-case"}},
					"actions": {EvidenceIDs: []string{"ev-case"}},
				},
			})
			if err == nil || !strings.Contains(err.Error(), "must not be a placeholder") {
				t.Fatalf("placeholder time range error = %v", err)
			}
		})
	}
}

func TestRenderCasePageManualAcceptanceSamples(t *testing.T) {
	t.Run("Looki 投资决策", func(t *testing.T) {
		object := caseTemplateObject(map[string]string{
			"context": "团队在 2024 年 Q3 评估多个增长机会。",
			"actors":  "投资团队与创始团队",
			"choices": "优先推进 Looki 的投资决策",
			"actions": "完成尽调并进入投资流程",
			"outcome": "形成最终投资决定",
		}, "ev-case-looki")
		object.CoreContent = "投资团队与创始团队在 2024 年 Q3 评估多个增长机会时，优先推进 Looki 的投资决策，通过尽调进入投资流程并最终形成投资决定；后续继续跟踪产品落地效果。"
		render, err := RenderCasePage(CaseTemplateInput{
			Object:    object,
			TimeRange: "00:10:00-00:10:50",
			FieldEvidence: map[string]CaseFieldEvidence{
				"context": {EvidenceIDs: []string{"ev-case-looki"}},
				"actors":  {EvidenceIDs: []string{"ev-case-looki"}},
				"choices": {EvidenceIDs: []string{"ev-case-looki"}},
				"actions": {EvidenceIDs: []string{"ev-case-looki"}},
				"outcome": {EvidenceIDs: []string{"ev-case-looki"}},
			},
			SourceParagraphs: []CaseSourceParagraph{{
				Text:        "团队先评估机会，再决定推进投资。",
				EvidenceIDs: []string{"ev-case-looki"},
			}},
		})
		if err != nil {
			t.Fatalf("render looki case: %v", err)
		}
		t.Logf("\n%s", render.Content)
	})

	t.Run("纯判断夹具", func(t *testing.T) {
		object := caseTemplateObject(map[string]string{
			"claim":     "应用层仍被低估",
			"reasoning": "基础设施投入增长未同步转化为应用收入",
		}, "ev-judgment")
		_, err := RenderCasePage(CaseTemplateInput{
			Object:    object,
			TimeRange: "00:10:00-00:10:50",
		})
		if err == nil {
			t.Fatal("pure judgment fixture should be rejected")
		}
		t.Logf("rejected as expected: %v", err)
	})
}

func caseTemplateObject(fields map[string]string, evidenceID string) ClassifiedKnowledge {
	return ClassifiedKnowledge{
		CandidateID:              "case-template",
		SourceDocumentID:         "doc-case",
		SourceVideoID:            "video-case",
		TranscriptGeneration:     "generation-case",
		PrimaryType:              TypeCase,
		Title:                    "Looki 投资决策",
		CoreContent:              "这是一个可审计的案例摘要。",
		StructureFields:          fields,
		EvidenceIDs:              []string{evidenceID},
		ClassificationConfidence: 0.93,
		AuditStatus:              "passed",
	}
}
