package docs_test

import "testing"

// Public tools consume all three generated forms. Empty-folder support must not
// lose the optional anchor or advertise a multipart field the handler ignores.
func TestExplicitKnowledgeFolderContract(t *testing.T) {
	for _, tt := range swaggerDocuments() {
		t.Run(tt.name, func(t *testing.T) {
			var spec struct {
				Paths map[string]map[string]struct {
					Parameters []swaggerParameter `json:"parameters" yaml:"parameters"`
					Responses  map[string]any     `json:"responses" yaml:"responses"`
				} `json:"paths" yaml:"paths"`
				Definitions map[string]struct {
					Required   []string `json:"required" yaml:"required"`
					Properties map[string]struct {
						Type string `json:"type" yaml:"type"`
					} `json:"properties" yaml:"properties"`
				} `json:"definitions" yaml:"definitions"`
			}
			if err := tt.parse(tt.loadSpec(t), &spec); err != nil {
				t.Fatal(err)
			}
			const folders = "/knowledge-bases/{id}/knowledge/folders"
			create, ok := spec.Paths[folders]["post"]
			if !ok || create.Responses["201"] == nil || create.Responses["409"] == nil {
				t.Fatal("folder creation must document success and duplicate conflict")
			}
			remove, ok := spec.Paths[folders+"/empty"]["delete"]
			if !ok || remove.Responses["200"] == nil || remove.Responses["409"] == nil {
				t.Fatal("empty-only deletion must document success and non-empty conflict")
			}
			pathRequired := false
			for _, param := range remove.Parameters {
				if param.Name == "path" && param.In == "query" && param.Required {
					pathRequired = true
				}
			}
			if !pathRequired {
				t.Fatal("empty-only deletion must require a query path")
			}
			deleted := spec.Definitions["internal_handler.DeleteKnowledgeFolderResponse"]
			if deleted.Properties["path"].Type != "string" {
				t.Fatal("empty-folder deletion must expose a typed path for operation tools")
			}
			request := spec.Definitions["internal_handler.CreateKnowledgeFolderRequest"]
			if request.Properties["name"].Type != "string" || request.Properties["parent_path"].Type != "string" {
				t.Fatal("folder creation must expose name and parent_path")
			}
			move := spec.Definitions["internal_handler.MoveKnowledgeToFolderRequest"]
			if move.Properties["existing_folder_anchor"].Type != "string" {
				t.Fatal("file move must expose its existing ancestor anchor")
			}
			for _, field := range move.Required {
				if field == "existing_folder_anchor" {
					t.Fatal("file move anchor must remain optional")
				}
			}
			upload := spec.Paths["/knowledge-bases/{id}/knowledge/file"]["post"]
			anchorFound := false
			for _, param := range upload.Parameters {
				if param.Name == "folder_path" {
					t.Fatal("upload uses fileName paths, not a folder_path multipart field")
				}
				if param.Name == "existing_folder_anchor" {
					anchorFound = param.In == "formData" && param.Type == "string" && !param.Required
				}
			}
			if !anchorFound {
				t.Fatal("upload must expose an optional formData anchor")
			}
		})
	}
}
