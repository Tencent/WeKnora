package agentcmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/cmd"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// A rename must send back every config field the server returned: the update
// replaces the whole stored config.
func TestAgentUpdate_NamePreservesServerConfigFields(t *testing.T) {
	t.Setenv("WEKNORA_PROFILE", "")
	iostreams.SetForTest(t)
	kept := map[string]any{
		"mcp_auth_wait_timeout":             float64(45),
		"access_level":                      "read_only",
		"sandbox_config_id":                 "sandbox-1",
		"attachment_image_understanding":    true,
		"attachment_ocr_max_pages":          float64(12),
		"attachment_parse_wait_timeout_sec": float64(30),
		"memory_enabled":                    false,
		"intent_prompts":                    map[string]any{"greeting": "Say hello"},
		"chat_parser_engine_rules": []any{
			map[string]any{"file_types": []any{"pdf"}, "engine": "mineru"},
		},
	}
	config := map[string]any{"agent_mode": "smart-reasoning", "model_id": "model-original"}
	for k, v := range kept {
		config[k] = v
	}
	var updated map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any = map[string]any{"id": "agent-1", "name": "Original", "config": config}
		if r.Method == http.MethodPut {
			if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data = updated
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	t.Cleanup(server.Close)
	factory := &cmdutil.Factory{
		Client:   func() (*sdk.Client, error) { return sdk.NewClient(server.URL), nil },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	root := cmd.NewRootCmd(factory)
	root.SetArgs([]string{"agent", "update", "agent-1", "--name", "Renamed", "-y", "--format", "json"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	root.SetContext(ctx)
	require.NoError(t, root.Execute())

	actual, ok := updated["config"].(map[string]any)
	require.True(t, ok, "PUT must carry the fetched agent config")
	for k, want := range kept {
		require.Equal(t, want, actual[k], k)
	}
}
