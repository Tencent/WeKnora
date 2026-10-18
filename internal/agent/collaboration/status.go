package collaboration

import "encoding/json"

// StatusSnapshot is the public projection of a durable collaboration. It never
// includes assignment text, provider configuration, leases or raw tool results.
type StatusSnapshot struct {
	GroupID  string       `json:"group_id"`
	Revision int64        `json:"revision"`
	Tasks    []TaskStatus `json:"tasks"`
}

// TaskStatus is the public current-run status of one child task.
type TaskStatus struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	DisplayName   string    `json:"display_name,omitempty"`
	AgentID       string    `json:"agent_id"`
	SessionID     string    `json:"session_id"`
	RunID         string    `json:"run_id"`
	Status        string    `json:"status"`
	Progress      *Progress `json:"progress,omitempty"`
	PromptID      string    `json:"prompt_id,omitempty"`
	LatestMessage string    `json:"latest_message,omitempty"`
}

// StatusView projects durable state into a public snapshot without private execution fields.
func (g *Group) StatusView() StatusSnapshot {
	out := StatusSnapshot{GroupID: g.Scope.ID, Revision: g.Revision, Tasks: make([]TaskStatus, 0, len(g.Members))}
	for _, m := range g.Members {
		if m.Inherited {
			continue
		}
		r := current(m)
		if r == nil {
			continue
		}
		task := TaskStatus{
			ID:        m.ID,
			Name:      m.Name,
			AgentID:   m.AgentID,
			SessionID: m.SessionID,
			RunID:     r.ID,
			Status:    r.State,
			PromptID:  r.PromptID,
		}
		if r.CancelRequested && active(r) {
			task.Status = "cancelling"
		}
		if r.Progress != nil {
			p := *r.Progress
			task.Progress = &p
		}
		for i := len(g.Events) - 1; i >= 0; i-- {
			e := g.Events[i]
			if e.From == m.Name && e.RunID == r.ID && e.Kind == "message" {
				task.LatestMessage = e.Content
				break
			}
		}
		out.Tasks = append(out.Tasks, task)
	}
	return out
}

// PromptState is an allowlisted projection of a broker record, never model text.
// It deliberately excludes arguments, user answers, credentials and descriptions.
type PromptState struct {
	ID         string `json:"id"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Service    string `json:"service,omitempty"`
	Tool       string `json:"tool,omitempty"`
	Decision   string `json:"decision,omitempty"`
}

// RuntimeNotice is internal model context, separate from tool output and final answers.
type RuntimeNotice struct {
	Type           string `json:"type"`
	Source         string `json:"source"`
	Task           string `json:"task,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	Sequence       int64  `json:"sequence"`
	PromptID       string `json:"prompt_id,omitempty"`
	PromptKind     string `json:"prompt_kind,omitempty"`
	ToolCallID     string `json:"tool_call_id,omitempty"`
	Service        string `json:"service,omitempty"`
	Tool           string `json:"tool,omitempty"`
	Decision       string `json:"decision,omitempty"`
	ExecutionState string `json:"execution_state,omitempty"`
	Message        string `json:"message,omitempty"`
}

// Prompt transitions enter the mailbox exactly once per state change. A resolved
// prompt only queues a continuation; it does not establish tool or task success.
func publishPromptState(g *Group, m *Member, r *Run, kind string) {
	g.NextSequence++
	notice := RuntimeNotice{
		Type: "prompt_required", Source: "runtime", Task: m.Name,
		RunID: r.ID, Sequence: g.NextSequence, PromptID: r.PromptID, ExecutionState: "waiting",
	}
	if kind == "resumed" {
		notice.Type, notice.ExecutionState = "prompt_resolved", "queued"
	}
	if p := r.Prompt; p != nil && p.ID == r.PromptID {
		notice.PromptKind, notice.ToolCallID, notice.Service, notice.Tool = p.Kind, p.ToolCallID, p.Service, p.Tool
		if p.Kind == "tool_approval" {
			notice.Type = "approval_required"
			if kind == "resumed" {
				notice.Type = "approval_resolved"
			}
		}
		if kind == "resumed" {
			notice.Decision = p.Decision
		}
	}
	content, _ := json.Marshal(notice)
	g.Events = append(
		g.Events,
		Event{Sequence: g.NextSequence, From: m.Name, RunID: r.ID, Kind: kind, Content: string(content)},
	)
}
