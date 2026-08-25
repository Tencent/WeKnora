package service

import (
	"context"
	"errors"
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
	// The live image is another skill's generation: this install died before
	// it ever produced a snapshot of its own.
	fx.installed("sk-2", "snap-other", "")

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
	fx.installed("sk-1", "snap-live", "")
	fx.live("snap-live")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status,
		"the image still has the skill, so showing it as removed would be a lie")
	require.Nil(t, got.InstallingSince)
}

// A config accumulates generations, and only the skill being installed gets
// its row rewritten. Judging an older skill by whether its own snapshot is
// still the live one therefore condemns every skill but the most recent —
// here by deleting the row and bundle of files the image still carries.
func TestReapStuckRunsRestoresRemovalOfSkillInheritedByALaterSnapshot(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusRemoving,
		InstalledSnapshotID: "snap-1", BundleRef: "bundle-1",
		InstallingSince: &staleSince,
	})
	// A second skill was installed afterwards. Its snapshot grew from the one
	// carrying pdf, so pdf is still in the image the pointer now names.
	fx.installed("sk-1", "snap-1", "")
	fx.installed("sk-2", "snap-2", "snap-1")
	fx.live("snap-2")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.NotNil(t, got, "the files are still in the image; deleting the row would lose a live skill")
	require.Equal(t, types.SkillStatusReady, got.Status)
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
	// The removal got as far as snapshotting the image without the skill and
	// switching the pointer; only its bookkeeping never landed.
	fx.installed("sk-1", "snap-old", "")
	fx.removed("sk-1", "snap-new", "snap-old")
	fx.live("snap-new")

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
	fx.installed("sk-2", "snap-other", "")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Nil(t, fx.skills.rows["sk-1"],
		"a skill that never reached an image must not be marked ready")
}

// Deleting the row and its bundle is the only irreversible thing the reaper
// does, so a chain it cannot follow must be left for the next sweep.
func TestReapStuckRunsLeavesRemovalAloneWhenTheChainCannotBeFollowed(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusRemoving,
		InstalledSnapshotID: "snap-1", BundleRef: "bundle-1",
		InstallingSince: &staleSince,
	})
	fx.installed("sk-1", "snap-1", "")
	// The pointer names a generation the ledger does not describe, so whether
	// snap-1 is one of its ancestors is unknowable.
	fx.live("snap-missing")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, types.SkillStatusRemoving, fx.skills.mustGet("sk-1").Status,
		"an unreadable chain must not be resolved by deleting the row")
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
	fx.installed("sk-1", "snap-live", "")
	fx.live("snap-live")

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

// The install's last step is a row write that runs after the pointer has
// already switched. When the process dies in between, the skill is in the
// image every session boots while its row still says nothing about it.
func TestReapStuckRunsHealsInstallThatDiedAfterThePointerSwitched(t *testing.T) {
	fx := newReaperFixture(t)
	staleSince := fx.now.Add(-skillInstallStuckTTL - time.Minute)
	fx.skills.put(&types.TenantSkillEntity{
		ID: "sk-1", TenantID: 7, SandboxConfigID: "cfg-1",
		Name: "pdf", Status: types.SkillStatusInstalling, InstallingSince: &staleSince,
	})
	fx.installed("sk-2", "snap-old", "")
	fx.installed("sk-1", "snap-new", "snap-old")
	fx.live("snap-new")

	n, err := fx.svc.ReapStuckRuns(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	got := fx.skills.mustGet("sk-1")
	require.Equal(t, types.SkillStatusReady, got.Status,
		"failing this row would hide a skill from the agent that the image really carries")
	require.Empty(t, got.Error)
	require.Equal(t, "snap-new", got.InstalledSnapshotID,
		"the snapshot the install never got to record is recoverable from the ledger")
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

func TestPruneSupersededSnapshotsDeletesOldLedgerSnapshots(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	old := fx.now.Add(-2 * time.Hour)
	recent := fx.now.Add(-30 * time.Minute)
	fx.live("snap-live")
	fx.superseded("sk-1", "snap-old", "", old)
	fx.superseded("sk-2", "snap-recent", "snap-old", recent)
	fx.installed("sk-3", "snap-live", "snap-recent")
	fx.provider.listed = []sandbox.RemoteSnapshotRef{
		{ID: "snap-old"}, {ID: "snap-recent"}, {ID: "snap-live"}, {ID: "snap-foreign"},
	}

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-old"}, fx.provider.deleted,
		"only a superseded snapshot older than retention is a billed leftover")
	rows, err := fx.skills.ListSnapshotsByConfig(context.Background(), 7, "cfg-1")
	require.NoError(t, err)
	states := map[string]string{}
	for _, row := range rows {
		states[row.SnapshotID] = row.State
	}
	require.Equal(t, types.SkillSnapshotStateDeleted, states["snap-old"])
	require.Equal(t, types.SkillSnapshotStateSuperseded, states["snap-recent"],
		"a snapshot still inside the retention window may have live sandboxes")
	require.Equal(t, types.SkillSnapshotStateActive, states["snap-live"])
}

func TestPruneSupersededSnapshotsNeverDeletesTheLiveImage(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	old := fx.now.Add(-2 * time.Hour)
	fx.live("snap-live")
	fx.skills.snapshots = append(fx.skills.snapshots, &types.TenantSkillSnapshotEntity{
		ID: "row-wrong", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-1",
		SnapshotID: "snap-live", Trigger: types.SkillSnapshotTriggerInstall,
		State: types.SkillSnapshotStateSuperseded, SupersededAt: &old,
	})

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, fx.provider.deleted,
		"a ledger bug that marks the live snapshot superseded must not delete it")
}

func TestPruneSupersededSnapshotsNeverDeletesUnknownProviderSnapshots(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	fx.live("snap-live")
	fx.installed("sk-1", "snap-live", "")
	fx.provider.listed = []sandbox.RemoteSnapshotRef{
		{ID: "snap-live"}, {ID: "snap-foreign"},
	}

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, fx.provider.deleted,
		"a snapshot the ledger does not name belongs to another environment")
}

func TestPruneSupersededSnapshotsDeletesStaleActiveLeftovers(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	old := fx.now.Add(-2 * time.Hour)
	fx.live("snap-live")
	fx.installed("sk-2", "snap-live", "snap-old")
	fx.skills.snapshots = append(fx.skills.snapshots, &types.TenantSkillSnapshotEntity{
		ID: "row-snap-old", TenantID: 7, SandboxConfigID: "cfg-1", SkillID: "sk-1",
		SnapshotID: "snap-old", Trigger: types.SkillSnapshotTriggerInstall,
		State: types.SkillSnapshotStateActive, CreatedAt: old, UpdatedAt: old,
	})

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-old"}, fx.provider.deleted,
		"a pointer switch that never marked the previous row superseded still leaves a billed snapshot")
	rows, err := fx.skills.ListSnapshotsByConfig(context.Background(), 7, "cfg-1")
	require.NoError(t, err)
	states := map[string]string{}
	for _, row := range rows {
		states[row.SnapshotID] = row.State
	}
	require.Equal(t, types.SkillSnapshotStateDeleted, states["snap-old"])
	require.Equal(t, types.SkillSnapshotStateActive, states["snap-live"])
}

func TestPruneSupersededSnapshotsLeavesTheRowWhenDeleteFails(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	old := fx.now.Add(-2 * time.Hour)
	fx.live("snap-live")
	fx.superseded("sk-1", "snap-old", "", old)
	fx.provider.deleteErr = errors.New("provider down")

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Empty(t, fx.provider.deleted)
	rows, err := fx.skills.ListSnapshotsByConfig(context.Background(), 7, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, types.SkillSnapshotStateSuperseded, rows[0].State,
		"a failed provider delete must not be recorded as deleted")
}

func TestPruneSupersededSnapshotsTreatsMissingProviderSnapshotAsDeleted(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	old := fx.now.Add(-2 * time.Hour)
	fx.live("snap-live")
	fx.superseded("sk-1", "snap-old", "", old)
	fx.provider.deleteErr = &sandbox.RemoteError{
		Kind: sandbox.RemoteErrorKindNotFound, Op: "DeleteSnapshot",
	}

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	rows, err := fx.skills.ListSnapshotsByConfig(context.Background(), 7, "cfg-1")
	require.NoError(t, err)
	require.Equal(t, types.SkillSnapshotStateDeleted, rows[0].State,
		"a snapshot the provider already dropped is gone; the ledger must catch up")
}

func TestPruneSupersededSnapshotsHonoursALongerConfiguredSandboxTTL(t *testing.T) {
	fx := newReaperFixture(t)
	fx.svc.snapshotRetention = time.Hour
	fx.configs.entity.Config.E2B = &types.E2BSandboxConfig{
		E2BSandboxTTLSeconds: int((48 * time.Hour).Seconds()),
	}
	young := fx.now.Add(-25 * time.Hour)
	old := fx.now.Add(-50 * time.Hour)
	fx.live("snap-live")
	fx.superseded("sk-1", "snap-young", "", young)
	fx.superseded("sk-2", "snap-old", "snap-young", old)
	fx.installed("sk-3", "snap-live", "snap-old")

	n, err := fx.svc.PruneSupersededSnapshots(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-old"}, fx.provider.deleted,
		"a config whose sandbox TTL exceeds the floor must keep templates that young")
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

// installed and removed write the ledger row an install or a removal leaves
// behind: the generation that changed the image, and the one it grew from.
func (f *reaperFixture) installed(skillID, snapshotID, parentSnapshotID string) {
	f.snapshotRow(skillID, snapshotID, parentSnapshotID, types.SkillSnapshotTriggerInstall)
}

func (f *reaperFixture) removed(skillID, snapshotID, parentSnapshotID string) {
	f.snapshotRow(skillID, snapshotID, parentSnapshotID, types.SkillSnapshotTriggerRemove)
}

func (f *reaperFixture) snapshotRow(skillID, snapshotID, parentSnapshotID, trigger string) {
	f.skills.snapshots = append(f.skills.snapshots, &types.TenantSkillSnapshotEntity{
		ID: "row-" + snapshotID, TenantID: 7, SandboxConfigID: "cfg-1", SkillID: skillID,
		SnapshotID: snapshotID, ParentSnapshotID: parentSnapshotID,
		Trigger: trigger, State: types.SkillSnapshotStateActive,
	})
}

func (f *reaperFixture) superseded(skillID, snapshotID, parentSnapshotID string, at time.Time) {
	f.skills.snapshots = append(f.skills.snapshots, &types.TenantSkillSnapshotEntity{
		ID: "row-" + snapshotID, TenantID: 7, SandboxConfigID: "cfg-1", SkillID: skillID,
		SnapshotID: snapshotID, ParentSnapshotID: parentSnapshotID,
		Trigger: types.SkillSnapshotTriggerInstall, State: types.SkillSnapshotStateSuperseded,
		SupersededAt: &at,
	})
}

// live points the config at a snapshot, the way an install's pointer switch
// does.
func (f *reaperFixture) live(snapshotID string) {
	f.configs.entity.Config.SkillImage = &types.SkillImageConfig{SnapshotID: snapshotID}
}

var (
	_ skillReaperStore              = (*reaperSkillStore)(nil)
	_ skillSnapshotLedger           = (*reaperSkillStore)(nil)
	_ skillReaperConfigReader       = (*reaperConfigStore)(nil)
	_ sandboxConfigEnumerator       = (*reaperConfigStore)(nil)
	_ sandbox.TenantSandboxResolver = (*reaperSandboxResolver)(nil)
	_ skillSnapshotLister           = (*reaperSnapshotProvider)(nil)
	_ skillSnapshotDeleter          = (*reaperSnapshotProvider)(nil)
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

func (r *reaperSkillStore) ListStaleInstalling(
	_ context.Context, olderThan time.Time,
) ([]*types.TenantSkillEntity, error) {
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

func (r *reaperSkillStore) GetSkill(
	_ context.Context, tenantID uint64, configID, skillID string,
) (*types.TenantSkillEntity, error) {
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

func (r *reaperSkillStore) ListSnapshotsByConfig(
	_ context.Context, tenantID uint64, configID string,
) ([]*types.TenantSkillSnapshotEntity, error) {
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
	_ context.Context, tenantID uint64, id, state, snapshotID string,
) error {
	for _, e := range r.snapshots {
		if e == nil || e.ID != id || e.TenantID != tenantID {
			continue
		}
		e.State = state
		if snapshotID != "" {
			e.SnapshotID = snapshotID
		}
		if state == types.SkillSnapshotStateSuperseded {
			now := time.Now()
			e.SupersededAt = &now
		}
		return nil
	}
	return nil
}

func (r *reaperSkillStore) DeleteSnapshotRowsByConfig(context.Context, uint64, string) error {
	panic("DeleteSnapshotRowsByConfig is outside the reaper surface")
}

type reaperConfigStore struct {
	entity *types.TenantSandboxConfigEntity
}

func (r *reaperConfigStore) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
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
	deleteErr error
}

func (p *reaperSnapshotProvider) ListSnapshots(
	_ context.Context, sandboxID string,
) ([]sandbox.RemoteSnapshotRef, error) {
	p.listCalls = append(p.listCalls, sandboxID)
	return p.listed, nil
}

func (p *reaperSnapshotProvider) DeleteSnapshot(_ context.Context, snapshotID string) error {
	if p.deleteErr != nil {
		return p.deleteErr
	}
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
