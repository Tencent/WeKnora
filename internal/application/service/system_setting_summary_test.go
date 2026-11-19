package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemSettingSummariesUseRegistryAndHideSecrets(t *testing.T) {
	rows := []*types.SystemSetting{
		nil,
		{Key: "unknown.api_key", Value: types.JSON(`"hidden-token"`), Description: "hidden-description"},
		{Key: "auth.registration_mode", Value: types.JSON(`"hidden-token"`), IsSecret: true},
		{
			Key:         "model.max_concurrency",
			Value:       types.JSON(`9007199254740993`),
			ValueType:   "string",
			Description: "hidden-description",
		},
		{Key: "sandbox.docker_enabled", Value: types.JSON(`false`)},
		{Key: "ssrf.whitelist", Value: types.JSON(`["example.com"]`)},
		{Key: "asynq.core_concurrency", Value: types.JSON(`"invalid"`)},
		{Key: "gallery.policy", Value: types.JSON(`"` + strings.Repeat("x", 4097) + `"`)},
	}
	out := SummarizeSystemSettings(rows, "")
	require.Len(t, out, 6)
	assert.True(t, out[0].ValueHidden)
	assert.Empty(t, out[0].ValueJSON)
	assert.Equal(t, "9007199254740993", out[1].ValueJSON)
	assert.Equal(t, "int", out[1].ValueType)
	assert.Equal(t, "false", out[2].ValueJSON)
	assert.Equal(t, `["example.com"]`, out[3].ValueJSON)
	assert.True(t, out[4].ValueHidden)
	assert.True(t, out[4].RequiresRestart)
	assert.True(t, out[5].ValueHidden)
	blob, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(blob), "hidden-")
	assert.NotContains(t, string(blob), "unknown.api_key")
	require.Len(t, SummarizeSystemSettings(rows, "model.max_concurrency"), 1)
	assert.Empty(t, SummarizeSystemSettings(rows, "unknown.api_key"))
}
