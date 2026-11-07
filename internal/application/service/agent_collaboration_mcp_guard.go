package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
)

const delegatedCallInProgressError = "A child task is already handling this call; wait for its result."

// guardDelegatedMCPCall coordinates a parent's new call with approvals already
// owned by its active children. It neither shares approval nor claims that an
// approved tool succeeded. All checks are limited to this parent execution.
func (s *AgentCollaboration) guardDelegatedMCPCall(
	ctx context.Context,
	b *collaboration.Binding,
	serviceID, toolName string,
	args json.RawMessage,
) (*types.ToolResult, error) {
	if b == nil || b.Child != nil || s.broker == nil {
		return nil, nil
	}
	caller := types.CallerFromContext(ctx)
	meta, ok := tools.ToolExecFromContext(ctx)
	if caller.TenantID != b.Scope.TenantID || caller.UserID != b.Scope.UserID || !ok ||
		meta.SessionID != b.Scope.SessionID {
		return nil, nil
	}
	if err := b.Runtime.CheckParent(ctx, b.Scope, b.ParentOwner); err != nil {
		return nil, err
	}
	g, err := b.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return nil, err
	}
	parentMessageID := g.ParentMessageID
	if parentMessageID == "" {
		parentMessageID = g.Scope.ID
	}
	if meta.AssistantMessageID != parentMessageID {
		return nil, nil
	}
	for _, m := range g.Members {
		if len(m.Runs) == 0 {
			continue
		}
		r := m.Runs[len(m.Runs)-1]
		if r.CancelRequested || r.PromptID == "" ||
			(r.State != "queued" && r.State != "running" && r.State != "waiting") {
			continue
		}
		p, err := s.broker.Store().Get(ctx, r.PromptID)
		if err != nil {
			return nil, err
		}
		if !matchesDelegatedMCPApproval(p, b.Scope, m, r, serviceID, toolName, args) {
			continue
		}
		// Approval lookup is outside the collaboration CAS. Recheck the current
		// run so completion, cancellation or a followup cannot leave a stale fence.
		latest, err := b.Runtime.Store.Read(ctx, b.Scope)
		if err != nil {
			return nil, err
		}
		if latest.Status != "active" {
			return nil, collaboration.ErrClosed
		}
		if latest.ParentOwner != b.ParentOwner || latest.ParentMessageID != g.ParentMessageID ||
			time.Now().After(latest.ParentLease) {
			return nil, collaboration.ErrConflict
		}
		stillActive := false
		for _, candidate := range latest.Members {
			if candidate.ID != m.ID || len(candidate.Runs) == 0 {
				continue
			}
			current := candidate.Runs[len(candidate.Runs)-1]
			stillActive = current.ID == r.ID && current.PromptID == p.ID && !current.CancelRequested &&
				(current.State == "queued" || current.State == "running" || current.State == "waiting")
		}
		if !stillActive {
			continue
		}
		// A user may have canceled the prompt or edited arguments while the
		// group was being checked. Do not fence using that earlier prompt copy.
		p, err = s.broker.Store().Get(ctx, r.PromptID)
		if err != nil {
			return nil, err
		}
		if !matchesDelegatedMCPApproval(p, b.Scope, m, r, serviceID, toolName, args) {
			continue
		}
		return &types.ToolResult{
			Success: false,
			Error:   delegatedCallInProgressError,
			Data: map[string]interface{}{
				"code": "delegated_call_in_progress", "task_name": m.Name,
				"run_id": r.ID, "status": r.State,
			},
		}, nil
	}
	return nil, nil
}

func matchesDelegatedMCPApproval(
	p *types.AgentPrompt,
	scope collaboration.Scope,
	m *collaboration.Member,
	r *collaboration.Run,
	serviceID, toolName string,
	args json.RawMessage,
) bool {
	if p == nil || p.Kind != types.AgentPromptKindToolApproval || p.TenantID != scope.TenantID ||
		p.UserID != delegatedPromptUser(scope.UserID) ||
		p.SessionID != m.SessionID ||
		p.AssistantMessageID != r.MessageID {
		return false
	}
	var request types.ToolApprovalPromptRequest
	if json.Unmarshal(p.Request, &request) != nil || request.ServiceID != serviceID || request.MCPToolName != toolName {
		return false
	}
	var response types.ToolApprovalPromptResponse
	_ = json.Unmarshal(p.Response, &response)
	switch p.Status {
	case types.AgentPromptPending, types.AgentPromptSuspended, types.AgentPromptAnswered, types.AgentPromptExecuting:
	case types.AgentPromptDone:
		if !response.Approved {
			return false
		}
	default:
		return false
	}
	effectiveArgs := request.Args
	if response.Approved && len(response.ModifiedArgs) > 0 {
		effectiveArgs = response.ModifiedArgs
	}
	return sameMCPArguments(args, effectiveArgs)
}

func sameMCPArguments(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		var value any
		if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
			return nil, false
		}
		return value, true
	}
	left, leftOK := decode(a)
	right, rightOK := decode(b)
	return leftOK && rightOK && equalMCPJSON(left, right)
}

func equalMCPJSON(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		other, ok := b.(json.Number)
		if !ok {
			return false
		}
		return canonicalMCPNumber(string(a)) == canonicalMCPNumber(string(other))
	case map[string]any:
		other, ok := b.(map[string]any)
		if !ok || len(a) != len(other) {
			return false
		}
		for key, value := range a {
			v, exists := other[key]
			if !exists || !equalMCPJSON(value, v) {
				return false
			}
		}
		return true
	case []any:
		other, ok := b.([]any)
		if !ok || len(a) != len(other) {
			return false
		}
		for i, value := range a {
			if !equalMCPJSON(value, other[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Normalize decimal tokens without expanding large exponents or rounding large
// integer IDs through float64. JSON decoding has already validated the grammar.
func canonicalMCPNumber(raw string) string {
	mantissa, exponent := raw, new(big.Int)
	if i := strings.IndexAny(raw, "eE"); i >= 0 {
		mantissa = raw[:i]
		exponent.SetString(raw[i+1:], 10)
	}
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(mantissa)-i-1)))
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	mantissa = strings.TrimLeft(mantissa, "0")
	if mantissa == "" {
		return "0"
	}
	digits := strings.TrimRight(mantissa, "0")
	exponent.Add(exponent, big.NewInt(int64(len(mantissa)-len(digits))))
	return sign + digits + "e" + exponent.String()
}
