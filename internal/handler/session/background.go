package session

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ackBackgroundNotices acknowledges the background notices a persisted
// assistant message carries: the ones injected into its turn and the ones its
// work tools returned. Until the message is saved they stay in the inbox, so a
// turn that never reached history delivers them again next run. ctx carries
// the session's tenant, as on every completeAssistantMessage path.
func (h *Handler) ackBackgroundNotices(ctx context.Context, msg *types.Message) {
	if h.background == nil || h.background.Inbox == nil || msg == nil {
		return
	}
	ids := backgroundNoticeIDs(msg.AgentSteps)
	if len(ids) == 0 {
		return
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || tenantID == 0 {
		return
	}
	ref := interfaces.SessionRef{TenantID: tenantID, SessionID: msg.SessionID}
	if err := h.background.Inbox.Ack(ctx, ref, ids); err != nil {
		logger.Warnf(ctx, "ack background notices for message %s failed: %v", msg.ID, err)
	}
}

// backgroundNoticeIDs collects every notice ID the steps delivered or showed,
// once each, in order.
func backgroundNoticeIDs(steps []types.AgentStep) []string {
	var ids []string
	seen := make(map[string]bool)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, step := range steps {
		for _, injection := range step.BackgroundInjections {
			for _, id := range injection.AckIDs {
				add(id)
			}
		}
		for _, id := range step.BackgroundSeenIDs {
			add(id)
		}
	}
	return ids
}

// notifyBackgroundStop tells background features the user stopped the turn.
// Each feature decides what that means for its own work.
func (h *Handler) notifyBackgroundStop(ctx context.Context, session *types.Session) {
	if h.background == nil || h.background.Listeners == nil || session == nil {
		return
	}
	h.background.Listeners.NotifyStop(context.WithoutCancel(ctx),
		interfaces.SessionRef{TenantID: session.TenantID, SessionID: session.ID})
}
