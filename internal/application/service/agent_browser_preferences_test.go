package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestBrowserPreferencesSaveReloadResetAndUserIsolation(t *testing.T) {
	tenant := uint64(7)
	repo := &switchTenantUserRepo{users: map[string]types.User{
		"alice": {ID: "alice", Preferences: types.UserPreferences{LastActiveTenantID: &tenant}},
		"bob":   {ID: "bob"},
	}}
	users := &userService{userRepo: repo}
	agents := &agentService{userRepo: repo}
	alice := context.WithValue(t.Context(), types.UserIDContextKey, "alice")
	bob := context.WithValue(t.Context(), types.UserIDContextKey, "bob")
	custom := "Use DuckDuckGo: https://duckduckgo.com/?q={query}"
	prefs, err := users.UpdateUserPreferences(alice, "alice", types.UserPreferences{BrowserSearchInstructions: &custom})
	require.NoError(t, err)
	require.Equal(t, &tenant, prefs.LastActiveTenantID)
	// Persisted preferences survive the same JSON round-trip as the DB column.
	raw, err := prefs.Value()
	require.NoError(t, err)
	var decoded types.UserPreferences
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, custom, decoded.EffectiveBrowserSearchInstructions())
	_, err = users.UpdateUserPreferences(alice, "alice", types.UserPreferences{LastActiveTenantID: &tenant})
	require.NoError(t, err)
	got, err := agents.browserSearchInstructions(alice)
	require.NoError(t, err)
	require.Equal(t, custom, got, "omitting the field preserves the saved preference")
	tool := tools.NewBrowserSkillTool(nil, browserskill.Scope{Tenant: 7, User: "alice"}, "chat", got)
	require.Contains(t, tool.Description(), custom)
	require.NotContains(t, tool.Description(), "bing.com", "custom instructions replace the default, not append to it")
	got, err = agents.browserSearchInstructions(bob)
	require.NoError(t, err)
	require.Equal(t, types.DefaultBrowserSearchInstructions, got)
	for _, reset := range []string{"  ", types.DefaultBrowserSearchInstructions} {
		prefs, err = users.UpdateUserPreferences(alice, "alice", types.UserPreferences{
			BrowserSearchInstructions: &reset,
		})
		require.NoError(t, err)
		require.Nil(t, prefs.BrowserSearchInstructions)
		got, err = agents.browserSearchInstructions(alice)
		require.NoError(t, err)
		require.Equal(t, types.DefaultBrowserSearchInstructions, got)
	}
	tooLong := strings.Repeat("中", types.MaxBrowserSearchInstructionsLength+1)
	_, err = users.UpdateUserPreferences(alice, "alice", types.UserPreferences{BrowserSearchInstructions: &tooLong})
	require.ErrorContains(t, err, "4000")
	require.Nil(t, repo.users["alice"].Preferences.BrowserSearchInstructions)
	_, err = agents.browserSearchInstructions(context.WithValue(alice, types.UserIDContextKey, "missing"))
	require.ErrorContains(t, err, "load browser search preferences",
		"lookup failure must not silently switch the user's search engine")
}
