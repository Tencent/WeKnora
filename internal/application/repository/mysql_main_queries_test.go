//go:build mysqlintegration

package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/testutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMySQLMainToolObservedRiskOnlyIncreases(t *testing.T) {
	db := testutil.MySQL(t)
	r := NewMCPToolApprovalRepository(db)
	ctx := context.Background()
	require.NoError(t, db.Create(&types.MCPService{ID: "svc", TenantID: 1, Name: "service", TransportType: types.MCPTransportSSE}).Error)
	destructive, read := "destructive", "read"
	require.NoError(t, r.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{Enabled: boolPtr(false)}))
	require.NoError(t, r.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{ObservedRisk: &destructive}))
	require.NoError(t, r.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{ObservedRisk: &read, RequireApproval: boolPtr(true)}))
	row, err := r.Get(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.Equal(t, destructive, row.ObservedRisk)
	require.False(t, row.Enabled)
	require.True(t, row.RequireApproval)
}

func TestMySQLMainChunksPreserveEnabledStateAndSequence(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	chunks := []*types.Chunk{
		{ID: "enabled", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc", Content: "enabled", IsEnabled: true},
		{ID: "disabled", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc", Content: "disabled", IsEnabled: false},
	}
	r := NewChunkRepository(db)
	require.NoError(t, r.CreateChunks(ctx, chunks))
	require.Positive(t, chunks[0].SeqID)
	require.Greater(t, chunks[1].SeqID, chunks[0].SeqID)
	for i, chunk := range chunks {
		stored, err := r.GetChunkBySeqID(ctx, 1, chunk.SeqID)
		require.NoError(t, err)
		require.Equal(t, chunk.ID, stored.ID)
		require.Equal(t, i == 0, stored.IsEnabled)
		require.Equal(t, stored.IsEnabled, chunk.IsEnabled)
		require.False(t, chunk.CreatedAt.IsZero())
	}
}

func TestMySQLMainKnowledgeMetadataLookups(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	r := &knowledgeRepository{db: db}
	for _, row := range []struct{ id, source, external string }{
		{"document", "source-a", "node_100%|attachment"},
		{"other-source", "source-b", "node_100%|attachment"},
		{"wildcard-decoy", "source-a", "nodeX100anything|attachment"},
	} {
		require.NoError(t, db.Exec(`INSERT INTO knowledges
			(id, tenant_id, knowledge_base_id, title, source, type, file_hash, file_type, parse_status, metadata)
			VALUES (?, 1, 'kb', 'document', 'sync', 'file', 'hash', 'pdf', 'completed', JSON_OBJECT('datasource_id', ?, 'external_id', ?))`,
			row.id, row.source, row.external).Error)
	}
	t.Run("exact", func(t *testing.T) {
		got, err := r.FindByMetadataKey(ctx, 1, "kb", "datasource_id", "source-b")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "other-source", got.ID)
		missing, err := r.FindByMetadataKey(ctx, 2, "kb", "datasource_id", "source-b")
		require.NoError(t, err)
		require.Nil(t, missing)
		missing, err = r.FindByMetadataKey(ctx, 1, "kb", `unknown"'key`, "source-b")
		require.NoError(t, err)
		require.Nil(t, missing)
	})
	t.Run("literal-prefix", func(t *testing.T) {
		got, err := r.FindByMetadataKeyPrefix(ctx, 1, "kb", "external_id", "node_100%|")
		require.NoError(t, err)
		require.Len(t, got, 2, "LIKE wildcards in external IDs must remain literal")
	})
	t.Run("source-isolation", func(t *testing.T) {
		got, err := r.FindByDataSourceExternalID(ctx, 1, "kb", "source-b", "node_100%|attachment")
		require.NoError(t, err)
		require.NotNil(t, got)
		require.Equal(t, "other-source", got.ID)
	})
	t.Run("file-dedup", func(t *testing.T) {
		params := &types.KnowledgeCheckParams{Type: "file", FileType: "pdf", FileHash: "hash", DataSourceID: "source-a", ExternalID: "node_100%|attachment"}
		exists, got, err := r.CheckKnowledgeExists(ctx, 1, "kb", params)
		require.NoError(t, err)
		require.True(t, exists)
		require.NotNil(t, got)
		require.Equal(t, "document", got.ID)
		params.DataSourceID = "missing"
		exists, got, err = r.CheckKnowledgeExists(ctx, 1, "kb", params)
		require.NoError(t, err)
		require.False(t, exists)
		require.Nil(t, got)
	})
}

func TestMySQLMainSyncLogRetentionPerSource(t *testing.T) {
	db := testutil.MySQL(t)
	now := time.Now().UTC()
	for _, source := range []struct {
		id      string
		days    int
		deleted bool
	}{{"short", 1, false}, {"long", 90, false}, {"default", 0, false}, {"deleted", 1, true}} {
		var deletedAt *time.Time
		if source.deleted {
			deletedAt = &now
		}
		require.NoError(t, db.Exec(`INSERT INTO data_sources
			(id, tenant_id, knowledge_base_id, name, type, sync_log_retention_days, deleted_at)
			VALUES (?, 1, 'kb', ?, 'feishu', ?, ?)`, source.id, source.id, source.days, deletedAt).Error)
	}
	var want []string
	for _, log := range []struct {
		id, source, status string
		tenant             uint64
		age, finishedAge   int
		keep               bool
	}{
		{"expired", "short", types.SyncLogStatusSuccess, 1, 2, 2, false},
		{"partial", "short", types.SyncLogStatusPartial, 1, 2, 2, false},
		{"canceled", "short", types.SyncLogStatusCanceled, 1, 2, 2, false},
		{"recent-finish", "short", types.SyncLogStatusSuccess, 1, 60, 0, true},
		{"running", "short", types.SyncLogStatusRunning, 1, 60, -1, true},
		{"retryable", "short", types.SyncLogStatusFailed, 1, 60, 60, true},
		{"long-retained", "long", types.SyncLogStatusSuccess, 1, 60, 60, true},
		{"default-expired", "default", types.SyncLogStatusSuccess, 1, 31, 31, false},
		{"deleted-failed", "deleted", types.SyncLogStatusFailed, 1, 2, 2, false},
		{"other-tenant", "short", types.SyncLogStatusSuccess, 2, 60, 60, true},
	} {
		var finishedAt *time.Time
		if log.finishedAge >= 0 {
			finished := now.AddDate(0, 0, -log.finishedAge)
			finishedAt = &finished
		}
		require.NoError(t, db.Exec(`INSERT INTO sync_logs
			(id, data_source_id, tenant_id, status, started_at, finished_at) VALUES (?, ?, ?, ?, ?, ?)`,
			log.id, log.source, log.tenant, log.status, now.AddDate(0, 0, -log.age), finishedAt).Error)
		if log.keep {
			want = append(want, log.id)
		}
	}
	for _, defaultDays := range []int{30, 0} {
		require.NoError(t, NewSyncLogRepository(db).CleanupOldLogs(context.Background(), defaultDays))
		var got []string
		require.NoError(t, db.Table("sync_logs").Pluck("id", &got).Error)
		require.ElementsMatch(t, want, got)
	}
}

func TestMySQLMainAssignmentDuplicateDoesNotReopenMachine(t *testing.T) {
	db := testutil.MySQL(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	machine := &types.SandboxUserMachine{MachineDigest: "machine", OwnerTenantID: 1, OwnerID: "owner", ConfigTenantID: 1, ConfigID: "config"}
	a := &types.SessionSandboxAssignment{SessionID: "session", ConfigTenantID: 1, ConfigID: "config", MachineDigest: machine.MachineDigest}
	require.NoError(t, sandboxTransaction(db, func(tx *gorm.DB) error {
		created, err := InsertSandboxAssignmentTx(tx, a, machine)
		require.NoError(t, err)
		require.True(t, created)
		return err
	}))
	require.NoError(t, db.Model(a).Update("released_at", at).Error)
	require.NoError(t, db.Model(machine).Update("empty_since", at).Error)
	require.NoError(t, sandboxTransaction(db, func(tx *gorm.DB) error {
		created, err := InsertSandboxAssignmentTx(tx, a, machine)
		require.NoError(t, err)
		require.False(t, created, "a duplicate assignment must not be reported as a fresh placement")
		return err
	}))
	var stored types.SandboxUserMachine
	require.NoError(t, db.First(&stored).Error)
	require.NotNil(t, stored.EmptySince)
	require.True(t, at.Equal(*stored.EmptySince))
}

func TestMySQLMainPromptCASAndGrantReplacement(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	r := NewAgentPromptRepository(db)
	until := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	p := &types.AgentPrompt{ID: "prompt", TenantID: 1, UserID: "user", SessionID: "session", Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptPending, OwnerInstance: "owner", LeaseUntil: &until}
	require.NoError(t, r.Create(ctx, p))
	var wg sync.WaitGroup
	wins := make(chan bool, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := r.Transition(ctx, p.ID, types.AgentPromptTransition{From: []string{types.AgentPromptPending}, To: types.AgentPromptAnswered, Response: types.JSON(`{"allowed":true}`)})
			wins <- ok
			errs <- err
		}()
	}
	wg.Wait()
	count := 0
	for range 2 {
		require.NoError(t, <-errs)
		if <-wins {
			count++
		}
	}
	require.Equal(t, 1, count)
	ok, err := r.RenewLease(ctx, p.ID, "other-owner", until)
	require.NoError(t, err)
	require.False(t, ok)
	suspensions := NewAgentRunSuspensionRepository(db)
	snapshot := &types.AgentRunSuspension{AssistantMessageID: "message", TenantID: 1, SessionID: "session", PromptID: p.ID, SnapshotVersion: 1, Snapshot: types.JSON(`{"step":1}`)}
	require.NoError(t, suspensions.Save(ctx, snapshot))
	snapshot.PromptID = "new-prompt"
	snapshot.Snapshot = types.JSON(`{"step":2}`)
	require.NoError(t, suspensions.Save(ctx, snapshot))
	require.NoError(t, suspensions.Delete(ctx, "message", p.ID))
	got, err := suspensions.Get(ctx, "message")
	require.NoError(t, err)
	require.Equal(t, "new-prompt", got.PromptID)
	grants := NewAgentGrantRepository(db)
	grant := &types.AgentGrant{TenantID: 1, UserID: "user", Scope: types.AgentGrantScopeSession, ScopeID: "session", GrantKey: "tool", BindingHash: "first"}
	require.NoError(t, grants.SaveGrant(ctx, grant))
	ok, err = grants.RevokeGrant(ctx, 1, "user", grant.ID, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	replacement := &types.AgentGrant{TenantID: 1, UserID: "user", Scope: grant.Scope, ScopeID: grant.ScopeID, GrantKey: grant.GrantKey, BindingHash: "second"}
	require.NoError(t, grants.SaveGrant(ctx, replacement))
	stored, err := grants.FindGrant(ctx, 1, "user", types.GrantScope{Scope: grant.Scope, ScopeID: grant.ScopeID}, grant.GrantKey)
	require.NoError(t, err)
	require.Equal(t, "second", stored.BindingHash)
	require.Nil(t, stored.RevokedAt)
	missing, err := grants.FindGrant(ctx, 2, "user", types.GrantScope{Scope: grant.Scope, ScopeID: grant.ScopeID}, grant.GrantKey)
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestMySQLMainDataSourceConfigCompareAndSet(t *testing.T) {
	db := testutil.MySQL(t)
	ctx := context.Background()
	r := NewDataSourceRepository(db)
	config := types.JSON(`{"selection":["old"],"credentials":{"token":"secret"}}`)
	ds := &types.DataSource{ID: "source", TenantID: 1, KnowledgeBaseID: "kb", Name: "source", Type: types.ConnectorTypeFeishu, Config: config}
	require.NoError(t, r.Create(ctx, ds))
	loaded, err := r.FindByID(ctx, ds.ID)
	require.NoError(t, err)
	expected := loaded.Config
	loaded.Config = types.JSON(`{"selection":["new"],"credentials":{"token":"secret"}}`)
	require.NoError(t, r.PatchSyncSettings(ctx, loaded, expected, []string{"config"}))
	loaded.Config = types.JSON(`{"selection":["stale"]}`)
	require.ErrorIs(t, r.PatchSyncSettings(ctx, loaded, expected, []string{"config"}), types.ErrDataSourceSettingsConflict)
	stored, err := r.FindByID(ctx, ds.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"selection":["new"],"credentials":{"token":"secret"}}`, string(stored.Config))
}

func TestMySQLMainOAuthRefreshLeaseSingleOwner(t *testing.T) {
	db := testutil.MySQL(t)
	r := NewMCPOAuthRepository(db)
	ctx := context.Background()
	require.NoError(t, db.Create(&types.MCPService{ID: "service", TenantID: 1, Name: "service", TransportType: types.MCPTransportSSE}).Error)
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user"}
	until := time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	require.NoError(t, r.SaveTokenForPrincipal(ctx, &types.MCPOAuthToken{TenantID: 1, PrincipalType: principal.Type, PrincipalID: principal.ID, ServiceID: "service", AccessToken: "access", ExpiresAt: until}))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wins := make(chan bool, 2)
	for _, lease := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := r.TryAcquireTokenRefreshLease(ctx, 1, principal, "service", lease, until)
			errs <- err
			wins <- ok
		}()
	}
	wg.Wait()
	count := 0
	for range 2 {
		require.NoError(t, <-errs)
		if <-wins {
			count++
		}
	}
	require.Equal(t, 1, count)
	stored, err := r.GetTokenForPrincipal(ctx, 1, principal, "service")
	require.NoError(t, err)
	require.NoError(t, r.ReleaseTokenRefreshLease(ctx, 1, principal, "service", "stale-owner"))
	ok, err := r.TryAcquireTokenRefreshLease(ctx, 1, principal, "service", "third", until)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, r.ReleaseTokenRefreshLease(ctx, 1, principal, "service", stored.RefreshLeaseID))
	ok, err = r.TryAcquireTokenRefreshLease(ctx, 1, principal, "service", "third", until)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestMySQLMainOAuthTokenWithoutExpiry(t *testing.T) {
	db := testutil.MySQL(t)
	r := NewMCPOAuthRepository(db)
	ctx := context.Background()
	require.NoError(t, db.Create(&types.MCPService{ID: "service", TenantID: 1, Name: "service", TransportType: types.MCPTransportSSE}).Error)
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user"}
	token := &types.MCPOAuthToken{TenantID: 1, PrincipalType: principal.Type, PrincipalID: principal.ID, ServiceID: "service", AccessToken: "no-expiry"}
	require.NoError(t, r.SaveTokenForPrincipal(ctx, token))
	stored, err := r.GetTokenForPrincipal(ctx, 1, principal, "service")
	require.NoError(t, err)
	require.True(t, stored.ExpiresAt.IsZero())
	token.ExpiresAt = time.Now().UTC().Add(time.Minute).Truncate(time.Microsecond)
	require.NoError(t, r.SaveTokenForPrincipal(ctx, token))
	token.ExpiresAt = time.Time{}
	token.AccessToken = "new-no-expiry"
	require.NoError(t, r.SaveTokenForPrincipal(ctx, token))
	stored, err = r.GetTokenForPrincipal(ctx, 1, principal, "service")
	require.NoError(t, err)
	require.True(t, stored.ExpiresAt.IsZero())
	require.Equal(t, token.AccessToken, stored.AccessToken)
}

func TestMySQLMainConcurrentFirstMachineAssignments(t *testing.T) {
	db := testutil.MySQL(t)
	var wg sync.WaitGroup
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- sandboxTransaction(db, func(tx *gorm.DB) error {
				// Both transactions may observe an unregistered machine before
				// either joins it. This is the first-use placement interleaving.
				if err := lockMachineRow(tx, "shared-machine"); err != nil {
					return err
				}
				ready <- struct{}{}
				<-start
				a := &types.SessionSandboxAssignment{SessionID: fmt.Sprintf("session-%d", i), ConfigTenantID: 1, ConfigID: "config", MachineDigest: "shared-machine"}
				m := &types.SandboxUserMachine{MachineDigest: a.MachineDigest, OwnerTenantID: 1, OwnerID: "owner", ConfigTenantID: 1, ConfigID: a.ConfigID}
				_, err := InsertSandboxAssignmentTx(tx, a, m)
				return err
			})
		}()
	}
	for range 2 {
		<-ready
	}
	close(start)
	wg.Wait()
	for range 2 {
		require.NoError(t, <-errs)
	}
	var count int64
	require.NoError(t, db.Model(&types.SessionSandboxAssignment{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestMySQLMainSandboxTransactionsUseFreshReadsLocally(t *testing.T) {
	db := testutil.MySQL(t)
	var before, after string
	require.NoError(t, db.Raw("SELECT @@transaction_isolation").Scan(&before).Error)
	require.Equal(t, "REPEATABLE-READ", before)
	require.NoError(t, sandboxTransaction(db, func(tx *gorm.DB) error {
		n, err := countLive(tx, "fresh-machine")
		require.NoError(t, err)
		require.Zero(t, n)
		require.NoError(t, db.Create(&types.SessionSandboxAssignment{SessionID: "fresh", MachineDigest: "fresh-machine", ConfigID: "config"}).Error)
		n, err = countLive(tx, "fresh-machine")
		require.NoError(t, err)
		require.EqualValues(t, 1, n, "sandbox counts must see commits after their initial read")
		return nil
	}))
	require.NoError(t, db.Raw("SELECT @@transaction_isolation").Scan(&after).Error)
	require.Equal(t, before, after, "sandbox isolation must not change the application connection default")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		n, err := countLive(tx, "snapshot-machine")
		require.NoError(t, err)
		require.Zero(t, n)
		require.NoError(t, db.Create(&types.SessionSandboxAssignment{SessionID: "snapshot", MachineDigest: "snapshot-machine", ConfigID: "config"}).Error)
		n, err = countLive(tx, "snapshot-machine")
		require.NoError(t, err)
		require.Zero(t, n, "unrelated transactions retain their repeatable-read snapshot")
		return nil
	}))
}

func TestMySQLMainClaimPlacementConcurrentFirstMachine(t *testing.T) {
	db := testutil.MySQL(t)
	r := NewSandboxAllocationRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, id := range []string{"session-1", "session-2"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{ID: id, TenantID: 1, UserID: "owner"}).Error)
	}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("mysql_first_machine_barrier", func(tx *gorm.DB) {
		if tx.Statement.Table == "sandbox_user_machines" && tx.Error == gorm.ErrRecordNotFound {
			ready <- struct{}{}
			<-start
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("mysql_first_machine_barrier")) })
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"session-1", "session-2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := &types.SessionSandboxAssignment{SessionID: id, ConfigTenantID: 1, ConfigID: "config", MachineDigest: "shared-machine"}
			m := &types.SandboxUserMachine{MachineDigest: a.MachineDigest, OwnerTenantID: 1, OwnerID: "owner", ConfigTenantID: 1, ConfigID: a.ConfigID}
			_, err := r.ClaimPlacement(ctx, a, m, interfaces.PlacementPin{})
			errs <- err
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			close(start)
			wg.Wait()
			t.Fatal("concurrent placements did not reach the machine lock: ", ctx.Err())
		}
	}
	close(start)
	wg.Wait()
	for range 2 {
		require.NoError(t, <-errs)
	}
	live, err := r.CountLiveAssignments(ctx, "shared-machine")
	require.NoError(t, err)
	require.EqualValues(t, 2, live)
}

func TestMySQLMainRetireSeesPlacementCommittedBeforeMachineLock(t *testing.T) {
	db := testutil.MySQL(t)
	r := NewSandboxAllocationRepository(db)
	ctx := context.Background()
	for _, id := range []string{"retiring-session", "joining-session"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{ID: id, TenantID: 1, UserID: "owner"}).Error)
	}
	place := func(id string) error {
		a := &types.SessionSandboxAssignment{SessionID: id, ConfigTenantID: 1, ConfigID: "config", MachineDigest: "shared-machine"}
		m := &types.SandboxUserMachine{MachineDigest: a.MachineDigest, OwnerTenantID: 1, OwnerID: "owner", ConfigTenantID: 1, ConfigID: a.ConfigID}
		_, err := r.ClaimPlacement(ctx, a, m, interfaces.PlacementPin{})
		return err
	}
	require.NoError(t, place("retiring-session"))
	var joined atomic.Bool
	var joinErr error
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("mysql_join_before_retire_lock", func(tx *gorm.DB) {
		if tx.Statement.Table == "sandbox_user_machines" && joined.CompareAndSwap(false, true) {
			// Retirement already read its assignment; another session joins
			// and commits before retirement acquires the shared-machine lock.
			joinErr = place("joining-session")
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove("mysql_join_before_retire_lock")) })
	retired, err := r.RetireSessions(ctx, []string{"retiring-session"}, time.Now())
	require.NoError(t, err)
	require.NoError(t, joinErr)
	require.True(t, joined.Load())
	require.Len(t, retired, 1)
	require.False(t, retired[0].MachineEmpty, "a pre-lock snapshot must not hide a newly committed placement")
	machine, err := r.GetUserMachine(ctx, "shared-machine")
	require.NoError(t, err)
	require.Nil(t, machine.EmptySince)
}
