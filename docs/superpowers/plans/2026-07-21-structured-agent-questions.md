# Structured Agent Questions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Web-only `ask_user` Agent tool that pauses the live run, renders a structured single-choice or multiple-choice question with remaining-question progress, and resumes the same run after the signed-in user answers or skips.

**Architecture:** Introduce an isolated `internal/agent/userinput` wait/resolve gate modeled on the existing approval gate, then connect it to a new Agent tool, SSE event types, an authenticated resolve endpoint, and a focused Vue question card. Pending state remains in memory; Redis Pub/Sub only routes cross-instance answers, and no database migration or restart recovery is added.

**Tech Stack:** Go, Gin, dig, Redis Pub/Sub, existing Agent EventBus/StreamManager, Vue 3, TypeScript, TDesign Vue Next, Node test runner, Go test.

**Execution note:** Work in the current dirty workspace without Git history operations. Do not modify or revert unrelated user changes. Each task uses TDD and ends with a local verification checkpoint instead of a commit.

---

### Task 1: User-input gate and validated domain contract

**Files:**
- Create: `internal/agent/userinput/types.go`
- Create: `internal/agent/userinput/validation.go`
- Create: `internal/agent/userinput/gate.go`
- Create: `internal/agent/userinput/gate_test.go`
- Create: `internal/agent/userinput/validation_test.go`
- Modify: `internal/event/event.go`
- Modify: `internal/event/event_data.go`

- [x] **Step 1: Write failing validation tests**

Cover valid single/multiple questions and rejection of invalid mode, progress, group/option IDs, duplicate option IDs, option counts, and rune-length limits. Use table-driven tests against:

```go
func ValidateQuestion(q Question) error
func ValidateAnswer(q Question, answer Answer) error
```

- [x] **Step 2: Run validation tests and verify RED**

Run:

```bash
go test ./internal/agent/userinput -run 'TestValidate' -count=1
```

Expected: compilation failure because the package contract does not exist.

- [x] **Step 3: Implement question and answer validation**

Define:

```go
type Mode string

const (
    ModeSingle   Mode = "single_choice"
    ModeMultiple Mode = "multiple_choice"
)

type Option struct {
    ID          string `json:"id"`
    Label       string `json:"label"`
    Description string `json:"description,omitempty"`
}

type Question struct {
    Text            string   `json:"question"`
    Mode            Mode     `json:"mode"`
    GroupID         string   `json:"question_group_id"`
    Index           int      `json:"question_index"`
    Total           int      `json:"question_total"`
    Options         []Option `json:"options"`
    AllowOther      bool     `json:"allow_other"`
    AllowSkip       bool     `json:"allow_skip"`
}

type Answer struct {
    SelectedOptionIDs []string `json:"selected_option_ids"`
    OtherText         string   `json:"other_text,omitempty"`
    Skipped           bool     `json:"skipped"`
}
```

Validate all limits from the design document using rune counts and a compiled identifier regexp.

- [x] **Step 4: Run validation tests and verify GREEN**

Run the Step 2 command. Expected: all validation cases pass.

- [x] **Step 5: Write failing gate lifecycle tests**

Test request/answer, skip, timeout, context cancellation, duplicate resolution, tenant mismatch, user mismatch, and emitted required/resolved events. Construct an EventBus and capture emitted typed data.

- [x] **Step 6: Run gate tests and verify RED**

Run:

```bash
go test ./internal/agent/userinput -run 'TestGate' -count=1
```

Expected: failures because `Gate.RequestAndWait` and `Gate.Resolve` are missing.

- [x] **Step 7: Implement the isolated gate**

Expose:

```go
type Requester interface {
    RequestAndWait(context.Context, PendingRequest) (Result, error)
}

func NewGate(cfg *config.Config, rdb *redis.Client) *Gate
func (g *Gate) RequestAndWait(ctx context.Context, req PendingRequest) (Result, error)
func (g *Gate) Resolve(tenantID uint64, userID, pendingID string, answer Answer) error
```

Use a mutex-protected waiter map, `sync.Once` delivery, timeout/cancel branches, a dedicated namespaced Redis channel, cross-instance acknowledgement, and explicit sentinel errors for not found, already resolved, tenant mismatch, user mismatch, and unavailable owner. Add the required/resolved EventBus constants and payload structs used by the Gate; Task 2 maps those events into the SSE stream.

- [x] **Step 8: Run package tests and verify GREEN**

Run:

```bash
go test ./internal/agent/userinput -count=1
```

Expected: all tests pass without goroutine leaks or races in the tested paths.

### Task 2: Agent event and stream protocol

**Files:**
- Modify: `internal/event/event.go`
- Modify: `internal/event/event_data.go`
- Modify: `internal/types/chat.go`
- Modify: `internal/handler/session/agent_stream_handler.go`
- Create: `internal/handler/session/agent_stream_user_input_test.go`

- [x] **Step 1: Write failing stream mapping tests**

Create an AgentStreamHandler with a fake StreamManager, emit `user_input_required` and `user_input_resolved`, and assert persisted stream events contain the documented response type and metadata, including `question_index`, `question_total`, and resolved status.

- [x] **Step 2: Run stream tests and verify RED**

Run:

```bash
go test ./internal/handler/session -run 'TestAgentStreamUserInput' -count=1
```

Expected: compilation failure because event and response types are missing.

- [x] **Step 3: Complete event types and payloads introduced by Task 1**

Verify Task 1 added:

```go
EventUserInputRequired EventType = "user_input_required"
EventUserInputResolved EventType = "user_input_resolved"
```

Define required/resolved data structs in `event_data.go` with the exact JSON-facing fields from the design.

- [x] **Step 4: Subscribe and persist user-input events**

Add response constants to `internal/types/chat.go`, subscribe in `AgentStreamHandler.Subscribe`, and append the two events through StreamManager using the same conversion pattern as approval/OAuth events.

- [x] **Step 5: Run stream tests and verify GREEN**

Run the Step 2 command. Expected: both mappings pass.

### Task 3: `ask_user` tool and Web-only runtime registration

**Files:**
- Modify: `internal/types/qa_request.go`
- Modify: `internal/types/agent.go`
- Modify: `internal/handler/session/qa.go`
- Modify: `internal/application/service/session_agent_qa.go`
- Modify: `internal/application/service/agent_service.go`
- Modify: `internal/agent/tools/definitions.go`
- Modify: `internal/agent/tools/exec_context.go`
- Modify: `internal/agent/act.go`
- Create: `internal/agent/tools/ask_user.go`
- Create: `internal/agent/tools/ask_user_test.go`
- Create: `internal/application/service/agent_user_input_registration_test.go`

- [x] **Step 1: Write failing tool tests**

Use a fake `userinput.Requester` and test valid answer, skip, timeout, cancellation, missing execution metadata, and invalid JSON. Assert the result is structured JSON and contains selected IDs and labels.

- [x] **Step 2: Run tool tests and verify RED**

Run:

```bash
go test ./internal/agent/tools -run 'TestAskUser' -count=1
```

Expected: compilation failure because `AskUserTool` is missing.

- [x] **Step 3: Implement `AskUserTool`**

Add `ToolAskUser = "ask_user"`, a JSON schema generated from a typed input, and a tool description that limits use to materially blocking missing information. In `Execute`, load tenant/user/session/EventBus metadata, switch from the normal tool timeout to `ApprovalCtx`, call the gate, and return a compact JSON result.

- [x] **Step 4: Resolve request ID from the inherited execution context**

Populate `ToolExecContext.RequestID` from `types.RequestIDFromContext(ctx)` in `act.go`, preserving existing approval and OAuth behavior.

- [x] **Step 5: Run tool tests and verify GREEN**

Run the Step 2 command. Expected: all tool cases pass.

- [x] **Step 6: Write failing Web-only registration tests**

Test that a runtime request with `Channel: "web"` sets `InteractiveUserInputEnabled`, and `Channel: "im"` or empty leaves it false. Test registry contents contain `ask_user` only when enabled.

- [x] **Step 7: Run registration tests and verify RED**

Run:

```bash
go test ./internal/application/service -run 'Test.*UserInput' -count=1
```

Expected: failures because channel propagation and registration are missing.

- [x] **Step 8: Propagate channel and register the tool**

Add `Channel` to `QARequest`, set it in `qaRequestContext.buildQARequest`, and add runtime-only `InteractiveUserInputEnabled` to `AgentConfig`. Set it only for `channel == "web"`. Inject `userinput.Requester` into AgentService and register `ask_user` independently of the custom agent `AllowedTools` whitelist when interactive input is enabled.

- [x] **Step 9: Run registration and affected service tests**

Run:

```bash
go test ./internal/application/service ./internal/agent/tools -run 'Test.*UserInput|TestAskUser' -count=1
```

Expected: all targeted tests pass.

### Task 4: Resolve API, dependency injection, and routing

**Files:**
- Create: `internal/handler/user_input.go`
- Create: `internal/handler/user_input_test.go`
- Modify: `internal/container/container.go`
- Modify: `internal/router/router.go`
- Create: `internal/router/router_user_input_test.go`

- [x] **Step 1: Write failing handler tests**

Test success, malformed payload, invalid selection, unauthenticated principal, tenant mismatch, user mismatch, missing pending request, duplicate resolution, and unavailable owner. Use a fake resolver so HTTP mapping is deterministic.

- [x] **Step 2: Run handler tests and verify RED**

Run:

```bash
go test ./internal/handler -run 'TestUserInputHandler' -count=1
```

Expected: compilation failure because the handler is missing.

- [x] **Step 3: Implement the answer handler**

Create `UserInputHandler.Resolve`, bind the documented answer body, read tenant and authenticated principal from context, invoke the gate, and map sentinel errors to `400`, `403`, `404`, `409`, or `503` without leaking internal Redis details.

- [x] **Step 4: Run handler tests and verify GREEN**

Run the Step 2 command. Expected: all status mappings pass.

- [x] **Step 5: Write failing route/DI tests**

Assert the authenticated `POST /api/v1/agent/user-inputs/:pending_id` route exists and that container construction can provide one shared Gate to AgentService and UserInputHandler.

- [x] **Step 6: Wire Gate, handler, and route**

Provide `userinput.NewGate`, expose it as `userinput.Requester`, provide `handler.NewUserInputHandler`, add it to `RouterParams`, and register the Viewer-protected endpoint alongside existing interactive Agent routes. Do not expose it to API-key or embed routes.

- [x] **Step 7: Run route and container tests**

Run:

```bash
go test ./internal/router ./internal/container -run 'Test.*UserInput' -count=1
```

Expected: route and dependency graph tests pass.

### Task 5: Frontend answer model, API, and question card

**Files:**
- Create: `frontend/src/api/user-input.ts`
- Create: `frontend/src/utils/structuredQuestion.ts`
- Create: `frontend/src/utils/structuredQuestion.test.ts`
- Create: `frontend/src/views/chat/components/StructuredQuestionCard.vue`
- Create: `frontend/src/views/chat/components/StructuredQuestionCard.style.test.mjs`

- [x] **Step 1: Write failing pure-state tests**

Cover remaining count, valid single/multiple answers, Other text, skip payloads, invalid mixed skip payloads, and required selection behavior through pure functions:

```ts
remainingQuestionCount(index: number, total: number): number
buildStructuredAnswer(state: StructuredQuestionState): StructuredAnswer | null
```

- [x] **Step 2: Run frontend state tests and verify RED**

Run:

```bash
cd frontend && npm test -- src/utils/structuredQuestion.test.ts
```

Expected: module-not-found failure.

- [x] **Step 3: Implement types, helpers, and resolve API**

Define strict event/answer types, clamp the remaining count at zero, construct only valid payloads, and add:

```ts
resolveUserInput(pendingId: string, answer: StructuredAnswer): Promise<void>
```

posting to `/api/v1/agent/user-inputs/:pending_id`.

- [x] **Step 4: Run state tests and verify GREEN**

Run the Step 2 command. Expected: all helper tests pass.

- [x] **Step 5: Write failing component structure/style tests**

Add source-level tests matching existing style-test conventions for stable card dimensions, wrapping option text, mobile action layout, focus-visible rules, no nested decorative card, and localized remaining-count rendering.

- [x] **Step 6: Implement `StructuredQuestionCard.vue`**

Use TDesign radio/checkbox/input/button controls. Support selecting predefined options, toggling Other, skipping, submit locking, inline API errors, resolved summaries, terminal timeout/cancel states, focus on first option, and an emitted local-resolved signal only after the API succeeds.

- [x] **Step 7: Run component tests and type-check**

Run:

```bash
cd frontend && npm test -- src/utils/structuredQuestion.test.ts src/views/chat/components/StructuredQuestionCard.style.test.mjs
npm run type-check
```

Expected: tests and Vue TypeScript checks pass.

### Task 6: Frontend SSE integration and localization

**Files:**
- Modify: `frontend/src/views/chat/components/AgentStreamDisplay.vue`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`
- Create: `frontend/src/utils/structuredQuestionEvents.ts`
- Create: `frontend/src/utils/structuredQuestionEvents.test.ts`

- [x] **Step 1: Write failing event-reconciliation tests**

Test that a required event produces one stable card, a resolved event updates the matching card rather than duplicating it, and replayed events preserve answered/skipped/timed-out/canceled status.

- [x] **Step 2: Run reconciliation tests and verify RED**

Run:

```bash
cd frontend && npm test -- src/utils/structuredQuestionEvents.test.ts
```

Expected: module-not-found failure.

- [x] **Step 3: Implement event reconciliation**

Create a pure `reconcileStructuredQuestionEvents` helper keyed by `pending_id`, then call it from the existing full-event-list construction path. Add `question-${pending_id}` to stable key generation.

- [x] **Step 4: Render the card in both Agent timeline branches**

Import `StructuredQuestionCard`, render `user_input_required` beside approval/OAuth cards in collapsed and streaming modes, and suppress standalone resolved events after they have updated the required card.

- [x] **Step 5: Add Chinese and English labels**

Add localized strings for mode, progress, remaining count, Other, Skip, Continue, submitting, answered, skipped, timed out, canceled, retry guidance, and API failure.

- [x] **Step 6: Run frontend tests, type-check, and build**

Run:

```bash
cd frontend
npm test -- src/utils/structuredQuestion.test.ts src/utils/structuredQuestionEvents.test.ts src/views/chat/components/StructuredQuestionCard.style.test.mjs
npm run type-check
npm run build-only
```

Expected: all commands exit successfully.

### Task 7: Backend integration, regression, and live browser verification

**Files:**
- Create: `internal/agent/userinput/integration_test.go`
- Modify only if a verified defect requires it: files from Tasks 1–6

- [x] **Step 1: Write the end-to-end backend integration test**

Execute the real `ask_user` tool against the real Gate, wait for the required event, verify the tool remains blocked, resolve the pending request, then assert the same call returns the structured result and emits the resolved event.

- [x] **Step 2: Run the integration test and fix only observed failures**

Run:

```bash
go test ./internal/agent/userinput ./internal/agent/tools ./internal/application/service ./internal/handler ./internal/handler/session ./internal/router ./internal/container -run 'Test.*UserInput|TestAskUser' -count=1
```

Expected: all structured-question tests pass.

- [ ] **Step 3: Run broader backend regression tests**

Run:

```bash
go test ./internal/agent/... ./internal/handler/... ./internal/router ./internal/container ./internal/application/service/... -count=1
```

Expected: zero failures. Any unrelated pre-existing failure must be recorded with its exact package and output.

Observed: all requested packages except `internal/handler` passed. The unrelated existing
`TestPutTenantParserConfigAdminPreservesRedactedSecrets` test reproducibly expected HTTP 200
but received 400; none of the structured-question files participate in that code path.

- [x] **Step 4: Build and restart the local service using the repository-supported path**

Build only after targeted and regression tests pass. Preserve the existing database and unrelated containers. Verify:

```bash
docker exec WeKnora-app curl -fsS http://localhost:8080/health
```

Expected: `{"status":"ok"}`.

- [x] **Step 5: Run a live Agent question/answer flow**

Use a test prompt that reliably asks one structured question, verify SSE contains `user_input_required`, submit an answer through the UI, and verify the same stream continues to `user_input_resolved`, tool result, answer, and complete without opening a second chat request.

- [x] **Step 6: Verify desktop and mobile UI**

Capture Playwright screenshots at desktop and mobile widths. Confirm the mode label, `1 / N`, remaining count, option wrapping, Other input, Skip/Continue controls, resolved summary, keyboard focus, and absence of overlap or horizontal scrolling.

Observed: the question card itself has no internal overflow at 390px (`scrollWidth == clientWidth`),
but the pre-existing application shell retains its 260px sidebar and makes the full document 600px wide.
Desktop pending/resolved states and mobile resolved layout were captured; the shell-level mobile overflow
is outside this feature's component boundary.

- [x] **Step 7: Final completion gate**

Run fresh backend targeted tests, frontend tests/type-check/build, container health, and `git diff --check`. Report changed files, verification evidence, known unsupported refresh/restart behavior, and unrelated dirty-worktree changes. Do not commit or push without explicit user approval.
