package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	wikiMapArtifactKind          = "wiki.document-map"
	wikiMapArtifactSchemaVersion = "wiki-document-map/v2"
	wikiMapPromptVersion         = "wiki-map-prompts/v1"
	wikiMapProducerVersion       = "wiki-map-producer/v2"
	wikiMapArtifactLease         = 5 * time.Minute
	wikiMapArtifactWait          = 6 * time.Minute
	wikiMapArtifactPoll          = 100 * time.Millisecond
	wikiMapArtifactCleanup       = 2 * time.Second
)

type wikiMapArtifactTiming struct{ Lease, Wait, Poll, Cleanup time.Duration }

func wikiMapArtifactKey(tenantID uint64, inputDigest, modelID, modelRevision, promptVersion, configDigest, producerVersion string) string {
	return artifactkey.Generate(artifactkey.KeyInput{Kind: wikiMapArtifactKind, TenantScope: fmt.Sprintf("tenant:%d", tenantID), InputDigest: inputDigest, ModelID: modelID, ModelRevision: modelRevision, PromptVersion: promptVersion, ConfigDigest: configDigest, ProducerVersion: producerVersion})
}

func (s *wikiIngestService) mapArtifactTiming() wikiMapArtifactTiming {
	t := s.wikiMapTiming
	if t.Lease <= 0 {
		t.Lease = wikiMapArtifactLease
	}
	if t.Wait <= 0 {
		t.Wait = wikiMapArtifactWait
	}
	if t.Poll <= 0 {
		t.Poll = wikiMapArtifactPoll
	}
	if t.Cleanup <= 0 {
		t.Cleanup = wikiMapArtifactCleanup
	}
	return t
}

type wikiMapCacheBypassKey struct{}
type wikiCanonicalMapKey struct{}

type wikiMapRequestCountingChat struct {
	inner    chat.Chat
	requests atomic.Int64
}

func (c *wikiMapRequestCountingChat) Chat(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	c.requests.Add(1)
	return c.inner.Chat(ctx, messages, opts)
}
func (c *wikiMapRequestCountingChat) ChatStream(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	c.requests.Add(1)
	return c.inner.ChatStream(ctx, messages, opts)
}
func (c *wikiMapRequestCountingChat) GetModelName() string { return c.inner.GetModelName() }
func (c *wikiMapRequestCountingChat) GetModelID() string   { return c.inner.GetModelID() }
func (c *wikiMapRequestCountingChat) Count() int           { return int(c.requests.Load()) }

func wikiMapCacheBypassed(ctx context.Context) bool {
	v, _ := ctx.Value(wikiMapCacheBypassKey{}).(bool)
	return v
}

func wikiCanonicalMap(ctx context.Context) bool {
	v, _ := ctx.Value(wikiCanonicalMapKey{}).(bool)
	return v
}

type wikiMapArtifactPayload struct {
	SchemaVersion string                 `json:"schema_version"`
	KnowledgeID   string                 `json:"knowledge_id"`
	DocTitle      string                 `json:"doc_title"`
	Summary       string                 `json:"summary"`
	Pages         []types.WikiLogPageRef `json:"pages"`
	Updates       []wikiMapCachedUpdate  `json:"updates"`
	MapStats      types.JSONMap          `json:"map_stats,omitempty"`
}

// wikiMapCachedUpdate is deliberately limited to additions. Retracts and
// reparse swaps depend on the live contributor set and are rebuilt on every
// run, including cache hits.
type wikiMapCachedUpdate struct {
	Slug                 string            `json:"slug"`
	Type                 string            `json:"type"`
	Item                 extractedItem     `json:"item,omitempty"`
	DocTitle             string            `json:"doc_title"`
	KnowledgeID          string            `json:"knowledge_id"`
	SourceRef            string            `json:"source_ref"`
	Language             string            `json:"language"`
	SummaryBody          string            `json:"summary_body,omitempty"`
	SummaryLine          string            `json:"summary_line,omitempty"`
	DocSummary           string            `json:"doc_summary,omitempty"`
	SourceChunks         []wikiMapChunkRef `json:"source_chunks,omitempty"`
	ResolvedSourceChunks []string          `json:"-"`
}

type wikiMapInputChunk struct {
	StableIdentity  string          `json:"stable_identity"`
	IdentityVersion string          `json:"identity_version"`
	Index           int             `json:"index"`
	Start           int             `json:"start"`
	Type            types.ChunkType `json:"type"`
	Content         string          `json:"content"`
}

type wikiMapChunkRef struct {
	StableIdentity  string `json:"stable_identity"`
	IdentityVersion string `json:"identity_version"`
}

func wikiMapSourceChunks(chunks []*types.Chunk) ([]*types.Chunk, map[string]string, map[string]wikiMapChunkRef, bool) {
	source := wikiMapConsumedChunks(chunks)
	stableToID := make(map[string]string)
	idToStable := make(map[string]wikiMapChunkRef)
	for _, chunk := range source {
		if chunk.ID == "" || chunk.StableIdentity == "" || chunk.IdentityVersion != contentkey.ChunkIdentityVersion {
			return nil, nil, nil, false
		}
		if _, duplicate := stableToID[chunk.StableIdentity]; duplicate {
			return nil, nil, nil, false
		}
		stableToID[chunk.StableIdentity] = chunk.ID
		idToStable[chunk.ID] = wikiMapChunkRef{StableIdentity: chunk.StableIdentity, IdentityVersion: chunk.IdentityVersion}
	}
	return source, stableToID, idToStable, true
}

func wikiMapConsumedChunks(chunks []*types.Chunk) []*types.Chunk {
	out := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk == nil || !chunk.IsEnabled || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if chunk.ChunkType != "" && chunk.ChunkType != types.ChunkTypeText {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func stableWikiMapChunkRefs(ids []string, idToStable map[string]wikiMapChunkRef) ([]wikiMapChunkRef, error) {
	seen := make(map[string]bool, len(ids))
	refs := make([]wikiMapChunkRef, 0, len(ids))
	for _, id := range ids {
		ref, ok := idToStable[id]
		if !ok || ref.StableIdentity == "" || ref.IdentityVersion != contentkey.ChunkIdentityVersion {
			return nil, fmt.Errorf("wiki map source chunk %q has no current stable identity", id)
		}
		if seen[ref.StableIdentity] {
			continue
		}
		seen[ref.StableIdentity] = true
		refs = append(refs, ref)
	}
	return refs, nil
}

func bindWikiMapChunkRefs(payload *wikiMapArtifactPayload, stableToID map[string]string) error {
	if payload == nil {
		return interfaces.ErrArtifactCorrupt
	}
	for i := range payload.Updates {
		seen := make(map[string]bool, len(payload.Updates[i].SourceChunks))
		resolved := make([]string, 0, len(payload.Updates[i].SourceChunks))
		for _, ref := range payload.Updates[i].SourceChunks {
			if ref.StableIdentity == "" || ref.IdentityVersion != contentkey.ChunkIdentityVersion || seen[ref.StableIdentity] {
				return fmt.Errorf("invalid or duplicate stable chunk reference %q: %w", ref.StableIdentity, interfaces.ErrArtifactCorrupt)
			}
			id, ok := stableToID[ref.StableIdentity]
			if !ok || id == "" {
				return fmt.Errorf("stable chunk reference %q is no longer active", ref.StableIdentity)
			}
			seen[ref.StableIdentity] = true
			resolved = append(resolved, id)
		}
		payload.Updates[i].ResolvedSourceChunks = resolved
		payload.Updates[i].Item.SourceChunks = append([]string(nil), resolved...)
	}
	return nil
}

func (s *wikiIngestService) computeWikiMapUncached(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, op WikiPendingOp, batchCtx *WikiBatchContext) (*docIngestResult, []SlugUpdate, error) {
	result, updates, err := s.mapOneDocument(context.WithValue(ctx, wikiMapCacheBypassKey{}, true), chatModel, payload, op, batchCtx)
	if result != nil {
		if result.MapStats == nil {
			result.MapStats = types.JSONMap{}
		}
		result.MapStats["cache_status"] = types.IngestionCacheStatusError
		result.MapStats["cache_supported"] = true
	}
	return result, updates, err
}

func (s *wikiIngestService) computeWikiMapBypass(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, op WikiPendingOp, batchCtx *WikiBatchContext, reason string) (*docIngestResult, []SlugUpdate, error) {
	result, updates, err := s.mapOneDocument(context.WithValue(ctx, wikiMapCacheBypassKey{}, true), chatModel, payload, op, batchCtx)
	if result != nil {
		if result.MapStats == nil {
			result.MapStats = types.JSONMap{}
		}
		result.MapStats["cache_status"] = types.IngestionCacheStatusNotSupported
		result.MapStats["cache_supported"] = false
		result.MapStats["cache_bypass_reason"] = reason
	}
	return result, updates, err
}

func (s *wikiIngestService) failClaimedWikiMapArtifact(ctx context.Context, tenantID uint64, key, owner, code, message string) error {
	timing := s.mapArtifactTiming()
	failCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		failCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), timing.Cleanup)
	}
	defer cancel()
	err := s.artifactRepo.Fail(failCtx, interfaces.ArtifactFailure{TenantID: tenantID, ArtifactKey: key, OwnerToken: owner, ErrorCode: code, ErrorMessage: message})
	if errors.Is(err, interfaces.ErrArtifactLostOwnership) {
		return nil
	}
	return err
}

func (s *wikiIngestService) mapOneDocumentCached(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, op WikiPendingOp, batchCtx *WikiBatchContext) (*docIngestResult, []SlugUpdate, error) {
	if s.isKnowledgeGone(ctx, payload.KnowledgeBaseID, op.KnowledgeID) {
		return s.computeWikiMapUncached(ctx, chatModel, payload, op, batchCtx)
	}
	chunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, payload.TenantID, op.KnowledgeID)
	if err != nil || len(chunks) == 0 {
		return s.computeWikiMapBypass(ctx, chatModel, payload, op, batchCtx, "source_chunks_unavailable")
	}
	sourceChunks, stableToID, idToStable, eligible := wikiMapSourceChunks(chunks)
	if !eligible || len(sourceChunks) == 0 {
		logger.Warnf(ctx, "wiki map cache: knowledge %s has missing, duplicate, or unsupported stable chunk identities; computing without cache", op.KnowledgeID)
		return s.computeWikiMapBypass(ctx, chatModel, payload, op, batchCtx, "unstable_chunk_identity")
	}
	inputChunks := make([]wikiMapInputChunk, 0, len(sourceChunks))
	for _, ch := range sourceChunks {
		inputChunks = append(inputChunks, wikiMapInputChunk{StableIdentity: ch.StableIdentity, IdentityVersion: ch.IdentityVersion, Index: ch.ChunkIndex, Start: ch.StartAt, Type: ch.ChunkType, Content: ch.Content})
	}
	sort.SliceStable(inputChunks, func(i, j int) bool {
		if inputChunks[i].Index == inputChunks[j].Index {
			return inputChunks[i].Start < inputChunks[j].Start
		}
		return inputChunks[i].Index < inputChunks[j].Index
	})
	docTitle := op.KnowledgeID
	if kn, getErr := s.knowledgeSvc.GetKnowledgeByIDOnly(ctx, op.KnowledgeID); getErr == nil && kn != nil && kn.Title != "" {
		docTitle = kn.Title
	}
	enrichedContent := reconstructEnrichedContent(ctx, s.chunkRepo, payload.TenantID, sourceChunks)
	// KnowledgeID participates because the canonical contribution contains a
	// document-owned summary slug/source ref. Stable chunk identities
	// participate because citations are rebound to ephemeral row IDs on hits.
	inputBytes, err := json.Marshal(struct {
		KnowledgeID     string              `json:"knowledge_id"`
		DocTitle        string              `json:"doc_title"`
		EnrichedContent string              `json:"enriched_content"`
		Chunks          []wikiMapInputChunk `json:"chunks"`
	}{KnowledgeID: op.KnowledgeID, DocTitle: docTitle, EnrichedContent: enrichedContent, Chunks: inputChunks})
	if err != nil {
		return nil, nil, fmt.Errorf("encode wiki map input: %w", err)
	}
	inputDigest := artifactkey.DigestBytes(inputBytes)
	modelConfig := map[string]any{"runtime_model_name": chatModel.GetModelName()}
	if s.modelService != nil {
		if model, getErr := s.modelService.GetModelByID(ctx, chatModel.GetModelID()); getErr == nil && model != nil {
			modelConfig = map[string]any{
				"name": model.Name, "source": model.Source, "provider": model.Parameters.Provider,
				"interface_type": model.Parameters.InterfaceType, "parameter_size": model.Parameters.ParameterSize,
				"remote_model_name": model.Parameters.ExtraConfig["remote_model_name"],
				"model_revision":    model.Parameters.ExtraConfig["model_revision"],
				"revision":          model.Parameters.ExtraConfig["revision"],
				"thinking_control":  model.Parameters.ExtraConfig[chat.ExtraConfigThinkingControl],
			}
		}
	}
	configDigest, err := artifactkey.DigestConfig(map[string]any{
		"language":                types.LanguageLocaleName(op.Language),
		"granularity":             batchCtx.ExtractionGranularity.Normalize(),
		"content_instructions":    batchCtx.ContentInstructions,
		"extraction_instructions": batchCtx.ExtractionInstructions,
		"max_content_runes":       maxContentForWiki,
		"model":                   modelConfig,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("digest wiki map config: %w", err)
	}
	modelID, modelName := chatModel.GetModelID(), chatModel.GetModelName()
	if modelID == "" {
		modelID = modelName
	}
	key := wikiMapArtifactKey(payload.TenantID, inputDigest, modelID, modelName, wikiMapPromptVersion, configDigest, wikiMapProducerVersion)
	timing := s.mapArtifactTiming()
	deadline := time.Now().Add(timing.Wait)
	for {
		owner, tokenErr := newArtifactOwnerToken()
		if tokenErr != nil {
			return nil, nil, tokenErr
		}
		claim, claimErr := s.artifactRepo.Claim(ctx, interfaces.ArtifactClaim{TenantID: payload.TenantID, ArtifactKey: key, ArtifactKind: wikiMapArtifactKind, InputDigest: inputDigest, ModelID: modelID, ModelRevision: modelName, PromptVersion: wikiMapPromptVersion, ConfigDigest: configDigest, ProducerVersion: wikiMapProducerVersion, OwnerToken: owner, LeaseDuration: timing.Lease})
		if claimErr != nil {
			if errors.Is(claimErr, interfaces.ErrArtifactCorrupt) {
				logger.Warnf(ctx, "wiki map cache: repository rejected corrupt artifact for knowledge %s; recomputing safely", op.KnowledgeID)
				return s.computeWikiMapUncached(ctx, chatModel, payload, op, batchCtx)
			}
			return nil, nil, fmt.Errorf("claim wiki map artifact: %w", claimErr)
		}
		switch claim.Outcome {
		case interfaces.ArtifactClaimHit:
			if s.isKnowledgeGone(ctx, payload.KnowledgeBaseID, op.KnowledgeID) {
				return s.computeWikiMapUncached(ctx, chatModel, payload, op, batchCtx)
			}
			cached, decodeErr := decodeWikiMapArtifact(claim.Artifact)
			if decodeErr != nil {
				logger.Warnf(ctx, "wiki map cache: corrupt artifact for knowledge %s; recomputing safely: %v", op.KnowledgeID, decodeErr)
				return s.computeWikiMapUncached(ctx, chatModel, payload, op, batchCtx)
			}
			if err := bindWikiMapChunkRefs(cached, stableToID); err != nil {
				logger.Warnf(ctx, "wiki map cache: cannot rebind artifact for knowledge %s; recomputing safely: %v", op.KnowledgeID, err)
				return s.computeWikiMapUncached(ctx, chatModel, payload, op, batchCtx)
			}
			return s.restoreWikiMapArtifact(ctx, chatModel, payload, op, batchCtx, cached, stableToID)
		case interfaces.ArtifactClaimBusy:
			if time.Now().After(deadline) {
				return nil, nil, fmt.Errorf("wiki map artifact busy wait timed out")
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(timing.Poll):
				continue
			}
		case interfaces.ArtifactClaimClaimed:
			return s.computeWikiMapArtifact(ctx, chatModel, payload, op, batchCtx, key, owner, idToStable)
		default:
			return nil, nil, fmt.Errorf("unknown wiki map artifact claim outcome %q", claim.Outcome)
		}
	}
}

func (s *wikiIngestService) computeWikiMapArtifact(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, op WikiPendingOp, batchCtx *WikiBatchContext, key, owner string, idToStable map[string]wikiMapChunkRef) (*docIngestResult, []SlugUpdate, error) {
	timing := s.mapArtifactTiming()
	heartbeatCtx, stop := context.WithCancel(ctx)
	var mu sync.Mutex
	var leaseErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(timing.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case now := <-ticker.C:
				if err := s.artifactRepo.RenewLease(heartbeatCtx, payload.TenantID, key, owner, now, timing.Lease); err != nil {
					mu.Lock()
					leaseErr = err
					mu.Unlock()
					return
				}
			}
		}
	}()
	countingChat := &wikiMapRequestCountingChat{inner: chatModel}
	computeCtx := context.WithValue(ctx, wikiMapCacheBypassKey{}, true)
	computeCtx = context.WithValue(computeCtx, wikiCanonicalMapKey{}, true)
	result, updates, computeErr := s.mapOneDocument(computeCtx, countingChat, payload, op, batchCtx)
	stop()
	<-done
	mu.Lock()
	lost := leaseErr
	mu.Unlock()
	if lost != nil {
		if !errors.Is(lost, interfaces.ErrArtifactLostOwnership) {
			if failErr := s.failClaimedWikiMapArtifact(ctx, payload.TenantID, key, owner, "lease_renew_failed", "wiki map artifact lease renewal failed"); failErr != nil {
				logger.Warnf(ctx, "wiki map cache: fail after lease renewal error: %v", failErr)
			}
		}
		return nil, nil, fmt.Errorf("wiki map artifact lease: %w", lost)
	}
	if computeErr != nil || result == nil {
		code := "wiki_map_failed"
		message := "wiki document map failed"
		if computeErr == nil {
			code = "wiki_map_empty"
			message = "wiki document map produced no contribution"
		}
		if err := s.failClaimedWikiMapArtifact(ctx, payload.TenantID, key, owner, code, message); err != nil {
			logger.Warnf(ctx, "wiki map cache: fail artifact: %v", err)
		}
		return result, updates, computeErr
	}
	p := wikiMapArtifactPayload{SchemaVersion: wikiMapArtifactSchemaVersion, KnowledgeID: result.KnowledgeID, DocTitle: result.DocTitle, Summary: result.Summary, Pages: result.Pages, MapStats: result.MapStats}
	if p.MapStats == nil {
		p.MapStats = types.JSONMap{}
	}
	p.MapStats["request_count"] = countingChat.Count()
	for _, u := range updates {
		if u.Type == "retract" || u.Type == "retractStale" {
			continue
		}
		refs, refErr := stableWikiMapChunkRefs(u.SourceChunks, idToStable)
		if refErr != nil {
			if failErr := s.failClaimedWikiMapArtifact(ctx, payload.TenantID, key, owner, "stable_ref_failed", "wiki map stable citation conversion failed"); failErr != nil {
				logger.Warnf(ctx, "wiki map cache: fail after stable citation conversion: %v", failErr)
			}
			return nil, nil, refErr
		}
		item := u.Item
		item.SourceChunks = nil
		p.Updates = append(p.Updates, wikiMapCachedUpdate{Slug: u.Slug, Type: u.Type, Item: item, DocTitle: u.DocTitle, KnowledgeID: u.KnowledgeID, SourceRef: u.SourceRef, Language: u.Language, SummaryBody: u.SummaryBody, SummaryLine: u.SummaryLine, SourceChunks: refs, DocSummary: u.DocSummary})
	}
	b, err := json.Marshal(p)
	if err != nil {
		if failErr := s.failClaimedWikiMapArtifact(ctx, payload.TenantID, key, owner, "encode_failed", "wiki map artifact encoding failed"); failErr != nil {
			logger.Warnf(ctx, "wiki map cache: fail after encoding error: %v", failErr)
		}
		return nil, nil, fmt.Errorf("encode wiki map artifact: %w", err)
	}
	if err := s.artifactRepo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: payload.TenantID, ArtifactKey: key, OwnerToken: owner, Payload: b, PayloadEncoding: "json", PayloadDigest: artifactkey.DigestBytes(b)}); err != nil {
		if !errors.Is(err, interfaces.ErrArtifactLostOwnership) {
			if failErr := s.failClaimedWikiMapArtifact(ctx, payload.TenantID, key, owner, "complete_failed", "wiki map artifact completion failed"); failErr != nil {
				logger.Warnf(ctx, "wiki map cache: fail after completion error: %v", failErr)
			}
		}
		return nil, nil, fmt.Errorf("complete wiki map artifact: %w", err)
	}
	stableToID := make(map[string]string, len(idToStable))
	for id, ref := range idToStable {
		stableToID[ref.StableIdentity] = id
	}
	return s.materializeWikiMapArtifact(ctx, chatModel, payload, op, batchCtx, &p, result.WikiSpan, types.IngestionCacheStatusMiss, stableToID)
}

func decodeWikiMapArtifact(a *types.DerivedArtifact) (*wikiMapArtifactPayload, error) {
	if a == nil || a.PayloadEncoding != "json" {
		return nil, interfaces.ErrArtifactCorrupt
	}
	var p wikiMapArtifactPayload
	if json.Unmarshal(a.Payload, &p) != nil || p.SchemaVersion != wikiMapArtifactSchemaVersion || p.KnowledgeID == "" {
		return nil, interfaces.ErrArtifactCorrupt
	}
	return &p, nil
}

func (s *wikiIngestService) restoreWikiMapArtifact(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, op WikiPendingOp, batchCtx *WikiBatchContext, p *wikiMapArtifactPayload, stableToID map[string]string) (*docIngestResult, []SlugUpdate, error) {
	wikiSpan := s.beginWikiSubspan(ctx, op.KnowledgeID, types.JSONMap{"language": types.LanguageLocaleName(op.Language), "knowledge_base_id": payload.KnowledgeBaseID, "cache_status": types.IngestionCacheStatusHit})
	return s.materializeWikiMapArtifact(ctx, chatModel, payload, op, batchCtx, p, wikiSpan, types.IngestionCacheStatusHit, stableToID)
}

func (s *wikiIngestService) materializeWikiMapArtifact(ctx context.Context, chatModel chat.Chat, payload WikiIngestPayload, op WikiPendingOp, batchCtx *WikiBatchContext, original *wikiMapArtifactPayload, wikiSpan *Span, cacheStatus types.IngestionCacheStatus, stableToID map[string]string) (*docIngestResult, []SlugUpdate, error) {
	// Clone before applying KB-state-dependent slug reconciliation: the stored
	// artifact remains a pure per-document map value.
	b, _ := json.Marshal(original)
	var p wikiMapArtifactPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, nil, fmt.Errorf("clone wiki map artifact: %w", err)
	}
	if err := bindWikiMapChunkRefs(&p, stableToID); err != nil {
		return nil, nil, err
	}
	entities, concepts := make([]extractedItem, 0), make([]extractedItem, 0)
	for _, u := range p.Updates {
		switch u.Type {
		case types.WikiPageTypeEntity:
			entities = append(entities, u.Item)
		case types.WikiPageTypeConcept:
			concepts = append(concepts, u.Item)
		}
	}
	dedupedEntities, dedupedConcepts := s.deduplicateExtractedBatch(ctx, chatModel, payload.KnowledgeBaseID, entities, concepts)
	remap := make(map[string]string)
	for i := range entities {
		if i < len(dedupedEntities) && entities[i].Slug != dedupedEntities[i].Slug {
			remap[entities[i].Slug] = dedupedEntities[i].Slug
		}
	}
	for i := range concepts {
		if i < len(dedupedConcepts) && concepts[i].Slug != dedupedConcepts[i].Slug {
			remap[concepts[i].Slug] = dedupedConcepts[i].Slug
		}
	}
	if len(remap) > 0 {
		for i := range p.Updates {
			if dst := remap[p.Updates[i].Slug]; dst != "" {
				p.Updates[i].Slug, p.Updates[i].Item.Slug = dst, dst
			}
			for src, dst := range remap {
				p.Updates[i].SummaryBody = strings.ReplaceAll(p.Updates[i].SummaryBody, "[["+src+"]]", "[["+dst+"]]")
				p.Updates[i].SummaryLine = strings.ReplaceAll(p.Updates[i].SummaryLine, "[["+src+"]]", "[["+dst+"]]")
			}
		}
		for i := range p.Pages {
			if dst := remap[p.Pages[i].Slug]; dst != "" {
				p.Pages[i].Slug = dst
			}
		}
	}
	p.Updates = normalizeWikiMapUpdates(p.Updates)
	p.Pages = normalizeWikiMapPages(p.Pages)
	updates := make([]SlugUpdate, 0, len(p.Updates)+4)
	for _, u := range p.Updates {
		updates = append(updates, SlugUpdate{Slug: u.Slug, Type: u.Type, Item: u.Item, DocTitle: u.DocTitle, KnowledgeID: u.KnowledgeID, SourceRef: u.SourceRef, Language: u.Language, SummaryBody: u.SummaryBody, SummaryLine: u.SummaryLine, SourceChunks: u.ResolvedSourceChunks, DocSummary: u.DocSummary})
	}
	chunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, payload.TenantID, op.KnowledgeID)
	if err != nil {
		return nil, nil, err
	}
	chunks = wikiMapConsumedChunks(chunks)
	content := reconstructEnrichedContent(ctx, s.chunkRepo, payload.TenantID, chunks)
	if r := []rune(content); len(r) > maxContentForWiki {
		content = string(r[:maxContentForWiki])
	}
	updates = s.appendCurrentWikiReconciliation(ctx, payload.KnowledgeBaseID, op.KnowledgeID, p.DocTitle, types.LanguageLocaleName(op.Language), content, p.Pages, updates, batchCtx)
	stats := p.MapStats
	if stats == nil {
		stats = types.JSONMap{}
	}
	stats["cache_status"] = cacheStatus
	stats["cache_reused"] = cacheStatus == types.IngestionCacheStatusHit
	stats["artifact_kind"] = wikiMapArtifactKind
	if cacheStatus == types.IngestionCacheStatusHit {
		stats["request_count"] = 0
		stats["computed_items"] = 0
		stats["reused_items"] = 1
	} else {
		stats["computed_items"] = 1
		stats["reused_items"] = 0
	}
	if cacheStatus == types.IngestionCacheStatusHit {
		logger.Infof(ctx, "wiki ingest: reused document map artifact for knowledge %s", op.KnowledgeID)
	}
	return &docIngestResult{KnowledgeID: p.KnowledgeID, DocTitle: p.DocTitle, Summary: p.Summary, Pages: p.Pages, MapStats: stats, WikiSpan: wikiSpan}, updates, nil
}

func normalizeWikiMapUpdates(in []wikiMapCachedUpdate) []wikiMapCachedUpdate {
	seen := make(map[string]int, len(in))
	out := make([]wikiMapCachedUpdate, 0, len(in))
	for _, update := range in {
		key := update.Type + "\x00" + update.Slug
		if idx, ok := seen[key]; ok && (update.Type == types.WikiPageTypeEntity || update.Type == types.WikiPageTypeConcept) {
			out[idx].SourceChunks = mergeWikiMapChunkRefs(out[idx].SourceChunks, update.SourceChunks)
			merged := mergeStableStrings(out[idx].ResolvedSourceChunks, update.ResolvedSourceChunks)
			out[idx].ResolvedSourceChunks = merged
			out[idx].Item.SourceChunks = merged
			continue
		}
		seen[key] = len(out)
		out = append(out, update)
	}
	return out
}

func mergeWikiMapChunkRefs(a, b []wikiMapChunkRef) []wikiMapChunkRef {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]wikiMapChunkRef, 0, len(a)+len(b))
	for _, refs := range [][]wikiMapChunkRef{a, b} {
		for _, ref := range refs {
			if ref.StableIdentity == "" || seen[ref.StableIdentity] {
				continue
			}
			seen[ref.StableIdentity] = true
			out = append(out, ref)
		}
	}
	return out
}

func normalizeWikiMapPages(in []types.WikiLogPageRef) []types.WikiLogPageRef {
	seen := make(map[string]bool, len(in))
	out := make([]types.WikiLogPageRef, 0, len(in))
	for _, page := range in {
		if page.Slug == "" || seen[page.Slug] {
			continue
		}
		seen[page.Slug] = true
		out = append(out, page)
	}
	return out
}

func mergeStableStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, values := range [][]string{a, b} {
		for _, value := range values {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func (s *wikiIngestService) appendCurrentWikiReconciliation(ctx context.Context, kbID, knowledgeID, docTitle, lang, content string, pages []types.WikiLogPageRef, updates []SlugUpdate, batchCtx *WikiBatchContext) []SlugUpdate {
	old := s.getExistingPageSlugsForKnowledge(ctx, kbID, knowledgeID)
	prior := batchCtx.SummaryContentByKnowledgeID(ctx, knowledgeID)
	return appendWikiReconciliation(old, pages, updates, knowledgeID, docTitle, lang, content, prior)
}

func appendWikiReconciliation(old map[string]bool, pages []types.WikiLogPageRef, updates []SlugUpdate, knowledgeID, docTitle, lang, content, prior string) []SlugUpdate {
	current := make(map[string]bool, len(pages))
	for _, page := range pages {
		current[page.Slug] = true
	}
	for slug := range old {
		if current[slug] {
			if !strings.HasPrefix(slug, "summary/") {
				updates = append(updates, SlugUpdate{Slug: slug, Type: "retract", RetractDocContent: prior, DocTitle: docTitle, KnowledgeID: knowledgeID, Language: lang})
			}
			continue
		}
		updates = append(updates, SlugUpdate{Slug: slug, Type: "retractStale", RetractDocContent: content, DocTitle: docTitle, KnowledgeID: knowledgeID, Language: lang})
	}
	return updates
}
