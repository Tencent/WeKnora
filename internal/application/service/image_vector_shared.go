package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// The two pieces of "make an image retrievable" that more than one service
// needs: reading image bytes through the right storage backend, and writing
// the image_vector child chunk and indexing it. They live outside
// ImageMultimodalService because the re-index path does the same for images
// ingested before anyone turned the switch on; duplicating either would let
// the two paths drift, silently.

// imageFileResolver reads image bytes the way the ingestion pipeline does.
// The URL decides the transport, and getting it wrong is not a soft failure: a
// provider:// or resource:// reference must never reach the HTTP downloader.
type imageFileResolver struct {
	tenantRepo      interfaces.TenantRepository
	kbService       interfaces.KnowledgeBaseService
	resourceCatalog interfaces.ResourceCatalog
	storageResolver interfaces.StorageBackendResolver
	fileSvc         interfaces.FileService
}

func (r imageFileResolver) resolveFileService(
	ctx context.Context, payload types.ImageMultimodalPayload,
) interfaces.FileService {
	tenant, err := r.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil || tenant == nil {
		logger.Warnf(ctx, "[ImageFile] GetTenantByID failed: tenant=%d err=%v", payload.TenantID, err)
		return r.fileSvc
	}

	backendID, _, _ := types.ParseStorageBackendPath(payload.ImageURL)
	provider := types.ParseProviderScheme(payload.ImageURL)
	// A resource:// reference carries no provider/backend in the URL itself; the
	// authoritative backend lives on the stored resource record. Using the KB's
	// currently configured backend here would break reads when the resource was
	// stored on a different backend (multi-backend / post-migration).
	if _, isResourceRef := types.ParseResourcePath(payload.ImageURL); isResourceRef && r.resourceCatalog != nil {
		if resource, resErr := r.resourceCatalog.Resolve(ctx, payload.ImageURL); resErr != nil {
			logger.Warnf(ctx, "[ImageFile] resolve resource reference failed: url=%s err=%v", payload.ImageURL, resErr)
		} else if resource != nil {
			backendID = resource.StorageBackendID
			provider = strings.ToLower(strings.TrimSpace(resource.Provider))
		}
	}
	if provider == "" {
		kb, kbErr := r.kbService.GetKnowledgeBaseByIDOnly(ctx, payload.KnowledgeBaseID)
		if kbErr != nil {
			logger.Warnf(ctx,
				"[ImageFile] GetKnowledgeBaseByIDOnly failed: kb=%s err=%v",
				payload.KnowledgeBaseID, kbErr)
		} else if kb != nil {
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
	logger.Infof(ctx, "[ImageFile] resolving file service: tenant=%d provider=%q imageURL=%s",
		payload.TenantID, provider, payload.ImageURL)
	fileSvc, _, svcErr := r.storageResolver.ResolveFileService(ctx, tenant, backendID, provider, baseDir)
	if svcErr != nil {
		logger.Warnf(ctx,
			"[ImageFile] resolve file service failed (falling back to default): tenant=%d provider=%s err=%v",
			payload.TenantID, provider, svcErr)
		return r.fileSvc
	}
	return fileSvc
}

// ReadImageBytes loads the image bytes for a multimodal payload.
//   - For provider:// URLs (local://, minio://, s3://, cos://, ...) it reads via
//     the resolved FileService and NEVER falls back to HTTP — handing a
//     provider:// URL to the HTTP downloader is what caused issue #1282.
//   - For legacy in-flight payloads with ImageLocalPath set, it tries the local
//     file before falling back to the URL.
//   - For plain http(s):// URLs it uses the SSRF-safe downloader.
func (r imageFileResolver) ReadImageBytes(
	ctx context.Context, payload types.ImageMultimodalPayload,
) ([]byte, error) {
	_, isResourceRef := types.ParseResourcePath(payload.ImageURL)
	if isResourceRef || types.ParseProviderScheme(payload.ImageURL) != "" {
		fileSvc := r.resolveFileService(ctx, payload)
		if fileSvc == nil {
			return nil, fmt.Errorf("no file service available for %s", payload.ImageURL)
		}
		reader, err := fileSvc.GetFile(ctx, payload.ImageURL)
		if err != nil {
			return nil, fmt.Errorf("file service get %s: %w", payload.ImageURL, err)
		}
		defer func() { _ = reader.Close() }()
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", payload.ImageURL, err)
		}
		return data, nil
	}

	if payload.ImageLocalPath != "" {
		data, err := os.ReadFile(payload.ImageLocalPath)
		if err == nil {
			return data, nil
		}
		logger.Warnf(ctx, "[ImageFile] Local file %s not available (%v), falling back to URL",
			payload.ImageLocalPath, err)
	}

	data, err := downloadImageFromURL(payload.ImageURL)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", payload.ImageURL, err)
	}
	return data, nil
}

// decodeChunkImageInfos parses the ImageInfo column of an image chunk, which
// holds a JSON array because a chunk can reference more than one image. A
// malformed value is not worth aborting over — the worst case is one image
// that does not get backfilled.
func decodeChunkImageInfos(raw string) []types.ImageInfo {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(raw), &infos); err != nil {
		return nil
	}
	return infos
}

// imageVectorLocation identifies where an image_vector child chunk belongs, so
// the ingestion path (which has a full task payload) and the backfill path
// (which reconstructs it from stored chunks) can share writeImageVectorChunk.
type imageVectorLocation struct {
	TenantID        uint64
	KnowledgeID     string
	KnowledgeBaseID string
	ParentChunkID   string
}

// writeImageVectorChunk persists an image_vector child chunk and indexes the
// image pixels so a text query can recall the picture. The content stays a
// markdown image reference so it still renders as an image downstream — which
// also means the content is NOT the source of the vector, so every re-embed
// path must skip this chunk type. A failure to index leaves the chunk stored
// but un-indexed on purpose: a later re-index picks it up, and the OCR /
// caption chunks still make the image searchable.
func writeImageVectorChunk(
	ctx context.Context,
	chunkService interfaces.ChunkService,
	engine imageVectorIndexer,
	kb *types.KnowledgeBase,
	embedder embedding.Embedder,
	loc imageVectorLocation,
	imageInfo types.ImageInfo,
	imgBytes []byte,
) error {
	imageURL := strings.TrimSpace(imageInfo.URL)
	if imageURL == "" {
		imageURL = strings.TrimSpace(imageInfo.OriginalURL)
	}
	imageInfoJSON, err := json.Marshal([]types.ImageInfo{imageInfo})
	if err != nil {
		return fmt.Errorf("marshal image info: %w", err)
	}

	chunk := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        loc.TenantID,
		KnowledgeID:     loc.KnowledgeID,
		KnowledgeBaseID: loc.KnowledgeBaseID,
		Content:         fmt.Sprintf("![image](%s)", imageURL),
		ChunkType:       types.ChunkTypeImageVector,
		ParentChunkID:   loc.ParentChunkID,
		IsEnabled:       true,
		Flags:           types.ChunkFlagRecommended,
		ImageInfo:       string(imageInfoJSON),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := chunkService.CreateChunks(ctx, []*types.Chunk{chunk}); err != nil {
		return fmt.Errorf("create image vector chunk: %w", err)
	}

	if err := indexChunksWithEnvelope(ctx, engine, kb, embedder, []*types.IndexInfo{{
		Content:         chunk.Content,
		SourceID:        chunk.ID,
		SourceType:      types.ChunkSourceType,
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		// Every engine stores this verbatim and retrieval filters on
		// is_enabled = true, so leaving it unset made image vectors invisible.
		IsEnabled:  chunk.IsEnabled,
		Modality:   types.EmbeddingModalityImage,
		ImageBytes: imgBytes,
	}}); err != nil {
		return fmt.Errorf("index image vector chunk %s: %w", chunk.ID, err)
	}

	dbChunk, err := chunkService.GetChunkByIDOnly(ctx, chunk.ID)
	if err != nil {
		return fmt.Errorf("fetch image vector chunk %s for status update: %w", chunk.ID, err)
	}
	dbChunk.Status = int(types.ChunkStatusIndexed)
	if err := chunkService.UpdateChunk(ctx, dbChunk); err != nil {
		return fmt.Errorf("update image vector chunk %s status: %w", chunk.ID, err)
	}

	logger.Infof(ctx, "[ImageVector] Indexed image vector chunk %s (%d bytes) for %s",
		chunk.ID, len(imgBytes), imageURL)
	return nil
}
