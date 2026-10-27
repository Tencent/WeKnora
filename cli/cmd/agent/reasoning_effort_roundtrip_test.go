package agentcmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/cmd"
	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestAgentUpdate_NamePreservesReasoningEffort(t *testing.T) {
	cases := []struct {
		name, effort string
		thinking     any
	}{
		{name: "off", effort: "off", thinking: false},
		{name: "auto", effort: "auto", thinking: true},
		{name: "minimal", effort: "minimal", thinking: true},
		{name: "low", effort: "low", thinking: true},
		{name: "medium", effort: "medium", thinking: true},
		{name: "high", effort: "high", thinking: true},
		{name: "xhigh", effort: "xhigh", thinking: true},
		{name: "max", effort: "max", thinking: true},
		{name: "legacy thinking enabled", thinking: true},
		{name: "legacy thinking disabled", thinking: false},
		{name: "legacy thinking unset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEKNORA_PROFILE", "")
			out, _ := iostreams.SetForTest(t)
			config := map[string]any{
				"thinking": tc.thinking, "agent_mode": "smart-reasoning",
				"model_id": "model-original", "system_prompt": "Keep this prompt",
				"temperature": 0.7, "knowledge_bases": []string{"kb-original"},
			}
			if tc.effort != "" {
				config["reasoning_effort"] = tc.effort
			}
			var reads, writes atomic.Int32
			var updated map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/agents/agent-1" {
					http.NotFound(w, r)
					return
				}
				var data any
				switch r.Method {
				case http.MethodGet:
					reads.Add(1)
					data = map[string]any{
						"id": "agent-1", "name": "Original", "description": "Keep description", "config": config,
					}
				case http.MethodPut:
					writes.Add(1)
					if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					data = updated
				default:
					http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
					return
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
			require.EqualValues(t, 1, reads.Load())
			require.EqualValues(t, 1, writes.Load())
			require.Equal(t, "Renamed", updated["name"])
			require.Equal(t, "Keep description", updated["description"])
			actual, ok := updated["config"].(map[string]any)
			require.True(t, ok, "PUT must carry the fetched agent config")
			require.Equal(t, tc.thinking, actual["thinking"])
			require.Equal(t, "model-original", actual["model_id"])
			require.Equal(t, "Keep this prompt", actual["system_prompt"])
			require.Equal(t, 0.7, actual["temperature"])
			require.Equal(t, []any{"kb-original"}, actual["knowledge_bases"])
			effort, present := actual["reasoning_effort"]
			if tc.effort == "" {
				require.False(t, present, "legacy configs must retain omitted reasoning_effort")
			} else {
				require.Equal(t, tc.effort, effort, "a name change must preserve the selected thinking intensity")
			}
			var envelope struct {
				OK   bool `json:"ok"`
				Data struct {
					Config map[string]any `json:"config"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(out.Bytes(), &envelope))
			require.True(t, envelope.OK)
			require.Equal(t, effort, envelope.Data.Config["reasoning_effort"])
		})
	}
}
