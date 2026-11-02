// internal/handler/credential_vault_test.go
package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service/credential"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeVault struct {
	created credential.LoginInput
	patched credential.LoginPatch
	deleted string
}

func (f *fakeVault) List(context.Context, credential.Owner, string) ([]*types.CredentialVaultItem, error) {
	return []*types.CredentialVaultItem{{
		ID: "i1", Site: "a.com", Username: "u", SecretCiphertext: "enc:v1:sentinel",
	}}, nil
}

func (f *fakeVault) Create(
	_ context.Context, _ credential.Owner, in credential.LoginInput,
) (*types.CredentialVaultItem, error) {
	f.created = in
	return &types.CredentialVaultItem{ID: "i2", Site: in.Site, SecretCiphertext: "enc:v1:sentinel"}, nil
}

func (f *fakeVault) Update(
	_ context.Context, _ credential.Owner, id string, p credential.LoginPatch,
) (*types.CredentialVaultItem, error) {
	f.patched = p
	return &types.CredentialVaultItem{ID: id}, nil
}

func (f *fakeVault) Delete(_ context.Context, _ credential.Owner, id string) error {
	f.deleted = id
	return nil
}

func vaultRouter(v credentialVault) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(renderAppErrors, asWebUser)
	h := &CredentialVaultHandler{vault: v}
	r.GET("/me/credential-vault", h.List)
	r.POST("/me/credential-vault", h.Create)
	r.PATCH("/me/credential-vault/:id", h.Update)
	r.DELETE("/me/credential-vault/:id", h.Delete)
	return r
}

func TestCredentialVaultHandler(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk")
	v := &fakeVault{}
	r := vaultRouter(v)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/me/credential-vault?site=a.com", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"available":true`)
	assert.NotContains(t, w.Body.String(), "sentinel", "ciphertext never leaves the server")

	w = postJSON(r, "/me/credential-vault",
		`{"site":"a.com","domains":["a.com"],"username":"u","password":"pw-sentinel"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "pw-sentinel", v.created.Password)
	assert.NotContains(t, w.Body.String(), "sentinel")

	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/me/credential-vault/i1", bytes.NewBufferString(`{"title":"工作"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, v.patched.Title)
	assert.Nil(t, v.patched.Password)
	assert.Nil(t, v.patched.Domains)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/me/credential-vault/i1", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "i1", v.deleted)
}
