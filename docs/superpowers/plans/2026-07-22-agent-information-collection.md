# Agent Information Collection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let agent owners configure a bounded information schema, deterministically collect each user's missing visible fields before the Agent runs, silently keep values current from later messages, and give System Admins a cross-tenant history and export surface.

**Architecture:** Store the schema inside `CustomAgentConfig`, while current values and append-only changes live in dedicated profile/history tables keyed by consumer tenant, agent, and user. Reuse the existing user-input gate and SSE stream by extending its typed contract for text, number, and date values; a session-level coordinator performs extraction, missing-field calculation, blocking collection, persistence, and then resumes the original Agent request. Cross-tenant reads, edits, history, and exports live only beneath the existing `g.SystemAdmin()` route group and inside the unified Settings modal.

**Tech Stack:** Go, Gin, GORM, PostgreSQL JSONB, dig, existing Chat model interface and EventBus, Redis-backed user-input gate, `excelize/v2`, Vue 3, TypeScript, TDesign Vue Next, Node test runner, Go test.

**Execution note:** Work in the current dirty workspace and preserve unrelated changes. Git commit commands below are checkpoints to run only after the user explicitly authorizes Git history operations.

---

## File Map

- `internal/types/custom_agent.go`, new `internal/types/agent_collection.go`: persisted schema, validation, profile/history DTOs.
- `migrations/versioned/000067_agent_collection_profiles.*.sql`: current profile, append-only history, and export-job storage.
- New `internal/types/interfaces/agent_collection.go`, repository and service files: transactional persistence, reconciliation, extraction, filtering.
- Existing `internal/agent/userinput` and EventBus files: generalized field types, values, and dynamic progress while preserving `ask_user`.
- New `internal/application/service/session_agent_collection.go`: extraction and blocking intake before Agent execution.
- New `internal/handler/system_agent_collection.go`: System Admin-only list, detail, history, edit, CSV, and XLSX.
- New focused Vue components for agent schema configuration and System Admin profile management.
- Existing structured question card: single choice, multiple choice, short text, long text, number, date, and remaining count.

### Task 1: Agent collection schema and validation

**Files:**
- Modify: `internal/types/custom_agent.go`
- Create: `internal/types/agent_collection.go`
- Create: `internal/types/agent_collection_test.go`
- Modify: `internal/application/service/custom_agent.go`
- Create or modify: `internal/application/service/custom_agent_collection_test.go`

- [x] **Step 1: Write failing schema validation tests**

Cover duplicate/invalid keys, unsupported field types, missing or duplicate options, invalid numeric/text constraints, unknown condition dependencies, dependency cycles, invalid thresholds, more than 100 enabled fields, and sensitive labels/descriptions containing password, token, private key, OTP, secret, API key, 密码, 令牌, 私钥, 验证码, or 密钥. Also cover a valid conditional schema.

```go
func ValidateAgentCollectionConfig(cfg CustomAgentConfig) error
func NormalizeAgentCollectionConfig(cfg *CustomAgentConfig)
func VisibleCollectionFields(fields []AgentCollectionField, values JSONMap) []AgentCollectionField
func ValidateCollectionValue(field AgentCollectionField, value any) error
```

- [x] **Step 2: Run tests and verify RED**

```bash
go test ./internal/types ./internal/application/service -run 'Test.*AgentCollection|Test.*CustomAgent.*Collection' -count=1
```

Expected: compilation fails because the collection contract is absent.

- [x] **Step 3: Define the exact persisted contract**

```go
type AgentCollectionFieldType string

const (
    AgentCollectionSingleChoice AgentCollectionFieldType = "single_choice"
    AgentCollectionMultipleChoice AgentCollectionFieldType = "multiple_choice"
    AgentCollectionShortText AgentCollectionFieldType = "short_text"
    AgentCollectionLongText AgentCollectionFieldType = "long_text"
    AgentCollectionNumber AgentCollectionFieldType = "number"
    AgentCollectionDate AgentCollectionFieldType = "date"
)

type AgentCollectionOption struct {
    ID string `json:"id" yaml:"id"`
    Label string `json:"label" yaml:"label"`
}

type AgentCollectionValidation struct {
    MinLength *int `json:"min_length,omitempty" yaml:"min_length,omitempty"`
    MaxLength *int `json:"max_length,omitempty" yaml:"max_length,omitempty"`
    MinNumber *float64 `json:"min_number,omitempty" yaml:"min_number,omitempty"`
    MaxNumber *float64 `json:"max_number,omitempty" yaml:"max_number,omitempty"`
}

type AgentCollectionCondition struct {
    FieldKey string `json:"field_key" yaml:"field_key"`
    Operator string `json:"operator" yaml:"operator"`
    Value any `json:"value" yaml:"value"`
}

type AgentCollectionField struct {
    Key string `json:"key" yaml:"key"`
    Label string `json:"label" yaml:"label"`
    Description string `json:"description,omitempty" yaml:"description,omitempty"`
    Type AgentCollectionFieldType `json:"type" yaml:"type"`
    Required bool `json:"required" yaml:"required"`
    Enabled bool `json:"enabled" yaml:"enabled"`
    Order int `json:"order" yaml:"order"`
    Options []AgentCollectionOption `json:"options,omitempty" yaml:"options,omitempty"`
    Validation AgentCollectionValidation `json:"validation,omitempty" yaml:"validation,omitempty"`
    VisibleWhen *AgentCollectionCondition `json:"visible_when,omitempty" yaml:"visible_when,omitempty"`
}
```

Add to `CustomAgentConfig` with these JSON keys: `collection_enabled`, `collection_goal`, `collection_schema_version`, `collection_extract_from_messages`, `collection_extraction_threshold`, `collection_collect_optional_during_intake`, and `collection_fields`.

Normalize a zero threshold to `0.85`. Allow condition operators `equals`, `not_equals`, `contains`, `not_contains`, `is_set`, and `is_not_set`. Reject cycles and more than 100 enabled fields.

- [x] **Step 4: Enforce validation at save boundaries**

Both create and update call normalization and validation before repository writes. Increment `collection_schema_version` only when normalized field definitions change; goal text and extraction threshold changes do not create a schema version.

- [x] **Step 5: Run focused tests and verify GREEN**

```bash
go test ./internal/types ./internal/application/service -run 'Test.*AgentCollection|Test.*CustomAgent.*Collection' -count=1
```

Expected: all schema and save-boundary cases pass.

- [ ] **Step 6: Git checkpoint after authorization**

```bash
git add internal/types/custom_agent.go internal/types/agent_collection.go internal/types/agent_collection_test.go internal/application/service/custom_agent.go internal/application/service/custom_agent_collection_test.go
git commit -m "feat(agent): 增加信息采集配置模型"
```

### Task 2: Profile and append-only history persistence

**Files:**
- Create: `migrations/versioned/000067_agent_collection_profiles.up.sql`
- Create: `migrations/versioned/000067_agent_collection_profiles.down.sql`
- Create: `internal/types/interfaces/agent_collection.go`
- Create: `internal/application/repository/agent_collection.go`
- Create: `internal/application/repository/agent_collection_test.go`

- [x] **Step 1: Write the migration**

Create `agent_collection_profiles` with UUID string `id`, consumer `tenant_id`, `agent_id`, `user_id`, `schema_version`, JSONB `values`, JSONB `inactive_values`, `required_total`, `completed_required`, `is_complete`, `lock_version`, timestamps, and soft delete. Each `values[field_key]` is an object containing `value`, `updated_at`, `source`, and `source_message_id`; service helpers unwrap `value` for condition checks. Create `agent_collection_history` with profile and scope identifiers, `field_key`, schema version, old/new JSONB values, source, confidence, source message ID, actor user ID, change reason, and timestamp.

Create `agent_collection_exports` with ID, actor user ID, format, filter snapshot JSONB, status (`pending`, `running`, `completed`, `failed`), storage path, filename, row count, error message, timestamps, and expiry time. This table makes creation, failure, and download auditable without generating partial responses.

```sql
CREATE UNIQUE INDEX uq_agent_collection_profiles_live_scope
ON agent_collection_profiles (tenant_id, agent_id, user_id)
WHERE deleted_at IS NULL;

ALTER TABLE agent_collection_history
ADD CONSTRAINT chk_agent_collection_history_source
CHECK (source IN ('structured_answer', 'message_extraction', 'system_admin', 'schema_migration'));

CREATE INDEX idx_agent_collection_profiles_admin_list
ON agent_collection_profiles (updated_at DESC, tenant_id, agent_id, user_id)
WHERE deleted_at IS NULL;

CREATE INDEX idx_agent_collection_history_profile_created
ON agent_collection_history (profile_id, created_at DESC);

CREATE INDEX idx_agent_collection_exports_actor_created
ON agent_collection_exports (actor_user_id, created_at DESC);
```

The down migration drops exports, history, then profiles.

- [x] **Step 2: Write failing repository tests**

Cover create-on-first-write, optimistic update by `lock_version`, one history row per changed field, no history for identical canonical values, current/inactive JSON separation, cross-tenant filtered pagination, history order, and transactional rollback.

```go
type AgentCollectionRepository interface {
    GetProfile(ctx context.Context, tenantID uint64, agentID, userID string) (*types.AgentCollectionProfile, error)
    ApplyChanges(ctx context.Context, input types.ApplyCollectionChangesInput) (*types.AgentCollectionProfile, error)
    ListProfiles(ctx context.Context, filter types.AgentCollectionProfileFilter) (*types.AgentCollectionProfilePage, error)
    ListHistory(ctx context.Context, profileID string, page, pageSize int) (*types.AgentCollectionHistoryPage, error)
    CreateExport(ctx context.Context, export *types.AgentCollectionExport) error
    UpdateExport(ctx context.Context, export *types.AgentCollectionExport) error
    GetExport(ctx context.Context, exportID string) (*types.AgentCollectionExport, error)
    SoftDeleteByAgent(ctx context.Context, agentID string) error
    SoftDeleteByUser(ctx context.Context, userID string) error
    PurgeProfile(ctx context.Context, profileID string) error
}
```

- [x] **Step 3: Run repository tests and verify RED**

```bash
go test ./internal/application/repository -run 'TestAgentCollectionRepository' -count=1
```

Expected: compilation fails because the repository does not exist.

- [x] **Step 4: Implement transactional persistence**

`ApplyChanges` locks the live profile row with `FOR UPDATE`, canonicalizes JSON values before equality checks, appends history only for actual changes, increments `lock_version`, and commits profile plus history atomically. Retry a create/update unique race once by reloading the winner. For message extraction, compare source message creation time with the stored field update metadata so an older concurrent extraction cannot overwrite a newer explicit value. Bind every filter parameter.

- [x] **Step 5: Verify repository behavior**

```bash
go test ./internal/application/repository -run 'TestAgentCollectionRepository' -count=1
go test ./internal/types -run 'TestJSONMap' -count=1
```

Expected: repository cases and JSONB scanning pass.

- [ ] **Step 6: Git checkpoint after authorization**

```bash
git add migrations/versioned/000067_agent_collection_profiles.* internal/types/interfaces/agent_collection.go internal/application/repository/agent_collection*
git commit -m "feat(agent): 持久化用户采集档案与历史"
```

### Task 3: Reconciliation, conditions, and completion service

**Files:**
- Modify: `internal/types/agent_collection.go`
- Create: `internal/application/service/agent_collection.go`
- Create: `internal/application/service/agent_collection_test.go`
- Modify: `internal/application/service/custom_agent.go`
- Modify: `internal/application/service/user.go`

- [x] **Step 1: Write failing service tests**

Test first use, completed profile reuse, required versus optional intake, a controlling answer revealing another field, condition-evaluation failure falling back to unconditional required fields, schema-version additions, removed/disabled fields moving to `inactive_values`, invalid saved values becoming missing, completion recomputation, and user/agent deletion soft-deleting profiles.

```go
type AgentCollectionService interface {
    Prepare(ctx context.Context, input types.PrepareCollectionInput) (*types.PreparedCollection, error)
    ApplyStructuredAnswer(ctx context.Context, input types.StructuredCollectionAnswerInput) (*types.AgentCollectionProfile, error)
    ApplyExtractedValues(ctx context.Context, input types.ExtractedCollectionValuesInput) (*types.AgentCollectionProfile, error)
    UpdateAsSystemAdmin(ctx context.Context, input types.SystemAdminCollectionUpdateInput) (*types.AgentCollectionProfile, error)
    ListProfiles(ctx context.Context, filter types.AgentCollectionProfileFilter) (*types.AgentCollectionProfilePage, error)
    ListHistory(ctx context.Context, profileID string, page, pageSize int) (*types.AgentCollectionHistoryPage, error)
}
```

`PreparedCollection` contains `Profile`, ordered `VisibleFields`, ordered `MissingFields`, `CompletedCount`, and `RemainingCount`.

- [x] **Step 2: Run service tests and verify RED**

```bash
go test ./internal/application/service -run 'TestAgentCollectionService' -count=1
```

Expected: compilation fails because the service is missing.

- [x] **Step 3: Implement deterministic reconciliation**

On every `Prepare`: load or initialize the scoped profile, validate saved values, move disabled/removed/now-hidden values to `inactive_values`, restore a valid inactive value when its field becomes visible again, calculate visible missing fields, and persist source `schema_migration` only when reconciliation changes state.

```go
func intakeField(field types.AgentCollectionField, collectOptional bool) bool {
    return field.Enabled && (field.Required || collectOptional)
}
```

Re-run visibility and missing calculation after every answer; do not capture a fixed total at intake start.

- [x] **Step 4: Implement source-specific updates**

Structured answers use source `structured_answer` and confidence `1`. Extracted values update only configured enabled fields at or above the threshold with source `message_extraction`. System Admin updates validate against the current agent schema and use source `system_admin` plus actor ID.

- [x] **Step 5: Run service tests and verify GREEN**

Wire custom-agent and user deletion paths to `SoftDeleteByAgent` and `SoftDeleteByUser` after their owning row is successfully deleted. Keep history for audit until an explicit System Admin purge.

```bash
go test ./internal/application/service -run 'TestAgentCollectionService' -count=1
```

Expected: all reconciliation, condition, and completion cases pass.

### Task 4: Generalize structured input without breaking `ask_user`

**Files:**
- Modify: `internal/agent/userinput/types.go`
- Modify: `internal/agent/userinput/validation.go`
- Modify: `internal/agent/userinput/validation_test.go`
- Modify: `internal/agent/userinput/gate_test.go`
- Modify: `internal/agent/userinput/events.go`
- Modify: `internal/event/event_data.go`
- Modify: `internal/handler/user_input.go`
- Modify: `internal/handler/user_input_test.go`

- [x] **Step 1: Write failing compatibility tests**

Retain every legacy choice test. Add short text, long text, number, and ISO date questions; type-specific invalid values; required/skip behavior; dynamic counts above 10; and a submitted field key mismatch.

- [x] **Step 2: Run tests and verify RED**

```bash
go test ./internal/agent/userinput ./internal/handler -run 'Test.*UserInput|TestValidate' -count=1
```

Expected: new typed cases fail while legacy tests remain green.

- [x] **Step 3: Extend the protocol types**

```go
type Answer struct {
    FieldKey string `json:"field_key,omitempty"`
    SchemaVersion int64 `json:"schema_version,omitempty"`
    SelectedOptionIDs []string `json:"selected_option_ids,omitempty"`
    Value any `json:"value,omitempty"`
    OtherText string `json:"other_text,omitempty"`
    Skipped bool `json:"skipped"`
}

type Question struct {
    Text string `json:"question"`
    Mode Mode `json:"mode"`
    FieldKey string `json:"field_key,omitempty"`
    GroupID string `json:"question_group_id"`
    Index int `json:"question_index"`
    Total int `json:"question_total"`
    CompletedCount int `json:"completed_count,omitempty"`
    RemainingCount int `json:"remaining_count,omitempty"`
    Options []Option `json:"options,omitempty"`
    Validation types.AgentCollectionValidation `json:"validation,omitempty"`
    AllowOther bool `json:"allow_other"`
    AllowSkip bool `json:"allow_skip"`
}
```

Add modes `short_text`, `long_text`, `number`, and `date`. Preserve legacy `Index/Total`; replace the current maximum of 10 with the schema cap of 100. Choice modes require 2-8 options; non-choice modes require zero. Collection answers must match the pending question's trusted field key and schema version; legacy `ask_user` answers may leave both empty.

- [x] **Step 4: Emit and resolve the new fields**

Add `field_key`, `completed_count`, `remaining_count`, `validation`, and `value` to required/resolved event payloads. Keep every existing JSON name unchanged so old persisted cards and the Agent `ask_user` tool remain readable.

Persist pending collection questions in Redis with owner, tenant, session, trusted field/schema, payload, status, and TTL. Add `GET /api/v1/agent/user-inputs/pending?session_id=...` behind `g.Viewer()` to return only the caller's live pending question. On page refresh or SSE reconnect, the frontend can restore the same pending ID; first valid resolve wins and later submissions return conflict. Keep the live waiter local and use the existing Redis channel to wake its owning process.

- [x] **Step 5: Run compatibility tests and verify GREEN**

```bash
go test ./internal/agent/userinput ./internal/agent/tools ./internal/handler ./internal/handler/session -run 'Test.*UserInput|TestValidate|TestAskUser|TestAgentStreamUserInput' -count=1
```

Expected: old choice flows and all new typed flows pass.

### Task 5: Bounded natural-language extraction

**Files:**
- Create: `internal/application/service/agent_collection_extractor.go`
- Create: `internal/application/service/agent_collection_extractor_test.go`

- [x] **Step 1: Write failing extractor tests**

Use a fake `chat.Chat` to test strict JSON, fenced JSON, unknown fields, type mismatches, confidence filtering, enum ID validation, no-field short-circuit, model error, timeout, and malformed output. Errors must never fabricate updates.

```go
type CollectionExtraction struct {
    FieldKey string `json:"field_key"`
    Value any `json:"value"`
    Confidence float64 `json:"confidence"`
    Evidence string `json:"evidence"`
}

func ExtractCollectionValues(
    ctx context.Context,
    model chat.Chat,
    goal string,
    fields []types.AgentCollectionField,
    current types.JSONMap,
    message string,
) ([]CollectionExtraction, error)
```

- [x] **Step 2: Run tests and verify RED**

```bash
go test ./internal/application/service -run 'TestExtractCollectionValues' -count=1
```

Expected: compilation fails because the extractor is missing.

- [x] **Step 3: Implement constrained extraction**

Send only configured keys, labels, types, option IDs, current values, goal, and the current message. Require `{ "updates": [...] }`, a short verbatim evidence span for each update, and omission of uncertain, negated, inferred, or third-party values. Parse using the repository's structured-output pattern, reject unknown fields and evidence not present in the message, then call `ValidateCollectionValue` on each candidate.

```go
thinking := false
opts := &chat.ChatOptions{Temperature: 0, MaxCompletionTokens: 800, Thinking: &thinking}
```

Use an 8-second child context. A timeout or model failure is returned to the coordinator, which logs it and continues deterministic collection.

- [x] **Step 4: Run tests and verify GREEN**

```bash
go test ./internal/application/service -run 'TestExtractCollectionValues' -count=1
```

Expected: parser, safety, and failure-isolation cases pass.

### Task 6: Collect missing fields before Agent execution and resume

**Files:**
- Create: `internal/application/service/session_agent_collection.go`
- Create: `internal/application/service/session_agent_collection_test.go`
- Modify: `internal/application/service/session.go`
- Modify: `internal/application/service/session_agent_qa.go`

- [x] **Step 1: Write failing orchestration tests**

Test Web first-use sequence, extraction before missing calculation, a condition revealing another field, completed-profile reuse without popup, answer persistence then Agent continuation, optional skip, required skip rejection, non-Web behavior, timeout/cancel stopping before Agent, extractor error fallback, page-refresh pending recovery without duplicate creation, simultaneous-answer first-writer behavior, and more than three dynamically generated questions.

```go
func (s *sessionService) prepareAgentCollection(
    ctx context.Context,
    req *types.QARequest,
    model chat.Chat,
    eventBus *event.EventBus,
) error
```

- [x] **Step 2: Run tests and verify RED**

```bash
go test ./internal/application/service -run 'TestSessionAgentCollection' -count=1
```

Expected: compilation fails because the coordinator and dependencies are missing.

- [x] **Step 3: Wire session dependencies**

Add `interfaces.AgentCollectionService` and `userinput.Requester` to `NewSessionService` and the struct. Keep nil-safe behavior when collection is disabled.

- [x] **Step 4: Implement the dynamic collection loop**

For enabled Web collection: scope by consumer `req.Session.TenantID`, agent ID, and session owner. Extract from `req.Query` first when enabled, passing the persisted user-message ID and creation time, call `Prepare`, then request one first-ordered missing field. Reuse an existing live pending record for the same profile, schema, and field. After an answer, persist it and call `Prepare` again so conditions and counts are recalculated.

```go
question.CompletedCount = prepared.CompletedCount
question.RemainingCount = prepared.RemainingCount
question.Index = prepared.CompletedCount + 1
question.Total = prepared.CompletedCount + prepared.RemainingCount
```

Use a stable group ID from profile ID and schema version. Do not inject profile values or a profile summary into user-visible Agent output.

- [x] **Step 5: Insert the AgentQA boundary call**

Resolve the chat model first, then call `prepareAgentCollection` before history loading and `CreateAgentEngine`. Return on canceled/timed-out required intake; otherwise continue through the existing execution path unchanged.

- [x] **Step 6: Run service and stream tests**

```bash
go test ./internal/application/service ./internal/agent/userinput ./internal/handler/session -run 'TestSessionAgentCollection|TestAgentStreamUserInput|TestGate' -count=1
```

Expected: collection pauses and resumes the same request and existing SSE behavior passes.

- [ ] **Step 7: Git checkpoint after authorization**

```bash
git add internal/application/service/agent_collection* internal/application/service/session_agent_collection* internal/application/service/session.go internal/application/service/session_agent_qa.go internal/agent/userinput internal/event/event_data.go internal/handler/user_input*
git commit -m "feat(agent): 动态采集并更新用户信息"
```

### Task 7: System Admin list, history, edit, and export API

**Files:**
- Create: `internal/handler/system_agent_collection.go`
- Create: `internal/handler/system_agent_collection_test.go`
- Modify: `internal/router/router.go`
- Create: `internal/router/router_agent_collection_test.go`
- Modify: `internal/container/container.go`

- [x] **Step 1: Write failing handler and route tests**

Cover summary counts, list filters (`tenant_id`, `agent_id`, `user_id`, keyword, completion, updated range, `field_key`, and `field_value`), pagination bounds, detail/history, admin edit reason requirements, valid/invalid edits, compliance purge confirmation/reason, export creation/status/download, CSV/XLSX headers and filenames, failed/empty exports, and service errors. Prove System Admin succeeds while tenant Owner/Admin is forbidden on every endpoint.

- [x] **Step 2: Run tests and verify RED**

```bash
go test ./internal/handler ./internal/router -run 'Test.*AgentCollection' -count=1
```

Expected: compilation fails because the handler and routes are missing.

- [x] **Step 3: Implement the API surface**

Register inside `/system/admin` with the group-level `g.SystemAdmin()` guard:

```text
GET  /collection-profiles
GET  /collection-profiles/:profile_id
GET  /collection-profiles/:profile_id/history
PUT  /collection-profiles/:profile_id/fields/:field_key
DELETE /collection-profiles/:profile_id
POST /collection-profiles/export
GET  /collection-exports/:export_id
```

List returns summary counts (`users`, `profiles`, `updated_today`, `incomplete`), current configured fields plus tenant, agent, user, completion, and timestamps. Detail adds inactive values and schema metadata. History is paginated newest-first. Field edits require a non-empty reason, store the acting admin ID, and write a platform audit row. DELETE is an explicit compliance purge requiring a typed confirmation and reason; it removes profile/history transactionally and writes an audit entry containing identifiers but no deleted field values.

- [x] **Step 4: Implement streaming exports**

The POST body contains `format` and a validated filter snapshot. Create a pending export row, generate in a bounded background job, write to the configured file service, then atomically mark completed or failed; never expose a partial file. CSV uses `encoding/csv` with UTF-8 BOM. XLSX uses the existing `github.com/xuri/excelize/v2`. Generate columns from configured keys in stable field order, join multiple choices with `; `, use ISO dates, apply list filters, exclude lock versions, and reject exports above 100,000 profiles. GET returns status JSON while pending/failed and downloads the completed file only to the creating System Admin. Audit detail view, edit, export creation, and download.

- [x] **Step 5: Register dependencies and verify permissions**

Provide repository, service, and handler in `internal/container/container.go`; extend router dependency parameters and mount only in the System Admin group.

```bash
go test ./internal/handler ./internal/router ./internal/container -run 'Test.*AgentCollection|Test.*Container' -count=1
```

Expected: mapping, exports, and System Admin enforcement pass.

### Task 8: Agent editor schema builder

**Files:**
- Modify: `frontend/src/api/agent/index.ts`
- Create: `frontend/src/views/agent/agentCollectionConfig.ts`
- Create: `frontend/src/views/agent/agentCollectionConfig.test.ts`
- Create: `frontend/src/views/agent/components/AgentCollectionConfig.vue`
- Modify: `frontend/src/views/agent/AgentEditorModal.vue`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`

- [x] **Step 1: Write failing config helper tests**

Test defaults, unique normalized keys, option requirements, condition target filtering, cycle detection, sensitive labels, 100-field maximum, deterministic ordering, and schema-version-neutral serialization.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd frontend && npm test -- src/views/agent/agentCollectionConfig.test.ts
```

Expected: module-not-found failure for the new helper.

- [x] **Step 3: Add TypeScript contracts and pure helpers**

Mirror every backend JSON key and export:

```ts
export type AgentCollectionFieldType =
  | 'single_choice' | 'multiple_choice' | 'short_text'
  | 'long_text' | 'number' | 'date'

export function normalizeCollectionFields(fields: AgentCollectionField[]): AgentCollectionField[]
export function validateCollectionConfig(config: CustomAgentConfig): string[]
export function nextCollectionFieldKey(fields: AgentCollectionField[]): string
```

- [x] **Step 4: Build the focused editor component**

Add an enable switch, goal textarea, extraction switch and threshold input, optional-during-intake switch, and ordered field list. Each field supports add, copy, reorder, disable, label, stable key, type, required/enabled toggles, description, type-specific validation, choice options, and one visibility condition. Include a live question-card preview and a publish-validation summary. Show inline errors and disable Agent save while invalid.

- [x] **Step 5: Mount in `AgentEditorModal`**

Add an “信息采集” section bound to `formData.config`, initialize defaults for older agents, and retain the existing built-in-agent read-only rules.

- [x] **Step 6: Run tests and type-check**

```bash
cd frontend && npm test -- src/views/agent/agentCollectionConfig.test.ts && npm run type-check
```

Expected: helper tests pass and Vue/TypeScript reports no errors.

### Task 9: Typed question card and remaining progress

**Files:**
- Modify: `frontend/src/utils/structuredQuestion.ts`
- Modify: `frontend/src/utils/structuredQuestion.test.ts`
- Modify: `frontend/src/utils/structuredQuestionEvents.ts`
- Modify: `frontend/src/utils/structuredQuestionEvents.test.ts`
- Modify: `frontend/src/api/user-input.ts`
- Modify: `frontend/src/composables/useChatStreamHandler.ts`
- Modify: `frontend/src/views/chat/components/StructuredQuestionCard.vue`
- Modify: `frontend/src/views/chat/components/structured-question-card.less`
- Modify: `frontend/src/views/chat/components/StructuredQuestionCard.style.test.mjs`

- [x] **Step 1: Write failing answer and progress tests**

Cover short/long text trimming and limits, finite bounded numbers, ISO dates, legacy choices, resolved value hydration, failed-save input retention, restored pending questions after refresh, and progress preferring server `remaining_count` over static index/total arithmetic.

- [x] **Step 2: Run tests and verify RED**

```bash
cd frontend && npm test -- src/utils/structuredQuestion.test.ts src/utils/structuredQuestionEvents.test.ts src/views/chat/components/StructuredQuestionCard.style.test.mjs
```

Expected: typed cases fail because the current contract supports choices only.

- [x] **Step 3: Extend event and answer state**

Add `field_key`, `value`, `validation`, `completed_count`, and `remaining_count`. `buildStructuredAnswer` returns `{ selected_option_ids, value, other_text, skipped }`, with `value` used only by non-choice modes.

```ts
const completed = event.completed_count ?? Math.max(0, event.question_index - 1)
const remaining = event.remaining_count ?? Math.max(0, event.question_total - event.question_index + 1)
```

Render “已完成 {completed} 项 / 剩余 {remaining} 个问题”. Conditions may change either number on the next event.

Add `getPendingUserInput(sessionId)` to `frontend/src/api/user-input.ts`. When a Web chat is restored or the event stream reconnects, merge the owner-scoped pending payload through the same `structuredQuestionEvents` reducer; deduplicate by pending ID so reconnect never creates a second card.

- [x] **Step 4: Render six stable controls**

Keep radio/checkbox choices. Use `t-input`, `t-textarea`, `t-input-number`, and a date picker for the other types. Preserve loading, resolved, expired, focus, mobile wrapping, and fixed control dimensions so validation does not shift the card.

- [x] **Step 5: Run UI tests and type-check**

```bash
cd frontend && npm test -- src/utils/structuredQuestion.test.ts src/utils/structuredQuestionEvents.test.ts src/views/chat/components/StructuredQuestionCard.style.test.mjs && npm run type-check
```

Expected: typed payload, event merge, progress, and style invariants pass.

### Task 10: System Admin profile management UI

**Files:**
- Create: `frontend/src/api/system/agent-collection.ts`
- Create: `frontend/src/views/system/agentCollectionProfiles.ts`
- Create: `frontend/src/views/system/agentCollectionProfiles.test.ts`
- Create: `frontend/src/views/system/AgentCollectionProfiles.vue`
- Modify: `frontend/src/views/settings/Settings.vue`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`

- [x] **Step 1: Write failing data-shaping tests**

Test query serialization, empty-filter removal, page normalization, configured-column ordering, multi-choice display, CSV/XLSX filename parsing, completion labels, and history value formatting.

- [ ] **Step 2: Run tests and verify RED**

```bash
cd frontend && npm test -- src/views/system/agentCollectionProfiles.test.ts
```

Expected: module-not-found failure for the new helper.

- [x] **Step 3: Implement the typed API client**

Expose list, detail, history, update, and download under `/api/v1/system/admin/collection-profiles`. Use the shared request wrapper for JSON and blob responses for exports; parse `Content-Disposition` with a date-based fallback filename.

- [x] **Step 4: Build the admin surface**

Create summary counters for users, profiles, today's updates, and incomplete profiles, followed by a dense table with tenant, agent, user, completion, last update, and configured fields. Add tenant/agent/user/completion/date and field-key/value filters, pagination, CSV/XLSX icon buttons with tooltips, a current/inactive-value detail drawer, paginated history, a type-aware edit dialog requiring a change reason, and a danger-zone compliance purge with typed confirmation and reason. Export buttons create jobs, poll status, and download only completed files. Do not expose the section to tenant admins.

- [x] **Step 5: Add settings navigation and deep link**

Add `system-collection` only for `authStore.isSystemAdmin`, render `AgentCollectionProfiles`, include it in full-width rules, and redirect `/platform/system/collection-profiles` to `/platform/settings?section=system-collection` with `requiresSystemAdmin: true`.

- [x] **Step 6: Run tests, type-check, and build**

```bash
cd frontend && npm test -- src/views/system/agentCollectionProfiles.test.ts && npm run type-check && npm run build
```

Expected: tests and type-check pass and Vite builds successfully.

- [ ] **Step 7: Git checkpoint after authorization**

```bash
git add frontend/src/api/agent/index.ts frontend/src/api/system/agent-collection.ts frontend/src/views/agent frontend/src/views/chat/components/StructuredQuestionCard.vue frontend/src/views/chat/components/structured-question-card.less frontend/src/views/system/AgentCollectionProfiles.vue frontend/src/views/system/agentCollectionProfiles* frontend/src/views/settings/Settings.vue frontend/src/router/index.ts frontend/src/utils/structuredQuestion* frontend/src/i18n/locales
git commit -m "feat(frontend): 增加动态采集与管理界面"
```

### Task 11: End-to-end verification and documentation sync

**Files:**
- Modify only if implementation decisions change: `docs/superpowers/specs/2026-07-22-agent-information-collection-design.md`
- Modify checkbox states only: `docs/superpowers/plans/2026-07-22-agent-information-collection.md`

- [ ] **Step 1: Run backend regression**

```bash
go test ./internal/types ./internal/agent/userinput ./internal/application/repository ./internal/application/service ./internal/handler ./internal/handler/session ./internal/router ./internal/container -count=1
```

Expected: all listed packages pass.

- [x] **Step 2: Run race tests**

```bash
go test -race ./internal/agent/userinput ./internal/application/repository ./internal/application/service -run 'TestGate|TestAgentCollection' -count=1
```

Expected: pass with no race report.

- [x] **Step 3: Run frontend regression**

```bash
cd frontend && npm test && npm run type-check && npm run build
```

Expected: Node tests, Vue type-check, and production build all exit 0.

- [ ] **Step 4: Apply migration and smoke-test APIs**

Use the repository's documented migration/start command for the active environment. Verify one profile is created, each actual change creates one history row, repeat use does not ask valid fields, tenant Admin gets 403, and System Admin gets 200 from list and export.

- [ ] **Step 5: Verify browser workflows**

At desktop and mobile widths verify schema creation; all six question types; completed/remaining progress; conditional remaining-count changes; Agent resume after the final answer; silent later-message update; System Admin filters, edit, history, CSV, and XLSX; and tenant Admin invisibility/deep-link rejection.

- [x] **Step 6: Inspect the final diff**

```bash
git diff --check
git status --short
git diff --stat
```

Expected: no whitespace errors, task files are present, and unrelated dirty-worktree changes remain untouched.

- [ ] **Step 7: Final Git checkpoint after authorization**

```bash
git add docs/superpowers/specs/2026-07-22-agent-information-collection-design.md docs/superpowers/plans/2026-07-22-agent-information-collection.md
git commit -m "docs(agent): 同步信息采集设计与验证"
```
