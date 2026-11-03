package sandbox

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	secretFillBinary   = "/usr/local/bin/wk-secret-fill"
	secretFillKeyDir   = "/dev/shm"
	secretFillTimeout  = 30 * time.Second
	secretProbeTimeout = 10 * time.Second
)

// ErrSecretFillHelperMissing means the session's image predates the helper.
var ErrSecretFillHelperMissing = errors.New(
	"sandbox: wk-secret-fill is not installed in this sandbox image; rebuild or upgrade the desktop image")

// BrowserPage is the page a session browser shows. An empty URL means it
// could not be read, including when no browser runs.
type BrowserPage struct {
	URL string
}

// SecretFillField is one value to type into the input a snapshot ref names.
type SecretFillField struct {
	Name  string `json:"name"`
	Ref   string `json:"ref"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// SecretFillRequest is what the helper decrypts.
type SecretFillRequest struct {
	Domains []string          `json:"domains"`
	Fields  []SecretFillField `json:"fields"`
}

// SecretFillResult maps each field name to its outcome.
type SecretFillResult struct {
	Fields map[string]string `json:"fields"`
}

// SessionSecureBrowser advertises the capability while the manager is open.
func (m *SessionBoundManager) SessionSecureBrowser() SessionSecureBrowser {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	return m
}

// CurrentPage reads the top-level URL of the session browser.
func (m *SessionBoundManager) CurrentPage(ctx context.Context, sessionID string) (BrowserPage, error) {
	out, err := m.runSecretHelper(ctx, sessionID, "", nil, []string{"--probe"}, secretProbeTimeout)
	if err != nil {
		return BrowserPage{}, err
	}
	var probe struct {
		URL   *string `json:"url"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &probe); err != nil {
		return BrowserPage{}, errors.New("sandbox: unreadable browser probe output")
	}
	if probe.Error != "" {
		return BrowserPage{}, fmt.Errorf("sandbox: browser probe: %s", probe.Error)
	}
	if probe.URL == nil {
		return BrowserPage{}, nil
	}
	return BrowserPage{URL: *probe.URL}, nil
}

// FillSecret hands req to the helper without putting a value on any channel
// envd logs: the one-time key travels through the file API, which logs only
// the path, and argv carries only ciphertext.
func (m *SessionBoundManager) FillSecret(
	ctx context.Context, sessionID string, req SecretFillRequest,
) (*SecretFillResult, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	defer clear(key)
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	sealed, err := sealSecretPayload(key, payload)
	clear(payload)
	if err != nil {
		return nil, err
	}
	name := make([]byte, 16)
	if _, err := rand.Read(name); err != nil {
		return nil, err
	}
	keyPath := secretFillKeyDir + "/wk-fill-" + hex.EncodeToString(name)
	out, err := m.runSecretHelper(ctx, sessionID, keyPath, key,
		[]string{"--key-file", keyPath, sealed}, secretFillTimeout)
	if err != nil {
		return nil, err
	}
	var res struct {
		Fields map[string]string `json:"fields"`
		Error  string            `json:"error"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		return nil, errors.New("sandbox: unreadable wk-secret-fill output")
	}
	switch res.Error {
	case "":
	case "no_browser":
		return nil, errors.New("sandbox: the session browser is not running")
	case "browser_busy":
		return nil, errors.New("sandbox: the session browser stayed busy; retry")
	default:
		return nil, fmt.Errorf("sandbox: wk-secret-fill: %s", res.Error)
	}
	return &SecretFillResult{Fields: res.Fields}, nil
}

// runSecretHelper runs wk-secret-fill in the session's sandbox. With keyPath
// set it stages key there first and always removes it afterwards.
func (m *SessionBoundManager) runSecretHelper(
	ctx context.Context, sessionID, keyPath string, key []byte, args []string, timeout time.Duration,
) (string, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return "", err
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("sandbox: session ID required for the browser helper")
	}
	sc, err := m.scope(ctx, sessionID)
	if err != nil {
		return "", err
	}
	ctx, release, err := m.admitUser(ctx, sc, "shell")
	if err != nil {
		return "", err
	}
	defer release()
	handle, err := m.resolveScope(ctx, sc)
	if err != nil {
		return "", err
	}
	if keyPath != "" {
		defer func() { _ = m.client.Remove(context.WithoutCancel(ctx), handle, keyPath) }()
		encoded := []byte(base64.StdEncoding.EncodeToString(key))
		err := m.client.WriteFile(ctx, handle, keyPath, encoded)
		clear(encoded)
		if err != nil {
			return "", fmt.Errorf("sandbox: stage secret fill key: %w", err)
		}
	}
	env := map[string]string{}
	if sc.user {
		env = withSessionEnv(env, sc.layout)
	}
	env = withBrowserEnv(env, sc.key.SessionID, sc.layout)
	res, err := m.client.Exec(ctx, handle, RemoteExecRequest{
		Command: secretFillBinary,
		Args:    args,
		Env:     env,
		WorkDir: "/",
		User:    DefaultSandboxExecUser,
		Timeout: timeout,
	})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return "", ErrSecretFillHelperMissing
		}
		return "", fmt.Errorf("sandbox: run wk-secret-fill: %w", err)
	}
	switch {
	case res == nil:
		return "", errors.New("sandbox: wk-secret-fill returned no result")
	case res.ExitCode == 127:
		return "", ErrSecretFillHelperMissing
	case res.Killed:
		return "", errors.New("sandbox: wk-secret-fill timed out")
	case res.ExitCode != 0:
		return "", fmt.Errorf("sandbox: wk-secret-fill exited with %d", res.ExitCode)
	}
	return res.Stdout, nil
}

// sealSecretPayload returns base64(nonce || AES-256-GCM(payload)).
func sealSecretPayload(key, payload []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, payload, nil)), nil
}
