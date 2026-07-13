---
change: knowledge-scope-foundation
design-doc: docs/superpowers/specs/2026-07-14-system-version-knowledge-scope-design.md
base-ref: 784a3888dd203cdd950d307a33735b3a9726c63c
---

# System Version Knowledge Scope Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox...

> Codex/Comet execution note: repository `AGENTS.md` overrides the dispatcher sentence above. Codex remains the only dispatcher; use `codex-managed-comet-subagents` for Codex-native workers or execute serially with `superpowers:executing-plans`. Workers must remain leaf workers.

**Goal:** Add deterministic tenant/system/business-version knowledge isolation to WeKnora while preserving legacy knowledge-base behavior and keeping the fork easy to synchronize with Tencent upstream.

**Architecture:** Add one deep `KnowledgeScope` module with a catalog, resolver, and immutable resolved-scope value. Registered knowledge bases use structure-backed scope rows; legacy knowledge bases keep the current path. Upload, RAG search, Agent tools, and Neo4j all consume the same server-resolved document set. Tags and `project_id` may only intersect that set; they never expand it.

**Tech Stack:** Go 1.24, Gin, GORM, PostgreSQL/SQLite migrations, Neo4j/APOC, Vue 3, Pinia, TypeScript, TDesign, Node test runner, Docker Compose.

**Approved design:** `docs/superpowers/specs/2026-07-14-system-version-knowledge-scope-design.md`

## Scope cut

Keep: scope catalog, current/historical business versions, one default department KB, one KB per system, atomic document scope, optional `project_id`, scoped RAG/Agent/Neo4j, explicit legacy migration, provenance, feature flag, and isolation tests.

Defer: risk fact model, risk-analysis tools, Word template renderer, cross-system graph edges, project authorization, and processed-document scope moves.

Delete from the approach: tag-only isolation, prompt-only enforcement, and every fail-open fallback from scoped search/graph to whole-KB access.

## Success criteria

- System A never returns system B documents or graph nodes.
- A/v2 returns only department-public, A/system-common, and A/v2 documents.
- Multi-system queries independently resolve each system's version.
- Forged version/project/file/tag/Agent arguments cannot widen scope.
- A registered document cannot start parsing without an atomic scope row.
- Feature disabled or unregistered KB preserves current behavior.
- PostgreSQL, SQLite Lite mode, frontend, and Neo4j smoke tests pass.

---

### Task 0: Establish the maintainable fork baseline

**Files:** Verify `.git/config`; preserve unstaged `docker-compose.yml`, `docs/system-security-risk-analysis/`, and `kg_tech_roadmap_v1.8.5.md`.

- [ ] **Step 1: Capture the baseline**

```powershell
git status --short
git log -1 --oneline
git remote -v
```

Expected: the approved design commit exists and user-owned dirty files remain unstaged.

- [ ] **Step 2: Rewire remotes and create the feature branch**

Prerequisite: `https://github.com/zhujianye0759/WeKnora` exists.

```powershell
git remote rename origin upstream
git remote add origin https://github.com/zhujianye0759/WeKnora.git
git fetch upstream --prune
git fetch origin --prune
git switch -c codex/knowledge-scope-foundation
```

Expected: `origin` is the user fork; `upstream` is Tencent; no local history is rewritten.

---

### Task 1: Add schema, domain types, and a disabled-by-default flag

**Files:**

- Create: `migrations/versioned/000066_knowledge_scopes.up.sql`
- Create: `migrations/versioned/000066_knowledge_scopes.down.sql`
- Create: `migrations/sqlite/000001_knowledge_scopes.up.sql`
- Create: `migrations/sqlite/000001_knowledge_scopes.down.sql`
- Create: `internal/types/knowledge_scope.go`
- Create: `internal/types/knowledge_scope_test.go`
- Modify: `internal/config/config.go`
- Create: `internal/config/knowledge_scope_test.go`
- Modify: `.env.example`

- [ ] **Step 1: Write failing type/config tests**

Test all three legal scope shapes, illegal field combinations, trimming, and `ENABLE_KNOWLEDGE_SCOPE` default false/explicit true.

```powershell
go test ./internal/types ./internal/config -run 'KnowledgeScope' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Add the minimum domain model**

```go
type ScopeContainerType string
type KnowledgeScopeType string

const (
    ScopeContainerDepartmentPublic ScopeContainerType = "department_public"
    ScopeContainerSystem           ScopeContainerType = "system"
    KnowledgeScopeDepartmentPublic KnowledgeScopeType = "department_public"
    KnowledgeScopeSystemCommon     KnowledgeScopeType = "system_common"
    KnowledgeScopeSystemVersion    KnowledgeScopeType = "system_version"
)

type ScopeContainer struct {
    KnowledgeBaseID     string
    TenantID            uint64
    ContainerType       ScopeContainerType
    SystemID            string
    IsDefaultDepartment bool
    CreatedAt           time.Time
    UpdatedAt           time.Time
}

type SystemVersion struct {
    ID, SystemID, VersionKey, DisplayName, Status string
    TenantID uint64
    IsCurrent bool
    CreatedAt, UpdatedAt time.Time
}

type KnowledgeScope struct {
    KnowledgeID, SystemID, VersionID, ProjectID string
    TenantID uint64
    ScopeType KnowledgeScopeType
    CreatedAt, UpdatedAt time.Time
}
```

Add `TableName()` and pure `Validate()` methods. No repository calls or environment reads in domain types.

- [ ] **Step 3: Create PostgreSQL migration 000066**

Create `scope_containers`, `system_versions`, and `knowledge_scopes` with:

- `tenant_id NOT NULL` everywhere;
- FK cascade for tenant/KB/document deletion and `ON DELETE RESTRICT` for referenced versions;
- `CHECK` constraints for type/field combinations;
- unique `(tenant_id, system_id)` for system containers;
- partial unique default-department index on `tenant_id`;
- unique `(tenant_id, system_id, version_key)`;
- partial unique current-version index on `(tenant_id, system_id) WHERE is_current`;
- unique `(tenant_id, system_id, id)` plus the matching composite FK from document scopes;
- indexes for tenant/KB and tenant/system/version/project lookups.

Do not backfill existing data automatically.

- [ ] **Step 4: Create SQLite migration 000001**

Use SQLite-compatible types, checks, FKs, and partial indexes. Do not modify only `000000_init`: existing Lite databases are already at migration version 0.

- [ ] **Step 5: Wire the feature flag**

Add `KnowledgeScopeConfig{Enabled bool}` to `Config`, `Config.IsKnowledgeScopeEnabled()`, and the `ENABLE_KNOWLEDGE_SCOPE` env override. Nil/unset means disabled.

- [ ] **Step 6: Verify and commit**

```powershell
go test ./internal/types ./internal/config ./internal/database -run 'KnowledgeScope|Migration' -count=1
git add migrations/versioned/000066_knowledge_scopes.* migrations/sqlite/000001_knowledge_scopes.* internal/types/knowledge_scope.go internal/types/knowledge_scope_test.go internal/config/config.go internal/config/knowledge_scope_test.go .env.example docs/superpowers/specs/2026-07-14-system-version-knowledge-scope-design.md
git commit -m "feat(scope): add tenant-safe knowledge scope schema"
```

---

### Task 2: Build the Scope Catalog repository and service

**Files:**

- Create: `internal/types/interfaces/knowledge_scope.go`
- Create: `internal/application/repository/knowledge_scope.go`
- Create: `internal/application/repository/knowledge_scope_test.go`
- Create: `internal/application/service/knowledge_scope_catalog.go`
- Create: `internal/application/service/knowledge_scope_catalog_test.go`
- Modify: `internal/container/container.go`

- [ ] **Step 1: Write failing repository/service tests**

Using an in-memory SQLite schema matching migration 000001, test tenant-local default department/system uniqueness, one current version under concurrent updates, referenced-version delete rejection, tenant/system/version mismatch, batch lookup tenant isolation, and transaction rollback.

```powershell
go test ./internal/application/repository ./internal/application/service -run 'KnowledgeScopeCatalog|KnowledgeScopeRepository' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Define narrow module interfaces**

```go
type KnowledgeScopeRepository interface {
    GetContainerByKB(ctx context.Context, tenantID uint64, kbID string) (*types.ScopeContainer, error)
    GetDefaultDepartment(ctx context.Context, tenantID uint64) (*types.ScopeContainer, error)
    UpsertContainer(ctx context.Context, container *types.ScopeContainer) error
    ListVersions(ctx context.Context, tenantID uint64, systemID string) ([]*types.SystemVersion, error)
    GetVersion(ctx context.Context, tenantID uint64, versionID string) (*types.SystemVersion, error)
    CreateVersion(ctx context.Context, version *types.SystemVersion) error
    UpdateVersion(ctx context.Context, version *types.SystemVersion) error
    SetCurrentVersion(ctx context.Context, tenantID uint64, systemID, versionID string) error
    DeleteVersion(ctx context.Context, tenantID uint64, versionID string) error
    GetKnowledgeScope(ctx context.Context, tenantID uint64, knowledgeID string) (*types.KnowledgeScope, error)
    GetKnowledgeScopes(ctx context.Context, tenantID uint64, knowledgeIDs []string) (map[string]*types.KnowledgeScope, error)
    ListKnowledgeIDs(ctx context.Context, filter types.KnowledgeScopeFilter) ([]string, error)
    CreateKnowledgeWithScope(ctx context.Context, knowledge *types.Knowledge, scope *types.KnowledgeScope) error
    BatchCreateKnowledgeScopes(ctx context.Context, tenantID uint64, scopes []*types.KnowledgeScope) error
}
```

Catalog service owns validation and lifecycle errors; repository owns SQL and transactions.

- [ ] **Step 3: Implement current-version switching atomically**

Validate ownership, lock the system's version rows on PostgreSQL, clear current, set target, and let the partial unique index guard races. Version status never controls writability.

- [ ] **Step 4: Implement atomic knowledge + scope creation**

Insert both rows through one GORM transaction after validating KB/document tenant and system/version chain. A failure leaves neither row.

- [ ] **Step 5: Register repository/service beside the tag providers and verify**

```powershell
go test ./internal/application/repository ./internal/application/service -run 'KnowledgeScopeCatalog|KnowledgeScopeRepository' -count=1
git add internal/types/interfaces/knowledge_scope.go internal/application/repository/knowledge_scope.go internal/application/repository/knowledge_scope_test.go internal/application/service/knowledge_scope_catalog.go internal/application/service/knowledge_scope_catalog_test.go internal/container/container.go
git commit -m "feat(scope): add scope catalog service"
```

---

### Task 3: Expose catalog, version, and explicit migration APIs

**Files:**

- Create: `internal/handler/knowledge_scope.go`
- Create: `internal/handler/knowledge_scope_test.go`
- Modify: `internal/router/router.go`
- Modify: `internal/router/router_api_key_capabilities_test.go`
- Modify: `internal/container/container.go`

- [ ] **Step 1: Write failing handler/route tests**

Register and secure:

```text
GET/PUT /api/v1/knowledge-bases/:id/scope-container
GET/POST /api/v1/knowledge-bases/:id/system-versions
PUT/DELETE /api/v1/knowledge-bases/:id/system-versions/:version_id
PUT /api/v1/knowledge-bases/:id/system-versions/:version_id/current
POST /api/v1/knowledge-bases/:id/knowledge-scopes/preview
POST /api/v1/knowledge-bases/:id/knowledge-scopes/apply
```

Reads use Viewer + KB read. Mutations use `OwnedKBOrAdmin` + KB write. API keys require `manage_kbs` for catalog/version and `ingest` for scope apply.

```powershell
go test ./internal/handler ./internal/router -run 'KnowledgeScope|SystemVersion' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Implement thin handlers**

Handlers bind transport DTOs and call the catalog. Never accept body `tenant_id`; resolve tenant/system from context and container.

- [ ] **Step 3: Implement migration preview/apply**

Input is an explicit list such as:

```json
{"assignments":[{"knowledge_id":"doc-1","scope_type":"system_version","version_id":"ver-1","project_id":"P100"}]}
```

Preview returns normalized rows plus per-row errors without writes. Apply requires `confirmed:true`, revalidates all rows, and writes one transaction. Existing identical rows are idempotent; conflicting rows are rejected. Tags may be shown as hints only.

- [ ] **Step 4: Verify feature-off behavior and commit**

```powershell
go test ./internal/handler ./internal/router -run 'KnowledgeScope|SystemVersion' -count=1
git add internal/handler/knowledge_scope.go internal/handler/knowledge_scope_test.go internal/router/router.go internal/router/router_api_key_capabilities_test.go internal/container/container.go
git commit -m "feat(scope): expose scope and version management APIs"
```

---

### Task 4: Implement the single fail-closed Scope Resolver seam

**Files:**

- Create: `internal/application/service/knowledge_scope_resolver.go`
- Create: `internal/application/service/knowledge_scope_resolver_test.go`
- Modify: `internal/types/knowledge_scope.go`
- Modify: `internal/types/interfaces/knowledge_scope.go`
- Modify: `internal/container/container.go`

- [ ] **Step 1: Write the failing truth-table tests**

Cover all write shapes, cross-system/tenant version forgery, current default, explicit history, multi-system selection, implicit department inclusion, missing department/current version, project shrink, outside-scope file mention, mixed registered/legacy requests, and feature off.

```powershell
go test ./internal/application/service -run 'KnowledgeScopeResolver' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Add request/resolved values**

```go
type WriteScopeSelection struct {
    ScopeType KnowledgeScopeType `json:"scope_type"`
    VersionID string `json:"version_id,omitempty"`
    ProjectID string `json:"project_id,omitempty"`
}

type QueryScopeSelection struct {
    KnowledgeBaseID string `json:"knowledge_base_id"`
    VersionID string `json:"version_id,omitempty"`
    ProjectID string `json:"project_id,omitempty"`
}

type ResolvedScopeTarget struct {
    TenantID uint64
    KnowledgeBaseID string
    ContainerType ScopeContainerType
    SystemID, VersionID, ProjectID string
    KnowledgeIDs []string
}

type ResolvedQueryScope struct {
    Targets []ResolvedScopeTarget
    LegacyKnowledgeBaseIDs []string
}
```

Return deep copies so downstream code cannot mutate repository-owned state.

- [ ] **Step 3: Implement only two public resolver operations**

```go
ResolveWriteScope(ctx context.Context, knowledgeBaseID string, selection *types.WriteScopeSelection) (*types.ResolvedWriteScope, error)
ResolveQueryScope(ctx context.Context, knowledgeBaseIDs []string, selections []types.QueryScopeSelection) (*types.ResolvedQueryScope, error)
```

Rules: registered system without explicit version uses current; explicit selection must be a selected KB; project filter retains system-common plus exact-project version docs; no project includes all selected-version docs; invalid/missing scope fails closed; feature off/unregistered KB goes only to the legacy list.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/application/service -run 'KnowledgeScopeResolver' -count=1
git add internal/application/service/knowledge_scope_resolver.go internal/application/service/knowledge_scope_resolver_test.go internal/types/knowledge_scope.go internal/types/interfaces/knowledge_scope.go internal/container/container.go
git commit -m "feat(scope): resolve immutable write and query scopes"
```

---

### Task 5: Make scoped ingestion atomic and inherited by workers

**Files:**

- Modify: `internal/types/interfaces/knowledge.go`
- Modify: `internal/application/service/knowledge.go`
- Modify: `internal/application/service/knowledge_create.go`
- Modify: `internal/application/service/knowledge_clone_move.go`
- Modify: `internal/application/service/knowledge_process.go`
- Modify: `internal/handler/knowledge.go`
- Create: `internal/application/service/knowledge_scope_ingestion_test.go`
- Create: `internal/handler/knowledge_scope_ingestion_test.go`

- [ ] **Step 1: Write failing ingestion tests**

Cover file/URL/manual creation for each scope type, missing selection, wrong-system version, atomic rollback, knowledge-ID-only queue payload, worker/reparse scope reload, missing registered scope failure, duplicate with a conflicting scope, and scoped-document move rejection.

```powershell
go test ./internal/application/service ./internal/handler -run 'KnowledgeScopeIngestion|ScopedKnowledgeCreate' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Extend create signatures surgically**

Append `scopeSelection *types.WriteScopeSelection` to `CreateKnowledgeFromFile`, `CreateKnowledgeFromURL`, and `CreateKnowledgeFromManual`.

Transport:

- multipart file: `knowledge_scope` is one JSON object;
- URL/manual: optional JSON `knowledge_scope` object;
- department KB: resolver fixes department scope;
- registered system KB: valid selection required;
- malformed scope is never ignored.

- [ ] **Step 3: Replace only the insert seam**

After `ResolveWriteScope`, call `CreateKnowledgeWithScope`. Preserve storage, duplicate checks, process overrides, tags, and enqueue order unless atomicity requires a local change.

- [ ] **Step 4: Reload authority in workers**

At registered document processing/reparse entry, load scope by `(tenant_id, knowledge_id)` and pass a trusted value to graph extraction. Do not put system/version/project in Asynq payloads.

- [ ] **Step 5: Reject current move path for scoped documents**

The A1 correction path is delete/re-upload. Do not partially update chunks, vector indexes, or Neo4j labels.

- [ ] **Step 6: Verify and commit**

```powershell
go test ./internal/application/service ./internal/handler -run 'KnowledgeScopeIngestion|ScopedKnowledgeCreate' -count=1
git add internal/types/interfaces/knowledge.go internal/application/service/knowledge.go internal/application/service/knowledge_create.go internal/application/service/knowledge_clone_move.go internal/application/service/knowledge_process.go internal/handler/knowledge.go internal/application/service/knowledge_scope_ingestion_test.go internal/handler/knowledge_scope_ingestion_test.go
git commit -m "feat(scope): enforce atomic scoped ingestion"
```

---

### Task 6: Apply resolved scope to chat and direct search

**Files:**

- Modify: `internal/handler/session/types.go`
- Modify: `internal/handler/session/qa.go`
- Modify: `internal/types/qa_request.go`
- Modify: `internal/types/search.go`
- Modify: `internal/types/chat_manage.go`
- Modify: `internal/types/interfaces/session.go`
- Modify: `internal/event/event.go`
- Modify: `internal/application/service/session.go`
- Modify: `internal/application/service/session_knowledge_qa.go`
- Modify: `internal/application/service/chat_pipeline/search.go`
- Create: `internal/application/service/session_system_scope_test.go`
- Modify: `internal/application/service/session_tag_targets_test.go`

- [ ] **Step 1: Write failing request/target tests**

Send `system_scopes` through knowledge-chat and direct search. Assert implicit department, exact system-common/version IDs, tag and file intersection, cross-KB tag non-expansion, independent multi-system versions, invalid-scope 400 before retrieval, and registered+legacy mixed behavior.

```powershell
go test ./internal/handler/session ./internal/application/service -run 'SystemScope|TagTargets' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Add one optional request field**

Add `SystemScopes []types.QueryScopeSelection` with JSON name `system_scopes` to both session request DTOs and carry it through `types.QARequest`.

- [ ] **Step 3: Resolve exactly once before targets**

Inject the resolver into `sessionService`. `buildSearchTargets` must resolve structure targets, convert them to exact knowledge IDs, intersect explicit files/tags, append legacy targets unchanged, and attach scope metadata. Never perform another current-version lookup later.

- [ ] **Step 4: Preserve the frozen value and per-system failures**

Add `ResolvedScope *types.ResolvedQueryScope` to `PipelineRequest` and deep-copy it in `ChatManage.Clone()`. Record retrieval failures against their resolved target. If at least one system succeeds, continue with `ScopeWarnings` and emit one `scope_warning` event on the existing event bus; if none succeeds, return the retrieval error. Never replace a failed target with an unscoped search.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/handler/session ./internal/application/service -run 'SystemScope|TagTargets' -count=1
git add internal/handler/session/types.go internal/handler/session/qa.go internal/types/qa_request.go internal/types/search.go internal/types/chat_manage.go internal/types/interfaces/session.go internal/event/event.go internal/application/service/session.go internal/application/service/session_knowledge_qa.go internal/application/service/chat_pipeline/search.go internal/application/service/session_system_scope_test.go internal/application/service/session_tag_targets_test.go
git commit -m "feat(scope): bind chat retrieval to resolved system scopes"
```

---

### Task 7: Add scope provenance to every result

**Files:**

- Modify: `internal/types/search.go`
- Create: `internal/application/service/knowledge_scope_provenance.go`
- Create: `internal/application/service/knowledge_scope_provenance_test.go`
- Modify: `internal/application/service/chat_pipeline/search.go`
- Modify: `internal/agent/tools/knowledge_search.go`
- Modify: `internal/agent/tools/knowledge_search_rerank_test.go`

- [ ] **Step 1: Write failing provenance tests**

Version results must expose `scope_type`, `system_id`, `version_id`, `version_key`, optional `project_id`, and original document fields. Department/system-common omit irrelevant values; legacy results omit `knowledge_scope`.

- [ ] **Step 2: Batch-enrich results**

Collect result knowledge IDs, batch-load scope/version display data, and attach `KnowledgeScopeView`. Never query once per result and never derive values from KB names/tags.

- [ ] **Step 3: Include provenance in Agent search output**

Update the existing formatter only. Provenance is output context, never a new model-controlled query input.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/application/service ./internal/agent/tools -run 'ScopeProvenance|KnowledgeSearch' -count=1
git add internal/types/search.go internal/application/service/knowledge_scope_provenance.go internal/application/service/knowledge_scope_provenance_test.go internal/application/service/chat_pipeline/search.go internal/agent/tools/knowledge_search.go internal/agent/tools/knowledge_search_rerank_test.go
git commit -m "feat(scope): preserve scope provenance in retrieval"
```

---

### Task 8: Make Neo4j writes and reads scope-aware

**Files:**

- Modify: `internal/types/extract_graph.go`
- Modify: `internal/types/interfaces/retriever_graph.go`
- Modify: `internal/application/repository/retriever/neo4j/repository.go`
- Create: `internal/application/repository/retriever/neo4j/scoped_repository_test.go`
- Modify: `internal/application/service/extract.go`
- Modify: `internal/application/service/chat_pipeline/search_entity.go`
- Create: `internal/application/service/chat_pipeline/search_entity_scope_test.go`

- [ ] **Step 1: Write failing scoped graph tests**

Assert writes add `tenant_id`, `knowledge_base_id`, `knowledge_id`, `scope_type`, `system_id`, `version_id`, and `project_id`. Reads always include tenant plus exact allowed knowledge IDs. Empty scope must error and never build a label-only query.

```powershell
go test ./internal/application/repository/retriever/neo4j ./internal/application/service/chat_pipeline -run 'ScopedGraph|EntityScope' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Add methods without deleting legacy behavior**

```go
AddScopedGraph(ctx context.Context, namespace types.NameSpace, scope types.GraphScope, graphs []*types.GraphData) error
SearchScopedNodes(ctx context.Context, scopes []types.GraphSearchScope, nodes []string) (*types.GraphData, error)
```

`GraphSearchScope` carries trusted tenant/KB and exact allowed document IDs.

- [ ] **Step 3: Retain labels and add parameterized properties**

Keep existing `ENTITY<kb>`, `ENTITY<knowledge>`, and `kg` for compatibility. Add values via APOC parameters; never concatenate IDs/values into Cypher.

- [ ] **Step 4: Switch at one graph seam**

Registered extraction uses `AddScopedGraph`; legacy uses `AddGraph`. Scoped entity search uses `SearchScopedNodes` once. A scoped graph error may produce the approved partial warning but must never call legacy `SearchNode` as fallback.

- [ ] **Step 5: Verify and commit**

```powershell
go test ./internal/application/repository/retriever/neo4j ./internal/application/service/chat_pipeline -run 'ScopedGraph|EntityScope' -count=1
git add internal/types/extract_graph.go internal/types/interfaces/retriever_graph.go internal/application/repository/retriever/neo4j/repository.go internal/application/repository/retriever/neo4j/scoped_repository_test.go internal/application/service/extract.go internal/application/service/chat_pipeline/search_entity.go internal/application/service/chat_pipeline/search_entity_scope_test.go
git commit -m "feat(scope): enforce scoped Neo4j reads and writes"
```

---

### Task 9: Lock Agent knowledge and graph tools to server scope

**Files:**

- Modify: `internal/agent/tools/knowledge_search.go`
- Modify: `internal/agent/tools/query_knowledge_graph.go`
- Modify: `internal/agent/tools/query_knowledge_graph_test.go`
- Create: `internal/agent/tools/scoped_tools_test.go`
- Modify: `internal/application/service/agent_service.go`
- Modify: `internal/application/service/agent_rerank_requirement_test.go`

- [ ] **Step 1: Write adversarial Agent tests**

While bound to A/v2, submit B KB/version/document IDs. Assert no repository receives forged IDs. Empty scope and graph failure are hard errors.

```powershell
go test ./internal/agent/tools ./internal/application/service -run 'ScopedTool|QueryKnowledgeGraph' -count=1
```

Expected: FAIL.

- [ ] **Step 2: Add scoped constructors and schemas**

Structure-backed requests register tools with frozen `SearchTargets`/`ResolvedQueryScope`. Their schema exposes query/search controls only, not system/version or an expandable KB list. Keep current constructors for legacy requests.

- [ ] **Step 3: Make scoped graph tool use Neo4j**

Call `SearchScopedNodes`, then load only returned chunks/documents within the same allowed knowledge-ID set. Do not use whole-KB `HybridSearch` as a scoped graph substitute.

- [ ] **Step 4: Verify and commit**

```powershell
go test ./internal/agent/tools ./internal/application/service -run 'ScopedTool|QueryKnowledgeGraph' -count=1
git add internal/agent/tools/knowledge_search.go internal/agent/tools/query_knowledge_graph.go internal/agent/tools/query_knowledge_graph_test.go internal/agent/tools/scoped_tools_test.go internal/application/service/agent_service.go internal/application/service/agent_rerank_requirement_test.go
git commit -m "feat(scope): bind Agent tools to server scope"
```

---

### Task 10: Add scope and version management UI

**Files:**

- Create: `frontend/src/types/knowledgeScope.ts`
- Modify: `frontend/src/api/knowledge-base/index.ts`
- Create: `frontend/src/views/knowledge/settings/KBScopeSettings.vue`
- Create: `frontend/src/views/knowledge/settings/KBScopeSettings.test.ts`
- Modify: `frontend/src/views/knowledge/KnowledgeBaseEditorModal.vue`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`
- Modify: `frontend/src/i18n/locales/ko-KR.ts`
- Modify: `frontend/src/i18n/locales/ru-RU.ts`

- [ ] **Step 1: Write failing component/API tests**

Cover department/system declaration, stable `system_id`, one default department, create/edit/status/delete-unused version, current switch, referenced-delete message, and feature-disabled hiding.

```powershell
Set-Location frontend
npm test -- --test-name-pattern="scope settings"
```

Expected: FAIL.

- [ ] **Step 2: Add typed API functions**

Keep all scope DTOs in `frontend/src/types/knowledgeScope.ts`; do not spread anonymous `any` payloads across components.

- [ ] **Step 3: Add one isolated editor section**

Mount `KBScopeSettings.vue` in the existing KB editor navigation. The component owns only container/version UI and does not modify chunking/model/storage state.

- [ ] **Step 4: Verify and commit**

```powershell
npm test -- --test-name-pattern="scope settings"
npm run type-check
Set-Location ..
git add frontend/src/types/knowledgeScope.ts frontend/src/api/knowledge-base/index.ts frontend/src/views/knowledge/settings/KBScopeSettings.vue frontend/src/views/knowledge/settings/KBScopeSettings.test.ts frontend/src/views/knowledge/KnowledgeBaseEditorModal.vue frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/en-US.ts frontend/src/i18n/locales/ko-KR.ts frontend/src/i18n/locales/ru-RU.ts
git commit -m "feat(scope): add system version management UI"
```

---

### Task 11: Require upload scope and provide explicit legacy migration UI

**Files:**

- Modify: `frontend/src/stores/uploadConfirm.ts`
- Modify: `frontend/src/views/knowledge/components/UploadConfirmDialog.vue`
- Create: `frontend/src/views/knowledge/components/KnowledgeScopeSelector.vue`
- Create: `frontend/src/views/knowledge/components/KnowledgeScopeSelector.test.ts`
- Create: `frontend/src/views/knowledge/components/KnowledgeScopeMigrationDrawer.vue`
- Create: `frontend/src/views/knowledge/components/KnowledgeScopeMigrationDrawer.test.ts`
- Modify: `frontend/src/views/knowledge/KnowledgeBase.vue`
- Modify: `frontend/src/api/knowledge-base/index.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`
- Modify: `frontend/src/i18n/locales/ko-KR.ts`
- Modify: `frontend/src/i18n/locales/ru-RU.ts`

- [ ] **Step 1: Write failing UI tests**

Test fixed department scope, required system common/version choice, required version for version scope, optional project, writable retired versions, identical file/URL/manual payloads, explicit migration confirmation, and no silent tag-derived assignment.

- [ ] **Step 2: Extend upload result**

```ts
knowledgeScope?: {
  scope_type: 'department_public' | 'system_common' | 'system_version'
  version_id?: string
  project_id?: string
}
```

Multipart sends `knowledge_scope=JSON.stringify(...)`; URL/manual send a JSON object.

- [ ] **Step 3: Add migration preview/apply drawer**

List unscoped documents, let the user assign values, preview normalized rows/errors, and apply only after confirmation. Display tags as hints only.

- [ ] **Step 4: Verify and commit**

```powershell
Set-Location frontend
npm test -- --test-name-pattern="knowledge scope"
npm run type-check
Set-Location ..
git add frontend/src/stores/uploadConfirm.ts frontend/src/views/knowledge/components/UploadConfirmDialog.vue frontend/src/views/knowledge/components/KnowledgeScopeSelector.vue frontend/src/views/knowledge/components/KnowledgeScopeSelector.test.ts frontend/src/views/knowledge/components/KnowledgeScopeMigrationDrawer.vue frontend/src/views/knowledge/components/KnowledgeScopeMigrationDrawer.test.ts frontend/src/views/knowledge/KnowledgeBase.vue frontend/src/api/knowledge-base/index.ts frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/en-US.ts frontend/src/i18n/locales/ko-KR.ts frontend/src/i18n/locales/ru-RU.ts
git commit -m "feat(scope): require scope on upload and migration"
```

---

### Task 12: Add per-system version selection to chat and render provenance

**Files:**

- Modify: `frontend/src/stores/settings.ts`
- Modify: `frontend/src/stores/settingsStorage.ts`
- Modify: `frontend/src/stores/chatResources.ts`
- Modify: `frontend/src/components/KnowledgeBaseSelector.vue`
- Create: `frontend/src/components/SystemScopePicker.vue`
- Create: `frontend/src/components/SystemScopePicker.test.ts`
- Modify: `frontend/src/components/Input-field.vue`
- Modify: `frontend/src/views/chat/index.vue`
- Modify: `frontend/src/api/chat/streame.ts`
- Modify: `frontend/src/types/tool-results.ts`
- Modify: `frontend/src/utils/referenceSources.ts`
- Modify: `frontend/src/utils/referenceSources.test.mjs`
- Modify: `frontend/src/components/ChatReferencesDrawer.vue`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`
- Modify: `frontend/src/i18n/locales/ko-KR.ts`
- Modify: `frontend/src/i18n/locales/ru-RU.ts`

- [ ] **Step 1: Re-inspect and reuse the existing reference renderer**

```powershell
rg -n "KnowledgeFilename|knowledge_source|references" frontend/src/components frontend/src/views/chat
```

Confirm `referenceSources.ts` still normalizes document references into `ChatReferencesDrawer.vue`; do not create a second citation renderer.

- [ ] **Step 2: Write failing picker/store/request tests**

Test independent version selection, initial current version, historical version, removal cleanup, two-system request payload, implicit department behavior, system/version/project citation rendering, and visible `ScopeWarnings` when one selected system fails.

- [ ] **Step 3: Add normalized store state**

```ts
selectedSystemScopes: Record<string, { versionId: string; projectId?: string }>
```

Persist/reconcile it with selected KB IDs. Never key by display name.

- [ ] **Step 4: Send the optional API field**

```ts
system_scopes?: Array<{
  knowledge_base_id: string
  version_id?: string
  project_id?: string
}>
```

Backend remains authoritative for current fallback and department inclusion.

- [ ] **Step 5: Verify and commit**

```powershell
Set-Location frontend
npm test -- --test-name-pattern="system scope"
npm run type-check
npm run build
Set-Location ..
git add frontend/src/stores/settings.ts frontend/src/stores/settingsStorage.ts frontend/src/stores/chatResources.ts frontend/src/components/KnowledgeBaseSelector.vue frontend/src/components/SystemScopePicker.vue frontend/src/components/SystemScopePicker.test.ts frontend/src/components/Input-field.vue frontend/src/views/chat/index.vue frontend/src/api/chat/streame.ts frontend/src/types/tool-results.ts frontend/src/utils/referenceSources.ts frontend/src/utils/referenceSources.test.mjs frontend/src/components/ChatReferencesDrawer.vue frontend/src/i18n/locales/zh-CN.ts frontend/src/i18n/locales/en-US.ts frontend/src/i18n/locales/ko-KR.ts frontend/src/i18n/locales/ru-RU.ts
git commit -m "feat(scope): select system versions in chat"
```

---

### Task 13: Verify isolation, compatibility, deployment, and operations

**Files:**

- Create: `tests/knowledge_scope_isolation_test.go`
- Create: `docs/KnowledgeScope.md`
- Modify: `README_CN.md`
- Modify: `.env.example`

- [ ] **Step 1: Build the end-to-end fixture matrix**

Create department-public, A/common, A/v1, A/v2 across projects, B/common, B/v3, one legacy KB, and identical graph entity names in A/B. Exercise RAG, direct search, Agent knowledge search, Agent graph query, and pipeline entity search against the same allowed knowledge-ID sets.

- [ ] **Step 2: Automate Murphy/adversarial paths**

Cover cross-system version, cross-tenant IDs, OR-tag widening, outside-version file mention, concurrent current updates, missing default department, missing worker scope, Neo4j failure, vector partial failure with explicit incomplete warning, flag off, and unregistered legacy behavior.

- [ ] **Step 3: Run focused and full verification**

```powershell
go test ./internal/types ./internal/config ./internal/application/repository ./internal/application/service ./internal/handler ./internal/handler/session ./internal/agent/tools -count=1
go test ./tests -run 'KnowledgeScopeIsolation' -count=1
go test ./... -count=1
Set-Location frontend
npm test
npm run build-with-types
Set-Location ..
git diff --check
```

Expected: all PASS.

- [ ] **Step 4: Run PostgreSQL/Neo4j Compose smoke**

With local `.env` containing `ENABLE_KNOWLEDGE_SCOPE=true`:

```powershell
docker compose --profile neo4j up -d --build
docker compose ps
(Invoke-WebRequest -UseBasicParsing http://localhost/).StatusCode
(Invoke-WebRequest -UseBasicParsing http://localhost:8080/health).StatusCode
```

Expected: healthy containers and both HTTP status codes 200. Run A/v2 and A/v1+B/v3 queries; inspect returned sources and Neo4j results for zero leakage.

- [ ] **Step 5: Document operation and rollback**

`docs/KnowledgeScope.md` documents flag enable/disable, default department, system/version setup, upload/project rules, migration, query examples, failure semantics, rollback by disabling the flag without dropping tables, and upstream sync.

- [ ] **Step 6: Review, verify, and commit**

Run `superpowers:requesting-code-review`, address in-scope findings, then run `superpowers:verification-before-completion`.

```powershell
git add tests/knowledge_scope_isolation_test.go docs/KnowledgeScope.md README_CN.md .env.example
git commit -m "test(scope): verify system version isolation"
git status --short
```

Expected: only the user's pre-existing unrelated dirty files remain.

---

## Upstream synchronization after landing

```powershell
git fetch upstream --prune
git switch main
git merge --ff-only upstream/main
git push origin main
git switch -c codex/sync-upstream-YYYYMMDD
git merge main
```

Resolve conflicts only in the narrow seams: `knowledge_scope` module, optional request fields, upload insert point, retrieval target builder, Agent tool registration, graph additive methods, and three UI components. Rerun Task 13 before merging the sync branch.

## Implementation checkpoints

1. Tasks 0-4: schema, catalog, APIs, resolver; no retrieval behavior changed.
2. Tasks 5-7: atomic ingestion, scoped RAG, provenance; usable non-graph pilot.
3. Tasks 8-9: Neo4j and Agent parity.
4. Tasks 10-12: complete non-technical UI.
5. Task 13: isolation gate, Compose proof, docs, and branch finish.
