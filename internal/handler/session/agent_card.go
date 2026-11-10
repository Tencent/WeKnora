package session

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// agentCardDetailsMaxLen bounds every string stored in the audit Details
	// blob (session_id / message_id / surface_id / source_component_id /
	// reason): all are request- or model-sized strings that must never reach
	// the table at full length.
	agentCardDetailsMaxLen = 128
	// agentCardTargetIDPartMaxLen bounds each half of TargetID so that the
	// "<surface_id>/<source_component_id>" concatenation stays inside
	// audit_logs.target_id VARCHAR(64) (31 + 1 + 31 = 63). Oversizing it makes
	// Postgres reject the row with 22001, which loses the audit silently.
	agentCardTargetIDPartMaxLen = 31
)

// executeAgentCardActionRequest is all the client may assert about a button
// click. The official A2UI action message carries the action name, surface id
// and the source component id; WeKnora scopes it to a session + message. The
// action's role and whitelist standing are re-derived server-side from the
// persisted component tree (FindA2UIButton), and the client's claimed name is
// cross-checked against the persisted one, so a tampered client can never
// execute anything the model did not actually offer.
type executeAgentCardActionRequest struct {
	SessionID         string `json:"session_id"`
	MessageID         string `json:"message_id"`
	SurfaceID         string `json:"surface_id"`
	SourceComponentID string `json:"source_component_id"`
	// ActionName is the client's claimed action.event.name. The persisted
	// value (btn.ActionName) is authoritative; a mismatch is refused.
	ActionName string `json:"action_name"`
	// Context carries the resolved data-binding values the client reports.
	// The confirm executor reads it to VETO: the persisted button context is
	// the spec, the submission is replayed against it (ReplayA2UIForm), and
	// every mismatch is a 400 denial. Accepted values are composed into the
	// follow-up text — the client never injects arbitrary text.
	Context json.RawMessage `json:"context,omitempty"`
}

// ExecuteAgentCardAction handles POST /api/v1/agent/actions/execute —
// upstream issue #3017's controlled button-action endpoint for the official
// A2UI v0.9 wire format.
//
// Defense layers, in order: session ownership (GetOwnedSession enforces
// owner+tenant with no Admin+ read fallback, foreign/unknown → 404), content
// re-validation (the clicked component must exist inside the persisted answer
// as a Button, and the client's claimed action name must equal the persisted
// action.event.name), the AgentCardActionAllowed execution whitelist, and the
// per-action role floor checked against the caller's tenant role.
//
// Auditing scope, stated exactly as implemented. No audit row on any exit
// before the persisted content is reached: malformed JSON (400), a missing
// required field (400), unknown/foreign session (404), failed session lookup
// (500), failed message lookup (404/500) and a nil message (404). An audit
// row fires on: success (both executors), the unresolved-button 404
// (component absent, not a Button, or no event action), the name-mismatch
// 404, the whitelist 403, the RBAC 403, and — confirm only — eleven denial
// reasons: the six replay rejections form_value_unsupported, form_path_unbound,
// form_keys_mismatch, form_literal_mismatch, form_value_invalid and
// form_too_large, plus context_missing, already_executed, run_in_progress,
// session_rewinding and agent_unresolvable. The already_executed denial row
// is written BY DESIGN (next to the idempotent 200) so the one-shot history
// stays reconstructable. Recorded tolerance: any caller holding their own
// session and message id can write one denial row per request; flood control
// for the audited denials is deliberately not done in this handler.
//
// @Summary      Execute an A2UI button action
// @Tags         会话
// @Router       /agent/actions/execute [post]
func (h *Handler) ExecuteAgentCardAction(c *gin.Context) {
	ctx := c.Request.Context()
	var req executeAgentCardActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(apperrors.NewBadRequestError("invalid request body").WithDetails(err.Error()))
		return
	}
	if req.SessionID == "" || req.MessageID == "" || req.SurfaceID == "" ||
		req.SourceComponentID == "" || req.ActionName == "" {
		_ = c.Error(apperrors.NewBadRequestError(
			"session_id, message_id, surface_id, source_component_id and action_name are required"))
		return
	}

	session, err := h.sessionService.GetOwnedSession(ctx, req.SessionID)
	if err != nil {
		if stderrors.Is(err, apperrors.ErrSessionNotFound) {
			_ = c.Error(apperrors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	msg, err := h.messageService.GetMessage(ctx, req.SessionID, req.MessageID)
	if err != nil {
		// Same mapping as ContinueStream in stream.go: a session that vanished
		// or a message row that does not exist are permanent client errors,
		// while anything else is a transient fault and must not be dressed up
		// as "message not found".
		if stderrors.Is(err, apperrors.ErrSessionNotFound) || stderrors.Is(err, gorm.ErrRecordNotFound) {
			logger.Warnf(ctx, "Card action: session or message not found, session ID: %s, message ID: %s",
				req.SessionID, req.MessageID)
			_ = c.Error(apperrors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		_ = c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	if msg == nil {
		_ = c.Error(apperrors.NewNotFoundError("message not found"))
		return
	}

	// Re-derive the button from the persisted component tree. The client only
	// gets to locate it (surface_id + source_component_id); everything else —
	// the action name, its whitelist standing, its role floor — comes from
	// what the model actually emitted and the server-side policy table.
	btn := types.FindA2UIButton(msg.Content, req.SurfaceID, req.SourceComponentID)
	if btn == nil {
		h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
			req, req.SurfaceID, req.SourceComponentID, "unresolved", "", "", "")
		_ = c.Error(apperrors.NewNotFoundError("card or button not found in message"))
		return
	}
	// The official action message always carries the action name. A client
	// that claims a name different from the persisted action.event.name is out
	// of sync (or forging); refuse before any policy decision. Only the
	// persisted name (btn.ActionName) feeds the whitelist and role checks.
	if req.ActionName != btn.ActionName {
		h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
			req, req.SurfaceID, req.SourceComponentID,
			"action_name_mismatch:"+btn.ActionName, "", "", "")
		_ = c.Error(apperrors.NewNotFoundError("card or button not found in message"))
		return
	}
	if !types.AgentCardActionAllowed(btn.ActionName) {
		h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
			req, req.SurfaceID, req.SourceComponentID,
			"action_name_not_allowed:"+btn.ActionName, "", "", "")
		_ = c.Error(apperrors.NewForbiddenError("this card action is not enabled"))
		return
	}
	required := types.AgentCardRequiredRole(btn.ActionName)
	if role := types.TenantRoleFromContext(ctx); !role.HasPermission(required) {
		h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
			req, req.SurfaceID, req.SourceComponentID, "insufficient_role", required, "", "")
		_ = c.Error(apperrors.NewForbiddenError("insufficient workspace role for this card action"))
		return
	}

	// Executor switch, and the hard prerequisites any case added here must keep.
	//
	// (1) The role floor comes from the server policy table
	// (agentCardActionPolicy), never from model-generated content: the official
	// protocol has no permission field, so a hallucination or a tampered
	// generator cannot widen what an arbitrary tenant member may trigger.
	// Each executor carries its own policy entry with an explicit role:
	// "noop" (no side effects) and "confirm" (persists a follow-up turn and
	// starts an agent run) both sit at the route's own floor.
	//
	// (2) This is a write path, so the session-ownership gate above must stay
	// owner-scoped (GetOwnedSession) and must never fall back to GetSession:
	// the latter applies the Admin+ API-key read fallback, which would let a
	// tenant admin mutate another principal's API-key session.
	switch btn.ActionName {
	case "noop":
	case "confirm":
		h.executeConfirmAction(c, req, session, btn, req.SurfaceID, req.SourceComponentID)
		return
	default:
		// Unreachable while the whitelist above holds; belt-and-braces. The
		// audit still fires so a refusal never goes unrecorded. Distinct reason
		// string from the whitelist refusal above so the two 403s stay separable.
		h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
			req, req.SurfaceID, req.SourceComponentID, "action_name_not_supported", "", "", "")
		_ = c.Error(apperrors.NewForbiddenError("this card action is not enabled"))
		return
	}

	h.emitAgentCardAudit(c, types.AuditActionAgentCardExecuted, types.AuditOutcomeSuccess,
		req, req.SurfaceID, req.SourceComponentID, "", "", "", btn.ActionName)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"status":      "accepted",
			"action_name": btn.ActionName,
			"label":       btn.Label,
		},
	})
}

// agentCardAuditTargetID builds the audit target_id exactly the way
// emitAgentCardAudit persists it: both parts truncated to
// agentCardTargetIDPartMaxLen bytes, joined by "/". The one-shot dedup
// lookup MUST build its target_id through this same helper — a divergence
// between write and read silently defeats the check (see the test
// TestExecuteAgentCardActionConfirmAlreadyExecuted, which pins the shape).
func agentCardAuditTargetID(surfaceID, componentID string) string {
	return common.TruncateUTF8Bytes(surfaceID, agentCardTargetIDPartMaxLen) + "/" +
		common.TruncateUTF8Bytes(componentID, agentCardTargetIDPartMaxLen)
}

// kickConfirmRun starts the confirm follow-up run in the background,
// mirroring kickNextRunFromSteerBacklog (steer.go): a recover-log wrapper
// around executeQA(skipSSE). Events land in the StreamManager; the client
// picks them up via continue-stream (Task 7). Package-level var on purpose:
// tests swap it for a synchronous spy and restore via defer, so executeQA
// (and its engine) never runs against stubs.
var kickConfirmRun = func(h *Handler, followUp *qaRequestContext) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(followUp.ctx, "a2ui confirm follow-up run panicked: %v", r)
			}
		}()
		h.executeQA(followUp, qaModeAgent, false)
	}()
}

// executeConfirmAction is the "confirm" executor: the persisted button
// context becomes a user message, and the agent continues in the same
// session as a server-started run.
//
// Order matters and is pinned by the tests: (1) the persisted context is the
// SPEC — the client submission is replayed against it (ReplayA2UIForm, six
// form_* rejections) and the accepted values compose into the follow-up text
// (ComposeConfirmFollowUp; byte-identical for a bindless confirm); (2) one-shot
// dedup against the audit log, lookup failure degrades to proceed (the
// exclusive claim below is the real backstop); (3) live-run / rewind guards
// BEFORE any row is written (skipSSE makes executeQA's own guards log-only, so
// a late refusal would orphan the turn); (4) request context rebuilt from the
// persisted input-bar state (LastRequestState), the same helpers parseQA
// Request uses; (5) persist then exclusive SetLiveRun — ErrLiveRunExists
// rolls the turn back and 409s (ClaimLiveRun is WRONG here: it overwrites
// unconditionally, see the interface doc in internal/stream); (6) success
// audit (with the sorted form_keys) BEFORE the kick so a racing second click
// sees it; (7) respond.
func (h *Handler) executeConfirmAction(
	c *gin.Context,
	req executeAgentCardActionRequest,
	session *types.Session,
	btn *types.A2uiResolvedButton,
	surfaceID, componentID string,
) {
	ctx := c.Request.Context()
	deny := func(reason string, httpErr error) {
		h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
			req, surfaceID, componentID, reason, "", "", "")
		_ = c.Error(httpErr)
	}

	// (1) PERSISTED context only; the client context is read and VETOED, never
	// trusted. The persisted button spec is the whitelist: every binding must
	// appear in the submission, the key sets must match, literal values must
	// survive a DeepEqual round-trip, bound values must stay scalar-shaped and
	// under the size caps. Accepted bindings compose into the follow-up text
	// with the persisted labels — the client never chooses what it echoes.
	var msgText string
	var formValues map[string]json.RawMessage
	if raw, ok := btn.Context["message"]; ok {
		_ = json.Unmarshal(raw, &msgText)
	}
	msgText = strings.TrimSpace(msgText)
	followUpText := msgText
	if msgText != "" {
		var replayReason string
		formValues, replayReason = types.ReplayA2UIForm(
			btn.Context, req.Context, btn.BoundInputPaths)
		if replayReason != "" {
			deny(replayReason, apperrors.NewBadRequestError(
				"the submitted form does not match this card (reason: "+replayReason+")"))
			return
		}
		followUpText = types.ComposeConfirmFollowUp(msgText, formValues, btn.FieldLabels)
	}
	if msgText == "" {
		deny("context_missing", apperrors.NewBadRequestError(
			"this confirm button has no follow-up instruction (action.event.context.message)"))
		return
	}

	// (2) One-shot guard. A lookup failure degrades to proceed: audit is
	// best-effort by design, and the exclusive claim below is what actually
	// prevents a double run while the first run is live.
	if audit := middleware.AuditServiceFromContext(c); audit != nil {
		if tenantID, ok := types.TenantIDFromContext(ctx); ok {
			executed, err := audit.CardActionAlreadyExecuted(ctx, tenantID,
				agentCardAuditTargetID(surfaceID, componentID),
				common.TruncateUTF8Bytes(req.MessageID, agentCardDetailsMaxLen),
				btn.ActionName)
			if err != nil {
				logger.Warnf(ctx, "card action dedup lookup failed for %s: %v",
					agentCardAuditTargetID(surfaceID, componentID), err)
			} else if executed {
				h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
					req, surfaceID, componentID, "already_executed", "", "", "")
				c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
					"status":      "already_executed",
					"action_name": btn.ActionName,
					"label":       btn.Label,
				}})
				return
			}
		}
	}

	// (3) Guards before any row is written.
	liveID, _, err := h.streamManager.GetLiveRun(ctx, req.SessionID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"session_id": req.SessionID})
		_ = c.Error(apperrors.NewServiceUnavailableError("failed to look up the running turn"))
		return
	}
	if liveID != "" {
		deny("run_in_progress", apperrors.NewConflictError("another turn is already running in this session"))
		return
	}
	if err := h.rejectIfSessionRewinding(ctx, req.SessionID); err != nil {
		deny("session_rewinding", err)
		return
	}

	// (4) Rebuild the request context from persisted input-bar state.
	state := session.LastRequestState
	if state == nil || state.AgentID == "" {
		deny("agent_unresolvable", apperrors.NewBadRequestError(
			"this session has no persisted agent state to continue with"))
		return
	}
	customAgent, _, _ := h.resolveAgent(ctx, c, state.AgentID, 0)
	if customAgent == nil {
		deny("agent_unresolvable", apperrors.NewBadRequestError("the persisted agent can no longer be resolved"))
		return
	}
	mentioned := mentionedItemsToRequests(state.MentionedItems)
	kbIDs, knowledgeIDs := mergeKnowledgeTargets(state.KnowledgeBaseIDs, state.KnowledgeIDs, mentioned)
	mentionScopes := tagScopesFromMentionedItems(mentioned)
	tagScopes := mergeTagScopesFromRequestIDs(mentionScopes, dedupRequestStrings(state.TagIDs), kbIDs)
	tagIDs := dedupRequestStrings(append(state.TagIDs, mentionedIDsByType(mentioned, "tag")...))
	mcpServiceIDs := dedupRequestStrings(append(state.MCPServiceIDs, mentionedIDsByType(mentioned, "mcp")...))
	skillNames := dedupRequestStrings(append(state.SkillNames, mentionedIDsByType(mentioned, "skill")...))
	executionContext, agentID, agentTenantID, modelID := buildMessageExecutionContext(
		ctx, customAgent, 0, state.ModelID,
		secutils.SanitizeForLogArray(kbIDs), secutils.SanitizeForLogArray(knowledgeIDs),
		secutils.SanitizeForLogArray(tagIDs), tagScopes,
		secutils.SanitizeForLogArray(mcpServiceIDs), secutils.SanitizeForLogArray(skillNames),
		state.WebSearchEnabled)
	executionContext.LocalBrowserEnabled = state.LocalBrowserEnabled

	requestID := uuid.New().String()
	followUp := &qaRequestContext{
		ctx:         context.WithoutCancel(ctx),
		c:           nil,
		sessionID:   req.SessionID,
		requestID:   requestID,
		receivedAt:  time.Now(),
		query:       followUpText,
		userInput:   followUpText,
		session:     session,
		customAgent: customAgent,
		assistantMessage: &types.Message{
			SessionID:        req.SessionID,
			Role:             "assistant",
			RequestID:        requestID,
			IsCompleted:      false,
			Channel:          "a2ui",
			AgentID:          agentID,
			AgentTenantID:    agentTenantID,
			ModelID:          modelID,
			ExecutionContext: executionContext,
		},
		knowledgeBaseIDs:    secutils.SanitizeForLogArray(kbIDs),
		knowledgeIDs:        secutils.SanitizeForLogArray(knowledgeIDs),
		tagScopes:           tagScopes,
		tagIDs:              secutils.SanitizeForLogArray(tagIDs),
		mcpServiceIDs:       secutils.SanitizeForLogArray(mcpServiceIDs),
		skillNames:          secutils.SanitizeForLogArray(skillNames),
		summaryModelID:      secutils.SanitizeForLog(state.ModelID),
		reasoningEffort:     state.ReasoningEffort,
		webSearchEnabled:    state.WebSearchEnabled,
		localBrowserEnabled: state.LocalBrowserEnabled,
		mentionedItems:      state.MentionedItems,
		channel:             "a2ui",
		reqAgentEnabled:     state.AgentEnabled,
		reqAgentID:          state.AgentID,
		skipSSE:             true,
	}

	// (5) Persist, then claim exclusively.
	if err := h.persistTurnMessages(followUp.ctx, followUp); err != nil {
		logger.ErrorWithFields(followUp.ctx, err, map[string]interface{}{"session_id": req.SessionID})
		h.rollbackTurnMessages(followUp.ctx, followUp, true, true)
		_ = c.Error(apperrors.NewInternalServerError(err.Error()))
		return
	}
	if followUp.userMessageID == "" || followUp.assistantMessage == nil || followUp.assistantMessage.ID == "" {
		h.rollbackTurnMessages(followUp.ctx, followUp, true, true)
		_ = c.Error(apperrors.NewInternalServerError("failed to persist the confirm turn"))
		return
	}
	if err := h.streamManager.SetLiveRun(followUp.ctx, req.SessionID,
		followUp.assistantMessage.ID, requestID); err != nil {
		h.rollbackTurnMessages(followUp.ctx, followUp, true, true)
		if stderrors.Is(err, stream.ErrLiveRunExists) {
			h.emitAgentCardAudit(c, types.AuditActionAgentCardDenied, types.AuditOutcomeDenied,
				req, surfaceID, componentID, "run_in_progress", "", "", "")
			_ = c.Error(apperrors.NewConflictError("another turn is already running in this session"))
		} else {
			_ = c.Error(apperrors.NewServiceUnavailableError("failed to publish the running turn"))
		}
		return
	}

	// (6) Success audit BEFORE the kick so a racing second click sees it.
	// form_keys is the ONE deterministic key order (the same sort the composer
	// used) — values never reach the audit, keys only.
	h.emitAgentCardAudit(c, types.AuditActionAgentCardExecuted, types.AuditOutcomeSuccess,
		req, surfaceID, componentID, "", "",
		strings.Join(types.SortedA2UIFormKeys(formValues), ","), btn.ActionName)
	kickConfirmRun(h, followUp)

	// (7) The echo of the persisted message lets the frontend render the
	// user bubble from one source of truth (the server).
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"status":               "started",
		"action_name":          btn.ActionName,
		"label":                btn.Label,
		"message":              followUpText,
		"message_id":           followUp.userMessageID,
		"assistant_message_id": followUp.assistantMessage.ID,
	}})
}

// emitAgentCardAudit is best-effort: audit failure must never change the
// business outcome. Same idiom as the RBAC denial audits in
// internal/middleware/rbac.go (nil-check the context-injected service). Unlike
// those, a rejected row is reported: Log's error was discarded, and that is how
// the embed plane lost every card-action row without a trace — the synthetic
// "embed-<channel UUID>" actor (42 chars) did not fit audit_logs.actor_user_id
// at its original VARCHAR(36), so the EXECUTED row that the confirm-once dedup
// (CardActionAlreadyExecuted) reads never existed and a second click re-ran the
// action. The error is therefore warned, never returned, and the actor is
// deliberately NOT clipped to fit: a clipped id would merge two embed channels
// whose UUIDs share a prefix, which is worse than a loudly lost row.
// AuditServiceProvider is installed globally, so the
// service is present on every /api/v1 route. It must also never panic: a
// panic here would reach gin's Recovery and turn the caller's 403 into a 500
// while losing the audit row, so tenant lookup uses the non-panicking context
// accessor and bails out silently. reason is the denial cause and is written
// only when non-empty; actionName is the executed action on the success row
// and is written only when non-empty — the two never share a field, so a
// success row carries action_name (and no reason) while a denial row carries
// reason (and no action_name). requiredRole is only set by the RBAC refusal.
func (h *Handler) emitAgentCardAudit(
	c *gin.Context,
	action types.AuditAction,
	outcome types.AuditOutcome,
	req executeAgentCardActionRequest,
	surfaceID string,
	componentID string,
	reason string,
	requiredRole types.TenantRole,
	formKeys string,
	actionName string,
) {
	audit := middleware.AuditServiceFromContext(c)
	if audit == nil {
		return
	}
	ctx := c.Request.Context()
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return
	}
	actorUserID, _ := types.UserIDFromContext(ctx)
	details := map[string]string{
		"session_id": common.TruncateUTF8Bytes(req.SessionID, agentCardDetailsMaxLen),
		"message_id": common.TruncateUTF8Bytes(req.MessageID, agentCardDetailsMaxLen),
		// Always the client-supplied locator, on every path including the
		// success row: FindA2UIButton echoes the ids it matched on back into
		// A2uiResolvedButton, so there is no server-resolved variant to prefer
		// here. That is exactly why both are clipped — the client controls their
		// length and must not be able to size-pump this table.
		"surface_id":          common.TruncateUTF8Bytes(surfaceID, agentCardDetailsMaxLen),
		"source_component_id": common.TruncateUTF8Bytes(componentID, agentCardDetailsMaxLen),
	}
	if reason != "" {
		details["reason"] = common.TruncateUTF8Bytes(reason, agentCardDetailsMaxLen)
	}
	if actionName != "" {
		details["action_name"] = common.TruncateUTF8Bytes(actionName, agentCardDetailsMaxLen)
	}
	if requiredRole != "" {
		details["required_role"] = string(requiredRole)
	}
	if formKeys != "" {
		details["form_keys"] = common.TruncateUTF8Bytes(formKeys, agentCardDetailsMaxLen)
	}
	// 31 + 1 + 31 fits target_id VARCHAR(64); see agentCardTargetIDPartMaxLen.
	targetID := agentCardAuditTargetID(surfaceID, componentID)
	detailsJSON, _ := json.Marshal(details)
	if err := audit.Log(ctx, &types.AuditLog{
		TenantID:      tenantID,
		ActorUserID:   actorUserID,
		ActorRole:     string(types.TenantRoleFromContext(ctx)),
		Action:        action,
		Outcome:       outcome,
		TargetType:    "a2ui_button",
		TargetID:      targetID,
		RequestMethod: c.Request.Method,
		RequestPath:   c.Request.URL.Path,
		Details:       types.JSON(detailsJSON),
	}); err != nil {
		logger.Warnf(ctx, "a2ui card-action audit row rejected: action=%s outcome=%s tenant=%d actor=%q: %v",
			action, outcome, tenantID, actorUserID, err)
	}
}
