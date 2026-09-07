export type ReasoningEffortValue =
  | 'none'
  | 'minimal'
  | 'low'
  | 'medium'
  | 'high'

const REASONING_EFFORT_VALUES: ReasoningEffortValue[] = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
]

/**
 * Default reasoning effort for the Responses provider.
 * Must stay aligned with resolveResponsesEffort in
 * internal/models/chat/responses.go (extra_config.reasoning_effort).
 */
export function defaultReasoningEffort(): ReasoningEffortValue {
  return 'medium'
}

/** Resolve a saved reasoning_effort or fall back to the default. */
export function resolveReasoningEffort(
  saved: string | undefined,
): ReasoningEffortValue {
  const v = saved?.trim().toLowerCase()
  if (v && (REASONING_EFFORT_VALUES as readonly string[]).includes(v)) {
    return v as ReasoningEffortValue
  }
  return defaultReasoningEffort()
}
