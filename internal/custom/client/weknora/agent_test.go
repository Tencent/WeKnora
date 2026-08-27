package weknora

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

func TestTriggerSkillScopesRequestToSourceKnowledge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		require.Equal(t, "knowledge-1", payload["knowledge_ids"].([]any)[0])
		require.Equal(t, "kb-1", payload["knowledge_base_ids"].([]any)[0])
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1"})
	require.NoError(t, client.TriggerSkill(context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"}))
}
