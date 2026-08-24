package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestReapStuckRunsFailsAbandonedInstalls(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusInstalling, InstallingSince: &staleSince,
	})

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusFailed, got.Status,
		"a row left installing after the process died must stop spinning in the UI")
	require.Contains(t, got.Error, "安装进程中断")
	require.Contains(t, strings.ToLower(got.Error), "process died")
	require.Nil(t, got.InstallingSince)
}

func TestReapStuckRunsRestoresAbandonedRemovals(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusRemoving,
		InstalledSnapshotID: "snap-live", InstallingSince: &staleSince,
	})
	fx.configs.entity.Config.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-live"}

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status,
		"the image still has the skill, so showing it as removed would be a lie")
	require.Nil(t, got.InstallingSince)
}

func TestReapStuckRunsDeletesAbandonedRemovalAfterPointerMoved(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusRemoving,
		InstalledSnapshotID: "snap-old", BundleRef: "bundle-1",
		InstallingSince: &staleSince,
	})
	fx.configs.entity.Config.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-new"}

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Nil(t, fx.skills.rows["sk-1"],
		"the pointer already left this skill behind; restoring ready would offer files the image no longer has")
}

func TestReapStuckRunsDeletesAbandonedRemovalThatNeverReachedAnImage(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusRemoving, InstallingSince: &staleSince,
	})

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Nil(t, fx.skills.rows["sk-1"],
		"a skill that never reached an image must not be marked ready")
}

func TestReapStuckRunsIgnoresFreshRuns(t *testing.T) {
	fx := newReaperFixture(t)
	freshSince := fx.now.Add(-time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusInstalling, InstallingSince: &freshSince,
	})

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Zero(t, n, "a run started a minute ago must not be killed")
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusInstalling, got.Status)
}

func TestReapStuckRunsHealsInstallingRowWhoseSnapshotIsStillLive(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusInstalling,
		InstalledSnapshotID: "snap-live", InstallingSince: &staleSince,
	})
	fx.configs.entity.Config.SkillImage = &types.SkillImageConfig{SnapshotID: "snap-live"}

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n,
		"a re-install that died before the pointer moved must become ready again")
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status)
	require.Empty(t, got.Error)
	require.Nil(t, got.InstallingSince)
	require.Equal(t, "snap-live", got.InstalledSnapshotID)
}

func TestReconcileSnapshotsWarnsExtrasWithoutDeleting(t *testing.T) {
	fx := newReaperFixture(t)
	require.NoError(t, fx.skills.CreateSnapshotRow(context.Background(), &types.TenantSkillSnapshotEntity{
		ID: "row-1", TenantID: 7, SandboxConfigID: "cfg-1",
		SnapshotID: "snap-ledger", State: types.SkillSnapshotStateActive,
	}))
	fx.provider.listed = []sandbox.RemoteSnapshotRef{
		{ID: "snap-ledger"},
		{ID: "snap-orphan"},
	}

	n, err := fx.svc.ReconcileSnapshots(context.Background(), 7, "cfg-1")

	require.NoError(t, err)
	require.Equal(t, 1, n, "the provider snapshot missing from the ledger is the extra")
	require.Equal(t, []string{""}, fx.provider.listCalls,
		"an empty sandboxID lists the whole account so extras from other environments are visible")
	require.Empty(t, fx.provider.deleted,
		"extras are only warned; the same provider account may be shared across environments")
}

func TestTenantSkillServiceStartIsIdempotent(t *testing.T) {
	svc := NewTenantSkillService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	require.NoError(t, svc.Start(context.Background()))
	require.NoError(t, svc.Start(context.Background()),
		"repeated Start must be a no-op so container wiring does not coordinate ordering")
	svc.Stop()
	svc.Stop()
}

type reaperFixture struct {
	svc      *TenantSkillService
	skills   *reaperSkillStore
	configs  *reaperConfigStore
	provider *reaperSnapshotProvider
	now      time.Time
}

func newReaperFixture(t *testing.T) *reaperFixture {
	t.Helper()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	skills := &reaperSkillStore{rows: map[string]*types.TenantSkillEntity{}}
	configs := &reaperConfigStore{
		entity: &types.TenantSandboxConfigEntity{
			ID: "cfg-1", TenantID: 7,
			Config: &types.TenantSandboxConfig{
				SkillImage: &types.SkillImageConfig{SnapshotID: "snap-other"},
			},
		},
	}
	provider := &reaperSnapshotProvider{}
	svc := NewTenantSkillService(
		skills, configs, nil, &reaperSandboxResolver{provider: provider},
		nil, nil, nil, nil, nil, nil, nil, nil,
	)
	svc.now = func() time.Time { return now }
	return &reaperFixture{svc: svc, skills: skills, configs: configs, provider: provider, now: now}
}

var (
	_ skillReaperStore              = (*reaperSkillStore)(nil)
	_ skillSnapshotLedger           = (*reaperSkillStore)(nil)
	_ skillReaperConfigReader       = (*reaperConfigStore)(nil)
	_ sandboxConfigEnumerator       = (*reaperConfigStore)(nil)
	_ sandbox.TenantSandboxResolver = (*reaperSandboxResolver)(nil)
	_ skillSnapshotLister           = (*reaperSnapshotProvider)(nil)
)

type reaperSkillStore struct {
	rows      map[string]*types.TenantSkillEntity
	snapshots []*types.TenantSkillSnapshotEntity
}

func (r *reaperSkillStore) put(e *types.TenantSkillEntity) {
	cp := *e
	r.rows[e.ID] = &cp
}

func (r *reaperSkillStore) mustGet(id string) *types.TenantSkillEntity {
	return r.rows[id]
}

func (r *reaperSkillStore) ListStaleInstalling(_ context.Context, olderThan time.Time) ([]*types.TenantSkillEntity, error) {
	var out []*types.TenantSkillEntity
	for _, e := range r.rows {
		if e.InstallingSince == nil || !e.InstallingSince.Before(olderThan) {
			continue
		}
		if e.Status != types.SkillStatusInstalling && e.Status != types.SkillStatusRemoving {
			continue
		}
		cp := *e
		out = append(out, &cp)
	}
	return out, nil
}

func (r *reaperSkillStore) GetSkill(_ context.Context, tenantID uint64, configID, skillID string) (*types.TenantSkillEntity, error) {
	e := r.rows[skillID]
	if e == nil || e.TenantID != tenantID || e.SandboxConfigID != configID {
		return nil, nil
	}
	cp := *e
	return &cp, nil
}

func (r *reaperSkillStore) UpdateSkill(_ context.Context, e *types.TenantSkillEntity) error {
	cp := *e
	r.rows[e.ID] = &cp
	return nil
}

func (r *reaperSkillStore) ListSnapshotsByConfig(_ context.Context, tenantID uint64, configID string) ([]*types.TenantSkillSnapshotEntity, error) {
	var out []*types.TenantSkillSnapshotEntity
	for _, e := range r.snapshots {
		if e.TenantID == tenantID && e.SandboxConfigID == configID {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (r *reaperSkillStore) CreateSkill(context.Context, *types.TenantSkillEntity) error {
	panic("CreateSkill is outside the reaper surface")
}

func (r *reaperSkillStore) GetSkillByName(context.Context, uint64, string, string) (*types.TenantSkillEntity, error) {
	panic("GetSkillByName is outside the reaper surface")
}

func (r *reaperSkillStore) ListSkillsByConfig(context.Context, uint64, string) ([]*types.TenantSkillEntity, error) {
	panic("ListSkillsByConfig is outside the reaper surface")
}

func (r *reaperSkillStore) DeleteSkill(_ context.Context, tenantID uint64, configID, skillID string) error {
	e := r.rows[skillID]
	if e == nil || e.TenantID != tenantID || e.SandboxConfigID != configID {
		return nil
	}
	delete(r.rows, skillID)
	return nil
}

func (r *reaperSkillStore) CreateSnapshotRow(_ context.Context, e *types.TenantSkillSnapshotEntity) error {
	cp := *e
	r.snapshots = append(r.snapshots, &cp)
	return nil
}

func (r *reaperSkillStore) MarkSnapshotState(
	context.Context, uint64, string, string, string,
) error {
	panic("MarkSnapshotState is outside the reaper surface")
}

func (r *reaperSkillStore) DeleteSnapshotRowsByConfig(context.Context, uint64, string) error {
	panic("DeleteSnapshotRowsByConfig is outside the reaper surface")
}

type reaperConfigStore struct {
	entity *types.TenantSandboxConfigEntity
}

func (r *reaperConfigStore) GetByID(_ context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error) {
	if r.entity == nil || r.entity.TenantID != tenantID || r.entity.ID != id {
		return nil, nil
	}
	cp := *r.entity
	return &cp, nil
}

func (r *reaperConfigStore) ListAll(_ context.Context) ([]*types.TenantSandboxConfigEntity, error) {
	if r.entity == nil {
		return nil, nil
	}
	cp := *r.entity
	return []*types.TenantSandboxConfigEntity{&cp}, nil
}

func (r *reaperConfigStore) Create(context.Context, *types.TenantSandboxConfigEntity) error {
	panic("Create is outside the reaper surface")
}

func (r *reaperConfigStore) ListByTenant(context.Context, uint64) ([]*types.TenantSandboxConfigEntity, error) {
	panic("ListByTenant is outside the reaper surface")
}

func (r *reaperConfigStore) Update(context.Context, *types.TenantSandboxConfigEntity) error {
	panic("Update is outside the reaper surface")
}

func (r *reaperConfigStore) SoftDelete(context.Context, uint64, string) error {
	panic("SoftDelete is outside the reaper surface")
}

func (r *reaperConfigStore) SetCordon(context.Context, uint64, string, time.Time) error {
	panic("SetCordon is outside the reaper surface")
}

func (r *reaperConfigStore) ClearCordon(context.Context, uint64, string) error {
	panic("ClearCordon is outside the reaper surface")
}

type reaperSandboxResolver struct {
	provider *reaperSnapshotProvider
}

func (r *reaperSandboxResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return r.provider, nil
}

// reaperSnapshotProvider is a Manager that can list and delete snapshots so a
// test can prove ReconcileSnapshots never type-asserts its way into a delete.
type reaperSnapshotProvider struct {
	listed    []sandbox.RemoteSnapshotRef
	listCalls []string
	deleted   []string
}

func (p *reaperSnapshotProvider) ListSnapshots(_ context.Context, sandboxID string) ([]sandbox.RemoteSnapshotRef, error) {
	p.listCalls = append(p.listCalls, sandboxID)
	return p.listed, nil
}

func (p *reaperSnapshotProvider) DeleteSnapshot(_ context.Context, snapshotID string) error {
	p.deleted = append(p.deleted, snapshotID)
	return nil
}

func (p *reaperSnapshotProvider) CreateSnapshot(context.Context, string, string) (sandbox.RemoteSnapshotRef, error) {
	panic("CreateSnapshot is outside the reaper surface")
}

func (p *reaperSnapshotProvider) Execute(context.Context, *sandbox.ExecuteConfig) (*sandbox.ExecuteResult, error) {
	return &sandbox.ExecuteResult{ExitCode: 0}, nil
}
func (p *reaperSnapshotProvider) Cleanup(context.Context) error { return nil }
func (p *reaperSnapshotProvider) GetSandbox() sandbox.Sandbox   { return nil }
func (p *reaperSnapshotProvider) GetType() sandbox.SandboxType  { return sandbox.SandboxTypeE2B }
