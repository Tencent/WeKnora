package weknora

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/contentprovenance"
	"github.com/Tencent/WeKnora/internal/custom/config"
)

const agentTestAuditSecret = "test-content-pipeline-secret-32-bytes"

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
	require.NoError(t, client.TriggerSkill(context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"}, nil))
}

func TestTriggerSkillKeepsReadingAfterNonTerminalToolError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: message\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"error\",\"content\":\"tool validation failed\",\"done\":false}\n\n")
		_, _ = io.WriteString(writer, "event: message\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL})
	require.NoError(t, client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", nil, nil,
	))
}

func TestTriggerSkillReturnsTerminalAgentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: message\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"error\",\"content\":\"agent failed\",\"done\":true}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", nil, nil,
	)
	require.EqualError(t, err, "agent chat error: agent failed")
}

func TestTriggerSkillRejectsStreamWithoutTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: message\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"error\",\"content\":\"tool failed\",\"done\":false}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", nil, nil,
	)
	require.EqualError(t, err, "agent chat stream ended before a terminal event")
}

func TestTriggerSkillSignsProductionProvenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		envelope, err := contentprovenance.Verify(
			agentTestAuditSecret,
			request.Header.Get(contentprovenance.EnvelopeHeader),
			request.Header.Get(contentprovenance.SignatureHeader),
		)
		require.NoError(t, err)
		require.Equal(t, "job-1", envelope.TaskID)
		require.True(t, envelope.MatchesRequest(
			"session-1", "agent-1", "query", []string{"extract-video-knowledge"},
			[]string{"kb-1"}, []string{"knowledge-1"},
		))
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{
		BaseURL: server.URL, KBID: "kb-1", ContentPipelineAuditSecret: agentTestAuditSecret,
	})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"},
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.NoError(t, err)
}

func TestTriggerSkillFailsClosedWhenProductionSecretIsMissing(t *testing.T) {
	client := NewAgentClient(config.WeKnoraConfig{BaseURL: "http://unused"})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", nil,
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.ErrorIs(t, err, contentprovenance.ErrMissingSecret)
}
