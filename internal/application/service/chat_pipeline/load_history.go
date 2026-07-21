package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// PluginLoadHistory is an exported type.
type PluginLoadHistory struct {
	messageService interfaces.MessageService
	config         *config.Config
}

// NewPluginLoadHistory is an exported function.
func NewPluginLoadHistory(eventManager *EventManager,
	messageService interfaces.MessageService,
	config *config.Config,
) *PluginLoadHistory {
	res := &PluginLoadHistory{
		messageService: messageService,
		config:         config,
	}
	eventManager.Register(res)
	return res
}

// ActivationEvents implements the required interface method.
func (p *PluginLoadHistory) ActivationEvents() []types.Type {
	return []types.Type{types.LoadHistory}
}

// OnEvent implements the required interface method.
func (p *PluginLoadHistory) OnEvent(ctx context.Context,
	_ types.Type, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	// chatManage.MaxRounds == 0 means multi-turn is explicitly disabled
	// (e.g. by a custom agent with MultiTurnEnabled=false). Skip loading so
	// history doesn't leak into the LLM context. We do NOT fall back to the
	// global Conversation.MaxRounds default here, otherwise the disable flag
	// would be silently overridden.
	if chatManage.MaxRounds <= 0 {
		pipelineInfo(ctx, "LoadHistory", "skipped", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"reason":     "multi_turn_disabled",
		})
		return next()
	}
	maxRounds := chatManage.MaxRounds

	pipelineInfo(ctx, "LoadHistory", "input", map[string]interface{}{
		"session_id": chatManage.SessionID,
		"max_rounds": maxRounds,
	})

	historyList, err := loadAndProcessHistory(ctx, p.messageService, chatManage.SessionID, maxRounds, maxRounds*2+10)
	if err != nil {
		pipelineWarn(ctx, "LoadHistory", "history_fetch", map[string]interface{}{
			"session_id": chatManage.SessionID,
			"error":      err.Error(),
		})
		return next()
	}

	chatManage.History = historyList

	pipelineInfo(ctx, "LoadHistory", "output", map[string]interface{}{
		"session_id":     chatManage.SessionID,
		"history_rounds": len(historyList),
		"max_rounds":     maxRounds,
	})

	return next()
}
