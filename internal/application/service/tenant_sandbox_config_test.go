package service

import (
	"context"
	stderrors "errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// testGlobalSandboxConfig mirrors a deployment's WEKNORA_SANDBOX_* values. Named
// configs inherit none of the provider fields from it; it is here so the service
// under test is wired the way production wires it.
func testGlobalSandboxConfig() *sandbox.Config {
	cfg := sandbox.DefaultConfig()
	cfg.Type = sandbox.SandboxTypeE2B
	cfg.E2BAPIURL = "https://api.e2b.app"
	cfg.E2BSandboxDomain = "e2b.app"
	cfg.E2BAPIKey = "global-key"
	cfg.E2BTemplate = "global-template"
	return cfg
}

func e2bCfg(key, url, domain, template string, ttl int) *types.TenantSandboxConfig {
	return &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey: key, APIURL: url, SandboxDomain: domain, TemplateID: template,
			E2BSandboxTTLSeconds: ttl,
		},
	}
}

func cubeCfg(key, apiURL, proxyURL, domain string) *types.TenantSandboxConfig {
	return &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIKey: key, APIURL: apiURL, ProxyURL: proxyURL, SandboxDomain: domain,
		},
	}
}

func TestSandboxIdentityChanged(t *testing.T) {
	tests := []struct {
		name     string
		old, new *types.TenantSandboxConfig
		want     bool
	}{
		// Control plane: losing these loses the ability to clean up, and the
		// leak bills forever.
		{
			name: "api key rotation changes identity",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new:  e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
			want: true,
		},
		{
			name: "endpoint change changes identity",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new:  e2bCfg("key-a", "https://self.hosted", "e2b.app", "t1", 300),
			want: true,
		},
		{
			name: "provider switch changes identity",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new:  cubeCfg("key-a", "https://cube.example.com", "https://proxy.example.com", "cube.app"),
			want: true,
		},
		// Data plane: the control plane still works so cleanup stays possible,
		// but every envd request now goes to the wrong host, so every live
		// session on this config fails at once.
		{
			name: "e2b sandbox domain change strands live sessions",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new:  e2bCfg("key-a", "https://api.e2b.app", "e2b.dev", "t1", 300),
			want: true,
		},
		{
			name: "cube proxy url change strands live sessions",
			old:  cubeCfg("key-a", "https://cube.example.com", "https://proxy.example.com", "cube.app"),
			new:  cubeCfg("key-a", "https://cube.example.com", "https://proxy2.example.com", "cube.app"),
			want: true,
		},
		{
			name: "cube sandbox domain change strands live sessions",
			old:  cubeCfg("key-a", "https://cube.example.com", "https://proxy.example.com", "cube.app"),
			new:  cubeCfg("key-a", "https://cube.example.com", "https://proxy.example.com", "cube2.app"),
			want: true,
		},
		// Not identity changes: these only shape FUTURE sandboxes, so refusing
		// them would be pure friction.
		{
			name: "template change only affects future sandboxes",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t2", 300),
			want: false,
		},
		{
			name: "ttl change is not an identity change",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 900),
			want: false,
		},
		{
			// Nothing is inherited, so filling in an endpoint that was blank
			// really does re-point the config at a different account.
			name: "filling in a blank endpoint changes identity",
			old: &types.TenantSandboxConfig{
				SandboxType: "e2b",
				E2B:         &types.E2BSandboxConfig{APIKey: "key-a"},
			},
			new:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 0),
			want: true,
		},
		{
			// Only the ACTIVE provider's fields describe where the sandboxes
			// are; a leftover sub-struct from an earlier switch must not count.
			name: "inactive provider fields are ignored",
			old: &types.TenantSandboxConfig{
				SandboxType: "e2b",
				E2B:         &types.E2BSandboxConfig{APIKey: "key-a"},
				Cube:        &types.CubeSandboxConfig{APIKey: "stale-a"},
			},
			new: &types.TenantSandboxConfig{
				SandboxType: "e2b",
				E2B:         &types.E2BSandboxConfig{APIKey: "key-a"},
				Cube:        &types.CubeSandboxConfig{APIKey: "stale-b"},
			},
			want: false,
		},
		{
			name: "no previous config means nothing to strand",
			old:  nil,
			new:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, SandboxIdentityChanged(tt.old, tt.new))
		})
	}
}

// The UI never sends the real API key back - it echoes a mask. Comparing the
// incoming payload directly would therefore report an identity change on EVERY
// save and make the config permanently uneditable. This pins the required
// ordering: merge first, judge second.
func TestSandboxIdentityChangedAfterMaskMerge(t *testing.T) {
	stored := e2bCfg("real-key", "https://api.e2b.app", "e2b.app", "t1", 300)
	incoming := e2bCfg(types.RedactedSecretPlaceholder, "https://api.e2b.app", "e2b.app", "t1", 300)

	merged := types.MergeSandboxConfigForUpdate(incoming, stored)

	require.False(t,
		SandboxIdentityChanged(stored, merged),
		"a masked key that merges back to the stored one is not a rotation")
}

// An admin re-points a config precisely because its old endpoint died, so the
// verdict must not depend on that endpoint being reachable or even resolvable.
// Routing this through the SSRF-guarding resolver would lock the config down at
// the worst possible moment.
func TestSandboxIdentityChangedJudgesUnreachableOldEndpoint(t *testing.T) {
	dead := cubeCfg("key-a", "https://decommissioned.invalid", "https://proxy.invalid", "cube.app")
	live := cubeCfg("key-a", "https://cube.example.com", "https://proxy.invalid", "cube.app")

	require.True(t, SandboxIdentityChanged(dead, live))
	require.False(t, SandboxIdentityChanged(dead, dead))
}

// fakeConfigRepo records cordon transitions so tests can assert the sequence
// that prevents old credentials from being overwritten while they still own
// provider resources.
type fakeConfigRepo struct {
	entity *types.TenantSandboxConfigEntity
	policy *types.TenantSandboxConfigEntity

	events  []string
	updated *types.TenantSandboxConfigEntity
	deleted bool

	onSetCordon func()
	onUpdate    func()

	clearCordonCtxErr error
}

func (f *fakeConfigRepo) Create(_ context.Context, e *types.TenantSandboxConfigEntity) error {
	if types.IsSandboxWorkspacePolicyRow(e) {
		f.policy = e
		return nil
	}
	f.entity = e
	return nil
}

func (f *fakeConfigRepo) GetByID(
	_ context.Context, _ uint64, _ string,
) (*types.TenantSandboxConfigEntity, error) {
	f.events = append(f.events, "get")
	return f.entity, nil
}

func (f *fakeConfigRepo) ListByTenant(
	context.Context, uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	var out []*types.TenantSandboxConfigEntity
	if f.entity != nil {
		out = append(out, f.entity)
	}
	if f.policy != nil {
		out = append(out, f.policy)
	}
	return out, nil
}

func (f *fakeConfigRepo) Update(
	_ context.Context, e *types.TenantSandboxConfigEntity,
) error {
	f.events = append(f.events, "write")
	f.updated = e
	if f.onUpdate != nil {
		f.onUpdate()
	}
	return nil
}

func (f *fakeConfigRepo) SoftDelete(_ context.Context, _ uint64, id string) error {
	f.events = append(f.events, "delete")
	if f.policy != nil && f.policy.ID == id {
		f.policy = nil
	}
	f.deleted = true
	return nil
}

func (f *fakeConfigRepo) SetCordon(_ context.Context, _ uint64, _ string, _ time.Time) error {
	f.events = append(f.events, "cordon")
	if f.onSetCordon != nil {
		f.onSetCordon()
	}
	return nil
}

func (f *fakeConfigRepo) ClearCordon(ctx context.Context, _ uint64, _ string) error {
	f.events = append(f.events, "uncordon")
	f.clearCordonCtxErr = ctx.Err()
	return nil
}

type stubAgentRepo struct {
	names []string
	err   error
}

func (s stubAgentRepo) ListNamesBySandboxConfigID(
	context.Context, uint64, string,
) ([]string, error) {
	return s.names, s.err
}

type stubProviderClient struct {
	inventories [][]sandbox.RemoteSandboxSummary

	listCalls    int
	deleted      []string
	deleteCtxErr error
}

func (s *stubProviderClient) List(
	ctx context.Context, _ sandbox.RemoteListFilter,
) ([]sandbox.RemoteSandboxSummary, error) {
	s.listCalls++
	if len(s.inventories) == 0 {
		return nil, nil
	}
	idx := s.listCalls - 1
	if idx >= len(s.inventories) {
		idx = len(s.inventories) - 1
	}
	return s.inventories[idx], nil
}

func (s *stubProviderClient) Delete(ctx context.Context, sandboxID string) error {
	s.deleted = append(s.deleted, sandboxID)
	s.deleteCtxErr = ctx.Err()
	return nil
}

func newTestConfigService(
	t *testing.T,
	repo *fakeConfigRepo,
	client *stubProviderClient,
	agents stubAgentRepo,
) *TenantSandboxConfigService {
	t.Helper()
	if client == nil {
		client = &stubProviderClient{}
	}
	svc := NewTenantSandboxConfigService(repo, agents, testGlobalSandboxConfig())
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return client, nil
	}
	return svc
}

// An identity change while sandboxes are alive must be refused, and the stored
// credentials must remain untouched so cleanup stays possible.
func TestUpdateRefusesIdentityChangeWhileSandboxesLive(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{inventories: [][]sandbox.RemoteSandboxSummary{{
		{ID: "sb-1", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-1"}},
	}}}, stubAgentRepo{})

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})

	require.ErrorIs(t, err, ErrSandboxesStillLive)
	require.Nil(t, repo.updated, "credentials must not be overwritten")
	require.Equal(t, []string{"get", "cordon", "uncordon"}, repo.events,
		"the cordon must be committed before the inventory check and released after")
}

// With no live sandboxes the write proceeds, and the cordon brackets it.
func TestUpdateIdentityChangeCordonsThenWrites(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})

	require.NoError(t, err)
	require.Equal(t, []string{"get", "cordon", "write", "uncordon"}, repo.events)
}

// A non-identity edit must not pay for a cordon or a provider round-trip.
func TestUpdateNonIdentityEditSkipsCordon(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	client := &stubProviderClient{}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod renamed",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 900),
	})

	require.NoError(t, err)
	require.Equal(t, []string{"get", "write"}, repo.events)
	require.Equal(t, "prod renamed", repo.updated.Name)
	require.Equal(t, 0, client.listCalls)
}

func TestUpdateRefusalCarriesInventory(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{inventories: [][]sandbox.RemoteSandboxSummary{{
		{ID: "sb-1", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-1"}},
		{ID: "sb-2", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-2"}},
	}}}, stubAgentRepo{names: []string{"analyst", "writer"}})

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})

	var liveErr *SandboxesStillLiveError
	require.ErrorAs(t, err, &liveErr)
	require.Equal(t, 2, liveErr.Inventory.SandboxCount)
	require.ElementsMatch(t, []string{"s-1", "s-2"}, liveErr.Inventory.SessionIDs)
	require.Equal(t, []string{"analyst", "writer"}, liveErr.Inventory.AgentNames)
}

func TestUpdateSweepsSandboxCreatedDuringCordonWindow(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	ctx, cancel := context.WithCancel(context.Background())
	client := &stubProviderClient{inventories: [][]sandbox.RemoteSandboxSummary{
		nil,
		{{ID: "sb-race", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-race"}}},
	}}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}, onUpdate: cancel}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	_, err := svc.Update(ctx, 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})

	require.NoError(t, err)
	require.Equal(t, []string{"get", "cordon", "write", "uncordon"}, repo.events)
	require.Equal(t, []string{"sb-race"}, client.deleted)
	require.NoError(t, client.deleteCtxErr, "post-write sweep must survive request cancellation")
}

func TestUpdateClearsCordonWhenRequestContextCancelled(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	ctx, cancel := context.WithCancel(context.Background())
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}, onSetCordon: cancel}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})

	_, err := svc.Update(ctx, 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})

	require.NoError(t, err)
	require.Equal(t, []string{"get", "cordon", "write", "uncordon"}, repo.events)
	require.NoError(t, repo.clearCordonCtxErr, "cordon release must survive request cancellation")
}

func TestDeleteRefusesWhileSandboxesLive(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{inventories: [][]sandbox.RemoteSandboxSummary{{
		{ID: "sb-1", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-1"}},
	}}}, stubAgentRepo{names: []string{"analyst"}})

	err := svc.Delete(context.Background(), 7, "cfg-a", false)

	require.ErrorIs(t, err, ErrSandboxesStillLive)
	var liveErr *SandboxesStillLiveError
	require.ErrorAs(t, err, &liveErr)
	require.Equal(t, 1, liveErr.Inventory.SandboxCount)
	require.Equal(t, []string{"s-1"}, liveErr.Inventory.SessionIDs)
	require.Equal(t, []string{"analyst"}, liveErr.Inventory.AgentNames)
	require.False(t, repo.deleted)
	require.Equal(t, []string{"get"}, repo.events)
}

// "We cannot tell" must not read as "nothing there": refuse, and say which of
// the two it is, because only this one is force-overridable.
func TestDeleteRefusesWhenInventoryUnverifiable(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return nil, stderrors.New("dial tcp: no such host")
	}

	err := svc.Delete(context.Background(), 7, "cfg-a", false)

	require.ErrorIs(t, err, ErrSandboxInventoryUnverifiable)
	require.NotErrorIs(t, err, ErrSandboxesStillLive)
	require.False(t, repo.deleted)
}

// Deletion has no "create a second config" way out, so a config whose endpoint
// vanished must remain removable once an admin takes responsibility.
func TestDeleteForceRemovesConfigWithUnverifiableInventory(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return nil, stderrors.New("dial tcp: no such host")
	}

	require.NoError(t, svc.Delete(context.Background(), 7, "cfg-a", true))
	require.True(t, repo.deleted)
}

// force covers "cannot verify", never "verified live". Letting it through here
// would strand paused sandboxes nobody can reach again - the exact leak this
// whole flow exists to prevent.
func TestDeleteForceStillRefusesVerifiedLiveSandboxes(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{inventories: [][]sandbox.RemoteSandboxSummary{{
		{ID: "sb-1", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-1"}},
	}}}, stubAgentRepo{})

	err := svc.Delete(context.Background(), 7, "cfg-a", true)

	require.ErrorIs(t, err, ErrSandboxesStillLive)
	require.False(t, repo.deleted)
}

// The card still has to render, and the agent warning comes from our own DB, so
// an unreachable provider is a flag rather than an error.
func TestInventoryMarksUnverifiableInsteadOfFailing(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{names: []string{"analyst"}})
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return nil, stderrors.New("dial tcp: no such host")
	}

	inv, err := svc.Inventory(context.Background(), 7, "cfg-a")

	require.NoError(t, err)
	require.True(t, inv.Unverifiable)
	require.Zero(t, inv.SandboxCount)
	require.Equal(t, []string{"analyst"}, inv.AgentNames)
}

// When old credentials cannot reach the provider, the update proceeds anyway
// so the admin can fix a mistyped key; sandboxes_still_live still blocks when
// the old credentials can list live instances.
func TestUpdateProceedsWhenInventoryUnverifiable(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return nil, stderrors.New("dial tcp: no such host")
	}

	updated, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.NotNil(t, repo.updated)
	require.Equal(t, "key-b", repo.updated.Config.E2B.APIKey)
	require.Equal(t, []string{"get", "cordon", "write", "uncordon"}, repo.events)
}

func TestDeleteSoftDeletesWhenEmpty(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})

	err := svc.Delete(context.Background(), 7, "cfg-a", false)

	require.NoError(t, err)
	require.True(t, repo.deleted)
	require.Equal(t, []string{"get", "delete"}, repo.events)
}

// Reporting success for a config that is not there would let the UI drop a card
// the workspace still has, and hide a wrong ID from the caller.
func TestDeleteAndInventoryReportMissingConfig(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	ctx := context.Background()

	err := svc.Delete(ctx, 7, "cfg-missing", false)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusNotFound, appErr.HTTPCode)
	require.False(t, repo.deleted)

	_, err = svc.Inventory(ctx, 7, "cfg-missing")
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusNotFound, appErr.HTTPCode)
}

func TestSanitizeSandboxConfigPreservesRedactedSecret(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	existing := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey: "stored-key", APIURL: "https://203.0.113.10", TemplateID: "t1",
		},
	}
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey:     types.RedactedSecretPlaceholder,
			APIURL:     "https://203.0.113.10",
			TemplateID: "t1",
		},
	}

	out, err := SanitizeSandboxConfig(incoming, existing)

	require.NoError(t, err)
	require.Equal(t, "stored-key", out.E2B.APIKey)
}

// Nothing is inherited from the deployment, so an incomplete config has to be
// refused when it is saved rather than at the first sandbox allocation.
func TestSanitizeSandboxConfigRejectsIncompleteConfig(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube:        &types.CubeSandboxConfig{APIURL: "https://203.0.113.10"},
	}

	_, err := SanitizeSandboxConfig(incoming, nil)

	require.ErrorIs(t, err, sandbox.ErrSandboxConfigIncomplete)
	require.Contains(t, err.Error(), "proxy_url")
}

// A masked key must still count as present: the merge restores it before the
// completeness check runs, otherwise every edit of a saved config would be
// rejected for a missing credential it never stopped having.
func TestSanitizeSandboxConfigCountsRedactedSecretAsPresent(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	existing := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "stored-key", TemplateID: "t1"},
	}
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey: types.RedactedSecretPlaceholder, TemplateID: "t2",
		},
	}

	out, err := SanitizeSandboxConfig(incoming, existing)

	require.NoError(t, err)
	require.Equal(t, "t2", out.E2B.TemplateID)
}

func TestSanitizeSandboxConfigRejectsUnsafeURL(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIURL: "http://169.254.169.254"},
	}

	_, err := SanitizeSandboxConfig(incoming, nil)

	require.ErrorIs(t, err, sandbox.ErrUnsafeOutboundURL)
}

func TestSanitizeSandboxConfigRejectsUnknownType(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{SandboxType: "quantum"}

	_, err := SanitizeSandboxConfig(incoming, nil)

	require.Error(t, err)
}

// Without an AES key the Value() hook would write these secrets as plaintext,
// so saving must be refused rather than silently downgrading storage security.
func TestSanitizeSandboxConfigRefusesSecretsWithoutAESKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "plaintext-risk"},
	}

	_, err := SanitizeSandboxConfig(incoming, nil)

	require.Error(t, err)

	// A config without secrets is still allowed in that deployment.
	_, err = SanitizeSandboxConfig(&types.TenantSandboxConfig{SandboxType: "local"}, nil)
	require.NoError(t, err)
}

func TestSandboxesStillLiveErrorSupportsErrorsIs(t *testing.T) {
	err := &SandboxesStillLiveError{Inventory: SandboxInventory{SandboxCount: 1}}

	require.True(t, stderrors.Is(err, ErrSandboxesStillLive))
}

func TestCreateRejectsNonNamedSandboxBackend(t *testing.T) {
	svc := newTestConfigService(t, &fakeConfigRepo{}, nil, stubAgentRepo{})

	_, err := svc.Create(context.Background(), 7, CreateSandboxConfigInput{
		Name:   "local-dev",
		Config: &types.TenantSandboxConfig{SandboxType: "local"},
	})

	require.ErrorIs(t, err, ErrNamedSandboxBackendUnsupported)
}

func TestWorkspaceScriptsDisabledPolicy(t *testing.T) {
	repo := &fakeConfigRepo{}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	ctx := context.Background()

	disabled, err := svc.WorkspaceScriptsDisabled(ctx, 7)
	require.NoError(t, err)
	require.False(t, disabled)

	require.NoError(t, svc.SetWorkspaceScriptsDisabled(ctx, 7, true))
	disabled, err = svc.WorkspaceScriptsDisabled(ctx, 7)
	require.NoError(t, err)
	require.True(t, disabled)

	list, err := svc.List(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, list)

	require.NoError(t, svc.SetWorkspaceScriptsDisabled(ctx, 7, false))
	disabled, err = svc.WorkspaceScriptsDisabled(ctx, 7)
	require.NoError(t, err)
	require.False(t, disabled)
}
