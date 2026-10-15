package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type assistantMutationApproval struct {
	approved bool
	requests int
}

func (a *assistantMutationApproval) RequestBuiltinApproval(
	context.Context, approval.PendingRequest,
) (approval.Decision, error) {
	a.requests++
	return approval.Decision{Approved: a.approved, PendingID: "approval-1"}, nil
}

func (a *assistantMutationApproval) ResumeDecision(
	context.Context, *types.AgentPrompt, *event.EventBus,
) approval.Decision {
	return approval.Decision{Approved: a.approved, PendingID: "approval-1"}
}

func (*assistantMutationApproval) MarkExecuted(context.Context, string) {}

// Each case is run against real persistence, so a denied call must preserve
// both current content and history, not merely return a failure result.
var assistantMutationCases = []struct {
	name     string
	level    types.AssistantAccessLevel
	noGate   bool
	approved bool
	writes   bool
	requests int
}{
	{"read only", types.AssistantAccessReadOnly, false, true, false, 0},
	{"confirm declined", types.AssistantAccessConfirm, false, false, false, 1},
	{"safe auto without broker", types.AssistantAccessAutoWrite, true, false, false, 0},
	{"safe auto declined", types.AssistantAccessAutoWrite, false, false, false, 1},
	{"safe auto approved", types.AssistantAccessAutoWrite, false, true, true, 1},
	{"full auto without broker", types.AssistantAccessFullAuto, true, false, true, 0},
	{"full auto does not ask", types.AssistantAccessFullAuto, false, false, true, 0},
}

func assistantMutationContext(ctx context.Context) context.Context {
	return tools.WithToolExecContext(ctx, &tools.ToolExecContext{
		SessionID: "session-1", UserID: "web_user:editor", ToolCallID: "call-1",
		Interactive: true, EventBus: event.NewEventBus(),
	})
}

func TestAssistantWikiRenameRequiresApprovalBeforeDeletingHistory(t *testing.T) {
	for _, tc := range assistantMutationCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, h, db := newWikiRevisionTestService(t)
			page, err := h.svc.CreatePage(ctx, &types.WikiPage{
				KnowledgeBaseID: "kb-rev", TenantID: 1, Slug: "concept/old",
				Title: "Original", PageType: types.WikiPageTypeConcept, Content: "v1",
			})
			require.NoError(t, err)
			edit := *page
			edit.Content = "v2"
			_, err = h.svc.UpdatePage(ctx, &edit)
			require.NoError(t, err)
			gate := &assistantMutationApproval{approved: tc.approved}
			var broker approval.BuiltinApproval = gate
			if tc.noGate {
				broker = nil
			}
			registry := tools.NewToolRegistry()
			registry.SetAssistantAccess(tc.level, broker)
			registry.RegisterTool(tools.NewWikiRenamePageTool(h.svc, []string{"kb-rev"}))
			result, err := registry.ExecuteTool(assistantMutationContext(ctx), tools.ToolWikiRenamePage,
				json.RawMessage(`{"slug":"concept/old","new_slug":"concept/new"}`))
			if tc.level != types.AssistantAccessReadOnly {
				require.NoError(t, err)
			}
			require.Equal(t, tc.writes, result.Success, result.Error)
			require.Equal(t, tc.requests, gate.requests)
			var revisions int64
			require.NoError(t, db.Model(&types.WikiPageRevision{}).
				Where("page_id = ?", page.ID).Count(&revisions).Error)
			if tc.writes {
				renamed, err := h.svc.GetPageBySlug(ctx, "kb-rev", "concept/new")
				require.NoError(t, err)
				require.Equal(t, "v2", renamed.Content)
				_, err = h.svc.GetPageBySlug(ctx, "kb-rev", "concept/old")
				require.Error(t, err)
			} else {
				original, err := h.svc.GetPageBySlug(ctx, "kb-rev", "concept/old")
				require.NoError(t, err)
				require.Equal(t, "v2", original.Content)
				require.Equal(t, 2, original.Version)
				require.EqualValues(t, 1, revisions)
				revision, err := h.svc.GetRevision(ctx, "kb-rev", "concept/old", 1)
				require.NoError(t, err)
				require.Equal(t, "v1", revision.Content)
				_, err = h.svc.GetPageBySlug(ctx, "kb-rev", "concept/new")
				require.Error(t, err)
			}
		})
	}
}

type assistantMutationDispatch func(context.Context, operations.Identity, operations.Call) (*operations.Response, error)

func (d assistantMutationDispatch) Dispatch(
	ctx context.Context, identity operations.Identity, call operations.Call,
) (*operations.Response, error) {
	return d(ctx, identity, call)
}

func TestAssistantManualOverwriteRequiresApprovalWithoutBackup(t *testing.T) {
	for _, tc := range assistantMutationCases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			row, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			require.NoError(t, row.SetManualMetadata(types.NewManualKnowledgeMetadata(
				"original draft", types.ManualKnowledgeStatusDraft, 1)))
			require.NoError(t, f.repo.UpdateKnowledge(f.ctx, row))
			writes := 0
			// Use the real catalog and persistence service; substitute only HTTP
			// routing so the test needs no running server or external services.
			dispatch := assistantMutationDispatch(func(
				ctx context.Context, _ operations.Identity, call operations.Call,
			) (*operations.Response, error) {
				var data *types.Knowledge
				var err error
				if call.Method == http.MethodGet {
					require.Equal(t, "/api/v1/knowledge/doc", call.Path)
					data, err = f.repo.GetKnowledgeByID(ctx, 7, "doc")
				} else {
					require.Equal(t, http.MethodPut, call.Method)
					require.Equal(t, "/api/v1/knowledge/manual/doc", call.Path)
					var payload types.ManualKnowledgePayload
					require.NoError(t, json.Unmarshal(call.Body, &payload))
					writes++
					data, err = f.svc.UpdateManualKnowledge(ctx, "doc", &payload)
				}
				if err != nil {
					return nil, err
				}
				body, err := json.Marshal(map[string]any{"success": true, "data": data})
				return &operations.Response{Status: http.StatusOK, Body: body}, err
			})
			gate := &assistantMutationApproval{approved: tc.approved}
			var broker approval.BuiltinApproval = gate
			if tc.noGate {
				broker = nil
			}
			tool := tools.NewRunOperationTool(tools.OperationsRuntime{
				Catalog: operations.Builtin(), Dispatcher: dispatch, Gate: broker,
				Access:   operations.Access{Level: tc.level},
				Identity: operations.Identity{UserID: "editor", TenantID: 7},
			})
			result, err := tool.Execute(assistantMutationContext(f.ctx), json.RawMessage(
				`{"operation":"knowledge.update_manual","params":{"knowledge_id":"doc",`+
					`"title":"Changed","content":"replacement draft","status":"draft"}}`))
			require.NoError(t, err)
			require.Equal(t, tc.writes, result.Success, result.Error)
			require.Equal(t, tc.requests, gate.requests)
			stored, err := f.repo.GetKnowledgeByID(f.ctx, 7, "doc")
			require.NoError(t, err)
			meta, err := stored.ManualMetadata()
			require.NoError(t, err)
			if tc.writes {
				require.Equal(t, 1, writes)
				require.Equal(t, "replacement draft", meta.Content)
				require.Equal(t, 2, meta.Version)
			} else {
				require.Zero(t, writes)
				require.Equal(t, "original draft", meta.Content)
				require.Equal(t, 1, meta.Version)
				require.Equal(t, "original", stored.Title)
			}
		})
	}
}
