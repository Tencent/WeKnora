package service

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// newPluginTestRepo applies the real SQLite migration so the partial unique
// indexes behave as in production.
func newPluginTestRepo(t *testing.T) interfaces.PluginRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	ddl, err := os.ReadFile("../../../migrations/sqlite/000051_plugins.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	return repository.NewPluginRepository(db)
}

// fakePluginMCPService keeps the guard of the real service: DeleteMCPService
// refuses a service a plugin owns. Unguarded delete does not touch the ledger.
type fakePluginMCPService struct {
	interfaces.MCPServiceService
	lookup interfaces.PluginOwnershipLookup

	mu              sync.Mutex
	created         []*types.MCPService
	guardedDeletes  []string
	unguardedDelete []string
	updates         []*types.MCPService
	updateFields    []map[string]bool
	testResult      *types.MCPTestResult
	testErr         error
	testDelay       time.Duration
}

func (f *fakePluginMCPService) DeleteMCPService(ctx context.Context, tenantID uint64, id string) error {
	f.mu.Lock()
	f.guardedDeletes = append(f.guardedDeletes, id)
	f.mu.Unlock()
	info, err := f.lookup.LookupPluginByResource(ctx, tenantID, types.PluginResourceKindMCPService, id)
	if err != nil {
		return err
	}
	if err := pluginOwnedDeleteError(info, types.PluginResourceKindMCPService); err != nil {
		return err
	}
	return f.DeleteMCPServiceUnguarded(ctx, tenantID, id)
}

func (f *fakePluginMCPService) DeleteMCPServiceUnguarded(_ context.Context, _ uint64, id string) error {
	f.mu.Lock()
	f.unguardedDelete = append(f.unguardedDelete, id)
	f.mu.Unlock()
	return nil
}

func (f *fakePluginMCPService) TestMCPService(ctx context.Context, _ uint64, _ string) (*types.MCPTestResult, error) {
	if f.testDelay > 0 {
		select {
		case <-time.After(f.testDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.testResult, f.testErr
}

func (f *fakePluginMCPService) UpdateMCPService(
	_ context.Context, svc *types.MCPService, fields map[string]bool,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, svc)
	f.updateFields = append(f.updateFields, fields)
	return nil
}

// seedPackage makes sure marketplace mpID exists with a package called name
// and that tenantID is bound to it, and returns the package ID.
func seedPackage(t *testing.T, repo interfaces.PluginRepository, tenantID uint64, mpID, name string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	pkgID := "pkg-" + mpID + "-" + name
	mp, err := repo.GetSharedMarketplace(ctx, mpID)
	require.NoError(t, err)
	if mp == nil {
		mp = &types.PluginMarketplaceEntity{
			ID: mpID, Name: mpID + "-market",
			BundleSHA256: "sha-" + mpID, CreatedAt: now, UpdatedAt: now,
		}
		require.NoError(t, repo.CreateMarketplace(ctx, mp, []*types.PluginPackageEntity{
			{ID: pkgID, Name: name, CreatedAt: now},
		}))
	}
	if bound, _ := repo.GetMarketplaceByID(ctx, tenantID, mpID); bound == nil {
		require.NoError(t, repo.BindMarketplace(ctx, tenantID, mp))
	}
	return pkgID
}

func seedInstall(
	t *testing.T,
	repo interfaces.PluginRepository,
	tenantID uint64,
	mpID, name string,
	sandboxes ...string,
) string {
	t.Helper()
	now := time.Now()
	installID := uuid.NewString()
	require.NoError(t, repo.CreateInstall(context.Background(), &types.PluginInstall{
		ID: installID, TenantID: tenantID, MarketplaceID: mpID,
		PackageID: seedPackage(t, repo, tenantID, mpID, name), PluginName: name,
		SandboxConfigIDs: sandboxes, Status: types.PluginStatusReady, CreatedAt: now, UpdatedAt: now,
	}))
	return installID
}

func seedMCPOnlyInstall(
	t *testing.T,
	repo interfaces.PluginRepository,
	tenantID uint64,
	mpID, name string,
) (string, string) {
	t.Helper()
	installID := seedInstall(t, repo, tenantID, mpID, name)
	mcpID := uuid.NewString()
	require.NoError(t, repo.CreateBinding(context.Background(), &types.PluginTenantBindingEntity{
		ID: uuid.NewString(), TenantID: tenantID, PluginInstallID: installID,
		Kind:         types.PluginResourceKindMCPService,
		ResourceID:   mcpID,
		ResourceName: name + "__srv",
		CreatedAt:    time.Now(),
	}))
	return installID, mcpID
}

func TestUninstallPluginBypassesItsOwnDeleteGuard(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	mcp := &fakePluginMCPService{lookup: repo}
	svc := &PluginService{repo: repo, mcpSvc: mcp}
	const tenantID = 7
	installID, mcpID := seedMCPOnlyInstall(t, repo, tenantID, "mp-1", "acme")
	otherID := uuid.NewString()
	require.NoError(t, repo.CreateBinding(ctx, &types.PluginTenantBindingEntity{
		ID: uuid.NewString(), TenantID: tenantID, PluginInstallID: installID,
		Kind: types.PluginResourceKindMCPService, ResourceID: otherID, ResourceName: "acme__other",
		CreatedAt: time.Now(),
	}))

	// A plugin-owned service cannot be deleted from the MCP page.
	err := mcp.DeleteMCPService(ctx, tenantID, mcpID)
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, apperrors.ErrConflict, appErr.Code)

	require.NoError(t, svc.UninstallPlugin(ctx, tenantID, "mp-1", "acme"))
	assert.ElementsMatch(t, []string{mcpID, otherID}, mcp.unguardedDelete)
	assert.Len(t, mcp.guardedDeletes, 1, "uninstall must not go through the guarded delete")

	install, err := repo.GetInstallByName(ctx, tenantID, "mp-1", "acme")
	require.NoError(t, err)
	assert.Nil(t, install)
	resources, err := repo.ListBindingsByKind(ctx, tenantID, types.PluginResourceKindMCPService)
	require.NoError(t, err)
	assert.Empty(t, resources)
}

func TestPluginCanBeReinstalledAfterUninstall(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	svc := &PluginService{repo: repo, mcpSvc: &fakePluginMCPService{lookup: repo}}
	const tenantID = 7
	seedMCPOnlyInstall(t, repo, tenantID, "mp-1", "acme")

	// A live row with the same name is rejected by the partial unique index.
	dup := &types.PluginInstall{
		ID: uuid.NewString(), TenantID: tenantID, MarketplaceID: "mp-1", PackageID: "pkg-mp-1-acme", PluginName: "acme",
		Status: types.PluginStatusInstalling, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.Error(t, repo.CreateInstall(ctx, dup))

	require.NoError(t, svc.UninstallPlugin(ctx, tenantID, "mp-1", "acme"))
	_, mcpID := seedMCPOnlyInstall(t, repo, tenantID, "mp-1", "acme")

	info, err := repo.LookupPluginByResource(ctx, tenantID, types.PluginResourceKindMCPService, mcpID)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "acme", info.PluginName)
}

func TestPluginOwnedMCPCannotBeDeletedDirectly(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	mcp := &fakePluginMCPService{lookup: repo}
	const tenantID = 7
	_, mcpID := seedMCPOnlyInstall(t, repo, tenantID, "mp-1", "calcom")

	err := mcp.DeleteMCPService(ctx, tenantID, mcpID)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, apperrors.ErrConflict, appErr.Code)
	assert.Empty(t, mcp.unguardedDelete)

	install, err := repo.GetInstallByName(ctx, tenantID, "mp-1", "calcom")
	require.NoError(t, err)
	assert.NotNil(t, install)
}

type fakeSandboxConfigs struct {
	repository.TenantSandboxConfigRepository
	ids []string
}

func (f *fakeSandboxConfigs) GetByID(
	_ context.Context,
	tenantID uint64,
	id string,
) (*types.TenantSandboxConfigEntity, error) {
	for _, known := range f.ids {
		if known == id {
			return &types.TenantSandboxConfigEntity{ID: id, TenantID: tenantID}, nil
		}
	}
	return nil, nil
}

func (f *fakeSandboxConfigs) ListByTenant(
	_ context.Context,
	tenantID uint64,
) ([]*types.TenantSandboxConfigEntity, error) {
	out := make([]*types.TenantSandboxConfigEntity, 0, len(f.ids))
	for _, id := range f.ids {
		out = append(out, &types.TenantSandboxConfigEntity{ID: id, TenantID: tenantID})
	}
	return out, nil
}

func TestUpdatePluginSandboxesUnchangedMakesNoSkillCalls(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	const tenantID = 7
	seedInstall(t, repo, tenantID, "mp-1", "acme", "cfg-a", "cfg-b")
	// Only the config lookup is wired: any install or remove call would reach
	// the nil skill repository and panic.
	skills := &TenantSkillService{configs: &fakeSandboxConfigs{ids: []string{"cfg-a", "cfg-b"}}}
	svc := &PluginService{repo: repo, skillSvc: skills}

	require.NoError(t, svc.UpdatePluginSandboxes(ctx, tenantID, "mp-1", "acme", []string{"cfg-b", "cfg-a", "cfg-a"}))
	install, err := repo.GetInstallByName(ctx, tenantID, "mp-1", "acme")
	require.NoError(t, err)
	assert.Equal(t, types.PluginStatusReady, install.Status, "an unchanged list must not restart installation")
}

func TestPluginMCPAuthMapsTemplatesToEncryptedAuth(t *testing.T) {
	env := map[string]string{"ACME_TOKEN": "tok-123", "ACME_KEY": "key-456"}

	headers, auth := pluginMCPAuth(map[string]string{
		"Authorization": "Bearer ${ACME_TOKEN}",
		"X-Trace":       "static",
	}, env)
	assert.Equal(t, types.MCPAuthBearer, auth.AuthType)
	assert.Equal(t, "tok-123", auth.Token)
	assert.Equal(t, types.MCPHeaders{"X-Trace": "static"}, headers)
	for _, value := range headers {
		assert.NotContains(t, value, "tok-123", "secrets must not land in the plaintext headers column")
	}

	headers, auth = pluginMCPAuth(map[string]string{"X-Api-Key": "${ACME_KEY}"}, env)
	assert.Equal(t, types.MCPAuthAPIKey, auth.AuthType)
	assert.Equal(t, "X-Api-Key", auth.APIKeyHeader)
	assert.Equal(t, "key-456", auth.APIKey)
	assert.Empty(t, headers)

	headers, auth = pluginMCPAuth(nil, env)
	assert.Equal(t, types.MCPAuthNone, auth.AuthType)
	assert.Empty(t, headers)
}

func TestProbeMCPServicesOAuthSwitchesToOAuth(t *testing.T) {
	ctx := context.Background()
	mcp := &fakePluginMCPService{testResult: &types.MCPTestResult{Success: false, OAuthRequired: true}}
	svc := &PluginService{mcpSvc: mcp}
	service := &types.MCPService{ID: "svc-1", Name: "acme__docs", AuthConfig: &types.MCPAuthConfig{}}

	states := map[string]string{}
	svc.probeMCPServicesOAuth(ctx, 7, []*types.MCPService{service}, states)

	assert.Equal(t, "needs_oauth", states["acme__docs"])
	require.Len(t, mcp.updates, 1)
	assert.Equal(t, types.MCPAuthOAuth, mcp.updates[0].AuthConfig.AuthType)
	assert.True(t, mcp.updateFields[0]["auth_type"])
}

func TestProbeMCPServicesOAuthKeepsStaticCredential(t *testing.T) {
	ctx := context.Background()
	mcp := &fakePluginMCPService{testResult: &types.MCPTestResult{Success: false, OAuthRequired: true}}
	svc := &PluginService{mcpSvc: mcp}
	service := &types.MCPService{ID: "svc-1", Name: "acme__docs", AuthConfig: &types.MCPAuthConfig{
		AuthType: types.MCPAuthBearer, Token: "tok",
	}}

	states := map[string]string{}
	svc.probeMCPServicesOAuth(ctx, 7, []*types.MCPService{service}, states)

	assert.Equal(t, "needs_oauth", states["acme__docs"])
	assert.Empty(t, mcp.updates, "a configured bearer token is not replaced by OAuth")
	assert.Equal(t, types.MCPAuthBearer, service.AuthConfig.AuthType)
}

type recordingPluginMetadata struct {
	fakePluginMCPService
	saved []*types.MCPTool
}

func (r *recordingPluginMetadata) PersistMCPMetadata(
	_ context.Context, _ uint64, _ string, tools []*types.MCPTool, _ string,
) error {
	r.saved = append([]*types.MCPTool(nil), tools...)
	return nil
}

func TestProbeMCPServicesOAuthStoresListedTools(t *testing.T) {
	ctx := context.Background()
	tool := &types.MCPTool{Name: "resolve-library-id"}
	mcp := &recordingPluginMetadata{fakePluginMCPService: fakePluginMCPService{
		testResult: &types.MCPTestResult{Success: true, Tools: []*types.MCPTool{tool}},
	}}
	svc := &PluginService{mcpSvc: mcp}
	service := &types.MCPService{ID: "svc-1", Name: "acme-plugins__context7", AuthConfig: &types.MCPAuthConfig{}}

	states := map[string]string{}
	svc.probeMCPServicesOAuth(ctx, 7, []*types.MCPService{service}, states)

	assert.Equal(t, "ready", states["acme-plugins__context7"])
	require.Len(t, mcp.saved, 1)
	assert.Equal(t, "resolve-library-id", mcp.saved[0].Name)
}

func TestProbeMCPServicesOAuthDoesNotStoreToolsBeforeConsent(t *testing.T) {
	ctx := context.Background()
	mcp := &recordingPluginMetadata{fakePluginMCPService: fakePluginMCPService{
		testResult: &types.MCPTestResult{
			Success:       false,
			OAuthRequired: true,
			Tools:         []*types.MCPTool{{Name: "hidden"}},
		},
	}}
	svc := &PluginService{mcpSvc: mcp}
	service := &types.MCPService{ID: "svc-1", Name: "acme__docs", AuthConfig: &types.MCPAuthConfig{}}

	states := map[string]string{}
	svc.probeMCPServicesOAuth(ctx, 7, []*types.MCPService{service}, states)

	assert.Equal(t, "needs_oauth", states["acme__docs"])
	assert.Empty(t, mcp.saved)
}

func TestProbeMCPServicesOAuthFailureIsOnlyAWarning(t *testing.T) {
	ctx := context.Background()
	mcp := &fakePluginMCPService{testErr: errors.New("connection refused")}
	svc := &PluginService{mcpSvc: mcp}
	service := &types.MCPService{ID: "svc-1", Name: "acme__docs", AuthConfig: &types.MCPAuthConfig{}}

	states := map[string]string{}
	svc.probeMCPServicesOAuth(ctx, 7, []*types.MCPService{service}, states)

	assert.Equal(t, "unknown", states["acme__docs"])
	assert.Empty(t, mcp.updates, "a failed probe must leave the auth type alone")
}

type memoryFileService struct {
	interfaces.FileService
	files   map[string][]byte
	tenants []uint64
}

func (m *memoryFileService) SaveBytes(
	_ context.Context,
	data []byte,
	tenantID uint64,
	name string,
	_ bool,
) (string, error) {
	m.tenants = append(m.tenants, tenantID)
	m.files["mem://"+name] = append([]byte(nil), data...)
	return "mem://" + name, nil
}

func (m *memoryFileService) DeleteFile(_ context.Context, path string) error {
	delete(m.files, path)
	return nil
}

func (m *memoryFileService) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	data, ok := m.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

type pluginTestMCPRepo struct {
	interfaces.MCPServiceRepository
	services []*types.MCPService
}

func (f *pluginTestMCPRepo) List(context.Context, uint64) ([]*types.MCPService, error) {
	return f.services, nil
}

func (f *pluginTestMCPRepo) GetByID(_ context.Context, _ uint64, id string) (*types.MCPService, error) {
	for _, svc := range f.services {
		if svc != nil && svc.ID == id {
			return svc, nil
		}
	}
	return nil, nil
}

// pluginOAuthTokenStub records whether any principal holds a token. The
// embedded repository is never called; only ServiceHasToken is.
type pluginOAuthTokenStub struct {
	interfaces.MCPOAuthRepository
	has bool
}

func (s *pluginOAuthTokenStub) ServiceHasToken(context.Context, uint64, string) (bool, error) {
	return s.has, nil
}

func (f *fakePluginMCPService) CreateMCPService(_ context.Context, svc *types.MCPService) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, svc)
	return nil
}

// newAcmePluginService registers the acme-plugins fixture as a marketplace.
func newAcmePluginService(t *testing.T, tenantID uint64) (*PluginService, *fakePluginMCPService, string) {
	t.Helper()
	archive := acmePluginsArchive(t)
	repo := newPluginTestRepo(t)
	mcp := &fakePluginMCPService{lookup: repo}
	svc := &PluginService{
		repo:    repo,
		mcpSvc:  mcp,
		mcpRepo: &pluginTestMCPRepo{},
		files:   &memoryFileService{files: map[string][]byte{}},
	}
	mp, err := svc.AddMarketplaceFromArchive(context.Background(), tenantID, archive)
	require.NoError(t, err)
	return svc, mcp, mp.ID
}

func TestInstallMCPOnlyPluginRoundTrip(t *testing.T) {
	ctx := context.Background()
	const tenantID = 7
	svc, mcp, mpID := newAcmePluginService(t, tenantID)
	mcp.testResult = &types.MCPTestResult{OAuthRequired: true}

	result, err := svc.InstallPlugin(ctx, tenantID, mpID, "calcom", nil, nil)
	require.NoError(t, err)
	assert.False(t, result.AsyncInstallation)
	assert.Equal(t, types.PluginStatusNeedsOAuth, result.Status)
	assert.Equal(t, "needs_oauth", result.MCPOAuthStates["acme-plugins__calcom"])
	require.Len(t, mcp.created, 1)
	assert.Equal(t, "acme-plugins__calcom", mcp.created[0].Name)
	assert.True(t, mcp.created[0].AuthConfig.IsOAuth())

	installed, err := svc.repo.GetInstallByName(ctx, tenantID, mpID, "calcom")
	require.NoError(t, err)
	require.NotNil(t, installed)
	assert.Equal(t, types.PluginStatusNeedsOAuth, installed.Status)

	svc.mcpRepo.(*pluginTestMCPRepo).services = []*types.MCPService{mcp.created[0]}
	tokens := &pluginOAuthTokenStub{}
	svc.oauth = tokens
	svc.NoteMCPAuthorized(ctx, tenantID, mcp.created[0].ID)
	installed, err = svc.repo.GetInstallByName(ctx, tenantID, mpID, "calcom")
	require.NoError(t, err)
	assert.Equal(t, types.PluginStatusNeedsOAuth, installed.Status, "authorization without a token stays pending")

	tokens.has = true
	svc.NoteMCPAuthorized(ctx, tenantID, mcp.created[0].ID)
	installed, err = svc.repo.GetInstallByName(ctx, tenantID, mpID, "calcom")
	require.NoError(t, err)
	assert.Equal(t, types.PluginStatusReady, installed.Status)

	info, err := svc.repo.LookupPluginByResource(ctx, tenantID, types.PluginResourceKindMCPService, mcp.created[0].ID)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "acme-plugins", info.MarketplaceName)

	_, err = svc.InstallPlugin(ctx, tenantID, mpID, "calcom", nil, nil)
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr), "got %v", err)
	assert.Equal(t, apperrors.ErrConflict, appErr.Code)

	require.NoError(t, svc.UninstallPlugin(ctx, tenantID, mpID, "calcom"))
	assert.Equal(t, []string{mcp.created[0].ID}, mcp.unguardedDelete)
	svc.mcpRepo.(*pluginTestMCPRepo).services = nil
	_, err = svc.InstallPlugin(ctx, tenantID, mpID, "calcom", nil, nil)
	require.NoError(t, err, "a plugin installs again after uninstall")
}

func TestInstallPluginRejectsNameHeldByAnotherMarketplace(t *testing.T) {
	ctx := context.Background()
	const tenantID = 7
	svc, _, mpID := newAcmePluginService(t, tenantID)
	seedMCPOnlyInstall(t, svc.repo, tenantID, "mp-other", "calcom")

	_, err := svc.InstallPlugin(ctx, tenantID, mpID, "calcom", nil, nil)
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr), "got %v", err)
	assert.Equal(t, apperrors.ErrConflict, appErr.Code)
	assert.True(t, strings.HasPrefix(appErr.Message, "plugin_name_taken:"), appErr.Message)
	assert.Contains(t, appErr.Message, "mp-other-market")
}

// repackAcmeArchive returns the fixture as a zip with one extra file, i.e. the
// same marketplace name with a different sha256.
func repackAcmeArchive(t *testing.T) []byte {
	t.Helper()
	archive := acmePluginsArchive(t)
	files, err := UnpackMarketplaceArchive(archive)
	require.NoError(t, err)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
	}
	w, err := zw.Create("CHANGELOG.md")
	require.NoError(t, err)
	_, err = w.Write([]byte("v2"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestMarketplaceIsSharedAcrossTenants(t *testing.T) {
	ctx := context.Background()
	const tenantA, tenantB = 7, 8
	svc, _, mpID := newAcmePluginService(t, tenantA)
	store := svc.files.(*memoryFileService)
	archive := acmePluginsArchive(t)
	storedFiles := len(store.files)
	require.Greater(t, storedFiles, 1, "the archive and each skill zip are stored")

	mpB, err := svc.AddMarketplaceFromArchive(ctx, tenantB, archive)
	require.NoError(t, err)
	assert.Equal(t, mpID, mpB.ID, "identical archives share one marketplace row")
	assert.Len(t, store.files, storedFiles, "identical archives share one stored copy")

	again, err := svc.AddMarketplaceFromArchive(ctx, tenantB, archive)
	require.NoError(t, err)
	assert.Equal(t, mpID, again.ID, "re-importing the same bytes is a no-op")

	listA, err := svc.ListMarketplaces(ctx, tenantA)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	listC, err := svc.ListMarketplaces(ctx, 9)
	require.NoError(t, err)
	assert.Empty(t, listC, "a tenant only sees marketplaces it imported")

	// Installs stay per tenant.
	_, err = svc.InstallPlugin(ctx, tenantA, mpID, "calcom", nil, nil)
	require.NoError(t, err)
	_, err = svc.InstallPlugin(ctx, tenantB, mpID, "calcom", nil, nil)
	require.NoError(t, err)

	var appErr *apperrors.AppError
	err = svc.DeleteMarketplace(ctx, tenantA, mpID)
	require.True(t, errors.As(err, &appErr), "got %v", err)
	assert.Equal(t, apperrors.ErrConflict, appErr.Code)

	require.NoError(t, svc.UninstallPlugin(ctx, tenantA, mpID, "calcom"))
	require.NoError(t, svc.DeleteMarketplace(ctx, tenantA, mpID))
	listA, err = svc.ListMarketplaces(ctx, tenantA)
	require.NoError(t, err)
	assert.Empty(t, listA)
	shared, err := svc.repo.GetSharedMarketplace(ctx, mpID)
	require.NoError(t, err)
	require.NotNil(t, shared, "tenant B still holds the marketplace")
	assert.Len(t, store.files, storedFiles)

	require.NoError(t, svc.UninstallPlugin(ctx, tenantB, mpID, "calcom"))
	require.NoError(t, svc.DeleteMarketplace(ctx, tenantB, mpID))
	shared, err = svc.repo.GetSharedMarketplace(ctx, mpID)
	require.NoError(t, err)
	assert.Nil(t, shared, "the last binding releases the shared row")
	assert.Empty(t, store.files, "the last binding releases the stored archive")
}

func TestReimportMovesTenantAndInstallsToNewArchive(t *testing.T) {
	ctx := context.Background()
	const tenantA, tenantB = 7, 8
	svc, _, oldID := newAcmePluginService(t, tenantA)
	archive := acmePluginsArchive(t)
	_, err := svc.AddMarketplaceFromArchive(ctx, tenantB, archive)
	require.NoError(t, err)
	_, err = svc.InstallPlugin(ctx, tenantA, oldID, "calcom", nil, nil)
	require.NoError(t, err)

	newMP, err := svc.AddMarketplaceFromArchive(ctx, tenantA, repackAcmeArchive(t))
	require.NoError(t, err)
	assert.NotEqual(t, oldID, newMP.ID)

	listA, err := svc.ListMarketplaces(ctx, tenantA)
	require.NoError(t, err)
	require.Len(t, listA, 1, "a tenant holds one marketplace per name")
	assert.Equal(t, newMP.ID, listA[0].ID)
	install, err := svc.repo.GetInstallByName(ctx, tenantA, newMP.ID, "calcom")
	require.NoError(t, err)
	require.NotNil(t, install, "installs follow the tenant to the new archive")

	listB, err := svc.ListMarketplaces(ctx, tenantB)
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, oldID, listB[0].ID, "other tenants keep the archive they imported")
}

func TestPackagesServeEverythingButImport(t *testing.T) {
	ctx := context.Background()
	const tenantID = 7
	svc, _, mpID := newAcmePluginService(t, tenantID)
	store := svc.files.(*memoryFileService)
	mp, err := svc.repo.GetSharedMarketplace(ctx, mpID)
	require.NoError(t, err)

	pkg, err := svc.repo.GetPackage(ctx, mpID, "sql-style")
	require.NoError(t, err)
	require.NotNil(t, pkg)
	require.NotEmpty(t, pkg.Skills)
	for _, skill := range pkg.Skills {
		assert.Contains(t, store.files, skill.BundleRef, "skill zips are stored at import")
	}

	// Without the marketplace archive, nothing after import can unpack it.
	delete(store.files, mp.BundleRef)

	list, err := svc.ListMarketplaces(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	names := make([]string, 0, len(list[0].Plugins))
	for _, p := range list[0].Plugins {
		names = append(names, p.Name)
		if p.Name == "sql-style" {
			assert.Equal(t, len(pkg.Skills), p.SkillCount)
			assert.True(t, p.RequiresSandbox)
		}
	}
	assert.Contains(t, names, "calcom")

	preflight, err := svc.PreflightPlugin(ctx, tenantID, mpID, "calcom")
	require.NoError(t, err)
	assert.Equal(t, 1, preflight.MCPCount)

	_, err = svc.InstallPlugin(ctx, tenantID, mpID, "calcom", nil, nil)
	require.NoError(t, err)
	installs, err := svc.ListInstalledPlugins(ctx, tenantID)
	require.NoError(t, err)
	require.Len(t, installs, 1)
	assert.Equal(t, mpID, installs[0].MarketplaceID)
	assert.Equal(t, "calcom", installs[0].PluginName)
}

func TestDiscardedPluginCatalogIsDeletedWhenIdle(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	ddl, err := os.ReadFile("../../../migrations/sqlite/000051_plugins.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillCatalogEntity{},
		&types.TenantSkillSnapshotEntity{}, &types.TenantUserEnvVar{},
	))
	pluginRepo := repository.NewPluginRepository(db)
	skillRepo := repository.NewTenantSkillRepository(db)
	const tenantID uint64 = 7
	installID := seedInstall(t, pluginRepo, tenantID, "mp-1", "acme")
	catalogID := uuid.NewString()
	require.NoError(t, skillRepo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: catalogID, TenantID: tenantID, Name: "acme__skill",
	}))
	require.NoError(t, skillRepo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-1", TenantID: tenantID, SandboxConfigID: "cfg-1", CatalogID: catalogID,
		Name: "acme__skill", Status: types.SkillStatusReady,
	}))
	require.NoError(t, pluginRepo.CreateBinding(ctx, &types.PluginTenantBindingEntity{
		ID: uuid.NewString(), TenantID: tenantID, PluginInstallID: installID,
		Kind: types.PluginResourceKindSkillCatalog, ResourceID: catalogID, ResourceName: "acme__skill",
		CreatedAt: time.Now(),
	}))
	skills := NewTenantSkillService(
		skillRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		HostSandboxManager{},
		pluginRepo,
	)

	// A sandbox install that disappears on its own does not delete the catalog
	// or the plugin install. Only a catalog the plugin uninstall discarded does,
	// and only after its last install is gone.
	require.NoError(t, skillRepo.DeleteSkill(ctx, tenantID, "cfg-1", "sk-1"))
	skills.deleteDiscardedCatalogIfIdle(ctx, tenantID, catalogID)
	cat, err := skillRepo.GetCatalog(ctx, tenantID, catalogID)
	require.NoError(t, err)
	require.NotNil(t, cat)

	require.NoError(t, skillRepo.CreateSkill(ctx, &types.TenantSkillEntity{
		ID: "sk-2", TenantID: tenantID, SandboxConfigID: "cfg-1", CatalogID: catalogID,
		Name: "acme__skill", Status: types.SkillStatusReady,
	}))
	skills.DiscardPluginCatalog(ctx, tenantID, catalogID)
	cat, err = skillRepo.GetCatalog(ctx, tenantID, catalogID)
	require.NoError(t, err)
	require.NotNil(t, cat, "a sandbox install is still using the catalog")

	require.NoError(t, skillRepo.DeleteSkill(ctx, tenantID, "cfg-1", "sk-2"))
	skills.deleteDiscardedCatalogIfIdle(ctx, tenantID, catalogID)
	cat, err = skillRepo.GetCatalog(ctx, tenantID, catalogID)
	require.NoError(t, err)
	assert.Nil(t, cat)
	install, err := pluginRepo.GetInstallByPluginName(ctx, tenantID, "acme")
	require.NoError(t, err)
	require.NotNil(t, install, "the skill path does not write the plugin ledger")
}

func TestReleaseMarketplaceDeletesOnlyThatRow(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	store := &memoryFileService{files: map[string][]byte{"mem://orphan": []byte("x"), "mem://other": []byte("y")}}
	svc := &PluginService{repo: repo, files: store}
	now := time.Now()
	require.NoError(t, repo.CreateMarketplace(ctx, &types.PluginMarketplaceEntity{
		ID: "mp-orphan", Name: "orphan",
		BundleRef: "mem://orphan", BundleSHA256: "sha-orphan", CreatedAt: now, UpdatedAt: now,
	}, nil))
	require.NoError(t, repo.CreateMarketplace(ctx, &types.PluginMarketplaceEntity{
		ID: "mp-other", Name: "other",
		BundleRef: "mem://other", BundleSHA256: "sha-other", CreatedAt: now, UpdatedAt: now,
	}, nil))

	svc.releaseMarketplace(ctx, "mp-orphan")
	orphan, err := repo.GetSharedMarketplace(ctx, "mp-orphan")
	require.NoError(t, err)
	assert.Nil(t, orphan)
	_, stillThere := store.files["mem://orphan"]
	assert.False(t, stillThere)
	other, err := repo.GetSharedMarketplace(ctx, "mp-other")
	require.NoError(t, err)
	assert.NotNil(t, other, "releasing one marketplace leaves the others in place")
	_, stillThere = store.files["mem://other"]
	assert.True(t, stillThere)
}

func TestInstallPluginWithSkillsNeedsASandbox(t *testing.T) {
	ctx := context.Background()
	const tenantID = 7
	svc, mcp, mpID := newAcmePluginService(t, tenantID)

	_, err := svc.InstallPlugin(ctx, tenantID, mpID, "sql-style", nil, nil)
	var appErr *apperrors.AppError
	require.True(t, errors.As(err, &appErr), "got %v", err)
	assert.Equal(t, apperrors.ErrBadRequest, appErr.Code)
	assert.Empty(t, mcp.created)
}

// newPluginSkillDB returns plugin and skill repositories over one database.
func newPluginSkillDB(t *testing.T) (interfaces.PluginRepository, repository.TenantSkillRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	ddl, err := os.ReadFile("../../../migrations/sqlite/000051_plugins.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	require.NoError(t, db.AutoMigrate(
		&types.TenantSkillEntity{}, &types.TenantSkillCatalogEntity{},
		&types.TenantSkillSnapshotEntity{}, &types.TenantUserEnvVar{},
	))
	return repository.NewPluginRepository(db), repository.NewTenantSkillRepository(db)
}

func TestMarketplaceBundlesAreStoredUnderTheFirstUploader(t *testing.T) {
	const tenantA, tenantB = uint64(7), uint64(8)
	svc, _, _ := newAcmePluginService(t, tenantA)
	store := svc.files.(*memoryFileService)
	require.NotEmpty(t, store.tenants)
	for _, tenantID := range store.tenants {
		assert.Equal(t, tenantA, tenantID)
	}
	archive := acmePluginsArchive(t)
	before := len(store.tenants)
	_, err := svc.AddMarketplaceFromArchive(context.Background(), tenantB, archive)
	require.NoError(t, err)
	assert.Len(t, store.tenants, before, "a later tenant reuses the first uploader's files")
}

func TestInstallFailureTearsDownCreatedResources(t *testing.T) {
	ctx := context.Background()
	const tenantID = 7
	repo, skillRepo := newPluginSkillDB(t)
	mcp := &fakePluginMCPService{lookup: repo}
	store := &memoryFileService{files: map[string][]byte{}}
	configs := &fakeSandboxConfigs{ids: []string{"cfg-a"}}
	svc := &PluginService{
		repo: repo, mcpSvc: mcp, mcpRepo: &pluginTestMCPRepo{}, files: store,
		skillRepo: skillRepo, configsRepo: configs,
		skillSvc: &TenantSkillService{configs: configs, skills: skillRepo},
	}
	archive := acmePluginsArchive(t)
	mp, err := svc.AddMarketplaceFromArchive(ctx, tenantID, archive)
	require.NoError(t, err)

	// neon creates its MCP services first, then fails to load its skills.
	pkg, err := repo.GetPackage(ctx, mp.ID, "neon")
	require.NoError(t, err)
	for _, skill := range pkg.Skills {
		delete(store.files, skill.BundleRef)
	}

	_, err = svc.InstallPlugin(ctx, tenantID, mp.ID, "neon", []string{"cfg-a"}, nil)
	require.Error(t, err)
	require.Len(t, mcp.created, 2)
	assert.ElementsMatch(t, []string{mcp.created[0].ID, mcp.created[1].ID}, mcp.unguardedDelete)

	install, err := repo.GetInstallByPluginName(ctx, tenantID, "neon")
	require.NoError(t, err)
	assert.Nil(t, install, "a failed install leaves nothing behind")
	bindings, err := repo.ListBindingsByKind(ctx, tenantID, types.PluginResourceKindMCPService)
	require.NoError(t, err)
	assert.Empty(t, bindings)
}

func TestSkillStateChangesDrivePluginStatus(t *testing.T) {
	ctx := context.Background()
	repo, skillRepo := newPluginSkillDB(t)
	const tenantID uint64 = 7
	installID := seedInstall(t, repo, tenantID, "mp-1", "acme", "cfg-a", "cfg-b")
	catalogID := uuid.NewString()
	require.NoError(t, skillRepo.CreateCatalog(ctx, &types.TenantSkillCatalogEntity{
		ID: catalogID, TenantID: tenantID, Name: "acme__skill",
	}))
	require.NoError(t, repo.CreateBinding(ctx, &types.PluginTenantBindingEntity{
		ID: uuid.NewString(), TenantID: tenantID, PluginInstallID: installID,
		Kind: types.PluginResourceKindSkillCatalog, ResourceID: catalogID, ResourceName: "acme__skill",
		CreatedAt: time.Now(),
	}))
	svc := &PluginService{repo: repo, skillRepo: skillRepo}
	settle := func() *types.PluginInstall {
		t.Helper()
		svc.HandleSkillStateChange(ctx, tenantID, catalogID)
		inst, err := repo.GetInstallByID(ctx, tenantID, installID)
		require.NoError(t, err)
		require.NotNil(t, inst)
		return inst
	}
	setSkill := func(cfgID string, mutate func(*types.TenantSkillEntity)) {
		t.Helper()
		row, err := skillRepo.GetSkill(ctx, tenantID, cfgID, "sk-"+cfgID)
		require.NoError(t, err)
		mutate(row)
		require.NoError(t, skillRepo.UpdateSkill(ctx, row))
	}
	for _, cfgID := range []string{"cfg-a", "cfg-b"} {
		require.NoError(t, skillRepo.CreateSkill(ctx, &types.TenantSkillEntity{
			ID: "sk-" + cfgID, TenantID: tenantID, SandboxConfigID: cfgID, CatalogID: catalogID,
			Name: "acme__skill", Status: types.SkillStatusInstalling,
		}))
	}

	inst := settle()
	assert.Equal(t, types.PluginStatusInstalling, inst.Status)

	setSkill("cfg-a", func(e *types.TenantSkillEntity) { e.Status = types.SkillStatusReady })
	setSkill("cfg-b", func(e *types.TenantSkillEntity) { e.Status = types.SkillStatusFailed; e.Error = "boom" })
	inst = settle()
	assert.Equal(t, types.PluginStatusFailed, inst.Status)
	assert.Equal(t, "boom", inst.Error)

	setSkill("cfg-b", func(e *types.TenantSkillEntity) { e.Status, e.Error = types.SkillStatusReady, "" })
	require.NoError(
		t,
		skillRepo.UpdateSkillEnvs(ctx, tenantID, "cfg-b", "sk-cfg-b", types.SkillEnvVars{{Name: "EXTRA"}}),
	)
	inst = settle()
	assert.Equal(t, types.PluginStatusReady, inst.Status)
	assert.Contains(t, inst.Error, "EXTRA", "undeclared variables are a warning on a ready plugin")

	require.NoError(t, skillRepo.UpdateSkillEnvs(ctx, tenantID, "cfg-b", "sk-cfg-b", nil))
	inst = settle()
	assert.Equal(t, types.PluginStatusReady, inst.Status)
	assert.Empty(t, inst.Error, "a ready plugin drops an old error")

	require.NoError(t, skillRepo.DeleteSkill(ctx, tenantID, "cfg-b", "sk-cfg-b"))
	inst = settle()
	assert.Equal(t, types.PluginStatusFailed, inst.Status)
	assert.Contains(t, inst.Error, "acme__skill@cfg-b", "a sandbox that lost the skill fails the plugin")
}

func TestPluginEnvsFromInstallsCopiesUserValues(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	now := time.Now()
	require.NoError(t, repo.CreateMarketplace(ctx, &types.PluginMarketplaceEntity{
		ID: "mp-env", Name: "env-market", BundleSHA256: "sha-env", CreatedAt: now, UpdatedAt: now,
	}, []*types.PluginPackageEntity{{
		ID: "pkg-env", Name: "acme", CreatedAt: now,
		EnvDeclarations: types.PluginEnvVars{{Name: "TOKEN", Required: true}, {Name: "REGION"}},
	}}))
	svc := &PluginService{repo: repo}

	envs, err := svc.pluginEnvsFromInstalls(ctx, &types.PluginInstall{PackageID: "pkg-env"},
		map[string][]*types.TenantSkillEntity{"cat": {{Envs: types.SkillEnvVars{
			{Name: "TOKEN", Source: SkillEnvVarSourcePlugin, Value: "tok"},
			{Name: "REGION", Value: "agent-declared"},
		}}}})
	require.NoError(t, err)
	require.Len(t, envs, 2)
	token, ok := envs.Get("TOKEN")
	require.True(t, ok)
	assert.Equal(t, "tok", token.Value)
	assert.Equal(t, SkillEnvVarSourcePlugin, token.Source)
	region, ok := envs.Get("REGION")
	require.True(t, ok)
	assert.Empty(t, region.Value, "only plugin-sourced values are copied")
}

func TestConcurrentInstallOfSameNameIsAConflict(t *testing.T) {
	ctx := context.Background()
	repo := newPluginTestRepo(t)
	const tenantID = 7
	seedInstall(t, repo, tenantID, "mp-1", "acme")
	err := repo.CreateInstall(ctx, &types.PluginInstall{
		ID: uuid.NewString(), TenantID: tenantID, MarketplaceID: "mp-1", PackageID: "pkg-mp-1-acme", PluginName: "acme",
		Status: types.PluginStatusInstalling, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	assert.ErrorIs(t, err, repository.ErrPluginInstallExists)
}
