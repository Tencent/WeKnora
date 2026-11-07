package session

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/common"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// a2uiCardMessageContent is a persisted assistant message containing one
// ```a2ui fence with two frames (createSurface + updateComponents). The
// component tree has three buttons:
//
//	go  → noop (whitelisted, viewer floor)
//	bad → drop_database (NOT whitelisted)
//	rst → reset_db (test-hook: mapped to admin)
const a2uiCardMessageContent = "正文\n```a2ui\n" +
	`[{"version":"v0.9","createSurface":{"surfaceId":"s1","catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
	`{"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[` +
	`{"id":"root","component":"Card","child":"col1"},{"id":"col1","component":"Column","children":["go_lbl","go","bad_lbl","bad","rst_lbl","rst"]},` +
	`{"id":"go_lbl","component":"Text","text":"知道了"},` +
	`{"id":"go","component":"Button","child":"go_lbl","variant":"primary","action":{"event":{"name":"noop"}}},` +
	`{"id":"bad_lbl","component":"Text","text":"删库"},` +
	`{"id":"bad","component":"Button","child":"bad_lbl","action":{"event":{"name":"drop_database"}}},` +
	`{"id":"rst_lbl","component":"Text","text":"重置"},` +
	`{"id":"rst","component":"Button","child":"rst_lbl","action":{"event":{"name":"reset_db"}}}` +
	`]}}]` + "\n```\n"

// --- stubs ---

type stubSessionServiceForCard struct {
	interfaces.SessionService
	err       error
	session   *types.Session // returned when non-nil (used to set LastRequestState)
	rewindErr error
}

func (s *stubSessionServiceForCard) GetOwnedSession(context.Context, string) (*types.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.session != nil {
		return s.session, nil
	}
	return &types.Session{ID: "sess-1"}, nil
}

func (s *stubSessionServiceForCard) RejectSendIfRewinding(context.Context, string) error {
	return s.rewindErr
}

type stubMessageServiceForCard struct {
	interfaces.MessageService
	content string
	err     error
	nilMsg  bool
	created []*types.Message
	deleted []string
}

func (s *stubMessageServiceForCard) GetMessage(context.Context, string, string) (*types.Message, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.nilMsg {
		return nil, nil
	}
	return &types.Message{Content: s.content}, nil
}

func (s *stubMessageServiceForCard) CreateMessage(_ context.Context, m *types.Message) (*types.Message, error) {
	if m.Role == "assistant" {
		m.ID = "asst-1"
	} else {
		m.ID = "user-1"
	}
	m.CreatedAt = time.Now()
	s.created = append(s.created, m)
	return m, nil
}

func (s *stubMessageServiceForCard) DeleteMessage(_ context.Context, _, id string) error {
	s.deleted = append(s.deleted, id)
	return nil
}

type capturingAuditForCard struct {
	interfaces.AuditLogService
	entries  []*types.AuditLog
	executed bool
}

func (a *capturingAuditForCard) Log(_ context.Context, entry *types.AuditLog) error {
	a.entries = append(a.entries, entry)
	return nil
}

func (a *capturingAuditForCard) CardActionAlreadyExecuted(context.Context, uint64, string, string, string) (bool, error) {
	return a.executed, nil
}

type stubStreamManagerForCard struct {
	interfaces.StreamManager
	liveID   string
	liveErr  error
	claimErr error
	claims   []string
}

func (s *stubStreamManagerForCard) GetLiveRun(context.Context, string) (string, string, error) {
	return s.liveID, "", s.liveErr
}

func (s *stubStreamManagerForCard) SetLiveRun(_ context.Context, sessionID, assistantMessageID, requestID string) error {
	if s.claimErr != nil {
		return s.claimErr
	}
	s.claims = append(s.claims, sessionID+"/"+assistantMessageID+"/"+requestID)
	return nil
}

type stubCustomAgentServiceForCard struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
	err   error
}

func (s *stubCustomAgentServiceForCard) GetAgentByID(context.Context, string) (*types.CustomAgent, error) {
	return s.agent, s.err
}

type cardRouterDeps struct {
	content    string
	sessionErr error
	msgErr     error
	msgNil     bool
	role       types.TenantRole
	// confirm-executor deps
	session   *types.Session // replaces the default session when non-nil
	rewindErr error
	liveRun   string
	liveErr   error
	claimErr  error
	executed  bool
	agent     *types.CustomAgent
	agentErr  error
}

type cardActionStubs struct {
	messages *stubMessageServiceForCard
	stream   *stubStreamManagerForCard
}

func newCardActionRouterWith(t *testing.T, d cardRouterDeps) (*gin.Engine, *capturingAuditForCard, *cardActionStubs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	audit := &capturingAuditForCard{executed: d.executed}
	streamMgr := &stubStreamManagerForCard{liveID: d.liveRun, liveErr: d.liveErr, claimErr: d.claimErr}
	messages := &stubMessageServiceForCard{content: d.content, err: d.msgErr, nilMsg: d.msgNil}
	h := &Handler{
		sessionService:     &stubSessionServiceForCard{err: d.sessionErr, session: d.session, rewindErr: d.rewindErr},
		messageService:     messages,
		streamManager:      streamMgr,
		customAgentService: &stubCustomAgentServiceForCard{agent: d.agent, err: d.agentErr},
	}
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(middleware.AuditServiceProvider(audit))
	router.POST("/api/v1/agent/actions/execute", func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, d.role)
		c.Request = c.Request.WithContext(ctx)
		h.ExecuteAgentCardAction(c)
	})
	return router, audit, &cardActionStubs{messages: messages, stream: streamMgr}
}

func newCardActionRouter(t *testing.T, content string, sessionErr error, role types.TenantRole) (*gin.Engine, *capturingAuditForCard) {
	t.Helper()
	router, audit, _ := newCardActionRouterWith(t, cardRouterDeps{content: content, sessionErr: sessionErr, role: role})
	return router, audit
}

func postCardActionRaw(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/actions/execute", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func postCardAction(t *testing.T, router *gin.Engine, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	return postCardActionRaw(t, router, string(raw))
}

func cardAuditDetails(t *testing.T, entry *types.AuditLog) map[string]string {
	t.Helper()
	var details map[string]string
	require.NoError(t, json.Unmarshal([]byte(entry.Details), &details))
	return details
}

// --- tests ---

func TestExecuteAgentCardActionHappyPath(t *testing.T) {
	router, audit := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleViewer)
	rec := postCardAction(t, router, map[string]string{
		"session_id": "sess-1", "message_id": "msg-1",
		"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Status     string `json:"status"`
			ActionName string `json:"action_name"`
			Label      string `json:"label"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, "accepted", resp.Data.Status)
	require.Equal(t, "noop", resp.Data.ActionName)
	require.Equal(t, "知道了", resp.Data.Label)
	require.Len(t, audit.entries, 1)
	require.Equal(t, "agent.card_action_executed", string(audit.entries[0].Action))
	require.Equal(t, "success", string(audit.entries[0].Outcome))
	require.Equal(t, "s1/go", audit.entries[0].TargetID)
	require.Equal(t, "a2ui_button", audit.entries[0].TargetType)
}

func TestExecuteAgentCardActionRejectsUnknownAndTampered(t *testing.T) {
	t.Run("missing field", func(t *testing.T) {
		router, _ := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleViewer)
		rec := postCardAction(t, router, map[string]string{"session_id": "s", "surface_id": "s1", "source_component_id": "go"})
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("body not JSON", func(t *testing.T) {
		router, _ := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleViewer)
		rec := postCardActionRaw(t, router, "<html>not json</html>")
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
	t.Run("session not owned", func(t *testing.T) {
		router, _ := newCardActionRouter(t, a2uiCardMessageContent, apperrors.ErrSessionNotFound, types.TenantRoleViewer)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
		})
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("message transient error", func(t *testing.T) {
		router, _, _ := newCardActionRouterWith(t, cardRouterDeps{
			content: a2uiCardMessageContent, msgErr: stderrors.New("connection reset"), role: types.TenantRoleViewer,
		})
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
		})
		require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	})
	t.Run("message not found", func(t *testing.T) {
		router, _, _ := newCardActionRouterWith(t, cardRouterDeps{
			content: a2uiCardMessageContent, msgErr: gorm.ErrRecordNotFound, role: types.TenantRoleViewer,
		})
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
		})
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})
	t.Run("message nil", func(t *testing.T) {
		router, _, _ := newCardActionRouterWith(t, cardRouterDeps{
			content: a2uiCardMessageContent, msgNil: true, role: types.TenantRoleViewer,
		})
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
		})
		require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	})
	t.Run("forged component id", func(t *testing.T) {
		router, audit := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleViewer)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "ghost", "action_name": "noop",
		})
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Len(t, audit.entries, 1)
		entry := audit.entries[0]
		require.Equal(t, "denied", string(entry.Outcome))
		require.Equal(t, "s1/ghost", entry.TargetID)
		details := cardAuditDetails(t, entry)
		require.Equal(t, "unresolved", details["reason"])
		_, hasRole := details["required_role"]
		require.False(t, hasRole)
	})
	t.Run("action name mismatch (client claims drop_database, persisted is noop)", func(t *testing.T) {
		router, audit := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleViewer)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "drop_database",
		})
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Len(t, audit.entries, 1)
		details := cardAuditDetails(t, audit.entries[0])
		require.Equal(t, "action_name_mismatch:noop", details["reason"])
	})
	t.Run("action not whitelisted", func(t *testing.T) {
		router, audit := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleOwner)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "bad", "action_name": "drop_database",
		})
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Len(t, audit.entries, 1)
		entry := audit.entries[0]
		require.Equal(t, "agent.card_action_denied", string(entry.Action))
		require.Equal(t, "s1/bad", entry.TargetID)
		require.Equal(t, "action_name_not_allowed:drop_database", cardAuditDetails(t, entry)["reason"])
	})
	// The role floor is exercised against the one real executor (noop) by
	// temporarily raising its floor to admin via the test hook: the gate must
	// block a caller below the floor and pass one at it. (reset_db has no
	// executor case, so it is refused by the switch regardless of role — the
	// "fully-authorized but unimplemented" refusal the next sub-test pins.)
	t.Run("insufficient role (noop floor raised to admin, caller is viewer)", func(t *testing.T) {
		restore := types.SetAgentCardActionPolicyForTest(map[string]types.TenantRole{"noop": types.TenantRoleAdmin})
		defer restore()
		router, audit := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleViewer)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
		})
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		require.Len(t, audit.entries, 1)
		details := cardAuditDetails(t, audit.entries[0])
		require.Equal(t, "insufficient_role", details["reason"])
		require.Equal(t, "admin", details["required_role"])
	})
	t.Run("role passes at threshold (noop floor raised to admin, caller is admin)", func(t *testing.T) {
		restore := types.SetAgentCardActionPolicyForTest(map[string]types.TenantRole{"noop": types.TenantRoleAdmin})
		defer restore()
		router, _ := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleAdmin)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "go", "action_name": "noop",
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
	t.Run("authorized but no executor (admin clicks reset_db)", func(t *testing.T) {
		restore := types.SetAgentCardActionPolicyForTest(map[string]types.TenantRole{"reset_db": types.TenantRoleViewer})
		defer restore()
		router, audit := newCardActionRouter(t, a2uiCardMessageContent, nil, types.TenantRoleOwner)
		rec := postCardAction(t, router, map[string]string{
			"session_id": "s", "message_id": "m",
			"surface_id": "s1", "source_component_id": "rst", "action_name": "reset_db",
		})
		// Whitelist and role both pass; the switch has no reset_db case, so it
		// is refused here and the refusal is recorded as unsupported (not the
		// whitelist or the RBAC reason) — an authorized-but-unimplemented
		// action still cannot execute.
		require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
		require.Len(t, audit.entries, 1)
		require.Equal(t, "action_name_not_supported", cardAuditDetails(t, audit.entries[0])["reason"])
	})
}

func TestClipAgentCardFieldIsColumnSafeAndRuneSafe(t *testing.T) {
	long := strings.Repeat("x", 200)
	// Every stored field stays inside its byte budget.
	require.Len(t, common.TruncateUTF8Bytes(long, agentCardDetailsMaxLen), agentCardDetailsMaxLen)
	require.Len(t, common.TruncateUTF8Bytes(long, agentCardTargetIDPartMaxLen), agentCardTargetIDPartMaxLen)
	// CJK is 3 bytes per rune; a cut must land on a rune boundary or Postgres
	// rejects the whole audit row as invalid UTF-8.
	require.Equal(t, "中", common.TruncateUTF8Bytes("中", 3))
	require.Equal(t, "", common.TruncateUTF8Bytes("中", 2))
	require.Equal(t, "中", common.TruncateUTF8Bytes("中中", 4))
}

// a2uiConfirmCardContent mirrors the shape the model is taught to emit for a
// confirm card: surface s2, a confirm button carrying
// action.event.context.message plus a noop button for the regression arm.
const a2uiConfirmCardContent = "方案如下\n```a2ui\n" +
	`[{"version":"v0.9","createSurface":{"surfaceId":"s2","catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
	`{"version":"v0.9","updateComponents":{"surfaceId":"s2","components":[` +
	`{"id":"root","component":"Card","child":"col1"},{"id":"col1","component":"Column","children":["t1","ok_lbl","ok","go_lbl","go"]},` +
	`{"id":"t1","component":"Text","text":"确认升级范围"},` +
	`{"id":"ok_lbl","component":"Text","text":"确定"},` +
	`{"id":"ok","component":"Button","child":"ok_lbl","variant":"primary","action":{"event":{"name":"confirm","context":{"message":"用户已确认，请按方案继续执行后续操作"}}}},` +
	`{"id":"go_lbl","component":"Text","text":"知道了"},` +
	`{"id":"go","component":"Button","child":"go_lbl","action":{"event":{"name":"noop"}}}` +
	`]}}]` + "\n```\n"

// a2uiConfirmNoContextContent is the same card minus the context — the model
// forgot to write the follow-up instruction.
const a2uiConfirmNoContextContent = "```a2ui\n" +
	`[{"version":"v0.9","createSurface":{"surfaceId":"s2","catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},` +
	`{"version":"v0.9","updateComponents":{"surfaceId":"s2","components":[` +
	`{"id":"root","component":"Card","child":"col1"},{"id":"col1","component":"Column","children":["ok_lbl","ok"]},` +
	`{"id":"ok_lbl","component":"Text","text":"确定"},` +
	`{"id":"ok","component":"Button","child":"ok_lbl","variant":"primary","action":{"event":{"name":"confirm"}}}` +
	`]}}]` + "\n```\n"

func confirmDeps() cardRouterDeps {
	return cardRouterDeps{
		content: a2uiConfirmCardContent,
		role:    types.TenantRoleViewer,
		session: &types.Session{
			ID: "sess-1",
			LastRequestState: &types.SessionLastRequestState{
				AgentID: "agent-1",
			},
		},
		agent: &types.CustomAgent{
			ID:       "agent-1",
			Name:     "confirm-agent",
			TenantID: 7,
			Config:   types.CustomAgentConfig{AgentMode: types.AgentModeSmartReasoning, ModelID: "model-1"},
		},
	}
}

func postConfirmAction(t *testing.T, router *gin.Engine, componentID string) *httptest.ResponseRecorder {
	t.Helper()
	return postCardAction(t, router, map[string]string{
		"session_id": "sess-1", "message_id": "msg-1",
		"surface_id": "s2", "source_component_id": componentID, "action_name": "confirm",
	})
}

// swapKickForSpy replaces the package-level kickConfirmRun with a synchronous
// spy so the real executeQA (and its engine) never runs against stubs; the
// goroutine wrapper itself is compile-checked by the package build.
func swapKickForSpy(t *testing.T, kicked **qaRequestContext) {
	t.Helper()
	orig := kickConfirmRun
	kickConfirmRun = func(_ *Handler, f *qaRequestContext) { *kicked = f }
	t.Cleanup(func() { kickConfirmRun = orig })
}

func TestExecuteAgentCardActionConfirmMissingContext(t *testing.T) {
	d := confirmDeps()
	d.content = a2uiConfirmNoContextContent
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Len(t, audit.entries, 1)
	require.Equal(t, "denied", string(audit.entries[0].Outcome))
	require.Equal(t, "context_missing", cardAuditDetails(t, audit.entries[0])["reason"])
	require.Equal(t, "s2/ok", audit.entries[0].TargetID)
	require.Empty(t, stubs.messages.created, "no message rows before the first guard")
	require.Empty(t, stubs.stream.claims)
}

func TestExecuteAgentCardActionConfirmMissingState(t *testing.T) {
	d := confirmDeps()
	d.session = &types.Session{ID: "sess-1"} // LastRequestState nil
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Len(t, audit.entries, 1)
	require.Equal(t, "agent_unresolvable", cardAuditDetails(t, audit.entries[0])["reason"])
	require.Empty(t, stubs.messages.created)
	require.Empty(t, stubs.stream.claims)
}

func TestExecuteAgentCardActionConfirmAgentUnresolvable(t *testing.T) {
	d := confirmDeps()
	d.agent = nil
	d.agentErr = stderrors.New("agent gone")
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Len(t, audit.entries, 1)
	require.Equal(t, "agent_unresolvable", cardAuditDetails(t, audit.entries[0])["reason"])
	require.Empty(t, stubs.messages.created)
}

func TestExecuteAgentCardActionConfirmAlreadyExecuted(t *testing.T) {
	d := confirmDeps()
	d.executed = true
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, "already_executed", resp.Data.Status)
	// The original success row is what the dedup reads; this click adds a
	// BY-DESIGN denial row for the trace.
	require.Len(t, audit.entries, 1)
	require.Equal(t, "denied", string(audit.entries[0].Outcome))
	require.Equal(t, "already_executed", cardAuditDetails(t, audit.entries[0])["reason"])
	require.Empty(t, stubs.messages.created, "an already-executed click must not persist anything")
	require.Empty(t, stubs.stream.claims)
}

func TestExecuteAgentCardActionConfirmRunInProgress(t *testing.T) {
	d := confirmDeps()
	d.liveRun = "asst-other"
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Len(t, audit.entries, 1)
	require.Equal(t, "run_in_progress", cardAuditDetails(t, audit.entries[0])["reason"])
	require.Empty(t, stubs.messages.created)
	require.Empty(t, stubs.stream.claims)
}

func TestExecuteAgentCardActionConfirmSessionRewinding(t *testing.T) {
	d := confirmDeps()
	d.rewindErr = sandbox.ErrSessionRewindLocked
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Len(t, audit.entries, 1)
	require.Equal(t, "session_rewinding", cardAuditDetails(t, audit.entries[0])["reason"])
	require.Empty(t, stubs.messages.created)
}

func TestExecuteAgentCardActionConfirmClaimConflict(t *testing.T) {
	d := confirmDeps()
	d.claimErr = stream.ErrLiveRunExists
	router, audit, stubs := newCardActionRouterWith(t, d)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Len(t, audit.entries, 1)
	require.Equal(t, "run_in_progress", cardAuditDetails(t, audit.entries[0])["reason"])
	// The turn was persisted, then the exclusive claim lost the race: both
	// rows roll back, user first.
	require.Len(t, stubs.messages.created, 2)
	require.Equal(t, []string{"user-1", "asst-1"}, stubs.messages.deleted)
}

func TestExecuteAgentCardActionConfirmHappyPath(t *testing.T) {
	d := confirmDeps()
	router, audit, stubs := newCardActionRouterWith(t, d)
	var kicked *qaRequestContext
	swapKickForSpy(t, &kicked)

	rec := postConfirmAction(t, router, "ok")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Status             string `json:"status"`
			ActionName         string `json:"action_name"`
			Label              string `json:"label"`
			Message            string `json:"message"`
			MessageID          string `json:"message_id"`
			AssistantMessageID string `json:"assistant_message_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Equal(t, "started", resp.Data.Status)
	require.Equal(t, "confirm", resp.Data.ActionName)
	require.Equal(t, "确定", resp.Data.Label)
	require.Equal(t, "用户已确认，请按方案继续执行后续操作", resp.Data.Message)
	require.Equal(t, "user-1", resp.Data.MessageID)
	require.Equal(t, "asst-1", resp.Data.AssistantMessageID)

	// The follow-up run is a server-started agent turn fed by the PERSISTED
	// context.message (the request body carried no context at all).
	require.NotNil(t, kicked, "the follow-up run must be kicked")
	require.Equal(t, "用户已确认，请按方案继续执行后续操作", kicked.query)
	require.Equal(t, "用户已确认，请按方案继续执行后续操作", kicked.userInput)
	require.Equal(t, "sess-1", kicked.sessionID)
	require.Equal(t, "a2ui", kicked.channel)
	require.True(t, kicked.skipSSE)
	require.Nil(t, kicked.c)
	require.NotNil(t, kicked.assistantMessage)
	require.Equal(t, "asst-1", kicked.assistantMessage.ID)
	require.Equal(t, "user-1", kicked.userMessageID)
	require.NotNil(t, kicked.customAgent)
	require.Equal(t, "agent-1", kicked.customAgent.ID)

	// Persisted rows: user row first (channel a2ui, literal content), then
	// the assistant placeholder.
	require.Len(t, stubs.messages.created, 2)
	require.Equal(t, "user", stubs.messages.created[0].Role)
	require.Equal(t, "用户已确认，请按方案继续执行后续操作", stubs.messages.created[0].Content)
	require.Equal(t, "a2ui", stubs.messages.created[0].Channel)
	require.Equal(t, "assistant", stubs.messages.created[1].Role)
	require.Equal(t, "asst-1", stubs.messages.created[1].ID)

	// Exactly one exclusive claim, for the new assistant row.
	require.Len(t, stubs.stream.claims, 1)
	require.Equal(t, "sess-1/asst-1/"+kicked.requestID, stubs.stream.claims[0])

	// One success audit row carrying the action name the dedup later reads.
	require.Len(t, audit.entries, 1)
	require.Equal(t, "agent.card_action_executed", string(audit.entries[0].Action))
	require.Equal(t, "success", string(audit.entries[0].Outcome))
	require.Equal(t, "s2/ok", audit.entries[0].TargetID)
	require.Equal(t, "confirm", cardAuditDetails(t, audit.entries[0])["action_name"])
	require.Equal(t, "msg-1", cardAuditDetails(t, audit.entries[0])["message_id"])
}
