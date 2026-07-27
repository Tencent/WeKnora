package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxAgentCollectionExportProfiles = 100000

func (h *SystemAgentCollectionHandler) CreateExport(c *gin.Context) {
	var body struct {
		Format string                       `json:"format"`
		Filter agentCollectionFilterRequest `json:"filter"`
	}
	if c.ShouldBindJSON(&body) != nil || (body.Format != "csv" && body.Format != "xlsx") {
		collectionError(c, 400, "format must be csv or xlsx")
		return
	}
	filter, err := body.Filter.collectionFilter()
	if err != nil {
		collectionError(c, 400, err.Error())
		return
	}
	actor, _ := types.UserIDFromContext(c.Request.Context())
	snapshot := collectionFilterSnapshot(body.Filter)
	now := time.Now().UTC()
	export := &types.AgentCollectionExport{
		ID: uuid.NewString(), ActorUserID: actor, Format: body.Format, FilterSnapshot: snapshot,
		Status: types.AgentCollectionExportPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := h.service.CreateExport(c.Request.Context(), export); err != nil {
		collectionError(c, 500, "failed to create collection export")
		return
	}
	h.auditExport(c, export, "created")
	go h.generateExport(context.WithoutCancel(c.Request.Context()), export, filter)
	c.JSON(202, export)
}

func (h *SystemAgentCollectionHandler) GetExport(c *gin.Context) {
	export, err := h.service.GetExport(c.Request.Context(), c.Param("export_id"))
	if err != nil {
		collectionError(c, 404, "collection export not found")
		return
	}
	actor, _ := types.UserIDFromContext(c.Request.Context())
	if export.ActorUserID != actor {
		collectionError(c, 403, "collection export belongs to another administrator")
		return
	}
	if export.Status != types.AgentCollectionExportCompleted || c.Query("download") != "1" {
		view := *export
		view.StoragePath = ""
		c.JSON(200, &view)
		return
	}
	if export.StoragePath == "" {
		collectionError(c, 500, "completed collection export has no file")
		return
	}
	h.auditExport(c, export, "downloaded")
	c.FileAttachment(export.StoragePath, export.Filename)
}

func (h *SystemAgentCollectionHandler) generateExport(
	ctx context.Context,
	export *types.AgentCollectionExport,
	filter types.AgentCollectionProfileFilter,
) {
	export.Status = types.AgentCollectionExportRunning
	export.UpdatedAt = time.Now().UTC()
	_ = h.service.UpdateExport(ctx, export)
	profiles, err := h.service.ListProfilesForExport(ctx, filter, maxAgentCollectionExportProfiles+1)
	if err != nil {
		h.failExport(ctx, export, err)
		return
	}
	if len(profiles) > maxAgentCollectionExportProfiles {
		h.failExport(ctx, export, fmt.Errorf("export exceeds %d profiles", maxAgentCollectionExportProfiles))
		return
	}
	fields := h.exportFieldKeys(ctx, profiles)
	dir := h.exportDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "weknora-agent-collection-exports")
	}
	var path string
	if export.Format == "csv" {
		path, err = writeAgentCollectionCSV(dir, export.ID, profiles, fields)
	} else {
		path, err = writeAgentCollectionXLSX(dir, export.ID, profiles, fields)
	}
	if err != nil {
		h.failExport(ctx, export, err)
		return
	}
	export.Status = types.AgentCollectionExportCompleted
	export.StoragePath = path
	export.Filename = "agent-collection-" + time.Now().UTC().Format("20060102-150405") + "." + export.Format
	export.RowCount = int64(len(profiles))
	export.ErrorMessage = ""
	export.UpdatedAt = time.Now().UTC()
	_ = h.service.UpdateExport(ctx, export)
}

func (h *SystemAgentCollectionHandler) failExport(
	ctx context.Context,
	export *types.AgentCollectionExport,
	err error,
) {
	if export.StoragePath != "" {
		_ = os.Remove(export.StoragePath)
	}
	export.Status = types.AgentCollectionExportFailed
	export.ErrorMessage = err.Error()
	export.StoragePath = ""
	export.UpdatedAt = time.Now().UTC()
	_ = h.service.UpdateExport(ctx, export)
}

type orderedCollectionField struct {
	key   string
	order int
}

func (h *SystemAgentCollectionHandler) exportFieldKeys(
	ctx context.Context,
	profiles []*types.AgentCollectionProfile,
) []string {
	orders := make(map[string]int)
	agents := make(map[string]struct{})
	for _, profile := range profiles {
		agentScope := fmt.Sprintf("%d:%s", profile.AgentTenantID, profile.AgentID)
		if _, exists := agents[agentScope]; exists {
			continue
		}
		agents[agentScope] = struct{}{}
		agent, err := h.agents.GetAgentByIDAndTenant(ctx, profile.AgentID, profile.AgentTenantID)
		if err != nil {
			continue
		}
		for _, field := range agent.Config.CollectionFields {
			if previous, exists := orders[field.Key]; !exists || field.Order < previous {
				orders[field.Key] = field.Order
			}
		}
	}
	ordered := make([]orderedCollectionField, 0, len(orders))
	for key, order := range orders {
		ordered = append(ordered, orderedCollectionField{key: key, order: order})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].order < ordered[j].order || ordered[i].order == ordered[j].order && ordered[i].key < ordered[j].key
	})
	keys := make([]string, len(ordered))
	for index := range ordered {
		keys[index] = ordered[index].key
	}
	return keys
}

func collectionFilterSnapshot(filter agentCollectionFilterRequest) types.JSONMap {
	data, _ := json.Marshal(filter)
	snapshot := types.JSONMap{}
	_ = json.Unmarshal(data, &snapshot)
	return snapshot
}

func (h *SystemAgentCollectionHandler) auditExport(c *gin.Context, export *types.AgentCollectionExport, eventName string) {
	if h.audit == nil {
		return
	}
	details, _ := json.Marshal(gin.H{"event": eventName, "format": export.Format, "row_count": export.RowCount})
	_ = h.audit.Log(c.Request.Context(), &types.AuditLog{
		TenantID: 0, ActorUserID: export.ActorUserID, ActorRole: "system_admin",
		Action: types.AuditActionAgentCollectionExported, TargetType: "agent_collection_export", TargetID: export.ID,
		RequestPath: c.Request.URL.Path, RequestMethod: c.Request.Method,
		Outcome: types.AuditOutcomeSuccess, Details: types.JSON(details),
	})
}
