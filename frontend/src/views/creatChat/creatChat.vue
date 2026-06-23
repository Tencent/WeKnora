<template>
    <div class="dialogue-wrap">
        <div class="dialogue-answers">
            <div class="dialogue-title" style="--wails-draggable: drag">
                <span style="--wails-draggable: drag">{{ $t('createChat.title') }}</span>
            </div>
            <!-- 推荐问题 -->
            <div ref="sqContainerRef" class="suggested-questions-container">
                <!-- 骨架屏占位 -->
                <div v-if="sqLoading && suggestedQuestions.length === 0" class="suggested-questions-inner">
                    <div class="suggested-questions-title"><t-skeleton animation="gradient"
                            :row-col="[{ width: '120px', height: '14px' }]" /></div>
                    <div class="suggested-questions-grid">
                        <div v-for="n in 6" :key="'sq-skel-' + n" class="suggested-question-card sq-card-skeleton">
                            <t-skeleton animation="gradient"
                                :row-col="[{ width: '100%', height: '14px', type: 'rect' }]" />
                        </div>
                    </div>
                </div>
                <transition v-else appear name="sq-slide-fade" mode="out-in" @before-leave="onBeforeLeave"
                    @after-leave="onAfterLeave" @enter="onEnter" @after-enter="onQuestionsEntered">
                    <div v-if="suggestedQuestions.length > 0" :key="sqRenderKey" class="suggested-questions-inner">
                        <div class="suggested-questions-title-row">
                            <p class="suggested-questions-caption">
                                <span class="suggested-questions-title">{{ $t('chat.suggestedQuestions') }}</span>
                                <button type="button" class="suggested-questions-refresh" :disabled="sqLoading"
                                    :title="$t('chat.refreshSuggestedQuestions')"
                                    :aria-label="$t('chat.refreshSuggestedQuestions')" @click="fetchSuggestedQuestions">
                                    <t-icon :name="sqLoading ? 'loading' : 'refresh'"
                                        :class="{ 'sq-refresh-spin': sqLoading }" />
                                </button>
                            </p>
                        </div>
                        <div class="suggested-questions-grid">
                            <div v-for="(item, index) in suggestedQuestions" :key="item.question"
                                class="suggested-question-card" :class="{ 'sq-card-visible': sqCardsRevealed }"
                                :style="{ transitionDelay: sqCardsRevealed ? `${index * 50}ms` : '0ms' }"
                                @click="handleSuggestedQuestionClick(item.question)">
                                <span class="suggested-question-text">{{ item.question }}</span>
                                <span v-if="item.source === 'faq'" class="suggested-question-badge faq">FAQ</span>
                            </div>
                        </div>
                    </div>
                </transition>
            </div>
            <InputField ref="inputFieldRef" @send-msg="sendMsg"></InputField>
        </div>
    </div>

    <ContextualGuide tour="chat" :when="showChatContextualGuide" />

    <!-- 知识库编辑器（创建/编辑统一组件） -->
    <KnowledgeBaseEditorModal :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
        :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
        @update:visible="(val) => val ? null : uiStore.closeKBEditor()" @success="handleKBEditorSuccess" />
</template>
<script setup lang="ts">
import { ref, watch, onMounted, nextTick, computed } from 'vue';
import ContextualGuide from '@/components/ContextualGuide.vue';
import InputField from '@/components/Input-field.vue';
import { createSessions } from "@/api/chat/index";
import { getSuggestedQuestions } from "@/api/agent/index";
import type { SuggestedQuestion } from "@/api/agent/index";
import { useMenuStore } from '@/stores/menu';
import { useSettingsStore } from '@/stores/settings';
import { useUIStore } from '@/stores/ui';
import { useRoute, useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import KnowledgeBaseEditorModal from '@/views/knowledge/KnowledgeBaseEditorModal.vue';
import { useKnowledgeBaseCreationNavigation } from '@/hooks/useKnowledgeBaseCreationNavigation';

const router = useRouter();
const route = useRoute();
const usemenuStore = useMenuStore();
const settingsStore = useSettingsStore();
const uiStore = useUIStore();
const { t } = useI18n();
const { navigateToKnowledgeBaseList } = useKnowledgeBaseCreationNavigation();

const showChatContextualGuide = computed(() => {
    return route.name === 'globalCreatChat' || route.name === 'kbCreatChat';
});

// ===== 推荐问题 =====
const suggestedQuestions = ref<SuggestedQuestion[]>([]);
const sqLoading = ref(true);
const sqCardsRevealed = ref(false);
const sqRenderKey = ref(0);
const sqContainerRef = ref<HTMLElement | null>(null);
let suggestedQuestionsFetchId = 0;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

// --- 高度平滑过渡钩子 ---
const onBeforeLeave = () => {
    const c = sqContainerRef.value;
    if (!c) return;
    c.style.height = c.offsetHeight + 'px';
    c.style.overflow = 'hidden';
};

const onAfterLeave = () => {
    const c = sqContainerRef.value;
    if (!c) return;
    if (suggestedQuestions.value.length === 0) {
        requestAnimationFrame(() => { c.style.height = '0px'; });
        c.addEventListener('transitionend', () => {
            c.style.height = '';
            c.style.overflow = '';
        }, { once: true });
    }
};

const onEnter = (el: Element) => {
    const c = sqContainerRef.value;
    if (!c) return;
    const startHeight = c.offsetHeight;
    c.style.height = 'auto';
    c.style.overflow = 'hidden';
    const targetHeight = c.offsetHeight;
    c.style.height = startHeight + 'px';
    requestAnimationFrame(() => {
        c.style.height = targetHeight + 'px';
    });
};

const onQuestionsEntered = () => {
    const c = sqContainerRef.value;
    if (c) {
        c.style.height = '';
        c.style.overflow = '';
    }
    nextTick(() => { sqCardsRevealed.value = true; });
};

const fetchSuggestedQuestions = async () => {
    const fetchId = ++suggestedQuestionsFetchId;
    sqLoading.value = true;
    try {
        const agentId = settingsStore.selectedAgentId;
        if (!agentId) return;
        const selectedKBs = settingsStore.getSelectedKnowledgeBases();
        const selectedFiles = settingsStore.getSelectedFiles();
        const res = await getSuggestedQuestions(agentId, {
            knowledge_base_ids: selectedKBs.length > 0 ? selectedKBs : undefined,
            knowledge_ids: selectedFiles.length > 0 ? selectedFiles : undefined,
            limit: 6,
        });
        if (fetchId === suggestedQuestionsFetchId) {
            sqCardsRevealed.value = false;
            sqRenderKey.value++;
            suggestedQuestions.value = res?.data?.questions || [];
        }
    } catch (err) {
        console.warn('[SuggestedQuestions] Failed to fetch:', err);
        if (fetchId === suggestedQuestionsFetchId) {
            suggestedQuestions.value = [];
        }
    } finally {
        if (fetchId === suggestedQuestionsFetchId) {
            sqLoading.value = false;
        }
    }
};

// 防抖包装，切换知识库/文件时300ms内不重复请求
const debouncedFetch = () => {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => { fetchSuggestedQuestions(); }, 300);
};

// 监听 Agent / 知识库 / 文件切换
watch(() => settingsStore.selectedAgentId, debouncedFetch);
watch(() => settingsStore.settings.selectedKnowledgeBases, debouncedFetch, { deep: true });
watch(() => settingsStore.settings.selectedFiles, debouncedFetch, { deep: true });

onMounted(() => { fetchSuggestedQuestions(); });

const inputFieldRef = ref();

const handleSuggestedQuestionClick = (question: string) => {
    inputFieldRef.value?.triggerSend(question);
};

const sendMsg = (value: string, modelId: string, mentionedItems: any[], imageFiles: any[] = [], attachmentFiles: any[] = []) => {
    createNewSession(value, modelId, mentionedItems, imageFiles, attachmentFiles);
}

async function createNewSession(value: string, modelId: string, mentionedItems: any[] = [], imageFiles: any[] = [], attachmentFiles: any[] = []) {
    const selectedKbs = settingsStore.settings.selectedKnowledgeBases || [];
    const selectedFiles = settingsStore.settings.selectedFiles || [];

    // 构建 session 数据，包含 Agent 配置
    const sessionData: any = {};

    // 添加 Agent 配置（知识库信息在 agent_config 中）
    sessionData.agent_config = {
        enabled: true,
        max_iterations: settingsStore.agentConfig.maxIterations,
        temperature: settingsStore.agentConfig.temperature,
        knowledge_bases: selectedKbs,  // 所有选中的知识库
        knowledge_ids: selectedFiles,  // 所有选中的普通知识/文件
        allowed_tools: settingsStore.agentConfig.allowedTools
    };

    try {
        const res = await createSessions(sessionData);
        if (res.data && res.data.id) {
            await navigateToSession(res.data.id, value, modelId, mentionedItems, imageFiles, attachmentFiles);
        } else {
            console.error('[createChat] Failed to create session');
            MessagePlugin.error(t('createChat.messages.createFailed'));
        }
    } catch (error) {
        console.error('[createChat] Create session error:', error);
        MessagePlugin.error(t('createChat.messages.createError'));
    }
}

const navigateToSession = async (sessionId: string, value: string, modelId: string, mentionedItems: any[], imageFiles: any[] = [], attachmentFiles: any[] = []) => {
    const now = new Date().toISOString();
    let obj = {
        title: t('createChat.newSessionTitle'),
        path: `chat/${sessionId}`,
        id: sessionId,
        isMore: false,
        isNoTitle: true,
        created_at: now,
        updated_at: now
    };
    usemenuStore.updataMenuChildren(obj);
    usemenuStore.changeIsFirstSession(true);
    usemenuStore.changeFirstQuery(value, mentionedItems, modelId, imageFiles, attachmentFiles);
    router.push(`/platform/chat/${sessionId}`);
}

const handleKBEditorSuccess = (kbId: string) => {
    navigateToKnowledgeBaseList(kbId)
}

</script>
<style lang="less" scoped>
.dialogue-wrap {
    flex: 1;
    display: flex;
    justify-content: center;
    align-items: center;
    // position: relative;
}

.dialogue-answers {
    display: flex;
    flex-flow: column;
    align-items: center;
    width: 100%;
    max-width: 800px;
    gap: 24px;

    :deep(.answers-input) {
        position: static;
        transform: translateX(0);
    }
}

.dialogue-title {
    display: flex;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 28px;
    font-weight: 600;
    align-items: center;
    margin-bottom: 0;

    .icon {
        display: flex;
        width: 32px;
        height: 32px;
        justify-content: center;
        align-items: center;
        border-radius: 6px;
        background: var(--td-bg-color-container);
        box-shadow: var(--td-shadow-1);
        margin-right: 12px;

        .logo_img {
            height: 24px;
            width: 24px;
        }
    }
}

@import '../../components/css/suggested-questions.less';

@keyframes skeletonFadeIn {
    from {
        opacity: 0;
    }

    to {
        opacity: 1;
    }
}

.suggested-questions-container {
    max-width: 800px;
    margin: 0;
    padding: 0 16px;
    transition: height 0.35s @suggested-ease;
}

.suggested-questions-inner {
    animation: skeletonFadeIn 0.3s ease-out;
}

.sq-slide-fade-enter-active {
    transition: opacity 0.35s @suggested-ease, transform 0.35s @suggested-ease;
}

.sq-slide-fade-leave-active {
    transition: opacity 0.15s cubic-bezier(0.4, 0, 1, 1),
        transform 0.15s cubic-bezier(0.4, 0, 1, 1);
}

.sq-slide-fade-enter-from {
    opacity: 0;
    transform: translateY(10px);
}

.sq-slide-fade-leave-to {
    opacity: 0;
    transform: translateY(-4px);
}

.suggested-question-card {
    opacity: 0;
    transform: translateY(8px) scale(0.97);
    transition:
        opacity 0.35s @suggested-ease,
        transform 0.35s @suggested-ease,
        background 0.2s @suggested-ease,
        border-color 0.25s @suggested-ease,
        box-shadow 0.25s @suggested-ease;

    &.sq-card-skeleton {
        opacity: 1;
        transform: none;
    }

    &.sq-card-visible {
        opacity: 1;
        transform: translateY(0) scale(1);
    }

    &:not(.sq-card-skeleton):active {
        transform: scale(0.98);
    }

    &.sq-card-visible:active {
        transform: scale(0.98);
    }
}

@media (max-width: 1250px) and (min-width: 1045px) {
    .answers-input {
        transform: translateX(-329px);
    }

    :deep(.t-textarea__inner) {
        width: 654px !important;
    }
}

@media (max-width: 1045px) {
    .answers-input {
        transform: translateX(-250px);
    }

    :deep(.t-textarea__inner) {
        width: 500px !important;
    }
}

@media (max-width: 750px) {
    .answers-input {
        transform: translateX(-250px);
    }

    :deep(.t-textarea__inner) {
        width: 340px !important;
    }
}

@media (max-width: 600px) {
    .answers-input {
        transform: translateX(-250px);
    }

    :deep(.t-textarea__inner) {
        width: 300px !important;
    }
}
</style>
<style lang="less">
.del-menu-popup {
    z-index: 99 !important;

    .t-popup__content {
        width: 100px;
        height: 40px;
        line-height: 30px;
        padding-left: 14px;
        cursor: pointer;
        margin-top: 4px !important;

    }
}
</style>