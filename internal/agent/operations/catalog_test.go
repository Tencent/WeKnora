package operations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinCatalogIsValid(t *testing.T) {
	catalog := Builtin()
	require.NotEmpty(t, catalog.All())
	for _, op := range catalog.All() {
		if op.Risk.Mutates() {
			require.NotNil(t, op.Target, op.ID)
		}
	}
}

func readOp(id, route string, params ...Param) Operation {
	return Operation{
		ID: id, Summary: "s", Risk: RiskRead, Method: http.MethodGet, Route: route, Params: params,
		Result: Result{Root: "data", Fields: []Field{{From: "title"}}},
	}
}

func TestNewCatalogRejectsUnsafeDeclarations(t *testing.T) {
	kb := Param{Name: "knowledge_base_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	cases := map[string]Operation{
		"forbidden area": readOp("auth.get", "/api/v1/auth/me"),
		"api keys":       readOp("keys.list", "/api/v1/api-keys"),
		"outside v1":     readOp("x.get", "/health"),
		"read as POST": {
			ID: "x.get", Summary: "s", Risk: RiskRead, Method: http.MethodPost,
			Route: "/api/v1/knowledge-bases",
		},
		"write as GET": {
			ID: "x.set", Summary: "s", Risk: RiskWrite, Method: http.MethodGet,
			Route: "/api/v1/knowledge-bases", Target: &Target{Operation: "x.get", Label: "title"},
		},
		"delete not destructive": {
			ID: "x.del", Summary: "s", Risk: RiskWrite, Method: http.MethodDelete,
			Route: "/api/v1/knowledge-bases", Target: &Target{Operation: "x.get", Label: "title"},
		},
		"mutation without target": {
			ID: "x.del", Summary: "s", Risk: RiskDestructive, Method: http.MethodDelete,
			Route: "/api/v1/knowledge-bases",
		},
		"route segment without param": readOp("x.get", "/api/v1/knowledge-bases/:id"),
		"param without route segment": readOp("x.get", "/api/v1/knowledge-bases", kb),
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewCatalog(op)
			assert.Error(t, err)
		})
	}
}

func TestNewCatalogChecksTargets(t *testing.T) {
	doc := Param{Name: "knowledge_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	get := readOp("knowledge.get", "/api/v1/knowledge/:id", doc)
	del := Operation{
		ID: "knowledge.delete", Summary: "s", Risk: RiskDestructive, Method: http.MethodDelete,
		Route: "/api/v1/knowledge/:id", Params: []Param{doc},
		Target: &Target{Operation: "knowledge.get", Label: "title"},
	}
	_, err := NewCatalog(get, del)
	require.NoError(t, err)

	missingLabel := del
	missingLabel.Target = &Target{Operation: "knowledge.get", Label: "name"}
	_, err = NewCatalog(get, missingLabel)
	assert.Error(t, err)

	noParam := del
	noParam.Route = "/api/v1/knowledge"
	noParam.Params = nil
	_, err = NewCatalog(get, noParam)
	assert.Error(t, err, "target needs a param the mutation does not declare")
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestBuildValidatesAndEscapes(t *testing.T) {
	op, ok := Builtin().Get("knowledge.list")
	require.True(t, ok)

	call, err := op.Build(map[string]json.RawMessage{
		"knowledge_base_id": raw("kb-1"),
		"parse_status":      raw("failed"),
		"page_size":         raw(20),
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, call.Method)
	assert.Equal(t, "/api/v1/knowledge-bases/kb-1/knowledge?page_size=20&parse_status=failed", call.URI())
	assert.Nil(t, call.Body)

	_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb-1"), "tenant_id": raw(2)})
	assert.ErrorContains(t, err, "unknown parameter")
	_, err = op.Build(map[string]json.RawMessage{})
	assert.ErrorContains(t, err, "is required")
	_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb-1"), "parse_status": raw("deleting")})
	assert.ErrorContains(t, err, "must be one of")
	_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb-1"), "page_size": raw(500)})
	assert.ErrorContains(t, err, "between")
	_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb-1"), "page": raw("1")})
	assert.ErrorContains(t, err, "integer")

	for _, bad := range []string{"..", "a/b", "a?x=1", "a#b", "a\\b"} {
		_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw(bad)})
		assert.Error(t, err, bad)
	}
	call, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb 1%")})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/knowledge-bases/kb%201%25/knowledge", call.Path)
}

func TestBuildCatchAllPath(t *testing.T) {
	slug := Param{Name: "slug", In: InPath, Type: TypeString, Required: true, Description: "d"}
	op := readOp(
		"wiki.get_page",
		"/api/v1/knowledgebase/:kb_id/wiki/pages/*slug",
		Param{
			Name:        "knowledge_base_id",
			In:          InPath,
			Key:         "kb_id",
			Type:        TypeString,
			Required:    true,
			Description: "d",
		},
		slug,
	)
	_, err := NewCatalog(op)
	require.NoError(t, err)

	call, err := op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "slug": raw("concept/a b")})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/knowledgebase/kb/wiki/pages/concept/a%20b", call.Path)
	for _, bad := range []string{"concept/../x", "/abs", "a//b", "."} {
		_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "slug": raw(bad)})
		assert.Error(t, err, bad)
	}
}

func TestProjectKeepsOnlyDeclaredFields(t *testing.T) {
	op, _ := Builtin().Get("knowledge.list")
	body := `{"success":true,"total":3,"page":1,"page_size":2,"data":[
		{"id":"k1","title":"A","parse_status":"failed","error_message":"bad pdf",
		 "file_path":"s3://secret","tenant_id":9,"metadata":{"x":1}},
		{"id":"k2","title":"B","parse_status":"completed"}]}`
	projected, err := op.Project([]byte(body))
	require.NoError(t, err)
	out, _ := json.Marshal(projected)
	assert.JSONEq(t, `{"data":[
		{"knowledge_id":"k1","title":"A","parse_status":"failed","error_message":"bad pdf"},
		{"knowledge_id":"k2","title":"B","parse_status":"completed"}],
		"meta":{"total":3,"page":1,"page_size":2}}`, string(out))
}

func TestProjectTruncatesLists(t *testing.T) {
	op, _ := Builtin().Get("wiki.list_issues")
	items := make([]map[string]any, defaultMaxResultItems+5)
	for i := range items {
		items[i] = map[string]any{"id": "x", "description": strings.Repeat("长", maxProjectedStringRunes+10)}
	}
	body, _ := json.Marshal(items)
	projected, err := op.Project(body)
	require.NoError(t, err)
	assert.True(t, projected.Truncated)
	list := projected.Data.([]any)
	assert.Len(t, list, defaultMaxResultItems)
	desc := list[0].(map[string]any)["description"].(string)
	assert.Equal(t, maxProjectedStringRunes+1, len([]rune(desc)))
}

type ctxKey struct{}

func TestDispatchDetachesContextAndCarriesIdentity(t *testing.T) {
	var got *http.Request
	var gotBody string
	d := NewHandlerDispatcher()
	_, err := d.Dispatch(
		context.Background(),
		Identity{UserID: "u", TenantID: 1},
		Call{Method: http.MethodGet, Path: "/x"},
	)
	assert.ErrorIs(t, err, ErrDispatcherUnavailable)

	d.Bind(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	parent, cancel := context.WithTimeout(context.WithValue(context.Background(), ctxKey{}, "leak"), time.Minute)
	defer cancel()
	parent = context.WithValue(parent, types.TenantIDContextKey, uint64(99))
	resp, err := d.Dispatch(parent, Identity{
		UserID: "u1", TenantID: 7, Language: "zh-CN",
		Agent: types.AuditAgentAction{SessionID: "s1", Operation: "knowledge.delete"},
	}, Call{Method: http.MethodPost, Path: "/api/v1/x", Body: []byte(`{"a":1}`)})
	require.NoError(t, err)
	assert.Equal(t, http.StatusAccepted, resp.Status)
	assert.JSONEq(t, `{"ok":true}`, string(resp.Body))

	ctx := got.Context()
	assert.Nil(t, ctx.Value(ctxKey{}), "agent run values must not reach the handler")
	assert.Nil(t, ctx.Value(types.TenantIDContextKey))
	_, hasDeadline := ctx.Deadline()
	assert.True(t, hasDeadline, "cancellation still propagates")
	dispatch, ok := types.InternalDispatchFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, types.InternalDispatch{UserID: "u1", TenantID: 7}, dispatch)
	agent, ok := types.AuditAgentActionFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "knowledge.delete", agent.Operation)
	assert.Empty(t, got.Header.Get("Authorization"))
	assert.Equal(t, "zh-CN", got.Header.Get("Accept-Language"))
	assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
	assert.Equal(t, `{"a":1}`, gotBody)

	_, err = d.Dispatch(parent, Identity{UserID: "", TenantID: 7}, Call{Method: http.MethodGet, Path: "/x"})
	assert.Error(t, err, "an incomplete identity is never dispatched")
}

func TestDispatchRejectsOversizedResponses(t *testing.T) {
	d := NewHandlerDispatcher()
	d.Bind(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxResponseBytes+1))
	}))
	_, err := d.Dispatch(
		context.Background(),
		Identity{UserID: "u", TenantID: 1},
		Call{Method: http.MethodGet, Path: "/x"},
	)
	assert.ErrorIs(t, err, ErrResponseTooLarge)
}
