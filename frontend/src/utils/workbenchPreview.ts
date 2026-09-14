import DOMPurify from 'dompurify'
import { resolvePreviewKind, sniffPreview, type FilePreviewKind } from './filePreview'

export const WORKBENCH_PREVIEW_CSP = "default-src 'none'; script-src 'none'; connect-src 'none'; img-src 'none'; style-src 'unsafe-inline'; font-src 'none'; media-src 'none'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'"

export function sanitizeWorkbenchPreview(html: string): string {
  const clean = DOMPurify.sanitize(html, {
    RETURN_DOM_FRAGMENT: true,
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'base', 'link', 'meta', 'form', 'input', 'button', 'textarea', 'select', 'audio', 'video', 'source', 'foreignObject', 'animate', 'animateMotion', 'animateTransform', 'set'],
    FORBID_ATTR: ['href', 'xlink:href', 'srcset', 'action', 'formaction', 'target', 'ping', 'background'],
    ALLOW_DATA_ATTR: false,
    ALLOWED_URI_REGEXP: /^(?:)$/,
  })
  // Restricted HTML has no decoded-pixel budget. Remove all image sources;
  // direct image files go through validateWorkbenchImage instead.
  for (const element of clean.querySelectorAll('[src]')) {
    element.removeAttribute('src')
  }
  const container = clean.ownerDocument.createElement('div')
  container.appendChild(clean)
  // CSP precedes any user content; sandbox="" gives the iframe an opaque origin.
  return `<!doctype html><html><head><meta http-equiv="Content-Security-Policy" content="${WORKBENCH_PREVIEW_CSP}"><meta name="referrer" content="no-referrer"><style>body{font:14px/1.6 system-ui,sans-serif;margin:20px;overflow-wrap:anywhere}img,svg{max-width:100%;height:auto}pre{overflow:auto}table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:6px}</style></head><body>${container.innerHTML}</body></html>`
}

export function workbenchPreviewKind(ext: string, sample: Uint8Array): FilePreviewKind {
  const declared = resolvePreviewKind(ext)
  const sniffed = sniffPreview(sample)
  // PPTX/XLSX packages are validated before reaching the existing Office viewers.
  // Spreadsheet output, like HTML, is rendered in an opaque CSP-isolated frame.
  if (['pdf', 'docx', 'audio', 'video'].includes(declared)) return 'unsupported'
  if (declared === 'image') {
    if (sniffed.ext === 'svg') return 'html'
    return sniffed.kind === 'image' ? 'image' : 'unsupported'
  }
  if (declared === 'mermaid') return 'text'
  if (declared !== 'unsupported') return declared
  if (sniffed.ext === 'svg') return 'html'
  return ['text', 'html', 'image'].includes(sniffed.kind) ? sniffed.kind : 'unsupported'
}
