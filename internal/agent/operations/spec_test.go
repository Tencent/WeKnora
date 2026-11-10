package operations

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSpec is a trimmed swag document: an envelope, a domain type with
// tagged and untagged fields, and three annotated operations.
const testSpec = `{
  "basePath": "/api/v1",
  "definitions": {
    "handler.PageResponse": {"type": "object", "properties": {
      "success": {"type": "boolean"},
      "data": {},
      "total": {"type": "integer", "x-agent": true}
    }},
    "handler.DataResponse": {"type": "object", "properties": {
      "success": {"type": "boolean"}, "data": {}
    }},
    "types.Status": {"type": "string", "enum": ["ok", "failed"]},
    "types.Strategy": {"type": "object", "properties": {
      "vector_enabled": {"type": "boolean", "x-agent": true},
      "secret_mode": {"type": "string"}
    }},
    "types.Doc": {"type": "object", "properties": {
      "id": {"type": "string", "x-agent": "knowledge_id"},
      "title": {"type": "string", "x-agent": true},
      "status": {"allOf": [{"$ref": "#/definitions/types.Status"}], "x-agent": true},
      "strategy": {"allOf": [{"$ref": "#/definitions/types.Strategy"}], "x-agent": true},
      "details": {"type": "object", "x-agent": true, "x-agent-keys": "via|trigger"},
      "tags": {"type": "array", "items": {"type": "string"}, "x-agent": true},
      "file_path": {"type": "string"},
      "tenant_id": {"type": "integer"}
    }}
  },
  "paths": {
    "/knowledge-bases/{id}/docs": {"get": {
      "summary": "List documents",
      "description": "Human-facing text.",
      "x-agent": {"id": "doc.list", "risk": "read"},
      "x-agent-hint": "Model-facing text.",
      "parameters": [
        {"name": "id", "in": "path", "type": "string", "required": true, "description": "KB",
         "x-agent": "knowledge_base_id"},
        {"name": "status", "in": "query", "type": "string", "enum": ["ok", "failed"], "description": "S",
         "x-agent": true},
        {"name": "page_size", "in": "query", "type": "integer", "minimum": 1, "maximum": 1000,
         "description": "N", "x-agent": true, "x-agent-maximum": "50"},
        {"name": "agent_id", "in": "query", "type": "string", "description": "not for the model"}
      ],
      "responses": {"200": {"schema": {"allOf": [
        {"$ref": "#/definitions/handler.PageResponse"},
        {"type": "object", "properties": {"data": {"type": "array", "items": {"$ref": "#/definitions/types.Doc"}}}}
      ]}}}
    }},
    "/knowledge/{id}": {
      "get": {
        "summary": "Get a document",
        "x-agent": {"id": "doc.get", "risk": "read"},
        "parameters": [{"name": "id", "in": "path", "type": "string", "required": true, "description": "D",
          "x-agent": "knowledge_id"}],
        "responses": {"200": {"schema": {"allOf": [
          {"$ref": "#/definitions/handler.DataResponse"},
          {"type": "object", "properties": {"data": {"$ref": "#/definitions/types.Doc"}}}
        ]}}}
      },
      "delete": {
        "summary": "Delete a document",
        "x-agent": {"id": "doc.delete", "risk": "destructive", "target": "doc.get", "label": "title"},
        "parameters": [{"name": "id", "in": "path", "type": "string", "required": true, "description": "D",
          "x-agent": "knowledge_id"}],
        "responses": {"200": {"schema": {"type": "object", "properties": {
          "message": {"type": "string", "x-agent": true}
        }}}}
      }
    },
    "/knowledge/{id}/raw": {"get": {
      "summary": "Not for agents",
      "parameters": [{"name": "id", "in": "path", "type": "string", "required": true}],
      "responses": {"200": {"schema": {"type": "string"}}}
    }}
  }
}`

func compileTestSpec(t *testing.T) *Catalog {
	t.Helper()
	ops, err := FromSpec([]byte(testSpec))
	require.NoError(t, err)
	catalog, err := NewCatalog(ops...)
	require.NoError(t, err)
	return catalog
}

func TestFromSpecCompilesAnnotatedOperations(t *testing.T) {
	catalog := compileTestSpec(t)
	require.Len(t, catalog.All(), 3, "operations without x-agent stay out")

	list, ok := catalog.Get("doc.list")
	require.True(t, ok)
	assert.Equal(t, http.MethodGet, list.Method)
	assert.Equal(t, "/api/v1/knowledge-bases/:id/docs", list.Route)
	assert.Equal(t, "List documents", list.Summary)
	assert.Equal(t, "Model-facing text.", list.Description, "the hint replaces the human description")

	require.Len(t, list.Params, 3, "agent_id is not exposed")
	kb, _ := list.Param("knowledge_base_id")
	assert.Equal(t, Param{
		Name: "knowledge_base_id", In: InPath, Key: "id", Type: TypeString, Required: true, Description: "KB",
	}, kb)
	status, _ := list.Param("status")
	assert.Equal(t, []string{"ok", "failed"}, status.Enum)
	size, _ := list.Param("page_size")
	assert.Equal(t, 1, size.Minimum)
	assert.Equal(t, 50, size.Maximum, "x-agent-maximum caps the API bound")

	assert.Equal(t, "data", list.Result.Root)
	assert.True(t, list.Result.List)
	assert.ElementsMatch(t, []Field{
		{From: "details.trigger", As: "trigger"},
		{From: "details.via", As: "via"},
		{From: "id", As: "knowledge_id"},
		{From: "status", As: "status"},
		{From: "strategy.vector_enabled", As: "vector_enabled"},
		{From: "tags", As: "tags"},
		{From: "title", As: "title"},
	}, list.Result.Fields)
	assert.Equal(t, []Field{{From: "total", As: "total"}}, list.Result.Meta)

	get, _ := catalog.Get("doc.get")
	assert.False(t, get.Result.List)
	assert.Empty(t, get.Result.Meta, "the plain envelope marks nothing")

	del, _ := catalog.Get("doc.delete")
	assert.Equal(t, &Target{Operation: "doc.get", Label: "title"}, del.Target)
	assert.Equal(t, "", del.Result.Root, "no data envelope projects the body")
}

func TestFromSpecProjectsOnlyTaggedFields(t *testing.T) {
	list, _ := compileTestSpec(t).Get("doc.list")
	body := `{"success":true,"total":1,"data":[{"id":"k1","title":"A","status":"failed",
		"strategy":{"vector_enabled":true,"secret_mode":"x"},"details":{"via":"agent","token":"t"},
		"file_path":"s3://secret","tenant_id":9}]}`
	projected, err := list.Project([]byte(body))
	require.NoError(t, err)
	out, _ := json.Marshal(projected)
	assert.JSONEq(t, `{"data":[{"knowledge_id":"k1","title":"A","status":"failed",
		"vector_enabled":true,"via":"agent"}],"meta":{"total":1}}`, string(out))
}

func TestFromSpecRejectsBadAnnotations(t *testing.T) {
	cases := map[string]struct{ from, to string }{
		"unknown x-agent key": {
			`{"id": "doc.get", "risk": "read"}`, `{"id": "doc.get", "risk": "read", "rsik": "write"}`,
		},
		"target without label": {
			`"target": "doc.get", "label": "title"`, `"target": "doc.get"`,
		},
		"required param not exposed": {
			`"description": "D",
          "x-agent": "knowledge_id"}],
        "responses": {"200": {"schema": {"allOf"`,
			`"description": "D"}],
        "responses": {"200": {"schema": {"allOf"`,
		},
		"agent maximum above the API maximum": {`"x-agent-maximum": "50"`, `"x-agent-maximum": "5000"`},
		"free-form object without keys":       {`"x-agent": true, "x-agent-keys": "via|trigger"`, `"x-agent": true`},
		"renamed object": {
			`"strategy": {"allOf": [{"$ref": "#/definitions/types.Strategy"}], "x-agent": true}`,
			`"strategy": {"allOf": [{"$ref": "#/definitions/types.Strategy"}], "x-agent": "strategy_flags"}`,
		},
		"list of objects without exposed fields": {
			`"tags": {"type": "array", "items": {"type": "string"}, "x-agent": true}`,
			`"tags": {"type": "array", "items": {"type": "object", ` +
				`"properties": {"secret": {"type": "string"}}}, "x-agent": true}`,
		},
		"length cap on a list of objects": {
			`"tags": {"type": "array", "items": {"type": "string"}, "x-agent": true}`,
			`"tags": {"type": "array", "items": {"$ref": "#/definitions/types.Strategy"}, ` +
				`"x-agent": true, "x-agent-max-runes": "10"}`,
		},
		"nothing exposed": {
			`"message": {"type": "string", "x-agent": true}`, `"message": {"type": "string"}`,
		},
		"undefined schema": {`"$ref": "#/definitions/types.Status"`, `"$ref": "#/definitions/types.Missing"`},
		"catch-all before the last segment": {
			`{"id": "doc.list", "risk": "read"}`, `{"id": "doc.list", "risk": "read", "catch_all": "id"}`,
		},
		"items without a target": {
			`{"id": "doc.get", "risk": "read"}`,
			`{"id": "doc.get", "risk": "read", "items": {"param": "a", "target": "b", "as": "c", "label": "d"}}`,
		},
		"items missing a key": {
			`"target": "doc.get", "label": "title"`,
			`"target": "doc.get", "label": "title", "items": {"param": "knowledge_ids", "target": "doc.get"}`,
		},
		"catch-all naming no segment": {
			`{"id": "doc.get", "risk": "read"}`, `{"id": "doc.get", "risk": "read", "catch_all": "slug"}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, 1, strings.Count(testSpec, tc.from), "fixture edit must be unambiguous")
			_, err := FromSpec([]byte(strings.Replace(testSpec, tc.from, tc.to, 1)))
			assert.Error(t, err)
		})
	}
}

func TestFromSpecMethodPicksOneOfTheHandlersRoutes(t *testing.T) {
	route := func(method string) string {
		return `"` + method + `": {
      "summary": "Search",
      "x-agent": {"id": "kb.search", "risk": "probe", "method": "post"},
      "parameters": [
        {"name": "id", "in": "path", "type": "string", "required": true, "description": "KB",
         "x-agent": "knowledge_base_id"},
        {"name": "request", "in": "body", "required": true, "schema": {"type": "object", "properties": {
          "query_text": {"type": "string", "description": "Q", "x-agent": true, "x-agent-required": true},
          "vector_threshold": {"type": "number", "description": "T", "x-agent": true}
        }}}
      ],
      "responses": {"200": {"schema": {"type": "object", "properties": {
        "data": {"type": "array", "items": {"type": "object", "properties": {
          "content": {"type": "string", "x-agent": true}
        }}}
      }}}}
    }`
	}
	spec := `{"basePath": "/api/v1", "paths": {"/knowledge-bases/{id}/hybrid-search": {` +
		route("post") + `, ` + route("get") + `}}}`

	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	require.Len(t, ops, 1, "the GET route that shares the doc stays out")
	assert.Equal(t, http.MethodPost, ops[0].Method)
	threshold, ok := ops[0].Param("vector_threshold")
	require.True(t, ok)
	assert.Equal(t, TypeNumber, threshold.Type)
	_, err = NewCatalog(ops...)
	require.NoError(t, err)

	_, err = FromSpec([]byte(strings.ReplaceAll(spec, `"method": "post"`, `"method": "fetch"`)))
	assert.ErrorContains(t, err, "unknown method")
}

func TestFromSpecAgentRequired(t *testing.T) {
	spec := strings.Replace(testSpec,
		`"description": "S",
         "x-agent": true}`,
		`"description": "S",
         "x-agent": true, "x-agent-required": true}`, 1)
	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	for _, op := range ops {
		if op.ID == "doc.list" {
			status, _ := op.Param("status")
			assert.True(t, status.Required, "x-agent-required makes an optional param mandatory for the model")
		}
	}
}

func TestFromSpecSkipsResponseOnlyBodyFields(t *testing.T) {
	spec := `{
      "paths": {
        "/api/v1/knowledge-bases": {
          "post": {
            "summary": "Create",
            "parameters": [{"name": "body", "in": "body", "required": true, "schema": {"$ref": "#/definitions/KB"}}],
            "responses": {"201": {"schema": {"$ref": "#/definitions/KB"}}},
            "x-agent": {"id": "knowledge_base.create", "risk": "write", "label_param": "name"},
            "x-agent-hint": "Creates one."
          }
        }
      },
      "definitions": {
        "KB": {
          "type": "object",
          "required": ["name"],
          "properties": {
            "name": {"type": "string", "description": "Name", "x-agent": true},
            "id": {"type": "string", "description": "ID", "x-agent": "knowledge_base_id", "x-agent-response": true},
            "indexing_strategy": {
              "type": "object",
              "description": "Indexing",
              "x-agent": true,
              "x-agent-response": true,
              "properties": {"vector": {"type": "boolean", "description": "Vector", "x-agent": true}}
            }
          }
        }
      }
    }`
	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.ElementsMatch(t, []string{"name", "vector"}, paramNames(ops[0]))
	_, ok := ops[0].Param("knowledge_base_id")
	assert.False(t, ok, "a response-only identifier is not a create parameter")
	_, ok = ops[0].Param("indexing_strategy")
	assert.False(t, ok, "the indexing object itself is not a parameter")
	vector, ok := ops[0].Param("vector")
	require.True(t, ok)
	assert.Equal(t, "indexing_strategy.vector", vector.Key)
	call, err := ops[0].Build(map[string]json.RawMessage{
		"name":   json.RawMessage(`"产品文档"`),
		"vector": json.RawMessage(`true`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"产品文档","indexing_strategy":{"vector":true}}`, string(call.Body))
	id, ok := fieldByFrom(ops[0].Result.Fields, "id")
	require.True(t, ok)
	assert.Equal(t, "knowledge_base_id", id.As)
}

func paramNames(op Operation) []string {
	names := make([]string, 0, len(op.Params))
	for _, p := range op.Params {
		names = append(names, p.Name)
	}
	return names
}

func fieldByFrom(fields []Field, from string) (Field, bool) {
	for _, field := range fields {
		if field.From == from {
			return field, true
		}
	}
	return Field{}, false
}

func TestFromSpecMarksCatchAllSegment(t *testing.T) {
	spec := strings.Replace(testSpec,
		`{"id": "doc.get", "risk": "read"}`, `{"id": "doc.get", "risk": "read", "catch_all": "id"}`, 1)
	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	catalog, err := NewCatalog(ops...)
	require.NoError(t, err)

	get, _ := catalog.Get("doc.get")
	assert.Equal(t, "/api/v1/knowledge/*id", get.Route)
	call, err := get.Build(map[string]json.RawMessage{"knowledge_id": json.RawMessage(`"a/b"`)})
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/knowledge/a/b", call.Path, "a catch-all keeps its slashes")

	del, _ := catalog.Get("doc.delete")
	assert.Equal(t, "/api/v1/knowledge/:id", del.Route, "the mark is per operation")
}

func TestFromSpecKeepsCatalogSafetyRules(t *testing.T) {
	forbidden := strings.Replace(testSpec, `"/knowledge/{id}": {`, `"/api-keys/{id}": {`, 1)
	ops, err := FromSpec([]byte(forbidden))
	require.NoError(t, err)
	_, err = NewCatalog(ops...)
	assert.ErrorContains(t, err, "outside the areas an agent may reach")

	readAsDelete := strings.Replace(testSpec, `"risk": "destructive"`, `"risk": "read"`, 1)
	ops, err = FromSpec([]byte(readAsDelete))
	require.NoError(t, err)
	_, err = NewCatalog(ops...)
	assert.Error(t, err)
}

var agentAnnotation = regexp.MustCompile(`^\s*//\s*@x-agent\s+(\{.*\})\s*$`)

// TestBuiltinMatchesHandlerAnnotations catches an annotation edited without
// regenerating the Swagger document (make docs).
func TestBuiltinMatchesHandlerAnnotations(t *testing.T) {
	declared := map[string]agentOperation{}
	root := filepath.Join("..", "..", "handler")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(src), "\n") {
			m := agentAnnotation.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			var meta agentOperation
			require.NoError(t, json.Unmarshal([]byte(m[1]), &meta), path)
			declared[meta.ID] = meta
		}
		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, declared)

	built := map[string]agentOperation{}
	methods := map[string]string{}
	for _, op := range Builtin().All() {
		methods[op.ID] = op.Method
		meta := agentOperation{
			ID:            op.ID,
			Risk:          op.Risk,
			LabelParam:    op.LabelParam,
			AlwaysConfirm: op.AlwaysConfirm,
			SystemAdmin:   op.SystemAdmin,
		}
		if op.Target != nil {
			meta.Target, meta.Label = op.Target.Operation, op.Target.Label
			meta.RequireCompleteTarget = op.Target.RequireComplete
			if it := op.Target.Items; it != nil {
				meta.Items = &agentItems{Param: it.Param, Target: it.Operation, As: it.As, Label: it.Label}
			}
		}
		if i := strings.LastIndex(op.Route, "/*"); i >= 0 {
			meta.CatchAll = op.Route[i+2:]
		}
		if op.Role != defaultRole(op.Risk) {
			meta.Role = op.Role
		}
		built[op.ID] = meta
	}
	for id, meta := range declared {
		if meta.Method != "" {
			assert.True(t, strings.EqualFold(meta.Method, methods[id]),
				"%s names method %s but was built from %s", id, meta.Method, methods[id])
		}
		meta.Root, meta.MaxItems, meta.Method = nil, 0, ""
		declared[id] = meta
	}
	assert.Equal(t, declared, built, "docs/swagger.json is stale; run make docs")
}

func TestFromSpecDropsDetailFieldsFromLists(t *testing.T) {
	const spec = `{
	  "basePath": "/api/v1",
	  "definitions": {
	    "Agent": {"type": "object", "properties": {
	      "id": {"type": "string", "x-agent": "agent_id"},
	      "config": {"type": "object", "x-agent": true, "properties": {
	        "mode": {"type": "string", "x-agent": true},
	        "prompt": {"type": "string", "x-agent": true, "x-agent-detail": true}
	      }}
	    }}
	  },
	  "paths": {
	    "/agents": {"get": {"summary": "list", "x-agent": {"id": "agent.list", "risk": "read"},
	      "responses": {"200": {"schema": {"type": "object", "properties": {
	        "data": {"type": "array", "items": {"$ref": "#/definitions/Agent"}}}}}}}},
	    "/agents/{id}": {"get": {"summary": "get", "x-agent": {"id": "agent.get", "risk": "read"},
	      "parameters": [{"name": "id", "in": "path", "type": "string", "required": true,
	        "description": "d", "x-agent": "agent_id"}],
	      "responses": {"200": {"schema": {"type": "object", "properties": {
	        "data": {"$ref": "#/definitions/Agent"}}}}}}}
	  }
	}`
	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	fields := map[string][]string{}
	for _, op := range ops {
		for _, f := range op.Result.Fields {
			fields[op.ID] = append(fields[op.ID], f.outputKey())
		}
	}
	assert.ElementsMatch(t, []string{"agent_id", "mode"}, fields["agent.list"])
	assert.ElementsMatch(t, []string{"agent_id", "mode", "prompt"}, fields["agent.get"])

	const exposed = `"prompt": {"type": "string", "x-agent": true,`
	unexposed := strings.Replace(spec, exposed, `"prompt": {"type": "string",`, 1)
	_, err = FromSpec([]byte(unexposed))
	assert.ErrorContains(t, err, "x-agent-detail needs x-agent")
}

func TestFromSpecProjectsNestedListMeta(t *testing.T) {
	const spec = `{
	  "basePath": "/api/v1",
	  "definitions": {
	    "Page": {"type": "object", "properties": {
	      "total": {"type": "integer", "x-agent": true},
	      "data": {"type": "array", "items": {"type": "object", "properties": {
	        "name": {"type": "string", "x-agent": true}
	      }}}
	    }}
	  },
	  "paths": {
	    "/knowledge-bases/{id}/tags": {"get": {
	      "summary": "Tags",
	      "x-agent": {"id": "tag.list", "risk": "read", "root": "data.data"},
	      "parameters": [
	        {"name": "id", "in": "path", "type": "string", "required": true, "description": "KB",
	         "x-agent": "knowledge_base_id"}
	      ],
	      "responses": {"200": {"schema": {"type": "object", "properties": {
	        "success": {"type": "boolean"},
	        "data": {"$ref": "#/definitions/Page"}
	      }}}}
	    }}
	  }
	}`
	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	catalog, err := NewCatalog(ops...)
	require.NoError(t, err)
	list, _ := catalog.Get("tag.list")
	assert.Equal(t, []Field{{From: "data.total", As: "total"}}, list.Result.Meta)

	projected, err := list.Project([]byte(`{"success":true,"data":{"total":3,"page":1,"data":[{"name":"a"}]}}`))
	require.NoError(t, err)
	out, _ := json.Marshal(projected)
	assert.JSONEq(t, `{"data":[{"name":"a"}],"meta":{"total":3}}`, string(out))
}

func TestFromSpecProjectsListsOfObjects(t *testing.T) {
	const spec = `{
	  "basePath": "/api/v1",
	  "definitions": {
	    "Tag": {"type": "object", "properties": {
	      "id": {"type": "string", "x-agent": "tag_id"},
	      "color": {"type": "string"},
	      "note": {"type": "string", "x-agent": true, "x-agent-detail": true}
	    }},
	    "Doc": {"type": "object", "properties": {
	      "id": {"type": "string", "x-agent": "knowledge_id"},
	      "tags": {"type": "array", "x-agent": true, "items": {"$ref": "#/definitions/Tag"}}
	    }}
	  },
	  "paths": {
	    "/knowledge": {"get": {"summary": "list", "x-agent": {"id": "doc.list", "risk": "read"},
	      "responses": {"200": {"schema": {"type": "object", "properties": {
	        "data": {"type": "array", "items": {"$ref": "#/definitions/Doc"}}}}}}}},
	    "/knowledge/{id}": {"get": {"summary": "get", "x-agent": {"id": "doc.get", "risk": "read"},
	      "parameters": [{"name": "id", "in": "path", "type": "string", "required": true,
	        "description": "d", "x-agent": "knowledge_id"}],
	      "responses": {"200": {"schema": {"type": "object", "properties": {
	        "data": {"$ref": "#/definitions/Doc"}}}}}}}
	  }
	}`
	ops, err := FromSpec([]byte(spec))
	require.NoError(t, err)
	catalog, err := NewCatalog(ops...)
	require.NoError(t, err)
	list, _ := catalog.Get("doc.list")
	tags, ok := fieldByFrom(list.Result.Fields, "tags")
	require.True(t, ok)
	assert.Equal(t, []Field{{From: "id", As: "tag_id"}}, tags.Items, "lists drop detail fields of items too")
	get, _ := catalog.Get("doc.get")
	tags, _ = fieldByFrom(get.Result.Fields, "tags")
	assert.Equal(t, []Field{{From: "id", As: "tag_id"}, {From: "note", As: "note"}}, tags.Items)

	nested := strings.Replace(spec, `"color": {"type": "string"},`,
		`"color": {"type": "array", "x-agent": true, "items": {"type": "object", "properties": {
		  "hex": {"type": "string", "x-agent": true}}}},`, 1)
	_, err = FromSpec([]byte(nested))
	assert.ErrorContains(t, err, "cannot hold lists of objects")

	bare := strings.Replace(spec, `"id": {"type": "string", "x-agent": "tag_id"},`, `"id": {"type": "string"},`, 1)
	bare = strings.Replace(bare,
		`"note": {"type": "string", "x-agent": true, "x-agent-detail": true}`, `"note": {"type": "string"}`, 1)
	_, err = FromSpec([]byte(bare))
	assert.ErrorContains(t, err, "list items expose no fields")
}

func TestBuildAllowsDeclaredLongStrings(t *testing.T) {
	op := Operation{
		Method: http.MethodPost,
		Params: []Param{{
			Name: "content", In: InBody, Key: "content", Type: TypeString,
			Description: "body", MaxBytes: maxAgentStringBytes,
		}},
	}
	body := strings.Repeat("文", 5000)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	call, err := op.Build(map[string]json.RawMessage{"content": raw})
	require.NoError(t, err)
	assert.Greater(t, len(call.Body), maxStringParamBytes)

	op.Params[0].MaxBytes = 0
	_, err = op.Build(map[string]json.RawMessage{"content": raw})
	assert.Error(t, err)
}

func TestBuiltinListAndDocumentLimits(t *testing.T) {
	catalog := Builtin()

	manual, ok := catalog.Get("knowledge.create_manual")
	require.True(t, ok)
	content, ok := manual.Param("content")
	require.True(t, ok)
	assert.Equal(t, maxAgentStringBytes, content.MaxBytes)

	list, ok := catalog.Get("knowledge_base.list")
	require.True(t, ok)
	assert.Equal(t, 200, list.Result.MaxItems)
	shared, _ := catalog.Get("knowledge_base.list_shared")
	assert.Equal(t, 200, shared.Result.MaxItems)
	folders, _ := catalog.Get("knowledge.list_folders")
	assert.Equal(t, 200, folders.Result.MaxItems)
	assert.Contains(t, folders.Result.Meta, Field{From: "data.root_document_count", As: "root_document_count"})
	wikiFolders, _ := catalog.Get("wiki.list_folders")
	assert.Equal(t, 200, wikiFolders.Result.MaxItems)

	tags, ok := catalog.Get("knowledge_base.list_tags")
	require.True(t, ok)
	assert.Equal(t, 50, tags.Result.MaxItems)
	pageSize, ok := tags.Param("page_size")
	require.True(t, ok)
	assert.Equal(t, 50, pageSize.Maximum)
	assert.Contains(t, tags.Result.Meta, Field{From: "data.total", As: "total"})

	revisions, ok := catalog.Get("wiki.list_revisions")
	require.True(t, ok)
	version, ok := revisions.Param("content_version")
	require.True(t, ok)
	assert.Equal(t, 1, version.Minimum)
	revert, ok := catalog.Get("wiki.revert")
	require.True(t, ok)
	revertVersion, ok := revert.Param("version")
	require.True(t, ok)
	assert.Equal(t, 1, revertVersion.Minimum)
	var contentField Field
	for _, f := range revisions.Result.Fields {
		if f.As == "content" {
			contentField = f
		}
	}
	assert.Equal(t, Field{From: "content", As: "content", MaxRunes: 8000}, contentField)
}

func TestTypeParamCombinesListAndStringBounds(t *testing.T) {
	doc := &specDocument{}
	shape := &specSchema{Type: "array", Items: &specSchema{Type: "string"}}
	var p Param
	require.NoError(t, doc.typeParam(&p, shape, "2", "5"))
	op := Operation{Method: http.MethodPost, Params: []Param{p}}
	op.Params[0].Name = "titles"
	op.Params[0].In = InBody
	_, err := op.Build(map[string]json.RawMessage{"titles": raw([]string{"one", "two"})})
	require.NoError(t, err)
	_, err = op.Build(map[string]json.RawMessage{"titles": raw([]string{"one", "two", "three"})})
	require.Error(t, err, "the item count remains bounded")
	_, err = op.Build(map[string]json.RawMessage{"titles": raw([]string{"too long"})})
	require.Error(t, err, "each string remains bounded")

	for _, kind := range []string{"integer", "boolean"} {
		assert.Error(t, doc.typeParam(&Param{}, &specSchema{Type: kind}, "", "5"))
	}
	for _, kind := range []string{"string", "boolean"} {
		assert.Error(t, doc.typeParam(&Param{}, &specSchema{Type: kind}, "2", ""))
	}
}
