package credential

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakePromptGate struct {
	prompts  map[string]*types.AgentPrompt
	resolved []hitl.ResolveRequest
	failWith error
}

func (f *fakePromptGate) SecureFormPrompt(_ context.Context, id string) (*types.AgentPrompt, error) {
	return f.prompts[id], nil
}

func (f *fakePromptGate) ResolveSecureForm(_ context.Context, req hitl.ResolveRequest) (hitl.Resolution, error) {
	if f.failWith != nil {
		return hitl.Resolution{}, f.failWith
	}
	f.resolved = append(f.resolved, req)
	return hitl.Resolution{Prompt: f.prompts[req.PromptID]}, nil
}

func loginPrompt(t *testing.T, id string, owner Owner, extra ...types.SecureFormExtraField) *types.AgentPrompt {
	t.Helper()
	form := types.SecureFormPromptRequest{Title: "登录", Kind: "login", Site: "a.com",
		Domains: []string{"a.com", "sso.a.com"}, Extra: extra}
	require.NoError(t, form.Validate())
	raw, err := json.Marshal(form)
	require.NoError(t, err)
	return &types.AgentPrompt{ID: id, TenantID: owner.TenantID, UserID: owner.UserID, SessionID: "s1",
		Kind: types.AgentPromptKindSecureForm, Status: types.AgentPromptPending, Request: types.JSON(raw)}
}

func newTestSubmitter(t *testing.T, prompts ...*types.AgentPrompt) (*Submitter, *fakePromptGate, *HandleStore, *Vault) {
	t.Helper()
	handles, vault, _ := newTestHandles(t)
	gate := &fakePromptGate{prompts: map[string]*types.AgentPrompt{}}
	for _, p := range prompts {
		gate.prompts[p.ID] = p
	}
	return NewSubmitter(gate, vault, handles), gate, handles, vault
}

func lastResponse(t *testing.T, g *fakePromptGate) types.SecureFormPromptResponse {
	t.Helper()
	require.NotEmpty(t, g.resolved)
	var r types.SecureFormPromptResponse
	require.NoError(t, json.Unmarshal(g.resolved[len(g.resolved)-1].Response, &r))
	return r
}

func TestSubmitUseOnce(t *testing.T) {
	otp := types.SecureFormExtraField{Name: "otp", Type: types.SecretFieldOneTimeCode}
	s, gate, handles, _ := newTestSubmitter(t, loginPrompt(t, "p1", alice, otp))
	_, err := s.Submit(context.Background(), alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit,
		Choice: ChoiceUseOnce, Values: map[string]string{"username": " alice ", "password": "sentinel-pw", "otp": "123456"}})
	require.NoError(t, err)
	r := lastResponse(t, gate)
	assert.Equal(t, []string{"username", "password", "otp"}, r.Fields)
	assert.Equal(t, "alice", r.Username)
	assert.False(t, r.Saved)
	requireNoSecret(t, "sentinel-pw", string(gate.resolved[0].Response))
	requireNoSecret(t, "123456", string(gate.resolved[0].Response))

	got, err := handles.Resolve(context.Background(), alice.TenantID, alice.UserID, "s1", r.Handle)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com", "sso.a.com"}, got.Domains)
	assert.Equal(t, "sentinel-pw", got.Fields[1].Value)
	assert.Equal(t, "123456", got.Fields[2].Value)
}

func TestSubmitRejectsBadValues(t *testing.T) {
	s, gate, _, _ := newTestSubmitter(t, loginPrompt(t, "p1", alice))
	ctx := context.Background()
	_, err := s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceUseOnce,
		Values: map[string]string{"username": "u", "password": "p", "pin": "sentinel-pin"}})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	assert.Contains(t, err.Error(), "pin")
	requireNoSecret(t, "sentinel-pin", err.Error())

	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceUseOnce,
		Values: map[string]string{"username": "u"}})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err), "password is required")

	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceUseOnce,
		Values: map[string]string{"username": "u", "password": string(make([]byte, 4097))}})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	assert.Empty(t, gate.resolved)
}

func TestSubmitSaveThenSaved(t *testing.T) {
	s, gate, handles, vault := newTestSubmitter(t, loginPrompt(t, "p1", alice), loginPrompt(t, "p2", alice))
	ctx := context.Background()
	_, err := s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceSave,
		Values: map[string]string{"username": "alice", "password": "vault-pw"}})
	require.NoError(t, err)
	r := lastResponse(t, gate)
	assert.True(t, r.Saved)
	items, err := vault.List(ctx, alice, "a.com")
	require.NoError(t, err)
	require.Len(t, items, 1)

	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p2", Action: ActionSubmit, Choice: ChoiceSaved,
		VaultItemID: items[0].ID})
	require.NoError(t, err)
	r = lastResponse(t, gate)
	assert.Equal(t, "alice", r.Username)
	got, err := handles.Resolve(ctx, alice.TenantID, alice.UserID, "s1", r.Handle)
	require.NoError(t, err)
	assert.Equal(t, "vault-pw", got.Fields[1].Value)
	assert.Equal(t, items[0].ID, got.VaultItemID)
}

func TestSubmitSavedChecksSiteAndDomains(t *testing.T) {
	s, _, _, vault := newTestSubmitter(t, loginPrompt(t, "p1", alice))
	ctx := context.Background()
	other, err := vault.Create(ctx, alice, LoginInput{Site: "b.com", Username: "x", Password: "p"})
	require.NoError(t, err)
	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceSaved, VaultItemID: other.ID})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceSaved, VaultItemID: "nope"})
	assert.Equal(t, http.StatusNotFound, httpStatus(t, err))
}

func TestSubmitDomainsAreTheIntersectionForSaved(t *testing.T) {
	// The agent asks for an extra host the saved login does not allow.
	form := types.SecureFormPromptRequest{Title: "登录", Kind: "login", Site: "a.com", Domains: []string{"sso.a.com"}}
	require.NoError(t, form.Validate())
	raw, _ := json.Marshal(form)
	p := &types.AgentPrompt{ID: "p1", TenantID: 1, UserID: alice.UserID, SessionID: "s1",
		Kind: types.AgentPromptKindSecureForm, Status: types.AgentPromptPending, Request: types.JSON(raw)}
	s, gate, handles, vault := newTestSubmitter(t, p)
	ctx := context.Background()
	item, err := vault.Create(ctx, alice, LoginInput{Site: "a.com", Username: "x", Password: "p"})
	require.NoError(t, err)
	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceSaved, VaultItemID: item.ID})
	require.NoError(t, err)
	got, err := handles.Resolve(ctx, alice.TenantID, alice.UserID, "s1", lastResponse(t, gate).Handle)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com"}, got.Domains, "sso.a.com is not allowed for the saved password")
}

func TestSubmitSaveOverExistingKeepsItsDomains(t *testing.T) {
	s, gate, handles, vault := newTestSubmitter(t, loginPrompt(t, "p1", alice))
	ctx := context.Background()
	item, err := vault.Create(ctx, alice, LoginInput{Site: "a.com", Username: "alice", Password: "old"})
	require.NoError(t, err)
	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceSave,
		Values: map[string]string{"username": "alice", "password": "new"}})
	require.NoError(t, err)

	stored, err := vault.Get(ctx, alice, item.ID)
	require.NoError(t, err)
	assert.Equal(t, types.StringArray{"a.com"}, stored.Domains, "the card's sso.a.com is not added")
	got, err := handles.Resolve(ctx, alice.TenantID, alice.UserID, "s1", lastResponse(t, gate).Handle)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.com"}, got.Domains)
	assert.Equal(t, "new", got.Fields[1].Value)
}

func TestSubmitDecline(t *testing.T) {
	s, gate, _, _ := newTestSubmitter(t, loginPrompt(t, "p1", alice))
	_, err := s.Submit(context.Background(), alice, SubmitRequest{PromptID: "p1", Action: ActionDecline})
	require.NoError(t, err)
	assert.True(t, lastResponse(t, gate).Declined)
}

func TestSubmitOwnership(t *testing.T) {
	closed := loginPrompt(t, "p2", alice)
	closed.Status = types.AgentPromptExpired
	s, _, _, _ := newTestSubmitter(t, loginPrompt(t, "p1", alice), closed)
	ctx := context.Background()
	_, err := s.Submit(ctx, bob, SubmitRequest{PromptID: "p1", Action: ActionDecline})
	assert.ErrorIs(t, err, hitl.ErrUserMismatch)
	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "missing", Action: ActionDecline})
	assert.ErrorIs(t, err, hitl.ErrPromptNotFound)
	_, err = s.Submit(ctx, alice, SubmitRequest{PromptID: "p2", Action: ActionDecline})
	assert.ErrorIs(t, err, hitl.ErrAlreadyResolved)
}

func TestSubmitResolveRaceDiscardsHandleButKeepsSavedLogin(t *testing.T) {
	s, gate, handles, vault := newTestSubmitter(t, loginPrompt(t, "p1", alice))
	gate.failWith = hitl.ErrAlreadyResolved
	ctx := context.Background()
	res, err := s.Submit(ctx, alice, SubmitRequest{PromptID: "p1", Action: ActionSubmit, Choice: ChoiceSave,
		Values: map[string]string{"username": "alice", "password": "pw"}})
	require.ErrorIs(t, err, hitl.ErrAlreadyResolved)
	assert.True(t, res.SavedButClosed)
	items, err := vault.List(ctx, alice, "a.com")
	require.NoError(t, err)
	assert.Len(t, items, 1, "the user asked to save; the login stays")
	_, err = handles.Resolve(ctx, alice.TenantID, alice.UserID, "s1", "cred_1")
	assert.ErrorIs(t, err, types.ErrSecretHandleNotFound, "the orphan handle is gone")
}
