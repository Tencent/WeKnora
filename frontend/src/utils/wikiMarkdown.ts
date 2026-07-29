import { Marked } from 'marked'

import { sanitizeMarkdownHTML } from './security'

// Keep Wiki rendering isolated from the global marked singleton. Other
// long-lived knowledge-base views register custom renderers globally; a
// private instance prevents opening a source document from changing how the
// Wiki is rendered when the user comes back.
const wikiMarkdown = new Marked({ gfm: true, breaks: true })

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[character] || character)
}

function slugDisplayName(slug: string): string {
  const parts = slug.split('/')
  return parts[parts.length - 1] || slug
}

export function renderWikiMarkdown(content: string): string {
  const preprocessed = (content || '').replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
    const pipeIndex = inner.indexOf('|')
    const slug = pipeIndex > 0 ? inner.substring(0, pipeIndex).trim() : inner.trim()
    const display = pipeIndex > 0 ? inner.substring(pipeIndex + 1).trim() : slugDisplayName(slug)
    return `<a href="#" class="wiki-content-link" data-slug="${escapeHTML(slug)}">${escapeHTML(display)}</a>`
  })
  const html = wikiMarkdown.parse(preprocessed, { async: false }) as string
  return sanitizeMarkdownHTML(html)
}
