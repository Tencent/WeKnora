package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

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
