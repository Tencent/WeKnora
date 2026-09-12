package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	chatpipeline "github.com/Tencent/WeKnora/internal/application/service/chat_pipeline"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type cardPipelineRecorder struct {
	events []types.EventType
	manage *types.ChatManage
}

func (*cardPipelineRecorder) ActivationEvents() []types.EventType {
	return []types.EventType{types.QUERY_UNDERSTAND, types.CHUNK_SEARCH_PARALLEL, types.WEB_FETCH, types.DATA_ANALYSIS}
}

func (p *cardPipelineRecorder) OnEvent(
	_ context.Context, kind types.EventType, cm *types.ChatManage, next func() *chatpipeline.PluginError,
) *chatpipeline.PluginError {
	p.events = append(p.events, kind)
	p.manage = cm
	if kind == types.CHUNK_SEARCH_PARALLEL {
		cm.SearchResult = []*types.SearchResult{{
			ID: "chunk-1", KnowledgeID: "doc-1", KnowledgeBaseID: "doc-kb", Content: "knowledge evidence",
		}}
		cm.MergeResult = cm.SearchResult
	}
	return next()
}

func TestCardMaterialQACapabilitiesMatchOrdinaryRequest(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "enabled"
		if !enabled {
			name = "disabled"
		}
		t.Run(name, func(t *testing.T) {
			var ordinaryAgentConfig *types.AgentConfig
			var ordinaryPipeline types.PipelineRequest
			for _, material := range []struct{ name, context string }{
				{"ordinary", ""},
				{"file_reference", "FILE RAW: quarterly report"},
				{"card_reference", "CARD RAW: 9007199254740993; https://card.example/action"},
			} {
				t.Logf("material: %s", material.name)
				svc := newTagTargetSessionService()
				model := &captureChatModel{}
				svc.modelService = &stubModelService{chatModel: model, modelsByID: map[string]*types.Model{
					"chat": {Type: types.ModelTypeKnowledgeQA},
				}}
				svc.cfg = &config.Config{Conversation: &config.ConversationConfig{
					Summary: &config.SummaryConfig{ContextTemplate: "{{query}}\n{{contexts}}"},
				}}
				svc.eventManager = chatpipeline.NewEventManager()
				recorder := &cardPipelineRecorder{}
				svc.eventManager.Register(recorder)
				chatpipeline.NewPluginIntoChatMessage(svc.eventManager, nil)
				chatpipeline.NewPluginChatCompletionStream(svc.eventManager, svc.modelService)
				req := &types.QARequest{
					Session: &types.Session{ID: "material-session", TenantID: 100},
					Query:   "请打开材料链接，结合知识库分析文件数据", KnowledgeBaseIDs: []string{"doc-kb"},
					WebSearchEnabled: enabled, LocalBrowserEnabled: enabled,
					SkillNames: []string{"report", "outside-skill"}, MCPServiceIDs: []string{"mcp-b", "outside-mcp"},
					QuotedContext: material.context, RewriteContext: material.context,
					CustomAgent: &types.CustomAgent{ID: "agent", TenantID: 100, Config: types.CustomAgentConfig{
						ModelID: "chat", WebSearchProviderID: "web", WebSearchEnabled: true,
						WebFetchEnabled: enabled, DataAnalysisEnabled: enabled, EnableRewrite: true,
						SkillsSelectionMode: "selected", SelectedSkills: []string{"analysis", "report"},
						SandboxConfigID:  "sandbox",
						MCPSelectionMode: "selected", MCPServices: []string{"mcp-a", "mcp-b"},
						AllowedTools: []string{
							tools.ToolKnowledgeSearch, tools.ToolWebSearch, tools.ToolWebFetch, tools.ToolDataAnalysis,
						},
					}},
				}
				if material.context != "" {
					req.Attachments = types.MessageAttachments{{
						FileName: "direct.txt", Content: "direct attachment content",
					}}
				}

				agentConfig, err := svc.buildAgentConfig(tagTargetContext(), req, &types.Tenant{ID: 100}, 100)
				require.NoError(t, err)
				require.Equal(t, enabled, agentConfig.WebSearchEnabled)
				require.Equal(t, enabled, agentConfig.LocalBrowserEnabled)
				require.True(t, agentConfig.SkillsEnabled)
				require.Equal(t, "sandbox", agentConfig.SandboxConfigID)
				require.Equal(t, req.CustomAgent.Config.AllowedTools, agentConfig.AllowedTools)
				require.Equal(t, req.CustomAgent.Config.SelectedSkills, agentConfig.AllowedSkills)
				require.Equal(t, []string{"report"}, agentConfig.PinnedSkillNames)
				require.Equal(t, req.CustomAgent.Config.MCPServices, agentConfig.MCPServices)
				require.Equal(t, []string{"mcp-b"}, agentConfig.PinnedMCPServiceIDs)
				require.Equal(t, []string{"doc-kb"}, agentConfig.KnowledgeBases)
				require.Len(t, agentConfig.SearchTargets, 1)
				if material.name == "ordinary" {
					ordinaryAgentConfig = agentConfig
				} else {
					require.Equal(t, ordinaryAgentConfig, agentConfig)
				}

				require.NoError(t, svc.KnowledgeQA(tagTargetContext(), req, event.NewEventBus()))
				wantEvents := []types.EventType{types.QUERY_UNDERSTAND, types.CHUNK_SEARCH_PARALLEL}
				if enabled {
					wantEvents = append(wantEvents, types.WEB_FETCH, types.DATA_ANALYSIS)
				}
				require.Equal(t, wantEvents, recorder.events)
				require.Equal(t, enabled, recorder.manage.WebSearchEnabled)
				require.Equal(t, enabled, recorder.manage.WebFetchEnabled)
				require.Equal(t, enabled, recorder.manage.DataAnalysisEnabled)
				require.Equal(t, req.Query, recorder.manage.Query)
				require.Equal(t, req.RewriteContext, recorder.manage.RewriteContext)
				require.Equal(t, req.Attachments, recorder.manage.Attachments)
				require.Len(t, model.lastMessages, 2)
				content := model.lastMessages[1].Content
				for _, expected := range []string{req.Query, "knowledge evidence", material.context} {
					require.Contains(t, content, expected)
				}
				if material.context != "" {
					require.Contains(t, content, "direct attachment content")
				}
				pipeline := recorder.manage.PipelineRequest
				pipeline.RewriteContext, pipeline.Attachments = "", nil
				if material.name == "ordinary" {
					ordinaryPipeline = pipeline
				} else {
					require.Equal(t, ordinaryPipeline, pipeline)
				}
			}
		})
	}
}
