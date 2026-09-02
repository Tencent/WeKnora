package session

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMCPRequestMetaUsesExplicitAllowlist(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer request-token"},
		"X-Trace-Id":    {"trace-a", "trace-b"},
		"X-Unlisted":    {"must-not-leak"},
	}
	request := &CreateKnowledgeQARequest{
		Query:            "where is the report?",
		AgentEnabled:     true,
		AgentID:          "agent-1",
		Channel:          "api",
		KnowledgeBaseIDs: []string{"kb-1", "kb-2"},
		MCPMetadata: map[string]string{
			"user_role": "auditor",
			"secret":    "must-not-leak",
		},
	}
	config := &types.MCPRequestMetaConfig{
		Headers: []string{"authorization", "X-Trace-ID", "x-trace-id", "X-Missing"},
		BodyFields: []string{
			"channel", "agent_enabled", "knowledge_base_ids",
			"mcp_metadata.user_role", "mcp_metadata.missing", "images",
		},
	}

	got := buildMCPRequestMeta(headers, request, config, false)
	require.NotNil(t, got)
	assert.Equal(t, map[string]string{
		"Authorization": "Bearer request-token",
		"X-Trace-Id":    "trace-a, trace-b",
	}, got.Headers)
	assert.Equal(t, "api", got.Body["channel"])
	assert.Equal(t, true, got.Body["agent_enabled"])
	assert.Equal(t, []string{"kb-1", "kb-2"}, got.Body["knowledge_base_ids"])
	assert.Equal(t, map[string]string{"user_role": "auditor"}, got.Body["mcp_metadata"])
	assert.NotContains(t, got.Headers, "X-Unlisted")
	assert.NotContains(t, got.Body["mcp_metadata"], "secret")
}

func TestBuildMCPRequestMetaSkipsSharedAgents(t *testing.T) {
	got := buildMCPRequestMeta(
		http.Header{"Authorization": {"Bearer caller-secret"}},
		&CreateKnowledgeQARequest{MCPMetadata: map[string]string{"role": "admin"}},
		&types.MCPRequestMetaConfig{
			Headers:    []string{"Authorization"},
			BodyFields: []string{"mcp_metadata.role"},
		},
		true,
	)
	assert.Nil(t, got)
}

func TestBuildMCPRequestMetaSkipsOversizedValues(t *testing.T) {
	headers := http.Header{
		"Authorization": {strings.Repeat("a", types.MCPRequestMetaMaxValueBytes+1)},
		"X-Trace-Id":    {"trace-ok"},
	}
	request := &CreateKnowledgeQARequest{MCPMetadata: map[string]string{
		"oversized": strings.Repeat("b", types.MCPRequestMetaMaxValueBytes+1),
		"small":     "kept",
	}}
	config := &types.MCPRequestMetaConfig{
		Headers: []string{"Authorization", "X-Trace-Id"},
		BodyFields: []string{
			"mcp_metadata.oversized", "mcp_metadata.small",
		},
	}

	got := buildMCPRequestMeta(headers, request, config, false)
	require.NotNil(t, got)
	assert.Equal(t, map[string]string{"X-Trace-Id": "trace-ok"}, got.Headers)
	assert.Equal(t, map[string]string{"small": "kept"}, got.Body["mcp_metadata"])
}

func TestBuildMCPRequestMetaReturnsNilWithoutSelectedValues(t *testing.T) {
	got := buildMCPRequestMeta(
		http.Header{},
		&CreateKnowledgeQARequest{},
		&types.MCPRequestMetaConfig{
			Headers:    []string{"X-Missing"},
			BodyFields: []string{"mcp_metadata.missing"},
		},
		false,
	)
	assert.Nil(t, got)
}
