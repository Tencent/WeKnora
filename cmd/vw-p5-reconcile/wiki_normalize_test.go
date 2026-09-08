package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
)

func TestPlanWikiRepairNormalizesEvidenceBackedPagesAndQuarantinesUnsafePages(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{"ev-1": {}, "ev-2": {}}}
	pages := []weknora.WikiPage{
		{
			ID: "concept-page", Slug: "concept/feedback-loop", Title: "反馈闭环", PageType: "index", Status: "published", Version: 2,
			Summary: "把结果持续回写到方法中。",
			Content: `---
knowledge_object_id: concept-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: concept
classification_confidence: 0.91
evidence_ids: [ev-1]
source_refs: [source-1]
structure_fields:
  definition: 将实践结果回写到原方法
  key_dimensions:
    - 实践
    - 复盘
relations: []
---

# 反馈闭环

把结果持续回写到方法中。`,
		},
		{
			ID: "entity-page", Slug: "entity/note-tool", Title: "笔记工具", PageType: "index", Status: "published", Version: 1,
			Summary: "用于记录知识的工具。",
			Content: `---
knowledge_object_id: entity-1
type: entity
primary_type: entity
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: product_tool
classification_confidence: 0.88
evidence_ids: [ev-2]
source_refs: [source-1]
structure_fields:
  entity_type: product_tool
  role: 记录和整理知识
---

# 笔记工具

用于记录知识的工具。`,
		},
		{
			ID: "duplicate-page", Slug: "case/duplicate", Title: "重复头", PageType: "index", Status: "published", Version: 1,
			Content: `---
type: case
type: case
source_video_id: video-1
transcript_generation: generation-1
---

# 重复头`,
		},
		{
			ID: "legacy-page", Slug: "insight/legacy", Title: "旧页", PageType: "index", Status: "published", Version: 1,
			Content: `---
type: insight
source_video_id: video-1
transcript_generation: generation-1
audit_status: aligned
---

# 旧页`,
		},
	}

	plan, err := planWikiRepair(video, pages, scope)
	require.NoError(t, err)
	require.Len(t, plan.Actions, 4)
	require.Equal(t, "normalize", actionBySlug(t, plan, "concept/feedback-loop").Action)
	require.Equal(t, "normalize", actionBySlug(t, plan, "entity/note-tool").Action)
	require.Equal(t, "quarantine", actionBySlug(t, plan, "case/duplicate").Action)
	require.Contains(t, actionBySlug(t, plan, "case/duplicate").Reason, "invalid YAML frontmatter")
	require.Equal(t, "quarantine", actionBySlug(t, plan, "insight/legacy").Action)

	concept := actionBySlug(t, plan, "concept/feedback-loop")
	validation, err := knowledge.ValidateWikiObjectPage(concept.write.Content, concept.write.PageType, video.ID, video.TranscriptGeneration)
	require.NoError(t, err)
	require.Equal(t, "概念", validationInformationNature(t, concept.write.Content))
	require.Equal(t, "实践；复盘", validation.StructureFields["components"])

	entity := actionBySlug(t, plan, "entity/note-tool")
	validation, err = knowledge.ValidateWikiObjectPage(entity.write.Content, entity.write.PageType, video.ID, video.TranscriptGeneration)
	require.NoError(t, err)
	require.Equal(t, "product", validation.EntitySubType)
	require.Equal(t, "产品", validationInformationNature(t, entity.write.Content))

	quarantined := actionBySlug(t, plan, "case/duplicate").write
	require.Equal(t, "draft", quarantined.Status)
	require.Equal(t, legacyDraftType, frontmatterString((&weknora.WikiPage{Content: quarantined.Content}).ParsedFrontmatter(), "type"))
	require.Contains(t, quarantined.Content, "type: case\ntype: case")

	index := &weknora.WikiPage{Content: plan.IndexWrite.Content}
	require.Equal(t, "knowledge_base", frontmatterString(index.ParsedFrontmatter(), "type"))
	require.Equal(t, "aligned", frontmatterString(index.ParsedFrontmatter(), "audit_status"))
	require.Contains(t, plan.IndexWrite.Content, "[[concept/feedback-loop|反馈闭环]]")
	require.Contains(t, plan.IndexWrite.Content, "[[entity/note-tool|笔记工具]]")
	require.NotContains(t, plan.IndexWrite.Content, "case/duplicate")
}

func TestPlanWikiRepairRejectsPagesWithoutActiveEvidence(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{"ev-real": {}}}
	page := weknora.WikiPage{ID: "page-1", Slug: "concept/unverified", Title: "未验证", PageType: "index", Status: "published", Summary: "未验证内容", Content: `---
knowledge_object_id: concept-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.9
evidence_ids: [c19]
source_refs: [source-1]
structure_fields:
  definition: 定义
  mechanism: 机制
---

# 未验证`}

	_, err := planWikiRepair(video, []weknora.WikiPage{page}, scope)
	require.ErrorContains(t, err, "no current-generation Wiki page has verifiable evidence")
}

func TestPlanWikiRepairMapsLegacyEvidenceIDToCurrentTranscript(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{
		SourceDocumentID: "source-1",
		EvidenceIDs:      map[string]struct{}{"ev-current": {}},
		EvidenceAliases:  map[string]string{"ev-legacy": "ev-current"},
	}
	page := weknora.WikiPage{ID: "page-1", Slug: "concept/feedback-loop", Title: "反馈闭环", PageType: "index", Status: "published", Summary: "通过反馈持续修正方法。", Content: `---
knowledge_object_id: concept-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
classification_confidence: 0.9
evidence_ids: [ev-legacy]
source_refs: [source-1]
structure_fields:
  definition: 通过实践反馈修正原有方法
  mechanism: 持续观察结果并迭代
---

# 反馈闭环`}

	plan, err := planWikiRepair(video, []weknora.WikiPage{page}, scope)
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, "normalize", plan.Actions[0].Action)
	require.Contains(t, plan.Actions[0].write.Content, "- ev-current")
	require.NotContains(t, plan.Actions[0].write.Content, "ev-legacy")
	_, err = knowledge.ValidateWikiObjectPage(plan.Actions[0].write.Content, "index", video.ID, video.TranscriptGeneration)
	require.NoError(t, err)
}

func TestPlanWikiRepairSemanticallySupersedesDuplicatePagesWithoutDeletingEvidence(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "语义归一化", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{
		"ev-deepseek-1": {}, "ev-deepseek-2": {}, "ev-brain-1": {}, "ev-brain-2": {}, "ev-brain-3": {},
	}}
	pages := []weknora.WikiPage{
		semanticRepairPage("page-deepseek", "entity/deepseek", "DeepSeek", "deepseek-canonical", "entity", "product", "ev-deepseek-1", "DeepSeek 是可读写本地文件并调用工具的 AI 助手。", map[string]string{"product_type": "AI 编程与工具调用助手", "core_function": "读写本地 Markdown 文件并调用工具"}),
		semanticRepairPage("page-deepseek-entity", "entity/deepseek-entity", "DeepSeek（实体）", "deepseek-duplicate", "entity", "product", "ev-deepseek-2", "视频中的 DeepSeek 是能够读写本地文件、调用工具的 AI Agent 配套工具。", map[string]string{"product_type": "AI 编程与工具调用助手", "core_function": "能够读写本地文件并调用工具"}),
		semanticRepairPage("page-brain", "concept/second-brain", "第二大脑", "brain-canonical", "concept", "", "ev-brain-1", "第二大脑是由本地知识库和 AI Agent 组成、能够调用知识并执行工作的系统。", map[string]string{"definition": "把静态档案库升级为可执行的知识系统", "components": "本地知识库、AI Agent 和方法模板", "mechanism": "AI Agent 调用知识、执行方法并回写经验"}),
		semanticRepairPage("page-brain-concept", "concept/second-brain-concept", "第二大脑（概念）", "brain-duplicate-1", "concept", "", "ev-brain-2", "第二大脑是让 AI Agent 调用本地知识并执行工作的知识系统。", map[string]string{"definition": "可执行的本地知识系统", "components": "本地知识库、AI Agent 和方法模板", "mechanism": "调用知识并回写经验"}),
		semanticRepairPage("page-agent-brain", "concept/agent-second-brain", "AI Agent 第二大脑（概念）", "brain-duplicate-2", "concept", "", "ev-brain-3", "接入 AI Agent 后，本地知识库升级为能够调用知识并执行工作的第二大脑。", map[string]string{"definition": "AI Agent 接入本地知识库后形成的可执行第二大脑", "components": "本地知识库、AI Agent 和方法模板", "mechanism": "读取知识、执行任务并回写经验"}),
	}

	plan, err := planWikiRepair(video, pages, scope)
	require.NoError(t, err)
	require.Len(t, plan.Actions, 5)
	var normalized, superseded int
	for _, action := range plan.Actions {
		switch action.Action {
		case "normalize":
			normalized++
			require.Contains(t, []string{"DeepSeek", "第二大脑"}, action.page.Title)
		case "quarantine":
			superseded++
			fm := (&weknora.WikiPage{Content: action.write.Content}).ParsedFrontmatter()
			require.Equal(t, legacyDraftType, frontmatterString(fm, "type"))
			require.NotEmpty(t, frontmatterString(fm, "canonical_wiki_page_id"))
			require.NotEmpty(t, frontmatterString(fm, "canonical_knowledge_object_id"))
			require.Contains(t, action.write.Content, "## 原始页面内容")
		}
	}
	require.Equal(t, 2, normalized)
	require.Equal(t, 3, superseded)
	require.Contains(t, plan.IndexWrite.Content, "[[entity/deepseek|DeepSeek]]")
	require.Contains(t, plan.IndexWrite.Content, "[[concept/second-brain|第二大脑]]")
	require.NotContains(t, plan.IndexWrite.Content, "deepseek-entity")
	require.NotContains(t, plan.IndexWrite.Content, "agent-second-brain")
}

func semanticRepairPage(id, slug, title, objectID, primaryType, entitySubType, evidenceID, core string, fields map[string]string) weknora.WikiPage {
	fm := map[string]any{
		"page_type": "index", "knowledge_object_id": objectID, "id": objectID,
		"type": primaryType, "primary_type": primaryType, "title": title,
		"source_video_id": "video-1", "transcript_generation": "generation-1",
		"audit_status": "passed", "classification_confidence": 0.9,
		"evidence_ids": []string{evidenceID}, "source_refs": []string{"source-1"},
		"core_content": core, "structure_fields": fields,
	}
	if primaryType == "entity" {
		fm["entity_sub_type"] = entitySubType
		fm["canonical_name"] = title
		fm["information_nature"] = "产品"
	} else {
		fm["information_nature"] = "概念"
	}
	content, err := renderRepairContent(fm, "# "+title+"\n\n## 一句话概述\n\n"+core)
	if err != nil {
		panic(err)
	}
	return weknora.WikiPage{ID: id, Slug: slug, Title: title, PageType: "index", Status: "published", Summary: core, Content: content}
}

func actionBySlug(t *testing.T, plan wikiRepairPlan, slug string) wikiRepairAction {
	t.Helper()
	for _, action := range plan.Actions {
		if action.Slug == slug {
			return action
		}
	}
	t.Fatalf("action %s not found", slug)
	return wikiRepairAction{}
}

func validationInformationNature(t *testing.T, content string) string {
	t.Helper()
	page := &weknora.WikiPage{Content: content}
	return strings.TrimSpace(frontmatterString(page.ParsedFrontmatter(), "information_nature"))
}
