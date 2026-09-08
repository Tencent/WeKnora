package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAuditPagesPlansTargetedRepairWithoutDeletingWikiPages(t *testing.T) {
	video := model.Video{
		ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1",
		KnowledgeBaseWikiPageID: "global-index", KnowledgeAuditStatus: "passed",
	}
	validContent := `---
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
  definition: 概念定义
  mechanism: 运行机制
---
# 概念

一句话概述：这是可展示的概念内容。`
	invalidContent := strings.Replace(validContent, "object-1", "object-2", 1)
	invalidContent = strings.Replace(invalidContent, "audit_status: passed", "audit_status: aligned", 1)
	indexContent := `---
type: knowledge_base
source_video_id: video-1
transcript_generation: generation-1
title: 测试视频_知识底座
audit_status: aligned
---
# 测试视频_知识底座`
	pages := []weknora.WikiPage{
		{ID: "index-1", Slug: "video/video-1", PageType: "index", Content: indexContent},
		{ID: "object-1", Slug: "concept/object-1", PageType: "index", Content: validContent},
		{ID: "object-2", Slug: "concept/object-2", PageType: "index", Content: invalidContent},
		{ID: "summary-1", Slug: "summary/video-1", PageType: "summary", Content: "# 摘要"},
	}

	report := auditPages(video, "knowledge-kb", pages)
	require.True(t, report.NeedsRepair)
	require.Equal(t, "index-1", report.CurrentIndexPageID)
	require.Equal(t, 1, report.ValidObjectCount)
	require.Len(t, report.InvalidObjects, 1)
	require.Equal(t, "object-2", report.InvalidObjects[0].WikiPageID)
	require.Equal(t, []string{"clear_invalid_video_reference", "requeue_graph_job"}, report.ProposedActions)
	require.True(t, report.WikiPagesPreserved)
}

func TestAuditPagesIgnoresInvalidObjectsFromOlderGeneration(t *testing.T) {
	video := model.Video{ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-2"}
	oldPage := weknora.WikiPage{
		ID: "old-object", Slug: "concept/old", PageType: "index",
		Content: `---
type: concept
primary_type: concept
source_video_id: video-1
transcript_generation: generation-1
audit_status: aligned
---
# 旧对象`,
	}

	report := auditPages(video, "knowledge-kb", []weknora.WikiPage{oldPage})
	require.Empty(t, report.InvalidObjects)
	require.Zero(t, report.ValidObjectCount)
	require.True(t, report.NeedsRepair, "missing current-generation artifacts still require repair")
}

func TestAuditPagesRequiresFormalRelationForMultiObjectBatch(t *testing.T) {
	video := model.Video{
		ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1",
		KnowledgeBaseWikiPageID: "index-1", KnowledgeAuditStatus: "passed",
	}
	index := weknora.WikiPage{
		ID: "index-1", Slug: "video/video-1", PageType: "index",
		Content: `---
type: knowledge_base
source_video_id: video-1
transcript_generation: generation-1
title: 测试视频_知识底座
audit_status: aligned
---
# 测试视频_知识底座`,
	}
	objectContent := `---
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
  definition: 概念定义
  mechanism: 运行机制
relations: []
---
# 概念

一句话概述：这是可展示的概念内容。`
	secondContent := strings.Replace(objectContent, "object-1", "object-2", 1)
	report := auditPages(video, "knowledge-kb", []weknora.WikiPage{
		index,
		{ID: "object-1", Slug: "concept/object-1", PageType: "index", Content: objectContent},
		{ID: "object-2", Slug: "concept/object-2", PageType: "index", Content: secondContent},
	})

	require.True(t, report.NeedsRepair)
	require.Equal(t, "repair_required", report.Status)
	require.Contains(t, report.ProposedActions, "requeue_graph_job")
}

func TestApplyRepairOnlyClearsKnowledgeReferenceAndRequeuesGraph(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Video{}, &model.VideoTranscriptSource{}, &model.VideoTranscriptChunk{}, &model.VideoProcessingJob{}))
	video := model.Video{
		ID: "video-1", Title: "测试视频", TranscriptGeneration: "generation-1",
		KnowledgeBaseWikiPageID: "wrong-index", KnowledgeAuditStatus: "passed",
		OutlineWikiPageID: "outline-1", SummaryWikiPageID: "summary-1",
	}
	require.NoError(t, db.Create(&video).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptSource{
		ID: "source-binding", VideoID: video.ID, TranscriptGeneration: video.TranscriptGeneration,
		KnowledgeBaseID: "knowledge-kb", KnowledgeID: "source-1", Status: "created",
	}).Error)
	require.NoError(t, db.Create(&model.VideoTranscriptChunk{
		VideoID: video.ID, Generation: video.TranscriptGeneration, ChunkIndex: 0,
		KnowledgeID: "chunk-1", ContentHash: "hash", Status: "completed",
	}).Error)
	job := model.VideoProcessingJob{
		ID: "graph-job", VideoID: video.ID, JobType: "graph", TranscriptGeneration: video.TranscriptGeneration,
		ResultStage: "final", Status: "succeeded", MaxAttempts: 3, IdempotencyKey: "graph:video-1:generation-1",
	}
	require.NoError(t, db.Create(&job).Error)
	report := reconciliationReport{NeedsRepair: true}

	require.NoError(t, applyRepair(context.Background(), db, "knowledge-kb", &video, &report))
	var storedVideo model.Video
	require.NoError(t, db.First(&storedVideo, "id = ?", video.ID).Error)
	require.Empty(t, storedVideo.KnowledgeBaseWikiPageID)
	require.Empty(t, storedVideo.KnowledgeAuditStatus)
	require.Equal(t, "outline-1", storedVideo.OutlineWikiPageID)
	require.Equal(t, "summary-1", storedVideo.SummaryWikiPageID)
	var storedJob model.VideoProcessingJob
	require.NoError(t, db.First(&storedJob, "id = ?", job.ID).Error)
	require.Equal(t, "pending", storedJob.Status)
	require.Zero(t, storedJob.AttemptCount)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(storedJob.InputPayload), &payload))
	require.Equal(t, "source-1", payload["transcript_source_knowledge_id"])
	require.True(t, report.Applied)
	require.Equal(t, "repair_queued", report.Status)
}
