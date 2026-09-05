// @ts-nocheck
<script setup lang="ts">
import { ref, shallowRef, watch, onUnmounted, nextTick, defineAsyncComponent } from 'vue';
import { previewKnowledgeFile } from '@/api/knowledge-base/index';
import { previewTemporaryAttachment } from '@/api/chat/temporary-attachments';
import { downloadArtifact } from '@/api/chat';
import hljs from 'highlight.js';
import 'highlight.js/styles/github.css';
import markedKatex from 'marked-katex-extension';
import 'katex/dist/katex.min.css';
import { useI18n } from 'vue-i18n';
import { sanitizeHTML, sanitizeMarkdownHTML, safeMarkdownToHTML } from '@/utils/security';
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


const VueOfficePptx = defineAsyncComponent(() => import('@vue-office/pptx'));

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
const docxContainer = ref<HTMLElement | null>(null);
const imageNaturalWidth = ref(0);
const imageNaturalHeight = ref(0);
let loadedForId = '';

const isFullscreen = ref(false);

function toggleFullscreen() {
  isFullscreen.value = !isFullscreen.value;
  if (isFullscreen.value) {
    document.body.style.overflow = 'hidden';
  } else {
    document.body.style.overflow = '';
  }
}


function ensureBlobType(blob: Blob, ft: string): Blob {
  const expected = getPreviewMimeType(ft);
  if (blob.type === expected) return blob;
  return new Blob([blob], { type: expected });
}

function getHighlightLang(ft: string): string {
  return resolveHighlightLang(ft);
}

const preprocessMathDelimiters = (rawText: string): string => {
  if (!rawText || typeof rawText !== 'string') {
    return '';
  }
  return rawText
    .replace(/\\\[([\s\S]*?)\\\]/g, '$$$$$1$$$$')
    .replace(/\\\(([\s\S]*?)\\\)/g, '$$$1$$');
};

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
  const { marked } = await import('marked');
  const text = await blob.text();

  // 校验文本内容是否有效
  if (!text || typeof text !== 'string') {
    markdownHtml.value = '<p style="color: var(--td-text-color-disabled); text-align: center; padding: 20px;">文档内容为空</p>';
    return;
  }

  marked.use({
    breaks: true,
    gfm: true,
  });
  marked.use(markedKatex({ throwOnError: false, nonStandard: true }));
  const renderer = new marked.Renderer();
  renderer.code = function ({text, lang}) {
    // 空值校验：防止 text 为 undefined 或 null
    if (!text || typeof text !== 'string') {
      text = '';
    }

    let highlighted = '';
    if (lang && hljs.getLanguage(lang)) {
      try { highlighted = hljs.highlight(text, { language: lang }).value; }
      catch { highlighted = hljs.highlightAuto(text).value; }
    } else {
      highlighted = hljs.highlightAuto(text).value;
    }
    return `<pre><code class="hljs">${highlighted}</code></pre>`;
  };
  const mathSafeText = preprocessMathDelimiters(text);
  const safeText = safeMarkdownToHTML(mathSafeText);
  // Keep this renderer local. `marked.use` mutates a shared singleton and
  // would otherwise inherit renderers installed by the chunk-content view.
  const rawHtml = marked.parse(safeText, { renderer }) as string;
  markdownHtml.value = sanitizeHTML(rawHtml);
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

function openMermaid() {
  if (mermaidSvg.value) openMermaidFullscreen(mermaidSvg.value);
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

    switch (kind) {
      case 'pdf':
      case 'image':
      case 'audio':
      case 'video': {
        blobUrl.value = URL.createObjectURL(blob);
        break;
      }
      case 'html': {
        if (allowsHtmlScriptPreview()) {
          blobUrl.value = URL.createObjectURL(blob);
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
        pptxData.value = await blob.arrayBuffer();
        break;
      }
      case 'mermaid': {
        await renderMermaid(blob);
        break;
      }
    }
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
  imageNaturalWidth.value = 0;
  imageNaturalHeight.value = 0;
  loadedForId = '';
  if (docxContainer.value) {
    docxContainer.value.innerHTML = '';
  }
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
  document.body.style.overflow = '';
  cleanup();
});
</script>

<template>
  <div class="document-preview" :class="{ 'is-fullscreen': isFullscreen, 'fill-height': fillHeight }">
    <!-- Toolbar -->
    <div class="preview-toolbar" v-if="!loading && !error && previewType !== 'unsupported'">
      <t-space size="small">
        <t-tooltip
          v-if="previewType === 'html' && allowsHtmlScriptPreview()"
          :content="htmlViewMode === 'render' ? $t('preview.htmlSource') : $t('preview.htmlRendered')"
          placement="bottom"
        >
          <t-button theme="default" variant="text" shape="square" @click="htmlViewMode = htmlViewMode === 'render' ? 'source' : 'render'">
            <template #icon><t-icon :name="htmlViewMode === 'render' ? 'code' : 'browse'" /></template>
          </t-button>
        </t-tooltip>
        <t-tooltip :content="isFullscreen ? $t('preview.exitFullscreen') : $t('preview.fullscreen')" placement="bottom">
          <t-button theme="default" variant="text" shape="square" @click="toggleFullscreen">
            <template #icon><t-icon :name="isFullscreen ? 'fullscreen-exit' : 'fullscreen'" /></template>
          </t-button>
        </t-tooltip>
      </t-space>
    </div>

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
    <div v-else-if="previewType === 'pdf' && blobUrl" class="preview-pdf">
      <iframe :src="blobUrl" class="pdf-iframe" />
    </div>

    <!-- HTML: artifacts render in a unique-origin iframe; other sources stay as source. -->
    <div v-else-if="previewType === 'html'" class="preview-html">
      <iframe
        v-if="allowsHtmlScriptPreview() && htmlViewMode === 'render' && blobUrl"
        :src="blobUrl"
        class="html-iframe"
        sandbox="allow-scripts"
        referrerpolicy="no-referrer"
      />
      <pre v-show="!allowsHtmlScriptPreview() || htmlViewMode === 'source'" class="code-preview"><code class="hljs" v-html="highlightedCode"></code></pre>
    </div>

    <!-- Image -->
    <div v-else-if="previewType === 'image' && blobUrl" class="preview-image">
      <div class="image-wrapper">
        <img :src="blobUrl" :alt="fileName" @load="onImageLoad" />
        <div v-if="imageNaturalWidth" class="image-info">
          {{ imageNaturalWidth }} × {{ imageNaturalHeight }} px
        </div>
      </div>
    </div>

    <!-- DOCX -->
    <div v-else-if="previewType === 'docx'" class="preview-docx">
      <div ref="docxContainer" class="docx-container" />
    </div>

    <!-- PPTX -->
    <div v-else-if="previewType === 'pptx' && pptxData" class="preview-pptx">
      <vue-office-pptx :src="pptxData" @rendered="() => {}" @error="(e: any) => { error = e?.message || $t('preview.loadFailed'); }" />
    </div>

    <!-- Excel -->
    <div v-else-if="previewType === 'excel' && excelHtml" class="preview-excel">
      <div class="excel-container" v-html="excelHtml" />
    </div>

    <!-- Markdown -->
    <div v-else-if="previewType === 'markdown' && markdownHtml" class="preview-markdown">
      <div class="markdown-body" v-html="markdownHtml" />
    </div>

    <!-- Text / Code -->
    <div v-else-if="previewType === 'text' && highlightedCode" class="preview-text">
      <pre class="code-preview"><code class="hljs" v-html="highlightedCode"></code></pre>
    </div>

    <!-- Mermaid -->
    <div v-else-if="previewType === 'mermaid' && mermaidSvg" class="preview-mermaid" @click="openMermaid">
      <div class="mermaid-body" v-html="mermaidSvg" />
    </div>

    <!-- Audio -->
    <div v-else-if="previewType === 'audio' && blobUrl" class="preview-audio">
      <div class="audio-wrapper">
        <t-icon name="sound" size="48px" />
        <p class="audio-filename">{{ fileName }}</p>
        <audio controls :src="blobUrl" class="audio-element">
          {{ $t('preview.audioNotSupported') }}
        </audio>
      </div>
    </div>

    <!-- Video -->
    <div v-else-if="previewType === 'video' && blobUrl" class="preview-video">
      <video controls playsinline :src="blobUrl" class="video-element">
        {{ $t('preview.videoNotSupported') }}
      </video>
    </div>
  </div>
</template>

<style scoped lang="less">
// ── Design tokens ──
@border-color: var(--td-component-stroke);
@border-radius: 6px;
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
@transition: all 0.2s ease;

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

  &.fill-height {
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

    .preview-docx {
      display: flex;
      flex-direction: column;

      .docx-container {
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
  overflow-y: auto;

  .preview-toolbar {
    position: fixed;
    top: 12px;
    right: 32px;
    z-index: 2002;
  }

  /* Children use height: 100% rather than 100vh because <html> carries a
     `zoom` multiplier for font-size control; 100vh resolves against the
     unscaled viewport and then gets scaled, overshooting the screen. The
     fullscreen container is already inset 0 on all sides, so 100% resolves
     to the true viewport height. */
  .preview-pdf {
    height: 100%;
  }

  .preview-pptx {
    height: auto;
    min-height: 100%;
    overflow: visible;
    border: none;

    :deep(.pptx-preview-wrapper) {
      height: auto !important;
      overflow-y: visible !important;
    }
  }

  .preview-docx {
    height: 100%;
    display: flex;
    flex-direction: column;
    .docx-container {
      max-height: 100%;
      height: 100%;
      flex: 1;
    }
  }

  .preview-image {
    min-height: 100%;
    display: flex;
    justify-content: center;
    align-items: center;
    .image-wrapper img {
      max-height: calc(100% - 80px);
    }
  }

  .preview-excel .excel-container,
  .preview-markdown,
  .preview-text .code-preview,
  .preview-html .code-preview {
    max-height: 100%;
  }

  .preview-html,
  .preview-video,
  .preview-mermaid {
    height: 100%;
  }
}

.preview-toolbar {
  position: absolute;
  top: 8px;
  right: 24px;
  z-index: 10;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-border);
  border-radius: var(--td-radius-default);
  box-shadow: var(--td-shadow-1);
  padding: 4px;
  opacity: 0.6;
  transition: opacity 0.2s;

  &:hover {
    opacity: 1;
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
  .loading-text { color: @text-tertiary; font-size: 14px; }
}

.preview-error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  gap: 12px;
  color: @error-color;
  p { margin: 0; font-size: 14px; color: @text-secondary; }
}

.preview-unsupported {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  gap: 12px;
  color: @text-disabled;
  p { margin: 0; font-size: 14px; color: @text-secondary; }
  .unsupported-hint { font-size: 12px; color: @text-tertiary; }
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
    font-size: 13px;
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

// ── Image ──
.preview-image {
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
      box-shadow: 0 2px 12px rgba(7, 192, 95, 0.08);
      object-fit: contain;
    }
    .image-info { font-size: 12px; color: @text-tertiary; }
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
    font-size: 13px;
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
    .audio-filename { font-size: 14px; color: @text-primary; margin: 0; }
    .audio-element { width: 100%; max-width: 480px; }
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
.preview-table() {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  th, td {
    border: 1px solid @table-border;
    padding: 6px 12px;
    text-align: left;
  }
  th {
    background: @accent-bg;
    font-weight: 600;
    color: @text-primary;
  }
  tr:hover td {
    background: @accent-bg;
    transition: @transition;
  }
}

:deep(.markdown-body) {
  font-size: 14px;
  line-height: 1.7;
  color: @text-primary;
  word-break: break-word;

  h1, h2, h3, h4, h5, h6 {
    margin-top: 20px;
    margin-bottom: 10px;
    font-weight: 600;
    line-height: 1.4;
  }
  h1 { font-size: 24px; border-bottom: 1px solid @border-color; padding-bottom: 8px; }
  h2 { font-size: 20px; border-bottom: 1px solid @border-color; padding-bottom: 6px; }
  h3 { font-size: 17px; }

  p { margin: 8px 0; }
  blockquote {
    margin: 12px 0;
    padding: 8px 16px;
    border-left: 4px solid @accent;
    background: @bg-subtle;
    color: var(--td-text-color-secondary);
  }
  ul, ol { padding-left: 24px; margin: 8px 0; }
  li { margin: 4px 0; }

  table { .preview-table(); margin: 12px 0; }

  pre {
    margin: 12px 0;
    padding: 14px;
    background: @bg-subtle;
    border-radius: @border-radius;
    overflow: auto;
    font-size: 13px;
    line-height: 1.5;
    code { background: transparent; padding: 0; }
  }
  code {
    background: var(--td-bg-color-secondarycontainer);
    padding: 2px 6px;
    border-radius: 3px;
    font-size: 0.9em;
  }
  img { max-width: 100%; border-radius: 4px; }
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
    font-size: 13px;
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
