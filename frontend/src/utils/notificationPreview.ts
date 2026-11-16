import { marked, type Token, type Tokens } from 'marked'

function plainText(tokens: Token[]): string {
  return tokens.map(token => {
    switch (token.type) {
      case 'html': return ''
      case 'space':
      case 'br':
      case 'hr': return ' '
      case 'list': return (token as Tokens.List).items.map(item => plainText(item.tokens)).join(' ')
      case 'table': {
        const table = token as Tokens.Table
        return [table.header, ...table.rows]
          .map(row => row.map(cell => plainText(cell.tokens)).join(' ')).join(' ')
      }
      case 'paragraph':
      case 'heading':
      case 'blockquote': return plainText(token.tokens ?? []) + ' '
      default:
        if ('tokens' in token && token.tokens) return plainText(token.tokens)
        return 'text' in token ? token.text : ''
    }
  }).join('')
}

/** Notification excerpts are plain text, including when the saved Markdown is truncated. */
export function notificationPreview(source: string): string {
  return plainText(marked.lexer(source))
    .replace(/(^|\s)(\*\*|__|~~)(?=\S)/g, '$1')
    .replace(/(\*\*|__|~~)(?=\s|$)/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}
