<script setup lang="ts">
// @ts-nocheck
import { marked } from "marked";
import markedKatex from 'marked-katex-extension';
import 'katex/dist/katex.min.css';

import hljs from "highlight.js";
import "highlight.js/styles/github.css";
import mermaid from "mermaid";
import { onMounted, ref, nextTick, onUnmounted, watch, computed } from "vue";
import { downKnowledgeDetails, deleteGeneratedQuestion, getChunkByIdOnly, previewKnowledgeFile } from "@/api/knowledge-base/index";
import { MessagePlugin, DialogPlugin } from "tdesign-vue-next";
import { sanitizeHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL, hydrateProtectedFileImages, isValidURL } from '@/utils/security';
import { normalizeSpuriousTablePrefixes } from '@/utils/markdownTableNormalize';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';
import { useI18n } from 'vue-i18n';
import { useAuthStore } from '@/stores/auth';
import DocumentPreview from '@/components/document-preview.vue';
import KnowledgeProcessingTimeline from '@/components/knowledge-processing-timeline.vue';

const { t } = useI18n();
const authStore = useAuthStore();

// canDeleteGeneratedQuestion 对应后端 DELETE /chunks/by-id/:id/questions
// 的 OwnedChunkKBOrAdminFromChunkID 守卫——KB 创建者或租户 Admin+
// 才允许删除。父组件 KnowledgeBase.vue 通过 :canEditKB 把 KB 级权限
// 传下来（包含 KB creator / Admin / 组织分享 editor 三种来源），未
// 传时按更严格的 Admin 兜底，避免 Viewer 看到一个会 403 的入口。
const canDeleteGeneratedQuestion = computed(() => {
  if (props.canEditKB === true) return true;
  return authStore.hasRole('admin');
});

// Mermaid 初始化计数器，用于生成唯一ID
let mermaidRenderCount = 0;

// 初始化 Mermaid
mermaid.initialize({
  startOnLoad: false,
  theme: 'default',
  securityLevel: 'strict',
  fontFamily: 'PingFang SC, Microsoft YaHei, sans-serif',
  flowchart: {
    useMaxWidth: true,
    htmlLabels: true,
    curve: 'basis'
  },
  sequence: {
    useMaxWidth: true,
    diagramMarginX: 8,
    diagramMarginY: 8,
    actorMargin: 50,
    width: 150,
    height: 65
  },
  gantt: {
    useMaxWidth: true,
    leftPadding: 75,
    gridLineStartPadding: 35,
    barHeight: 20,
    barGap: 4,
    topPadding: 50
  }
});
const props = defineProps(["visible", "details", "knowledgeType", "sourceInfo", "canEditKB", "parse_status"]);
const emit = defineEmits(["closeDoc", "getDoc", "questionDeleted"]);

const hasTimelineSpans = ref(false);
const timelineDrawerVisible = ref(false);
const timelineSummary = ref<{ totalMs: number; status: string; stageIndex: number; stageTotal: number; stageLabel: string }>({
  totalMs: 0, status: '', stageIndex: 0, stageTotal: 0, stageLabel: '',
});

watch(() => props.details?.id, () => {
  hasTimelineSpans.value = false;
  timelineDrawerVisible.value = false;
  timelineSummary.value = { totalMs: 0, status: '', stageIndex: 0, stageTotal: 0, stageLabel: '' };
});

function formatTimelineDuration(ms: number): string {
  if (!ms || ms < 0) return '—';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(2)}s`;
  const mins = Math.floor(ms / 60000);
  const rem = ((ms % 60000) / 1000).toFixed(1);
  return `${mins}m${rem}s`;
}

function openTimeline() {
  timelineDrawerVisible.value = true;
}

function closeTimeline() {
  timelineDrawerVisible.value = false;
}

const TRACE_DRAWER_WIDTH_KEY = 'weknora-trace-drawer-width';
const TRACE_DRAWER_DEFAULT_WIDTH = 820;
const TRACE_DRAWER_MIN_WIDTH = 560;

const timelineDrawerWidth = ref(TRACE_DRAWER_DEFAULT_WIDTH);
const timelineDrawerResizing = ref(false);

let traceResizeStartX = 0;
let traceResizeStartWidth = 0;

function traceDrawerMaxWidth() {
  return Math.min(1400, Math.max(TRACE_DRAWER_MIN_WIDTH, Math.floor(window.innerWidth * 0.92)));
}

function clampTraceDrawerWidth(width: number) {
  return Math.max(TRACE_DRAWER_MIN_WIDTH, Math.min(traceDrawerMaxWidth(), width));
}

function loadTraceDrawerWidth() {
  try {
    const raw = localStorage.getItem(TRACE_DRAWER_WIDTH_KEY);
    const parsed = raw ? parseInt(raw, 10) : NaN;
    if (!Number.isNaN(parsed)) {
      timelineDrawerWidth.value = clampTraceDrawerWidth(parsed);
    }
  } catch {
    /* ignore quota / private mode */
  }
}

function onTraceDrawerResizeStart(e: MouseEvent) {
  timelineDrawerResizing.value = true;
  traceResizeStartX = e.clientX;
  traceResizeStartWidth = timelineDrawerWidth.value;
  document.addEventListener('mousemove', onTraceDrawerResizeMove);
  document.addEventListener('mouseup', onTraceDrawerResizeEnd);
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
}

function onTraceDrawerResizeMove(e: MouseEvent) {
  const delta = traceResizeStartX - e.clientX;
  timelineDrawerWidth.value = clampTraceDrawerWidth(traceResizeStartWidth + delta);
}

function onTraceDrawerResizeEnd() {
  document.removeEventListener('mousemove', onTraceDrawerResizeMove);
  document.removeEventListener('mouseup', onTraceDrawerResizeEnd);
  document.body.style.cursor = '';
  document.body.style.userSelect = '';
  timelineDrawerResizing.value = false;
  try {
    localStorage.setItem(TRACE_DRAWER_WIDTH_KEY, String(timelineDrawerWidth.value));
  } catch {
    /* ignore */
  }
}

function onTraceDrawerWindowResize() {
  timelineDrawerWidth.value = clampTraceDrawerWidth(timelineDrawerWidth.value);
  mainDrawerWidth.value = clampMainDrawerWidth(mainDrawerWidth.value);
}

function cleanupTraceDrawerResize() {
  document.removeEventListener('mousemove', onTraceDrawerResizeMove);
  document.removeEventListener('mouseup', onTraceDrawerResizeEnd);
  document.body.style.cursor = '';
  document.body.style.userSelect = '';
  timelineDrawerResizing.value = false;
}

// ============== 主抽屉（文档详情）宽度可调 ==============
const MAIN_DRAWER_WIDTH_KEY = 'weknora-doc-drawer-width';
const MAIN_DRAWER_DEFAULT_WIDTH = 654;
const MAIN_DRAWER_MIN_WIDTH = 480;

const mainDrawerWidth = ref(MAIN_DRAWER_DEFAULT_WIDTH);
const mainDrawerResizing = ref(false);

let mainResizeStartX = 0;
let mainResizeStartWidth = 0;

function mainDrawerMaxWidth() {
  return Math.min(1600, Math.max(MAIN_DRAWER_MIN_WIDTH, Math.floor(window.innerWidth * 0.95)));
}

function clampMainDrawerWidth(width: number) {
  return Math.max(MAIN_DRAWER_MIN_WIDTH, Math.min(mainDrawerMaxWidth(), width));
}

function loadMainDrawerWidth() {
  try {
    const raw = localStorage.getItem(MAIN_DRAWER_WIDTH_KEY);
    const parsed = raw ? parseInt(raw, 10) : NaN;
    if (!Number.isNaN(parsed)) {
      mainDrawerWidth.value = clampMainDrawerWidth(parsed);
    }
  } catch {
    /* ignore quota / private mode */
  }
}

function onMainDrawerResizeStart(e: MouseEvent) {
  mainDrawerResizing.value = true;
  mainResizeStartX = e.clientX;
  mainResizeStartWidth = mainDrawerWidth.value;
  document.addEventListener('mousemove', onMainDrawerResizeMove);
  document.addEventListener('mouseup', onMainDrawerResizeEnd);
  document.body.style.cursor = 'col-resize';
  document.body.style.userSelect = 'none';
}

function onMainDrawerResizeMove(e: MouseEvent) {
  // 抽屉在右侧，向左拖动变宽
  const delta = mainResizeStartX - e.clientX;
  mainDrawerWidth.value = clampMainDrawerWidth(mainResizeStartWidth + delta);
}

function onMainDrawerResizeEnd() {
  document.removeEventListener('mousemove', onMainDrawerResizeMove);
  document.removeEventListener('mouseup', onMainDrawerResizeEnd);
  document.body.style.cursor = '';
  document.body.style.userSelect = '';
  mainDrawerResizing.value = false;
  try {
    localStorage.setItem(MAIN_DRAWER_WIDTH_KEY, String(mainDrawerWidth.value));
  } catch {
    /* ignore */
  }
}

function cleanupMainDrawerResize() {
  document.removeEventListener('mousemove', onMainDrawerResizeMove);
  document.removeEventListener('mouseup', onMainDrawerResizeEnd);
  document.body.style.cursor = '';
  document.body.style.userSelect = '';
  mainDrawerResizing.value = false;
}

const traceEntryTheme = computed(() => {
  const s = timelineSummary.value.status || '';
  switch (s) {
    case 'done':
    case 'completed':
      return 'success';
    case 'failed':
      return 'danger';
    case 'running':
    case 'processing':
    case 'pending':
      return 'warning';
    default:
      return 'default';
  }
});

const traceEntryTitle = computed(() => {
  let tip = t('knowledgeStages.viewTrace');
  if (timelineSummary.value.totalMs > 0) {
    tip += ` · ${formatTimelineDuration(timelineSummary.value.totalMs)}`;
  } else if (timelineSummary.value.stageTotal > 0) {
    tip += ` · ${timelineSummary.value.stageIndex}/${timelineSummary.value.stageTotal}`;
  }
  return tip;
});

// Exposed so the parent's three-dot menu can jump straight into the
// trace drawer for a card without forcing the user to click the
// detail drawer header link manually.
defineExpose({ openTimeline });

marked.use({
  breaks: true,      // 启用单行换行转 <br>
  gfm: true,         // 启用 GitHub Flavored Markdown
});
marked.use(markedKatex({ throwOnError: false, nonStandard: true }));

const preprocessMathDelimiters = (rawText: string): string => {
  if (!rawText || typeof rawText !== 'string') {
    return '';
  }
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$');
};
const renderer = new marked.Renderer();
let page = 1;
let loadingChunks = false;
let pendingRequestedPage: number | null = null;
let pendingChunksBeforeLoad = 0;
const CHUNK_PAGE_SIZE = 25;
/** Scroll container for the main doc drawer (not the first .t-drawer__body on the page). */
let docScrollEl: HTMLElement | null = null;
let mdContentWrap = ref()
// Drawer uses attach="body", so markdown nodes live outside mdContentWrap in the DOM.
const docMarkdownRoot = ref<HTMLElement | null>(null)

const getMarkdownRenderRoot = (): ParentNode | null =>
  docMarkdownRoot.value ?? (mdContentWrap.value as ParentNode | null) ?? null
let url = ref('')
// 视图模式：chunks / merged / preview
// file 类型默认「预览」，URL / 手动创建 默认「全文」
const viewMode = ref<'chunks' | 'merged' | 'preview'>('merged');

// 合并后的文档内容（在下方通过 computed 定义）

/**
 * 把已合并文本 acc 和下一个 chunk 内容 next 拼接，并去除两者的重叠部分。
 *
 * 不再依赖 start_at / end_at 做位置裁剪，而是用「文本重叠匹配」：在 next 的
 * 开头窗口里找 acc 后缀首次出现的位置，从该位置之后接上。这样能同时兼容：
 *  1. chunker 给拆分表格补写的表头（零宽 start/end，位置上不可见）——表头出现
 *     在重叠行之前，会被自然跳过；
 *  2. HTML 实体编码（&#34; 等）导致的 content 长度与原文区间不一致——比对的是
 *     文本本身，不受长度偏差影响。
 *
 * @param positionOverlap 由 start/end 估算的重叠量，仅用于界定搜索窗口大小。
 */
const appendChunkContent = (acc: string, next: string, positionOverlap: number): string => {
  if (!acc) return next;
  if (!next) return acc;

  const MIN_OVERLAP = 12;          // 过短的后缀容易误匹配（如分隔行），忽略
  const span = Math.max(positionOverlap, 0);
  // 搜索的后缀最大长度；按位置重叠量放大几倍兜底，并设下限
  const maxK = Math.min(acc.length, next.length, Math.max(span * 3, 400));
  // 重叠行之前最多允许多少前缀（补写的表头）被跳过
  const headSlack = Math.max(span * 2, 320);

  for (let k = maxK; k >= MIN_OVERLAP; k--) {
    const suffix = acc.slice(acc.length - k);
    const pos = next.indexOf(suffix);
    if (pos !== -1 && pos <= headSlack) {
      return acc + next.slice(pos + k);
    }
  }
  return acc + next;
};

/**
 * 合并分块内容，还原完整文档。chunks 按 start_at 排序后逐段用文本重叠匹配拼接。
 */
const mergeChunks = (chunks: any[]): string => {
  if (!chunks || chunks.length === 0) return '';

  // 按 start_at 排序
  const sortedChunks = [...chunks].sort((a, b) => {
    const startA = a.start_at ?? a.chunk_index ?? 0;
    const startB = b.start_at ?? b.chunk_index ?? 0;
    return startA - startB;
  });

  let merged = sortedChunks[0].content || '';
  let mergedEnd = sortedChunks[0].end_at ?? 0;

  for (let i = 1; i < sortedChunks.length; i++) {
    const currentChunk = sortedChunks[i];
    const currentStartAt = currentChunk.start_at ?? 0;
    const currentEndAt = currentChunk.end_at ?? 0;
    const currentContent = currentChunk.content || '';

    if (!currentContent) continue;

    // 与上一段有明显间隙（位置不相邻），用空行分隔后整段拼接
    if (currentStartAt > mergedEnd && mergedEnd > 0) {
      merged = merged + '\n\n' + currentContent;
    } else {
      const positionOverlap = mergedEnd - currentStartAt;
      merged = appendChunkContent(merged, currentContent, positionOverlap);
    }

    if (currentEndAt > mergedEnd) {
      mergedEnd = currentEndAt;
    }
  }

  return merged;
};

const findDocDrawerScrollEl = (): HTMLElement | null =>
  document.querySelector('.doc-main-drawer .t-drawer__body') as HTMLElement | null;

const unbindDrawerScroll = () => {
  if (docScrollEl) {
    docScrollEl.removeEventListener('scroll', handleDetailsScroll);
    docScrollEl = null;
  }
};

const bindDrawerScroll = () => {
  unbindDrawerScroll();
  docScrollEl = findDocDrawerScrollEl();
  if (docScrollEl) {
    docScrollEl.addEventListener('scroll', handleDetailsScroll, { passive: true });
  }
};

onMounted(() => {
  loadTraceDrawerWidth();
  loadMainDrawerWidth();
  window.addEventListener('resize', onTraceDrawerWindowResize, { passive: true });
});

watch(() => props.visible, (visible) => {
  if (visible) {
    nextTick(() => {
      bindDrawerScroll();
      maybeLoadMoreChunks();
    });
  } else {
    unbindDrawerScroll();
  }
});
watch(() => props.details?.id, () => {
  page = 1;
  loadingChunks = false;
  pendingRequestedPage = null;
  pendingChunksBeforeLoad = 0;
});
watch(() => props.details?.chunkLoading, (val) => {
  if (val === false) {
    if (pendingRequestedPage !== null) {
      const currentLength = props.details?.md?.length || 0;
      const hasError = Boolean(props.details?.chunkLoadError);
      if (hasError && currentLength <= pendingChunksBeforeLoad) {
        page = Math.max(1, pendingRequestedPage - 1);
        MessagePlugin.warning(props.details?.chunkLoadError);
      }
    }
    pendingRequestedPage = null;
    pendingChunksBeforeLoad = 0;
    loadingChunks = false;
    if (props.visible) {
      nextTick(() => maybeLoadMoreChunks());
    }
  }
});
onUnmounted(() => {
  window.removeEventListener('resize', onTraceDrawerWindowResize);
  cleanupTraceDrawerResize();
  cleanupMainDrawerResize();
  unbindDrawerScroll();
  if (audioBlobUrl.value) {
    URL.revokeObjectURL(audioBlobUrl.value);
  }
})
const checkImage = (url) => {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = url;
  });
};
renderer.image = function ({ href, title, text }) {
  if (!isValidImageURL(href)) {
    return `<p>${t('error.invalidImageLink')}</p>`;
  }

  const safeImage = createSafeImage(href, text || '', title || '');
  return `<figure>
                ${safeImage}
                <figcaption style="text-align: left;">${text || ''}</figcaption>
            </figure>`;
};

// 自定义代码块渲染器，只显示语言标签
renderer.code = function ({ text, lang }) {
  // 空值校验：防止 text 为 undefined 或 null
  if (!text || typeof text !== 'string') {
    text = '';
  }

  // Mermaid 图表处理
  if (lang === 'mermaid') {
    // 生成唯一ID
    const id = `mermaid-${++mermaidRenderCount}`;
    // 返回带有 mermaid 类的 div，后续由 mermaid.run() 处理
    return `<div class="mermaid" id="${id}">${text}</div>`;
  }

  let detectedLang = lang;
  let highlighted = '';
  if (lang && hljs.getLanguage(lang)) {
    try {
      highlighted = hljs.highlight(text, { language: lang }).value;
    } catch (e) {
      highlighted = hljs.highlightAuto(text).value;
      detectedLang = hljs.highlightAuto(text).language || lang;
    }
  } else {
    const auto = hljs.highlightAuto(text);
    highlighted = auto.value;
    detectedLang = auto.language || lang;
  }
  const displayLang = detectedLang || 'Code';
  return `
    <div class="code-block-wrapper">
      <div class="code-block-header">
        <span class="code-block-lang">${displayLang}</span>
      </div>
      <pre class="code-block-pre"><code class="hljs language-${detectedLang || ''}">${highlighted}</code></pre>
    </div>
  `;
};
// 监听 chunks 变化，自动更新合并内容（已改为 computed 属性）
const mergedContent = computed(() => {
  const newChunks = props.details?.md;
  if (newChunks && newChunks.length > 0) {
    return mergeChunks(newChunks);
  }
  return '';
});

// 计算处理后的分块数据，避免在模板中频繁调用方法和 JSON.parse
const processedChunks = computed(() => {
  return (props.details?.md || []).map((item: any, index: number) => {
    return {
      original: item,
      processedContent: processMarkdown(item.content),
      questions: getGeneratedQuestions(item),
      meta: getChunkMeta(item),
      hasParent: hasParentChunk(item),
      chunkClass: getChunkClass(index)
    };
  });
});

const previewSupportedTypes = new Set([
  'pdf', 'docx', 'pptx', 'ppt', 'xlsx', 'xls', 'csv',
  'jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tiff', 'svg',
  'txt', 'md', 'markdown', 'json', 'xml', 'html', 'css', 'js', 'ts',
  'py', 'java', 'go', 'cpp', 'c', 'h', 'sh', 'yaml', 'yml',
  'ini', 'conf', 'log', 'sql', 'rs', 'rb', 'php', 'swift', 'kt',
  'scala', 'r', 'lua', 'pl', 'toml',
  'mp3', 'wav', 'm4a', 'flac', 'ogg',
]);

const canPreview = (): boolean => {
  if (props.details?.type !== 'file') return false;
  const ft = props.details?.file_type?.toLowerCase();
  if (!ft) return false;
  if (audioExtensions.has(ft)) return false; // 音频不走预览tab，播放器已内嵌
  return previewSupportedTypes.has(ft);
};

// 当文档详情加载完成时，file 类型自动切换到「预览」；音频类型使用 merged + 播放器
watch(() => props.details?.id, (newId) => {
  // 清理旧音频
  if (audioBlobUrl.value) {
    URL.revokeObjectURL(audioBlobUrl.value);
    audioBlobUrl.value = '';
  }
  if (!newId) return;
  if (isAudioFile(props.details?.file_type)) {
    viewMode.value = 'merged'; // 音频默认全文视图，播放器已内嵌
    loadAudioPreview();
  } else if (props.details?.type === 'file' && canPreview()) {
    viewMode.value = 'preview';
  } else {
    viewMode.value = 'merged';
  }
});

const isTextFile = (fileType?: string): boolean => {
  if (!fileType) return false;
  const textTypes = ['txt', 'md', 'markdown', 'json', 'xml', 'html', 'css', 'js', 'ts', 'py', 'java', 'go', 'cpp', 'c', 'h', 'sh', 'yaml', 'yml', 'ini', 'conf', 'log'];
  return textTypes.includes(fileType.toLowerCase());
};
const isMarkdownFile = (fileType?: string): boolean => {
  if (!fileType) return false;
  const markdownTypes = ['md', 'markdown'];
  return markdownTypes.includes(fileType.toLowerCase());
};

// 音频文件判断与播放器状态
const audioExtensions = new Set(['mp3', 'wav', 'm4a', 'flac', 'ogg']);
const isAudioFile = (fileType?: string): boolean => {
  if (!fileType) return false;
  return audioExtensions.has(fileType.toLowerCase());
};
const audioBlobUrl = ref('');
const audioLoading = ref(false);

const loadAudioPreview = async () => {
  if (!props.details?.id || audioBlobUrl.value) return;
  audioLoading.value = true;
  try {
    const blob = await previewKnowledgeFile(props.details.id);
    audioBlobUrl.value = URL.createObjectURL(blob);
  } catch (err) {
    console.error('Audio preview load failed:', err);
  } finally {
    audioLoading.value = false;
  }
};
const runMarkdownPostRenderPipeline = async () => {
  await nextTick();
  const renderRoot = getMarkdownRenderRoot();
  if (!renderRoot) {
    return;
  }
  await hydrateProtectedFileImages(renderRoot);
  const images = renderRoot?.querySelectorAll?.('img.markdown-image') as NodeListOf<HTMLImageElement> | undefined;
  if (images) {
    images.forEach(async item => {
      const isValid = await checkImage(item.src);
      if (!isValid) {
        item.remove();
      }
    })
  }
  // 渲染 Mermaid 图表
  await renderMermaidDiagrams();
};

watch(() => props.details.md, () => {
  runMarkdownPostRenderPipeline();
}, { immediate: true, deep: true, flush: 'post' })

watch(() => viewMode.value, (mode) => {
  if ((mode === 'chunks' || mode === 'merged') && props.visible) {
    runMarkdownPostRenderPipeline();
    if (mode === 'chunks') {
      nextTick(() => maybeLoadMoreChunks());
    }
  }
}, { flush: 'post' });

watch(() => props.visible, (visible) => {
  if (visible && (viewMode.value === 'chunks' || viewMode.value === 'merged')) {
    runMarkdownPostRenderPipeline();
  }
}, { flush: 'post' });

// 渲染 Mermaid 图表的函数
const renderMermaidDiagrams = async () => {
  try {
    const mermaidElements = getMarkdownRenderRoot()?.querySelectorAll('.mermaid');
    console.log('[Mermaid] Found mermaid elements:', mermaidElements?.length);
    if (mermaidElements && mermaidElements.length > 0) {
      await mermaid.run({
        nodes: mermaidElements
      });
      console.log('[Mermaid] Rendering complete');
      // 渲染完成后绑定点击事件
      nextTick(() => {
        bindMermaidClickEvents();
      });
    }
  } catch (error) {
    console.error('Mermaid rendering error:', error);
  }
};

// Mermaid 点击处理函数 - 必须在 bindMermaidClickEvents 之前定义
const handleMermaidClick = (e: Event) => {
  e.stopPropagation();
  const target = e.currentTarget as HTMLElement;
  const svg = target.querySelector('svg');
  if (svg) {
    openMermaidFullscreen(svg.outerHTML);
  }
};

// 为 Mermaid 容器绑定点击全屏事件（绑定在 div 上，不是 SVG 上）
const bindMermaidClickEvents = () => {
  const renderRoot = getMarkdownRenderRoot();
  if (!renderRoot) {
    console.log('[Mermaid] markdown render root is null');
    return;
  }
  // 绑定在 .mermaid div 上，而不是 SVG 上
  const mermaidDivs = renderRoot.querySelectorAll('.mermaid');
  console.log('[Mermaid] Found mermaid divs:', mermaidDivs.length);
  mermaidDivs.forEach((div, index) => {
    const divEl = div as HTMLElement;
    divEl.style.cursor = 'pointer';
    // 移除旧的事件监听器（避免重复绑定）
    divEl.removeEventListener('click', handleMermaidClick);
    divEl.addEventListener('click', handleMermaidClick);
    console.log(`[Mermaid] Bound click event to div ${index}`);
  });
};

// 安全地处理 Markdown 内容（使用 marked）
const processMarkdown = (markdownText) => {
  if (!markdownText || typeof markdownText !== 'string') return '';

  // 去除 Markdown 头部的 YAML Frontmatter（例如 --- title: xxx ---）
  let processedText = markdownText.replace(/^\s*---\r?\n[\s\S]*?\r?\n---\r?\n/, '');

  // 先还原原始文本中的 HTML 实体，让它们作为普通字符参与渲染
  processedText = processedText
    .replace(/&#39;/g, "'")
    .replace(/&#x27;/gi, "'")
    .replace(/&apos;/g, "'")
    .replace(/&#34;/g, '"')
    .replace(/&#x22;/gi, '"')
    .replace(/&quot;/g, '"')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&');

  // 处理被 <p> 包裹的表格行，转换为正常的表格行，并在前后补空行
  processedText = processedText.replace(/<p>\s*(\|[\s\S]*?\|)\s*<\/p>/gi, '\n$1\n');

  // MarkItDown 常在表格前插入空行 + 分隔行，渲染会出现多余空行
  processedText = normalizeSpuriousTablePrefixes(processedText);

  // 保留表格单元格中的 <br>，不转成换行，避免打散表格；其他区域原样交给 marked 处理

  // 先预处理数学定界符，再做安全预处理
  const mathSafeText = preprocessMathDelimiters(processedText);
  const safeMarkdown = safeMarkdownToHTML(mathSafeText);

  // 使用标记渲染
  marked.use({ renderer });
  let html = marked.parse(safeMarkdown) as string;

  // 还原被转义的 <br>
  html = html.replace(/&lt;br\s*\/?&gt;/gi, '<br>');

  // 最终安全清理
  let result = sanitizeHTML(html);

  return result;
};
const handleClose = () => {
  emit("closeDoc", false);
  const scrollEl = docScrollEl || findDocDrawerScrollEl();
  if (scrollEl) scrollEl.scrollTop = 0;
  viewMode.value = 'merged';
};

// 获取显示标题
const getDisplayTitle = () => {
  if (!props.details.title) return '';
  if (props.details.type === 'file') {
    // 文件类型去掉扩展名
    const lastDotIndex = props.details.title.lastIndexOf(".");
    return lastDotIndex > 0 ? props.details.title.substring(0, lastDotIndex) : props.details.title;
  }
  // URL和手动创建直接返回标题
  return props.details.title;
};

const channelLabelMap: Record<string, string> = {
  web: 'knowledgeBase.channelWeb',
  api: 'knowledgeBase.channelApi',
  browser_extension: 'knowledgeBase.channelBrowserExtension',
  wechat: 'knowledgeBase.channelWechat',
  wecom: 'knowledgeBase.channelWecom',
  feishu: 'knowledgeBase.channelFeishu',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
};

const getChannelLabel = (channel: string) => {
  const key = channelLabelMap[channel];
  return key ? t(key) : t('knowledgeBase.channelUnknown');
};

// 获取类型标签
const getTypeLabel = () => {
  switch (props.details.type) {
    case 'url':
      return t('knowledgeBase.typeURL');
    case 'manual':
      return t('knowledgeBase.typeManual');
    case 'file':
      return props.details.file_type ? props.details.file_type.toUpperCase() : t('knowledgeBase.typeFile');
    default:
      return '';
  }
};

// 获取类型主题色
const getTypeTheme = () => {
  switch (props.details.type) {
    case 'url':
      return 'primary';
    case 'manual':
      return 'success';
    case 'file':
      return 'default';
    default:
      return 'default';
  }
};

// 获取内容标签
const getContentLabel = () => {
  switch (props.details.type) {
    case 'url':
      return t('knowledgeBase.webContent');
    case 'manual':
      return t('knowledgeBase.documentContent');
    case 'file':
    default:
      return t('knowledgeBase.fileContent');
  }
};

// 获取时间标签
const getTimeLabel = () => {
  switch (props.details.type) {
    case 'url':
      return t('knowledgeBase.importTime');
    case 'manual':
      return t('knowledgeBase.createTime');
    case 'file':
    default:
      return t('knowledgeBase.uploadTime');
  }
};

// 获取Chunk样式类
const getChunkClass = (index: number) => {
  return index % 2 !== 0 ? 'chunk-odd' : 'chunk-even';
};

// 获取Chunk元数据
const getChunkMeta = (item: any) => {
  if (!item) return '';
  const parts = [];
  if (item.char_count) {
    parts.push(`${item.char_count} ${t('knowledgeBase.characters')}`);
  }
  if (item.token_count) {
    parts.push(`${item.token_count} tokens`);
  }
  return parts.join(' · ');
};

// 生成的问题类型
interface GeneratedQuestion {
  id: string;
  question: string;
}

// 解析生成的问题
const getGeneratedQuestions = (item: any): GeneratedQuestion[] => {
  if (!item || !item.metadata) return [];
  try {
    const metadata = typeof item.metadata === 'string' ? JSON.parse(item.metadata) : item.metadata;
    const questions = metadata.generated_questions || [];
    // 兼容旧格式（字符串数组）和新格式（对象数组）
    return questions.map((q: string | GeneratedQuestion, index: number) => {
      if (typeof q === 'string') {
        // 旧格式：字符串，生成临时ID
        return { id: `legacy-${index}`, question: q };
      }
      return q;
    });
  } catch {
    return [];
  }
};

// 展开状态管理
const expandedChunks = ref<Set<number>>(new Set());

const toggleQuestions = (index: number) => {
  if (expandedChunks.value.has(index)) {
    expandedChunks.value.delete(index);
  } else {
    expandedChunks.value.add(index);
  }
  // 触发响应式更新
  expandedChunks.value = new Set(expandedChunks.value);
};

const isExpanded = (index: number) => expandedChunks.value.has(index);

// 删除中的状态
const deletingQuestion = ref<{ chunkIndex: number; questionId: string } | null>(null);

// 删除生成的问题
const handleDeleteQuestion = async (item: any, chunkIndex: number, question: GeneratedQuestion) => {
  if (!item || !item.id) {
    MessagePlugin.error(t('common.error'));
    return;
  }

  // 检查是否是旧格式数据（无法删除）
  if (question.id.startsWith('legacy-')) {
    MessagePlugin.warning(t('knowledgeBase.legacyQuestionCannotDelete'));
    return;
  }

  const confirmDialog = DialogPlugin.confirm({
    header: t('common.confirmDelete'),
    body: t('knowledgeBase.confirmDeleteQuestion'),
    confirmBtn: t('common.confirm'),
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      confirmDialog.hide();
      deletingQuestion.value = { chunkIndex, questionId: question.id };
      try {
        await deleteGeneratedQuestion(item.id, question.id);
        MessagePlugin.success(t('common.deleteSuccess'));

        // 更新本地数据
        const metadata = typeof item.metadata === 'string' ? JSON.parse(item.metadata) : item.metadata;
        if (metadata && metadata.generated_questions) {
          const idx = metadata.generated_questions.findIndex((q: GeneratedQuestion) => q.id === question.id);
          if (idx > -1) {
            metadata.generated_questions.splice(idx, 1);
          }
          item.metadata = typeof item.metadata === 'string' ? JSON.stringify(metadata) : metadata;
        }

        // 通知父组件刷新数据
        emit('questionDeleted', { chunkId: item.id, questionId: question.id });
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('common.deleteFailed'));
      } finally {
        deletingQuestion.value = null;
      }
    },
    onClose: () => {
      confirmDialog.hide();
    }
  });
};

// 检查是否正在删除某个问题
const isDeleting = (chunkIndex: number, questionId: string) => {
  return deletingQuestion.value?.chunkIndex === chunkIndex && deletingQuestion.value?.questionId === questionId;
};

// 父 Chunk 上下文展开状态
const parentContextExpanded = ref<Set<number>>(new Set());
const parentContextCache = ref<Map<string, string>>(new Map());
const parentContextLoading = ref<Set<number>>(new Set());

const hasParentChunk = (item: any) => !!item?.parent_chunk_id;

const isParentExpanded = (index: number) => parentContextExpanded.value.has(index);

const toggleParentContext = async (item: any, index: number) => {
  if (parentContextExpanded.value.has(index)) {
    parentContextExpanded.value.delete(index);
    parentContextExpanded.value = new Set(parentContextExpanded.value);
    return;
  }

  const parentId = item.parent_chunk_id;
  if (!parentContextCache.value.has(parentId)) {
    parentContextLoading.value.add(index);
    parentContextLoading.value = new Set(parentContextLoading.value);
    try {
      const result: any = await getChunkByIdOnly(parentId);
      if (result.success && result.data) {
        parentContextCache.value.set(parentId, result.data.content || '');
        parentContextCache.value = new Map(parentContextCache.value);
      }
    } catch (err) {
      MessagePlugin.error(t('knowledgeBase.parentContextLoadFailed'));
      return;
    } finally {
      parentContextLoading.value.delete(index);
      parentContextLoading.value = new Set(parentContextLoading.value);
    }
  }

  parentContextExpanded.value.add(index);
  parentContextExpanded.value = new Set(parentContextExpanded.value);
  await runMarkdownPostRenderPipeline();
};

const getParentContent = (item: any) => {
  return parentContextCache.value.get(item.parent_chunk_id) || '';
};

const summaryExpanded = ref(false);
const summaryRef = ref<HTMLElement>();
const summaryOverflow = ref(false);

const checkSummaryOverflow = () => {
  nextTick(() => {
    const el = summaryRef.value;
    if (!el) { summaryOverflow.value = false; return; }
    summaryOverflow.value = el.scrollHeight > el.clientHeight + 1;
  });
};

watch(() => props.details?.description, () => {
  summaryExpanded.value = false;
  checkSummaryOverflow();
});
watch(summaryRef, () => checkSummaryOverflow());

const downloadFile = () => {
  downKnowledgeDetails(props.details.id)
    .then((result) => {
      if (result) {
        if (url.value) {
          URL.revokeObjectURL(url.value);
        }
        url.value = URL.createObjectURL(result);
        const link = document.createElement("a");
        link.style.display = "none";
        link.setAttribute("href", url.value);
        const needsExt = props.details.type === 'manual' && !props.details.title.toLowerCase().endsWith('.md');
        const ext = needsExt ? '.md' : '';
        link.setAttribute("download", props.details.title + ext);
        document.body.appendChild(link);
        link.click();
        nextTick(() => {
          document.body.removeChild(link);
          URL.revokeObjectURL(url.value);
        })
      }
    })
    .catch((err) => {
      MessagePlugin.error(t('file.downloadFailed'));
    });
};
const requestNextChunkPage = () => {
  if (loadingChunks || props.details?.chunkLoading) return;
  const total = props.details?.total ?? 0;
  const loaded = props.details?.md?.length ?? 0;
  if (loaded >= total || total === 0) return;
  const pageNum = Math.ceil(total / CHUNK_PAGE_SIZE);
  if (page + 1 > pageNum) return;
  page++;
  loadingChunks = true;
  pendingRequestedPage = page;
  pendingChunksBeforeLoad = loaded;
  emit('getDoc', page);
};

/** When the list is shorter than the drawer, scroll never fires — prefetch until scrollable or done. */
const maybeLoadMoreChunks = () => {
  if (!props.visible || loadingChunks || props.details?.chunkLoading) return;
  const el = docScrollEl || findDocDrawerScrollEl();
  if (!el) return;
  const loaded = props.details?.md?.length ?? 0;
  const total = props.details?.total ?? 0;
  if (loaded >= total) return;
  const { scrollHeight, clientHeight } = el;
  if (scrollHeight <= clientHeight + 8) {
    requestNextChunkPage();
  }
};

const handleDetailsScroll = () => {
  if (loadingChunks || props.details?.chunkLoading) return;
  const el = docScrollEl || findDocDrawerScrollEl();
  if (!el) return;
  const { scrollTop, scrollHeight, clientHeight } = el;
  if (scrollTop + clientHeight >= scrollHeight - 8) {
    requestNextChunkPage();
  }
};
</script>
<template>
  <div class="doc_content" ref="mdContentWrap">
    <teleport to="body">
      <div v-if="visible" class="doc-drawer-resize-handle" :style="{ right: `${mainDrawerWidth}px` }" role="separator"
        aria-orientation="vertical" @mousedown.prevent="onMainDrawerResizeStart">
        <div class="doc-drawer-resize-line" />
      </div>
    </teleport>
    <t-drawer :visible="visible" :zIndex="2000" :size="`${mainDrawerWidth}px`" attach="body" :closeBtn="true"
      :footer="false" :class="['doc-main-drawer', { 'doc-main-drawer--resizing': mainDrawerResizing }]"
      @close="handleClose">
      <template #header>
        <div class="drawer-header">
          <span class="header-title">{{ getDisplayTitle() }}</span>
          <t-tag v-if="details.type" class="header-type-tag" size="small" :theme="getTypeTheme()" variant="light">
            {{ getTypeLabel() }}
          </t-tag>
          <div class="header-actions">
            <t-button v-if="details.type === 'file' || details.type === 'manual'" class="header-action-btn" size="small"
              variant="text" shape="square" theme="default" :title="$t('common.download') || 'Download'"
              @click="downloadFile()">
              <template #icon>
                <t-icon name="download" size="16px" />
              </template>
            </t-button>
            <t-button v-if="details.id && hasTimelineSpans" class="header-action-btn trace-entry-btn" size="small"
              variant="text" shape="square" :theme="traceEntryTheme" :title="traceEntryTitle" @click="openTimeline">
              <template #icon>
                <t-icon name="chart-line" size="16px" />
              </template>
            </t-button>
          </div>
        </div>
      </template>

      <!-- Hidden mount: keeps the timeline fetching data so the header
           link's status dot / duration stays live even before the user
           opens the secondary drawer. -->
      <div class="kp-trigger-shadow" aria-hidden="true">
        <KnowledgeProcessingTimeline v-if="details.id" :knowledge-id="details.id" :parse-status="details.parse_status"
          :compact="true" :grace-poll="false" @update:has-spans="hasTimelineSpans = $event"
          @update:summary="timelineSummary = $event" />
      </div>

      <!-- 二级抽屉：完整 Langfuse-style waterfall -->
      <teleport to="body">
        <div v-if="timelineDrawerVisible" class="trace-drawer-resize-handle"
          :style="{ right: `${timelineDrawerWidth}px` }" role="separator" aria-orientation="vertical"
          :aria-label="$t('knowledgeStages.resizeDrawer')" :title="$t('knowledgeStages.resizeDrawer')"
          @mousedown.prevent="onTraceDrawerResizeStart">
          <div class="trace-drawer-resize-line" />
        </div>
      </teleport>
      <t-drawer :visible="timelineDrawerVisible" :zIndex="2100" :size="`${timelineDrawerWidth}px`" attach="body"
        :closeBtn="false" :footer="false" :header="false" :showOverlay="true" :closeOnOverlayClick="true"
        placement="right" :class="['kp-secondary-drawer', { 'kp-secondary-drawer--resizing': timelineDrawerResizing }]"
        @close="closeTimeline">
        <div class="kp-drawer-shell" :class="{ 'kp-drawer-shell--resizing': timelineDrawerResizing }">
          <KnowledgeProcessingTimeline v-if="details.id && timelineDrawerVisible" :knowledge-id="details.id"
            :parse-status="details.parse_status" :doc-title="details.title" show-close @close="closeTimeline" />
        </div>
      </t-drawer>

      <div ref="docMarkdownRoot" class="doc-markdown-root">
      <!-- URL类型专属区域（保留：source 是真实链接，不与标题重复） -->
      <div v-if="details.type === 'url'" class="url_box">
        <span class="label">{{ $t('knowledgeBase.urlSource') }}</span>
        <div class="url_link_box">
          <a :href="isValidURL(details.source) ? details.source : 'javascript:void(0)'"
            :target="isValidURL(details.source) ? '_blank' : undefined" class="url_link">
            <t-icon name="link" size="14px" />
            <span class="url_text">{{ details.source }}</span>
            <t-icon name="jump" size="14px" class="jump-icon" />
          </a>
        </div>
      </div>

      <!-- 文档摘要 -->
      <div v-if="details.description" class="summary_box">
        <span class="label">{{ $t('knowledgeBase.documentSummary') }}</span>
        <div class="summary_wrapper" :class="{ 'summary_clickable': summaryOverflow || summaryExpanded }"
          @click="(summaryOverflow || summaryExpanded) && (summaryExpanded = !summaryExpanded)">
          <div ref="summaryRef" :class="['summary_content', { 'summary_collapsed': !summaryExpanded }]">{{
            details.description
            }}</div>
          <div v-if="(summaryOverflow && !summaryExpanded) || summaryExpanded" class="summary_fade"
            :class="{ 'summary_fade_expanded': summaryExpanded }">
            <t-icon :name="summaryExpanded ? 'chevron-up' : 'chevron-down'" size="14px" class="summary_fade_icon" />
          </div>
        </div>
      </div>
      <div v-else-if="details.summary_status === 'pending' || details.summary_status === 'processing'"
        class="summary_box">
        <span class="label">{{ $t('knowledgeBase.documentSummary') }}</span>
        <div class="summary_loading">
          <t-loading size="small" />
          <span>{{ $t('knowledgeBase.generatingSummary') }}</span>
        </div>
      </div>

      <div class="content_header">
        <div class="header-left">
          <div class="title-row">
            <span class="label">{{ getContentLabel() }}</span>
            <span v-if="details.total > 0" class="chunk-count">
              {{ $t('knowledgeBase.chunkCount', { count: details.total }) }}
            </span>
          </div>
          <div class="meta-row">
            <div class="meta-left">
              <span class="time"> {{ getTimeLabel() }}：{{ details.time }} </span>
              <t-tag v-if="details.channel && details.channel !== 'web'" size="small" variant="light" theme="warning"
                class="channel-tag">
                {{ getChannelLabel(details.channel) }}
              </t-tag>
            </div>
            <div class="view-mode-buttons">
              <t-button v-if="canPreview()" size="small" :variant="viewMode === 'preview' ? 'base' : 'outline'"
                :theme="viewMode === 'preview' ? 'primary' : 'default'" @click="viewMode = 'preview'"
                class="view-mode-btn">
                {{ $t('preview.tab') }}
              </t-button>
              <t-button v-if="!canPreview()" size="small" :variant="viewMode === 'merged' ? 'base' : 'outline'"
                :theme="viewMode === 'merged' ? 'primary' : 'default'" @click="viewMode = 'merged'"
                class="view-mode-btn">
                {{ $t('knowledgeBase.viewMerged') }}
              </t-button>
              <t-button size="small" :variant="viewMode === 'chunks' ? 'base' : 'outline'"
                :theme="viewMode === 'chunks' ? 'primary' : 'default'" @click="viewMode = 'chunks'"
                class="view-mode-btn">
                {{ $t('knowledgeBase.viewChunks') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>

      <!-- 音频播放器（音频文件时固定显示在内容区顶部） -->
      <div v-if="isAudioFile(details.file_type)" class="audio-player-section">
        <div v-if="audioLoading" class="audio-loading">
          <t-loading size="small" />
          <span>{{ $t('preview.audioLoading') }}</span>
        </div>
        <audio v-else-if="audioBlobUrl" controls class="audio-player" :src="audioBlobUrl">
          {{ $t('preview.audioNotSupported') }}
        </audio>
      </div>

      <!-- 合并视图 -->
      <div v-if="viewMode === 'merged'">
        <div v-if="!mergedContent" class="no_content">{{ $t('common.noData') }}</div>
        <div v-else class="md-content" v-html="processMarkdown(mergedContent)"></div>
      </div>

      <!-- 分块视图 -->
      <div v-else-if="viewMode === 'chunks'">
        <div v-if="!processedChunks.length" class="no_content">{{ $t('common.noData') }}</div>
        <div v-else class="chunk-list">
          <div class="chunk-item" v-for="(chunk, index) in processedChunks" :key="index">
            <div class="chunk-header">
              <span class="chunk-index">{{ $t('knowledgeBase.segment') }} {{ index + 1 }}</span>
              <div class="chunk-header-right">
                <t-tag v-if="chunk.hasParent" size="small" theme="primary" variant="light">
                  {{ $t('knowledgeBase.childChunk') }}
                </t-tag>
                <t-tag v-if="chunk.questions.length > 0" size="small" theme="success" variant="light">
                  {{ $t('knowledgeBase.questions') }} {{ chunk.questions.length }}
                </t-tag>
                <span class="chunk-meta">{{ chunk.meta }}</span>
              </div>
            </div>
            <div class="md-content" v-html="chunk.processedContent"></div>

            <!-- 父 Chunk 上下文展开 -->
            <div v-if="chunk.hasParent" class="parent-context-section">
              <div class="parent-context-toggle" @click="toggleParentContext(chunk.original, index)">
                <t-icon v-if="!parentContextLoading.has(index)"
                  :name="isParentExpanded(index) ? 'chevron-down' : 'chevron-right'" size="14px" />
                <t-loading v-else size="small" style="width: 14px; height: 14px;" />
                <span>{{ $t('knowledgeBase.viewParentContext') }}</span>
              </div>
              <div v-show="isParentExpanded(index)" class="parent-context-content">
                <div class="md-content" v-html="processMarkdown(getParentContent(chunk.original))"></div>
              </div>
            </div>

            <!-- 生成的问题展示 -->
            <div v-if="chunk.questions.length > 0" class="questions-section">
              <div class="questions-toggle" @click="toggleQuestions(index)">
                <t-icon :name="isExpanded(index) ? 'chevron-down' : 'chevron-right'" size="14px" />
                <span>{{ $t('knowledgeBase.generatedQuestions') }} ({{ chunk.questions.length }})</span>
              </div>
              <div v-show="isExpanded(index)" class="questions-list">
                <div v-for="question in chunk.questions" :key="question.id" class="question-item">
                  <t-icon name="help-circle" size="14px" class="question-icon" />
                  <span class="question-text">{{ question.question }}</span>
                  <t-button v-if="canDeleteGeneratedQuestion" theme="default" variant="text" size="small"
                    class="delete-question-btn" :loading="isDeleting(index, question.id)"
                    @click.stop="handleDeleteQuestion(chunk.original, index, question)">
                    <template #icon>
                      <t-icon name="delete" size="14px" />
                    </template>
                  </t-button>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 文档预览视图 -->
      <div v-else-if="viewMode === 'preview'">
        <DocumentPreview :knowledgeId="details.id" :fileType="details.file_type" :fileName="details.title"
          :active="viewMode === 'preview'" />
      </div>
      </div>

    </t-drawer>
  </div>
</template>
<style scoped lang="less">
@import "./css/markdown.less";

/* Drawer widths are now driven by the `:size` prop on each <t-drawer>
   (see mainDrawerSize / timelineDrawerSize in <script>). CSS rules with
   !important were removed because they fought each other across the
   scoped/non-scoped boundary and had no clean specificity ordering in
   dev mode (Vite injects scoped <style> tags later than non-scoped,
   inverting prod). Inline width via the prop is unambiguous. */

// 代码块样式
:deep(.code-block-wrapper) {
  margin: 12px 0;
  border: 1px solid var(--td-component-border);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);

  .code-block-header {
    display: flex;
    align-items: center;
    padding: 8px 12px;
    background: var(--td-bg-color-secondarycontainer);
    border-bottom: 1px solid var(--td-component-stroke);
    font-size: 12px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .code-block-pre {
    margin: 0;
    padding: 12px;
    background: var(--td-bg-color-secondarycontainer);
    overflow: auto;
    font-size: 13px;
    line-height: 1.5;

    code {
      background: transparent;
      padding: 0;
      border: none;
      white-space: pre;
      word-wrap: normal;
      display: block;
    }
  }
}

:deep(.t-drawer__header) {
  font-weight: normal;
}

:deep(.t-drawer__body.narrow-scrollbar) {
  padding: 16px 20px;
}

.drawer-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  width: 100%;
  /* TDesign 抽屉的 X 关闭按钮浮在 header 右上角（约 16px 宽 + 16px 间距），
     给右侧留出空间，避免我们的图标按钮被 X 遮挡。 */
  padding-right: 32px;

  .header-title {
    /* flex: 1 1 auto + min-width:0 让标题在标题超长时收缩出省略号，
       而不是把右侧 tag/操作按钮挤出 header。 */
    flex: 1 1 auto;
    min-width: 0;
    font-size: 16px;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .header-type-tag {
    flex-shrink: 0;
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 2px;
    /* 关键：操作区永不收缩，标题再长也能完整看到图标 */
    flex-shrink: 0;
    flex-grow: 0;
  }

  .header-action-btn {
    /* 28×28 文本按钮：无边框，与抽屉头部融为一体；hover 时浅灰背景，
       与右上角 X 关闭按钮的视觉风格一致。 */
    width: 28px;
    min-width: 28px;
    height: 28px;
    padding: 0;
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
    border-radius: 4px;
    transition: background-color 0.15s ease, color 0.15s ease;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }

    :deep(.t-button__text) {
      display: flex;
      align-items: center;
      justify-content: center;
    }
  }
}

// 信息面板通用样式（仅 url_box 在用，file/manual 已合并到 header）
.info_panel {
  display: flex;
  flex-direction: column;
  margin-bottom: 16px;
}

.url_box {
  .info_panel();
}

.parse_timeline_box {
  margin-top: 8px;
  margin-bottom: 16px;
  padding: 12px 16px;
  background: var(--td-bg-color-component);
  border-radius: 6px;
}

/* Hidden mount keeps fetcher live without showing UI */
.kp-trigger-shadow {
  display: none;
}

/* ============== Secondary drawer shell ============== */
.kp-drawer-shell {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 100%;
  background: var(--td-bg-color-container);
  /* Belt-and-suspenders: even if some inner element forgets a min-width:0
     declaration in a flex chain, clip rather than overflow the drawer. */
  overflow: hidden;
  min-width: 0;
}

.kp-drawer-shell> :deep(.kp-timeline) {
  width: 100%;
  height: 100%;
}

/* Width is set via the :size prop on the <t-drawer>, not CSS — see the
   <script> for mainDrawerSize / timelineDrawerSize. Only padding +
   background are still tweaked here so the timeline fills the secondary
   drawer cleanly, edge-to-edge. */
:deep(.kp-secondary-drawer .t-drawer__body) {
  padding: 0 !important;
}

:deep(.kp-secondary-drawer .t-drawer__content) {
  background: var(--td-bg-color-container);
}

// 文档摘要区域
.summary_box {
  display: flex;
  flex-direction: column;
  margin-bottom: 24px;
  margin-top: 8px;

  .label {
    margin-bottom: 8px;
    font-weight: 600;
    font-size: 14px;
  }

  .summary_wrapper {
    position: relative;
    background: var(--td-bg-color-container-hover);
    border-radius: 4px;

    &.summary_clickable {
      cursor: pointer;
    }
  }

  .summary_content {
    padding: 12px;
    color: var(--td-text-color-primary);
    font-size: 13px;
    line-height: 1.5;
    word-break: break-word;
    white-space: pre-wrap;

    &.summary_collapsed {
      max-height: 4.5em;
      overflow: hidden;
    }
  }

  .summary_fade {
    display: flex;
    justify-content: center;
    padding-bottom: 4px;
    pointer-events: none;

    &:not(.summary_fade_expanded) {
      position: absolute;
      bottom: 0;
      left: 0;
      right: 0;
      height: 28px;
      background: linear-gradient(transparent, var(--td-bg-color-container-hover) 80%);
      border-radius: 0 0 4px 4px;
      align-items: flex-end;
    }
  }

  .summary_fade_icon {
    color: var(--td-text-color-placeholder);
  }

  .summary_loading {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px;
    background: var(--td-bg-color-container-hover);
    border-radius: 4px;
    color: var(--td-text-color-placeholder);
    font-size: 13px;
  }
}

.label {
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-style: normal;
  font-weight: 600;
  line-height: 22px;
  margin-bottom: 8px;
}

// URL链接区域
.url_link_box {
  border-radius: 4px;
  background: var(--td-bg-color-container-hover);
  padding: 8px 12px;

  .url_link {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--td-brand-color);
    text-decoration: none;

    .url_text {
      flex: 1;
      font-size: 13px;
      word-break: break-all;
    }

    .jump-icon {
      flex-shrink: 0;
      color: var(--td-brand-color);
    }
  }
}

.content_header {
  margin-top: 16px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 12px;

  .header-left {
    display: flex;
    flex-direction: column;
    gap: 8px;
    width: 100%;
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: 8px;

    .label {
      margin: 0;
      font-size: 14px;
      font-weight: 600;
      color: var(--td-text-color-primary);
    }
  }

  .meta-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    flex-wrap: wrap;
    gap: 12px;
  }

  .meta-left {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .channel-tag {
    flex-shrink: 0;
  }

  .chunk-count {
    color: var(--td-text-color-secondary);
    font-size: 12px;
    background: var(--td-bg-color-container-hover);
    padding: 2px 8px;
    border-radius: 4px;
  }

  .view-mode-buttons {
    display: flex;
    gap: 4px;

    .view-mode-btn {
      height: 28px;
      min-width: 60px;
    }
  }
}

.time {
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-style: normal;
  font-weight: 400;
}

.no_content {
  margin-top: 12px;
  color: var(--td-text-color-disabled);
  font-size: 13px;
  padding: 16px;
  text-align: center;
}

// Chunk列表样式
.chunk-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.chunk-item {
  border-radius: 4px;
  padding: 12px 16px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
}

.chunk-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--td-component-stroke);

  .chunk-index {
    color: var(--td-text-color-placeholder);
    font-size: 12px;
    font-weight: 600;
    letter-spacing: 0.5px;
  }

  .chunk-header-right {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .chunk-meta {
    color: var(--td-text-color-disabled);
    font-size: 11px;
  }
}

// 父 Chunk 上下文样式
.parent-context-section {
  margin-top: 10px;
  padding-top: 8px;
  border-top: 1px dashed var(--td-component-stroke);
}

.parent-context-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 500;
  padding: 4px 0;
}

.parent-context-content {
  margin-top: 8px;
  padding: 10px 12px;
  background: var(--td-brand-color-light);
  border-radius: 4px;
  border-left: 3px solid var(--td-brand-color);

  .md-content {
    color: var(--td-text-color-secondary);
    font-size: 13px;
  }
}

// 生成的问题样式
.questions-section {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--td-component-stroke);
}

.questions-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  color: var(--td-brand-color-active);
  font-size: 12px;
  font-weight: 500;
  padding: 4px 0;
}

.questions-list {
  margin-top: 8px;
  padding-left: 4px;
}

.question-item {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 6px 8px;
  margin-bottom: 4px;
  background: var(--td-success-color-light);
  border-radius: 4px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  line-height: 1.5;

  &:hover {
    .delete-question-btn {
      opacity: 1;
    }
  }

  .question-icon {
    color: var(--td-brand-color-active);
    flex-shrink: 0;
    margin-top: 2px;
  }

  .question-text {
    flex: 1;
    word-break: break-word;
  }

  .delete-question-btn {
    opacity: 0;
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);

    &:hover {
      color: var(--td-error-color);
    }
  }
}

// 音频播放器样式
.audio-player-section {
  margin-bottom: 16px;
  padding: 12px 16px;
  background: var(--td-bg-color-container-hover);
  border-radius: 6px;
  border: 1px solid var(--td-component-border);

  .audio-player {
    width: 100%;
    height: 40px;
  }

  .audio-loading {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--td-text-color-placeholder);
    font-size: 13px;
    padding: 4px 0;
  }
}

.md-content {
  word-break: break-word;
  line-height: 1.6;
  color: var(--td-text-color-primary);
}

// 保留旧样式作为兼容（已被chunk-item替代）
.content {
  word-break: break-word;
  padding: 4px;
  gap: 4px;
  margin-top: 12px;
}
</style>

<!-- Non-scoped padding/background overrides for the secondary drawer.
     Width is now controlled via the :size prop on <t-drawer> (see
     timelineDrawerSize in <script>) — that puts width on element.style
     rather than fighting !important CSS rules. We only keep these
     non-scoped rules because TDesign's default body padding and
     content background need to be flushed for the timeline to fill
     edge-to-edge. -->
<style lang="less">
/* 主抽屉宽度可调：拖拽手柄通过 teleport 挂到 body，不受 scoped 影响，
   故样式写在非 scoped 块里。手柄贴在抽屉面板左缘（right = 抽屉宽度）。 */
.doc-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  cursor: col-resize;
  z-index: 2001;
  display: flex;
  align-items: center;
  justify-content: center;
}

.doc-drawer-resize-handle .doc-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.doc-drawer-resize-handle:hover .doc-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

/* 拖拽过程中关闭宽度过渡，避免跟手卡顿 */
.t-drawer.doc-main-drawer--resizing .t-drawer__content {
  transition: none !important;
}

/* Trace 二级抽屉拖拽手柄：与主抽屉保持一致，teleport 到 body，
   position: fixed，z-index 高于二级抽屉本体，避免被其他层级遮挡。 */
.trace-drawer-resize-handle {
  position: fixed;
  top: 0;
  bottom: 0;
  width: 12px;
  margin-left: -6px;
  cursor: col-resize;
  z-index: 2101;
  display: flex;
  align-items: center;
  justify-content: center;
}

.trace-drawer-resize-handle .trace-drawer-resize-line {
  width: 2px;
  height: 48px;
  border-radius: 1px;
  background: var(--td-component-border);
  opacity: 0.55;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.trace-drawer-resize-handle:hover .trace-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

.t-drawer.kp-secondary-drawer--resizing .trace-drawer-resize-line,
body:has(.t-drawer.kp-secondary-drawer--resizing) .trace-drawer-resize-line {
  opacity: 1;
  background: var(--td-brand-color);
}

.t-drawer.kp-secondary-drawer .t-drawer__body {
  padding: 0 !important;
}

.t-drawer.kp-secondary-drawer .t-drawer__content {
  background: var(--td-bg-color-container);
}

.t-drawer.kp-secondary-drawer--resizing .t-drawer__content {
  transition: none !important;
}
</style>
