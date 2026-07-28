package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	wikiMapArtifactStage         = "wiki.map.document"
	wikiMapArtifactKeyVersion    = uint16(1)
	wikiMapArtifactSchemaVersion = uint8(2)

	wikiMapPromptSuiteVersion   = "wiki-map-prompts-v1"
	wikiMapCanonicalizerVersion = "wiki-map-canonicalizer-v1"
	wikiMapChatOptionsVersion   = "temperature=0.3;thinking=false"

	wikiMapCacheStatusBypass      = "bypass"
	wikiMapCacheStatusHit         = "hit"
	wikiMapCacheStatusMiss        = "miss"
	wikiMapCacheStatusStored      = "stored"
	wikiMapCacheStatusCorrupt     = "corrupt"
	wikiMapCacheStatusStale       = "stale"
	wikiMapCacheStatusUncacheable = "uncacheable"
)

type wikiMapArtifactRequest struct {
	tenantID               uint64
	knowledgeBaseID        string
	knowledgeID            string
	modelID                string
	modelName              string
	modelRevision          string
	content                string
	chunks                 []*types.Chunk
	language               string
	granularity            types.WikiExtractionGranularity
	contentInstructions    string
	extractionInstructions string
	promptSuiteVersion     string
	canonicalizerVersion   string
}

type wikiMapArtifactValue struct {
	Entities                 []extractedItem
	Concepts                 []extractedItem
	SummaryContent           string
	PreviousSlugsFingerprint string
	Pass0Fallback            bool
	CitationBatches          int
	UncitedSlugs             int
	NewSlugCount             int
	DedupContext             wikiMapDedupContext
}

type wikiMapDedupContext struct {
	Entities                    []extractedItem
	Concepts                    []extractedItem
	CandidateSlugs              []string
	CandidateFingerprint        string
	ReducedCandidateFingerprint string
	OutputPageFingerprints      map[string]string
}

type wikiMapCanonicalChunk struct {
	Content    string          `json:"content"`
	ChunkIndex int             `json:"chunk_index"`
	StartAt    int             `json:"start_at"`
	ChunkType  types.ChunkType `json:"chunk_type"`
	ID         string          `json:"-"`
}

type wikiMapArtifactItem struct {
	Name           string   `json:"name"`
	Slug           string   `json:"slug"`
	Aliases        []string `json:"aliases,omitempty"`
	Description    string   `json:"description,omitempty"`
	Details        string   `json:"details,omitempty"`
	SourceOrdinals []int    `json:"source_ordinals,omitempty"`
}

type wikiMapArtifactPayload struct {
	Version                  uint8                 `json:"version"`
	ChunkFingerprints        []string              `json:"chunk_fingerprints"`
	Entities                 []wikiMapArtifactItem `json:"entities,omitempty"`
	Concepts                 []wikiMapArtifactItem `json:"concepts,omitempty"`
	SummaryContent           string                `json:"summary_content"`
	PreviousSlugsFingerprint string                `json:"previous_slugs_fingerprint,omitempty"`
	Pass0Fallback            bool                  `json:"pass0_fallback,omitempty"`
	CitationBatches          int                   `json:"citation_batches,omitempty"`
	UncitedSlugs             int                   `json:"uncited_slugs,omitempty"`
	NewSlugCount             int                   `json:"new_slug_count,omitempty"`
	DedupEntities            []wikiMapArtifactItem `json:"dedup_entities,omitempty"`
	DedupConcepts            []wikiMapArtifactItem `json:"dedup_concepts,omitempty"`
	DedupCandidateSlugs      []string              `json:"dedup_candidate_slugs,omitempty"`
	DedupFingerprint         string                `json:"dedup_fingerprint"`
	DedupReducedFingerprint  string                `json:"dedup_reduced_fingerprint,omitempty"`
	DedupOutputFingerprints  map[string]string     `json:"dedup_output_fingerprints,omitempty"`
}

func hasUsableWikiMapSummary(raw string) bool {
	summary, content := splitSummaryLine(raw)
	return strings.TrimSpace(summary) != "" || strings.TrimSpace(content) != ""
}

func completeWikiMapArtifact(
	ctx context.Context,
	store interfaces.ProcessingArtifactStore,
	request wikiMapArtifactRequest,
	isCurrent func(wikiMapArtifactValue) bool,
	isConcurrentWinnerCurrent func(winner, computed wikiMapArtifactValue) bool,
	compute func() (wikiMapArtifactValue, bool, error),
) (wikiMapArtifactValue, bool, string, error) {
	if store == nil || strings.TrimSpace(request.modelRevision) == "" {
		value, _, err := compute()
		return value, false, wikiMapCacheStatusBypass, err
	}

	key, err := newWikiMapArtifactKey(request)
	if err != nil {
		value, _, computeErr := compute()
		return value, false, wikiMapCacheStatusBypass, computeErr
	}
	payload, hit, err := store.Get(ctx, key)
	if err != nil {
		return wikiMapArtifactValue{}, false, "", fmt.Errorf("get wiki map artifact: %w", err)
	}
	cacheStatus := wikiMapCacheStatusMiss
	if hit {
		value, decodeErr := decodeWikiMapArtifact(payload, request.chunks)
		if decodeErr == nil && (isCurrent == nil || isCurrent(value)) {
			return value, true, wikiMapCacheStatusHit, nil
		}
		if decodeErr != nil {
			cacheStatus = wikiMapCacheStatusCorrupt
		} else {
			cacheStatus = wikiMapCacheStatusStale
		}
		if err := store.Invalidate(ctx, key, payload); err != nil {
			return wikiMapArtifactValue{}, false, cacheStatus, fmt.Errorf("invalidate wiki map artifact: %w", err)
		}
	}

	value, cacheable, err := compute()
	if err != nil {
		return wikiMapArtifactValue{}, false, cacheStatus, err
	}
	if !cacheable {
		return value, false, wikiMapCacheStatusUncacheable, nil
	}
	encoded, err := encodeWikiMapArtifact(value, request.chunks)
	if err != nil {
		return value, false, wikiMapCacheStatusUncacheable, nil
	}
	canonical, created, err := store.PutIfAbsent(ctx, key, encoded)
	if err != nil {
		return wikiMapArtifactValue{}, false, cacheStatus, fmt.Errorf("put wiki map artifact: %w", err)
	}
	canonicalValue, decodeErr := decodeWikiMapArtifact(canonical, request.chunks)
	if decodeErr != nil {
		if err := store.Invalidate(ctx, key, canonical); err != nil {
			return wikiMapArtifactValue{}, false, wikiMapCacheStatusCorrupt, fmt.Errorf("invalidate wiki map artifact: %w", err)
		}
		canonical, _, err = store.PutIfAbsent(ctx, key, encoded)
		if err != nil {
			return wikiMapArtifactValue{}, false, wikiMapCacheStatusCorrupt, fmt.Errorf("put wiki map artifact repair: %w", err)
		}
		canonicalValue, decodeErr = decodeWikiMapArtifact(canonical, request.chunks)
		if decodeErr != nil {
			return value, false, wikiMapCacheStatusCorrupt, nil
		}
		if isConcurrentWinnerCurrent != nil && !isConcurrentWinnerCurrent(canonicalValue, value) {
			return value, false, wikiMapCacheStatusCorrupt, nil
		}
		return canonicalValue, false, wikiMapCacheStatusCorrupt, nil
	}
	if !created && isConcurrentWinnerCurrent != nil && !isConcurrentWinnerCurrent(canonicalValue, value) {
		if err := store.Invalidate(ctx, key, canonical); err != nil {
			return wikiMapArtifactValue{}, false, wikiMapCacheStatusStale, fmt.Errorf("invalidate wiki map artifact: %w", err)
		}
		canonical, created, err = store.PutIfAbsent(ctx, key, encoded)
		if err != nil {
			return wikiMapArtifactValue{}, false, wikiMapCacheStatusStale, fmt.Errorf("put wiki map artifact repair: %w", err)
		}
		canonicalValue, decodeErr = decodeWikiMapArtifact(canonical, request.chunks)
		if decodeErr != nil {
			return value, false, wikiMapCacheStatusCorrupt, nil
		}
		if !created && !isConcurrentWinnerCurrent(canonicalValue, value) {
			return value, false, wikiMapCacheStatusStale, nil
		}
		return canonicalValue, false, wikiMapCacheStatusStale, nil
	}
	if hit {
		return canonicalValue, false, cacheStatus, nil
	}
	return canonicalValue, false, wikiMapCacheStatusStored, nil
}

func newWikiMapArtifactKey(request wikiMapArtifactRequest) (types.ProcessingArtifactKey, error) {
	if strings.TrimSpace(request.modelID) == "" || strings.TrimSpace(request.modelName) == "" {
		return types.ProcessingArtifactKey{}, errors.New("wiki map artifact model identity must not be empty")
	}
	if strings.TrimSpace(request.modelRevision) == "" {
		return types.ProcessingArtifactKey{}, errors.New("wiki map artifact model revision must not be empty")
	}
	if strings.TrimSpace(request.knowledgeBaseID) == "" || strings.TrimSpace(request.knowledgeID) == "" {
		return types.ProcessingArtifactKey{}, errors.New("wiki map artifact knowledge scope must not be empty")
	}
	if strings.TrimSpace(request.promptSuiteVersion) == "" || strings.TrimSpace(request.canonicalizerVersion) == "" {
		return types.ProcessingArtifactKey{}, errors.New("wiki map artifact versions must not be empty")
	}
	if !utf8.ValidString(request.content) || !utf8.ValidString(request.contentInstructions) ||
		!utf8.ValidString(request.extractionInstructions) {
		return types.ProcessingArtifactKey{}, errors.New("wiki map artifact input is not valid UTF-8")
	}

	layout, err := canonicalWikiMapChunks(request.chunks)
	if err != nil {
		return types.ProcessingArtifactKey{}, err
	}
	encodedLayout, err := json.Marshal(layout)
	if err != nil {
		return types.ProcessingArtifactKey{}, fmt.Errorf("encode wiki map chunk layout: %w", err)
	}

	return types.NewProcessingArtifactKey(
		request.tenantID,
		wikiMapArtifactStage,
		wikiMapArtifactKeyVersion,
		[]byte(request.knowledgeBaseID),
		[]byte(request.knowledgeID),
		[]byte(request.modelID),
		[]byte(request.modelName),
		[]byte(request.modelRevision),
		[]byte(request.content),
		encodedLayout,
		[]byte(request.language),
		[]byte(request.granularity.Normalize()),
		[]byte(request.contentInstructions),
		[]byte(request.extractionInstructions),
		[]byte(request.promptSuiteVersion),
		wikiMapPromptDigest(),
		[]byte(request.canonicalizerVersion),
		[]byte(wikiMapChatOptionsVersion),
	)
}

func wikiMapPromptDigest() []byte {
	prompts, _ := json.Marshal([]string{
		agent.WikiCandidateSlugPrompt,
		agent.WikiKnowledgeExtractPrompt,
		agent.WikiDeduplicationPrompt,
		agent.WikiSummaryPrompt,
		agent.WikiChunkCitationPrompt,
		agent.WikiGranularityGuidanceFocused,
		agent.WikiGranularityGuidanceStandard,
		agent.WikiGranularityGuidanceExhaustive,
	})
	digest := sha256.Sum256(prompts)
	return digest[:]
}

func encodeWikiMapArtifact(value wikiMapArtifactValue, chunks []*types.Chunk) ([]byte, error) {
	layout, err := canonicalWikiMapChunks(chunks)
	if err != nil {
		return nil, err
	}
	idToOrdinal := make(map[string]int, len(layout))
	for ordinal, chunk := range layout {
		if chunk.ID == "" {
			return nil, errors.New("wiki map artifact chunk ID must not be empty")
		}
		if _, exists := idToOrdinal[chunk.ID]; exists {
			return nil, fmt.Errorf("wiki map artifact duplicate chunk ID %q", chunk.ID)
		}
		idToOrdinal[chunk.ID] = ordinal
	}

	if !hasUsableWikiMapSummary(value.SummaryContent) {
		return nil, errors.New("wiki map artifact summary must not be empty")
	}
	if !utf8.ValidString(value.SummaryContent) {
		return nil, errors.New("wiki map artifact summary is not valid UTF-8")
	}
	seenSlugs := make(map[string]struct{}, len(value.Entities)+len(value.Concepts))
	encodeItems := func(items []extractedItem, kind string) ([]wikiMapArtifactItem, error) {
		encoded := make([]wikiMapArtifactItem, 0, len(items))
		for _, item := range items {
			if err := validateWikiMapExtractedItem(item, kind); err != nil {
				return nil, err
			}
			if _, exists := seenSlugs[item.Slug]; exists {
				return nil, fmt.Errorf("wiki map artifact contains duplicate slug %q", item.Slug)
			}
			seenSlugs[item.Slug] = struct{}{}
			ordinals := make([]int, 0, len(item.SourceChunks))
			seenOrdinals := make(map[int]struct{}, len(item.SourceChunks))
			for _, chunkID := range item.SourceChunks {
				ordinal, ok := idToOrdinal[chunkID]
				if !ok {
					return nil, fmt.Errorf("wiki map artifact references unknown chunk %q", chunkID)
				}
				if _, exists := seenOrdinals[ordinal]; exists {
					return nil, fmt.Errorf("wiki map artifact contains duplicate source chunk %q", chunkID)
				}
				seenOrdinals[ordinal] = struct{}{}
				ordinals = append(ordinals, ordinal)
			}
			encoded = append(encoded, wikiMapArtifactItem{
				Name:           item.Name,
				Slug:           item.Slug,
				Aliases:        append([]string(nil), item.Aliases...),
				Description:    item.Description,
				Details:        item.Details,
				SourceOrdinals: ordinals,
			})
		}
		for i := range encoded {
			sort.Strings(encoded[i].Aliases)
			sort.Ints(encoded[i].SourceOrdinals)
		}
		sort.Slice(encoded, func(i, j int) bool {
			if encoded[i].Slug != encoded[j].Slug {
				return encoded[i].Slug < encoded[j].Slug
			}
			return encoded[i].Name < encoded[j].Name
		})
		return encoded, nil
	}

	entities, err := encodeItems(value.Entities, types.WikiPageTypeEntity)
	if err != nil {
		return nil, err
	}
	concepts, err := encodeItems(value.Concepts, types.WikiPageTypeConcept)
	if err != nil {
		return nil, err
	}
	seenSlugs = make(map[string]struct{}, len(value.DedupContext.Entities)+len(value.DedupContext.Concepts))
	dedupEntities, err := encodeItems(value.DedupContext.Entities, types.WikiPageTypeEntity)
	if err != nil {
		return nil, err
	}
	dedupConcepts, err := encodeItems(value.DedupContext.Concepts, types.WikiPageTypeConcept)
	if err != nil {
		return nil, err
	}
	dedupCandidateSlugs, err := canonicalWikiMapCandidateSlugs(value.DedupContext.CandidateSlugs)
	if err != nil {
		return nil, err
	}
	dedupOutputFingerprints, err := canonicalWikiMapPageFingerprints(value.DedupContext.OutputPageFingerprints)
	if err != nil {
		return nil, err
	}
	payload := wikiMapArtifactPayload{
		Version:                  wikiMapArtifactSchemaVersion,
		ChunkFingerprints:        wikiMapChunkFingerprints(layout),
		Entities:                 entities,
		Concepts:                 concepts,
		SummaryContent:           value.SummaryContent,
		PreviousSlugsFingerprint: value.PreviousSlugsFingerprint,
		Pass0Fallback:            value.Pass0Fallback,
		CitationBatches:          value.CitationBatches,
		UncitedSlugs:             value.UncitedSlugs,
		NewSlugCount:             value.NewSlugCount,
		DedupEntities:            dedupEntities,
		DedupConcepts:            dedupConcepts,
		DedupCandidateSlugs:      dedupCandidateSlugs,
		DedupFingerprint:         value.DedupContext.CandidateFingerprint,
		DedupReducedFingerprint:  value.DedupContext.ReducedCandidateFingerprint,
		DedupOutputFingerprints:  dedupOutputFingerprints,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode wiki map artifact: %w", err)
	}
	return encoded, nil
}

func decodeWikiMapArtifact(payload []byte, chunks []*types.Chunk) (wikiMapArtifactValue, error) {
	if !utf8.Valid(payload) {
		return wikiMapArtifactValue{}, errors.New("wiki map artifact payload is not valid UTF-8")
	}
	var stored wikiMapArtifactPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return wikiMapArtifactValue{}, fmt.Errorf("decode wiki map artifact: %w", err)
	}
	if stored.Version != wikiMapArtifactSchemaVersion {
		return wikiMapArtifactValue{}, fmt.Errorf("unsupported wiki map artifact version %d", stored.Version)
	}
	layout, err := canonicalWikiMapChunks(chunks)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	if !sameWikiMapFingerprints(stored.ChunkFingerprints, wikiMapChunkFingerprints(layout)) {
		return wikiMapArtifactValue{}, errors.New("wiki map artifact chunk layout does not match")
	}

	if !hasUsableWikiMapSummary(stored.SummaryContent) {
		return wikiMapArtifactValue{}, errors.New("wiki map artifact summary must not be empty")
	}
	if !utf8.ValidString(stored.SummaryContent) {
		return wikiMapArtifactValue{}, errors.New("wiki map artifact summary is not valid UTF-8")
	}
	seenSlugs := make(map[string]struct{}, len(stored.Entities)+len(stored.Concepts))
	decodeItems := func(items []wikiMapArtifactItem, kind string) ([]extractedItem, error) {
		decoded := make([]extractedItem, 0, len(items))
		for _, item := range items {
			sourceChunks := make([]string, 0, len(item.SourceOrdinals))
			seenOrdinals := make(map[int]struct{}, len(item.SourceOrdinals))
			for _, ordinal := range item.SourceOrdinals {
				if ordinal < 0 || ordinal >= len(layout) || layout[ordinal].ID == "" {
					return nil, fmt.Errorf("wiki map artifact source ordinal %d is invalid", ordinal)
				}
				if _, exists := seenOrdinals[ordinal]; exists {
					return nil, fmt.Errorf("wiki map artifact source ordinal %d is duplicated", ordinal)
				}
				seenOrdinals[ordinal] = struct{}{}
				sourceChunks = append(sourceChunks, layout[ordinal].ID)
			}
			decodedItem := extractedItem{
				Name:         item.Name,
				Slug:         item.Slug,
				Aliases:      append([]string(nil), item.Aliases...),
				Description:  item.Description,
				Details:      item.Details,
				SourceChunks: sourceChunks,
			}
			if err := validateWikiMapExtractedItem(decodedItem, kind); err != nil {
				return nil, err
			}
			if _, exists := seenSlugs[decodedItem.Slug]; exists {
				return nil, fmt.Errorf("wiki map artifact contains duplicate slug %q", decodedItem.Slug)
			}
			seenSlugs[decodedItem.Slug] = struct{}{}
			decoded = append(decoded, decodedItem)
		}
		return decoded, nil
	}

	entities, err := decodeItems(stored.Entities, types.WikiPageTypeEntity)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	concepts, err := decodeItems(stored.Concepts, types.WikiPageTypeConcept)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	seenSlugs = make(map[string]struct{}, len(stored.DedupEntities)+len(stored.DedupConcepts))
	dedupEntities, err := decodeItems(stored.DedupEntities, types.WikiPageTypeEntity)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	dedupConcepts, err := decodeItems(stored.DedupConcepts, types.WikiPageTypeConcept)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	dedupCandidateSlugs, err := canonicalWikiMapCandidateSlugs(stored.DedupCandidateSlugs)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	dedupOutputFingerprints, err := canonicalWikiMapPageFingerprints(stored.DedupOutputFingerprints)
	if err != nil {
		return wikiMapArtifactValue{}, err
	}
	return wikiMapArtifactValue{
		Entities:                 entities,
		Concepts:                 concepts,
		SummaryContent:           stored.SummaryContent,
		PreviousSlugsFingerprint: stored.PreviousSlugsFingerprint,
		Pass0Fallback:            stored.Pass0Fallback,
		CitationBatches:          stored.CitationBatches,
		UncitedSlugs:             stored.UncitedSlugs,
		NewSlugCount:             stored.NewSlugCount,
		DedupContext: wikiMapDedupContext{
			Entities: dedupEntities, Concepts: dedupConcepts,
			CandidateSlugs:              dedupCandidateSlugs,
			CandidateFingerprint:        stored.DedupFingerprint,
			ReducedCandidateFingerprint: stored.DedupReducedFingerprint,
			OutputPageFingerprints:      dedupOutputFingerprints,
		},
	}, nil
}

func canonicalWikiMapCandidateSlugs(slugs []string) ([]string, error) {
	canonical := append([]string(nil), slugs...)
	sort.Strings(canonical)
	for i, slug := range canonical {
		if strings.TrimSpace(slug) == "" || !utf8.ValidString(slug) {
			return nil, errors.New("wiki map artifact dedup candidate slug is invalid")
		}
		if i > 0 && canonical[i-1] == slug {
			return nil, fmt.Errorf("wiki map artifact duplicate dedup candidate slug %q", slug)
		}
	}
	return canonical, nil
}

func canonicalWikiMapPageFingerprints(fingerprints map[string]string) (map[string]string, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}
	canonical := make(map[string]string, len(fingerprints))
	for slug, fingerprint := range fingerprints {
		if strings.TrimSpace(slug) == "" || !utf8.ValidString(slug) ||
			strings.TrimSpace(fingerprint) == "" || !utf8.ValidString(fingerprint) {
			return nil, errors.New("wiki map artifact output page fingerprint is invalid")
		}
		canonical[slug] = fingerprint
	}
	return canonical, nil
}

func canonicalWikiMapChunks(chunks []*types.Chunk) ([]wikiMapCanonicalChunk, error) {
	canonical := make([]wikiMapCanonicalChunk, 0, len(chunks))
	seenIDs := make(map[string]struct{}, len(chunks))
	seenDescriptors := make(map[string]struct{}, len(chunks))
	for _, chunk := range canonicalWikiMapChunkOrder(chunks) {
		if chunk == nil || chunk.Content == "" {
			continue
		}
		if chunk.ChunkType != "" && chunk.ChunkType != types.ChunkTypeText {
			continue
		}
		if !utf8.ValidString(chunk.Content) {
			return nil, errors.New("wiki map artifact chunk content is not valid UTF-8")
		}
		if strings.TrimSpace(chunk.ID) == "" {
			return nil, errors.New("wiki map artifact chunk ID must not be empty")
		}
		if _, exists := seenIDs[chunk.ID]; exists {
			return nil, fmt.Errorf("wiki map artifact duplicate chunk ID %q", chunk.ID)
		}
		seenIDs[chunk.ID] = struct{}{}
		descriptor, err := json.Marshal(struct {
			Content    string          `json:"content"`
			ChunkIndex int             `json:"chunk_index"`
			StartAt    int             `json:"start_at"`
			ChunkType  types.ChunkType `json:"chunk_type"`
		}{chunk.Content, chunk.ChunkIndex, chunk.StartAt, chunk.ChunkType})
		if err != nil {
			return nil, fmt.Errorf("encode wiki map chunk descriptor: %w", err)
		}
		if _, exists := seenDescriptors[string(descriptor)]; exists {
			return nil, errors.New("wiki map artifact contains an ambiguous chunk layout")
		}
		seenDescriptors[string(descriptor)] = struct{}{}
		canonical = append(canonical, wikiMapCanonicalChunk{
			ID:         chunk.ID,
			Content:    chunk.Content,
			ChunkIndex: chunk.ChunkIndex,
			StartAt:    chunk.StartAt,
			ChunkType:  chunk.ChunkType,
		})
	}
	return canonical, nil
}

func canonicalWikiMapChunkOrder(chunks []*types.Chunk) []*types.Chunk {
	ordered := append([]*types.Chunk(nil), chunks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := ordered[i], ordered[j]
		if left == nil || right == nil {
			return left != nil
		}
		if left.ChunkIndex != right.ChunkIndex {
			return left.ChunkIndex < right.ChunkIndex
		}
		if left.StartAt != right.StartAt {
			return left.StartAt < right.StartAt
		}
		if left.Content != right.Content {
			return left.Content < right.Content
		}
		if left.ChunkType != right.ChunkType {
			return left.ChunkType < right.ChunkType
		}
		return left.ID < right.ID
	})
	return ordered
}

func validateWikiMapExtractedItem(item extractedItem, kind string) error {
	if !isCompleteExtractedCandidate(item, kind) {
		return fmt.Errorf("wiki map artifact contains incomplete %s item %q", kind, item.Slug)
	}
	values := append([]string{item.Name, item.Slug, item.Description, item.Details}, item.Aliases...)
	values = append(values, item.SourceChunks...)
	for _, value := range values {
		if !utf8.ValidString(value) {
			return errors.New("wiki map artifact item is not valid UTF-8")
		}
	}
	return nil
}

func cloneWikiMapItems(items []extractedItem) []extractedItem {
	cloned := make([]extractedItem, len(items))
	for i, item := range items {
		cloned[i] = item
		cloned[i].Aliases = append([]string(nil), item.Aliases...)
		cloned[i].SourceChunks = append([]string(nil), item.SourceChunks...)
	}
	return cloned
}

func wikiMapChunkFingerprints(chunks []wikiMapCanonicalChunk) []string {
	fingerprints := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		descriptor, _ := json.Marshal(struct {
			Content    string          `json:"content"`
			ChunkIndex int             `json:"chunk_index"`
			StartAt    int             `json:"start_at"`
			ChunkType  types.ChunkType `json:"chunk_type"`
		}{chunk.Content, chunk.ChunkIndex, chunk.StartAt, chunk.ChunkType})
		digest := sha256.Sum256(descriptor)
		fingerprints = append(fingerprints, fmt.Sprintf("%x", digest[:]))
	}
	return fingerprints
}

func sameWikiMapFingerprints(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
