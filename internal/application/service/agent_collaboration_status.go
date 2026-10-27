package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// publishStatus only exposes the explicit public projection. A snapshot may
// arrive after a newer commit; its durable revision lets clients discard it.
func (s *AgentCollaboration) publishStatus(ctx context.Context, g *collaboration.Group) {
	if s.streams == nil || len(g.Members) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// Read again to fence a parent takeover and to coalesce concurrent commits.
	current, err := s.Runtime.Store.Read(ctx, g.Scope)
	if err != nil {
		return
	}
	view := collaborationStatusView(current)
	messageID := current.ParentMessageID
	if messageID == "" {
		messageID = current.Scope.ID
	}
	_ = s.streams.AppendEvent(ctx, current.Scope.SessionID, messageID, interfaces.StreamEvent{
		ID:   fmt.Sprintf("collaboration:%s:%d", view.GroupID, view.Revision),
		Type: types.ResponseType("collaboration_status"), Timestamp: time.Now(),
		Data: map[string]interface{}{"group_id": view.GroupID, "revision": view.Revision, "tasks": view.Tasks},
	})
}

// replayStatus restores the current state after a stream reset or parent
// recovery. It reads durable state rather than relying on process-local events.
func (s *AgentCollaboration) replayStatus(ctx context.Context, b *collaboration.Binding) {
	if b.Child != nil {
		return
	}
	g, err := s.Runtime.Store.Read(ctx, b.Scope)
	if err == nil {
		s.publishStatus(ctx, g)
	}
}

// HistoryStatuses restores UI state independently of Redis and live execution.
// The handler has authorized session access; this additional scope prevents a
// shared session read from exposing another user's delegated work.
func (s *AgentCollaboration) HistoryStatuses(
	ctx context.Context,
	sessionID string,
	messageIDs []string,
) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)
	if len(messageIDs) == 0 {
		return out, nil
	}
	wanted := make(map[string]bool, len(messageIDs))
	for _, id := range messageIDs {
		wanted[id] = true
	}
	caller := types.CallerFromContext(ctx)
	var rows []collaboration.Record
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ?",
		caller.TenantID,
		caller.UserID,
		sessionID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var g collaboration.Group
		if err := json.Unmarshal([]byte(row.Data), &g); err != nil {
			return nil, err
		}
		if len(g.Members) == 0 || (!wanted[row.ID] && !wanted[g.ParentMessageID]) {
			continue
		}
		g.Revision = row.Version
		view, err := json.Marshal(collaborationStatusView(&g))
		if err != nil {
			return nil, err
		}
		if wanted[row.ID] {
			out[row.ID] = view
		}
		if wanted[g.ParentMessageID] {
			out[g.ParentMessageID] = view
		}
	}
	return out, nil
}

// Template tasks use their pinned display name; temporary workers use the task name.
// Decode only the public name; never expose the rest of the template snapshot.
func collaborationStatusView(g *collaboration.Group) collaboration.StatusSnapshot {
	view := g.StatusView()
	for i := range view.Tasks {
		for _, member := range g.Members {
			if member.ID != view.Tasks[i].ID {
				continue
			}
			var label struct {
				Inherited json.RawMessage `json:"inherited"`
				Agent     struct {
					Name string `json:"name"`
				} `json:"agent"`
			}
			if json.Unmarshal([]byte(member.ConfigJSON), &label) == nil {
				view.Tasks[i].DisplayName = label.Agent.Name
				if len(label.Inherited) > 0 && string(label.Inherited) != "null" {
					view.Tasks[i].DisplayName = member.Name
				}
			}
			break
		}
	}
	return view
}
