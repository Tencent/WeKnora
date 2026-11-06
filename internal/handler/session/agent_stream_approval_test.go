package session

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAttachToolApprovalsMatchesTheToolCall(t *testing.T) {
	steps := []types.AgentStep{{
		ToolCalls: []types.ToolCall{{ID: "call-1"}, {ID: "call-2"}},
	}}
	records := []types.ToolApprovalRecord{
		{PendingID: "p2", ToolCallID: "call-2", Resolved: true, Approved: true},
		{PendingID: "p1", ToolCallID: "call-1", Resolved: true, Approved: false},
		{PendingID: "loose", Resolved: true},
	}

	got := attachToolApprovals(steps, records)
	if len(got) != 1 {
		t.Fatalf("steps = %d", len(got))
	}
	approvals := got[0].Approvals
	if len(approvals) != 3 {
		t.Fatalf("approvals = %#v", approvals)
	}
	if approvals[0].PendingID != "p2" || approvals[1].PendingID != "p1" || approvals[2].PendingID != "loose" {
		t.Fatalf("order = %#v", approvals)
	}
}

func TestHostApprovalSnapshotKeepsSessionRules(t *testing.T) {
	got := hostApprovalSnapshot(&event.HostApprovalPayload{
		Reason: "delete", Command: "rm -rf build", AllowSession: true,
		SessionRules: []string{"rm -f -r"},
	})
	if got == nil || len(got.SessionRules) != 1 || got.SessionRules[0] != "rm -f -r" {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestRememberedApprovalKeepsReviewerAndRisk(t *testing.T) {
	h := &AgentStreamHandler{}
	h.rememberToolApproval(event.ToolApprovalRequiredData{
		PendingID: "p1", ToolCallID: "c1", Kind: "host_command",
		Host: &event.HostApprovalPayload{
			Reason: "file_write", Reviewer: "model", Tool: "write_sandbox_file",
			FilePath: "/Users/dev/Desktop/a.md", WriteMode: "overwrite", WriteBytes: 12, WritePreview: "hello",
		},
	})
	h.resolveRememberedToolApproval(event.ToolApprovalResolvedData{PendingID: "p1", Approved: true, RiskLevel: "low"})

	rec := h.toolApprovals[0]
	require.Equal(t, "model", rec.Host.Reviewer)
	require.Equal(t, "/Users/dev/Desktop/a.md", rec.Host.FilePath)
	require.Equal(t, 12, rec.Host.WriteBytes)
	require.Equal(t, "low", rec.RiskLevel)
}
