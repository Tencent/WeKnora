package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// roundTrip mimics persistence: Data comes back from the database as generic JSON.
func roundTrip(t *testing.T, data map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestHistoryKeepsFillSecretStatuses(t *testing.T) {
	data := roundTrip(t, map[string]interface{}{
		"display_type": "browser_fill_secret",
		"handle":       "cred_1",
		"fields":       map[string]string{"username": "filled", "password": "ref_not_found"},
	})
	got := CompactToolOutputForHistory(ToolBrowserFillSecret, &types.ToolResult{Success: true, Output: "x", Data: data})
	assert.Equal(t, "Fill results for cred_1:\n- password: ref_not_found\n- username: filled", got)
}

func TestHistoryKeepsSecureFormHandle(t *testing.T) {
	data := roundTrip(t, map[string]interface{}{
		"display_type": "secure_form",
		"answered":     true,
		"handle":       "cred_2",
		"fields":       []string{"username", "password"},
		"username":     "alice",
		"saved":        true,
		"request":      map[string]interface{}{"title": "登录", "site": "a.com"},
	})
	got := CompactToolOutputForHistory(ToolRequestSecureForm, &types.ToolResult{Success: true, Output: "x", Data: data})
	assert.Contains(t, got, "handle: cred_2  fields: username, password  username: alice")
	assert.NotContains(t, got, "payload omitted")
}

func TestHistorySecureFormDeclined(t *testing.T) {
	data := roundTrip(t, map[string]interface{}{
		"display_type": "secure_form", "answered": false, "reason": "the user declined to provide credentials",
	})
	got := CompactToolOutputForHistory(ToolRequestSecureForm, &types.ToolResult{Success: true, Data: data})
	assert.Equal(t, "The user did not provide credentials (the user declined to provide credentials).", got)
}
