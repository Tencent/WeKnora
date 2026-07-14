package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/config"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// Error definitions for knowledge service operations
var (
	// ErrInvalidFileType is returned when an unsupported file type is provided
	ErrInvalidFileType = errors.New("unsupported file type")
	// ErrInvalidURL is returned when an invalid URL is provided
	ErrInvalidURL = errors.New("invalid URL")
	// ErrChunkNotFound is returned when a requested chunk cannot be found.
	// Aliases the repository sentinel so a chunk-not-found from the repo
	// errors.Is-matches at the service and middleware layers (a single
	// identity instead of two string-equal-but-distinct errors).
	ErrChunkNotFound = repository.ErrChunkNotFound
	// ErrDuplicateFile is returned when trying to add a file that already exists
	ErrDuplicateFile = errors.New("file already exists")
	// ErrDuplicateURL is returned when trying to add a URL that already exists
	ErrDuplicateURL = errors.New("URL already exists")
	// ErrImageNotParse is returned when trying to update image information without enabling multimodel
	ErrImageNotParse = errors.New("image not parse without enable multimodel")
)

// knowledgeService implements the knowledge service interface
// service 实现知识服务接口
type knowledgeService struct {
	config          *config.Config
	retrieveEngine  interfaces.RetrieveEngineRegistry
	ownership       retriever.TenantStoreOwnership
	repo            interfaces.KnowledgeRepository
	kbService       interfaces.KnowledgeBaseService
	tenantRepo      interfaces.TenantRepository
	tenantService   interfaces.TenantService
	documentReader  interfaces.DocumentReader
	chunkService    interfaces.ChunkService
	chunkRepo       interfaces.ChunkRepository
	tagRepo         interfaces.KnowledgeTagRepository
	tagService      interfaces.KnowledgeTagService
	fileSvc         interfaces.FileService
	modelService    interfaces.ModelService
	task            interfaces.TaskEnqueuer
	taskInspector   interfaces.TaskInspector
	graphEngine     interfaces.RetrieveGraphRepository
	redisClient     *redis.Client
	kbShareService  interfaces.KBShareService
	imageResolver   *docparser.ImageResolver
	taskPendingRepo interfaces.TaskPendingOpsRepository

	// In-memory fallbacks for Lite mode (no Redis)
	memFAQProgress      sync.Map // taskID -> *types.FAQImportProgress
	memFAQRunningImport sync.Map // kbID -> *runningFAQImportInfo
	wikiRepo            interfaces.WikiPageRepository
	wikiService         interfaces.WikiPageService

	// spanTracker records the per-attempt span tree for the parsing
	// pipeline. Best-effort: a nil tracker (test harness) is safely
	// handled because the public surface is the SpanTracker interface,
	// which has a no-op fallback. See knowledge_span_tracker.go.
	spanTracker SpanTracker
}

const (
	manualContentMaxLength = 200000
	manualFileExtension    = ".md"
	faqImportBatchSize     = 50 // 每批处理的FAQ条目数
)

// NewKnowledgeService creates a new knowledge service instance
func NewKnowledgeService(
	config *config.Config,
	repo interfaces.KnowledgeRepository,
	documentReader interfaces.DocumentReader,
	kbService interfaces.KnowledgeBaseService,
	tenantRepo interfaces.TenantRepository,
	tenantService interfaces.TenantService,
	chunkService interfaces.ChunkService,
	chunkRepo interfaces.ChunkRepository,
	tagRepo interfaces.KnowledgeTagRepository,
	tagService interfaces.KnowledgeTagService,
	fileSvc interfaces.FileService,
	modelService interfaces.ModelService,
	task interfaces.TaskEnqueuer,
	taskInspector interfaces.TaskInspector,
	graphEngine interfaces.RetrieveGraphRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership,
	redisClient *redis.Client,
	kbShareService interfaces.KBShareService,
	imageResolver *docparser.ImageResolver,
	wikiRepo interfaces.WikiPageRepository,
	wikiService interfaces.WikiPageService,
	taskPendingRepo interfaces.TaskPendingOpsRepository,
	spanTracker SpanTracker,
) (interfaces.KnowledgeService, error) {
	return &knowledgeService{
		config:          config,
		repo:            repo,
		kbService:       kbService,
		tenantRepo:      tenantRepo,
		tenantService:   tenantService,
		documentReader:  documentReader,
		chunkService:    chunkService,
		chunkRepo:       chunkRepo,
		tagRepo:         tagRepo,
		tagService:      tagService,
		fileSvc:         fileSvc,
		modelService:    modelService,
		task:            task,
		taskInspector:   taskInspector,
		graphEngine:     graphEngine,
		retrieveEngine:  retrieveEngine,
		ownership:       ownership,
		redisClient:     redisClient,
		kbShareService:  kbShareService,
		imageResolver:   imageResolver,
		wikiRepo:        wikiRepo,
		wikiService:     wikiService,
		taskPendingRepo: taskPendingRepo,
		spanTracker:     spanTracker,
	}, nil
}

// tracker returns a usable SpanTracker — falls back to a no-op when the
// service was constructed without one (test harness, lite mode w/o repo).
// All pipeline call sites go through this so they never need a nil check.
func (s *knowledgeService) tracker() SpanTracker {
	if s.spanTracker == nil {
		return noopSpanTracker{}
	}
	return s.spanTracker
}

// attemptCtxKey scopes the per-task attempt number to a single execution.
// Set once at the start of ProcessDocument / ProcessManualUpdate /
// KnowledgePostProcess so every nested tracker call within the same task
// can locate the right attempt without threading it through signatures.
type attemptCtxKey struct{}

// withAttempt returns a child ctx tagged with the given attempt number.
// Pass through every call site that may invoke the tracker.
func withAttempt(ctx context.Context, attempt int) context.Context {
	if attempt <= 0 {
		return ctx
	}
	return context.WithValue(ctx, attemptCtxKey{}, attempt)
}

// attemptFromCtx extracts the attempt number stored by withAttempt;
// returns 0 when missing (legacy paths or tests). Tracker call sites
// treat 0 as "skip recording" since we have no attempt to anchor under.
func attemptFromCtx(ctx context.Context) int {
	if v, ok := ctx.Value(attemptCtxKey{}).(int); ok {
		return v
	}
	return 0
}

// attemptSuperseded reports whether a newer parse attempt has started for the
// knowledge since this enrichment subtask was enqueued. Stale subtasks from a
// previous upload/edit/reparse that is still draining must NOT touch the new
// attempt's chunks or decrement its pending_subtasks_count — doing so would
// race-promote the row to completed before the new attempt finishes. An attempt
// of 0 predates attempt tracking (or tracking is disabled) and is never treated
// as superseded.
func attemptSuperseded(ctx context.Context, tracker SpanTracker, knowledgeID string, attempt int) bool {
	if attempt <= 0 || knowledgeID == "" {
		return false
	}
	return tracker.LatestAttempt(ctx, knowledgeID) > attempt
}

// finalizeSubtaskDetachedTimeout bounds the detached decrement so a wedged DB
// connection can't hang a worker goroutine forever in its terminal defer.
const finalizeSubtaskDetachedTimeout = 10 * time.Second

// finalizeSubtaskDetached evaluates the drain decision for a subtask's
// terminal exit and — when the subtask should drain — decrements
// pending_subtasks_count using a context DETACHED from the caller's
// cancellation.
//
// Decision: a subtask drains exactly once, on its terminal exit, UNLESS a newer
// attempt superseded it. "Terminal" means either the handler succeeded
// (retErr == nil) or it's the final asynq attempt (final). A non-final failure
// returns without draining because asynq will retry.
//
// Why detach: the decrement runs after the handler body, often as the very
// last thing a worker does. If it rode the task ctx, a cancelled ctx (graceful
// shutdown, a worker being preempted, or the task being interrupted under
// load) would make the DB UPDATE fail. That failure is only logged and
// swallowed, and because enrichment handlers frequently still return success
// (per-chunk LLM errors are tolerated, not propagated), asynq never retries —
// so the slot is never drained and the parent knowledge is stranded in
// "finalizing" forever with a non-zero counter. Detaching keeps the counter
// correct across cancellation; a bounded timeout guards against a wedged DB.
//
// source is a free-form tag (e.g. "question_batch[3]", "summary", "wiki")
// used to attribute a decrement failure to a specific subtask in logs.
func finalizeSubtaskDetached(
	ctx context.Context,
	repo interfaces.KnowledgeRepository,
	knowledgeID, source string,
	retErr error,
	superseded, final bool,
) {
	willDrain := repo != nil && knowledgeID != "" && !superseded && (retErr == nil || final)
	if !willDrain {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeSubtaskDetachedTimeout)
	defer cancel()
	if _, _, err := repo.FinalizeSubtask(dctx, knowledgeID); err != nil {
		logger.Warnf(ctx, "finalize subtask decrement failed source=%s knowledge=%s err=%v",
			source, knowledgeID, err)
	}
}

// beginStage / endStage / failStage / skipStage are the by-name shims
// the pipeline uses so call sites don't have to thread *Span values
// through the existing function signatures. Each helper looks up the
// stage from (kid, attempt-from-ctx, stageName) at write time — costs
// one extra DB read per terminal transition (≤ a dozen per knowledge),
// which is dwarfed by the work the stages themselves do.
func (s *knowledgeService) beginStage(ctx context.Context, kid, name string, input types.JSONMap) {
	a := attemptFromCtx(ctx)
	if a <= 0 {
		return
	}
	s.tracker().BeginStage(ctx, kid, a, name, input)
}

func (s *knowledgeService) endStage(ctx context.Context, kid, name string, output types.JSONMap) {
	a := attemptFromCtx(ctx)
	if a <= 0 {
		return
	}
	span := s.tracker().LookupStage(ctx, kid, a, name)
	if span == nil {
		return
	}
	s.tracker().EndSpan(ctx, span, output)
}

func (s *knowledgeService) failStage(ctx context.Context, kid, name, code, msg string, err error) {
	a := attemptFromCtx(ctx)
	if a <= 0 {
		return
	}
	span := s.tracker().LookupStage(ctx, kid, a, name)
	if span == nil {
		return
	}
	s.tracker().FailSpan(ctx, span, code, msg, err)
}

func (s *knowledgeService) skipStage(ctx context.Context, kid, name, reason string) {
	a := attemptFromCtx(ctx)
	if a <= 0 {
		return
	}
	span := s.tracker().LookupStage(ctx, kid, a, name)
	if span == nil {
		// No begin recorded — synthesize a span row for skipped state.
		// Use BeginStage with no input then SkipSpan to keep schema
		// invariants (started_at / kind set).
		span = s.tracker().BeginStage(ctx, kid, a, name, nil)
	}
	s.tracker().SkipSpan(ctx, span, reason)
}

// beginPostprocessSubspan opens a subspan beneath the postprocess stage
// span for (kid, attempt). Async post-pipeline tasks (summary, question,
// graph, wiki) call this on entry so their actual processing time shows
// up in the trace tree under postprocess instead of the stage looking
// like an instant ~10ms enqueue.
//
// Returns nil when:
//   - attempt <= 0 (legacy in-flight task without span tracking)
//   - the postprocess stage span is missing (parse predates tracker, or
//     the upstream BeginStage call failed)
//
// Callers must tolerate nil — pair every begin with a deferred
// endPostprocessSubspan / failPostprocessSubspan that no-ops on nil.
func (s *knowledgeService) beginPostprocessSubspan(
	ctx context.Context, knowledgeID string, attempt int, name string, input types.JSONMap,
) *Span {
	if attempt <= 0 || knowledgeID == "" || name == "" {
		return nil
	}
	parent := s.tracker().LookupStage(ctx, knowledgeID, attempt, types.StagePostProcess)
	if parent == nil {
		return nil
	}
	return s.tracker().BeginSubSpan(ctx, parent, name, types.SpanKindSubSpan, input)
}

// beginQuestionBatchSubspan opens a per-batch question subspan under the
// "postprocess.question" grouping span created by the orchestrator, falling
// back to the postprocess stage when the group span isn't found (legacy
// in-flight tasks or a tracker that skipped it). Mirrors beginPostprocessSubspan
// but resolves the grouping parent first.
func (s *knowledgeService) beginQuestionBatchSubspan(
	ctx context.Context, knowledgeID string, attempt int, name string, input types.JSONMap,
) *Span {
	if attempt <= 0 || knowledgeID == "" || name == "" {
		return nil
	}
	parent := s.tracker().LookupSpanByName(ctx, knowledgeID, attempt, postprocessQuestionGroupSpanName)
	if parent == nil {
		parent = s.tracker().LookupStage(ctx, knowledgeID, attempt, types.StagePostProcess)
	}
	if parent == nil {
		return nil
	}
	return s.tracker().BeginSubSpan(ctx, parent, name, types.SpanKindSubSpan, input)
}

func (s *knowledgeService) endPostprocessSubspan(ctx context.Context, span *Span, output types.JSONMap) {
	if span == nil {
		return
	}
	s.tracker().EndSpan(ctx, span, output)
}

func (s *knowledgeService) failPostprocessSubspan(
	ctx context.Context, span *Span, code, msg string, err error,
) {
	if span == nil {
		return
	}
	s.tracker().FailSpan(ctx, span, code, msg, err)
}

// getParserEngineOverridesFromContext returns parser engine overrides from tenant in context (e.g. MinerU endpoint, API key).
// Used when building document ReadRequest so UI-configured values take precedence over env.
func (s *knowledgeService) getParserEngineOverridesFromContext(ctx context.Context) map[string]string {
	if v := ctx.Value(types.TenantInfoContextKey); v != nil {
		if tenant, ok := v.(*types.Tenant); ok && tenant != nil {
			return tenant.ParserEngineConfig.ToOverridesMap()
		}
	}
	return nil
}

// GetRepository gets the knowledge repository
// Parameters:
//   - ctx: Context with authentication and request information
//
// Returns:
//   - interfaces.KnowledgeRepository: Knowledge repository
func (s *knowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

// isKnowledgeDeleting checks if a knowledge entry is being deleted.
// This is used to prevent async tasks from conflicting with deletion operations.
func (s *knowledgeService) isKnowledgeDeleting(ctx context.Context, tenantID uint64, knowledgeID string) bool {
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		// If we can't find the knowledge, assume it's deleted
		logger.Warnf(ctx, "Failed to check knowledge deletion status (assuming deleted): %v", err)
		return true
	}
	if knowledge == nil {
		return true
	}
	return knowledge.ParseStatus == types.ParseStatusDeleting
}

// isKnowledgeAborted returns (true, status) when the knowledge has been
// marked as deleting OR cancelled so async pipeline workers should bail
// out. Status is returned so callers can branch on cleanup behavior:
// deleting → existing cleanup of partial chunks/index applies;
// cancelled → keep partially written data per user expectation.
//
// When the row is missing or unreadable we conservatively return
// (true, ParseStatusDeleting): the existing deleting branch already
// handles cleanup-or-no-op semantics safely.
func (s *knowledgeService) isKnowledgeAborted(
	ctx context.Context, tenantID uint64, knowledgeID string,
) (bool, string) {
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Warnf(ctx, "Failed to check knowledge abort status (assuming deleted): %v", err)
		return true, types.ParseStatusDeleting
	}
	if knowledge == nil {
		return true, types.ParseStatusDeleting
	}
	switch knowledge.ParseStatus {
	case types.ParseStatusDeleting, types.ParseStatusCancelled:
		return true, knowledge.ParseStatus
	}
	return false, knowledge.ParseStatus
}

// checkStorageEngineConfigured verifies that the knowledge base has a storage engine configured
// (either at the KB level or via the tenant default).
//
// 内部版兜底语义：当 KB 与空间都未配置 storage provider 时，如果服务实例持有
// 全局 FileService（由容器按 STORAGE_TYPE 注入，默认 local），允许直接落到该
// 全局 fileSvc 上，不再硬性阻断。这与 resolveFileService / resolveFileServiceForPath
// 在 provider 为空时回退到 s.fileSvc 的行为保持一致，避免上层闸门和下游解析口径不一。
// 仅当 KB/空间/全局三处都拿不到任何可用 FileService 时才报错。
func (s *knowledgeService) checkStorageEngineConfigured(ctx context.Context, kb *types.KnowledgeBase) error {
	provider := kb.GetStorageProvider()
	if provider == "" {
		tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		if tenant != nil && tenant.StorageEngineConfig != nil {
			provider = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
		}
	}
	if provider != "" {
		return nil
	}
	if s != nil && s.fileSvc != nil {
		logger.Warnf(ctx,
			"[storage] checkStorageEngineConfigured: no KB/tenant provider, fallback to global fileSvc (kb=%s)",
			kbIDOrEmpty(kb))
		return nil
	}
	return werrors.NewBadRequestError("请先为知识库选择存储引擎，再上传内容。请前往知识库设置页面进行配置。")
}

func kbIDOrEmpty(kb *types.KnowledgeBase) string {
	if kb == nil {
		return ""
	}
	return kb.ID
}

func defaultChannel(ch string) string {
	if ch == "" {
		return types.ChannelWeb
	}
	return ch
}

// GetKnowledgeByID retrieves a knowledge entry by its ID
func (s *knowledgeService) GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
			"tenant_id":    tenantID,
		})
		return nil, err
	}

	// Load tags for this knowledge
	tagMap, err := s.repo.GetKnowledgeTags(ctx, []string{knowledge.ID})
	if err != nil {
		logger.Warnf(ctx, "Failed to load tags for knowledge %s: %v", knowledge.ID, err)
	} else if tags, ok := tagMap[knowledge.ID]; ok {
		knowledge.Tags = tags
	}

	logger.Infof(ctx, "Knowledge retrieved successfully, ID: %s, type: %s", knowledge.ID, knowledge.Type)
	return knowledge, nil
}

// GetKnowledgeByIDOnly retrieves knowledge by ID without tenant filter (for permission resolution).
func (s *knowledgeService) GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error) {
	return s.repo.GetKnowledgeByIDOnly(ctx, id)
}

// GetOwningKBCreatorID walks knowledge_id -> kb_id -> KB.CreatorID for
// the per-KB ownership lookups in handler/rbac_lookups.go (PR 5, #1303).
// Both fetches are tenant-scoped (GetKnowledgeByID reads tenant from
// ctx; GetKnowledgeBaseByID is then constrained to the same tenant by
// the KB service), so a cross-tenant id surfaces as the underlying
// "not found" error and the caller maps it to ErrResourceNotFound. The
// KB row itself is not returned so callers can't accidentally widen
// their scope past "needed the creator id".
func (s *knowledgeService) GetOwningKBCreatorID(ctx context.Context, knowledgeID string) (string, error) {
	// Resolve via the repository directly: ownership only needs the
	// knowledge -> kb_id link, so we deliberately skip the service-level
	// GetKnowledgeByID (which also eagerly loads tags) to keep this lookup
	// minimal and tenant-scoped.
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok {
		return "", werrors.NewUnauthorizedError("Workspace ID not found in context")
	}
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return "", err
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return "", err
	}
	if kb == nil {
		return "", repository.ErrKnowledgeBaseNotFound
	}
	return kb.CreatorID, nil
}

// ListKnowledgeByKnowledgeBaseID returns all knowledge entries in a knowledge base
func (s *knowledgeService) ListKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string,
) ([]*types.Knowledge, error) {
	return s.repo.ListKnowledgeByKnowledgeBaseID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), kbID)
}

// ListPagedKnowledgeByKnowledgeBaseID returns paginated knowledge entries in a knowledge base
func (s *knowledgeService) ListPagedKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string, page *types.Pagination, filter types.KnowledgeListFilter,
) (*types.PageResult, error) {
	knowledges, total, err := s.repo.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		ctx.Value(types.TenantIDContextKey).(uint64), kbID, page, filter)
	if err != nil {
		return nil, err
	}

	// Batch load tags for all knowledge entries
	if len(knowledges) > 0 {
		ids := make([]string, len(knowledges))
		for i, k := range knowledges {
			ids[i] = k.ID
		}
		tagMap, err := s.repo.GetKnowledgeTags(ctx, ids)
		if err != nil {
			logger.Errorf(ctx, "Failed to load tags for knowledge list: %v", err)
			// Non-fatal: continue without tags
		} else {
			for _, k := range knowledges {
				if tags, ok := tagMap[k.ID]; ok {
					k.Tags = tags
				}
			}
		}
	}

	return types.NewPageResult(total, page, knowledges), nil
}

// GetKnowledgeFile retrieves the physical file associated with a knowledge entry
func (s *knowledgeService) GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error) {
	// Get knowledge record
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, id)
	if err != nil {
		return nil, "", err
	}

	// Manual knowledge stores content in Metadata — stream it directly as a .md file.
	if knowledge.IsManual() {
		meta, err := knowledge.ManualMetadata()
		if err != nil {
			return nil, "", err
		}
		// ManualMetadata returns (nil, nil) when Metadata column is empty; treat as empty content.
		content := ""
		if meta != nil {
			content = meta.Content
		}
		filename := sanitizeManualDownloadFilename(knowledge.Title)
		return io.NopCloser(strings.NewReader(content)), filename, nil
	}

	// Resolve KB-level file service with FilePath fallback protection
	kb, _ := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	file, err := s.resolveFileServiceForPath(ctx, kb, knowledge.FilePath).GetFile(ctx, knowledge.FilePath)
	if err != nil {
		return nil, "", err
	}

	return file, knowledge.FileName, nil
}

func (s *knowledgeService) UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	record, err := s.repo.GetKnowledgeByID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), knowledge.ID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge record: %v", err)
		return err
	}
	// if need other fields update, please add here
	if knowledge.Title != "" {
		record.Title = knowledge.Title
	}
	if knowledge.Description != "" {
		record.Description = knowledge.Description
	}

	// Update knowledge record in the repository
	if err := s.repo.UpdateKnowledge(ctx, record); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge: %v", err)
		return err
	}
	logger.Infof(ctx, "Knowledge updated successfully, ID: %s", knowledge.ID)
	return nil
}

// GetKnowledgeBatch retrieves multiple knowledge entries by their IDs
func (s *knowledgeService) GetKnowledgeBatch(ctx context.Context,
	tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return s.repo.GetKnowledgeBatch(ctx, tenantID, ids)
}

// GetKnowledgeBatchWithSharedAccess retrieves knowledge by IDs, including items from shared KBs the user has access to.
// Used when building search targets so that @mentioned files from shared KBs are included.
func (s *knowledgeService) GetKnowledgeBatchWithSharedAccess(ctx context.Context,
	tenantID uint64, ids []string,
) ([]*types.Knowledge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ownList, err := s.repo.GetKnowledgeBatch(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	foundSet := make(map[string]bool)
	for _, k := range ownList {
		if k != nil {
			foundSet[k.ID] = true
		}
	}
	userIDVal := ctx.Value(types.UserIDContextKey)
	if userIDVal == nil {
		return ownList, nil
	}
	userID, ok := userIDVal.(string)
	if !ok || userID == "" {
		return ownList, nil
	}
	// Plan 3: shared-KB permission is keyed on (tenant, tenant_role)
	// rather than user. callerTenantRole drives the 3-D cap.
	callerTenantRole := types.TenantRoleFromContext(ctx)
	for _, id := range ids {
		if foundSet[id] {
			continue
		}
		k, err := s.repo.GetKnowledgeByIDOnly(ctx, id)
		if err != nil || k == nil || k.KnowledgeBaseID == "" {
			continue
		}
		hasPermission, err := s.kbShareService.HasTenantKBPermission(ctx, k.KnowledgeBaseID, tenantID, callerTenantRole, types.OrgRoleViewer)
		if err != nil || !hasPermission {
			continue
		}
		foundSet[k.ID] = true
		ownList = append(ownList, k)
	}
	return ownList, nil
}

// SetKnowledgeTags replaces all tags for a single knowledge entry.
func (s *knowledgeService) SetKnowledgeTags(ctx context.Context, knowledgeID string, tagIDs []string) error {
	return s.repo.SetKnowledgeTags(ctx, knowledgeID, tagIDs)
}

// ListKnowledgeIDsByTagIDs returns document knowledge IDs carrying any of the
// specified KB-local tags.
func (s *knowledgeService) ListKnowledgeIDsByTagIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagIDs []string,
) ([]string, error) {
	return s.repo.ListIDsByTagIDs(ctx, tenantID, kbID, tagIDs)
}

// validateKnowledgeTagIDs ensures every tag exists and belongs to the given knowledge base.
func (s *knowledgeService) validateKnowledgeTagIDs(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	tagIDs []string,
) error {
	unique := make([]string, 0, len(tagIDs))
	seen := make(map[string]struct{}, len(tagIDs))
	for _, tagID := range tagIDs {
		if tagID == "" {
			continue
		}
		if _, dup := seen[tagID]; dup {
			continue
		}
		seen[tagID] = struct{}{}
		unique = append(unique, tagID)
	}
	if len(unique) == 0 {
		return nil
	}

	tags, err := s.tagRepo.GetByIDs(ctx, tenantID, unique)
	if err != nil {
		return err
	}
	tagMap := make(map[string]*types.KnowledgeTag, len(tags))
	for _, tag := range tags {
		tagMap[tag.ID] = tag
	}
	for _, tagID := range unique {
		tag, ok := tagMap[tagID]
		if !ok {
			return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不存在", tagID))
		}
		if tag.KnowledgeBaseID != kbID {
			return werrors.NewBadRequestError("标签不属于当前知识库")
		}
	}
	return nil
}

// attachTagsToKnowledge populates knowledge.Tags from the join table.
func (s *knowledgeService) attachTagsToKnowledge(ctx context.Context, knowledge *types.Knowledge) {
	if knowledge == nil {
		return
	}
	tagMap, err := s.repo.GetKnowledgeTags(ctx, []string{knowledge.ID})
	if err != nil {
		logger.Warnf(ctx, "Failed to load tags for knowledge %s: %v", knowledge.ID, err)
		return
	}
	if tags, ok := tagMap[knowledge.ID]; ok {
		knowledge.Tags = tags
	}
}

// setAndAttachKnowledgeTags validates, persists, and populates tags on a knowledge entry.
func (s *knowledgeService) setAndAttachKnowledgeTags(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	knowledge *types.Knowledge,
	tagIDs []string,
) error {
	if err := s.validateKnowledgeTagIDs(ctx, tenantID, kbID, tagIDs); err != nil {
		return err
	}
	if len(tagIDs) > 0 {
		if err := s.repo.SetKnowledgeTags(ctx, knowledge.ID, tagIDs); err != nil {
			return err
		}
	}
	s.attachTagsToKnowledge(ctx, knowledge)
	return nil
}

// GetKnowledgeTags returns tags for multiple knowledge IDs.
func (s *knowledgeService) GetKnowledgeTags(ctx context.Context, knowledgeIDs []string) (map[string][]*types.KnowledgeTag, error) {
	return s.repo.GetKnowledgeTags(ctx, knowledgeIDs)
}

// UpdateKnowledgeTag updates the tags assigned to a knowledge document.
func (s *knowledgeService) UpdateKnowledgeTag(ctx context.Context, knowledgeID string, tagIDs []string) error {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return err
	}

	// Validate all tag IDs
	if err := s.validateKnowledgeTagIDs(ctx, tenantID, knowledge.KnowledgeBaseID, tagIDs); err != nil {
		return err
	}

	return s.repo.SetKnowledgeTags(ctx, knowledgeID, tagIDs)
}

// UpdateKnowledgeTagBatch updates tags for document knowledge items in batch.
// authorizedKBID restricts all updates to knowledge items belonging to this KB;
// pass empty string to skip the check (caller must ensure authorization by other means).
func (s *knowledgeService) UpdateKnowledgeTagBatch(ctx context.Context, authorizedKBID string, updates map[string][]string) error {
	if len(updates) == 0 {
		return nil
	}
	tenantIDVal := ctx.Value(types.TenantIDContextKey)
	if tenantIDVal == nil {
		return werrors.NewUnauthorizedError("workspace ID not found in context")
	}
	tenantID, ok := tenantIDVal.(uint64)
	if !ok {
		return werrors.NewUnauthorizedError("invalid workspace ID in context")
	}

	// Get all knowledge items in batch
	knowledgeIDs := make([]string, 0, len(updates))
	for knowledgeID := range updates {
		knowledgeIDs = append(knowledgeIDs, knowledgeID)
	}
	knowledgeList, err := s.repo.GetKnowledgeBatch(ctx, tenantID, knowledgeIDs)
	if err != nil {
		return err
	}

	// Validate all requested IDs were found and belong to the authorized KB
	if authorizedKBID != "" {
		if len(knowledgeList) != len(updates) {
			return werrors.NewForbiddenError("some knowledge IDs are not accessible in the authorized scope")
		}
		for _, k := range knowledgeList {
			if k.KnowledgeBaseID != authorizedKBID {
				return werrors.NewForbiddenError(
					fmt.Sprintf("knowledge %s does not belong to authorized knowledge base", k.ID))
			}
		}
	}

	// Collect all unique tag IDs for validation
	tagIDSet := make(map[string]bool)
	for _, tagIDs := range updates {
		for _, tagID := range tagIDs {
			if tagID != "" {
				tagIDSet[tagID] = true
			}
		}
	}

	// Validate all tags exist and belong to the correct KB
	tagMap := make(map[string]*types.KnowledgeTag)
	if len(tagIDSet) > 0 {
		tagIDList := make([]string, 0, len(tagIDSet))
		for tagID := range tagIDSet {
			tagIDList = append(tagIDList, tagID)
		}
		tags, err := s.tagRepo.GetByIDs(ctx, tenantID, tagIDList)
		if err != nil {
			return err
		}
		for _, tag := range tags {
			tagMap[tag.ID] = tag
		}
	}

	// Validate tag ownership per knowledge
	for _, knowledge := range knowledgeList {
		tagIDs, exists := updates[knowledge.ID]
		if !exists {
			continue
		}
		for _, tagID := range tagIDs {
			if tagID == "" {
				continue
			}
			tag, ok := tagMap[tagID]
			if !ok {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不存在", tagID))
			}
			if tag.KnowledgeBaseID != knowledge.KnowledgeBaseID {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不属于知识库 %s", tagID, knowledge.KnowledgeBaseID))
			}
		}
	}

	// Set tags for each knowledge
	for knowledgeID, tagIDs := range updates {
		if err := s.repo.SetKnowledgeTags(ctx, knowledgeID, tagIDs); err != nil {
			return err
		}
	}

	return nil
}

// SearchKnowledge searches knowledge items by keyword across the tenant and shared knowledge bases.
// fileTypes: optional list of file extensions to filter by (e.g., ["csv", "xlsx"])
func (s *knowledgeService) SearchKnowledge(ctx context.Context, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, int64, error) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok {
		return nil, false, 0, werrors.NewUnauthorizedError("Workspace ID not found in context")
	}

	scopes := make([]types.KnowledgeSearchScope, 0)

	// Own tenant: document-type knowledge bases
	ownKBs, err := s.kbService.ListKnowledgeBases(ctx)
	if err == nil {
		for _, kb := range ownKBs {
			if kb != nil && kb.Type == types.KnowledgeBaseTypeDocument {
				scopes = append(scopes, types.KnowledgeSearchScope{TenantID: tenantID, KBID: kb.ID})
			}
		}
	}

	// Shared knowledge bases (document type only). Plan 3 of #1303 keys
	// the share lookup on (tenantID, callerTenantRole); userID is no
	// longer load-bearing for org-share access.
	if userIDVal := ctx.Value(types.UserIDContextKey); userIDVal != nil {
		if userID, ok := userIDVal.(string); ok && userID != "" {
			callerTenantRole := types.TenantRoleFromContext(ctx)
			sharedList, err := s.kbShareService.ListSharedKnowledgeBases(ctx, tenantID, callerTenantRole)
			if err == nil {
				for _, info := range sharedList {
					if info != nil && info.KnowledgeBase != nil && info.KnowledgeBase.Type == types.KnowledgeBaseTypeDocument {
						scopes = append(scopes, types.KnowledgeSearchScope{
							TenantID: info.SourceTenantID,
							KBID:     info.KnowledgeBase.ID,
						})
					}
				}
			}
		}
	}

	if len(scopes) == 0 {
		return nil, false, 0, nil
	}
	return s.repo.SearchKnowledgeInScopes(ctx, scopes, keyword, offset, limit, fileTypes)
}

// SearchKnowledgeForScopes searches knowledge within the given scopes (e.g. for shared agent context).
func (s *knowledgeService) SearchKnowledgeForScopes(ctx context.Context, scopes []types.KnowledgeSearchScope, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, int64, error) {
	if len(scopes) == 0 {
		return nil, false, 0, nil
	}
	return s.repo.SearchKnowledgeInScopes(ctx, scopes, keyword, offset, limit, fileTypes)
}
