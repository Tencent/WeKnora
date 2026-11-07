package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestMCPGrantKeyFollowsTheToolContract(t *testing.T) {
	svc := &types.MCPService{ID: "svc", Name: "Tracker"}
	tool := func(schema string) *MCPTool {
		return NewMCPTool(svc, &types.MCPTool{Name: "create_issue", InputSchema: json.RawMessage(schema)}, nil, nil, 0)
	}
	ctx := context.Background()
	key, label := tool(`{"type":"object"}`).approvalGrant(ctx)
	same, _ := tool(`{"type":"object"}`).approvalGrant(ctx)
	changed, _ := tool(`{"type":"object","properties":{"x":{}}}`).approvalGrant(ctx)

	assert.Equal(t, key, same)
	assert.NotEqual(t, key, changed, "a changed input schema needs a fresh approval")
	assert.Contains(t, key, "mcp:svc/create_issue#")
	assert.Contains(t, label, "Tracker")
}
