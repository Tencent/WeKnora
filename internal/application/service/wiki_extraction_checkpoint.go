package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Progress belongs to the durable pending operation, not to a worker or Redis
// lease. Successful consumption/deletion removes it along with that operation.
type wikiExtractionProgress struct {
	Version       int                        `json:"version"`
	Fingerprint   string                     `json:"fingerprint"`
	Total         int                        `json:"total"`
	Segments      map[string]json.RawMessage `json:"segments"`
	PreviousSlugs *string                    `json:"previous_slugs,omitempty"`
	Mapped        *wikiMappedDocument        `json:"mapped,omitempty"`
	mu            sync.Mutex
	persisted     json.RawMessage
}

type wikiSegmentExtractor func(context.Context, string) (combinedExtraction, error)

func wikiExtractionDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (s *wikiIngestService) resumableWikiExtraction(
	ctx context.Context, model chat.Chat, op *WikiPendingOp, tenantID uint64,
	recipe map[string]string, extract wikiSegmentExtractor,
) (wikiSegmentExtractor, error) {
	if op == nil {
		return nil, errors.New("wiki extraction: missing operation")
	}
	store, ok := s.pendingRepo.(interfaces.TaskPendingOpsPayloadCheckpointer)
	if op.dbID != 0 && !ok {
		return nil, errors.New("wiki extraction: repository cannot persist progress")
	}
	// Include the complete input and effective extraction settings. Changing the
	// concurrency alone does not invalidate completed results.
	segments := splitWikiExtractionDocument(recipe["Content"])
	layout := make([]string, len(segments))
	for i, segment := range segments {
		layout[i] = wikiExtractionDigest([]byte(segment))
	}
	stableRecipe := make(map[string]string, len(recipe))
	for k, v := range recipe {
		if k != "PreviousSlugs" {
			stableRecipe[k] = v
		}
	}
	encoded, err := json.Marshal(struct {
		Version            int
		ModelID, ModelName string
		Recipe             map[string]string
		Layout             []string
		MaxTokens          int
	}{2, model.GetModelID(), model.GetModelName(), stableRecipe, layout, wikiLLMMaxTokens})
	if err != nil {
		return nil, err
	}
	fingerprint := wikiExtractionDigest(encoded)
	if op.Extraction == nil {
		op.Extraction = &wikiExtractionProgress{}
	}
	progress := op.Extraction
	progress.mu.Lock()
	if len(progress.persisted) == 0 {
		progress.persisted = append(json.RawMessage(nil), op.dbPayload...)
	}
	if progress.Version == 1 && len(progress.Segments) > 0 {
		legacy, _ := json.Marshal(struct {
			Version            int
			ModelID, ModelName string
			Recipe             map[string]string
			Layout             []string
			MaxTokens          int
		}{1, model.GetModelID(), model.GetModelName(), recipe, layout, wikiLLMMaxTokens})
		if progress.Fingerprint != wikiExtractionDigest(legacy) {
			progress.mu.Unlock()
			return nil, errors.New("wiki extraction: legacy checkpoint input changed; preserving saved blocks")
		}
		progress.Version = 2
		progress.Fingerprint = fingerprint
		previous := recipe["PreviousSlugs"]
		progress.PreviousSlugs = &previous
	}
	if progress.Version != 2 || progress.Fingerprint != fingerprint {
		progress.Version = 2
		progress.Fingerprint = fingerprint
		progress.Total = len(segments)
		progress.Segments = make(map[string]json.RawMessage)
		progress.Mapped = nil
		previous := recipe["PreviousSlugs"]
		progress.PreviousSlugs = &previous
	}
	if progress.Segments == nil {
		progress.Segments = make(map[string]json.RawMessage)
	}
	if progress.PreviousSlugs != nil {
		recipe["PreviousSlugs"] = *progress.PreviousSlugs
	}
	resumed := len(progress.Segments)
	progress.mu.Unlock()
	logger.Infof(ctx, "wiki extraction: knowledge=%s resume=%d/%d", op.KnowledgeID, resumed, progress.Total)
	return func(ctx context.Context, segment string) (combinedExtraction, error) {
		key := wikiExtractionDigest([]byte(segment))
		progress.mu.Lock()
		cached, found := progress.Segments[key]
		progress.mu.Unlock()
		if found {
			var result combinedExtraction
			if err := json.Unmarshal(cached, &result); err != nil {
				return result, fmt.Errorf("read wiki extraction checkpoint: %w", err)
			}
			return result, nil
		}
		result, err := extract(ctx, segment)
		if err != nil {
			return combinedExtraction{}, err
		}
		data, err := json.Marshal(result)
		if err != nil {
			return combinedExtraction{}, err
		}
		// Persist each validated completion before reporting success. Serialize
		// advances from concurrent segments; CAS rejects a deleted or newer row.
		progress.mu.Lock()
		defer progress.mu.Unlock()
		segments := make(map[string]json.RawMessage, len(progress.Segments)+1)
		for k, v := range progress.Segments {
			segments[k] = v
		}
		segments[key] = data
		next := &wikiExtractionProgress{Version: 2, Fingerprint: fingerprint, Total: progress.Total, Segments: segments, PreviousSlugs: progress.PreviousSlugs, Mapped: progress.Mapped}
		nextOp := *op
		nextOp.Extraction = next
		payload, err := json.Marshal(nextOp)
		if err != nil {
			return combinedExtraction{}, err
		}
		if op.dbID != 0 {
			// Cancellation must not discard a response already returned and validated.
			saveCtx, cancel := wikiIngestCleanupContext(ctx)
			defer cancel()
			updated, err := store.CompareAndSwapPayload(saveCtx, tenantID, op.dbID, progress.persisted, payload)
			if err != nil {
				return combinedExtraction{}, fmt.Errorf("save wiki extraction checkpoint: %w", err)
			}
			if !updated {
				return combinedExtraction{}, errors.New("wiki extraction checkpoint: operation deleted or changed")
			}
		}
		progress.Segments = segments
		progress.persisted = payload
		logger.Infof(ctx, "wiki extraction: knowledge=%s saved=%d/%d", op.KnowledgeID, len(segments), progress.Total)
		return result, nil
	}, nil
}
