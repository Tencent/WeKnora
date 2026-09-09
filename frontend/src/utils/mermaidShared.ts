import type { Tokens } from 'marked'
import hljs from 'highlight.js'
import 'highlight.js/styles/github.css'
import { openMermaidFullscreen } from '@/utils/mermaidViewer.ts'
import {
  buildCodeBlockHtml,
  buildMermaidBlockHtml,
  buildMermaidErrorFragment,
  attachMarkdownEnhancementListeners,
  highlightCodeBlocksInContainer,
  syncMermaidExpandButtons,
} from '@/utils/markdownEnhancements'
import { encodeMermaidRenderError } from '@/utils/mermaidStreaming'

hljs.registerAliases('mermaid', { languageName: 'plaintext' })

let mermaidMod: typeof import('mermaid') | null = null
let mermaidInitialized = false
let initPromise: Promise<void> | null = null

const MERMAID_LIGHT_THEME = {
  darkMode: false,
  background: '#ffffff',
  primaryColor: '#e2e8f0',
  primaryTextColor: '#334155',
  primaryBorderColor: '#94a3b8',
  secondaryColor: '#f1f5f9',
  secondaryTextColor: '#475569',
  secondaryBorderColor: '#cbd5e1',
  tertiaryColor: '#f8fafc',
  tertiaryTextColor: '#64748b',
  tertiaryBorderColor: '#e2e8f0',
  lineColor: '#94a3b8',
  textColor: '#334155',
  mainBkg: '#ffffff',
  nodeBorder: '#94a3b8',
  clusterBkg: '#f8fafc',
  clusterBorder: '#cbd5e1',
  titleColor: '#1e293b',
  edgeLabelBackground: '#ffffff',
  actorBorder: '#94a3b8',
  actorBkg: '#f1f5f9',
  actorTextColor: '#334155',
  actorLineColor: '#94a3b8',
  signalColor: '#94a3b8',
  labelBoxBkgColor: '#f1f5f9',
  labelBoxBorderColor: '#cbd5e1',
  labelTextColor: '#334155',
  loopTextColor: '#475569',
  noteBkgColor: '#f8fafc',
  noteTextColor: '#475569',
  noteBorderColor: '#cbd5e1',
  // Gantt
  sectionBkgColor: '#f8fafc',
  altSectionBkgColor: '#ffffff',
  gridColor: '#e2e8f0',
  todayLineColor: '#64748b',
  taskBorderColor: '#94a3b8',
  taskBkgColor: '#e2e8f0',
  activeTaskBorderColor: '#64748b',
  activeTaskBkgColor: '#94a3b8',
  doneTaskBkgColor: '#cbd5e1',
  doneTaskBorderColor: '#94a3b8',
  critBkgColor: '#fecaca',
  critBorderColor: '#f87171',
  fontSize: '14px',
}

const MERMAID_DARK_THEME = {
  darkMode: true,
  background: '#1a1f28',
  primaryColor: '#475569',
  primaryTextColor: '#e2e8f0',
  primaryBorderColor: '#64748b',
  secondaryColor: '#334155',
  secondaryTextColor: '#cbd5e1',
  secondaryBorderColor: '#64748b',
  tertiaryColor: '#1e293b',
  tertiaryTextColor: '#94a3b8',
  tertiaryBorderColor: '#475569',
  lineColor: '#64748b',
  textColor: '#e2e8f0',
  mainBkg: '#1e293b',
  nodeBorder: '#64748b',
  clusterBkg: '#1a2332',
  clusterBorder: '#475569',
  titleColor: '#f1f5f9',
  edgeLabelBackground: '#1e293b',
  actorBorder: '#64748b',
  actorBkg: '#1e293b',
  actorTextColor: '#f1f5f9',
  actorLineColor: '#64748b',
  signalColor: '#64748b',
  labelBoxBkgColor: '#334155',
  labelBoxBorderColor: '#64748b',
  labelTextColor: '#e2e8f0',
  loopTextColor: '#cbd5e1',
  noteBkgColor: '#334155',
  noteTextColor: '#e2e8f0',
  noteBorderColor: '#64748b',
  // Gantt
  sectionBkgColor: '#1a2332',
  altSectionBkgColor: '#1e293b',
  gridColor: '#334155',
  todayLineColor: '#94a3b8',
  taskBorderColor: '#64748b',
  taskBkgColor: '#475569',
  activeTaskBorderColor: '#94a3b8',
  activeTaskBkgColor: '#64748b',
  doneTaskBkgColor: '#334155',
  doneTaskBorderColor: '#64748b',
  critBkgColor: '#7f1d1d',
  critBorderColor: '#ef4444',
  fontSize: '14px',
}

function resolveMermaidThemeVariables() {
  const isDark = document.documentElement.getAttribute('theme-mode') === 'dark'
  return isDark ? MERMAID_DARK_THEME : MERMAID_LIGHT_THEME
}

const MERMAID_CONFIG = {
  startOnLoad: false,
  theme: 'base' as const,
  securityLevel: 'strict' as const,
  fontFamily: 'PingFang SC, Microsoft YaHei, sans-serif',
  flowchart: {
    useMaxWidth: true,
    htmlLabels: true,
    curve: 'basis',
    padding: 16,
  },
  sequence: {
    useMaxWidth: true,
    diagramMarginX: 12,
    diagramMarginY: 12,
    actorMargin: 56,
    width: 156,
    height: 68,
    boxMargin: 10,
  },
  gantt: {
    useMaxWidth: true,
    leftPadding: 80,
    gridLineStartPadding: 40,
    barHeight: 22,
    barGap: 6,
    topPadding: 56,
  },
  er: {
    useMaxWidth: true,
  },
  journey: {
    useMaxWidth: true,
  },
}

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
          ...MERMAID_CONFIG,
          themeVariables: resolveMermaidThemeVariables(),
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

const MERMAID_ERROR_MESSAGE_MAX_LINES = 4
const MERMAID_ERROR_MESSAGE_MAX_CHARS = 300

function mermaidErrorMessage(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e)
  // UnknownDiagramError repeats the entire diagram after "for text:"; the
  // source is already available in the collapsible block under the message.
  const withoutSource = raw.replace(/\s*for text:[\s\S]*$/, '').trim()
  const lines = withoutSource.split('\n').slice(0, MERMAID_ERROR_MESSAGE_MAX_LINES).join('\n')
  return lines.length > MERMAID_ERROR_MESSAGE_MAX_CHARS
    ? `${lines.slice(0, MERMAID_ERROR_MESSAGE_MAX_CHARS)}…`
    : lines
}

type MermaidRenderResult = { svg: string } | { error: string }

// Shared by renderMermaidToSvg (existing string|null contract, kept for
// document-preview.vue and any other simple caller) and the chat render
// paths, which also need the caught message to show the user something
// more useful than a silently-dropped diagram.
//
// Returns null only when mermaid itself could not be loaded/initialised —
// that is not a diagram problem, so callers leave the slot unresolved and a
// later pass retries. An {error} is always a genuine parse/render failure.
async function renderMermaidDiagnostic(code: string, id?: string): Promise<MermaidRenderResult | null> {
  let mermaid: Awaited<ReturnType<typeof getMermaid>>
  try {
    mermaid = await getMermaid()
    ensureMermaidInitialized()
    await initPromise
  } catch (e) {
    console.error('Mermaid load error:', e)
    return null
  }
  try {
    await mermaid.parse(code)
    // Mermaid reuses the render id as the SVG root id. Always mint a fresh
    // one so a later render cannot collide with an SVG already in the DOM.
    const renderId = `${id || 'mermaid'}-${++mermaidCount}`
    const { svg } = await mermaid.render(renderId, code)
    return { svg }
  } catch (e) {
    console.error('Mermaid rendering error:', e)
    return { error: mermaidErrorMessage(e) }
  }
}

export const renderMermaidToSvg = async (code: string, id?: string): Promise<string | null> => {
  if (!code.trim()) return null
  const result = await renderMermaidDiagnostic(code, id)
  return result && 'svg' in result ? result.svg : null
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
    const result = await renderMermaidDiagnostic(codes[i], `${idPrefix}-${i}`)
    if (!result) {
      // Mermaid unavailable: leave the slot unresolved so it is retried.
      next.push('')
    } else if ('svg' in result) {
      next.push(result.svg)
    } else {
      next.push(encodeMermaidRenderError(codes[i], result.error))
    }
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
    let code = ''
    try {
      if (el.querySelector('svg')) {
        el.setAttribute('data-mermaid', 'true')
        continue
      }
      code = mermaidSourceFromElement(el)
      if (!code) continue
      await mermaid.parse(code)
      const renderId = `mermaid-render-${++mermaidCount}`
      const { svg } = await mermaid.render(renderId, code)
      el.classList.add('mermaid')
      el.innerHTML = svg
      el.setAttribute('data-mermaid', 'true')
    } catch (e) {
      console.error('Mermaid rendering error:', e)
      el.innerHTML = buildMermaidErrorFragment(code, mermaidErrorMessage(e))
      el.setAttribute('data-mermaid', 'error')
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
