/** Matches internal/types.DefaultMaxContextTokens. */
export const DEFAULT_MODEL_CONTEXT_WINDOW = 200000

export function modelHasContextWindow(type?: string): boolean {
  return type === 'KnowledgeQA' || type === 'VLLM' || type === 'chat' || type === 'vllm'
}

/** Tokens the backend will actually use: the model's value, or 200K. */
export function effectiveContextWindow(tokens?: number | null): number {
  if (typeof tokens === 'number' && tokens > 0) {
    return tokens
  }
  return DEFAULT_MODEL_CONTEXT_WINDOW
}

export function isDefaultContextWindow(tokens?: number | null): boolean {
  return !(typeof tokens === 'number' && tokens > 0)
}

/** Compact label: 128000 → 128K, 200000 → 200K, 1048576 → 1M. */
export function formatTokenCount(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens <= 0) {
    return ''
  }
  const n = Math.round(tokens)
  if (n >= 1_000_000 && n % 1_000_000 === 0) {
    return `${n / 1_000_000}M`
  }
  if (n >= 1000 && n % 1000 === 0) {
    return `${n / 1000}K`
  }
  if (n >= 1024 && n % 1024 === 0) {
    const k = n / 1024
    if (k >= 1024 && k % 1024 === 0) {
      return `${k / 1024}M`
    }
    return `${k}K`
  }
  return String(n)
}

export function formatContextWindow(tokens?: number | null): string {
  return formatTokenCount(effectiveContextWindow(tokens))
}
