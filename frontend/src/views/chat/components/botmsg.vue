<template>
    <div class="bot_msg">
        <div style="display: flex;flex-direction: column; gap:8px">
            <!-- 显示@的知识库和文件（非 Agent 模式下显示） -->
            <div v-if="!session.isAgentMode && mentionedItems && mentionedItems.length > 0" class="mentioned_items">
                <span 
                    v-for="item in mentionedItems" 
                    :key="item.id" 
                    class="mentioned_tag"
                    :class="[
                      item.type === 'kb' ? (item.kb_type === 'faq' ? 'faq-tag' : 'kb-tag') : 'file-tag'
                    ]"
                >
                    <span class="tag_icon">
                        <t-icon v-if="item.type === 'kb'" :name="item.kb_type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                        <t-icon v-else name="file" />
                    </span>
                    <span class="tag_name">{{ item.name }}</span>
                </span>
            </div>
            <docInfo :session="session"></docInfo>
            <AgentStreamDisplay :session="session" :user-query="userQuery" v-if="session.isAgentMode"></AgentStreamDisplay>
            <deepThink :deepSession="session" v-if="session.showThink && !session.isAgentMode"></deepThink>
        </div>
        <!-- 非 Agent 模式下才显示传统的 markdown 渲染 -->
        <div ref="parentMd" v-if="!session.hideContent && !session.isAgentMode">
            <!-- 直接渲染完整内容，避免切分导致的问题，样式与 thinking 一致 -->
            <!-- 只有当有实际内容时才显示包围框 -->
            <div class="content-wrapper" v-if="hasActualContent">
                <div class="ai-markdown-template markdown-content">
                    <div v-for="(token, index) in markdownTokens" :key="index" v-html="renderToken(token)"></div>
                </div>
            </div>
            <!-- 复制和添加到知识库按钮 - 非 Agent 模式下显示 -->
            <div v-if="session.is_completed && (content || session.content)" class="answer-toolbar">
                <t-button size="small" variant="outline" shape="round" @click.stop="handleCopyAnswer" :title="$t('agent.copy')">
                    <t-icon name="copy" />
                </t-button>
                <t-button size="small" variant="outline" shape="round" @click.stop="handleAddToKnowledge" :title="$t('agent.addToKnowledgeBase')">
                    <t-icon name="add" />
                </t-button>
            </div>
            <div v-if="isImgLoading" class="img_loading"><t-loading size="small"></t-loading><span>{{ $t('common.loading') }}</span></div>
        </div>
        <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg"></picturePreview>
    </div>
</template>
<script setup>
import { onMounted, onBeforeUnmount, watch, computed, ref, reactive, defineProps, nextTick } from 'vue';
import { marked } from 'marked';
import mermaid from 'mermaid';
import docInfo from './docInfo.vue';
import deepThink from './deepThink.vue';
import AgentStreamDisplay from './AgentStreamDisplay.vue';
import picturePreview from '@/components/picture-preview.vue';
import { sanitizeHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL } from '@/utils/security';
import { openMermaidFullscreen } from '@/utils/mermaidViewer';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { useUIStore } from '@/stores/ui';

marked.use({
    mangle: false,
    headerIds: false,
    breaks: true,  // 全局启用单个换行支持
});

// Mermaid 初始化计数器
let botmsgMermaidCount = 0;

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

const emit = defineEmits(['scroll-bottom'])
const { t } = useI18n()
const uiStore = useUIStore();
const renderer = new marked.Renderer();
let parentMd = ref()
let reviewUrl = ref('')
let reviewImg = ref(false)
let isImgLoading = ref(false);
const props = defineProps({
    // 必填项
    content: {
        type: String,
        required: false
    },
    session: {
        type: Object,
        required: false
    },
    userQuery: {
        type: String,
        required: false,
        default: ''
    },
    isFirstEnter: {
        type: Boolean,
        required: false
    }
});

const preview = (url) => {
    nextTick(() => {
        reviewUrl.value = url;
        reviewImg.value = true
    })
}

const closePreImg = () => {
    reviewImg.value = false
    reviewUrl.value = '';
}

// 创建自定义渲染器实例
const customRenderer = new marked.Renderer();
// 覆盖图片渲染方法
customRenderer.image = function(href, title, text) {
    // 验证图片 URL 是否安全
    if (!isValidImageURL(href)) {
        return `<p>${t('error.invalidImageLink')}</p>`;
    }
    // 使用安全的图片创建函数
    return createSafeImage(href, text || '', title || '');
};

// 覆盖代码块渲染方法，支持 Mermaid
customRenderer.code = function(code, infostring) {
    const lang = (infostring || '').trim();

    // Mermaid 图表处理
    if (lang === 'mermaid') {
        const id = `mermaid-botmsg-${++botmsgMermaidCount}`;
        return `<div class="mermaid" id="${id}">${code}</div>`;
    }

    // 普通代码块
    const displayLang = lang || 'Code';
    const escapedCode = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return `<pre><code class="language-${displayLang}">${escapedCode}</code></pre>`;
};

// 计算属性：将 Markdown 文本转换为 tokens
const markdownTokens = computed(() => {
    const text = props.content || props.session?.content || '';
    if (!text || typeof text !== 'string') {
        return [];
    }
    
    // 首先对 Markdown 内容进行安全处理
    const safeMarkdown = safeMarkdownToHTML(text);
    
    // 使用 marked.lexer 分词
    return marked.lexer(safeMarkdown);
});

// 计算属性：判断是否有实际内容（非空且不只是空白）
const hasActualContent = computed(() => {
    const text = props.content || props.session?.content || '';
    return text && text.trim().length > 0;
});

// 渲染单个 token 为 HTML
const renderToken = (token) => {
    try {
        // 创建临时的 marked 配置
        const markedOptions = {
            renderer: customRenderer,
            breaks: true
        };
        
        // 解析单个 token
        // marked.parser 接受 token 数组
        let html = marked.parser([token], markedOptions);
        
        // 使用 DOMPurify 进行最终的安全清理
        return sanitizeHTML(html);
    } catch (e) {
        console.error('Token rendering error:', e);
        return '';
    }
};

const myMarkdown = (res) => {
    return marked.parse(res, { renderer })
}

// 获取实际内容
const getActualContent = () => {
    return (props.content || props.session?.content || '').trim();
};

// 格式化标题
const formatManualTitle = (question) => {
    if (!question) {
        return '会话摘录';
    }
    const condensed = question.replace(/\s+/g, ' ').trim();
    if (!condensed) {
        return '会话摘录';
    }
    return condensed.length > 40 ? `${condensed.slice(0, 40)}...` : condensed;
};

// 构建手动添加的 Markdown 内容
const buildManualMarkdown = (question, answer) => {
    const safeAnswer = answer?.trim() || '（无回答内容）';
    return `${safeAnswer}`;
};

// 复制回答内容
const handleCopyAnswer = async () => {
    const content = getActualContent();
    if (!content) {
        MessagePlugin.warning(t('chat.emptyContentWarning') || '当前回答为空，无法复制');
        return;
    }

    try {
        if (navigator.clipboard && navigator.clipboard.writeText) {
            await navigator.clipboard.writeText(content);
            MessagePlugin.success(t('chat.copySuccess') || '已复制到剪贴板');
        } else {
            const textArea = document.createElement('textarea');
            textArea.value = content;
            textArea.style.position = 'fixed';
            textArea.style.opacity = '0';
            document.body.appendChild(textArea);
            textArea.select();
            document.execCommand('copy');
            document.body.removeChild(textArea);
            MessagePlugin.success(t('chat.copySuccess') || '已复制到剪贴板');
        }
    } catch (err) {
        console.error('复制失败:', err);
        MessagePlugin.error(t('chat.copyFailed') || '复制失败，请手动复制');
    }
};

// 添加到知识库
const handleAddToKnowledge = () => {
    const content = getActualContent();
    if (!content) {
        MessagePlugin.warning(t('chat.emptyContentWarning') || '当前回答为空，无法保存到知识库');
        return;
    }

    const question = (props.userQuery || '').trim();
    const manualContent = buildManualMarkdown(question, content);
    const manualTitle = formatManualTitle(question);

    uiStore.openManualEditor({
        mode: 'create',
        title: manualTitle,
        content: manualContent,
        status: 'draft',
    });

    MessagePlugin.info(t('chat.editorOpened') || '已打开编辑器，请选择知识库后保存');
};

// 处理 markdown-content 中图片的点击事件
const handleMarkdownImageClick = (e) => {
    const target = e.target;
    if (target && target.tagName === 'IMG') {
        const src = target.getAttribute('src');
        if (src) {
            e.preventDefault();
            e.stopPropagation();
            preview(src);
        }
    }
};

// 渲染 Mermaid 图表的函数
const renderMermaidDiagrams = async () => {
    try {
        if (parentMd.value) {
            const mermaidElements = parentMd.value.querySelectorAll('.mermaid');
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
        }
    } catch (error) {
        console.error('Mermaid rendering error:', error);
    }
};

// 已渲染的 mermaid 元素 ID 集合
const renderedMermaidIds = new Set();

// Mermaid 点击处理函数 - 必须在 bindMermaidClickEvents 之前定义
const handleMermaidClick = (e) => {
    e.stopPropagation();
    const target = e.currentTarget;
    const svg = target.querySelector('svg');
    if (svg) {
        openMermaidFullscreen(svg.outerHTML);
    }
};

// 为 Mermaid 容器绑定点击全屏事件（绑定在 div 上，不是 SVG 上）
const bindMermaidClickEvents = () => {
    if (!parentMd.value) {
        console.log('[Mermaid] parentMd is null');
        return;
    }
    // 绑定在 .mermaid div 上，而不是 SVG 上
    const mermaidDivs = parentMd.value.querySelectorAll('.mermaid');
    console.log('[Mermaid] Found mermaid divs:', mermaidDivs.length);
    mermaidDivs.forEach((div, index) => {
        const divEl = div;
        divEl.style.cursor = 'pointer';
        // 移除旧的事件监听器（避免重复绑定）
        divEl.removeEventListener('click', handleMermaidClick);
        divEl.addEventListener('click', handleMermaidClick);
        console.log(`[Mermaid] Bound click event to div ${index}`);
    });
};

// 监听内容变化并渲染 Mermaid - 只在会话完成后渲染
watch(() => [props.content, props.session?.content, props.session?.is_completed], () => {
    // 只在会话完成后渲染 mermaid
    if (props.session?.is_completed) {
        nextTick(() => {
            renderMermaidDiagrams();
        });
    }
}, { immediate: true });

onMounted(async () => {
    // 为 markdown-content 中的图片添加点击事件
    nextTick(() => {
        if (parentMd.value) {
            parentMd.value.addEventListener('click', handleMarkdownImageClick, true);
        }
        // 初始渲染 Mermaid 图表
        renderMermaidDiagrams();
    });
});

onBeforeUnmount(() => {
    if (parentMd.value) {
        parentMd.value.removeEventListener('click', handleMarkdownImageClick, true);
    }
});
</script>
<style lang="less" scoped>
@import '../../../components/css/markdown.less';

// 内容包装器 - 与 Agent 模式的 answer 样式一致
.content-wrapper {
    background: #ffffff;
    border-radius: 6px;
    padding: 8px 12px;
    border: 1px solid #07c05f;
    box-shadow: 0 1px 3px rgba(7, 192, 95, 0.06);
    transition: all 0.2s ease;
}

@keyframes fadeInUp {
    from {
        opacity: 0;
        transform: translateY(8px);
    }
    to {
        opacity: 1;
        transform: translateY(0);
    }
}

.ai-markdown-template {
    font-size: 13px;
    color: #374151;
    line-height: 1.6;
}

.markdown-content {
    :deep(p) {
        margin: 6px 0;
        line-height: 1.6;
    }

    :deep(code) {
        background: #f3f4f6;
        padding: 2px 5px;
        border-radius: 3px;
        font-family: 'Monaco', 'Menlo', 'Courier New', monospace;
        font-size: 11px;
    }

    :deep(pre) {
        background: #f9fafb;
        padding: 10px;
        border-radius: 4px;
        overflow-x: auto;
        margin: 6px 0;

        code {
            background: none;
            padding: 0;
        }
    }

    :deep(ul), :deep(ol) {
        margin: 6px 0;
        padding-left: 20px;
    }

    :deep(li) {
        margin: 3px 0;
    }

    :deep(blockquote) {
        border-left: 2px solid #07c05f;
        padding-left: 10px;
        margin: 6px 0;
        color: #6b7280;
    }

    :deep(h1), :deep(h2), :deep(h3), :deep(h4), :deep(h5), :deep(h6) {
        margin: 10px 0 6px 0;
        font-weight: 600;
        color: #374151;
    }

    :deep(a) {
        color: #07c05f;
        text-decoration: none;

        &:hover {
            text-decoration: underline;
        }
    }

    :deep(table) {
        border-collapse: collapse;
        margin: 6px 0;
        font-size: 11px;
        width: 100%;

        th, td {
            border: 1px solid #e5e7eb;
            padding: 5px 8px;
            text-align: left;
        }

        th {
            background: #f9fafb;
            font-weight: 600;
        }

        tbody tr:nth-child(even) {
            background: #fafafa;
        }
    }

    :deep(img) {
        max-width: 80%;
        max-height: 300px;
        width: auto;
        height: auto;
        border-radius: 8px;
        display: block;
        margin: 8px 0;
        border: 0.5px solid #e5e7eb;
        object-fit: contain;
        cursor: pointer;
        transition: transform 0.2s ease;

        &:hover {
            transform: scale(1.02);
        }
    }

    // Mermaid 图表样式
    :deep(.mermaid) {
        margin: 16px 0;
        padding: 16px;
        background: #f8f9fa;
        border-radius: 8px;
        overflow-x: auto;
        text-align: center;

        svg {
            max-width: 100%;
            height: auto;
        }
    }
}

.ai-markdown-img {
    max-width: 80%;
    max-height: 300px;
    width: auto;
    height: auto;
    border-radius: 8px;
    display: block;
    cursor: pointer;
    object-fit: contain;
    margin: 8px 0 8px 16px;
    border: 0.5px solid #E7E7E7;
    transition: transform 0.2s ease;

    &:hover {
        transform: scale(1.02);
    }
}

.bot_msg {
    // background: #fff;
    border-radius: 4px;
    color: rgba(0, 0, 0, 0.9);
    font-size: 16px;
    // padding: 10px 12px;
    margin-right: auto;
    max-width: 100%;
    box-sizing: border-box;
}

.botanswer_laoding_gif {
    width: 24px;
    height: 18px;
    margin-left: 16px;
}

.thinking-loading {
    padding: 8px 0;
}

.loading-typing {
    display: flex;
    align-items: center;
    gap: 4px;
    
    span {
        width: 6px;
        height: 6px;
        border-radius: 50%;
        background: #07c05f;
        animation: typingBounce 1.4s ease-in-out infinite;
        
        &:nth-child(1) {
            animation-delay: 0s;
        }
        
        &:nth-child(2) {
            animation-delay: 0.2s;
        }
        
        &:nth-child(3) {
            animation-delay: 0.4s;
        }
    }
}

@keyframes typingBounce {
    0%, 60%, 100% {
        transform: translateY(0);
    }
    30% {
        transform: translateY(-8px);
    }
}

// 复制和添加到知识库按钮工具栏
.answer-toolbar {
    display: flex;
    justify-content: flex-start;
    gap: 6px;
    margin-top: 8px;
    min-height: 32px;

    :deep(.t-button) {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        min-width: auto;
        width: auto;
        border: 1px solid #e0e0e0;
        border-radius: 6px;
        background: #ffffff;
        color: #666;
        transition: all 0.2s ease;
        
        .t-button__content {
            display: inline-flex !important;
            align-items: center;
            justify-content: center;
            gap: 0;
        }
        
        .t-button__text {
            display: inline-flex !important;
            align-items: center;
            justify-content: center;
            gap: 0;
        }
        
        .t-icon {
            display: inline-flex !important;
            visibility: visible !important;
            opacity: 1 !important;
            align-items: center;
            justify-content: center;
            font-size: 16px;
            width: 16px;
            height: 16px;
            flex-shrink: 0;
            color: #666;
        }
        
        .t-icon svg {
            display: block !important;
            width: 16px;
            height: 16px;
        }
        
        .t-button__text > :not(.t-icon) {
            display: none;
        }
        
        &:hover:not(:disabled) {
            background: rgba(7, 192, 95, 0.08);
            border-color: rgba(7, 192, 95, 0.3);
            color: #07c05f;
            
            .t-icon {
                color: #07c05f;
            }
        }
        
        &:active:not(:disabled) {
            background: rgba(7, 192, 95, 0.12);
            border-color: rgba(7, 192, 95, 0.4);
            transform: translateY(0.5px);
        }
    }
}

.img_loading {
    background: #3032360f;
    height: 230px;
    width: 230px;
    color: #00000042;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-direction: column;
    font-size: 12px;
    gap: 4px;
    margin-left: 16px;
    border-radius: 8px;
}

:deep(.t-loading__gradient-conic) {
    background: conic-gradient(from 90deg at 50% 50%, #fff 0deg, #676767 360deg) !important;

}
</style>