import { normalizeFolderPath } from '../views/knowledge/folderTree.ts'

/** Knowledge list pages used to find a sibling image. The public list API is queried at this size. */
export const PREVIEW_IMAGE_PAGE_SIZE = 100

/** Shown until a sibling image has been fetched. It does not hit the network. */
export const PREVIEW_IMAGE_PLACEHOLDER =
  'data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=='

/** Replace relative image hrefs so the first paint cannot 404 against the app route. */
export function neutralizeRelativePreviewImages(markdown: string): string {
  const spans = relativeImageSpans(markdown)
  if (spans.length === 0) return markdown
  let rewritten = markdown
  for (const span of [...spans].sort((a, b) => b.start - a.start)) {
    rewritten = rewritten.slice(0, span.start) + PREVIEW_IMAGE_PLACEHOLDER + rewritten.slice(span.end)
  }
  return rewritten
}

/** Stop walking a folder after this many pages so a huge directory cannot stall preview. */
const MAX_PREVIEW_IMAGE_LIST_PAGES = 20

export interface PreviewImageLocation {
  folderPath: string
  fileName: string
}

export interface PreviewImageKnowledge {
  knowledgeBaseId: string
  folderPath: string
}

export interface PreviewImageFileRow {
  id: string
  fileName: string
  folderPath: string
}

export interface PreviewImageListPage {
  rows: PreviewImageFileRow[]
  total: number
}

export interface RewriteKnowledgeMarkdownImagesDeps {
  getKnowledge: (knowledgeId: string) => Promise<PreviewImageKnowledge | null>
  listFiles: (knowledgeBaseId: string, folderPath: string, page: number) => Promise<PreviewImageListPage>
  loadPreview: (knowledgeId: string) => Promise<Blob>
  createObjectURL?: (blob: Blob) => string
}

interface ImageHrefSpan {
  href: string
  start: number
  end: number
}

/**
 * Resolve a Markdown image href against the document's stored folder_path.
 * Absolute, scheme, and root-relative URLs stay untouched. `..` that would
 * leave the knowledge base does not match a file by name alone.
 */
export function resolvePreviewImageLocation(documentFolder: string, href: string): PreviewImageLocation | null {
  const decoded = decodePreviewHref(href)
  if (decoded == null || !isKnowledgeRelativeHref(decoded)) return null

  const parts = decoded.replace(/\\/g, '/').split('/')
  const segments = normalizeFolderPath(documentFolder || '').split('/').filter(Boolean)
  for (let i = 0; i < parts.length; i += 1) {
    const part = parts[i]
    const last = i === parts.length - 1
    if (part === '' || part === '.') {
      if (last) return null
      continue
    }
    if (part === '..') {
      if (last || segments.length === 0) return null
      segments.pop()
      continue
    }
    if (last) {
      const fileName = part.trim()
      if (!fileName || fileName === '.' || fileName === '..') return null
      return {
        folderPath: normalizeFolderPath(segments.join('/')),
        fileName,
      }
    }
    segments.push(part)
  }
  return null
}

/**
 * Rewrite relative Markdown and HTML images to sibling knowledge files.
 * Lookup is exact folder_path + file_name. A file that merely contains the
 * same name, or lives in another folder, is left as written.
 */
export async function rewriteKnowledgeMarkdownImages(
  markdown: string,
  knowledgeId: string,
  deps: RewriteKnowledgeMarkdownImagesDeps,
): Promise<{ markdown: string; objectUrls: string[] }> {
  const spans = relativeImageSpans(markdown)
  if (spans.length === 0 || !knowledgeId) return { markdown, objectUrls: [] }

  const knowledge = await deps.getKnowledge(knowledgeId)
  if (!knowledge?.knowledgeBaseId) return { markdown, objectUrls: [] }

  const locations = new Map<string, PreviewImageLocation>()
  for (const span of spans) {
    const location = resolvePreviewImageLocation(knowledge.folderPath, span.href)
    if (!location) continue
    locations.set(locationKey(location), location)
  }
  if (locations.size === 0) return { markdown, objectUrls: [] }

  const byFolder = new Map<string, PreviewImageLocation[]>()
  for (const location of locations.values()) {
    const group = byFolder.get(location.folderPath) || []
    group.push(location)
    byFolder.set(location.folderPath, group)
  }

  const ids = new Map<string, string>()
  for (const [folderPath, group] of byFolder) {
    const found = await findExactFiles(deps, knowledge.knowledgeBaseId, folderPath, group)
    for (const [key, id] of found) ids.set(key, id)
  }

  const objectUrls: string[] = []
  const hrefToUrl = new Map<string, string>()
  const createObjectURL = deps.createObjectURL || ((blob: Blob) => URL.createObjectURL(blob))
  for (const span of spans) {
    if (hrefToUrl.has(span.href)) continue
    const location = resolvePreviewImageLocation(knowledge.folderPath, span.href)
    if (!location) continue
    const id = ids.get(locationKey(location))
    if (!id || id === knowledgeId) continue
    try {
      const blob = await deps.loadPreview(id)
      const url = createObjectURL(blob)
      objectUrls.push(url)
      hrefToUrl.set(span.href, url)
    } catch {
      // Leave this href unchanged. The rest of the document still previews.
    }
  }
  if (hrefToUrl.size === 0) return { markdown, objectUrls }

  let rewritten = markdown
  const ordered = [...spans].sort((a, b) => b.start - a.start)
  for (const span of ordered) {
    const url = hrefToUrl.get(span.href)
    if (!url) continue
    rewritten = rewritten.slice(0, span.start) + url + rewritten.slice(span.end)
  }
  return { markdown: rewritten, objectUrls }
}

function locationKey(location: PreviewImageLocation): string {
  return `${location.folderPath}\n${location.fileName}`
}

async function findExactFiles(
  deps: RewriteKnowledgeMarkdownImagesDeps,
  knowledgeBaseId: string,
  folderPath: string,
  wanted: PreviewImageLocation[],
): Promise<Map<string, string>> {
  const needed = new Set(wanted.map(item => item.fileName))
  const found = new Map<string, string>()
  let total = Number.POSITIVE_INFINITY
  for (let page = 1; page <= MAX_PREVIEW_IMAGE_LIST_PAGES && found.size < needed.size; page += 1) {
    if ((page - 1) * PREVIEW_IMAGE_PAGE_SIZE >= total) break
    const result = await deps.listFiles(knowledgeBaseId, folderPath, page)
    const rows = result.rows || []
    total = Number.isFinite(result.total) ? result.total : rows.length
    for (const row of rows) {
      if (!row?.id || !needed.has(row.fileName) || found.has(row.fileName)) continue
      if ((row.folderPath || '') !== folderPath) continue
      found.set(row.fileName, row.id)
    }
    if (rows.length < PREVIEW_IMAGE_PAGE_SIZE) break
  }
  const ids = new Map<string, string>()
  for (const location of wanted) {
    const id = found.get(location.fileName)
    if (id) ids.set(locationKey(location), id)
  }
  return ids
}

function relativeImageSpans(markdown: string): ImageHrefSpan[] {
  const hidden = hiddenSpans(markdown)
  return imageHrefSpans(markdown).filter(span => {
    if (hidden.some(([start, end]) => span.start >= start && span.start < end)) return false
    const decoded = decodePreviewHref(span.href)
    return decoded != null && isKnowledgeRelativeHref(decoded)
  })
}

function imageHrefSpans(markdown: string): ImageHrefSpan[] {
  const spans: ImageHrefSpan[] = []
  let cursor = 0
  while (cursor < markdown.length) {
    const image = markdown.indexOf('![', cursor)
    const tag = markdown.toLowerCase().indexOf('<img', cursor)
    if (image < 0 && tag < 0) break
    const useImage = image >= 0 && (tag < 0 || image <= tag)
    if (useImage) {
      if (isEscaped(markdown, image)) {
        cursor = image + 2
        continue
      }
      const parsed = parseMarkdownImage(markdown, image)
      if (!parsed) {
        cursor = image + 2
        continue
      }
      spans.push(parsed)
      cursor = parsed.end
      continue
    }
    const parsed = parseHtmlImage(markdown, tag)
    if (!parsed) {
      cursor = tag + 4
      continue
    }
    spans.push(parsed)
    cursor = parsed.end
  }
  return spans
}

function parseMarkdownImage(markdown: string, imageStart: number): (ImageHrefSpan & { end: number }) | null {
  const altEnd = findClosingBracket(markdown, imageStart + 1)
  if (altEnd < 0 || markdown[altEnd + 1] !== '(') return null
  let i = altEnd + 2
  while (i < markdown.length && /\s/.test(markdown[i])) i += 1
  if (i >= markdown.length) return null
  let hrefStart = i
  let hrefEnd = i
  if (markdown[i] === '<') {
    hrefStart = i + 1
    hrefEnd = markdown.indexOf('>', hrefStart)
    if (hrefEnd < 0) return null
    i = hrefEnd + 1
  } else {
    while (i < markdown.length && !/[\s)]/.test(markdown[i])) i += 1
    hrefEnd = i
  }
  while (i < markdown.length && markdown[i] !== ')') i += 1
  if (markdown[i] !== ')') return null
  if (hrefEnd <= hrefStart) return null
  return { href: markdown.slice(hrefStart, hrefEnd), start: hrefStart, end: hrefEnd }
}

function parseHtmlImage(markdown: string, tagStart: number): (ImageHrefSpan & { end: number }) | null {
  const tagEnd = markdown.indexOf('>', tagStart)
  if (tagEnd < 0) return null
  const tag = markdown.slice(tagStart, tagEnd + 1)
  const match = tag.match(/\bsrc\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i)
  if (!match) return null
  const href = match[1] ?? match[2] ?? match[3] ?? ''
  const rel = match.index
  if (rel == null || !href) return null
  // lastIndexOf: a one-letter file name must not match the "a" inside "src".
  const hrefStart = tagStart + rel + match[0].lastIndexOf(href)
  return { href, start: hrefStart, end: hrefStart + href.length }
}

function findClosingBracket(text: string, open: number): number {
  if (text[open] !== '[') return -1
  for (let i = open + 1; i < text.length; i += 1) {
    if (text[i] === '\\') {
      i += 1
      continue
    }
    if (text[i] === ']') return i
  }
  return -1
}

function isEscaped(text: string, index: number): boolean {
  let slashes = 0
  for (let i = index - 1; i >= 0 && text[i] === '\\'; i -= 1) slashes += 1
  return slashes % 2 === 1
}

function hiddenSpans(markdown: string): Array<[number, number]> {
  const spans: Array<[number, number]> = []
  const fenceRe = /^ {0,3}(`{3,}|~{3,}).*$/gm
  const opens: Array<{ index: number; marker: string }> = []
  for (const match of markdown.matchAll(fenceRe)) {
    const marker = match[1]
    const index = match.index ?? 0
    const last = opens[opens.length - 1]
    if (last && last.marker[0] === marker[0] && marker.length >= last.marker.length) {
      const lineEnd = markdown.indexOf('\n', index + match[0].length)
      spans.push([last.index, lineEnd < 0 ? markdown.length : lineEnd + 1])
      opens.pop()
    } else {
      opens.push({ index, marker })
    }
  }
  for (const open of opens) spans.push([open.index, markdown.length])

  for (const match of markdown.matchAll(/<!--[\s\S]*?-->/g)) {
    const index = match.index ?? 0
    if (spans.some(([start, end]) => index >= start && index < end)) continue
    spans.push([index, index + match[0].length])
  }
  for (const match of markdown.matchAll(/`+[^`\n]*`+/g)) {
    const index = match.index ?? 0
    if (spans.some(([start, end]) => index >= start && index < end)) continue
    spans.push([index, index + match[0].length])
  }
  return spans
}

function decodePreviewHref(href: string): string | null {
  let raw = href.trim()
  if (!raw) return null
  raw = raw
    .replace(/&amp;/gi, '&')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;|&apos;/gi, "'")
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
  const hash = raw.indexOf('#')
  if (hash >= 0) raw = raw.slice(0, hash)
  const query = raw.indexOf('?')
  if (query >= 0) raw = raw.slice(0, query)
  if (!raw) return null
  try {
    return decodeURIComponent(raw)
  } catch {
    return null
  }
}

function isKnowledgeRelativeHref(href: string): boolean {
  if (!href || href.startsWith('#') || href.startsWith('//') || href.startsWith('/')) return false
  return !/^[a-z][a-z0-9+.-]*:/i.test(href)
}
