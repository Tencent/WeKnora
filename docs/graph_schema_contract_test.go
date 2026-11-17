package docs_test

import (
	"slices"
	"testing"
)

func TestGraphSchemaContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Definitions map[string]struct {
					Properties map[string]any `json:"properties" yaml:"properties"`
					Required   []string       `json:"required" yaml:"required"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			const prefix = "github_com_Tencent_WeKnora_internal_types."
			for name, fields := range map[string][]string{
				"GraphSchema":       {"enabled", "entity_types", "relation_types"},
				"GraphEntityType":   {"name", "description"},
				"GraphRelationType": {"name", "source_type", "target_type", "description"},
				"ExtractConfig":     {"schema"},
				"GraphNode":         {"entity_type"},
				"GraphRelation":     {"source_type", "target_type"},
				"GraphSearchEntity": {"entity_type"},
			} {
				properties := spec.Definitions[prefix+name].Properties
				for _, field := range fields {
					if properties[field] == nil {
						t.Errorf("%s.%s must be documented", name, field)
					}
				}
				if (name == "GraphEntityType" || name == "GraphRelationType") && properties["attributes"] != nil {
					t.Errorf("%s must not expose entity properties", name)
				}
			}
			request := spec.Definitions["internal_handler.TextRelationExtractionRequest"]
			if request.Properties["schema"] == nil || request.Properties["custom_instructions"] == nil {
				t.Error("extraction request must document schema and supplemental instructions")
			}
			if slices.Contains(request.Required, "tags") {
				t.Error("legacy tags cannot be required in schema mode")
			}
			if spec.Definitions["internal_handler.FabriTextRequest"].Properties["schema"] == nil {
				t.Error("text generation request must document the optional schema")
			}
			initial := spec.Definitions["internal_handler.InitializationRequest"].Properties["nodeExtract"].(map[string]any)
			properties := initial["properties"].(map[string]any)
			for field, model := range map[string]string{"nodes": "GraphNode", "relations": "GraphRelation"} {
				array := properties[field].(map[string]any)
				items := array["items"].(map[string]any)
				if items["$ref"] != "#/definitions/"+prefix+model {
					t.Errorf("initialization %s must reference the typed %s contract", field, model)
				}
			}
		})
	}
}
