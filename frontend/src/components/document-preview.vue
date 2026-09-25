// @ts-nocheck
<script setup lang="ts">
import { ref, shallowRef, watch, onMounted, onUnmounted, nextTick, defineAsyncComponent } from 'vue';
import { previewKnowledgeFile } from '@/api/knowledge-base/index';
import { previewTemporaryAttachment } from '@/api/chat/temporary-attachments';
import { downloadArtifact } from '@/api/chat';
import hljs from 'highlight.js';
import 'highlight.js/styles/github.css';
import 'katex/dist/katex.min.css';
import { useI18n } from 'vue-i18n';
import { sanitizeHTML, sanitizeMarkdownHTML } from '@/utils/security';
import { preparePptxPreview, isCompletePptxRender } from '@/utils/pptxPreview';
import { renderDocumentPreviewMarkdown } from '@/utils/documentPreviewMarkdown';
import { buildHtmlPreview } from '@/utils/htmlPreview';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';
import { renderMermaidToSvg } from '@/utils/mermaidShared';
import {
  FILE_PREVIEW_SNIFF_BYTES,
  getHighlightLang as resolveHighlightLang,
  getPreviewMimeType,
  prettyPrintJson,
  resolveFilePreviewExt,
  resolvePreviewKind,
  shouldPrettyPrintJson,
  sniffPreview,
  isValidUTF8,
  type FilePreviewKind,
} from '@/utils/filePreview';
import {
  narrowTextLines,
  ngramOverlap,
  normalizeForMatch,
  pickBestTexts,
  type SourceLocateRequest,
  type SourceLocator,
} from '@/utils/sourceLocator';
import {
  buildTextIndex,
  findTextRange,
  highlightElements,
  highlightRanges,
  rangeForOffsets,
  scrollRectIntoContainer,
} from '@/utils/sourceLocatorDom';
import { renderEpubPreview } from '@/utils/epubPreview';


const VueOfficePptx = defineAsyncComponent(() => import('@vue-office/pptx'));
const PdfSourceViewer = defineAsyncComponent(() => import('@/components/source-preview/PdfSourceViewer.vue'));

const { t } = useI18n();

const props = defineProps<{
  sourceBlob?: Blob;
  knowledgeId?: string;
  sessionId?: string;
  attachmentId?: string;
  messageId?: string;
  artifactIndex?: number;
  fileType: string;
  fileName: string;
  active: boolean;
  fillHeight?: boolean;
  /** Place preview actions beside the host header's download button. */
  toolbarTarget?: HTMLElement | null;
  /**
   * Source mode renders PDFs with pdf.js instead of the browser viewer so a
   * citation can be scrolled to and highlighted.
   */
  sourceMode?: boolean;
  /** Where to scroll and what to highlight once the file is rendered. */
  locate?: SourceLocateRequest | null;
}>();

export type SourceLocateResult = { found: boolean; precise: boolean };

const emit = defineEmits<{
  located: [result: SourceLocateResult];
}>();

const loading = ref(false);
const error = ref('');
const previewType = ref<FilePreviewKind>('unsupported');
const blobUrl = ref('');
const textContent = ref('');
const highlightedCode = ref('');
const markdownHtml = ref('');
const excelHtml = ref('');
const mermaidSvg = ref('');
const htmlViewMode = ref<'render' | 'source'>('render');
const pptxData = shallowRef<ArrayBuffer | null>(null);
const pdfData = shallowRef<ArrayBuffer | null>(null);
const epubHtml = ref('');
let epubObjectUrls: string[] = [];
let excelSheetNames: string[] = [];
let previewExt = '';
let pptxSlideCount = 0;
function onPptxRendered(result: unknown) {
  if (!isCompletePptxRender(result, pptxSlideCount)) {
    error.value = t('preview.loadFailed');
    return;
  }
  scheduleLocate();
}
const docxContainer = ref<HTMLElement | null>(null);
const imageNaturalWidth = ref(0);
const imageNaturalHeight = ref(0);
let loadedForId = '';

const isFullscreen = ref(false);
const previewRoot = ref<HTMLElement | null>(null);
const previewContent = ref<HTMLElement | null>(null);

function focusPreviewContent() {
  const root = previewRoot.value;
  if (!props.active || !root?.isConnected) return;
  // Focus the actual scrolling element so the browser handles arrows, Space,
  // PageUp/Down and Home/End, including native iframe and media controls.
  const target = previewContent.value || docxContainer.value || root;
  target.focus({ preventScroll: true });
}

watch(
  () => [props.active, previewRoot.value, getPreviewSourceKey(), props.sourceBlob, htmlViewMode.value, isFullscreen.value],
  async () => {
    if (!props.active) return;
    const previousFocus = document.activeElement;
    await nextTick();
    // Do not override a user who moved to another control while rendering.
    if (document.activeElement !== previousFocus && document.activeElement !== document.body) return;
    focusPreviewContent();
  },
  { flush: 'post' },
);

watch(
  () => [previewContent.value, docxContainer.value],
  () => {
    // While downloading, focus rests on the preview root. Transfer it only
    // if it is still there when the content arrives (never steal input focus).
    if (document.activeElement === previewRoot.value) focusPreviewContent();
  },
  { flush: 'post' },
);

function onPreviewFrameLoad() {
  if (document.activeElement === previewContent.value || document.activeElement === previewRoot.value) {
    focusPreviewContent();
  }
}

function toggleFullscreen() {
  if (isFullscreen.value) {
    void exitPreviewFullscreen();
  } else {
    void enterPreviewFullscreen();
  }
}

let fallbackOverflow: string | null = null;
let fullscreenDisposed = false;

async function enterPreviewFullscreen() {
  const root = previewRoot.value;
  if (!props.active || !root?.isConnected) return;
  // Native fullscreen also handles Escape when focus is inside a PDF viewer
  // or a sandboxed HTML iframe, whose keyboard events cannot bubble here.
  if (root.requestFullscreen) {
    try {
      await root.requestFullscreen();
      if (fullscreenDisposed || !props.active) {
        if (document.fullscreenElement === root) await document.exitFullscreen();
      } else {
        syncFullscreen();
      }
      return;
    } catch {
      // Embedded hosts may disallow the Fullscreen API. Keep page fullscreen
      // available there, with Escape handled in the parent document.
    }
  }
  if (fullscreenDisposed || !props.active || !root.isConnected) return;
  if (fallbackOverflow === null) fallbackOverflow = document.body.style.overflow;
  document.body.style.overflow = 'hidden';
  isFullscreen.value = true;
}

function clearFallbackFullscreen() {
  if (fallbackOverflow !== null) {
    document.body.style.overflow = fallbackOverflow;
    fallbackOverflow = null;
  }
  isFullscreen.value = false;
}

async function exitPreviewFullscreen() {
  if (previewRoot.value && document.fullscreenElement === previewRoot.value) {
    try {
      await document.exitFullscreen();
    } catch {
      // The browser may already have exited in response to Escape.
    }
    syncFullscreen();
    return;
  }
  clearFallbackFullscreen();
}

function syncFullscreen() {
  if (fallbackOverflow === null) {
    isFullscreen.value = !!previewRoot.value && document.fullscreenElement === previewRoot.value;
  }
}

function onFullscreenEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.isComposing || !isFullscreen.value || !props.active) return;
  // Consume this Escape before a containing drawer can also close.
  event.preventDefault();
  event.stopImmediatePropagation();
  void exitPreviewFullscreen();
}

watch(() => props.active, (active) => {
  if (!active) void exitPreviewFullscreen();
});

onMounted(() => {
  document.addEventListener('fullscreenchange', syncFullscreen);
  window.addEventListener('keydown', onFullscreenEscape, true);
});

onUnmounted(() => {
  fullscreenDisposed = true;
  document.removeEventListener('fullscreenchange', syncFullscreen);
  window.removeEventListener('keydown', onFullscreenEscape, true);
  void exitPreviewFullscreen();
  clearFallbackFullscreen();
});


function ensureBlobType(blob: Blob, ft: string): Blob {
  const expected = getPreviewMimeType(ft);
  if (blob.type === expected) return blob;
  return new Blob([blob], { type: expected });
}

function getHighlightLang(ft: string): string {
  return resolveHighlightLang(ft);
}

async function renderDocx(blob: Blob) {
  const { renderAsync } = await import('docx-preview');
  if (docxContainer.value) {
    docxContainer.value.innerHTML = '';
    await renderAsync(blob, docxContainer.value, undefined, {
      className: 'docx-preview-wrapper',
      inWrapper: true,
      ignoreWidth: false,
      ignoreHeight: false,
      ignoreFonts: false,
      breakPages: true,
      ignoreLastRenderedPageBreak: true,
      experimental: false,
      trimXmlDeclaration: true,
      useBase64URL: true,
    });
  }
}

function decodeCSVBlob(arrayBuffer: ArrayBuffer): string {
  const bytes = new Uint8Array(arrayBuffer);
  if (bytes[0] === 0xEF && bytes[1] === 0xBB && bytes[2] === 0xBF) {
    return new TextDecoder('utf-8').decode(bytes);
  }
  if (isValidUTF8(bytes)) {
    return new TextDecoder('utf-8').decode(bytes);
  }
  return new TextDecoder('gbk').decode(bytes);
}

async function renderExcel(blob: Blob, fileType?: string) {
  const XLSX = await import('xlsx');
  const arrayBuffer = await blob.arrayBuffer();

  let workbook;
  const lowerType = fileType?.toLowerCase();
  if (lowerType === 'csv') {
    const csvText = decodeCSVBlob(arrayBuffer);
    workbook = XLSX.read(csvText, { type: 'string' });
  } else if (lowerType === 'tsv' || lowerType === 'tab') {
    const tsvText = decodeCSVBlob(arrayBuffer);
    workbook = XLSX.read(tsvText, { type: 'string', FS: '\t' });
  } else {
    workbook = XLSX.read(arrayBuffer, { type: 'array' });
  }

  excelSheetNames = [...workbook.SheetNames];
  let html = '';
  workbook.SheetNames.forEach((name, sheetIdx) => {
    const sheet = workbook.Sheets[name];
    const sheetHtml = XLSX.utils.sheet_to_html(sheet, { id: `sheet-${sheetIdx}` });
    html += `<div class="excel-sheet">`;
    if (workbook.SheetNames.length > 1) {
      html += `<div class="excel-sheet-name">${name}</div>`;
    }
    html += sheetHtml;
    html += `</div>`;
  });
  excelHtml.value = sanitizeHTML(html);
}

async function renderText(blob: Blob, fileType: string) {
  let text = await blob.text();
  if (shouldPrettyPrintJson(fileType)) {
    text = prettyPrintJson(text);
  }
  textContent.value = text;

  const lang = getHighlightLang(fileType);
  if (lang && hljs.getLanguage(lang)) {
    try {
      highlightedCode.value = hljs.highlight(text, { language: lang }).value;
      return;
    } catch { /* fallthrough */ }
  }
  const auto = hljs.highlightAuto(text);
  highlightedCode.value = auto.value;
}

async function renderMarkdown(blob: Blob) {
  const text = await blob.text();

  // 校验文本内容是否有效
  if (!text || typeof text !== 'string') {
    markdownHtml.value = '<p style="color: var(--td-text-color-disabled); text-align: center; padding: 20px;">文档内容为空</p>';
    return;
  }

  markdownHtml.value = renderDocumentPreviewMarkdown(text);
}

function onImageLoad(e: Event) {
  const img = e.target as HTMLImageElement;
  imageNaturalWidth.value = img.naturalWidth;
  imageNaturalHeight.value = img.naturalHeight;
}

function getPreviewSourceKey(): string {
  if (props.sourceBlob) {
    return `resource-blob:${props.fileName}:${props.fileType}:${props.sourceBlob.size}:${props.sourceBlob.type}`;
  }
  if (props.knowledgeId) return `knowledge:${props.knowledgeId}`;
  if (props.sessionId && props.attachmentId) return `attachment:${props.sessionId}:${props.attachmentId}`;
  if (
    props.sessionId &&
    props.messageId &&
    Number.isInteger(props.artifactIndex) &&
    (props.artifactIndex as number) >= 0
  ) {
    return `artifact:${props.sessionId}:${props.messageId}:${props.artifactIndex}`;
  }
  return '';
}

// Skill-generated HTML is often a self-contained chart that needs its own
// scripts. Knowledge-base files and chat attachments are untrusted uploads:
// they stay as source, matching the previous text preview.
function allowsHtmlScriptPreview(): boolean {
  return getPreviewSourceKey().startsWith('artifact:');
}

async function fetchPreviewBlob(): Promise<Blob> {
  if (props.sourceBlob) return props.sourceBlob;
  if (props.knowledgeId) {
    return previewKnowledgeFile(props.knowledgeId);
  }
  if (props.sessionId && props.attachmentId) {
    return previewTemporaryAttachment(props.sessionId, props.attachmentId);
  }
  if (
    props.sessionId &&
    props.messageId &&
    Number.isInteger(props.artifactIndex) &&
    (props.artifactIndex as number) >= 0
  ) {
    return downloadArtifact(props.sessionId, props.messageId, props.artifactIndex as number);
  }
  throw new Error('Missing preview source');
}

async function renderMermaid(blob: Blob) {
  const text = await blob.text();
  const svg = await renderMermaidToSvg(text, `file-preview-mermaid-${Date.now()}`);
  if (svg) {
    mermaidSvg.value = sanitizeMarkdownHTML(svg);
    return;
  }
  previewType.value = 'text';
  await renderText(new Blob([text], { type: 'text/plain' }), 'mmd');
}

async function openMermaid() {
  const svg = mermaidSvg.value;
  if (!svg) return;
  // The diagram viewer mounts on body, outside the native fullscreen element.
  await exitPreviewFullscreen();
  if (props.active && previewRoot.value?.isConnected) openMermaidFullscreen(svg);
}

async function loadPreview() {
  const sourceKey = getPreviewSourceKey();
  if (!sourceKey) return;
  if (loadedForId === sourceKey) return;

  cleanup();
  loading.value = true;
  error.value = '';
  htmlViewMode.value = allowsHtmlScriptPreview() ? 'render' : 'source';

  let ft = resolveFilePreviewExt(props.fileName, props.fileType);
  previewType.value = resolvePreviewKind(ft);

  try {
    const rawBlob = await fetchPreviewBlob();
    let kind = resolvePreviewKind(ft);
    if (kind === 'unsupported') {
      const sample = new Uint8Array(await rawBlob.slice(0, FILE_PREVIEW_SNIFF_BYTES).arrayBuffer());
      const sniffed = sniffPreview(sample);
      kind = sniffed.kind;
      if (sniffed.ext) ft = sniffed.ext;
    }
    previewType.value = kind;

    if (kind === 'unsupported') {
      loading.value = false;
      return;
    }

    const blob = ensureBlobType(rawBlob, ft);
    loadedForId = sourceKey;

    loading.value = false;
    await nextTick();

    previewExt = ft;
    switch (kind) {
      case 'pdf': {
        if (props.sourceMode) {
          pdfData.value = await blob.arrayBuffer();
        } else {
          blobUrl.value = URL.createObjectURL(blob);
        }
        break;
      }
      case 'image':
      case 'audio':
      case 'video': {
        blobUrl.value = URL.createObjectURL(blob);
        break;
      }
      case 'epub': {
        const rendered = await renderEpubPreview(await blob.arrayBuffer());
        epubObjectUrls = rendered.objectUrls;
        epubHtml.value = sanitizeHTML(rendered.html);
        break;
      }
      case 'html': {
        if (allowsHtmlScriptPreview()) {
          const previewHtml = buildHtmlPreview(await blob.text());
          blobUrl.value = URL.createObjectURL(new Blob([previewHtml], { type: 'text/html;charset=utf-8' }));
        }
        await renderText(blob, ft || 'html');
        break;
      }
      case 'docx': {
        await renderDocx(blob);
        break;
      }
      case 'excel': {
        await renderExcel(blob, ft);
        break;
      }
      case 'text': {
        await renderText(blob, ft);
        break;
      }
      case 'markdown': {
        await renderMarkdown(blob);
        break;
      }
      case 'pptx': {
        const prepared = await preparePptxPreview(await blob.arrayBuffer());
        if (getPreviewSourceKey() !== sourceKey || !props.active) return;
        pptxSlideCount = prepared.slideCount;
        pptxData.value = prepared.data;
        break;
      }
      case 'mermaid': {
        await renderMermaid(blob);
        break;
      }
    }
    // PPTX renders asynchronously and locates from its rendered event.
    if (kind !== 'pptx') scheduleLocate();
  } catch (err: any) {
    console.error('Document preview failed:', err);
    error.value = err?.message || t('preview.loadFailed');
  } finally {
    loading.value = false;
  }
}

function cleanup() {
  if (blobUrl.value) {
    URL.revokeObjectURL(blobUrl.value);
    blobUrl.value = '';
  }
  textContent.value = '';
  highlightedCode.value = '';
  markdownHtml.value = '';
  excelHtml.value = '';
  mermaidSvg.value = '';
  htmlViewMode.value = 'render';
  pptxData.value = null;
  pdfData.value = null;
  for (const url of epubObjectUrls) URL.revokeObjectURL(url);
  epubObjectUrls = [];
  epubHtml.value = '';
  excelSheetNames = [];
  clearLocateMarks();
  pptxSlideCount = 0;
  imageNaturalWidth.value = 0;
  imageNaturalHeight.value = 0;
  loadedForId = '';
  if (docxContainer.value) {
    docxContainer.value.innerHTML = '';
  }
}

// ── Citation locating ──
// Each preview kind reveals a SourceLocateRequest its own way: structural
// locators first (page, block, slide, rows, section, time), then the quoted
// text inside whatever they narrowed down, then the quotes anywhere.

type ImageMark = { left: number; top: number; width: number; height: number };
const imageMarks = ref<ImageMark[]>([]);
const audioQuote = ref('');
const audioRange = ref('');
let clearLocate: Array<() => void> = [];

function clearLocateMarks() {
  for (const clear of clearLocate) clear();
  clearLocate = [];
  imageMarks.value = [];
  audioQuote.value = '';
  audioRange.value = '';
}

let locateScheduled = false;
function scheduleLocate() {
  if (!props.locate || locateScheduled) return;
  locateScheduled = true;
  void nextTick(() => {
    locateScheduled = false;
    applyLocate();
  });
}

watch(
  () => props.locate?.token,
  () => {
    if (loadedForId && !loading.value) scheduleLocate();
  },
);

const NOT_FOUND: SourceLocateResult = { found: false, precise: false };

function locatorQuotes(request: SourceLocateRequest, locators: SourceLocator[] = request.locators): string[] {
  return [...locators.map((l) => l.quote || '').filter(Boolean), ...request.quotes];
}

function findQuoteRange(root: Element | null | undefined, quotes: string[]): Range | null {
  if (!root) return null;
  const index = buildTextIndex(root);
  for (const quote of quotes) {
    const range = findTextRange(root, quote, index);
    if (range) return range;
  }
  return null;
}

function reveal(container: Element | null | undefined, target: Range | Element | null | undefined) {
  if (!container || !target) return;
  scrollRectIntoContainer(container, target.getBoundingClientRect());
}

function commitMarks(ranges: Range[], elements: Element[]) {
  if (ranges.length) clearLocate.push(highlightRanges(ranges));
  if (elements.length) clearLocate.push(highlightElements(elements));
}

/** Reveal the first quote found under `root`, scrolling `container`. */
function locateQuote(root: Element | null | undefined, container: Element | null | undefined, quotes: string[]): SourceLocateResult {
  const range = findQuoteRange(root, quotes);
  if (!range) return NOT_FOUND;
  commitMarks([range], []);
  reveal(container, range);
  return { found: true, precise: true };
}

/** The element among `blocks` near index `at` whose text best matches `quote`. */
function pickNearby(blocks: Element[], at: number, quote?: string): Element | null {
  if (!blocks.length) return null;
  const center = Math.max(0, Math.min(blocks.length - 1, at));
  const needle = normalizeForMatch(quote || '').slice(0, 120);
  if (needle.length < 4) return blocks[center];
  let best = { el: blocks[center], score: -1 };
  for (let i = Math.max(0, center - 4); i <= Math.min(blocks.length - 1, center + 4); i++) {
    const score = ngramOverlap(needle, normalizeForMatch(blocks[i].textContent || ''));
    if (score > best.score) best = { el: blocks[i], score };
  }
  return best.score >= 0.4 ? best.el : blocks[center];
}

/** The candidates that best match the cited sentence, or all of them when none aligns. */
function narrowToSentence(candidates: Element[], request: SourceLocateRequest): Element[] {
  const picked = pickBestTexts(candidates.map((el) => el.textContent || ''), request.sentence || '');
  return picked.length ? picked.map((i) => candidates[i]) : candidates;
}

function contentsRange(el: Element): Range {
  const range = el.ownerDocument.createRange();
  range.selectNodeContents(el);
  return range;
}

function locateDocx(request: SourceLocateRequest): SourceLocateResult {
  const root = docxContainer.value;
  if (!root) return NOT_FOUND;
  const locators = request.locators.filter((l) => l.type === 'docx' && l.block);
  if (locators.length) {
    // Body paragraphs and tables in order, the unit the backend counts.
    const blocks = Array.from(root.querySelectorAll('section > article')).flatMap((article) =>
      Array.from(article.children).filter((c) => c.tagName === 'P' || c.tagName === 'TABLE'),
    );
    const targets: Element[] = [];
    for (const loc of locators) {
      const el = pickNearby(blocks, (loc.block as number) - 1, loc.quote);
      if (el && !targets.includes(el)) targets.push(el);
    }
    if (targets.length) {
      const range = findQuoteRange(targets[0], locatorQuotes(request, locators));
      commitMarks(range ? [range] : [], targets);
      reveal(root, range || targets[0]);
      return { found: true, precise: true };
    }
  }
  return locateQuote(root, root, request.quotes);
}

function locatePptx(request: SourceLocateRequest): SourceLocateResult {
  const box = previewContent.value;
  if (!box) return NOT_FOUND;
  const slides = Array.from(box.querySelectorAll('.pptx-preview-slide-wrapper'));
  const locators = request.locators.filter((l) => l.type === 'slide' && l.slide);
  if (locators.length) {
    const slide = slides[(locators[0].slide as number) - 1];
    if (slide) {
      // Master and layout text repeats on every slide; match the slide's own.
      const own = slide.querySelector('.slide-wrapper') || slide;
      // A slide locator covers the whole slide; the cited bullet is narrower.
      const paragraphs = Array.from(own.querySelectorAll('p')).filter((p) => (p.textContent || '').trim());
      const cited = narrowToSentence(paragraphs, request);
      if (paragraphs.length && cited.length < paragraphs.length) {
        const ranges = cited.map(contentsRange);
        commitMarks(ranges, []);
        reveal(box, ranges[0]);
        return { found: true, precise: true };
      }
      const range = findQuoteRange(own, locatorQuotes(request, locators));
      commitMarks(range ? [range] : [], range ? [] : [slide]);
      reveal(box, range || slide);
      return { found: true, precise: !!range };
    }
  }
  for (const slide of slides) {
    const own = slide.querySelector('.slide-wrapper') || slide;
    const range = findQuoteRange(own, request.quotes);
    if (range) {
      commitMarks([range], []);
      reveal(box, range);
      return { found: true, precise: true };
    }
  }
  return NOT_FOUND;
}

/** Map sheet row numbers to table rows using the cell ids SheetJS emits. */
function sheetRows(table: Element): Map<number, Element> {
  const rows = new Map<number, Element>();
  for (const cell of Array.from(table.querySelectorAll('td[id]'))) {
    const match = /-[A-Z]+(\d+)$/.exec(cell.id);
    const tr = cell.closest('tr');
    if (match && tr && !rows.has(Number(match[1]))) rows.set(Number(match[1]), tr);
  }
  return rows;
}

function locateExcel(request: SourceLocateRequest): SourceLocateResult {
  const box = previewContent.value;
  if (!box) return NOT_FOUND;
  const locators = request.locators.filter((l) => l.type === 'sheet' && l.row_start);
  const targets: Element[] = [];
  for (const loc of locators) {
    const byName = loc.sheet ? excelSheetNames.indexOf(loc.sheet) : -1;
    const sheetIdx = byName >= 0 ? byName : 0;
    const table = box.querySelector(`#user-content-sheet-${sheetIdx}`) || box.querySelector(`#sheet-${sheetIdx}`);
    if (!table) continue;
    const rows = sheetRows(table);
    const last = Math.min(loc.row_end || loc.row_start || 0, (loc.row_start || 0) + 200);
    for (let r = loc.row_start as number; r <= last; r++) {
      const tr = rows.get(r);
      if (tr && !targets.includes(tr)) targets.push(tr);
    }
  }
  if (targets.length) {
    // A chunk often spans many rows; keep the ones the sentence is about.
    // Header rows repeat the column names every answer mentions.
    const body = targets.filter((tr) => tr.parentElement?.firstElementChild !== tr);
    const cited = body.length > 1 ? narrowToSentence(body, request) : targets;
    const rows = cited.length < body.length ? cited : targets;
    commitMarks([], rows);
    reveal(box, rows[0]);
    return { found: true, precise: true };
  }
  return locateQuote(box, box, request.quotes);
}

function locateEpub(request: SourceLocateRequest): SourceLocateResult {
  const box = previewContent.value;
  if (!box) return NOT_FOUND;
  const locators = request.locators.filter((l) => l.type === 'section' && l.section);
  const section = locators.length
    ? box.querySelector(`[data-epub-section="${locators[0].section}"]`)
    : null;
  if (section) {
    // A section locator covers the whole chapter; the cited paragraph is narrower.
    const paragraphs = Array.from(section.querySelectorAll('p, li, h1, h2, h3, h4, h5, h6')).filter(
      (el) => (el.textContent || '').trim(),
    );
    const cited = narrowToSentence(paragraphs, request);
    if (paragraphs.length && cited.length < paragraphs.length) {
      const ranges = cited.map(contentsRange);
      commitMarks(ranges, []);
      reveal(box, ranges[0]);
      return { found: true, precise: true };
    }
    const range = findQuoteRange(section, locatorQuotes(request, locators));
    if (range) commitMarks([range], []);
    reveal(box, range || section);
    return { found: true, precise: !!range };
  }
  return locateQuote(box, box, request.quotes);
}

function locateText(request: SourceLocateRequest): SourceLocateResult {
  const box = previewContent.value;
  const code = box?.querySelector('code') || box;
  if (!box || !code) return NOT_FOUND;
  // Offsets index the original file; pretty-printed JSON no longer matches it.
  const locators = request.locators.filter((l) => l.type === 'text' && (l.end || 0) > (l.start || 0));
  if (locators.length && previewType.value === 'text' && !shouldPrettyPrintJson(previewExt)) {
    const spans = locators.map((l): [number, number] => [l.start || 0, l.end || 0]);
    // A chunk spans several paragraphs; keep the lines the sentence is about.
    const cited = narrowTextLines(code.textContent || '', spans, request.sentence || '');
    const ranges = (cited.length ? cited : spans)
      .map(([start, end]) => rangeForOffsets(code, start, end))
      .filter((r): r is Range => !!r && !r.collapsed);
    if (ranges.length) {
      commitMarks(ranges, []);
      reveal(box, ranges[0]);
      return { found: true, precise: true };
    }
  }
  return locateQuote(code, box, locatorQuotes(request));
}

const MARKDOWN_BLOCKS = 'p, li, tr, h1, h2, h3, h4, h5, h6, pre, blockquote, dt, dd';

/**
 * Rendered Markdown has no offsets back into the file, so the cited chunk's
 * text bounds the candidate blocks and the sentence picks among them.
 */
function locateMarkdown(request: SourceLocateRequest): SourceLocateResult {
  const box = previewContent.value;
  if (!box) return NOT_FOUND;
  const scope = normalizeForMatch(request.scope || '');
  if (scope && request.sentence) {
    const blocks = Array.from(box.querySelectorAll(MARKDOWN_BLOCKS)).filter((el) => {
      if (el.querySelector(MARKDOWN_BLOCKS)) return false;
      const text = normalizeForMatch(el.textContent || '');
      return text.length >= 4 && scope.includes(text);
    });
    const cited = pickBestTexts(blocks.map((el) => el.textContent || ''), request.sentence);
    if (cited.length) {
      const ranges = cited.map((i) => contentsRange(blocks[i]));
      commitMarks(ranges, []);
      reveal(box, ranges[0]);
      return { found: true, precise: true };
    }
  }
  return locateQuote(box, box, locatorQuotes(request));
}

function locateImage(request: SourceLocateRequest): SourceLocateResult {
  const boxes = request.locators
    .filter((l) => l.type === 'pdf' && (l.page || 1) === 1 && l.bbox?.length === 4)
    .map((l) => {
      const [x0, y0, x1, y1] = l.bbox as number[];
      return { left: x0, top: y0, width: x1 - x0, height: y1 - y0 };
    });
  imageMarks.value = boxes;
  // Without regions the whole image is the cited source.
  return { found: true, precise: boxes.length > 0 };
}

function formatClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

function locateAudio(request: SourceLocateRequest): SourceLocateResult {
  const audio = previewContent.value as HTMLAudioElement | null;
  const loc = request.locators.find((l) => l.type === 'time' && (l.end_ms || 0) > (l.start_ms || 0));
  if (!audio || !loc) return NOT_FOUND;
  // Seek only; playback stays the reader's choice.
  const seek = () => {
    audio.currentTime = (loc.start_ms || 0) / 1000;
  };
  if (audio.readyState >= 1) {
    seek();
  } else {
    // A newer locate clears this, so an older citation cannot win the seek.
    audio.addEventListener('loadedmetadata', seek, { once: true });
    clearLocate.push(() => audio.removeEventListener('loadedmetadata', seek));
  }
  audioRange.value = `${formatClock(loc.start_ms || 0)} – ${formatClock(loc.end_ms || 0)}`;
  audioQuote.value = loc.quote || '';
  return { found: true, precise: true };
}

function applyLocate() {
  const request = props.locate;
  clearLocateMarks();
  // The pdf.js viewer locates on its own and reports through @located.
  if (!request || previewType.value === 'pdf') return;
  let result = NOT_FOUND;
  try {
    switch (previewType.value) {
      case 'docx': result = locateDocx(request); break;
      case 'pptx': result = locatePptx(request); break;
      case 'excel': result = locateExcel(request); break;
      case 'epub': result = locateEpub(request); break;
      case 'text':
      case 'html': result = locateText(request); break;
      case 'markdown': result = locateMarkdown(request); break;
      case 'image': result = locateImage(request); break;
      case 'audio': result = locateAudio(request); break;
    }
  } catch (err) {
    console.warn('Locating the citation failed:', err);
  }
  emit('located', result);
}

watch(
  () => [props.active, props.knowledgeId, props.sessionId, props.attachmentId, props.messageId, props.artifactIndex, props.sourceBlob, props.fileName, props.fileType],
  ([active]) => {
    if (active && getPreviewSourceKey()) {
      loadPreview();
    }
  },
  { immediate: true }
);

onUnmounted(() => {
  cleanup();
});
</script>

<template>
  <div ref="previewRoot" tabindex="-1" :aria-label="fileName" class="document-preview" :class="{ 'is-fullscreen': isFullscreen, 'fill-height': fillHeight }">
    <!-- Toolbar -->
    <Teleport :to="toolbarTarget || 'body'" :disabled="isFullscreen || !toolbarTarget">
      <div class="preview-toolbar" :class="{ 'is-inline': toolbarTarget && !isFullscreen }" v-if="isFullscreen || (!loading && !error && previewType !== 'unsupported')">
        <span v-if="isFullscreen" class="preview-toolbar-title" :title="fileName">{{ fileName }}</span>
        <div class="preview-toolbar-actions">
          <t-button
            v-if="previewType === 'html' && allowsHtmlScriptPreview()"
            theme="default" variant="text" size="small" shape="square"
            :title="htmlViewMode === 'render' ? $t('preview.htmlSource') : $t('preview.htmlRendered')"
            :aria-label="htmlViewMode === 'render' ? $t('preview.htmlSource') : $t('preview.htmlRendered')"
            @click="htmlViewMode = htmlViewMode === 'render' ? 'source' : 'render'"
          >
            <template #icon><t-icon :name="htmlViewMode === 'render' ? 'code' : 'browse'" /></template>
          </t-button>
          <t-button theme="default" variant="text" size="small" shape="square" :aria-pressed="isFullscreen"
            :title="isFullscreen ? $t('preview.exitFullscreen') : $t('preview.fullscreen')"
            :aria-label="isFullscreen ? $t('preview.exitFullscreen') : $t('preview.fullscreen')"
            @click="toggleFullscreen">
            <template #icon><t-icon :name="isFullscreen ? 'fullscreen-exit' : 'fullscreen'" /></template>
          </t-button>
        </div>
      </div>
    </Teleport>

    <!-- Loading -->
    <div v-if="loading" class="preview-loading">
      <t-loading size="medium" />
      <span class="loading-text">{{ $t('preview.loading') }}</span>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="preview-error">
      <t-icon name="error-circle" size="48px" />
      <p>{{ error }}</p>
      <t-button theme="primary" size="small" @click="loadedForId = ''; loadPreview()">
        {{ $t('preview.retry') }}
      </t-button>
    </div>

    <!-- Unsupported -->
    <div v-else-if="previewType === 'unsupported'" class="preview-unsupported">
      <t-icon name="file-unknown" size="48px" />
      <p>{{ $t('preview.unsupported') }}</p>
      <p class="unsupported-hint">{{ $t('preview.unsupportedHint') }}</p>
    </div>

    <!-- PDF -->
    <div v-else-if="previewType === 'pdf' && pdfData" class="preview-pdf preview-pdf--source">
      <PdfSourceViewer :data="pdfData" :file-name="fileName" :locate="locate" @located="(r) => emit('located', r)" />
    </div>
    <div v-else-if="previewType === 'pdf' && blobUrl" class="preview-pdf">
      <iframe ref="previewContent" tabindex="0" :title="fileName" :src="blobUrl" class="pdf-iframe" @load="onPreviewFrameLoad" />
    </div>

    <!-- HTML: artifacts render in a unique-origin iframe; other sources stay as source. -->
    <div v-else-if="previewType === 'html'" class="preview-html">
      <iframe
        v-if="allowsHtmlScriptPreview() && htmlViewMode === 'render' && blobUrl"
        ref="previewContent"
        tabindex="0"
        :title="fileName"
        :src="blobUrl"
        class="html-iframe"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
        @load="onPreviewFrameLoad"
      />
      <pre v-else-if="!allowsHtmlScriptPreview() || htmlViewMode === 'source'" ref="previewContent" tabindex="0" :aria-label="fileName" class="code-preview"><code class="hljs" v-html="highlightedCode"></code></pre>
    </div>

    <!-- Image -->
    <div v-else-if="previewType === 'image' && blobUrl" ref="previewContent" tabindex="0" :aria-label="fileName" class="preview-image">
      <div class="image-wrapper">
        <div class="image-frame">
          <img :src="blobUrl" :alt="fileName" @load="onImageLoad" />
          <div
            v-for="(mark, i) in imageMarks"
            :key="i"
            class="image-locate-mark"
            aria-hidden="true"
            :style="{ left: `${mark.left * 100}%`, top: `${mark.top * 100}%`, width: `${mark.width * 100}%`, height: `${mark.height * 100}%` }"
          />
        </div>
        <div v-if="imageNaturalWidth" class="image-info">
          {{ imageNaturalWidth }} × {{ imageNaturalHeight }} px
        </div>
      </div>
    </div>

    <!-- DOCX -->
    <div v-else-if="previewType === 'docx'" class="preview-docx">
      <div ref="docxContainer" tabindex="0" :aria-label="fileName" class="docx-container" />
    </div>

    <!-- PPTX -->
    <div v-else-if="previewType === 'pptx' && pptxData" ref="previewContent" tabindex="0" :aria-label="fileName" class="preview-pptx">
      <vue-office-pptx :key="loadedForId" :src="pptxData" @rendered="onPptxRendered" @error="(e: any) => { error = e?.message || $t('preview.loadFailed'); }" />
    </div>

    <!-- Excel -->
    <div v-else-if="previewType === 'excel' && excelHtml" class="preview-excel">
      <div ref="previewContent" tabindex="0" :aria-label="fileName" class="excel-container" v-html="excelHtml" />
    </div>

    <!-- EPUB -->
    <div v-else-if="previewType === 'epub' && epubHtml" ref="previewContent" tabindex="0" :aria-label="fileName" class="preview-markdown preview-epub">
      <div class="markdown-body" v-html="epubHtml" />
    </div>

    <!-- Markdown -->
    <div v-else-if="previewType === 'markdown' && markdownHtml" ref="previewContent" tabindex="0" :aria-label="fileName" class="preview-markdown">
      <div class="markdown-body" v-html="markdownHtml" />
    </div>

    <!-- Text / Code -->
    <div v-else-if="previewType === 'text' && highlightedCode" class="preview-text">
      <pre ref="previewContent" tabindex="0" :aria-label="fileName" class="code-preview"><code class="hljs" v-html="highlightedCode"></code></pre>
    </div>

    <!-- Mermaid -->
    <div v-else-if="previewType === 'mermaid' && mermaidSvg" ref="previewContent" tabindex="0" :aria-label="fileName" class="preview-mermaid" @click="openMermaid">
      <div class="mermaid-body" v-html="mermaidSvg" />
    </div>

    <!-- Audio -->
    <div v-else-if="previewType === 'audio' && blobUrl" class="preview-audio">
      <div class="audio-wrapper">
        <t-icon name="sound" size="48px" />
        <p class="audio-filename">{{ fileName }}</p>
        <audio ref="previewContent" controls :src="blobUrl" class="audio-element">
          {{ $t('preview.audioNotSupported') }}
        </audio>
        <div v-if="audioRange" class="audio-locate">
          <span class="audio-locate__time">{{ audioRange }}</span>
          <p v-if="audioQuote" class="audio-locate__quote">{{ audioQuote }}</p>
        </div>
      </div>
    </div>

    <!-- Video -->
    <div v-else-if="previewType === 'video' && blobUrl" class="preview-video">
      <video ref="previewContent" controls playsinline :src="blobUrl" class="video-element">
        {{ $t('preview.videoNotSupported') }}
      </video>
    </div>
  </div>
</template>

<style scoped lang="less">
// ── Design tokens ──
@border-color: var(--td-component-stroke);
@border-radius: var(--app-radius-sm);
@bg-white: var(--td-bg-color-container);
@bg-subtle: var(--td-bg-color-container);
@bg-muted: var(--td-bg-color-secondarycontainer);
@text-primary: var(--td-text-color-primary);
@text-secondary: var(--td-text-color-secondary);
@text-tertiary: var(--td-text-color-placeholder);
@text-disabled: var(--td-text-color-disabled);
@accent: var(--td-brand-color);
@accent-hover: var(--td-brand-color-active);
@accent-bg: var(--td-success-color-light);
@accent-bg-hover: var(--td-success-color-light);
@error-color: var(--td-error-color);
@table-border: var(--td-component-stroke);
@preview-max-h: calc(100vh - 200px);
// Note: <html> carries a `zoom` multiplier for font-size control, so 100vh
// is evaluated against the unscaled viewport and the resulting max-height
// may exceed the real viewport by the zoom factor (≤12.5% at "large").
// That produces an extra bit of scroll inside the non-fullscreen preview,
// which is acceptable for document reading. Not worth the complexity of
// inverse-scaling here.
@transition: all var(--app-motion-base) ease;

// ── Shared container mixin ──
.preview-container() {
  border: 1px solid @border-color;
  border-radius: @border-radius;
  overflow: auto;
  max-height: @preview-max-h;
  background: @bg-white;
}

.document-preview {
  min-height: 200px;
  position: relative;

  &.fill-height,
  &.is-fullscreen {
    height: 100%;
    min-height: 0;
    display: flex;
    flex-direction: column;

    .preview-pdf,
    .preview-pptx,
    .preview-docx,
    .preview-image,
    .preview-excel,
    .preview-markdown,
    .preview-text,
    .preview-html,
    .preview-mermaid,
    .preview-audio,
    .preview-video,
    .preview-loading,
    .preview-error,
    .preview-unsupported {
      flex: 1;
      min-height: 0;
      max-height: none;
    }

    .preview-pdf {
      height: auto;
      min-height: 0;
    }

    .preview-pptx {
      overflow: auto;
    }

    .preview-docx,
    .preview-excel,
    .preview-text {
      display: flex;
      flex-direction: column;

      .docx-container,
      .excel-container,
      .code-preview {
        flex: 1;
        min-height: 0;
        max-height: none;
        height: auto;
      }
    }

    .preview-image .image-wrapper img {
      max-height: 100%;
    }

    .preview-excel .excel-container,
    .preview-markdown,
    .preview-text .code-preview,
    .preview-html .code-preview {
      max-height: none;
    }

    .preview-html .html-iframe {
      height: 100%;
    }

    .preview-html {
      height: auto;
      min-height: 0;
    }
  }
}

.is-fullscreen {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 2001;
  background: var(--td-bg-color-container);
  padding: 0;
  overflow: hidden;

  .preview-toolbar {
    padding: 12px 16px;
    margin-bottom: 0;
  }

  .preview-pptx {
    height: auto;
    min-height: 0;
    overflow: auto;
    border: none;

    :deep(.pptx-preview-wrapper) {
      height: auto !important;
      overflow-y: visible !important;
    }
  }

  .preview-image {
    display: flex;
    justify-content: center;
    align-items: center;
  }
}

.preview-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
  min-width: 0;
  padding: 8px 0;
  margin-bottom: 8px;
  border-bottom: 1px solid @border-color;
  background: @bg-white;
}

.preview-toolbar-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: @text-primary;
  font-size: var(--app-text-base);
  font-weight: 500;
}

.preview-toolbar.is-inline {
  padding: 0;
  margin: 0;
  border: 0;
  background: transparent;
}

.preview-toolbar-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
  margin-left: auto;

  :deep(.t-button) {
    flex-shrink: 0;
    width: 30px;
    height: 30px;
    border-radius: 7px;
    color: @text-secondary;
  }

  :deep(.t-button__icon) {
    margin: 0;
    font-size: var(--app-text-xl);
  }
}

// ── States ──
.preview-loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  gap: 16px;
  .loading-text { color: @text-tertiary; font-size: var(--app-text-base); }
}

.preview-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  gap: 12px;
  color: @error-color;
  p { margin: 0; font-size: var(--app-text-base); color: @text-secondary; }
}

.preview-unsupported {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  gap: 12px;
  color: @text-disabled;
  p { margin: 0; font-size: var(--app-text-base); color: @text-secondary; }
  .unsupported-hint { font-size: var(--app-text-sm); color: @text-tertiary; }
}

// ── PDF ──
.preview-pdf {
  width: 100%;
  height: @preview-max-h;
  min-height: 500px;
  .pdf-iframe {
    width: 100%;
    height: 100%;
    border: none;
    border-radius: @border-radius;
  }
}

// ── HTML ──
.preview-html {
  width: 100%;
  height: @preview-max-h;
  min-height: 420px;
  display: flex;
  flex-direction: column;
  .html-iframe {
    flex: 1;
    width: 100%;
    min-height: 0;
    border: 1px solid @border-color;
    border-radius: @border-radius;
    background: @bg-white;
  }
  .code-preview {
    .preview-container();
    flex: 1;
    min-height: 0;
    margin: 0;
    padding: 16px;
    background: @bg-subtle;
    font-size: var(--app-text-md);
    line-height: 1.6;
    code {
      white-space: pre;
      word-wrap: normal;
      display: block;
      background: transparent;
    }
  }
}

// ── Mermaid ──
.preview-mermaid {
  .preview-container();
  display: flex;
  justify-content: center;
  padding: 24px 16px;
  cursor: zoom-in;
  .mermaid-body {
    max-width: 100%;
    :deep(svg) {
      max-width: 100%;
      height: auto;
    }
  }
}

// ── PDF (pdf.js, source mode) ──
.preview-pdf--source {
  height: @preview-max-h;
  border: 1px solid @border-color;
  border-radius: @border-radius;
  overflow: hidden;
}

// ── Image ──
.image-frame {
  position: relative;
  display: inline-block;
  line-height: 0;
}

.image-locate-mark {
  position: absolute;
  pointer-events: none;
  border-radius: var(--app-radius-xs);
  background: color-mix(in srgb, var(--td-success-color) 22%, transparent);
  outline: 1px solid color-mix(in srgb, var(--td-success-color) 50%, transparent);
}

.preview-image {
  overflow: auto;
  display: flex;
  justify-content: center;
  padding: 20px 0;
  .image-wrapper {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
    img {
      max-width: 100%;
      max-height: calc(100vh - 280px);
      border-radius: @border-radius;
      box-shadow: 0 2px 12px color-mix(in srgb, var(--td-brand-color) 8%, transparent);
      object-fit: contain;
    }
    .image-info { font-size: var(--app-text-sm); color: @text-tertiary; }
  }
}

// ── Markdown ──
.preview-markdown {
  .preview-container();
  padding: 20px 24px;
}

// ── DOCX ──
.preview-docx {
  .docx-container { .preview-container(); }
}

// ── PPTX ──
.preview-pptx {
  max-height: @preview-max-h;
  min-height: 500px;
  border: 1px solid @border-color;
  border-radius: @border-radius;
  overflow: auto;
  background: @bg-subtle;

  :deep(.pptx-preview-wrapper) {
    height: auto !important;
    overflow-y: visible !important;
  }
}

// ── Excel ──
.preview-excel {
  .excel-container { .preview-container(); }
}

// ── Text / Code ──
.preview-text {
  .code-preview {
    .preview-container();
    margin: 0;
    padding: 16px;
    background: @bg-subtle;
    font-size: var(--app-text-md);
    line-height: 1.6;
    code {
      white-space: pre;
      word-wrap: normal;
      display: block;
      background: transparent;
    }
  }
}

// ── Audio ──
.preview-audio {
  display: flex;
  justify-content: center;
  padding: 40px 20px;
  .audio-wrapper {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 16px;
    color: @text-secondary;
    .audio-filename { font-size: var(--app-text-base); color: @text-primary; margin: 0; }
    .audio-element { width: 100%; max-width: 480px; }
  }
  .audio-locate {
    width: 100%;
    max-width: 480px;
    padding: 10px 12px;
    border-radius: @border-radius;
    background: color-mix(in srgb, var(--td-success-color) 10%, transparent);
    color: @text-primary;
    &__time {
      font-size: var(--app-text-sm);
      font-variant-numeric: tabular-nums;
      color: @text-secondary;
    }
    &__quote {
      margin: 4px 0 0;
      font-size: var(--app-text-md);
      line-height: 1.6;
    }
  }
}

// ── Video ──
.preview-video {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 16px;
  min-height: 280px;
  .video-element {
    width: 100%;
    max-height: calc(100vh - 240px);
    border-radius: @border-radius;
    background: #000;
  }
}

// ── Deep styles (v-html / third-party components) ──

// Shared table mixin for v-html content
.preview-table(@header-bg: @accent-bg; @hover-bg: @accent-bg) {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--app-text-md);
  th, td {
    border: 1px solid @table-border;
    padding: 6px 12px;
    text-align: left;
  }
  th {
    background: @header-bg;
    font-weight: 600;
    color: @text-primary;
  }
  tr:hover td {
    background: @hover-bg;
    transition: @transition;
  }
}

:deep(.markdown-body) {
  font-size: var(--app-text-base);
  line-height: 1.7;
  color: @text-primary;
  word-break: break-word;

  h1, h2, h3, h4, h5, h6 {
    margin-top: 20px;
    margin-bottom: 10px;
    font-weight: 600;
    line-height: 1.4;
  }
  h1 { font-size: var(--app-text-4xl); border-bottom: 1px solid @border-color; padding-bottom: 8px; }
  h2 { font-size: var(--app-text-3xl); border-bottom: 1px solid @border-color; padding-bottom: 6px; }
  h3 { font-size: 17px; }

  p { margin: 8px 0; }
  blockquote {
    margin: 12px 0;
    padding: 8px 16px;
    border-left: 4px solid @border-color;
    background: @bg-subtle;
    color: var(--td-text-color-secondary);
  }
  ul, ol { padding-left: 24px; margin: 8px 0; }
  li { margin: 4px 0; }

  table {
    .preview-table(@bg-muted; var(--td-bg-color-container-hover));
    margin: 12px 0;
  }

  pre {
    margin: 12px 0;
    padding: 14px;
    background: @bg-subtle;
    border-radius: @border-radius;
    overflow: auto;
    font-size: var(--app-text-md);
    line-height: 1.5;
    code { background: transparent; padding: 0; }
  }
  code {
    background: var(--td-bg-color-secondarycontainer);
    padding: 2px 6px;
    border-radius: 3px;
    font-size: 0.9em;
  }
  img { max-width: 100%; border-radius: var(--app-radius-xs); }
  hr { border: none; border-top: 1px solid @border-color; margin: 20px 0; }
  a { color: @accent; text-decoration: none; &:hover { color: @accent-hover; text-decoration: underline; } }
  strong { font-weight: 600; }
}

:deep(.docx-preview-wrapper) {
  padding: 20px;
  max-width: 100%;
  width: 100%;
  box-sizing: border-box;
  overflow-x: auto; // 如果内容过宽，允许水平滚动而不是溢出
  
  // 约束所有子元素的宽度
  * {
    max-width: 100%;
    box-sizing: border-box;
  }
  
  // 特别处理表格
  table {
    width: 100%;
    table-layout: auto;
    word-wrap: break-word;
  }
  
  // 处理图片
  img {
    max-width: 100%;
    height: auto;
  }
  
  // 处理可能的固定宽度元素
  [style*="width"] {
    max-width: 100% !important;
  }
}

:deep(.vue-office-pptx) {
  width: 100%;
  min-height: 100%;
}

:deep(.vue-office-pptx-main) {
  width: 100%;
  min-height: 100%;
}

:deep(.excel-sheet) {
  padding: 0;
  .excel-sheet-name {
    position: sticky;
    top: 0;
    background: @accent-bg;
    padding: 8px 16px;
    font-weight: 600;
    font-size: var(--app-text-md);
    color: @text-primary;
    border-bottom: 1px solid @border-color;
    z-index: 1;
  }
  table {
    .preview-table();
    th, td {
      white-space: nowrap;
      max-width: 300px;
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }
}
</style>

<!-- highlight.js github.css is a light theme imported globally; its token
     colors (notably the base #24292e) become unreadable on the dark
     container background used in dark mode. Remap the palette to the
     github-dark colors when dark mode is active. Non-scoped on purpose so
     it covers every hljs block (txt/code preview, markdown code fences). -->
<style lang="less">
html[theme-mode="dark"] {
  .hljs {
    color: #c9d1d9;
    background: transparent;
  }
  .hljs-doctag,
  .hljs-keyword,
  .hljs-meta .hljs-keyword,
  .hljs-template-tag,
  .hljs-template-variable,
  .hljs-type,
  .hljs-variable.language_ {
    color: #ff7b72;
  }
  .hljs-title,
  .hljs-title.class_,
  .hljs-title.class_.inherited__,
  .hljs-title.function_ {
    color: #d2a8ff;
  }
  .hljs-attr,
  .hljs-attribute,
  .hljs-literal,
  .hljs-meta,
  .hljs-number,
  .hljs-operator,
  .hljs-variable,
  .hljs-selector-attr,
  .hljs-selector-class,
  .hljs-selector-id {
    color: #79c0ff;
  }
  .hljs-regexp,
  .hljs-string,
  .hljs-meta .hljs-string {
    color: #a5d6ff;
  }
  .hljs-built_in,
  .hljs-symbol {
    color: #ffa657;
  }
  .hljs-comment,
  .hljs-code,
  .hljs-formula {
    color: #8b949e;
  }
  .hljs-name,
  .hljs-quote,
  .hljs-selector-tag,
  .hljs-selector-pseudo {
    color: #7ee787;
  }
  .hljs-subst {
    color: #c9d1d9;
  }
  .hljs-section {
    color: #1f6feb;
    font-weight: bold;
  }
  .hljs-bullet {
    color: #f2cc60;
  }
  .hljs-emphasis {
    color: #c9d1d9;
    font-style: italic;
  }
  .hljs-strong {
    color: #c9d1d9;
    font-weight: bold;
  }
  .hljs-addition {
    color: #aff5b4;
    background-color: #033a16;
  }
  .hljs-deletion {
    color: #ffdcd7;
    background-color: #67060c;
  }
}
</style>

<style lang="less">
/* Citation highlights live outside the scoped block: they apply to DOM that
   third-party renderers (docx-preview, pptx-preview, SheetJS) create. */
::highlight(source-locate) {
  background-color: color-mix(in srgb, var(--app-source-highlight) 60%, transparent);
}

mark.source-locate-mark {
  background-color: color-mix(in srgb, var(--app-source-highlight) 60%, transparent);
  color: inherit;
}

.source-locate-block {
  background-color: color-mix(in srgb, var(--app-source-highlight) 22%, transparent) !important;
  outline: 1px solid var(--app-source-highlight);
  outline-offset: 2px;
  border-radius: var(--app-radius-xs);
}

tr.source-locate-block > td {
  background-color: color-mix(in srgb, var(--app-source-highlight) 45%, transparent) !important;
}
</style>
