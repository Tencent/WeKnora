package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMCPRequestMetaForAgentRun(t *testing.T) {
	meta := &types.MCPRequestMeta{Headers: map[string]string{"Authorization": "Bearer token"}}

	if got := mcpRequestMetaForAgentRun(&types.QARequest{MCPRequestMeta: meta}); got != meta {
		t.Fatal("local Agent run should retain its request metadata")
	}
	if got := mcpRequestMetaForAgentRun(&types.QARequest{
		MCPRequestMeta:      meta,
		SharedAgentReadOnly: true,
	}); got != nil {
		t.Fatal("shared Agent run must not receive caller request metadata")
	}
	if got := mcpRequestMetaForAgentRun(nil); got != nil {
		t.Fatal("nil request should not produce metadata")
	}
}
