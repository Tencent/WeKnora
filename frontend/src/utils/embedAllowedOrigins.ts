export type AllowedOriginsValidationError =
  | { code: 'required' }
  | { code: 'wildcard_prod' }
  | { code: 'invalid'; origin: string }

/** Mirrors backend validateAllowedOrigins in internal/handler/embed_channel.go */
export function parseAllowedOrigins(text: string): string[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
}

export function validateAllowedOrigins(
  origins: string[],
  prod = import.meta.env.PROD,
): { ok: true; origins: string[] } | { ok: false; error: AllowedOriginsValidationError } {
  const cleaned = parseAllowedOrigins(origins.join('\n'))
  if (cleaned.length === 0) {
    return { ok: false, error: { code: 'required' } }
  }
  for (const origin of cleaned) {
    if (origin === '*') {
      if (prod) {
        return { ok: false, error: { code: 'wildcard_prod' } }
      }
      continue
    }
    let host = origin
    if (origin.startsWith('*.')) {
      host = `https://${origin.slice(2)}`
    }
    try {
      const url = new URL(host)
      if ((url.protocol !== 'http:' && url.protocol !== 'https:') || !url.host) {
        return { ok: false, error: { code: 'invalid', origin } }
      }
    } catch {
      return { ok: false, error: { code: 'invalid', origin } }
    }
  }
  return { ok: true, origins: cleaned }
}
