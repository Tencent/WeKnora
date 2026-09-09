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
core_content: 通过异常数据定位业务原因。
evidence_ids: [chunk-1]
source_refs: [source-document-1]
structure_fields:
  input: 留存曲线
  steps: 按渠道拆分并对比异常
---
# 异常归因方法`

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
core_content: 网络效应会随参与者增加而增强。
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

func TestValidateWikiObjectPageRequiresCoreContent(t *testing.T) {
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.92
evidence_ids: [chunk-1]
source_refs: [source-document-1]
structure_fields:
  definition: 稳定定义
  mechanism: 运行机制
---
# 网络效应`

	_, err := ValidateWikiObjectPage(content, "index", "video-1", "generation-1")
	if err == nil || !strings.Contains(err.Error(), "core content") {
		t.Fatalf("expected missing core content to fail, got %v", err)
	}
}

func TestValidateWikiObjectPageRejectsBodyOnlyCoreContent(t *testing.T) {
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.92
evidence_ids: [chunk-1]
source_refs: [source-document-1]
structure_fields:
  definition: 稳定定义
  mechanism: 运行机制
---
# 网络效应

一句话概述：正文不能替代结构化核心内容。`

	_, err := ValidateWikiObjectWritePage(content, "index", "video-1", "generation-1")
	if err == nil || !strings.Contains(err.Error(), "frontmatter.core_content") {
		t.Fatalf("expected body-only core content to fail, got %v", err)
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

func TestCompareIdentityUsesSemanticContentAfterRemovingTypeDecoration(t *testing.T) {
	base := IdentityCandidate{
		KnowledgeObjectID: "deepseek-canonical", KnowledgeType: TypeEntity, EntitySubType: "product",
		Title: "DeepSeek", CoreContent: "DeepSeek 是可读写 Obsidian 本地文件并调用工具的 AI 助手。",
		SourceVideoID: "video-1", TranscriptGeneration: "generation-1",
		StructureFields: map[string]string{
			"product_type":  "AI 编程与工具调用助手",
			"core_function": "读写本地 Markdown 文件并调用工具",
		},
	}
	decorated := IdentityCandidate{
		KnowledgeObjectID: "deepseek-duplicate", KnowledgeType: TypeEntity, EntitySubType: "product",
		Title: "DeepSeek（实体）", CoreContent: "视频中的 DeepSeek 是能够读写本地文件、调用工具的 AI Agent 配套工具。",
		SourceVideoID: "video-1", TranscriptGeneration: "generation-1",
		StructureFields: map[string]string{
			"product_type":  "AI 编程/工具调用助手",
			"core_function": "能够读写本地文件并调用工具",
		},
	}

	if !IdentityRecall(base, decorated) {
		t.Fatal("decorated semantic duplicate was not recalled")
	}
	comparison := CompareIdentity(base, decorated)
	if comparison.Decision != IdentityReuse {
		t.Fatalf("comparison = %#v, want reuse", comparison)
	}
	if NormalizeIdentity(decorated.Title) != "deepseek" {
		t.Fatalf("normalized decorated title = %q", NormalizeIdentity(decorated.Title))
	}
}

func TestCanonicalKnowledgeTitleRemovesOnlyTypeDecoration(t *testing.T) {
	tests := map[string]string{
		"Codex（实体）":              "Codex",
		"Codex【实体】":              "Codex",
		"【概念】第二大脑":               "第二大脑",
		"[方法论] 用户访谈":             "用户访谈",
		"第二大脑 (concept)":         "第二大脑",
		"个人知识库五关标准（概念）（concept）": "个人知识库五关标准",
		"AI Agent 第二大脑":          "AI Agent 第二大脑",
		"Codex 企业版":              "Codex 企业版",
	}
	for input, want := range tests {
		if got := CanonicalKnowledgeTitle(input); got != want {
			t.Fatalf("CanonicalKnowledgeTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareIdentityUsesConceptMeaningWithoutBlindContainmentMerge(t *testing.T) {
	secondBrain := IdentityCandidate{
		KnowledgeObjectID: "second-brain", KnowledgeType: TypeConcept, Title: "第二大脑",
		CoreContent:   "第二大脑是由本地知识库和 AI Agent 组成、能够调用知识并执行工作的系统。",
		SourceVideoID: "video-1", TranscriptGeneration: "generation-1",
		StructureFields: map[string]string{
			"definition": "把静态档案库升级为可执行的知识系统",
			"components": "本地 Markdown 知识库、AI Agent 和方法模板",
			"mechanism":  "AI Agent 调用知识、执行方法并回写经验",
		},
	}
	aiAgentSecondBrain := IdentityCandidate{
		KnowledgeObjectID: "second-brain-duplicate", KnowledgeType: TypeConcept, Title: "AI Agent 第二大脑（概念）",
		CoreContent:   "接入 AI Agent 后，Obsidian 从静态档案库升级为能够调用知识并执行工作的第二大脑。",
		SourceVideoID: "video-1", TranscriptGeneration: "generation-1",
		StructureFields: map[string]string{
			"definition": "AI Agent 接入本地知识库后形成的可执行第二大脑",
			"components": "Obsidian、AI Agent 和方法模板",
			"mechanism":  "读取知识、执行任务并把经验回写到体系",
		},
	}
	unrelated := aiAgentSecondBrain
	unrelated.KnowledgeObjectID = "memory-training"
	unrelated.Title = "第二大脑训练营"
	unrelated.CoreContent = "第二大脑训练营是一项面向知识工作者的收费培训服务。"
	unrelated.StructureFields = map[string]string{
		"definition": "教授笔记整理方法的培训课程",
		"components": "课程、作业和社群",
	}

	if got := CompareIdentity(secondBrain, aiAgentSecondBrain); got.Decision != IdentityReuse {
		t.Fatalf("semantic concept duplicate = %#v, want reuse", got)
	}
	if got := CompareIdentity(secondBrain, unrelated); got.Decision == IdentityReuse {
		t.Fatalf("title containment caused false merge: %#v", got)
	}
}

func TestCompareIdentityReusesObservedGraphSemanticDuplicates(t *testing.T) {
	tests := []struct {
		name  string
		left  IdentityCandidate
		right IdentityCandidate
	}{
		{
			name: "personal knowledge base five gates",
			left: IdentityCandidate{
				KnowledgeObjectID: "five-gates", KnowledgeType: TypeConcept, Title: "个人知识库五关标准（概念）",
				CoreContent: "五关标准是衡量个人知识库是否真正可用的五项独立条件:安全归属、碎片输入体系化、方法可执行、经验持续迭代、灵活可拓展。",
				StructureFields: map[string]string{
					"definition": "衡量一个个人知识库是否真正可用的五项独立条件",
					"components": "安全归属 + 碎片输入体系化 + 方法可执行 + 经验持续迭代 + 灵活可拓展",
					"mechanism":  "候选知识库逐项过五关;任何一项不满足都视为不够用",
				},
			},
			right: IdentityCandidate{
				KnowledgeObjectID: "five-conditions", KnowledgeType: TypeConcept, Title: "个人知识库五大条件",
				CoreContent: "个人知识库五大条件是主讲人基于十余年带千名学员经验总结出的评判知识库是否真正有用的五项独立条件，分别是安全归属、输入可碎片化沉淀须体系化、方法可执行、经验持续迭代、灵活可拓展。",
				StructureFields: map[string]string{
					"definition": "判断个人知识库是否真正有用的五项独立条件",
					"components": "第一关安全归属；第二关输入可碎片化、沉淀必须体系化；第三关方法要能执行；第四关经验要持续迭代；第五关灵活拓展",
					"mechanism":  "五项条件并列成立，缺一不可",
				},
			},
		},
		{
			name: "codex entity decoration",
			left: IdentityCandidate{
				KnowledgeObjectID: "codex", KnowledgeType: TypeEntity, EntitySubType: "product", Title: "Codex",
				CoreContent:     "被列为可读写 Obsidian Markdown 的 AI 工具之一。",
				StructureFields: map[string]string{"product_type": "AI 编程/工具调用助手", "core_function": "直接读写本地 Markdown 文件"},
			},
			right: IdentityCandidate{
				KnowledgeObjectID: "codex-entity", KnowledgeType: TypeEntity, EntitySubType: "product", Title: "Codex（实体）",
				CoreContent:     "Codex 是视频中提到的本地 AI 助手之一,可作为 Obsidian 的 AI Agent 配套使用。",
				StructureFields: map[string]string{"product_type": "AI 编程/工具调用助手", "core_function": "能读写本地文件、按方法执行任务的 AI 助手"},
			},
		},
		{
			name: "agent second brain compound title",
			left: IdentityCandidate{
				KnowledgeObjectID: "agent-second-brain", KnowledgeType: TypeConcept, Title: "AI Agent 第二大脑（概念）",
				CoreContent: "AI Agent 是能读写本地文件、调用工具、按流程执行任务的 AI 助手,是把 Obsidian 从静态档案库激活为第二大脑的关键能力。",
				StructureFields: map[string]string{
					"definition": "AI Agent 让 Obsidian 从静态档案库升级为可执行的第二大脑",
					"components": "本地文件读写能力、工具调用能力、流程编排能力、自然语言接口",
					"mechanism":  "Agent 读取方法模板、执行任务并把结果回写到 Obsidian",
				},
			},
			right: IdentityCandidate{
				KnowledgeObjectID: "second-brain", KnowledgeType: TypeConcept, Title: "第二大脑",
				CoreContent: "接入 AI Agent 后的 Obsidian 不只是存东西的档案库，而是能帮你执行工作的第二大脑，把知识调出来、按方法执行、再把新经验放回体系。",
				StructureFields: map[string]string{
					"definition": "接入 AI Agent 后的 Obsidian 相对于单纯本地档案库的概念升级",
					"components": "本地 Markdown 知识库、AI Agent、使用复盘更新闭环",
					"mechanism":  "知识被主动调用、按方法执行并回流新经验",
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			comparison := CompareIdentity(test.left, test.right)
			if comparison.Decision != IdentityReuse {
				t.Fatalf("observed semantic duplicate = %#v, want reuse", comparison)
			}
		})
	}
}

func TestGroupSemanticIdentitiesKeepsDirectDuplicateTogetherWhenAnotherVariantIsPartial(t *testing.T) {
	canonical := IdentityCandidate{
		KnowledgeObjectID: "codex", KnowledgeType: TypeEntity, EntitySubType: "product", Title: "Codex",
		CoreContent:     "被列为可读写 Obsidian Markdown 的 AI 工具之一。",
		StructureFields: map[string]string{"product_type": "AI 编程工具", "core_function": "直接读写本地 Markdown 文件"},
	}
	partial := IdentityCandidate{
		KnowledgeObjectID: "codex-local", KnowledgeType: TypeEntity, EntitySubType: "product", Title: "Codex 本地文件工具",
		CoreContent:     "被列为可读写 Obsidian Markdown 的 AI 工具之一。",
		StructureFields: map[string]string{"product_type": "AI 编程工具", "core_function": "直接读写本地 Markdown 文件"},
	}
	decorated := IdentityCandidate{
		KnowledgeObjectID: "codex-decorated", KnowledgeType: TypeEntity, EntitySubType: "product", Title: "Codex（实体）",
		CoreContent:     "Codex 是本地 AI 助手之一，可作为 AI Agent 配套使用。",
		StructureFields: map[string]string{"product_type": "AI 编程工具", "core_function": "按方法执行任务"},
	}
	if CompareIdentity(canonical, partial).Decision != IdentityReuse || CompareIdentity(canonical, decorated).Decision != IdentityReuse {
		t.Fatal("fixture must directly recall both Codex variants")
	}
	if CompareIdentity(partial, decorated).Decision == IdentityReuse {
		t.Fatal("fixture must reproduce the partial-link grouping case")
	}
	groups := GroupSemanticIdentities([]IdentityCandidate{canonical, partial, decorated})
	if len(groups) != 1 {
		t.Fatalf("direct semantic duplicate split by grouping order: %#v", groups)
	}
}
