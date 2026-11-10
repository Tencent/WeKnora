// Timeout budgets for HTTP requests whose duration is dominated by payload
// transfer rather than server processing.
//
// The axios instance uses a 30s default, which is right for JSON calls but
// unreachable for file uploads: MAX_FILE_SIZE_MB is deployment-configurable
// (200MB is a realistic value) and no ordinary uplink pushes that in 30s.
// Callers used to work around this by passing an explicit timeout, which only
// some of them remembered to do.

/**
 * Lower bound for any upload, regardless of size. Matches the value the skill
 * and model upload call sites already pass explicitly.
 */
export const UPLOAD_TIMEOUT_FLOOR_MS = 5 * 60 * 1000

/**
 * Worst-case sustained uplink we budget for: 10MB per minute (~170KB/s).
 * Deliberately pessimistic — the timeout is a ceiling that only matters when a
 * transfer stalls, so an over-generous budget costs nothing on healthy links.
 */
const UPLOAD_BYTES_PER_MINUTE = 10 * 1024 * 1024

/** Total byte size of every Blob/File carried by a FormData payload. */
export function uploadPayloadBytes(data: unknown): number {
  if (typeof FormData === 'undefined' || !(data instanceof FormData)) return 0
  let total = 0
  data.forEach((value) => {
    if (typeof Blob !== 'undefined' && value instanceof Blob) total += value.size
  })
  return total
}

/**
 * Timeout budget for an upload payload: the floor for small payloads, scaling
 * linearly beyond it.
 */
export function uploadTimeoutMs(data: unknown): number {
  const scaled = Math.ceil((uploadPayloadBytes(data) / UPLOAD_BYTES_PER_MINUTE) * 60_000)
  return Math.max(UPLOAD_TIMEOUT_FLOOR_MS, scaled)
}

/**
 * Whether a rejected request timed out, as opposed to failing to reach the
 * server at all. Both arrive without `error.response`, but reporting a timeout
 * as "check your network connection" sends people chasing the wrong problem.
 */
export function isTimeoutError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const { code, message } = error as { code?: unknown; message?: unknown }
  if (code === 'ECONNABORTED' || code === 'ETIMEDOUT') return true
  return typeof message === 'string' && /timeout/i.test(message)
}
