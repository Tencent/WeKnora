package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Only the injected SQLite failure is removed; all successful writes use
// the production message service and repositories.
type recoveringSteerMessages struct {
	interfaces.MessageService
	db *gorm.DB
}

func (s *recoveringSteerMessages) CreateMessage(ctx context.Context, msg *types.Message) (*types.Message, error) {
	created, err := s.MessageService.CreateMessage(ctx, msg)
	if err != nil {
		s.db.Exec("DROP TRIGGER IF EXISTS audit_fail_first_steer")
	}
	return created, err
}

func TestSteerOrderWithSQLiteFailure(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail_first_%t", failFirst), func(t *testing.T) {
			runSteerOrderIntegration(t, failFirst)
		})
	}
}

func steerOrderDatabase(t *testing.T, failFirst bool) (*gorm.DB, string) {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}))
	auditSession := &types.Session{TenantID: 1}
	require.NoError(t, db.Create(auditSession).Error)
	if failFirst {
		require.NoError(t, db.Exec(`CREATE TRIGGER audit_fail_first_steer BEFORE INSERT ON messages
  WHEN NEW.content = 'OLDER: use option A'
  BEGIN SELECT RAISE(FAIL, 'injected one-shot write failure'); END`).Error)
	}
	return db, auditSession.ID
}

type steerOrderModelRequests struct {
	mu       sync.Mutex
	requests []string
}

func (a *steerOrderModelRequests) serveHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var text strings.Builder
	for _, m := range body.Messages {
		text.WriteString(m.Content)
		text.WriteByte('\n')
	}
	a.mu.Lock()
	a.requests = append(a.requests, text.String())
	a.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w,
		`data: {"choices":[{"index":0,"delta":{"content":"Acknowledged"},"finish_reason":"stop"}]}`+"\n\n")
}

func (a *steerOrderModelRequests) assertOrder(t *testing.T, db *gorm.DB, failFirst bool) {
	t.Helper()
	var rows []types.Message
	require.NoError(t, db.Order("created_at").Find(&rows).Error)
	var order []string
	for _, row := range rows {
		order = append(order, row.Content)
	}
	a.mu.Lock()
	wire := append([]string(nil), a.requests...)
	a.mu.Unlock()
	require.NotEmpty(t, wire)
	last := wire[len(wire)-1]
	t.Logf("fail_first=%t; persisted_order=%q; model_calls=%d; older_index=%d; newer_index=%d",
		failFirst, order, len(wire), strings.Index(last, "OLDER:"), strings.Index(last, "NEWER:"))
	require.Contains(t, last, "OLDER:")
	require.Contains(t, last, "NEWER:")
	require.Less(t, strings.Index(last, "OLDER:"), strings.Index(last, "NEWER:"),
		"the model must receive the newer correction after the older instruction")
	require.Equal(t, []string{"OLDER: use option A", "NEWER: replace A with option B"}, order,
		"a failed write must not let the newer correction overtake the older instruction")
}

func runSteerOrderIntegration(t *testing.T, failFirst bool) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	db, sessionID := steerOrderDatabase(t, failFirst)
	msgs := &recoveringSteerMessages{MessageService: service.NewMessageService(
		repository.NewMessageRepository(db), repository.NewSessionRepository(db), nil, nil, nil, nil, nil), db: db}
	tenantCtx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx, cancel := context.WithTimeout(tenantCtx, 10*time.Second)
	defer cancel()
	mgr := stream.NewMemoryStreamManager()
	require.NoError(t, mgr.AppendSteerEvents(ctx, sessionID, "assistant", []interfaces.StreamEvent{
		steerEventWithDelivery("older", "OLDER: use option A", steerDeliveryInject),
		steerEventWithDelivery("newer", "NEWER: replace A with option B", steerDeliveryInject),
	}))
	sink := newSteerSink(ctx, sessionID, "request", &types.Message{ID: "assistant"}, msgs, mgr)
	captured := &steerOrderModelRequests{}
	server := httptest.NewServer(http.HandlerFunc(captured.serveHTTP))
	defer server.Close()
	model := openaicompletions.New(openaicompletions.Config{Endpoint: api.Endpoint{
		BaseURL: server.URL, Model: "order-audit", Client: server.Client(),
	}})
	engine := agent.NewAgentEngine(&types.AgentConfig{MaxIterations: 3, MaxContextTokens: 100000},
		model, agenttools.NewToolRegistry(), event.NewEventBus(), nil, nil, sessionID,
		"Follow the user's latest correction.")
	require.NotNil(t, engine)
	engine.SetSteerSink(sink)
	_, err := engine.Execute(ctx, sessionID, "assistant", "Choose an option.", nil)
	require.NoError(t, err)
	captured.assertOrder(t, db, failFirst)
}
