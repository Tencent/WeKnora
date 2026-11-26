package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestImportPreservesCustomInstructions(t *testing.T) {
	for _, section := range []string{"vlm_config", "question_generation_config", "extract_config"} {
		t.Run(section, func(t *testing.T) {
			input := `{"url":"https://example.test/doc.pdf","process_config":{"` + section +
				`":{"enabled":true,"custom_instructions":"Focus on domain terminology"}}}`
			var request CreateKnowledgeFromURLRequest
			if err := json.Unmarshal([]byte(input), &request); err != nil {
				t.Fatal(err)
			}
			transport := tagTransport(func(req *http.Request) (*http.Response, error) {
				var received struct {
					ProcessConfig map[string]struct {
						Enabled      bool   `json:"enabled"`
						Instructions string `json:"custom_instructions"`
					} `json:"process_config"`
				}
				if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
					t.Fatal(err)
				}
				if got := received.ProcessConfig[section].Instructions; got != "Focus on domain terminology" {
					t.Errorf("%s.custom_instructions = %q, want the supplied guidance", section, got)
				}
				if !received.ProcessConfig[section].Enabled {
					t.Errorf("%s must remain enabled", section)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"id":"doc-1"}}`)),
					Header:     make(http.Header),
				}, nil
			})
			c := NewClient("https://example.test", WithTransport(transport))
			if _, err := c.CreateKnowledgeFromURL(context.Background(), "kb-1", request); err != nil {
				t.Fatal(err)
			}
		})
	}
}
