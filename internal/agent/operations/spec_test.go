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
    "/kbs/{id}/docs": {"get": {
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
    "/docs/{id}": {
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
    "/docs/{id}/raw": {"get": {
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
	assert.Equal(t, "/api/v1/kbs/:id/docs", list.Route)
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
		"list of objects inside a result": {
			`"tags": {"type": "array", "items": {"type": "string"}, "x-agent": true}`,
			`"tags": {"type": "array", "items": {"$ref": "#/definitions/types.Strategy"}, "x-agent": true}`,
		},
		"nothing exposed": {
			`"message": {"type": "string", "x-agent": true}`, `"message": {"type": "string"}`,
		},
		"undefined schema": {`"$ref": "#/definitions/types.Status"`, `"$ref": "#/definitions/types.Missing"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, 1, strings.Count(testSpec, tc.from), "fixture edit must be unambiguous")
			_, err := FromSpec([]byte(strings.Replace(testSpec, tc.from, tc.to, 1)))
			assert.Error(t, err)
		})
	}
}

func TestFromSpecKeepsCatalogSafetyRules(t *testing.T) {
	forbidden := strings.Replace(testSpec, `"/docs/{id}": {`, `"/api-keys/{id}": {`, 1)
	ops, err := FromSpec([]byte(forbidden))
	require.NoError(t, err)
	_, err = NewCatalog(ops...)
	assert.ErrorContains(t, err, "forbidden area")

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
	for _, op := range Builtin().All() {
		meta := agentOperation{ID: op.ID, Risk: op.Risk}
		if op.Target != nil {
			meta.Target, meta.Label = op.Target.Operation, op.Target.Label
		}
		built[op.ID] = meta
	}
	for id, meta := range declared {
		meta.Root, meta.MaxItems = nil, 0
		declared[id] = meta
	}
	assert.Equal(t, declared, built, "docs/swagger.json is stale; run make docs")
}
