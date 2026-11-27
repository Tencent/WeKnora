package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// reparseRefreshRepo records the metadata column persists that reparseKnowledge
// performs when it rewrites the per-document process overrides snapshot.
type reparseRefreshRepo struct {
	interfaces.KnowledgeRepository
	knowledge      *types.Knowledge
	metadataWrites int
	updateCalls    int
}

func (r *reparseRefreshRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, _ string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *reparseRefreshRepo) UpdateKnowledge(_ context.Context, _ *types.Knowledge) error {
	r.updateCalls++
	return nil
}

func (r *reparseRefreshRepo) UpdateKnowledgeColumn(
	_ context.Context, _ string, column string, _ interface{},
) error {
	if column == "metadata" {
		r.metadataWrites++
	}
	return nil
}

type reparseRefreshKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *reparseRefreshKBService) GetKnowledgeBaseByID(
	_ context.Context, _ string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func newManualKnowledgeForRefresh(t *testing.T, overrides *types.KnowledgeProcessOverrides) *types.Knowledge {
	t.Helper()
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            types.KnowledgeTypeManual,
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
	}
	require.NoError(t, knowledge.SetManualMetadata(
		types.NewManualKnowledgeMetadata("# content", types.ManualKnowledgeStatusPublish, 1),
	))
	if overrides != nil {
		require.NoError(t, knowledge.SetProcessOverrides(overrides))
	}
	return knowledge
}

func reparseRefreshContext(t *testing.T, kb *types.KnowledgeBase) context.Context {
	t.Helper()
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx, err := access.WithKBTaskWrite(ctx, kb, 7)
	require.NoError(t, err)
	return ctx
}

// An override-less reparse rebuilds with the knowledge base's current
// indexing strategy: the graph pins frozen at upload time must be dropped so
// a KB-level strategy change reaches existing documents (#5305), while
// per-document choices such as the summary toggle keep their stored values.
func TestReparseKnowledgeNilOverridesRefreshGraphPinsFromKB(t *testing.T) {
	graphPin := false
	summaryChoice := false
	knowledge := newManualKnowledgeForRefresh(t, &types.KnowledgeProcessOverrides{
		GraphEnabled:   &graphPin,
		SummaryEnabled: &summaryChoice,
	})
	repo := &reparseRefreshRepo{knowledge: knowledge}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true},
	}
	queue := &wikiEnqueueFailureTaskQueue{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &reparseRefreshKBService{kb: kb},
		task:      queue,
	}

	got, err := svc.ReparseKnowledge(reparseRefreshContext(t, kb), knowledge.ID, nil)

	require.NoError(t, err)
	require.Equal(t, knowledge.ID, got.ID)
	require.Equal(t, 1, repo.metadataWrites, "the refreshed snapshot must be persisted")
	stored, storedErr := got.ProcessOverrides()
	require.NoError(t, storedErr)
	require.NotNil(t, stored, "the snapshot is rewritten, not removed")
	require.Nil(t, stored.GraphEnabled, "the upload-time graph_enabled=false pin must be dropped")
	require.Nil(t, stored.ExtractConfig, "the upload-time extract_config pin must be dropped")
	require.NotNil(t, stored.SummaryEnabled, "per-document choices keep their stored values")
	require.False(t, *stored.SummaryEnabled)
	eff := ResolveProcessConfig(kb, stored)
	require.True(t, eff.GraphEnabled,
		"the worker re-reads the stored snapshot; it must resolve graph on for a graph-enabled KB")
	require.False(t, eff.SummaryEnabled, "the preserved summary choice still wins")
}

// Explicit overrides still replace the whole snapshot: fields the caller did
// not mention fall back to the KB instead of keeping stored values.
func TestReparseKnowledgeExplicitOverridesReplaceWholeSnapshot(t *testing.T) {
	graphPin := true
	summaryChoice := true
	knowledge := newManualKnowledgeForRefresh(t, &types.KnowledgeProcessOverrides{
		GraphEnabled:   &graphPin,
		SummaryEnabled: &summaryChoice,
	})
	repo := &reparseRefreshRepo{knowledge: knowledge}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true},
	}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &reparseRefreshKBService{kb: kb},
		task:      &wikiEnqueueFailureTaskQueue{},
	}

	pinned := false
	got, err := svc.ReparseKnowledge(reparseRefreshContext(t, kb), knowledge.ID,
		&types.KnowledgeProcessOverrides{GraphEnabled: &pinned})

	require.NoError(t, err)
	require.Equal(t, knowledge.ID, got.ID)
	stored, storedErr := got.ProcessOverrides()
	require.NoError(t, storedErr)
	require.NotNil(t, stored.GraphEnabled)
	require.False(t, *stored.GraphEnabled, "an explicit pin wins even against a graph-enabled KB")
	require.Nil(t, stored.SummaryEnabled, "unmentioned fields fall back to the KB, not to stored values")
}

// A knowledge base without a stored snapshot has nothing stale to drop: nil
// stays nil, nothing is persisted, and the config already resolves from the KB.
func TestReparseKnowledgeNilWithoutStoredSnapshotStaysNil(t *testing.T) {
	knowledge := newManualKnowledgeForRefresh(t, nil)
	repo := &reparseRefreshRepo{knowledge: knowledge}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		IndexingStrategy: types.IndexingStrategy{GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true},
	}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &reparseRefreshKBService{kb: kb},
		task:      &wikiEnqueueFailureTaskQueue{},
	}

	got, err := svc.ReparseKnowledge(reparseRefreshContext(t, kb), knowledge.ID, nil)

	require.NoError(t, err)
	require.Equal(t, knowledge.ID, got.ID)
	require.Zero(t, repo.metadataWrites, "nothing to refresh, nothing to persist")
	stored, storedErr := got.ProcessOverrides()
	require.NoError(t, storedErr)
	require.Nil(t, stored)
	require.True(t, ResolveProcessConfig(kb, stored).GraphEnabled)
}

// The refresh keeps the validation parity of an explicit request while
// preserving upload-time VLM choices: rebuilding an image document without
// overrides still passes the image gate through the preserved VLM config,
// where a snapshot-less {} request would fail it.
func TestValidateProcessOverridesRefreshKeepsStoredVLMChoiceForImages(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1"} // KB-level VLM disabled
	vlmChoice := &types.VLMConfig{Enabled: true, ModelID: "vlm-1"}

	err := ValidateProcessOverrides(context.Background(), kb,
		&types.KnowledgeProcessOverrides{VLMConfig: vlmChoice}, []string{"png"})

	require.NoError(t, err, "the preserved VLM choice satisfies the image gate")

	err = ValidateProcessOverrides(context.Background(), kb,
		&types.KnowledgeProcessOverrides{}, []string{"png"})
	require.Error(t, err, "an empty request still fails the gate without any VLM config")
	require.ErrorContains(t, err, "VLM")
}
