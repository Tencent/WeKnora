import mermaid from 'mermaid';
import 'marked';
import type {Tokens} from "marked";

let mermaidInitialized = false;

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
};

export const ensureMermaidInitialized = () => {
  if (mermaidInitialized) return;
  mermaid.initialize(MERMAID_CONFIG as any);
  mermaidInitialized = true;
};


export const createMermaidCodeRenderer = (idPrefix: string) => {
  let mermaidCount = 0;

  return ({lang, text}: Tokens.Code) => {
    if (lang === 'mermaid') {
      const id = `${idPrefix}-${++mermaidCount}`;
      return `<div class="mermaid" id="${id}">${text}</div>`;
    }

    const displayLang = lang || 'Code';
    const escapedCode = text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
    return `<pre><code class="language-${displayLang}">${escapedCode}</code></pre>`;
  };
};

export const renderMermaidInContainer = async (
  rootElement: HTMLElement | null | undefined,
  renderedMermaidIds: Set<string>,
) => {
  if (!rootElement) return 0;

  const mermaidElements = rootElement.querySelectorAll<HTMLElement>('.mermaid');
  const unrenderedElements: HTMLElement[] = [];

  mermaidElements.forEach((el) => {
    const id = el.id || `mermaid-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    if (!el.id) {
      el.id = id;
    }
    if (!renderedMermaidIds.has(el.id) && !el.querySelector('svg')) {
      renderedMermaidIds.add(el.id);
      unrenderedElements.push(el);
    }
  });

  if (unrenderedElements.length === 0) return 0;

  await mermaid.run({ nodes: unrenderedElements });
  return unrenderedElements.length;
};

export const bindMermaidFullscreenEvents = (
  rootElement: HTMLElement | null | undefined,
  onOpenFullscreen: (svgOuterHTML: string) => void,
) => {
  if (!rootElement) return;

  const mermaidDivs = rootElement.querySelectorAll<HTMLElement>('.mermaid');
  mermaidDivs.forEach((div) => {
    div.style.cursor = 'pointer';
    const oldHandler = (div as any).__mermaidClickHandler as EventListener | undefined;
    if (oldHandler) {
      div.removeEventListener('click', oldHandler);
    }
    const handler: EventListener = (e: Event) => {
      e.stopPropagation();
      const target = e.currentTarget as HTMLElement;
      const svg = target.querySelector('svg');
      if (svg) {
        onOpenFullscreen(svg.outerHTML);
      }
    };
    (div as any).__mermaidClickHandler = handler;
    div.addEventListener('click', handler);
  });
};
