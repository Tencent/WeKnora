package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func documentChunkingReuseFingerprint(kb *types.KnowledgeBase, options ProcessChunksOptions) string {
	if kb == nil {
		return ""
	}
	return types.CacheFingerprint("document-chunking", map[string]any{
		"chunking_config": kb.ChunkingConfig,
		"parent_child":    len(options.ParentChunks) > 0,
	})
}

func docParseContentHash(fileContent []byte) string {
	sum := sha256.Sum256(fileContent)
	return hex.EncodeToString(sum[:])
}

func docParseConfigHash(req *types.ReadRequest) string {
	if req == nil {
		return types.CacheFingerprint("doc-parse-config", nil)
	}
	return types.CacheFingerprint("doc-parse-config", map[string]any{
		"schema":                  types.DocParseCacheSchemaV1,
		"file_name":               strings.TrimSpace(req.FileName),
		"file_type":               strings.TrimSpace(req.FileType),
		"title":                   strings.TrimSpace(req.Title),
		"parser_engine":           strings.TrimSpace(req.ParserEngine),
		"parser_engine_overrides": req.ParserEngineOverrides,
	})
}

func docParseCacheKey(contentHash, parserEngine, configHash string) string {
	return types.CacheFingerprint("doc-parse-result", map[string]any{
		"schema":        types.DocParseCacheSchemaV1,
		"content_hash":  strings.TrimSpace(contentHash),
		"parser_engine": strings.TrimSpace(parserEngine),
		"config_hash":   strings.TrimSpace(configHash),
	})
}

func reuseRatio(reused, total int) float64 {
	if total <= 0 || reused <= 0 {
		return 0
	}
	return float64(reused) / float64(total)
}

func readResultFromDocParseCache(cache *types.DocParseCache) (*types.ReadResult, error) {
	if cache == nil || len(cache.Payload) == 0 {
		return nil, nil
	}
	var result types.ReadResult
	if err := json.Unmarshal(cache.Payload, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func reusableDocumentChunkType(chunkType types.ChunkType) bool {
	return chunkType == types.ChunkTypeText || chunkType == types.ChunkTypeParentText
}

func reusableChunkMap(existing []*types.Chunk) map[string][]*types.Chunk {
	byKey := make(map[string][]*types.Chunk)
	for _, chunk := range existing {
		if chunk == nil || chunk.ContentHash == "" || !reusableDocumentChunkType(chunk.ChunkType) {
			continue
		}
		key := chunk.ChunkType + "\x00" + chunk.ContentHash
		byKey[key] = append(byKey[key], chunk)
	}
	return byKey
}

func popReusableChunk(byKey map[string][]*types.Chunk, chunk *types.Chunk) *types.Chunk {
	if chunk == nil || chunk.ContentHash == "" {
		return nil
	}
	key := chunk.ChunkType + "\x00" + chunk.ContentHash
	matches := byKey[key]
	if len(matches) == 0 {
		return nil
	}
	reused := matches[0]
	if len(matches) == 1 {
		delete(byKey, key)
	} else {
		byKey[key] = matches[1:]
	}
	return reused
}

func remapChunkRelationships(chunks []*types.Chunk, idRemap map[string]string) {
	if len(idRemap) == 0 {
		return
	}
	remap := func(id string) string {
		if id == "" {
			return ""
		}
		if mapped, ok := idRemap[id]; ok {
			return mapped
		}
		return id
	}
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		chunk.ParentChunkID = remap(chunk.ParentChunkID)
		chunk.PreChunkID = remap(chunk.PreChunkID)
		chunk.NextChunkID = remap(chunk.NextChunkID)
	}
}

func generatedQuestionSourceIDs(chunk *types.Chunk) []string {
	if chunk == nil {
		return nil
	}
	meta, err := chunk.DocumentMetadata()
	if err != nil || meta == nil || len(meta.GeneratedQuestions) == 0 {
		return nil
	}
	sourceIDs := make([]string, 0, len(meta.GeneratedQuestions))
	for _, question := range meta.GeneratedQuestions {
		if question.ID == "" {
			continue
		}
		sourceIDs = append(sourceIDs, fmt.Sprintf("%s-%s", chunk.ID, question.ID))
	}
	return sourceIDs
}
