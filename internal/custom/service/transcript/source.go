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
	"github.com/Tencent/WeKnora/internal/custom/service/evidence"
	"github.com/Tencent/WeKnora/internal/types"
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
	UpdateManualKnowledge(context.Context, string, weknora.ManualKnowledgeInput) (weknora.ManualKnowledgeResult, error)
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
			if repairErr := w.repairLegacySpeakerIdentity(ctx, binding, doc, documentJSON, hash); repairErr != nil {
				return result, fmt.Errorf("transcript source content hash mismatch for generation %s: %w", generation, repairErr)
			}
			result.KnowledgeID = binding.KnowledgeID
			result.Action = "repaired"
			return logSourceAudit(result, input.TaskID), nil
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

	title := SourceTitle(doc.Title)
	knowledge, findErr := w.Gateway.FindManualKnowledgeByTitle(ctx, w.KBID, title)
	if findErr != nil {
		return result, w.fail(binding.ID, result, fmt.Errorf("reconcile transcript source: %w", findErr))
	}
	action := "created"
	var verified *weknora.ManualKnowledgeResult
	if knowledge != nil {
		if err := validateSourceKnowledgeBase(knowledge.KnowledgeBaseID, w.KBID); err != nil {
			return result, w.fail(binding.ID, result, err)
		}
		candidate, getErr := w.Gateway.GetKnowledge(ctx, knowledge.ID)
		if getErr != nil {
			return result, w.fail(binding.ID, result, fmt.Errorf("verify transcript source %s: %w", knowledge.ID, getErr))
		}
		if err := validateSourceKnowledgeBase(candidate.KnowledgeBaseID, w.KBID); err != nil {
			return result, w.fail(binding.ID, result, err)
		}
		if sourceDocumentMatches(candidate.Content, videoID, generation, doc.DurationSeconds, hash) {
			knowledge = &candidate
			verified = &candidate
			action = "reconciled"
		} else {
			// Display titles are intentionally human-readable and therefore not
			// unique. Never reuse a same-title document from another video or
			// transcript generation.
			knowledge = nil
		}
	}
	if knowledge == nil {
		wikiEnabled := false
		value, createErr := w.Gateway.CreateManualKnowledge(ctx, w.KBID, weknora.ManualKnowledgeInput{
			Title: title, Content: SourceContent(doc, documentJSON, hash), Status: "publish", Channel: "api",
			ProcessConfig: &types.KnowledgeProcessOverrides{WikiEnabled: &wikiEnabled},
		})
		if createErr != nil {
			return result, w.fail(binding.ID, result, fmt.Errorf("create transcript source: %w", createErr))
		}
		knowledge = &value
	}
	if knowledge == nil || strings.TrimSpace(knowledge.ID) == "" {
		return result, w.fail(binding.ID, result, fmt.Errorf("transcript source returned empty knowledge id"))
	}
	if err := validateSourceKnowledgeBase(knowledge.KnowledgeBaseID, w.KBID); err != nil {
		return result, w.fail(binding.ID, result, err)
	}
	if verified == nil {
		parsed, getErr := w.Gateway.GetKnowledge(ctx, knowledge.ID)
		if getErr != nil {
			return result, w.fail(binding.ID, result, fmt.Errorf("verify transcript source %s: %w", knowledge.ID, getErr))
		}
		verified = &parsed
	}
	if strings.EqualFold(strings.TrimSpace(verified.ParseStatus), "failed") {
		message := strings.TrimSpace(verified.ErrorMessage)
		if message == "" {
			message = "WeKnora source document parse failed"
		}
		return result, w.fail(binding.ID, result, fmt.Errorf("verify transcript source: %s", message))
	}
	if err := validateSourceKnowledgeBase(verified.KnowledgeBaseID, w.KBID); err != nil {
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

func (w *SourceWriter) repairLegacySpeakerIdentity(
	ctx context.Context,
	binding model.VideoTranscriptSource,
	doc FullVideoDocument,
	documentJSON string,
	hash string,
) error {
	if binding.Status != SourceStatusCreated || strings.TrimSpace(binding.KnowledgeID) == "" {
		return fmt.Errorf("legacy source binding is not ready")
	}
	existing, err := w.Gateway.GetKnowledge(ctx, binding.KnowledgeID)
	if err != nil {
		return fmt.Errorf("read legacy source knowledge: %w", err)
	}
	if err := validateSourceKnowledgeBase(existing.KnowledgeBaseID, w.KBID); err != nil {
		return err
	}
	legacy, err := ParseSourceContent(existing.Content)
	if err != nil {
		return fmt.Errorf("parse legacy source knowledge: %w", err)
	}
	existingJSON, err := legacy.JSON()
	if err != nil {
		return fmt.Errorf("encode legacy source knowledge: %w", err)
	}
	if existingJSON != documentJSON {
		normalized, changed, normalizeErr := normalizeEvidenceIdentity(legacy)
		if normalizeErr != nil {
			return fmt.Errorf("normalize legacy source knowledge: %w", normalizeErr)
		}
		normalizedJSON, encodeErr := normalized.JSON()
		if encodeErr != nil {
			return fmt.Errorf("encode normalized legacy source knowledge: %w", encodeErr)
		}
		if !changed || normalizedJSON != documentJSON {
			return fmt.Errorf("source differs beyond canonical missing-speaker normalization")
		}
		wikiEnabled := false
		updated, updateErr := w.Gateway.UpdateManualKnowledge(ctx, binding.KnowledgeID, weknora.ManualKnowledgeInput{
			Title: SourceTitle(doc.Title), Content: SourceContent(doc, documentJSON, hash),
			Status: "publish", Channel: "api", ProcessConfig: &types.KnowledgeProcessOverrides{WikiEnabled: &wikiEnabled},
		})
		if updateErr != nil {
			return fmt.Errorf("update legacy source knowledge: %w", updateErr)
		}
		if updated.ID != binding.KnowledgeID {
			return fmt.Errorf("updated source knowledge identity changed")
		}
		verified, verifyErr := w.Gateway.GetKnowledge(ctx, binding.KnowledgeID)
		if verifyErr != nil {
			return fmt.Errorf("verify repaired source knowledge: %w", verifyErr)
		}
		if err := validateSourceKnowledgeBase(verified.KnowledgeBaseID, w.KBID); err != nil {
			return err
		}
		verifiedDoc, validateErr := ValidateSourceContent(verified.Content, doc.VideoID, doc.TranscriptGeneration, doc.DurationSeconds)
		if validateErr != nil {
			return fmt.Errorf("validate repaired source knowledge: %w", validateErr)
		}
		verifiedJSON, encodeErr := verifiedDoc.JSON()
		if encodeErr != nil || verifiedJSON != documentJSON {
			return fmt.Errorf("repaired source knowledge does not match expected document")
		}
	}
	update := w.DB.WithContext(ctx).Model(&model.VideoTranscriptSource{}).
		Where("id = ? AND content_hash = ?", binding.ID, binding.ContentHash).
		Updates(map[string]any{
			"content_hash": hash, "status": SourceStatusCreated, "error_message": "", "updated_at": time.Now().UTC(),
		})
	if update.Error != nil {
		return fmt.Errorf("save repaired source binding: %w", update.Error)
	}
	if update.RowsAffected != 1 {
		return fmt.Errorf("source binding changed concurrently during repair")
	}
	slog.Info("transcript source evidence identity repaired", "video_id", doc.VideoID,
		"transcript_generation", doc.TranscriptGeneration, "knowledge_id", binding.KnowledgeID,
		"knowledge_base_id", w.KBID, "content_hash", hash)
	return nil
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

func SourceTitle(videoTitle string) string {
	return strings.TrimSpace(videoTitle)
}

func sourceDocumentMatches(content, videoID, generation string, durationSeconds int, expectedHash string) bool {
	doc, err := ValidateSourceContent(content, videoID, generation, durationSeconds)
	if err != nil {
		return false
	}
	documentJSON, err := doc.JSON()
	if err != nil {
		return false
	}
	actualHash := fmt.Sprintf("%x", sha256.Sum256([]byte(documentJSON)))
	return actualHash == expectedHash
}

// legacySourceDocumentEquivalent recognizes the only historical source
// document drift that is safe to repair in place: unknown speakers were
// serialized as an empty value when the source document was created, while
// the active evidence manifest now uses the canonical "0" fallback. Every
// other source field, including text, timing and source IDs, must match.
func legacySourceDocumentEquivalent(legacy, current FullVideoDocument) bool {
	if legacy.SchemaVersion != current.SchemaVersion ||
		legacy.VideoID != current.VideoID ||
		legacy.TranscriptGeneration != current.TranscriptGeneration ||
		legacy.Title != current.Title ||
		legacy.DurationSeconds != current.DurationSeconds ||
		legacy.ContinuousText != current.ContinuousText ||
		len(legacy.Chapters) != len(current.Chapters) {
		return false
	}
	for chapterIndex := range current.Chapters {
		oldChapter, newChapter := legacy.Chapters[chapterIndex], current.Chapters[chapterIndex]
		if oldChapter.Index != newChapter.Index ||
			oldChapter.Title != newChapter.Title ||
			oldChapter.StartMs != newChapter.StartMs ||
			oldChapter.EndMs != newChapter.EndMs ||
			oldChapter.ContinuousText != newChapter.ContinuousText ||
			len(oldChapter.Paragraphs) != len(newChapter.Paragraphs) {
			return false
		}
		for paragraphIndex := range newChapter.Paragraphs {
			oldParagraph, newParagraph := oldChapter.Paragraphs[paragraphIndex], newChapter.Paragraphs[paragraphIndex]
			if strings.TrimSpace(oldParagraph.SpeakerID) != "" || evidenceSpeakerID(newParagraph.SpeakerID) != "0" ||
				oldParagraph.ParagraphID != newParagraph.ParagraphID ||
				oldParagraph.Index != newParagraph.Index ||
				oldParagraph.Text != newParagraph.Text ||
				oldParagraph.StartMs != newParagraph.StartMs ||
				oldParagraph.EndMs != newParagraph.EndMs ||
				len(oldParagraph.TimeMarks) != len(newParagraph.TimeMarks) ||
				len(oldParagraph.SourceSentenceIDs) != len(newParagraph.SourceSentenceIDs) ||
				len(oldParagraph.EvidenceSentenceIDs) != len(newParagraph.EvidenceSentenceIDs) {
				return false
			}
			for index := range newParagraph.TimeMarks {
				oldMark, newMark := oldParagraph.TimeMarks[index], newParagraph.TimeMarks[index]
				if oldParagraph.SourceSentenceIDs[index] != newParagraph.SourceSentenceIDs[index] ||
					oldParagraph.EvidenceSentenceIDs[index] != oldMark.EvidenceSentenceID ||
					newParagraph.EvidenceSentenceIDs[index] != newMark.EvidenceSentenceID ||
					oldMark.SourceSentenceID != newMark.SourceSentenceID ||
					oldMark.Text != newMark.Text ||
					oldMark.StartMs != newMark.StartMs ||
					oldMark.EndMs != newMark.EndMs {
					return false
				}
				legacyEvidence, legacyErr := evidence.BuildSentence(evidence.Input{
					VideoID: legacy.VideoID, TranscriptGeneration: legacy.TranscriptGeneration,
					Ordinal:          evidenceOrdinal(legacy, chapterIndex, paragraphIndex, index),
					SourceSentenceID: oldMark.SourceSentenceID, Text: oldMark.Text,
					SpeakerID: "", StartMs: oldMark.StartMs, EndMs: oldMark.EndMs,
				})
				currentEvidence, currentErr := evidence.BuildSentence(evidence.Input{
					VideoID: current.VideoID, TranscriptGeneration: current.TranscriptGeneration,
					Ordinal:          evidenceOrdinal(current, chapterIndex, paragraphIndex, index),
					SourceSentenceID: newMark.SourceSentenceID, Text: newMark.Text,
					SpeakerID: "0", StartMs: newMark.StartMs, EndMs: newMark.EndMs,
				})
				if legacyErr != nil || currentErr != nil ||
					oldMark.EvidenceSentenceID != legacyEvidence.ID ||
					newMark.EvidenceSentenceID != currentEvidence.ID {
					return false
				}
			}
		}
	}
	return true
}

func evidenceOrdinal(doc FullVideoDocument, chapterIndex, paragraphIndex, markIndex int) int {
	ordinal := 0
	for chapter := 0; chapter < chapterIndex; chapter++ {
		for _, paragraph := range doc.Chapters[chapter].Paragraphs {
			ordinal += len(paragraph.TimeMarks)
		}
	}
	for paragraph := 0; paragraph < paragraphIndex; paragraph++ {
		ordinal += len(doc.Chapters[chapterIndex].Paragraphs[paragraph].TimeMarks)
	}
	return ordinal + markIndex
}

func SourceContent(doc FullVideoDocument, documentJSON, hash string) string {
	return fmt.Sprintf("---\ntype: video_transcript_source\nsource_video_id: %s\ntranscript_generation: %s\nschema_version: %d\ncontent_sha256: %s\n---\n\n# %s\n\n```json\n%s\n```\n", doc.VideoID, doc.TranscriptGeneration, doc.SchemaVersion, hash, doc.Title, documentJSON)
}
