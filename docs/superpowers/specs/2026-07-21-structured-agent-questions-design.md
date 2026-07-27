# Structured Agent Questions Design

## 1. Goal

Add an interactive `ask_user` tool to Web Agent conversations. The tool pauses the current Agent run, sends a structured single-choice or multiple-choice question over SSE, accepts the signed-in user's answer, returns that answer as the tool result, and lets the same Agent run continue.

The experience should support one question per tool call, optional free-form "Other" input, skipping, and visible progress such as `1 / 3` and `2 questions remaining`.

## 2. Scope

### Included

- A globally available `ask_user` tool for Web Agent conversations.
- One question per tool call.
- Single-choice and multiple-choice modes.
- Between 2 and 8 predefined options.
- Optional "Other" free-form input.
- A skip action.
- Question-group progress with a maximum of 10 questions.
- A blocking in-memory wait that resumes the current Agent execution.
- Redis Pub/Sub routing for multi-instance deployments.
- SSE required/resolved events and stream replay data.
- A responsive frontend question card.
- Tenant, session-owner, and pending-request authorization.
- Backend, frontend, and end-to-end tests.

### Excluded

- Resuming after a browser refresh, lost connection, process restart, or deployment.
- Persisting Agent checkpoints or pending questions in the database.
- Batch question forms containing multiple questions in one tool call.
- Structured questions in IM, API-only, or embedded channels without a live Web client.
- Changes to existing MCP approval or MCP OAuth behavior.

## 3. Architecture Decision

Implement a dedicated user-input gate rather than generalizing the existing MCP approval gate. The new gate follows the established wait/resolve, authorization, timeout, and Redis fan-out patterns while keeping the security-sensitive approval paths unchanged.

The end-to-end flow is:

1. The model calls `ask_user` during a Web Agent run.
2. The tool validates its structured arguments.
3. The user-input gate creates a pending request and emits `user_input_required`.
4. The Agent goroutine blocks while the original SSE response remains open.
5. The frontend renders a structured question card.
6. The signed-in user submits or skips the question.
7. The answer endpoint resolves the pending request locally or through Redis Pub/Sub.
8. The gate emits `user_input_resolved` and returns a structured tool result.
9. The Agent consumes the result and continues its current execution.

## 4. Tool Contract

The registered tool name is `ask_user`.

### Input

```json
{
  "question": "How did the company notify you of the dismissal?",
  "mode": "single_choice",
  "question_group_id": "dismissal-facts",
  "question_index": 1,
  "question_total": 3,
  "options": [
    {
      "id": "written_with_reason",
      "label": "Written notice with a reason",
      "description": "The company issued a written termination notice and stated a reason"
    },
    {
      "id": "verbal_notice",
      "label": "Verbal notice",
      "description": "A manager or HR only communicated the dismissal verbally"
    }
  ],
  "allow_other": true,
  "allow_skip": true
}
```

### Validation

- `question` is required and limited to 500 Unicode characters.
- `mode` must be `single_choice` or `multiple_choice`.
- `question_group_id` is required, stable within a question sequence, and limited to 64 ASCII letters, digits, hyphens, and underscores.
- `question_index` starts at 1.
- `question_total` is between 1 and 10.
- `question_index` cannot exceed `question_total`.
- `options` contains 2 to 8 entries.
- Option IDs are unique within the question and follow the same identifier character rules.
- Option labels are required and limited to 120 Unicode characters.
- Option descriptions are optional and limited to 300 Unicode characters.
- `allow_other` and `allow_skip` default to `true` when omitted.

Invalid arguments return a compact tool error that tells the model which field to correct. They do not create a pending request.

### Result

Answered result:

```json
{
  "status": "answered",
  "question_group_id": "dismissal-facts",
  "question_index": 1,
  "question_total": 3,
  "selected_options": [
    {
      "id": "written_with_reason",
      "label": "Written notice with a reason"
    }
  ],
  "other_text": ""
}
```

Other terminal statuses are `skipped`, `timed_out`, and `canceled`. A skipped result contains no selected options. Timeout and cancellation results include a machine-readable reason so the Agent can fall back to a normal textual response.

## 5. Pending Request Gate

Create `internal/agent/userinput` as a focused package with:

- A pending request type containing tenant ID, session owner ID, session ID, assistant message ID, request ID, tool-call ID, validated question data, and EventBus reference.
- An answer type containing selected option IDs, optional other text, and skip state.
- A waiter map protected by a mutex.
- A single-delivery guard to prevent duplicate resolution races.
- A bounded timeout derived from the existing Agent interaction timeout configuration.
- Context cancellation handling for stopped requests and disconnected clients.
- Redis Pub/Sub fan-out using a dedicated namespaced channel.

Pending waiters live only in the process that owns the Agent execution. Redis routes a resolution request to that process but does not persist questions or recreate canceled executions.

## 6. SSE Events

Add two response types.

### `user_input_required`

Data includes:

- `pending_id`
- `session_id`
- `assistant_message_id`
- `tool_call_id`
- `question`
- `mode`
- `question_group_id`
- `question_index`
- `question_total`
- validated options
- `allow_other`
- `allow_skip`
- `timeout_seconds`
- `requested_at`

### `user_input_resolved`

Data includes:

- `pending_id`
- `status`
- `question_group_id`
- `question_index`
- `question_total`
- selected option IDs and labels for the resolved-card summary
- `other_text` when supplied
- `reason` for timeout or cancellation

Both events are appended through the existing stream manager so normal SSE delivery and in-session event replay use one representation.

## 7. Answer API

Add:

```http
POST /api/v1/agent/user-inputs/:pending_id
```

Request body:

```json
{
  "selected_option_ids": ["written_with_reason"],
  "other_text": "",
  "skipped": false
}
```

Server validation enforces:

- The request is authenticated as a tenant Viewer or higher.
- Tenant ID matches the pending request.
- The signed-in user owns the originating session.
- Selected IDs exist in the pending question.
- Single-choice accepts at most one predefined selection.
- Multiple-choice accepts one or more selections unless `other_text` is present.
- `other_text` is accepted only when `allow_other` is true and is limited to 1,000 Unicode characters.
- Skip is accepted only when `allow_skip` is true and cannot be combined with selections or other text.

HTTP outcomes:

- `200`: answer delivered.
- `400`: invalid answer payload.
- `403`: tenant or user mismatch.
- `404`: pending ID never existed on any active instance.
- `409`: already answered, timed out, or canceled.
- `503`: the owning instance cannot be reached through Redis in a multi-instance deployment.

## 8. Agent Registration and Behavior

Register `ask_user` for live Web Agent runs. Do not register it for channels that cannot render and resolve an in-conversation prompt.

The Agent prompt states:

- Ask only when missing information materially blocks a reliable answer or action.
- Do not ask for facts already present in conversation context.
- Plan the total number of related questions before the first call.
- Use one call per question and keep the same group ID and total.
- Stop early when later questions become unnecessary.
- Do not exceed 10 questions in a group.
- Do not request passwords, access tokens, private keys, or equivalent secrets.
- Treat a skipped answer as a user choice and do not repeatedly force the same question.

## 9. Frontend Experience

Add `StructuredQuestionCard.vue` beside the existing approval and OAuth cards in the Agent event timeline.

The card displays:

- A concise title and mode label.
- `question_index / question_total`.
- `question_total - question_index` as the remaining-question count.
- Radio controls for single choice.
- Checkbox controls for multiple choice.
- Option descriptions when present.
- An "Other" control that reveals a text field.
- Skip and Continue commands.

Interaction states:

- Continue is disabled until the answer is valid.
- Skip remains available when permitted.
- Submission locks the controls and shows a local progress state.
- A failed API call unlocks the controls and shows an inline error.
- A resolved event collapses the card to an answer summary.
- Timeout, cancellation, stop, refresh, or disconnect leaves a non-interactive terminal card instructing the user to start a new request.

Accessibility and responsive behavior:

- Native or TDesign radio and checkbox semantics.
- Keyboard navigation and Enter submission.
- Focus moves to the first option when the card appears.
- Option text wraps without changing control dimensions.
- Mobile actions remain visible below the option list without horizontal scrolling.

## 10. Error and Lifecycle Rules

- Tool validation failures never enter the wait state.
- A pending request has exactly one terminal resolution.
- Duplicate answer attempts return conflict and never wake the Agent twice.
- Context cancellation removes the waiter and emits a canceled event when possible.
- Timeout removes the waiter and returns `timed_out` to the tool.
- Redis failure degrades to same-instance resolution; requests reaching another instance return an explicit failure.
- Existing MCP approval and OAuth waiters, routes, events, and cards remain behaviorally unchanged.
- No database migration is introduced.

## 11. Testing Strategy

### Backend unit tests

- Gate resolves answered and skipped requests.
- Timeout and context cancellation terminate the waiter.
- Duplicate resolution is rejected.
- Tenant and session-owner mismatches are rejected.
- Single-choice and multiple-choice payload validation.
- Option, progress, identifier, and text-length validation.
- Redis messages route to the owning instance and preserve authorization data.

### Backend integration tests

- `ask_user` emits `user_input_required`, blocks, receives an answer, emits `user_input_resolved`, and returns the expected tool result.
- The answer endpoint returns the documented status codes.
- Agent tool registration includes `ask_user` for Web and excludes it for unsupported channels.
- Existing approval and OAuth tests continue to pass.

### Frontend tests

- Single-choice and multiple-choice selection.
- Other-text validation.
- Skip behavior.
- Submit locking and API error recovery.
- Progress and remaining-question calculation.
- Resolved, skipped, timed-out, and canceled summaries.
- Event normalization and stable keys for required/resolved events.

### Browser verification

- A real Agent call pauses on `ask_user` and resumes after submission.
- Desktop and mobile layouts have no overlap or horizontal overflow.
- Keyboard-only selection and submission work.
- Stopping generation disables the pending card.

## 12. Acceptance Criteria

- A Web Agent can call `ask_user` with a valid single-choice or multiple-choice question.
- The frontend shows the question, choices, current/total position, and remaining count.
- The user can choose predefined options, provide an allowed Other answer, or skip.
- The original Agent execution resumes and consumes the structured result without starting a new chat request.
- Invalid, unauthorized, duplicate, timed-out, and canceled answers follow the documented behavior.
- Refresh and restart recovery are explicitly unsupported and do not leave an apparently actionable card.
- No regression occurs in normal Agent streaming, MCP approval, or MCP OAuth flows.
