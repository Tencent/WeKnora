// Mirrors internal/infrastructure/youtube.ParseURL on the backend. YouTube
// video and playlist links are imported together through the YouTube batch
// endpoint; every other link goes through the regular URL import.
const YOUTUBE_HOSTS = new Set([
  'youtube.com',
  'www.youtube.com',
  'm.youtube.com',
  'music.youtube.com',
  'youtube-nocookie.com',
  'www.youtube-nocookie.com',
])

const VIDEO_ID_PATTERN = /^[A-Za-z0-9_-]{11}$/
const PLAYLIST_ID_PATTERN = /^[A-Za-z0-9_-]{2,64}$/

export type YouTubeLinkKind = 'video' | 'playlist'

export function youTubeLinkKind(raw: string): YouTubeLinkKind | null {
  let parsed: URL
  try {
    parsed = new URL(raw.trim())
  } catch {
    return null
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
  const host = parsed.hostname.toLowerCase()
  const segments = parsed.pathname.replace(/^\/+/, '').split('/')

  if (host === 'youtu.be' || host === 'www.youtu.be') {
    return VIDEO_ID_PATTERN.test(segments[0]) ? 'video' : null
  }
  if (!YOUTUBE_HOSTS.has(host)) return null

  switch (segments[0].toLowerCase()) {
    case 'watch':
      return VIDEO_ID_PATTERN.test(parsed.searchParams.get('v') || '') ? 'video' : null
    case 'playlist':
      return PLAYLIST_ID_PATTERN.test(parsed.searchParams.get('list') || '') ? 'playlist' : null
    case 'shorts':
    case 'live':
    case 'embed':
    case 'v':
      return VIDEO_ID_PATTERN.test(segments[1] || '') ? 'video' : null
    default:
      return null
  }
}

export function isYouTubeUrl(raw: string): boolean {
  return youTubeLinkKind(raw) !== null
}

// The backend accepts at most this many links per YouTube import request.
export const YOUTUBE_IMPORT_BATCH_SIZE = 100

// parseImportUrls splits pasted text into distinct http(s) links. Links may be
// separated by new lines, spaces or commas (ASCII or full-width).
export function parseImportUrls(text: string): { urls: string[]; invalid: string[] } {
  const urls: string[] = []
  const invalid: string[] = []
  const seen = new Set<string>()
  for (const token of text.split(/[\s,，、]+/)) {
    if (!token) continue
    let valid = false
    try {
      const parsed = new URL(token)
      valid = parsed.protocol === 'http:' || parsed.protocol === 'https:'
    } catch {
      valid = false
    }
    if (!valid) {
      invalid.push(token)
      continue
    }
    if (!seen.has(token)) {
      seen.add(token)
      urls.push(token)
    }
  }
  return { urls, invalid }
}
