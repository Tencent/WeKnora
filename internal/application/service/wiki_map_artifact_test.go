package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/inferencecache"
	"github.com/Tencent/WeKnora/internal/types"
)

type wikiArtifactTestChat struct {
	id   string
	name string
}

func (*wikiArtifactTestChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}

func (*wikiArtifactTestChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *wikiArtifactTestChat) GetModelName() string { return m.name }
func (m *wikiArtifactTestChat) GetModelID() string   { return m.id }

func wikiArtifactChunk(id, content string) *types.Chunk {
	return &types.Chunk{
		ID:            id,
		Content:       content,
		ChunkIndex:    3,
		StartAt:       120,
		EndAt:         240,
		ChunkType:     types.ChunkTypeText,
		ContextHeader: "# Results",
	}
}

func TestWikiStableChunkRefSurvivesReparseUUIDAndImageURL(t *testing.T) {
	first := wikiArtifactChunk("old-uuid", "evidence ![chart](local://exports/random-a.png)")
	second := wikiArtifactChunk("new-uuid", "evidence ![chart](local://exports/random-b.png)")

	if got, want := wikiStableChunkRef(first), wikiStableChunkRef(second); got != want {
		t.Fatalf("stable refs differ across physical UUID/URL changes: %q != %q", got, want)
	}

	changed := wikiArtifactChunk("third-uuid", "different evidence ![chart](local://exports/random-b.png)")
	if wikiStableChunkRef(first) == wikiStableChunkRef(changed) {
		t.Fatal("content change did not invalidate stable chunk ref")
	}
}

func TestWikiDocMapArtifactKeyLayeredInvalidation(t *testing.T) {
	ctx := context.Background()
	firstModel := &wikiArtifactTestChat{id: "model-a", name: "chat-a"}
	secondModel := &wikiArtifactTestChat{id: "model-b", name: "chat-b"}
	firstChunk := wikiArtifactChunk("old-uuid", "evidence ![chart](local://exports/random-a.png)")
	secondChunk := wikiArtifactChunk("new-uuid", "evidence ![chart](local://exports/random-b.png)")
	firstContent := "paper ![chart](local://exports/random-a.png)"
	secondContent := "paper ![chart](local://exports/random-b.png)"
	standard := &WikiBatchContext{ExtractionGranularity: types.WikiExtractionStandard}

	base := wikiDocMapArtifactKey(ctx, firstModel, "kb-1", firstContent, "English", []*types.Chunk{firstChunk}, standard)
	reparse := wikiDocMapArtifactKey(ctx, firstModel, "kb-1", secondContent, "English", []*types.Chunk{secondChunk}, standard)
	if base != reparse {
		t.Fatalf("random physical IDs changed artifact key: %q != %q", base, reparse)
	}

	changedModel := wikiDocMapArtifactKey(ctx, secondModel, "kb-1", secondContent, "English", []*types.Chunk{secondChunk}, standard)
	if base == changedModel {
		t.Fatal("chat model change did not invalidate artifact key")
	}

	exhaustive := &WikiBatchContext{ExtractionGranularity: types.WikiExtractionExhaustive}
	changedGranularity := wikiDocMapArtifactKey(ctx, firstModel, "kb-1", secondContent, "English", []*types.Chunk{secondChunk}, exhaustive)
	if base == changedGranularity {
		t.Fatal("extraction granularity change did not invalidate artifact key")
	}

	changedContent := wikiDocMapArtifactKey(ctx, firstModel, "kb-1", "revised paper", "English", []*types.Chunk{wikiArtifactChunk("new", "revised evidence")}, standard)
	if base == changedContent {
		t.Fatal("document content change did not invalidate artifact key")
	}
}

func TestWikiDedupCandidateStateIgnoresSelfOnlyPages(t *testing.T) {
	const knowledgeID = "knowledge-1"
	baseline := wikiDedupCandidateState(nil, knowledgeID)
	selfOnly := map[string]*types.WikiPageLite{
		"entity/cache": {
			Slug:       "entity/cache",
			Title:      "Cache",
			PageType:   types.WikiPageTypeEntity,
			Status:     types.WikiPageStatusPublished,
			Aliases:    types.StringArray{"Caching"},
			SourceRefs: types.StringArray{knowledgeID + "|Current document"},
		},
	}
	if got := wikiDedupCandidateState(selfOnly, knowledgeID); got != baseline {
		t.Fatalf("self-only reduce output invalidated map state: %q != %q", got, baseline)
	}

	external := map[string]*types.WikiPageLite{
		"entity/cache": {
			Slug:       "entity/cache",
			Title:      "Cache",
			PageType:   types.WikiPageTypeEntity,
			Status:     types.WikiPageStatusPublished,
			Aliases:    types.StringArray{"Caching"},
			SourceRefs: types.StringArray{"knowledge-2|Other document"},
		},
	}
	externalState := wikiDedupCandidateState(external, knowledgeID)
	if externalState == baseline {
		t.Fatal("external dedup candidate did not invalidate map state")
	}
	external["entity/cache"].SourceRefs = types.StringArray{
		knowledgeID + "|Current document",
		"knowledge-2|Other document",
	}
	if got := wikiDedupCandidateState(external, knowledgeID); got != externalState {
		t.Fatalf("adding the current document to an external page changed its dedup state: %q != %q", got, externalState)
	}

	external["entity/cache"].Aliases = types.StringArray{"Memoization"}
	if changed := wikiDedupCandidateState(external, knowledgeID); changed == externalState {
		t.Fatal("external candidate alias change did not invalidate map state")
	}
}

func TestWikiDedupCandidateStateIsOrderIndependent(t *testing.T) {
	first := map[string]*types.WikiPageLite{
		"concept/rag": {
			Slug:       "concept/rag",
			Title:      "RAG",
			PageType:   types.WikiPageTypeConcept,
			Status:     types.WikiPageStatusPublished,
			Aliases:    types.StringArray{"Retrieval Augmented Generation", "Retrieval-Augmented Generation"},
			SourceRefs: types.StringArray{"knowledge-2"},
		},
		"entity/weknora": {
			Slug:       "entity/weknora",
			Title:      "WeKnora",
			PageType:   types.WikiPageTypeEntity,
			Status:     types.WikiPageStatusPublished,
			SourceRefs: types.StringArray{"knowledge-3"},
		},
	}
	second := map[string]*types.WikiPageLite{
		"entity/weknora": first["entity/weknora"],
		"concept/rag": {
			Slug:       "concept/rag",
			Title:      "RAG",
			PageType:   types.WikiPageTypeConcept,
			Status:     types.WikiPageStatusPublished,
			Aliases:    types.StringArray{"Retrieval-Augmented Generation", "Retrieval Augmented Generation"},
			SourceRefs: types.StringArray{"knowledge-2"},
		},
	}
	if got, want := wikiDedupCandidateState(first, "knowledge-1"), wikiDedupCandidateState(second, "knowledge-1"); got != want {
		t.Fatalf("map or alias order changed dedup state: %q != %q", got, want)
	}
}

func TestResolveWikiDocMapArtifactValueHitsAndRebinds(t *testing.T) {
	t.Setenv("WEKNORA_INFERENCE_CACHE_ENABLED", "true")
	ctx := context.Background()
	cache := inferencecache.New(nil)
	const key = "wiki-doc-map-test-key"
	const firstURL = "local://exports/random-first.png"
	const secondURL = "local://exports/random-second.png"

	firstChunk := wikiArtifactChunk("old-uuid", "evidence ![chart]("+firstURL+")")
	_, firstIDToRef, firstRefToID := freezeWikiChunks([]*types.Chunk{firstChunk})
	loaderCalls := 0
	loader := func(context.Context) (wikiDocMapArtifact, error) {
		loaderCalls++
		return wikiDocMapArtifact{
			Entities: []extractedItem{{
				Name:         "Chart",
				Slug:         "entity/chart",
				Details:      "See ![chart](" + firstURL + ")",
				SourceChunks: []string{firstChunk.ID},
			}},
			Summary: "Summary ![chart](" + firstURL + ")",
		}, nil
	}

	first, firstStats, err := resolveWikiDocMapArtifactValue(
		ctx, cache, key, "paper ![chart]("+firstURL+")",
		firstIDToRef, firstRefToID, loader,
	)
	if err != nil {
		t.Fatalf("first resolve error = %v", err)
	}
	if firstStats.Hit || loaderCalls != 1 {
		t.Fatalf("first resolve stats=%+v loaderCalls=%d, want miss and one load", firstStats, loaderCalls)
	}
	if first.Entities[0].SourceChunks[0] != "old-uuid" || !strings.Contains(first.Summary, firstURL) {
		t.Fatalf("first result not rebound: %+v", first)
	}

	secondChunk := wikiArtifactChunk("new-uuid", "evidence ![chart]("+secondURL+")")
	_, secondIDToRef, secondRefToID := freezeWikiChunks([]*types.Chunk{secondChunk})
	second, secondStats, err := resolveWikiDocMapArtifactValue(
		ctx, cache, key, "paper ![chart]("+secondURL+")",
		secondIDToRef, secondRefToID, loader,
	)
	if err != nil {
		t.Fatalf("second resolve error = %v", err)
	}
	if !secondStats.Hit || loaderCalls != 1 {
		t.Fatalf("second resolve stats=%+v loaderCalls=%d, want hit without reload", secondStats, loaderCalls)
	}
	if got := second.Entities[0].SourceChunks[0]; got != "new-uuid" {
		t.Fatalf("cached stable ref rebound to %q, want new-uuid", got)
	}
	if !strings.Contains(second.Summary, secondURL) || strings.Contains(second.Summary, firstURL) {
		t.Fatalf("cached image URL was not rebound to current parse: %q", second.Summary)
	}
}
