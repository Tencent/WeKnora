// internal/agent/tools/secure_form_test.go
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeSecureFormAsker struct {
	answer  approval.SecureFormAnswer
	asked   []approval.SecureFormRequest
	resumed []*types.AgentPrompt
}

func (f *fakeSecureFormAsker) RequestSecureForm(_ context.Context, req approval.SecureFormRequest) (approval.SecureFormAnswer, error) {
	f.asked = append(f.asked, req)
	return f.answer, nil
}

func (f *fakeSecureFormAsker) ResumeSecureForm(_ context.Context, p *types.AgentPrompt, _ *event.EventBus) approval.SecureFormAnswer {
	f.resumed = append(f.resumed, p)
	return f.answer
}

type fakeSecureBrowser struct {
	page    sandbox.BrowserPage
	pageErr error
	fills   []sandbox.SecretFillRequest
	result  *sandbox.SecretFillResult
	fillErr error
}

func (f *fakeSecureBrowser) CurrentPage(context.Context, string) (sandbox.BrowserPage, error) {
	return f.page, f.pageErr
}

func (f *fakeSecureBrowser) FillSecret(_ context.Context, _ string, req sandbox.SecretFillRequest) (*sandbox.SecretFillResult, error) {
	f.fills = append(f.fills, req)
	return f.result, f.fillErr
}

const loginArgs = `{"title":"登录 Google","kind":"login","site":"accounts.google.com",
	"observed_origin":"https://evil.example"}`

func attendedMeta() *ToolExecContext {
	return &ToolExecContext{Mode: types.RunModeAttended, EventBus: event.NewEventBus(), SessionID: "s1",
		AssistantMessageID: "m1", ToolCallID: "tc1", UserID: "web_user:u1"}
}

func TestRequestSecureForm_NotInteractive(t *testing.T) {
	asker := &fakeSecureFormAsker{}
	res, err := NewRequestSecureFormTool(asker, &fakeSecureBrowser{}).Execute(
		askCtx(&ToolExecContext{EventBus: event.NewEventBus()}), json.RawMessage(loginArgs))
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Contains(t, res.Output, "Never ask for passwords")
	assert.Empty(t, asker.asked)
}

func TestRequestSecureForm_InvalidArgs(t *testing.T) {
	res, err := NewRequestSecureFormTool(&fakeSecureFormAsker{}, &fakeSecureBrowser{}).Execute(
		askCtx(attendedMeta()), json.RawMessage(`{"title":"x","kind":"login","site":"https://a.com"}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "Invalid form")
}

func TestRequestSecureForm_OverwritesObservedOrigin(t *testing.T) {
	asker := &fakeSecureFormAsker{answer: approval.SecureFormAnswer{PromptID: "p1", Answered: true,
		Response: types.SecureFormPromptResponse{Handle: "cred_1", Fields: []string{"username", "password"},
			Username: "alice@example.com", Saved: true}}}
	browser := &fakeSecureBrowser{page: sandbox.BrowserPage{URL: "https://accounts.google.com/v3/signin?x=1"}}
	res, err := NewRequestSecureFormTool(asker, browser).Execute(askCtx(attendedMeta()), json.RawMessage(loginArgs))
	require.NoError(t, err)
	require.True(t, res.Success)
	require.Len(t, asker.asked, 1)
	assert.Equal(t, "https://accounts.google.com", asker.asked[0].Form.ObservedOrigin)
	assert.Equal(t, "The user provided credentials for accounts.google.com.\n"+
		"handle: cred_1  fields: username, password  username: alice@example.com  saved: true\n"+
		"Take a fresh snapshot, then call browser_fill_secret with this handle.", res.Output)
	raw, _ := json.Marshal(res.Data)
	assert.Contains(t, string(raw), `"handle":"cred_1"`)
}

func TestRequestSecureForm_ProbeFailureStillAsks(t *testing.T) {
	asker := &fakeSecureFormAsker{answer: approval.SecureFormAnswer{PromptID: "p1", Reason: "the user declined to provide credentials"}}
	browser := &fakeSecureBrowser{pageErr: errors.New("no browser")}
	res, err := NewRequestSecureFormTool(asker, browser).Execute(askCtx(attendedMeta()), json.RawMessage(loginArgs))
	require.NoError(t, err)
	require.Len(t, asker.asked, 1)
	assert.Equal(t, "", asker.asked[0].Form.ObservedOrigin)
	assert.Contains(t, res.Output, "did not provide credentials")
}

func TestRequestSecureForm_Suspends(t *testing.T) {
	suspension := &types.PromptSuspension{PromptID: "p1", Kind: types.AgentPromptKindSecureForm}
	asker := &fakeSecureFormAsker{answer: approval.SecureFormAnswer{PromptID: "p1", Suspended: suspension}}
	res, err := NewRequestSecureFormTool(asker, &fakeSecureBrowser{}).Execute(askCtx(attendedMeta()), json.RawMessage(loginArgs))
	require.NoError(t, err)
	assert.Same(t, suspension, res.Suspend)
}

func TestRequestSecureForm_Resumed(t *testing.T) {
	asker := &fakeSecureFormAsker{answer: approval.SecureFormAnswer{PromptID: "p1", Answered: true,
		Response: types.SecureFormPromptResponse{Handle: "cred_2", Fields: []string{"password"}}}}
	meta := attendedMeta()
	meta.Answered = &types.AgentPrompt{ID: "p1", Kind: types.AgentPromptKindSecureForm}
	browser := &fakeSecureBrowser{}
	res, err := NewRequestSecureFormTool(asker, browser).Execute(askCtx(meta), json.RawMessage(loginArgs))
	require.NoError(t, err)
	assert.Len(t, asker.resumed, 1)
	assert.Empty(t, asker.asked)
	assert.Contains(t, res.Output, "handle: cred_2")
}
