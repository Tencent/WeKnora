package adapter

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAssistantLiteFileAccessLevels(t *testing.T) {
	for _, level := range []types.AssistantAccessLevel{
		types.AssistantAccessReadOnly, types.AssistantAccessConfirm,
		types.AssistantAccessAutoWrite, types.AssistantAccessFullAuto,
	} {
		t.Run(string(level), func(t *testing.T) {
			a := New(testService(t, &stubBackend{}))
			ctx := tools.WithToolExecContext(context.Background(), &tools.ToolExecContext{SessionID: "s1"})
			require.NoError(t, a.WriteSessionWorkspaceFile(ctx, "s1", "notes.txt", []byte("original")))
			r := tools.NewToolRegistry()
			r.SetAssistantAccess(level, nil)
			r.RegisterTool(tools.NewWriteSandboxFileTool(a, 0))
			r.RegisterTool(tools.NewEditSandboxFileTool(a))
			r.BindSession("s1")
			for _, tc := range []struct{ name, args string }{
				{tools.ToolWriteSandboxFile, `{"path":"notes.txt","content":""}`},
				{tools.ToolWriteSandboxFile, `{"path":"notes.txt","content":"changed","mode":"append"}`},
				{
					tools.ToolEditSandboxFile,
					`{"path":"notes.txt","edits":[{"old_string":"original","new_string":"edited"}]}`,
				},
			} {
				require.NoError(t, a.WriteSessionWorkspaceFile(ctx, "s1", "notes.txt", []byte("original")))
				result, err := r.ExecuteTool(ctx, tc.name, json.RawMessage(tc.args))
				if level != types.AssistantAccessReadOnly {
					require.NoError(t, err)
				}
				require.Equal(t, level == types.AssistantAccessFullAuto, result.Success, result.Error)
				data, err := a.ReadSessionFile(ctx, "s1", "notes.txt")
				require.NoError(t, err)
				if level != types.AssistantAccessFullAuto {
					require.Equal(t, "original", string(data))
				} else {
					require.NotEqual(t, "original", string(data))
				}
			}
			res, err := r.ExecuteTool(ctx, tools.ToolWriteSandboxFile,
				json.RawMessage(`{"path":"new.txt","content":"new"}`))
			if level != types.AssistantAccessReadOnly {
				require.NoError(t, err)
			}
			canCreate := level == types.AssistantAccessAutoWrite || level == types.AssistantAccessFullAuto
			require.Equal(t, canCreate, res.Success, res.Error)
			// A second call now targets an existing file; safe auto must no longer write.
			res, err = r.ExecuteTool(ctx, tools.ToolWriteSandboxFile,
				json.RawMessage(`{"path":"new.txt","content":"replacement"}`))
			if level != types.AssistantAccessReadOnly {
				require.NoError(t, err)
			}
			require.Equal(t, level == types.AssistantAccessFullAuto, res.Success, res.Error)
			res, _ = r.ExecuteTool(ctx, tools.ToolWriteSandboxFile,
				json.RawMessage(`{"path":"/outside-workspace.txt","content":"no"}`))
			require.False(t, res.Success, "full auto must retain path restrictions")
		})
	}
}
