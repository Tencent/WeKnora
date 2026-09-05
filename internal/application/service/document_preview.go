package service

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

type documentPreviewService struct {
	repo       *repository.DocumentPreviewRepository
	tenant     interfaces.TenantRepository
	kb         interfaces.KnowledgeBaseRepository
	catalog    interfaces.ResourceCatalog
	resolver   interfaces.StorageBackendResolver
	file       interfaces.FileService
	reader     interfaces.DocumentReader
	queue      interfaces.TaskEnqueuer
	conversion chan struct{}
}

// NewDocumentPreviewService creates the durable legacy DOC preview service.
func NewDocumentPreviewService(
	repo *repository.DocumentPreviewRepository,
	tenant interfaces.TenantRepository,
	kb interfaces.KnowledgeBaseRepository,
	catalog interfaces.ResourceCatalog,
	resolver interfaces.StorageBackendResolver,
	file interfaces.FileService,
	reader interfaces.DocumentReader,
	queue interfaces.TaskEnqueuer,
) interfaces.DocumentPreviewService {
	return &documentPreviewService{
		repo: repo, tenant: tenant, kb: kb, catalog: catalog, resolver: resolver,
		file: file, reader: reader, queue: queue, conversion: make(chan struct{}, 1),
	}
}

// Resolve an exact registered backend; do not silently fall back to a different
// disk or bucket when an old backend is unavailable. Cleanup needs no live KB.
func (s *documentPreviewService) storage(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	path string,
) (interfaces.FileService, error) {
	if s.resolver == nil {
		return s.file, nil
	}
	tenant, err := s.tenant.GetTenantByID(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	backendID, provider := "", ""
	if _, ok := types.ParseResourcePath(path); ok {
		r, err := s.catalog.Resolve(ctx, path)
		if err != nil {
			return nil, err
		}
		if r.TenantID != tenantID {
			return nil, errors.New("preview resource owner mismatch")
		}
		backendID, provider = r.StorageBackendID, r.Provider
	} else if path != "" {
		var inner string
		var scoped bool
		backendID, inner, scoped = types.ParseStorageBackendPath(path)
		if !scoped {
			inner = path
		}
		provider = types.ParseProviderScheme(inner)
	} else {
		kb, err := s.kb.GetKnowledgeBaseByID(ctx, kbID)
		if err != nil {
			return nil, err
		}
		if kb == nil || kb.TenantID != tenantID {
			return nil, repository.ErrPreviewGone
		}
		if kb.StorageBackendID != nil {
			backendID = *kb.StorageBackendID
		}
		provider = kb.GetStorageProvider()
	}
	svc, _, err := s.resolver.ResolveFileService(
		ctx, tenant, backendID, provider, os.Getenv("LOCAL_STORAGE_BASE_DIR"),
	)
	return svc, err
}

func (s *documentPreviewService) enqueue(id int64) {
	payload, _ := json.Marshal(struct {
		ID int64 `json:"id"`
	}{id})
	_, err := s.queue.Enqueue(
		asynq.NewTask(types.TypeDocumentPreview, payload),
		asynq.Queue(types.QueueMaintenance),
		asynq.Unique(15*time.Second),
		asynq.MaxRetry(0),
		asynq.Timeout(90*time.Second),
	)
	if err != nil && !errors.Is(err, asynq.ErrDuplicateTask) {
		logger.Warnf(context.Background(), "Preview wake-up deferred; durable task will retry")
	}
}

func (s *documentPreviewService) Wake(ctx context.Context, tenant uint64, id string) {
	op, err := s.repo.Ensure(ctx, tenant, id, false)
	if err != nil {
		logger.Warnf(ctx, "Preview wake-up deferred; durable upload intent retained")
		return
	}
	s.enqueue(op.ID)
}

func (s *documentPreviewService) Get(
	ctx context.Context,
	tenant uint64,
	id string,
	retry bool,
) (*types.DocumentPreviewResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	op, err := s.repo.Ensure(ctx, tenant, id, retry)
	if err != nil {
		return nil, err
	}
	state, err := repository.DecodeDocumentPreview(op)
	if err != nil {
		return nil, err
	}
	if state.Status == "ready" {
		svc, err := s.storage(ctx, tenant, state.KnowledgeBaseID, state.ResourceRef)
		if err != nil {
			return nil, err
		}
		content, err := svc.GetFile(ctx, state.ResourceRef)
		var data []byte
		if err == nil {
			data, err = docparser.ReadLegacyDocPreview(content)
			// The bounded read and validation are complete; Close only releases the source.
			_ = content.Close()
		}
		if err == nil {
			return &types.DocumentPreviewResult{Status: "ready", Content: io.NopCloser(bytes.NewReader(data))}, nil
		}
		// Only confirmed missing/corrupt representations are rebuilt. Storage
		// permissions and network failures retain the current cached identity.
		if !filesvc.IsFileNotFound(err) && !errors.Is(err, docparser.ErrInvalidLegacyDocPreview) {
			return nil, err
		}
		if err := s.repo.Invalidate(ctx, op); err != nil {
			return nil, err
		}
		state.Status = "pending"
	}
	if state.Status == "pending" {
		s.enqueue(op.ID)
	}
	return &types.DocumentPreviewResult{Status: state.Status}, nil
}

// Run periodically re-arms durable work in Redis and Lite mode. The queue is
// a wake-up transport, not the source of truth. Cursor pagination bounds reads.
func (s *documentPreviewService) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	sweep := func() {
		var after int64
		for ctx.Err() == nil {
			ops, err := s.repo.List(ctx, after)
			if err != nil {
				logger.Warnf(ctx, "Preview recovery scan failed")
				return
			}
			for _, op := range ops {
				s.enqueue(op.ID)
				after = op.ID
			}
			if len(ops) < 100 {
				return
			}
		}
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}

func (s *documentPreviewService) Handle(ctx context.Context, t *asynq.Task) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var p struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(t.Payload(), &p) != nil || p.ID <= 0 {
		return errors.New("invalid preview task")
	}
	op, err := s.repo.Load(ctx, p.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if op.Op == "artifact" {
		path, err := s.repo.RetireArtifact(ctx, op)
		if err != nil || path == "" {
			return err
		}
		svc, err := s.storage(ctx, op.TenantID, "", path)
		if err != nil {
			return err
		}
		if err = svc.DeleteFile(ctx, path); err != nil && !filesvc.IsFileNotFound(err) {
			return err
		}
		return s.repo.DeleteOp(ctx, op.ID)
	}
	if op.Op != "state" {
		return errors.New("unknown preview operation")
	}
	fresh, err := s.repo.Ensure(ctx, op.TenantID, op.ScopeID, false)
	if errors.Is(err, repository.ErrPreviewGone) {
		return s.repo.DeleteOp(ctx, op.ID)
	}
	if err != nil {
		return err
	}
	state, err := repository.DecodeDocumentPreview(fresh)
	if err != nil {
		return err
	}
	if state.Status != "pending" {
		return nil
	}
	// Don't claim a row while waiting for conversion capacity. Recovery retries
	// it later without burning the conversion failure budget.
	select {
	case s.conversion <- struct{}{}:
		defer func() { <-s.conversion }()
	default:
		return nil
	}
	claimed, err := s.repo.Claim(ctx, fresh)
	if err != nil || !claimed {
		return err
	}
	normalizer, ok := s.reader.(interfaces.LegacyDocNormalizer)
	if !ok {
		return s.repo.Fail(ctx, fresh)
	}
	err = s.generate(ctx, fresh, state, normalizer)
	if errors.Is(err, interfaces.ErrLegacyDocPreviewBusy) {
		return s.repo.DeferBusy(ctx, fresh)
	}
	if err != nil {
		logger.Warnf(ctx, "Preview generation failed; original file retained")
		// A canceled worker still records its retry using a bounded fresh context.
		retryCtx, retryCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer retryCancel()
		return s.repo.Fail(retryCtx, fresh)
	}
	return nil
}

func (s *documentPreviewService) generate(
	ctx context.Context,
	op *types.TaskPendingOp,
	state types.DocumentPreviewState,
	normalizer interfaces.LegacyDocNormalizer,
) error {
	srcSvc, err := s.storage(ctx, op.TenantID, state.KnowledgeBaseID, state.SourcePath)
	if err != nil {
		return err
	}
	src, err := srcSvc.GetFile(ctx, state.SourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	hash := md5.New() // Match knowledge.FileHash; not used as an authentication hash.
	content, err := normalizer.NormalizeLegacyDoc(ctx, io.TeeReader(src, hash))
	if err != nil {
		return err
	}
	sourceHash := hex.EncodeToString(hash.Sum(nil))
	if state.SourceHash != "" && state.SourceHash != sourceHash {
		return repository.ErrPreviewGone
	}
	dstSvc, err := s.storage(ctx, op.TenantID, state.KnowledgeBaseID, "")
	if err != nil {
		return err
	}
	tracked := false
	writeCtx := interfaces.WithFileWriteIntent(ctx, func(intentCtx context.Context, path string) error {
		if err := s.repo.Track(intentCtx, op, path); err != nil {
			return err
		}
		tracked = true
		return nil
	})
	ref, err := dstSvc.SaveBytes(writeCtx, content, op.TenantID, "preview.docx", false)
	if err != nil {
		return err
	}
	if !tracked {
		return errors.New("storage backend does not support recoverable preview writes")
	}
	return s.repo.Publish(ctx, op, ref, sourceHash)
}
