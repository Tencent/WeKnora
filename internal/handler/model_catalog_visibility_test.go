package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// listProvidersAs drives the real handler with one tenant role.
func listProvidersAs(t *testing.T, role types.TenantRole) []map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/providers", nil)
	ctx := context.WithValue(req.Context(), types.TenantRoleContextKey, role)
	c.Request = req.WithContext(ctx)

	(&ModelHandler{}).ListModelProviders(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) == 0 {
		t.Fatalf("expected the vendor catalog to be registered")
	}
	return body.Data
}

// A deployment overlay can repoint a vendor at an internal gateway, and the
// same URL is stripped from model rows for a viewer (dto.NewModelResponse),
// so the vendor list must not hand it out either. Everything else on a vendor
// is public product information and stays.
func TestListModelProvidersHidesDefaultURLsFromViewers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		role    types.TenantRole
		wantURL bool
	}{
		{"admin configures models, so it needs the prefill", types.TenantRoleAdmin, true},
		{"viewer cannot configure models", types.TenantRoleViewer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providers := listProvidersAs(t, tc.role)
			sawURL := false
			for _, p := range providers {
				if urls, ok := p["defaultUrls"].(map[string]any); ok && len(urls) > 0 {
					sawURL = true
				}
				// The rest of the vendor definition is what the picker needs.
				if p["value"] == "" || p["label"] == "" {
					t.Errorf("vendor identity should always be present: %+v", p)
				}
			}
			if sawURL != tc.wantURL {
				t.Errorf("defaultUrls present = %v, want %v", sawURL, tc.wantURL)
			}
		})
	}
}

// resolveAs drives ResolveModelCatalog with a query string.
func resolveAs(t *testing.T, query string) map[string]any {
	t.Helper()
	return resolveAsRole(t, query, types.TenantRoleAdmin)
}

func resolveAsRole(t *testing.T, query string, role types.TenantRole) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/catalog/resolve?"+query, nil)
	ctx := context.WithValue(req.Context(), types.TenantRoleContextKey, role)
	c.Request = req.WithContext(ctx)

	(&ModelHandler{}).ResolveModelCatalog(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body.Data
}

// The editor's "接入诊断" panel must resolve the same request the row will
// actually make. Azure is the sharp case: api_version alone decides between
// the v1 data plane and the dated deployments path, so a preview that drops
// it describes the wrong endpoint.
func TestResolveModelCatalogForwardsVendorExtraFields(t *testing.T) {
	base := "provider=azure_openai&model=gpt-4o&base_url=https://acme.openai.azure.com&model_type=chat"

	withoutVersion := resolveAs(t, base)
	withVersion := resolveAs(t, base+"&api_version=2025-04-01-preview")

	url1, _ := withoutVersion["url"].(string)
	url2, _ := withVersion["url"].(string)
	if url1 == "" || url2 == "" {
		t.Fatalf("expected a resolved url, got %+v / %+v", withoutVersion, withVersion)
	}
	if url1 == url2 {
		t.Fatalf("api_version must change the resolved endpoint, both were %q", url1)
	}
	if !strings.Contains(url1, "/openai/v1") {
		t.Errorf("no api_version should resolve to the v1 data plane, got %q", url1)
	}
	if !strings.Contains(url2, "/openai/deployments/") || !strings.Contains(url2, "2025-04-01-preview") {
		t.Errorf("an explicit api_version should resolve to the dated path, got %q", url2)
	}
}

// Omitting base_url makes the resolution fall back to the vendor default,
// which a deployment overlay may have repointed at an internal gateway — so
// the same gate that hides defaultUrls has to cover this response.
func TestResolveModelCatalogHidesEndpointFromViewers(t *testing.T) {
	query := "provider=azure_openai&model=gpt-4o&base_url=https://acme.openai.azure.com&model_type=chat"

	admin := resolveAsRole(t, query, types.TenantRoleAdmin)
	if admin["base_url"] == nil || admin["url"] == nil {
		t.Fatalf("an admin should see the endpoint, got %+v", admin)
	}

	viewer := resolveAsRole(t, query, types.TenantRoleViewer)
	if viewer["base_url"] != nil || viewer["url"] != nil {
		t.Errorf("viewer should not see the endpoint, got %+v", viewer)
	}
	// The capability answer itself is not configuration and still resolves.
	if viewer["api"] == nil || viewer["capabilities"] == nil {
		t.Errorf("viewer should still get the protocol and capabilities, got %+v", viewer)
	}
}
