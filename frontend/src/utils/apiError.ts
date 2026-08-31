function stringifyErrorPart(value: unknown): string | undefined {
  if (typeof value === 'string') {
    const text = value.trim()
    return text || undefined
  }
  if (value == null) return undefined

  try {
    const text = JSON.stringify(value)
    return text && text !== '{}' ? text : undefined
  } catch (_) {
    return undefined
  }
}

/** Extracts a useful message from the API error envelope used by the backend. */
export function getApiErrorMessage(error: any, fallback?: string): string | undefined {
  const data = error?.response?.data ?? error?.data ?? error
  if (typeof data === 'string') return stringifyErrorPart(data) || fallback

  const envelope = data && typeof data === 'object' ? data : {}
  const nestedError = envelope.error
  const errorObject = nestedError && typeof nestedError === 'object' ? nestedError : undefined
  const message = stringifyErrorPart(
    typeof nestedError === 'string'
      ? nestedError
      : errorObject?.message ?? envelope.message ?? error?.message,
  )
  const details = stringifyErrorPart(
    errorObject?.details ?? envelope.details ?? error?.details,
  )

  if (message && details && details !== message) return `${message}: ${details}`
  return message || details || fallback
}
