# Tenant Skill Upload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 WeKnora Standard 增加租户级 Skill ZIP 上传、版本管理、强制权限隔离、独立 Sandbox Runner、卡片化管理和 Agent 选择能力。

**Architecture:** PostgreSQL 保存 Skill、版本和执行审计；App 在 `tenant-skills` 持久卷中管理经过安全校验的版本；独立 `skill-runner` 通过内部认证 API 创建无网络、只读根文件系统的临时执行容器。Agent 使用 `{source, skill_id}` 稳定引用，并在每次读取或执行前按认证租户重新验证状态、版本和哈希。

**Tech Stack:** Go 1.26、Gin、GORM、PostgreSQL migrations、Docker API/Compose、Vue 3、TypeScript、TDesign、Node test、Go test。

**Scope note:** 第一版只支持 Standard/PostgreSQL + Docker Compose；Lite/SQLite 不展示租户 Skill 上传入口，现有内置 Skill 保持可用。Git commit 步骤仅在用户明确授权后执行。

---

## File map

**Database and domain**

- Create `migrations/versioned/000066_tenant_skills.up.sql` and `.down.sql`: tables, foreign keys, partial unique indexes.
- Create `internal/types/tenant_skill.go`: domain records, enums and canonical reference.
- Create `internal/application/repository/tenant_skill.go`: tenant-scoped CRUD, version state transitions and audit persistence.
- Create `internal/types/interfaces/tenant_skill.go`: repository/service contracts.

**Upload and storage**

- Create `internal/skillpkg/validator.go`: streaming ZIP validation and manifest parsing.
- Create `internal/skillpkg/path.go`: Unicode/case-fold path normalization and no-follow boundary checks.
- Create `internal/skillpkg/storage.go`: staging, version materialization, hashing and reconciliation.
- Create focused tests in `internal/skillpkg/*_test.go`.

**Authorization and API**

- Create `internal/middleware/tenant_skill_manager.go`: always-enforced JWT membership guard.
- Replace `internal/application/service/skill_service.go` with focused preloaded-list and tenant-management collaborators while preserving its public constructor.
- Extend `internal/handler/skill_handler.go` and `internal/router/router.go` with management routes.
- Modify `internal/container/container.go` to wire repositories, storage and services.

**Runner and execution**

- Create `cmd/skill-runner/main.go`, `internal/skillrunner/{api,auth,executor,limits}.go` and tests.
- Create `internal/skillrunner/client.go` for the App-side authenticated client.
- Create `docker/Dockerfile.skill-runner`; modify `docker-compose.yml` and `.env.example`.
- Modify `internal/sandbox/sandbox.go` only for bounded execution result fields used by Runner; tenant scripts never use Local Sandbox.

**Agent integration**

- Modify `internal/types/custom_agent.go`, `internal/types/agent.go`, `internal/application/service/session_agent_qa.go` and `internal/application/service/agent_service.go`.
- Create `internal/agent/skills/{tenant_loader,tenant_executor}.go`; keep `loader.go` for trusted preloaded files.
- Modify `internal/agent/skills/manager.go` and `internal/agent/tools/{skill_read,skill_execute}.go` so tenant Skills use canonical references and Runner rather than the legacy name/local-Sandbox path.
- Extend Agent/chat request DTOs and tests for canonical Skill references.

**Frontend**

- Extend `frontend/src/api/skill/index.ts`.
- Create `frontend/src/views/settings/SkillSettings.vue` and focused components under `frontend/src/views/settings/components/skills/`.
- Modify `frontend/src/views/settings/Settings.vue`, locale files and `frontend/src/views/agent/AgentEditorModal.vue`.
- Add Node tests under `frontend/tests/skills/`.

## Task 1: PostgreSQL schema and tenant-scoped repository

**Files:**

- Create: `migrations/versioned/000066_tenant_skills.up.sql`
- Create: `migrations/versioned/000066_tenant_skills.down.sql`
- Create: `internal/types/tenant_skill.go`
- Create: `internal/types/interfaces/tenant_skill.go`
- Create: `internal/application/repository/tenant_skill.go`
- Create: `internal/application/repository/tenant_skill_test.go`

- [ ] **Step 1: Write repository tests that prove tenant isolation and current-version uniqueness**

```go
func TestTenantSkillRepository_GetRequiresTenant(t *testing.T) {
    repo, db := newTenantSkillRepoTest(t)
    skill := seedTenantSkill(t, db, 10000, "invoice-reader")

    _, err := repo.GetByID(context.Background(), 20000, skill.ID)
    require.ErrorIs(t, err, repository.ErrTenantSkillNotFound)

    got, err := repo.GetByID(context.Background(), 10000, skill.ID)
    require.NoError(t, err)
    require.Equal(t, uint64(10000), got.TenantID)
}

func TestTenantSkillRepository_SwitchCurrentVersion(t *testing.T) {
    repo, db := newTenantSkillRepoTest(t)
    skill := seedTenantSkill(t, db, 10000, "invoice-reader")
    v1 := seedSkillVersion(t, db, skill.ID, 10000, 1, types.SkillVersionCurrent)
    v2 := seedSkillVersion(t, db, skill.ID, 10000, 2, types.SkillVersionReady)

    require.NoError(t, repo.SwitchCurrentVersion(context.Background(), 10000, skill.ID, v1.ID, v2.ID))
    require.Equal(t, types.SkillVersionGarbage, loadVersion(t, db, v1.ID).State)
    require.Equal(t, types.SkillVersionCurrent, loadVersion(t, db, v2.ID).State)
}
```

- [ ] **Step 2: Run the focused test and verify the missing repository failure**

Run: `go test ./internal/application/repository -run 'TestTenantSkillRepository' -count=1`

Expected: compile failure because tenant Skill types and repository do not exist.

- [ ] **Step 3: Add migration constraints and domain types**

Migration must include these enforceable constraints:

```sql
CREATE UNIQUE INDEX uq_tenant_skills_live_name
ON tenant_skills (tenant_id, lower(name)) WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX uq_tenant_skill_versions_number
ON tenant_skill_versions (skill_id, version);

CREATE UNIQUE INDEX uq_tenant_skill_versions_current
ON tenant_skill_versions (skill_id) WHERE state = 'current';
```

Define stable references without overloading names:

```go
type SkillSource string

const (
    SkillSourcePreloaded SkillSource = "preloaded"
    SkillSourceTenant    SkillSource = "tenant"
)

type SkillReference struct {
    Source  SkillSource `json:"source" yaml:"source"`
    SkillID string      `json:"skill_id" yaml:"skill_id"`
}
```

- [ ] **Step 4: Implement repository methods with mandatory `tenantID` arguments**

Required interface:

```go
type TenantSkillRepository interface {
    CreateStaging(ctx context.Context, skill *types.TenantSkill, version *types.TenantSkillVersion) error
    GetByID(ctx context.Context, tenantID uint64, skillID string) (*types.TenantSkill, error)
    List(ctx context.Context, tenantID uint64, includeDisabled bool) ([]*types.TenantSkill, error)
    SwitchCurrentVersion(ctx context.Context, tenantID uint64, skillID, oldVersionID, newVersionID string) error
    SetStatus(ctx context.Context, tenantID uint64, skillID string, status types.TenantSkillStatus) error
    SoftDelete(ctx context.Context, tenantID uint64, skillID string) error
    ListReconciliationCandidates(ctx context.Context, olderThan time.Time) ([]*types.TenantSkillVersion, error)
    CreateExecutionAudit(ctx context.Context, audit *types.SkillExecutionAudit) error
    FinishExecutionAudit(ctx context.Context, tenantID uint64, auditID string, finish types.ExecutionAuditFinish) error
}
```

- [ ] **Step 5: Run repository tests and migration smoke test**

Run: `go test ./internal/application/repository -run 'TestTenantSkillRepository' -count=1`

Expected: PASS.

Run the migration only in a random temporary database and always drop it:

```bash
set -e
db_user=$(docker exec WeKnora-postgres printenv POSTGRES_USER)
tmp_db="weknora_migration_$(date +%s)_$$"
cleanup() { docker exec WeKnora-postgres dropdb --if-exists -U "$db_user" "$tmp_db" >/dev/null; }
trap cleanup EXIT
docker exec WeKnora-postgres createdb -U "$db_user" "$tmp_db"
for migration in migrations/versioned/*.up.sql; do
  docker exec -i WeKnora-postgres psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$tmp_db" < "$migration"
done
docker exec -i WeKnora-postgres psql -v ON_ERROR_STOP=1 -U "$db_user" -d "$tmp_db" < migrations/versioned/000066_tenant_skills.down.sql
```

Expected: the complete up chain through 000066 and the 000066 down migration exit 0; the trap drops the temporary database on success or failure; the configured working database is never selected.

## Task 2: Safe ZIP validation and version storage

**Files:**

- Create: `internal/skillpkg/validator.go`
- Create: `internal/skillpkg/path.go`
- Create: `internal/skillpkg/storage.go`
- Create: `internal/skillpkg/validator_test.go`
- Create: `internal/skillpkg/storage_test.go`

- [ ] **Step 1: Add failing malicious-archive table tests**

```go
func TestValidatorRejectsUnsafeArchives(t *testing.T) {
    cases := []struct {
        name string
        zip  []byte
        code string
    }{
        {"traversal", zipEntry("../escape", "x"), "path_traversal"},
        {"absolute", zipEntry("/etc/passwd", "x"), "absolute_path"},
        {"encrypted", encryptedZipFixture(t), "encrypted_zip"},
        {"unicode duplicate", unicodeDuplicateZip(t), "duplicate_path"},
        {"case duplicate", zipEntries(map[string]string{"A.txt": "1", "a.txt": "2"}), "duplicate_path"},
        {"symlink", symlinkZipFixture(t), "unsupported_entry_type"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            _, err := NewValidator(DefaultLimits()).Validate(bytes.NewReader(tc.zip), int64(len(tc.zip)))
            require.ErrorContains(t, err, tc.code)
        })
    }
}
```

- [ ] **Step 2: Run tests and verify they fail before implementation**

Run: `go test ./internal/skillpkg -count=1`

Expected: compile failure because validator and storage are missing.

- [ ] **Step 3: Implement streaming limits and normalized paths**

```go
type Limits struct {
    MaxArchiveBytes   int64
    MaxExpandedBytes  int64
    MaxFileBytes      int64
    MaxFiles          int
    MaxPathDepth      int
}

func DefaultLimits() Limits {
    return Limits{20 << 20, 100 << 20, 20 << 20, 500, 12}
}
```

Each entry must be copied through `io.LimitedReader`; increment actual bytes written, normalize with Unicode NFC plus lower-case collision key, inspect Unix mode, and create files with `O_CREATE|O_EXCL|O_NOFOLLOW` or the platform-equivalent no-follow helper.

- [ ] **Step 4: Implement staging and reconciliation with database state as commit point**

```go
type Storage interface {
    Stage(ctx context.Context, tenantID uint64, uploadID string, archive io.Reader, archiveSize int64) (*ValidatedPackage, error)
    Materialize(ctx context.Context, tenantID uint64, skillID, versionID string, pkg *ValidatedPackage) (string, string, error)
    VerifyVersion(ctx context.Context, tenantID uint64, version *types.TenantSkillVersion) error
    RemoveVersion(ctx context.Context, tenantID uint64, version *types.TenantSkillVersion) error
    Reconcile(ctx context.Context, now time.Time) error
}
```

`storage_path` is always `<tenant_id>/<skill_id>/<version_id>` using immutable UUID identifiers; the numeric `version` is display/concurrency metadata only. No public method accepts a caller-provided root or absolute path.

- [ ] **Step 5: Run package tests including interruption recovery**

Run: `go test ./internal/skillpkg -count=1`

Expected: PASS, including archive limits, path collision, failed materialization and reconciliation cases.

## Task 3: Always-enforced management guard and tenant Skill service

**Files:**

- Create: `internal/middleware/tenant_skill_manager.go`
- Create: `internal/middleware/tenant_skill_manager_test.go`
- Modify: `internal/types/interfaces/skill.go`
- Modify: `internal/application/service/skill_service.go`
- Create: `internal/application/service/skill_execution_audit.go`
- Create: `internal/router/skill_audit_task.go`
- Create: `internal/application/service/tenant_skill_service_test.go`

- [ ] **Step 1: Write permission matrix tests before the guard**

```go
func TestRequireTenantSkillManager(t *testing.T) {
    cases := []struct {
        name       string
        principal  principalFixture
        enableRBAC bool
        wantStatus int
    }{
        {"owner", jwtMember("owner", 10000), false, http.StatusNoContent},
        {"admin", jwtMember("admin", 10000), false, http.StatusNoContent},
        {"viewer", jwtMember("viewer", 10000), true, http.StatusForbidden},
        {"api key full", apiKeyPrincipal("full_access", 10000), true, http.StatusForbidden},
        {"cross tenant system admin", crossTenantSystemAdmin(20000), true, http.StatusForbidden},
    }
    runGuardMatrix(t, cases)
}
```

- [ ] **Step 2: Verify the matrix fails with the existing `g.Admin()` behavior**

Run: `go test ./internal/middleware -run TestRequireTenantSkillManager -count=1`

Expected: compile failure before the dedicated guard exists.

- [ ] **Step 3: Implement JWT-only active-membership lookup**

The guard must reject when API-key scope exists, load `tenant_members` independently of rollout RBAC, require `status=active` and `role IN ('owner','admin')`, and never call `IsCrossTenantSuperuser`. The same result is required when `RBAC=false`; this management guard is always fail-closed.

```go
func RequireTenantSkillManager(members interfaces.TenantMemberRepository) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := c.Request.Context()
        if _, apiKey := types.TenantAPIKeyScopeFromContext(ctx); apiKey {
            abortSkillManager(c)
            return
        }
        userID, userOK := types.UserIDFromContext(ctx)
        tenantID, tenantOK := types.TenantIDFromContext(ctx)
        if !userOK || !tenantOK || !hasActiveManagerMembership(ctx, members, tenantID, userID) {
            abortSkillManager(c)
            return
        }
        c.Next()
    }
}
```

- [ ] **Step 4: Extend Skill service with upload transaction orchestration**

Service methods must use authenticated IDs supplied by handlers and return typed errors:

```go
type SkillService interface {
    ListVisible(ctx context.Context, tenantID uint64, manager bool) ([]*types.SkillSummary, error)
    GetVisible(ctx context.Context, tenantID uint64, ref types.SkillReference, manager bool) (*types.SkillDetail, error)
    Upload(ctx context.Context, tenantID uint64, userID string, archive io.Reader, size int64) (*types.TenantSkill, error)
    UpdatePackage(ctx context.Context, tenantID uint64, userID, skillID string, archive io.Reader, size int64, expectedVersion int64) (*types.TenantSkill, error)
    SetStatuses(ctx context.Context, tenantID uint64, updates []types.SkillStatusUpdate) []types.SkillStatusResult
    Delete(ctx context.Context, tenantID uint64, userID, skillID string) error
}
```

App is the sole owner of execution audit persistence; Runner never calls the database or an audit callback. `BeginExecution` creates the running audit before the App calls Runner and is fail-closed. After Runner returns, `FinishExecution` updates the row; if that update fails, App enqueues a `skill-audit-finish` task through `TaskEnqueuer`. If both update and enqueue fail, return the script result with `audit_update_pending=true`, emit a structured error, and let `ReconcileStaleExecutions` mark expired running rows `unknown` on the next housekeeping pass.

```go
type SkillExecutionAuditService interface {
    BeginExecution(ctx context.Context, tenantID uint64, userID string, ref types.SkillReference, versionID, scriptPath string) (*types.SkillExecutionAudit, error)
    FinishExecution(ctx context.Context, tenantID uint64, auditID string, result types.ExecutionAuditFinish) error
    EnqueueFinish(ctx context.Context, tenantID uint64, auditID string, result types.ExecutionAuditFinish) error
    ReconcileStaleExecutions(ctx context.Context, cutoff time.Time) error
}
```

The `skill-audit-finish` queue payload is `{tenant_id, audit_id, finish}` and the worker updates by `tenant_id + audit_id`; a payload without tenant ID is rejected before repository access.

- [ ] **Step 5: Run middleware and service tests**

Run: `go test ./internal/middleware ./internal/application/service -run 'TenantSkill|RequireTenantSkillManager' -count=1`

Expected: PASS.

## Task 4: HTTP API, anti-enumeration responses and DI wiring

**Files:**

- Modify: `internal/handler/skill_handler.go`
- Create: `internal/handler/skill_handler_test.go`
- Modify: `internal/router/router.go`
- Modify: `internal/container/container.go`
- Modify: `docs/swagger.yaml`

- [ ] **Step 1: Add handler contract tests**

```go
func TestSkillHandler_CrossTenantAndMissingBothReturn404(t *testing.T) {
    handler := newSkillHandlerFixture(t)
    require.Equal(t, http.StatusNotFound, performGet(handler, 10000, "tenant-b-id").Code)
    require.Equal(t, http.StatusNotFound, performGet(handler, 10000, "missing-id").Code)
}

func TestSkillHandler_BatchStatusReturnsPerItemResults(t *testing.T) {
    response := performBatchStatus(newSkillHandlerFixture(t), batchFixture())
    require.Equal(t, http.StatusMultiStatus, response.Code)
    require.JSONEq(t, `{"success":false,"results":[{"id":"ok","success":true},{"id":"missing","success":false,"code":"not_found"}]}`, response.Body.String())
}

func TestSkillHandler_LiteKeepsPreloadedListButDisablesTenantUpload(t *testing.T) {
    handler := newLiteSkillHandlerFixture(t)
    require.Equal(t, http.StatusOK, performList(handler).Code)
    require.Equal(t, http.StatusNotFound, performUpload(handler, safeScriptZip(t)).Code)
    require.Zero(t, handler.tenantRepositoryCalls())
}

func TestSkillHandler_RunnerUnavailableStillAllowsUpload(t *testing.T) {
    handler := newStandardSkillHandlerFixture(t, withRunnerAvailable(false))
    response := performList(handler)
    var body struct {
        TenantUploadAvailable   bool `json:"tenant_upload_available"`
        ScriptExecutionAvailable bool `json:"script_execution_available"`
    }
    require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
    require.True(t, body.TenantUploadAvailable)
    require.False(t, body.ScriptExecutionAvailable)
    require.Equal(t, http.StatusCreated, performUpload(handler, safeScriptZip(t)).Code)
    require.Equal(t, http.StatusServiceUnavailable, performScriptExecution(handler).Code)
}

func TestSystemAdminSkillAuditIsReadOnly(t *testing.T) {
    handler := newSystemAdminSkillAuditFixture(t)
    require.Equal(t, http.StatusOK, performSystemAuditList(handler, 10000).Code)
    require.Equal(t, http.StatusForbidden, performSystemAuditListAsTenantAdmin(handler, 10000).Code)
}
```

- [ ] **Step 2: Run focused handler tests and observe missing routes**

Run: `go test ./internal/handler -run 'SkillHandler' -count=1`

Expected: FAIL because management handlers and response DTOs are absent.

- [ ] **Step 3: Add upload, update, status, batch and delete handlers**

Handlers must call `http.MaxBytesReader` before multipart parsing, read tenant/user IDs from context, return 404 for missing/cross-tenant IDs, and return stable package-validation error codes with safe relative paths. `GET /skills` returns two independent capabilities: `tenant_upload_available` is true only for Standard/PostgreSQL with tenant storage initialized; `script_execution_available` reflects Runner health. All tenant-management handlers return capability-not-available 404 before repository access when upload capability is false, while preloaded listing remains operational. Runner downtime never disables upload/list/update, but tenant script execution remains fail-closed with 503.

- [ ] **Step 4: Register routes with separate read and write groups**

```go
skills := r.Group("/skills")
skills.GET("", g.Viewer(), skillHandler.ListSkills)
skills.GET("/:id", g.Viewer(), skillHandler.GetSkill)

managed := r.Group("/skills", middleware.RequireTenantSkillManager(memberRepo))
managed.POST("/upload", skillHandler.Upload)
managed.PUT("/:id/package", skillHandler.UpdatePackage)
managed.PATCH("/:id/status", skillHandler.SetStatus)
managed.PATCH("/status/batch", skillHandler.SetStatuses)
managed.DELETE("/:id", skillHandler.Delete)

system.GET("/tenant-skills", middleware.RequireSystemAdmin(), skillHandler.ListTenantSkillsForAudit)
```

The System Admin endpoint returns metadata and status only, supports an explicit `tenant_id` filter, has no package download or mutation method, and is covered by platform audit logging.

- [ ] **Step 5: Wire repository/storage/service and run route policy tests**

Run: `go test ./internal/handler ./internal/router ./internal/container -run 'Skill|RoutePolicy' -count=1`

Expected: PASS with all write routes covered by the dedicated guard.

## Task 5: Independent Skill Runner and fail-closed execution

**Files:**

- Create: `cmd/skill-runner/main.go`
- Create: `internal/skillrunner/api.go`
- Create: `internal/skillrunner/auth.go`
- Create: `internal/skillrunner/executor.go`
- Create: `internal/skillrunner/limits.go`
- Create: `internal/skillrunner/client.go`
- Create: `internal/skillrunner/executor_test.go`
- Create: `internal/skillrunner/client_test.go`
- Create: `docker/Dockerfile.skill-runner`
- Modify: `docker-compose.yml`
- Modify: `.env.example`

- [ ] **Step 1: Add failing Runner boundary tests**

```go
func TestExecutorRejectsCallerControlledContainerOptions(t *testing.T) {
    request := ExecuteRequest{
        TenantID: "10000", SkillID: "skill-id", VersionID: "version-id",
        ScriptPath: "scripts/run.py", Args: []string{"--safe"},
    }
    args, err := BuildContainerSpec(request, resolvedVersionFixture(t))
    require.NoError(t, err)
    require.NotContains(t, strings.Join(args, " "), "--privileged")
    require.Contains(t, args, "--network=none")
    require.Contains(t, args, "--read-only")
}

func TestRunnerUnavailableNeverUsesLocalProcess(t *testing.T) {
    client := NewClient(unreachableRunnerURL(), testCredential())
    _, err := client.Execute(context.Background(), validExecuteRequest())
    require.ErrorIs(t, err, ErrRunnerUnavailable)
    require.Zero(t, localExecutionCount())
}
```

- [ ] **Step 2: Run Runner tests and confirm missing implementation**

Run: `go test ./internal/skillrunner -count=1`

Expected: compile failure.

- [ ] **Step 3: Implement authenticated narrow request contract**

```go
type ExecuteRequest struct {
    ExecutionID string   `json:"execution_id"`
    TenantID  string   `json:"tenant_id"`
    SkillID   string   `json:"skill_id"`
    VersionID string   `json:"version_id"`
    ScriptPath string  `json:"script_path"`
    Args       []string `json:"args"`
    Stdin      string   `json:"stdin"`
}

type ExecuteResponse struct {
    ExitCode int    `json:"exit_code"`
    Stdout   string `json:"stdout"`
    Stderr   string `json:"stderr"`
    Truncated bool  `json:"truncated"`
    Killed   bool   `json:"killed"`
}
```

Reject unknown JSON fields, cap request body and argument counts, authenticate with rotatable service credentials, and never accept image, mount, network, environment or Docker flags from the request.

- [ ] **Step 4: Implement per-run ephemeral volume and bounded streams**

Runner reads the authorized version from the source `tenant-skills` volume, verifies hash/no-follow boundaries, copies only that version into a unique execution volume, mounts it read-only at `/skill`, mounts a separate work volume at `/work`, and deletes both after completion. Use capped writers for stdout/stderr. App must create the audit before calling Runner and passes only the opaque `execution_id`; Runner returns execution facts but never writes audit storage or invokes a callback. Tenant execution constructs the manager with `FallbackEnabled=false` and has no code path to `NewLocalSandbox`.

- [ ] **Step 5: Add Compose service without public ports**

```yaml
app:
  volumes:
    - tenant-skills:/data/skills
  networks:
    - WeKnora-network
    - skill-runner-internal

skill-runner:
  build:
    context: .
    dockerfile: docker/Dockerfile.skill-runner
  volumes:
    - tenant-skills:/data/skills:ro
    - /var/run/docker.sock:/var/run/docker.sock
  expose:
    - "8091"
  networks:
    - skill-runner-internal
  restart: unless-stopped

networks:
  skill-runner-internal:
    internal: true

volumes:
  tenant-skills:
```

App retains `WeKnora-network`, additionally joins `skill-runner-internal`, mounts `tenant-skills` read-write, and does not mount the Docker Socket. Runner mounts the source volume read-only. Do not publish port 8091 to the host. Add health checks and service-credential environment variables with blank examples, never real values.

- [ ] **Step 6: Run Runner tests and Compose static verification**

Run: `go test ./internal/skillrunner -count=1`

Expected: PASS.

Run: `docker compose config --quiet`

Expected: exit 0.

Run this structural assertion against rendered Compose JSON:

```bash
docker compose config --format json | python3 -c '
import json, sys
c=json.load(sys.stdin); s=c["services"]
assert not s["skill-runner"].get("ports")
networks=set(s["app"]["networks"])
assert any(n.endswith("WeKnora-network") for n in networks)
assert any(n.endswith("skill-runner-internal") for n in networks)
assert any(v.get("source", "").endswith("tenant-skills") and v["target"] == "/data/skills" and not v.get("read_only", False) for v in s["app"]["volumes"])
assert any(v.get("source", "").endswith("tenant-skills") and v["target"] == "/data/skills" and v.get("read_only", False) for v in s["skill-runner"]["volumes"])
'
```

Expected: exit 0, proving no public Runner port and correct App/Runner network and volume modes.

## Task 6: Canonical Agent references and tenant-aware runtime loading

**Files:**

- Modify: `internal/types/custom_agent.go`
- Modify: `internal/types/agent.go`
- Modify: `internal/application/service/session_agent_qa.go`
- Modify: `internal/application/service/agent_service.go`
- Modify: `internal/agent/skills/manager.go`
- Create: `internal/agent/skills/tenant_loader.go`
- Create: `internal/agent/skills/tenant_executor.go`
- Create: `internal/agent/skills/tenant_loader_test.go`
- Modify: `internal/agent/tools/skill_read.go`
- Modify: `internal/agent/tools/skill_execute.go`
- Create: `internal/agent/tools/skill_tenant_test.go`
- Modify: `internal/application/service/session_agent_qa_scope_test.go`

- [ ] **Step 1: Add migration and runtime reauthorization tests**

```go
func TestLegacySelectedSkillsMigrateOnlyToPreloaded(t *testing.T) {
    cfg := types.CustomAgentConfig{SelectedSkills: []string{"citation-generator", "unknown"}}
    refs, invalid := MigrateLegacySkillRefs(cfg, preloadedFixture("citation-generator"))
    require.Equal(t, []types.SkillReference{{Source: types.SkillSourcePreloaded, SkillID: "citation-generator"}}, refs)
    require.Equal(t, []string{"unknown"}, invalid)
}

func TestTenantLoaderRejectsDisabledSkillDespiteCachedMetadata(t *testing.T) {
    loader := newTenantLoaderFixture(t, enabledSkillFixture())
    require.NoError(t, loader.Prime(context.Background(), 10000, tenantRefFixture()))
    loader.repo.SetDisabled()
    _, err := loader.LoadInstructions(context.Background(), 10000, tenantRefFixture())
    require.ErrorIs(t, err, ErrSkillDisabled)
}
```

- [ ] **Step 2: Run focused tests and verify missing reference types**

Run: `go test ./internal/agent/skills ./internal/application/service -run 'SkillRef|TenantLoader|LegacySelectedSkills' -count=1`

Expected: FAIL before canonical references and tenant loader exist.

- [ ] **Step 3: Add `selected_skill_refs` with legacy read compatibility**

```go
SelectedSkills    []string         `yaml:"selected_skills,omitempty" json:"selected_skills,omitempty"`
SelectedSkillRefs []SkillReference `yaml:"selected_skill_refs,omitempty" json:"selected_skill_refs,omitempty"`
```

New saves write only `selected_skill_refs`; legacy names resolve only against preloaded Skills. Unknown names remain visible as invalid configuration and never bind to a tenant upload with the same name.

- [ ] **Step 4: Adapt Manager and both Agent tools to canonical references**

`skills.Manager` receives a trusted runtime scope rather than only `SkillDirs/AllowedSkills []string`:

```go
type RuntimeScope struct {
    TenantID uint64
    UserID   string
    Allowed  []types.SkillReference
}

type Resolver interface {
    LoadInstructions(ctx context.Context, scope RuntimeScope, ref types.SkillReference) (*Skill, error)
    ReadFile(ctx context.Context, scope RuntimeScope, ref types.SkillReference, relativePath string) (string, error)
    Execute(ctx context.Context, scope RuntimeScope, ref types.SkillReference, scriptPath string, args []string, stdin string) (*sandbox.ExecuteResult, error)
}
```

`read_skill` and `execute_skill_script` add `skill_ref` while retaining `skill_name` only for preloaded compatibility:

```go
type SkillRefInput struct {
    Source  types.SkillSource `json:"source"`
    SkillID string            `json:"skill_id"`
}

type ReadSkillInput struct {
    SkillRef *SkillRefInput `json:"skill_ref,omitempty"`
    SkillName string        `json:"skill_name,omitempty"`
    FilePath string         `json:"file_path,omitempty"`
}
```

If `skill_name` is supplied, resolve it only against preloaded Skills. Tenant cards and prompt metadata emit `skill_ref`; tenant names never participate in identity resolution. `agent_service.initializeSkillsManager` injects authenticated tenant/user scope, the tenant resolver and App-side Runner client. Tenant execution bypasses the old `sandbox.Manager`; preloaded execution retains the existing compatibility path.

- [ ] **Step 5: Add runtime tenant reauthorization and versioned cache keys**

Use cache key `tenantID/source/skillID/version`. Every `read_skill`, resource read and script execution reloads current record by `tenant_id + skill_id`, checks enabled/current/hash, acquires an execution lease, calls App-owned `BeginExecution`, invokes Runner, then calls `FinishExecution` or its reliable finish queue. Tenant scripts never construct `SkillDirs` from client input.

- [ ] **Step 6: Run Agent, tool, Skill and session regression tests**

Run: `go test ./internal/agent/... ./internal/application/service/... -run 'Skill|Agent|Session' -count=1`

Expected: PASS with deletion, disable, version switch, cache-event loss and hash-tamper cases.

## Task 7: Skills management API client and settings UI

**Files:**

- Modify: `frontend/src/api/skill/index.ts`
- Create: `frontend/src/views/settings/SkillSettings.vue`
- Create: `frontend/src/views/settings/components/skills/SkillCard.vue`
- Create: `frontend/src/views/settings/components/skills/SkillUploadDialog.vue`
- Create: `frontend/src/views/settings/components/skills/SkillBatchBar.vue`
- Modify: `frontend/src/views/settings/Settings.vue`
- Modify: `frontend/src/locales/zh-CN.ts`
- Modify: `frontend/src/locales/en-US.ts`
- Create: `frontend/tests/skills/skill-management.test.js`

- [ ] **Step 1: Run Impeccable setup before UI work**

Run: `node .agents/skills/impeccable/scripts/context.mjs`

Expected: project product/design context or the required initialization instruction; follow the output before editing Vue files. Read `impeccable/reference/product.md` and the existing Settings/Agent visual patterns.

- [ ] **Step 2: Add failing source-level interaction tests**

```js
test('viewer sees cards but no management actions', () => {
  const ui = renderSkillSettings({ role: 'viewer', skills: tenantSkillFixtures() })
  assert.equal(ui.queryByText('上传 Skill'), null)
  assert.equal(ui.queryByRole('checkbox'), null)
})

test('batch response preserves per-item failures', async () => {
  const ui = renderSkillSettings({ role: 'admin', skills: tenantSkillFixtures() })
  await ui.select(['skill-a', 'skill-b'])
  await ui.batchDisable(batchMixedResultFixture())
  assert.match(ui.text(), /skill-b.*无权限/)
  assert.match(ui.text(), /重试失败项/)
})

test('lite hides tenant upload management but preserves preloaded selector', () => {
  const settings = renderSettings({ edition: 'lite', tenant_upload_available: false, script_execution_available: false })
  assert.equal(settings.queryByText('Skills 管理'), null)
  assert.ok(renderAgentSelector({ preloaded: preloadedSkillFixtures() }).queryByText('citation-generator'))
})

test('runner outage keeps management visible and marks scripts unavailable', () => {
  const settings = renderSettings({ edition: 'standard', tenant_upload_available: true, script_execution_available: false })
  assert.ok(settings.queryByText('上传 Skill'))
  assert.match(settings.text(), /脚本执行服务暂不可用/)
})
```

- [ ] **Step 3: Run tests and verify missing components**

Run: `cd frontend && node --test tests/skills/skill-management.test.js`

Expected: FAIL because the management UI does not exist.

- [ ] **Step 4: Extend API types and methods**

```ts
export type SkillSource = 'preloaded' | 'tenant'
export type SkillCategory = 'content' | 'data' | 'development' | 'workflow' | 'other'

export interface SkillSummary {
  source: SkillSource
  skill_id: string
  name: string
  description: string
  category: SkillCategory
  status: 'enabled' | 'disabled'
  version?: number
  has_scripts: boolean
  readonly: boolean
}

export interface SkillListResponse {
  data: SkillSummary[]
  tenant_upload_available: boolean
  script_execution_available: boolean
}
```

Add `uploadSkill`, `updateSkillPackage`, `setSkillStatus`, `setSkillStatuses`, `deleteSkill` using the existing request helpers and upload progress support.

- [ ] **Step 5: Implement Settings navigation, tabs, cards and upload dialog**

Use existing TDesign tokens and spacing. Tabs are `全部/内容处理/数据分析/开发工具/业务流程/其他`; cards cap at 16px radius, show source/status/script badges, and do not nest decorative cards. Upload dialog shows file size, progress and a persistent list of validation errors. Keyboard focus, 4.5:1 text contrast and reduced-motion behavior are required. Settings navigation renders `Skills 管理` only when `tenant_upload_available=true`; Lite still consumes the preloaded portion of `GET /skills` for Agent selection and never imports tenant-management components. When only `script_execution_available=false`, management/upload remains available and script-bearing cards show a non-blocking execution-unavailable status.

- [ ] **Step 6: Run focused UI tests, type-check and build**

Run: `cd frontend && node --test tests/skills/skill-management.test.js`

Expected: PASS.

Run: `cd frontend && npm run build-with-types`

Expected: exit 0; existing chunk-size warnings may remain, no new type errors.

## Task 8: Agent Skill card selector and legacy-invalid state

**Files:**

- Modify: `frontend/src/views/agent/AgentEditorModal.vue`
- Create: `frontend/src/views/agent/components/SkillSelector.vue`
- Modify: `frontend/src/api/agent/index.ts`
- Create: `frontend/tests/skills/agent-skill-selector.test.js`

- [ ] **Step 1: Add failing selector tests**

```js
test('selector submits canonical references only', async () => {
  const selector = renderSelector(skillFixtures())
  await selector.choose('tenant-skill-id')
  assert.deepEqual(selector.value(), [{ source: 'tenant', skill_id: 'tenant-skill-id' }])
})

test('legacy unknown name remains invalid and unbound', () => {
  const selector = renderSelector(skillFixtures(), { selected_skills: ['same-name'] })
  assert.match(selector.text(), /same-name.*不可用/)
  assert.deepEqual(selector.value(), [])
})
```

- [ ] **Step 2: Run tests and verify the old checkbox list fails the contract**

Run: `cd frontend && node --test tests/skills/agent-skill-selector.test.js`

Expected: FAIL because the current editor stores name strings.

- [ ] **Step 3: Implement category tabs and card checkboxes**

The component receives enabled visible Skills, groups by controlled category, disables no longer available references, and emits `SkillReference[]`. Built-in and tenant cards use stable IDs and visually distinct source badges without changing selection semantics.

- [ ] **Step 4: Update Agent request types and save payload**

```ts
export interface SkillReference {
  source: 'preloaded' | 'tenant'
  skill_id: string
}

selected_skill_refs?: SkillReference[]
selected_skills?: string[]
```

Only send `selected_skills` when preserving an untouched legacy Agent; after any Skill selection edit, send canonical refs and clear legacy names.

- [ ] **Step 5: Run selector and full frontend tests**

Run: `cd frontend && node --test tests/skills/agent-skill-selector.test.js && npm test && npm run build-with-types`

Expected: focused tests PASS, existing frontend test suite PASS, type/build exit 0.

## Task 9: Integration, security verification and documentation

**Files:**

- Create: `tests/tenant_skill_upload_test.go`
- Modify: `Config.md`
- Modify: `docs/agent-skills.md`
- Modify: `.env.example`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add end-to-end tenant isolation test**

```go
func TestTenantSkillUploadAndExecutionIsolation(t *testing.T) {
    env := startTenantSkillEnvironment(t)
    skill := env.UploadAsAdmin(10000, safeScriptZip(t))
    env.RequireVisible(10000, skill.ID)
    env.RequireNotFound(20000, skill.ID)
    env.ExecuteAsMember(10000, skill.ID, "scripts/run.py")
    env.RequireExecutionSucceeded(skill.ID)
    env.DisableAsAdmin(10000, skill.ID)
    env.RequireExecutionRejected(skill.ID, "skill_disabled")
}
```

- [ ] **Step 2: Run the end-to-end test before final integration fixes**

Run: `go test ./tests -run TestTenantSkillUploadAndExecutionIsolation -count=1`

Expected: test identifies any missing integration wiring; fix only the demonstrated boundary before rerunning.

- [ ] **Step 3: Verify Runner isolation with adversarial scripts**

Run fixtures that attempt network access, fork flooding, output flooding, `/data/skills` traversal, Docker Socket access and cross-tenant file reads.

Expected: each attempt is denied or killed; no Local Sandbox process is created; audit rows contain bounded summaries.

- [ ] **Step 4: Run complete verification matrix**

Run:

```bash
go test -count=1 ./...
go vet ./...
(cd client && go test -count=1 ./... && go vet ./...)
(cd cli && go test -count=1 ./... && go vet ./...)
(cd frontend && npm test && npm run build-with-types)
docker compose config --quiet
docker compose up -d --build skill-runner app frontend
curl -fsS http://localhost:8080/health
```

Expected: every command exits 0; App/PostgreSQL/DocReader/Runner are healthy; Frontend and Redis running; no restart loop.

- [ ] **Step 5: Update operator and user documentation**

`Config.md` must list `tenant-skills` volume, Runner service credentials, resource-limit variables and the Docker Socket trust boundary. `docs/agent-skills.md` must document ZIP structure, allowed scripts, categories, role permissions, immediate activation, no-network default, update behavior and error codes. Per project requirement, submit both modified documents to independent subagent approval before completion.

- [ ] **Step 6: Request code review and run completion verification**

Use `superpowers:requesting-code-review`, resolve all Critical/Important findings, then use `superpowers:verification-before-completion` with fresh command output. Do not claim completion if Runner isolation or cross-tenant tests are skipped.

## Proposed commit checkpoints

These commits require explicit user authorization before execution:

1. `feat(skills): 增加租户技能数据模型`
2. `feat(skills): 实现安全上传与权限隔离`
3. `feat(sandbox): 增加独立技能执行服务`
4. `feat(agent): 支持租户技能稳定引用`
5. `feat(frontend): 增加技能管理与分类选择`
6. `docs(skills): 补充上传与沙盒配置说明`
