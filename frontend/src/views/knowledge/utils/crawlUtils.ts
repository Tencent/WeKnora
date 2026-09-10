/**
 * Frontend web crawler utility.
 * Uses CORS proxy to fetch HTML pages and extract links via BFS traversal.
 */

export interface CrawlOptions {
  maxDepth: number
  maxPages?: number
  domainOnly?: boolean
  proxyUrl?: string
  timeoutMs?: number
  delayMs?: number
  onProgress?: (depth: number, current: number, total: number, url: string) => void
  signal?: AbortSignal
}

export interface CrawlResult {
  url: string
  depth: number
  title?: string
}

const DEFAULT_PROXY = 'https://corsproxy.io/?'
const DEFAULT_TIMEOUT = 15000
const DEFAULT_DELAY = 300
const DEFAULT_MAX_PAGES = 50

const FILE_EXTENSIONS = /\.(pdf|doc|docx|xls|xlsx|ppt|pptx|zip|rar|tar|gz|jpg|jpeg|png|gif|svg|mp3|mp4|webm|ico|css|js|json|xml|rss|atom|yaml|yml|md|txt|csv|rtf|odt|ods|odp|epub|mobi|exe|msi|dmg|iso|apk|ipa|deb|rpm)$/i
const SKIP_PROTOCOLS = /^(javascript:|mailto:|tel:|#|data:|blob:|file:)/i

/**
 * Normalize URL: remove fragment, ensure trailing slash consistency, lowercase scheme.
 */
function normalizeUrl(urlStr: string): string {
  try {
    const u = new URL(urlStr)
    // Remove fragment
    u.hash = ''
    // Remove default ports
    if ((u.protocol === 'http:' && u.port === '80') ||
        (u.protocol === 'https:' && u.port === '443')) {
      u.port = ''
    }
    // Remove trailing slash (except root path)
    if (u.pathname !== '/' && u.pathname.endsWith('/')) {
      u.pathname = u.pathname.slice(0, -1)
    }
    return u.toString().toLowerCase().replace(/\/$/, '') || u.toString().toLowerCase()
  } catch {
    return urlStr
  }
}

/**
 * Extract same-domain links from HTML, with filtering.
 */
function extractLinks(
  html: string,
  baseUrl: string,
  domainOnly: boolean,
): string[] {
  const parser = new DOMParser()
  const doc = parser.parseFromString(html, 'text/html')
  const links = new Set<string>()
  const seedHost = domainOnly ? new URL(baseUrl).hostname.toLowerCase() : null

  const anchorElements = doc.querySelectorAll('a[href]')
  anchorElements.forEach((el) => {
    const href = el.getAttribute('href')
    if (!href) return

    // Skip javascript:, mailto:, #, data:, etc.
    if (SKIP_PROTOCOLS.test(href.trim())) return

    try {
      const resolved = new URL(href, baseUrl)
      // Only http/https
      if (resolved.protocol !== 'http:' && resolved.protocol !== 'https:') return

      // Skip file downloads
      if (FILE_EXTENSIONS.test(resolved.pathname)) return

      // Domain restriction
      if (domainOnly && seedHost && resolved.hostname.toLowerCase() !== seedHost) return

      const normalized = normalizeUrl(resolved.toString())
      if (normalized) {
        links.add(normalized)
      }
    } catch {
      // Skip invalid URLs
    }
  })

  return Array.from(links)
}

/**
 * Extract title from HTML.
 */
function extractTitle(html: string): string | undefined {
  const match = html.match(/<title[^>]*>([^<]*)<\/title>/i)
  if (match && match[1]) {
    return match[1].trim()
  }
  return undefined
}

/**
 * Fetch page HTML through CORS proxy.
 */
async function fetchThroughProxy(
  url: string,
  proxyUrl: string,
  timeoutMs: number,
  signal?: AbortSignal,
): Promise<string> {
  const controller = new AbortController()
  const timeoutId = setTimeout(() => controller.abort(), timeoutMs)
  if (signal) {
    signal.addEventListener('abort', () => controller.abort())
  }

  try {
    const proxiedUrl = proxyUrl + encodeURIComponent(url)
    const resp = await fetch(proxiedUrl, {
      signal: controller.signal,
      headers: { Accept: 'text/html,application/xhtml+xml' },
    })
    if (!resp.ok) {
      throw new Error(`HTTP ${resp.status}`)
    }
    return await resp.text()
  } finally {
    clearTimeout(timeoutId)
  }
}

/**
 * BFS crawler: crawl pages up to maxDepth and return all discovered URLs.
 *
 * @param seedUrl - Starting URL
 * @param options - Crawl options
 * @returns Array of crawl results with URLs and metadata
 */
export async function crawlUrls(
  seedUrl: string,
  options: CrawlOptions,
): Promise<CrawlResult[]> {
  const {
    maxDepth,
    maxPages = DEFAULT_MAX_PAGES,
    domainOnly = true,
    proxyUrl = DEFAULT_PROXY,
    timeoutMs = DEFAULT_TIMEOUT,
    delayMs = DEFAULT_DELAY,
    onProgress,
    signal,
  } = options

  const results: CrawlResult[] = []
  const visited = new Set<string>()
  const queue: { url: string; depth: number }[] = [
    { url: normalizeUrl(seedUrl), depth: 0 },
  ]
  visited.add(normalizeUrl(seedUrl))

  let processed = 0

  while (queue.length > 0 && results.length < maxPages) {
    if (signal?.aborted) break

    const current = queue.shift()!
    const { url, depth } = current

    onProgress?.(depth, results.length, maxPages, url)

    try {
      const html = await fetchThroughProxy(url, proxyUrl, timeoutMs, signal)
      const title = extractTitle(html)
      results.push({ url, depth, title })

      // Extract and enqueue sub-links if within depth limit
      if (depth < maxDepth) {
        const links = extractLinks(html, url, domainOnly)
        for (const link of links) {
          if (!visited.has(link) && results.length + queue.length < maxPages) {
            visited.add(link)
            queue.push({ url: link, depth: depth + 1 })
          }
        }
      }
    } catch {
      // Skip failed pages and continue
      results.push({ url, depth })
    }

    processed++

    // Rate limiting
    if (delayMs > 0 && queue.length > 0) {
      await new Promise((resolve) => setTimeout(resolve, delayMs))
    }
  }

  onProgress?.(maxDepth, results.length, maxPages, '')
  return results
}
