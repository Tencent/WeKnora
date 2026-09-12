package tools

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/stretchr/testify/require"
)

const browserTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aTioAAAAASUVORK5CYII="

func TestScreenshotReturnsImageOutsideModelText(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"format": "png", "image_base64": browserTestPNG, "width": 999, "height": 999, "tab_id": 7,
	})
	require.NoError(t, err)
	tool := NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 1, User: "alice"}, "chat")
	result := tool.interpretResult("screenshot", raw)
	require.True(t, result.Success)
	require.Equal(t, []string{"data:image/png;base64," + browserTestPNG}, result.Images)
	require.NotContains(t, result.Output, browserTestPNG)
	require.Equal(t, browserTestPNG, result.Data["image_base64"])
	require.JSONEq(t, `{"format":"png","width":1,"height":1,"tab_id":7}`, result.Output)
	for _, invalid := range []string{
		`{}`, `{"format":"svg","image_base64":"PHN2Zz4="}`,
		`{"format":"png","image_base64":"not-an-image"}`,
		`{"format":"jpeg","image_base64":"` + browserTestPNG + `"}`,
	} {
		result = tool.interpretResult("screenshot", json.RawMessage(invalid))
		require.False(t, result.Success)
		require.Empty(t, result.Images)
		require.Empty(t, result.Output)
		require.True(t, tool.failed.Load())
	}
}
