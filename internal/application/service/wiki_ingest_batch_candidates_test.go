package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type batchCandidateWikiStub struct {
	interfaces.WikiPageService
	calls     int
	kbID      string
	terms     []string
	pageTypes []string
	limit     int
	hits      map[string][]*types.WikiPageLite
	err       error
}

func (s *batchCandidateWikiStub) FindSimilarPagesBatch(
	_ context.Context, kbID string, terms, pageTypes []string, limit int,
) (map[string][]*types.WikiPageLite, error) {
	s.calls++
	s.kbID, s.terms, s.pageTypes, s.limit = kbID, terms, pageTypes, limit
	return s.hits, s.err
}

func TestCollectBatchDedupCandidatesPreservesTermOwnership(t *testing.T) {
	stub := &batchCandidateWikiStub{hits: map[string][]*types.WikiPageLite{
		"alpha": {{Slug: "entity/title-alpha"}},
		"shared": {
			{Slug: "entity/shared"}, {Slug: "concept/shared"}, {Slug: "entity/shared"}, nil, {},
		},
		"beta":    {{Slug: "entity/title-beta"}},
		"concept": {{Slug: "concept/only"}},
		"foreign": {{Slug: "entity/must-not-leak"}},
	}}
	svc := &wikiIngestService{wikiService: stub}
	entities := []extractedItem{
		{Slug: "entity/a", Name: " Alpha ", Aliases: []string{"SHARED", " shared ", ""}},
		{Slug: "entity/b", Name: "Beta", Aliases: []string{"shared"}},
		{Slug: "entity/empty", Name: " ", Aliases: []string{""}},
	}
	concepts := []extractedItem{{Slug: "concept/c", Name: "Concept", Aliases: []string{"Shared"}}}
	pages, own := svc.collectBatchDedupCandidates(context.Background(), "kb", entities, concepts)
	require.Equal(t, 1, stub.calls)
	require.Equal(t, "kb", stub.kbID)
	require.Equal(t, []string{"alpha", "shared", "beta", "concept"}, stub.terms)
	require.Equal(t, []string{types.WikiPageTypeEntity, types.WikiPageTypeConcept}, stub.pageTypes)
	require.Equal(t, dedupCandidateTopK, stub.limit)
	require.Len(t, pages, 5)
	require.Equal(t, map[string]bool{
		"entity/title-alpha": true, "entity/shared": true, "concept/shared": true,
	}, own["entity/a"])
	require.Equal(t, map[string]bool{
		"entity/title-beta": true, "entity/shared": true, "concept/shared": true,
	}, own["entity/b"])
	require.Equal(t, map[string]bool{
		"concept/only": true, "entity/shared": true, "concept/shared": true,
	}, own["concept/c"])
	require.Empty(t, own["entity/empty"])
	require.NotContains(t, pages, "entity/must-not-leak")
}

func TestCollectBatchDedupCandidatesEmptyAndFailure(t *testing.T) {
	stub := &batchCandidateWikiStub{}
	svc := &wikiIngestService{wikiService: stub}
	pages, own := svc.collectBatchDedupCandidates(context.Background(), "kb", nil, nil)
	require.Empty(t, pages)
	require.Empty(t, own)
	require.Zero(t, stub.calls)
	stub.err = errors.New("lookup unavailable")
	stub.hits = map[string][]*types.WikiPageLite{"alpha": {{Slug: "entity/partial"}}}
	pages, own = svc.collectBatchDedupCandidates(context.Background(), "kb",
		[]extractedItem{{Slug: "entity/a", Name: "Alpha"}}, nil)
	require.Equal(t, 1, stub.calls)
	require.Empty(t, pages, "never use partial candidates returned with an error")
	require.Empty(t, own["entity/a"])
}
