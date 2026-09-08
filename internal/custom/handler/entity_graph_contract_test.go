package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/gin-gonic/gin"
)

const (
	graphContractVideoID    = "11111111-1111-4111-8111-111111111111"
	graphContractPageID     = "22222222-2222-4222-8222-222222222222"
	graphContractTargetID   = "33333333-3333-4333-8333-333333333333"
	graphContractGeneration = "generation-current"
)

func TestEntityGraphDetailUsesWikiPageIDAndReturnsCurrentEvidence(t *testing.T) {
	page := contractWikiPage(graphContractPageID, "concept/source", "concept", "来源概念", "evs:v1:source")
	target := contractWikiPage(graphContractTargetID, "methodology/target", "methodology", "目标方法", "evs:v1:target")
	page.Content += "\n\n参阅 [[methodology/target|目标方法]]。"
	wikiServer := newGraphContractWikiServer(t, []weknora.WikiPage{page, target})

	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	if err := db.Create(&model.Video{ID: graphContractVideoID, Title: "证据视频", TranscriptGeneration: graphContractGeneration}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: graphContractVideoID, Generation: graphContractGeneration, ChunkIndex: 0,
		KnowledgeID: "knowledge-source", EvidenceSentenceID: "evs:v1:source",
		StartMs: 1200, EndMs: 3400, ContentHash: "hash", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: graphContractVideoID, Generation: graphContractGeneration, ChunkIndex: 1,
		KnowledgeID: "knowledge-target", EvidenceSentenceID: "evs:v1:target",
		StartMs: 4000, EndMs: 5200, ContentHash: "hash-target", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create target evidence: %v", err)
	}

	h := &EntityGraphHandler{
		db: db,
		graph: &graphStoreStub{graph: &knowledgegraph.Graph{Nodes: []knowledgegraph.Node{{
			ID: "wiki:" + graphContractPageID, WikiPageID: graphContractPageID,
			KnowledgeObjectID: "object-" + graphContractPageID, KnowledgeType: knowledge.TypeConcept,
			SourceVideoID: graphContractVideoID, TranscriptGeneration: graphContractGeneration, AuditStatus: "passed",
		}}, Edges: []knowledgegraph.Edge{{
			ID: "formal-detail", SourceWikiPageID: graphContractPageID, TargetWikiPageID: graphContractTargetID,
			RelationType: "explains", Confidence: 0.9, EvidenceIDs: []string{"evs:v1:source"},
		}}}},
		wiki:     weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
		evidence: weknora.New(config.WeKnoraConfig{BaseURL: wikiServer.URL, KBID: "evidence-kb"}),
		kbID:     "kb-1",
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "wikiPageID", Value: graphContractPageID}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph/wiki-pages/"+graphContractPageID, nil)
	h.Detail(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool                      `json:"success"`
		Data    entityGraphDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data.Status != graphStatusReady || response.Data.Detail.ID != graphContractPageID {
		t.Fatalf("detail response = %#v", response)
	}
	if response.Data.Detail.SourceVideoTitle != "证据视频" || response.Data.Detail.TranscriptGeneration != graphContractGeneration {
		t.Fatalf("detail source = %#v", response.Data.Detail)
	}
	if len(response.Data.Evidence) != 1 || response.Data.Evidence[0].EvidenceSentenceID != "evs:v1:source" || response.Data.Evidence[0].StartMs != 1200 || response.Data.Evidence[0].Seconds != 1 || response.Data.Evidence[0].Text != "可回读的证据原句" {
		t.Fatalf("detail evidence = %#v", response.Data.Evidence)
	}
	if len(response.Data.Detail.StructureFields) == 0 {
		t.Fatalf("detail structure fields = %#v", response.Data.Detail.StructureFields)
	}
	if len(response.Data.FormalRelations) != 1 || len(response.Data.ReadingAssociations) != 1 {
		t.Fatalf("detail relation layers = formal:%#v reading:%#v", response.Data.FormalRelations, response.Data.ReadingAssociations)
	}
	if len(response.Data.Detail.Relations) != 1 || response.Data.Detail.Relations[0].TargetWikiPageID != graphContractTargetID {
		t.Fatalf("validated detail relations = %#v", response.Data.Detail.Relations)
	}
	h.graph = &graphStoreStub{graph: &knowledgegraph.Graph{}}
	notProjectedRecorder := httptest.NewRecorder()
	notProjectedContext, _ := gin.CreateTestContext(notProjectedRecorder)
	notProjectedContext.Params = gin.Params{{Key: "wikiPageID", Value: graphContractPageID}}
	notProjectedContext.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph/wiki-pages/"+graphContractPageID, nil)
	h.Detail(notProjectedContext)
	var notProjected struct {
		Data entityGraphDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(notProjectedRecorder.Body.Bytes(), &notProjected); err != nil || notProjectedRecorder.Code != http.StatusOK || notProjected.Data.Status != graphStatusNotProjected {
		t.Fatalf("not projected response = status:%d body:%s err=%v", notProjectedRecorder.Code, notProjectedRecorder.Body.String(), err)
	}
}

func TestEntityGraphDetailRejectsSlugAndGraphID(t *testing.T) {
	h := &EntityGraphHandler{}
	for _, invalid := range []string{"concept/source", "wiki:" + graphContractPageID} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Params = gin.Params{{Key: "wikiPageID", Value: invalid}}
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph/wiki-pages/value", nil)
		h.Detail(ctx)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("id %q status = %d, body = %s", invalid, recorder.Code, recorder.Body.String())
		}
		var body struct {
			ErrorCode string `json:"error_code"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.ErrorCode != graphErrorInvalidPageID {
			t.Fatalf("id %q response = %#v err=%v", invalid, body, err)
		}
	}
}

func TestEntityGraphDetailRouteEnforcesWikiPageID(t *testing.T) {
	deps := &Deps{
		DB: openTestVideoDB(t),
		Cfg: &config.Config{WeKnora: config.WeKnoraConfig{
			EvidenceKBID: "evidence-kb", KnowledgeKBID: "kb-1",
		}},
		Graph: &graphStoreStub{graph: &knowledgegraph.Graph{}},
	}
	router := BuildRouterForDeps(deps)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/custom/graph/wiki-pages/concept-title", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.ErrorCode != graphErrorInvalidPageID {
		t.Fatalf("route response = %#v err=%v", body, err)
	}
}

func TestEntityGraphDetailSeparatesMissingUnknownAndDamagedPages(t *testing.T) {
	unknown := contractWikiPage(graphContractPageID, "knowledge/unknown", "framework", "未知框架", "evs:v1:unknown")
	damaged := contractWikiPage(graphContractPageID, "concept/damaged", "concept", "损坏概念", "evs:v1:source")
	damaged.Content = strings.Replace(damaged.Content, "knowledge_object_id: object-"+graphContractPageID, "knowledge_object_id:", 1)
	tests := []struct {
		name       string
		pages      []weknora.WikiPage
		wantStatus int
		wantCode   string
	}{
		{name: "missing page", wantStatus: http.StatusNotFound, wantCode: graphErrorPageMissing},
		{name: "unknown type", pages: []weknora.WikiPage{unknown}, wantStatus: http.StatusUnprocessableEntity, wantCode: graphErrorUnknownType},
		{name: "damaged page", pages: []weknora.WikiPage{damaged}, wantStatus: http.StatusUnprocessableEntity, wantCode: graphErrorPageInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wikiServer := newGraphContractWikiServer(t, test.pages)
			h := &EntityGraphHandler{
				db: openTestVideoDB(t), graph: &graphStoreStub{graph: &knowledgegraph.Graph{}},
				wiki:     weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
				evidence: weknora.New(config.WeKnoraConfig{BaseURL: wikiServer.URL, KBID: "evidence-kb"}), kbID: "kb-1",
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "wikiPageID", Value: graphContractPageID}}
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph/wiki-pages/"+graphContractPageID, nil)
			h.Detail(ctx)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var body struct {
				ErrorCode string `json:"error_code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.ErrorCode != test.wantCode {
				t.Fatalf("response = %#v err=%v", body, err)
			}
		})
	}
}

func TestEntityGraphDetailSeparatesGenerationAndEvidenceFailures(t *testing.T) {
	tests := []struct {
		name       string
		generation string
		evidenceID string
		wantStatus int
		wantCode   string
	}{
		{name: "cross generation", generation: "generation-old", evidenceID: "evs:v1:source", wantStatus: http.StatusConflict, wantCode: graphErrorGenerationMismatch},
		{name: "missing evidence", generation: graphContractGeneration, evidenceID: "evs:v1:missing", wantStatus: http.StatusUnprocessableEntity, wantCode: graphErrorEvidenceInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page := contractWikiPage(graphContractPageID, "concept/source", "concept", "来源概念", test.evidenceID)
			page.Content = replaceGraphContractGeneration(page.Content, test.generation)
			wikiServer := newGraphContractWikiServer(t, []weknora.WikiPage{page})
			db := openTestVideoDB(t)
			if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
				t.Fatalf("migrate chunks: %v", err)
			}
			if err := db.Create(&model.Video{ID: graphContractVideoID, Title: "证据视频", TranscriptGeneration: graphContractGeneration}).Error; err != nil {
				t.Fatalf("create video: %v", err)
			}
			h := &EntityGraphHandler{
				db: db, graph: &graphStoreStub{graph: &knowledgegraph.Graph{}},
				wiki:     weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}),
				evidence: weknora.New(config.WeKnoraConfig{BaseURL: wikiServer.URL, KBID: "evidence-kb"}), kbID: "kb-1",
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "wikiPageID", Value: graphContractPageID}}
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph/wiki-pages/"+graphContractPageID, nil)
			h.Detail(ctx)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			var body struct {
				ErrorCode string `json:"error_code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.ErrorCode != test.wantCode {
				t.Fatalf("response = %#v err=%v", body, err)
			}
		})
	}
}

func TestEntityGraphOverviewSeparatesFiveTypesUnknownRelationsAndOrphans(t *testing.T) {
	types := []knowledge.KnowledgeType{
		knowledge.TypeEntity, knowledge.TypeConcept, knowledge.TypeMethodology, knowledge.TypeCase, knowledge.TypeInsight,
	}
	pageIDs := []string{
		"40000000-0000-4000-8000-000000000001", "40000000-0000-4000-8000-000000000002",
		"40000000-0000-4000-8000-000000000003", "40000000-0000-4000-8000-000000000004",
		"40000000-0000-4000-8000-000000000005", "40000000-0000-4000-8000-000000000006",
	}
	pages := make([]weknora.WikiPage, 0, len(pageIDs))
	nodes := make([]knowledgegraph.Node, 0, len(pageIDs))
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	if err := db.Create(&model.Video{ID: graphContractVideoID, Title: "五类视频", TranscriptGeneration: graphContractGeneration, KnowledgeBaseWikiPageID: "index-page"}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	for index, knowledgeType := range types {
		evidenceID := "evs:v1:type-" + string(knowledgeType)
		page := contractWikiPage(pageIDs[index], "knowledge/"+string(knowledgeType), string(knowledgeType), "类型 "+string(knowledgeType), evidenceID)
		pages = append(pages, page)
		nodes = append(nodes, knowledgegraph.Node{
			ID: "temporary-node-" + string(knowledgeType), WikiPageID: page.ID,
			KnowledgeObjectID: "object-" + page.ID, KnowledgeType: knowledgeType, Title: page.Title,
			SourceVideoID: graphContractVideoID, TranscriptGeneration: graphContractGeneration,
			AuditStatus: "passed", EvidenceIDs: []string{evidenceID},
		})
		if err := db.Create(&model.VideoTranscriptChunk{
			VideoID: graphContractVideoID, Generation: graphContractGeneration, ChunkIndex: index,
			KnowledgeID: "knowledge-" + string(knowledgeType), EvidenceSentenceID: evidenceID,
			StartMs: index * 1000, EndMs: (index + 1) * 1000, ContentHash: "hash-" + string(knowledgeType), Status: "completed",
		}).Error; err != nil {
			t.Fatalf("create type evidence: %v", err)
		}
	}
	unknown := contractWikiPage(pageIDs[5], "knowledge/unknown", "framework", "未知框架", "evs:v1:unknown")
	pages = append(pages, unknown)
	nodes = append(nodes, knowledgegraph.Node{
		ID: "temporary-unknown", WikiPageID: unknown.ID, KnowledgeObjectID: "object-" + unknown.ID,
		KnowledgeType: knowledge.KnowledgeType("framework"), Title: unknown.Title,
		SourceVideoID: graphContractVideoID, TranscriptGeneration: graphContractGeneration, AuditStatus: "passed",
	})
	pages[0].Content += "\n\n参阅 [[knowledge/concept|类型 concept]]。"
	wikiServer := newGraphContractWikiServer(t, pages)
	h := &EntityGraphHandler{
		db: db,
		graph: &graphStoreStub{graph: &knowledgegraph.Graph{
			Nodes: nodes,
			Edges: []knowledgegraph.Edge{{
				ID: "formal-1", SourceWikiPageID: pageIDs[0], TargetWikiPageID: pageIDs[1],
				RelationType: "explains", Confidence: 0.9, EvidenceIDs: []string{"evs:v1:type-entity"},
			}},
		}},
		wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}), kbID: "kb-1",
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph?video_id="+graphContractVideoID+"&limit=10", nil)
	h.Get(ctx)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool                `json:"success"`
		Data    entityGraphResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data.Status != graphStatusPartial {
		t.Fatalf("response status = %#v", response.Data)
	}
	if len(response.Data.Nodes) != 5 || len(response.Data.UnknownNodes) != 1 || len(response.Data.RejectedRecords) != 0 {
		t.Fatalf("node partitions = known:%d unknown:%d rejected:%d", len(response.Data.Nodes), len(response.Data.UnknownNodes), len(response.Data.RejectedRecords))
	}
	for _, knowledgeType := range types {
		if response.Data.Counts.TypeCounts[string(knowledgeType)] != 1 {
			t.Fatalf("type counts = %#v", response.Data.Counts.TypeCounts)
		}
	}
	if response.Data.Counts.TypeDenominator != 6 || response.Data.Counts.UnknownTypes != 1 || response.Data.Counts.UnknownTypeDenominator != 6 {
		t.Fatalf("type denominators = %#v", response.Data.Counts)
	}
	if response.Data.Counts.FormalRelations != 1 || response.Data.Counts.FormalRelationDenominator != 1 || response.Data.Counts.ReadingAssociations != 1 {
		t.Fatalf("relation counts = %#v", response.Data.Counts)
	}
	for _, node := range response.Data.Nodes {
		if node.ID != "wiki:"+node.WikiPageID {
			t.Fatalf("node uses non-Wiki identity: %#v", node)
		}
	}

	for _, knowledgeType := range types {
		filteredRecorder := httptest.NewRecorder()
		filteredContext, _ := gin.CreateTestContext(filteredRecorder)
		filteredContext.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph?types="+string(knowledgeType)+"&limit=10", nil)
		h.Get(filteredContext)
		var filtered struct {
			Data entityGraphResponse `json:"data"`
		}
		if err := json.Unmarshal(filteredRecorder.Body.Bytes(), &filtered); err != nil {
			t.Fatalf("decode %s response: %v", knowledgeType, err)
		}
		if filteredRecorder.Code != http.StatusOK || len(filtered.Data.Nodes) != 1 || filtered.Data.Nodes[0].KnowledgeType != knowledgeType {
			t.Fatalf("%s response = status:%d body:%s", knowledgeType, filteredRecorder.Code, filteredRecorder.Body.String())
		}
	}

	var previousIDs string
	for attempt := 0; attempt < 2; attempt++ {
		limitedRecorder := httptest.NewRecorder()
		limitedContext, _ := gin.CreateTestContext(limitedRecorder)
		limitedContext.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph?limit=2", nil)
		h.Get(limitedContext)
		var limited struct {
			Data entityGraphResponse `json:"data"`
		}
		if err := json.Unmarshal(limitedRecorder.Body.Bytes(), &limited); err != nil {
			t.Fatalf("decode limited response: %v", err)
		}
		if len(limited.Data.Nodes) != 2 || !limited.Data.Meta.Truncated {
			t.Fatalf("limited response = %s", limitedRecorder.Body.String())
		}
		ids := limited.Data.Nodes[0].WikiPageID + "," + limited.Data.Nodes[1].WikiPageID
		if previousIDs != "" && ids != previousIDs {
			t.Fatalf("limited order changed: first=%s second=%s", previousIDs, ids)
		}
		previousIDs = ids
	}
}

func TestResolveEvidenceChunkRejectsEmbeddedCrossVideoAndGenerationRefs(t *testing.T) {
	current := model.VideoTranscriptChunk{
		VideoID: graphContractVideoID, Generation: graphContractGeneration,
		KnowledgeID: "knowledge-current", ChunkIndex: 3,
	}
	otherVideoID := "88888888-8888-4888-8888-888888888888"
	other := model.VideoTranscriptChunk{
		VideoID: otherVideoID, Generation: graphContractGeneration,
		KnowledgeID: "knowledge-other", ChunkIndex: 3,
	}
	old := model.VideoTranscriptChunk{
		VideoID: graphContractVideoID, Generation: "generation-old",
		KnowledgeID: "knowledge-old", ChunkIndex: 3,
	}
	byEvidence := map[string]model.VideoTranscriptChunk{
		graphContractVideoID + "\x00" + graphContractGeneration + "\x00" + current.KnowledgeID: current,
		otherVideoID + "\x00" + graphContractGeneration + "\x00" + other.KnowledgeID:           other,
		graphContractVideoID + "\x00generation-old\x00" + old.KnowledgeID:                      old,
	}
	byIndex := map[string]model.VideoTranscriptChunk{
		graphContractVideoID + "\x00" + graphContractGeneration + "\x003": current,
		otherVideoID + "\x00" + graphContractGeneration + "\x003":         other,
		graphContractVideoID + "\x00generation-old\x003":                  old,
	}

	validRef := "knowledge-current|transcript/" + graphContractVideoID + "/" + graphContractGeneration + "/000003"
	if _, ok := resolveEvidenceChunk(byEvidence, byIndex, graphContractVideoID, graphContractGeneration, validRef); !ok {
		t.Fatal("current embedded evidence ref was rejected")
	}
	invalidRefs := []string{
		"knowledge-other|transcript/" + otherVideoID + "/" + graphContractGeneration + "/000003",
		"knowledge-old|transcript/" + graphContractVideoID + "/generation-old/000003",
	}
	for _, evidenceID := range invalidRefs {
		if _, ok := resolveEvidenceChunk(byEvidence, byIndex, graphContractVideoID, graphContractGeneration, evidenceID); ok {
			t.Fatalf("cross-scope evidence %q was accepted", evidenceID)
		}
	}
}

func TestFormalRelationsRequireCurrentValidEndpointsAndExcludeReadingEdges(t *testing.T) {
	source := contractWikiPage(graphContractPageID, "concept/source", "concept", "来源概念", "evs:v1:source")
	target := contractWikiPage(graphContractTargetID, "methodology/target", "methodology", "目标方法", "evs:v1:target")
	oldTargetID := "99999999-9999-4999-8999-999999999999"
	oldTarget := contractWikiPage(oldTargetID, "methodology/old", "methodology", "旧代次方法", "evs:v1:old")
	oldTarget.Content = replaceGraphContractGeneration(oldTarget.Content, "generation-old")
	chunks := []model.VideoTranscriptChunk{
		{VideoID: graphContractVideoID, Generation: graphContractGeneration, KnowledgeID: "knowledge-source", EvidenceSentenceID: "evs:v1:source", ChunkIndex: 0},
		{VideoID: graphContractVideoID, Generation: graphContractGeneration, KnowledgeID: "knowledge-target", EvidenceSentenceID: "evs:v1:target", ChunkIndex: 1},
	}
	byEvidence := make(map[string]model.VideoTranscriptChunk)
	byIndex := make(map[string]model.VideoTranscriptChunk)
	for _, chunk := range chunks {
		prefix := chunk.VideoID + "\x00" + chunk.Generation + "\x00"
		byEvidence[prefix+chunk.KnowledgeID] = chunk
		byEvidence[prefix+chunk.EvidenceSentenceID] = chunk
		byIndex[prefix+strconv.Itoa(chunk.ChunkIndex)] = chunk
	}
	edges := []knowledgegraph.Edge{
		{ID: "valid", SourceWikiPageID: graphContractPageID, TargetWikiPageID: graphContractTargetID, RelationType: "explains", EvidenceIDs: []string{"evs:v1:source"}},
		{ID: "wiki-link:reading", SourceWikiPageID: graphContractPageID, TargetWikiPageID: graphContractTargetID, RelationType: "related_to"},
		{ID: "old-target", SourceWikiPageID: graphContractPageID, TargetWikiPageID: oldTargetID, RelationType: "explains", EvidenceIDs: []string{"evs:v1:source"}},
		{ID: "missing-target", SourceWikiPageID: graphContractPageID, TargetWikiPageID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", RelationType: "explains", EvidenceIDs: []string{"evs:v1:source"}},
	}
	pages := map[string]weknora.WikiPage{source.ID: source, target.ID: target, oldTarget.ID: oldTarget}
	got := formalRelationsForPage(graphContractPageID, edges, pages, graphContractVideoID, graphContractGeneration, byEvidence, byIndex)
	if len(got) != 1 || got[0].ID != "valid" {
		t.Fatalf("formal relations = %#v", got)
	}
	if got[0].SourceTitle != "来源概念" || got[0].TargetTitle != "目标方法" || got[0].TargetSlug != "methodology/target" {
		t.Fatalf("formal relation endpoints = %#v", got[0])
	}
	incoming := formalRelationsForPage(graphContractTargetID, edges, pages, graphContractVideoID, graphContractGeneration, byEvidence, byIndex)
	if len(incoming) != 1 || incoming[0].SourceTitle != "来源概念" || incoming[0].SourceSlug != "concept/source" {
		t.Fatalf("incoming formal relations = %#v", incoming)
	}
}

func TestEntityGraphOverviewRejectsUnknownFilterWithIndependentCode(t *testing.T) {
	h := &EntityGraphHandler{db: openTestVideoDB(t), graph: &graphStoreStub{graph: &knowledgegraph.Graph{}}, wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: "http://127.0.0.1"}), kbID: "kb-1"}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph?types=framework", nil)
	h.Get(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.ErrorCode != graphErrorUnknownFilter {
		t.Fatalf("response = %#v err=%v", body, err)
	}
}

func TestEntityGraphOverviewRejectsNonUUIDWikiPageIdentity(t *testing.T) {
	page := contractWikiPage("not-a-uuid", "concept/invalid-id", "concept", "无效页面标识", "evs:v1:source")
	wikiServer := newGraphContractWikiServer(t, []weknora.WikiPage{page})
	db := openTestVideoDB(t)
	if err := db.AutoMigrate(&model.VideoTranscriptChunk{}); err != nil {
		t.Fatalf("migrate chunks: %v", err)
	}
	if err := db.Create(&model.Video{ID: graphContractVideoID, TranscriptGeneration: graphContractGeneration}).Error; err != nil {
		t.Fatalf("create video: %v", err)
	}
	if err := db.Create(&model.VideoTranscriptChunk{
		VideoID: graphContractVideoID, Generation: graphContractGeneration, ChunkIndex: 0,
		KnowledgeID: "knowledge-source", EvidenceSentenceID: "evs:v1:source", ContentHash: "hash", Status: "completed",
	}).Error; err != nil {
		t.Fatalf("create evidence: %v", err)
	}
	h := &EntityGraphHandler{
		db: db,
		graph: &graphStoreStub{graph: &knowledgegraph.Graph{Nodes: []knowledgegraph.Node{{
			ID: "wiki:not-a-uuid", WikiPageID: page.ID, KnowledgeObjectID: "object-" + page.ID,
			KnowledgeType: knowledge.TypeConcept, SourceVideoID: graphContractVideoID,
			TranscriptGeneration: graphContractGeneration, AuditStatus: "passed", EvidenceIDs: []string{"evs:v1:source"},
		}}}},
		wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: wikiServer.URL}), kbID: "kb-1",
	}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph", nil)
	h.Get(ctx)
	var response struct {
		Data entityGraphResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if recorder.Code != http.StatusOK || response.Data.Status != graphStatusPartial || len(response.Data.Nodes) != 0 ||
		len(response.Data.RejectedRecords) != 1 || response.Data.RejectedRecords[0].Status != graphErrorInvalidPageID {
		t.Fatalf("response = status:%d body:%s", recorder.Code, recorder.Body.String())
	}
}

func TestEntityGraphOverviewReturnsIndependentEmptyStatuses(t *testing.T) {
	db := openTestVideoDB(t)
	if err := db.Create(&model.Video{ID: graphContractVideoID, Title: "已生成页面", KnowledgeBaseWikiPageID: "index-page"}).Error; err != nil {
		t.Fatalf("create projected video: %v", err)
	}
	secondVideoID := "55555555-5555-4555-8555-555555555555"
	if err := db.Create(&model.Video{ID: secondVideoID, Title: "未生成页面"}).Error; err != nil {
		t.Fatalf("create ungenerated video: %v", err)
	}
	tests := []struct {
		name   string
		path   string
		graph  *knowledgegraph.Graph
		status string
	}{
		{name: "empty graph", path: "/api/custom/graph", graph: &knowledgegraph.Graph{}, status: graphStatusEmpty},
		{name: "not projected", path: "/api/custom/graph?video_id=" + graphContractVideoID, graph: &knowledgegraph.Graph{}, status: graphStatusNotProjected},
		{name: "not generated", path: "/api/custom/graph?video_id=" + secondVideoID, graph: &knowledgegraph.Graph{}, status: graphStatusNotGenerated},
		{name: "video missing", path: "/api/custom/graph?video_id=66666666-6666-4666-8666-666666666666", graph: &knowledgegraph.Graph{}, status: graphStatusVideoMissing},
		{name: "filter empty", path: "/api/custom/graph?types=case", graph: &knowledgegraph.Graph{Nodes: []knowledgegraph.Node{{WikiPageID: graphContractPageID, KnowledgeType: knowledge.TypeConcept}}}, status: graphStatusFilterEmpty},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := &EntityGraphHandler{
				db: db, graph: &graphStoreStub{graph: test.graph},
				wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: "http://127.0.0.1"}), kbID: "kb-1",
			}
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, test.path, nil)
			h.Get(ctx)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Data entityGraphResponse `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Data.Status != test.status {
				t.Fatalf("response status = %q, want %q, err=%v", response.Data.Status, test.status, err)
			}
		})
	}
}

func TestEntityGraphReadFailureHasIndependentCodeAndNoProjection(t *testing.T) {
	store := &graphStoreStub{queryErr: errors.New("neo4j unavailable")}
	h := &EntityGraphHandler{db: openTestVideoDB(t), graph: store, wiki: weknora.NewWikiClient(config.WeKnoraConfig{BaseURL: "http://127.0.0.1"}), kbID: "kb-1"}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/custom/graph", nil)
	h.Get(ctx)
	if recorder.Code != http.StatusBadGateway || store.projectCount != 0 {
		t.Fatalf("status=%d project_count=%d body=%s", recorder.Code, store.projectCount, recorder.Body.String())
	}
	var body struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.ErrorCode != graphErrorReadFailed {
		t.Fatalf("response = %#v err=%v", body, err)
	}
}

func contractWikiPage(id, slug, knowledgeType, title, evidenceID string) weknora.WikiPage {
	structure := "definition: 可验证定义"
	if knowledgeType == "methodology" {
		structure = "input: 输入材料\n  steps: 执行步骤"
	}
	return weknora.WikiPage{
		ID: id, Slug: slug, Title: title, PageType: "index", Status: "published", Version: 1,
		Content: "---\nknowledge_object_id: object-" + id + "\nprimary_type: " + knowledgeType + "\ntype: " + knowledgeType + "\nsource_video_id: " + graphContractVideoID + "\ntranscript_generation: " + graphContractGeneration + "\naudit_status: passed\nclassification_confidence: 0.95\nevidence_ids: [" + evidenceID + "]\nstructure_fields:\n  " + structure + "\n---\n# " + title + "\n\n核心内容：真实内容。\n\n时间范围：00:00:01-00:00:04",
	}
}

func replaceGraphContractGeneration(content, generation string) string {
	return strings.Replace(content, "transcript_generation: "+graphContractGeneration, "transcript_generation: "+generation, 1)
}

func newGraphContractWikiServer(t *testing.T, pages []weknora.WikiPage) *httptest.Server {
	t.Helper()
	byPath := make(map[string]weknora.WikiPage, len(pages))
	for _, page := range pages {
		byPath["/api/v1/knowledgebase/kb-1/wiki/pages/"+page.Slug] = page
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/knowledgebase/kb-1/wiki/pages" {
			_ = json.NewEncoder(writer).Encode(weknora.ListPagesResp{Pages: pages, Total: len(pages), TotalPages: 1})
			return
		}
		if page, ok := byPath[request.URL.Path]; ok {
			_ = json.NewEncoder(writer).Encode(page)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/v1/chunks/") {
			knowledgeID := strings.TrimPrefix(request.URL.Path, "/api/v1/chunks/")
			_ = json.NewEncoder(writer).Encode(struct {
				Success  bool                     `json:"success"`
				Data     []weknora.KnowledgeChunk `json:"data"`
				Total    int                      `json:"total"`
				PageSize int                      `json:"page_size"`
			}{
				Success: true, Total: 1, PageSize: 100,
				Data: []weknora.KnowledgeChunk{{ID: "chunk-1", KnowledgeID: knowledgeID, ChunkIndex: 0, Content: "## 原文\n\n可回读的证据原句"}},
			})
			return
		}
		http.NotFound(writer, request)
	}))
	t.Cleanup(server.Close)
	return server
}
