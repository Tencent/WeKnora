package chatpipeline

import (
	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type PluginLoadHistory struct {
	messageService interfaces.MessageService
	config         *config.Config
}

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

func (p *PluginLoadHistory) ActivationEvents() []types.EventType {
	return []types.EventType{types.LOAD_HISTORY}
}

func (p *PluginLoadHistory) OnEvent(ctx context.Context,
	eventType types.EventType, chatManage *types.ChatManage, next func() *PluginError,
) *PluginError {
	maxRounds := p.config.Conversation.MaxRounds
	if chatManage.MaxRounds > 0 {
		maxRounds = chatManage.MaxRounds
	}

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
