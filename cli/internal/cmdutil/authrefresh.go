package cmdutil

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// Refresher is the narrow SDK surface RefreshAndPersist depends on.
// *sdk.Client satisfies it implicitly; tests inject fakes.
type Refresher interface {
	RefreshToken(ctx context.Context, refreshToken string) (*sdk.RefreshTokenResponse, error)
}

// RefreshAndPersist reads the stored refresh token for ctxName, exchanges it
// for a new access + refresh pair via refresher, and writes both back to the
// secrets store. Returns the new access token (the refresh is already
// persisted as a side-effect, so callers only need the access value to
// retry the original request).
//
// Single canonical implementation shared by `weknora auth refresh` and the
// AuthRetryTransport's refresh closure — both used to inline the same
// six-step sequence with subtly diverging error wording.
func RefreshAndPersist(ctx context.Context, store secrets.Store, refresher Refresher, ctxName string) (string, error) {
	refresh, err := store.Get(ctxName, "refresh")
	if errors.Is(err, secrets.ErrNotFound) || refresh == "" {
		return "", &Error{
			Code:    CodeAuthTokenExpired,
			Message: "refresh token missing for context " + ctxName,
			Hint:    "run `weknora auth login` to re-authenticate",
		}
	}
	if err != nil {
		return "", Wrapf(CodeLocalKeychainDenied, err, "load refresh token")
	}

	resp, err := refresher.RefreshToken(ctx, refresh)
	if err != nil {
		// WrapHTTP rather than fixed CodeNetworkError so a refresh
		// rejected by the server (401/403) surfaces as auth.token_expired /
		// auth.forbidden instead of collapsing to network.error.
		return "", WrapHTTP(err, "refresh access token")
	}
	if resp == nil || !resp.Success || resp.AccessToken == "" || resp.RefreshToken == "" {
		msg := "refresh token rejected"
		if resp != nil && resp.Message != "" {
			msg = "refresh token rejected: " + resp.Message
		}
		return "", &Error{
			Code:    CodeAuthTokenExpired,
			Message: msg,
			Hint:    "run `weknora auth login` to re-authenticate",
		}
	}

	if err := store.Set(ctxName, "access", resp.AccessToken); err != nil {
		return "", Wrapf(CodeLocalKeychainDenied, err, "save access token")
	}
	if err := store.Set(ctxName, "refresh", resp.RefreshToken); err != nil {
		return "", Wrapf(CodeLocalKeychainDenied, err, "save refresh token")
	}
	return resp.AccessToken, nil
}
