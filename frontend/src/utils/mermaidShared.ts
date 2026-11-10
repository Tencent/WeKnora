import { mermaidConfig } from './mermaidConfig'
import type { Tokens } from 'marked'
import hljs from 'highlight.js'
import 'highlight.js/styles/github.css'
import { openMermaidFullscreen } from '@/utils/mermaidViewer.ts'
import {
  buildCodeBlockHtml,
  buildMermaidBlockHtml,
  attachMarkdownEnhancementListeners,
  highlightCodeBlocksInContainer,
  syncMermaidExpandButtons,
} from '@/utils/markdownEnhancements'

hljs.registerAliases('mermaid', { languageName: 'plaintext' })

let mermaidMod: typeof import('mermaid') | null = null
let mermaidInitialized = false
let initPromise: Promise<void> | null = null

async function getMermaid() {
  if (!mermaidMod) {
    mermaidMod = await import('mermaid')
  }
  return mermaidMod.default
}

function escapeHtml(text: string) {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function highlightCode(text: string, lang?: string | null) {
  const language = (lang || '').trim()
  if (language && hljs.getLanguage(language)) {
    try {
      const result = hljs.highlight(text, { language })
      return { html: result.value, language: result.language || language }
    } catch {
      // fall through
    }
  }
  const auto = hljs.highlightAuto(text, language ? [language] : undefined)
  return { html: auto.value, language: auto.language || language || 'plaintext' }
}

export const ensureMermaidInitialized = () => {
  if (!initPromise) {
    initPromise = (async () => {
      const mermaid = await getMermaid()
      if (!mermaidInitialized) {
        mermaid.initialize({
          ...mermaidConfig(document.documentElement.getAttribute('theme-mode') === 'dark'),
        } as Parameters<typeof mermaid.initialize>[0])
        mermaidInitialized = true
      }
    })()
  }
}

let mermaidCount = 0

export const createMermaidCodeRenderer = (idPrefix: string) => {
  return ({ text, lang }: Tokens.Code) => {
    const { html: highlighted, language: highlightLang } = highlightCode(text, lang)
    if (lang === 'mermaid') {
      const id = `${idPrefix}-${++mermaidCount}`
      const inner = `<code class="hljs language-${highlightLang}">${highlighted}</code>`
      return buildMermaidBlockHtml(inner, `id="${id}" data-mermaid="false"`)
    }
    return buildCodeBlockHtml(lang || highlightLang, highlighted, highlightLang)
  }
}

export const renderMermaidToSvg = async (code: string, id?: string): Promise<string | null> => {
  if (!code.trim()) return null
  try {
    const mermaid = await getMermaid()
    ensureMermaidInitialized()
    await initPromise
    await mermaid.parse(code)
    // Mermaid reuses the render id as the SVG root id. Always mint a fresh
    // one so a later render cannot collide with an SVG already in the DOM.
    const renderId = `${id || 'mermaid'}-${++mermaidCount}`
    const { svg } = await mermaid.render(renderId, code)
    return svg
  } catch (e) {
    console.error('Mermaid rendering error:', e)
    return null
  }
}

function mermaidSourceFromElement(el: HTMLElement): string {
  const codeEl = el.querySelector('code')
  return (codeEl?.textContent ?? el.textContent ?? '').trim()
}

export async function appendMermaidSvgCache(
  codes: string[],
  cache: readonly string[],
  idPrefix: string,
): Promise<string[]> {
  const next = cache.slice()
  while (next.length < codes.length) {
    const i = next.length
    const svg = await renderMermaidToSvg(codes[i], `${idPrefix}-${i}`)
    next.push(svg || '')
  }
  return next
}

export const renderMermaidInContainer = async (
  rootElement: HTMLElement | null | undefined,
) => {
  if (!rootElement) return

  const mermaid = await getMermaid()
  ensureMermaidInitialized()
  await initPromise

  const mermaidElements = rootElement.querySelectorAll<HTMLElement>(
    'pre[data-mermaid="false"], .chat-mermaid-block__canvas[data-mermaid="false"]',
  )
  for (const el of mermaidElements) {
    try {
      if (el.querySelector('svg')) {
        el.setAttribute('data-mermaid', 'true')
        continue
      }
      const code = mermaidSourceFromElement(el)
      if (!code) continue
      await mermaid.parse(code)
      const renderId = `mermaid-render-${++mermaidCount}`
      const { svg } = await mermaid.render(renderId, code)
      el.classList.add('mermaid')
      el.innerHTML = svg
      el.setAttribute('data-mermaid', 'true')
    } catch (e) {
      console.error('Mermaid rendering error:', e)
      continue
    }
  }
}

export async function enhanceMarkdownContainer(
  rootElement: HTMLElement | null | undefined,
): Promise<void> {
  if (!rootElement) return
  attachMarkdownEnhancementListeners(rootElement)
  highlightCodeBlocksInContainer(rootElement)
  await renderMermaidInContainer(rootElement)
  syncMermaidExpandButtons(rootElement)
}
