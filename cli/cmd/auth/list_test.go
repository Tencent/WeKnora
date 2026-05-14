package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func newListFactory(cfg *config.Config) *cmdutil.Factory {
	return &cmdutil.Factory{
		Config: func() (*config.Config, error) { return cfg, nil },
	}
}

func TestList_HumanRender(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Host: "https://prod", User: "alice@example.com", TokenRef: "keychain://prod/access"},
			"staging": {Host: "https://staging", APIKeyRef: "keychain://staging/api_key"},
		},
	}
	require.NoError(t, runList(nil, newListFactory(cfg)))

	got := out.String()
	// One row per context, current marked with `*`.
	assert.Contains(t, got, "* prod")
	assert.Contains(t, got, "  staging")
	// Mode column.
	assert.Contains(t, got, ModeBearer)
	assert.Contains(t, got, ModeAPIKey)
	// Sorted alphabetically — prod after staging? No: "prod" < "staging".
	assert.Less(t, strings.Index(got, "prod"), strings.Index(got, "staging"),
		"contexts should render sorted by name")
}

func TestList_Empty(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	require.NoError(t, runList(nil, newListFactory(&config.Config{})))
	assert.Contains(t, out.String(), "No contexts configured")
}

func TestList_JSON_BareArray(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Host: "https://prod", User: "alice", TokenRef: "tok"},
			"staging": {Host: "https://staging", APIKeyRef: "key"},
		},
	}
	require.NoError(t, runList(&cmdutil.JSONOptions{}, newListFactory(cfg)))

	var got []listEntry
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 2)
	// Sorted: prod < staging.
	assert.Equal(t, "prod", got[0].Name)
	assert.True(t, got[0].Current)
	assert.Equal(t, ModeBearer, got[0].Mode)
	assert.Equal(t, "staging", got[1].Name)
	assert.False(t, got[1].Current)
	assert.Equal(t, ModeAPIKey, got[1].Mode)
}

func TestModeFromRefs(t *testing.T) {
	// Hand-edited config with neither ref set — surface "unknown" rather
	// than pretending the context is a valid login.
	assert.Equal(t, ModeUnknown, modeFromRefs("", ""))
	assert.Equal(t, ModeBearer, modeFromRefs("", "tok"))
	assert.Equal(t, ModeAPIKey, modeFromRefs("key", ""))
	assert.Equal(t, ModeBearer, modeFromRefs("key", "tok"), "JWT wins when both set")
}
