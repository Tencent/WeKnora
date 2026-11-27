type DocumentSource = {
  type?: string
  source?: string
  metadata?: { source_url?: unknown }
}

export function documentSourceUrl(details?: DocumentSource | null): string {
  if (details?.type === 'url') return details.source || ''
  const source = details?.metadata?.source_url
  if (typeof source !== 'string') return ''
  const address = source.trim()
  try {
    const url = new URL(address)
    return (url.protocol === 'https:' || url.protocol === 'http:') && !url.username && !url.password
      ? address
      : ''
  } catch {
    return ''
  }
}
