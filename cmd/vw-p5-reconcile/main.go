package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	customllm "github.com/Tencent/WeKnora/internal/custom/client/llm"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/Tencent/WeKnora/internal/custom/service/skill"
)

type invalidObject struct {
	WikiPageID string `json:"wiki_page_id"`
	Slug       string `json:"slug"`
	Reason     string `json:"reason"`
}

type reconciliationReport struct {
	Mode                  string                 `json:"mode"`
	Status                string                 `json:"status"`
	VideoID               string                 `json:"video_id"`
	TranscriptGeneration  string                 `json:"transcript_generation"`
	KnowledgeBaseID       string                 `json:"knowledge_base_id"`
	StoredIndexPageID     string                 `json:"stored_index_page_id,omitempty"`
	CurrentIndexPageID    string                 `json:"current_index_page_id,omitempty"`
	IndexValid            bool                   `json:"index_valid"`
	ValidObjectCount      int                    `json:"valid_object_count"`
	FormalRelationCount   int                    `json:"formal_relation_count"`
	RelationGatePassed    bool                   `json:"relation_gate_passed"`
	InvalidObjects        []invalidObject        `json:"invalid_objects"`
	NeedsRepair           bool                   `json:"needs_repair"`
	ProposedActions       []string               `json:"proposed_actions"`
	Applied               bool                   `json:"applied"`
	GraphJobID            string                 `json:"graph_job_id,omitempty"`
	GraphJobPreviousState string                 `json:"graph_job_previous_state,omitempty"`
	WikiPagesPreserved    bool                   `json:"wiki_pages_preserved"`
	WikiRepairActions     []wikiRepairAction     `json:"wiki_repair_actions,omitempty"`
	NormalizedPageCount   int                    `json:"normalized_page_count,omitempty"`
	QuarantinedPageCount  int                    `json:"quarantined_page_count,omitempty"`
	BlockingReasons       []string               `json:"blocking_reasons,omitempty"`
	SemanticDecisions     []wikiSemanticDecision `json:"semantic_identity_decisions,omitempty"`
}

func main() {
	videoID := flag.String("video-id", "", "single video ID to inspect and repair")
	apply := flag.Bool("apply", false, "apply the targeted database repair; default is dry-run")
	normalizeWiki := flag.Bool("normalize-wiki", false, "normalize current-generation Wiki pages before requeueing the graph job")
	reportPath := flag.String("report", "", "optional JSON report path")
	flag.Parse()
	if strings.TrimSpace(*videoID) == "" {
		fatalf("video-id is required")
	}

	cfg := config.Load()
	roles, err := cfg.WeKnora.ResolveKnowledgeBaseRoles()
	if err != nil {
		fatalf("resolve knowledge-base roles: %v", err)
	}
	db, err := openDatabase(cfg.Database)
	if err != nil {
		fatalf("open business database: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	var video model.Video
	if err := db.WithContext(ctx).First(&video, "id = ?", strings.TrimSpace(*videoID)).Error; err != nil {
		fatalf("load video: %v", err)
	}
	wiki := weknora.NewWikiClient(cfg.WeKnora.ForKnowledgeBase(roles.Knowledge))
	knowledgeReader := weknora.New(cfg.WeKnora.ForKnowledgeBase(roles.Knowledge))
	pages, err := wiki.ListAllPages(ctx, roles.Knowledge, "")
	if err != nil {
		fatalf("list Wiki pages: %v", err)
	}
	pages, err = hydratePages(ctx, wiki, roles.Knowledge, pages)
	if err != nil {
		fatalf("read Wiki pages: %v", err)
	}
	report := auditPages(video, roles.Knowledge, pages)
	if *normalizeWiki {
		scope, err := loadWikiRepairScope(ctx, db, knowledgeReader, roles.Knowledge, video)
		if err != nil {
			if *apply {
				fatalf("load Wiki repair scope: %v", err)
			}
			report.Mode = "dry-run-normalize-wiki"
			report.Status = "blocked"
			report.NeedsRepair = true
			report.BlockingReasons = append(report.BlockingReasons, "load Wiki repair scope: "+err.Error())
			report.ProposedActions = append([]string{"repair_transcript_source"}, report.ProposedActions...)
			if err := writeReport(report, strings.TrimSpace(*reportPath)); err != nil {
				fatalf("write report: %v", err)
			}
			return
		}
		semanticAdapter := knowledge.NewCompletionModelSemanticIdentityAdapter(customllm.NewClient(cfg.LLM))
		plan, err := planWikiRepair(ctx, video, pages, scope, semanticAdapter)
		planBlocked := errors.Is(err, errNoVerifiableWikiPages) || errors.Is(err, errSemanticIdentityReviewRequired)
		if err != nil && !planBlocked {
			fatalf("plan Wiki normalization: %v", err)
		}
		attachWikiRepairPlan(&report, plan)
		report.Mode = "dry-run-normalize-wiki"
		if planBlocked {
			report.Status = "blocked"
			report.NeedsRepair = true
			report.BlockingReasons = append(report.BlockingReasons, err.Error())
			action := "review_semantic_identity_decisions"
			if errors.Is(err, errNoVerifiableWikiPages) {
				action = "review_unverifiable_wiki_pages"
			}
			report.ProposedActions = append([]string{action}, report.ProposedActions...)
			if *apply {
				fatalf("plan Wiki normalization: %v", err)
			}
		} else {
			report.ProposedActions = append([]string{"normalize_valid_wiki_pages", "semantically_supersede_duplicate_pages", "quarantine_unverified_wiki_pages", "rebuild_video_index"}, report.ProposedActions...)
		}
		if *apply {
			if err := ensureGraphJobIdle(ctx, db, video); err != nil {
				fatalf("apply Wiki normalization: %v", err)
			}
			if err := applyWikiRepair(ctx, wiki, roles.Knowledge, video, plan); err != nil {
				fatalf("apply Wiki normalization: %v", err)
			}
			pages, err = wiki.ListAllPages(ctx, roles.Knowledge, "")
			if err != nil {
				fatalf("list normalized Wiki pages: %v", err)
			}
			pages, err = hydratePages(ctx, wiki, roles.Knowledge, pages)
			if err != nil {
				fatalf("read normalized Wiki pages: %v", err)
			}
			report = auditPages(video, roles.Knowledge, pages)
			attachWikiRepairPlan(&report, plan)
			report.Mode = "apply-normalize-wiki"
			if !report.IndexValid || report.ValidObjectCount == 0 || len(report.InvalidObjects) > 0 {
				fatalf("normalized Wiki pages did not pass strict audit: index_valid=%t valid_objects=%d invalid_objects=%d", report.IndexValid, report.ValidObjectCount, len(report.InvalidObjects))
			}
			if report.NeedsRepair {
				if err := applyRepair(ctx, db, roles.Knowledge, &video, &report); err != nil {
					fatalf("queue normalized graph repair: %v", err)
				}
			}
		}
	} else if *apply && report.NeedsRepair {
		if err := applyRepair(ctx, db, roles.Knowledge, &video, &report); err != nil {
			fatalf("apply repair: %v", err)
		}
	}
	if err := writeReport(report, strings.TrimSpace(*reportPath)); err != nil {
		fatalf("write report: %v", err)
	}
}

func auditPages(video model.Video, knowledgeBaseID string, pages []weknora.WikiPage) reconciliationReport {
	report := reconciliationReport{
		Mode: "dry-run", Status: "ready", VideoID: video.ID,
		TranscriptGeneration: strings.TrimSpace(video.TranscriptGeneration),
		KnowledgeBaseID:      knowledgeBaseID, StoredIndexPageID: strings.TrimSpace(video.KnowledgeBaseWikiPageID),
		InvalidObjects: make([]invalidObject, 0), WikiPagesPreserved: true, RelationGatePassed: true,
	}
	validatedPages := make([]weknora.WikiPage, 0)
	for _, page := range pages {
		if isCurrentIndex(page, video) {
			report.IndexValid = true
			report.CurrentIndexPageID = page.ID
			continue
		}
		if !isCurrentObjectCandidate(page, video) {
			continue
		}
		if _, err := knowledge.ValidateWikiObjectPage(page.Content, page.PageType, video.ID, video.TranscriptGeneration); err != nil {
			report.InvalidObjects = append(report.InvalidObjects, invalidObject{WikiPageID: page.ID, Slug: page.Slug, Reason: err.Error()})
			continue
		}
		report.ValidObjectCount++
		validatedPages = append(validatedPages, page)
	}
	if len(validatedPages) > 1 {
		_, relationCount, relationErr := knowledgegraph.ValidateFormalRelationCompletion(video.ID, video.TranscriptGeneration, validatedPages)
		report.FormalRelationCount = relationCount
		report.RelationGatePassed = relationErr == nil
	}
	report.NeedsRepair = !report.IndexValid || report.ValidObjectCount == 0 || len(report.InvalidObjects) > 0 ||
		!report.RelationGatePassed || report.StoredIndexPageID != report.CurrentIndexPageID ||
		strings.ToLower(strings.TrimSpace(video.KnowledgeAuditStatus)) != "passed"
	if report.NeedsRepair {
		report.Status = "repair_required"
		if report.StoredIndexPageID != "" || strings.TrimSpace(video.KnowledgeAuditStatus) != "" {
			report.ProposedActions = append(report.ProposedActions, "clear_invalid_video_reference")
		}
		report.ProposedActions = append(report.ProposedActions, "requeue_graph_job")
	}
	return report
}

func isCurrentIndex(page weknora.WikiPage, video model.Video) bool {
	if page.PageType != "index" || page.Slug != "video/"+strings.TrimSpace(video.ID) || strings.TrimSpace(page.Content) == "" {
		return false
	}
	frontmatter := page.ParsedFrontmatter()
	if frontmatterString(frontmatter, "type") != "knowledge_base" ||
		frontmatterString(frontmatter, "source_video_id") != strings.TrimSpace(video.ID) ||
		frontmatterString(frontmatter, "transcript_generation") != strings.TrimSpace(video.TranscriptGeneration) ||
		strings.ToLower(frontmatterString(frontmatter, "audit_status")) != "aligned" {
		return false
	}
	title := strings.TrimSpace(video.Title)
	return title == "" || frontmatterString(frontmatter, "title") == title+"_知识底座"
}

func isCurrentObjectCandidate(page weknora.WikiPage, video model.Video) bool {
	frontmatter := page.ParsedFrontmatter()
	if frontmatterString(frontmatter, "source_video_id") != strings.TrimSpace(video.ID) ||
		frontmatterString(frontmatter, "transcript_generation") != strings.TrimSpace(video.TranscriptGeneration) {
		return false
	}
	for _, key := range []string{"type", "primary_type"} {
		if knowledge.IsKnowledgeType(knowledge.KnowledgeType(strings.ToLower(frontmatterString(frontmatter, key)))) {
			return true
		}
	}
	return false
}

func frontmatterString(frontmatter map[string]any, key string) string {
	value, _ := frontmatter[key].(string)
	return strings.TrimSpace(value)
}

func hydratePages(ctx context.Context, wiki *weknora.WikiClient, knowledgeBaseID string, pages []weknora.WikiPage) ([]weknora.WikiPage, error) {
	result := make([]weknora.WikiPage, 0, len(pages))
	for _, page := range pages {
		current, err := wiki.GetPage(ctx, knowledgeBaseID, page.Slug)
		if err != nil {
			return nil, fmt.Errorf("read page %s: %w", page.ID, err)
		}
		if current != nil && current.ID == page.ID {
			result = append(result, *current)
		}
	}
	return result, nil
}

func applyRepair(ctx context.Context, db *gorm.DB, knowledgeBaseID string, video *model.Video, report *reconciliationReport) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked model.Video
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", video.ID).Error; err != nil {
			return err
		}
		if locked.TranscriptGeneration != video.TranscriptGeneration {
			return fmt.Errorf("video transcript generation changed during reconciliation")
		}

		var job model.VideoProcessingJob
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("video_id = ? AND job_type = ? AND transcript_generation = ?", video.ID, skill.JobGraph, video.TranscriptGeneration).
			Where("result_stage = ? OR result_stage = '' OR result_stage IS NULL", "final").
			Order("updated_at DESC, created_at DESC")
		jobErr := query.First(&job).Error
		if jobErr == nil && (job.Status == "pending" || job.Status == "running") {
			return fmt.Errorf("graph job %s is already %s", job.ID, job.Status)
		}
		if jobErr != nil && !errors.Is(jobErr, gorm.ErrRecordNotFound) {
			return jobErr
		}

		inputPayload, err := graphInputPayload(tx, knowledgeBaseID, *video)
		if err != nil {
			return err
		}
		previousStatus := job.Status
		if errors.Is(jobErr, gorm.ErrRecordNotFound) {
			job = model.VideoProcessingJob{
				ID: uuid.NewString(), VideoID: video.ID, JobType: skill.JobGraph,
				TranscriptGeneration: video.TranscriptGeneration, Provider: "weknora", ResultStage: "final",
				IdempotencyKey: skill.IdempotencyKey(video.ID, skill.JobGraph) + ":" + video.TranscriptGeneration,
				Status:         "pending", MaxAttempts: 3, InputPayload: inputPayload,
			}
			if err := tx.Create(&job).Error; err != nil {
				return fmt.Errorf("create graph job: %w", err)
			}
		} else {
			updates := map[string]any{
				"status": "pending", "progress": 0, "attempt_count": 0, "input_payload": inputPayload,
				"result_payload": "", "error_category": "", "error_code": "", "error_message": "",
				"external_task_id": "", "started_at": nil, "completed_at": nil, "callback_received_at": nil,
			}
			if job.MaxAttempts <= 0 {
				updates["max_attempts"] = 3
			}
			if err := tx.Model(&job).Updates(updates).Error; err != nil {
				return fmt.Errorf("reset graph job: %w", err)
			}
		}
		if err := tx.Model(&model.Video{}).Where("id = ?", video.ID).Updates(map[string]any{
			"knowledge_base_wiki_page_id": "", "knowledge_audit_status": "",
		}).Error; err != nil {
			return fmt.Errorf("clear invalid video knowledge reference: %w", err)
		}

		report.Mode = "apply"
		report.Status = "repair_queued"
		report.Applied = true
		report.GraphJobID = job.ID
		report.GraphJobPreviousState = previousStatus
		return nil
	})
}

func graphInputPayload(tx *gorm.DB, knowledgeBaseID string, video model.Video) (string, error) {
	var source model.VideoTranscriptSource
	if err := tx.Where(
		"video_id = ? AND transcript_generation = ? AND knowledge_base_id = ?",
		video.ID, video.TranscriptGeneration, knowledgeBaseID,
	).First(&source).Error; err != nil {
		return "", fmt.Errorf("load current transcript source: %w", err)
	}
	if source.Status != "created" || strings.TrimSpace(source.KnowledgeID) == "" {
		return "", fmt.Errorf("current transcript source is not ready")
	}
	var chunkCount int64
	if err := tx.Model(&model.VideoTranscriptChunk{}).
		Where("video_id = ? AND generation = ? AND status = ?", video.ID, video.TranscriptGeneration, "completed").
		Count(&chunkCount).Error; err != nil {
		return "", fmt.Errorf("count current transcript chunks: %w", err)
	}
	if chunkCount == 0 {
		return "", fmt.Errorf("current transcript has no completed chunks")
	}
	payload, err := json.Marshal(map[string]any{
		"transcript_generation":               video.TranscriptGeneration,
		"transcript_source_knowledge_id":      source.KnowledgeID,
		"transcript_source_knowledge_base_id": source.KnowledgeBaseID,
		"transcript_input_mode":               "full_document",
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func openDatabase(database config.DatabaseConfig) (*gorm.DB, error) {
	if database.Driver == "sqlite" {
		return gorm.Open(sqlite.Open(database.Path), &gorm.Config{})
	}
	return gorm.Open(postgres.Open(database.DSN()), &gorm.Config{})
}

func writeReport(report reconciliationReport, path string) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path != "" {
		return os.WriteFile(path, data, 0o600)
	}
	_, err = os.Stdout.Write(data)
	return err
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vw-p5-reconcile: "+format+"\n", args...)
	os.Exit(1)
}
