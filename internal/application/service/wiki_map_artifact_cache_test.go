package service

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestDecodeWikiMapArtifactRejectsWrongSchema(t *testing.T) {
	b, err := json.Marshal(wikiMapArtifactPayload{SchemaVersion: "old", KnowledgeID: "doc-1"})
	require.NoError(t, err)
	_, err = decodeWikiMapArtifact(&types.DerivedArtifact{PayloadEncoding: "json", Payload: b, PayloadDigest: artifactkey.DigestBytes(b)})
	require.ErrorIs(t, err, interfaces.ErrArtifactCorrupt)
}

func TestAppendWikiReconciliationRecomputesLiveState(t *testing.T) {
	base := []SlugUpdate{{Slug: "summary/doc-1", Type: types.WikiPageTypeSummary}, {Slug: "entity/live", Type: types.WikiPageTypeEntity}}
	got := appendWikiReconciliation(
		map[string]bool{"summary/doc-1": true, "entity/live": true, "concept/stale": true},
		[]wikiIngestPageRef{{Slug: "summary/doc-1"}, {Slug: "entity/live"}},
		base, "doc-1", "Doc", "English", "new content", "prior contribution",
	)
	require.Len(t, got, 4)
	bySlug := map[string]SlugUpdate{got[2].Slug: got[2], got[3].Slug: got[3]}
	require.Equal(t, "retract", bySlug["entity/live"].Type)
	require.Equal(t, "prior contribution", bySlug["entity/live"].RetractDocContent)
	require.Equal(t, "retractStale", bySlug["concept/stale"].Type)
	require.Equal(t, "new content", bySlug["concept/stale"].RetractDocContent)
}

func TestNormalizeWikiMapUpdatesMergesStateRemapCollisions(t *testing.T) {
	got := normalizeWikiMapUpdates([]wikiMapCachedUpdate{
		{Slug: "entity/existing", Type: types.WikiPageTypeEntity, SourceChunks: []wikiMapChunkRef{{StableIdentity: "s1"}}, ResolvedSourceChunks: []string{"c1"}, Item: extractedItem{Slug: "entity/existing", SourceChunks: []string{"c1"}}},
		{Slug: "entity/existing", Type: types.WikiPageTypeEntity, SourceChunks: []wikiMapChunkRef{{StableIdentity: "s1"}, {StableIdentity: "s2"}}, ResolvedSourceChunks: []string{"c1", "c2"}, Item: extractedItem{Slug: "entity/existing", SourceChunks: []string{"c2"}}},
		{Slug: "summary/doc-1", Type: types.WikiPageTypeSummary},
	})
	require.Len(t, got, 2)
	require.Equal(t, []string{"s1", "s2"}, []string{got[0].SourceChunks[0].StableIdentity, got[0].SourceChunks[1].StableIdentity})
	require.Equal(t, []string{"c1", "c2"}, got[0].Item.SourceChunks)
}

func TestBindWikiMapChunkRefsUsesCurrentDatabaseIDs(t *testing.T) {
	p := &wikiMapArtifactPayload{Updates: []wikiMapCachedUpdate{{SourceChunks: []wikiMapChunkRef{{StableIdentity: "stable-a", IdentityVersion: contentkey.ChunkIdentityVersion}}}}}
	require.NoError(t, bindWikiMapChunkRefs(p, map[string]string{"stable-a": "new-row-id"}))
	require.Equal(t, []string{"new-row-id"}, p.Updates[0].ResolvedSourceChunks)
	require.Equal(t, []string{"new-row-id"}, p.Updates[0].Item.SourceChunks)
}

func TestWikiMapArtifactPayloadNeverSerializesDatabaseChunkID(t *testing.T) {
	p := wikiMapArtifactPayload{SchemaVersion: wikiMapArtifactSchemaVersion, KnowledgeID: "doc-1", Updates: []wikiMapCachedUpdate{{Item: extractedItem{SourceChunks: nil}, SourceChunks: []wikiMapChunkRef{{StableIdentity: "stable-a", IdentityVersion: contentkey.ChunkIdentityVersion}}, ResolvedSourceChunks: []string{"ephemeral-row-id"}}}}
	b, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(b), "ephemeral-row-id")
	require.Contains(t, string(b), "stable-a")
}

func TestBindWikiMapChunkRefsRejectsMissingDuplicateAndOldVersion(t *testing.T) {
	for name, refs := range map[string][]wikiMapChunkRef{
		"missing":     {{StableIdentity: "gone", IdentityVersion: contentkey.ChunkIdentityVersion}},
		"duplicate":   {{StableIdentity: "stable-a", IdentityVersion: contentkey.ChunkIdentityVersion}, {StableIdentity: "stable-a", IdentityVersion: contentkey.ChunkIdentityVersion}},
		"old version": {{StableIdentity: "stable-a", IdentityVersion: "chunk-identity-v0"}},
	} {
		t.Run(name, func(t *testing.T) {
			p := &wikiMapArtifactPayload{Updates: []wikiMapCachedUpdate{{SourceChunks: refs}}}
			require.Error(t, bindWikiMapChunkRefs(p, map[string]string{"stable-a": "row-a"}))
		})
	}
}

func TestWikiMapSourceChunksFiltersDerivedAndRejectsUnsafeIdentitySets(t *testing.T) {
	valid := &types.Chunk{ID: "row-a", IsEnabled: true, ChunkType: types.ChunkTypeText, Content: "source", StableIdentity: "stable-a", IdentityVersion: contentkey.ChunkIdentityVersion}
	derived := &types.Chunk{ID: "summary", IsEnabled: true, ChunkType: types.ChunkTypeSummary, Content: "must not affect map key"}
	source, stableToID, _, eligible := wikiMapSourceChunks([]*types.Chunk{derived, valid})
	require.True(t, eligible)
	require.Equal(t, []*types.Chunk{valid}, source)
	require.Equal(t, "row-a", stableToID["stable-a"])

	_, _, _, eligible = wikiMapSourceChunks([]*types.Chunk{valid, {ID: "row-b", IsEnabled: true, ChunkType: types.ChunkTypeText, Content: "duplicate source", StableIdentity: "stable-a", IdentityVersion: contentkey.ChunkIdentityVersion}})
	require.False(t, eligible)
	_, _, _, eligible = wikiMapSourceChunks([]*types.Chunk{{ID: "legacy", IsEnabled: true, ChunkType: types.ChunkTypeText, Content: "legacy source"}})
	require.False(t, eligible)
}

func TestWikiMapArtifactKeyInvalidationDimensions(t *testing.T) {
	base := wikiMapArtifactKey(1, "input-a", "model-a", "revision-a", "prompt-a", "config-a", "producer-a")
	cases := map[string]string{
		"tenant":   wikiMapArtifactKey(2, "input-a", "model-a", "revision-a", "prompt-a", "config-a", "producer-a"),
		"content":  wikiMapArtifactKey(1, "input-b", "model-a", "revision-a", "prompt-a", "config-a", "producer-a"),
		"model":    wikiMapArtifactKey(1, "input-a", "model-b", "revision-a", "prompt-a", "config-a", "producer-a"),
		"revision": wikiMapArtifactKey(1, "input-a", "model-a", "revision-b", "prompt-a", "config-a", "producer-a"),
		"prompt":   wikiMapArtifactKey(1, "input-a", "model-a", "revision-a", "prompt-b", "config-a", "producer-a"),
		"config":   wikiMapArtifactKey(1, "input-a", "model-a", "revision-a", "prompt-a", "config-b", "producer-a"),
		"producer": wikiMapArtifactKey(1, "input-a", "model-a", "revision-a", "prompt-a", "config-a", "producer-b"),
	}
	for dimension, key := range cases {
		t.Run(dimension, func(t *testing.T) { require.NotEqual(t, base, key) })
	}
}
