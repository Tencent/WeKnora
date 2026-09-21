const YOUTUBE_HOSTS = new Set(['youtube.com', 'www.youtube.com', 'm.youtube.com', 'youtu.be'])

function isYoutubeUrl(line: string): boolean {
  try {
    const parsed = new URL(line)
    return YOUTUBE_HOSTS.has(parsed.hostname.toLowerCase())
  } catch {
    return false
  }
}

export function parseYoutubeUrlsInput(text: string): { urls: string[]; invalidLines: string[] } {
  const urls: string[] = []
  const invalidLines: string[] = []

  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    if (isYoutubeUrl(line)) {
      urls.push(line)
    } else {
      invalidLines.push(line)
    }
  }

  return { urls, invalidLines }
}
