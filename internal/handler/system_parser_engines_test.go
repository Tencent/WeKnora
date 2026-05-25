package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func makeParserCtxWithTenant(tenant *types.Tenant) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, tenant)
	c.Request = req.WithContext(ctx)
	c.Set(types.TenantInfoContextKey.String(), tenant)
	return c, w
}

type parserEnginesBody struct {
	Code             int                      `json:"code"`
	Msg              string                   `json:"msg"`
	Data             []types.ParserEngineInfo `json:"data"`
	DocReaderAddr    string                   `json:"docreader_addr"`
	Connected        bool                     `json:"connected"`
	AllowedProviders []string                 `json:"allowed_providers"`
}

func decodeParserBody(t *testing.T, w *httptest.ResponseRecorder) *parserEnginesBody {
	t.Helper()
	var body parserEnginesBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v\nraw=%s", err, w.Body.String())
	}
	return &body
}

func findEngine(list []types.ParserEngineInfo, name string) *types.ParserEngineInfo {
	for i := range list {
		if list[i].Name == name {
			return &list[i]
		}
	}
	return nil
}

// Builtin mineru endpoint provided → mineru engine reports a different reason
// than "not configured" even when tenant has nothing.
func TestListParserEngines_BuiltinEnablesMineru(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	types.SetBuiltinParserEngineForTest(&types.BuiltinParserEngineConfig{
		MinerU: &types.BuiltinMinerUConfig{Endpoint: "http://invalid-but-non-empty"},
	})
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "")

	h := &SystemHandler{}
	c, w := makeParserCtxWithTenant(&types.Tenant{})
	h.ListParserEngines(c)

	body := decodeParserBody(t, w)
	mineru := findEngine(body.Data, "mineru")
	if mineru == nil {
		t.Fatal("mineru not in response")
	}
	if mineru.UnavailableReason == "MinerU service not configured" {
		t.Fatalf("builtin endpoint should suppress 'not configured' reason, got %q", mineru.UnavailableReason)
	}
}

// Allow list excludes mineru → mineru reports Allowed=false, Available=false.
func TestListParserEngines_AllowListBlocksMineru(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "builtin,simple")

	h := &SystemHandler{}
	c, w := makeParserCtxWithTenant(&types.Tenant{})
	h.ListParserEngines(c)

	body := decodeParserBody(t, w)
	mineru := findEngine(body.Data, "mineru")
	if mineru == nil {
		t.Fatal("mineru should still appear in response (visible-but-blocked)")
	}
	if mineru.Allowed {
		t.Errorf("mineru should be Allowed=false")
	}
	if mineru.Available {
		t.Errorf("mineru should be Available=false when blocked")
	}
	if mineru.UnavailableReason != "not allowed by PARSER_ENGINE_ALLOW_LIST" {
		t.Errorf("UnavailableReason=%q", mineru.UnavailableReason)
	}

	wantAllowed := map[string]bool{"builtin": true, "simple": true}
	if len(body.AllowedProviders) != 2 {
		t.Fatalf("AllowedProviders=%v", body.AllowedProviders)
	}
	for _, p := range body.AllowedProviders {
		if !wantAllowed[p] {
			t.Errorf("unexpected allowed provider %q", p)
		}
	}
}

// Empty allow list env + no builtin + no tenant → simple still Allowed && Available.
func TestListParserEngines_DefaultsToAllAllowed(t *testing.T) {
	types.ResetBuiltinParserEngineForTest()
	t.Cleanup(types.ResetBuiltinParserEngineForTest)
	t.Setenv("PARSER_ENGINE_ALLOW_LIST", "")

	h := &SystemHandler{}
	c, w := makeParserCtxWithTenant(&types.Tenant{})
	h.ListParserEngines(c)

	body := decodeParserBody(t, w)
	simple := findEngine(body.Data, "simple")
	if simple == nil || !simple.Allowed || !simple.Available {
		t.Fatalf("simple should be Allowed && Available, got %+v", simple)
	}
}
