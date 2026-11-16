import { marked, type Token, type Tokens } from 'marked'

function htmlText(template: HTMLTemplateElement, html: string): string {
  // A template keeps the fragment inert while we extract text for Vue interpolation.
  template.innerHTML = html
  template.content.querySelectorAll('script, style, template, noscript').forEach(node => node.remove())
  template.content.querySelectorAll('br, p, div, li, tr, td, th, h1, h2, h3, h4, h5, h6')
    .forEach(node => node.after(' '))
  return template.content.textContent ?? ''
}

function plainText(tokens: Token[], template?: HTMLTemplateElement): string {
  const extract = (children: Token[]) => plainText(children, template)
  return tokens.map(token => {
    switch (token.type) {
      case 'html': return token.block && template ? ` ${htmlText(template, token.text)} ` : ''
      case 'space':
      case 'br':
      case 'hr': return ' '
      case 'code':
      case 'codespan': return token.text
      case 'list': return (token as Tokens.List).items.map(item => extract(item.tokens)).join(' ')
      case 'table': {
        const table = token as Tokens.Table
        return [table.header, ...table.rows]
          .map(row => row.map(cell => extract(cell.tokens)).join(' ')).join(' ')
      }
      case 'paragraph':
      case 'heading':
      case 'blockquote': return extract(token.tokens ?? []) + ' '
      default: {
        if ('tokens' in token && token.tokens) return extract(token.tokens)
        const text = 'text' in token ? token.text : ''
        return template && text.includes('&') ? htmlText(template, text.replace(/</g, '&lt;')) : text
      }
    }
  }).join('')
}

/** Notification excerpts are plain text, including when the saved Markdown is truncated. */
export function notificationPreview(source: string): string {
  const template = typeof document === 'undefined' ? undefined : document.createElement('template')
  return plainText(marked.lexer(source), template)
    .replace(/(^|\s)(\*\*|__|~~)(?=\S)/g, '$1')
    .replace(/(\*\*|__|~~)(?=\s|$)/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}
