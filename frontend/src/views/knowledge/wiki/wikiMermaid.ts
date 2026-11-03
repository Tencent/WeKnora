import { Renderer, type Tokens } from 'marked'

const MERMAID_PENDING_SELECTOR = 'pre.mermaid[data-mermaid="false"]'

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

// Wiki pages keep marked's default code rendering; only ```mermaid fences are
// emitted as a pending `<pre class="mermaid">` that renderMermaidInContainer
// later swaps for the SVG, the same hook the chat answer uses.
export function createWikiMarkdownRenderer(): Renderer {
  const renderer = new Renderer()
  renderer.code = function (this: Renderer, token: Tokens.Code): string {
    if ((token.lang || '').trim().split(/\s+/)[0] === 'mermaid') {
      return `<pre class="mermaid" data-mermaid="false"><code>${escapeHtml(token.text)}</code></pre>\n`
    }
    return Renderer.prototype.code.call(this, token)
  }
  return renderer
}

export async function renderWikiMermaid(root: HTMLElement | null | undefined): Promise<void> {
  if (!root || !root.querySelector(MERMAID_PENDING_SELECTOR)) return
  const { renderMermaidInContainer } = await import('@/utils/mermaidShared')
  await renderMermaidInContainer(root)
}
