package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

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
	// ErrChunkNotFound is returned when a requested chunk cannot be found
	ErrChunkNotFound = errors.New("chunk not found")
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
	config           *config.Config
	retrieveEngine   interfaces.RetrieveEngineRegistry
	repo             interfaces.KnowledgeRepository
	kbService        interfaces.KnowledgeBaseService
	tenantRepo       interfaces.TenantRepository
	tenantService    interfaces.TenantService
	documentReader   interfaces.DocumentReader
	chunkService     interfaces.ChunkService
	chunkRepo        interfaces.ChunkRepository
	tagRepo          interfaces.KnowledgeTagRepository
	tagService       interfaces.KnowledgeTagService
	fileSvc          interfaces.FileService
	modelService     interfaces.ModelService
	task             interfaces.TaskEnqueuer
	graphEngine      interfaces.RetrieveGraphRepository
	redisClient      *redis.Client
	kbShareService   interfaces.KBShareService
	imageResolver    *docparser.ImageResolver
	taskPendingRepo  interfaces.TaskPendingOpsRepository

	// In-memory fallbacks for Lite mode (no Redis)
	memFAQProgress      sync.Map // taskID -> *types.FAQImportProgress
	memFAQRunningImport sync.Map // kbID -> *runningFAQImportInfo
	wikiRepo            interfaces.WikiPageRepository
	wikiService         interfaces.WikiPageService
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
	graphEngine interfaces.RetrieveGraphRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	redisClient *redis.Client,
	kbShareService interfaces.KBShareService,
	imageResolver *docparser.ImageResolver,
	wikiRepo interfaces.WikiPageRepository,
	wikiService interfaces.WikiPageService,
	taskPendingRepo interfaces.TaskPendingOpsRepository,
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
		graphEngine:     graphEngine,
		retrieveEngine:  retrieveEngine,
		redisClient:     redisClient,
		kbShareService:  kbShareService,
		imageResolver:   imageResolver,
		wikiRepo:        wikiRepo,
		wikiService:     wikiService,
		taskPendingRepo: taskPendingRepo,
	}, nil
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

// checkStorageEngineConfigured verifies that the knowledge base has a storage engine configured
// (either at the KB level or via the tenant default). Returns an error if no storage engine is found.
func checkStorageEngineConfigured(ctx context.Context, kb *types.KnowledgeBase) error {
	provider := kb.GetStorageProvider()
	if provider == "" {
		tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		if tenant != nil && tenant.StorageEngineConfig != nil {
			provider = strings.ToLower(strings.TrimSpace(tenant.StorageEngineConfig.DefaultProvider))
		}
	}
	if provider == "" {
		return werrors.NewBadRequestError("请先为知识库选择存储引擎，再上传内容。请前往知识库设置页面进行配置。")
	}
	return nil
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

	logger.Infof(ctx, "Knowledge retrieved successfully, ID: %s, type: %s", knowledge.ID, knowledge.Type)
	return knowledge, nil
}

// GetKnowledgeByIDOnly retrieves knowledge by ID without tenant filter (for permission resolution).
func (s *knowledgeService) GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error) {
	return s.repo.GetKnowledgeByIDOnly(ctx, id)
}

// ListKnowledgeByKnowledgeBaseID returns all knowledge entries in a knowledge base
func (s *knowledgeService) ListKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string,
) ([]*types.Knowledge, error) {
	return s.repo.ListKnowledgeByKnowledgeBaseID(ctx, ctx.Value(types.TenantIDContextKey).(uint64), kbID)
}

// ListPagedKnowledgeByKnowledgeBaseID returns paginated knowledge entries in a knowledge base
func (s *knowledgeService) ListPagedKnowledgeByKnowledgeBaseID(ctx context.Context,
	kbID string, page *types.Pagination, tagID string, keyword string, fileType string,
) (*types.PageResult, error) {
	knowledges, total, err := s.repo.ListPagedKnowledgeByKnowledgeBaseID(ctx,
		ctx.Value(types.TenantIDContextKey).(uint64), kbID, page, tagID, keyword, fileType)
	if err != nil {
		return nil, err
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
	for _, id := range ids {
		if foundSet[id] {
			continue
		}
		k, err := s.repo.GetKnowledgeByIDOnly(ctx, id)
		if err != nil || k == nil || k.KnowledgeBaseID == "" {
			continue
		}
		hasPermission, err := s.kbShareService.HasKBPermission(ctx, k.KnowledgeBaseID, userID, types.OrgRoleViewer)
		if err != nil || !hasPermission {
			continue
		}
		foundSet[k.ID] = true
		ownList = append(ownList, k)
	}
	return ownList, nil
}

// UpdateKnowledgeTag updates the tag assigned to a knowledge document.
func (s *knowledgeService) UpdateKnowledgeTag(ctx context.Context, knowledgeID string, tagID *string) error {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return err
	}

	var resolvedTagID string
	if tagID != nil && *tagID != "" {
		tag, err := s.tagRepo.GetByID(ctx, tenantID, *tagID)
		if err != nil {
			return err
		}
		if tag.KnowledgeBaseID != knowledge.KnowledgeBaseID {
			return werrors.NewBadRequestError("标签不属于当前知识库")
		}
		resolvedTagID = tag.ID
	}

	knowledge.TagID = resolvedTagID
	return s.repo.UpdateKnowledge(ctx, knowledge)
}

// UpdateKnowledgeTagBatch updates tags for document knowledge items in batch.
// authorizedKBID restricts all updates to knowledge items belonging to this KB;
// pass empty string to skip the check (caller must ensure authorization by other means).
func (s *knowledgeService) UpdateKnowledgeTagBatch(ctx context.Context, authorizedKBID string, updates map[string]*string) error {
	if len(updates) == 0 {
		return nil
	}
	tenantIDVal := ctx.Value(types.TenantIDContextKey)
	if tenantIDVal == nil {
		return werrors.NewUnauthorizedError("tenant ID not found in context")
	}
	tenantID, ok := tenantIDVal.(uint64)
	if !ok {
		return werrors.NewUnauthorizedError("invalid tenant ID in context")
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

	// Build tag ID map for validation
	tagIDSet := make(map[string]bool)
	for _, tagID := range updates {
		if tagID != nil && *tagID != "" {
			tagIDSet[*tagID] = true
		}
	}

	// Validate all tags in batch
	tagMap := make(map[string]*types.KnowledgeTag)
	if len(tagIDSet) > 0 {
		tagIDs := make([]string, 0, len(tagIDSet))
		for tagID := range tagIDSet {
			tagIDs = append(tagIDs, tagID)
		}
		for _, tagID := range tagIDs {
			tag, err := s.tagRepo.GetByID(ctx, tenantID, tagID)
			if err != nil {
				return err
			}
			tagMap[tagID] = tag
		}
	}

	// Update knowledge items
	knowledgeToUpdate := make([]*types.Knowledge, 0)
	for _, knowledge := range knowledgeList {
		tagID, exists := updates[knowledge.ID]
		if !exists {
			continue
		}

		var resolvedTagID string
		if tagID != nil && *tagID != "" {
			tag, ok := tagMap[*tagID]
			if !ok {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不存在", *tagID))
			}
			if tag.KnowledgeBaseID != knowledge.KnowledgeBaseID {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %s 不属于知识库 %s", *tagID, knowledge.KnowledgeBaseID))
			}
			resolvedTagID = tag.ID
		}

		knowledge.TagID = resolvedTagID
		knowledgeToUpdate = append(knowledgeToUpdate, knowledge)
	}

	if len(knowledgeToUpdate) > 0 {
		return s.repo.UpdateKnowledgeBatch(ctx, knowledgeToUpdate)
	}

	return nil
}

// SearchKnowledge searches knowledge items by keyword across the tenant and shared knowledge bases.
// fileTypes: optional list of file extensions to filter by (e.g., ["csv", "xlsx"])
func (s *knowledgeService) SearchKnowledge(ctx context.Context, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error) {
	tenantID, ok := ctx.Value(types.TenantIDContextKey).(uint64)
	if !ok {
		return nil, false, werrors.NewUnauthorizedError("Tenant ID not found in context")
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

	// Shared knowledge bases (document type only)
	if userIDVal := ctx.Value(types.UserIDContextKey); userIDVal != nil {
		if userID, ok := userIDVal.(string); ok && userID != "" {
			sharedList, err := s.kbShareService.ListSharedKnowledgeBases(ctx, userID, tenantID)
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
		return nil, false, nil
	}
	return s.repo.SearchKnowledgeInScopes(ctx, scopes, keyword, offset, limit, fileTypes)
}

// SearchKnowledgeForScopes searches knowledge within the given scopes (e.g. for shared agent context).
func (s *knowledgeService) SearchKnowledgeForScopes(ctx context.Context, scopes []types.KnowledgeSearchScope, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error) {
	if len(scopes) == 0 {
		return nil, false, nil
	}
	return s.repo.SearchKnowledgeInScopes(ctx, scopes, keyword, offset, limit, fileTypes)
}
