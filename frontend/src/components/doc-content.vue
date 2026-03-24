// @ts-nocheck
<script setup lang="ts">
import { marked } from "marked";
import type { Tokens } from 'marked';
import hljs from "highlight.js";
import "highlight.js/styles/github.css";
import mermaid from "mermaid";
import { onMounted, ref, nextTick, onUnmounted, watch } from "vue";
import { downKnowledgeDetails, deleteGeneratedQuestion, getChunkByIdOnly } from "@/api/knowledge-base/index";
import { MessagePlugin, DialogPlugin } from "tdesign-vue-next";
import { sanitizeHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL, hydrateProtectedFileImages } from '@/utils/security';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';
import { useI18n } from 'vue-i18n';
import DocumentPreview from '@/components/document-preview.vue';

const { t } = useI18n();

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
const props = defineProps(["visible", "details", "knowledgeType", "sourceInfo"]);
const emit = defineEmits(["closeDoc", "getDoc", "questionDeleted"]);

marked.use({
  breaks: true,      // 启用单行换行转 <br>
  gfm: true,         // 启用 GitHub Flavored Markdown
});
const renderer = new marked.Renderer();
let page = 1;
let loadingChunks = false;
let pendingRequestedPage: number | null = null;
let pendingChunksBeforeLoad = 0;
let doc = null;
let down = ref()
let mdContentWrap = ref()
let url = ref('')
// 视图模式：chunks / merged / preview
// file 类型默认「预览」，URL / 手动创建 默认「全文」
const viewMode = ref<'chunks' | 'merged' | 'preview'>('merged');

// 合并后的文档内容
const mergedContent = ref<string>('');

/**
 * 根据 start_at 和 end_at 字段合并有 overlap 的 chunks
 * 返回合并后的完整文档内容
 * 实现逻辑与后端 Go 代码保持一致
 */
const mergeChunks = (chunks: any[]): string => {
  if (!chunks || chunks.length === 0) return '';
  
  // 按 start_at 排序
  const sortedChunks = [...chunks].sort((a, b) => {
    const startA = a.start_at ?? a.chunk_index ?? 0;
    const startB = b.start_at ?? b.chunk_index ?? 0;
    return startA - startB;
  });
  
  // 初始化合并结果，第一个 chunk 直接加入
  const mergedChunks: Array<{
    content: string;
    start_at: number;
    end_at: number;
  }> = [{
    content: sortedChunks[0].content || '',
    start_at: sortedChunks[0].start_at ?? 0,
    end_at: sortedChunks[0].end_at ?? 0
  }];
  
  // 从第二个 chunk 开始遍历
  for (let i = 1; i < sortedChunks.length; i++) {
    const currentChunk = sortedChunks[i];
    const lastChunk = mergedChunks[mergedChunks.length - 1];
    
    const currentStartAt = currentChunk.start_at ?? 0;
    const currentEndAt = currentChunk.end_at ?? 0;
    const currentContent = currentChunk.content || '';
    
    // 如果当前 chunk 的起始位置在最后一个 chunk 的结束位置之后，直接添加
    if (currentStartAt > lastChunk.end_at) {
      mergedChunks.push({
        content: currentContent,
        start_at: currentStartAt,
        end_at: currentEndAt
      });
      continue;
    }
    
    // 合并重叠的 chunks
    if (currentEndAt > lastChunk.end_at) {
      // 将内容转换为字符数组以正确处理多字节字符
      const contentRunes = Array.from(currentContent);
      const contentLength = contentRunes.length;
      
      // 计算偏移量：内容长度 - (当前结束位置 - 上一个结束位置)
      const offset = contentLength - (currentEndAt - lastChunk.end_at);
      
      // 拼接非重叠部分
      const newContent = contentRunes.slice(offset).join('');
      lastChunk.content = lastChunk.content + newContent;
      lastChunk.end_at = currentEndAt;
    }
  }
  
  // 合并所有段落，用双换行符连接
  return mergedChunks.map(chunk => chunk.content).join('\n\n');
};

onMounted(() => {
  nextTick(() => {
    doc = document.getElementsByClassName('t-drawer__body')[0]
    doc.addEventListener('scroll', handleDetailsScroll);
  })
})
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
  }
});
onUnmounted(() => {
  doc.removeEventListener('scroll', handleDetailsScroll);
})
const checkImage = (url) => {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = url;
  });
};
renderer.image = function ({href, title, text}: Tokens.Image) {
  // 安全地处理图片链接
  if (!isValidImageURL(href)) {
    return `<p>${t('error.invalidImageLink')}</p>`;
  }
  
  // 使用安全的图片创建函数
  const safeImage = createSafeImage(href, text || '', title || '');
  return `<figure>
                ${safeImage}
                <figcaption style="text-align: left;">${text || ''}</figcaption>
            </figure>`;
};

// 自定义代码块渲染器，只显示语言标签
renderer.code = function ({text, lang}: Tokens.Code) {
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
// 监听 chunks 变化，自动更新合并内容
watch(() => props.details?.md, (newChunks) => {
  if (newChunks && newChunks.length > 0) {
    mergedContent.value = mergeChunks(newChunks);
  } else {
    mergedContent.value = '';
  }
}, { immediate: true, deep: true });

const previewSupportedTypes = new Set([
  'pdf', 'docx', 'pptx', 'ppt', 'xlsx', 'xls', 'csv',
  'jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tiff', 'svg',
  'txt', 'md', 'markdown', 'json', 'xml', 'html', 'css', 'js', 'ts',
  'py', 'java', 'go', 'cpp', 'c', 'h', 'sh', 'yaml', 'yml',
  'ini', 'conf', 'log', 'sql', 'rs', 'rb', 'php', 'swift', 'kt',
  'scala', 'r', 'lua', 'pl', 'toml',
]);

const canPreview = (): boolean => {
  if (props.details?.type !== 'file') return false;
  const ft = props.details?.file_type?.toLowerCase();
  return !!ft && previewSupportedTypes.has(ft);
};

// 当文档详情加载完成时，file 类型自动切换到「预览」
watch(() => props.details?.id, (newId) => {
  if (!newId) return;
  if (props.details?.type === 'file' && canPreview()) {
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
const runMarkdownPostRenderPipeline = async () => {
  await nextTick();
  const renderRoot = mdContentWrap.value as ParentNode;
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

watch(() => props.details.md, (newVal) => {
  runMarkdownPostRenderPipeline();
}, { immediate: true, deep: true })

watch(() => viewMode.value, (mode) => {
  if ((mode === 'chunks' || mode === 'merged') && props.visible) {
    runMarkdownPostRenderPipeline();
  }
});

watch(() => props.visible, (visible) => {
  if (visible && (viewMode.value === 'chunks' || viewMode.value === 'merged')) {
    runMarkdownPostRenderPipeline();
  }
});

// 渲染 Mermaid 图表的函数
const renderMermaidDiagrams = async () => {
  try {
    const mermaidElements = mdContentWrap.value?.querySelectorAll('.mermaid');
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
  if (!mdContentWrap.value) {
    console.log('[Mermaid] mdContentWrap is null');
    return;
  }
  // 绑定在 .mermaid div 上，而不是 SVG 上
  const mermaidDivs = mdContentWrap.value.querySelectorAll('.mermaid');
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

  // 先还原原始文本中的 HTML 实体，让它们作为普通字符参与渲染
  let processedText = markdownText
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

  // 保留表格单元格中的 <br>，不转成换行，避免打散表格；其他区域原样交给 marked 处理

  // 安全预处理
  const safeMarkdown = safeMarkdownToHTML(processedText);

  // 使用标记渲染
  marked.use({ renderer });
  let html = marked.parse(safeMarkdown);

  // 还原被转义的 <br>
  html = html.replace(/&lt;br\s*\/?&gt;/gi, '<br>');

  // 最终安全清理
  let result = sanitizeHTML(html);
  
  return result;
};
const handleClose = () => {
  emit("closeDoc", false);
  doc.scrollTop = 0;
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
const handleDetailsScroll = () => {
  if (doc && !loadingChunks) {
    let pageNum = Math.ceil(props.details.total / 25);
    const { scrollTop, scrollHeight, clientHeight } = doc;
    if (scrollTop + clientHeight >= scrollHeight - 8) {
      if (props.details.md.length < props.details.total && page + 1 <= pageNum) {
        page++;
        loadingChunks = true;
        pendingRequestedPage = page;
        pendingChunksBeforeLoad = props.details.md.length;
        emit("getDoc", page);
      }
    }
  }
};
</script>
<template>
  <div class="doc_content" ref="mdContentWrap">
    <t-drawer :visible="visible" :zIndex="2000" :closeBtn="true" @close="handleClose">
      <template #header>
        <div class="drawer-header">
          <span class="header-title">{{ getDisplayTitle() }}</span>
          <t-tag v-if="details.type" size="small" :theme="getTypeTheme()" variant="light">
            {{ getTypeLabel() }}
          </t-tag>
        </div>
      </template>
      
      <!-- 文件类型专属区域 -->
      <div v-if="details.type === 'file'" class="doc_box">
        <a :href="url" style="display: none" ref="down" :download="details.title"></a>
        <span class="label">{{ $t('knowledgeBase.fileName') }}</span>
        <div class="download_box">
          <span class="doc_t">{{ details.title }}</span>
          <div class="icon_box" @click="downloadFile()" aria-label="Download">
            <img class="download_box" src="@/assets/img/download.svg" alt="">
          </div>
        </div>
      </div>
      
      <!-- URL类型专属区域 -->
      <div v-else-if="details.type === 'url'" class="url_box">
        <span class="label">{{ $t('knowledgeBase.urlSource') }}</span>
        <div class="url_link_box">
          <a :href="details.source" target="_blank" class="url_link">
            <t-icon name="link" size="14px" />
            <span class="url_text">{{ details.source }}</span>
            <t-icon name="jump" size="14px" class="jump-icon" />
          </a>
        </div>
      </div>
      
      <!-- 手动创建类型专属区域 -->
      <div v-else-if="details.type === 'manual'" class="manual_box">
        <span class="label">{{ $t('knowledgeBase.documentTitle') }}</span>
        <div class="download_box">
          <div class="manual_title_box">
            <span class="manual_title">{{ details.title }}</span>
          </div>
          <div class="icon_box" @click="downloadFile()" aria-label="Download">
            <img class="download_box" src="@/assets/img/download.svg" alt="">
          </div>
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
            <span class="time"> {{ getTimeLabel() }}：{{ details.time }} </span>
            <div class="view-mode-buttons">
              <t-button 
                v-if="canPreview()"
                size="small" 
                :variant="viewMode === 'preview' ? 'base' : 'outline'" 
                :theme="viewMode === 'preview' ? 'primary' : 'default'"
                @click="viewMode = 'preview'"
                class="view-mode-btn"
              >
                {{ $t('preview.tab') }}
              </t-button>
              <t-button 
                v-if="!canPreview()"
                size="small" 
                :variant="viewMode === 'merged' ? 'base' : 'outline'" 
                :theme="viewMode === 'merged' ? 'primary' : 'default'"
                @click="viewMode = 'merged'"
                class="view-mode-btn"
              >
                {{ $t('knowledgeBase.viewMerged') }}
              </t-button>
              <t-button 
                size="small" 
                :variant="viewMode === 'chunks' ? 'base' : 'outline'" 
                :theme="viewMode === 'chunks' ? 'primary' : 'default'"
                @click="viewMode = 'chunks'"
                class="view-mode-btn"
              >
                {{ $t('knowledgeBase.viewChunks') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>
      
      <!-- 合并视图 -->
      <div v-if="viewMode === 'merged'">
        <div v-if="!mergedContent" class="no_content">{{ $t('common.noData') }}</div>
        <div v-else class="md-content" v-html="processMarkdown(mergedContent)"></div>
      </div>
      
      <!-- 分块视图 -->
      <div v-else-if="viewMode === 'chunks'">
        <div v-if="details.md.length == 0" class="no_content">{{ $t('common.noData') }}</div>
        <div v-else class="chunk-list">
          <div class="chunk-item" 
            v-for="(item, index) in details.md" 
            :key="index"
            :class="getChunkClass(index)"
          >
            <div class="chunk-header">
              <span class="chunk-index">{{ $t('knowledgeBase.segment') }} {{ index + 1 }}</span>
              <div class="chunk-header-right">
                <t-tag 
                  v-if="hasParentChunk(item)" 
                  size="small" 
                  theme="primary" 
                  variant="light"
                >
                  {{ $t('knowledgeBase.childChunk') }}
                </t-tag>
                <t-tag 
                  v-if="getGeneratedQuestions(item).length > 0" 
                  size="small" 
                  theme="success" 
                  variant="light"
                >
                  {{ $t('knowledgeBase.questions') }} {{ getGeneratedQuestions(item).length }}
                </t-tag>
                <span class="chunk-meta">{{ getChunkMeta(item) }}</span>
              </div>
            </div>
            <div class="md-content" v-html="processMarkdown(item.content)"></div>
            
            <!-- 父 Chunk 上下文展开 -->
            <div v-if="hasParentChunk(item)" class="parent-context-section">
              <div class="parent-context-toggle" @click="toggleParentContext(item, index)">
                <t-icon v-if="!parentContextLoading.has(index)" :name="isParentExpanded(index) ? 'chevron-down' : 'chevron-right'" size="14px" />
                <t-loading v-else size="small" style="width: 14px; height: 14px;" />
                <span>{{ $t('knowledgeBase.viewParentContext') }}</span>
              </div>
              <div v-show="isParentExpanded(index)" class="parent-context-content">
                <div class="md-content" v-html="processMarkdown(getParentContent(item))"></div>
              </div>
            </div>
            
            <!-- 生成的问题展示 -->
            <div v-if="getGeneratedQuestions(item).length > 0" class="questions-section">
              <div class="questions-toggle" @click="toggleQuestions(index)">
                <t-icon :name="isExpanded(index) ? 'chevron-down' : 'chevron-right'" size="14px" />
                <span>{{ $t('knowledgeBase.generatedQuestions') }} ({{ getGeneratedQuestions(item).length }})</span>
              </div>
              <div v-show="isExpanded(index)" class="questions-list">
                <div 
                  v-for="question in getGeneratedQuestions(item)" 
                  :key="question.id" 
                  class="question-item"
                >
                  <t-icon name="help-circle" size="14px" class="question-icon" />
                  <span class="question-text">{{ question.question }}</span>
                  <t-button 
                    theme="default" 
                    variant="text" 
                    size="small"
                    class="delete-question-btn"
                    :loading="isDeleting(index, question.id)"
                    @click.stop="handleDeleteQuestion(item, index, question)"
                  >
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
        <DocumentPreview
          :knowledgeId="details.id"
          :fileType="details.file_type"
          :fileName="details.title"
          :active="viewMode === 'preview'"
        />
      </div>
      
      <template #footer>
        <t-button @click="handleClose">{{ $t('common.confirm') }}</t-button>
        <t-button theme="default" @click="handleClose">{{ $t('common.cancel') }}</t-button>
      </template>
    </t-drawer>
  </div>
</template>
<style scoped lang="less">
@import "./css/markdown.less";

:deep(.t-drawer .t-drawer__content-wrapper) {
  width: min(654px, 85vw) !important; // 减少到85%视口宽度，给左侧留更多空间
  max-width: 654px !important;
}

// 在小屏幕上进一步调整
@media (max-width: 768px) {
  :deep(.t-drawer .t-drawer__content-wrapper) {
    width: 90vw !important; // 小屏幕上使用90%宽度
    max-width: none !important;
  }
}

// 代码块样式
:deep(.code-block-wrapper) {
  margin: 12px 0;
  border: 1px solid var(--td-component-border);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(0,0,0,0.05);

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
  font-weight: 800;
}

:deep(.t-drawer__body.narrow-scrollbar) {
  padding: 16px 24px;
}

.drawer-header {
  display: flex;
  align-items: center;
  gap: 12px;
  
  .header-title {
    flex: 1;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.doc_box, .url_box, .manual_box {
  display: flex;
  flex-direction: column;
  margin-bottom: 16px;
}

.label {
  color: var(--td-text-color-primary);
  font-size: 14px;
  font-style: normal;
  font-weight: 500;
  line-height: 22px;
  margin-bottom: 8px;
}

// 文件下载区域
.download_box {
  display: flex;
  align-items: center;
}

.doc_t {
  box-sizing: border-box;
  display: flex;
  padding: 5px 8px;
  align-items: center;
  border-radius: 3px;
  border: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container-hover);
  word-break: break-all;
  text-align: justify;
}

.icon_box {
  margin-left: 18px;
  display: flex;
  overflow: hidden;
  color: var(--td-brand-color);

  .download_box {
    width: 16px;
    height: 16px;
    fill: currentColor;
    overflow: hidden;
    cursor: pointer;
  }
}

// URL链接区域
.url_link_box {
  border-radius: 4px;
  border: 1px solid var(--td-success-color-focus);
  background: var(--td-success-color-light);
  padding: 8px 12px;
  
  .url_link {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--td-brand-color-active);
    text-decoration: none;
    transition: all 0.2s ease;
    
    &:hover {
      color: var(--td-brand-color);
      background: var(--td-success-color-light);
      border-radius: 3px;
      padding: 4px 6px;
      margin: -4px -6px;
      
      .jump-icon {
        transform: translateX(2px);
      }
    }
    
    .url_text {
      flex: 1;
      font-size: 13px;
      word-break: break-all;
    }
    
    .jump-icon {
      transition: transform 0.2s ease;
      flex-shrink: 0;
      color: var(--td-brand-color-active);
    }
  }
}

// 手动创建标题区域
.manual_title_box {
  border-radius: 4px;
  border: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container-hover);
  padding: 8px 12px;
  
  .manual_title {
    color: var(--td-text-color-primary);
    font-size: 14px;
    font-weight: 500;
    word-break: break-word;
  }
}

.content_header {
  margin-top: 22px;
  margin-bottom: 16px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;

  .header-left {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .title-row {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .meta-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
  }

  .chunk-count {
    color: var(--td-brand-color);
    font-size: 12px;
    background: var(--td-brand-color)14;
    padding: 4px 8px;
    border-radius: 12px;
  }

  .view-mode-buttons {
    display: flex;
    gap: 4px;
    
    .view-mode-btn {
      height: 28px;
      min-width: 60px;
    }
  }

  .view-mode-toggle {
    height: 28px;
  }
}

.time {
  color: var(--td-text-color-disabled);
  font-size: 12px;
  font-style: normal;
  font-weight: 400;
  line-height: 20px;
}

.no_content {
  margin-top: 12px;
  color: var(--td-text-color-disabled);
  font-size: 12px;
  padding: 16px;
  background: var(--td-bg-color-container);
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
  padding: 12px;
  transition: all 0.2s ease;
  border: 1px solid transparent;
  
  &.chunk-even {
    background: var(--td-bg-color-container-hover);
  }
  
  &.chunk-odd {
    background: var(--td-brand-color)0d;
  }
  
  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 2px 8px rgba(7, 192, 95, 0.1);
  }
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
  transition: color 0.2s ease;
  
  &:hover {
    color: var(--td-brand-color);
  }
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
  transition: color 0.2s ease;
  
  &:hover {
    color: var(--td-brand-color);
  }
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
  transition: background-color 0.2s ease;
  
  &:hover {
    background: var(--td-success-color-light);
    
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
    transition: opacity 0.2s ease, color 0.2s ease;
    
    &:hover {
      color: var(--td-error-color);
    }
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
