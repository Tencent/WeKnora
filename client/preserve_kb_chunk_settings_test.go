package client

import (
	"encoding/json"
	"testing"
)

func TestUpdatePreservesChunkingStrategy(t *testing.T) {
	var kb KnowledgeBase
	if err := json.Unmarshal([]byte(`{
  "chunking_config": {
    "enable_parent_child": true,
    "parent_chunk_size": 4096,
    "child_chunk_size": 384,
    "strategy": "heading",
    "token_limit": 320,
    "parser_engine_rules": [
      {
        "engine": "builtin",
        "file_types": [
          "xlsx"
        ],
        "xlsx_first_row_as_header": false
      }
    ]
  }
}`), &kb); err != nil {
		t.Fatal(err)
	}
	kb.ChunkingConfig.ChunkSize = 768
	body, err := json.Marshal(UpdateKnowledgeBaseRequest{
		Config: &KnowledgeBaseConfig{ChunkingConfig: kb.ChunkingConfig},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Config struct {
			Chunking struct {
				Strategy string
				Parent   bool `json:"enable_parent_child"`
				Limit    int  `json:"token_limit"`
				Rules    []struct {
					Header *bool `json:"xlsx_first_row_as_header"`
				} `json:"parser_engine_rules"`
			} `json:"chunking_config"`
		}
	}
	if err = json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	c := wire.Config.Chunking
	if c.Strategy != "heading" || !c.Parent || c.Limit != 320 ||
		len(c.Rules) != 1 || c.Rules[0].Header == nil || *c.Rules[0].Header {
		t.Fatalf("update lost parser settings: %s", body)
	}
}
