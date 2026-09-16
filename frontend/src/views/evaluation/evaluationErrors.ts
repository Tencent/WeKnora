/** The shared HTTP client rejects both Error instances and structured API errors. */
export function evaluationErrorMessage(reason: unknown, fallback: string): string {
  if (reason && typeof reason === 'object' && 'message' in reason && typeof reason.message === 'string') return reason.message
  return fallback
}
