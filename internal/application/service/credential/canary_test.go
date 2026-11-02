// Tests that a password never appears in tool output, logs, or Redis.
package credential

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// The sentinel includes \" so a log redactor that stops at the first quote
// would be caught leaking its tail.
const canary = `canary-\"pw-7f3a`

type recordingBrowser struct {
	mu    sync.Mutex
	fills []sandbox.SecretFillRequest
}

func (b *recordingBrowser) CurrentPage(context.Context, string) (sandbox.BrowserPage, error) {
	return sandbox.BrowserPage{URL: "https://a.com/login"}, nil
}

func (b *recordingBrowser) FillSecret(
	_ context.Context, _ string, req sandbox.SecretFillRequest,
) (*sandbox.SecretFillResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cp := req
	cp.Fields = append([]sandbox.SecretFillField(nil), req.Fields...)
	b.fills = append(b.fills, cp)
	out := map[string]string{}
	for _, f := range req.Fields {
		out[f.Name] = types.SecretFillFilled
	}
	return &sandbox.SecretFillResult{Fields: out}, nil
}

func TestSecretNeverLeavesTheFillPath(t *testing.T) {
	handles, vault, mr := newTestHandles(t)
	gate := approval.NewGate(&config.Config{Agent: &config.AgentConfig{ToolApprovalTimeoutSeconds: 5}}, nil, nil)
	submitter := NewSubmitter(gate, vault, handles)
	browser := &recordingBrowser{}
	bus := event.NewEventBus()

	var mu sync.Mutex
	var events []string
	var promptID string
	record := func(_ context.Context, evt event.Event) error {
		raw, _ := json.Marshal(evt.Data)
		mu.Lock()
		events = append(events, string(raw))
		mu.Unlock()
		return nil
	}
	bus.On(event.EventAgentPromptResolved, record)
	submitErr := make(chan error, 1)
	bus.On(event.EventAgentPromptRequired, func(ctx context.Context, evt event.Event) error {
		_ = record(ctx, evt)
		id := evt.Data.(event.AgentPromptRequiredData).PendingID
		mu.Lock()
		promptID = id
		mu.Unlock()
		go func() {
			_, err := submitter.Submit(context.Background(), alice, SubmitRequest{
				PromptID: id, Action: ActionSubmit,
				Choice: ChoiceSave, Values: map[string]string{"username": "alice", "password": canary},
			})
			submitErr <- err
		}()
		return nil
	})

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, alice.TenantID)
	meta := &tools.ToolExecContext{
		Mode: types.RunModeAttended, EventBus: bus, SessionID: "s1",
		AssistantMessageID: "m1", ToolCallID: "tc1", UserID: alice.UserID,
	}
	ctx = tools.WithToolExecContext(ctx, meta)

	ask := tools.NewRequestSecureFormTool(gate, browser)
	askArgs := json.RawMessage(`{"title":"登录","kind":"login","site":"a.com"}`)
	askRes, err := ask.Execute(ctx, askArgs)
	require.NoError(t, err)
	require.NoError(t, <-submitErr)
	require.True(t, askRes.Success, askRes.Error)

	handle, _ := askRes.Data["handle"].(string)
	require.Equal(t, "cred_1", handle)

	fill := tools.NewBrowserFillSecretTool(handles, browser)
	fillArgs := json.RawMessage(`{"handle":"cred_1","targets":{"username":"@e2","password":"@e3"}}`)
	fillRes, err := fill.Execute(ctx, fillArgs)
	require.NoError(t, err)
	require.True(t, fillRes.Success, fillRes.Error)

	require.Len(t, browser.fills, 1)
	assert.Equal(t, canary, browser.fills[0].Fields[1].Value, "the value does reach the browser")

	askData, _ := json.Marshal(askRes.Data)
	fillData, _ := json.Marshal(fillRes.Data)
	blobs := []string{
		askRes.Output, string(askData), fillRes.Output, string(fillData),
		string(askArgs), string(fillArgs),
	}
	mu.Lock()
	blobs = append(blobs, events...)
	id := promptID
	mu.Unlock()
	row, err := gate.SecureFormPrompt(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, row)
	blobs = append(blobs, string(row.Request), string(row.Response))
	for _, key := range mr.Keys() {
		v, _ := mr.Get(key)
		blobs = append(blobs, v)
	}
	requireNoSecret(t, canary, blobs...)
	requireNoSecret(t, `pw-7f3a`, blobs...)
}
