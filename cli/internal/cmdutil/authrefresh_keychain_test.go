package cmdutil

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingStore simulates a secrets backend that answers reads with a hard
// error — macOS keychain denied, or a file store whose backing file is
// unreadable. Set/Delete record calls so the happy path can assert writes.
type failingStore struct {
	getErr    error
	getValue  string
	sets      [][2]string
	deleteErr error
}

func (f *failingStore) Get(_, _ string) (string, error) {
	return f.getValue, f.getErr
}

func (f *failingStore) Set(_ string, key, value string) error {
	f.sets = append(f.sets, [2]string{key, value})
	return nil
}

func (f *failingStore) Delete(_, _ string) error { return f.deleteErr }

func (f *failingStore) Ref(_, _ string) string { return "" }

type stubRefresher struct {
	called   bool
	gotToken string
	resp     *sdk.RefreshTokenResponse
	err      error
}

func (s *stubRefresher) RefreshToken(_ context.Context, refreshToken string) (*sdk.RefreshTokenResponse, error) {
	s.called = true
	s.gotToken = refreshToken
	return s.resp, s.err
}

// A keychain or file-store failure is a different failure from "no refresh
// token stored": it must surface as local.keychain_denied (exit 1, "verify
// keyring access"), not auth.token_expired (exit 3, "run auth login"), or an
// agent following the hint re-logins without ever fixing the real problem.
func TestRefreshAndPersistClassifiesStoreReadFailureAsKeychainDenied(t *testing.T) {
	boom := errors.New("keychain access denied")
	store := &failingStore{getErr: boom}
	_, err := RefreshAndPersist(context.Background(), store, &stubRefresher{}, "prod")
	require.Error(t, err)
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, CodeLocalKeychainDenied, e.Code)
	assert.Equal(t, 1, ExitCode(e))
	assert.Contains(t, err.Error(), "load refresh token")
}

// Missing-but-readable stays token_expired: the stored credential is gone,
// re-login is the right recovery.
func TestRefreshAndPersistMissingTokenStaysTokenExpired(t *testing.T) {
	store := &failingStore{getErr: secrets.ErrNotFound}
	_, err := RefreshAndPersist(context.Background(), store, &stubRefresher{}, "prod")
	require.Error(t, err)
	var e *Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, CodeAuthTokenExpired, e.Code)
	assert.Equal(t, 3, ExitCode(e))
}

// Readable store with a stored token reaches the refresher and persists the
// new pair.
func TestRefreshAndPersistHappyPathRefreshesAndPersists(t *testing.T) {
	store := &failingStore{getValue: "stored-refresh"}
	refresher := &stubRefresher{resp: &sdk.RefreshTokenResponse{
		Success: true, AccessToken: "new-access", RefreshToken: "new-refresh",
	}}
	access, err := RefreshAndPersist(context.Background(), store, refresher, "prod")
	require.NoError(t, err)
	assert.True(t, refresher.called)
	assert.Equal(t, "stored-refresh", refresher.gotToken)
	assert.Equal(t, "new-access", access)
	require.Len(t, store.sets, 2)
	assert.Equal(t, [2]string{"access", "new-access"}, store.sets[0])
	assert.Equal(t, [2]string{"refresh", "new-refresh"}, store.sets[1])
}
