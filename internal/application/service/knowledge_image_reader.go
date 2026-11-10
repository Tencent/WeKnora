package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// maxRerankImageBytes caps one image read for reranking, so a mislabelled
// file cannot exhaust memory on the request path.
const maxRerankImageBytes = 32 << 20

// knowledgeImageReader reads an image a knowledge base stored, from the
// backend that holds it. A resource:// reference names its backend on the
// resource record, a provider:// URL in its scheme; anything else falls back
// to the knowledge base's configured storage. Ingestion and reranking read
// images through it, so both find an image in the same place.
type knowledgeImageReader struct {
	tenantRepo      interfaces.TenantRepository
	fileSvc         interfaces.FileService
	storageResolver interfaces.StorageBackendResolver
	resourceCatalog interfaces.ResourceCatalog
}

// fileService picks the FileService holding imageURL. kb is looked up only
// when the URL itself does not say which backend holds it.
func (r knowledgeImageReader) fileService(
	ctx context.Context, tenantID uint64, kb func() *types.KnowledgeBase, imageURL string,
) interfaces.FileService {
	tenant, err := r.tenantRepo.GetTenantByID(ctx, tenantID)
	if err != nil || tenant == nil {
		logger.Warnf(ctx, "[ImageReader] GetTenantByID failed: tenant=%d err=%v", tenantID, err)
		return r.fileSvc
	}

	backendID, _, _ := types.ParseStorageBackendPath(imageURL)
	provider := types.ParseProviderScheme(imageURL)
	// A resource:// reference carries no provider/backend in the URL itself; the
	// authoritative backend lives on the stored resource record. Using the KB's
	// currently configured backend here would break reads when the resource was
	// stored on a different backend (multi-backend / post-migration).
	if _, isResourceRef := types.ParseResourcePath(imageURL); isResourceRef && r.resourceCatalog != nil {
		if resource, resErr := r.resourceCatalog.Resolve(ctx, imageURL); resErr != nil {
			logger.Warnf(ctx, "[ImageReader] resolve resource reference failed: url=%s err=%v", imageURL, resErr)
		} else if resource != nil {
			backendID = resource.StorageBackendID
			provider = strings.ToLower(strings.TrimSpace(resource.Provider))
		}
	}
	if provider == "" {
		if kb := kb(); kb != nil {
			provider = strings.ToLower(strings.TrimSpace(kb.GetStorageProvider()))
			if backendID == "" && kb.StorageBackendID != nil {
				backendID = *kb.StorageBackendID
			}
		}
	}

	if r.storageResolver == nil {
		return r.fileSvc
	}
	baseDir := strings.TrimSpace(os.Getenv("LOCAL_STORAGE_BASE_DIR"))
	logger.Infof(ctx,
		"[ImageReader] resolving file service: tenant=%d provider=%q LOCAL_STORAGE_BASE_DIR=%q imageURL=%s",
		tenantID, provider, baseDir, imageURL)
	fileSvc, _, svcErr := r.storageResolver.ResolveFileService(ctx, tenant, backendID, provider, baseDir)
	if svcErr != nil {
		logger.Warnf(ctx,
			"[ImageReader] resolve file service failed (falling back to default): tenant=%d provider=%s err=%v",
			tenantID, provider, svcErr)
		return r.fileSvc
	}
	return fileSvc
}

// isStoredImageURL reports a URL a FileService reads: a resource:// reference
// or a provider:// URL. Such a URL must never reach the HTTP downloader,
// which is what caused issue #1282.
func isStoredImageURL(imageURL string) bool {
	_, isResourceRef := types.ParseResourcePath(imageURL)
	return isResourceRef || types.ParseProviderScheme(imageURL) != ""
}

// readStored reads a stored image. maxBytes, when positive, refuses a larger
// one instead of reading it whole.
func (r knowledgeImageReader) readStored(
	ctx context.Context, tenantID uint64, kb func() *types.KnowledgeBase, imageURL string, maxBytes int64,
) ([]byte, error) {
	fileSvc := r.fileService(ctx, tenantID, kb, imageURL)
	if fileSvc == nil {
		return nil, fmt.Errorf("no file service available for %s", imageURL)
	}
	reader, err := fileSvc.GetFile(ctx, imageURL)
	if err != nil {
		return nil, fmt.Errorf("file service get %s: %w", imageURL, err)
	}
	defer func() { _ = reader.Close() }()
	var src io.Reader = reader
	if maxBytes > 0 {
		src = io.LimitReader(reader, maxBytes+1)
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", imageURL, err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("image %s is larger than %d bytes", imageURL, maxBytes)
	}
	return data, nil
}

func (s *knowledgeBaseService) imageReader() knowledgeImageReader {
	return knowledgeImageReader{
		tenantRepo:      s.tenantRepo,
		fileSvc:         s.fileSvc,
		storageResolver: s.storageResolver,
		resourceCatalog: s.resourceCatalog,
	}
}

// ReadChunkImage reads the image a search result shows: the first image of
// its image_info, from the storage of the knowledge base that holds it. The
// result must come from a search the caller was authorized to run.
func (s *knowledgeBaseService) ReadChunkImage(ctx context.Context, result *types.SearchResult) ([]byte, error) {
	if result == nil {
		return nil, errors.New("no search result")
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(result.ImageInfo), &infos); err != nil || len(infos) == 0 {
		return nil, fmt.Errorf("chunk %s carries no image", result.ID)
	}
	imageURL := strings.TrimSpace(infos[0].URL)
	if imageURL == "" {
		imageURL = strings.TrimSpace(infos[0].OriginalURL)
	}
	if imageURL == "" {
		return nil, fmt.Errorf("chunk %s carries no image URL", result.ID)
	}
	if !isStoredImageURL(imageURL) {
		data, err := secutils.DownloadBytes(imageURL)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", imageURL, err)
		}
		if len(data) > maxRerankImageBytes {
			return nil, fmt.Errorf("image %s is larger than %d bytes", imageURL, maxRerankImageBytes)
		}
		return data, nil
	}
	kb, err := s.repo.GetKnowledgeBaseByID(ctx, result.KnowledgeBaseID)
	if err != nil || kb == nil {
		return nil, fmt.Errorf("knowledge base %s of chunk %s: %w", result.KnowledgeBaseID, result.ID, err)
	}
	return s.imageReader().readStored(ctx, kb.TenantID, func() *types.KnowledgeBase { return kb }, imageURL,
		maxRerankImageBytes)
}
