package chat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const codexOAuthRedirectURI = "http://localhost:1455/auth/callback"

var (
	codexAuthorizeEndpoint    = "https://auth.openai.com/oauth/authorize"
	codexOAuthDefaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

	codexPendingOAuthMu sync.Mutex
	codexPendingOAuth   = map[string]codexPendingOAuthState{}
)

type codexPendingOAuthState struct {
	CodeVerifier string
	CreatedAt    time.Time
}

type CodexOAuthStartResult struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
	RedirectURI  string `json:"redirect_uri"`
	AuthFile     string `json:"auth_file"`
}

type CodexOAuthCompleteRequest struct {
	AuthFile     string `json:"auth_file"`
	CallbackURL  string `json:"callback_url"`
	Code         string `json:"code"`
	State        string `json:"state"`
	CodeVerifier string `json:"code_verifier,omitempty"`
}

func codexOAuthClientID() string {
	for _, key := range []string{"WEKNORA_CODEX_OAUTH_CLIENT_ID", "OPENAI_CODEX_OAUTH_CLIENT_ID"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return codexOAuthDefaultClientID
}

func StartCodexOAuth(authFile string) (*CodexOAuthStartResult, error) {
	verifier, err := newCodexCodeVerifier()
	if err != nil {
		return nil, err
	}
	state, err := newCodexOAuthState()
	if err != nil {
		return nil, err
	}
	challenge := codexCodeChallenge(verifier)
	values := []struct {
		key   string
		value string
	}{
		{"response_type", "code"},
		{"client_id", codexOAuthClientID()},
		{"redirect_uri", codexOAuthRedirectURI},
		{"scope", codexOAuthScope},
		{"code_challenge", challenge},
		{"code_challenge_method", "S256"},
		{"id_token_add_organizations", "true"},
		{"codex_cli_simplified_flow", "true"},
		{"state", state},
		{"originator", "codex_cli_rs"},
	}
	parts := make([]string, 0, len(values))
	for _, item := range values {
		parts = append(parts, url.QueryEscape(item.key)+"="+strings.ReplaceAll(url.QueryEscape(item.value), "+", "%20"))
	}

	codexPendingOAuthMu.Lock()
	codexPendingOAuth[state] = codexPendingOAuthState{CodeVerifier: verifier, CreatedAt: time.Now().UTC()}
	codexPendingOAuthMu.Unlock()

	return &CodexOAuthStartResult{
		AuthorizeURL: codexAuthorizeEndpoint + "?" + strings.Join(parts, "&"),
		State:        state,
		RedirectURI:  codexOAuthRedirectURI,
		AuthFile:     resolveCodexAuthFile(authFile),
	}, nil
}

func CompleteCodexOAuth(ctx context.Context, req CodexOAuthCompleteRequest) (*CodexOAuthStatus, error) {
	code, state, err := codexCodeAndState(req)
	if err != nil {
		return nil, err
	}
	verifier := strings.TrimSpace(req.CodeVerifier)
	if verifier == "" {
		var ok bool
		verifier, ok = takeCodexPendingVerifier(state)
		if !ok {
			return nil, fmt.Errorf("unknown or expired codex OAuth state")
		}
	}
	tokenResp, err := exchangeCodexOAuthCode(ctx, code, verifier)
	if err != nil {
		return nil, err
	}
	return saveCodexOAuthTokens(req.AuthFile, tokenResp)
}

func codexCodeAndState(req CodexOAuthCompleteRequest) (string, string, error) {
	code := strings.TrimSpace(req.Code)
	state := strings.TrimSpace(req.State)
	if strings.TrimSpace(req.CallbackURL) != "" {
		u, err := url.Parse(strings.TrimSpace(req.CallbackURL))
		if err != nil {
			return "", "", fmt.Errorf("parse callback URL: %w", err)
		}
		q := u.Query()
		if code == "" {
			code = q.Get("code")
		}
		if state == "" {
			state = q.Get("state")
		}
	}
	if code == "" || state == "" {
		return "", "", fmt.Errorf("codex OAuth completion requires code and state")
	}
	return code, state, nil
}

func takeCodexPendingVerifier(state string) (string, bool) {
	codexPendingOAuthMu.Lock()
	defer codexPendingOAuthMu.Unlock()
	pending, ok := codexPendingOAuth[state]
	if !ok {
		return "", false
	}
	delete(codexPendingOAuth, state)
	if time.Since(pending.CreatedAt) > 15*time.Minute {
		return "", false
	}
	return pending.CodeVerifier, true
}

func exchangeCodexOAuthCode(ctx context.Context, code, verifier string) (codexTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", codexOAuthClientID())
	form.Set("code", code)
	form.Set("redirect_uri", codexOAuthRedirectURI)
	form.Set("code_verifier", verifier)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return codexTokenResponse{}, fmt.Errorf("create codex token exchange request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := rawHTTPClient.Do(httpReq)
	if err != nil {
		return codexTokenResponse{}, fmt.Errorf("exchange codex OAuth code: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return codexTokenResponse{}, fmt.Errorf("read codex token exchange response: %w", err)
	}
	var tokenResp codexTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return codexTokenResponse{}, fmt.Errorf("decode codex token exchange response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return codexTokenResponse{}, fmt.Errorf("codex OAuth code exchange failed with status %d: %s", resp.StatusCode, tokenResp.messageOrBody(body))
	}
	return tokenResp, nil
}

func newCodexCodeVerifier() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate codex OAuth code verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func codexCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newCodexOAuthState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate codex OAuth state: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
