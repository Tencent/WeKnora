package operations

import (
	"context"
	"encoding/json"
	"fmt"
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
		if op.Risk.Mutates() && op.LabelParam == "" {
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
		// Areas whose names extend a sensitive one, or that no list named.
		"sandbox configs":         readOp("x.get", "/api/v1/sandbox-configs"),
		"storage backends":        readOp("x.get", "/api/v1/storage-backends"),
		"web search credentials":  readOp("x.get", "/api/v1/web-search-providers/:id/credentials", kb),
		"im channels":             readOp("x.get", "/api/v1/im-channels"),
		"agent approvals":         readOp("x.get", "/api/v1/agent/tool-approvals"),
		"memory":                  readOp("x.get", "/api/v1/memory/items"),
		"a new area":              readOp("x.get", "/api/v1/something-new"),
		"kb shares":               readOp("x.get", "/api/v1/knowledge-bases/:id/shares", kb),
		"kb prefix lookalike":     readOp("x.get", "/api/v1/knowledge-bases-admin"),
		"agent shares":            readOp("x.get", "/api/v1/agents/:id/shares", kb),
		"agent embed channels":    readOp("x.get", "/api/v1/agents/:id/embed-channels", kb),
		"agent im channels":       readOp("x.get", "/api/v1/agents/:id/im-channels", kb),
		"model credentials":       readOp("x.get", "/api/v1/models/:id/credentials", kb),
		"data source credentials": readOp("x.get", "/api/v1/datasource/:id/credentials", kb),
		"raw data source credentials": {
			ID: "x.test", Summary: "s", Risk: RiskProbe, Method: http.MethodPost,
			Route: "/api/v1/datasource/validate-credentials",
		},
		"mcp oauth": readOp("x.get", "/api/v1/mcp-services/:id/oauth/status", kb),
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
		"probe as GET": {
			ID: "x.check", Summary: "s", Risk: RiskProbe, Method: http.MethodGet,
			Route: "/api/v1/knowledge-bases",
		},
		"probe as PUT": {
			ID: "x.check", Summary: "s", Risk: RiskProbe, Method: http.MethodPut,
			Route: "/api/v1/knowledge-bases",
		},
		"probe with an approval target": {
			ID: "x.check", Summary: "s", Risk: RiskProbe, Method: http.MethodPost,
			Route: "/api/v1/knowledge-bases", Target: &Target{Operation: "x.get", Label: "title"},
		},
		// Conversations are read-only: no deleting, stopping or rewinding.
		"delete a session": {
			ID: "session.delete", Summary: "s", Risk: RiskDestructive, Method: http.MethodDelete,
			Route: "/api/v1/sessions/:id", Params: routeParams("/api/v1/sessions/:id"),
			Target: &Target{Operation: "session.get", Label: "title"},
		},
		"stop a session": {
			ID: "session.stop", Summary: "s", Risk: RiskWrite, Method: http.MethodPost,
			Route: "/api/v1/sessions/:session_id/stop", Params: routeParams("/api/v1/sessions/:session_id/stop"),
			Target: &Target{Operation: "session.get", Label: "title"},
		},
		"delete a message": {
			ID: "message.delete", Summary: "s", Risk: RiskDestructive, Method: http.MethodDelete,
			Route:  "/api/v1/messages/:session_id/:id",
			Params: routeParams("/api/v1/messages/:session_id/:id"),
			Target: &Target{Operation: "session.get", Label: "title"},
		},
	}
	reasons := map[string]string{
		"delete a session": "may only read",
		"stop a session":   "may only read",
		"delete a message": "may only read",
	}
	// A sandbox config is reachable only for its installed skills, and never
	// the installer conversation a user is watching.
	for _, route := range []struct{ method, path, reason string }{
		{http.MethodGet, "/api/v1/sandbox-configs/:id", "outside the areas"},
		{http.MethodPut, "/api/v1/sandbox-configs/:id", "outside the areas"},
		{http.MethodGet, "/api/v1/sandbox-configs/:id/sandboxes", "outside the areas"},
		{http.MethodPut, "/api/v1/sandbox-configs/workspace-policy", "outside the areas"},
		{http.MethodPost, "/api/v1/sandbox-configs/templates/query", "outside the areas"},
		{http.MethodGet, "/api/v1/sandbox-configs/:id/skills/:skillId/guidance", `reaches "guidance"`},
		{http.MethodPost, "/api/v1/sandbox-configs/:id/skills/:skillId/guidance", `reaches "guidance"`},
		{http.MethodGet, "/api/v1/sandbox-configs/:id/skills/:skillId/install-events", `reaches "install-events"`},
		{http.MethodGet, "/api/v1/sandbox-configs/:id/skills/:skillId/transcript", `reaches "transcript"`},
	} {
		op := readOp("skill.admin", route.path, routeParams(route.path)...)
		op.Method = route.method
		if route.method != http.MethodGet {
			op.Risk = RiskWrite
			op.Target = &Target{Operation: "skill.get", Label: "name"}
		}
		cases[route.method+" "+route.path] = op
		reasons[route.method+" "+route.path] = route.reason
	}
	for name, op := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewCatalog(op)
			require.Error(t, err)
			if reason := reasons[name]; reason != "" {
				assert.Contains(t, err.Error(), reason)
			}
		})
	}
}

// routeParams declares a string path param for every :name segment.
func routeParams(route string) []Param {
	var params []Param
	for _, seg := range strings.Split(route, "/") {
		if strings.HasPrefix(seg, ":") {
			params = append(params, Param{
				Name: strings.ToLower(seg[1:]), In: InPath, Key: seg[1:], Type: TypeString,
				Required: true, Description: "d",
			})
		}
	}
	return params
}

func TestNewCatalogAdmitsInstalledSkillRoutes(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/sandbox-configs/:id/skills"},
		{http.MethodGet, "/api/v1/sandbox-configs/:id/skills/:skillId"},
		{http.MethodPatch, "/api/v1/sandbox-configs/:id/skills/:skillId"},
		{http.MethodGet, "/api/v1/skills/catalog/:id"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			op := readOp("skill.x", route.path, routeParams(route.path)...)
			op.Method = route.method
			if route.method != http.MethodGet {
				op.Risk = RiskWrite
				op.Target = &Target{Operation: "skill.get", Label: "title"}
			}
			get := readOp("skill.get", "/api/v1/sandbox-configs/:id/skills/:skillId",
				routeParams("/api/v1/sandbox-configs/:id/skills/:skillId")...)
			if op.ID == get.ID {
				op.ID = "skill.other"
			}
			_, err := NewCatalog(op, get)
			assert.NoError(t, err)
		})
	}
}

func TestAlwaysConfirmAsksAtEveryLevel(t *testing.T) {
	install := Operation{ID: "skill.install", Risk: RiskWrite, AlwaysConfirm: true}
	update := Operation{ID: "agent.update", Risk: RiskWrite}
	auto := Access{Level: types.AssistantAccessAutoWrite}
	assert.True(t, auto.NeedsApproval(install))
	assert.False(t, auto.NeedsApproval(update))

	op := Operation{
		ID: "skill.uninstall", Summary: "s", Risk: RiskDestructive, Method: http.MethodPost,
		Route: "/api/v1/knowledge-bases", LabelParam: "", AlwaysConfirm: true,
		Target: &Target{Operation: "x.get", Label: "title"},
	}
	_, err := NewCatalog(op, readOp("x.get", "/api/v1/knowledge-bases"))
	assert.ErrorContains(t, err, "always_confirm is for writes")
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

func TestNewCatalogChecksLabelParam(t *testing.T) {
	name := Param{Name: "name", In: InBody, Type: TypeString, Required: true, Description: "d"}
	create := Operation{
		ID: "agent.create", Summary: "s", Risk: RiskWrite, Method: http.MethodPost,
		Route: "/api/v1/agents", Params: []Param{name}, LabelParam: "name",
	}
	_, err := NewCatalog(create)
	require.NoError(t, err)

	cases := map[string]func(*Operation){
		"unknown param": func(op *Operation) { op.LabelParam = "title" },
		"optional param": func(op *Operation) {
			op.Params = []Param{{Name: "name", In: InBody, Type: TypeString, Description: "d"}}
		},
		"destructive":     func(op *Operation) { op.Risk = RiskDestructive },
		"not a POST":      func(op *Operation) { op.Method = http.MethodPut },
		"with a target":   func(op *Operation) { op.Target = &Target{Operation: "x.get", Label: "title"} },
		"read with label": func(op *Operation) { op.Risk, op.Method = RiskRead, http.MethodGet },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			op := create
			op.Params = append([]Param(nil), create.Params...)
			mut(&op)
			_, err := NewCatalog(op, readOp("x.get", "/api/v1/agents"))
			assert.Error(t, err)
		})
	}
}

func TestNewCatalogChecksItemTargets(t *testing.T) {
	kb := Param{Name: "knowledge_base_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	doc := Param{Name: "knowledge_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	ids := Param{
		Name: "knowledge_ids", In: InBody, Key: "ids", Type: TypeStringList, Required: true,
		Description: "d", MaxItems: MaxApprovalItems,
	}
	getKB := readOp("knowledge_base.get", "/api/v1/knowledge-bases/:id", kb)
	getDoc := readOp("knowledge.get", "/api/v1/knowledge/:id", doc)
	batch := func(mut func(*Operation)) Operation {
		kbBody := kb
		kbBody.In, kbBody.Key = InBody, "kb_id"
		op := Operation{
			ID: "knowledge.batch_delete", Summary: "s", Risk: RiskDestructive, Method: http.MethodPost,
			Route: "/api/v1/knowledge/batch-delete", Params: []Param{kbBody, ids},
			Target: &Target{Operation: "knowledge_base.get", Label: "title", Items: &ItemTarget{
				Param: "knowledge_ids", Operation: "knowledge.get", As: "knowledge_id", Label: "title",
			}},
		}
		if mut != nil {
			mut(&op)
		}
		return op
	}
	_, err := NewCatalog(getKB, getDoc, batch(nil))
	require.NoError(t, err)

	cases := map[string]func(*Operation){
		"uncapped list": func(op *Operation) { op.Params[1].MaxItems = 0 },
		"list longer than an approval shows": func(op *Operation) {
			op.Params[1].MaxItems = MaxApprovalItems + 1
		},
		"items param is not a list":   func(op *Operation) { op.Target.Items.Param = "knowledge_base_id" },
		"item param the target lacks": func(op *Operation) { op.Target.Items.As = "chunk_id" },
		"item param shadows a param": func(op *Operation) {
			op.Params = append(op.Params, Param{Name: "knowledge_id", In: InBody, Type: TypeString, Description: "d"})
		},
		"item label not projected":  func(op *Operation) { op.Target.Items.Label = "name" },
		"item target is a mutation": func(op *Operation) { op.Target.Items.Operation = "knowledge.batch_delete" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewCatalog(getKB, getDoc, batch(mut))
			assert.Error(t, err)
		})
	}
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
	_, err = op.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb-1"), "parse_status": raw("archived")})
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

func TestBuildIntegerPathAndList(t *testing.T) {
	kb := Param{Name: "knowledge_base_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	entry := Param{Name: "entry_id", In: InPath, Type: TypeInteger, Required: true, Description: "d"}
	get := readOp("faq.get_entry", "/api/v1/knowledge-bases/:id/faq/entries/:entry_id", kb, entry)
	del := Operation{
		ID: "faq.delete_entries", Summary: "s", Risk: RiskDestructive, Method: http.MethodDelete,
		Route: "/api/v1/knowledge-bases/:id/faq/entries",
		Params: []Param{kb, {
			Name: "entry_ids", In: InBody, Key: "ids", Type: TypeIntegerList, Required: true,
			Description: "d", MaxItems: 2,
		}},
		Target: &Target{Operation: "knowledge_base.get", Label: "title", Items: &ItemTarget{
			Param: "entry_ids", Operation: "faq.get_entry", As: "entry_id", Label: "title",
		}},
	}
	getKB := readOp("knowledge_base.get", "/api/v1/knowledge-bases/:id", kb)
	_, err := NewCatalog(getKB, get, del)
	require.NoError(t, err, "a DELETE may carry a body, and integer items resolve an integer param")

	call, err := get.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "entry_id": raw(42)})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/knowledge-bases/kb/faq/entries/42", call.Path)
	_, err = get.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "entry_id": raw("42/../x")})
	assert.Error(t, err, "an integer path param takes integers only")

	call, err = del.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "entry_ids": raw([]int{1, 2})})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ids":[1,2]}`, string(call.Body))
	for _, bad := range []any{[]int{1, 2, 3}, []string{"1"}, []float64{1.5}, []int{}} {
		_, err = del.Build(map[string]json.RawMessage{"knowledge_base_id": raw("kb"), "entry_ids": raw(bad)})
		assert.Error(t, err, bad)
	}

	stringItem := del
	stringItem.Target = &Target{Operation: "knowledge_base.get", Label: "title", Items: &ItemTarget{
		Param: "entry_ids", Operation: "knowledge.get", As: "knowledge_id", Label: "title",
	}}
	doc := Param{Name: "knowledge_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	_, err = NewCatalog(getKB, get, readOp("knowledge.get", "/api/v1/knowledge/:id", doc), stringItem)
	assert.Error(t, err, "integer items cannot fill a string param")
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
		 "file_path":"s3://secret","tenant_id":9,"metadata":{"x":1},
		 "tags":[{"id":"t1","seq_id":7,"name":"Ops","color":"#f00","tenant_id":9}]},
		{"id":"k2","title":"B","parse_status":"completed","tags":null}]}`
	projected, err := op.Project([]byte(body))
	require.NoError(t, err)
	out, _ := json.Marshal(projected)
	assert.JSONEq(t, `{"data":[
		{"knowledge_id":"k1","title":"A","parse_status":"failed","error_message":"bad pdf",
		 "tags":[{"tag_id":"t1","tag_seq_id":7,"name":"Ops"}]},
		{"knowledge_id":"k2","title":"B","parse_status":"completed","tags":[]}],
		"meta":{"total":3,"page":1,"page_size":2}}`, string(out))
}

func TestProjectModelEndpointAndCatalog(t *testing.T) {
	get, ok := Builtin().Get("model.get")
	require.True(t, ok)
	model := []byte(`{"data":{"id":"m1","name":"hy4","type":"KnowledgeQA",
		"parameters":{"base_url":"http://127.0.0.1:3425/v1","provider":"magpie",
		"custom_headers":{"Authorization":"x"},"extra_config":{"k":"v","remote_model_name":"hy4-preview"},
		"spec":{"compat":{"extra_body":{"token":"x"}}}},
		"capabilities":{"provider":"magpie","api":"openai-responses","context_window":960000,
		"max_output_tokens":64000,"input":["text","image"],"thinking_format":"x"}}}`)
	projected, err := get.Project(model)
	require.NoError(t, err)
	data := projected.Data.(map[string]any)
	assert.Equal(t, "http://127.0.0.1:3425/v1", data["base_url"])
	assert.Equal(t, "magpie", data["provider"])
	assert.Equal(t, "hy4-preview", data["remote_model_name"])
	assert.Equal(t, "openai-responses", data["api"])
	assert.Equal(t, json.Number("960000"), data["context_window"])
	assert.Equal(t, []any{"text", "image"}, data["input"])
	for _, hidden := range []string{"custom_headers", "extra_config", "k", "spec", "compat", "thinking_format"} {
		assert.NotContains(t, data, hidden)
	}

	models, ok := Builtin().Get("model.list")
	require.True(t, ok)
	modelObject := strings.TrimSuffix(strings.TrimPrefix(string(model), `{"data":`), `}`)
	projected, err = models.Project([]byte(`{"data":[` + modelObject + `]}`))
	require.NoError(t, err)
	row := projected.Data.([]any)[0].(map[string]any)
	assert.Equal(t, "magpie", row["provider"])
	assert.NotContains(t, row, "context_window", "the list leaves capabilities to model.get")
	assert.NotContains(t, row, "remote_model_name")

	list, ok := Builtin().Get("model.list_providers")
	require.True(t, ok)
	body := `{"data":[{"value":"magpie","label":"Magpie","description":"d","icon":"data:x",
		"modelTypes":["chat"],"models":[{"id":"a","name":"A"}]}]}`
	projected, err = list.Project([]byte(body))
	require.NoError(t, err)
	out, _ := json.Marshal(projected.Data)
	assert.JSONEq(t, `[{"provider":"magpie","label":"Magpie","model_types":["chat"]}]`, string(out),
		"the list leaves models and descriptions to model.get_provider")

	one, ok := Builtin().Get("model.get_provider")
	require.True(t, ok)
	projected, err = one.Project([]byte(`{"data":{"value":"magpie","label":"Magpie","models":[
		{"id":"codex/gpt","name":"GPT","type":"chat","reasoning":true,"context_window":272000,"cost":{"input":1}}]}}`))
	require.NoError(t, err)
	entries := projected.Data.(map[string]any)["models"].([]any)
	assert.Equal(t, map[string]any{
		"model_name": "codex/gpt", "display_name": "GPT", "type": "chat", "reasoning": true,
		"context_window": json.Number("272000"),
	}, entries[0])
}

func TestProjectKnowledgeGetReportsTags(t *testing.T) {
	op, ok := Builtin().Get("knowledge.get")
	require.True(t, ok)
	tags := make([]map[string]any, defaultMaxListItems+1)
	for i := range tags {
		tags[i] = map[string]any{"id": fmt.Sprintf("t%d", i), "name": "n", "color": "#000"}
	}
	body, _ := json.Marshal(map[string]any{"data": map[string]any{"id": "k1", "tags": tags}})
	projected, err := op.Project(body)
	require.NoError(t, err)
	assert.True(t, projected.Truncated)
	got := projected.Data.(map[string]any)["tags"].([]any)
	require.Len(t, got, defaultMaxListItems)
	assert.Equal(t, map[string]any{"tag_id": "t0", "name": "n"}, got[0])
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

func TestCatalogForFiltersByRoleAndLevel(t *testing.T) {
	id := Param{Name: "knowledge_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	get := readOp("knowledge.get", "/api/v1/knowledge/:id", id)
	audit := readOp("knowledge.audit", "/api/v1/knowledge/:id/audit", id)
	audit.Role = types.TenantRoleAdmin
	write := func(opID, route string, target string) Operation {
		return Operation{
			ID: opID, Summary: "s", Risk: RiskWrite, Method: http.MethodPost, Route: route, Params: []Param{id},
			Target: &Target{Operation: target, Label: "title"},
			Result: Result{Root: "data", Fields: []Field{{From: "title"}}},
		}
	}
	catalog, err := NewCatalog(
		get, audit,
		write("knowledge.reparse", "/api/v1/knowledge/:id/reparse", "knowledge.get"),
		write("knowledge.seal", "/api/v1/knowledge/:id/seal", "knowledge.audit"),
	)
	require.NoError(t, err)
	ids := func(c *Catalog) []string {
		var out []string
		for _, op := range c.All() {
			out = append(out, op.ID)
		}
		return out
	}

	assert.Equal(t, []string{"knowledge.get"},
		ids(catalog.For(Access{Role: types.TenantRoleViewer, EnforceRole: true})))
	assert.Equal(t, []string{"knowledge.get", "knowledge.reparse"},
		ids(catalog.For(Access{Role: types.TenantRoleContributor, EnforceRole: true})),
		"a mutation is dropped with the target it could not show")
	assert.Equal(t, ids(catalog),
		ids(catalog.For(Access{Role: types.TenantRoleViewer})), "roles filter only when RBAC is enforced")
	assert.Equal(t, []string{"knowledge.audit", "knowledge.get"},
		ids(catalog.For(Access{Role: types.TenantRoleOwner, EnforceRole: true, Level: types.AssistantAccessReadOnly})))
}

func TestProbeRunsWithoutApprovalAtEveryLevel(t *testing.T) {
	kb := Param{Name: "knowledge_base_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "d"}
	probe := Operation{
		ID: "knowledge_base.search", Summary: "s", Risk: RiskProbe, Method: http.MethodPost,
		Route: "/api/v1/knowledge-bases/:id/hybrid-search", Params: []Param{kb},
		Result: Result{Root: "data", List: true, Fields: []Field{{From: "content"}}},
	}
	catalog, err := NewCatalog(probe)
	require.NoError(t, err)
	op, _ := catalog.Get(probe.ID)
	assert.Equal(t, types.TenantRoleViewer, op.Role, "a probe needs only a viewer by default")
	assert.False(t, op.Risk.Mutates())
	for _, level := range []types.AssistantAccessLevel{
		types.AssistantAccessReadOnly, types.AssistantAccessConfirm, types.AssistantAccessAutoWrite,
	} {
		access := Access{Level: level, Role: types.TenantRoleViewer, EnforceRole: true}
		_, withheld := catalog.Withheld(access, op)
		assert.False(t, withheld, level)
		assert.False(t, access.NeedsApproval(op), level)
	}
}

func TestBuiltinProbeOperations(t *testing.T) {
	catalog := Builtin()
	project := func(op Operation, body string) string {
		t.Helper()
		projected, err := op.Project([]byte(body))
		require.NoError(t, err)
		out, err := json.Marshal(projected)
		require.NoError(t, err)
		return string(out)
	}

	search, ok := catalog.Get("knowledge_base.search")
	require.True(t, ok)
	assert.Equal(t, RiskProbe, search.Risk)
	assert.Equal(t, http.MethodPost, search.Method, "the legacy GET route is not compiled")
	assert.Equal(t, types.TenantRoleViewer, search.Role)
	var names []string
	for _, p := range search.Params {
		names = append(names, p.Name)
	}
	assert.ElementsMatch(t, []string{
		"knowledge_base_id", "query_text", "vector_threshold", "keyword_threshold", "match_count",
		"disable_keywords_match", "disable_vector_match", "knowledge_ids",
	}, names)
	query, _ := search.Param("query_text")
	assert.True(t, query.Required)
	threshold, _ := search.Param("vector_threshold")
	assert.Equal(t, TypeNumber, threshold.Type)
	count, _ := search.Param("match_count")
	assert.Equal(t, 30, count.Maximum)
	assert.JSONEq(t, `{"data":[{"chunk_id":"c1","content":"hello","knowledge_id":"k1","chunk_index":2,
		"knowledge_title":"Q3","score":0.8,"match_type":1,"chunk_type":"text"}]}`,
		project(search, `{"success":true,"data":[{"id":"c1","content":"hello","knowledge_id":"k1",
		"chunk_index":2,"knowledge_title":"Q3","score":0.8,"match_type":1,"chunk_type":"text",
		"metadata":{"k":"v"},"image_info":"local://secret","knowledge_filename":"q3.pdf"}]}`))

	test, ok := catalog.Get("mcp_service.test")
	require.True(t, ok)
	assert.Equal(t, RiskProbe, test.Risk)
	assert.Equal(t, types.TenantRoleAdmin, test.Role)
	assert.JSONEq(t, `{"data":{"success":false,"message":"Connection failed: 401","oauth_required":true}}`,
		project(test, `{"success":true,"data":{"success":false,"message":"Connection failed: 401",
		"oauth_required":true,"description":"server","tools":[{"name":"t"}]}}`))

	tools, ok := catalog.Get("mcp_service.list_tools")
	require.True(t, ok)
	assert.Equal(t, RiskRead, tools.Risk)
	assert.JSONEq(t, `{"data":[{"name":"maps_weather","description":"Weather by city","require_approval":true}]}`,
		project(tools, `{"success":true,"data":[{"name":"maps_weather","description":"Weather by city",
		"inputSchema":{"type":"object"},"require_approval":true}]}`))
}

func TestBuildNumberParams(t *testing.T) {
	threshold := Param{Name: "vector_threshold", In: InBody, Type: TypeNumber, Description: "d"}
	op := Operation{
		ID: "x.check", Summary: "s", Risk: RiskProbe, Method: http.MethodPost,
		Route: "/api/v1/knowledge-bases", Params: []Param{threshold},
		Result: Result{Fields: []Field{{From: "title"}}},
	}
	_, err := NewCatalog(op)
	require.NoError(t, err)

	call, err := op.Build(map[string]json.RawMessage{"vector_threshold": json.RawMessage(`0.35`)})
	require.NoError(t, err)
	assert.JSONEq(t, `{"vector_threshold":0.35}`, string(call.Body))
	for _, bad := range []string{`"0.35"`, `true`, `[1]`} {
		_, err := op.Build(map[string]json.RawMessage{"vector_threshold": json.RawMessage(bad)})
		assert.Error(t, err, bad)
	}
}

func TestBuiltinSkillList(t *testing.T) {
	op, ok := Builtin().Get("skill.list")
	require.True(t, ok)
	assert.Equal(t, types.TenantRoleViewer, op.Role)
	require.Len(t, op.Params, 1)
	param, ok := op.Param("sandbox_config_id")
	require.True(t, ok)
	assert.Equal(t, InQuery, param.In)
	assert.True(t, param.Required)
	assert.True(t, param.AllowEmpty)
	_, err := op.Build(nil)
	require.Error(t, err)
	for _, id := range []string{`"cfg-1"`, `"host"`, `""`} {
		_, err = op.Build(map[string]json.RawMessage{"sandbox_config_id": json.RawMessage(id)})
		require.NoError(t, err)
	}
	for _, hidden := range []string{"agent_id", "agent_source_tenant_id"} {
		_, err = op.Build(map[string]json.RawMessage{
			"sandbox_config_id": json.RawMessage(`"cfg-1"`),
			hidden:              json.RawMessage(`"secret"`),
		})
		require.Error(t, err)
	}
	assert.Equal(t, []Field{{From: "description", As: "description"}, {From: "name", As: "name"}}, op.Result.Fields)
	assert.Equal(t, []Field{{From: "skills_available", As: "skills_available"}}, op.Result.Meta)
	for _, tc := range []struct{ body, want string }{
		{
			`{"success":true,"data":[{"id":"row-1","name":"ppt-generator","description":"Make slides",
			"env_values":{"TOKEN":"secret"},"source":"https://private","headers":{"Authorization":"secret"}}],
			"skills_available":true,"tenant_id":1}`,
			`{"data":[{"name":"ppt-generator","description":"Make slides"}],"meta":{"skills_available":true}}`,
		},
		{`{"success":true,"data":[],"skills_available":false}`, `{"data":[],"meta":{"skills_available":false}}`},
	} {
		projected, err := op.Project([]byte(tc.body))
		require.NoError(t, err)
		out, err := json.Marshal(projected)
		require.NoError(t, err)
		assert.JSONEq(t, tc.want, string(out))
	}
	_, ok = Builtin().Get("skill.get")
	assert.False(t, ok)
}

func TestBuiltinAgentSandboxProjection(t *testing.T) {
	for _, id := range []string{"agent.get", "agent.list"} {
		op, ok := Builtin().Get(id)
		require.True(t, ok)
		data := `{"id":"a1","config":{"sandbox_config_id":"cfg-1","api_key":"secret",
			"headers":{"Authorization":"secret"},"extensions":{"url":"https://private"}}}`
		want := `{"agent_id":"a1","sandbox_config_id":"cfg-1"}`
		if id == "agent.list" {
			data = "[" + data + "]"
			want = `[{"agent_id":"a1"}]`
		}
		projected, err := op.Project([]byte(`{"data":` + data + `}`))
		require.NoError(t, err)
		out, err := json.Marshal(projected)
		require.NoError(t, err)
		assert.JSONEq(t, `{"data":`+want+`}`, string(out))
	}
}

func TestBuiltinAgentSkillParams(t *testing.T) {
	op, ok := Builtin().Get("agent.update")
	require.True(t, ok)
	mode, ok := op.Param("agent_mode")
	require.True(t, ok)
	assert.Equal(t, []string{"quick-answer", "smart-reasoning"}, mode.Enum)
	skills, ok := op.Param("skills_selection_mode")
	require.True(t, ok)
	assert.Equal(t, []string{"all", "selected", "none"}, skills.Enum)
	names, ok := op.Param("selected_skills")
	require.True(t, ok)
	assert.Equal(t, TypeStringList, names.Type)
	assert.Equal(t, 50, names.MaxItems)
	for _, value := range []any{"not-a-list", make([]string, 51)} {
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		_, err = op.Build(map[string]json.RawMessage{"agent_id": json.RawMessage(`"a1"`), "selected_skills": raw})
		require.Error(t, err)
	}
}

func TestBuiltinKnowledgeBaseLifecycle(t *testing.T) {
	catalog := Builtin()
	create, ok := catalog.Get("knowledge_base.create")
	require.True(t, ok)
	assert.Equal(t, "name", create.LabelParam)
	name, ok := create.Param("name")
	require.True(t, ok)
	assert.True(t, name.Required)
	_, ok = create.Param("knowledge_base_id")
	assert.False(t, ok)
	_, ok = create.Param("indexing_strategy")
	assert.False(t, ok)
	vector, ok := create.Param("vector_enabled")
	require.True(t, ok)
	assert.Equal(t, TypeBoolean, vector.Type)
	_, ok = create.Param("embedding_model_id")
	assert.True(t, ok)
	_, ok = create.Param("chunk_size")
	assert.True(t, ok)
	_, ok = create.Param("vector_store_id")
	assert.True(t, ok)
	kind, ok := create.Param("type")
	require.True(t, ok)
	assert.Equal(t, []string{"document", "faq", "wiki"}, kind.Enum)

	update, ok := catalog.Get("knowledge_base.update")
	require.True(t, ok)
	assert.Equal(t, http.MethodPatch, update.Method)
	_, ok = update.Param("name")
	assert.True(t, ok)
	_, ok = update.Param("description")
	assert.True(t, ok)
	_, ok = update.Param("vector_enabled")
	assert.True(t, ok)
	_, ok = update.Param("embedding_model_id")
	assert.True(t, ok)
	_, ok = update.Param("chunk_size")
	assert.True(t, ok)
	_, ok = update.Param("vector_store_id")
	assert.False(t, ok, "the vector store is chosen when the knowledge base is created")

	del, ok := catalog.Get("knowledge_base.delete")
	require.True(t, ok)
	assert.Equal(t, RiskDestructive, del.Risk)

	setTags, ok := catalog.Get("knowledge.set_tags")
	require.True(t, ok)
	docs, ok := setTags.Param("knowledge_ids")
	require.True(t, ok)
	assert.Equal(t, TypeStringList, docs.Type)
	assert.Equal(t, 20, docs.MaxItems)
	tags, ok := setTags.Param("tag_ids")
	require.True(t, ok)
	assert.Equal(t, 50, tags.MaxItems)
	require.NotNil(t, setTags.Target)
	require.NotNil(t, setTags.Target.Items)
	assert.Equal(t, "knowledge_ids", setTags.Target.Items.Param)

	manual, ok := catalog.Get("knowledge.update_manual")
	require.True(t, ok)
	content, ok := manual.Param("content")
	require.True(t, ok)
	assert.True(t, content.Required)
}
