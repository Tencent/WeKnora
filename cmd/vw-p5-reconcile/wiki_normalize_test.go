package main

import (
	"context"
	"errors"
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

	plan, err := planWikiRepair(context.Background(), video, pages, scope, knowledge.RuleSemanticIdentityAdapter{})
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

	plan, err := planWikiRepair(context.Background(), video, []weknora.WikiPage{page}, scope, knowledge.RuleSemanticIdentityAdapter{})
	require.ErrorContains(t, err, "no current-generation Wiki page has verifiable evidence")
	require.Len(t, plan.Actions, 1)
	require.Equal(t, "quarantine", plan.Actions[0].Action)
	require.Contains(t, plan.Actions[0].Reason, "not in the active transcript")
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

	plan, err := planWikiRepair(context.Background(), video, []weknora.WikiPage{page}, scope, knowledge.RuleSemanticIdentityAdapter{})
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

	plan, err := planWikiRepair(context.Background(), video, pages, scope, knowledge.RuleSemanticIdentityAdapter{})
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

func TestPlanWikiRepairUsesSemanticModelDecision(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "语义归一化", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{"ev-a": {}, "ev-b": {}}}
	pages := []weknora.WikiPage{
		semanticRepairPage("page-a", "concept/shared-a", "共享概念", "object-a", "concept", "", "ev-a", "用于验证语义身份的概念。", map[string]string{"definition": "语义身份测试概念", "mechanism": "比较定义与边界"}),
		semanticRepairPage("page-b", "concept/shared-b", "共享概念（概念）", "object-b", "concept", "", "ev-b", "用于验证语义身份的概念。", map[string]string{"definition": "语义身份测试概念", "mechanism": "比较定义与边界"}),
	}

	for _, test := range []struct {
		name              string
		decision          string
		expectedNormalize int
		expectedArchived  int
	}{
		{name: "same object archives duplicate", decision: "same_object", expectedNormalize: 1, expectedArchived: 1},
		{name: "different objects remain separate", decision: "different_object", expectedNormalize: 2, expectedArchived: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			adapter := semanticIdentityAdapterFunc(func(_ context.Context, _, _ knowledge.IdentityCandidate) (knowledge.SemanticIdentityAssessment, error) {
				calls++
				return knowledge.SemanticIdentityAssessment{
					Decision: test.decision, Confidence: 0.95, Reason: "model compared identity-defining fields",
				}, nil
			})
			plan, err := planWikiRepair(context.Background(), video, pages, scope, adapter)
			require.NoError(t, err)
			require.Equal(t, 1, calls)
			require.Len(t, plan.SemanticDecisions, 1)
			require.Equal(t, test.decision, plan.SemanticDecisions[0].Decision)
			require.Equal(t, test.expectedNormalize, countWikiRepairActions(plan, "normalize"))
			require.Equal(t, test.expectedArchived, countWikiRepairActions(plan, "quarantine"))
		})
	}
}

func TestPlanWikiRepairBlocksSemanticModelFailureWithoutArchivingCandidate(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "语义归一化", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{"ev-a": {}, "ev-b": {}}}
	pages := []weknora.WikiPage{
		semanticRepairPage("page-a", "concept/blocked-a", "阻断概念", "object-a", "concept", "", "ev-a", "用于验证失败关闭。", map[string]string{"definition": "失败关闭测试", "mechanism": "模型裁决"}),
		semanticRepairPage("page-b", "concept/blocked-b", "阻断概念（概念）", "object-b", "concept", "", "ev-b", "用于验证失败关闭。", map[string]string{"definition": "失败关闭测试", "mechanism": "模型裁决"}),
	}

	for _, test := range []struct {
		name       string
		assessment knowledge.SemanticIdentityAssessment
		err        error
		decision   string
	}{
		{
			name: "uncertain", assessment: knowledge.SemanticIdentityAssessment{
				Decision: "uncertain", Confidence: 0.92, Reason: "scope cannot be verified", ConflictFields: []string{"scope"},
			}, decision: "uncertain",
		},
		{name: "model error", err: errors.New("model unavailable"), decision: "error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := semanticIdentityAdapterFunc(func(_ context.Context, _, _ knowledge.IdentityCandidate) (knowledge.SemanticIdentityAssessment, error) {
				return test.assessment, test.err
			})
			plan, err := planWikiRepair(context.Background(), video, pages, scope, adapter)
			require.ErrorIs(t, err, errSemanticIdentityReviewRequired)
			require.Equal(t, 2, countWikiRepairActions(plan, "normalize"))
			require.Equal(t, 0, countWikiRepairActions(plan, "quarantine"))
			require.Len(t, plan.SemanticDecisions, 1)
			require.Equal(t, test.decision, plan.SemanticDecisions[0].Decision)
			for _, action := range plan.Actions {
				require.Empty(t, action.CanonicalWikiPageID)
				require.Empty(t, action.CanonicalKnowledgeObjectID)
			}
		})
	}
}

func TestPlanWikiRepairBlocksCandidateMatchingMultipleCanonicalAnchors(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "语义归一化", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{"ev-a": {}, "ev-b": {}, "ev-c": {}}}
	pages := []weknora.WikiPage{
		semanticRepairPage("page-a", "concept/multiple-a", "同名概念", "object-a", "concept", "", "ev-a", "第一个规范候选。", map[string]string{"definition": "同名概念", "mechanism": "机制一"}),
		semanticRepairPage("page-b", "concept/multiple-b", "同名概念", "object-b", "concept", "", "ev-b", "第二个规范候选。", map[string]string{"definition": "同名概念", "mechanism": "机制二"}),
		semanticRepairPage("page-c", "concept/multiple-c", "同名概念", "object-c", "concept", "", "ev-c", "歧义候选。", map[string]string{"definition": "同名概念", "mechanism": "机制待确认"}),
	}
	adapter := semanticIdentityAdapterFunc(func(_ context.Context, left, right knowledge.IdentityCandidate) (knowledge.SemanticIdentityAssessment, error) {
		decision := "same_object"
		if left.KnowledgeObjectID == "object-a" && right.KnowledgeObjectID == "object-b" {
			decision = "different_object"
		}
		return knowledge.SemanticIdentityAssessment{Decision: decision, Confidence: 0.96, Reason: "scripted identity decision"}, nil
	})

	plan, err := planWikiRepair(context.Background(), video, pages, scope, adapter)
	require.ErrorIs(t, err, errSemanticIdentityReviewRequired)
	require.Equal(t, 3, countWikiRepairActions(plan, "normalize"))
	require.Equal(t, 0, countWikiRepairActions(plan, "quarantine"))
	require.Condition(t, func() bool {
		for _, decision := range plan.SemanticDecisions {
			if decision.Decision == "multiple_anchor_match" && decision.CandidateWikiPageID == "page-c" {
				return true
			}
		}
		return false
	})
}

func TestPlanWikiRepairReusesExactObjectIDWithoutSemanticModel(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "语义归一化", TranscriptGeneration: "generation-1"}
	scope := wikiRepairScope{SourceDocumentID: "source-1", EvidenceIDs: map[string]struct{}{"ev-a": {}, "ev-b": {}}}
	pages := []weknora.WikiPage{
		semanticRepairPage("page-a", "concept/exact-a", "相同对象", "shared-object", "concept", "", "ev-a", "相同身份。", map[string]string{"definition": "相同身份", "mechanism": "同一 ID"}),
		semanticRepairPage("page-b", "concept/exact-b", "相同对象（概念）", "shared-object", "concept", "", "ev-b", "相同身份。", map[string]string{"definition": "相同身份", "mechanism": "同一 ID"}),
	}

	plan, err := planWikiRepair(context.Background(), video, pages, scope, nil)
	require.NoError(t, err)
	require.Equal(t, 1, countWikiRepairActions(plan, "normalize"))
	require.Equal(t, 1, countWikiRepairActions(plan, "quarantine"))
	require.Len(t, plan.SemanticDecisions, 1)
	require.Equal(t, "same_object", plan.SemanticDecisions[0].Decision)
	require.Equal(t, float64(1), plan.SemanticDecisions[0].Confidence)
}

type semanticIdentityAdapterFunc func(context.Context, knowledge.IdentityCandidate, knowledge.IdentityCandidate) (knowledge.SemanticIdentityAssessment, error)

func (f semanticIdentityAdapterFunc) Compare(
	ctx context.Context,
	left knowledge.IdentityCandidate,
	right knowledge.IdentityCandidate,
) (knowledge.SemanticIdentityAssessment, error) {
	return f(ctx, left, right)
}

func countWikiRepairActions(plan wikiRepairPlan, action string) int {
	count := 0
	for _, item := range plan.Actions {
		if item.Action == action {
			count++
		}
	}
	return count
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
