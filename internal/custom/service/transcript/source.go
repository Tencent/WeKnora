package transcript

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/wikiaudit"
)

const (
	SourceStatusCreating = "creating"
	SourceStatusCreated  = "created"
	SourceStatusFailed   = "failed"

	SourceKnowledgeBaseMissing = "knowledge_base_routing:knowledge_kb_missing"
	SourceOwnershipMismatch    = "knowledge_base_routing:source_ownership_mismatch"
)

// KnowledgeGateway is the small WeKnora surface needed to create and
// reconcile a source document. Keeping it narrow makes the workflow testable
// without calling a real knowledge base.
type KnowledgeGateway interface {
	FindManualKnowledgeByTitle(context.Context, string, string) (*weknora.ManualKnowledgeResult, error)
	CreateManualKnowledge(context.Context, string, weknora.ManualKnowledgeInput) (weknora.ManualKnowledgeResult, error)
	GetKnowledge(context.Context, string) (weknora.ManualKnowledgeResult, error)
}

type SourceWriter struct {
	DB      *gorm.DB
	Gateway KnowledgeGateway
	KBID    string
	mu      sync.Mutex
}

func NewSourceWriter(db *gorm.DB, client *weknora.Client) *SourceWriter {
	writer := &SourceWriter{DB: db}
	if client != nil {
		writer.Gateway = client
		writer.KBID = client.KBID()
	}
	return writer
}

type SourceInput struct {
	Document FullVideoDocument
	TaskID   string
}

type SourceResult struct {
	VideoID              string
	TranscriptGeneration string
	KnowledgeID          string
	KnowledgeBaseID      string
	ContentHash          string
	Action               string
	AuditEventID         string
}

// Ensure creates or reuses the source document for one immutable transcript
// generation. The local checkpoint is written only after WeKnora accepts the
// document and reports a non-failed parse state.
func (w *SourceWriter) Ensure(ctx context.Context, input SourceInput) (SourceResult, error) {
	if w == nil || w.DB == nil || w.Gateway == nil {
		return SourceResult{}, fmt.Errorf("transcript source writer dependencies are not configured")
	}
	if strings.TrimSpace(w.KBID) == "" {
		return SourceResult{}, fmt.Errorf(SourceKnowledgeBaseMissing)
	}
	doc := input.Document
	documentJSON, err := doc.JSON()
	if err != nil {
		return SourceResult{}, fmt.Errorf("validate full video document: %w", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(documentJSON)))
	videoID := strings.TrimSpace(doc.VideoID)
	generation := strings.TrimSpace(doc.TranscriptGeneration)
	result := SourceResult{VideoID: videoID, TranscriptGeneration: generation, KnowledgeBaseID: w.KBID, ContentHash: hash}

	// A process-local lock avoids two workers racing between reconciliation and
	// creation. The database unique key remains the cross-instance guard.
	w.mu.Lock()
	defer w.mu.Unlock()

	var binding model.VideoTranscriptSource
	err = w.DB.WithContext(ctx).Where(
		"video_id = ? AND transcript_generation = ? AND knowledge_base_id = ?",
		videoID, generation, w.KBID,
	).First(&binding).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return result, fmt.Errorf("load transcript source binding: %w", err)
	}
	if err == nil {
		if binding.ContentHash != hash {
			return result, fmt.Errorf("transcript source content hash mismatch for generation %s", generation)
		}
		if binding.Status == SourceStatusCreated && strings.TrimSpace(binding.KnowledgeID) != "" {
			result.KnowledgeID = binding.KnowledgeID
			result.Action = "reused"
			return logSourceAudit(result, input.TaskID), nil
		}
	} else {
		binding = model.VideoTranscriptSource{
			ID: uuid.NewString(), VideoID: videoID, TranscriptGeneration: generation,
			KnowledgeBaseID: w.KBID, ContentHash: hash, Status: SourceStatusCreating,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
		createResult := w.DB.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "video_id"}, {Name: "transcript_generation"}, {Name: "knowledge_base_id"},
			},
			DoNothing: true,
		}).Create(&binding)
		if createResult.Error != nil {
			return result, fmt.Errorf("create transcript source binding: %w", createResult.Error)
		}
		if createResult.RowsAffected == 0 {
			if loadErr := w.DB.WithContext(ctx).Where(
				"video_id = ? AND transcript_generation = ? AND knowledge_base_id = ?",
				videoID, generation, w.KBID,
			).First(&binding).Error; loadErr != nil {
				return result, fmt.Errorf("load concurrent transcript source binding: %w", loadErr)
			}
			if binding.ContentHash != hash {
				return result, fmt.Errorf("transcript source content hash mismatch for generation %s", generation)
			}
			if binding.Status == SourceStatusCreated && strings.TrimSpace(binding.KnowledgeID) != "" {
				result.KnowledgeID = binding.KnowledgeID
				result.Action = "reused"
				return logSourceAudit(result, input.TaskID), nil
			}
		}
	}

	if updateErr := w.DB.WithContext(ctx).Model(&model.VideoTranscriptSource{}).Where("id = ?", binding.ID).Updates(map[string]any{
		"status": SourceStatusCreating, "error_message": "", "updated_at": time.Now().UTC(),
	}).Error; updateErr != nil {
		return result, fmt.Errorf("mark transcript source creating: %w", updateErr)
	}

	title := SourceTitle(videoID, generation)
	knowledge, findErr := w.Gateway.FindManualKnowledgeByTitle(ctx, w.KBID, title)
	if findErr != nil {
		return result, w.fail(binding.ID, result, fmt.Errorf("reconcile transcript source: %w", findErr))
	}
	action := "created"
	if knowledge == nil {
		value, createErr := w.Gateway.CreateManualKnowledge(ctx, w.KBID, weknora.ManualKnowledgeInput{
			Title: title, Content: SourceContent(doc, documentJSON, hash), Status: "publish", Channel: "api",
		})
		if createErr != nil {
			return result, w.fail(binding.ID, result, fmt.Errorf("create transcript source: %w", createErr))
		}
		knowledge = &value
	} else {
		action = "reconciled"
	}
	if knowledge == nil || strings.TrimSpace(knowledge.ID) == "" {
		return result, w.fail(binding.ID, result, fmt.Errorf("transcript source returned empty knowledge id"))
	}
	if err := validateSourceKnowledgeBase(knowledge.KnowledgeBaseID, w.KBID); err != nil {
		return result, w.fail(binding.ID, result, err)
	}
	parsed, getErr := w.Gateway.GetKnowledge(ctx, knowledge.ID)
	if getErr != nil {
		return result, w.fail(binding.ID, result, fmt.Errorf("verify transcript source %s: %w", knowledge.ID, getErr))
	}
	if strings.EqualFold(strings.TrimSpace(parsed.ParseStatus), "failed") {
		message := strings.TrimSpace(parsed.ErrorMessage)
		if message == "" {
			message = "WeKnora source document parse failed"
		}
		return result, w.fail(binding.ID, result, fmt.Errorf("verify transcript source: %s", message))
	}
	if err := validateSourceKnowledgeBase(parsed.KnowledgeBaseID, w.KBID); err != nil {
		return result, w.fail(binding.ID, result, err)
	}
	if err := w.DB.WithContext(ctx).Model(&model.VideoTranscriptSource{}).Where("id = ?", binding.ID).Updates(map[string]any{
		"knowledge_id": knowledge.ID, "content_hash": hash, "status": SourceStatusCreated,
		"error_message": "", "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return result, fmt.Errorf("save transcript source binding: %w", err)
	}
	slog.Info("transcript source document ready", "video_id", videoID, "transcript_generation", generation,
		"knowledge_id", knowledge.ID, "knowledge_base_id", w.KBID, "content_hash", hash, "action", action)
	result.KnowledgeID = knowledge.ID
	result.Action = action
	return logSourceAudit(result, input.TaskID), nil
}

func logSourceAudit(result SourceResult, inputTaskID string) SourceResult {
	taskID := strings.TrimSpace(inputTaskID)
	if taskID == "" {
		taskID = "source:" + wikiaudit.RunID(wikiaudit.SourceIdentity{
			VideoID: result.VideoID, TranscriptGeneration: result.TranscriptGeneration,
			SourceKnowledgeID: result.KnowledgeID, KnowledgeBaseID: result.KnowledgeBaseID,
		})
	}
	auditEvent := wikiaudit.New(wikiaudit.SourceIdentity{
		VideoID: result.VideoID, TranscriptGeneration: result.TranscriptGeneration,
		SourceKnowledgeID: result.KnowledgeID, KnowledgeBaseID: result.KnowledgeBaseID,
	}, taskID, "source:ingest", result.Action, "not_applicable", "source_ingest", "succeeded")
	if auditJSON, auditErr := auditEvent.JSON(); auditErr == nil {
		result.AuditEventID = auditEvent.EventID
		slog.Info("wiki audit event", "event", auditJSON)
	} else {
		slog.Error("wiki audit event rejected", "error", auditErr)
	}
	return result
}

func validateSourceKnowledgeBase(actual, expected string) error {
	actual = strings.TrimSpace(actual)
	if actual != "" && actual != strings.TrimSpace(expected) {
		return fmt.Errorf("%s: expected=%s actual=%s", SourceOwnershipMismatch, expected, actual)
	}
	return nil
}

func (w *SourceWriter) fail(bindingID string, result SourceResult, sourceErr error) error {
	if err := w.DB.Model(&model.VideoTranscriptSource{}).Where("id = ?", bindingID).Updates(map[string]any{
		"status": SourceStatusFailed, "error_message": sourceErr.Error(), "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		return fmt.Errorf("%v; save source failure: %w", sourceErr, err)
	}
	return sourceErr
}

func SourceTitle(videoID, generation string) string {
	return "video-transcript-source/" + strings.TrimSpace(videoID) + "/" + strings.TrimSpace(generation)
}

func SourceContent(doc FullVideoDocument, documentJSON, hash string) string {
	return fmt.Sprintf("---\ntype: video_transcript_source\nsource_video_id: %s\ntranscript_generation: %s\nschema_version: %d\ncontent_sha256: %s\n---\n\n# %s\n\n```json\n%s\n```\n", doc.VideoID, doc.TranscriptGeneration, doc.SchemaVersion, hash, doc.Title, documentJSON)
}
