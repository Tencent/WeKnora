import type { Tokens } from 'marked'
import { openMermaidFullscreen } from '@/utils/mermaidViewer.ts'

let mermaidMod: typeof import('mermaid') | null = null
let hljsMod: typeof import('highlight.js') | null = null
let mermaidInitialized = false
let initPromise: Promise<void> | null = null
let hljsPromise: Promise<typeof import('highlight.js').default> | null = null

const MERMAID_CONFIG = {
  startOnLoad: false,
  theme: 'default',
  securityLevel: 'strict',
  fontFamily: 'PingFang SC, Microsoft YaHei, sans-serif',
  flowchart: {
    useMaxWidth: true,
    htmlLabels: true,
    curve: 'basis',
  },
  sequence: {
    useMaxWidth: true,
    diagramMarginX: 8,
    diagramMarginY: 8,
    actorMargin: 50,
    width: 150,
    height: 65,
  },
  gantt: {
    useMaxWidth: true,
    leftPadding: 75,
    gridLineStartPadding: 35,
    barHeight: 20,
    barGap: 4,
    topPadding: 50,
  },
}

async function getMermaid() {
  if (!mermaidMod) {
    mermaidMod = await import('mermaid')
  }
  return mermaidMod.default
}

async function getHljs() {
  if (!hljsMod) {
    hljsMod = await import('highlight.js')
    await import('highlight.js/styles/github.css')
    hljsMod.default.registerAliases('mermaid', { languageName: 'plaintext' })
  }
  return hljsMod.default
}

function escapeHtml(text: string) {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

export const ensureMermaidInitialized = () => {
  if (!initPromise) {
    initPromise = (async () => {
      const mermaid = await getMermaid()
      if (!mermaidInitialized) {
        mermaid.initialize(MERMAID_CONFIG as Parameters<typeof mermaid.initialize>[0])
        mermaidInitialized = true
      }
    })()
  }
}

let mermaidCount = 0

export const createMermaidCodeRenderer = (idPrefix: string) => {
  if (!hljsPromise) {
    hljsPromise = getHljs()
  }

  return ({ text, lang }: Tokens.Code) => {
    let highlighted = escapeHtml(text)
    let highlightLang: string = lang || 'Code'
    const hljs = hljsMod?.default
    if (hljs) {
      if (highlightLang && hljs.getLanguage(highlightLang)) {
        try {
          highlighted = hljs.highlight(text, { language: highlightLang }).value
        } catch {
          const ret = hljs.highlightAuto(text)
          highlighted = ret.value
          highlightLang = ret.language || 'Code'
        }
      } else {
        const ret = hljs.highlightAuto(text)
        highlighted = ret.value
        highlightLang = ret.language || 'Code'
      }
    }
    if (lang === 'mermaid') {
      const id = `${idPrefix}-${++mermaidCount}`
      return `<pre id="${id}" data-mermaid="false"><code class="hljs language-${highlightLang}">${highlighted}</code></pre>`
    }
    return `<pre><code class="hljs language-${highlightLang}">${highlighted}</code></pre>`
  }
}

export const renderMermaidInContainer = async (
  rootElement: HTMLElement | null | undefined,
) => {
  if (!rootElement) return
  const mermaid = await getMermaid()
  ensureMermaidInitialized()
  await initPromise

  const mermaidElements = rootElement.querySelectorAll<HTMLElement>('pre[data-mermaid="false"]')
  for (const el of mermaidElements) {
    try {
      const code = el.innerText
      await mermaid.parse(code)
      const { svg } = await mermaid.render(`${el.id}-svg`, code)
      el.classList.add('mermaid')
      el.innerHTML = svg
      el.onclick = (event) => {
        event.stopPropagation()
        openMermaidFullscreen(svg)
      }
    } catch (e) {
      console.error('Mermaid rendering error:', e)
    }
    el.setAttribute('data-mermaid', 'true')
  }
}
