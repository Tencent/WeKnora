// @ts-nocheck
<script setup lang="ts">
import { marked } from "marked";
import hljs from "highlight.js";
import "highlight.js/styles/github.css";
import { onMounted, ref, nextTick, onUnmounted, onUpdated, watch } from "vue";
import { downKnowledgeDetails, deleteGeneratedQuestion } from "@/api/knowledge-base/index";
import { MessagePlugin, DialogPlugin } from "tdesign-vue-next";
import { sanitizeHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL } from '@/utils/security';
import { useI18n } from 'vue-i18n';

const { t } = useI18n();

marked.use({
  mangle: false,
  headerIds: false,
  breaks: true,      // 启用单行换行转 <br>
  gfm: true,         // 启用 GitHub Flavored Markdown
});
const renderer = new marked.Renderer();
let page = 1;
let doc = null;
let down = ref()
let mdContentWrap = ref()
let url = ref('')
// 视图模式：chunks / original / merged
const viewMode = ref<'chunks' | 'original' | 'merged'>('merged');
const originalContent = ref<string>('');
const loadingOriginal = ref(false);

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
onUpdated(() => {
  page = 1
})
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
renderer.image = function (href, title, text) {
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
renderer.code = function (code, infostring) {
  const lang = (infostring || '').trim();
  let detectedLang = lang;
  let highlighted = '';
  if (lang && hljs.getLanguage(lang)) {
    try {
      highlighted = hljs.highlight(code, { language: lang }).value;
    } catch (e) {
      highlighted = hljs.highlightAuto(code).value;
      detectedLang = hljs.highlightAuto(code).language || lang;
    }
  } else {
    const auto = hljs.highlightAuto(code);
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
const props = defineProps(["visible", "details", "knowledgeType", "sourceInfo"]);
const emit = defineEmits(["closeDoc", "getDoc", "questionDeleted"]);

// 监听 chunks 变化，自动更新合并内容
watch(() => props.details?.md, (newChunks) => {
  if (newChunks && newChunks.length > 0) {
    mergedContent.value = mergeChunks(newChunks);
  } else {
    mergedContent.value = '';
  }
}, { immediate: true, deep: true });

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
const loadOriginalContent = async () => {
  if (!props.details.id || !props.details.type || props.details.type !== 'file') return;
  const fileType = props.details.file_type?.toLowerCase();
  if (!isTextFile(fileType)) {
    MessagePlugin.warning(t('knowledgeBase.originalFileNotSupported') || '该文件类型不支持原文件展示，请下载查看');
    return;
  }
  loadingOriginal.value = true;
  try {
    const blob = await downKnowledgeDetails(props.details.id);
    const text = await blob.text();
    originalContent.value = text;
  } catch (error: any) {
    console.error('Failed to load original content:', error);
    MessagePlugin.error(error?.message || t('knowledgeBase.loadOriginalFailed') || '加载原文件内容失败');
  } finally {
    loadingOriginal.value = false;
  }
};
watch(() => props.details.md, (newVal) => {
  nextTick(async () => {
    const images = mdContentWrap.value.querySelectorAll('img.markdown-image');
    if (images) {
      images.forEach(async item => {
        const isValid = await checkImage(item.src);
        if (!isValid) {
          item.remove();
        }
      })
    }
  })
}, { immediate: true, deep: true })

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
  originalContent.value = '';
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
      return t('knowledgeBase.typeURL') || '网页';
    case 'manual':
      return t('knowledgeBase.typeManual') || '手动创建';
    case 'file':
      return props.details.file_type ? props.details.file_type.toUpperCase() : t('knowledgeBase.typeFile') || '文件';
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
      return t('knowledgeBase.webContent') || '网页内容';
    case 'manual':
      return t('knowledgeBase.documentContent') || '文档内容';
    case 'file':
    default:
      return t('knowledgeBase.fileContent') || '文件内容';
  }
};

// 获取时间标签
const getTimeLabel = () => {
  switch (props.details.type) {
    case 'url':
      return t('knowledgeBase.importTime') || '导入时间';
    case 'manual':
      return t('knowledgeBase.createTime') || '创建时间';
    case 'file':
    default:
      return t('knowledgeBase.uploadTime') || '上传时间';
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
    parts.push(`${item.char_count} ${t('knowledgeBase.characters') || '字符'}`);
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
    MessagePlugin.error(t('common.error') || '操作失败');
    return;
  }

  // 检查是否是旧格式数据（无法删除）
  if (question.id.startsWith('legacy-')) {
    MessagePlugin.warning(t('knowledgeBase.legacyQuestionCannotDelete') || '旧格式问题无法删除，请重新生成问题');
    return;
  }

  const confirmDialog = DialogPlugin.confirm({
    header: t('common.confirmDelete') || '确认删除',
    body: t('knowledgeBase.confirmDeleteQuestion') || '确定要删除这个问题吗？删除后将同时移除对应的向量索引。',
    confirmBtn: t('common.confirm') || '确认',
    cancelBtn: t('common.cancel') || '取消',
    onConfirm: async () => {
      confirmDialog.hide();
      deletingQuestion.value = { chunkIndex, questionId: question.id };
      try {
        await deleteGeneratedQuestion(item.id, question.id);
        MessagePlugin.success(t('common.deleteSuccess') || '删除成功');
        
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
        MessagePlugin.error(error?.message || t('common.deleteFailed') || '删除失败');
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
        link.setAttribute("download", props.details.title);
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
  if (doc) {
    let pageNum = Math.ceil(props.details.total / 20);
    const { scrollTop, scrollHeight, clientHeight } = doc;
    if (scrollTop + clientHeight >= scrollHeight) {
      page++;
      if (props.details.md.length < props.details.total && page <= pageNum) {
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
          <div class="icon_box" @click="downloadFile()">
            <img class="download_box" src="@/assets/img/download.svg" alt="">
          </div>
        </div>
      </div>
      
      <!-- URL类型专属区域 -->
      <div v-else-if="details.type === 'url'" class="url_box">
        <span class="label">{{ $t('knowledgeBase.urlSource') || '来源网址' }}</span>
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
        <span class="label">{{ $t('knowledgeBase.documentTitle') || '文档标题' }}</span>
        <div class="manual_title_box">
          <span class="manual_title">{{ details.title }}</span>
        </div>
      </div>
      
      <div class="content_header">
        <div class="header-left">
          <div class="title-row">
            <span class="label">{{ getContentLabel() }}</span>
            <span v-if="details.total > 0" class="chunk-count">
              {{ $t('knowledgeBase.chunkCount', { count: details.total }) || `共 ${details.total} 个片段` }}
            </span>
          </div>
          <div class="meta-row">
            <span class="time"> {{ getTimeLabel() }}：{{ details.time }} </span>
            <div class="view-mode-buttons">
              <t-button 
                size="small" 
                :variant="viewMode === 'merged' ? 'base' : 'outline'" 
                :theme="viewMode === 'merged' ? 'primary' : 'default'"
                @click="viewMode = 'merged'"
                class="view-mode-btn"
              >
                {{ $t('knowledgeBase.viewMerged') || '全文' }}
              </t-button>
              <t-button 
                size="small" 
                :variant="viewMode === 'chunks' ? 'base' : 'outline'" 
                :theme="viewMode === 'chunks' ? 'primary' : 'default'"
                @click="viewMode = 'chunks'"
                class="view-mode-btn"
              >
                {{ $t('knowledgeBase.viewChunks') || '分块' }}
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
              <span class="chunk-index">{{ $t('knowledgeBase.segment') || '片段' }} {{ index + 1 }}</span>
              <div class="chunk-header-right">
                <t-tag 
                  v-if="getGeneratedQuestions(item).length > 0" 
                  size="small" 
                  theme="success" 
                  variant="light"
                >
                  {{ $t('knowledgeBase.questions') || '问题' }} {{ getGeneratedQuestions(item).length }}
                </t-tag>
                <span class="chunk-meta">{{ getChunkMeta(item) }}</span>
              </div>
            </div>
            <div class="md-content" v-html="processMarkdown(item.content)"></div>
            
            <!-- 生成的问题展示 -->
            <div v-if="getGeneratedQuestions(item).length > 0" class="questions-section">
              <div class="questions-toggle" @click="toggleQuestions(index)">
                <t-icon :name="isExpanded(index) ? 'chevron-down' : 'chevron-right'" size="14px" />
                <span>{{ $t('knowledgeBase.generatedQuestions') || '生成的问题' }} ({{ getGeneratedQuestions(item).length }})</span>
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
  width: 654px !important;
}

// 代码块样式
:deep(.code-block-wrapper) {
  margin: 12px 0;
  border: 1px solid #d1d5db;
  border-radius: 6px;
  background: #fff;
  overflow: hidden;
  box-shadow: 0 1px 2px rgba(0,0,0,0.05);

  .code-block-header {
    display: flex;
    align-items: center;
    padding: 8px 12px;
    background: #f3f4f6;
    border-bottom: 1px solid #e5e7eb;
    font-size: 12px;
    font-weight: 600;
    color: #1f2937;
  }

  .code-block-pre {
    margin: 0;
    padding: 12px;
    background: #f6f8fa;
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
  color: #000000e6;
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
  border: 1px solid #dcdcdc;
  background: #30323605;
  word-break: break-all;
  text-align: justify;
}

.icon_box {
  margin-left: 18px;
  display: flex;
  overflow: hidden;
  color: #07c05f;

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
  border: 1px solid #d0e8dc;
  background: #f0fdf4;
  padding: 8px 12px;
  
  .url_link {
    display: flex;
    align-items: center;
    gap: 8px;
    color: #059669;
    text-decoration: none;
    transition: all 0.2s ease;
    
    &:hover {
      color: #07c05f;
      background: #e6f7ed;
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
      color: #059669;
    }
  }
}

// 手动创建标题区域
.manual_title_box {
  border-radius: 4px;
  border: 1px solid #dcdcdc;
  background: #f0fdf4;
  padding: 8px 12px;
  
  .manual_title {
    color: #1d2129;
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
    color: #07c05f;
    font-size: 12px;
    background: #07c05f14;
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
  color: #00000066;
  font-size: 12px;
  font-style: normal;
  font-weight: 400;
  line-height: 20px;
}

.no_content {
  margin-top: 12px;
  color: #00000066;
  font-size: 12px;
  padding: 16px;
  background: #fbfbfb;
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
    background: #3032360f;
  }
  
  &.chunk-odd {
    background: #07c05f0d;
  }
  
  &:hover {
    border-color: #07c05f;
    box-shadow: 0 2px 8px rgba(7, 192, 95, 0.1);
  }
}

.chunk-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid #e7e7e7;
  
  .chunk-index {
    color: #00000099;
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
    color: #00000066;
    font-size: 11px;
  }
}

// 生成的问题样式
.questions-section {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed #e0e0e0;
}

.questions-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  color: #059669;
  font-size: 12px;
  font-weight: 500;
  padding: 4px 0;
  transition: color 0.2s ease;
  
  &:hover {
    color: #07c05f;
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
  background: #f0fdf4;
  border-radius: 4px;
  font-size: 13px;
  color: #1d2129;
  line-height: 1.5;
  transition: background-color 0.2s ease;
  
  &:hover {
    background: #e6f7ed;
    
    .delete-question-btn {
      opacity: 1;
    }
  }
  
  .question-icon {
    color: #059669;
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
    color: #999;
    transition: opacity 0.2s ease, color 0.2s ease;
    
    &:hover {
      color: #e34d59;
    }
  }
}

.md-content {
  word-break: break-word;
  line-height: 1.6;
  color: #1d2129;
}

// 保留旧样式作为兼容（已被chunk-item替代）
.content {
  word-break: break-word;
  padding: 4px;
  gap: 4px;
  margin-top: 12px;
}
</style>
