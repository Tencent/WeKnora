package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderMethodologyPageUsesFrameworkOrderAndSeparatesApplicability(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input":         "用户留存率下降的周度数据",
		"steps":         "按渠道拆分并逐一排查变更",
		"criteria":      "变更时间与留存拐点不超过 3 天",
		"output":        "导致留存下降的具体变更项",
		"applicability": "适用于单指标异常归因",
	}, "ev-method")
	render, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input":         {EvidenceIDs: []string{"ev-method"}},
			"steps":         {EvidenceIDs: []string{"ev-method"}},
			"criteria":      {EvidenceIDs: []string{"ev-method"}},
			"output":        {EvidenceIDs: []string{"ev-method"}},
			"applicability": {EvidenceIDs: []string{"ev-method"}},
		},
	})
	if err != nil {
		t.Fatalf("render methodology page: %v", err)
	}
	if render.PageType != "index" || render.Title != object.Title {
		t.Fatalf("render metadata = %#v", render)
	}
	if err := ValidateMethodologyPageRender(render, object.Title); err != nil {
		t.Fatalf("validate render: %v", err)
	}
	positions := []int{
		strings.Index(render.Content, "- 输入："),
		strings.Index(render.Content, "- 步骤："),
		strings.Index(render.Content, "- 判断标准："),
		strings.Index(render.Content, "- 输出："),
	}
	for index, position := range positions {
		if position < 0 || (index > 0 && positions[index-1] >= position) {
			t.Fatalf("framework field order is wrong: %s", render.Content)
		}
	}
	if !strings.Contains(render.Content, "## 适用条件") ||
		!strings.Contains(render.Content, "- 适用条件：适用于单指标异常归因") {
		t.Fatalf("applicability section missing or malformed: %s", render.Content)
	}
	if !strings.Contains(render.Content, "\n方法论\n") {
		t.Fatalf("information nature is missing: %s", render.Content)
	}
}

func TestRenderMethodologyPageSkipsEmptyUnsupportedAndUnevidencedFields(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input":         "项目材料",
		"steps":         "执行步骤",
		"criteria":      "",
		"output":        "不得展示",
		"applicability": "不得展示",
		"definition":    "外来概念字段",
	}, "ev-method")
	render, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input":         {EvidenceIDs: []string{"ev-method"}},
			"steps":         {EvidenceIDs: []string{"ev-method"}},
			"criteria":      {EvidenceIDs: []string{"ev-method"}},
			"output":        {},
			"applicability": {},
		},
	})
	if err != nil {
		t.Fatalf("render methodology page: %v", err)
	}
	for _, unexpected := range []string{"输出：", "外来概念字段", "definition"} {
		if strings.Contains(render.Content, unexpected) {
			t.Fatalf("unexpected field rendered %q: %s", unexpected, render.Content)
		}
	}
	if strings.Contains(render.Content, "## 适用条件") {
		t.Fatalf("applicability section should be omitted when blank: %s", render.Content)
	}
	if !strings.Contains(render.Content, "输入：项目材料") ||
		!strings.Contains(render.Content, "步骤：执行步骤") {
		t.Fatalf("supported evidenced fields are missing: %s", render.Content)
	}
}

func TestRenderMethodologyPageRequiresTwoCoreFieldsWithEvidence(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input":         "项目材料",
		"steps":         "执行步骤",
		"applicability": "适用于早期项目",
	}, "ev-method")
	_, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input":         {EvidenceIDs: []string{"ev-method"}},
			"applicability": {EvidenceIDs: []string{"ev-method"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at least 2 populated fields with evidence") {
		t.Fatalf("methodology minimum field error = %v", err)
	}
}

func TestRenderMethodologyPageRejectsConceptFixture(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input":  "概念输入",
		"steps":  "概念步骤",
		"output": "概念输出",
	}, "ev-concept")
	object.PrimaryType = TypeConcept
	_, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input":  {EvidenceIDs: []string{"ev-concept"}},
			"steps":  {EvidenceIDs: []string{"ev-concept"}},
			"output": {EvidenceIDs: []string{"ev-concept"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires primary_type methodology") {
		t.Fatalf("concept fixture should be rejected, error = %v", err)
	}
}

func TestRenderMethodologyPageRejectsFieldOrSourceEvidenceOutsideMethodology(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input": "项目材料",
		"steps": "执行步骤",
	}, "ev-method")
	_, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input": {EvidenceIDs: []string{"foreign"}},
			"steps": {EvidenceIDs: []string{"ev-method"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on methodology") {
		t.Fatalf("field evidence error = %v", err)
	}

	_, err = RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input": {EvidenceIDs: []string{"ev-method"}},
			"steps": {EvidenceIDs: []string{"ev-method"}},
		},
		SourceParagraphs: []MethodologySourceParagraph{{
			Text:        "无效来源",
			EvidenceIDs: []string{"foreign"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on methodology") {
		t.Fatalf("source evidence error = %v", err)
	}
}

func TestRenderMethodologyPageKeepsDescriptionAliasesAndSourceParagraph(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input": "用户留存率下降的周度数据",
		"steps": "按渠道拆分留存曲线",
	}, "ev-method")
	render, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:      object,
		Aliases:     []string{"用户留存归因", "Retention Attribution", " Retention Attribution "},
		Description: "通过数据拆分、变更排查和标准比较定位异常原因。",
		TimeRange:   "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input": {EvidenceIDs: []string{"ev-method"}},
			"steps": {EvidenceIDs: []string{"ev-method"}},
		},
		SourceParagraphs: []MethodologySourceParagraph{{
			Text:        "先按渠道拆分留存曲线，再对比异常渠道与正常渠道。",
			EvidenceIDs: []string{"ev-method"},
		}},
	})
	if err != nil {
		t.Fatalf("render methodology page: %v", err)
	}
	for _, expected := range []string{
		"通过数据拆分、变更排查和标准比较定位异常原因。",
		"Retention Attribution",
		"先按渠道拆分留存曲线，再对比异常渠道与正常渠道。",
		"输入：用户留存率下降的周度数据",
		"步骤：按渠道拆分留存曲线",
	} {
		if !strings.Contains(render.Content, expected) {
			t.Fatalf("rendered page missing %q: %s", expected, render.Content)
		}
	}
	if strings.Contains(render.Content, "- 用户留存归因\n") {
		t.Fatal("title duplicate should not be rendered as alias")
	}
}

func TestRenderMethodologyPagePassesWikiObjectContract(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input":    "项目材料",
		"steps":    "执行步骤",
		"criteria": "判断标准",
	}, "ev-method")
	render, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input":    {EvidenceIDs: []string{"ev-method"}},
			"steps":    {EvidenceIDs: []string{"ev-method"}},
			"criteria": {EvidenceIDs: []string{"ev-method"}},
		},
		SourceParagraphs: []MethodologySourceParagraph{{
			Text:        "方法论来源段落。",
			EvidenceIDs: []string{"ev-method"},
		}},
	})
	if err != nil {
		t.Fatalf("render methodology page: %v", err)
	}
	validation, err := ValidateWikiObjectPage(render.Content, render.PageType, object.SourceVideoID, object.TranscriptGeneration)
	if err != nil {
		t.Fatalf("rendered methodology page failed Wiki contract: %v\n%s", err, render.Content)
	}
	if validation.Title != object.Title || validation.KnowledgeType != TypeMethodology {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestRenderMethodologyPageIsByteStableAcrossRepeatedRuns(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input":         "项目材料",
		"steps":         "执行步骤",
		"applicability": "适用于单指标异常",
	}, "ev-method")
	input := MethodologyTemplateInput{
		Object:    object,
		Aliases:   []string{"Retention Attribution", "留存归因"},
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input":         {EvidenceIDs: []string{"ev-method"}},
			"steps":         {EvidenceIDs: []string{"ev-method"}},
			"applicability": {EvidenceIDs: []string{"ev-method"}},
		},
		SourceParagraphs: []MethodologySourceParagraph{{
			Text:        "方法论来源段落。",
			EvidenceIDs: []string{"ev-method"},
		}},
	}
	var previous []byte
	for run := 0; run < 50; run++ {
		render, err := RenderMethodologyPage(input)
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

func TestRenderMethodologyPageNormalizesFrameworkFieldKeyCasing(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"Input": "项目材料",
		"STEPS": "执行步骤",
	}, "ev-method")
	render, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input": {EvidenceIDs: []string{"ev-method"}},
			"steps": {EvidenceIDs: []string{"ev-method"}},
		},
	})
	if err != nil {
		t.Fatalf("render methodology page: %v", err)
	}
	if !strings.Contains(render.Content, "输入：项目材料") ||
		!strings.Contains(render.Content, "步骤：执行步骤") {
		t.Fatalf("case-insensitive framework fields were not rendered: %s", render.Content)
	}
}

func TestValidateMethodologyPageRenderRejectsMismatchedFrontmatterTitleAndType(t *testing.T) {
	object := methodologyTemplateObject(map[string]string{
		"input": "项目材料",
		"steps": "执行步骤",
	}, "ev-method")
	render, err := RenderMethodologyPage(MethodologyTemplateInput{
		Object:    object,
		TimeRange: "00:05:00-00:05:40",
		FieldEvidence: map[string]MethodologyFieldEvidence{
			"input": {EvidenceIDs: []string{"ev-method"}},
			"steps": {EvidenceIDs: []string{"ev-method"}},
		},
	})
	if err != nil {
		t.Fatalf("render methodology page: %v", err)
	}
	for _, replacement := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "title", old: "title: 用户留存归因", new: "title: 另一个方法论", want: "title and frontmatter title"},
		{name: "type", old: "type: methodology", new: "type: concept", want: "type and primary_type"},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			mutated := strings.Replace(render.Content, replacement.old, replacement.new, 1)
			if mutated == render.Content {
				t.Fatalf("fixture replacement %q did not apply", replacement.name)
			}
			if err := ValidateMethodologyPageRender(MethodologyPageRender{
				PageType: render.PageType,
				Title:    render.Title,
				Content:  mutated,
			}, object.Title); err == nil || !strings.Contains(err.Error(), replacement.want) {
				t.Fatalf("validation error = %v, want %q", err, replacement.want)
			}
		})
	}
}

func methodologyTemplateObject(fields map[string]string, evidenceID string) ClassifiedKnowledge {
	return ClassifiedKnowledge{
		CandidateID:              "methodology-template",
		SourceDocumentID:         "doc-methodology",
		SourceVideoID:            "video-methodology",
		TranscriptGeneration:     "generation-methodology",
		PrimaryType:              TypeMethodology,
		Title:                    "用户留存归因",
		CoreContent:              "通过结构化步骤定位用户留存下降的原因。",
		StructureFields:          fields,
		EvidenceIDs:              []string{evidenceID},
		ClassificationConfidence: 0.93,
		AuditStatus:              "passed",
	}
}
