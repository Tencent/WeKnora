package knowledgegraph

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGraphQueryStatementsKeepReadContract(t *testing.T) {
	statements := buildGraphQueryStatements("TEST_GRAPH")
	checks := []struct {
		name      string
		statement string
		required  []string
	}{
		{
			name: "count", statement: statements.count,
			required: []string{"MATCH (n:TEST_GRAPH)", "n.source_video_id = $video_id", "$video_id IN coalesce(n.source_video_ids, [])", "n.audit_status = 'passed'", "n.projection_version = 'wiki-v1'"},
		},
		{
			name: "nodes", statement: statements.nodes,
			required: []string{"n.wiki_page_id = $wiki_page_id", "n.knowledge_type IN $types", "ORDER BY toLower(coalesce(n.title, '')), n.wiki_page_id", "LIMIT $limit"},
		},
		{
			name: "edges", statement: statements.edges,
			required: []string{"[r:KNOWLEDGE_RELATION]", "source.wiki_page_id = $wiki_page_id OR target.wiki_page_id = $wiki_page_id", "r.source_video_id = $video_id", "r.projection_version = 'wiki-v1'", "ORDER BY r.relation_id"},
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			for _, required := range check.required {
				if !strings.Contains(check.statement, required) {
					t.Fatalf("statement is missing %q:\n%s", required, check.statement)
				}
			}
		})
	}
}

func TestGraphQueryFailsClosedWithoutNeo4jDriver(t *testing.T) {
	store := &StoreImpl{namespace: "TEST_GRAPH"}
	if _, err := store.Query(context.Background(), Query{}); err == nil || !strings.Contains(err.Error(), "neo4j is unavailable") {
		t.Fatalf("Query error = %v", err)
	}
}

func TestFormalRelationTypeContract(t *testing.T) {
	for _, relationType := range []string{"contradicts", "complements", "explains", "example_of", "part_of", "applies_to", "supports", "involves"} {
		if !IsFormalRelationType(relationType) {
			t.Fatalf("formal relation type %q was rejected", relationType)
		}
	}
	for _, relationType := range []string{"", "related_to", "random_link"} {
		if IsFormalRelationType(relationType) {
			t.Fatalf("non-formal relation type %q was accepted", relationType)
		}
	}
}

func TestBuildProjectionAcceptsOnlyPassedCurrentObjectsAndValidatedRelations(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	pages := []weknora.WikiPage{
		{
			ID: "page-1", Slug: "concept/one", PageType: "index",
			Content: `---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.92
evidence_ids: [chunk-1]
source_refs: [chunk-1]
structure_fields:
  definition: 概念一解释概念二
  mechanism: 通过关系说明概念二
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: object-2
    target_wiki_page_id: page-2
    evidence_ids: [chunk-1]
    confidence: 0.88
--- 
# 概念一

核心内容：概念一解释概念二。`,
		},
		{
			ID: "page-2", Slug: "methodology/two", PageType: "index",
			Content: `---
knowledge_object_id: object-2
type: methodology
primary_type: methodology
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.90
evidence_ids: [chunk-2]
source_refs: [chunk-2]
structure_fields:
  input: 方法论输入
  steps: 方法论步骤
--- 
# 方法论二

一句话概述：方法论二用于完成评估。`,
		},
		{
			ID: "page-failed", Slug: "insight/failed", PageType: "index",
			Content: `---
knowledge_object_id: object-failed
type: insight
primary_type: insight
source_video_id: video-1
transcript_generation: generation-1
audit_status: failed
information_nature: 洞察
classification_confidence: 0.95
evidence_ids: [chunk-3]
source_refs: [chunk-3]
--- 
# 不应入图`,
		},
		{
			ID: "page-old", Slug: "case/old", PageType: "index",
			Content: `---
knowledge_object_id: object-old
type: case
primary_type: case
source_video_id: video-1
transcript_generation: generation-old
audit_status: passed
information_nature: 案例
classification_confidence: 0.95
evidence_ids: [chunk-4]
source_refs: [chunk-4]
--- 
# 旧代次`,
		},
		{
			ID: "page-invalid-relation", Slug: "entity/invalid", PageType: "index",
			Content: `---
knowledge_object_id: object-3
type: entity
primary_type: entity
entity_sub_type: organization
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 机构
classification_confidence: 0.95
evidence_ids: [chunk-5]
source_refs: [chunk-5]
structure_fields:
  org_type: 组织
  industry: 培训
relations:
  - relation_type: random_link
    target_object_id: object-2
    target_wiki_page_id: page-2
    evidence_ids: [chunk-5]
    confidence: 0.99
  - relation_type: explains
    target_object_id: object-2
    target_wiki_page_id: wrong-page
    evidence_ids: [chunk-5]
    confidence: 0.99
--- 
# 组织

一句话概述：这是参与培训的组织。`,
		},
	}

	objects, edges, err := buildProjection(video, pages)
	if err != nil {
		t.Fatalf("buildProjection returned error: %v", err)
	}
	if len(objects) != 3 {
		t.Fatalf("objects = %d, want 3", len(objects))
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if edges[0].SourceWikiPageID != "page-1" || edges[0].TargetWikiPageID != "page-2" || edges[0].RelationType != "explains" {
		t.Fatalf("edge = %#v", edges[0])
	}
	if objects[0].KnowledgeType != knowledge.TypeConcept {
		t.Fatalf("object type = %q", objects[0].KnowledgeType)
	}
}

func TestParseObjectRejectsMissingStructuredIdentity(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	_, ok, err := parseObject(video, weknora.WikiPage{
		ID: "page-1", PageType: "index",
		Content: "---\ntype: concept\nsource_video_id: video-1\ntranscript_generation: generation-1\naudit_status: passed\nclassification_confidence: 0.9\nevidence_ids: [chunk-1]\nsource_refs: [chunk-1]\n---\n# 概念",
	})
	if err == nil || ok {
		t.Fatalf("expected missing object identity error, ok=%v err=%v", ok, err)
	}
}

func TestKnowledgeBaseProjectionIncludesOnlyAuditedVideoKnowledge(t *testing.T) {
	pages := []weknora.WikiPage{
		{
			ID: "valid-page", Slug: "concept/validated", Title: "合规概念", PageType: "index",
			Content: `---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [chunk-1]
source_refs: [source-1]
structure_fields:
  definition: 合规概念定义
  mechanism: 合规概念机制
---
# 合规概念

一句话概述：这是可展示的合规概念。`,
		},
		{
			ID: "legacy-page", Slug: "concept/legacy", Title: "旧概念", PageType: "concept",
			Content: "# 旧概念\n\n没有视频归属、转写代次或证据。",
		},
	}

	nodes, edges := buildKnowledgeBaseProjection(pages)
	require.Len(t, nodes, 1)
	require.Equal(t, "valid-page", nodes[0].WikiPageID)
	require.Equal(t, "video-1", nodes[0].SourceVideoID)
	require.Equal(t, "generation-1", nodes[0].TranscriptGeneration)
	require.Equal(t, "passed", nodes[0].AuditStatus)
	require.Empty(t, edges)
}

func TestKnowledgeBaseProjectionExcludesStaleVideoGeneration(t *testing.T) {
	current := weknora.WikiPage{
		ID: "current-page", Slug: "concept/current", Title: "当前概念", PageType: "index",
		Content: strings.ReplaceAll(`---
knowledge_object_id: object-current
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: GENERATION
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [chunk-1]
source_refs: [source-1]
structure_fields:
  definition: 当前概念定义
  mechanism: 当前概念机制
---
# 当前概念

一句话概述：这是当前代次的概念。`, "GENERATION", "generation-2"),
	}
	stale := current
	stale.ID = "stale-page"
	stale.Slug = "concept/stale"
	stale.Content = strings.ReplaceAll(stale.Content, "object-current", "object-stale")
	stale.Content = strings.ReplaceAll(stale.Content, "generation-2", "generation-1")

	nodes, _ := buildKnowledgeBaseProjectionForGenerations(
		[]weknora.WikiPage{stale, current},
		map[string]string{"video-1": "generation-2"},
	)
	require.Len(t, nodes, 1)
	require.Equal(t, "current-page", nodes[0].WikiPageID)
}

func TestKnowledgeBaseProjectionUsesCanonicalContributionsAcrossVideos(t *testing.T) {
	page := weknora.WikiPage{ID: "canonical-page", Slug: "concept/canonical", Title: "规范概念", PageType: "index", Content: `---
knowledge_object_id: object-canonical
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [chunk-1, chunk-2]
source_refs: [source-1, source-2]
evidence_contributions:
  - video_id: video-1
    source_document_id: source-1
    transcript_generation: generation-1
    evidence_ids: [chunk-1]
    quality_status: passed
  - video_id: video-2
    source_document_id: source-2
    transcript_generation: generation-2
    evidence_ids: [chunk-2]
    quality_status: passed
structure_fields:
  definition: 两个视频共同定义的概念
  mechanism: 通过证据贡献保持规范身份
---
# 规范概念

一句话概述：同一规范概念来自两个视频。`}

	nodes, _ := buildKnowledgeBaseProjectionForGenerations([]weknora.WikiPage{page}, map[string]string{"video-1": "generation-1", "video-2": "generation-2"})
	require.Len(t, nodes, 1)
	require.Equal(t, []string{"video-1", "video-2"}, nodes[0].SourceVideoIDs)
	require.Equal(t, []string{"generation-1", "generation-2"}, nodes[0].TranscriptGenerations)
}

func TestBuildProjectionScopesFormalRelationEvidenceToVideoGeneration(t *testing.T) {
	sharedContributions := `evidence_contributions:
  - video_id: video-1
    source_document_id: source-1
    transcript_generation: generation-1
    evidence_ids: [ev-1]
    quality_status: passed
  - video_id: video-2
    source_document_id: source-2
    transcript_generation: generation-2
    evidence_ids: [ev-2]
    quality_status: passed`
	source := weknora.WikiPage{
		ID: "page-1", Slug: "concept/shared", Title: "共享概念", PageType: "index",
		Content: fmt.Sprintf(`---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [ev-1, ev-2]
source_refs: [source-1, source-2]
core_content: 共享概念解释方法
%s
structure_fields:
  definition: 共享概念定义
  mechanism: 共享概念机制
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: object-2
    target_wiki_page_id: page-2
    evidence_contributions:
      - video_id: video-1
        transcript_generation: generation-1
        evidence_ids: [ev-1]
        time_range: 00:00:01.000-00:00:03.000
        confidence: 0.9
        quality_status: passed
---
# 共享概念`, sharedContributions),
	}
	target := weknora.WikiPage{
		ID: "page-2", Slug: "methodology/shared", Title: "共享方法", PageType: "index",
		Content: fmt.Sprintf(`---
knowledge_object_id: object-2
type: methodology
primary_type: methodology
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.9
evidence_ids: [ev-1, ev-2]
source_refs: [source-1, source-2]
core_content: 共享方法内容
%s
structure_fields:
  input: 方法输入
  steps: 方法步骤
---
# 共享方法`, sharedContributions),
	}

	_, videoOneEdges, err := buildProjection(&model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}, []weknora.WikiPage{source, target})
	require.NoError(t, err)
	require.Len(t, videoOneEdges, 1)
	require.Equal(t, []string{"ev-1"}, videoOneEdges[0].EvidenceIDs)

	_, videoTwoEdges, err := buildProjection(&model.Video{ID: "video-2", TranscriptGeneration: "generation-2"}, []weknora.WikiPage{source, target})
	require.NoError(t, err)
	require.Empty(t, videoTwoEdges)
}

func TestKnowledgeBaseProjectionKeepsEachActiveRelationContribution(t *testing.T) {
	pages := []weknora.WikiPage{
		{
			ID: "page-1", Slug: "concept/shared", Title: "共享概念", PageType: "index",
			Content: `---
knowledge_object_id: object-1
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [ev-1, ev-2]
source_refs: [source-1, source-2]
core_content: 共享概念解释方法
evidence_contributions:
  - video_id: video-1
    source_document_id: source-1
    transcript_generation: generation-1
    evidence_ids: [ev-1]
    quality_status: passed
  - video_id: video-2
    source_document_id: source-2
    transcript_generation: generation-2
    evidence_ids: [ev-2]
    quality_status: passed
structure_fields:
  definition: 共享概念定义
  mechanism: 共享概念机制
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: object-2
    target_wiki_page_id: page-2
    evidence_contributions:
      - video_id: video-1
        transcript_generation: generation-1
        evidence_ids: [ev-1]
        time_range: 00:00:01.000-00:00:03.000
        confidence: 0.9
        quality_status: passed
      - video_id: video-2
        transcript_generation: generation-2
        evidence_ids: [ev-2]
        time_range: 00:00:04.000-00:00:06.000
        confidence: 0.8
        quality_status: passed
---
# 共享概念`,
		},
		{
			ID: "page-2", Slug: "methodology/shared", Title: "共享方法", PageType: "index",
			Content: `---
knowledge_object_id: object-2
type: methodology
primary_type: methodology
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.9
evidence_ids: [ev-1, ev-2]
source_refs: [source-1, source-2]
core_content: 共享方法内容
evidence_contributions:
  - video_id: video-1
    source_document_id: source-1
    transcript_generation: generation-1
    evidence_ids: [ev-1]
    quality_status: passed
  - video_id: video-2
    source_document_id: source-2
    transcript_generation: generation-2
    evidence_ids: [ev-2]
    quality_status: passed
structure_fields:
  input: 方法输入
  steps: 方法步骤
---
# 共享方法`,
		},
	}

	_, edges := buildKnowledgeBaseProjectionForGenerations(pages, map[string]string{
		"video-1": "generation-1",
		"video-2": "generation-2",
	})
	require.Len(t, edges, 2)
	require.Equal(t, "video-1", edges[0].SourceVideoID)
	require.Equal(t, []string{"ev-1"}, edges[0].EvidenceIDs)
	require.Equal(t, "video-2", edges[1].SourceVideoID)
	require.Equal(t, []string{"ev-2"}, edges[1].EvidenceIDs)
}

func TestBuildKnowledgeBaseProjectionUsesStructuredRelationsOnly(t *testing.T) {
	pages := []weknora.WikiPage{
		{
			ID: "concept-page", Slug: "concept/harness", Title: "Harness", PageType: "index",
			Content: `---
knowledge_object_id: concept-object
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [chunk-1]
source_refs: [source-1]
structure_fields:
  definition: Harness 定义
  mechanism: Harness 运行机制
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: method-object
    target_wiki_page_id: method-page
    evidence_ids: [chunk-1]
    confidence: 0.88
---
# Harness

一句话概述：Harness 用于组织评估过程。

正文中的普通双链 [[method/agent-eval|Agent Eval]] 不应直接入图。`,
		},
		{
			ID: "method-page", Slug: "method/agent-eval", Title: "Agent Eval", PageType: "index",
			Content: `---
knowledge_object_id: method-object
type: methodology
primary_type: methodology
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.9
evidence_ids: [chunk-2]
source_refs: [source-1]
structure_fields:
  input: 评估输入
  steps: 执行评估步骤
---
# Agent Eval

一句话概述：这是评估方法。`,
		},
		{
			ID: "summary-page", Slug: "summary/chunk-1", Title: "Summary", PageType: "summary",
			Content: "# Summary",
		},
	}

	nodes, edges := buildKnowledgeBaseProjection(pages)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	var methodFound bool
	for _, node := range nodes {
		if node.WikiPageID == "method-page" && node.KnowledgeType == knowledge.TypeMethodology {
			methodFound = true
		}
	}
	if !methodFound {
		t.Fatalf("method page was not mapped to methodology: %#v", nodes)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if edges[0].SourceWikiPageID != "concept-page" || edges[0].TargetWikiPageID != "method-page" || edges[0].RelationType != "explains" {
		t.Fatalf("edge = %#v", edges[0])
	}
}

func TestBuildKnowledgeBaseProjectionRejectsPlainWikiLinks(t *testing.T) {
	pages := []weknora.WikiPage{
		{
			ID: "concept-page", Slug: "concept/harness", Title: "Harness", PageType: "index",
			Content: `---
knowledge_object_id: concept-object
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [chunk-1]
source_refs: [source-1]
structure_fields:
  definition: Harness 定义
  mechanism: Harness 运行机制
---
# Harness

一句话概述：Harness 用于组织评估过程。

关联 [[method/agent-eval|Agent Eval]]。`,
		},
		{
			ID: "method-page", Slug: "method/agent-eval", Title: "Agent Eval", PageType: "index",
			Content: `---
knowledge_object_id: method-object
type: methodology
primary_type: methodology
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.9
evidence_ids: [chunk-2]
source_refs: [source-1]
structure_fields:
  input: 评估输入
  steps: 执行评估步骤
---
# Agent Eval

一句话概述：这是评估方法。`,
		},
	}

	nodes, edges := buildKnowledgeBaseProjection(pages)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}
	if len(edges) != 0 {
		t.Fatalf("plain wiki links must not become graph edges: %#v", edges)
	}
}

func TestIdentityAuditsRecordSameNameDecisions(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	pages := []weknora.WikiPage{
		{
			ID: "page-concept", Slug: "concept/context", Title: "Context", PageType: "index",
			Content: `---
knowledge_object_id: object-concept
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
title: Context
audit_status: passed
information_nature: 概念
classification_confidence: 0.91
evidence_ids: [chunk-1]
source_refs: [chunk-1]
structure_fields:
  definition: 上下文信息集合
  mechanism: 通过历史记录帮助判断
---
# Context

一句话概述：上下文帮助系统理解历史信息。`,
		},
		{
			ID: "page-product", Slug: "product/context", Title: "Context", PageType: "index",
			Content: `---
knowledge_object_id: object-product
type: entity
primary_type: entity
entity_sub_type: product
source_video_id: video-1
transcript_generation: generation-1
title: Context
aliases: [Context Machine]
audit_status: passed
information_nature: 产品
classification_confidence: 0.93
evidence_ids: [chunk-2]
source_refs: [chunk-2]
structure_fields:
  product_type: AI 产品
  core_function: 记录和理解上下文
---
# Context

一句话概述：Context Machine 是记录上下文的产品。`,
		},
	}

	_, _, _, identityAudits, err := buildProjectionWithAudit(video, pages)
	if err != nil {
		t.Fatalf("buildProjectionWithAudit returned error: %v", err)
	}
	if len(identityAudits) != 1 {
		t.Fatalf("identity audits = %d, want 1", len(identityAudits))
	}
	if identityAudits[0].Decision != string(knowledge.IdentityConflict) ||
		identityAudits[0].NormalizedName != "context" ||
		identityAudits[0].TitleMatch != true {
		t.Fatalf("identity audit = %#v", identityAudits[0])
	}
}

func TestValidateSemanticIdentityCompletionFoldsMeaningDuplicatesWithoutDeletingWiki(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	pages := []weknora.WikiPage{
		semanticIdentityTestPage("10000000-0000-4000-8000-000000000001", "concept/second-brain", "第二大脑", "second-brain", "第二大脑是可调用知识并执行工作的系统。"),
		semanticIdentityTestPage("10000000-0000-4000-8000-000000000002", "concept/second-brain-v2", "第二大脑（概念）", "second-brain-v2", "第二大脑是由 AI Agent 调用知识并执行工作的系统。"),
	}

	if err := ValidateSemanticIdentityCompletion(video.ID, video.TranscriptGeneration, pages); err != nil {
		t.Fatalf("historical semantic duplicates should be folded instead of blocking completion: %v", err)
	}
	objects, _, _, identityAudits, err := buildProjectionWithAudit(video, pages)
	if err != nil {
		t.Fatalf("buildProjectionWithAudit returned error: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("objects = %d, want folded canonical object", len(objects))
	}
	if len(identityAudits) != 1 || identityAudits[0].Decision != string(knowledge.IdentityReuse) {
		t.Fatalf("identity audits = %#v, want auditable reuse decision", identityAudits)
	}
}

func TestValidateSemanticIdentityCompletionStillRejectsTypeConflicts(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	entity := semanticIdentityTestPage("10000000-0000-4000-8000-000000000003", "entity/second-brain", "第二大脑", "second-brain-product", "第二大脑是可调用知识并执行工作的系统。")
	entity.Content = strings.NewReplacer(
		"type: concept", "type: entity",
		"primary_type: concept", "primary_type: entity",
		"information_nature: 概念", "information_nature: 产品",
		"structure_fields:\n  definition: 把静态档案库升级为可执行的知识系统\n  components: 本地知识库、AI Agent 和方法模板\n  mechanism: AI Agent 调用知识、执行方法并回写经验",
		"entity_sub_type: product\nstructure_fields:\n  product_type: 知识管理产品\n  core_function: 调用知识并执行工作",
	).Replace(entity.Content)
	pages := []weknora.WikiPage{
		semanticIdentityTestPage("10000000-0000-4000-8000-000000000001", "concept/second-brain", "第二大脑", "second-brain", "第二大脑是可调用知识并执行工作的系统。"),
		entity,
	}

	if err := ValidateSemanticIdentityCompletion(video.ID, video.TranscriptGeneration, pages); err == nil || !strings.Contains(err.Error(), "semantic identity") {
		t.Fatalf("semantic type conflicts must still block completion, got %v", err)
	}
}

func TestBuildProjectionRedirectsDuplicateRelationTargetToCanonicalPage(t *testing.T) {
	video := &model.Video{ID: "video-1", TranscriptGeneration: "generation-1"}
	canonical := semanticIdentityTestPage("10000000-0000-4000-8000-000000000001", "concept/think-act-observe", "思考行动观察", "think-act-observe", "思考行动观察是 AI Agent 执行任务的核心循环。")
	duplicate := semanticIdentityTestPage("10000000-0000-4000-8000-000000000002", "concept/ai-agent-think-act-observe", "AI Agent 思考行动观察循环", "ai-agent-think-act-observe", "AI Agent 通过思考、行动、观察循环完成任务。")
	source := weknora.WikiPage{
		ID: "20000000-0000-4000-8000-000000000001", Slug: "methodology/agent-workflow", Title: "Agent 工作法", PageType: "index",
		Content: `---
knowledge_object_id: agent-workflow
type: methodology
primary_type: methodology
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.9
evidence_ids: [ev-2]
source_refs: [doc-1]
core_content: Agent 工作法解释思考行动观察循环如何落地。
structure_fields:
  input: 用户目标和约束
  steps: 拆解、行动、观察、调整
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: ai-agent-think-act-observe
    target_wiki_page_id: 10000000-0000-4000-8000-000000000002
    evidence_ids: [ev-2]
    confidence: 0.9
---
# Agent 工作法

一句话概述：Agent 工作法解释思考行动观察循环如何落地。`,
	}

	objects, edges, err := buildProjection(video, []weknora.WikiPage{source, duplicate, canonical})
	require.NoError(t, err)
	require.Len(t, objects, 2)
	require.Len(t, edges, 1)
	require.Equal(t, "20000000-0000-4000-8000-000000000001", edges[0].SourceWikiPageID)
	require.Equal(t, "10000000-0000-4000-8000-000000000001", edges[0].TargetWikiPageID)
	require.Equal(t, "think-act-observe", edges[0].TargetObjectID)
}

func semanticIdentityTestPage(id, slug, title, objectID, core string) weknora.WikiPage {
	return weknora.WikiPage{
		ID: id, Slug: slug, Title: title, PageType: "index", Status: "published",
		Content: fmt.Sprintf(`---
knowledge_object_id: %s
type: concept
primary_type: concept
title: %s
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [ev-1]
source_refs: [doc-1]
core_content: %s
structure_fields:
  definition: 把静态档案库升级为可执行的知识系统
  components: 本地知识库、AI Agent 和方法模板
  mechanism: AI Agent 调用知识、执行方法并回写经验
relations: []
---

# %s

## 一句话概述

%s`, objectID, title, core, title, core),
	}
}

func TestPersistIdentityAuditsIsRebuildable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.WikiIdentityAudit{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	audits := []IdentityAudit{{
		VideoID: "video-1", TranscriptGeneration: "generation-1",
		SourceWikiPageID: "page-1", SourceObjectID: "object-1",
		CandidateWikiPageID: "page-2", CandidateObjectID: "object-2",
		NormalizedName: "context", SourceType: knowledge.TypeConcept, CandidateType: knowledge.TypeInsight,
		TitleMatch: true, Decision: string(knowledge.IdentityConflict), Reason: "same candidate name has different top-level types",
	}}
	if err := persistIdentityAudits(db, "video-1", "generation-1", audits); err != nil {
		t.Fatalf("persist identity audits: %v", err)
	}
	if err := persistIdentityAudits(db, "video-1", "generation-1", audits); err != nil {
		t.Fatalf("persist identity audits again: %v", err)
	}
	var count int64
	if err := db.Model(&model.WikiIdentityAudit{}).Count(&count).Error; err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit count = %d, want 1", count)
	}
}
