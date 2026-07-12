package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const parseArtifactCacheVersion = "v1"

func (s *knowledgeService) readDocumentCached(ctx context.Context, reader interfaces.DocReader, req *types.ReadRequest) (*types.ReadResult, error) {
	// URL resources may change without their URL changing. Only byte-addressed
	// uploads are safe to reuse here.
	if s.redisClient == nil || len(req.FileContent) == 0 {
		return s.callDocReaderWithTimeout(ctx, reader, req)
	}
	config, _ := json.Marshal(struct {
		FileType  string            `json:"file_type"`
		Title     string            `json:"title"`
		Engine    string            `json:"engine"`
		Overrides map[string]string `json:"overrides"`
	}{req.FileType, req.Title, req.ParserEngine, req.ParserEngineOverrides})
	h := sha256.New()
	_, _ = h.Write(req.FileContent)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(config)
	key := "weknora:artifact:parse:" + parseArtifactCacheVersion + ":" + hex.EncodeToString(h.Sum(nil))
	if raw, err := s.redisClient.Get(ctx, key).Bytes(); err == nil {
		var result types.ReadResult
		if json.Unmarshal(raw, &result) == nil {
			return &result, nil
		}
	}
	result, err := s.callDocReaderWithTimeout(ctx, reader, req)
	if err != nil || result == nil || result.Error != "" {
		return result, err
	}
	if raw, marshalErr := json.Marshal(result); marshalErr == nil {
		_ = s.redisClient.Set(ctx, key, raw, artifactCacheTTL).Err()
	}
	return result, nil
}
