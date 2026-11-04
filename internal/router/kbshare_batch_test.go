package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Only the external KB and organization lookups are stubbed. Shares use the
// production service and SQLite repository, including existing-share updates.
type batchShareKBs struct {
	interfaces.KnowledgeBaseRepository
}

func (*batchShareKBs) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	kb := &types.KnowledgeBase{ID: id, TenantID: 7, CreatorID: "user"}
	switch id {
	case "kb-a", "kb-b":
	case "kb-colleague":
		kb.CreatorID = "colleague"
	case "kb-foreign":
		kb.TenantID = 9
	case "kb-unavailable":
		return nil, errors.New("private database connection details")
	default:
		return nil, repository.ErrKnowledgeBaseNotFound
	}
	return kb, nil
}

type batchShareKBLookup struct {
	interfaces.KnowledgeBaseService
	repo *batchShareKBs
}

func (s batchShareKBLookup) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	return s.repo.GetKnowledgeBaseByID(ctx, id)
}

type batchShareOrgs struct {
	interfaces.OrganizationRepository
}

func (batchShareOrgs) GetByID(_ context.Context, id string) (*types.Organization, error) {
	if id == "org-unavailable" {
		return nil, errors.New("private database connection details")
	}
	if id == "org-missing" {
		return nil, repository.ErrOrganizationNotFound
	}
	return &types.Organization{ID: id}, nil
}

func (batchShareOrgs) GetTenantMember(_ context.Context, id string, tenantID uint64) (
	*types.OrganizationTenantMember, error,
) {
	if id == "org-nonmember" {
		return nil, repository.ErrOrgMemberNotFound
	}
	role := types.OrgRoleEditor
	if id == "org-viewer" {
		role = types.OrgRoleViewer
	}
	return &types.OrganizationTenantMember{OrganizationID: id, TenantID: tenantID, Role: role}, nil
}

type batchShareFixture struct {
	router *gin.Engine
	db     *gorm.DB
	role   types.TenantRole
	scope  *types.TenantAPIKeyScope
}

func newBatchShareFixture(t *testing.T) *batchShareFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "shares.db")), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Discard,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Migrator().CreateTable(&types.KnowledgeBaseShare{}))

	kbs := &batchShareKBs{}
	shares := service.NewKBShareService(repository.NewKBShareRepository(db), batchShareOrgs{}, kbs, nil, nil, nil)
	h := handler.NewOrganizationHandler(nil, shares, nil, nil, nil, nil, batchShareKBLookup{repo: kbs}, nil, nil)
	enabled := true
	g := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}}
	f := &batchShareFixture{db: db, role: types.TenantRoleContributor, router: gin.New()}
	f.router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, f.role)
		if f.scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *f.scope)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), uint64(7))
		c.Set(types.UserIDContextKey.String(), "user")
		c.Next()
	})
	api := f.router.Group("/api/v1")
	api.Use(g.ensureAPIKeyAuthorizer().Middleware())
	RegisterOrganizationRoutes(api, h, g)
	g.assertAPIKeyPoliciesMatchRoutes(f.router)
	return f
}

func (f *batchShareFixture) post(body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/knowledge-bases/shares/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	f.router.ServeHTTP(w, req)
	return w
}

type batchShareResult struct {
	KBID           string                    `json:"kb_id"`
	OrganizationID string                    `json:"organization_id"`
	Status         int                       `json:"status"`
	Share          *types.KnowledgeBaseShare `json:"share"`
	Error          *apperrors.AppError       `json:"error"`
}

func batchShareResults(t *testing.T, w *httptest.ResponseRecorder) []batchShareResult {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Results []batchShareResult `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	return response.Data.Results
}

func TestBatchShareKnowledgeBasesRepeatedRequestsUpdateExistingShares(t *testing.T) {
	f := newBatchShareFixture(t)
	shareIDs := make(map[string]string)
	for _, permission := range []string{"viewer", "viewer", "editor"} {
		results := batchShareResults(t, f.post(fmt.Sprintf(
			`{"kb_ids":["kb-a","kb-b","kb-a"],"organization_ids":["org-a","org-b","org-a"],"permission":%q}`,
			permission,
		)))
		require.Len(t, results, 4)
		for i, pair := range [][2]string{{"kb-a", "org-a"}, {"kb-a", "org-b"}, {"kb-b", "org-a"}, {"kb-b", "org-b"}} {
			require.Equal(t, pair[0], results[i].KBID)
			require.Equal(t, pair[1], results[i].OrganizationID)
			require.Equal(t, http.StatusOK, results[i].Status)
			require.Nil(t, results[i].Error)
			require.NotNil(t, results[i].Share)
			require.Equal(t, types.OrgMemberRole(permission), results[i].Share.Permission)
			key := pair[0] + "/" + pair[1]
			if id, exists := shareIDs[key]; exists {
				require.Equal(t, id, results[i].Share.ID, "updating a share must preserve its identity")
			} else {
				shareIDs[key] = results[i].Share.ID
			}
		}
	}
	var stored []types.KnowledgeBaseShare
	require.NoError(t, f.db.Find(&stored).Error)
	require.Len(t, stored, 4)
	for _, share := range stored {
		require.Equal(t, types.OrgRoleEditor, share.Permission)
		require.Equal(t, uint64(7), share.SourceTenantID)
		require.Equal(t, "user", share.SharedByUserID)
	}
}

func TestBatchShareKnowledgeBasesChecksEveryKBOwner(t *testing.T) {
	f := newBatchShareFixture(t)
	results := batchShareResults(t, f.post(`{"kb_ids":["kb-a","kb-colleague","kb-foreign","missing"],`+
		`"organization_ids":["org-a"],"permission":"viewer"}`))
	require.Len(t, results, 4)
	for i, status := range []int{200, 403, 404, 404} {
		require.Equal(t, status, results[i].Status)
	}
	var count int64
	require.NoError(t, f.db.Model(&types.KnowledgeBaseShare{}).Count(&count).Error)
	require.EqualValues(t, 1, count)

	f.role = types.TenantRoleAdmin
	results = batchShareResults(t, f.post(`{"kb_ids":["kb-colleague","kb-foreign"],`+
		`"organization_ids":["org-a"],"permission":"viewer"}`))
	require.Equal(t, http.StatusOK, results[0].Status)
	require.Equal(t, http.StatusForbidden, results[1].Status)
}

func TestBatchShareKnowledgeBasesReturnsPartialFailures(t *testing.T) {
	f := newBatchShareFixture(t)
	w := f.post(`{"kb_ids":["kb-a"],"organization_ids":["org-viewer","org-a","org-nonmember",` +
		`"org-unavailable","org-missing","org-b"],"permission":"viewer"}`)
	results := batchShareResults(t, w)
	require.Len(t, results, 6)
	for i, status := range []int{403, 200, 403, 500, 404, 200} {
		require.Equal(t, status, results[i].Status)
		if status != http.StatusOK {
			require.Nil(t, results[i].Share)
			require.NotNil(t, results[i].Error)
		}
	}
	require.NotContains(t, w.Body.String(), "private database")

	w = f.post(`{"kb_ids":["kb-unavailable"],"organization_ids":["org-a"],"permission":"viewer"}`)
	results = batchShareResults(t, w)
	require.Equal(t, http.StatusServiceUnavailable, results[0].Status)
	require.NotContains(t, w.Body.String(), "private database")
}

func TestBatchShareKnowledgeBasesPreservesRouteAuthorization(t *testing.T) {
	for _, tt := range []struct {
		name   string
		role   types.TenantRole
		scope  *types.TenantAPIKeyScope
		status int
	}{
		{name: "JWT viewer", role: types.TenantRoleViewer, status: 403},
		{name: "JWT contributor", role: types.TenantRoleContributor, status: 200},
		{name: "full-access key", scope: &types.TenantAPIKeyScope{FullAccess: true}, status: 200},
		{name: "manage-spaces key", scope: &types.TenantAPIKeyScope{
			Capabilities: types.StringArray{string(types.APIKeyCapabilityManageSpaces)},
		}, status: 403},
		{name: "ingest key", scope: &types.TenantAPIKeyScope{
			Capabilities: types.StringArray{string(types.APIKeyCapabilityIngest)},
		}, status: 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newBatchShareFixture(t)
			f.role, f.scope = tt.role, tt.scope
			w := f.post(`{"kb_ids":["kb-a"],"organization_ids":["org-a"],"permission":"viewer"}`)
			require.Equal(t, tt.status, w.Code, w.Body.String())
			var count int64
			require.NoError(t, f.db.Model(&types.KnowledgeBaseShare{}).Count(&count).Error)
			if tt.status == http.StatusForbidden {
				require.Zero(t, count)
			} else {
				require.EqualValues(t, 1, count)
			}
		})
	}
}

func TestBatchShareKnowledgeBasesRejectsInvalidBatchesBeforeWriting(t *testing.T) {
	for name, body := range map[string]string{
		"malformed JSON":     `{`,
		"empty lists":        `{"kb_ids":[],"organization_ids":[],"permission":"viewer"}`,
		"missing org list":   `{"kb_ids":["kb-a"],"permission":"viewer"}`,
		"invalid permission": `{"kb_ids":["kb-a"],"organization_ids":["org-a"],"permission":"owner"}`,
		"empty KB ID":        `{"kb_ids":["kb-a",""],"organization_ids":["org-a"],"permission":"viewer"}`,
		"empty org ID":       `{"kb_ids":["kb-a"],"organization_ids":[""],"permission":"viewer"}`,
		"oversized body":     `{"padding":"` + strings.Repeat("x", 65536) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newBatchShareFixture(t)
			w := f.post(body)
			require.Contains(t, []int{http.StatusBadRequest, http.StatusRequestEntityTooLarge}, w.Code)
			var count int64
			require.NoError(t, f.db.Model(&types.KnowledgeBaseShare{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}

	f := newBatchShareFixture(t)
	ids := make([]string, 100)
	for i := range ids {
		ids[i] = fmt.Sprintf("kb-%d", i)
	}
	body, err := json.Marshal(map[string]any{
		"kb_ids": ids, "organization_ids": []string{"org-a", "org-b"}, "permission": "viewer",
	})
	require.NoError(t, err)
	w := f.post(string(body))
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
}

func TestBatchShareKnowledgeBasesConsumesWholeRequestBeforeWriting(t *testing.T) {
	const valid = `{"kb_ids":["kb-a"],"organization_ids":["org-a"],"permission":"viewer"}`
	for _, tt := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "oversized trailing whitespace", body: valid + strings.Repeat(" ", 65536), status: 413},
		{name: "second JSON value", body: valid + `{}`, status: 400},
		{name: "trailing junk", body: valid + `junk`, status: 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newBatchShareFixture(t)
			w := f.post(tt.body)
			require.Equal(t, tt.status, w.Code, w.Body.String())
			var count int64
			require.NoError(t, f.db.Model(&types.KnowledgeBaseShare{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestBatchShareKnowledgeBasesAcceptsExactly100Pairs(t *testing.T) {
	f := newBatchShareFixture(t)
	orgIDs := make([]string, 100)
	for i := range orgIDs {
		orgIDs[i] = fmt.Sprintf("org-%d", i)
	}
	body, err := json.Marshal(map[string]any{
		"kb_ids": []string{"kb-a"}, "organization_ids": orgIDs, "permission": "viewer",
	})
	require.NoError(t, err)
	results := batchShareResults(t, f.post(string(body)))
	require.Len(t, results, 100)
	for _, result := range results {
		require.Equal(t, http.StatusOK, result.Status)
	}
	var count int64
	require.NoError(t, f.db.Model(&types.KnowledgeBaseShare{}).Count(&count).Error)
	require.EqualValues(t, 100, count)

	orgIDs = append(orgIDs, orgIDs[0])
	body, err = json.Marshal(map[string]any{
		"kb_ids": []string{"kb-b"}, "organization_ids": orgIDs, "permission": "editor",
	})
	require.NoError(t, err)
	w := f.post(string(body))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.NoError(t, f.db.Model(&types.KnowledgeBaseShare{}).Count(&count).Error)
	require.EqualValues(t, 100, count, "raw lists longer than 100 must fail without new writes")
}
