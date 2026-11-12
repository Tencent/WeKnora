const ATTACHMENT_PATH = '/download/attachments/'

/** Resolve an attachment href against the document's own Confluence context. */
export function resolveConfluenceAttachmentHref(href: string, baseUrl: string): string {
  const relative = href.trim()
  if (!baseUrl || !relative.startsWith('/') || relative.startsWith('//')) return href

  try {
    const base = new URL(baseUrl)
    if ((base.protocol !== 'https:' && base.protocol !== 'http:') || base.username || base.password) return href

    const context = base.pathname.replace(/\/+$/, '')
    const attachmentPath = context + ATTACHMENT_PATH
    const hasContext = relative.startsWith(attachmentPath) || relative.startsWith('/wiki' + ATTACHMENT_PATH)
    if (!hasContext && !relative.startsWith(ATTACHMENT_PATH)) return href

    const resolved = new URL(hasContext ? relative : context + relative, base.origin)
    if (!resolved.pathname.startsWith(attachmentPath) && !resolved.pathname.startsWith('/wiki' + ATTACHMENT_PATH)) return href
    return resolved.href
  } catch {
    return href
  }
}

/** Update actual HTML anchors during sanitization, leaving code and image nodes alone. */
export function rewriteConfluenceAttachmentAnchor(node: Node, baseUrl: string): void {
  if (!baseUrl || node.nodeName !== 'A') return
  const anchor = node as HTMLAnchorElement
  const href = anchor.getAttribute('href')
  if (!href) return
  const resolved = resolveConfluenceAttachmentHref(href, baseUrl)
  if (resolved !== href) anchor.setAttribute('href', resolved)
}
