package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderConceptPageUsesFrameworkOrderAndChineseLabels(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "用户增加会提升产品价值",
		"mechanism":  "连接关系增加整体效用",
	}, "ev-concept")
	render, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
			"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
		},
	})
	if err != nil {
		t.Fatalf("render concept page: %v", err)
	}
	if render.PageType != "index" || render.Title != object.Title {
		t.Fatalf("render metadata = %#v", render)
	}
	if err := ValidateConceptPageRender(render, object.Title); err != nil {
		t.Fatalf("validate render: %v", err)
	}
	if strings.Count(render.Content, "# "+object.Title) != 1 {
		t.Fatalf("H1 is not unique: %s", render.Content)
	}
	definition := strings.Index(render.Content, "- 定义：")
	mechanism := strings.Index(render.Content, "- 运行机制：")
	if definition < 0 || mechanism < 0 || definition >= mechanism {
		t.Fatalf("framework field order is wrong: %s", render.Content)
	}
	if !strings.Contains(render.Content, "\n概念\n") {
		t.Fatalf("information nature is missing: %s", render.Content)
	}
}

func TestRenderConceptPageSkipsEmptyUnsupportedAndUnevidencedFields(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition":  "可复用的抽象知识单元",
		"components":  "",
		"mechanism":   "内部关系形成运行结果",
		"distinction": "不得渲染",
		"identity":    "外来实体字段",
	}, "ev-concept")
	render, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition":  {EvidenceIDs: []string{"ev-concept"}},
			"components":  {EvidenceIDs: []string{"ev-concept"}},
			"mechanism":   {EvidenceIDs: []string{"ev-concept"}},
			"distinction": {},
		},
	})
	if err != nil {
		t.Fatalf("render concept page: %v", err)
	}
	for _, unexpected := range []string{"构成要素：", "相邻区别：", "外来实体字段", "identity"} {
		if strings.Contains(render.Content, unexpected) {
			t.Fatalf("unexpected field rendered %q: %s", unexpected, render.Content)
		}
	}
	if !strings.Contains(render.Content, "定义：可复用的抽象知识单元") ||
		!strings.Contains(render.Content, "运行机制：内部关系形成运行结果") {
		t.Fatalf("supported evidenced fields are missing: %s", render.Content)
	}
}

func TestRenderConceptPageRejectsFieldOrSourceEvidenceOutsideConcept(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "可复用的抽象知识单元",
		"mechanism":  "内部关系形成运行结果",
	}, "ev-concept")
	_, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"foreign"}},
			"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on concept") {
		t.Fatalf("field evidence error = %v", err)
	}

	_, err = RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
			"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
		},
		SourceParagraphs: []ConceptSourceParagraph{{
			Text:        "无效来源",
			EvidenceIDs: []string{"foreign"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on concept") {
		t.Fatalf("source evidence error = %v", err)
	}
}

func TestRenderConceptPageRequiresTwoFieldsWithEvidence(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "可复用的抽象知识单元",
		"mechanism":  "内部关系形成运行结果",
	}, "ev-concept")
	_, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "at least 2 populated fields with evidence") {
		t.Fatalf("missing field evidence error = %v", err)
	}
}

func TestRenderConceptPageRejectsPlaceholderTimeRange(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "可复用的抽象知识单元",
		"mechanism":  "内部关系形成运行结果",
	}, "ev-concept")
	for _, timeRange := range []string{"TBD", "待定-待定", "placeholder"} {
		t.Run(timeRange, func(t *testing.T) {
			_, err := RenderConceptPage(ConceptTemplateInput{
				Object:    object,
				TimeRange: timeRange,
				FieldEvidence: map[string]ConceptFieldEvidence{
					"definition": {EvidenceIDs: []string{"ev-concept"}},
					"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
				},
			})
			if err == nil || !strings.Contains(err.Error(), "must not be a placeholder") {
				t.Fatalf("placeholder time range error = %v", err)
			}
		})
	}
}

func TestRenderConceptPageManualAcceptanceSamples(t *testing.T) {
	t.Run("网络效应", func(t *testing.T) {
		object := conceptTemplateObject(map[string]string{
			"definition": "用户增加会提升产品价值",
			"mechanism":  "连接关系增加整体效用",
		}, "ev-network-effect")
		render, err := RenderConceptPage(ConceptTemplateInput{
			Object:    object,
			TimeRange: "00:02:00-00:02:20",
			FieldEvidence: map[string]ConceptFieldEvidence{
				"definition": {EvidenceIDs: []string{"ev-network-effect"}},
				"mechanism":  {EvidenceIDs: []string{"ev-network-effect"}},
			},
			SourceParagraphs: []ConceptSourceParagraph{{
				Text:        "新增用户会增加已有用户可以连接的对象。",
				EvidenceIDs: []string{"ev-network-effect"},
			}},
		})
		if err != nil {
			t.Fatalf("render network effect: %v", err)
		}
		t.Logf("\n%s", render.Content)
	})

	t.Run("三非理论", func(t *testing.T) {
		object := conceptTemplateObject(map[string]string{
			"definition": "用于识别早期项目非共识、非连续和非线性特征的抽象框架",
			"components": "非共识、非连续、非线性",
		}, "ev-three-non")
		object.Title = "三非理论"
		object.CoreContent = "三非理论用于从非共识、非连续和非线性三个维度理解早期项目。"
		render, err := RenderConceptPage(ConceptTemplateInput{
			Object:    object,
			TimeRange: "00:12:00-00:12:30",
			FieldEvidence: map[string]ConceptFieldEvidence{
				"definition": {EvidenceIDs: []string{"ev-three-non"}},
				"components": {EvidenceIDs: []string{"ev-three-non"}},
			},
		})
		if err != nil {
			t.Fatalf("render three-non theory: %v", err)
		}
		t.Logf("\n%s", render.Content)
	})

	t.Run("普通词实", func(t *testing.T) {
		object := conceptTemplateObject(map[string]string{
			"definition": "知道其内容的普通词义解释",
			"components": "字面解释",
		}, "ev-ordinary-word")
		object.Title = "实"
		_, err := RenderConceptPage(ConceptTemplateInput{
			Object:    object,
			TimeRange: "00:20:00-00:20:10",
			FieldEvidence: map[string]ConceptFieldEvidence{
				"definition": {EvidenceIDs: []string{"ev-ordinary-word"}},
				"components": {EvidenceIDs: []string{"ev-ordinary-word"}},
			},
		})
		if err == nil || !strings.Contains(err.Error(), "ordinary word meaning") {
			t.Fatalf("ordinary word should be rejected, error = %v", err)
		}
		t.Logf("rejected as expected: %v", err)
	})
}

func TestRenderConceptPageRejectsOrdinaryWordEvenWithTwoFields(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "知道其内容的普通词义解释",
		"components": "字面解释",
	}, "ev-ordinary")
	object.Title = "实"
	_, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-ordinary"}},
			"components": {EvidenceIDs: []string{"ev-ordinary"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "ordinary word meaning") {
		t.Fatalf("ordinary word error = %v", err)
	}
}

func TestRenderConceptPageKeepsDescriptionAliasesAndSourceParagraph(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "用户增加会提升产品价值",
		"components": "用户基数与连接密度",
	}, "ev-concept")
	render, err := RenderConceptPage(ConceptTemplateInput{
		Object:      object,
		Aliases:     []string{"网络效应", "Network Effect", " Network Effect "},
		Description: "一个可独立理解并可复用的抽象知识单元。",
		TimeRange:   "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
			"components": {EvidenceIDs: []string{"ev-concept"}},
		},
		SourceParagraphs: []ConceptSourceParagraph{{
			Text:        "新增用户会增加已有用户可以连接的对象。",
			EvidenceIDs: []string{"ev-concept"},
		}},
	})
	if err != nil {
		t.Fatalf("render concept page: %v", err)
	}
	for _, expected := range []string{
		"一个可独立理解并可复用的抽象知识单元。",
		"Network Effect",
		"新增用户会增加已有用户可以连接的对象。",
		"定义：用户增加会提升产品价值",
		"构成要素：用户基数与连接密度",
	} {
		if !strings.Contains(render.Content, expected) {
			t.Fatalf("rendered page missing %q: %s", expected, render.Content)
		}
	}
	if strings.Contains(render.Content, "- 网络效应\n") {
		t.Fatal("title duplicate should not be rendered as alias")
	}
}

func TestRenderConceptPagePassesWikiObjectContract(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "可复用的抽象知识单元",
		"mechanism":  "内部关系形成运行结果",
	}, "ev-concept")
	render, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
			"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
		},
		SourceParagraphs: []ConceptSourceParagraph{{
			Text:        "概念来源段落。",
			EvidenceIDs: []string{"ev-concept"},
		}},
	})
	if err != nil {
		t.Fatalf("render concept page: %v", err)
	}
	validation, err := ValidateWikiObjectPage(render.Content, render.PageType, object.SourceVideoID, object.TranscriptGeneration)
	if err != nil {
		t.Fatalf("rendered concept page failed Wiki contract: %v\n%s", err, render.Content)
	}
	if validation.Title != object.Title || validation.KnowledgeType != TypeConcept {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestRenderConceptPageIsByteStableAcrossRepeatedRuns(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition":  "可复用的抽象知识单元",
		"distinction": "与规模效应的价值来源不同",
	}, "ev-concept")
	input := ConceptTemplateInput{
		Object:    object,
		Aliases:   []string{"Network Effect", "网络效应"},
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition":  {EvidenceIDs: []string{"ev-concept"}},
			"distinction": {EvidenceIDs: []string{"ev-concept"}},
		},
		SourceParagraphs: []ConceptSourceParagraph{{
			Text:        "概念来源段落。",
			EvidenceIDs: []string{"ev-concept"},
		}},
	}
	var previous []byte
	for run := 0; run < 50; run++ {
		render, err := RenderConceptPage(input)
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

func TestRenderConceptPageNormalizesFrameworkFieldKeyCasing(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"Definition": "可复用的抽象知识单元",
		"MECHANISM":  "内部关系形成运行结果",
	}, "ev-concept")
	render, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
			"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
		},
	})
	if err != nil {
		t.Fatalf("render concept page: %v", err)
	}
	if !strings.Contains(render.Content, "定义：可复用的抽象知识单元") ||
		!strings.Contains(render.Content, "运行机制：内部关系形成运行结果") {
		t.Fatalf("case-insensitive framework fields were not rendered: %s", render.Content)
	}
}

func TestValidateConceptPageRenderRejectsMismatchedFrontmatterTitleAndType(t *testing.T) {
	object := conceptTemplateObject(map[string]string{
		"definition": "可复用的抽象知识单元",
		"mechanism":  "内部关系形成运行结果",
	}, "ev-concept")
	render, err := RenderConceptPage(ConceptTemplateInput{
		Object:    object,
		TimeRange: "00:02:00-00:02:20",
		FieldEvidence: map[string]ConceptFieldEvidence{
			"definition": {EvidenceIDs: []string{"ev-concept"}},
			"mechanism":  {EvidenceIDs: []string{"ev-concept"}},
		},
	})
	if err != nil {
		t.Fatalf("render concept page: %v", err)
	}
	for _, replacement := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "title", old: "title: 网络效应", new: "title: 另一个概念", want: "title and frontmatter title"},
		{name: "type", old: "type: concept", new: "type: entity", want: "type and primary_type"},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			mutated := strings.Replace(render.Content, replacement.old, replacement.new, 1)
			if mutated == render.Content {
				t.Fatalf("fixture replacement %q did not apply", replacement.name)
			}
			if err := ValidateConceptPageRender(ConceptPageRender{
				PageType: render.PageType,
				Title:    render.Title,
				Content:  mutated,
			}, object.Title); err == nil || !strings.Contains(err.Error(), replacement.want) {
				t.Fatalf("validation error = %v, want %q", err, replacement.want)
			}
		})
	}
}

func conceptTemplateObject(fields map[string]string, evidenceID string) ClassifiedKnowledge {
	return ClassifiedKnowledge{
		CandidateID:              "concept-template",
		SourceDocumentID:         "doc-concept",
		SourceVideoID:            "video-concept",
		TranscriptGeneration:     "generation-concept",
		PrimaryType:              TypeConcept,
		Title:                    "网络效应",
		CoreContent:              "用户增加会提升产品价值。",
		StructureFields:          fields,
		EvidenceIDs:              []string{evidenceID},
		ClassificationConfidence: 0.91,
		AuditStatus:              "passed",
	}
}
