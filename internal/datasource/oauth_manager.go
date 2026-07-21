package datasource

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

const (
	oneDriveProvider     = "onedrive"
	oneDriveDefaultLogin = "https://login.microsoftonline.com"
	oneDriveDefaultGraph = "https://graph.microsoft.com/v1.0"
	oneDriveScopes       = "offline_access Files.Read"
	oauthRefreshSkew     = 5 * time.Minute
	oauthHTTPTimeout     = 30 * time.Second
)

type DataSourceOAuthManager struct {
	tokens     interfaces.DataSourceOAuthRepository
	dataSource interfaces.DataSourceRepository
	states     *dataSourceOAuthStateStore
	httpClient *http.Client
	loginBase  string
	graphBase  string
	refreshMu  sync.Mutex
}

type DataSourceOAuthStatus struct {
	Authorized            bool      `json:"authorized"`
	Provider              string    `json:"provider,omitempty"`
	AccountDisplayName    string    `json:"account_display_name,omitempty"`
	ProviderTenantID      string    `json:"provider_tenant_id,omitempty"`
	AuthorizedDriveID     string    `json:"authorized_drive_id,omitempty"`
	ExpiresAt             time.Time `json:"expires_at,omitempty"`
	ConnectionVersion     uint64    `json:"connection_version"`
	ReauthorizationNeeded bool      `json:"reauthorization_required"`
	DataSourceID          string    `json:"-"`
	ReplacedConnection    bool      `json:"-"`
}

type microsoftTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

type microsoftDriveResponse struct {
	ID    string `json:"id"`
	Owner struct {
		User struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
	} `json:"owner"`
}

func NewDataSourceOAuthManager(
	tokens interfaces.DataSourceOAuthRepository,
	dataSource interfaces.DataSourceRepository,
	rdb *redis.Client,
) *DataSourceOAuthManager {
	return &DataSourceOAuthManager{
		tokens: tokens, dataSource: dataSource, states: newDataSourceOAuthStateStore(rdb),
		httpClient: &http.Client{Timeout: oauthHTTPTimeout},
		loginBase:  oneDriveDefaultLogin,
		graphBase:  oneDriveDefaultGraph,
	}
}

func (m *DataSourceOAuthManager) AuthorizeURL(
	ctx context.Context,
	tenantID uint64,
	dataSourceID string,
	authorizedBy string,
	replaceConnection bool,
) (string, error) {
	clientID, _, tenant, redirectURI, err := m.configuration()
	if err != nil {
		return "", err
	}
	if m.states.rdb == nil && !strings.EqualFold(strings.TrimSpace(os.Getenv("EDITION")), "lite") {
		return "", fmt.Errorf("OneDrive OAuth requires Redis outside Lite single-instance mode")
	}
	ds, err := m.dataSource.FindByID(ctx, dataSourceID)
	if err != nil || ds == nil || ds.TenantID != tenantID || ds.Type != types.ConnectorTypeOneDrive {
		return "", ErrDataSourceNotFound
	}
	if strings.TrimSpace(authorizedBy) == "" {
		return "", fmt.Errorf("authorized user is required")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return "", err
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])
	if err := m.states.put(ctx, state, dataSourceOAuthState{
		TenantID: tenantID, DataSourceID: dataSourceID, AuthorizedBy: authorizedBy,
		ConnectionVersion: ds.ConnectionVersion, CodeVerifier: verifier,
		ReplaceConnection: replaceConnection,
	}); err != nil {
		return "", err
	}

	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("response_mode", "query")
	q.Set("scope", oneDriveScopes)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return fmt.Sprintf("%s/%s/oauth2/v2.0/authorize?%s", m.loginBase, url.PathEscape(tenant), q.Encode()), nil
}

func (m *DataSourceOAuthManager) CompleteAuthorization(
	ctx context.Context, stateValue, code, providerError string,
) (*DataSourceOAuthStatus, error) {
	state, err := m.states.take(ctx, stateValue)
	if err != nil {
		return nil, err
	}
	if providerError != "" {
		if providerError == "access_denied" {
			return nil, fmt.Errorf("Microsoft authorization was canceled")
		}
		return nil, fmt.Errorf("Microsoft authorization failed: %s", safeOAuthError(providerError))
	}
	if code == "" {
		return nil, fmt.Errorf("authorization code is missing")
	}
	initialDS, err := m.dataSource.FindByID(ctx, state.DataSourceID)
	if err != nil || initialDS == nil || initialDS.TenantID != state.TenantID ||
		initialDS.Type != types.ConnectorTypeOneDrive {
		return nil, ErrDataSourceNotFound
	}
	if initialDS.ConnectionVersion != state.ConnectionVersion {
		return nil, ErrOAuthConnectionChanged
	}
	clientID, clientSecret, tenant, redirectURI, err := m.configuration()
	if err != nil {
		return nil, err
	}
	token, err := m.exchange(ctx, tenant, url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {state.CodeVerifier},
		"scope":         {oneDriveScopes},
	})
	if err != nil {
		return nil, err
	}
	drive, err := m.getDrive(ctx, token.AccessToken)
	if err != nil {
		return nil, err
	}
	if drive.ID == "" || drive.Owner.User.ID == "" {
		return nil, fmt.Errorf("Microsoft did not return a stable drive/account identity")
	}

	ds, err := m.dataSource.FindByID(ctx, state.DataSourceID)
	if err != nil || ds == nil || ds.TenantID != state.TenantID || ds.Type != types.ConnectorTypeOneDrive {
		return nil, ErrDataSourceNotFound
	}
	if ds.ConnectionVersion != state.ConnectionVersion {
		return nil, ErrOAuthConnectionChanged
	}
	config, err := ds.ParseConfig()
	if err != nil || config == nil {
		config = &types.DataSourceConfig{Type: types.ConnectorTypeOneDrive}
	}
	if state.ReplaceConnection {
		config.ResourceIDs = nil
	}
	configJSON, err := config.ToJSON()
	if err != nil {
		return nil, err
	}

	stored := &types.DataSourceOAuthToken{
		TenantID: state.TenantID, DataSourceID: state.DataSourceID, Provider: oneDriveProvider,
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, TokenType: token.TokenType,
		Scopes: token.Scope, ExpiresAt: time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second),
		ProviderAccountID: drive.Owner.User.ID, AuthorizedDriveID: drive.ID,
		AccountDisplayName: drive.Owner.User.DisplayName, AuthorizedByUserID: state.AuthorizedBy,
		ConnectionVersion: state.ConnectionVersion,
	}
	newVersion, err := m.tokens.SaveAuthorization(
		ctx, stored, state.ConnectionVersion, state.ReplaceConnection, configJSON,
	)
	if err != nil {
		if strings.Contains(err.Error(), "does not match") {
			return nil, ErrOAuthAccountMismatch
		}
		if strings.Contains(err.Error(), "version changed") {
			return nil, ErrOAuthConnectionChanged
		}
		return nil, err
	}
	stored.ConnectionVersion = newVersion
	status := statusFromToken(stored, false)
	status.DataSourceID = state.DataSourceID
	status.ReplacedConnection = state.ReplaceConnection && newVersion != state.ConnectionVersion
	return status, nil
}

func (m *DataSourceOAuthManager) Status(
	ctx context.Context, tenantID uint64, dataSourceID string,
) (*DataSourceOAuthStatus, error) {
	ds, err := m.dataSource.FindByID(ctx, dataSourceID)
	if err != nil || ds == nil || ds.TenantID != tenantID || ds.Type != types.ConnectorTypeOneDrive {
		return nil, ErrDataSourceNotFound
	}
	token, err := m.tokens.Get(ctx, tenantID, dataSourceID)
	if err != nil {
		return nil, err
	}
	if token == nil {
		return &DataSourceOAuthStatus{ConnectionVersion: ds.ConnectionVersion}, nil
	}
	return statusFromToken(token, ds.Status == types.DataSourceStatusReauthorizationRequired), nil
}

func (m *DataSourceOAuthManager) Revoke(ctx context.Context, tenantID uint64, dataSourceID string) error {
	ds, err := m.dataSource.FindByID(ctx, dataSourceID)
	if err != nil || ds == nil || ds.TenantID != tenantID || ds.Type != types.ConnectorTypeOneDrive {
		return ErrDataSourceNotFound
	}
	config, configErr := ds.ParseConfig()
	if configErr != nil {
		return fmt.Errorf("parse OneDrive data source config before revoke: %w", configErr)
	}
	if config == nil {
		config = &types.DataSourceConfig{Type: types.ConnectorTypeOneDrive}
	}
	config.ResourceIDs = nil
	resetConfig, configErr := config.ToJSON()
	if configErr != nil {
		return configErr
	}
	_, err = m.tokens.RevokeAuthorization(ctx, tenantID, dataSourceID, ds.ConnectionVersion, resetConfig)
	return err
}

func (m *DataSourceOAuthManager) AccessToken(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
) (string, error) {
	return m.accessToken(ctx, tenantID, dataSourceID, connectionVersion, false)
}

func (m *DataSourceOAuthManager) RefreshAccessToken(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64,
) (string, error) {
	return m.accessToken(ctx, tenantID, dataSourceID, connectionVersion, true)
}

func (m *DataSourceOAuthManager) accessToken(
	ctx context.Context, tenantID uint64, dataSourceID string, connectionVersion uint64, force bool,
) (string, error) {
	token, err := m.tokens.Get(ctx, tenantID, dataSourceID)
	if err != nil {
		return "", err
	}
	if token == nil || token.RefreshToken == "" || token.ConnectionVersion != connectionVersion {
		return "", ErrOAuthReauthorizationRequired
	}
	if !force && time.Until(token.ExpiresAt) > oauthRefreshSkew && token.AccessToken != "" {
		return token.AccessToken, nil
	}
	clientID, clientSecret, tenant, _, err := m.configuration()
	if err != nil {
		return "", err
	}
	// SQLite Lite mode has no effective SELECT ... FOR UPDATE; the process lock
	// covers that deployment, while the repository row lock covers multi-instance
	// PostgreSQL deployments.
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	refreshed, err := m.tokens.RefreshWithLock(ctx, tenantID, dataSourceID, connectionVersion,
		func(current *types.DataSourceOAuthToken) error {
			if !force && time.Until(current.ExpiresAt) > oauthRefreshSkew && current.AccessToken != "" {
				return nil
			}
			response, refreshErr := m.exchange(ctx, tenant, url.Values{
				"client_id": {clientID}, "client_secret": {clientSecret},
				"grant_type": {"refresh_token"}, "refresh_token": {current.RefreshToken},
				"scope": {oneDriveScopes},
			})
			if refreshErr != nil {
				return refreshErr
			}
			current.AccessToken = response.AccessToken
			if response.RefreshToken != "" {
				current.RefreshToken = response.RefreshToken
			}
			current.TokenType = response.TokenType
			current.Scopes = response.Scope
			current.ExpiresAt = time.Now().UTC().Add(time.Duration(response.ExpiresIn) * time.Second)
			return nil
		})
	if err != nil {
		if isReauthorizationError(err) {
			return "", fmt.Errorf("%w: %v", ErrOAuthReauthorizationRequired, err)
		}
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (m *DataSourceOAuthManager) exchange(
	ctx context.Context, tenant string, values url.Values,
) (*microsoftTokenResponse, error) {
	endpoint := fmt.Sprintf("%s/%s/oauth2/v2.0/token", m.loginBase, url.PathEscape(tenant))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Microsoft token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read Microsoft token response: %w", err)
	}
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("Microsoft token response is too large")
	}
	var token microsoftTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("decode Microsoft token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || token.Error != "" {
		return nil, &oauthProviderError{Code: token.Error, Description: safeOAuthError(token.Description)}
	}
	if token.AccessToken == "" || (values.Get("grant_type") == "authorization_code" && token.RefreshToken == "") {
		return nil, fmt.Errorf("Microsoft token response is incomplete")
	}
	if token.ExpiresIn <= 0 {
		token.ExpiresIn = 3600
	}
	return &token, nil
}

func (m *DataSourceOAuthManager) getDrive(ctx context.Context, accessToken string) (*microsoftDriveResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.graphBase, "/")+"/me/drive", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("validate OneDrive authorization: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("validate OneDrive authorization: Microsoft Graph returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("Microsoft Graph drive response is too large")
	}
	var drive microsoftDriveResponse
	if err := json.Unmarshal(body, &drive); err != nil {
		return nil, err
	}
	return &drive, nil
}

func (m *DataSourceOAuthManager) configuration() (clientID, clientSecret, tenant, redirectURI string, err error) {
	clientID = strings.TrimSpace(os.Getenv("ONEDRIVE_CLIENT_ID"))
	clientSecret = strings.TrimSpace(os.Getenv("ONEDRIVE_CLIENT_SECRET"))
	tenant = strings.TrimSpace(os.Getenv("ONEDRIVE_TENANT"))
	redirectURI = strings.TrimSpace(os.Getenv("ONEDRIVE_REDIRECT_URL"))
	if tenant == "" {
		tenant = "common"
	}
	if clientID == "" || clientSecret == "" || redirectURI == "" {
		return "", "", "", "", fmt.Errorf("%w: ONEDRIVE_CLIENT_ID, ONEDRIVE_CLIENT_SECRET and ONEDRIVE_REDIRECT_URL are required", ErrOAuthNotConfigured)
	}
	parsed, parseErr := url.Parse(redirectURI)
	if parseErr != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Fragment != "" {
		return "", "", "", "", fmt.Errorf("%w: ONEDRIVE_REDIRECT_URL must be an absolute URL without a fragment", ErrOAuthNotConfigured)
	}
	if !strings.EqualFold(parsed.Scheme, "https") && !isLoopbackHost(parsed.Hostname()) {
		return "", "", "", "", fmt.Errorf("%w: ONEDRIVE_REDIRECT_URL must use HTTPS", ErrOAuthNotConfigured)
	}
	if len(os.Getenv("SYSTEM_AES_KEY")) != 32 {
		return "", "", "", "", fmt.Errorf("%w: a 32-byte SYSTEM_AES_KEY is required", ErrOAuthNotConfigured)
	}
	return clientID, clientSecret, tenant, redirectURI, nil
}

type oauthProviderError struct {
	Code        string
	Description string
}

func (e *oauthProviderError) Error() string {
	if e.Description == "" {
		return "Microsoft OAuth error: " + safeOAuthError(e.Code)
	}
	return "Microsoft OAuth error " + safeOAuthError(e.Code) + ": " + e.Description
}

func isReauthorizationError(err error) bool {
	var providerErr *oauthProviderError
	if !errors.As(err, &providerErr) {
		return false
	}
	switch providerErr.Code {
	case "invalid_grant", "interaction_required", "login_required", "consent_required":
		return true
	default:
		return false
	}
}

func statusFromToken(token *types.DataSourceOAuthToken, reauthorization bool) *DataSourceOAuthStatus {
	if token == nil {
		return &DataSourceOAuthStatus{ReauthorizationNeeded: reauthorization}
	}
	return &DataSourceOAuthStatus{
		Authorized: token != nil, Provider: token.Provider,
		AccountDisplayName: token.AccountDisplayName, ProviderTenantID: token.ProviderTenantID,
		AuthorizedDriveID: token.AuthorizedDriveID, ExpiresAt: token.ExpiresAt,
		ConnectionVersion: token.ConnectionVersion, ReauthorizationNeeded: reauthorization,
	}
}

func randomURLToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func safeOAuthError(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
