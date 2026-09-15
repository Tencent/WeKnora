package weknora

import (
	"context"
	"encoding/json"
	"errors"
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

func TestTriggerSkillAcceptsTopLevelTypeAsTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"failed\",\"failure_reason\":\"production_graph_missing_audited_wiki_write\",\"total_steps\":40}}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL})
	err := client.TriggerSkill(context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", nil, nil)
	require.EqualError(t, err, "agent chat failed: production_graph_missing_audited_wiki_write")
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
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"tool_result\",\"done\":false,\"data\":{\"tool_name\":\"wiki_write_page\",\"success\":true,\"production_source\":{\"page_producer\":\"extract_video_knowledge_v2\",\"event_id\":\"event-1\"}}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"succeeded\",\"total_steps\":1}}\n\n")
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

func TestTriggerSkillRejectsProductionGraphWithoutAuditedWikiWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"succeeded\",\"total_steps\":1}}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{
		BaseURL: server.URL, KBID: "kb-1", ContentPipelineAuditSecret: agentTestAuditSecret,
	})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"},
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.EqualError(t, err, "agent chat production graph completed without an audited wiki_write_page")
}

func TestTriggerSkillAcceptsProductionGraphWithAuditedWikiWrite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"tool_result\",\"done\":false,\"data\":{\"tool_name\":\"wiki_write_page\",\"success\":true,\"production_source\":{\"page_producer\":\"extract_video_knowledge_v2\",\"event_id\":\"event-1\"}}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"succeeded\",\"total_steps\":1}}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{
		BaseURL: server.URL, KBID: "kb-1", ContentPipelineAuditSecret: agentTestAuditSecret,
	})
	require.NoError(t, client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"},
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	))
}

func TestTriggerSkillReadsProductionFailureFromStreamData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"failed\",\"failure_reason\":\"empty_response\",\"total_steps\":0}}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{
		BaseURL: server.URL, KBID: "kb-1", ContentPipelineAuditSecret: agentTestAuditSecret,
	})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"},
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.EqualError(t, err, "agent chat failed: empty_response")
}

func TestTriggerSkillClassifiesProductionFailureWithoutReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"failed\",\"total_steps\":4}}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1", ContentPipelineAuditSecret: agentTestAuditSecret})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"},
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.EqualError(t, err, "agent chat failed: agent_completion_failed")
}

func TestTriggerSkillPreservesBoundedFailureDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"tool_result\",\"done\":false,\"data\":{\"round\":2,\"tool_name\":\"wiki_write_page\",\"success\":false,\"error\":\"schema validation failed\",\"tool_output\":\"secret transcript text\"}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"response_type\":\"complete\",\"done\":true,\"data\":{\"outcome\":\"failed\",\"failure_reason\":\"context window exceeded\",\"total_steps\":4}}\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{BaseURL: server.URL})
	err := client.TriggerSkill(context.Background(), "session-42", "agent-1", "extract-video-knowledge", "query", nil, nil)
	var failure *AgentFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "session-42", failure.Diagnostic.SessionID)
	require.Equal(t, 2, failure.Diagnostic.Rounds)
	require.Equal(t, "context_limit", failure.Diagnostic.FailureClass)
	require.Equal(t, []string{"context window exceeded"}, failure.Diagnostic.ModelErrors)
	require.Equal(t, []string{"schema validation failed"}, failure.Diagnostic.ToolErrors)
	require.Len(t, failure.Diagnostic.ToolResults, 1)
	require.Equal(t, 22, failure.Diagnostic.ToolResults[0].OutputBytes)
	require.NotContains(t, failure.Diagnostic.FailureMessage, "secret transcript")
}

func TestClassifyAgentFailureUsesStableFailureClasses(t *testing.T) {
	tests := []struct {
		name       string
		diagnostic AgentRunDiagnostic
		err        error
		want       string
	}{
		{name: "timeout", err: context.DeadlineExceeded, want: "model_timeout"},
		{name: "context", err: errors.New("model context length exceeded"), want: "context_limit"},
		{name: "tool", diagnostic: AgentRunDiagnostic{ToolResults: []AgentToolResultSummary{{ToolName: "wiki_write_page", Success: false}}}, err: errors.New("agent chat failed: agent_completion_failed"), want: "tool_failure"},
		{name: "contract", err: errors.New("invalid JSON output contract"), want: "output_contract"},
		{name: "empty response", err: errors.New("agent chat failed: empty_response"), want: "output_contract"},
		{name: "audited write", err: errors.New("agent chat failed: production_graph_missing_audited_wiki_write"), want: "output_contract"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyAgentFailure(tt.diagnostic, tt.err))
		})
	}
}

func TestTriggerSkillRejectsProductionGraphDoneWithoutVerifiedCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewAgentClient(config.WeKnoraConfig{
		BaseURL: server.URL, KBID: "kb-1", ContentPipelineAuditSecret: agentTestAuditSecret,
	})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", []string{"knowledge-1"},
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.EqualError(t, err, "agent chat production graph stream ended without a verified completion event")
}

func TestTriggerSkillFailsClosedWhenProductionSecretIsMissing(t *testing.T) {
	client := NewAgentClient(config.WeKnoraConfig{BaseURL: "http://unused"})
	err := client.TriggerSkill(
		context.Background(), "session-1", "agent-1", "extract-video-knowledge", "query", nil,
		&contentprovenance.Job{TaskID: "job-1", VideoID: "video-1", TranscriptGeneration: "generation-1", JobType: "graph"},
	)
	require.ErrorIs(t, err, contentprovenance.ErrMissingSecret)
}
