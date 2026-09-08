package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSharedAgentKBScope(t *testing.T) {
	for _, tt := range []struct {
		name, mode         string
		ids                []string
		all, empty, allows bool
	}{
		{name: "all", mode: "all", all: true, allows: true},
		{name: "selected", mode: "selected", ids: []string{"kb"}, allows: true},
		{name: "other selection", mode: "selected", ids: []string{"other"}},
		{name: "empty selection", mode: "selected", empty: true},
		{name: "blank selection", mode: "selected", ids: []string{""}, empty: true},
		{name: "none", mode: "none", ids: []string{"kb"}, empty: true},
		{name: "unknown mode", mode: "unknown", ids: []string{"kb"}, empty: true},
		{name: "missing mode", empty: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			agent := &CustomAgent{
				TenantID: 2,
				Config:   CustomAgentConfig{KBSelectionMode: tt.mode, KnowledgeBases: tt.ids},
			}
			scope := NewSharedAgentKBScope(agent)
			require.Equal(t, tt.all, scope.IsAll())
			require.Equal(t, tt.empty, scope.IsEmpty())
			require.Equal(t, tt.allows, scope.Allows("kb", 2))
			require.False(t, scope.Allows("kb", 3), "even all is tenant-bound")
			require.False(t, scope.Allows("", 2))
			require.Equal(t, tt.allows, SharedAgentIncludesKB(agent, &KnowledgeBase{ID: "kb", TenantID: 2}))
		})
	}
	require.True(t, NewSharedAgentKBScope(nil).IsEmpty())
	require.False(t, SharedAgentIncludesKB(nil, &KnowledgeBase{ID: "kb", TenantID: 2}))
}

func TestSharedAgentKBScopeCopiesSelection(t *testing.T) {
	agent := &CustomAgent{
		TenantID: 2,
		Config:   CustomAgentConfig{KBSelectionMode: "selected", KnowledgeBases: []string{"kb", "", "kb"}},
	}
	scope := NewSharedAgentKBScope(agent)
	require.Equal(t, []string{"kb"}, scope.IDs())
	agent.Config.KnowledgeBases[0] = "other"
	ids := scope.IDs()
	ids[0] = "other"
	require.True(t, scope.Allows("kb", 2))
	require.False(t, scope.Allows("other", 2))
}
