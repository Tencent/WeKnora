<script setup lang="ts">
// @ts-nocheck
import { marked } from "marked";
import markedKatex from 'marked-katex-extension';
import 'katex/dist/katex.min.css';

import hljs from "highlight.js";
import "highlight.js/styles/github.css";
import mermaid from "mermaid";
import { onMounted, ref, nextTick, onUnmounted, watch, computed } from "vue";
import {
  downKnowledgeDetails, deleteGeneratedQuestion, getChunkByIdOnly, previewKnowledgeFile,
  updateDocumentChunk, listChunkRevisions, revertDocumentChunk, updateKnowledgeMetadata,
  updateKnowledgeSummary, regenerateKnowledgeSummary, upsertGeneratedQuestion, regenerateGeneratedQuestions, getKnowledgeDetails,
  KNOWLEDGE_CHUNK_PAGE_SIZE,
} from "@/api/knowledge-base/index";
import { MessagePlugin } from "tdesign-vue-next";
import { sanitizeHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL, hydrateProtectedFileImages, isValidURL } from '@/utils/security';
import { normalizeSpuriousTablePrefixes } from '@/utils/markdownTableNormalize';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';
import { diffWikiLines, type WikiDiffLine } from '@/utils/wikiLineDiff';
import { useI18n } from 'vue-i18n';
import { useAuthStore } from '@/stores/auth';
import DocumentPreview from '@/components/document-preview.vue';
import KnowledgeProcessingTimeline from '@/components/knowledge-processing-timeline.vue';
import { resolveKnowledgeDownloadFileName } from '@/views/knowledge/knowledgeDownloadFileName';
import { isKnownPreviewableExt, resolveFilePreviewExt } from '@/utils/filePreview';

const { t } = useI18n();
const authStore = useAuthStore();

// canDeleteGeneratedQuestion 对应后端 DELETE /chunks/by-id/:id/questions
// 的 OwnedChunkKBOrAdminFromChunkID 守卫——KB 创建者或空间 Admin+
// 才允许删除。父组件 KnowledgeBase.vue 通过 :canEditKB 把 KB 级权限
// 传下来（包含 KB creator / Admin / 组织分享 editor 三种来源），未
// 传时按更严格的 Admin 兜底，避免 Viewer 看到一个会 403 的入口。
const canDeleteGeneratedQuestion = computed(() => {
  if (props.canEditKB === true) return true;
  return authStore.hasRole('admin');
});
const canEditContent = canDeleteGeneratedQuestion;

type MetadataValueType = 'text' | 'number' | 'boolean' | 'null';
interface MetadataDraftRow {
  id: number;
  key: string;
  value: string;
  type: MetadataValueType;
}

let metadataRowSeed = 0;
const metadataEditing = ref(false);
const metadataDraft = ref<MetadataDraftRow[]>([]);
const metadataSaving = ref(false);
const summaryRefreshing = ref(false);
const summaryEditing = ref(false);
const summaryDraft = ref('');
const summarySaving = ref(false);

const startSummaryEdit = () => {
  summaryDraft.value = props.details?.description || '';
  summaryEditing.value = true;
};

const cancelSummaryEdit = () => {
  summaryEditing.value = false;
  summaryDraft.value = '';
};

const saveSummary = async () => {
  summarySaving.value = true;
  try {
    const description = summaryDraft.value.trim();
    const result: any = await updateKnowledgeSummary(props.details.id, description);
    applySummaryState(result?.data?.summary_status, result?.data?.description ?? description);
    summaryEditing.value = false;
    MessagePlugin.success(t('common.saveSuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.saveFailed'));
  } finally {
    summarySaving.value = false;
  }
};

const metadataTypeOptions = computed(() => [
  { label: t('knowledgeBase.metadataTypeText'), value: 'text' },
  { label: t('knowledgeBase.metadataTypeNumber'), value: 'number' },
  { label: t('knowledgeBase.metadataTypeBoolean'), value: 'boolean' },
  { label: t('knowledgeBase.metadataTypeNull'), value: 'null' },
]);

const makeMetadataRow = (key = '', value: unknown = ''): MetadataDraftRow => {
  let type: MetadataValueType = 'text';
  if (value === null) type = 'null';
  else if (typeof value === 'number') type = 'number';
  else if (typeof value === 'boolean') type = 'boolean';
  return {
    id: ++metadataRowSeed,
    key,
    value: value === null ? '' : String(value),
    type,
  };
};

const syncMetadataDraft = () => {
  metadataDraft.value = Object.entries(props.details?.custom_metadata || {})
    .map(([key, value]) => makeMetadataRow(key, value));
};

const startMetadataEdit = () => {
  syncMetadataDraft();
  if (!metadataDraft.value.length) metadataDraft.value.push(makeMetadataRow());
  metadataEditing.value = true;
};

const addMetadataRow = () => {
  if (metadataDraft.value.length >= 20) return;
  metadataDraft.value.push(makeMetadataRow());
};

const removeMetadataRow = (id: number) => {
  metadataDraft.value = metadataDraft.value.filter((row) => row.id !== id);
};

const parseMetadataValue = (row: MetadataDraftRow): unknown => {
  if (row.type === 'null') return null;
  if (row.type === 'boolean') return row.value === 'true';
  if (row.type === 'number') {
    const value = Number(row.value);
    if (!row.value.trim() || !Number.isFinite(value)) {
      throw new Error(t('knowledgeBase.metadataNumberRequired', { key: row.key }));
    }
    return value;
  }
  return row.value;
};

const formatMetadataValue = (value: unknown) => {
  if (value === null) return 'null';
  if (typeof value === 'boolean') return value ? 'true' : 'false';
  return String(value);
};

const saveMetadata = async () => {
  try {
    const value: Record<string, unknown> = {};
    for (const row of metadataDraft.value) {
      const key = row.key.trim();
      if (!key) throw new Error(t('knowledgeBase.metadataKeyRequired'));
      if (Object.prototype.hasOwnProperty.call(value, key)) {
        throw new Error(t('knowledgeBase.metadataKeyDuplicate', { key }));
      }
      value[key] = parseMetadataValue(row);
    }
    metadataSaving.value = true;
    const result: any = await updateKnowledgeMetadata(props.details.id, value);
    props.details.custom_metadata = value;
    metadataEditing.value = false;
    if (result?.data) {
      applySummaryState(result.data.summary_status, result.data.description);
    }
    MessagePlugin.success(t('common.saveSuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.saveFailed'));
  } finally {
    metadataSaving.value = false;
  }
};

const refreshSummary = async () => {
  summaryRefreshing.value = true;
  try {
    const result: any = await regenerateKnowledgeSummary(props.details.id);
    if (result?.data) {
      applySummaryState(result.data.summary_status, result.data.description);
    }
    const status = result?.data?.summary_status;
    if (status === 'pending' || status === 'processing') {
      MessagePlugin.success(t('knowledgeBase.summaryRefreshQueued'));
    } else {
      MessagePlugin.success(t('knowledgeBase.summaryRefreshed'));
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.error'));
  } finally {
    summaryRefreshing.value = false;
  }
};

const detailTags = computed(() => {
  const tags = props.details?.tags;
  return Array.isArray(tags) ? tags : [];
});

const headerIconName = computed(() => {
  switch (props.details?.type) {
    case 'url':
      return 'link';
    case 'manual':
      return 'edit';
    default:
      return 'file';
  }
});

const showSummarySection = computed(() =>
  Boolean(props.details?.description)
  || props.details?.summary_status === 'pending'
  || props.details?.summary_status === 'processing'
  || Boolean(props.details?.id && canEditContent.value),
);

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
const props = defineProps(["visible", "details", "knowledgeType", "sourceInfo", "canEditKB", "canDownloadKB", "parse_status", "kbId"]);
const emit = defineEmits(["closeDoc", "getDoc", "questionDeleted", "summaryStateChange"]);

const applySummaryState = (summaryStatus?: string, description?: string) => {
  if (typeof summaryStatus === 'string' && summaryStatus) {
    props.details.summary_status = summaryStatus;
  }
  if (typeof description === 'string') {
    props.details.description = description;
  }
  if (props.details?.id) {
    emit('summaryStateChange', {
      id: props.details.id,
      summary_status: props.details.summary_status,
      description: props.details.description,
    });
  }
};

const isSummaryStatusInFlight = (status?: string) => status === 'pending' || status === 'processing';
const summaryStatusRefreshing = computed(() => isSummaryStatusInFlight(props.details?.summary_status));
const canEditSummary = computed(() => canEditContent.value && !summaryStatusRefreshing.value);
let summaryStatusPollTimer: ReturnType<typeof setTimeout> | null = null;
let summaryStatusPollGeneration = 0;

const stopSummaryStatusPolling = () => {
  summaryStatusPollGeneration++;
  if (summaryStatusPollTimer !== null) {
    clearTimeout(summaryStatusPollTimer);
    summaryStatusPollTimer = null;
  }
};

const scheduleSummaryStatusPoll = () => {
  if (summaryStatusPollTimer !== null || !props.visible || !props.details?.id ||
    !isSummaryStatusInFlight(props.details?.summary_status)) return;

  const knowledgeID = props.details.id;
  const generation = summaryStatusPollGeneration;
  summaryStatusPollTimer = setTimeout(async () => {
    summaryStatusPollTimer = null;
    if (generation !== summaryStatusPollGeneration || !props.visible || props.details?.id !== knowledgeID) return;
    try {
      const result: any = await getKnowledgeDetails(knowledgeID);
      if (generation !== summaryStatusPollGeneration || props.details?.id !== knowledgeID) return;
      if (result?.success && result.data) {
        applySummaryState(result.data.summary_status, result.data.description);
      }
    } catch {
      // Keep the current status visible and retry while the drawer remains open.
    }
    if (generation === summaryStatusPollGeneration && props.visible && props.details?.id === knowledgeID &&
      isSummaryStatusInFlight(props.details?.summary_status)) {
      scheduleSummaryStatusPoll();
    }
  }, 1500);
};

watch(
  () => [props.visible, props.details?.id, props.details?.summary_status],
  ([visible, knowledgeID, summaryStatus]) => {
    if (visible && knowledgeID && isSummaryStatusInFlight(summaryStatus as string)) {
      scheduleSummaryStatusPoll();
    } else {
      stopSummaryStatusPolling();
    }
  },
  { immediate: true },
);
watch(() => props.details?.id, syncMetadataDraft, { immediate: true });

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
const CHUNK_PAGE_SIZE = KNOWLEDGE_CHUNK_PAGE_SIZE;
const chunkPage = ref(1);
const loadedChunkPage = ref(1);
let pendingChunkPage: number | null = null;
const isChunkPageTransition = computed(
  () => Boolean(props.details?.chunkLoading) && chunkPage.value !== loadedChunkPage.value,
);
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
 * positionOverlap <= 0 时两段位置上严格相邻或不相交，无可去重重叠；此时进入
 * 文本匹配会因 headSlack 下限 320 在 next 开头窗口内误命中 acc 后缀的真实
 * 内容重复（如同一句话在文档多次出现），把 next 开头整段误判为补写表头删掉，
 * 造成不可逆的内容丢失。直接拼接，补写表头重复交给调用方后处理。
 *
 * @param positionOverlap 由 start/end 估算的重叠量，仅用于界定搜索窗口大小。
 */
const appendChunkContent = (acc: string, next: string, positionOverlap: number): string => {
  if (!acc) return next;
  if (!next) return acc;
  if (positionOverlap <= 0) return acc + next;

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

onMounted(() => {
  loadTraceDrawerWidth();
  loadMainDrawerWidth();
  window.addEventListener('resize', onTraceDrawerWindowResize, { passive: true });
});

watch(() => props.details?.id, () => {
  cancelSummaryEdit();
  chunkPage.value = 1;
  loadedChunkPage.value = 1;
  pendingChunkPage = null;
});
watch(() => props.details?.chunkLoading, (val) => {
  if (val === false && pendingChunkPage !== null) {
    if (props.details?.chunkLoadError) {
      chunkPage.value = loadedChunkPage.value;
      MessagePlugin.warning(props.details.chunkLoadError);
    } else {
      loadedChunkPage.value = pendingChunkPage;
    }
    pendingChunkPage = null;
  }
});
onUnmounted(() => {
  stopSummaryStatusPolling();
  window.removeEventListener('resize', onTraceDrawerWindowResize);
  cleanupTraceDrawerResize();
  cleanupMainDrawerResize();
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

const canPreview = (): boolean => {
  if (props.details?.type !== 'file') return false;
  const ft = resolveFilePreviewExt(props.details?.title, props.details?.file_type);
  if (!ft) return false;
  if (audioExtensions.has(ft)) return false; // 音频不走预览tab，播放器已内嵌
  return ft === 'doc' || isKnownPreviewableExt(ft);
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
  await hydrateProtectedFileImages(
    renderRoot,
    props.kbId ? { mode: 'knowledgeBase', kbId: props.kbId } : undefined,
  );
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
  }
}, { flush: 'post' });

watch(() => props.visible, (visible) => {
  if (!visible) {
    cancelSummaryEdit();
  } else if (viewMode.value === 'chunks' || viewMode.value === 'merged') {
    runMarkdownPostRenderPipeline();
  }
}, { flush: 'post' });

watch(summaryStatusRefreshing, (refreshing) => {
  if (refreshing) {
    cancelSummaryEdit();
  }
});

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
  // Do not register this renderer globally. DocumentPreview uses the same
  // `marked` module; registering here made its later Markdown preview reuse
  // this image validator after the user had opened the chunk view.
  let html = marked.parse(safeMarkdown, { renderer }) as string;

  // 还原被转义的 <br>
  html = html.replace(/&lt;br\s*\/?&gt;/gi, '<br>');

  // 最终安全清理
  let result = sanitizeHTML(html);

  return result;
};
const handleClose = () => {
  emit("closeDoc", false);
  const scrollEl = document.querySelector('.doc-main-drawer .t-drawer__body') as HTMLElement | null;
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
  gitlab: 'knowledgeBase.channelGitLab',
  // Drive (云盘) connectors get their own channel so Drive docs show
  // "飞书云盘" / "Lark 云盘", distinct from the wiki connector's "飞书".
  feishu_drive: 'knowledgeBase.channelFeishuDrive',
  lark_drive: 'knowledgeBase.channelLarkDrive',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
  ima: 'knowledgeBase.channelIma',
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
  content_revision?: number;
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

const hasStaleGeneratedQuestions = (item: any) => {
  const questions = getGeneratedQuestions(item);
  if (!questions.length) return false;
  try {
    const metadata = typeof item.metadata === 'string' ? JSON.parse(item.metadata || '{}') : (item.metadata || {});
    const fallbackRevision = metadata.generated_questions_revision || 0;
    const currentRevision = item.content_revision || 0;
    return questions.some((question) => (question.content_revision ?? fallbackRevision) !== currentRevision);
  } catch {
    return false;
  }
};

const editingChunkId = ref('');
const chunkDraft = ref('');
const savingChunkId = ref('');
const chunkStatusLoading = ref('');

const getChunkMetadata = (item: any): Record<string, any> => (
  typeof item.metadata === 'string'
    ? JSON.parse(item.metadata || '{}')
    : { ...(item.metadata || {}) }
);

const setChunkMetadata = (item: any, metadata: Record<string, any>) => {
  item.metadata = metadata;
};

const upsertChunkGeneratedQuestion = (item: any, questionData: GeneratedQuestion) => {
  const metadata = getChunkMetadata(item);
  const questions: GeneratedQuestion[] = [...(metadata.generated_questions || [])];
  const index = questions.findIndex((q) => q.id === questionData.id);
  if (index >= 0) {
    questions[index] = questionData;
  } else {
    questions.push(questionData);
  }
  metadata.generated_questions = questions;
  setChunkMetadata(item, metadata);
};

const notifyChunkMutationOutcome = (item: any, result: any, successMessage?: string) => {
  Object.assign(item, result.data);
  applySummaryState(result.summary_status, result.description);
  if (item.index_status === 'failed') {
    MessagePlugin.warning(t('knowledgeBase.chunkSavedIndexFailed'));
    return;
  }
  if (successMessage) {
    MessagePlugin.success(successMessage);
  }
};

const reloadChunksFromStart = () => {
  chunkPage.value = 1;
  pendingChunkPage = 1;
  editingChunkId.value = '';
  chunkDraft.value = '';
  emit('getDoc', 1);
};

const handleChunkEditError = (error: any, fallbackMessage: string) => {
  if (error?.status === 409) {
    MessagePlugin.warning(error?.message || t('knowledgeBase.chunkEditConflict'));
    reloadChunksFromStart();
    return;
  }
  MessagePlugin.error(error?.message || fallbackMessage);
};

const startChunkEdit = (item: any) => {
  if (editingChunkId.value === item.id) {
    editingChunkId.value = '';
    chunkDraft.value = '';
    return;
  }
  editingChunkId.value = item.id;
  chunkDraft.value = item.content || '';
};

const saveChunkEdit = async (item: any) => {
  if (!chunkDraft.value.trim()) {
    MessagePlugin.warning(t('knowledgeBase.chunkContentRequired'));
    return;
  }
  savingChunkId.value = item.id;
  try {
    const result: any = await updateDocumentChunk(props.details.id, item.id, {
      content: chunkDraft.value,
      expected_revision: item.content_revision || 0,
    });
    editingChunkId.value = '';
    notifyChunkMutationOutcome(item, result, t('common.saveSuccess'));
    void refreshChunkHistoryAfterMutation(item);
  } catch (error: any) {
    handleChunkEditError(error, t('common.saveFailed'));
  } finally {
    savingChunkId.value = '';
  }
};

const toggleChunkEnabled = async (item: any, isEnabled: boolean) => {
  chunkStatusLoading.value = item.id;
  try {
    const result: any = await updateDocumentChunk(props.details.id, item.id, {
      is_enabled: isEnabled,
      expected_revision: item.content_revision || 0,
    });
    notifyChunkMutationOutcome(item, result);
    void refreshChunkHistoryAfterMutation(item);
  } catch (error: any) {
    handleChunkEditError(error, t('common.error'));
  } finally {
    chunkStatusLoading.value = '';
  }
};

const retryChunkIndex = async (item: any) => {
  try {
    const result: any = await updateDocumentChunk(props.details.id, item.id, {
      expected_revision: item.content_revision || 0,
    });
    Object.assign(item, result.data);
    if (item.index_status === 'failed') throw new Error(t('knowledgeBase.indexFailed'));
    MessagePlugin.success(t('knowledgeBase.indexRetrySuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.error'));
  }
};

type ChunkDiffLine = WikiDiffLine | { type: 'skip'; text: string };

const chunkHistoryPopup = ref('');
const chunkHistoryLoading = ref('');
const chunkHistories = ref<Record<string, any[]>>({});
const selectedChunkRevision = ref<Record<string, number | null>>({});
const chunkHistoryRequestSequence = ref<Record<string, number>>({});
const revertingRevision = ref('');

const loadChunkHistory = async (item: any, force = false, silent = false) => {
  if (!force && Object.prototype.hasOwnProperty.call(chunkHistories.value, item.id)) return;

  const requestSequence = (chunkHistoryRequestSequence.value[item.id] || 0) + 1;
  chunkHistoryRequestSequence.value[item.id] = requestSequence;
  chunkHistoryLoading.value = item.id;
  try {
    const result: any = await listChunkRevisions(props.details.id, item.id);
    if (chunkHistoryRequestSequence.value[item.id] === requestSequence) {
      chunkHistories.value[item.id] = result?.data || [];
    }
  } catch (error: any) {
    if (!silent) MessagePlugin.error(error?.message || t('common.error'));
  } finally {
    if (chunkHistoryRequestSequence.value[item.id] === requestSequence) {
      chunkHistoryLoading.value = '';
    }
  }
};

const showChunkHistory = async (item: any) => loadChunkHistory(item);

const refreshChunkHistoryAfterMutation = async (item: any) => {
  const wasLoaded = Object.prototype.hasOwnProperty.call(chunkHistories.value, item.id);
  const shouldReload = wasLoaded || chunkHistoryPopup.value === item.id;
  // Invalidate both the cached list and any older request still in flight.
  chunkHistoryRequestSequence.value[item.id] = (chunkHistoryRequestSequence.value[item.id] || 0) + 1;
  delete chunkHistories.value[item.id];
  selectedChunkRevision.value[item.id] = null;
  if (shouldReload) {
    await loadChunkHistory(item, true, true);
  } else if (chunkHistoryLoading.value === item.id) {
    chunkHistoryLoading.value = '';
  }
};

const setChunkHistoryPopupVisible = (item: any, visible: boolean) => {
  chunkHistoryPopup.value = visible ? item.id : '';
  if (visible) {
    parentContextPopup.value = '';
    questionPopupChunk.value = '';
    showChunkHistory(item);
  }
};

const selectChunkRevision = (item: any, revision: number) => {
  selectedChunkRevision.value[item.id] = selectedChunkRevision.value[item.id] === revision ? null : revision;
};

const getSelectedChunkRevision = (item: any) => {
  const revision = selectedChunkRevision.value[item.id];
  return (chunkHistories.value[item.id] || []).find((entry: any) => entry.revision === revision) || null;
};

const compactChunkDiff = (item: any): ChunkDiffLine[] => {
  const revision = getSelectedChunkRevision(item);
  if (!revision) return [];
  const lines = diffWikiLines(revision.content || '', item.content || '');
  const changed = new Set<number>();
  lines.forEach((line, index) => {
    if (line.type !== 'same') {
      for (let i = Math.max(0, index - 2); i <= Math.min(lines.length - 1, index + 2); i++) changed.add(i);
    }
  });
  if (!changed.size) return [];

  const compact: ChunkDiffLine[] = [];
  let lastIndex = -2;
  [...changed].sort((a, b) => a - b).forEach((index) => {
    if (index > lastIndex + 1) compact.push({ type: 'skip', text: '…' });
    compact.push(lines[index]);
    lastIndex = index;
  });
  return compact;
};

const diffLinePrefix = (type: ChunkDiffLine['type']) => {
  if (type === 'add') return '+ ';
  if (type === 'del') return '- ';
  return type === 'same' ? '  ' : '';
};

const revisionStatusChanged = (item: any, revisionIndex: number) => {
  const revisions = chunkHistories.value[item.id] || [];
  const newerEnabled = revisionIndex === 0 ? item.is_enabled : revisions[revisionIndex - 1]?.is_enabled;
  return revisions[revisionIndex]?.is_enabled !== newerEnabled;
};

const revertChunk = async (item: any, revision: number) => {
  revertingRevision.value = `${item.id}:${revision}`;
  try {
    const result: any = await revertDocumentChunk(props.details.id, item.id, revision, item.content_revision || 0);
    notifyChunkMutationOutcome(item, result, t('knowledgeBase.chunkReverted'));
    await refreshChunkHistoryAfterMutation(item);
  } catch (error: any) {
    handleChunkEditError(error, t('common.error'));
  } finally {
    revertingRevision.value = '';
  }
};

const questionDrafts = ref<Record<string, string>>({});
const savingQuestionChunk = ref('');
const regeneratingQuestionChunk = ref('');
const questionPopupChunk = ref('');
const questionComposerChunk = ref('');
const editingQuestionKey = ref('');
const questionEditDraft = ref('');
const savingQuestionKey = ref('');

watch(() => props.details?.id, () => {
  metadataEditing.value = false;
  editingChunkId.value = '';
  chunkHistoryPopup.value = '';
  chunkHistoryLoading.value = '';
  chunkHistories.value = {};
  selectedChunkRevision.value = {};
  chunkHistoryRequestSequence.value = {};
  parentContextPopup.value = '';
  questionPopupChunk.value = '';
  questionComposerChunk.value = '';
  editingQuestionKey.value = '';
});

const setQuestionPopupVisible = (item: any, visible: boolean) => {
  questionPopupChunk.value = visible ? item.id : '';
  if (visible) {
    parentContextPopup.value = '';
    chunkHistoryPopup.value = '';
  } else {
    closeQuestionComposer(item);
    cancelQuestionEdit();
  }
};

const openQuestionComposer = (item: any) => {
  questionPopupChunk.value = item.id;
  questionComposerChunk.value = item.id;
};

const closeQuestionComposer = (item: any) => {
  questionComposerChunk.value = '';
  questionDrafts.value[item.id] = '';
};

const addQuestion = async (item: any) => {
  const question = (questionDrafts.value[item.id] || '').trim();
  if (!question) return;
  savingQuestionChunk.value = item.id;
  try {
    const result: any = await upsertGeneratedQuestion(item.id, question);
    questionDrafts.value[item.id] = '';
    questionComposerChunk.value = '';
    upsertChunkGeneratedQuestion(item, result.data);
    MessagePlugin.success(t('common.saveSuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.error'));
  } finally {
    savingQuestionChunk.value = '';
  }
};

const startQuestionEdit = (item: any, question: GeneratedQuestion) => {
  editingQuestionKey.value = `${item.id}:${question.id}`;
  questionEditDraft.value = question.question;
};

const cancelQuestionEdit = () => {
  editingQuestionKey.value = '';
  questionEditDraft.value = '';
};

const saveQuestionEdit = async (item: any, question: GeneratedQuestion) => {
  const value = questionEditDraft.value.trim();
  if (!value) return;
  if (value === question.question) {
    cancelQuestionEdit();
    return;
  }
  savingQuestionKey.value = `${item.id}:${question.id}`;
  try {
    const result: any = await upsertGeneratedQuestion(item.id, value, question.id);
    if (result?.data) {
      upsertChunkGeneratedQuestion(item, result.data);
    }
    cancelQuestionEdit();
    MessagePlugin.success(t('common.saveSuccess'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.error'));
  } finally {
    savingQuestionKey.value = '';
  }
};

const regenerateQuestions = async (item: any) => {
  regeneratingQuestionChunk.value = item.id;
  try {
    const result: any = await regenerateGeneratedQuestions(item.id);
    const metadata = typeof item.metadata === 'string' ? JSON.parse(item.metadata || '{}') : (item.metadata || {});
    metadata.generated_questions = result?.data || [];
    metadata.generated_questions_revision = item.content_revision || 0;
    item.metadata = metadata;
    questionComposerChunk.value = '';
    MessagePlugin.success(t('knowledgeBase.questionsRegenerated'));
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.error'));
  } finally {
    regeneratingQuestionChunk.value = '';
  }
};

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
};

// 检查是否正在删除某个问题
const isDeleting = (chunkIndex: number, questionId: string) => {
  return deletingQuestion.value?.chunkIndex === chunkIndex && deletingQuestion.value?.questionId === questionId;
};

// 父 Chunk 上下文通过 Header Popup 展示，避免在每个分块下方占用高度。
const parentContextPopup = ref('');
const parentContextCache = ref<Map<string, string>>(new Map());
const parentContextLoading = ref<Set<number>>(new Set());

const hasParentChunk = (item: any) => !!item?.parent_chunk_id;

const loadParentContext = async (item: any, index: number) => {
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
      parentContextPopup.value = '';
      return;
    } finally {
      parentContextLoading.value.delete(index);
      parentContextLoading.value = new Set(parentContextLoading.value);
    }
  }

  await nextTick();
  await runMarkdownPostRenderPipeline();
};

const setParentContextPopupVisible = (item: any, index: number, visible: boolean) => {
  parentContextPopup.value = visible ? item.id : '';
  if (visible) {
    questionPopupChunk.value = '';
    chunkHistoryPopup.value = '';
    loadParentContext(item, index);
  }
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
        link.setAttribute("download", resolveKnowledgeDownloadFileName(props.details));
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
const handleChunkPageChange = (pageInfo: { current: number }) => {
  if (props.details?.chunkLoading || pageInfo.current === loadedChunkPage.value) return;
  pendingChunkPage = pageInfo.current;
  emit('getDoc', pageInfo.current);
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
        <div class="doc-drawer-header">
          <div class="doc-drawer-header-icon">
            <t-icon :name="headerIconName" />
          </div>
          <div class="doc-drawer-header-text">
            <div class="doc-drawer-header-title">{{ getDisplayTitle() }}</div>
          </div>
          <div class="header-actions">
            <t-button v-if="canDownloadKB && (details.type === 'file' || details.type === 'manual')" class="header-action-btn" size="small"
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

      <div ref="docMarkdownRoot" class="doc-markdown-root doc-drawer-body setting-drawer__body">
        <section v-if="details.id" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.detailSectionMeta') }}</h4>
          <div class="doc-detail-rows">
            <div v-if="details.time" class="doc-detail-row">
              <span class="doc-detail-label">{{ getTimeLabel() }}</span>
              <span class="doc-detail-value">{{ details.time }}</span>
            </div>
            <div v-if="details.type" class="doc-detail-row">
              <span class="doc-detail-label">{{ $t('knowledgeBase.infoCard.type') }}</span>
              <span class="doc-detail-value">
                <t-tag size="small" :theme="getTypeTheme()" variant="light">{{ getTypeLabel() }}</t-tag>
              </span>
            </div>
            <div v-if="details.channel && details.channel !== 'web'" class="doc-detail-row">
              <span class="doc-detail-label">{{ $t('knowledgeBase.infoCard.source') }}</span>
              <span class="doc-detail-value">
                <t-tag size="small" variant="light" theme="warning">{{ getChannelLabel(details.channel) }}</t-tag>
              </span>
            </div>
            <div v-if="detailTags.length > 0" class="doc-detail-row">
              <span class="doc-detail-label">{{ $t('knowledgeBase.tagLabel') }}</span>
              <span class="doc-detail-value doc-tag-chips">
                <t-tag
                  v-for="tag in detailTags"
                  :key="tag.id"
                  size="small"
                  variant="light-outline"
                  class="doc-tag-chip"
                >
                  <span class="tag-text">{{ tag.name }}</span>
                </t-tag>
              </span>
            </div>
          </div>
        </section>

        <section v-if="details.id" class="setting-drawer__section metadata-section">
          <div class="section-title-actions">
            <h4 class="setting-drawer__section-title">
              <span>{{ $t('knowledgeBase.customMetadata') }}</span>
              <t-tooltip :content="$t('knowledgeBase.metadataCapabilityHint')" placement="top">
                <t-icon name="info-circle" size="14px" class="metadata-capability-icon" />
              </t-tooltip>
              <span v-if="Object.keys(details.custom_metadata || {}).length" class="metadata-count">
                {{ Object.keys(details.custom_metadata || {}).length }}/20
              </span>
            </h4>
            <t-tooltip v-if="canEditContent && !metadataEditing" :content="$t('common.edit')" placement="top">
              <t-button class="icon-action-btn" size="small" variant="text" shape="square" @click="startMetadataEdit">
                <template #icon><t-icon name="edit" size="15px" /></template>
              </t-button>
            </t-tooltip>
          </div>

          <div v-if="!metadataEditing" class="metadata-display">
            <div v-if="Object.keys(details.custom_metadata || {}).length" class="metadata-grid">
              <div v-for="(value, key) in (details.custom_metadata || {})" :key="key" class="metadata-item">
                <span class="metadata-item-key">{{ key }}</span>
                <span class="metadata-item-value">{{ formatMetadataValue(value) }}</span>
              </div>
            </div>
            <button v-else-if="canEditContent" type="button" class="metadata-empty-action" @click="startMetadataEdit">
              <t-icon name="add" size="15px" />
              <span>{{ $t('knowledgeBase.addMetadataField') }}</span>
            </button>
            <span v-else class="metadata-empty">{{ $t('knowledgeBase.noCustomMetadata') }}</span>
          </div>

          <div v-else class="metadata-editor">
            <div v-for="row in metadataDraft" :key="row.id" class="metadata-editor-row">
              <t-input v-model="row.key" class="metadata-key-input" :placeholder="$t('knowledgeBase.metadataKeyPlaceholder')" />
              <t-select v-model="row.type" class="metadata-type-select" :options="metadataTypeOptions" />
              <t-select v-if="row.type === 'boolean'" v-model="row.value" class="metadata-value-input"
                :options="[{ label: 'true', value: 'true' }, { label: 'false', value: 'false' }]" />
              <t-input v-else-if="row.type !== 'null'" v-model="row.value" class="metadata-value-input"
                :placeholder="$t('knowledgeBase.metadataValuePlaceholder')" />
              <div v-else class="metadata-null-value">null</div>
              <t-tooltip :content="$t('common.delete')" placement="top">
                <t-button class="icon-action-btn metadata-remove-btn" size="small" variant="text" shape="square"
                  @click="removeMetadataRow(row.id)">
                  <template #icon><t-icon name="delete" size="15px" /></template>
                </t-button>
              </t-tooltip>
            </div>
            <button v-if="metadataDraft.length < 20" type="button" class="metadata-add-row" @click="addMetadataRow">
              <t-icon name="add" size="15px" />
              <span>{{ $t('knowledgeBase.addMetadataField') }}</span>
            </button>
            <div class="metadata-actions">
              <t-button size="small" variant="outline" :disabled="metadataSaving" @click="metadataEditing = false; syncMetadataDraft()">
                {{ $t('common.cancel') }}
              </t-button>
              <t-button size="small" theme="primary" :loading="metadataSaving" @click="saveMetadata">
                {{ $t('common.save') }}
              </t-button>
            </div>
          </div>
        </section>

        <section v-if="details.type === 'url'" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.urlSource') }}</h4>
          <div class="url_link_box">
            <a :href="isValidURL(details.source) ? details.source : 'javascript:void(0)'"
              :target="isValidURL(details.source) ? '_blank' : undefined" class="url_link">
              <t-icon name="link" size="14px" />
              <span class="url_text">{{ details.source }}</span>
              <t-icon name="jump" size="14px" class="jump-icon" />
            </a>
          </div>
        </section>

        <section v-if="showSummarySection" class="setting-drawer__section summary-section">
          <div class="section-title-actions">
            <div class="summary-section-heading">
              <h4 class="setting-drawer__section-title">{{ $t('knowledgeBase.documentSummary') }}</h4>
              <span v-if="details.description && summaryStatusRefreshing" class="summary-refreshing-indicator">
                <t-loading size="small" />
                <span>{{ $t('knowledgeBase.generatingSummary') }}</span>
              </span>
            </div>
            <div v-if="canEditContent && !summaryEditing" class="summary-title-actions">
              <t-tooltip v-if="canEditSummary" :content="$t('common.edit')" placement="top">
                <t-button class="icon-action-btn" size="small" variant="text" shape="square"
                  @click="startSummaryEdit">
                  <template #icon><t-icon name="edit" size="15px" /></template>
                </t-button>
              </t-tooltip>
              <t-tooltip :content="$t('knowledgeBase.regenerateSummary')" placement="top">
                <t-button class="icon-action-btn" size="small" variant="text" shape="square"
                  :loading="summaryRefreshing" @click="refreshSummary">
                  <template #icon><t-icon name="refresh" size="15px" /></template>
                </t-button>
              </t-tooltip>
            </div>
          </div>
          <div v-if="summaryEditing" class="summary_editor">
            <t-textarea v-model="summaryDraft" :autosize="{ minRows: 4, maxRows: 10 }"
              :placeholder="$t('knowledgeBase.noDocumentSummary')" />
            <div class="summary_editor_actions">
              <t-button size="small" variant="outline" :disabled="summarySaving" @click="cancelSummaryEdit">
                {{ $t('common.cancel') }}
              </t-button>
              <t-button size="small" theme="primary" :loading="summarySaving" @click="saveSummary">
                {{ $t('common.save') }}
              </t-button>
            </div>
          </div>
          <div v-else-if="details.description" class="summary_wrapper"
            :class="{ 'summary_clickable': summaryOverflow || summaryExpanded }"
            @click="(summaryOverflow || summaryExpanded) && (summaryExpanded = !summaryExpanded)">
            <div ref="summaryRef" :class="['summary_content', { 'summary_collapsed': !summaryExpanded }]">{{
              details.description
            }}</div>
            <div v-if="(summaryOverflow && !summaryExpanded) || summaryExpanded" class="summary_fade"
              :class="{ 'summary_fade_expanded': summaryExpanded }">
              <t-icon :name="summaryExpanded ? 'chevron-up' : 'chevron-down'" size="14px" class="summary_fade_icon" />
            </div>
          </div>
          <div v-else class="summary_loading">
            <template v-if="details.summary_status === 'pending' || details.summary_status === 'processing'">
              <t-loading size="small" />
              <span>{{ $t('knowledgeBase.generatingSummary') }}</span>
            </template>
            <template v-else>
              <t-icon name="file-unknown" size="18px" />
              <span>{{ $t('knowledgeBase.noDocumentSummary') }}</span>
              <t-button v-if="canEditContent" size="small" variant="text" :loading="summaryRefreshing" @click="refreshSummary">
                <template #icon><t-icon name="refresh" size="14px" /></template>
                {{ $t('knowledgeBase.generateSummary') }}
              </t-button>
            </template>
          </div>
        </section>

        <section class="setting-drawer__section doc-content-section">
          <div class="doc-content-section-head">
            <div class="doc-content-section-head-left">
              <h4 class="setting-drawer__section-title">{{ getContentLabel() }}</h4>
              <span v-if="details.total > 0" class="chunk-count">
                {{ $t('knowledgeBase.chunkCount', { count: details.total }) }}
              </span>
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
            <div v-if="isChunkPageTransition" class="chunk-page-loading">
              <t-loading size="small" />
              <span>{{ $t('common.loading') }}</span>
            </div>
            <template v-else>
              <div v-if="!mergedContent" class="no_content">{{ $t('common.noData') }}</div>
              <div v-else class="md-content" v-html="processMarkdown(mergedContent)"></div>
            </template>
          </div>

          <!-- 分块视图 -->
          <div v-else-if="viewMode === 'chunks'">
            <div v-if="isChunkPageTransition" class="chunk-page-loading">
              <t-loading size="small" />
              <span>{{ $t('common.loading') }}</span>
            </div>
            <template v-else>
              <div v-if="!processedChunks.length" class="no_content">{{ $t('common.noData') }}</div>
              <div v-else class="chunk-list">
              <div class="chunk-item" :class="{ 'chunk-item--disabled': !chunk.original.is_enabled }"
                v-for="(chunk, index) in processedChunks" :key="chunk.original.id || index">
                <div class="chunk-header">
                  <div class="chunk-heading">
                    <span class="chunk-index">{{ $t('knowledgeBase.segment') }} {{ (loadedChunkPage - 1) * CHUNK_PAGE_SIZE + index + 1 }}</span>
                    <span class="chunk-meta">{{ chunk.meta }}</span>
                  </div>
                  <div class="chunk-header-right">
                    <t-tooltip v-if="chunk.original.index_status === 'failed' && canEditContent"
                      :content="$t('knowledgeBase.retryIndex')" placement="top">
                      <t-button class="icon-action-btn" size="small" theme="danger" variant="text" shape="square"
                        @click="retryChunkIndex(chunk.original)">
                        <template #icon><t-icon name="refresh" size="15px" /></template>
                      </t-button>
                    </t-tooltip>
                    <t-popup v-if="chunk.hasParent" :visible="parentContextPopup === chunk.original.id"
                      trigger="click" placement="bottom-right" :show-arrow="true" destroy-on-close
                      :overlay-inner-style="{ padding: 0 }" overlay-class-name="chunk-context-popup-overlay"
                      @visible-change="(visible: boolean) => setParentContextPopupVisible(chunk.original, index, visible)">
                      <t-button class="icon-action-btn" :class="{ 'is-active': parentContextPopup === chunk.original.id }"
                        size="small" variant="text" shape="square" :title="$t('knowledgeBase.viewParentContext')">
                        <template #icon><t-icon name="git-branch" size="15px" /></template>
                      </t-button>
                      <template #content>
                        <div class="chunk-context-popup" @click.stop>
                          <div class="chunk-popup-head">
                            <div class="chunk-popup-title">
                              <t-icon name="git-branch" size="15px" />
                              <span>{{ $t('knowledgeBase.viewParentContext') }}</span>
                            </div>
                          </div>
                          <div v-if="parentContextLoading.has(index)" class="chunk-popup-state">
                            <t-loading size="small" />
                            <span>{{ $t('common.loading') }}</span>
                          </div>
                          <div v-else class="chunk-context-popup-body md-content"
                            v-html="processMarkdown(getParentContent(chunk.original))"></div>
                        </div>
                      </template>
                    </t-popup>
                    <t-popup v-if="chunk.questions.length > 0 || canEditContent"
                      :visible="questionPopupChunk === chunk.original.id" trigger="click" placement="bottom-right"
                      :show-arrow="true" destroy-on-close :overlay-inner-style="{ padding: 0 }"
                      overlay-class-name="chunk-questions-popup-overlay"
                      @visible-change="(visible: boolean) => setQuestionPopupVisible(chunk.original, visible)">
                      <t-button class="icon-action-btn chunk-question-entry"
                        :class="{ 'is-active': questionPopupChunk === chunk.original.id }" size="small" variant="text"
                        shape="square" :title="$t('knowledgeBase.generatedQuestions')">
                        <template #icon><t-icon name="help-circle" size="15px" /></template>
                      </t-button>
                      <template #content>
                        <div class="chunk-questions-popup" @click.stop>
                          <div class="chunk-popup-head">
                            <div class="chunk-popup-title">
                              <t-icon name="help-circle" size="15px" />
                              <span>{{ $t('knowledgeBase.generatedQuestions') }}</span>
                              <span class="chunk-popup-count">{{ chunk.questions.length }}</span>
                              <span v-if="hasStaleGeneratedQuestions(chunk.original)" class="chunk-question-stale-hint">
                                {{ $t('knowledgeBase.staleGeneratedQuestions') }}
                              </span>
                            </div>
                            <div v-if="canEditContent" class="chunk-popup-actions">
                              <t-tooltip :content="$t('knowledgeBase.addGeneratedQuestion')" placement="top">
                                <t-button class="icon-action-btn" size="small" variant="text" shape="square"
                                  @click.stop="openQuestionComposer(chunk.original)">
                                  <template #icon><t-icon name="add" size="15px" /></template>
                                </t-button>
                              </t-tooltip>
                              <t-tooltip :content="$t('knowledgeBase.regenerateQuestions')" placement="top">
                                <t-button class="icon-action-btn" size="small" variant="text" shape="square"
                                  :loading="regeneratingQuestionChunk === chunk.original.id"
                                  @click.stop="regenerateQuestions(chunk.original)">
                                  <template #icon><t-icon name="refresh" size="15px" /></template>
                                </t-button>
                              </t-tooltip>
                            </div>
                          </div>
                          <div class="chunk-questions-popup-body">
                            <div v-if="canEditContent && questionComposerChunk === chunk.original.id" class="question-composer">
                              <t-input v-model="questionDrafts[chunk.original.id]" autofocus
                                :placeholder="$t('knowledgeBase.addGeneratedQuestion')" @enter="addQuestion(chunk.original)" />
                              <t-tooltip :content="$t('common.cancel')" placement="top">
                                <t-button class="icon-action-btn" size="small" variant="text" shape="square"
                                  :disabled="savingQuestionChunk === chunk.original.id"
                                  @click="closeQuestionComposer(chunk.original)">
                                  <template #icon><t-icon name="close" size="14px" /></template>
                                </t-button>
                              </t-tooltip>
                              <t-button size="small" theme="primary" :loading="savingQuestionChunk === chunk.original.id"
                                :disabled="!(questionDrafts[chunk.original.id] || '').trim()" @click="addQuestion(chunk.original)">
                                {{ $t('common.add') }}
                              </t-button>
                            </div>
                            <div v-if="chunk.questions.length" class="questions-list">
                              <div v-for="question in chunk.questions" :key="question.id" class="question-item">
                                <span class="question-leading-icon"><t-icon name="help-circle" size="14px" /></span>
                                <div v-if="editingQuestionKey === `${chunk.original.id}:${question.id}`" class="question-inline-editor">
                                  <t-input v-model="questionEditDraft" autofocus
                                    @enter="saveQuestionEdit(chunk.original, question)" />
                                  <t-button size="small" variant="text" @click="cancelQuestionEdit">{{ $t('common.cancel') }}</t-button>
                                  <t-button size="small" theme="primary"
                                    :loading="savingQuestionKey === `${chunk.original.id}:${question.id}`"
                                    @click="saveQuestionEdit(chunk.original, question)">{{ $t('common.save') }}</t-button>
                                </div>
                                <template v-else>
                                  <span class="question-text">{{ question.question }}</span>
                                  <div class="question-actions">
                                    <t-tooltip v-if="canEditContent && !question.id.startsWith('legacy-')"
                                      :content="$t('common.edit')" placement="top">
                                      <t-button class="icon-action-btn" theme="default" variant="text" shape="square"
                                        size="small" @click.stop="startQuestionEdit(chunk.original, question)">
                                        <template #icon><t-icon name="edit" size="14px" /></template>
                                      </t-button>
                                    </t-tooltip>
                                    <t-popconfirm v-if="canDeleteGeneratedQuestion && !question.id.startsWith('legacy-')"
                                      theme="warning" :content="$t('knowledgeBase.confirmDeleteQuestion')"
                                      @confirm="handleDeleteQuestion(chunk.original, index, question)">
                                      <t-button class="icon-action-btn delete-question-btn" theme="default" variant="text"
                                        shape="square" size="small" :loading="isDeleting(index, question.id)">
                                        <template #icon><t-icon name="delete" size="14px" /></template>
                                      </t-button>
                                    </t-popconfirm>
                                  </div>
                                </template>
                              </div>
                            </div>
                            <div v-else-if="questionComposerChunk !== chunk.original.id" class="questions-empty">
                              <t-icon name="chat-bubble-help" size="20px" />
                              <span>{{ $t('knowledgeBase.noGeneratedQuestions') }}</span>
                            </div>
                          </div>
                        </div>
                      </template>
                    </t-popup>
                    <template v-if="canEditContent">
                      <t-tooltip :content="$t('common.edit')" placement="top">
                        <t-button class="icon-action-btn" :class="{ 'is-active': editingChunkId === chunk.original.id }"
                          size="small" variant="text" shape="square" @click="startChunkEdit(chunk.original)">
                          <template #icon><t-icon name="edit" size="15px" /></template>
                        </t-button>
                      </t-tooltip>
                      <t-popup :visible="chunkHistoryPopup === chunk.original.id" trigger="click" placement="bottom-right"
                        :show-arrow="true" destroy-on-close :overlay-inner-style="{ padding: 0 }"
                        overlay-class-name="chunk-history-popup-overlay"
                        @visible-change="(visible: boolean) => setChunkHistoryPopupVisible(chunk.original, visible)">
                        <t-button class="icon-action-btn" :class="{ 'is-active': chunkHistoryPopup === chunk.original.id }"
                          size="small" variant="text" shape="square" :title="$t('knowledgeBase.chunkHistory')">
                          <template #icon><t-icon name="history" size="15px" /></template>
                        </t-button>
                        <template #content>
                          <div class="chunk-history-popup" @click.stop>
                            <div class="chunk-history-popup-head">
                              <div>
                                <div class="chunk-history-popup-title">
                                  <t-icon name="history" size="15px" />
                                  <span>{{ $t('knowledgeBase.chunkHistory') }}</span>
                                </div>
                                <div class="chunk-history-current">
                                  v{{ chunk.original.content_revision || 0 }} · {{ $t('knowledgeBase.currentVersion') }} ·
                                  {{ chunk.original.is_enabled ? $t('knowledgeBase.enabledStatus') : $t('knowledgeBase.disabledStatus') }}
                                </div>
                              </div>
                              <div class="chunk-history-diff-legend">
                                <span class="chunk-history-diff-legend-item chunk-history-diff-legend-item--add">
                                  <i />{{ $t('knowledgeBase.diffAddedInCurrent') }}
                                </span>
                                <span class="chunk-history-diff-legend-item chunk-history-diff-legend-item--del">
                                  <i />{{ $t('knowledgeBase.diffRemovedFromCurrent') }}
                                </span>
                              </div>
                            </div>
                            <div v-if="chunkHistoryLoading === chunk.original.id" class="chunk-history-popup-state">
                              <t-loading size="small" />
                              <span>{{ $t('common.loading') }}</span>
                            </div>
                            <div v-else-if="!(chunkHistories[chunk.original.id] || []).length" class="chunk-history-popup-state">
                              {{ $t('knowledgeBase.noChunkHistory') }}
                            </div>
                            <div v-else class="chunk-history-popup-list">
                              <div v-for="(revision, revisionIndex) in chunkHistories[chunk.original.id]" :key="revision.id"
                                class="chunk-history-popup-item"
                                :class="{ 'is-selected': selectedChunkRevision[chunk.original.id] === revision.revision }">
                                <button type="button" class="chunk-history-version-row"
                                  @click="selectChunkRevision(chunk.original, revision.revision)">
                                  <span class="chunk-history-version">v{{ revision.revision }}</span>
                                  <span class="chunk-history-time">{{ new Date(revision.edited_at).toLocaleString() }}</span>
                                  <span v-if="revisionStatusChanged(chunk.original, revisionIndex)" class="chunk-history-status-change">
                                    <t-icon :name="revision.is_enabled ? 'play-circle' : 'stop-circle'" size="13px" />
                                    {{ revision.is_enabled ? $t('knowledgeBase.enabledStatus') : $t('knowledgeBase.disabledStatus') }}
                                  </span>
                                  <t-icon :name="selectedChunkRevision[chunk.original.id] === revision.revision ? 'chevron-up' : 'chevron-down'"
                                    size="14px" class="chunk-history-row-chevron" />
                                </button>
                                <div v-if="selectedChunkRevision[chunk.original.id] === revision.revision" class="chunk-history-diff">
                                  <div class="chunk-history-diff-head">
                                    <span>{{ $t('knowledgeBase.compareRevisionWithCurrent', {
                                      revision: revision.revision,
                                      current: chunk.original.content_revision || 0,
                                    }) }}</span>
                                    <t-popconfirm theme="warning"
                                      :content="$t('knowledgeBase.revertRevisionConfirm', { revision: revision.revision })"
                                      @confirm="revertChunk(chunk.original, revision.revision)">
                                      <t-button class="icon-action-btn" size="small" variant="text" shape="square"
                                        :title="$t('knowledgeBase.revertRevision')"
                                        :loading="revertingRevision === `${chunk.original.id}:${revision.revision}`">
                                        <template #icon><t-icon name="rollback" size="14px" /></template>
                                      </t-button>
                                    </t-popconfirm>
                                  </div>
                                  <div v-if="!compactChunkDiff(chunk.original).length" class="chunk-history-no-diff">
                                    {{ $t('knowledgeBase.noContentChanges') }}
                                  </div>
                                  <pre v-else class="chunk-history-diff-body"><span
                                    v-for="(line, lineIndex) in compactChunkDiff(chunk.original)" :key="lineIndex"
                                    :class="['chunk-history-diff-line', `chunk-history-diff-line--${line.type}`]">{{ diffLinePrefix(line.type) }}{{ line.text }}
</span></pre>
                                </div>
                              </div>
                            </div>
                          </div>
                        </template>
                      </t-popup>
                      <span class="chunk-toolbar-divider" />
                      <t-tooltip
                        :content="chunk.original.is_enabled ? $t('knowledgeBase.disableChunk') : $t('knowledgeBase.enableChunk')"
                        placement="top">
                        <t-switch :key="`${chunk.original.id}-${chunk.original.is_enabled}`" size="small"
                          :value="chunk.original.is_enabled" :loading="chunkStatusLoading === chunk.original.id"
                          :disabled="chunkStatusLoading === chunk.original.id"
                          @change="(value: boolean) => toggleChunkEnabled(chunk.original, value)" />
                      </t-tooltip>
                    </template>
                  </div>
                </div>
                <div v-if="editingChunkId === chunk.original.id" class="chunk-editor">
                  <div class="chunk-editor-label">
                    <t-icon name="edit" size="14px" />
                    <span>{{ $t('knowledgeBase.editChunkContent') }}</span>
                  </div>
                  <t-textarea v-model="chunkDraft" :autosize="{ minRows: 6, maxRows: 20 }" autofocus />
                  <div class="chunk-editor-actions">
                    <t-button size="small" variant="outline" :disabled="savingChunkId === chunk.original.id"
                      @click="editingChunkId = ''">{{ $t('common.cancel') }}</t-button>
                    <t-button size="small" theme="primary" :loading="savingChunkId === chunk.original.id"
                      @click="saveChunkEdit(chunk.original)">{{ $t('common.save') }}</t-button>
                  </div>
                </div>
                <div v-else class="md-content" :class="{ 'chunk-disabled': !chunk.original.is_enabled }" v-html="chunk.processedContent"></div>

              </div>
            </div>
            </template>
          </div>

          <!-- 文档预览视图 -->
          <div v-if="(viewMode === 'merged' || viewMode === 'chunks') && details.total > CHUNK_PAGE_SIZE"
            class="chunk-pagination">
            <t-pagination v-model="chunkPage" :total="details.total" :page-size="CHUNK_PAGE_SIZE" size="small"
              show-jumper show-page-number :show-page-size="false" :disabled="details.chunkLoading"
              @change="handleChunkPageChange" />
          </div>

          <div v-else-if="viewMode === 'preview'">
            <DocumentPreview :knowledgeId="details.id" :fileType="details.file_type" :fileName="details.title"
              :active="viewMode === 'preview'" />
          </div>
        </section>
      </div>

    </t-drawer>
  </div>
</template>
<style scoped lang="less">
@import "./css/markdown.less";

.section-title-actions,
.metadata-actions,
.chunk-editor-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.section-title-actions { justify-content: space-between; }
.metadata-empty { color: var(--td-text-color-placeholder); }
.metadata-actions, .chunk-editor-actions { margin-top: 8px; justify-content: flex-end; }
.chunk-disabled { opacity: .5; }

.chunk-pagination {
  display: flex;
  justify-content: center;
  margin-top: 20px;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.chunk-page-loading {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 120px;
  color: var(--td-text-color-secondary);
}

.icon-action-btn {
  width: 28px;
  min-width: 28px;
  height: 28px;
  padding: 0;
  color: var(--td-text-color-secondary);
  border-radius: 4px;

  &:hover,
  &.is-active {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

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

.doc-drawer-header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  width: 100%;
  padding-right: 32px;
}

.doc-drawer-header-icon {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(7, 192, 95, 0.1);
  color: var(--td-brand-color);
  font-size: 16px;
}

.doc-drawer-header-text {
  flex: 1 1 auto;
  min-width: 0;
}

.doc-drawer-header-title {
  font-size: 15px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.doc-drawer-body {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.doc-drawer-body .setting-drawer__section {
  padding: 12px 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: 14px;

  &:first-child {
    padding-top: 0;
  }

  &:last-child {
    border-bottom: none;
    padding-bottom: 0;
  }
}

.doc-drawer-body .setting-drawer__section-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 4px;
  user-select: none;
  display: flex;
  align-items: center;
  gap: 8px;

  &::before {
    content: '';
    width: 3px;
    height: 14px;
    background: var(--td-brand-color);
    border-radius: 2px;
    flex-shrink: 0;
  }
}

.doc-detail-rows {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.doc-detail-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  line-height: 1.6;
}

.doc-detail-label {
  flex: 0 0 72px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.doc-detail-value {
  flex: 1;
  min-width: 0;
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.metadata-editor-row {
  display: flex;
  align-items: center;
}

.metadata-capability-icon {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  cursor: help;
}

.metadata-count {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  font-weight: 400;
}

.metadata-display {
  min-width: 0;
}

.summary-section-heading,
.summary-refreshing-indicator {
  display: flex;
  align-items: center;
}

.summary-section-heading {
  min-width: 0;
  gap: 10px;
}

.summary-refreshing-indicator {
  gap: 5px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  white-space: nowrap;
}

.metadata-grid {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 7px 18px;
}

.metadata-item {
  display: inline-flex;
  align-items: baseline;
  gap: 5px;
  min-width: 0;
}

.metadata-item-key,
.metadata-item-value {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.metadata-item-key {
  color: var(--td-text-color-placeholder);
  font-size: 12px;

  &::after {
    content: ':';
  }
}

.metadata-item-value {
  color: var(--td-text-color-primary);
  font-size: 13px;
}

.metadata-empty-action,
.metadata-add-row {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 5px;
  width: fit-content;
  min-height: 28px;
  padding: 0 4px;
  border: none;
  border-radius: 4px;
  color: var(--td-text-color-secondary);
  background: transparent;
  cursor: pointer;
  font: inherit;
  font-size: 12px;

  &:hover {
    color: var(--td-brand-color);
    background: var(--td-brand-color-light);
  }
}

.metadata-editor {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.metadata-editor-row {
  gap: 6px;
}

.metadata-key-input {
  flex: 0 1 30%;
  min-width: 100px;
}

.metadata-type-select {
  flex: 0 0 92px;
}

.metadata-value-input,
.metadata-null-value {
  flex: 1;
  min-width: 110px;
}

.metadata-null-value {
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--td-component-border);
  border-radius: 3px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-component-disabled);
  font-size: 13px;
  line-height: 30px;
}

.metadata-remove-btn:hover {
  color: var(--td-error-color);
  background: var(--td-error-color-light);
}

@media (max-width: 720px) {
  .metadata-editor-row {
    flex-wrap: wrap;
  }

  .metadata-key-input,
  .metadata-value-input,
  .metadata-null-value {
    flex: 1 1 calc(50% - 50px);
  }
}

.doc-content-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.doc-content-section-head-left {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;

  .setting-drawer__section-title {
    margin-bottom: 0;
  }
}

.doc-content-section {
  gap: 12px;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
  flex-grow: 0;
}

.header-action-btn {
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
  overflow: hidden;
  min-width: 0;
}

.kp-drawer-shell> :deep(.kp-timeline) {
  width: 100%;
  height: 100%;
}

:deep(.kp-secondary-drawer .t-drawer__body) {
  padding: 0 !important;
}

:deep(.kp-secondary-drawer .t-drawer__content) {
  background: var(--td-bg-color-container);
}

// 文档摘要区域
.summary_wrapper {
  position: relative;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
  border-radius: 6px;

  &.summary_clickable {
    cursor: pointer;
  }
}

.summary-title-actions,
.summary_editor_actions {
  display: flex;
  align-items: center;
  gap: 6px;
}

.summary_editor {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.summary_editor_actions {
  justify-content: flex-end;
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
    background: linear-gradient(transparent, var(--td-bg-color-container) 80%);
    border-radius: 0 0 6px 6px;
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
  min-height: 42px;
  background: var(--td-bg-color-container);
  border: 1px dashed var(--td-component-border);
  border-radius: 6px;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
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

.doc-tag-chips {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}

.doc-tag-chip {
  max-width: 140px;
  height: 20px;
  line-height: 20px;
  border-radius: 999px;
  border-color: var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  padding: 0 8px;
  background: transparent;

  .tag-text {
    display: inline-block;
    max-width: 100px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
    font-size: 11px;
  }
}

.chunk-count {
  color: var(--td-text-color-secondary);
  font-size: 12px;
  background: var(--td-bg-color-container-hover);
  padding: 2px 8px;
  border-radius: 4px;
  flex-shrink: 0;
}

.view-mode-buttons {
  display: flex;
  gap: 4px;
  flex-shrink: 0;

  .view-mode-btn {
    height: 28px;
    min-width: 60px;
  }
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
  border-radius: 6px;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);

  &.chunk-item--disabled {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.chunk-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  min-height: 28px;
  margin-bottom: 10px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--td-component-stroke);

  .chunk-heading {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    min-width: 0;
  }

  .chunk-index {
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 600;
  }

  .chunk-header-right {
    display: flex;
    align-items: center;
    gap: 2px;
    flex-shrink: 0;
  }

  .chunk-meta {
    color: var(--td-text-color-placeholder);
    font-size: 11px;
  }
}

.chunk-toolbar-divider {
  width: 1px;
  height: 16px;
  margin: 0 6px;
  background: var(--td-component-stroke);
}

.chunk-editor {
  margin: 4px 0 10px;
  padding: 12px;
  border-radius: 5px;
  background: var(--td-bg-color-secondarycontainer);
}

.chunk-editor-label {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 8px;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  font-weight: 500;
}

.chunk-history-popup {
  width: min(560px, calc(100vw - 32px));
  max-height: min(620px, calc(100vh - 96px));
  overflow: hidden;
  border-radius: 6px;
  background: var(--td-bg-color-container);
}

.chunk-history-popup-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 8px 12px 7px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.chunk-history-popup-title {
  display: flex;
  align-items: center;
  gap: 6px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.chunk-history-current {
  margin-top: 2px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.chunk-history-popup-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  min-height: 100px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.chunk-history-popup-list {
  max-height: min(520px, calc(100vh - 180px));
  overflow-y: auto;
}

.chunk-history-popup-item {
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }

  &.is-selected {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.chunk-history-version-row {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 40px;
  padding: 8px 14px;
  border: none;
  color: var(--td-text-color-secondary);
  background: transparent;
  cursor: pointer;
  font: inherit;
  text-align: left;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.chunk-history-version {
  min-width: 32px;
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-weight: 600;
}

.chunk-history-time {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.chunk-history-status-change {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-left: auto;
  color: var(--td-text-color-secondary);
  font-size: 11px;
}

.chunk-history-row-chevron {
  margin-left: auto;
  color: var(--td-text-color-placeholder);
}

.chunk-history-status-change + .chunk-history-row-chevron {
  margin-left: 0;
}

.chunk-history-diff {
  padding: 0 12px 10px;
}

.chunk-history-diff-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-height: 30px;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.chunk-history-diff-legend {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 0;
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-size: 10px;
}

.chunk-history-diff-legend-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;

  i {
    width: 7px;
    height: 7px;
    border-radius: 2px;
  }

  &--add i {
    background: var(--td-success-color);
  }

  &--del i {
    background: var(--td-error-color);
  }
}

.chunk-history-no-diff {
  padding: 12px;
  border-radius: 4px;
  color: var(--td-text-color-placeholder);
  background: var(--td-bg-color-container);
  text-align: center;
  font-size: 12px;
}

.chunk-history-diff-body {
  max-height: 240px;
  margin: 0;
  padding: 8px 0;
  overflow: auto;
  border: 1px solid var(--td-component-border);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  font-family: var(--app-font-family-mono);
  font-size: 11px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

.chunk-history-diff-line {
  display: block;
  min-height: 17px;
  padding: 0 10px;

  &--add {
    color: var(--td-success-color-active);
    background: var(--td-success-color-light);
  }

  &--del {
    color: var(--td-error-color-active);
    background: var(--td-error-color-light);
  }

  &--same {
    color: var(--td-text-color-secondary);
  }

  &--skip {
    color: var(--td-text-color-placeholder);
  }
}

.chunk-context-popup,
.chunk-questions-popup {
  width: min(520px, calc(100vw - 32px));
  max-height: min(560px, calc(100vh - 96px));
  overflow: hidden;
  border-radius: 6px;
  background: var(--td-bg-color-container);
}

.chunk-popup-head,
.chunk-popup-title,
.chunk-popup-actions,
.chunk-popup-state {
  display: flex;
  align-items: center;
}

.chunk-popup-head {
  justify-content: space-between;
  min-height: 38px;
  padding: 4px 8px 4px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.chunk-popup-title {
  gap: 7px;
  color: var(--td-text-color-primary);
  font-size: 13px;
  font-weight: 600;
}

.chunk-popup-count {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  font-weight: 400;
}

.chunk-question-stale-hint {
  color: var(--td-warning-color);
  font-size: 11px;
  font-weight: 400;
}

.chunk-popup-actions {
  gap: 2px;
}

.chunk-popup-state {
  justify-content: center;
  gap: 8px;
  min-height: 120px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.chunk-context-popup-body {
  max-height: min(480px, calc(100vh - 170px));
  padding: 14px 16px;
  overflow: auto;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}

.chunk-questions-popup-body {
  max-height: min(480px, calc(100vh - 170px));
  padding: 0 10px 8px;
  overflow-y: auto;
}

.question-item,
.question-inline-editor,
.question-composer,
.questions-empty {
  display: flex;
  align-items: center;
}

.questions-list {
  padding-top: 0;
}

.question-item {
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 5px 6px;
  border-bottom: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: transparent;
  font-size: 13px;
  color: var(--td-text-color-primary);
  line-height: 20px;

  &:hover {
    background: var(--td-bg-color-container-hover);

    .question-actions {
      opacity: 1;
    }
  }

  .question-text {
    flex: 1;
    line-height: 20px;
    word-break: break-word;
  }

  .delete-question-btn {
    color: var(--td-text-color-placeholder);

    &:hover {
      color: var(--td-error-color);
      background: var(--td-error-color-light);
    }
  }
}

.question-leading-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 20px;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}

.question-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  opacity: 0;
  transition: opacity 0.15s ease;

  &:focus-within {
    opacity: 1;
  }
}

.question-inline-editor {
  align-items: center;
  gap: 6px;
  flex: 1;
}

.question-inline-editor :deep(.t-input) {
  flex: 1;
}

.questions-empty {
  justify-content: center;
  gap: 8px;
  padding: 20px 8px 12px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.question-composer {
  gap: 6px;
  padding: 8px 4px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.question-composer :deep(.t-input) {
  flex: 1;
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
.t-drawer.doc-main-drawer {
  .t-drawer__header {
    padding: 14px 18px;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  .t-drawer__body {
    padding: 16px 18px;
  }
}

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
