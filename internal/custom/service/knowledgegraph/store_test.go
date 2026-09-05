package knowledgegraph

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
# 方法论二`,
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
# 组织`,
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

func TestBuildKnowledgeBaseProjectionUsesStructuredRelationsOnly(t *testing.T) {
	pages := []weknora.WikiPage{
		{
			ID: "concept-page", Slug: "concept/harness", Title: "Harness", PageType: "concept",
			Content: `---
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: method-page
    target_wiki_page_id: method-page
    evidence_ids: [chunk-1]
    confidence: 0.88
---
# Harness

正文中的普通双链 [[method/agent-eval|Agent Eval]] 不应直接入图。`,
			SourceRefs: []string{"chunk-1"},
		},
		{
			ID: "method-page", Slug: "method/agent-eval", Title: "Agent Eval", PageType: "method",
			Content: "# Agent Eval\n\n评估方法。",
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
			ID: "concept-page", Slug: "concept/harness", Title: "Harness", PageType: "concept",
			Content: "# Harness\n\n关联 [[method/agent-eval|Agent Eval]]。",
		},
		{
			ID: "method-page", Slug: "method/agent-eval", Title: "Agent Eval", PageType: "method",
			Content: "# Agent Eval\n\n评估方法。",
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
# Context`,
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
# Context`,
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
