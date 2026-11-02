// internal/sandbox/secret_fill_test.go
package sandbox

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openSecretPayload(t *testing.T, keyB64, sealed string) SecretFillRequest {
	t.Helper()
	key, err := base64.StdEncoding.DecodeString(keyB64)
	require.NoError(t, err)
	require.Len(t, key, 32)
	raw, err := base64.StdEncoding.DecodeString(sealed)
	require.NoError(t, err)
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	plain, err := gcm.Open(nil, raw[:12], raw[12:], nil)
	require.NoError(t, err)
	var req SecretFillRequest
	require.NoError(t, json.Unmarshal(plain, &req))
	return req
}

func helperRequests(c *fakeRemoteClient) []RemoteExecRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []RemoteExecRequest
	for _, r := range c.execRequests {
		if r.Command == secretFillBinary {
			out = append(out, r)
		}
	}
	return out
}

func TestFillSecretSplitsKeyAndCiphertext(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerDesktopTestHarness(t)
	inner := client.fakeRemoteClient
	inner.execHook = func(r RemoteExecRequest) (*RemoteExecResult, error) {
		if r.Command == secretFillBinary {
			return &RemoteExecResult{Stdout: `{"fields":{"password":"filled"}}` + "\n"}, nil
		}
		return &RemoteExecResult{}, nil
	}
	res, err := mgr.FillSecret(ctx, "sess-live", SecretFillRequest{
		Domains: []string{"a.com"},
		Fields:  []SecretFillField{{Name: "password", Ref: "@e3", Type: "password", Value: "sentinel-pw"}},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"password": "filled"}, res.Fields)

	reqs := helperRequests(inner)
	require.Len(t, reqs, 1)
	r := reqs[0]
	assert.False(t, r.Shell)
	require.Len(t, r.Args, 3)
	assert.Equal(t, "--key-file", r.Args[0])
	keyPath := r.Args[1]
	assert.True(t, strings.HasPrefix(keyPath, "/dev/shm/wk-fill-"))
	assert.NotContains(t, strings.Join(r.Args, " "), "sentinel-pw", "argv carries only ciphertext")
	assert.NotEmpty(t, r.Env["WEKNORA_BROWSER_ROOT"])

	inner.mu.Lock()
	writes := append([]fakeRemoteWriteFile(nil), inner.writeFiles...)
	removed := append([]string(nil), inner.removePaths...)
	inner.mu.Unlock()
	var keyB64 string
	for _, w := range writes {
		if w.path == keyPath {
			keyB64 = string(w.content)
		}
	}
	require.NotEmpty(t, keyB64, "the key goes through WriteFile")
	assert.NotContains(t, keyB64, "sentinel-pw")
	payload := openSecretPayload(t, keyB64, r.Args[2])
	assert.Equal(t, "sentinel-pw", payload.Fields[0].Value)
	assert.Equal(t, []string{"a.com"}, payload.Domains)
	assert.Contains(t, removed, keyPath, "the key file is removed afterwards")
}

func TestFillSecretRemovesKeyOnFailure(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerDesktopTestHarness(t)
	inner := client.fakeRemoteClient
	inner.execHook = func(r RemoteExecRequest) (*RemoteExecResult, error) {
		if r.Command == secretFillBinary {
			return nil, errors.New("boom")
		}
		return &RemoteExecResult{}, nil
	}
	_, err := mgr.FillSecret(ctx, "sess-live", SecretFillRequest{Domains: []string{"a.com"},
		Fields: []SecretFillField{{Name: "password", Ref: "@e1", Type: "password", Value: "x"}}})
	require.Error(t, err)
	reqs := helperRequests(inner)
	require.Len(t, reqs, 1)
	inner.mu.Lock()
	defer inner.mu.Unlock()
	assert.Contains(t, inner.removePaths, reqs[0].Args[1])
}

func TestFillSecretReportsMissingHelper(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerDesktopTestHarness(t)
	client.fakeRemoteClient.execHook = func(r RemoteExecRequest) (*RemoteExecResult, error) {
		if r.Command == secretFillBinary {
			return &RemoteExecResult{ExitCode: 127}, nil
		}
		return &RemoteExecResult{}, nil
	}
	_, err := mgr.FillSecret(ctx, "sess-live", SecretFillRequest{Domains: []string{"a.com"},
		Fields: []SecretFillField{{Name: "otp", Ref: "@e1", Type: "one_time_code", Value: "1"}}})
	assert.ErrorIs(t, err, ErrSecretFillHelperMissing)
}

func TestFillSecretHelperError(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerDesktopTestHarness(t)
	client.fakeRemoteClient.execHook = func(r RemoteExecRequest) (*RemoteExecResult, error) {
		if r.Command == secretFillBinary {
			return &RemoteExecResult{Stdout: `{"error":"no_browser"}`}, nil
		}
		return &RemoteExecResult{}, nil
	}
	_, err := mgr.FillSecret(ctx, "sess-live", SecretFillRequest{Domains: []string{"a.com"},
		Fields: []SecretFillField{{Name: "otp", Ref: "@e1", Type: "one_time_code", Value: "1"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "browser is not running")
}

func TestCurrentPage(t *testing.T) {
	ctx := terminalTestContext()
	mgr, client := newSessionManagerDesktopTestHarness(t)
	out := `{"url":"https://accounts.google.com/signin"}`
	client.fakeRemoteClient.execHook = func(r RemoteExecRequest) (*RemoteExecResult, error) {
		if r.Command == secretFillBinary {
			assert.Equal(t, []string{"--probe"}, r.Args)
			return &RemoteExecResult{Stdout: out}, nil
		}
		return &RemoteExecResult{}, nil
	}
	page, err := mgr.CurrentPage(ctx, "sess-live")
	require.NoError(t, err)
	assert.Equal(t, "https://accounts.google.com/signin", page.URL)

	out = `{"url":null}`
	page, err = mgr.CurrentPage(ctx, "sess-live")
	require.NoError(t, err)
	assert.Equal(t, "", page.URL)
	client.fakeRemoteClient.mu.Lock()
	defer client.fakeRemoteClient.mu.Unlock()
	assert.Empty(t, client.fakeRemoteClient.writeFiles, "the probe stages no key")
}

func TestSessionSecureBrowserCapability(t *testing.T) {
	mgr, _ := newSessionManagerDesktopTestHarness(t)
	var _ SessionSecureBrowserProvider = mgr
	assert.NotNil(t, mgr.SessionSecureBrowser())
}
