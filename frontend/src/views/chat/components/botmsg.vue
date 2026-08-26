<template>
    <div class="bot_msg" :class="{ 'is-embedded': embeddedMode }">
        <div style="display: flex;flex-direction: column; gap:8px">
            <!-- 显示@的知识库和文件（非 Agent 模式下显示） -->
            <div v-if="!session.isAgentMode && mentionedItems && mentionedItems.length > 0" class="mentioned_items">
                <span v-for="item in mentionedItems" :key="item.id" class="mentioned_tag" :class="[
                    mentionTagClass(item)
                ]">
                    <span class="tag_icon">
                        <t-icon v-if="item.type === 'kb'"
                            :name="item.kb_type === 'faq' ? 'chat-bubble-help' : 'folder'" />
                        <t-icon v-else :name="mentionTagIcon(item)" />
                    </span>
                    <span class="tag_name">{{ item.name }}</span>
                </span>
            </div>
            <div v-if="session.isRagMode" class="rag-answer-stack">
                <RagPipelineProgress :session="session" :embedded-mode="embeddedMode" />
                <AgentStreamDisplay v-if="session.isAgentMode" :session="session" :session-id="sessionId"
                    :user-query="userQuery" :rag-mode="true" :follow-up-loading="followUpLoading"
                    @render-complete-change="emit('render-complete-change', $event)" />
            </div>
            <template v-else>
                <!-- A plain answer has no timeline to put the memory row on, so
                     it gets the standalone row. Agent turns render theirs inside
                     the agent timeline instead, next to the steps it belongs
                     with. -->
                <RagPipelineProgress v-if="!session.isAgentMode && session.used_memories?.length"
                    :session="session" :embedded-mode="embeddedMode" memory-only />
                <docInfo v-if="session.knowledge_references?.length" :session="session"></docInfo>
                <AgentStreamDisplay :session="session" :session-id="sessionId" :user-query="userQuery"
                    v-if="session.isAgentMode" :follow-up-loading="followUpLoading"
                    @render-complete-change="emit('render-complete-change', $event)" />
            </template>
            <deepThink :deepSession="session" v-if="session.showThink && !session.isAgentMode"></deepThink>
        </div>
        <!-- 非 Agent 模式下才显示传统的 markdown 渲染 -->
        <div ref="parentMd" v-if="!session.hideContent && !session.isAgentMode">
            <!-- 直接渲染完整内容，避免切分导致的问题，样式与 thinking 一致 -->
            <!-- 只有当有实际内容时才显示包围框 -->
            <div class="content-wrapper" v-if="hasActualContent">
                <div class="ai-markdown-template markdown-content" v-stable-html="renderedHTML">
                </div>
            </div>
            <!-- 复制和添加到知识库按钮 - 非 Agent 模式下显示 -->
            <div v-if="answerFullyRendered && (content || session.content)" class="answer-toolbar">
                <t-button size="small" variant="outline" shape="round" @click.stop="handleCopyAnswer"
                    :title="$t('agent.copy')">
                    <t-icon name="copy" />
                </t-button>
                <t-button size="small" variant="outline" shape="round" @click.stop="handleAddToKnowledge"
                    :title="$t('agent.addToKnowledgeBase')">
                    <t-icon name="bookmark-add" />
                </t-button>
                <!-- Skill artifact download: only shown when this reply's
                     assistant message actually recorded any generated files.
                     Emptiness is the default: the button stays hidden for
                     conversational messages that never touched a skill. -->
                <span v-if="hasArtifacts" class="answer-toolbar__artifact">
                    <t-button size="small" variant="outline" shape="round"
                        @click.stop="openArtifactDrawer"
                        :title="$t('agent.artifactDrawer.buttonTitle')">
                        <t-icon name="download" />
                    </t-button>
                    <span class="answer-toolbar__artifact-count" aria-hidden="true">{{ artifactCount }}</span>
                </span>
                <!-- Fallback 提示图标 -->
                <t-tooltip v-if="session.is_fallback" :content="$t('chat.fallbackHint')" placement="top">
                    <t-button size="small" variant="outline" shape="round" class="fallback-icon-btn">
                        <t-icon name="info-circle" />
                    </t-button>
                </t-tooltip>
                <ChatRequestInfoButton v-if="showRequestInfo" :session="session" :session-id="sessionId" />
                <transition name="follow-up-toolbar-loading">
                    <span v-if="followUpLoading" class="answer-toolbar__follow-up-loading" role="status"
                        aria-live="polite">
                        <t-icon name="lightbulb" />
                        <span class="answer-toolbar__follow-up-label">{{ t('chat.followUpQuestionsLoading') }}</span>
                    </span>
                </transition>
            </div>
            <div v-if="isImgLoading" class="img_loading"><t-loading size="small"></t-loading><span>{{
                $t('common.loading') }}</span></div>
        </div>
        <picturePreview :reviewImg="reviewImg" :reviewUrl="reviewUrl" @closePreImg="closePreImg"></picturePreview>
        <Teleport to="body">
            <ChatCitationFloat :float="citationFloat" :on-enter="cancelCitationClose"
                :on-leave="scheduleCitationClose" />
        </Teleport>
        <ChatArtifactsDrawer
            v-if="hasArtifacts"
            v-model:visible="showArtifactDrawer"
            :session-id="sessionId"
            :message-id="messageIdForArtifacts"
            :artifacts="artifactList"
        />
    </div>
</template>
<script setup>
import { onMounted, onBeforeUnmount, watch, computed, ref, reactive, nextTick, onUpdated } from 'vue';
import 'katex/dist/katex.min.css';
import docInfo from './docInfo.vue';
import deepThink from './deepThink.vue';
import AgentStreamDisplay from './AgentStreamDisplay.vue';
import RagPipelineProgress from './RagPipelineProgress.vue';
import ChatRequestInfoButton from '@/components/ChatRequestInfoButton.vue';
import ChatCitationFloat from '@/components/ChatCitationFloat.vue';
import picturePreview from '@/components/picture-preview.vue';
import ChatArtifactsDrawer from './ChatArtifactsDrawer.vue';
import { sanitizeMarkdownHTML, safeMarkdownToHTML, createSafeImage, isValidImageURL, hydrateProtectedFileImages } from '@/utils/security';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { useUIStore } from '@/stores/ui';
import {
    buildManualMarkdown,
    formatManualTitle,
} from '@/utils/chatMessageShared';
import { copyWithToast } from '@/utils/clipboard';
import {
    createChatMarkdownRenderer,
    renderChatMarkdown,
} from '@/utils/chatMarkdownRenderer';
import {
    createMermaidCodeRenderer,
    ensureMermaidInitialized,
    renderMermaidInContainer,
    enhanceMarkdownContainer,
} from '@/utils/mermaidShared';
import { refreshMarkdownEnhancements } from '@/utils/markdownEnhancements';
import { useChatCitationPopover } from '@/composables/useChatCitationPopover';
import { useTypewriter } from '@/composables/useTypewriter';
import { vStableHtml } from '@/directives/stableHtml';
import { SKILL_ICON } from '@/types/mention';

ensureMermaidInitialized();

const mentionTagClass = (item) => {
    if (item.type === 'kb') return item.kb_type === 'faq' ? 'faq-tag' : 'kb-tag';
    return `${item.type || 'file'}-tag`;
};

const mentionTagIcon = (item) => {
    if (item.type === 'tag') return 'tag';
    if (item.type === 'mcp') return 'tools';
    if (item.type === 'skill') return SKILL_ICON;
    return 'file';
};

const emit = defineEmits(['scroll-bottom', 'render-complete-change'])
const { t } = useI18n()
const uiStore = useUIStore();
let parentMd = ref()
const { float: citationFloat, rebind: rebindCitations, cancelClose: cancelCitationClose, scheduleClose: scheduleCitationClose } = useChatCitationPopover(parentMd, {
    getKnowledgeReferences: () => props.session?.knowledge_references,
    sessionId: () => props.sessionId,
});
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
    },
    embeddedMode: {
        type: Boolean,
        default: false
    },
    sessionId: {
        type: String,
        default: ''
    },
    followUpLoading: {
        type: Boolean,
        default: false
    }
});

const showRequestInfo = computed(() => !!(props.session?.request_id || props.session?.id));

// -----------------------------------------------------------------------------
// Skill artifact download (drawer)
// -----------------------------------------------------------------------------
// The download button and drawer are opt-in per message: the toolbar checks
// `hasArtifacts` and only renders when the assistant message actually
// recorded a file. `messageIdForArtifacts` resolves to whichever field the
// caller uses to identify the row on the server (session.id from the SSE
// hydration path, request_id when the caller pre-populated it).
//
// NOTE: this file's <script setup> block is plain JS (no lang="ts"), so we
// stay away from TypeScript-only syntax like `as any[]` — the vite Vue
// plugin routes non-TS blocks through babel which rejects those tokens.
const showArtifactDrawer = ref(false);
const artifactList = computed(() => {
    const raw = props.session && props.session.artifacts;
    const list = Array.isArray(raw) ? raw : [];
    // Enrich each entry with its position so the download endpoint can
    // resolve it. Server responses already include `index` when they come
    // via listMessageArtifacts; SSE payloads that land through Message.Artifacts
    // omit it. Normalising here keeps ChatArtifactsDrawer index-agnostic.
    return list.map((a, i) => ({ index: i, ...a }));
});
const hasArtifacts = computed(() => artifactList.value.length > 0);
const artifactCount = computed(() => artifactList.value.length);
const messageIdForArtifacts = computed(() => {
    // Prefer the persistent message ID; fall back to request_id for the
    // in-flight path where the SSE stream still identifies rows by request.
    return String((props.session && (props.session.id || props.session.request_id)) || '');
});
function openArtifactDrawer() {
    if (!hasArtifacts.value) return;
    showArtifactDrawer.value = true;
}

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

const markdownRenderer = createChatMarkdownRenderer({
    codeRenderer: createMermaidCodeRenderer('mermaid-botmsg'),
    imageRenderer: ({ href, title, text }) => createSafeImage(href, text || '', title || ''),
    invalidImageHtml: () => `<p>${t('error.invalidImageLink')}</p>`,
    isValidImageUrl: isValidImageURL,
});

// 计算属性：将 Markdown 文本转换为 tokens
const mentionedItems = computed(() => {
    return props.session?.mentioned_items || [];
});

// Smooth the streamed answer into a steady typewriter cadence (shared with the
// Agent path). Copy/toolbar still read the full content; only display is paced.
const answerText = computed(() => {
    const text = props.content || props.session?.content || '';
    return typeof text === 'string' ? text : '';
});
const { displayed: typedAnswer } = useTypewriter(
    () => answerText.value,
    () => Boolean(props.session?.is_completed),
);

// The backend completion event can arrive while the local typewriter still has
// buffered text to reveal. Treat the answer as visually complete only after the
// displayed text has caught up, so actions never appear beside a moving answer.
const answerFullyRendered = computed(() =>
    Boolean(props.session?.is_completed) && typedAnswer.value.length >= answerText.value.length
);

watch(
    answerFullyRendered,
    (ready) => {
        if (!props.session?.isAgentMode) emit('render-complete-change', ready);
    },
    { immediate: true },
);

// 单次渲染整个 Markdown 内容（替代 token-by-token，修复 KaTeX 公式在 streaming 时闪烁消失的问题）
const renderedHTML = computed(() => {
    const text = typedAnswer.value;
    if (!text || typeof text !== 'string') return '';
    return renderChatMarkdown(text, {
        renderer: markdownRenderer,
        escapeMarkdown: safeMarkdownToHTML,
        sanitizeHtml: sanitizeMarkdownHTML,
        streaming: !props.session?.is_completed,
        knowledgeReferences: props.session?.knowledge_references,
    });
});

// 计算属性：判断是否有实际内容（非空且不只是空白）
const hasActualContent = computed(() => {
    const text = props.content || props.session?.content || '';
    return text && text.trim().length > 0;
});

// 获取实际内容
const getActualContent = () => {
    return (props.content || props.session?.content || '').trim();
};

// 复制回答内容
const handleCopyAnswer = async () => {
    const content = getActualContent();
    if (!content) {
        MessagePlugin.warning(t('chat.emptyContentWarning'));
        return;
    }

    await copyWithToast(content, 'chat.copySuccess', 'chat.copyFailed');
};

// 添加到知识库
const handleAddToKnowledge = () => {
    const content = getActualContent();
    if (!content) {
        MessagePlugin.warning(t('chat.emptyContentWarning'));
        return;
    }

    const question = (props.userQuery || '').trim();
    const manualContent = buildManualMarkdown(question, content);
    const manualTitle = formatManualTitle(question);
    ``
    uiStore.openManualEditor({
        mode: 'create',
        title: manualTitle,
        content: manualContent,
        status: 'draft',
    });

    MessagePlugin.info(t('chat.editorOpened'));
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

watch(renderedHTML, () => {
    nextTick(() => {
        rebindCitations();
    });
});

// 渲染 Mermaid 图表的函数
onUpdated(() => {
    nextTick(async () => {
        await hydrateProtectedFileImages(parentMd.value);
        refreshMarkdownEnhancements(parentMd.value);
        if (props.session?.is_completed) {
            await renderMermaidInContainer(parentMd.value);
        }
    });
});

onMounted(async () => {
    // 为 markdown-content 中的图片添加点击事件
    nextTick(async () => {
        if (parentMd.value) {
            parentMd.value.addEventListener('click', handleMarkdownImageClick, true);
        }
        rebindCitations();
        await hydrateProtectedFileImages(parentMd.value);
        await enhanceMarkdownContainer(parentMd.value);
    });
});

onBeforeUnmount(() => {
    if (parentMd.value) {
        parentMd.value.removeEventListener('click', handleMarkdownImageClick, true);
    }
});
</script>
<style lang="less" scoped>
@import '../../../components/css/chat-markdown.less';
@import '../../../components/css/chat-message-shared.less';
@import '../../../components/css/chat-citations.less';

.bot_msg {
    &.is-embedded {
        width: 100%;

        :deep(.agent-stream-display) {
            width: 100%;
        }
    }
}

.rag-answer-stack {
    display: flex;
    flex-direction: column;
    gap: 0;
}

// 内容包装器 - 与 Agent 模式的 answer 样式一致
.content-wrapper {
    padding: 2px 0;
}

.markdown-content {
    // Chat Markdown visual styles are centralized in chat-markdown.less.
    // Do not add element-level Markdown rules here; update the shared mixin.
    .chat-markdown-typography();
    .chat-citation-pills();
}

.mentioned_items {
    .chat-mentioned-items();
}

.mentioned_tag {
    .chat-mentioned-tag();
}

.fallback-icon-btn {
    color: var(--td-text-color-disabled) !important;
    border-color: var(--td-component-stroke) !important;

    &:hover {
        color: var(--td-text-color-placeholder) !important;
        border-color: var(--td-component-border) !important;
    }
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
    border: 0.5px solid var(--td-component-stroke);
    transition: transform 0.2s ease;

    &:hover {
        transform: scale(1.02);
    }
}

.bot_msg {
    // background: var(--td-bg-color-container);
    border-radius: 4px;
    color: var(--td-text-color-primary);
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

.img_loading {
    background: var(--td-bg-color-container-hover);
    height: 230px;
    width: 230px;
    color: var(--td-text-color-placeholder);
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
