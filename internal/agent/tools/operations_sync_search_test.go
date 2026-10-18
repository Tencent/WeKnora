package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncSettingsApprovalAndAccessPolicy(t *testing.T) {
	for _, level := range []types.AssistantAccessLevel{
		"",
		types.AssistantAccessAutoWrite,
		types.AssistantAccessReadOnly,
	} {
		for _, approved := range []bool{false, true} {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/datasource/ds1": {
					Status: 200,
					Body:   []byte(`{"id":"ds1","name":"Team docs","knowledge_base_id":"kb1"}`),
				},
				"PATCH /api/v1/datasource/ds1/sync-settings": {
					Status: 200,
					Body:   []byte(`{"id":"ds1","knowledge_base_id":"kb1","sync_deletions":true}`),
				},
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: approved, PendingID: "p1"}}
			tool := newRunTool(d, gate)
			tool.rt.Access.Level = level
			result, err := tool.Execute(
				interactiveCtx(),
				runArgs(
					"data_source.update_sync",
					map[string]any{
						"data_source_id": "ds1",
						"sync_schedule":  "",
						"sync_deletions": true,
						"resource_ids":   []string{"folder-a", "folder-b"},
					},
				),
			)
			require.NoError(t, err)
			if level == types.AssistantAccessReadOnly {
				assert.False(t, result.Success)
				assert.Empty(t, d.calls)
				assert.Empty(t, gate.requests)
				continue
			}
			if level == types.AssistantAccessAutoWrite {
				assert.True(t, result.Success, result.Error)
				assert.Empty(t, gate.requests)
			} else {
				require.Len(t, gate.requests, 1)
				for _, text := range []string{
					"Team docs",
					"manual only",
					"Future syncs may delete",
					"folder-a, folder-b",
				} {
					assert.Contains(t, gate.requests[0].Description, text)
				}
				assert.Equal(t, approved, result.Success, result.Error)
			}
			if result.Success {
				require.NotEmpty(t, result.Data)
				assert.JSONEq(
					t,
					`{"sync_schedule":"","sync_deletions":true,"resource_ids":["folder-a","folder-b"]}`,
					string(d.calls[len(d.calls)-1].call.Body),
				)
			} else {
				assert.Len(t, d.calls, 1)
				assert.Empty(t, result.Data)
			}
		}
	}
}

func TestSearchDefaultApprovalNamesImpact(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/web-search-providers/p1": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"p1","name":"Team search"}}`),
		},
		"PATCH /api/v1/web-search-providers/p1": {Status: 200, Body: []byte(`{"data":{"id":"p1","is_default":true}}`)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	result, err := newRunTool(
		d,
		gate,
	).Execute(
		interactiveCtx(),
		runArgs(
			"web_search_provider.update",
			map[string]any{
				"web_search_provider_id": "p1",
				"is_default":             true,
			},
		),
	)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Len(t, gate.requests, 1)
	assert.Contains(t, gate.requests[0].Description, "Team search")
	assert.Contains(t, gate.requests[0].Description, "affects agents that follow the default")
	require.NotEmpty(t, result.Data)
}
