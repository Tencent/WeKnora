package service
import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/wikiaudit"
	"github.com/hibiken/asynq"
)

// wikiAuditContext is the non-content identity shared by all events emitted
// while one standardized video source is processed. It is deliberately kept
// outside the Wiki page model: page content remains owned by WeKnora.
type wikiAuditContext struct {
	Identity      wikiaudit.SourceIdentity
	RunID         string
	TaskID        string
	PendingOpID   string
	IngestEventID string
}

// wikiSourceIdentityFromKnowledge recognizes only the standardized source
// document. Ordinary manual documents continue through the native Wiki path,
// but do not produce video-scoped P2 audit events.
func wikiSourceIdentityFromKnowledge(knowledge *types.Knowledge, kbID string) (wikiaudit.SourceIdentity, bool) {
	if knowledge == nil || strings.TrimSpace(kbID) == "" {
		return wikiaudit.SourceIdentity{}, false
	}
	if actual := strings.TrimSpace(knowledge.KnowledgeBaseID); actual != "" && actual != strings.TrimSpace(kbID) {
		return wikiaudit.SourceIdentity{}, false
	}
	metadata, err := knowledge.ManualMetadata()
	if err != nil || metadata == nil || strings.TrimSpace(metadata.Content) == "" {
		return wikiaudit.SourceIdentity{}, false
	}
	identity, err := wikiaudit.ParseSourceIdentity(metadata.Content, knowledge.ID, kbID)
	if err != nil || identity.SourceKnowledgeID != strings.TrimSpace(knowledge.ID) {
		return wikiaudit.SourceIdentity{}, false
	}
	return identity, true
}

func pendingOpAuditID(id int64) string {
	if id <= 0 {
		return "not_applicable"
	}
	return strconv.FormatInt(id, 10)
}

func wikiTaskID(ctx context.Context, taskType, kbID string) string {
	if id, ok := asynq.GetTaskID(ctx); ok && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	return fmt.Sprintf("%s:%s", taskType, strings.TrimSpace(kbID))
}

// resolveWikiAuditContext first trusts the identity persisted by
// KnowledgePostProcess. The fallback reads the manual source metadata so
// legacy queued rows can still be audited after a rolling upgrade.
func (s *wikiIngestService) resolveWikiAuditContext(
	ctx context.Context,
	payload WikiIngestPayload,
	op WikiPendingOp,
) (wikiAuditContext, bool) {
	identity := wikiaudit.SourceIdentity{
		VideoID:              strings.TrimSpace(op.VideoID),
		TranscriptGeneration: strings.TrimSpace(op.TranscriptGeneration),
		SourceKnowledgeID:    strings.TrimSpace(op.SourceKnowledgeID),
		KnowledgeBaseID:      strings.TrimSpace(op.KnowledgeBaseID),
	}
	valid := identity.VideoID != "" && identity.TranscriptGeneration != "" &&
		identity.SourceKnowledgeID != "" && identity.KnowledgeBaseID != ""
	if !valid && s.knowledgeSvc != nil {
		if knowledge, err := s.knowledgeSvc.GetKnowledgeByIDOnly(ctx, op.KnowledgeID); err == nil {
			if parsed, ok := wikiSourceIdentityFromKnowledge(knowledge, payload.KnowledgeBaseID); ok {
				identity = parsed
				valid = true
			}
		}
	}
	if !valid || identity.SourceKnowledgeID != strings.TrimSpace(op.KnowledgeID) ||
		identity.KnowledgeBaseID != strings.TrimSpace(payload.KnowledgeBaseID) {
		return wikiAuditContext{}, false
	}
	runID := strings.TrimSpace(op.RunID)
	if runID == "" {
		runID = wikiaudit.RunID(identity)
	}
	return wikiAuditContext{
		Identity:    identity,
		RunID:       runID,
		TaskID:      wikiTaskID(ctx, wikiTaskType, payload.KnowledgeBaseID),
		PendingOpID: pendingOpAuditID(op.dbID),
	}, true
}

func (a wikiAuditContext) event(taskType, op, phase, status string) wikiaudit.Event {
	event := wikiaudit.New(a.Identity, a.TaskID, taskType, op, a.PendingOpID, phase, status)
	event.RunID = a.RunID
	return event
}

// emitWikiAudit validates before logging. Invalid events are rejected instead
// of becoming misleading P2 evidence; the log contains fields individually so
// aggregators can filter without parsing page content or a nested payload.
func emitWikiAudit(ctx context.Context, event wikiaudit.Event) string {
	raw, err := event.JSON()
	if err != nil {
		logger.GetLogger(ctx).WithField("audit_error", err.Error()).Error("wiki audit event rejected")
		return ""
	}
	fields := logger.Fields{"event": raw}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		for key, value := range decoded {
			fields[key] = value
		}
	}
	logger.GetLogger(ctx).WithFields(fields).Info("wiki audit event")
	return event.EventID
}

func auditRefs(contexts []wikiAuditContext) []wikiFinalizeAuditRef {
	refs := make([]wikiFinalizeAuditRef, 0, len(contexts))
	seen := make(map[string]struct{}, len(contexts))
	for _, audit := range contexts {
		if strings.TrimSpace(audit.IngestEventID) == "" {
			continue
		}
		if _, ok := seen[audit.RunID+"|"+audit.IngestEventID]; ok {
			continue
		}
		seen[audit.RunID+"|"+audit.IngestEventID] = struct{}{}
		refs = append(refs, wikiFinalizeAuditRef{
			RunID:                audit.RunID,
			VideoID:              audit.Identity.VideoID,
			TranscriptGeneration: audit.Identity.TranscriptGeneration,
			SourceKnowledgeID:    audit.Identity.SourceKnowledgeID,
			KnowledgeBaseID:      audit.Identity.KnowledgeBaseID,
			TaskID:               audit.TaskID,
			PendingOpID:          audit.PendingOpID,
			IngestEventID:        audit.IngestEventID,
		})
	}
	return refs
}
