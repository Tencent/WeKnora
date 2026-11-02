// internal/agent/tools/browser_fill_secret_test.go
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeSecretHandles struct {
	secret    *types.ResolvedSecret
	err       error
	completed []string
}

func (f *fakeSecretHandles) Resolve(context.Context, uint64, string, string, string) (*types.ResolvedSecret, error) {
	return f.secret, f.err
}

func (f *fakeSecretHandles) Complete(_ context.Context, _ uint64, _, _, handle, vaultItemID string) error {
	f.completed = append(f.completed, handle+"|"+vaultItemID)
	return nil
}

func loginSecret() *types.ResolvedSecret {
	return &types.ResolvedSecret{Domains: []string{"a.com"}, VaultItemID: "v1", Fields: []types.SecretField{
		{Name: "username", Type: types.SecretFieldText, Value: "alice"},
		{Name: "password", Type: types.SecretFieldPassword, Value: "sentinel-pw"},
	}}
}

func TestBrowserFillSecret_AllFilledCompletesHandle(t *testing.T) {
	handles := &fakeSecretHandles{secret: loginSecret()}
	browser := &fakeSecureBrowser{result: &sandbox.SecretFillResult{
		Fields: map[string]string{"username": "filled", "password": "filled"},
	}}
	res, err := NewBrowserFillSecretTool(handles, browser).Execute(askCtx(attendedMeta()),
		json.RawMessage(`{"handle":"cred_1","targets":{"username":"@e2","password":"e3"}}`))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, browser.fills, 1)
	fill := browser.fills[0]
	assert.Equal(t, []string{"a.com"}, fill.Domains)
	assert.Equal(t, "@e3", fill.Fields[1].Ref, "a bare ref gets its @")
	assert.Equal(t, []string{"cred_1|v1"}, handles.completed)
	assert.Contains(t, res.Output, "- password: filled")
	raw, _ := json.Marshal(res.Data)
	assert.NotContains(t, res.Output+string(raw), "sentinel-pw")
}

func TestBrowserFillSecret_PartialKeepsHandle(t *testing.T) {
	handles := &fakeSecretHandles{secret: loginSecret()}
	browser := &fakeSecureBrowser{result: &sandbox.SecretFillResult{Fields: map[string]string{
		"username": "filled", "password": "ref_not_found",
	}}}
	res, err := NewBrowserFillSecretTool(handles, browser).Execute(askCtx(attendedMeta()),
		json.RawMessage(`{"handle":"cred_1","targets":{"username":"@e2","password":"@e9"}}`))
	require.NoError(t, err)
	assert.Empty(t, handles.completed)
	assert.Contains(t, res.Output, "fresh snapshot")
}

func TestBrowserFillSecret_SubsetKeepsHandle(t *testing.T) {
	handles := &fakeSecretHandles{secret: loginSecret()}
	browser := &fakeSecureBrowser{result: &sandbox.SecretFillResult{Fields: map[string]string{"username": "filled"}}}
	_, err := NewBrowserFillSecretTool(handles, browser).Execute(askCtx(attendedMeta()),
		json.RawMessage(`{"handle":"cred_1","targets":{"username":"@e2"}}`))
	require.NoError(t, err)
	assert.Empty(t, handles.completed, "the password is still to be filled")
}

func TestBrowserFillSecret_HandleProblems(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status string
	}{
		{types.ErrSecretHandleNotFound, types.SecretFillHandleExpired},
		{types.ErrCredentialUnavailable, types.SecretFillCredentialUnavailable},
	} {
		browser := &fakeSecureBrowser{}
		res, err := NewBrowserFillSecretTool(&fakeSecretHandles{err: tc.err}, browser).Execute(askCtx(attendedMeta()),
			json.RawMessage(`{"handle":"cred_1","targets":{"password":"@e3"}}`))
		require.NoError(t, err)
		assert.True(t, res.Success)
		assert.Contains(t, res.Output, "- password: "+tc.status)
		assert.Contains(t, res.Output, "request_secure_form")
		assert.Empty(t, browser.fills)
	}
}

func TestBrowserFillSecret_RejectsBadArgs(t *testing.T) {
	tool := NewBrowserFillSecretTool(&fakeSecretHandles{secret: loginSecret()}, &fakeSecureBrowser{})
	for _, args := range []string{
		`{"handle":"cred_x","targets":{"password":"@e3"}}`,
		`{"handle":"cred_1","targets":{}}`,
		`{"handle":"cred_1","targets":{"password":"#login"}}`,
		`{"handle":"cred_1","targets":{"pin":"@e3"}}`,
	} {
		res, err := tool.Execute(askCtx(attendedMeta()), json.RawMessage(args))
		require.NoError(t, err)
		assert.False(t, res.Success, args)
	}
}

func TestBrowserFillSecret_HelperMissing(t *testing.T) {
	browser := &fakeSecureBrowser{fillErr: sandbox.ErrSecretFillHelperMissing}
	res, err := NewBrowserFillSecretTool(&fakeSecretHandles{secret: loginSecret()}, browser).Execute(
		askCtx(attendedMeta()), json.RawMessage(`{"handle":"cred_1","targets":{"password":"@e3"}}`))
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "upgrade")
}
