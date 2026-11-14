package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuiltinArtifactOperationsAreReadOnly(t *testing.T) {
	for _, op := range Builtin().All() {
		if op.Domain() != "artifact" {
			continue
		}
		assert.False(t, op.Risk.Mutates(), op.ID)
	}
}

func TestBuiltinArtifactList(t *testing.T) {
	op, ok := Builtin().Get("artifact.list")
	require.True(t, ok)
	assert.Equal(t, RiskRead, op.Risk)
	assert.Equal(t, "/api/v1/artifacts", op.Route)
	assert.ElementsMatch(t, []string{"keyword", "file_types", "page", "page_size"}, paramNames(op))
	size, _ := op.Param("page_size")
	assert.Equal(t, 20, size.Maximum)

	call, err := op.Build(map[string]json.RawMessage{
		"keyword": json.RawMessage(`"报告"`), "page_size": json.RawMessage(`10`),
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, call.Method)
	assert.Contains(t, call.URI(), "page_size=10")

	assert.JSONEq(t, `{"data":[{"file_name":"报告.docx","file_type":".docx","file_size":1024,
		"created_at":"2026-10-19T08:00:00Z","session_id":"s1","session_title":"周报",
		"message_id":"m1","index":0,"version_count":2,"handle":"resource://abc"}],
		"meta":{"total":1,"page":1,"page_size":10}}`,
		projectJSON(t, op, `{"success":true,"total":1,"page":1,"page_size":10,"data":[{
			"file_name":"报告.docx","file_type":".docx","file_size":1024,
			"created_at":"2026-10-19T08:00:00Z","session_id":"s1","session_title":"周报",
			"message_id":"m1","index":0,"version_count":2,"handle":"resource://abc",
			"source_path":"/workspace/output/report.docx"}]}`))
}
