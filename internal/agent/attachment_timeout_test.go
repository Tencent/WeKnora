package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type attachmentDeadlineStore struct{ deadline time.Time }

func (s *attachmentDeadlineStore) SessionAttachments(ctx context.Context) (types.MessageAttachments, error) {
	s.deadline, _ = ctx.Deadline()
	return types.MessageAttachments{{ID: "report"}}, nil
}

func (s *attachmentDeadlineStore) TemporaryDocument(context.Context, string) (*types.TemporaryDocument, error) {
	return &types.TemporaryDocument{ID: "report", Status: types.TemporaryDocumentStatusReady, Content: "body"}, nil
}

func TestAttachmentToolExecutionHonorsParseWaitAndParentDeadline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		wait   time.Duration
		parent time.Duration
		want   time.Duration
	}{
		{name: "configured wait above default", wait: 120 * time.Second, want: 125 * time.Second},
		{name: "default wait has return margin", wait: 60 * time.Second, want: 65 * time.Second},
		{name: "short wait keeps generic execution budget", wait: time.Second, want: 60 * time.Second},
		{name: "parent deadline still wins", wait: 120 * time.Second, parent: 5 * time.Second, want: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newTestEngine(t, &mockChat{})
			engine.toolRegistry = tools.NewToolRegistry()
			store := &attachmentDeadlineStore{}
			engine.toolRegistry.RegisterTool(
				tools.NewReadFileTool(nil).WithAttachments(attachment.NewReader(store, tc.wait, nil)))
			ctx := context.Background()
			if tc.parent > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.parent)
				defer cancel()
			}
			start := time.Now()
			call := engine.runToolCall(ctx, types.LLMToolCall{ID: "read", Function: types.FunctionCall{
				Name: tools.ToolReadFile, Arguments: `{"path":"attachment://report"}`,
			}}, 0, 0, 0, "session", "message")
			require.NotNil(t, call.Result)
			require.True(t, call.Result.Success, call.Result.Error)
			require.WithinDuration(t, start.Add(tc.want), store.deadline, time.Second)
		})
	}
}
