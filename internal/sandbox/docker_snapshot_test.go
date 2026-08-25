package sandbox

import (
	"context"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/stretchr/testify/require"
)

func TestDockerCreateSnapshotTagsASkillImage(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	ref, err := docker.CreateSnapshot(context.Background(), "container-1", "weknora-sk-cfg1-g1")
	require.NoError(t, err)
	require.Equal(t, "weknora-skill/weknora-sk-cfg1-g1", ref.ID)
	require.Len(t, engine.committed, 1)
	require.Equal(t, "weknora-skill/weknora-sk-cfg1-g1", engine.committed[0].Reference)
	require.False(t, engine.committed[0].NoPause,
		"the Engine must pause the container for the duration of the commit")
	require.Contains(t, engine.committed[0].Changes, "LABEL "+dockerSkillSnapshotLabel+"=true")
	require.Contains(t, engine.committed[0].Changes,
		"LABEL "+dockerSkillSnapshotSourceLabel+"=container-1")
}

func TestDockerCreateSnapshotRejectsEmptySandboxID(t *testing.T) {
	_, err := newTestDockerClient(t, newFakeDockerEngine()).
		CreateSnapshot(context.Background(), "  ", "n")
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestDockerDeleteSnapshotTreatsMissingAsSuccess(t *testing.T) {
	err := newTestDockerClient(t, newFakeDockerEngine()).
		DeleteSnapshot(context.Background(), "weknora-skill/missing")
	require.NoError(t, err)
}

func TestDockerDeleteSnapshotRejectsEmptySnapshotID(t *testing.T) {
	err := newTestDockerClient(t, newFakeDockerEngine()).
		DeleteSnapshot(context.Background(), "  ")
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestDockerSnapshotRoundTripAndListFilter(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)
	ctx := context.Background()

	first, err := docker.CreateSnapshot(ctx, "container-a", "weknora-sk-a-g1")
	require.NoError(t, err)
	second, err := docker.CreateSnapshot(ctx, "container-a", "weknora-sk-a-g2")
	require.NoError(t, err)
	_, err = docker.CreateSnapshot(ctx, "container-b", "weknora-sk-b-g1")
	require.NoError(t, err)

	all, err := docker.ListSnapshots(ctx, "")
	require.NoError(t, err)
	require.Len(t, all, 3)

	fromA, err := docker.ListSnapshots(ctx, "container-a")
	require.NoError(t, err)
	require.Len(t, fromA, 2)
	ids := map[string]struct{}{fromA[0].ID: {}, fromA[1].ID: {}}
	require.Contains(t, ids, first.ID)
	require.Contains(t, ids, second.ID)

	require.NoError(t, docker.DeleteSnapshot(ctx, first.ID))
	require.NoError(t, docker.DeleteSnapshot(ctx, first.ID), "deleting twice must stay idempotent")

	fromA, err = docker.ListSnapshots(ctx, "container-a")
	require.NoError(t, err)
	require.Len(t, fromA, 1)
	require.Equal(t, second.ID, fromA[0].ID)
}

func TestDockerListTemplatesHidesSkillSnapshots(t *testing.T) {
	engine := newFakeDockerEngine()
	engine.images = []image.Summary{
		{
			ID:       "sha256:base",
			RepoTags: []string{"weknora/sandbox:test"},
			Labels:   map[string]string{dockerTemplateLabel: "true"},
		},
		{
			ID:       "sha256:snap",
			RepoTags: []string{"weknora-skill/weknora-sk-cfg1-g1:latest"},
			Labels:   map[string]string{dockerSkillSnapshotLabel: "true"},
		},
	}
	docker := newTestDockerClient(t, engine)

	templates, err := docker.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "weknora/sandbox:test", templates[0].ID)
}

func TestDockerCreateDoesNotPullAMissingSkillSnapshot(t *testing.T) {
	engine := newFakeDockerEngine()
	docker := newTestDockerClient(t, engine)

	_, err := docker.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "weknora-skill/weknora-sk-cfg1-g1",
	})
	require.True(t, IsRemoteInvalidRequest(err), err)
	require.Empty(t, engine.pulled,
		"a local commit must not be fetched from a registry")
}

func TestDockerSanitizeImageName(t *testing.T) {
	require.Equal(t, "weknora-sk-cfg1-g1", dockerSanitizeImageName("weknora-sk-cfg1-g1"))
	require.Equal(t, "abc-def", dockerSanitizeImageName("ABC_DEF"))
	require.Equal(t, "snap", dockerSanitizeImageName("--snap--"))
	require.Empty(t, dockerSanitizeImageName("***"))
}
