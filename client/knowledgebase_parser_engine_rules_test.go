package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestChunkingConfigParserEngineRulesJSONRoundTrip(t *testing.T) {
	const body = `{
		"chunk_size": 512,
		"chunk_overlap": 50,
		"separators": ["\n\n"],
		"parser_engine_rules": [
			{"file_types": ["pdf", "docx"], "engine": "anydoc"},
			{"file_types": ["txt"], "engine": "builtin"}
		]
	}`
	var config ChunkingConfig
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chunking config changed during JSON round trip: got %#v, want %#v", got, want)
	}
}

func TestKnowledgeBaseParserEngineRulesRequests(t *testing.T) {
	rules := []ParserEngineRule{
		{FileTypes: []string{"pdf", "docx"}, Engine: "anydoc"},
		{FileTypes: []string{"txt"}, Engine: "builtin"},
	}
	config := ChunkingConfig{ChunkSize: 512, ChunkOverlap: 50, ParserEngineRules: rules}
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			path := "/api/v1/knowledge-bases/kb-1"
			if method == http.MethodPost {
				path = "/api/v1/knowledge-bases"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method || r.URL.Path != path {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if method != http.MethodGet {
					var request struct {
						ChunkingConfig ChunkingConfig       `json:"chunking_config"`
						Config         *KnowledgeBaseConfig `json:"config"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						return
					}
					got := request.ChunkingConfig.ParserEngineRules
					if method == http.MethodPut {
						if request.Config == nil {
							t.Error("update request is missing config")
							return
						}
						got = request.Config.ChunkingConfig.ParserEngineRules
					}
					if !reflect.DeepEqual(got, rules) {
						t.Errorf("request parser engine rules: got %#v, want %#v", got, rules)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(KnowledgeBaseResponse{
					Success: true,
					Data:    KnowledgeBase{ID: "kb-1", ChunkingConfig: config},
				}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			client := NewClient(srv.URL)
			var kb *KnowledgeBase
			var err error
			switch method {
			case http.MethodPost:
				kb, err = client.CreateKnowledgeBase(context.Background(), &KnowledgeBase{
					Name: "parser-config", ChunkingConfig: config,
				})
			case http.MethodGet:
				kb, err = client.GetKnowledgeBase(context.Background(), "kb-1")
			case http.MethodPut:
				kb, err = client.UpdateKnowledgeBase(context.Background(), "kb-1", &UpdateKnowledgeBaseRequest{
					Config: &KnowledgeBaseConfig{ChunkingConfig: config},
				})
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(kb.ChunkingConfig.ParserEngineRules, rules) {
				t.Errorf("response parser engine rules: got %#v, want %#v", kb.ChunkingConfig.ParserEngineRules, rules)
			}
		})
	}
}

func TestChunkingConfigOmitsUnsetParserEngineRules(t *testing.T) {
	for _, rules := range [][]ParserEngineRule{nil, {}} {
		encoded, err := json.Marshal(ChunkingConfig{ParserEngineRules: rules})
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["parser_engine_rules"]; ok {
			t.Errorf("unset parser engine rules should be omitted: %s", encoded)
		}
	}
}
