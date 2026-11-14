package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// testGlobalSandboxConfig supplies built-in runtime tuning values. Named
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

func TestSandboxConfigHasSecretsIncludesInjectedHeaders(t *testing.T) {
	cfg := &types.TenantSandboxConfig{
		Network: &types.SandboxNetworkPolicy{
			CubeRules: []types.CubeEgressRule{{
				Inject: []types.CubeHeaderInject{{Secret: "cube-secret"}},
			}},
		},
	}
	require.True(t, sandboxConfigHasSecrets(cfg))

	cfg.Network.CubeRules = nil
	cfg.Network.E2BHostRules = []types.E2BHostRule{{
		Headers: map[string]string{"X-Key": "e2b-secret"},
	}}
	require.True(t, sandboxConfigHasSecrets(cfg))

	cfg.Network = &types.SandboxNetworkPolicy{
		DenyEgressByDefault: true,
		CubeRules: []types.CubeEgressRule{{
			Inject: []types.CubeHeaderInject{{Header: "X-Key"}},
		}},
	}
	require.False(t, sandboxConfigHasSecrets(cfg))
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
			name: "cube dns change only affects future templates",
			old:  cubeCfg("key-a", "https://cube.example.com", "https://proxy.example.com", "cube.app"),
			new: func() *types.TenantSandboxConfig {
				cfg := cubeCfg("key-a", "https://cube.example.com", "https://proxy.example.com", "cube.app")
				cfg.Cube.DNSServers = []string{"8.8.8.8"}
				return cfg
			}(),
			want: false,
		},
		{
			name: "private endpoint policy changes transport identity",
			old:  e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
			new: func() *types.TenantSandboxConfig {
				cfg := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
				cfg.AllowPrivateEndpoints = true
				return cfg
			}(),
			want: true,
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
	others []*types.TenantSandboxConfigEntity

	events  []string
	updated *types.TenantSandboxConfigEntity
	deleted bool

	onSetCordon func()
	onUpdate    func()
	updateErr   error

	clearCordonCtxErr error
	cordonedAt        *time.Time
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
	out = append(out, f.others...)
	return out, nil
}

func (f *fakeConfigRepo) ListAll(context.Context) ([]*types.TenantSandboxConfigEntity, error) {
	return f.ListByTenant(context.Background(), 0)
}

func (f *fakeConfigRepo) Update(
	_ context.Context, e *types.TenantSandboxConfigEntity,
) error {
	f.events = append(f.events, "write")
	f.updated = e
	if f.onUpdate != nil {
		f.onUpdate()
	}
	if f.updateErr != nil {
		return f.updateErr
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

func (f *fakeConfigRepo) SetCordon(_ context.Context, _ uint64, _ string, at time.Time) error {
	f.events = append(f.events, "cordon")
	stamped := at
	f.cordonedAt = &stamped
	if f.onSetCordon != nil {
		f.onSetCordon()
	}
	return nil
}

func (f *fakeConfigRepo) RefreshCordon(
	_ context.Context, _ uint64, _ string, previous, next time.Time,
) error {
	if f.cordonedAt == nil || !f.cordonedAt.Equal(previous) {
		return repository.ErrSandboxConfigCordoned
	}
	stamped := next
	f.cordonedAt = &stamped
	return nil
}

func (f *fakeConfigRepo) ClearCordon(ctx context.Context, _ uint64, _ string) error {
	f.events = append(f.events, "uncordon")
	f.cordonedAt = nil
	f.clearCordonCtxErr = ctx.Err()
	return nil
}

func (f *fakeConfigRepo) ClearCordonIf(ctx context.Context, tenantID uint64, id string, at time.Time) error {
	if f.cordonedAt == nil || !f.cordonedAt.Equal(at) {
		return nil
	}
	return f.ClearCordon(ctx, tenantID, id)
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
	inventories    [][]sandbox.RemoteSandboxSummary
	templates      []sandbox.RemoteTemplate
	ensured        *sandbox.RemoteTemplate
	ensuredDesktop *sandbox.RemoteTemplate
	// ensureDelay widens the window in which concurrent provisioning requests
	// overlap, which is the only way to observe whether they were collapsed.
	ensureDelay      time.Duration
	ensureDesktopErr error

	ensureCalls         atomic.Int32
	ensureDesktopCalls  atomic.Int32
	replaceCalls        atomic.Int32
	replaceDesktopCalls atomic.Int32

	listCalls        int
	deleted          []string
	deletedTemplates []string
	deleteCtxErr     error
	replaced         *sandbox.RemoteTemplate
}

func (s *stubProviderClient) ListTemplates(context.Context) ([]sandbox.RemoteTemplate, error) {
	return append([]sandbox.RemoteTemplate(nil), s.templates...), nil
}

func (s *stubProviderClient) EnsureStandardTemplate(context.Context) (*sandbox.RemoteTemplate, error) {
	s.ensureCalls.Add(1)
	if s.ensureDelay > 0 {
		time.Sleep(s.ensureDelay)
	}
	if s.ensured != nil {
		copy := *s.ensured
		return &copy, nil
	}
	return &sandbox.RemoteTemplate{ID: "tpl-weknora", Name: "weknora", Status: "building", Standard: true}, nil
}

func (s *stubProviderClient) ReplaceStandardTemplate(ctx context.Context) (*sandbox.RemoteTemplate, error) {
	s.replaceCalls.Add(1)
	if s.ensureDelay > 0 {
		time.Sleep(s.ensureDelay)
	}
	if s.replaced != nil {
		copyTpl := *s.replaced
		return &copyTpl, nil
	}
	return s.EnsureStandardTemplate(ctx)
}

func (s *stubProviderClient) EnsureDesktopTemplate(context.Context) (*sandbox.RemoteTemplate, error) {
	s.ensureDesktopCalls.Add(1)
	if s.ensureDelay > 0 {
		time.Sleep(s.ensureDelay)
	}
	if s.ensureDesktopErr != nil {
		return nil, s.ensureDesktopErr
	}
	if s.ensuredDesktop != nil {
		copyTpl := *s.ensuredDesktop
		return &copyTpl, nil
	}
	return &sandbox.RemoteTemplate{ID: "tpl-desktop", Name: "weknora-desktop", Status: "building", Desktop: true}, nil
}

func (s *stubProviderClient) ReplaceDesktopTemplate(ctx context.Context) (*sandbox.RemoteTemplate, error) {
	s.replaceDesktopCalls.Add(1)
	if s.ensureDelay > 0 {
		time.Sleep(s.ensureDelay)
	}
	if s.ensureDesktopErr != nil {
		return nil, s.ensureDesktopErr
	}
	if s.ensuredDesktop != nil {
		copyTpl := *s.ensuredDesktop
		return &copyTpl, nil
	}
	return s.EnsureDesktopTemplate(ctx)
}

func (s *stubProviderClient) DeleteSupersededDesktopTemplates(_ context.Context, keepID string) error {
	for _, item := range s.templates {
		if item.Desktop && item.ID != "" && item.ID != keepID {
			s.deletedTemplates = append(s.deletedTemplates, item.ID)
		}
	}
	return nil
}

func (s *stubProviderClient) DeleteSupersededStandardTemplates(_ context.Context, keepID string) error {
	for _, item := range s.templates {
		if item.Standard && item.ID != "" && item.ID != keepID {
			s.deletedTemplates = append(s.deletedTemplates, item.ID)
		}
	}
	return nil
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
	svc := NewTenantSandboxConfigService(repo, agents, testGlobalSandboxConfig(), nil, nil)
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return client, nil
	}
	return svc
}

func TestQueryTemplatesEnsuresMissingWeKnoraTemplate(t *testing.T) {
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{{ID: "tpl-custom", Name: "custom", Status: "ready"}},
		ensured:   &sandbox.RemoteTemplate{ID: "tpl-weknora", Name: "weknora", Status: "building", Standard: true},
	}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config:         e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
		EnsureStandard: true,
	})

	require.NoError(t, err)
	require.True(t, result.Provisioned)
	require.Equal(t, "tpl-weknora", result.StandardTemplateID)
	require.Len(t, result.Templates, 2)
	require.True(t, result.Templates[0].Standard, "standard template should sort first")
}

func TestQueryTemplatesEnsuresMissingDesktopTemplate(t *testing.T) {
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-weknora", Name: "weknora", Status: "ready", Standard: true},
		},
		ensuredDesktop: &sandbox.RemoteTemplate{
			ID: "tpl-desktop", Name: "weknora-desktop", Status: "building", Desktop: true,
		},
	}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config:        e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
		EnsureDesktop: true,
	})

	require.NoError(t, err)
	require.True(t, result.Provisioned)
	require.Equal(t, "tpl-weknora", result.StandardTemplateID)
	require.Equal(t, "tpl-desktop", result.DesktopTemplateID)
	require.Equal(t, int32(0), client.ensureCalls.Load(), "creating desktop must not rebuild the CLI template")
	require.Equal(t, int32(1), client.ensureDesktopCalls.Load())
	var sawDesktop bool
	for _, item := range result.Templates {
		if item.Desktop && item.ID == "tpl-desktop" {
			sawDesktop = true
		}
	}
	require.True(t, sawDesktop)
}

func TestQueryTemplatesEnsureDesktopFailureKeepsCatalog(t *testing.T) {
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-weknora", Name: "weknora", Status: "ready", Standard: true},
		},
		ensureDesktopErr: stderrors.New("hub missing main-desktop"),
	}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config:        e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
		EnsureDesktop: true,
	})

	require.NoError(t, err)
	require.Equal(t, "tpl-weknora", result.StandardTemplateID)
	require.Empty(t, result.DesktopTemplateID)
	require.Equal(t, int32(1), client.ensureDesktopCalls.Load())
	require.Equal(t, 1, len(result.Templates))
}

func TestQueryTemplatesReplaceDesktopFailureIsFatal(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "tpl-desktop", 300)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
	}}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-desktop", Name: "weknora-desktop", Status: "ready", Desktop: true},
		},
		ensureDesktopErr: stderrors.New("rebuild refused"),
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:       "cfg-a",
		ReplaceDesktop: true,
	})
	require.Error(t, err)
	require.Equal(t, int32(1), client.replaceDesktopCalls.Load())
}

// Provisioning only becomes idempotent once the build shows up in the
// provider's catalog, so overlapping requests have to share one attempt or the
// cluster ends up with a template per click.
func TestQueryTemplatesCollapsesConcurrentProvisioning(t *testing.T) {
	client := &stubProviderClient{ensureDelay: 50 * time.Millisecond}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
				Config:         e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
				EnsureStandard: true,
			})
			require.NoError(t, err)
		}()
	}
	group.Wait()

	require.Equal(t, int32(1), client.ensureCalls.Load())
}

// Two tenants pointing at different clusters must not be serialised behind one
// another, and neither may receive the other's template.
func TestQueryTemplatesProvisionsPerClusterIndependently(t *testing.T) {
	client := &stubProviderClient{}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	for _, key := range []string{"key-a", "key-b"} {
		_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
			Config:         e2bCfg(key, "https://api.e2b.app", "e2b.app", "", 300),
			EnsureStandard: true,
		})
		require.NoError(t, err)
	}

	require.Equal(t, int32(2), client.ensureCalls.Load())
}

// A cluster whose only WeKnora template failed to build must be reprovisioned,
// not reported as already equipped.
func TestQueryTemplatesReprovisionsOverFailedStandardTemplate(t *testing.T) {
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-broken", Name: "weknora", Status: "failed", Standard: true, Error: "no space left"},
		},
		ensured: &sandbox.RemoteTemplate{
			ID: "tpl-broken", Name: "weknora", Status: "building", Standard: true,
		},
	}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config:         e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
		EnsureStandard: true,
	})

	require.NoError(t, err)
	require.Equal(t, int32(1), client.ensureCalls.Load())
	require.True(t, result.Provisioned)
	require.Equal(t, "tpl-broken", result.StandardTemplateID)
	require.Len(t, result.Templates, 1, "a rebuild must not add a catalog entry")
	require.Equal(t, "building", result.Templates[0].Status)
}

// Without EnsureStandard the catalog is read-only, so a failed template is
// reported as it is rather than silently hidden.
func TestQueryTemplatesReportsFailedStandardTemplateWithoutEnsure(t *testing.T) {
	client := &stubProviderClient{templates: []sandbox.RemoteTemplate{
		{ID: "tpl-broken", Name: "weknora", Status: "failed", Standard: true, Error: "no space left"},
	}}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
	})

	require.NoError(t, err)
	require.Equal(t, int32(0), client.ensureCalls.Load())
	require.False(t, result.Provisioned)
	require.Empty(t, result.StandardTemplateID, "a failed template must not be preselected")
	require.Equal(t, "no space left", result.Templates[0].Error)
}

// EnsureStandard that cannot recover must still return the catalog: a 500
// would hide the provider's lastError and stop the wizard from polling.
func TestQueryTemplatesSurfacesFailedEnsureWithoutProvisioning(t *testing.T) {
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-broken", Name: "weknora", Status: "failed", Standard: true, Error: "TOOMANYREQUESTS"},
		},
		ensured: &sandbox.RemoteTemplate{
			ID: "tpl-broken", Name: "weknora", Status: "failed", Standard: true, Error: "TOOMANYREQUESTS",
		},
	}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config:         e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
		EnsureStandard: true,
	})

	require.NoError(t, err)
	require.Equal(t, int32(1), client.ensureCalls.Load())
	require.False(t, result.Provisioned)
	require.Empty(t, result.StandardTemplateID)
	require.Equal(t, "TOOMANYREQUESTS", result.Templates[0].Error)
}

func TestQueryTemplatesReplaceStandardRequiresConfigID(t *testing.T) {
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-old", Name: "weknora", Status: "ready", Standard: true},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "building", Standard: true,
		},
	}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config:          e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
		ReplaceStandard: true,
	})
	require.Error(t, err)
	require.Equal(t, int32(0), client.replaceCalls.Load())
}

func TestQueryTemplatesReplaceStandardPersistsNewTemplateID(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "tpl-old", 300)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
	}}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-old", Name: "weknora", Status: "ready", Standard: true},
			{ID: "tpl-custom", Name: "custom", Status: "ready"},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "ready", Standard: true,
		},
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:        "cfg-a",
		Config:          stored,
		ReplaceStandard: true,
	})

	require.NoError(t, err)
	require.Equal(t, int32(1), client.replaceCalls.Load())
	require.Equal(t, int32(0), client.ensureCalls.Load())
	require.Equal(t, []string{"tpl-old"}, client.deletedTemplates)
	require.True(t, result.Provisioned)
	require.Equal(t, "tpl-new", result.StandardTemplateID)
	require.Equal(t, "tpl-new", repo.entity.Config.E2B.TemplateID,
		"rebuild must write the new template id so sessions do not keep targeting the deleted one")
	require.Len(t, result.Templates, 2)
	require.Equal(t, "tpl-new", result.Templates[0].ID)
	require.Equal(t, "tpl-custom", result.Templates[1].ID)
}

func TestQueryTemplatesReplaceStandardKeepsOldTemplateWhileReplacementBuilds(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "tpl-old", 300)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
	}}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-old", Name: "weknora", Status: "ready", Standard: true},
			{ID: "tpl-custom", Name: "custom", Status: "ready"},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "building", Standard: true,
		},
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:        "cfg-a",
		Config:          stored,
		ReplaceStandard: true,
	})

	require.NoError(t, err)
	require.Empty(t, client.deletedTemplates,
		"a building replacement must not retire the spawnable template")
	require.Equal(t, "tpl-old", repo.entity.Config.E2B.TemplateID,
		"sessions must keep the previous READY id until the replacement can spawn")
	require.Equal(t, "tpl-old", result.StandardTemplateID)
	require.True(t, result.Provisioned)
	ids := make([]string, 0, len(result.Templates))
	for _, item := range result.Templates {
		ids = append(ids, item.ID)
	}
	require.Contains(t, ids, "tpl-old")
	require.Contains(t, ids, "tpl-new")
}

func TestQueryTemplatesReplaceStandardKeepsOldTemplateWhenPersistFails(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "tpl-old", 300)
	repo := &fakeConfigRepo{
		entity: &types.TenantSandboxConfigEntity{
			ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
		},
		updateErr: stderrors.New("db write failed"),
	}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-old", Name: "weknora", Status: "ready", Standard: true},
			{ID: "tpl-custom", Name: "custom", Status: "ready"},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "ready", Standard: true,
		},
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:        "cfg-a",
		Config:          stored,
		ReplaceStandard: true,
	})

	require.NoError(t, err)
	require.Empty(t, client.deletedTemplates,
		"a persist failure must not retire the previous spawnable template")
	require.Equal(t, "tpl-old", repo.entity.Config.E2B.TemplateID)
	require.Equal(t, "tpl-old", result.StandardTemplateID)
	ids := make([]string, 0, len(result.Templates))
	for _, item := range result.Templates {
		ids = append(ids, item.ID)
	}
	require.Contains(t, ids, "tpl-old")
	require.Contains(t, ids, "tpl-new")
}

func TestQueryTemplatesReplaceStandardRefusesWhenSkillIsInstalling(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
	}}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "t1", Name: "weknora", Status: "ready", Standard: true},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "ready", Standard: true,
		},
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})
	svc.skills = &deleteSkillStore{skills: []*types.TenantSkillEntity{{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-a",
		Status: types.SkillStatusInstalling,
	}}}

	_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:        "cfg-a",
		Config:          stored,
		ReplaceStandard: true,
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Equal(t, int32(0), client.replaceCalls.Load())
}

func TestQueryTemplatesReplaceStandardRefusesWhenSiblingHasSkillSnapshot(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	sibling := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	sibling.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{
		entity: &types.TenantSandboxConfigEntity{
			ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
		},
		others: []*types.TenantSandboxConfigEntity{{
			ID: "cfg-b", TenantID: 7, SandboxType: "e2b", Config: sibling,
		}},
	}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "t1", Name: "weknora", Status: "ready", Standard: true},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "building", Standard: true,
		},
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:        "cfg-a",
		Config:          stored,
		ReplaceStandard: true,
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Equal(t, int32(0), client.replaceCalls.Load())
}

func TestQueryTemplatesReplaceStandardRefusesWhenSkillSnapshotExists(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	stored.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
	}}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "t1", Name: "weknora", Status: "ready", Standard: true},
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-new", Name: "weknora", Status: "building", Standard: true,
		},
	}
	svc := NewTenantSandboxConfigService(repo, stubAgentRepo{}, sandbox.DefaultConfig(), nil, nil)
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return client, nil
	}

	_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID:        "cfg-a",
		Config:          e2bCfg(types.RedactedSecretPlaceholder, "https://api.e2b.app", "e2b.app", "t1", 300),
		ReplaceStandard: true,
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Equal(t, int32(0), client.replaceCalls.Load())
}

func TestUpdateRefusesTemplateChangeWhenSkillSnapshotExists(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	stored.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t2", 300),
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Nil(t, repo.updated)
}

func TestUpdateRefusesDesktopEnabledChangeWhenSkillSnapshotExists(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	stored.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})

	incoming := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	incoming.DesktopEnabled = true
	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: incoming,
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Nil(t, repo.updated)
}

func TestUpdateRefusesIdentityChangeWhenSkillSnapshotExists(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	stored.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Nil(t, repo.updated)
}

func TestUpdateRefusesIdentityChangeWhileSkillIsInstalling(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})
	svc.skills = &deleteSkillStore{skills: []*types.TenantSkillEntity{{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-a",
		Status: types.SkillStatusInstalling,
	}}}

	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-b", "https://api.e2b.app", "e2b.app", "t1", 300),
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Nil(t, repo.updated)
}

func TestQueryTemplatesEnsureAndReplaceDoNotShareSingleflight(t *testing.T) {
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b", Config: stored,
	}}
	client := &stubProviderClient{
		templates: []sandbox.RemoteTemplate{
			{ID: "tpl-broken", Name: "weknora", Status: "failed", Standard: true},
		},
		ensureDelay: 80 * time.Millisecond,
		ensured: &sandbox.RemoteTemplate{
			ID: "tpl-ensured", Name: "weknora", Status: "building", Standard: true,
		},
		replaced: &sandbox.RemoteTemplate{
			ID: "tpl-replaced", Name: "weknora", Status: "building", Standard: true,
		},
	}
	svc := newTestConfigService(t, repo, client, stubAgentRepo{})

	errCh := make(chan error, 2)
	go func() {
		_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
			ConfigID:       "cfg-a",
			Config:         stored,
			EnsureStandard: true,
		})
		errCh <- err
	}()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
			ConfigID:        "cfg-a",
			Config:          stored,
			ReplaceStandard: true,
		})
		errCh <- err
	}()
	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
	require.Equal(t, int32(1), client.ensureCalls.Load())
	require.Equal(t, int32(1), client.replaceCalls.Load())
}

func TestUpdateRefusesDNSChangeWhenSkillSnapshotExists(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := cubeCfg("key-a", "https://203.0.113.10", "https://203.0.113.11", "cube.app")
	stored.Cube.TemplateID = "tpl-1"
	stored.Cube.DNSServers = []string{"8.8.8.8"}
	stored.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "cube",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})

	next := cubeCfg("key-a", "https://203.0.113.10", "https://203.0.113.11", "cube.app")
	next.Cube.TemplateID = "tpl-1"
	next.Cube.DNSServers = []string{"1.1.1.1"}
	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: next,
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Nil(t, repo.updated)
}

func TestUpdateRefusesPrivateEndpointChangeWhenSkillSnapshotExists(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	stored.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-1"}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})

	next := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	next.AllowPrivateEndpoints = true
	_, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: next,
	})
	require.ErrorIs(t, err, ErrSkillSnapshotBlocksTemplateChange)
	require.Nil(t, repo.updated)
}

func TestQueryTemplatesDeduplicatesSameProviderTemplateID(t *testing.T) {
	client := &stubProviderClient{templates: []sandbox.RemoteTemplate{
		{ID: "tpl-weknora", Name: "weknora", Status: "building", Standard: true},
		{ID: "tpl-weknora", Name: "project-b89e/weknora", Status: "ready", Standard: true},
	}}
	svc := newTestConfigService(t, &fakeConfigRepo{}, client, stubAgentRepo{})

	result, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "", 300),
	})

	require.NoError(t, err)
	require.Len(t, result.Templates, 1)
	require.Equal(t, "project-b89e/weknora", result.Templates[0].Name)
	require.Equal(t, "ready", result.Templates[0].Status)
	require.Equal(t, "tpl-weknora", result.StandardTemplateID)
}

func TestQueryTemplatesResolvesMaskedStoredCredential(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, SandboxType: "e2b",
		Config: e2bCfg("stored-secret", "https://api.e2b.app", "e2b.app", "old", 300),
	}}
	svc := NewTenantSandboxConfigService(repo, stubAgentRepo{}, sandbox.DefaultConfig(), nil, nil)
	client := &stubProviderClient{templates: []sandbox.RemoteTemplate{{ID: "tpl-a", Name: "a", Status: "ready"}}}
	svc.newClient = func(cfg *sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		require.Equal(t, "stored-secret", cfg.E2BAPIKey)
		return client, nil
	}

	_, err := svc.QueryTemplates(context.Background(), 7, SandboxTemplateQueryInput{
		ConfigID: "cfg-a",
		Config:   e2bCfg(types.RedactedSecretPlaceholder, "https://api.e2b.app", "e2b.app", "", 300),
	})
	require.NoError(t, err)
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

// Saving the connection / runtime form must not discard the snapshot pointer.
// The editor payload has no skill_image field; that pointer is owned by the
// install path. Wiping it is how a config can list two ready skills while
// every session still boots the base template.
func TestUpdateKeepsSkillImageWhenEditorOmitsIt(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	stored := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	stored.SkillImage = &types.SkillImageConfig{
		SnapshotID:       "snap-2",
		Generation:       2,
		BaseTemplateID:   "base-template",
		OwnerFingerprint: "fp-1",
	}
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID:          "cfg-a",
		TenantID:    7,
		Name:        "prod",
		SandboxType: "e2b",
		Config:      stored,
	}}
	svc := newTestConfigService(t, repo, &stubProviderClient{}, stubAgentRepo{})

	updated, err := svc.Update(context.Background(), 7, "cfg-a", UpdateSandboxConfigInput{
		Name:   "prod",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 900),
	})

	require.NoError(t, err)
	require.NotNil(t, updated.Config.SkillImage)
	require.Equal(t, "snap-2", updated.Config.SkillImage.SnapshotID)
	require.Equal(t, 2, updated.Config.SkillImage.Generation)
	require.Equal(t, "base-template", updated.Config.SkillImage.BaseTemplateID)
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
	require.Equal(t, []string{"get", "cordon", "uncordon"}, repo.events)
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

// An inventory failure does not mean the provider has no sandboxes. force
// does not delete the config or its user-machine registry in that case:
// cleanup treats a missing registry row as a machine that is already gone.
func TestDeleteForceKeepsTheConfigWhenInventoryIsUnverifiable(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	alloc := &recordingAlloc{}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	svc.WithAllocationRepo(alloc)
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return nil, stderrors.New("dial tcp: no such host")
	}

	err := svc.Delete(context.Background(), 7, "cfg-a", true)
	require.ErrorIs(t, err, ErrSandboxInventoryUnverifiable)
	require.False(t, repo.deleted)
	require.Zero(t, alloc.calls)
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
	require.Equal(t, []string{"get", "cordon", "delete", "uncordon"}, repo.events)
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

func TestSanitizeSandboxConfigRejectsDomainAllowWithoutDenyAll(t *testing.T) {
	_, err := SanitizeSandboxConfig(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL: "https://cube.example.com", ProxyURL: "https://cube.example.com",
			SandboxDomain: "cube.app", TemplateID: "tpl-1",
		},
		Network: &types.SandboxNetworkPolicy{AllowOut: []string{"api.example.com"}},
	}, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "0.0.0.0/0")
}

func TestSanitizeSandboxConfigPreservesSkillImage(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	existing := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey: "stored-key", APIURL: "https://203.0.113.10", TemplateID: "t1",
		},
		SkillImage: &types.SkillImageConfig{SnapshotID: "snap-1", OwnerFingerprint: "fp-1"},
	}
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey:     types.RedactedSecretPlaceholder,
			APIURL:     "https://203.0.113.10",
			TemplateID: "t1",
		},
		SkillImage: &types.SkillImageConfig{SnapshotID: "forged-snap"},
	}

	out, err := SanitizeSandboxConfig(incoming, existing)

	require.NoError(t, err)
	require.Equal(t, "snap-1", out.SkillImage.SnapshotID,
		"the sandbox config API must not let a client plant or wipe the skill image")
}

func TestSanitizeSandboxConfigRejectsUnknownSkillRollout(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{
		SandboxType:  "e2b",
		SkillRollout: "whenever",
		E2B: &types.E2BSandboxConfig{
			APIKey: "stored-key", APIURL: "https://203.0.113.10", TemplateID: "t1",
		},
	}

	_, err := SanitizeSandboxConfig(incoming, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "skill_rollout")
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

func TestSanitizeSandboxConfigRejectsInvalidCubeDNS(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL: "https://203.0.113.20", DNSServers: []string{"dns.google"},
		},
	}

	_, err := SanitizeSandboxConfig(incoming, nil)

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Contains(t, err.Error(), "dns.google")
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
	sandbox.ClearDockerBackendEnabledOverride()
	t.Cleanup(sandbox.ClearDockerBackendEnabledOverride)
	t.Setenv(sandbox.DockerBackendEnabledEnv, "true")
	_, err = SanitizeSandboxConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker:      &types.DockerSandboxConfig{Image: "weknora:test"},
	}, nil)
	require.NoError(t, err)
}

func TestSandboxesStillLiveErrorSupportsErrorsIs(t *testing.T) {
	err := &SandboxesStillLiveError{Inventory: SandboxInventory{SandboxCount: 1}}

	require.True(t, stderrors.Is(err, ErrSandboxesStillLive))
}

func TestCreateAcceptsDockerNamedSandboxBackend(t *testing.T) {
	sandbox.ClearDockerBackendEnabledOverride()
	t.Cleanup(sandbox.ClearDockerBackendEnabledOverride)
	t.Setenv(sandbox.DockerBackendEnabledEnv, "true")
	repo := &fakeConfigRepo{}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})

	docker, err := svc.Create(context.Background(), 7, CreateSandboxConfigInput{
		Name: "docker-dev",
		Config: &types.TenantSandboxConfig{
			SandboxType: "docker",
			Docker:      &types.DockerSandboxConfig{Image: "weknora:test"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "docker", docker.SandboxType)
}

func TestCreateRejectsRemovedLocalBackend(t *testing.T) {
	svc := newTestConfigService(t, &fakeConfigRepo{}, nil, stubAgentRepo{})

	_, err := svc.Create(context.Background(), 7, CreateSandboxConfigInput{
		Name:   "local-dev",
		Config: &types.TenantSandboxConfig{SandboxType: "local"},
	})
	require.ErrorIs(t, err, ErrNamedSandboxBackendUnsupported)
}

func TestCreateRejectsDockerWithoutImage(t *testing.T) {
	sandbox.ClearDockerBackendEnabledOverride()
	t.Cleanup(sandbox.ClearDockerBackendEnabledOverride)
	t.Setenv(sandbox.DockerBackendEnabledEnv, "true")
	svc := newTestConfigService(t, &fakeConfigRepo{}, nil, stubAgentRepo{})

	_, err := svc.Create(context.Background(), 7, CreateSandboxConfigInput{
		Name:   "docker-dev",
		Config: &types.TenantSandboxConfig{SandboxType: "docker"},
	})

	require.ErrorIs(t, err, sandbox.ErrSandboxConfigIncomplete)
}

func TestCreateRejectsDockerWhenDisabled(t *testing.T) {
	sandbox.ClearDockerBackendEnabledOverride()
	t.Cleanup(sandbox.ClearDockerBackendEnabledOverride)
	t.Setenv(sandbox.DockerBackendEnabledEnv, "")
	svc := newTestConfigService(t, &fakeConfigRepo{}, nil, stubAgentRepo{})

	_, err := svc.Create(context.Background(), 7, CreateSandboxConfigInput{
		Name: "docker-dev",
		Config: &types.TenantSandboxConfig{
			SandboxType: "docker",
			Docker:      &types.DockerSandboxConfig{Image: "weknora:test"},
		},
	})
	require.ErrorIs(t, err, sandbox.ErrDockerBackendDisabled)
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

func TestDeleteReleasesSkillSnapshotsBeforeSoftDelete(t *testing.T) {
	fx := newSnapshotReleaseFixture(t, nil)

	err := fx.svc.Delete(context.Background(), 7, "cfg-a", false)

	require.NoError(t, err)
	require.Equal(t, []string{"snap-1", "snap-2", "soft-delete"}, fx.events)
	require.Contains(t, fx.skills.marks, "row-1:"+types.SkillSnapshotStateDeleted)
	require.Contains(t, fx.skills.marks, "row-2:"+types.SkillSnapshotStateDeleted)
	require.NotContains(t, fx.skills.marks, "row-0:"+types.SkillSnapshotStateDeleted)
	require.Empty(t, fx.skills.skills, "tenant_skills rows must be dropped before SoftDelete")
	require.True(t, fx.skills.ledgerCleared)
	require.Empty(t, fx.files.deleted,
		"deleting a sandbox must not drop the catalog archive")
	// DeleteSkill only takes values filed under a skill; the config-wide ones
	// would outlive the config without this.
	require.Equal(t, []string{"7:cfg-a"}, fx.skills.envVarsClearedFor)
}

// An install row names an archive only when a re-register replaced the
// definition's copy and this sandbox kept running the image built from the old
// one. Deleting the config drops the only reader those bytes will ever have, so
// they have to go with it — nothing else records that the version existed.
func TestDeleteReclaimsArchivePinnedOnlyByThisConfig(t *testing.T) {
	fx := newSnapshotReleaseFixture(t, nil)
	fx.skills.skills[0].BundleRef = "bundle://skill-a-v1.zip"

	require.NoError(t, fx.svc.Delete(context.Background(), 7, "cfg-a", false))

	require.Equal(t, []string{"bundle://skill-a-v1.zip"}, fx.files.deleted)
}

func TestDeleteKeepsArchiveAnotherConfigStillPins(t *testing.T) {
	fx := newSnapshotReleaseFixture(t, nil)
	fx.skills.skills[0].BundleRef = "bundle://skill-a-v1.zip"
	// A sibling sandbox was built from the same replaced archive and is not
	// being deleted, so the pin is not this config's to release.
	fx.skills.skills = append(fx.skills.skills, &types.TenantSkillEntity{
		ID: "sk-2", TenantID: 7, SandboxConfigID: "cfg-b",
		BundleRef: "bundle://skill-a-v1.zip",
	})

	require.NoError(t, fx.svc.Delete(context.Background(), 7, "cfg-a", false))

	require.Empty(t, fx.files.deleted)
}

// A list that cannot be read is not evidence that nothing needs the bytes. A
// leaked archive costs storage; a deleted one costs some other sandbox its
// files, so an unreadable table keeps it.
func TestDeleteKeepsPinnedArchiveWhenReadersCannotBeListed(t *testing.T) {
	fx := newSnapshotReleaseFixture(t, nil)
	fx.skills.skills[0].BundleRef = "bundle://skill-a-v1.zip"
	fx.skills.listTenantErr = stderrors.New("database unavailable")

	require.NoError(t, fx.svc.Delete(context.Background(), 7, "cfg-a", false))

	require.Empty(t, fx.files.deleted)
}

func TestDeleteRefusesWhenSnapshotReleaseFailsWithoutForce(t *testing.T) {
	fx := newSnapshotReleaseFixture(t, map[string]error{
		"snap-2": stderrors.New("provider unavailable"),
	})

	err := fx.svc.Delete(context.Background(), 7, "cfg-a", false)

	require.ErrorIs(t, err, ErrSkillSnapshotReleaseFailed)
	var releaseErr *SkillSnapshotReleaseFailedError
	require.ErrorAs(t, err, &releaseErr)
	require.Equal(t, []string{"snap-2"}, releaseErr.Remaining)
	require.NotContains(t, fx.events, "soft-delete",
		"leaving the row behind is recoverable; leaking a billable snapshot is not")
	require.Equal(t, types.SkillSnapshotStateDeleted, fx.skills.snapshot("row-1").State)
	require.Equal(t, types.SkillSnapshotStateSuperseded, fx.skills.snapshot("row-2").State)
	require.Len(t, fx.skills.skills, 1, "skill rows stay so a retry can finish the release")
	require.False(t, fx.skills.ledgerCleared)
}

func TestDeleteWithForceProceedsAndRecordsThePartialSuccess(t *testing.T) {
	fx := newSnapshotReleaseFixture(t, map[string]error{
		"snap-2": stderrors.New("provider unavailable"),
	})

	err := fx.svc.Delete(context.Background(), 7, "cfg-a", true)

	require.NoError(t, err)
	require.Equal(t, []string{"snap-1", "soft-delete"}, fx.events)
	require.Contains(t, fx.skills.marks, "row-1:"+types.SkillSnapshotStateDeleted)
	require.NotContains(t, fx.skills.marks, "row-2:"+types.SkillSnapshotStateDeleted)
	require.True(t, fx.repo.deleted)
	require.True(t, fx.skills.ledgerCleared)
	require.Empty(t, fx.skills.skills)
}

func TestDeleteMarksBuildingSnapshotsWhenProviderHasNoSnapshotClient(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "docker", SandboxType: "docker",
		Config: &types.TenantSandboxConfig{
			SandboxType: "docker",
			Docker:      &types.DockerSandboxConfig{Image: "weknora:test"},
		},
	}}
	skills := &deleteSkillStore{
		snapshots: []*types.TenantSkillSnapshotEntity{{
			ID: "row-build", TenantID: 7, SandboxConfigID: "cfg-a",
			State: types.SkillSnapshotStateBuilding,
		}},
	}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	svc.skills = skills

	err := svc.Delete(context.Background(), 7, "cfg-a", false)

	require.NoError(t, err)
	require.True(t, repo.deleted)
	require.Contains(t, skills.marks, "row-build:"+types.SkillSnapshotStateDeleted)
}

// Deleting the config is the last chance to reclaim a build that died between
// its commit and the ledger write: PruneSupersededSnapshots walks live configs,
// so once this row is gone nothing can reach that snapshot again.
func TestDeleteReleasesAnAbandonedBuildByPlannedName(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	skills := &deleteSkillStore{
		snapshots: []*types.TenantSkillSnapshotEntity{{
			ID: "row-build", TenantID: 7, SandboxConfigID: "cfg-a",
			PlannedName: "weknora-sk-cfga-g3", State: types.SkillSnapshotStateBuilding,
		}},
	}
	var events []string
	client := &snapshotReleaseClient{
		events: &events,
		listed: []sandbox.RemoteSnapshotRef{
			{ID: "snap-orphan", Names: []string{"weknora-sk-cfga-g3"}},
		},
	}
	svc := newTestConfigService(t, repo, &client.stubProviderClient, stubAgentRepo{})
	svc.skills = skills
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return client, nil
	}

	require.NoError(t, svc.Delete(context.Background(), 7, "cfg-a", false))

	require.Equal(t, []string{"list-snapshots", "snap-orphan"}, events,
		"the name has to be resolved to a provider ID before it can be released")
	require.Contains(t, skills.marks, "row-build:"+types.SkillSnapshotStateDeleted)
	require.True(t, repo.deleted)
}

// A planned name that resolves to nothing is the ordinary case: the commit
// never happened, so there is no provider resource to release.
func TestDeleteAcceptsAnAbandonedBuildThatNeverCommitted(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	skills := &deleteSkillStore{
		snapshots: []*types.TenantSkillSnapshotEntity{{
			ID: "row-build", TenantID: 7, SandboxConfigID: "cfg-a",
			PlannedName: "weknora-sk-cfga-g3", State: types.SkillSnapshotStateBuilding,
		}},
	}
	var events []string
	client := &snapshotReleaseClient{events: &events}
	svc := newTestConfigService(t, repo, &client.stubProviderClient, stubAgentRepo{})
	svc.skills = skills
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return client, nil
	}

	require.NoError(t, svc.Delete(context.Background(), 7, "cfg-a", false))

	require.Equal(t, []string{"list-snapshots"}, events,
		"nothing carries that name, so there is nothing to delete")
	require.Contains(t, skills.marks, "row-build:"+types.SkillSnapshotStateDeleted)
	require.True(t, repo.deleted)
}

// snapshotReleaseFixture drives Delete through a provider that also implements
// snapshot deletion, which is the production Cube/E2B split.
type snapshotReleaseFixture struct {
	events []string
	repo   *eventRecordingConfigRepo
	skills *deleteSkillStore
	files  *deleteBundleResolver
	svc    *TenantSandboxConfigService
}

func newSnapshotReleaseFixture(t *testing.T, failDelete map[string]error) *snapshotReleaseFixture {
	t.Helper()
	fx := &snapshotReleaseFixture{
		skills: &deleteSkillStore{
			snapshots: []*types.TenantSkillSnapshotEntity{
				{
					ID: "row-0", TenantID: 7, SandboxConfigID: "cfg-a",
					SnapshotID: "snap-gone", State: types.SkillSnapshotStateDeleted,
				},
				{
					ID: "row-1", TenantID: 7, SandboxConfigID: "cfg-a",
					SnapshotID: "snap-1", State: types.SkillSnapshotStateActive,
				},
				{
					ID: "row-2", TenantID: 7, SandboxConfigID: "cfg-a",
					SnapshotID: "snap-2", State: types.SkillSnapshotStateSuperseded,
				},
			},
			skills: []*types.TenantSkillEntity{{
				ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-a",
				BundleRef: "bundle://skill-a.zip",
			}},
			// The install names the archive the definition itself holds, so
			// dropping the config must leave those bytes where they are.
			catalogs: []*types.TenantSkillCatalogEntity{{
				ID: "cat-1", TenantID: 7, Name: "skill-a",
				BundleRef: "bundle://skill-a.zip",
			}},
		},
		files: &deleteBundleResolver{},
	}
	inner := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}}
	fx.repo = &eventRecordingConfigRepo{fakeConfigRepo: inner, events: &fx.events}
	client := &snapshotReleaseClient{events: &fx.events, failDelete: failDelete}
	fx.svc = newTestConfigService(t, inner, &client.stubProviderClient, stubAgentRepo{})
	fx.svc.repo = fx.repo
	fx.svc.skills = fx.skills
	fx.svc.files = fx.files
	fx.svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		return client, nil
	}
	return fx
}

type eventRecordingConfigRepo struct {
	*fakeConfigRepo
	events *[]string
}

func (r *eventRecordingConfigRepo) SoftDelete(ctx context.Context, tenantID uint64, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*r.events = append(*r.events, "soft-delete")
	return r.fakeConfigRepo.SoftDelete(ctx, tenantID, id)
}

type snapshotReleaseClient struct {
	stubProviderClient
	events     *[]string
	failDelete map[string]error
	listed     []sandbox.RemoteSnapshotRef
}

func (c *snapshotReleaseClient) ListSnapshots(
	ctx context.Context, _ string,
) ([]sandbox.RemoteSnapshotRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	*c.events = append(*c.events, "list-snapshots")
	return c.listed, nil
}

func (c *snapshotReleaseClient) List(
	ctx context.Context, filter sandbox.RemoteListFilter,
) ([]sandbox.RemoteSandboxSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.stubProviderClient.List(ctx, filter)
}

func (c *snapshotReleaseClient) Delete(ctx context.Context, sandboxID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.stubProviderClient.Delete(ctx, sandboxID)
}

func (c *snapshotReleaseClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.failDelete[snapshotID]; err != nil {
		return err
	}
	*c.events = append(*c.events, snapshotID)
	return nil
}

type deleteSkillStore struct {
	snapshots         []*types.TenantSkillSnapshotEntity
	skills            []*types.TenantSkillEntity
	catalogs          []*types.TenantSkillCatalogEntity
	marks             []string
	ledgerCleared     bool
	envVarsClearedFor []string

	// listTenantErr makes the tenant-wide lookups fail, which is what decides
	// whether a pinned archive is kept rather than deleted on a bad read.
	listTenantErr error
}

func (s *deleteSkillStore) snapshot(id string) *types.TenantSkillSnapshotEntity {
	for _, row := range s.snapshots {
		if row.ID == id {
			return row
		}
	}
	return &types.TenantSkillSnapshotEntity{}
}

func (s *deleteSkillStore) ListSnapshotsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillSnapshotEntity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []*types.TenantSkillSnapshotEntity
	for _, row := range s.snapshots {
		if row.TenantID == tenantID && row.SandboxConfigID == configID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *deleteSkillStore) MarkSnapshotState(
	ctx context.Context, tenantID uint64, id, state, snapshotID string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.marks = append(s.marks, id+":"+state)
	for _, row := range s.snapshots {
		// The real query is tenant-scoped, so a caller passing the wrong
		// workspace must match nothing here too.
		if row.ID != id || row.TenantID != tenantID {
			continue
		}
		row.State = state
		if snapshotID != "" {
			row.SnapshotID = snapshotID
		}
	}
	return nil
}

func (s *deleteSkillStore) ListSkillsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillEntity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []*types.TenantSkillEntity
	for _, row := range s.skills {
		if row.TenantID == tenantID && row.SandboxConfigID == configID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *deleteSkillStore) ListSkillsByTenant(
	ctx context.Context, tenantID uint64,
) ([]*types.TenantSkillEntity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.listTenantErr != nil {
		return nil, s.listTenantErr
	}
	var out []*types.TenantSkillEntity
	for _, row := range s.skills {
		if row.TenantID == tenantID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *deleteSkillStore) ListCatalogsByTenant(
	ctx context.Context, tenantID uint64,
) ([]*types.TenantSkillCatalogEntity, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.listTenantErr != nil {
		return nil, s.listTenantErr
	}
	var out []*types.TenantSkillCatalogEntity
	for _, row := range s.catalogs {
		if row.TenantID == tenantID {
			cp := *row
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *deleteSkillStore) DeleteSkill(ctx context.Context, tenantID uint64, configID, skillID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	kept := s.skills[:0]
	for _, row := range s.skills {
		if row.TenantID == tenantID && row.SandboxConfigID == configID && row.ID == skillID {
			continue
		}
		kept = append(kept, row)
	}
	s.skills = kept
	return nil
}

func (s *deleteSkillStore) DeleteSnapshotRowsByConfig(ctx context.Context, tenantID uint64, configID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.ledgerCleared = true
	kept := s.snapshots[:0]
	for _, row := range s.snapshots {
		if row.TenantID == tenantID && row.SandboxConfigID == configID {
			continue
		}
		kept = append(kept, row)
	}
	s.snapshots = kept
	return nil
}

func (s *deleteSkillStore) DeleteUserEnvVarsByConfig(
	ctx context.Context, tenantID uint64, configID string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.envVarsClearedFor = append(s.envVarsClearedFor, fmt.Sprintf("%d:%s", tenantID, configID))
	return nil
}

type deleteBundleResolver struct {
	deleted []string
}

func (r *deleteBundleResolver) ResolveFileService(
	ctx context.Context, _ *types.Tenant, _, _, _ string,
) (interfaces.FileService, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return deleteFileService{r: r}, "", nil
}

type deleteFileService struct{ r *deleteBundleResolver }

func (deleteFileService) CheckConnectivity(context.Context) error { return nil }
func (deleteFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (deleteFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}
func (deleteFileService) GetFile(context.Context, string) (io.ReadCloser, error) { return nil, nil }
func (deleteFileService) GetFileURL(context.Context, string) (string, error)     { return "", nil }
func (s deleteFileService) DeleteFile(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.r.deleted = append(s.r.deleted, ref)
	return nil
}

func (deleteFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

func allocationTestConfig(mode string, days int) *types.TenantSandboxConfig {
	cfg := e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300)
	cfg.AllocationMode = mode
	cfg.UserSandboxEmptyRetentionDays = days
	return cfg
}

func TestSanitizeSandboxConfigAllocationPolicy(t *testing.T) {
	// e2bCfg carries an API key. Sanitize refuses secrets when SYSTEM_AES_KEY
	// is unset, before allocation policy is checked.
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	t.Cleanup(func() { allocation.SetUserModeAvailable(false) })

	allocation.SetUserModeAvailable(false)
	_, err := SanitizeSandboxConfig(allocationTestConfig("user", 0), nil)
	require.ErrorContains(t, err, "allocation_mode=user is not enabled")

	// A config already saved as user stays saveable after the gate closes, so
	// an unrelated edit does not fail; allocation falls back at decide time.
	_, err = SanitizeSandboxConfig(allocationTestConfig("user", 0), allocationTestConfig("user", 0))
	require.NoError(t, err)

	allocation.SetUserModeAvailable(true)
	out, err := SanitizeSandboxConfig(allocationTestConfig("user", 14), nil)
	require.NoError(t, err)
	require.Equal(t, "user", out.AllocationMode)
	require.Equal(t, 14, out.UserSandboxEmptyRetentionDays)

	_, err = SanitizeSandboxConfig(allocationTestConfig("machine", 0), nil)
	require.ErrorContains(t, err, "invalid allocation_mode")
	_, err = SanitizeSandboxConfig(allocationTestConfig("session", 91), nil)
	require.ErrorContains(t, err, "between 1 and 90")
	_, err = SanitizeSandboxConfig(allocationTestConfig("session", -1), nil)
	require.Error(t, err)
}

var (
	_ sandbox.ConfigSandboxClient = (*snapshotReleaseClient)(nil)
	_ sandboxSnapshotReleaser     = (*snapshotReleaseClient)(nil)
	_ sandboxConfigSkillStore     = (*deleteSkillStore)(nil)
	_ sandboxConfigBundleResolver = (*deleteBundleResolver)(nil)
)

func configA() *types.TenantSandboxConfigEntity {
	return &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7, Name: "prod", SandboxType: "e2b",
		Config: e2bCfg("key-a", "https://api.e2b.app", "e2b.app", "t1", 300),
	}
}

func userMachineOfConfigA(id string) sandbox.RemoteSandboxSummary {
	return sandbox.RemoteSandboxSummary{
		ID: id,
		Metadata: map[string]string{
			"weknora_tenant_id":             "7",
			"weknora_sandbox_config_id":     "cfg-a",
			sandbox.MetadataAllocationKey(): sandbox.MetadataAllocationUser,
		},
	}
}

// orderConfigRepo records cordon, delete, and uncordon beside the fake repo's
// own event log, so a test can assert one timeline across the provider and
// the admission registry.
type orderConfigRepo struct {
	repository.TenantSandboxConfigRepository
	order *[]string
}

func (r orderConfigRepo) SetCordon(ctx context.Context, tenantID uint64, id string, at time.Time) error {
	*r.order = append(*r.order, "SetCordon")
	return r.TenantSandboxConfigRepository.SetCordon(ctx, tenantID, id, at)
}

func (r orderConfigRepo) ClearCordon(ctx context.Context, tenantID uint64, id string) error {
	*r.order = append(*r.order, "ClearCordon")
	return r.TenantSandboxConfigRepository.ClearCordon(ctx, tenantID, id)
}

func (r orderConfigRepo) ClearCordonIf(ctx context.Context, tenantID uint64, id string, at time.Time) error {
	*r.order = append(*r.order, "ClearCordon")
	return r.TenantSandboxConfigRepository.ClearCordonIf(ctx, tenantID, id, at)
}

func (r orderConfigRepo) SoftDelete(ctx context.Context, tenantID uint64, id string) error {
	*r.order = append(*r.order, "SoftDelete")
	return r.TenantSandboxConfigRepository.SoftDelete(ctx, tenantID, id)
}

// recordingAlloc records DeleteUserMachinesByConfig. Other methods are not
// part of config deletion; calling one panics instead of pretending success.
type recordingAlloc struct {
	interfaces.SandboxAllocationRepository
	order *[]string
	err   error
	calls int
}

func (r *recordingAlloc) DeleteUserMachinesByConfig(_ context.Context, tenantID uint64, configID string) error {
	r.calls++
	if r.order != nil {
		*r.order = append(*r.order, fmt.Sprintf("DeleteUserMachinesByConfig(%d,%q)", tenantID, configID))
	}
	return r.err
}

type recordingProvider struct {
	summaries []sandbox.RemoteSandboxSummary
	order     *[]string
	lists     int
	deleted   []string
}

func (p *recordingProvider) List(context.Context, sandbox.RemoteListFilter) ([]sandbox.RemoteSandboxSummary, error) {
	p.lists++
	if p.order != nil {
		*p.order = append(*p.order, "List")
	}
	return append([]sandbox.RemoteSandboxSummary(nil), p.summaries...), nil
}

func (p *recordingProvider) Delete(_ context.Context, sandboxID string) error {
	p.deleted = append(p.deleted, sandboxID)
	if p.order != nil {
		*p.order = append(*p.order, "Delete")
	}
	return nil
}

type recordingAdmission struct {
	admission.Registry
	order *[]string
}

func (r recordingAdmission) Close(
	ctx context.Context, scope admission.Scope, ttl time.Duration,
) (admission.Barrier, error) {
	*r.order = append(*r.order, "Close("+scope.Name+")")
	b, err := r.Registry.Close(ctx, scope, ttl)
	if err != nil || b == nil {
		return b, err
	}
	return recordingBarrier{Barrier: b, order: r.order, name: scope.Name}, nil
}

type recordingBarrier struct {
	admission.Barrier
	order *[]string
	name  string
}

func (b recordingBarrier) Release() {
	*b.order = append(*b.order, "release("+b.name+")")
	b.Barrier.Release()
}

func (b recordingBarrier) Seal() {
	*b.order = append(*b.order, "Seal("+b.name+")")
	b.Barrier.Seal()
}

// gatedInventory lists nothing until released, then the sandbox the in-flight
// create produced. early counts lists that ran before the release.
type gatedInventory struct {
	mu       sync.Mutex
	released bool
	calls    int
	early    int
	deleted  []string
}

func (g *gatedInventory) List(context.Context, sandbox.RemoteListFilter) ([]sandbox.RemoteSandboxSummary, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if !g.released {
		g.early++
		return nil, nil
	}
	return []sandbox.RemoteSandboxSummary{userMachineOfConfigA("sb-inflight")}, nil
}

func (g *gatedInventory) Delete(_ context.Context, sandboxID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.deleted = append(g.deleted, sandboxID)
	return nil
}

func (g *gatedInventory) release() {
	g.mu.Lock()
	g.released = true
	g.mu.Unlock()
}

func (g *gatedInventory) snapshot() (calls, early int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls, g.early
}

func newConfigDeleteService(
	repo repository.TenantSandboxConfigRepository,
	client sandbox.ConfigSandboxClient,
) *TenantSandboxConfigService {
	svc := NewTenantSandboxConfigService(repo, stubAgentRepo{}, testGlobalSandboxConfig(), nil, nil)
	svc.newClient = func(*sandbox.Config) (sandbox.ConfigSandboxClient, error) {
		if client == nil {
			return &stubProviderClient{}, nil
		}
		return client, nil
	}
	return svc
}

func TestDeleteConfigWaitsForInFlightCreatesBeforeTheInventory(t *testing.T) {
	cordoned := make(chan struct{})
	repo := &fakeConfigRepo{entity: configA(), onSetCordon: func() { close(cordoned) }}
	reg := admission.NewMemory()
	scope := admission.ConfigCreateScope(7, "cfg-a")
	lease, err := reg.Hold(context.Background(), scope, "create:inflight", admission.DefaultHoldTTL)
	require.NoError(t, err)
	defer lease.Release()

	inventory := &gatedInventory{}
	svc := newConfigDeleteService(repo, inventory)
	svc.WithAdmission(reg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.Delete(context.Background(), 7, "cfg-a", false)
	}()

	select {
	case <-cordoned:
	case <-time.After(time.Second):
		t.Fatal("Delete did not cordon while a create was still in flight")
	}
	time.Sleep(100 * time.Millisecond)
	calls, _ := inventory.snapshot()
	require.Zero(t, calls, "inventory must wait until the in-flight create is released")
	inventory.release()
	lease.Release()

	select {
	case err = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Delete did not return after the in-flight create finished")
	}
	var live *SandboxesStillLiveError
	require.ErrorAs(t, err, &live)
	require.Equal(t, 1, live.Inventory.SandboxCount)
	calls, early := inventory.snapshot()
	require.Equal(t, 1, calls)
	require.Zero(t, early, "List ran only after the release")
	require.False(t, repo.deleted)
	require.NotContains(t, repo.events, "delete")
	require.Equal(t, []string{"get", "cordon", "uncordon"}, repo.events)
}

func TestDeleteConfigTimesOutWhileACreateIsStillRunning(t *testing.T) {
	repo := &fakeConfigRepo{entity: configA()}
	reg := admission.NewMemory()
	scope := admission.ConfigCreateScope(7, "cfg-a")
	lease, err := reg.Hold(context.Background(), scope, "create:stuck", admission.DefaultHoldTTL)
	require.NoError(t, err)
	defer lease.Release()

	client := &stubProviderClient{}
	svc := newConfigDeleteService(repo, client)
	svc.WithAdmission(reg)
	svc.createDrainTimeout = 100 * time.Millisecond

	err = svc.Delete(context.Background(), 7, "cfg-a", false)

	require.ErrorIs(t, err, ErrSandboxConfigCreatesInFlight)
	require.Zero(t, client.listCalls)
	require.False(t, repo.deleted)
	require.Equal(t, []string{"get", "cordon", "uncordon"}, repo.events)
}

func TestDeleteConfigCordonsBeforeInventoryAndUncordonsWhenLive(t *testing.T) {
	fake := &fakeConfigRepo{entity: configA()}
	var order []string
	repo := orderConfigRepo{TenantSandboxConfigRepository: fake, order: &order}
	provider := &recordingProvider{
		summaries: []sandbox.RemoteSandboxSummary{userMachineOfConfigA("sb-user")},
		order:     &order,
	}
	alloc := &recordingAlloc{}
	svc := newConfigDeleteService(repo, provider)
	svc.WithAllocationRepo(alloc)

	err := svc.Delete(context.Background(), 7, "cfg-a", false)

	var live *SandboxesStillLiveError
	require.ErrorAs(t, err, &live)
	require.Equal(t, []string{"SetCordon", "List", "ClearCordon"}, order)
	require.False(t, fake.deleted)
	require.Zero(t, alloc.calls)
	require.Empty(t, provider.deleted)
}

func TestDeleteConfigKeepsTheConfigWhenTheRegistryCannotBeDropped(t *testing.T) {
	fake := &fakeConfigRepo{entity: configA()}
	dropErr := stderrors.New("registry down")
	alloc := &recordingAlloc{err: dropErr}
	svc := newConfigDeleteService(fake, &stubProviderClient{})
	svc.WithAllocationRepo(alloc)

	err := svc.Delete(context.Background(), 7, "cfg-a", false)

	require.ErrorIs(t, err, dropErr)
	require.Equal(t, 1, alloc.calls)
	require.False(t, fake.deleted)
	require.NotContains(t, fake.events, "delete")
}

func TestDeleteConfigDropsTheRegistryThenSoftDeletes(t *testing.T) {
	fake := &fakeConfigRepo{entity: configA()}
	var order []string
	repo := orderConfigRepo{TenantSandboxConfigRepository: fake, order: &order}
	provider := &recordingProvider{order: &order}
	reg := recordingAdmission{Registry: admission.NewMemory(), order: &order}
	alloc := &recordingAlloc{order: &order}
	svc := newConfigDeleteService(repo, provider)
	svc.WithAdmission(reg).WithAllocationRepo(alloc)

	require.NoError(t, svc.Delete(context.Background(), 7, "cfg-a", false))
	require.Equal(t, []string{
		"SetCordon",
		"Close(create)",
		"List",
		`DeleteUserMachinesByConfig(7,"cfg-a")`,
		"SoftDelete",
		"Seal(create)",
		"ClearCordon",
	}, order)
	require.True(t, fake.deleted)
	require.Equal(t, 1, provider.lists, "a drained delete does not sweep after the write")
	require.Empty(t, provider.deleted)
}

func TestDeleteConfigSealsCreatesOnTheResolvedManager(t *testing.T) {
	reg := admission.NewMemory()
	client := &sealRemoteClient{}
	cfg := sandbox.DefaultConfig()
	cfg.Type = sandbox.SandboxTypeCube
	cfg.CubeTemplate = "tpl-test"
	mgr, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
		Config: cfg, Client: client, Store: sandbox.NewMemorySessionSandboxBindingStore(),
		Checker: alwaysExistsChecker{}, SkipHealthProbe: true,
		ConfigID: "cfg-a", ConfigTenantID: 7, Admission: reg,
	})
	require.NoError(t, err)
	ctx := types.WithSandboxTenantID(context.Background(), 7)
	_, err = mgr.ExecShellCommand(ctx, "sess-1", "echo hi", "", time.Second, nil)
	require.NoError(t, err)
	require.Equal(t, int32(1), client.creates.Load())

	svc := newConfigDeleteService(&fakeConfigRepo{entity: configA()}, &stubProviderClient{})
	svc.WithAdmission(reg)
	require.NoError(t, svc.Delete(ctx, 7, "cfg-a", false))

	_, err = mgr.ExecShellCommand(ctx, "sess-2", "echo hi", "", time.Second, nil)
	require.ErrorIs(t, err, sandbox.ErrSandboxConfigCordoned)
	require.Equal(t, int32(1), client.creates.Load(), "a sealed config must not reach the provider")
}

func TestDeleteConfigRefreshesCordonBeforeTheLeaseLapses(t *testing.T) {
	start := time.Date(2026, 10, 16, 3, 4, 5, 0, time.UTC)
	fake := &fakeConfigRepo{entity: configA()}
	repo := &countingRefresh{fakeConfigRepo: fake}
	svc := newConfigDeleteService(repo, &stubProviderClient{})
	svc.WithAdmission(admission.NewMemory())
	svc.now = cordonClock(start)

	require.NoError(t, svc.Delete(context.Background(), 7, "cfg-a", false))
	require.Equal(t, 1, repo.calls)
	require.True(t, repo.prev.Equal(start))
	require.True(t, repo.next.Equal(start.Add(types.SandboxCordonLease/2)))
	require.True(t, fake.deleted)
	require.Contains(t, fake.events, "uncordon")
}

func TestDeleteConfigLeavesACordonOwnedBySomeoneElse(t *testing.T) {
	start := time.Date(2026, 10, 16, 3, 4, 5, 0, time.UTC)
	fake := &fakeConfigRepo{entity: configA()}
	svc := newConfigDeleteService(loseRefresh{fakeConfigRepo: fake}, &stubProviderClient{})
	svc.WithAdmission(admission.NewMemory())
	svc.now = cordonClock(start)

	err := svc.Delete(context.Background(), 7, "cfg-a", false)
	require.ErrorIs(t, err, repository.ErrSandboxConfigCordoned)
	require.False(t, fake.deleted)
	require.NotContains(t, fake.events, "uncordon")
	require.NotContains(t, fake.events, "delete")
}

func cordonClock(start time.Time) func() time.Time {
	var n atomic.Int32
	advanced := start.Add(types.SandboxCordonLease / 2)
	return func() time.Time {
		if n.Add(1) == 1 {
			return start
		}
		return advanced
	}
}

type countingRefresh struct {
	*fakeConfigRepo
	calls      int
	prev, next time.Time
}

func (c *countingRefresh) RefreshCordon(
	ctx context.Context, tenantID uint64, id string, previous, next time.Time,
) error {
	c.calls++
	c.prev, c.next = previous, next
	return c.fakeConfigRepo.RefreshCordon(ctx, tenantID, id, previous, next)
}

type loseRefresh struct {
	*fakeConfigRepo
}

func (loseRefresh) RefreshCordon(context.Context, uint64, string, time.Time, time.Time) error {
	return repository.ErrSandboxConfigCordoned
}

type alwaysExistsChecker struct{}

func (alwaysExistsChecker) SessionExists(context.Context, sandbox.SessionSandboxKey) (bool, error) {
	return true, nil
}

type sealRemoteClient struct {
	creates atomic.Int32
}

func (c *sealRemoteClient) Provider() sandbox.RemoteProvider { return sandbox.SandboxTypeCube }
func (c *sealRemoteClient) Capabilities() sandbox.RemoteSandboxCapabilities {
	return sandbox.RemoteSandboxCapabilities{SupportsReconnect: true}
}
func (c *sealRemoteClient) Health(context.Context) error { return nil }
func (c *sealRemoteClient) Create(context.Context, sandbox.RemoteCreateRequest) (sandbox.RemoteSandboxHandle, error) {
	n := c.creates.Add(1)
	return sealHandle{id: fmt.Sprintf("sb-%d", n)}, nil
}

func (c *sealRemoteClient) Connect(
	_ context.Context, req sandbox.RemoteConnectRequest,
) (sandbox.RemoteSandboxHandle, error) {
	return sealHandle{id: req.SandboxID}, nil
}

func (c *sealRemoteClient) Get(context.Context, string) (*sandbox.RemoteSandboxSummary, error) {
	return nil, nil
}

func (c *sealRemoteClient) List(context.Context, sandbox.RemoteListFilter) ([]sandbox.RemoteSandboxSummary, error) {
	return nil, nil
}
func (c *sealRemoteClient) Delete(context.Context, string) error { return nil }
func (c *sealRemoteClient) Exec(
	context.Context, sandbox.RemoteSandboxHandle, sandbox.RemoteExecRequest,
) (*sandbox.RemoteExecResult, error) {
	return &sandbox.RemoteExecResult{}, nil
}

func (c *sealRemoteClient) WriteFile(context.Context, sandbox.RemoteSandboxHandle, string, []byte) error {
	return nil
}

func (c *sealRemoteClient) ReadFile(context.Context, sandbox.RemoteSandboxHandle, string) ([]byte, error) {
	return nil, nil
}

func (c *sealRemoteClient) ListDir(
	context.Context, sandbox.RemoteSandboxHandle, string,
) ([]sandbox.RemoteDirEntry, error) {
	return nil, nil
}

func (c *sealRemoteClient) MakeDir(context.Context, sandbox.RemoteSandboxHandle, string) error {
	return nil
}

func (c *sealRemoteClient) Remove(context.Context, sandbox.RemoteSandboxHandle, string) error {
	return nil
}

func (c *sealRemoteClient) Stat(
	context.Context, sandbox.RemoteSandboxHandle, string,
) (*sandbox.RemoteStatEntry, error) {
	return nil, nil
}

type sealHandle struct{ id string }

func (h sealHandle) ID() string                       { return h.id }
func (h sealHandle) Provider() sandbox.RemoteProvider { return sandbox.SandboxTypeCube }
func (h sealHandle) Metadata() map[string]string      { return nil }

// recordingReleaser stands in for WorkspaceCleaner.ReleaseMachine. busy
// reports a terminal, desktop or command still registered on the machine.
type recordingReleaser struct {
	released []allocation.UserMachine
	busy     bool
}

func (r *recordingReleaser) ReleaseMachine(_ context.Context, m allocation.UserMachine) (bool, error) {
	if r.busy {
		return false, nil
	}
	r.released = append(r.released, m)
	return true, nil
}

// userMachineAllocRepo embeds the allocation repository and overrides only
// the reads the admin list and release actions use.
type userMachineAllocRepo struct {
	interfaces.SandboxAllocationRepository
	byDigest map[string]*types.SandboxUserMachine
	byConfig []*types.SandboxUserMachine
	live     map[string]int64
}

func (r userMachineAllocRepo) GetUserMachine(_ context.Context, digest string) (*types.SandboxUserMachine, error) {
	return r.byDigest[digest], nil
}

func (r userMachineAllocRepo) ListUserMachinesByConfig(
	context.Context, uint64, string,
) ([]*types.SandboxUserMachine, error) {
	return r.byConfig, nil
}

func (r userMachineAllocRepo) CountLiveAssignments(_ context.Context, digest string) (int64, error) {
	return r.live[digest], nil
}

func TestReleaseUserMachineChecksConfigOwnershipAndBusy(t *testing.T) {
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{ID: "cfg-a", TenantID: 7}}
	allocRepo := userMachineAllocRepo{byDigest: map[string]*types.SandboxUserMachine{
		"d-1": {MachineDigest: "d-1", ConfigTenantID: 7, ConfigID: "cfg-a", OwnerID: "u-1", OwnerTenantID: 7},
		"d-x": {MachineDigest: "d-x", ConfigTenantID: 7, ConfigID: "cfg-b", OwnerID: "u-1", OwnerTenantID: 7},
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{})
	rel := &recordingReleaser{}
	svc.WithAllocationRepo(allocRepo).WithUserMachineReleaser(rel)

	require.NoError(t, svc.ReleaseUserMachine(context.Background(), 7, "cfg-a", "d-1"))
	require.Equal(t, []allocation.UserMachine{{
		OwnerTenantID: 7, OwnerID: "u-1", ConfigTenantID: 7, ConfigID: "cfg-a",
	}}, rel.released)
	// A digest that belongs to another config is the same answer as a missing
	// config: this file reports absence as a not-found AppError, not a sentinel.
	err := svc.ReleaseUserMachine(context.Background(), 7, "cfg-a", "d-x")
	appErr, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, http.StatusNotFound, appErr.HTTPCode)
	require.Equal(t, "sandbox config not found", appErr.Message)
	rel.busy = true
	require.ErrorIs(t, svc.ReleaseUserMachine(context.Background(), 7, "cfg-a", "d-1"), ErrUserSandboxBusy)
	require.Len(t, rel.released, 1, "a busy machine is not released")
}

func TestListUserMachinesHidesOtherWorkspacesAndClocksRetention(t *testing.T) {
	empty := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7,
		Config: &types.TenantSandboxConfig{UserSandboxEmptyRetentionDays: 14},
	}}
	alloc := userMachineAllocRepo{
		byConfig: []*types.SandboxUserMachine{
			{
				MachineDigest: "d-own", OwnerTenantID: 7, OwnerID: "u-1",
				ConfigTenantID: 7, ConfigID: "cfg-a", EmptySince: &empty,
			},
			{
				MachineDigest: "d-borrow", OwnerTenantID: 9, OwnerID: "secret-owner",
				ConfigTenantID: 7, ConfigID: "cfg-a",
			},
		},
		live: map[string]int64{"d-own": 2},
	}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{}).WithAllocationRepo(alloc)

	got, err := svc.ListUserMachines(context.Background(), 7, "cfg-a")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "u-1", got[0].OwnerID)
	require.EqualValues(t, 2, got[0].LiveSessions)
	require.NotNil(t, got[0].ReclaimAfter)
	require.True(t, got[0].ReclaimAfter.Equal(empty.Add(14*24*time.Hour)))
	require.Empty(t, got[1].OwnerID, "a borrower from another workspace is not named")
	require.Nil(t, got[1].EmptySince)
	require.Nil(t, got[1].ReclaimAfter)
}

func TestListUserMachinesUsesDefaultRetentionWhenUnset(t *testing.T) {
	empty := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakeConfigRepo{entity: &types.TenantSandboxConfigEntity{
		ID: "cfg-a", TenantID: 7,
		Config: &types.TenantSandboxConfig{UserSandboxEmptyRetentionDays: 0},
	}}
	alloc := userMachineAllocRepo{byConfig: []*types.SandboxUserMachine{
		{
			MachineDigest: "d-1", OwnerTenantID: 7, OwnerID: "u-1",
			ConfigTenantID: 7, ConfigID: "cfg-a", EmptySince: &empty,
		},
	}}
	svc := newTestConfigService(t, repo, nil, stubAgentRepo{}).WithAllocationRepo(alloc)

	got, err := svc.ListUserMachines(context.Background(), 7, "cfg-a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, got[0].ReclaimAfter.Equal(empty.Add(7*24*time.Hour)))
}

func TestInventoryCountsUserMachinesSeparately(t *testing.T) {
	svc := &TenantSandboxConfigService{}
	inv := svc.inventoryFromSummaries(context.Background(), 7, "cfg-a", []sandbox.RemoteSandboxSummary{
		{ID: "sb-1", Metadata: map[string]string{sandbox.MetadataSessionIDKey(): "s-1"}},
		{ID: "sb-2", Metadata: map[string]string{sandbox.MetadataAllocationKey(): sandbox.MetadataAllocationUser}},
	})
	require.Equal(t, 2, inv.SandboxCount)
	require.Equal(t, []string{"s-1"}, inv.SessionIDs)
	require.Equal(t, 1, inv.UserSandboxCount)
}
