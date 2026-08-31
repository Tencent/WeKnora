package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
)

func TestSummaryNotGeneratedReturnsStructuredStageStatus(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/summary", nil)

	NewContentHandler(db, nil, "kb-1").Summary(context)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Status       string `json:"status"`
		Stage        string `json:"stage"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Status != "failed" || payload.Stage != "summary" || payload.ErrorCode != "not_generated" || payload.ErrorMessage == "" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestContentEndpointRejectsWrongArtifactPage(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusCompleted,
		OutlineWikiPageID: "shared-page",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{
					ID: "shared-page", Slug: "outline/video-1", PageType: "index",
				}},
				Total: 1, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "shared-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: overview\nsource_video_id: " + video.ID + "\n---\n概览内容",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/outline", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").Outline(context)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.ErrorCode != "artifact_contract_mismatch" {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
}

func TestOutlineEndpointReturnsCanonicalChaptersWithoutSourceContent(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusCompleted,
		DurationSeconds: 60, TranscriptGeneration: "generation-1", OutlineWikiPageID: "outline-page",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	document := outline.Document{
		SchemaVersion: outline.SchemaVersion,
		Chapters: []outline.Chapter{{
			ChapterIndex: 1, ChapterTitle: "视频引入", StartSeconds: 0, EndSeconds: 60,
			ChapterSummary: "本章介绍视频主题。", EvidenceChunkIDs: []string{"chunk-1"},
			KnowledgePoints: []outline.KnowledgePoint{{Title: "观察场景", Seconds: 12, EvidenceChunkIDs: []string{"chunk-1"}}},
		}},
	}
	canonical, err := outline.Marshal(document)
	if err != nil {
		t.Fatalf("marshal outline: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{ID: "outline-page", Slug: "outline/video-1", PageType: "index"}},
				Total: 1, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: outline\nsource_video_id: " + video.ID + "\ntranscript_generation: generation-1\nschema_version: 1\n---\n\n" + canonical,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/outline", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").Outline(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		SchemaVersion int               `json:"schema_version"`
		Chapters      []outline.Chapter `json:"chapters"`
		Content       string            `json:"content"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.SchemaVersion != outline.SchemaVersion || len(payload.Chapters) != 1 || payload.Chapters[0].ChapterTitle != "视频引入" || !strings.Contains(payload.Content, "## 视频引入") {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestOutlineEndpointRejectsPlaceholderArtifact(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusCompleted,
		TranscriptGeneration: "generation-1", OutlineWikiPageID: "outline-page",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/knowledgebase/kb-1/wiki/pages":
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{
				Pages: []weknora.WikiPage{{ID: "outline-page", Slug: "outline/video-1", PageType: "index"}},
				Total: 1, TotalPages: 1,
			})
		case "/api/v1/knowledgebase/kb-1/wiki/pages/outline/video-1":
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "outline-page", Slug: "outline/video-1", PageType: "index",
				Content: "---\ntype: outline\nsource_video_id: " + video.ID + "\ntranscript_generation: generation-1\n---\n\n...",
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/outline", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").Outline(context)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.ErrorCode != "artifact_invalid" {
		t.Fatalf("error_code = %q", payload.ErrorCode)
	}
}

func TestRelatedKnowledgeNotGeneratedReturnsStructuredStageStatus(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/related-knowledge", nil)

	wiki := weknora.NewWikiClient(config.WeKnoraConfig{})
	NewContentHandler(db, wiki, "kb-1").RelatedKnowledge(context)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Status       string `json:"status"`
		Stage        string `json:"stage"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Status != "failed" || payload.Stage != "graph" || payload.ErrorCode != "not_generated" || payload.ErrorMessage == "" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRelatedKnowledgeReturnsAnchorTimelineFromWikiContent(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusProcessing,
		KnowledgeBaseWikiPageID: "knowledge-base-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/knowledge-base/video-1") {
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "knowledge-base-1", Slug: "knowledge-base/video-1", PageType: "index",
				Content: "---\ntype: knowledge_base\nsource_video_id: " + video.ID + "\n---\n知识底座",
			})
			return
		}
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		pages := []weknora.WikiPage{
			{ID: "knowledge-base-1", Title: "知识底座", Slug: "knowledge-base/video-1", PageType: "index"},
			{ID: "entity-1", Title: "张三", PageType: "entity",
				Content: "---\ntype: person\nsource_video_id: " + video.ID + "\n---\n# 张三\n\n时间范围：00:01:02–00:01:10"},
			{ID: "case-1", Title: "复盘案例", PageType: "index",
				Content: "---\ntype: case\nsource_video_id: " + video.ID + "\n---\n# 复盘案例\n\n时间范围：00:02:03–00:02:30"},
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: pages, TotalPages: 1})
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/related-knowledge", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").RelatedKnowledge(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Anchors map[string][]struct {
			Timestamp string `json:"timestamp"`
			Seconds   int    `json:"seconds"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Anchors["entity"]) != 1 || payload.Anchors["entity"][0].Timestamp != "00:01:02" || payload.Anchors["entity"][0].Seconds != 62 {
		t.Fatalf("entity anchors = %#v", payload.Anchors["entity"])
	}
	if len(payload.Anchors["case"]) != 1 || payload.Anchors["case"][0].Timestamp != "00:02:03" || payload.Anchors["case"][0].Seconds != 123 {
		t.Fatalf("case anchors = %#v", payload.Anchors["case"])
	}
}

func TestRelatedKnowledgeReturnsTypeFrameworkDetails(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusCompleted,
		KnowledgeBaseWikiPageID: "knowledge-base-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/knowledgebase/kb-1/wiki/pages/video/"+video.ID {
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "knowledge-base-1", Slug: "video/" + video.ID, PageType: "index",
				Content: "---\ntype: knowledge_base\nsource_video_id: " + video.ID + "\n---\n# 视频知识底座\n\n- [[操作方法]]",
			})
			return
		}
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		pages := []weknora.WikiPage{
			{ID: "knowledge-base-1", Slug: "video/" + video.ID, PageType: "index"},
			{ID: "method-1", Slug: "methodology/method-1", PageType: "methodology", Title: "操作方法", Content: "---\ntype: methodology\nsource_video_id: " + video.ID + "\n---\n# 操作方法\n\n核心内容：通过异常数据定位原因。\n\n### 方法论结构\n\n- 输入：留存曲线\n- 步骤：按渠道拆分；对比异常渠道\n- 判断标准：变更时间与留存拐点接近\n- 输出：导致留存下降的变更项\n- 适用条件：单指标异常归因\n\n时间范围：00:03:00-00:04:00\n证据 ID：E001、E002\n信息性质：归纳"},
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: pages, TotalPages: 1})
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/related-knowledge", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").RelatedKnowledge(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Anchors map[string][]struct {
			CoreContent       string   `json:"core_content"`
			InformationNature string   `json:"information_nature"`
			EvidenceIDs       []string `json:"evidence_ids"`
			StructureFields   []struct {
				Key   string `json:"key"`
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"structure_fields"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	methods := payload.Anchors["method"]
	if len(methods) != 1 {
		t.Fatalf("method anchors = %#v", methods)
	}
	method := methods[0]
	if method.CoreContent != "通过异常数据定位原因。" || method.InformationNature != "归纳" || strings.Join(method.EvidenceIDs, ",") != "E001,E002" {
		t.Fatalf("method detail = %#v", method)
	}
	if len(method.StructureFields) != 5 || method.StructureFields[0].Key != "input" || method.StructureFields[0].Value != "留存曲线" || method.StructureFields[2].Key != "criteria" {
		t.Fatalf("structure fields = %#v", method.StructureFields)
	}
}

func TestWikiKnowledgeDetailParsesMarkdownTableFields(t *testing.T) {
	content := "---\ntype: concept\nsource_video_id: video-1\n---\n# 网络效应\n\n核心内容：用户越多，产品价值越高。\n\n### 概念结构\n\n| 字段 | 内容 |\n|---|---|\n| 定义 | 产品价值随用户数量增加而增加 |\n| 构成要素 | 用户基数、连接密度 |\n| 运行机制 | 新用户增加可连接节点 |\n| 相邻区别 | 区别于规模效应 |\n\n证据 ID：E003\n"
	detail := wikiKnowledgeDetail(content, "concept", "")

	if detail.CoreContent != "用户越多，产品价值越高。" || strings.Join(detail.EvidenceIDs, ",") != "E003" {
		t.Fatalf("detail = %#v", detail)
	}
	if len(detail.StructureFields) != 4 || detail.StructureFields[0].Key != "definition" || detail.StructureFields[0].Value != "产品价值随用户数量增加而增加" {
		t.Fatalf("structure fields = %#v", detail.StructureFields)
	}
}

func TestWikiKnowledgeDetailParsesContractSections(t *testing.T) {
	content := "---\ntype: insight\nsource_video_id: video-1\n---\n# 智能会通胀，智慧仍然稀缺\n\n核心内容：模型能力普及后，人的判断和反思更稀缺。\n\n## 洞察结构\n\n- 核心判断：智能工具会变便宜，但高质量判断不会自动增加\n- 推导依据：工具降低了执行门槛，却没有替代经验和反思\n\n## 时间范围\n\n00:01:02-00:01:40\n\n## 证据 ID\n\nE003、E004\n\n## 信息性质\n\n判断\n\n## 关联知识\n\n- [[智能通胀]]\n- [[人文教育|创造力教育]]\n\n## 关联实体\n\n- [[程乐松]]\n"

	detail := wikiKnowledgeDetail(content, "insight", "")

	if detail.CoreContent != "模型能力普及后，人的判断和反思更稀缺。" || detail.TimeRange != "00:01:02-00:01:40" || detail.InformationNature != "判断" || strings.Join(detail.EvidenceIDs, ",") != "E003,E004" {
		t.Fatalf("detail = %#v", detail)
	}
	if len(detail.StructureFields) != 2 || detail.StructureFields[0].Key != "claim" || detail.StructureFields[1].Key != "reasoning" {
		t.Fatalf("structure fields = %#v", detail.StructureFields)
	}
	if len(detail.RelatedKnowledge) != 2 || detail.RelatedKnowledge[0].Title != "智能通胀" || detail.RelatedKnowledge[1].Title != "创造力教育" || detail.RelatedKnowledge[1].Slug != "人文教育" {
		t.Fatalf("related knowledge = %#v", detail.RelatedKnowledge)
	}
	if len(detail.RelatedEntities) != 1 || detail.RelatedEntities[0].Title != "程乐松" {
		t.Fatalf("related entities = %#v", detail.RelatedEntities)
	}
}

func TestKnowledgeBaseWikiPageRequiresExtractionContract(t *testing.T) {
	valid := &weknora.WikiPage{
		PageType: "index",
		Content:  "---\ntype: knowledge_base\nsource_video_id: video-1\n---\n知识底座",
	}
	if !isKnowledgeBaseWikiPage(valid, "video-1") {
		t.Fatal("valid knowledge base page was rejected")
	}

	invalid := &weknora.WikiPage{
		PageType: "index",
		Content:  "---\ntype: knowledge_base\nsource_video_id: video-2\n---\n知识底座",
	}
	if isKnowledgeBaseWikiPage(invalid, "video-1") {
		t.Fatal("knowledge base page from another video was accepted")
	}

	legacy := &weknora.WikiPage{
		PageType: "index",
		Slug:     "video/video-1",
		Content:  "# 视频知识底座\n\n业务视频 ID：video-1",
	}
	if !isKnowledgeBaseWikiPage(legacy, "video-1") {
		t.Fatal("legacy knowledge base page was rejected")
	}

	legacyWrongSlug := *legacy
	legacyWrongSlug.Slug = "video/another-video"
	if isKnowledgeBaseWikiPage(&legacyWrongSlug, "video-1") {
		t.Fatal("legacy knowledge base page with another slug was accepted")
	}

	wrongType := &weknora.WikiPage{
		PageType: "index",
		Slug:     "video/video-1",
		Content:  "---\ntype: overview\n---\n正文包含 video-1",
	}
	if isKnowledgeBaseWikiPage(wrongType, "video-1") {
		t.Fatal("non-knowledge-base page was accepted as a legacy index")
	}
}

func TestRelatedKnowledgeReadsAllFiveKnowledgePageTypesAndLegacyIndex(t *testing.T) {
	db := openTestVideoDB(t)
	video := model.Video{
		ID: uuid.NewString(), Title: "video", Status: model.VideoStatusCompleted,
		KnowledgeBaseWikiPageID: "knowledge-base-1",
	}
	if err := db.Create(&video).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}

	pages := []weknora.WikiPage{
		{ID: "knowledge-base-1", Slug: "video/" + video.ID, PageType: "index"},
		{ID: "entity-1", Slug: "entity/person-1", PageType: "entity", Title: "张三", Content: "---\ntype: person\nsource_video_id: " + video.ID + "\n---\n# 张三"},
		{ID: "concept-1", Slug: "concept/concept-1", PageType: "concept", Title: "核心概念", Content: "---\ntype: concept\nsource_video_id: " + video.ID + "\n---\n# 核心概念"},
		{ID: "case-1", Slug: "case/case-1", PageType: "case", Title: "真实案例", Content: "---\ntype: case\nsource_video_id: " + video.ID + "\n---\n# 真实案例"},
		{ID: "method-1", Slug: "methodology/method-1", PageType: "methodology", Title: "操作方法", Content: "---\ntype: methodology\nsource_video_id: " + video.ID + "\n---\n# 操作方法"},
		{ID: "insight-1", Slug: "insight/insight-1", PageType: "insight", Title: "关键洞察", Content: "---\ntype: insight\nsource_video_id: " + video.ID + "\n---\n# 关键洞察"},
		{ID: "legacy-entity-1", Slug: "entity/legacy", PageType: "entity", Title: "历史实体", Content: "# 历史实体\n\n视频 ID：" + video.ID},
		{ID: "foreign-1", Slug: "concept/foreign-1", PageType: "concept", Title: "跨视频概念", Content: "---\ntype: concept\nsource_video_id: another-video\n---\n正文提及视频 ID：" + video.ID},
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/knowledgebase/kb-1/wiki/pages/video/"+video.ID {
			_ = json.NewEncoder(writer).Encode(weknora.WikiPage{
				ID: "knowledge-base-1", Slug: "video/" + video.ID, PageType: "index",
				Content: "# 视频知识底座\n\n业务视频 ID：" + video.ID,
			})
			return
		}
		if request.URL.Path != "/api/v1/knowledgebase/kb-1/wiki/pages" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: pages, TotalPages: 1})
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "id", Value: video.ID}}
	context.Request = httptest.NewRequest(http.MethodGet, "/api/custom/videos/"+video.ID+"/related-knowledge", nil)
	wiki := weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: server.URL})

	NewContentHandler(db, wiki, "kb-1").RelatedKnowledge(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Anchors map[string][]json.RawMessage `json:"anchors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, knowledgeType := range []string{"entity", "concept", "case", "method", "insight"} {
		expectedCount := 1
		if knowledgeType == "entity" {
			expectedCount = 2
		}
		if len(payload.Anchors[knowledgeType]) != expectedCount {
			t.Fatalf("%s anchors = %#v", knowledgeType, payload.Anchors[knowledgeType])
		}
	}
}
