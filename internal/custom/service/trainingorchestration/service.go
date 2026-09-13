package trainingorchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

const (
	JobQueued    = "queued"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"

	StageCollecting    = "collecting"
	StagePlanning      = "planning"
	StageMaterializing = "materializing"
	StageGenerating    = "generating"
	StageAssembling    = "assembling"
	StagePublishing    = "publishing"

	WarningEvidenceRetrievalDegraded = "evidence_retrieval_degraded"
)

const warningEvidenceRetrievalDegradedMessage = "证据召回降级，已使用规划证据完成生成。"

type InputCollector interface {
	Collect(context.Context) (InputPackage, error)
}
type CatalogCollector interface {
	CollectCatalog(context.Context) (CatalogSnapshot, error)
}
type ProjectionGenerator interface {
	Generate(context.Context, InputPackage) (ProjectionDocument, error)
}
type StageFourRunner interface {
	Run(context.Context, CatalogSnapshot) (ProjectionDocument, error)
}
type IncrementalStageFourRunner interface {
	RunIncremental(context.Context, CatalogSnapshot, ProjectionDocument, []string) (ProjectionDocument, error)
}
type ProjectionWiki interface {
	EnsurePage(context.Context, string, weknora.WikiPageWrite) (*weknora.WikiPage, error)
	GetPageByID(context.Context, string, string) (*weknora.WikiPage, error)
}

type projectionOverwriteWiki interface {
	UpsertPage(context.Context, string, weknora.WikiPageWrite) (*weknora.WikiPage, error)
}

type projectionSlugReader interface {
	GetPage(context.Context, string, string) (*weknora.WikiPage, error)
}

type Service struct {
	DB               *gorm.DB
	Collector        InputCollector
	Generator        ProjectionGenerator
	CatalogCollector CatalogCollector
	StageFour        StageFourRunner
	Wiki             ProjectionWiki
	KnowledgeBaseID  string
	OwnerScopeID     string
	Model            string
	PromptVersion    string
	RunTimeout       time.Duration
	CompletionGate   *CompletionGate
	mu               sync.Mutex
	running          map[string]struct{}
}

func (s *Service) Start(ctx context.Context) (model.TrainingOrchestrationJob, error) {
	return s.start(ctx, false)
}

// StartWithOptions preserves fingerprint reuse by default while allowing an
// explicit user-triggered overwrite of the currently published result.
func (s *Service) StartWithOptions(ctx context.Context, overwrite bool) (model.TrainingOrchestrationJob, error) {
	return s.start(ctx, overwrite)
}

func (s *Service) start(ctx context.Context, overwrite bool) (model.TrainingOrchestrationJob, error) {
	if err := s.validate(); err != nil {
		return model.TrainingOrchestrationJob{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var active model.TrainingOrchestrationJob
	err := s.DB.WithContext(ctx).Where("owner_scope_id = ? AND status IN ?", s.OwnerScopeID, []string{JobQueued, JobRunning}).Order("created_at DESC").First(&active).Error
	if err == nil {
		if _, owned := s.running[active.ID]; owned {
			return active, nil
		}
		now := time.Now().UTC()
		if updateErr := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", active.ID).Updates(map[string]any{"status": JobFailed, "error_code": "process_interrupted", "error_message": "服务进程已中断，请重新生成", "finished_at": now, "updated_at": now}).Error; updateErr != nil {
			return model.TrainingOrchestrationJob{}, fmt.Errorf("recover interrupted training orchestration job: %w", updateErr)
		}
		err = gorm.ErrRecordNotFound
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.TrainingOrchestrationJob{}, fmt.Errorf("find active training orchestration job: %w", err)
	}
	if s.CompletionGate != nil {
		s.CompletionGate.Reset()
	}
	job := model.TrainingOrchestrationJob{ID: uuid.NewString(), OwnerScopeID: s.OwnerScopeID, Status: JobQueued, Stage: StageCollecting, Progress: 0, Model: s.Model, PromptVersion: s.PromptVersion, RefreshMode: RefreshModeFull}
	if err := s.DB.WithContext(ctx).Create(&job).Error; err != nil {
		return model.TrainingOrchestrationJob{}, fmt.Errorf("create training orchestration job: %w", err)
	}
	if s.running == nil {
		s.running = make(map[string]struct{})
	}
	s.running[job.ID] = struct{}{}
	go s.run(job.ID, overwrite)
	return job, nil
}

func (s *Service) GetJob(ctx context.Context, id string) (model.TrainingOrchestrationJob, error) {
	var job model.TrainingOrchestrationJob
	if err := s.DB.WithContext(ctx).Where("id = ? AND owner_scope_id = ?", strings.TrimSpace(id), s.OwnerScopeID).First(&job).Error; err != nil {
		return job, err
	}
	return job, nil
}

func (s *Service) GetCurrent(ctx context.Context) (*ProjectionDocument, *model.TrainingOrchestrationCurrent, error) {
	if err := s.validate(); err != nil {
		return nil, nil, err
	}
	var current model.TrainingOrchestrationCurrent
	if err := s.DB.WithContext(ctx).Where("owner_scope_id = ?", s.OwnerScopeID).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read current training orchestration: %w", err)
	}
	page, err := s.Wiki.GetPageByID(ctx, s.KnowledgeBaseID, current.ResultWikiPageID)
	if err != nil {
		return nil, &current, fmt.Errorf("read training orchestration Wiki: %w", err)
	}
	if page == nil {
		return nil, &current, fmt.Errorf("current training orchestration Wiki does not exist")
	}
	var doc ProjectionDocument
	if err := json.Unmarshal([]byte(page.Content), &doc); err != nil {
		return nil, &current, fmt.Errorf("decode current training orchestration Wiki: %w", err)
	}
	if doc.TrainingPathProjection.OwnerScopeID != s.OwnerScopeID || doc.TrainingPathProjection.SourceFingerprint != current.SourceFingerprint || doc.TrainingPathProjection.SchemaVersion != SchemaVersion {
		return nil, &current, fmt.Errorf("current training orchestration identity is invalid")
	}
	if s.Collector != nil {
		if input, collectErr := s.Collector.Collect(ctx); collectErr == nil {
			fingerprint, fingerprintErr := SourceFingerprint(input, s.Model, s.PromptVersion)
			if fingerprintErr == nil && fingerprint == doc.TrainingPathProjection.SourceFingerprint {
				bindProjectionKnowledgeFields(&doc.TrainingPathProjection, input)
				doc.TrainingPathProjection.Statistics = buildStatistics(input, doc.TrainingPathProjection.TopicClusters, selectedVideoIDs(doc.TrainingPathProjection.TopicClusters), allProjectionEvidence(doc.TrainingPathProjection.TopicClusters))
			} else {
				// Keep the last validated publication readable while a newer source
				// is waiting for refresh. The refresh job still rechecks the source
				// before publishing, so this fallback never promotes stale content.
				normalizeProjectionKnowledgeFields(&doc.TrainingPathProjection)
			}
		}
	}
	ensureGapAnalyses(&doc.TrainingPathProjection)
	if err := validateKnowledgeCompatibilityFields(doc); err != nil {
		return nil, &current, fmt.Errorf("current training orchestration content is invalid: %w", err)
	}
	for index, cluster := range doc.TrainingPathProjection.TopicClusters {
		if err := validateGapAnalysis(cluster.GapAnalysis); err != nil {
			return nil, &current, fmt.Errorf("current training orchestration topic %d gap analysis is invalid: %w", index+1, err)
		}
	}
	return &doc, &current, nil
}

func (s *Service) run(jobID string, overwrite bool) {
	defer func() {
		s.mu.Lock()
		delete(s.running, jobID)
		s.mu.Unlock()
	}()
	timeout := s.RunTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	now := time.Now().UTC()
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ? AND status = ?", jobID, JobQueued).Updates(map[string]any{"status": JobRunning, "stage": StageCollecting, "progress": 5, "started_at": now, "updated_at": now}).Error; err != nil {
		return
	}
	if s.CatalogCollector != nil && s.StageFour != nil {
		s.runStageFour(ctx, jobID, overwrite)
		return
	}
	input, err := s.Collector.Collect(ctx)
	if err != nil {
		s.fail(jobID, "input_collection_failed", err)
		return
	}
	fingerprint, err := SourceFingerprint(input, s.Model, s.PromptVersion)
	if err != nil {
		s.fail(jobID, "fingerprint_failed", err)
		return
	}
	// The catalog is assembled from records that may still be changing while
	// background video processing runs. Confirm the same source immediately
	// before spending model budget; the later post-generation recheck remains
	// the final publication guard.
	stableInput, err := s.Collector.Collect(ctx)
	if err != nil {
		s.fail(jobID, "source_stability_check_failed", err)
		return
	}
	stableFingerprint, err := SourceFingerprint(stableInput, s.Model, s.PromptVersion)
	if err != nil {
		s.fail(jobID, "fingerprint_failed", err)
		return
	}
	if stableFingerprint != fingerprint {
		s.fail(jobID, "source_not_stable", fmt.Errorf("training orchestration sources changed before generation started"))
		return
	}
	currentRefs := inputReferenceSnapshot(stableInput)
	inputRefs, _ := json.Marshal(currentRefs)
	_ = s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"progress": 25, "source_fingerprint": fingerprint, "input_references": string(inputRefs)}).Error
	decision := RefreshDecision{Mode: RefreshModeFull}
	var previous *ProjectionDocument
	if !overwrite {
		var decisionErr error
		decision, previous, decisionErr = s.refreshDecision(ctx, currentRefs)
		if decisionErr != nil {
			s.fail(jobID, "refresh_decision_failed", decisionErr)
			return
		}
		s.recordRefreshDecision(ctx, jobID, decision)
		if decision.isLocal() && previous != nil {
			if err := s.publishLocalRefresh(ctx, jobID, previous, stableInput, fingerprint, decision, false); err != nil {
				s.fail(jobID, "incremental_refresh_failed", err)
			}
			return
		}
		if decision.Mode == RefreshModeReused {
			var current model.TrainingOrchestrationCurrent
			if err := s.DB.WithContext(ctx).Where("owner_scope_id = ? AND source_fingerprint = ?", s.OwnerScopeID, fingerprint).First(&current).Error; err == nil {
				if err := s.succeedWithRefresh(ctx, jobID, current.ResultWikiPageID, true, decision); err != nil {
					s.fail(jobID, "job_update_failed", err)
				}
				return
			}
			decision.Mode = RefreshModeFull
			s.recordRefreshDecision(ctx, jobID, decision)
		}
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"stage": StageGenerating, "updated_at": time.Now().UTC()}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	doc, err := s.Generator.Generate(ctx, input)
	if err != nil {
		code := "generation_failed"
		var generationErr *GenerationError
		if errors.As(err, &generationErr) && strings.TrimSpace(generationErr.Code) != "" {
			code = generationErr.Code
		}
		s.fail(jobID, code, err)
		return
	}
	// Keep knowledge references program-owned even when a compatibility
	// generator implementation returns a prebuilt document.
	bindProjectionKnowledgeFields(&doc.TrainingPathProjection, input)
	ensureGapAnalyses(&doc.TrainingPathProjection)
	if doc.TrainingPathProjection.SourceFingerprint != fingerprint {
		s.fail(jobID, "generation_validation_failed", fmt.Errorf("training orchestration generator fingerprint does not match the job input"))
		return
	}
	if err := ValidateProjection(doc, input); err != nil {
		s.fail(jobID, "generation_validation_failed", err)
		return
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"stage": StageAssembling, "progress": 75, "updated_at": time.Now().UTC()}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	latest, err := s.Collector.Collect(ctx)
	if err != nil {
		s.fail(jobID, "source_recheck_failed", err)
		return
	}
	latestFingerprint, err := SourceFingerprint(latest, s.Model, s.PromptVersion)
	if err != nil || latestFingerprint != fingerprint {
		s.fail(jobID, "source_changed", fmt.Errorf("training orchestration sources changed while generation was running"))
		return
	}
	content, err := json.Marshal(doc)
	if err != nil {
		s.fail(jobID, "encode_result_failed", err)
		return
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"stage": StagePublishing, "updated_at": time.Now().UTC()}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	page, err := s.writeProjectionPage(ctx, overwrite, weknora.WikiPageWrite{Slug: projectionSlug(s.OwnerScopeID, fingerprint), Title: "培训学习路径 " + time.Now().Format("2006-01-02 15:04"), PageType: "index", Status: "published", Content: string(content), Summary: "由当前视频、正式总结或规范化转写与字幕证据生成的培训学习路径", SourceRefs: projectionSourceRefs(input), ChunkRefs: projectionChunkRefs(input)})
	if err != nil {
		s.fail(jobID, "wiki_publish_failed", err)
		return
	}
	if page == nil {
		s.fail(jobID, "wiki_publish_failed", fmt.Errorf("training orchestration Wiki writer returned no page"))
		return
	}
	var published ProjectionDocument
	if err := json.Unmarshal([]byte(page.Content), &published); err != nil {
		s.fail(jobID, "wiki_publish_validation_failed", err)
		return
	}
	bindProjectionKnowledgeFields(&published.TrainingPathProjection, input)
	ensureGapAnalyses(&published.TrainingPathProjection)
	published.TrainingPathProjection.Statistics = buildStatistics(
		input,
		published.TrainingPathProjection.TopicClusters,
		selectedVideoIDs(published.TrainingPathProjection.TopicClusters),
		allProjectionEvidence(published.TrainingPathProjection.TopicClusters),
	)
	validationErr := ValidateProjection(published, input)
	if validationErr != nil {
		slog.Warn("training orchestration published projection validation failed", "job_id", jobID, "reason", validationErr.Error())
	}
	if validationErr != nil || published.TrainingPathProjection.SourceFingerprint != fingerprint {
		err := validationErr
		if err == nil {
			err = fmt.Errorf("published training orchestration fingerprint does not match the job input")
		}
		s.fail(jobID, "wiki_publish_validation_failed", err)
		return
	}
	if stable, stableErr := s.sourcesStillMatch(ctx, fingerprint); stableErr != nil || !stable {
		if stableErr == nil {
			stableErr = fmt.Errorf("training orchestration sources changed after Wiki publication")
		}
		s.fail(jobID, "source_changed", stableErr)
		return
	}
	warningCode, warningMessage := projectionWarning(doc)
	if err := s.publishCurrent(ctx, jobID, page.ID, fingerprint, warningCode, warningMessage); err != nil {
		s.fail(jobID, "current_switch_failed", err)
		return
	}
}

// runStageFour executes the bounded two-stage pipeline behind the existing
// HTTP job contract. The legacy path remains available when the new runner is
// not configured, which keeps local rollback cheap and explicit.
func (s *Service) runStageFour(ctx context.Context, jobID string, overwrite bool) {
	snapshot, err := s.CatalogCollector.CollectCatalog(ctx)
	if err != nil {
		s.fail(jobID, "input_collection_failed", err)
		return
	}
	fingerprint, err := CatalogFingerprint(snapshot, s.Model, s.PromptVersion)
	if err != nil {
		s.fail(jobID, "fingerprint_failed", err)
		return
	}
	// Collect twice before any model call. A changed transcript, summary or
	// video set is rejected before spending model budget.
	stableSnapshot, err := s.CatalogCollector.CollectCatalog(ctx)
	if err != nil {
		s.fail(jobID, "source_stability_check_failed", err)
		return
	}
	stableFingerprint, err := CatalogFingerprint(stableSnapshot, s.Model, s.PromptVersion)
	if err != nil {
		s.fail(jobID, "fingerprint_failed", err)
		return
	}
	if stableFingerprint != fingerprint {
		s.fail(jobID, "source_not_stable", fmt.Errorf("training orchestration sources changed before generation started"))
		return
	}
	currentRefs := catalogReferenceSnapshot(snapshot)
	refs, _ := json.Marshal(currentRefs)
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"progress": 25, "source_fingerprint": fingerprint, "input_references": string(refs), "stage": StagePlanning, "updated_at": time.Now().UTC(),
	}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	decision := RefreshDecision{Mode: RefreshModeFull}
	var previous *ProjectionDocument
	if !overwrite {
		var decisionErr error
		decision, previous, decisionErr = s.refreshDecision(ctx, currentRefs)
		if decisionErr != nil {
			s.fail(jobID, "refresh_decision_failed", decisionErr)
			return
		}
		s.recordRefreshDecision(ctx, jobID, decision)
		if decision.isLocal() && previous != nil {
			if err := s.publishLocalRefresh(ctx, jobID, previous, catalogInputPackage(snapshot), fingerprint, decision, false); err != nil {
				s.fail(jobID, "incremental_refresh_failed", err)
			}
			return
		}
		if decision.Mode == RefreshModeReused {
			var current model.TrainingOrchestrationCurrent
			if err := s.DB.WithContext(ctx).Where("owner_scope_id = ? AND source_fingerprint = ?", s.OwnerScopeID, fingerprint).First(&current).Error; err == nil {
				if err := s.succeedWithRefresh(ctx, jobID, current.ResultWikiPageID, true, decision); err != nil {
					s.fail(jobID, "job_update_failed", err)
				}
				return
			}
			decision.Mode = RefreshModeFull
			s.recordRefreshDecision(ctx, jobID, decision)
		}
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"stage": StageGenerating, "updated_at": time.Now().UTC()}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	var doc ProjectionDocument
	snapshot.SourceFingerprint = fingerprint
	if decision.Mode == RefreshModeIncremental && previous != nil {
		if runner, ok := s.StageFour.(IncrementalStageFourRunner); ok {
			incrementalSnapshot := snapshot
			incrementalSnapshot.SourceFingerprint = fingerprint
			doc, err = runner.RunIncremental(ctx, incrementalSnapshot, *previous, decision.ChangedVideoIDs)
		} else {
			doc, err = s.StageFour.Run(ctx, snapshot)
			decision.Mode = RefreshModeFull
			s.recordRefreshDecision(ctx, jobID, decision)
		}
	} else {
		doc, err = s.StageFour.Run(ctx, snapshot)
	}
	if err != nil {
		code := "generation_failed"
		var generationErr *GenerationError
		if errors.As(err, &generationErr) && strings.TrimSpace(generationErr.Code) != "" {
			code = generationErr.Code
		}
		s.fail(jobID, code, err)
		return
	}
	if doc.TrainingPathProjection.SourceFingerprint != fingerprint {
		s.fail(jobID, "generation_validation_failed", fmt.Errorf("training orchestration stage-four fingerprint does not match the job input"))
		return
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"stage": StageAssembling, "progress": 75, "updated_at": time.Now().UTC()}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	content, err := json.Marshal(doc)
	if err != nil {
		s.fail(jobID, "encode_result_failed", err)
		return
	}
	if err := s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"stage": StagePublishing, "updated_at": time.Now().UTC()}).Error; err != nil {
		s.fail(jobID, "job_update_failed", err)
		return
	}
	page, err := s.writeProjectionPage(ctx, overwrite, weknora.WikiPageWrite{
		Slug: projectionSlug(s.OwnerScopeID, fingerprint), Title: "培训学习路径 " + time.Now().Format("2006-01-02 15:04"),
		PageType: "index", Status: "published", Content: string(content),
		Summary:    "由当前视频、正式总结或规范化转写与字幕证据生成的培训学习路径",
		SourceRefs: catalogSourceRefs(snapshot), ChunkRefs: catalogChunkRefs(snapshot),
	})
	if err != nil {
		s.fail(jobID, "wiki_publish_failed", err)
		return
	}
	if page == nil {
		s.fail(jobID, "wiki_publish_failed", fmt.Errorf("training orchestration Wiki writer returned no page"))
		return
	}
	var published ProjectionDocument
	if err := json.Unmarshal([]byte(page.Content), &published); err != nil {
		s.fail(jobID, "wiki_publish_validation_failed", err)
		return
	}
	if published.TrainingPathProjection.SourceFingerprint != fingerprint || published.TrainingPathProjection.OwnerScopeID != s.OwnerScopeID || published.TrainingPathProjection.SchemaVersion != SchemaVersion {
		slog.Warn("training orchestration published projection validation failed", "job_id", jobID, "reason", "projection identity does not match the job input")
		s.fail(jobID, "wiki_publish_validation_failed", fmt.Errorf("published training orchestration identity is invalid"))
		return
	}
	// Compare the serialized contract after clearing internal-only assembly
	// fields. JSON round-tripping intentionally drops EvidenceText, so an
	// in-memory deep equality check would reject a valid published document.
	if !samePublishedProjection(published, doc) {
		slog.Warn("training orchestration published projection validation failed", "job_id", jobID, "reason", "published projection payload differs from candidate")
		s.fail(jobID, "wiki_publish_validation_failed", fmt.Errorf("published training orchestration payload differs from candidate"))
		return
	}
	if stable, stableErr := s.sourcesStillMatch(ctx, fingerprint); stableErr != nil || !stable {
		if stableErr == nil {
			stableErr = fmt.Errorf("training orchestration sources changed after Wiki publication")
		}
		s.fail(jobID, "source_changed", stableErr)
		return
	}
	warningCode, warningMessage := projectionWarning(doc)
	if err := s.publishCurrent(ctx, jobID, page.ID, fingerprint, warningCode, warningMessage); err != nil {
		s.fail(jobID, "current_switch_failed", err)
	}
}

func samePublishedProjection(published, candidate ProjectionDocument) bool {
	publishedClusters := append([]TopicCluster(nil), published.TrainingPathProjection.TopicClusters...)
	candidateClusters := append([]TopicCluster(nil), candidate.TrainingPathProjection.TopicClusters...)
	for i := range publishedClusters {
		publishedClusters[i].EvidenceText = nil
	}
	for i := range candidateClusters {
		candidateClusters[i].EvidenceText = nil
	}
	published.TrainingPathProjection.TopicClusters = publishedClusters
	candidate.TrainingPathProjection.TopicClusters = candidateClusters
	publishedJSON, publishedErr := json.Marshal(published)
	candidateJSON, candidateErr := json.Marshal(candidate)
	return publishedErr == nil && candidateErr == nil && bytes.Equal(publishedJSON, candidateJSON)
}

func (s *Service) writeProjectionPage(ctx context.Context, overwrite bool, input weknora.WikiPageWrite) (*weknora.WikiPage, error) {
	if overwrite {
		if wiki, ok := s.Wiki.(projectionOverwriteWiki); ok {
			return wiki.UpsertPage(ctx, s.KnowledgeBaseID, input)
		}
	}
	page, err := s.Wiki.EnsurePage(ctx, s.KnowledgeBaseID, input)
	if err != nil || page == nil {
		return page, err
	}
	// A previous run may have created the deterministic slug and then failed
	// before switching the current pointer. Reuse is valid only when the page
	// body is the candidate we just validated; otherwise repair that orphan
	// page through the explicit upsert capability before publish-current.
	if strings.TrimSpace(page.Content) != strings.TrimSpace(input.Content) {
		if wiki, ok := s.Wiki.(projectionOverwriteWiki); ok {
			updated, updateErr := wiki.UpsertPage(ctx, s.KnowledgeBaseID, input)
			if updateErr != nil {
				return nil, updateErr
			}
			// The write endpoint may return before its read model is refreshed.
			// Re-read the deterministic page so publication validates the content
			// that readers will actually receive.
			if reader, ok := s.Wiki.(projectionSlugReader); ok {
				for attempt := 0; attempt < 4; attempt++ {
					if refreshed, readErr := reader.GetPage(ctx, s.KnowledgeBaseID, input.Slug); readErr == nil && refreshed != nil {
						if strings.TrimSpace(refreshed.Content) == strings.TrimSpace(input.Content) || attempt == 3 {
							return refreshed, nil
						}
					}
					if attempt < 3 {
						select {
						case <-ctx.Done():
							return updated, ctx.Err()
						case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
						}
					}
				}
			}
			return updated, nil
		}
	}
	return page, nil
}

func (s *Service) publishCurrent(ctx context.Context, jobID, pageID, fingerprint, warningCode, warningMessage string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job model.TrainingOrchestrationJob
		if err := tx.Where("id = ? AND owner_scope_id = ? AND status = ?", jobID, s.OwnerScopeID, JobRunning).First(&job).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		current := model.TrainingOrchestrationCurrent{OwnerScopeID: s.OwnerScopeID, JobID: jobID, ResultWikiPageID: pageID, SourceFingerprint: fingerprint, UpdatedAt: now}
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		return tx.Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{
			"status": JobSucceeded, "progress": 100, "result_wiki_page_id": pageID, "reused": false,
			"warning_code": strings.TrimSpace(warningCode), "warning_message": strings.TrimSpace(warningMessage),
			"finished_at": now, "updated_at": now,
		}).Error
	})
}

func (s *Service) succeed(ctx context.Context, jobID, pageID string, reused bool) error {
	return s.succeedWithRefresh(ctx, jobID, pageID, reused, RefreshDecision{Mode: RefreshModeReused})
}

func (s *Service) succeedWithRefresh(ctx context.Context, jobID, pageID string, reused bool, decision RefreshDecision) error {
	now := time.Now().UTC()
	return s.DB.WithContext(ctx).Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{
		"status": JobSucceeded, "progress": 100, "result_wiki_page_id": pageID, "reused": reused,
		"refresh_mode": decision.Mode, "changed_video_count": decision.ChangedVideoCount,
		"changed_knowledge_count": decision.ChangedKnowledgeCount, "changed_evidence_count": decision.ChangedEvidenceCount,
		"changed_cluster_count": decision.ChangedClusterCount,
		"warning_code":          "", "warning_message": "",
		"finished_at": now, "updated_at": now,
	}).Error
}

func projectionWarning(doc ProjectionDocument) (string, string) {
	if !doc.TrainingPathProjection.RetrievalDegraded {
		return "", ""
	}
	return WarningEvidenceRetrievalDegraded, warningEvidenceRetrievalDegradedMessage
}
func (s *Service) fail(jobID, code string, err error) {
	now := time.Now().UTC()
	slog.Warn("training orchestration job failed",
		"job_id", jobID,
		"error_code", strings.TrimSpace(code),
		"safe_reason", safeErrorReason(err),
		"error_type", fmt.Sprintf("%T", err),
	)
	message := safeTaskErrorMessage(code, err)
	s.DB.Model(&model.TrainingOrchestrationJob{}).Where("id = ?", jobID).Updates(map[string]any{"status": JobFailed, "error_code": code, "error_message": message, "finished_at": now, "updated_at": now})
}
func (s *Service) validate() error {
	legacyReady := s != nil && s.Collector != nil && s.Generator != nil
	stageFourReady := s != nil && s.CatalogCollector != nil && s.StageFour != nil
	if s == nil || s.DB == nil || (!legacyReady && !stageFourReady) || s.Wiki == nil || strings.TrimSpace(s.KnowledgeBaseID) == "" || strings.TrimSpace(s.OwnerScopeID) == "" {
		return fmt.Errorf("training orchestration service dependencies are not configured")
	}
	return nil
}

type inputReference struct {
	VideoID, Availability, SkipReason, Title, TranscriptGeneration, TopicSource, SummaryWikiPageID, TranscriptKnowledgeID, KnowledgeIndexWikiPageID string
	DurationSeconds, SummaryVersion                                                                                                                 int
	KnowledgeWikiPageIDs, EvidenceIDs                                                                                                               []string
	MetadataDigest, ContentDigest                                                                                                                   string
	TopicDigest                                                                                                                                     string
	EvidenceDigests, EvidenceMetadataDigests, KnowledgeDigests, KnowledgeMetadataDigests                                                            map[string]string
}

func inputReferenceSnapshot(input InputPackage) []inputReference {
	out := make([]inputReference, 0, len(input.QualifiedVideos)+len(input.SkippedVideos))
	for _, v := range input.QualifiedVideos {
		item := referenceFromVideoProfile(v)
		item.Availability = "available"
		if v.Summary != nil {
			item.SummaryWikiPageID = v.Summary.WikiPageID
			item.SummaryVersion = v.Summary.Version
		}
		if v.Transcript != nil {
			item.TranscriptKnowledgeID = v.Transcript.KnowledgeID
		}
		for _, e := range v.EvidenceSignals {
			item.EvidenceIDs = append(item.EvidenceIDs, e.EvidenceID)
		}
		item.MetadataDigest = referenceMetadataDigest(item)
		item.ContentDigest = referenceContentDigest(v)
		out = append(out, item)
	}
	for _, skipped := range input.SkippedVideos {
		ref := inputReference{VideoID: skipped.VideoID, Availability: "unavailable", SkipReason: string(skipped.Reason)}
		ref.MetadataDigest, ref.ContentDigest = digestValue(struct{ ID string }{skipped.VideoID}), digestValue(struct{ ID string }{skipped.VideoID})
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VideoID < out[j].VideoID })
	return out
}

func catalogReferenceSnapshot(snapshot CatalogSnapshot) []inputReference {
	out := make([]inputReference, 0, len(snapshot.Videos)+len(snapshot.SkippedVideos))
	for _, video := range snapshot.Videos {
		item := inputReference{VideoID: video.VideoID, Availability: "available", Title: video.Title, DurationSeconds: video.DurationSeconds, TranscriptGeneration: video.TranscriptGeneration, SummaryWikiPageID: video.SummaryWikiPageID, SummaryVersion: video.SummaryVersion}
		profile := video.OrchestrationProfile
		if profile == nil {
			profile = video.CompatibilityProfile
		}
		if profile != nil {
			item.TopicDigest = digestValue(profile.PrimaryTopic)
			for _, unit := range profile.TopicUnits {
				for _, ref := range unit.EvidenceRefs {
					item.EvidenceIDs = appendUnique(item.EvidenceIDs, ref.EvidenceSentenceID)
					if item.EvidenceMetadataDigests == nil {
						item.EvidenceMetadataDigests = map[string]string{}
					}
					item.EvidenceMetadataDigests[ref.EvidenceSentenceID] = digestValue(struct {
						ID         string
						Start, End int
					}{ref.EvidenceSentenceID, ref.StartMs, ref.EndMs})
				}
			}
		}
		for _, signal := range video.KnowledgeSignals {
			if item.KnowledgeDigests == nil {
				item.KnowledgeDigests = map[string]string{}
			}
			item.KnowledgeDigests[signal.KnowledgeObjectID] = digestValue(struct {
				ID, Core    string
				Structure   map[string]string
				EvidenceIDs []string
			}{signal.KnowledgeObjectID, signal.CoreContent, signal.StructureFields, signal.EvidenceIDs})
			if item.KnowledgeMetadataDigests == nil {
				item.KnowledgeMetadataDigests = map[string]string{}
			}
			item.KnowledgeMetadataDigests[signal.KnowledgeObjectID] = digestValue(struct {
				ID, PageID, Title string
				Version           int
			}{signal.KnowledgeObjectID, signal.WikiPageID, signal.Title, signal.WikiPageVersion})
		}
		item.MetadataDigest = referenceMetadataDigest(item)
		item.ContentDigest = catalogContentDigest(video)
		out = append(out, item)
	}
	for _, skipped := range snapshot.SkippedVideos {
		ref := inputReference{VideoID: skipped.VideoID, Availability: "unavailable", SkipReason: string(skipped.Reason)}
		ref.MetadataDigest, ref.ContentDigest = digestValue(struct{ ID string }{skipped.VideoID}), digestValue(struct{ ID string }{skipped.VideoID})
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VideoID < out[j].VideoID })
	return out
}

func catalogSourceRefs(snapshot CatalogSnapshot) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, video := range snapshot.Videos {
		add(video.SummaryWikiPageID)
	}
	return out
}

func catalogChunkRefs(snapshot CatalogSnapshot) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, video := range snapshot.Videos {
		profile := video.OrchestrationProfile
		if profile == nil {
			profile = video.CompatibilityProfile
		}
		if profile == nil {
			continue
		}
		for _, unit := range profile.TopicUnits {
			for _, ref := range unit.EvidenceRefs {
				add(ref.ChunkID)
			}
		}
	}
	return out
}
func projectionSlug(owner, fingerprint string) string {
	clean := strings.NewReplacer(":", "-", "/", "-").Replace(fingerprint)
	if len(clean) > 28 {
		clean = clean[:28]
	}
	ownerHash := sha256String(owner)
	return "training-orchestration/" + ownerHash[:12] + "/" + clean
}
func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}
func projectionSourceRefs(input InputPackage) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, v := range input.QualifiedVideos {
		if v.Summary != nil {
			add(v.Summary.WikiPageID)
		}
		if v.Transcript != nil {
			add(v.Transcript.KnowledgeID)
		}
	}
	return out
}
func projectionChunkRefs(input InputPackage) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, v := range input.QualifiedVideos {
		for _, e := range v.EvidenceSignals {
			if _, ok := seen[e.ChunkKnowledgeID]; !ok {
				seen[e.ChunkKnowledgeID] = struct{}{}
				out = append(out, e.ChunkKnowledgeID)
			}
		}
	}
	return out
}
