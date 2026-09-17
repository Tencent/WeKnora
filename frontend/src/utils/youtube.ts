// Mirrors internal/infrastructure/youtube.ParseURL on the backend: playlist
// pages fan out into one knowledge entry per video through a dedicated
// endpoint, while video links go through the regular URL import.
const YOUTUBE_HOSTS = new Set([
  'youtube.com',
  'www.youtube.com',
  'm.youtube.com',
  'music.youtube.com',
  'youtube-nocookie.com',
  'www.youtube-nocookie.com',
])

const PLAYLIST_ID_PATTERN = /^[A-Za-z0-9_-]{2,64}$/

export function isYouTubePlaylistUrl(raw: string): boolean {
  let parsed: URL
  try {
    parsed = new URL(raw.trim())
  } catch {
    return false
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return false
  if (!YOUTUBE_HOSTS.has(parsed.hostname.toLowerCase())) return false
  const firstSegment = parsed.pathname.replace(/^\/+/, '').split('/')[0].toLowerCase()
  return firstSegment === 'playlist' && PLAYLIST_ID_PATTERN.test(parsed.searchParams.get('list') || '')
}
