import type { CustomAgentConfig } from '@/api/agent'
import type { ModelConfig } from '@/api/model'

export type AgentNotReadyReasonKey = 'summary_model' | 'rerank_model' | 'allowed_tools'

/**
 * An agent is chat-ready only when it explicitly references a usable chat
 * model. Merely having some built-in/default model in the tenant must not hide
 * incomplete agent configuration.
 *
 * For shared agents the model belongs to the source tenant, so the caller can
 * require a non-empty ID but must leave existence validation to the backend.
 */
export function agentHasConfiguredChatModel(
  config: Pick<CustomAgentConfig, 'model_id'> | undefined,
  models: Pick<ModelConfig, 'id' | 'type'>[],
  sourceModelIsRemote = false,
): boolean {
  const modelID = config?.model_id?.trim()
  if (!modelID) return false
  if (sourceModelIsRemote) return true
  return models.some(model => model.type === 'KnowledgeQA' && model.id === modelID)
}

/**
 * Keep this aligned with backend agentRequiresRerankModel.
 *
 * Rerank is needed only when knowledge_search can actually run. Explicitly
 * disabling the knowledge-base scope makes KB tools ineffective, even if an
 * older agent configuration still contains knowledge_search in allowed_tools.
 */
export function agentRequiresRerankModel(
  config: Pick<CustomAgentConfig, 'kb_selection_mode' | 'allowed_tools'> | undefined,
): boolean {
  if (!config || config.kb_selection_mode === 'none') return false

  const allowedTools = config.allowed_tools || []
  // The backend falls back to DefaultAllowedTools when the list is empty,
  // and that default includes knowledge_search.
  if (allowedTools.length === 0) return true

  return allowedTools.includes('knowledge_search')
}

export function getAgentNotReadyReasonKeys(
  config: Pick<
    CustomAgentConfig,
    'model_id' | 'rerank_model_id' | 'kb_selection_mode' | 'allowed_tools' | 'agent_mode'
  > | undefined,
  models: Pick<ModelConfig, 'id' | 'type'>[],
  options: { isAgentMode: boolean; isSharedAgent: boolean },
): AgentNotReadyReasonKey[] {
  const reasons: AgentNotReadyReasonKey[] = []

  if (!agentHasConfiguredChatModel(config, models, options.isSharedAgent)) {
    reasons.push('summary_model')
  }

  if (options.isAgentMode && agentRequiresRerankModel(config)) {
    const rerankModelID = config?.rerank_model_id?.trim()
    const rerankExists = !!rerankModelID && (
      options.isSharedAgent
      || models.some(model => model.type === 'Rerank' && model.id === rerankModelID)
    )
    if (!rerankExists) {
      reasons.push('rerank_model')
    }
  }

  if (options.isAgentMode && (!config?.allowed_tools || config.allowed_tools.length === 0)) {
    reasons.push('allowed_tools')
  }

  return reasons
}

/** Map missing-config reasons to the agent editor section that fixes them. */
export function resolveAgentNotReadySection(reasons: AgentNotReadyReasonKey[]): string {
  if (reasons.includes('summary_model') || reasons.includes('rerank_model')) {
    return 'model'
  }
  if (reasons.includes('allowed_tools')) {
    return 'tools'
  }
  return 'model'
}

/** First missing item — used to highlight the corresponding editor field after navigation. */
export function resolveAgentNotReadyHighlight(
  reasons: AgentNotReadyReasonKey[],
): AgentNotReadyReasonKey | undefined {
  return reasons[0]
}
