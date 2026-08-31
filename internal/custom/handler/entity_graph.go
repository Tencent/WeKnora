package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type graphSource interface {
	GetEntityGraph(context.Context, string, int, []string) (*weknora.EntityGraphData, error)
}

type EntityGraphEvidence struct {
	VideoID    string   `json:"video_id"`
	VideoTitle string   `json:"video_title"`
	StartMs    int      `json:"start_ms"`
	EndMs      int      `json:"end_ms"`
	ChunkIndex int      `json:"chunk_index"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
}

type EntityGraphNode struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Label       string                `json:"label"`
	Type        string                `json:"type"`
	Attributes  []string              `json:"attributes"`
	KnowledgeID string                `json:"knowledge_id,omitempty"`
	VideoID     string                `json:"video_id,omitempty"`
	VideoTitle  string                `json:"video_title,omitempty"`
	VideoType   string                `json:"video_category,omitempty"`
	Seconds     int                   `json:"seconds,omitempty"`
	LinkCount   int                   `json:"link_count"`
	Evidence    []EntityGraphEvidence `json:"evidence,omitempty"`
}

type EntityGraphEdge struct {
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Type       string  `json:"type"`
	Weight     int     `json:"weight"`
	Confidence float64 `json:"confidence,omitempty"`
}

type entityGraphResponse struct {
	Nodes      []EntityGraphNode `json:"nodes"`
	Edges      []EntityGraphEdge `json:"edges"`
	Attributes []string          `json:"attributes"`
	Meta       struct {
		Mode      string `json:"mode"`
		Total     int    `json:"total"`
		Returned  int    `json:"returned"`
		Truncated bool   `json:"truncated"`
	} `json:"meta"`
}

type EntityGraphHandler struct {
	db    *gorm.DB
	graph graphSource
	kbID  string
}

func NewEntityGraphHandler(db *gorm.DB, graph *weknora.Client, kbID string) *EntityGraphHandler {
	return &EntityGraphHandler{db: db, graph: graph, kbID: strings.TrimSpace(kbID)}
}

func (h *EntityGraphHandler) Get(c *gin.Context) {
	if h.graph == nil || strings.TrimSpace(h.kbID) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "entity graph is unavailable"})
		return
	}
	limit := 500
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	if limit > 500 {
		limit = 500
	}
	requestedTypes := splitQueryValues(c.Query("types"))
	requestedVideoID := strings.TrimSpace(c.Query("video_id"))
	graphData, err := h.graph.GetEntityGraph(c.Request.Context(), h.kbID, 2000, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	if graphData == nil {
		graphData = &weknora.EntityGraphData{}
	}
	response, err := h.buildResponse(c.Request.Context(), graphData, requestedTypes, requestedVideoID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

func (h *EntityGraphHandler) buildResponse(ctx context.Context, source *weknora.EntityGraphData, requestedTypes []string, requestedVideoID string, limit int) (*entityGraphResponse, error) {
	knowledgeIDs := make([]string, 0, len(source.Nodes))
	seenKnowledgeIDs := make(map[string]struct{}, len(source.Nodes))
	for _, node := range source.Nodes {
		if node == nil || node.KnowledgeID == "" {
			continue
		}
		if _, exists := seenKnowledgeIDs[node.KnowledgeID]; !exists {
			seenKnowledgeIDs[node.KnowledgeID] = struct{}{}
			knowledgeIDs = append(knowledgeIDs, node.KnowledgeID)
		}
	}

	chunkByKnowledgeID := make(map[string]model.VideoTranscriptChunk, len(knowledgeIDs))
	videoByID := make(map[string]model.Video, len(knowledgeIDs))
	if h.db != nil && len(knowledgeIDs) > 0 {
		var chunks []model.VideoTranscriptChunk
		if err := h.db.WithContext(ctx).Where("knowledge_id IN ?", knowledgeIDs).Find(&chunks).Error; err != nil {
			return nil, fmt.Errorf("load transcript evidence: %w", err)
		}
		videoIDs := make([]string, 0, len(chunks))
		seenVideoIDs := make(map[string]struct{}, len(chunks))
		for _, chunk := range chunks {
			chunkByKnowledgeID[chunk.KnowledgeID] = chunk
			if _, exists := seenVideoIDs[chunk.VideoID]; !exists {
				seenVideoIDs[chunk.VideoID] = struct{}{}
				videoIDs = append(videoIDs, chunk.VideoID)
			}
		}
		if len(videoIDs) > 0 {
			var videos []model.Video
			if err := h.db.WithContext(ctx).Where("id IN ?", videoIDs).Find(&videos).Error; err != nil {
				return nil, fmt.Errorf("load graph videos: %w", err)
			}
			for _, video := range videos {
				videoByID[video.ID] = video
			}
		}
	}

	result := &entityGraphResponse{}
	result.Meta.Mode = "overview"
	allNodes := make([]EntityGraphNode, 0, len(source.Nodes))
	nodeIDByKey := make(map[string]string, len(source.Nodes))
	nodeIDByName := make(map[string]string, len(source.Nodes))
	for _, node := range source.Nodes {
		if node == nil || strings.TrimSpace(node.Name) == "" {
			continue
		}
		typeName := graphType(node.Attributes)
		if len(requestedTypes) > 0 && !contains(requestedTypes, typeName) {
			continue
		}
		chunk, hasChunk := chunkByKnowledgeID[node.KnowledgeID]
		video, hasVideo := videoByID[chunk.VideoID]
		if requestedVideoID != "" && (!hasChunk || chunk.VideoID != requestedVideoID) {
			continue
		}
		id := graphNodeID(node.KnowledgeID, node.Name)
		item := EntityGraphNode{
			ID: id, Name: node.Name, Label: node.Name, Type: typeName,
			Attributes: []string{typeName}, KnowledgeID: node.KnowledgeID,
			LinkCount: 0,
		}
		if hasVideo {
			item.VideoID = video.ID
			item.VideoTitle = video.Title
			item.VideoType = video.VideoType
		}
		if hasChunk {
			item.Seconds = chunk.StartMs / 1000
			item.Evidence = []EntityGraphEvidence{{
				VideoID: chunk.VideoID, VideoTitle: video.Title, StartMs: chunk.StartMs, EndMs: chunk.EndMs,
				ChunkIndex: chunk.ChunkIndex, ChunkIDs: append([]string(nil), node.Chunks...),
			}}
		}
		allNodes = append(allNodes, item)
		if node.KnowledgeID != "" {
			nodeIDByKey[node.KnowledgeID+"|"+node.Name] = id
		}
		if _, exists := nodeIDByName[node.Name]; !exists {
			nodeIDByName[node.Name] = id
		}
	}
	result.Meta.Total = len(allNodes)
	if len(allNodes) > limit {
		allNodes = allNodes[:limit]
		result.Meta.Truncated = true
	}
	result.Meta.Returned = len(allNodes)
	result.Nodes = allNodes

	visible := make(map[string]struct{}, len(allNodes))
	for _, node := range allNodes {
		visible[node.ID] = struct{}{}
	}
	for _, relation := range source.Edges {
		if relation == nil {
			continue
		}
		sourceID := nodeIDByKey[relation.SourceKnowledgeID+"|"+relation.Node1]
		if sourceID == "" {
			sourceID = nodeIDByName[relation.Node1]
		}
		targetID := nodeIDByKey[relation.TargetKnowledgeID+"|"+relation.Node2]
		if targetID == "" {
			targetID = nodeIDByName[relation.Node2]
		}
		if sourceID == "" || targetID == "" || sourceID == targetID {
			continue
		}
		if _, ok := visible[sourceID]; !ok {
			continue
		}
		if _, ok := visible[targetID]; !ok {
			continue
		}
		result.Edges = append(result.Edges, EntityGraphEdge{ID: sourceID + "->" + targetID + ":" + relation.Type, Source: sourceID, Target: targetID, Type: relation.Type, Weight: 1})
	}
	for index := range result.Nodes {
		for _, edge := range result.Edges {
			if edge.Source == result.Nodes[index].ID || edge.Target == result.Nodes[index].ID {
				result.Nodes[index].LinkCount++
			}
		}
	}
	attributes := []string{"实体", "概念", "案例", "方法", "洞察"}
	for _, typeName := range attributes {
		for _, node := range result.Nodes {
			if node.Type == typeName {
				result.Attributes = append(result.Attributes, typeName)
				break
			}
		}
	}
	return result, nil
}

func splitQueryValues(value string) []string {
	values := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func graphNodeID(knowledgeID, name string) string {
	if knowledgeID == "" {
		return name
	}
	return knowledgeID + "|" + name
}

func graphType(attributes []string) string {
	for _, attribute := range attributes {
		normalized := strings.ToLower(strings.TrimSpace(attribute))
		switch normalized {
		case "entity", "实体", "person", "organization", "机构", "人物", "产品", "技术", "行业", "地点":
			return "实体"
		case "concept", "概念":
			return "概念"
		case "case", "案例", "事件":
			return "案例"
		case "method", "methodology", "方法", "方法论":
			return "方法"
		case "insight", "洞察":
			return "洞察"
		}
	}
	return "概念"
}
