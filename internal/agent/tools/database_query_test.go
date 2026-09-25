package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDatabaseQueryInjectsEnabledChunkFilter(t *testing.T) {
	tool := NewDatabaseQueryTool(nil, types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}})

	securedSQL, err := tool.validateAndSecureSQL(
		"SELECT c.id, c.content FROM chunks c WHERE c.chunk_type = 'faq'",
		1,
	)
	if err != nil {
		t.Fatalf("validateAndSecureSQL() error = %v", err)
	}
	if !strings.Contains(securedSQL, "c.is_enabled = true") {
		t.Fatalf("Agent SQL must exclude disabled chunks:\n%s", securedSQL)
	}
}
