package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDatabaseQueryRejectsUnattributableChunkTable(t *testing.T) {
	tool := NewDatabaseQueryTool(nil, types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}})

	for _, query := range []string{
		"SELECT c.id, c.chunk_index FROM chunks c",
		"SELECT c.content FROM chunks c",
		"SELECT CONCAT(c.content, '') AS excerpt FROM chunks c",
		"SELECT * FROM chunks",
		"SELECT c FROM chunks c",
		"SELECT row_to_json(c) FROM chunks c",
	} {
		_, err := tool.validateAndSecureSQL(query, 1)
		if err == nil || !strings.Contains(err.Error(), "feedback attribution") {
			t.Fatalf("validateAndSecureSQL(%q) error = %v, want attribution rejection", query, err)
		}
	}
}
