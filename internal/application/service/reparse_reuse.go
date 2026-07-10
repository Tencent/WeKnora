package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	svcretriever "github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	vlmMultimodalPromptVersion = "vlm_multimodal_v1"
	graphExtractPromptVersion  = "graph_extract_v1"
	wikiMapPromptVersion       = "wiki_map_v1"
	documentParsePromptVersion = "document_parse_v1"
	summaryPromptVersion       = "summary_v1"
	questionsPromptVersion     = "questions_v1"
)

var stableChunkNamespace = uuid.MustParse("7d26a815-72ec-4c95-9003-fffe6dfb6f27")

var (
	markdownImageDestinationPattern = regexp.MustCompile(`(!\[[^\]]*\]\()\s*[^)\s]+([^)]*\))`)
	htmlImageSourcePattern          = regexp.MustCompile(`(?i)(<img\b[^>]*\bsrc=["'])[^"']+(["'][^>]*>)`)
	imageXMLURLPattern              = regexp.MustCompile(`(?i)(<image\b[^>]*\burl=["'])[^"']+(["'][^>]*>)`)
)

const maxIndexSourceIDLength = 64

func normalizeCacheText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = canonicalizeImageReferences(s)
	return strings.TrimSpace(s)
}

// canonicalizeImageReferences removes volatile storage destinations while
// preserving the surrounding Markdown/HTML and alt text. ImageResolver may
// persist identical bytes under a fresh local://, minio://, ... URL on every
// parse; those transport URLs must not change chunk identity or LLM cache keys.
// Image semantics are still invalidated independently by the byte hash used by
// the OCR/caption cache, and changed OCR/caption text changes summary inputs.
func canonicalizeImageReferences(s string) string {
	s = markdownImageDestinationPattern.ReplaceAllString(s, `${1}[image]${2}`)
	s = htmlImageSourcePattern.ReplaceAllString(s, `${1}[image]${2}`)
	return imageXMLURLPattern.ReplaceAllString(s, `${1}[image]${2}`)
}

func sha256Hex(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sha256BytesHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func stableChunkID(parts ...string) string {
	return uuid.NewSHA1(stableChunkNamespace, []byte(strings.Join(parts, "\x00"))).String()
}

func stableContentHash(content string) string {
	return sha256Hex(normalizeCacheText(content))
}

func modelIdentity(id, name string) string {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id != "" && name != "" {
		return id + ":" + name
	}
	if id != "" {
		return id
	}
	return name
}

// modelCacheFingerprint returns a stable, secret-safe fingerprint of the
// effective inference configuration. Model IDs alone are insufficient because
// users can edit provider/base URL/model parameters in place.
func modelCacheFingerprint(
	ctx context.Context,
	modelService interfaces.ModelService,
	modelID, fallbackName string,
) string {
	fallback := sha256Hex("model_fallback_v1", modelIdentity(modelID, fallbackName))
	if modelService == nil || strings.TrimSpace(modelID) == "" {
		return fallback
	}
	model, err := modelService.GetModelByID(ctx, modelID)
	if err != nil || model == nil {
		logger.Warnf(ctx, "cache model fingerprint fallback model_id=%s: %v", modelID, err)
		return fallback
	}
	// Deliberately exclude APIKey/AppSecret. Credential rotation does not
	// change model semantics, and no credential material should influence a
	// persisted lookup key. Routing/configuration fields remain included.
	payload := map[string]any{
		"id":                   model.ID,
		"name":                 model.Name,
		"type":                 model.Type,
		"source":               model.Source,
		"base_url":             model.Parameters.BaseURL,
		"interface_type":       model.Parameters.InterfaceType,
		"embedding_parameters": model.Parameters.EmbeddingParameters,
		"parameter_size":       model.Parameters.ParameterSize,
		"provider":             model.Parameters.Provider,
		"extra_config":         model.Parameters.ExtraConfig,
		"custom_headers":       model.Parameters.CustomHeaders,
		"supports_vision":      model.Parameters.SupportsVision,
		"app_id":               model.Parameters.AppID,
	}
	return sha256Hex("model_config_v1", stableJSON(payload))
}

func stableJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return string(data)
}

func stableJSONHash(v any) string {
	return sha256Hex(stableJSON(v))
}

func embeddingCacheKey(
	tenantID uint64, embedder embedding.Embedder, modelFingerprint, content string,
) (string, string) {
	normalized := svcretriever.SanitizeForEmbedding(context.Background(), normalizeCacheText(content))
	contentHash := sha256Hex(normalized)
	key := sha256Hex(
		"embedding",
		fmt.Sprintf("tenant:%d", tenantID),
		contentHash,
		modelFingerprint,
		fmt.Sprintf("dim:%d", embedder.GetDimensions()),
	)
	return key, contentHash
}

func imageMultimodalCacheKey(
	tenantID uint64, imageHash, modelFingerprint, artifactType, promptHash, sourceType string,
) string {
	return sha256Hex(
		"image_multimodal",
		fmt.Sprintf("tenant:%d", tenantID),
		imageHash,
		modelFingerprint,
		artifactType,
		promptHash,
		vlmMultimodalPromptVersion,
		strings.TrimSpace(sourceType),
	)
}

func graphExtractionCacheKey(tenantID uint64, chunkHash, modelFingerprint, configHash string) string {
	return sha256Hex(
		"graph_extract",
		fmt.Sprintf("tenant:%d", tenantID),
		chunkHash,
		modelFingerprint,
		configHash,
		graphExtractPromptVersion,
	)
}

func wikiMapCacheKey(tenantID uint64, contentHash, modelFingerprint, configHash string) string {
	return sha256Hex(
		"wiki_map",
		fmt.Sprintf("tenant:%d", tenantID),
		contentHash,
		modelFingerprint,
		configHash,
		wikiMapPromptVersion,
	)
}

func artifactCacheKey(
	tenantID uint64,
	artifactType, contentHash, modelFingerprint, configHash, promptVersion string,
) string {
	return sha256Hex(
		"reparse_artifact",
		fmt.Sprintf("tenant:%d", tenantID),
		artifactType,
		contentHash,
		modelFingerprint,
		configHash,
		promptVersion,
	)
}

func encodeArtifact(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeArtifact(data []byte, out any) error {
	if len(data) == 0 {
		return fmt.Errorf("empty artifact payload")
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func getCachedArtifact(
	ctx context.Context,
	repo interfaces.ReparseArtifactCacheRepository,
	tenantID uint64,
	cacheKey string,
	out any,
) (bool, error) {
	if repo == nil {
		return false, nil
	}
	entry, err := repo.Get(ctx, tenantID, cacheKey)
	if err != nil || entry == nil {
		return false, err
	}
	if err := decodeArtifact(entry.ResultData, out); err != nil {
		return false, err
	}
	return true, nil
}

func putCachedArtifact(
	ctx context.Context,
	repo interfaces.ReparseArtifactCacheRepository,
	entry *types.ReparseArtifactCache,
	value any,
) error {
	if repo == nil || entry == nil {
		return nil
	}
	data, err := encodeArtifact(value)
	if err != nil {
		return err
	}
	entry.ResultData = data
	return repo.Upsert(ctx, entry)
}

func chunkSetHash(chunks []*types.Chunk) string {
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf(
			"%09d:%s:%s", chunk.ChunkIndex, chunk.ID, stableContentHash(chunk.Content),
		))
	}
	sort.Strings(parts)
	return sha256Hex(parts...)
}

func isCacheableReadResult(result *types.ReadResult) bool {
	if result == nil || result.Error != "" || result.IsAudio || len(result.AudioData) > 0 {
		return false
	}
	for _, ref := range result.ImageRefs {
		if len(ref.ImageData) > 0 || strings.TrimSpace(ref.StorageKey) != "" {
			continue
		}
		original := strings.TrimSpace(ref.OriginalRef)
		if strings.HasPrefix(original, "http://") || strings.HasPrefix(original, "https://") ||
			strings.HasPrefix(original, "data:") {
			continue
		}
		// A local temp-file-only image would disappear with the DocReader
		// workspace, so caching this result would produce broken images.
		return false
	}
	return true
}

func marshalJSONRaw(v any) types.JSON {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return types.JSON(data)
}

func unmarshalEmbedding(raw types.JSON) ([]float32, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []float32
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func buildCachedEmbeddings(
	ctx context.Context,
	repo interfaces.EmbeddingCacheRepository,
	tenantID uint64,
	embedder embedding.Embedder,
	modelFingerprint string,
	indexInfoList []*types.IndexInfo,
) (map[string][]float32, int, int, error) {
	if repo == nil || embedder == nil || len(indexInfoList) == 0 {
		return nil, 0, len(indexInfoList), nil
	}

	embeddingMap := make(map[string][]float32, len(indexInfoList))
	misses := make([]*types.IndexInfo, 0)
	missKeys := make([]string, 0)
	missHashes := make([]string, 0)

	for _, indexInfo := range indexInfoList {
		key, contentHash := embeddingCacheKey(tenantID, embedder, modelFingerprint, indexInfo.Content)
		entry, err := repo.Get(ctx, tenantID, key)
		if err != nil {
			logger.Warnf(ctx, "embedding cache lookup failed key=%s: %v", key, err)
			misses = append(misses, indexInfo)
			missKeys = append(missKeys, key)
			missHashes = append(missHashes, contentHash)
			continue
		}
		if entry == nil {
			misses = append(misses, indexInfo)
			missKeys = append(missKeys, key)
			missHashes = append(missHashes, contentHash)
			continue
		}
		vec, err := unmarshalEmbedding(entry.Embedding)
		if err != nil || len(vec) != embedder.GetDimensions() {
			logger.Warnf(ctx, "embedding cache entry invalid key=%s dim=%d err=%v", key, len(vec), err)
			misses = append(misses, indexInfo)
			missKeys = append(missKeys, key)
			missHashes = append(missHashes, contentHash)
			continue
		}
		embeddingMap[indexInfo.SourceID] = vec
		indexInfo.PrecomputedEmbedding = vec
	}

	if len(misses) == 0 {
		return embeddingMap, len(indexInfoList), 0, nil
	}

	texts := make([]string, 0, len(misses))
	for _, info := range misses {
		texts = append(texts, svcretriever.SanitizeForEmbedding(ctx, normalizeCacheText(info.Content)))
	}
	vectors, err := svcretriever.BatchEmbedWithBackoff(ctx, embedder, texts)
	if err != nil {
		return nil, 0, len(misses), err
	}
	if len(vectors) != len(misses) {
		return nil, 0, len(misses), fmt.Errorf(
			"embedding result count mismatch: got %d want %d", len(vectors), len(misses),
		)
	}
	for i, info := range misses {
		vec := vectors[i]
		if len(vec) != embedder.GetDimensions() {
			return nil, 0, len(misses), fmt.Errorf(
				"embedding dimension mismatch at %d: got %d want %d", i, len(vec), embedder.GetDimensions(),
			)
		}
		embeddingMap[info.SourceID] = vec
		info.PrecomputedEmbedding = vec
		entry := &types.EmbeddingCache{
			CacheKey:    missKeys[i],
			TenantID:    tenantID,
			ContentHash: missHashes[i],
			ModelID:     embedder.GetModelID(),
			ModelName:   embedder.GetModelName(),
			Dimensions:  embedder.GetDimensions(),
			Embedding:   marshalJSONRaw(vec),
		}
		if err := repo.Upsert(ctx, entry); err != nil {
			logger.Warnf(ctx, "embedding cache upsert failed key=%s: %v", missKeys[i], err)
		}
	}
	return embeddingMap, len(indexInfoList) - len(misses), len(misses), nil
}

type chunkDiffPlan struct {
	toCreate []*types.Chunk
	toUpdate []*types.Chunk
	toDelete []*types.Chunk
	reused   map[string]bool
}

func planChunkDiff(existing []*types.Chunk, desired []*types.Chunk, now time.Time) chunkDiffPlan {
	existingByID := make(map[string]*types.Chunk, len(existing))
	for _, chunk := range existing {
		if chunk == nil || chunk.ID == "" {
			continue
		}
		existingByID[chunk.ID] = chunk
	}

	desiredIDs := make(map[string]bool, len(desired))
	plan := chunkDiffPlan{reused: make(map[string]bool)}
	for _, chunk := range desired {
		if chunk == nil || chunk.ID == "" {
			continue
		}
		desiredIDs[chunk.ID] = true
		if chunk.ContentHash == "" {
			chunk.ContentHash = stableContentHash(chunk.Content)
		}
		old := existingByID[chunk.ID]
		if old == nil {
			if chunk.CreatedAt.IsZero() {
				chunk.CreatedAt = now
			}
			chunk.UpdatedAt = now
			plan.toCreate = append(plan.toCreate, chunk)
			continue
		}
		chunk.SeqID = old.SeqID
		chunk.CreatedAt = old.CreatedAt
		if chunk.CreatedAt.IsZero() {
			chunk.CreatedAt = now
		}
		plan.reused[chunk.ID] = true
		if samePersistedChunk(old, chunk) {
			chunk.UpdatedAt = old.UpdatedAt
			continue
		}
		chunk.UpdatedAt = now
		plan.toUpdate = append(plan.toUpdate, chunk)
	}

	for _, old := range existing {
		if old == nil || old.ID == "" {
			continue
		}
		if !desiredIDs[old.ID] {
			plan.toDelete = append(plan.toDelete, old)
		}
	}
	return plan
}

func samePersistedChunk(a, b *types.Chunk) bool {
	if a == nil || b == nil {
		return false
	}
	return a.TenantID == b.TenantID &&
		a.KnowledgeID == b.KnowledgeID &&
		a.KnowledgeBaseID == b.KnowledgeBaseID &&
		a.Content == b.Content &&
		a.ChunkIndex == b.ChunkIndex &&
		a.IsEnabled == b.IsEnabled &&
		a.Flags == b.Flags &&
		a.Status == b.Status &&
		a.StartAt == b.StartAt &&
		a.EndAt == b.EndAt &&
		a.PreChunkID == b.PreChunkID &&
		a.NextChunkID == b.NextChunkID &&
		a.ChunkType == b.ChunkType &&
		a.ParentChunkID == b.ParentChunkID &&
		a.ContentHash == b.ContentHash &&
		a.ImageInfo == b.ImageInfo
}

func chunkIDs(chunks []*types.Chunk) []string {
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk != nil && chunk.ID != "" {
			ids = append(ids, chunk.ID)
		}
	}
	return ids
}

func indexInfoSourceIDs(items []*types.IndexInfo) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && item.SourceID != "" {
			ids = append(ids, item.SourceID)
		}
	}
	return ids
}

func stableGeneratedQuestions(chunkID string, questions []string) []types.GeneratedQuestion {
	out := make([]types.GeneratedQuestion, 0, len(questions))
	for i, question := range questions {
		question = strings.TrimSpace(question)
		if question == "" {
			continue
		}
		out = append(out, types.GeneratedQuestion{
			// source_id is VARCHAR(64). A chunk UUID plus a UUID question ID
			// would be 73 bytes (36 + 1 + 36), so keep a collision-resistant
			// 96-bit content address here: 36 + 1 + 24 = 61 bytes.
			ID: sha256Hex(
				"generated_question", chunkID, fmt.Sprintf("ordinal:%d", i), normalizeCacheText(question),
			)[:24],
			Question: question,
		})
	}
	return out
}

func generatedQuestionSourceID(chunkID, questionID string) string {
	id := fmt.Sprintf("%s-%s", chunkID, questionID)
	if len(id) > maxIndexSourceIDLength {
		// Old failed attempts may have persisted UUID-sized generated-question
		// IDs even though PostgreSQL rejected their index rows.
		return ""
	}
	return id
}

func generatedQuestionSourceIDs(chunk *types.Chunk) []string {
	if chunk == nil {
		return nil
	}
	meta, err := chunk.DocumentMetadata()
	if err != nil || meta == nil {
		return nil
	}
	ids := make([]string, 0, len(meta.GeneratedQuestions))
	for _, question := range meta.GeneratedQuestions {
		if id := generatedQuestionSourceID(chunk.ID, question.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func staleSourceIDs(previous, desired []string) []string {
	keep := make(map[string]bool, len(desired))
	for _, id := range desired {
		keep[id] = true
	}
	stale := make([]string, 0)
	for _, id := range previous {
		if id != "" && !keep[id] {
			stale = append(stale, id)
		}
	}
	return stale
}

type cachedWikiMapResult struct {
	KnowledgeID string                 `json:"knowledge_id"`
	DocTitle    string                 `json:"doc_title"`
	Summary     string                 `json:"summary"`
	Pages       []types.WikiLogPageRef `json:"pages"`
	MapStats    types.JSONMap          `json:"map_stats"`
}
