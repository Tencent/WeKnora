package openairesponses

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
