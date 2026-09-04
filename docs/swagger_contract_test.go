package docs_test

import (
	"encoding/json"
	"os"
	"testing"

	docs "github.com/Tencent/WeKnora/docs"
	"gopkg.in/yaml.v3"
)

type swaggerDocumentCase struct {
	name     string
	loadSpec func(t *testing.T) []byte
	parse    func([]byte, any) error
}

func swaggerDocuments() []swaggerDocumentCase {
	return []swaggerDocumentCase{
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
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertKnowledgeSearchRouteContract(t, tt.loadSpec(t), tt.parse)
		})
	}
}

func TestModelDeleteUsageContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			assertModelDeleteUsageContract(t, tt.loadSpec(t), tt.parse)
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

func assertModelDeleteUsageContract(t *testing.T, data []byte, parse func([]byte, any) error) {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]struct {
			Responses map[string]any `json:"responses" yaml:"responses"`
		} `json:"paths" yaml:"paths"`
		Definitions map[string]map[string]any `json:"definitions" yaml:"definitions"`
	}
	if err := parse(data, &spec); err != nil {
		t.Fatalf("parse generated Swagger document: %v", err)
	}

	models, ok := spec.Paths["/models/{id}"]
	if !ok {
		t.Fatal("generated Swagger document does not expose /models/{id}")
	}
	deleteOperation, ok := models["delete"]
	if !ok {
		t.Fatal("generated Swagger document does not expose DELETE /models/{id}")
	}
	if _, ok := deleteOperation.Responses["400"]; !ok {
		t.Fatal("model DELETE Swagger contract does not document the model-in-use 400 response")
	}

	const errorCodeDefinition = "github_com_Tencent_WeKnora_internal_errors.ErrorCode"
	errorCodes, ok := spec.Definitions[errorCodeDefinition]
	if !ok {
		t.Fatalf("generated Swagger document does not expose %s", errorCodeDefinition)
	}
	enum, ok := errorCodes["enum"].([]any)
	if !ok {
		t.Fatalf("generated Swagger ErrorCode enum has unexpected shape: %#v", errorCodes["enum"])
	}
	enumVarNames, ok := errorCodes["x-enum-varnames"].([]any)
	if !ok {
		t.Fatalf("generated Swagger ErrorCode names have unexpected shape: %#v", errorCodes["x-enum-varnames"])
	}
	for i, value := range enum {
		if swaggerInteger(value) == 2300 {
			if i >= len(enumVarNames) || enumVarNames[i] != "ErrModelInUse" {
				t.Fatalf("error code 2300 must align with ErrModelInUse, got names=%v", enumVarNames)
			}
			return
		}
	}
	t.Fatal("generated Swagger ErrorCode enum does not include model-in-use code 2300")
}

func swaggerInteger(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return 0
	}
}
