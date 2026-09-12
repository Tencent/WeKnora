package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type workbenchTestSessions struct {
	interfaces.SessionService
	db *gorm.DB
}

func (s workbenchTestSessions) GetOwnedSession(ctx context.Context, id string) (*types.Session, error) {
	tid, _ := types.TenantIDFromContext(ctx)
	uid, _ := types.UserIDFromContext(ctx)
	return repository.NewSessionRepository(s.db).Get(ctx, tid, uid, id)
}

func (s workbenchTestSessions) GetSession(context.Context, string) (*types.Session, error) {
	panic("workbench must never use admin read fallback")
}

type workbenchTestPolicy struct {
	disabled bool
	err      error
}

func (p *workbenchTestPolicy) WorkspaceScriptsDisabled(context.Context, uint64) (bool, error) {
	return p.disabled, p.err
}

type workbenchTestConfigs struct {
	repository.TenantSandboxConfigRepository
}

func (workbenchTestConfigs) GetByID(
	_ context.Context, tid uint64, id string,
) (*types.TenantSandboxConfigEntity, error) {
	if tid != 7 || (id != "config" && id != "second") {
		return nil, nil
	}
	return &types.TenantSandboxConfigEntity{ID: id, TenantID: tid, SandboxType: "docker"}, nil
}

type workbenchTestResolver struct {
	sandbox.TenantSandboxResolver
	manager sandbox.Manager
}

func (r workbenchTestResolver) Resolve(context.Context, uint64, string) (sandbox.Manager, error) {
	return r.manager, nil
}

type workbenchTestAudit struct {
	interfaces.AuditLogRepository
	mu     sync.Mutex
	rows   []*types.AuditLog
	failAt int
}

func (a *workbenchTestAudit) Create(_ context.Context, row *types.AuditLog) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failAt == len(a.rows)+1 {
		return errors.New("audit unavailable")
	}
	a.rows = append(a.rows, row)
	return nil
}

func (a *workbenchTestAudit) List(_ context.Context, _ uint64, q *interfaces.AuditLogQuery) ([]*types.AuditLog, error) {
	if q.Limit > 100 {
		panic("unbounded audit")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]*types.AuditLog(nil), a.rows...), nil
}

type workbenchTestManager struct {
	sandbox.Manager
	audit                  *workbenchTestAudit
	opened, files, ensured int
	request                sandbox.CommandTerminalRequest
	openErr, fileErr       error
}

func (m *workbenchTestManager) GetType() sandbox.SandboxType { return sandbox.SandboxTypeDocker }
func (m *workbenchTestManager) EnsureWorkbenchSession(context.Context, string) error {
	m.assertAudited()
	m.ensured++
	return nil
}

func (m *workbenchTestManager) assertAudited() {
	if len(m.audit.rows) == 0 || m.audit.rows[len(m.audit.rows)-1].Outcome != types.AuditOutcomeAccepted {
		panic("operation before durable intent")
	}
}

func (m *workbenchTestManager) OpenSessionCommandTerminal(
	_ context.Context, _ string, r sandbox.CommandTerminalRequest,
) (sandbox.CommandTerminal, error) {
	m.assertAudited()
	m.opened++
	m.request = r
	if m.openErr != nil {
		return nil, m.openErr
	}
	return workbenchTestTerminal{Reader: strings.NewReader("early\n")}, nil
}

func (m *workbenchTestManager) WorkbenchFiles(
	_ context.Context, _ string, r sandbox.WorkbenchFileRequest,
) (*sandbox.WorkbenchFileResult, error) {
	if r.Operation != "list" && r.Operation != "read" {
		m.assertAudited()
	}
	m.files++
	return &sandbox.WorkbenchFileResult{Path: r.Path, Entries: []sandbox.WorkbenchFileEntry{}}, m.fileErr
}

type workbenchTestTerminal struct{ io.Reader }

func (workbenchTestTerminal) Close() error                                 { return nil }
func (workbenchTestTerminal) Input(context.Context, []byte) error          { return nil }
func (workbenchTestTerminal) Resize(context.Context, uint16, uint16) error { return nil }
func (workbenchTestTerminal) Interrupt(context.Context) error              { return nil }
func (workbenchTestTerminal) Wait(context.Context) (sandbox.CommandTerminalExit, error) {
	return sandbox.CommandTerminalExit{ExitCode: 0, Reason: "exited"}, nil
}

func newWorkbenchFixture(
	t *testing.T,
) (*WorkbenchService, context.Context, *workbenchTestManager, *workbenchTestAudit, *workbenchTestPolicy) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, sql := range []string{
		"CREATE TABLE users (id text PRIMARY KEY, is_active boolean, deleted_at datetime)",
		"CREATE TABLE tenants (id integer PRIMARY KEY, status text, deleted_at datetime)",
		"CREATE TABLE tenant_members (user_id text, tenant_id integer, status text, deleted_at datetime)",
		"CREATE TABLE im_channel_sessions (session_id text)",
		"CREATE TABLE auth_tokens (id text PRIMARY KEY, user_id text, token text, token_type text, " +
			"expires_at datetime, is_revoked boolean, created_at datetime, updated_at datetime)",
		"INSERT INTO users VALUES ('alice', true, NULL), ('bob', true, NULL)",
		"INSERT INTO tenants VALUES (7, 'active', NULL), (8, 'active', NULL)",
		"INSERT INTO tenant_members VALUES " +
			"('alice', 7, 'active', NULL), ('bob', 7, 'active', NULL), ('alice', 8, 'active', NULL)",
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	require.NoError(t, db.AutoMigrate(&types.Session{}))
	for _, row := range []map[string]any{
		{"id": "session", "tenant_id": 7, "user_id": "alice", "sandbox_config_id": "config"},
		{"id": "bob-session", "tenant_id": 7, "user_id": "bob"},
		{"id": "foreign", "tenant_id": 8, "user_id": "alice"},
		{"id": "api", "tenant_id": 7, "user_id": "api_tenant_key:7:1"},
	} {
		require.NoError(t, db.Model(&types.Session{}).Create(row).Error)
	}
	audit := &workbenchTestAudit{}
	tokens := repository.NewAuthTokenRepository(db)
	require.NoError(t, tokens.CreateToken(context.Background(), &types.AuthToken{
		ID: "access", UserID: "alice", Token: "workbench-test-access", TokenType: "access_token",
		ExpiresAt: time.Now().Add(time.Hour),
	}))
	manager := &workbenchTestManager{audit: audit}
	policy := &workbenchTestPolicy{}
	s := NewWorkbenchService(WorkbenchServiceDeps{
		Authorization: repository.NewWorkbenchAuthorizationRepository(db),
		Sessions:      workbenchTestSessions{db: db}, Policy: policy, Pinner: NewSessionSandboxPinner(db),
		Users:    &userService{tokenRepo: tokens},
		Resolver: workbenchTestResolver{manager: manager}, Configs: workbenchTestConfigs{}, Audit: audit,
	})
	s.enabled = true
	s.store = newMemoryWorkbenchStore()
	ctx := (WorkbenchIdentity{TenantID: 7, UserID: "alice", SessionID: "session"}).Context(context.Background())
	return s, ctx, manager, audit, policy
}

func TestWorkbenchAuthorization(t *testing.T) {
	s, ctx, _, _, policy := newWorkbenchFixture(t)
	db := s.deps.Sessions.(workbenchTestSessions).db
	_, err := s.Status(ctx, "session")
	require.NoError(t, err)
	for _, id := range []string{"bob-session", "foreign", "api", "missing"} {
		_, err := s.Status(ctx, id)
		require.ErrorIs(t, err, ErrWorkbenchSession)
	}
	for _, kind := range []string{
		types.PrincipalAPITenant, types.PrincipalIMUser, types.PrincipalEmbedSession, types.PrincipalAPIPlatform,
	} {
		_, err := s.Status(types.WithPrincipal(ctx, types.Principal{Type: kind, ID: "alice"}), "session")
		require.ErrorIs(t, err, ErrWorkbenchDenied)
	}
	adminCtx := context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	require.NoError(t, db.Exec("UPDATE tenant_members SET status='suspended' WHERE user_id='alice'").Error)
	_, err = s.Status(adminCtx, "session")
	require.ErrorIs(t, err, ErrWorkbenchDenied)
	require.NoError(t, db.Exec("UPDATE tenant_members SET status='active'").Error)
	require.NoError(t, db.Exec("UPDATE users SET is_active=false WHERE id='alice'").Error)
	_, err = s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchDenied)
	require.NoError(t, db.Exec("UPDATE users SET is_active=true").Error)
	require.NoError(t, db.Exec("UPDATE tenants SET status='disabled' WHERE id=7").Error)
	_, err = s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchDenied)
	require.NoError(t, db.Exec("UPDATE tenants SET status='active'").Error)
	policy.err = errors.New("database read failed")
	_, err = s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	policy.err = nil
	policy.disabled = true
	_, err = s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchPolicy)
}

func TestWorkbenchRejectsIMAndMaintenance(t *testing.T) {
	s, ctx, _, _, _ := newWorkbenchFixture(t)
	db := s.deps.Sessions.(workbenchTestSessions).db
	require.NoError(t, db.Exec("INSERT INTO im_channel_sessions VALUES ('session')").Error)
	_, err := s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchSession)
	require.NoError(t, db.Exec("DELETE FROM im_channel_sessions").Error)
	require.NoError(t, db.Model(&types.Session{}).Where("id = ?", "session").
		Update("description", types.SkillMaintenanceSessionMarker+"test").Error)
	_, err = s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchSession)
}

func TestWorkbenchStatusAndBind(t *testing.T) {
	s, ctx, manager, audit, _ := newWorkbenchFixture(t)
	require.NoError(t, s.deps.Pinner.Clear(ctx, "session"))
	status, err := s.Status(ctx, "session")
	require.NoError(t, err)
	require.False(t, status.Available)
	require.Equal(t, "unbound", status.Reason)
	require.Equal(t, 0, manager.ensured)
	require.Empty(t, audit.rows)
	_, err = s.Bind(ctx, "session", "foreign-config")
	require.ErrorIs(t, err, ErrWorkbenchCapability)
	pin, err := s.deps.Pinner.Read(ctx, "session")
	require.NoError(t, err)
	require.Empty(t, pin)
	status, err = s.Bind(ctx, "session", "config")
	require.NoError(t, err)
	require.True(t, status.Available)
	require.Equal(t, "docker", status.Provider)
	require.Equal(t, "/workspace/output", status.Root)
	require.Equal(t, 1, manager.ensured)
	require.Len(t, audit.rows, 2)
	status, err = s.Bind(ctx, "session", "second")
	require.NoError(t, err)
	require.Equal(t, "config", status.ConfigID)
	require.Equal(t, DefaultWorkbenchLimits(), status.Limits)
}

func TestWorkbenchStatusDoesNotAdvertiseTerminalWithoutStore(t *testing.T) {
	s, ctx, _, _, _ := newWorkbenchFixture(t)
	s.store = nil
	status, err := s.Status(ctx, "session")
	require.NoError(t, err)
	require.False(t, status.Capabilities["terminal"])
	require.True(t, status.Capabilities["files"])
	_, err = s.IssueTicket(ctx, "session", "http://localhost:15173", "workbench-test-access")
	require.ErrorIs(t, err, ErrWorkbenchCapability)
}

func TestWorkbenchAuditFailureRefusesOperations(t *testing.T) {
	s, ctx, manager, audit, _ := newWorkbenchFixture(t)
	audit.failAt = 1
	_, err := s.OpenTerminal(ctx, "session", sandbox.CommandTerminalRequest{Command: "echo hello", Cols: 80, Rows: 24})
	require.ErrorIs(t, err, ErrWorkbenchAudit)
	_, err = s.Files(ctx, "session", sandbox.WorkbenchFileRequest{
		Operation: "write", Path: "ok.txt", Content: []byte("private"),
	})
	require.ErrorIs(t, err, ErrWorkbenchAudit)
	_, err = s.Bind(ctx, "session", "config")
	require.ErrorIs(t, err, ErrWorkbenchAudit)
	require.Zero(t, manager.opened)
	require.Zero(t, manager.files)
	require.Zero(t, manager.ensured)
	audit.failAt = 2
	execution, err := s.OpenTerminal(ctx, "session", sandbox.CommandTerminalRequest{
		Command: "echo hello", Cols: 80, Rows: 24,
	})
	require.NoError(t, err)
	err = execution.Finish(sandbox.CommandTerminalExit{ExitCode: 0, Reason: "exited"}, nil)
	require.ErrorIs(t, err, ErrWorkbenchAudit)
	require.Equal(t, types.AuditOutcomeAccepted, audit.rows[0].Outcome)
}

func TestWorkbenchTerminalLimitsAuditAndUnknownOutcome(t *testing.T) {
	s, ctx, manager, audit, _ := newWorkbenchFixture(t)
	command := `TOKEN=private curl -H "Authorization: Bearer secret-value" ` +
		`https://user:password@example.com?api_key=hidden`
	execution, err := s.OpenTerminal(ctx, "session", sandbox.CommandTerminalRequest{
		Command: command, Cols: 80, Rows: 24, CPUSeconds: 999, MemoryBytes: 1,
	})
	require.NoError(t, err)
	require.Equal(t, 60, manager.request.CPUSeconds)
	require.EqualValues(t, 512<<20, manager.request.MemoryBytes)
	for _, secret := range []string{"private", "secret-value", "password", "hidden"} {
		require.NotContains(t, string(audit.rows[0].Details), secret)
	}
	require.NoError(t, execution.Finish(sandbox.CommandTerminalExit{ExitCode: 152, Reason: "cpu_limit"}, nil))
	require.Contains(t, string(audit.rows[1].Details), "cpu_limit")
	require.NoError(t, execution.Finish(sandbox.CommandTerminalExit{}, nil))
	require.Len(t, audit.rows, 2)
	manager.openErr = errors.New("ambiguous provider launch secret token")
	_, err = s.OpenTerminal(ctx, "session", sandbox.CommandTerminalRequest{Command: "echo test", Cols: 80, Rows: 24})
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	require.Equal(t, types.AuditOutcome("unknown"), audit.rows[3].Outcome)
	require.NotContains(t, string(audit.rows[3].Details), "secret")
	require.Equal(t, 2, manager.opened, "no retry on ambiguous launch")
}

func TestWorkbenchAuditScopedAndCapped(t *testing.T) {
	s, ctx, _, audit, _ := newWorkbenchFixture(t)
	for i := 0; i < 150; i++ {
		audit.rows = append(audit.rows, &types.AuditLog{
			TenantID: 7, ActorUserID: "alice", ScopeType: workbenchAuditScope, ScopeID: "session",
		})
	}
	audit.rows = append([]*types.AuditLog{{
		TenantID: 8, ActorUserID: "alice", ScopeType: workbenchAuditScope, ScopeID: "session",
		Details: types.JSON(`{"command":"foreign secret"}`),
	}}, audit.rows...)
	rows, err := s.Audit(ctx, "session", 0, 500)
	require.NoError(t, err)
	require.Len(t, rows, 100)
	for _, row := range rows {
		require.EqualValues(t, 7, row.TenantID)
	}
	_, err = s.Audit(ctx, "bob-session", 0, 100)
	require.ErrorIs(t, err, ErrWorkbenchSession)
}

func TestWorkbenchMemoryLimitAuditQueryable(t *testing.T) {
	s, ctx, _, _, _ := newWorkbenchFixture(t)
	execution, err := s.OpenTerminal(ctx, "session", sandbox.CommandTerminalRequest{
		Command: "memory workload", Cols: 80, Rows: 24,
	})
	require.NoError(t, err)
	require.NoError(t, execution.Finish(sandbox.CommandTerminalExit{ExitCode: 200, Reason: "memory_limit"}, nil))
	rows, err := s.Audit(ctx, "session", 0, 100)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, types.AuditOutcomeAccepted, rows[0].Outcome)
	require.Equal(t, types.AuditOutcomeFailed, rows[1].Outcome)
	require.Equal(t, execution.ExecutionID, rows[1].TargetID)
	require.Contains(t, string(rows[1].Details), `"reason":"memory_limit"`)
	require.Contains(t, string(rows[1].Details), `"exit_code":200`)
}

func TestWorkbenchPathsAndRedaction(t *testing.T) {
	for _, value := range []string{"/etc/passwd", "../secret", "a/../b", "a\\b", "a\x00b", "a\nb", "a//b", "a/./b"} {
		require.Error(t, ValidateWorkbenchPath(value, false), value)
	}
	for _, value := range []string{"file.txt", "folder/file.txt", "a b.txt"} {
		require.NoError(t, ValidateWorkbenchPath(value, false))
	}
	for _, command := range []string{
		"HELLO=world\necho " + strings.Repeat("x", 3000),
		"mysql -pSecretValue",
		"psql postgresql://alice:SecretValue@db.example/app",
	} {
		details := workbenchCommandAuditDetails(command)
		require.Equal(t, "[REDACTED]", details["command"])
		require.Equal(t, len(command), details["command_bytes"])
		require.NotContains(t, details, "SecretValue")
	}
}

func TestWorkbenchOptInAndMemoryRequiresSingleInstance(t *testing.T) {
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_ENABLED", "")
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_SINGLE_INSTANCE", "")
	s := NewWorkbenchService(WorkbenchServiceDeps{})
	require.False(t, s.Enabled())
	require.Nil(t, s.store)
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_ENABLED", "true")
	s = NewWorkbenchService(WorkbenchServiceDeps{})
	require.True(t, s.Enabled())
	require.Nil(t, s.store)
	t.Setenv("WEKNORA_SANDBOX_WORKBENCH_SINGLE_INSTANCE", "true")
	require.NotNil(t, NewWorkbenchService(WorkbenchServiceDeps{}).store)
}

func TestWorkbenchExitReasonSurvivesUnknownOutcome(t *testing.T) {
	s, ctx, _, audit, _ := newWorkbenchFixture(t)
	for _, reason := range []string{"cpu_limit", "memory_limit", "closed", "lease_lost"} {
		execution, err := s.OpenTerminal(ctx, "session", sandbox.CommandTerminalRequest{
			Command: "echo test", Cols: 80, Rows: 24,
		})
		require.NoError(t, err)
		require.NoError(t, execution.Finish(
			sandbox.CommandTerminalExit{ExitCode: -1, Reason: reason}, errors.New("cleanup could not confirm"),
		))
		row := audit.rows[len(audit.rows)-1]
		require.Equal(t, types.AuditOutcome("unknown"), row.Outcome)
		require.Contains(t, string(row.Details), `"reason":"`+reason+`"`)
	}
	exit := NormalizeWorkbenchTerminalExit(sandbox.CommandTerminalExit{Reason: "provider-secret"}, errors.New("failed"))
	require.Equal(t, "unknown", exit.Reason)
}

func TestWorkbenchStrictPrincipal(t *testing.T) {
	s, ctx, _, _, _ := newWorkbenchFixture(t)
	for _, rejected := range []context.Context{
		context.WithValue(ctx, types.PrincipalContextKey, types.Principal{}),
		context.WithValue(ctx, types.PrincipalContextKey, "alice"),
		types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "bob"}),
		context.WithValue(ctx, types.UserIDContextKey, ""),
		context.WithValue(ctx, types.TenantIDContextKey, uint64(0)),
		types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{FullAccess: true}),
	} {
		_, err := s.Authorize(rejected, "session")
		require.ErrorIs(t, err, ErrWorkbenchDenied)
	}
}

func TestWorkbenchAuthorizationFailsClosed(t *testing.T) {
	for _, table := range []string{"users", "tenants", "tenant_members", "sessions", "im_channel_sessions"} {
		t.Run(table, func(t *testing.T) {
			s, ctx, _, _, _ := newWorkbenchFixture(t)
			db := s.deps.Sessions.(workbenchTestSessions).db
			_, err := s.Authorize(ctx, "session")
			require.NoError(t, err)
			require.NoError(t, db.Exec("DROP TABLE "+table).Error)
			_, err = s.Authorize(ctx, "session")
			require.ErrorIs(t, err, ErrWorkbenchUnavailable)
		})
	}
	s, ctx, _, _, _ := newWorkbenchFixture(t)
	s.deps.Authorization = nil
	_, err := s.Authorize(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
}

func TestWorkbenchAuthorizationRejectsSoftDeletedIdentity(t *testing.T) {
	for _, table := range []string{"users", "tenants", "tenant_members", "sessions"} {
		t.Run(table, func(t *testing.T) {
			s, ctx, _, _, _ := newWorkbenchFixture(t)
			db := s.deps.Sessions.(workbenchTestSessions).db
			require.NoError(t, db.Table(table).Where("1 = 1").Update("deleted_at", "2026-09-10").Error)
			_, err := s.Authorize(ctx, "session")
			expected := ErrWorkbenchDenied
			if table == "sessions" {
				expected = ErrWorkbenchSession
			}
			require.ErrorIs(t, err, expected)
		})
	}
}

type workbenchRuntimeTestManager struct {
	*workbenchTestManager
	states    map[string]sandbox.WorkbenchRuntimeStatus
	err       error
	ensureErr error
	tenant    uint64
}

func (m *workbenchRuntimeTestManager) WorkbenchRuntimeStatus(
	ctx context.Context, sessionID string,
) (sandbox.WorkbenchRuntimeStatus, error) {
	m.tenant, _ = types.SandboxTenantIDFromContext(ctx)
	return m.states[sessionID], m.err
}

func (m *workbenchRuntimeTestManager) EnsureWorkbenchSession(ctx context.Context, sessionID string) error {
	if err := m.workbenchTestManager.EnsureWorkbenchSession(ctx, sessionID); err != nil {
		return err
	}
	return m.ensureErr
}

func TestWorkbenchStatusRuntimeIsSessionScoped(t *testing.T) {
	s, ctx, manager, audit, _ := newWorkbenchFixture(t)
	db := s.deps.Sessions.(workbenchTestSessions).db
	require.NoError(t, db.Model(&types.Session{}).Create(map[string]any{
		"id": "healthy", "tenant_id": 7, "user_id": "alice", "sandbox_config_id": "config",
	}).Error)
	runtime := &workbenchRuntimeTestManager{
		workbenchTestManager: manager,
		states: map[string]sandbox.WorkbenchRuntimeStatus{
			"session": {Known: true},
			"healthy": {Known: true, Terminal: true, Files: true},
		},
	}
	s.deps.Resolver = workbenchTestResolver{manager: runtime}
	ctx = types.WithSandboxTenantID(ctx, 99)
	failed, err := s.Status(ctx, "session")
	require.NoError(t, err)
	require.False(t, failed.Available)
	require.Equal(t, "capability_unavailable", failed.Reason)
	require.Equal(t, "bound", failed.State)
	require.Equal(t, "config", failed.ConfigID)
	healthy, err := s.Status(ctx, "healthy")
	require.NoError(t, err)
	require.True(t, healthy.Available)
	require.Empty(t, healthy.Reason)
	require.EqualValues(t, 7, runtime.tenant)
	require.Zero(t, manager.opened)
	require.Zero(t, manager.files)
	require.Zero(t, manager.ensured)
	require.Empty(t, audit.rows)
	pin, err := s.deps.Pinner.Read(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, "config", pin)
}

func TestWorkbenchStatusRuntimeOnlyNarrowsKnownCapabilities(t *testing.T) {
	for _, test := range []struct {
		name     string
		runtime  sandbox.WorkbenchRuntimeStatus
		noStore  bool
		terminal bool
		files    bool
	}{
		{"unknown", sandbox.WorkbenchRuntimeStatus{}, false, true, true},
		{"terminal only", sandbox.WorkbenchRuntimeStatus{Known: true, Terminal: true}, false, true, false},
		{"files only", sandbox.WorkbenchRuntimeStatus{Known: true, Files: true}, false, false, true},
		{"no console store", sandbox.WorkbenchRuntimeStatus{Known: true, Terminal: true}, true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, ctx, manager, _, _ := newWorkbenchFixture(t)
			runtime := &workbenchRuntimeTestManager{
				workbenchTestManager: manager,
				states:               map[string]sandbox.WorkbenchRuntimeStatus{"session": test.runtime},
			}
			s.deps.Resolver = workbenchTestResolver{manager: runtime}
			if test.noStore {
				s.store = nil
			}
			status, err := s.Status(ctx, "session")
			require.NoError(t, err)
			require.Equal(t, test.terminal, status.Capabilities["terminal"])
			require.Equal(t, test.files, status.Capabilities["files"])
			require.Equal(t, test.terminal || test.files, status.Available)
		})
	}
}

func TestWorkbenchRuntimeReadFailureKeepsPin(t *testing.T) {
	s, ctx, manager, _, _ := newWorkbenchFixture(t)
	runtime := &workbenchRuntimeTestManager{workbenchTestManager: manager, err: errors.New("binding unavailable")}
	s.deps.Resolver = workbenchTestResolver{manager: runtime}
	_, err := s.Status(ctx, "session")
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	_, err = s.Bind(ctx, "session", "config")
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	pin, err := s.deps.Pinner.Read(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, "config", pin)
}

func TestWorkbenchBindReportsRuntimeAndKeepsFailedPin(t *testing.T) {
	s, ctx, manager, _, _ := newWorkbenchFixture(t)
	runtime := &workbenchRuntimeTestManager{
		workbenchTestManager: manager,
		states:               map[string]sandbox.WorkbenchRuntimeStatus{"session": {Known: true, Files: true}},
	}
	s.deps.Resolver = workbenchTestResolver{manager: runtime}
	ctx = types.WithSandboxTenantID(ctx, 99)
	status, err := s.Bind(ctx, "session", "config")
	require.NoError(t, err)
	require.True(t, status.Available)
	require.False(t, status.Capabilities["terminal"])
	require.True(t, status.Capabilities["files"])
	require.EqualValues(t, 7, runtime.tenant)
	runtime.ensureErr = sandbox.ErrWorkbenchRuntimeIncompatible
	_, err = s.Bind(ctx, "session", "config")
	require.ErrorIs(t, err, ErrWorkbenchUnavailable)
	pin, err := s.deps.Pinner.Read(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, "config", pin)
}
