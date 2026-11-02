package credential

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestVaultCreateAndOpen(t *testing.T) {
	v, db := newTestVault(t)
	ctx := context.Background()
	item, err := v.Create(ctx, alice, LoginInput{Site: "GitHub.com", Domains: []string{"api.github.com"},
		Username: " alice ", Password: "s3cret-Δ"})
	require.NoError(t, err)
	assert.Equal(t, "github.com", item.Site)
	assert.Equal(t, "github.com", item.Title, "title defaults to the site")
	assert.Equal(t, types.StringArray{"github.com", "api.github.com"}, item.Domains)
	assert.Equal(t, "alice", item.Username)

	var raw types.CredentialVaultItem
	require.NoError(t, db.First(&raw, "id = ?", item.ID).Error)
	requireNoSecret(t, "s3cret-Δ", raw.SecretCiphertext)

	got, pw, err := v.Password(ctx, alice, item.ID)
	require.NoError(t, err)
	assert.Equal(t, "s3cret-Δ", pw)
	assert.Equal(t, item.ID, got.ID)

	_, _, err = v.Password(ctx, bob, item.ID)
	require.ErrorIs(t, err, types.ErrCredentialUnavailable)
	_, err = v.Get(ctx, bob, item.ID)
	assert.Equal(t, http.StatusNotFound, httpStatus(t, err))
}

func TestVaultRejectsSwappedCiphertext(t *testing.T) {
	v, db := newTestVault(t)
	ctx := context.Background()
	a, err := v.Create(ctx, alice, LoginInput{Site: "a.com", Username: "x", Password: "pw-a"})
	require.NoError(t, err)
	b, err := v.Create(ctx, alice, LoginInput{Site: "b.com", Username: "x", Password: "pw-b"})
	require.NoError(t, err)
	var rowB types.CredentialVaultItem
	require.NoError(t, db.First(&rowB, "id = ?", b.ID).Error)
	require.NoError(t, db.Model(&types.CredentialVaultItem{}).Where("id = ?", a.ID).
		Update("secret_ciphertext", rowB.SecretCiphertext).Error)
	_, _, err = v.Password(ctx, alice, a.ID)
	require.ErrorIs(t, err, types.ErrCredentialUnavailable)
}

func TestVaultValidation(t *testing.T) {
	v, _ := newTestVault(t)
	ctx := context.Background()
	_, err := v.Create(ctx, alice, LoginInput{Site: "https://a.com", Password: "p"})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	_, err = v.Create(ctx, alice, LoginInput{Site: "a.com"})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err), "a password is required")
	_, err = v.Create(ctx, alice, LoginInput{Site: "a.com", Password: string(make([]byte, 4097))})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	_, err = v.Create(ctx, alice, LoginInput{Site: "a.com", Username: "u", Password: "p"})
	require.NoError(t, err)
	_, err = v.Create(ctx, alice, LoginInput{Site: "a.com", Username: "u", Password: "q"})
	assert.Equal(t, http.StatusConflict, httpStatus(t, err))
}

func TestVaultItemLimit(t *testing.T) {
	v, _ := newTestVault(t)
	v.maxItems = 1
	ctx := context.Background()
	_, err := v.Create(ctx, alice, LoginInput{Site: "a.com", Password: "p"})
	require.NoError(t, err)
	_, err = v.Create(ctx, alice, LoginInput{Site: "b.com", Password: "p"})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
}

func TestVaultUpsertReplacesPasswordButKeepsDomains(t *testing.T) {
	v, _ := newTestVault(t)
	ctx := context.Background()
	first, err := v.Upsert(ctx, alice, LoginInput{Site: "a.com", Username: "u", Password: "old"})
	require.NoError(t, err)
	second, err := v.Upsert(ctx, alice, LoginInput{Site: "a.com", Domains: []string{"sso.a.com"}, Username: "u", Password: "new"})
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, types.StringArray{"a.com"}, second.Domains, "a card never widens a saved login")
	stored, err := v.Get(ctx, alice, first.ID)
	require.NoError(t, err)
	assert.Equal(t, types.StringArray{"a.com"}, stored.Domains)
	_, pw, err := v.Password(ctx, alice, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "new", pw)
}

func TestVaultUpdateAndDelete(t *testing.T) {
	v, _ := newTestVault(t)
	ctx := context.Background()
	item, err := v.Create(ctx, alice, LoginInput{Site: "a.com", Username: "u", Password: "p"})
	require.NoError(t, err)
	title, pw := "工作", "p2"
	updated, err := v.Update(ctx, alice, item.ID, LoginPatch{Title: &title, Domains: []string{}, Password: &pw})
	require.NoError(t, err)
	assert.Equal(t, "工作", updated.Title)
	assert.Equal(t, types.StringArray{"a.com"}, updated.Domains, "the site always stays allowed")
	_, got, err := v.Password(ctx, alice, item.ID)
	require.NoError(t, err)
	assert.Equal(t, "p2", got)

	assert.Equal(t, http.StatusNotFound, httpStatus(t, v.Delete(ctx, bob, item.ID)))
	require.NoError(t, v.Delete(ctx, alice, item.ID))
	_, _, err = v.Password(ctx, alice, item.ID)
	require.ErrorIs(t, err, types.ErrCredentialUnavailable)
}

func TestVaultWithoutKey(t *testing.T) {
	v, _ := newTestVault(t)
	t.Setenv("SYSTEM_AES_KEY", "")
	assert.False(t, Available())
	_, err := v.Create(context.Background(), alice, LoginInput{Site: "a.com", Password: "p"})
	assert.Equal(t, http.StatusServiceUnavailable, httpStatus(t, err))
}

func TestOwnerFromContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u1"})
	owner, err := OwnerFromContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, Owner{TenantID: 7, UserID: "web_user:u1"}, owner)

	embed := types.WithPrincipal(context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7)),
		types.Principal{Type: types.PrincipalEmbedVisitor, ID: "7:c:v"})
	_, err = OwnerFromContext(embed)
	assert.Equal(t, http.StatusForbidden, httpStatus(t, err))
}
