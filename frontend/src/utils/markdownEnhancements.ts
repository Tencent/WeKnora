import i18n from '@/i18n';
import hljs from 'highlight.js';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';

const ENHANCE_FLAG = 'data-markdown-enhancements';
const boundMarkdownRoots = new WeakSet<HTMLElement>();

const MERMAID_DIAGRAM_SVG_SELECTOR = '.chat-mermaid-block__canvas svg, pre.mermaid svg';

function getMermaidDiagramSvg(block: Element | null | undefined): SVGElement | null {
  if (!block) return null;
  const svg = block.querySelector(MERMAID_DIAGRAM_SVG_SELECTOR);
  return svg instanceof SVGElement ? svg : null;
}

function openMermaidFromBlock(block: Element | null | undefined): void {
  const svg = getMermaidDiagramSvg(block);
  if (svg) openMermaidFullscreen(svg.outerHTML);
}

export function syncMermaidExpandButtons(root: HTMLElement | null | undefined): void {
  if (!root) return;
  root.querySelectorAll<HTMLElement>('.chat-mermaid-block').forEach((block) => {
    const expandBtn = block.querySelector<HTMLButtonElement>('.chat-mermaid-block__expand');
    if (!expandBtn) return;
    const hasSvg = !!getMermaidDiagramSvg(block);
    expandBtn.disabled = !hasSvg;
    expandBtn.classList.toggle('is-disabled', !hasSvg);
  });
}

const COPY_ICON = '<svg class="chat-code-block__copy-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>';

const EXPAND_ICON = '<svg class="chat-mermaid-block__expand-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/></svg>';

const LANG_LABELS: Record<string, string> = {
  js: 'JavaScript',
  javascript: 'JavaScript',
  ts: 'TypeScript',
  typescript: 'TypeScript',
  py: 'Python',
  python: 'Python',
  go: 'Go',
  rust: 'Rust',
  java: 'Java',
  kotlin: 'Kotlin',
  swift: 'Swift',
  rb: 'Ruby',
  ruby: 'Ruby',
  php: 'PHP',
  cs: 'C#',
  cpp: 'C++',
  c: 'C',
  sql: 'SQL',
  bash: 'Bash',
  sh: 'Shell',
  shell: 'Shell',
  json: 'JSON',
  yaml: 'YAML',
  yml: 'YAML',
  xml: 'XML',
  html: 'HTML',
  css: 'CSS',
  markdown: 'Markdown',
  md: 'Markdown',
};

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

export function formatCodeLang(lang: string): string {
  const normalized = (lang || 'Code').trim();
  if (!normalized) return 'Code';
  const key = normalized.toLowerCase();
  return LANG_LABELS[key] || normalized.charAt(0).toUpperCase() + normalized.slice(1);
}

export function buildCodeBlockHtml(
  lang: string,
  highlighted: string,
  highlightLang: string,
): string {
  const displayLang = escapeHtml(formatCodeLang(lang));
  const copyLabel = escapeHtml(i18n.global.t('embedPublish.copyCode'));
  const safeLang = escapeHtml(highlightLang || lang || 'text');
  return `<div class="chat-code-block">
    <div class="chat-code-block__header">
      <span class="chat-code-block__lang">${displayLang}</span>
      <div class="chat-code-block__actions">
        <button type="button" class="chat-code-block__copy" aria-label="${copyLabel}" title="${copyLabel}">
          ${COPY_ICON}<span class="chat-code-block__copy-text">${copyLabel}</span>
        </button>
      </div>
    </div>
    <pre class="chat-code-block__pre"><code class="hljs language-${safeLang}">${highlighted}</code></pre>
  </div>`;
}

export function buildMermaidBlockHtml(innerHtml: string, preAttrs = ''): string {
  const label = escapeHtml(i18n.global.t('mermaid.diagram'));
  const expandLabel = escapeHtml(i18n.global.t('mermaid.expand'));
  const attrs = preAttrs ? ` ${preAttrs}` : '';
  return `<div class="chat-mermaid-block">
    <div class="chat-mermaid-block__header">
      <span class="chat-mermaid-block__badge">${label}</span>
      <div class="chat-mermaid-block__actions">
        <button type="button" class="chat-mermaid-block__expand" aria-label="${expandLabel}" title="${expandLabel}">${EXPAND_ICON}</button>
      </div>
    </div>
    <pre class="chat-mermaid-block__canvas mermaid"${attrs}>${innerHtml}</pre>
  </div>`;
}

export function buildMermaidLoadingHtml(): string {
  const label = escapeHtml(i18n.global.t('mermaid.diagram'));
  return `<div class="chat-mermaid-block chat-mermaid-block--loading">
    <div class="chat-mermaid-block__header">
      <span class="chat-mermaid-block__badge">${label}</span>
    </div>
    <div class="streaming-mermaid-loading" aria-hidden="true"><span class="streaming-mermaid-loading__skeleton"></span></div>
  </div>`;
}

async function handleCodeCopy(btn: HTMLButtonElement): Promise<void> {
  const code = btn.closest('.chat-code-block')?.querySelector('code')?.textContent ?? '';
  if (!code) return;

  const textEl = btn.querySelector<HTMLElement>('.chat-code-block__copy-text');
  const copiedLabel = i18n.global.t('common.copied');
  const defaultLabel = i18n.global.t('embedPublish.copyCode');

  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(code);
    } else {
      const textArea = document.createElement('textarea');
      textArea.value = code;
      textArea.style.position = 'fixed';
      textArea.style.opacity = '0';
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
    }
    btn.classList.add('is-copied');
    if (textEl) textEl.textContent = copiedLabel;
    window.setTimeout(() => {
      btn.classList.remove('is-copied');
      if (textEl) textEl.textContent = defaultLabel;
    }, 1600);
  } catch {
    btn.classList.add('is-error');
    window.setTimeout(() => btn.classList.remove('is-error'), 1600);
  }
}

export function highlightCodeBlocksInContainer(root: HTMLElement | null | undefined): void {
  if (!root) return;

  root.querySelectorAll<HTMLElement>('.chat-code-block__pre code').forEach((codeEl) => {
    if (codeEl.querySelector('span[class^="hljs-"], span.hljs')) return;
    const language = [...codeEl.classList]
      .find((cls) => cls.startsWith('language-'))
      ?.slice('language-'.length);
    if (language && hljs.getLanguage(language)) {
      try {
        codeEl.innerHTML = hljs.highlight(codeEl.textContent || '', { language }).value;
        codeEl.classList.add('hljs');
        return;
      } catch {
        // fall through
      }
    }
    hljs.highlightElement(codeEl);
  });
}

export function refreshMarkdownEnhancements(root: HTMLElement | null | undefined): void {
  if (!root) return;
  highlightCodeBlocksInContainer(root);
  syncMermaidExpandButtons(root);
}

export function attachMarkdownEnhancementListeners(
  root: HTMLElement | null | undefined,
): void {
  if (!root || boundMarkdownRoots.has(root)) return;
  boundMarkdownRoots.add(root);
  root.setAttribute(ENHANCE_FLAG, 'true');

  const onClick = (event: Event) => {
    const target = event.target as HTMLElement;

    const copyBtn = target.closest<HTMLButtonElement>('.chat-code-block__copy');
    if (copyBtn) {
      event.preventDefault();
      event.stopPropagation();
      void handleCodeCopy(copyBtn);
      return;
    }

    const expandBtn = target.closest<HTMLButtonElement>('.chat-mermaid-block__expand');
    if (expandBtn) {
      event.preventDefault();
      event.stopPropagation();
      if (expandBtn.disabled) return;
      openMermaidFromBlock(expandBtn.closest('.chat-mermaid-block'));
      return;
    }

    const canvas = target.closest<HTMLElement>(
      '.chat-mermaid-block__canvas[data-mermaid="true"], .chat-mermaid-block__canvas[data-mermaid="cached"], pre.mermaid[data-mermaid="true"], pre.mermaid[data-mermaid="cached"]',
    );
    if (canvas && !target.closest('button')) {
      const svg = canvas.querySelector('svg');
      if (svg) {
        event.preventDefault();
        event.stopPropagation();
        openMermaidFullscreen(svg.outerHTML);
      }
    }
  };

  root.addEventListener('click', onClick, true);
  syncMermaidExpandButtons(root);
}
