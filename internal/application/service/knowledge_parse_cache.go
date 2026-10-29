package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var errParserCacheAdmission = errors.New("parser artifact cannot be persisted")

// readDocumentWithCache stores parser bytes before image storage assigns current references.
func (s *knowledgeService) readDocumentWithCache(ctx context.Context, reader interfaces.DocReader, req *types.ReadRequest, knowledge *types.Knowledge) (*types.ReadResult, error) {
	live := func(ctx context.Context) (*types.ReadResult, error) {
		result, err := s.callDocReaderWithTimeout(ctx, reader, req)
		if err == nil && result == nil {
			return nil, fmt.Errorf("docreader returned nil result")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return result, err
	}
	identity := docparser.ParserCacheIdentity(reader, req)
	if !s.processingCache.Enabled() || s.processingCache.PayloadLimit("document.parse.v1") <= 0 || processingCacheBypassed(ctx) || req.URL != "" || identity == "" || knowledge == nil || knowledge.ID == "" || knowledge.TenantID == 0 {
		return live(ctx)
	}
	digest := sha256.Sum256(req.FileContent)
	input := struct {
		Digest, Name, Type, Title, Engine, Identity string
		Overrides                                   map[string]string
	}{hex.EncodeToString(digest[:]), req.FileName, req.FileType, req.Title, req.ParserEngine, identity, req.ParserEngineOverrides}
	var original *types.ReadResult
	var computed bool
	result, err := processingcache.Value(ctx, s.processingCache, knowledge.TenantID, knowledge.ID, "document.parse.v1", input, docparser.ValidateProcessingCacheArtifact, func(ctx context.Context) (*types.ReadResult, error) {
		var err error
		original, err = live(ctx)
		computed = true
		if err != nil {
			return nil, err
		}
		if original.ImageDirPath != "" && len(original.ImageRefs) == 0 {
			return nil, errParserCacheAdmission
		}
		// Marshal gives each invocation its own byte slices and canonical UTF-8 strings.
		raw, err := json.Marshal(original)
		if err != nil {
			return nil, errParserCacheAdmission
		}
		var portable types.ReadResult
		if json.Unmarshal(raw, &portable) != nil {
			return nil, errParserCacheAdmission
		}
		portable.ImageDirPath = ""
		for i := range portable.ImageRefs {
			portable.ImageRefs[i].StorageKey = ""
		}
		if docparser.ValidateProcessingCacheArtifact(&portable) != nil {
			return nil, errParserCacheAdmission
		}
		return &portable, nil
	})
	if errors.Is(err, errParserCacheAdmission) && ctx.Err() == nil {
		if computed {
			return original, nil
		}
		return live(ctx)
	}
	return result, err
}
