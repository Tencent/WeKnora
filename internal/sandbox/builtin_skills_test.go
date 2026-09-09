package sandbox

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

type builtinCatalogClient struct {
	*fakeRemoteClient
	RemoteTemplateCatalog // Unexpected administration calls panic.
	templates             []RemoteTemplate
}

func (c *builtinCatalogClient) ListTemplates(context.Context) ([]RemoteTemplate, error) {
	return append([]RemoteTemplate(nil), c.templates...), nil
}

func TestCloudBuiltinDiscoveryNeverStartsOrResumesSandbox(t *testing.T) {
	for _, provider := range []SandboxType{SandboxTypeCube, SandboxTypeE2B} {
		t.Run(string(provider), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Type, cfg.CubeTemplate, cfg.E2BTemplate = provider, "tpl-test", "tpl-test"
			cfg.TemplateSkills = &types.TemplateSkillsDeclaration{
				Provider: string(provider), Endpoint: TemplateSkillsEndpoint(cfg), TemplateID: "tpl-test",
				Revision: "build-1", Manifest: *builtin.PublishedManifest(),
			}
			client := &builtinCatalogClient{
				fakeRemoteClient: newFakeRemoteClient(provider),
				templates:        []RemoteTemplate{{ID: "tpl-test", Version: "build-1", Status: "ready"}},
			}
			mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
				Config: cfg, Client: client,
				Store: NewMemorySessionSandboxBindingStore(), Checker: &fakeSessionExistenceChecker{exists: true},
				SkipHealthProbe: true,
			})
			require.NoError(t, err)
			ctx := types.WithSandboxTenantID(context.Background(), 7)
			manifest, err := mgr.BuiltinSkills(ctx, "session-1")
			require.NoError(t, err)
			require.Len(t, builtin.CompatibleEntries(manifest), 8)
			require.Zero(t, client.createCount)
			require.Empty(t, client.connectIDs)
			require.Empty(t, client.execRequests)
			require.Empty(t, client.writeFiles)

			// Create only when execution needs the session, preserving its manifest.
			_, err = mgr.lifecycle.Resolve(ctx, SessionSandboxKey{TenantID: 7, SessionID: "session-1"})
			require.NoError(t, err)
			pauseAllFakeSandboxes(t, client.fakeRemoteClient)
			client.templates[0].Version = "build-2"
			current, err := mgr.BuiltinSkills(ctx, "")
			require.NoError(t, err)
			require.Nil(t, current, "a changed template must invalidate the declaration")
			connects := len(client.connectIDs)
			pinned, err := mgr.BuiltinSkills(ctx, "session-1")
			require.NoError(t, err)
			require.Len(t, builtin.CompatibleEntries(pinned), 8)
			require.Equal(t, connects, len(client.connectIDs), "metadata must not resume a paused sandbox")
			require.Empty(t, client.getIDs, "SDK Get may call Connect; metadata must use List")
			require.Equal(t, 1, client.createCount)
			require.Empty(t, client.execRequests)
			// Old sessions with no creation-time declaration remain unknown.
			for _, r := range client.sandboxes {
				delete(r.metadata, builtinManifestMetadata)
			}
			pinned, err = mgr.BuiltinSkills(ctx, "session-1")
			require.NoError(t, err)
			require.Nil(t, pinned)
		})
	}
}

func TestTemplateDeclarationIsScopedToProviderEndpointAndRevision(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeCube
	cfg.CubeAPIURL = "https://cluster.example/"
	cfg.TemplateSkills = &types.TemplateSkillsDeclaration{
		Provider:   "cube",
		Endpoint:   "https://cluster.example",
		TemplateID: "tpl-1",
		Revision:   "v1",
		Manifest:   *builtin.PublishedManifest(),
	}
	for _, mutate := range []func(*types.TemplateSkillsDeclaration){
		func(d *types.TemplateSkillsDeclaration) { d.Provider = "e2b" },
		func(d *types.TemplateSkillsDeclaration) { d.Endpoint = "https://another.example" },
		func(d *types.TemplateSkillsDeclaration) { d.TemplateID = "tpl-2" },
		func(d *types.TemplateSkillsDeclaration) { d.Revision = "v2" },
		func(d *types.TemplateSkillsDeclaration) { d.Revision = "" },
	} {
		copyCfg := *cfg
		d := *cfg.TemplateSkills
		mutate(&d)
		copyCfg.TemplateSkills = &d
		templates := []RemoteTemplate{{ID: "tpl-1", Version: "v1", Status: "ready"}}
		ApplyTemplateSkills(&copyCfg, templates)
		require.Nil(t, templates[0].BuiltinSkills)
	}
}

type builtinDockerEngine struct {
	*fakeDockerEngine
	manifests       map[string]string
	inspectedImages []string
}

func (e *builtinDockerEngine) ImageInspect(
	_ context.Context,
	ref string,
	_ ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	e.inspectedImages = append(e.inspectedImages, ref)
	var result image.InspectResponse
	raw, _ := json.Marshal(
		map[string]any{
			"Id":     ref,
			"Config": map[string]any{"Labels": map[string]string{builtin.ManifestLabel: e.manifests[ref]}},
		},
	)
	_ = json.Unmarshal(raw, &result)
	return client.ImageInspectResult{InspectResponse: result}, nil
}

func TestDockerBuiltinDiscoveryUsesLabelsAndPinnedImageID(t *testing.T) {
	raw, _ := json.Marshal(builtin.PublishedManifest())
	e := &builtinDockerEngine{
		fakeDockerEngine: newFakeDockerEngine(),
		manifests:        map[string]string{"sha256:old": string(raw)},
	}
	e.inspect["container-1"] = container.InspectResponse{ID: "container-1", Image: "sha256:old"}
	docker := newTestDockerClient(t, e.fakeDockerEngine)
	docker.api = e
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeDocker
	cfg.DockerImage = "tag-now-points-elsewhere"
	store := NewMemorySessionSandboxBindingStore()
	mgr, err := NewSessionBoundManager(
		SessionBoundManagerConfig{
			Config:          cfg,
			Client:          docker,
			Store:           store,
			Checker:         &fakeSessionExistenceChecker{exists: true},
			SkipHealthProbe: true,
		},
	)
	require.NoError(t, err)
	ctx := types.WithSandboxTenantID(context.Background(), 7)
	key := SessionSandboxKey{TenantID: 7, SessionID: "session-1"}
	_, err = store.Create(
		ctx,
		key,
		SessionSandboxBinding{
			Version:    SessionSandboxBindingVersion,
			TenantID:   7,
			SessionID:  key.SessionID,
			Provider:   SandboxTypeDocker,
			SandboxID:  "container-1",
			TemplateID: "old-tag",
			CreatedAt:  time.Now(),
		},
	)
	require.NoError(t, err)
	m, err := mgr.BuiltinSkills(ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, builtin.CompatibleEntries(m), 8)
	require.Equal(t, []string{"sha256:old"}, e.inspectedImages)
	require.Empty(t, e.created)
	require.Empty(t, e.started)
	require.Empty(t, e.unpaused)
	require.Empty(t, e.execOptions)
}
