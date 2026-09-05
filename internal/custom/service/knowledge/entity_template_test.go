package knowledge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderEntityPageCoversSixSubtypesAndUsesFrameworkOrder(t *testing.T) {
	tests := []struct {
		subtype string
		fields  map[string]string
		labels  []string
		nature  string
	}{
		{"person", map[string]string{"identity": "投资人", "background": "长期从事早期投资"}, []string{"职业身份", "教育背景与经历"}, "人物"},
		{"organization", map[string]string{"org_type": "投资机构", "industry": "风险投资"}, []string{"机构类型", "所在行业"}, "机构"},
		{"product", map[string]string{"product_type": "硬件", "core_function": "记录上下文"}, []string{"产品类别", "核心功能"}, "产品"},
		{"technology", map[string]string{"tech_category": "人工智能", "application_area": "投资研究"}, []string{"技术分类", "应用领域"}, "技术"},
		{"industry", map[string]string{"scope": "早期科技投资", "key_trends": "AI 原生组织"}, []string{"行业范围", "关键趋势"}, "行业"},
		{"place", map[string]string{"place_type": "城市", "associated_activity": "项目所在地"}, []string{"地点类型", "关联活动"}, "地点"},
	}

	for _, tt := range tests {
		t.Run(tt.subtype, func(t *testing.T) {
			object := entityTemplateObject(tt.subtype, tt.fields, "ev-"+tt.subtype)
			render, err := RenderEntityPage(EntityTemplateInput{
				Object:    object,
				TimeRange: "00:01:00-00:01:30",
				FieldEvidence: map[string]EntityFieldEvidence{
					firstEntityField(tt.fields):  {EvidenceIDs: []string{"ev-" + tt.subtype}},
					secondEntityField(tt.fields): {EvidenceIDs: []string{"ev-" + tt.subtype}},
				},
				SourceParagraphs: []EntitySourceParagraph{{
					Text:        "来源段落：" + tt.subtype,
					EvidenceIDs: []string{"ev-" + tt.subtype},
				}},
			})
			if err != nil {
				t.Fatalf("render entity page: %v", err)
			}
			if render.PageType != "index" || render.Title != object.Title {
				t.Fatalf("render metadata = %#v", render)
			}
			if err := ValidateEntityPageRender(render, object.Title); err != nil {
				t.Fatalf("validate render: %v", err)
			}
			if strings.Count(render.Content, "# "+object.Title) != 1 {
				t.Fatalf("H1 is not unique: %s", render.Content)
			}
			first := strings.Index(render.Content, "- "+tt.labels[0]+"：")
			second := strings.Index(render.Content, "- "+tt.labels[1]+"：")
			if first < 0 || second < 0 || first >= second {
				t.Fatalf("framework field order is wrong: %s", render.Content)
			}
			if !strings.Contains(render.Content, "\n"+tt.nature+"\n") {
				t.Fatalf("information nature %q is missing: %s", tt.nature, render.Content)
			}
		})
	}
}

func TestRenderEntityPageSkipsEmptyAndUnsupportedFieldsWithoutInventingValues(t *testing.T) {
	object := entityTemplateObject("person", map[string]string{
		"identity":   "投资人",
		"background": "",
		"expertise":  "早期投资",
		"standpoint": "长期主义",
		"claim":      "不得渲染",
	}, "ev-person")
	render, err := RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"identity":   {EvidenceIDs: []string{"ev-person"}},
			"expertise":  {EvidenceIDs: []string{"ev-person"}},
			"standpoint": {EvidenceIDs: []string{}},
		},
	})
	if err != nil {
		t.Fatalf("render entity page: %v", err)
	}
	if strings.Contains(render.Content, "教育背景与经历") || strings.Contains(render.Content, "代表性观点") {
		t.Fatalf("empty or unsupported evidence field was rendered: %s", render.Content)
	}
	if strings.Contains(render.Content, "不得渲染") || strings.Contains(render.Content, "claim") {
		t.Fatalf("foreign structure field was rendered: %s", render.Content)
	}
}

func TestRenderEntityPageRejectsFieldOrSourceEvidenceOutsideEntity(t *testing.T) {
	object := entityTemplateObject("technology", map[string]string{
		"tech_category":    "人工智能",
		"application_area": "投资研究",
	}, "ev-technology")
	_, err := RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"tech_category":    {EvidenceIDs: []string{"foreign"}},
			"application_area": {EvidenceIDs: []string{"ev-technology"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on entity") {
		t.Fatalf("field evidence error = %v", err)
	}

	_, err = RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"tech_category":    {EvidenceIDs: []string{"ev-technology"}},
			"application_area": {EvidenceIDs: []string{"ev-technology"}},
		},
		SourceParagraphs: []EntitySourceParagraph{{
			Text:        "无效来源",
			EvidenceIDs: []string{"foreign"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "not present on entity") {
		t.Fatalf("source evidence error = %v", err)
	}

	_, err = RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"tech_category":    {EvidenceIDs: []string{"ev-technology"}},
			"application_area": {EvidenceIDs: []string{"ev-technology"}},
		},
		SourceParagraphs: []EntitySourceParagraph{{
			Text: "缺少证据",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires evidence_ids") {
		t.Fatalf("missing source evidence error = %v", err)
	}
}

func TestRenderEntityPageRequiresEvidenceForDisplayedFields(t *testing.T) {
	object := entityTemplateObject("place", map[string]string{
		"place_type":          "城市",
		"associated_activity": "项目所在地",
	}, "ev-place")
	_, err := RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"place_type": {EvidenceIDs: []string{"ev-place"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "with evidence") {
		t.Fatalf("missing field evidence error = %v", err)
	}
}

func TestRenderEntityPageRequiresTimeRangeAndRejectsDuplicateNormalizedEvidenceFields(t *testing.T) {
	object := entityTemplateObject("person", map[string]string{
		"identity": "投资人", "expertise": "早期投资",
	}, "ev-person")
	_, err := RenderEntityPage(EntityTemplateInput{
		Object: object,
		FieldEvidence: map[string]EntityFieldEvidence{
			"identity":  {EvidenceIDs: []string{"ev-person"}},
			"expertise": {EvidenceIDs: []string{"ev-person"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "time_range is required") {
		t.Fatalf("missing time range error = %v", err)
	}

	_, err = RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"identity":   {EvidenceIDs: []string{"ev-person"}},
			" IDENTITY ": {EvidenceIDs: []string{"ev-person"}},
			"expertise":  {EvidenceIDs: []string{"ev-person"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate normalized field") {
		t.Fatalf("duplicate normalized field error = %v", err)
	}
}

func TestRenderEntityPageKeepsDescriptionAliasesAndSourceParagraph(t *testing.T) {
	object := entityTemplateObject("organization", map[string]string{
		"org_type": "研究机构", "industry": "人工智能",
	}, "ev-org")
	render, err := RenderEntityPage(EntityTemplateInput{
		Object:      object,
		TimeRange:   "00:01:00-00:01:30",
		Aliases:     []string{"  AI Lab ", "AI Lab", "实体-organization"},
		Description: "视频中被明确提及的研究机构。",
		FieldEvidence: map[string]EntityFieldEvidence{
			"org_type": {EvidenceIDs: []string{"ev-org"}},
			"industry": {EvidenceIDs: []string{"ev-org"}},
		},
		SourceParagraphs: []EntitySourceParagraph{{
			Text:        "研究机构参与了该项目。",
			EvidenceIDs: []string{"ev-org"},
		}},
	})
	if err != nil {
		t.Fatalf("render entity page: %v", err)
	}
	for _, expected := range []string{"视频中被明确提及的研究机构。", "AI Lab", "研究机构参与了该项目。", "机构类型：研究机构", "所在行业：人工智能"} {
		if !strings.Contains(render.Content, expected) {
			t.Fatalf("rendered page missing %q: %s", expected, render.Content)
		}
	}
	for _, pair := range []struct {
		before string
		after  string
	}{
		{before: "## 别名", after: "一句话概述："},
		{before: "一句话概述：", after: "## 关键信息维度"},
		{before: "## 关键信息维度", after: "## 知识来源"},
		{before: "## 知识来源", after: "## 信息性质"},
	} {
		if strings.Index(render.Content, pair.before) >= strings.Index(render.Content, pair.after) {
			t.Fatalf("entity body order is wrong: %q should be before %q\n%s", pair.before, pair.after, render.Content)
		}
	}
	if strings.Contains(render.Content, "- 实体-organization\n") {
		t.Fatal("title duplicate should not be rendered as alias")
	}
}

func TestRenderEntityPagePassesWikiObjectContract(t *testing.T) {
	object := entityTemplateObject("person", map[string]string{
		"identity":  "投资人",
		"expertise": "早期投资",
	}, "ev-person")
	render, err := RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"identity":  {EvidenceIDs: []string{"ev-person"}},
			"expertise": {EvidenceIDs: []string{"ev-person"}},
		},
		SourceParagraphs: []EntitySourceParagraph{{
			Text:        "该人物长期从事早期投资。",
			EvidenceIDs: []string{"ev-person"},
		}},
	})
	if err != nil {
		t.Fatalf("render entity page: %v", err)
	}
	validation, err := ValidateWikiObjectPage(render.Content, render.PageType, object.SourceVideoID, object.TranscriptGeneration)
	if err != nil {
		t.Fatalf("rendered entity page failed Wiki contract: %v\n%s", err, render.Content)
	}
	if validation.Title != object.Title || validation.EntitySubType != "person" || validation.KnowledgeType != TypeEntity {
		t.Fatalf("validation = %#v", validation)
	}
}

func TestRenderEntityPageIsByteStableAcrossRepeatedRuns(t *testing.T) {
	object := entityTemplateObject("product", map[string]string{
		"product_type":  "可穿戴硬件",
		"core_function": "记录上下文",
	}, "ev-product")
	input := EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		Aliases: []string{
			"Context Machine",
			"CM",
		},
		FieldEvidence: map[string]EntityFieldEvidence{
			"product_type":  {EvidenceIDs: []string{"ev-product"}},
			"core_function": {EvidenceIDs: []string{"ev-product"}},
		},
		SourceParagraphs: []EntitySourceParagraph{{
			Text:        "产品用于记录上下文。",
			EvidenceIDs: []string{"ev-product"},
		}},
	}
	var previous []byte
	for run := 0; run < 50; run++ {
		render, err := RenderEntityPage(input)
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

func TestRenderEntityPageNormalizesFrameworkFieldKeyCasing(t *testing.T) {
	object := entityTemplateObject("person", map[string]string{
		"Identity":  "投资人",
		"EXPERTISE": "早期投资",
	}, "ev-person")
	render, err := RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"identity":  {EvidenceIDs: []string{"ev-person"}},
			"expertise": {EvidenceIDs: []string{"ev-person"}},
		},
	})
	if err != nil {
		t.Fatalf("render entity page: %v", err)
	}
	if !strings.Contains(render.Content, "职业身份：投资人") || !strings.Contains(render.Content, "擅长领域：早期投资") {
		t.Fatalf("case-insensitive framework fields were not rendered: %s", render.Content)
	}
}

func TestValidateEntityPageRenderRejectsMismatchedFrontmatterTitleAndType(t *testing.T) {
	object := entityTemplateObject("person", map[string]string{
		"identity": "投资人", "expertise": "早期投资",
	}, "ev-person")
	render, err := RenderEntityPage(EntityTemplateInput{
		Object:    object,
		TimeRange: "00:01:00-00:01:30",
		FieldEvidence: map[string]EntityFieldEvidence{
			"identity":  {EvidenceIDs: []string{"ev-person"}},
			"expertise": {EvidenceIDs: []string{"ev-person"}},
		},
	})
	if err != nil {
		t.Fatalf("render entity page: %v", err)
	}
	for _, replacement := range []struct {
		name string
		old  string
		new  string
		want string
	}{
		{name: "title", old: "title: 实体-person", new: "title: 另一个人物", want: "title and canonical_name"},
		{name: "canonical-name", old: "canonical_name: 实体-person", new: "canonical_name: 另一个人物", want: "title and canonical_name"},
		{name: "type", old: "type: entity", new: "type: concept", want: "type and primary_type"},
	} {
		t.Run(replacement.name, func(t *testing.T) {
			mutated := strings.Replace(render.Content, replacement.old, replacement.new, 1)
			if mutated == render.Content {
				t.Fatalf("fixture replacement %q did not apply", replacement.name)
			}
			if err := ValidateEntityPageRender(EntityPageRender{
				PageType: render.PageType,
				Title:    render.Title,
				Content:  mutated,
			}, object.Title); err == nil || !strings.Contains(err.Error(), replacement.want) {
				t.Fatalf("validation error = %v, want %q", err, replacement.want)
			}
		})
	}
}

func entityTemplateObject(subtype string, fields map[string]string, evidenceID string) ClassifiedKnowledge {
	return ClassifiedKnowledge{
		CandidateID:              "entity-" + subtype,
		SourceDocumentID:         "doc-entity",
		SourceVideoID:            "video-entity",
		TranscriptGeneration:     "generation-entity",
		PrimaryType:              TypeEntity,
		EntitySubType:            subtype,
		Title:                    "实体-" + subtype,
		CoreContent:              "该实体在视频中的身份或用途。",
		StructureFields:          fields,
		EvidenceIDs:              []string{evidenceID},
		ClassificationConfidence: 0.91,
		AuditStatus:              "passed",
	}
}

func firstEntityField(fields map[string]string) string {
	for _, key := range []string{"identity", "org_type", "product_type", "tech_category", "scope", "place_type"} {
		if fields[key] != "" {
			return key
		}
	}
	return ""
}

func secondEntityField(fields map[string]string) string {
	for _, key := range []string{"background", "industry", "core_function", "application_area", "key_trends", "associated_activity"} {
		if fields[key] != "" {
			return key
		}
	}
	return ""
}
