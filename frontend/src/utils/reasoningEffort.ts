/**
 * Reasoning-effort helpers shared by the agent editor, the model debug
 * drawer and the model editor.
 *
 * Mirrors internal/models/api.ReasoningEffort: a protocol-neutral thinking
 * level that vendors map onto their own knobs. `off` disables thinking,
 * `auto` lets the vendor pick its default intensity, and the graded ladder
 * (minimal ... max) is clamped by the backend to what the model supports.
 *
 * Legacy boolean semantics: `thinking: true` == `auto`, `thinking: false` == `off`.
 */

export const REASONING_LEVELS = [
  'off',
  'auto',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
] as const

export type ReasoningLevel = (typeof REASONING_LEVELS)[number]

/** Offered when the selected model has no catalog capabilities yet. */
export const FALLBACK_REASONING_LEVELS: ReasoningLevel[] = ['off', 'auto', 'low', 'medium', 'high']

/** Minimal shape of the backend `capabilities` object we rely on here. */
export interface ThinkingCapabilities {
  thinking_levels?: ReadonlyArray<string> | null
}

export function isReasoningLevel(value: unknown): value is ReasoningLevel {
  return typeof value === 'string' && (REASONING_LEVELS as ReadonlyArray<string>).includes(value)
}

/** Sort levels into the canonical ladder order, dropping unknown values and duplicates. */
export function canonicalLevels(levels: ReadonlyArray<string> | null | undefined): ReasoningLevel[] {
  if (!levels || levels.length === 0) return []
  const wanted = new Set(levels.map((level) => String(level).trim().toLowerCase()))
  return REASONING_LEVELS.filter((level) => wanted.has(level))
}

/**
 * Derive the graded level from a config that may carry only the legacy
 * boolean. A valid `effort` always wins; otherwise `true` → `auto`,
 * anything else → `off`.
 */
export function levelFromLegacy(thinking?: boolean | null, effort?: string | null): ReasoningLevel {
  const normalized = typeof effort === 'string' ? effort.trim().toLowerCase() : ''
  if (isReasoningLevel(normalized)) return normalized
  return thinking === true ? 'auto' : 'off'
}

/** Whether a level asks the model to think at all. */
export function levelEnablesThinking(level: string | null | undefined): boolean {
  return !!level && level !== 'off'
}

/** i18n key of a level's short name. */
export function levelLabelKey(level: string): string {
  return `model.reasoning.levels.${level}`
}

/** i18n key of a level's one-line description. */
export function levelDescriptionKey(level: string): string {
  return `model.reasoning.levelDescriptions.${level}`
}

/** Whether the model can be asked to think (non-empty thinking_levels). */
export function modelCanThink(capabilities?: ThinkingCapabilities | null): boolean {
  return (capabilities?.thinking_levels?.length ?? 0) > 0
}

/**
 * Levels the model itself reports, in ladder order. Empty when the model
 * cannot think (or no capabilities are known).
 */
export function supportedLevels(capabilities?: ThinkingCapabilities | null): ReasoningLevel[] {
  return canonicalLevels(capabilities?.thinking_levels)
}

/**
 * Options for a reasoning-level selector.
 *
 * When the model reports capabilities, `thinking_levels` is authoritative and
 * is followed EXACTLY — including the absence of `off`. Always-on reasoning
 * models (deepseek-reasoner, qwq-plus, the gemini-3 family, ...) map `off` to
 * null in the catalog, so offering "关闭" there would promise something the
 * backend cannot deliver: no switch is sent and the model thinks anyway.
 *
 * With no capabilities at all (model list not loaded, local/Ollama model, ...)
 * the generic off/auto/low/medium/high ladder is offered so the user can still
 * express a preference; the backend clamps it later.
 */
export function optionsFor(capabilities?: ThinkingCapabilities | null): ReasoningLevel[] {
  if (!capabilities || !Array.isArray(capabilities.thinking_levels)) {
    return [...FALLBACK_REASONING_LEVELS]
  }
  const levels = canonicalLevels(capabilities.thinking_levels)
  // A model that cannot think at all still needs one truthful value to show.
  return levels.length > 0 ? levels : ['off']
}

/**
 * Whether the model reports capabilities but cannot be switched off — the UI
 * must say so, otherwise the absence of a "关闭" option looks like a bug.
 */
export function modelCannotDisableThinking(capabilities?: ThinkingCapabilities | null): boolean {
  if (!capabilities || !Array.isArray(capabilities.thinking_levels)) return false
  const levels = canonicalLevels(capabilities.thinking_levels)
  return levels.length > 0 && !levels.includes('off')
}

/** Pick a level to show for a selector whose current value may be unsupported. */
export function clampLevel(level: string | null | undefined, options: ReadonlyArray<ReasoningLevel>): ReasoningLevel {
  if (options.length === 0) return 'off'
  const normalized = typeof level === 'string' ? level.trim().toLowerCase() : ''
  if (isReasoningLevel(normalized) && options.includes(normalized)) return normalized
  // A stored level the model does not accept — an unsupported rung, or `off`
  // on an always-on reasoning model — becomes the vendor default.
  if (normalized && options.includes('auto')) return 'auto'
  return options.includes('off') ? 'off' : options[0]
}
