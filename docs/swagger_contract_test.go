package docs_test

import (
	"encoding/json"
	"os"
	"testing"

	docs "github.com/Tencent/WeKnora/docs"
	"gopkg.in/yaml.v3"
)

type generatedSwaggerDocument struct {
	name     string
	loadSpec func(t *testing.T) []byte
	parse    func([]byte, any) error
}

func generatedSwaggerDocuments() []generatedSwaggerDocument {
	return []generatedSwaggerDocument{
		{
			name: "registered document",
			loadSpec: func(t *testing.T) []byte {
				t.Helper()
				return []byte(docs.SwaggerInfo.ReadDoc())
			},
			parse: json.Unmarshal,
		},
		{
			name: "swagger.json",
			loadSpec: func(t *testing.T) []byte {
				t.Helper()
				return readSwaggerFile(t, "swagger.json")
			},
			parse: json.Unmarshal,
		},
		{
			name: "swagger.yaml",
			loadSpec: func(t *testing.T) []byte {
				t.Helper()
				return readSwaggerFile(t, "swagger.yaml")
			},
			parse: yaml.Unmarshal,
		},
	}
}

func TestKnowledgeSearchRouteContract(t *testing.T) {
	for _, tt := range generatedSwaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertKnowledgeSearchRouteContract(t, tt.loadSpec(t), tt.parse)
		})
	}
}

func TestCrossTenantAccessRouteContract(t *testing.T) {
	for _, tt := range generatedSwaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertCrossTenantAccessRouteContract(t, tt.loadSpec(t), tt.parse)
		})
	}
}

func readSwaggerFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func assertKnowledgeSearchRouteContract(t *testing.T, data []byte, parse func([]byte, any) error) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]any `json:"paths" yaml:"paths"`
	}
	if err := parse(data, &spec); err != nil {
		t.Fatalf("parse generated Swagger document: %v", err)
	}

	knowledgeSearch, ok := spec.Paths["/knowledge-search"]
	if !ok {
		t.Fatal("generated Swagger document does not expose /knowledge-search")
	}
	if _, ok := knowledgeSearch["post"]; !ok {
		t.Fatal("generated Swagger document does not expose POST /knowledge-search")
	}

	if staleRoute, ok := spec.Paths["/sessions/search"]; ok {
		if _, ok := staleRoute["post"]; ok {
			t.Fatal("generated Swagger document still exposes stale POST /sessions/search")
		}
	}
}

func assertCrossTenantAccessRouteContract(t *testing.T, data []byte, parse func([]byte, any) error) {
	t.Helper()
	type operation struct {
		Parameters []struct {
			Name string `json:"name" yaml:"name"`
		} `json:"parameters" yaml:"parameters"`
	}
	var spec struct {
		Paths       map[string]map[string]operation `json:"paths" yaml:"paths"`
		Definitions map[string]struct {
			Properties map[string]any `json:"properties" yaml:"properties"`
		} `json:"definitions" yaml:"definitions"`
	}
	if err := parse(data, &spec); err != nil {
		t.Fatalf("parse generated Swagger document: %v", err)
	}

	expectedRoutes := map[string]string{
		"/system/admin/cross-tenant-access/grant":  "post",
		"/system/admin/cross-tenant-access/revoke": "post",
		"/system/admin/cross-tenant-access/list":   "get",
	}
	for route, method := range expectedRoutes {
		operations, ok := spec.Paths[route]
		if !ok {
			t.Errorf("generated Swagger document does not expose %s", route)
			continue
		}
		if _, ok := operations[method]; !ok {
			t.Errorf("generated Swagger document does not expose %s %s", method, route)
		}
	}

	listOperation := spec.Paths["/system/admin/cross-tenant-access/list"]["get"]
	parameterNames := make(map[string]bool, len(listOperation.Parameters))
	for _, parameter := range listOperation.Parameters {
		parameterNames[parameter.Name] = true
	}
	if !parameterNames["cursor"] || parameterNames["offset"] {
		t.Fatalf("list parameters must expose cursor and omit offset: %+v", parameterNames)
	}
	properties := spec.Definitions["internal_handler.ListCrossTenantAccessUsersResponse"].Properties
	if _, ok := properties["next_cursor"]; !ok {
		t.Fatal("list response schema does not expose next_cursor")
	}
	if _, ok := properties["total"]; ok {
		t.Fatal("list response schema still exposes offset-pagination total")
	}
}
