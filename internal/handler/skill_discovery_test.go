package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeBuiltinRegistration struct {
	fakeSkillCatalog
	replace bool
	calls   int
	tenant  uint64
	id      string
}

func (f *fakeBuiltinRegistration) RegisterBuiltin(
	_ context.Context, tenant uint64, id string, replace bool,
) (*types.TenantSkillCatalogEntity, error) {
	f.calls++
	f.replace, f.tenant, f.id = replace, tenant, id
	return &types.TenantSkillCatalogEntity{ID: "browser-catalog", Name: "browser"}, nil
}

func TestBuiltinRegistrationRequiresExplicitReplacement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		replace bool
		status  int
	}{
		{"empty body", "", false, http.StatusCreated},
		{"default", `{}`, false, http.StatusCreated},
		{"preserve", `{"replace_existing":false}`, false, http.StatusCreated},
		{"replace", `{"replace_existing":true}`, true, http.StatusCreated},
		{"invalid", `{"replace_existing":"true"}`, false, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := &fakeBuiltinRegistration{}
			h := NewSkillHandler(&fakeUsableSkillLister{}, catalog)
			r := newCatalogRouter(h)
			r.POST("/skills/discovery/:id/register", h.RegisterBuiltin)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost,
				"/skills/discovery/browser/register", strings.NewReader(tc.body)))
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusBadRequest {
				require.Zero(t, catalog.calls)
				return
			}
			require.Equal(t, 1, catalog.calls)
			require.Equal(t, tc.replace, catalog.replace)
			require.Equal(t, uint64(testSkillTenantID), catalog.tenant)
			require.Equal(t, "browser", catalog.id)
		})
	}
}

func TestListDiscoveryIncludesInstallArchiveDigest(t *testing.T) {
	h := NewSkillHandler(&fakeUsableSkillLister{}, &fakeBuiltinRegistration{})
	r := newCatalogRouter(h)
	r.GET("/skills/discovery", h.ListDiscovery)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/skills/discovery", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Data []builtin.Entry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.NotEmpty(t, response.Data)
	for _, entry := range response.Data {
		if entry.Distribution != "builtin" {
			require.Empty(t, entry.BundleSHA256)
			continue
		}
		archive, err := builtin.Archive(entry.ID)
		require.NoError(t, err)
		sum := sha256.Sum256(archive)
		require.Equal(t, hex.EncodeToString(sum[:]), entry.BundleSHA256, entry.ID)
	}
}
