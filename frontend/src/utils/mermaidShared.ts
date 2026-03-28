import mermaid from 'mermaid';
import type {Tokens} from 'marked';
import {openMermaidFullscreen} from "@/utils/mermaidViewer.ts";
import hljs from "highlight.js";
import "highlight.js/styles/github.css";

hljs.registerAliases("mermaid", { languageName: "plaintext" });

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

let mermaidCount = 0;

export const createMermaidCodeRenderer = (idPrefix: string) => {
  return ({text, lang}: Tokens.Code) => {
    let highlighted = '';
    let highlightLang: string = lang || 'Code';
    if (highlightLang && hljs.getLanguage(highlightLang)) {
        try {
            highlighted = hljs.highlight(text, { language: lang }).value;
        } catch {
            let ret = hljs.highlightAuto(text);
            highlighted = ret.value;
            highlightLang = ret.language || "Code";
        }
    } else {
        let ret = hljs.highlightAuto(text);
        highlighted = ret.value;
        highlightLang = ret.language || "Code";
    }
    if (lang === 'mermaid') {
      const id = `${idPrefix}-${++mermaidCount}`;
      return `<pre id="${id}" data-mermaid="false"><code class="hljs language-${highlightLang}">${highlighted}</code></pre>`;
    }
    return `<pre><code class="hljs language-${highlightLang}">${highlighted}</code></pre>`;
  };
};

export const renderMermaidInContainer = async (
  rootElement: HTMLElement | null | undefined,
) => {
  if (!rootElement) return 0;
  const mermaidElements = rootElement.querySelectorAll<HTMLElement>('pre[data-mermaid="false"]');
  for (const el of mermaidElements) {
    try{
        const code = el.innerText;
        // 验证
        await mermaid.parse(code);
        // 渲染
        let {svg} = await mermaid.render(`${el.id}-svg`, code);
        el.classList.add('mermaid')
        el.innerHTML = svg;
        el.onclick = (event) => {
            event.stopPropagation();
            openMermaidFullscreen(svg);
        };
    } catch(e){
        console.error("Mermaid rendering error:", e);
    }
    // 标记为已渲染
    el.setAttribute('data-mermaid', 'true');
  }
};