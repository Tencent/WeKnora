/** Mirrors backend internal/models/provider.IsQwenThinkingModel */
export function isQwenThinkingModel(modelName: string): boolean {
  const lower = modelName.trim().toLowerCase()
  return (
    lower.startsWith('qwen3')
    || lower.startsWith('qwen-plus')
    || lower.startsWith('qwen-max')
    || lower.startsWith('qwen-turbo')
  )
}

/** Mirrors backend internal/models/provider.IsLKEAPDeepSeekR1Model */
export function isLkeapDeepSeekR1Model(modelName: string): boolean {
  return modelName.toLowerCase().includes('deepseek-r1')
}

/** Hy3/Hy4 chat templates use chat_template_kwargs.reasoning_effort. */
export function isHyReasoningEffortModel(modelName: string): boolean {
  const leaf = modelName.trim().toLowerCase().split('/').pop() || ''
  return /^hy(?:3|4)(?:$|[-_.])/.test(leaf)
}

/** GLM-5.x (Zhipu) uses top-level reasoning_effort; thinking is always on. */
export function isGlmReasoningEffortModel(modelName: string): boolean {
  const leaf = modelName.trim().toLowerCase().split('/').pop() || ''
  return leaf === 'glm-5' || /^glm-5[-._]/.test(leaf)
}

export type ThinkingControlValue =
  | 'none'
  | 'chat_template_kwargs'
  | 'enable_thinking'
  | 'thinking_type'
  | 'reasoning_effort'
  | 'glm_reasoning_effort'

const THINKING_CONTROL_VALUES: ThinkingControlValue[] = [
  'none',
  'chat_template_kwargs',
  'enable_thinking',
  'thinking_type',
  'reasoning_effort',
  'glm_reasoning_effort',
]

/**
 * Default thinking_control for provider+model.
 * Must stay aligned with chat.resolveProvider(...).Thinking() in provider.go.
 */
export function defaultThinkingControl(
  provider: string,
  modelName = '',
): ThinkingControlValue {
  const p = provider.trim().toLowerCase()
  const model = modelName.trim()

  switch (p) {
    case 'aliyun':
      return isQwenThinkingModel(model) ? 'enable_thinking' : 'none'
    case 'lkeap':
      // R1 系列后端不发 thinking 参数；其余（含未填模型名）按 LKEAP 的 thinking.type 格式预选
      if (model && isLkeapDeepSeekR1Model(model)) return 'none'
      return 'thinking_type'
    case 'generic':
    case 'nvidia':
    case 'litellm':
      if (isHyReasoningEffortModel(model) || isGlmReasoningEffortModel(model)) {
        return 'reasoning_effort'
      }
      return 'chat_template_kwargs'
    case 'volcengine':
      return 'thinking_type'
    case 'hunyuan':
      return isHyReasoningEffortModel(model) ? 'reasoning_effort' : 'none'
    case 'zhipu':
      return isGlmReasoningEffortModel(model) ? 'glm_reasoning_effort' : 'none'
    default:
      // openai, azure_openai, anthropic, deepseek, gemini, siliconflow,
      // moonshot, openrouter, weknoracloud, … → baseProvider / noThinking
      return 'none'
  }
}

export type ThinkingEffortLevel = 'no_think' | 'low' | 'high' | 'max'

/** i18n keys for the effort labels, shared by every effort selector. */
export const THINKING_EFFORT_LABEL_KEYS: Record<ThinkingEffortLevel, string> = {
  no_think: 'agentEditor.thinkingEffort.noThink',
  low: 'agentEditor.thinkingEffort.low',
  high: 'agentEditor.thinkingEffort.high',
  max: 'agentEditor.thinkingEffort.max',
}

/** Ordered weakest to strongest; clampThinkingEffort relies on the ordering. */
const EFFORT_ORDINAL: Record<ThinkingEffortLevel, number> = {
  no_think: 0,
  low: 1,
  high: 2,
  max: 3,
}

/**
 * Effort levels a model accepts, ordered weakest to strongest, or null when the
 * model uses the plain thinking on/off switch instead.
 *
 * Must stay aligned with the effortStrategy level sets in thinking.go and with
 * the adapter routing in provider.go. savedControl is extra_config.thinking_control.
 */
export function thinkingEffortLevels(
  provider: string,
  modelName = '',
  savedControl?: string,
): readonly ThinkingEffortLevel[] | null {
  const control = resolveThinkingControl(savedControl, provider, modelName)
  if (control === 'glm_reasoning_effort') {
    return ['low', 'high', 'max']
  }
  if (control !== 'reasoning_effort') return null

  const leaf = modelName.trim().toLowerCase().split('/').pop() || ''
  if (isHyReasoningEffortModel(modelName) && /^hy4(?:$|[-_.])/.test(leaf)) {
    return ['no_think', 'high']
  }
  if (isHyReasoningEffortModel(modelName)) {
    return ['no_think', 'low', 'high']
  }
  return ['no_think', 'low', 'high', 'max']
}

/**
 * Snap a stored effort onto the levels the current model accepts, choosing the
 * nearest level and rounding up on a tie. Selecting a different model must not
 * leave a saved level the new model cannot express, or the dropdown would show
 * one level while the request carried another.
 */
export function clampThinkingEffort(
  level: string | undefined,
  levels: readonly ThinkingEffortLevel[],
): ThinkingEffortLevel {
  const current = (level || '').trim().toLowerCase() as ThinkingEffortLevel
  if (levels.includes(current)) return current

  const target = EFFORT_ORDINAL[current]
  if (target === undefined) return levels[levels.length - 1]

  let best = levels[0]
  let bestDistance = Number.POSITIVE_INFINITY
  for (const candidate of levels) {
    const distance = Math.abs(EFFORT_ORDINAL[candidate] - target)
    // `<=` rounds ties upward because levels run weakest to strongest.
    if (distance <= bestDistance) {
      best = candidate
      bestDistance = distance
    }
  }
  return best
}

/** Resolve stored extra_config or fall back to the provider default. */
export function resolveThinkingControl(
  saved: string | undefined,
  provider: string,
  modelName = '',
): ThinkingControlValue {
  const v = saved?.trim().toLowerCase()
  if (
    v === 'glm_reasoning_effort'
    && (provider.trim().toLowerCase() !== 'zhipu' || !isGlmReasoningEffortModel(modelName))
  ) {
    return defaultThinkingControl(provider, modelName)
  }
  if (THINKING_CONTROL_VALUES.includes(v as ThinkingControlValue)) {
    return v as ThinkingControlValue
  }
  return defaultThinkingControl(provider, modelName)
}

/** Whether the debug drawer should expose Think on/off for this saved model. */
export function modelSupportsThinking(model: {
  type: string
  source: string
  name: string
  parameters: {
    provider?: string
    extra_config?: { thinking_control?: string }
  }
}): boolean {
  if (model.type !== 'KnowledgeQA' || model.source !== 'remote') return false
  return resolveThinkingControl(
    model.parameters.extra_config?.thinking_control,
    model.parameters.provider || '',
    model.name || '',
  ) !== 'none'
}
