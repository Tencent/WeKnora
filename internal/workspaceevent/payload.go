package workspaceevent

import (
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

func NewEventID() string {
	return "evt_" + stringsNoDash(uuid.NewString())
}

func NewDeliveryID() string {
	return "dlv_" + stringsNoDash(uuid.NewString())
}

func NewDeleteBatchID() string {
	return "bdel_" + stringsNoDash(uuid.NewString())
}

func stringsNoDash(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '-' {
			out = append(out, s[i])
		}
	}
	return string(out)
}

func ActorAndRequest(ctxActor, ctxRequest func() (string, bool)) (actor, request string) {
	if ctxActor != nil {
		if v, ok := ctxActor(); ok {
			actor = v
		}
	}
	if ctxRequest != nil {
		if v, ok := ctxRequest(); ok {
			request = v
		}
	}
	return actor, request
}

func BuildEnvelope(ev types.WorkspaceEvent, now time.Time) (types.WorkspaceWebhookEnvelope, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id := NewEventID()
	data, err := json.Marshal(ev.Data)
	if err != nil {
		return types.WorkspaceWebhookEnvelope{}, err
	}
	return types.WorkspaceWebhookEnvelope{
		SpecVersion: types.WorkspaceWebhookSpecVersion,
		ID:          id,
		Type:        ev.Type,
		Time:        now.UTC().Format(time.RFC3339),
		TenantID:    ev.TenantID,
		ActorUserID: ev.ActorUserID,
		RequestID:   ev.RequestID,
		Data:        data,
	}, nil
}

func KnowledgeDownloadPlaceholder(knowledgeType, knowledgeID, filePath string, deleted bool) types.WebhookDownloadInfo {
	info := types.WebhookDownloadInfo{
		HTTPMethod:   "GET",
		TicketHeader: types.WebhookDownloadTicketHeader,
	}
	if deleted {
		info.Reason = "deleted"
		return info
	}
	if types.KnowledgeNeedsDownloadTicket(knowledgeType, filePath, false) {
		info.Available = true
		info.Path = types.WebhookDownloadPathPrefix + knowledgeID
		return info
	}
	info.Reason = "not_a_file"
	return info
}

func KnowledgeDataFrom(k *types.Knowledge, deleted bool) types.WebhookKnowledgeData {
	if k == nil {
		return types.WebhookKnowledgeData{
			Resource:     "knowledge",
			KnowledgeIDs: []string{},
			Download:     types.WebhookDownloadInfo{HTTPMethod: "GET", TicketHeader: types.WebhookDownloadTicketHeader, Reason: "deleted"},
			Deleted:      deleted,
		}
	}
	return types.WebhookKnowledgeData{
		Resource:        "knowledge",
		KnowledgeID:     k.ID,
		KnowledgeBaseID: k.KnowledgeBaseID,
		OwnerTenantID:   k.TenantID,
		Title:           k.Title,
		KnowledgeType:   k.Type,
		Source:          k.Source,
		FileType:        k.FileType,
		ParseStatus:     k.ParseStatus,
		EnableStatus:    k.EnableStatus,
		FolderPath:      k.FolderPath,
		ErrorMessage:    k.ErrorMessage,
		Deleted:         deleted,
		KnowledgeIDs:    []string{},
		Download:        KnowledgeDownloadPlaceholder(k.Type, k.ID, k.FilePath, deleted),
	}
}

func BatchDeletedData(tenantID uint64, kbID string, ids []string, total, batchIndex, batchTotal int, batchID string) types.WebhookKnowledgeData {
	if ids == nil {
		ids = []string{}
	}
	return types.WebhookKnowledgeData{
		Resource:        "knowledge",
		KnowledgeBaseID: kbID,
		OwnerTenantID:   tenantID,
		Deleted:         true,
		KnowledgeIDs:    ids,
		Count:           len(ids),
		TotalCount:      total,
		BatchIndex:      batchIndex,
		BatchTotal:      batchTotal,
		DeleteBatchID:   batchID,
		Truncated:       false,
		Download: types.WebhookDownloadInfo{
			Reason:       "deleted",
			HTTPMethod:   "GET",
			TicketHeader: types.WebhookDownloadTicketHeader,
		},
	}
}

func IsKnownEventType(t string) bool {
	for _, item := range types.P0WebhookEventTypes() {
		if item == t {
			return true
		}
	}
	return t == types.EventWebhookTest
}
