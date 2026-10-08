/**
 * Minimal EPUB rendering for previews: every spine item becomes a
 * `<section data-epub-section="N">` (N is the 1-based spine position, the same
 * numbering the backend uses in section locators), with images served from
 * blob URLs. The result must still be sanitized before it reaches the DOM.
 */
import JSZip from 'jszip'

export type EpubPreview = {
  html: string
  /** Blob URLs created for images; revoke them when the preview goes away. */
  objectUrls: string[]
}

const IMAGE_MIME: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  bmp: 'image/bmp',
}

/** Resolve `href` against the directory of `base` inside the archive. */
export function resolveEpubPath(base: string, href: string): string {
  const clean = decodeURIComponent(href.split('#')[0].split('?')[0])
  if (clean.startsWith('/')) return clean.slice(1)
  const parts = base.split('/').slice(0, -1)
  for (const seg of clean.split('/')) {
    if (seg === '..') parts.pop()
    else if (seg && seg !== '.') parts.push(seg)
  }
  return parts.join('/')
}

function parseXml(text: string): Document {
  return new DOMParser().parseFromString(text, 'application/xml')
}

function parseChapter(text: string): Document {
  const xhtml = new DOMParser().parseFromString(text, 'application/xhtml+xml')
  if (!xhtml.getElementsByTagName('parsererror').length && xhtml.body) return xhtml
  return new DOMParser().parseFromString(text, 'text/html')
}

function byLocalName(root: Document | Element, name: string): Element[] {
  return Array.from(root.getElementsByTagName('*')).filter((el) => el.localName === name)
}

export async function renderEpubPreview(data: ArrayBuffer): Promise<EpubPreview> {
  const zip = await JSZip.loadAsync(data)
  const containerXml = await zip.file('META-INF/container.xml')?.async('string')
  if (!containerXml) throw new Error('EPUB: missing META-INF/container.xml')
  const rootfile = byLocalName(parseXml(containerXml), 'rootfile')[0]?.getAttribute('full-path')
  if (!rootfile) throw new Error('EPUB: no rootfile')
  const opfText = await zip.file(rootfile)?.async('string')
  if (!opfText) throw new Error('EPUB: missing package document')
  const opf = parseXml(opfText)

  const manifest = new Map<string, string>()
  for (const item of byLocalName(opf, 'item')) {
    const id = item.getAttribute('id')
    const href = item.getAttribute('href')
    if (id && href) manifest.set(id, resolveEpubPath(rootfile, href))
  }
  // Keep unresolved entries so section numbers match spine positions.
  const spine = byLocalName(opf, 'itemref').map((ref) => manifest.get(ref.getAttribute('idref') || ''))

  const objectUrls: string[] = []
  const imageUrls = new Map<string, string>()
  const imageUrl = async (path: string): Promise<string> => {
    const cached = imageUrls.get(path)
    if (cached) return cached
    const file = zip.file(path)
    if (!file) return ''
    const ext = path.split('.').pop()?.toLowerCase() || ''
    const blob = new Blob([await file.async('arraybuffer')], { type: IMAGE_MIME[ext] || 'application/octet-stream' })
    const url = URL.createObjectURL(blob)
    objectUrls.push(url)
    imageUrls.set(path, url)
    return url
  }

  const sections: string[] = []
  for (let i = 0; i < spine.length; i++) {
    const path = spine[i]
    if (!path) continue
    const text = await zip.file(path)?.async('string')
    if (!text) continue
    const doc = parseChapter(text)
    const body = doc.body || doc.documentElement
    for (const img of Array.from(body.querySelectorAll('img'))) {
      const src = img.getAttribute('src')
      img.setAttribute('src', src ? await imageUrl(resolveEpubPath(path, src)) : '')
    }
    for (const image of byLocalName(body as Element, 'image')) {
      const href = image.getAttribute('href') || image.getAttribute('xlink:href')
      if (href) {
        const url = await imageUrl(resolveEpubPath(path, href))
        image.setAttribute('href', url)
        image.removeAttribute('xlink:href')
      }
    }
    // Chapter links point inside the archive; keep the text, drop the target.
    for (const a of Array.from(body.querySelectorAll('a[href]'))) {
      const href = a.getAttribute('href') || ''
      if (!/^https?:/i.test(href)) a.removeAttribute('href')
    }
    sections.push(`<section class="epub-section" data-epub-section="${i + 1}">${body.innerHTML}</section>`)
  }
  return { html: sections.join('\n'), objectUrls }
}
