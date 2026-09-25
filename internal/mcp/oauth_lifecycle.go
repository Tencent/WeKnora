package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
)

const (
	oauthRefreshSkew       = 30 * time.Second
	oauthRefreshLease      = 45 * time.Second
	oauthRefreshPoll       = 100 * time.Millisecond
	oauthStateAuthorized   = "authorized"
	oauthStateRefreshable  = "refreshable"
	oauthStateReauthNeeded = "reauth_required"
)

// OAuthAuthorizationStatus distinguishes a currently usable access token from
// an expired token that can still be refreshed. This prevents a stale database
// row from being presented as an already successful authorization.
type OAuthAuthorizationStatus struct {
	Authorized       bool       `json:"authorized"`
	State            string     `json:"state"`
	RefreshAvailable bool       `json:"refresh_available"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

// OAuthReauthorizationRequiredError means no usable access token can be
// recovered without interactive user consent.
type OAuthReauthorizationRequiredError struct {
	Reason string
}

func (e *OAuthReauthorizationRequiredError) Error() string {
	if e.Reason == "" {
		return "MCP OAuth authorization required"
	}
	return "MCP OAuth authorization required: " + e.Reason
}

// OAuthRefreshTemporaryError preserves a token when refresh failed for a
// transient reason. Callers must surface/retry this as an operational failure,
// not open a new consent popup.
type OAuthRefreshTemporaryError struct {
	Err error
}

func (e *OAuthRefreshTemporaryError) Error() string {
	return fmt.Sprintf("MCP OAuth token refresh temporarily failed: %v", e.Err)
}

func (e *OAuthRefreshTemporaryError) Unwrap() error { return e.Err }

type oauthRuntime struct {
	repo          interfaces.MCPOAuthRepository
	tenantID      uint64
	principal     types.Principal
	serviceID     string
	handler       *transport.OAuthHandler
	leaseDuration time.Duration
}

func newOAuthRuntime(
	repo interfaces.MCPOAuthRepository,
	tenantID uint64,
	principal types.Principal,
	serviceID, baseURL string,
	cfg transport.OAuthConfig,
) *oauthRuntime {
	h := transport.NewOAuthHandler(cfg)
	h.SetBaseURL(baseURL)
	leaseDuration := oauthRefreshLease
	if cfg.HTTPClient != nil && cfg.HTTPClient.Timeout > 0 && cfg.HTTPClient.Timeout+15*time.Second > leaseDuration {
		leaseDuration = cfg.HTTPClient.Timeout + 15*time.Second
	}
	return &oauthRuntime{
		repo:          repo,
		tenantID:      tenantID,
		principal:     principal.Normalize(),
		serviceID:     serviceID,
		handler:       h,
		leaseDuration: leaseDuration,
	}
}

func tokenStatus(token *types.MCPOAuthToken, now time.Time) OAuthAuthorizationStatus {
	status := OAuthAuthorizationStatus{State: oauthStateReauthNeeded}
	if token == nil || token.AccessToken == "" {
		return status
	}
	status.RefreshAvailable = token.RefreshToken != ""
	if !token.ExpiresAt.IsZero() {
		expiresAt := token.ExpiresAt
		status.ExpiresAt = &expiresAt
	}
	if token.ExpiresAt.IsZero() || token.ExpiresAt.After(now) {
		status.Authorized = true
		status.State = oauthStateAuthorized
		return status
	}
	if status.RefreshAvailable {
		status.State = oauthStateRefreshable
	}
	return status
}

func (r *oauthRuntime) ensureFresh(ctx context.Context, force bool, override *transport.OAuthHandler) error {
	row, err := r.repo.GetTokenForPrincipal(ctx, r.tenantID, r.principal, r.serviceID)
	if err != nil {
		return fmt.Errorf("load MCP OAuth token: %w", err)
	}
	if row == nil || row.AccessToken == "" {
		return &OAuthReauthorizationRequiredError{Reason: "no token is stored"}
	}
	now := time.Now()
	if !force {
		if row.ExpiresAt.IsZero() || row.ExpiresAt.After(now.Add(oauthRefreshSkew)) {
			return nil
		}
		// Tokens issued without refresh_token remain usable through their actual
		// expiry; the refresh skew must not shorten their lifetime.
		if row.RefreshToken == "" && row.ExpiresAt.After(now) {
			return nil
		}
	}
	if row.RefreshToken == "" {
		_ = r.repo.DeleteTokenForPrincipal(ctx, r.tenantID, r.principal, r.serviceID)
		return &OAuthReauthorizationRequiredError{Reason: "the access token expired and no refresh token is available"}
	}
	return r.refreshWithLease(ctx, row, override)
}

func (r *oauthRuntime) refreshWithLease(
	ctx context.Context, observed *types.MCPOAuthToken, override *transport.OAuthHandler,
) error {
	for {
		leaseID := uuid.NewString()
		leaseDuration := r.leaseDuration
		if leaseDuration <= 0 {
			leaseDuration = oauthRefreshLease
		}
		leaseUntil := time.Now().Add(leaseDuration)
		acquired, err := r.repo.TryAcquireTokenRefreshLease(
			ctx, r.tenantID, r.principal, r.serviceID, leaseID, leaseUntil,
		)
		if err != nil {
			return fmt.Errorf("claim MCP OAuth token refresh: %w", err)
		}
		if acquired {
			return r.refreshAsLeaseOwner(ctx, observed, leaseID, override)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(oauthRefreshPoll):
		}
		current, err := r.repo.GetTokenForPrincipal(ctx, r.tenantID, r.principal, r.serviceID)
		if err != nil {
			return fmt.Errorf("reload MCP OAuth token after concurrent refresh: %w", err)
		}
		if current == nil || current.AccessToken == "" {
			return &OAuthReauthorizationRequiredError{Reason: "the refresh token is no longer valid"}
		}
		if oauthTokenMaterialChanged(current, observed) {
			if current.ExpiresAt.IsZero() || current.ExpiresAt.After(time.Now().Add(oauthRefreshSkew)) {
				return nil
			}
			observed = current
		}
	}
}

func (r *oauthRuntime) refreshAsLeaseOwner(
	ctx context.Context, observed *types.MCPOAuthToken, leaseID string, override *transport.OAuthHandler,
) error {
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := r.repo.ReleaseTokenRefreshLease(
			releaseCtx, r.tenantID, r.principal, r.serviceID, leaseID,
		); err != nil {
			logger.GetLogger(releaseCtx).Warnf("failed to release MCP OAuth refresh lease: %v", err)
		}
	}()

	current, err := r.repo.GetTokenForPrincipal(ctx, r.tenantID, r.principal, r.serviceID)
	if err != nil {
		return fmt.Errorf("reload MCP OAuth token before refresh: %w", err)
	}
	if current == nil || current.AccessToken == "" {
		return &OAuthReauthorizationRequiredError{Reason: "no token is stored"}
	}
	// Another owner may have completed a refresh immediately before this lease
	// was acquired. Never consume its newly rotated refresh token unnecessarily.
	if oauthTokenMaterialChanged(current, observed) &&
		(current.ExpiresAt.IsZero() || current.ExpiresAt.After(time.Now().Add(oauthRefreshSkew))) {
		return nil
	}
	if current.RefreshToken == "" {
		return r.invalidateToken(ctx, false, "no refresh token is available")
	}

	handler := override
	if handler == nil {
		handler = r.handler
	}
	refreshed, refreshErr := handler.RefreshToken(ctx, current.RefreshToken)
	if refreshErr == nil && refreshed != nil && refreshed.AccessToken != "" {
		logger.GetLogger(ctx).Infof("MCP OAuth token refreshed: service=%s principal=%s", r.serviceID, r.principal.StorageID())
		return nil
	}
	if refreshErr == nil {
		refreshErr = errors.New("authorization server returned an empty access token")
	}
	permanent, resetClient := permanentRefreshFailure(refreshErr)
	if permanent {
		return r.invalidateToken(ctx, resetClient, "the refresh token or OAuth client is no longer valid")
	}
	return &OAuthRefreshTemporaryError{Err: refreshErr}
}

func oauthTokenMaterialChanged(current, observed *types.MCPOAuthToken) bool {
	if current == nil || observed == nil {
		return current != observed
	}
	return current.AccessToken != observed.AccessToken ||
		current.RefreshToken != observed.RefreshToken ||
		!current.ExpiresAt.Equal(observed.ExpiresAt)
}

func (r *oauthRuntime) invalidateToken(ctx context.Context, resetClient bool, reason string) error {
	if err := r.repo.DeleteTokenForPrincipal(ctx, r.tenantID, r.principal, r.serviceID); err != nil {
		return fmt.Errorf("delete invalid MCP OAuth token: %w", err)
	}
	if resetClient {
		if err := r.repo.DeleteClient(ctx, r.tenantID, r.serviceID); err != nil {
			return fmt.Errorf("delete invalid MCP OAuth client registration: %w", err)
		}
	}
	return &OAuthReauthorizationRequiredError{Reason: reason}
}

func permanentRefreshFailure(err error) (permanent bool, resetClient bool) {
	var oauthErr transport.OAuthError
	if errors.As(err, &oauthErr) {
		switch strings.ToLower(oauthErr.ErrorCode) {
		case "invalid_grant", "invalid_token", "bad_refresh_token", "expired_token":
			return true, false
		case "invalid_client", "unauthorized_client":
			return true, true
		}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "status 400") {
		return true, false
	}
	if strings.Contains(lower, "status 401") {
		return true, true
	}
	return false, false
}

func isOAuthAuthorizationFailure(err error) bool {
	if err == nil {
		return false
	}
	var reauth *OAuthReauthorizationRequiredError
	return errors.As(err, &reauth) || client.IsOAuthAuthorizationRequiredError(err) ||
		client.IsAuthorizationRequiredError(err)
}
