package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultCodexAuthFile     = "/data/weknora/codex_auth.json"
	codexOAuthRefreshSkew    = 5 * time.Minute
	codexOAuthRefreshMaxAge  = 55 * time.Minute
	codexOAuthFilePermission = 0o600
)

var (
	codexTokenEndpoint = "https://auth.openai.com/oauth/token"
	codexOAuthScope    = "openid profile email offline_access"

	codexTokenSourcesMu sync.Mutex
	codexTokenSources   = map[string]*codexTokenSource{}
)

type codexTokenFile struct {
	Tokens      codexTokens `json:"tokens"`
	LastRefresh time.Time   `json:"last_refresh"`
}

type codexTokens struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

type codexTokenResponse struct {
	IDToken      string               `json:"id_token"`
	AccessToken  string               `json:"access_token"`
	RefreshToken string               `json:"refresh_token"`
	ExpiresIn    int                  `json:"expires_in,omitempty"`
	Error        codexOAuthErrorField `json:"error,omitempty"`
	ErrorDesc    codexOAuthErrorField `json:"error_description,omitempty"`
}

type codexOAuthErrorField struct {
	Message string
}

func (e *codexOAuthErrorField) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		e.Message = text
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		e.Message = string(data)
		return nil
	}
	for _, key := range []string{"message", "error", "description", "type", "code"} {
		if value, ok := obj[key].(string); ok && value != "" {
			e.Message = value
			return nil
		}
	}
	if b, err := json.Marshal(obj); err == nil {
		e.Message = string(b)
	}
	return nil
}

func (e codexOAuthErrorField) String() string {
	return e.Message
}

type codexTokenSource struct {
	authFile string
	client   *http.Client
	mu       sync.RWMutex
	cached   *codexTokenFile
}

type CodexOAuthStatus struct {
	Configured bool      `json:"configured"`
	AccountID  string    `json:"account_id,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	AuthFile   string    `json:"auth_file,omitempty"`
}

func resolveCodexAuthFile(authFile string) string {
	if strings.TrimSpace(authFile) != "" {
		return strings.TrimSpace(authFile)
	}
	if env := strings.TrimSpace(os.Getenv("WEKNORA_CODEX_AUTH_FILE")); env != "" {
		return env
	}
	return defaultCodexAuthFile
}

func getCodexTokenSource(authFile string) *codexTokenSource {
	path := resolveCodexAuthFile(authFile)
	codexTokenSourcesMu.Lock()
	defer codexTokenSourcesMu.Unlock()
	if source, ok := codexTokenSources[path]; ok {
		return source
	}
	source := &codexTokenSource{
		authFile: path,
		client:   rawHTTPClient,
	}
	codexTokenSources[path] = source
	return source
}

func (s *codexTokenSource) bearer(ctx context.Context) (accessToken, accountID string, err error) {
	now := time.Now()
	s.mu.RLock()
	file := cloneCodexTokenFile(s.cached)
	if file != nil && !shouldRefreshCodexTokens(file, now) {
		s.mu.RUnlock()
		return codexBearerFromFile(file)
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	file = s.cached
	if file == nil {
		file, err = readCodexTokenFile(s.authFile)
		if err != nil {
			return "", "", err
		}
		s.cached = cloneCodexTokenFile(file)
	} else {
		file = cloneCodexTokenFile(file)
	}
	if file.Tokens.AccessToken == "" || file.Tokens.RefreshToken == "" {
		return "", "", fmt.Errorf("codex OAuth credentials are incomplete")
	}

	if shouldRefreshCodexTokens(file, now) {
		if err := s.refreshLocked(ctx, file); err != nil {
			return "", "", err
		}
	} else {
		s.cached = cloneCodexTokenFile(file)
	}
	return codexBearerFromFile(file)
}

func codexBearerFromFile(file *codexTokenFile) (accessToken, accountID string, err error) {
	accountID = file.Tokens.AccountID
	if accountID == "" {
		accountID = accountIDFromIDToken(file.Tokens.IDToken)
	}
	if accountID == "" {
		return "", "", fmt.Errorf("codex OAuth credentials missing chatgpt account id")
	}
	return file.Tokens.AccessToken, accountID, nil
}

func (s *codexTokenSource) refreshLocked(ctx context.Context, file *codexTokenFile) error {
	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": file.Tokens.RefreshToken,
		"client_id":     codexOAuthClientID(),
		"scope":         codexOAuthScope,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal codex refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenEndpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create codex refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh codex OAuth token: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read codex refresh response: %w", err)
	}

	var tokenResp codexTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return fmt.Errorf("decode codex refresh response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("codex OAuth refresh failed with status %d: %s", resp.StatusCode, tokenResp.messageOrBody(respBody))
	}
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("codex OAuth refresh response missing access_token")
	}

	file.Tokens.AccessToken = tokenResp.AccessToken
	if tokenResp.IDToken != "" {
		file.Tokens.IDToken = tokenResp.IDToken
	}
	if tokenResp.RefreshToken != "" {
		file.Tokens.RefreshToken = tokenResp.RefreshToken
	}
	if accountID := accountIDFromIDToken(file.Tokens.IDToken); accountID != "" {
		file.Tokens.AccountID = accountID
	}
	file.LastRefresh = time.Now().UTC()
	if err := writeCodexTokenFileWithRetry(s.authFile, file); err != nil {
		return err
	}
	s.cached = cloneCodexTokenFile(file)
	return nil
}

func cloneCodexTokenFile(file *codexTokenFile) *codexTokenFile {
	if file == nil {
		return nil
	}
	copy := *file
	return &copy
}

func readCodexTokenFile(path string) (*codexTokenFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("codex OAuth credentials not found at %s", path)
		}
		return nil, fmt.Errorf("read codex OAuth credentials: %w", err)
	}
	var file codexTokenFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode codex OAuth credentials: %w", err)
	}
	return &file, nil
}

func writeCodexTokenFile(path string, file *codexTokenFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create codex OAuth credentials dir: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode codex OAuth credentials: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, codexOAuthFilePermission); err != nil {
		return fmt.Errorf("write codex OAuth credentials: %w", err)
	}
	if err := os.Chmod(tmp, codexOAuthFilePermission); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod codex OAuth credentials: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace codex OAuth credentials: %w", err)
	}
	return nil
}

func writeCodexTokenFileWithRetry(path string, file *codexTokenFile) error {
	err := writeCodexTokenFile(path, file)
	if err == nil {
		return nil
	}
	time.Sleep(100 * time.Millisecond)
	if retryErr := writeCodexTokenFile(path, file); retryErr == nil {
		return nil
	} else {
		return fmt.Errorf("%w (retry failed: %v)", err, retryErr)
	}
}

func saveCodexOAuthTokens(authFile string, tokenResp codexTokenResponse) (*CodexOAuthStatus, error) {
	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("codex OAuth token response missing access_token or refresh_token")
	}
	accountID := accountIDFromIDToken(tokenResp.IDToken)
	file := &codexTokenFile{
		Tokens: codexTokens{
			IDToken:      tokenResp.IDToken,
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			AccountID:    accountID,
		},
		LastRefresh: time.Now().UTC(),
	}
	path := resolveCodexAuthFile(authFile)
	if err := writeCodexTokenFileWithRetry(path, file); err != nil {
		return nil, err
	}
	if source := getCodexTokenSource(path); source != nil {
		source.mu.Lock()
		source.cached = cloneCodexTokenFile(file)
		source.mu.Unlock()
	}
	return codexOAuthStatusFromFile(path, file), nil
}

func GetCodexOAuthStatus(authFile string) (*CodexOAuthStatus, error) {
	path := resolveCodexAuthFile(authFile)
	file, err := readCodexTokenFile(path)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return &CodexOAuthStatus{Configured: false, AuthFile: path}, nil
		}
		return nil, err
	}
	return codexOAuthStatusFromFile(path, file), nil
}

func codexOAuthStatusFromFile(path string, file *codexTokenFile) *CodexOAuthStatus {
	accountID := file.Tokens.AccountID
	if accountID == "" {
		accountID = accountIDFromIDToken(file.Tokens.IDToken)
	}
	return &CodexOAuthStatus{
		Configured: file.Tokens.AccessToken != "" && file.Tokens.RefreshToken != "",
		AccountID:  accountID,
		ExpiresAt:  expiryFromJWT(file.Tokens.AccessToken),
		AuthFile:   path,
	}
}

func shouldRefreshCodexTokens(file *codexTokenFile, now time.Time) bool {
	if file == nil {
		return true
	}
	if !file.LastRefresh.IsZero() && now.Sub(file.LastRefresh) >= codexOAuthRefreshMaxAge {
		return true
	}
	expiresAt := expiryFromJWT(file.Tokens.AccessToken)
	if expiresAt.IsZero() {
		return true
	}
	return !expiresAt.After(now.Add(codexOAuthRefreshSkew))
}

func expiryFromJWT(token string) time.Time {
	payload, ok := decodeJWTPayload(token)
	if !ok {
		return time.Time{}
	}
	exp, ok := payload["exp"]
	if !ok {
		return time.Time{}
	}
	switch v := exp.(type) {
	case float64:
		return time.Unix(int64(v), 0).UTC()
	case json.Number:
		n, _ := v.Int64()
		return time.Unix(n, 0).UTC()
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return time.Unix(n, 0).UTC()
		}
	}
	return time.Time{}
}

func accountIDFromIDToken(idToken string) string {
	payload, ok := decodeJWTPayload(idToken)
	if !ok {
		return ""
	}
	authClaim, ok := payload["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return ""
	}
	if accountID, ok := authClaim["chatgpt_account_id"].(string); ok {
		return accountID
	}
	return ""
}

func decodeJWTPayload(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var payload map[string]any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, false
	}
	return payload, true
}

func (r codexTokenResponse) messageOrBody(body []byte) string {
	if msg := r.ErrorDesc.String(); msg != "" {
		return msg
	}
	if msg := r.Error.String(); msg != "" {
		return msg
	}
	return string(body)
}
