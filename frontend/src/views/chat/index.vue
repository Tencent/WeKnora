<template>
    <div class="chat" :class="{
        'is-embedded': embeddedMode,
        'is-sidebar-collapsed': uiStore.sidebarCollapsed,
        'has-references-panel': referencesDrawerVisible,
    }">
        <ChatHeader v-if="!embeddedMode" :session="currentSession" :has-references-panel="referencesDrawerVisible" />
        <div ref="scrollContainer" class="chat_scroll_box" @scroll="handleScroll">
            <div class="msg_list" :class="{ 'is-embedded': embeddedMode }">
                <!-- 消息列表骨架屏 -->
                <div v-if="historyLoading && messagesList.length === 0" class="msg-skeleton-list">
                    <div class="msg-skeleton msg-skeleton-user">
                        <t-skeleton animation="gradient" :row-col="[{ width: '45%', height: '36px', type: 'rect' }]" />
                    </div>
                    <div class="msg-skeleton msg-skeleton-bot">
                        <t-skeleton animation="gradient"
                            :row-col="[{ width: '80%', height: '16px' }, { width: '100%', height: '16px' }, { width: '60%', height: '16px' }]" />
                    </div>
                    <div class="msg-skeleton msg-skeleton-user">
                        <t-skeleton animation="gradient" :row-col="[{ width: '35%', height: '36px', type: 'rect' }]" />
                    </div>
                    <div class="msg-skeleton msg-skeleton-bot">
                        <t-skeleton animation="gradient"
                            :row-col="[{ width: '70%', height: '16px' }, { width: '90%', height: '16px' }]" />
                    </div>
                </div>
                <!-- 推荐问题卡片 - 仅在新会话（无消息）时展示 -->
                <div v-if="!embeddedMode && messagesList.length === 0 && !loading" class="suggested-questions-container"
                    :class="{ 'has-questions': suggestedQuestions.length > 0 || suggestedQuestionsLoading }">
                    <!-- 骨架屏占位 -->
                    <div v-if="suggestedQuestionsLoading && suggestedQuestions.length === 0"
                        class="suggested-questions-inner">
                        <div class="suggested-questions-title"><t-skeleton animation="gradient"
                                :row-col="[{ width: '120px', height: '14px' }]" /></div>
                        <div class="suggested-questions-grid">
                            <div v-for="n in 6" :key="'sq-skel-' + n" class="suggested-question-card sq-card-skeleton">
                                <t-skeleton animation="gradient"
                                    :row-col="[{ width: '100%', height: '14px', type: 'rect' }]" />
                            </div>
                        </div>
                    </div>
                    <transition v-else appear name="sq-fade">
                        <div v-if="suggestedQuestions.length > 0" class="suggested-questions-inner">
                            <div class="suggested-questions-title-row">
                                <p class="suggested-questions-caption">
                                    <span class="suggested-questions-title">{{ t('chat.suggestedQuestions') }}</span>
                                    <button type="button" class="suggested-questions-refresh"
                                        :disabled="suggestedQuestionsLoading"
                                        :title="t('chat.refreshSuggestedQuestions')"
                                        :aria-label="t('chat.refreshSuggestedQuestions')"
                                        @click="fetchSuggestedQuestions">
                                        <t-icon :name="suggestedQuestionsLoading ? 'loading' : 'refresh'"
                                            :class="{ 'sq-refresh-spin': suggestedQuestionsLoading }" />
                                    </button>
                                </p>
                            </div>
                            <div class="suggested-questions-grid">
                                <div v-for="(item, index) in suggestedQuestions" :key="item.question"
                                    class="suggested-question-card"
                                    @click="handleSuggestedQuestionClick(item.question)">
                                    <span class="suggested-question-text">{{ item.question }}</span>
                                    <span v-if="item.source === 'faq'" class="suggested-question-badge faq">FAQ</span>
                                </div>
                            </div>
                        </div>
                    </transition>
                </div>
                <!--
                  关键：必须用 session.id 作为 key，不能用 v-for 的索引。
                  向上滚动加载历史时会插入一批消息（push/unshift）到列表，
                  若用索引作 key 会让所有已渲染消息的 key 漂移，触发整个列表的销毁重建
                  （botmsg / AgentStreamDisplay 全部重新挂载、markdown 重新渲染），
                  这是历史加载时白屏 + layout shift 蔓延到 session 列表的根因。
                  仅对极少数尚未拿到 id 的本地占位消息 fallback 到 role+created_at+index。
                -->
                <div v-for="(session, index) in messagesList"
                    :key="session.id || `${session.role}-${session.created_at}-${index}`" class="msg-item-wrapper">

                    <div v-if="session.role == 'user'">
                        <usermsg :content="session.content" :mentioned_items="session.mentioned_items"
                            :images="session.images" :attachments="session.attachments" :embeddedMode="embeddedMode"
                            :session-id="session_id">
                        </usermsg>
                    </div>
                    <div v-if="session.role == 'assistant' && shouldRenderAssistantMessage(session)">
                        <botmsg :content="session.content" :session="session" :session-id="session_id"
                            :user-query="getUserQuery(index)" @scroll-bottom="scrollToBottom"
                            :isFirstEnter="isFirstEnter" :embeddedMode="embeddedMode"
                            :follow-up-loading="Boolean(session.suggestionLoading && !session.suggestionSet?.questions?.length)"
                            @render-complete-change="(ready) => handleAnswerRenderComplete(session, ready)">
                        </botmsg>
                        <FollowUpSuggestions v-if="session.answerFullyRendered && !session.suggestionsDismissed"
                            :suggestion-set="session.suggestionSet"
                            :loading="session.suggestionLoading"
                            :allow-regenerate="session.suggestionSet?.allow_regenerate"
                            @select="(item) => handleFollowUpSelect(session, item)"
                            @regenerate="loadFollowUpSuggestions(session, true, true)"
                            @impression="(set) => recordSuggestionEvent(session, set, 'impression')"
                            @dismiss="(set) => dismissSuggestions(session, set)" />
                    </div>
                </div>
                <div v-if="showGlobalTypingIndicator" class="chat-global-wait" role="status"
                    :aria-label="t('chat.thinkingAlt')">
                    <span class="chat-global-wait__spinner" aria-hidden="true"></span>
                </div>
            </div>
        </div>
        <transition name="scroll-btn-fade">
            <div v-show="userHasScrolledUp" class="scroll-to-bottom-btn" @click="onClickScrollToBottom">
                <t-icon name="chevron-down" size="20px" />
            </div>
        </transition>
        <div class="input-container" :class="{ 'is-embedded': embeddedMode }">
            <InputField ref="inputFieldRef"
                @send-msg="(query, modelId, mentionedItems, imageFiles, attachmentFiles) => sendMsg(query, modelId, mentionedItems, imageFiles, attachmentFiles)"
                @stop-generation="handleStopGeneration" :isReplying="isReplying" :sessionId="session_id"
                :assistantMessageId="currentAssistantMessageId" :embeddedMode="embeddedMode"></InputField>
        </div>
    </div>
    <KnowledgeBaseEditorModal :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
        :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
        @update:visible="(val) => val ? null : uiStore.closeKBEditor()" @success="handleKBEditorSuccess" />
    <ChatReferencesDrawer />
    <ChatAttachmentPreviewDrawer />
</template>
<script setup>
import { storeToRefs } from 'pinia';
import { ref, onMounted, onBeforeMount, onUnmounted, nextTick, watch, reactive, computed } from 'vue';
import { useRoute, onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router';
import InputField from '../../components/Input-field.vue';
import botmsg from './components/botmsg.vue';
import usermsg from './components/usermsg.vue';
import { getMessageList, getSession } from "@/api/chat/index";
import { getSuggestedQuestions } from "@/api/agent/index";
import { deleteTemporaryAttachment, uploadTemporaryAttachment } from '@/api/chat/temporary-attachments';
import { useStream } from '../../api/chat/streame'
import { useMenuStore } from '@/stores/menu';
import { useSettingsStore } from '@/stores/settings';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { useUIStore } from '@/stores/ui';
import KnowledgeBaseEditorModal from '@/views/knowledge/KnowledgeBaseEditorModal.vue';
import { useKnowledgeBaseCreationNavigation } from '@/hooks/useKnowledgeBaseCreationNavigation';
import { useChatStreamHandler } from '@/composables/useChatStreamHandler';
import { useStickyBottomOnResize } from '@/composables/useStickyBottomOnResize';
import { clearCitationChunkCache } from '@/utils/citationChunkCache';
import ChatReferencesDrawer from '@/components/ChatReferencesDrawer.vue';
import ChatAttachmentPreviewDrawer from '@/components/ChatAttachmentPreviewDrawer.vue';
import FollowUpSuggestions from '@/components/chat/FollowUpSuggestions.vue';
import ChatHeader from '@/components/ChatHeader.vue';
import {
    notifySessionMutation,
    SESSION_MUTATION_EVENT,
} from '@/components/sessionMutations';
import {
    ensureMessageSuggestions,
    getMessageSuggestions,
    recordMessageSuggestionEvent,
} from '@/api/message-suggestion';
import { provideChatReferencesDrawer } from '@/composables/useChatReferencesDrawer';
import { provideChatAttachmentPreviewDrawer } from '@/composables/useChatAttachmentPreviewDrawer';

const referencesDrawer = provideChatReferencesDrawer();
provideChatAttachmentPreviewDrawer();
const { visible: referencesDrawerVisible } = referencesDrawer;

const props = defineProps({
    session_id: { type: String, default: '' },
    agentId: { type: String, default: '' },
    kbIds: { type: Array, default: () => [] },
    embeddedMode: { type: Boolean, default: false },
});

const usemenuStore = useMenuStore();
const useSettingsStoreInstance = useSettingsStore();

// Whether the active chat session is using the Agent pipeline (not quick-answer).
const isAgentStreamSession = () => {
    if (props.embeddedMode) {
        return !!(props.agentId && props.agentId !== 'builtin-quick-answer');
    }
    return useSettingsStoreInstance.isAgentStreamMode;
};

const uiStore = useUIStore();
const { navigateToKnowledgeBaseList } = useKnowledgeBaseCreationNavigation();
const { t } = useI18n();
const { firstQuery, firstMentionedItems, firstModelId, firstImageFiles, firstAttachmentFiles } = storeToRefs(usemenuStore);
const { onChunk, error, startStream, stopStream, lastStreamRequest } = useStream();
/** Snapshot of the in-flight HTTP request for attaching to the next assistant message. */
const pendingStreamDebug = ref(null);

const buildStreamDebugPayload = () => {
    const meta = lastStreamRequest.value;
    if (!meta) return null;
    return {
        requestId: meta.requestId,
        url: meta.url,
        method: meta.method,
        body: meta.body,
        sentAt: meta.sentAt,
        sessionId: session_id.value,
    };
};

const attachStreamDebugToMessage = (message) => {
    if (!message) return;
    const payload = pendingStreamDebug.value || buildStreamDebugPayload();
    if (!payload) return;
    if (payload.requestId && !message.request_id) {
        message.request_id = payload.requestId;
    }
    message.debugRequest = payload;
};
const route = useRoute();
const session_id = ref(props.session_id || route.params.chatid);
const currentSession = ref(null);

// 拉 session 详情，并按其 last_request_state 把输入栏状态恢复到当时的发起态。
// 嵌入式（embeddedMode）由宿主页面注入 agent/KB，所以跳过整套恢复逻辑，
// 避免污染宿主的 settings store。
const loadSessionAndHydrate = async (sid) => {
    if (!sid || props.embeddedMode) return;
    try {
        const sessionRes = await getSession(sid);
        if (sessionRes?.data && sid === session_id.value) {
            currentSession.value = sessionRes.data;
            const lastState = sessionRes.data.last_request_state;
            if (lastState) {
                // 先把当前的"全局默认"快照下来，再用 session 状态覆盖；
                // 离开会话时会从快照还原，避免本会话的状态污染新建对话。
                useSettingsStoreInstance.snapshotAsDefaultsIfNeeded();
                useSettingsStoreInstance.applyLastRequestState(lastState);
            }
        }
    } catch (error) {
        console.error('Failed to load session data:', error);
    }
};
const inputFieldRef = ref();
const created_at = ref('');
const limit = ref(20);
const messagesList = reactive([]);
const isReplying = ref(false);
const currentAssistantMessageId = ref(''); // 当前正在生成的 assistant message ID
// True only while attaching to an in-flight *IM-originated* reply via continue-stream.
// Such replies are generated on the IM side and never stream through this server, so
// continue-stream always fails even though the answer is coming — recover by polling
// instead of erroring. Web/api replies are left on the original error path.
const isAttachingImStream = ref(false);
let recoverPollTimer = null;
// True while polling to recover an in-flight IM reply we couldn't stream. Drives
// the same "generating" typing indicator the normal reply path shows, so the wait
// isn't a silent gap. IM-only: false everywhere else, so other flows are unchanged.
const isImRecovering = ref(false);
const scrollLock = ref(false);
const isFirstEnter = ref(true);
const loading = ref(false);
const historyLoading = ref(true);
const historyLoadingMore = ref(false);
const hasMoreHistory = ref(true);
let fullContent = ref('')
const scrollContainer = ref(null)
const userHasScrolledUp = ref(false)
const SCROLL_BOTTOM_THRESHOLD = 80

const isNearBottom = () => {
    if (!scrollContainer.value) return true;
    const { scrollTop, scrollHeight, clientHeight } = scrollContainer.value;
    return scrollHeight - scrollTop - clientHeight < SCROLL_BOTTOM_THRESHOLD;
}

const handleKBEditorSuccess = (kbId) => {
    navigateToKnowledgeBaseList(kbId)
}

// ===== 推荐问题 =====
const suggestedQuestions = ref([]);
const suggestedQuestionsLoading = ref(false);
let suggestedQuestionsFetchId = 0; // 用于取消过时的请求
let suggestedDebounceTimer = null;
let pendingSuggestionAttribution = null;
let pendingSuggestionKnowledgeBaseIds = [];

const cancelSuggestedQuestionsFetch = () => {
    suggestedQuestionsFetchId++;
    suggestedQuestionsLoading.value = false;
    suggestedQuestions.value = [];
    if (suggestedDebounceTimer) {
        clearTimeout(suggestedDebounceTimer);
        suggestedDebounceTimer = null;
    }
};

const fetchSuggestedQuestionsIfNeeded = async () => {
    if (props.embeddedMode) return;
    // 初始历史尚未拉完时不能判断是否有消息，避免有历史的会话误请求推荐问法
    if (historyLoading.value || messagesList.length > 0) {
        if (messagesList.length > 0) {
            cancelSuggestedQuestionsFetch();
        }
        return;
    }
    await fetchSuggestedQuestions();
};

const fetchSuggestedQuestions = async () => {
    if (historyLoading.value || messagesList.length > 0) {
        return;
    }
    const fetchId = ++suggestedQuestionsFetchId;
    suggestedQuestionsLoading.value = true;
    // 加载期间保留旧数据，不清空，避免布局抖动
    try {
        const agentId = useSettingsStoreInstance.selectedAgentId;
        if (!agentId) return;
        const res = await getSuggestedQuestions(agentId, useSettingsStoreInstance.getSuggestedQuestionsParams(6));
        if (fetchId === suggestedQuestionsFetchId) {
            suggestedQuestions.value = res?.data?.questions || [];
        }
    } catch (err) {
        console.warn('[SuggestedQuestions] Failed to fetch:', err);
        if (fetchId === suggestedQuestionsFetchId) {
            suggestedQuestions.value = [];
        }
    } finally {
        if (fetchId === suggestedQuestionsFetchId) {
            suggestedQuestionsLoading.value = false;
        }
    }
};

const handleSuggestedQuestionClick = (question) => {
    if (inputFieldRef.value?.triggerSend) {
        inputFieldRef.value.triggerSend(question);
    } else {
        sendMsg(question);
    }
};

const resolveAssistantMessageId = (message) => message?.id || message?.assistant_message_id;

const handleAnswerRenderComplete = (message, ready) => {
    message.answerFullyRendered = Boolean(ready);
};

const loadFollowUpSuggestions = async (message, ensure = false, regenerate = false) => {
    const messageId = resolveAssistantMessageId(message);
    const targetSessionId = session_id.value;
    if (!messageId || !targetSessionId || message.suggestionsDismissed) return;
    message.suggestionLoading = true;
    try {
        let response = ensure
            ? await ensureMessageSuggestions(targetSessionId, messageId, regenerate)
            : await getMessageSuggestions(targetSessionId, messageId);
        let set = response?.data;
        for (let attempt = 0; set?.status === 'generating' && attempt < 120; attempt++) {
            await new Promise((resolve) => setTimeout(resolve, 1000));
            if (session_id.value !== targetSessionId || message.suggestionsDismissed) return;
            response = await getMessageSuggestions(targetSessionId, messageId);
            set = response?.data;
        }
        message.suggestionSet = set?.status === 'ready' ? set : null;
    } catch (error) {
        if (ensure) console.warn('[FollowUpSuggestions] Failed to generate:', error);
        message.suggestionSet = null;
    } finally {
        message.suggestionLoading = false;
    }
};

const recordSuggestionEvent = (message, set, eventType, questionId = '') => {
    if (!set?.id) return;
    void recordMessageSuggestionEvent(session_id.value, set.id, eventType, questionId).catch(() => undefined);
};

const handleFollowUpSelect = (message, item) => {
    recordSuggestionEvent(message, message.suggestionSet, 'click', item.id);
    pendingSuggestionAttribution = {
        suggestion_set_id: message.suggestionSet.id,
        question_id: item.id,
    };
    // Knowledge-backed follow-ups are generated from a specific KB. Keep that
    // authorized retrieval anchor for the immediate next request; model-backed
    // suggestions intentionally do not inherit transient @file/@tag/MCP/Skill scope.
    pendingSuggestionKnowledgeBaseIds = [...new Set(item.knowledge_base_ids || [])];
    if (inputFieldRef.value?.triggerSend) inputFieldRef.value.triggerSend(item.text);
    else sendMsg(item.text);
};

const dismissSuggestions = (message, set) => {
    message.suggestionsDismissed = true;
    recordSuggestionEvent(message, set, 'dismiss');
};

// 防抖包装，切换知识库/文件时300ms内不重复请求
const debouncedFetchSuggestions = () => {
    if (historyLoading.value || messagesList.length > 0) return;
    if (suggestedDebounceTimer) clearTimeout(suggestedDebounceTimer);
    suggestedDebounceTimer = setTimeout(() => { fetchSuggestedQuestionsIfNeeded(); }, 300);
};

// 监听 Agent / 知识库 / 文件 / 标签 / MCP / Skill @mention，重新获取推荐问题
watch(
    () => ({
        agentId: useSettingsStoreInstance.selectedAgentId,
        kbs: useSettingsStoreInstance.settings.selectedKnowledgeBases,
        files: useSettingsStoreInstance.settings.selectedFiles,
        tags: useSettingsStoreInstance.settings.selectedTags,
        mcps: useSettingsStoreInstance.settings.selectedMCPServices,
        skills: useSettingsStoreInstance.settings.selectedSkills,
    }),
    debouncedFetchSuggestions,
    { deep: true },
);

function fileToBase64(file) {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = reject;
        reader.readAsDataURL(file);
    });
}

const getUserQuery = (index) => {
    if (index <= 0) {
        return '';
    }
    const previous = messagesList[index - 1];
    if (previous && previous.role === 'user') {
        return previous.content || '';
    }
    return '';
};

watch([() => route.params], async (newvalue) => {
    isFirstEnter.value = true;
    if (newvalue[0].chatid) {
        if (!firstQuery.value) {
            scrollLock.value = false;
        }
        messagesList.splice(0);
        session_id.value = newvalue[0].chatid;
        currentSession.value = null;
        clearCitationChunkCache();

        // 切换会话时，重置状态
        historyLoading.value = true;
        historyLoadingMore.value = false;
        hasMoreHistory.value = true;
        created_at.value = '';
        loading.value = false;
        isReplying.value = false;
        currentAssistantMessageId.value = '';
        userHasScrolledUp.value = false;

        // 跨会话切换：先把旧会话覆盖前的全局默认还原，再让新会话重新拍快照
        // 并应用自己的 last_request_state（在 loadSessionAndHydrate 内部完成）。
        useSettingsStoreInstance.restoreDefaultsIfSnapshotted();

        await loadSessionAndHydrate(session_id.value);
        let data = {
            session_id: session_id.value,
            created_at: '',
            limit: limit.value
        }
        getmsgList(data);
    }
});
const scrollToBottom = (force = false) => {
    if (!force && userHasScrolledUp.value) return;
    nextTick(() => {
        if (scrollContainer.value) {
            scrollContainer.value.scrollTop = scrollContainer.value.scrollHeight;
        }
    })
}
const onClickScrollToBottom = () => {
    userHasScrolledUp.value = false;
    scrollToBottom(true);
}

// Images and other rich Markdown content can grow after the SSE chunk that
// introduced them. Follow those delayed height changes while the user remains
// at the live edge; preserve position when they intentionally scroll upward.
useStickyBottomOnResize(scrollContainer, userHasScrolledUp, scrollToBottom);

const debounce = (fn, delay) => {
    let timer
    return (...args) => {
        clearTimeout(timer)
        timer = setTimeout(() => fn(...args), delay)
    }
}
const onChatScrollTop = () => {
    if (scrollLock.value || historyLoadingMore.value || !hasMoreHistory.value) return;
    if (!scrollContainer.value) return;
    const { scrollTop, scrollHeight } = scrollContainer.value;
    isFirstEnter.value = false
    if (scrollTop <= 0) {
        let data = {
            session_id: session_id.value,
            created_at: created_at.value,
            limit: limit.value
        }
        getmsgList(data, true, scrollHeight);
    }
}
const debouncedScrollTop = debounce(onChatScrollTop, 500);
let lastScrollTop = 0;
const handleScroll = () => {
    const el = scrollContainer.value;
    if (el) {
        const currentTop = el.scrollTop;
        // Only an actual upward scroll detaches from the live edge. Content that
        // grows after a chunk (images, diagrams) keeps scrollTop fixed and would
        // otherwise fire a stale scroll event that falsely marks the user as
        // scrolled up, killing the auto-follow during streaming.
        if (currentTop < lastScrollTop - 1) {
            userHasScrolledUp.value = !isNearBottom();
        } else if (isNearBottom()) {
            userHasScrolledUp.value = false;
        }
        lastScrollTop = currentTop;
    }
    debouncedScrollTop();
};

const fetchMessageList = (data) => getMessageList(data);

const {
    findLastMessage,
    shouldRenderAssistantMessage,
    shouldShowGlobalTypingIndicator,
    handleMsgList,
    processStreamChunk,
    prepareForNewOutgoingMessage,
    markInFlightAssistantStopped,
} = useChatStreamHandler({
    messagesList,
    loading,
    isReplying,
    currentAssistantMessageId,
    fullContent,
    isAgentStreamSession,
    scrollToBottom,
    onError: (msg) => MessagePlugin.error(msg),
    preserveIncompleteStreamReactive: true,
    isFirstEnter,
    scrollContainer,
    debug: import.meta.env.DEV,
    onAfterMsgList: async () => {
        for (const message of messagesList) {
            if (message.role === 'assistant' && message.is_completed && message.suggestionSet === undefined) {
                void loadFollowUpSuggestions(message, false);
            }
        }
        const lastMessage = messagesList[messagesList.length - 1];
        if (lastMessage && !lastMessage.is_completed) {
            isReplying.value = true;
            if (lastMessage.role === 'assistant') {
                currentAssistantMessageId.value = lastMessage.id;
                console.log('[Continue Stream] Set assistant message ID:', lastMessage.id);
            }
            // Only IM-originated replies (channel === 'im') get the quiet poll-to-recover
            // path: their answer is generated on the IM side and never streams through
            // this server, so continue-stream always 404s even though the reply *is*
            // coming. Web/api replies keep the original behaviour (a real failure to
            // resume the stream still surfaces as an error) — we don't touch them.
            isAttachingImStream.value = lastMessage.channel === 'im';
            await startStream({
                session_id: session_id.value,
                query: lastMessage.id,
                method: 'GET',
                url: '/api/v1/sessions/continue-stream',
            });
            // On success the stream resumed normally; on failure the error watcher
            // already took over (quiet recovery for IM), so only clear the flag here.
            if (!error.value) isAttachingImStream.value = false;
        }
    },
    onAgentQuery: (data, existingMessage) => {
        pendingStreamDebug.value = buildStreamDebugPayload();
        if (existingMessage) attachStreamDebugToMessage(existingMessage);
    },
    onMessageCreated: (message) => attachStreamDebugToMessage(message),
    onMessageUpdated: (message, payload) => {
        attachStreamDebugToMessage(message);
        if (payload?.is_completed) pendingStreamDebug.value = null;
    },
    onAgentAnswerDone: (message) => {
        attachStreamDebugToMessage(message);
        pendingStreamDebug.value = null;
    },
    onAgentChunkBound: (message) => {
        attachStreamDebugToMessage(message);
        pendingStreamDebug.value = null;
    },
    onTurnComplete: (message) => {
        void loadFollowUpSuggestions(message, true);
    },
});

const showGlobalTypingIndicator = computed(() =>
    shouldShowGlobalTypingIndicator(messagesList, loading.value, isImRecovering.value),
);

const getmsgList = (data, isScrollType = false, scrollHeight) => {
    if (isScrollType) {
        if (historyLoadingMore.value || !hasMoreHistory.value) return;
        historyLoadingMore.value = true;
    }
    fetchMessageList(data).then(async (res) => {
        const batch = res?.data;
        if (!batch?.length) {
            if (isScrollType) {
                hasMoreHistory.value = false;
            }
            return;
        }
        if (!isScrollType) {
            cancelSuggestedQuestionsFetch();
        }
        const nextCursor = batch[0].created_at;
        if (isScrollType && created_at.value && nextCursor === created_at.value) {
            hasMoreHistory.value = false;
            return;
        }
        if (batch.length < limit.value) {
            hasMoreHistory.value = false;
        }
        created_at.value = nextCursor;
        await handleMsgList(batch, isScrollType, scrollHeight);
    }).catch((err) => {
        console.error('Failed to load messages:', err);
        if (isScrollType) {
            hasMoreHistory.value = false;
        }
    }).finally(() => {
        historyLoading.value = false;
        historyLoadingMore.value = false;
        if (!isScrollType && messagesList.length === 0) {
            fetchSuggestedQuestionsIfNeeded();
        }
    })
}

// 发送消息
// 处理停止生成事件 - 立即清除 loading 状态
const handleStopGeneration = () => {
    console.log('[Stop Generation] Immediately clearing loading state');
    stopStream();
    loading.value = false;
    isReplying.value = false;
    // 标记当前 assistant 为已结束，避免下一条 query 复用该消息行
    markInFlightAssistantStopped(currentAssistantMessageId.value);
    // 保留 currentAssistantMessageId，Input-field 仍需用它调用 stop API
};

const sendMsg = async (value, modelId = '', mentionedItems = [], imageFiles = [], attachmentFiles = []) => {
    stopStream();
    prepareForNewOutgoingMessage();
    isReplying.value = true;
    loading.value = true;
    const selectedAgentId = props.embeddedMode ? props.agentId : (useSettingsStoreInstance.selectedAgentId || '');

    // Images are unified with the attachment pipeline: on the authenticated web
    // client they upload as temporary documents (understood in the background by
    // the VLM) and are sent as attachment_ids. The inline base64 `images`
    // payload is kept only for the embedded/public API path. A base64 fallback
    // is used per-image if the async upload fails.
    let imageAttachments = [];
    let userImages = [];
    const imageAttachmentIds = [];
    if (imageFiles && imageFiles.length > 0) {
        for (const file of imageFiles) {
            let dataURI;
            try {
                dataURI = await fileToBase64(file);
            } catch (e) {
                console.error('[Image] Failed to read images:', e);
                loading.value = false;
                isReplying.value = false;
                return;
            }
            userImages.push({ url: dataURI });
            if (props.embeddedMode) {
                imageAttachments.push({ data: dataURI });
                continue;
            }
            try {
                const upload = await uploadTemporaryAttachment(session_id.value, file, selectedAgentId, 'auto');
                imageAttachmentIds.push(upload.data.id);
            } catch (e) {
                console.error('[Image] Temporary image upload failed, falling back to inline:', e);
                imageAttachments.push({ data: dataURI });
            }
        }
    }

    // The create-chat page cannot upload before its session exists. Once it
    // navigates here, move those local files through the same asynchronous
    // upload/parse flow before starting the first stream.
    const localAttachments = (attachmentFiles || []).filter(attachment => !attachment.documentId);
    if (!props.embeddedMode && localAttachments.length > 0) {
        try {
            // Only upload to obtain a document ID; parsing continues in the
            // background and is awaited by the backend (shown on the timeline).
            await Promise.all(localAttachments.map(async (attachment) => {
                attachment.status = 'uploading';
                const upload = await uploadTemporaryAttachment(
                    session_id.value, attachment.file, selectedAgentId, 'auto'
                );
                attachment.documentId = upload.data.id;
                attachment.status = upload.data.status;
            }));
        } catch (error) {
            console.error('[Attachment] Temporary document upload failed:', error);
            await Promise.all(localAttachments
                .filter(attachment => attachment.documentId)
                .map(attachment => deleteTemporaryAttachment(session_id.value, attachment.documentId).catch(() => undefined)));
            MessagePlugin.error(error?.message || t('chat.attachmentParseFailed'));
            loading.value = false;
            isReplying.value = false;
            return;
        }
    }

    // Send any successfully uploaded attachment (parsing may still be running);
    // the backend waits for readiness and reports progress on the timeline.
    const attachmentIds = (attachmentFiles || [])
        .filter(attachment => attachment.documentId && attachment.status !== 'failed')
        .map(attachment => attachment.documentId);
    attachmentIds.push(...imageAttachmentIds);
	// Embedded public routes do not expose the authenticated session upload API;
	// keep their existing inline payload for compatibility.
    const legacyAttachmentFiles = props.embeddedMode
        ? (attachmentFiles || []).filter(attachment => !attachment.documentId)
        : [];
    let attachmentUploads = [];
    if (legacyAttachmentFiles.length > 0) {
        try {
            for (const attachment of legacyAttachmentFiles) {
                const reader = new FileReader();
                const base64Promise = new Promise((resolve, reject) => {
                    reader.onload = () => {
                        const result = reader.result;
                        // Extract base64 content (remove data:...;base64, prefix)
                        const base64 = result.split(',')[1];
                        resolve(base64);
                    };
                    reader.onerror = reject;
                    reader.readAsDataURL(attachment.file);
                });
                const base64Data = await base64Promise;
                attachmentUploads.push({
                    data: base64Data,
                    file_name: attachment.name,
                    file_size: attachment.size
                });
            }
        } catch (e) {
            console.error('[Attachment] Failed to read attachments:', e);
            loading.value = false;
            isReplying.value = false;
            return;
        }
    }

    // 将@提及的知识库和文件信息存入用户消息
    messagesList.push({ content: value, role: 'user', mentioned_items: mentionedItems, images: userImages, attachments: attachmentFiles.map(a => ({ id: a.documentId, file_name: a.name, file_size: a.size, file_type: '.' + a.name.split('.').pop()?.toLowerCase() })), channel: 'web' });
    userHasScrolledUp.value = false;
    scrollToBottom(true);

    // Get agent mode status from settings store (prefer selectedAgentId for builtins)
    const agentEnabled = props.embeddedMode
        ? (props.agentId && props.agentId !== 'builtin-quick-answer')
        : useSettingsStoreInstance.isAgentStreamMode;

    // Get web search status from settings store
    const webSearchEnabled = props.embeddedMode ? false : useSettingsStoreInstance.isWebSearchEnabled;

    // Get knowledge_base_ids from settings store (selected by user via KnowledgeBaseSelector)
    // Merge @mentioned KB/file IDs so retrieval uses the same targets user @mentioned (including shared KBs)
    const sidebarKbIds = props.embeddedMode ? props.kbIds : (useSettingsStoreInstance.settings.selectedKnowledgeBases || []);
    const sidebarFileIds = props.embeddedMode ? [] : (useSettingsStoreInstance.settings.selectedFiles || []);
    const kbIdSet = new Set(sidebarKbIds);
    const fileIdSet = new Set(sidebarFileIds);
    for (const kbId of pendingSuggestionKnowledgeBaseIds) {
        if (kbId) kbIdSet.add(kbId);
    }
    for (const item of mentionedItems || []) {
        if (!item?.id) continue;
        if (item.type === 'kb' && !kbIdSet.has(item.id)) {
            kbIdSet.add(item.id);
        } else if (item.type === 'file' && !fileIdSet.has(item.id)) {
            fileIdSet.add(item.id);
        }
    }
    const kbIds = [...kbIdSet];
    const knowledgeIds = [...fileIdSet];
    const tagIds = [...new Set((mentionedItems || []).filter(item => item.type === 'tag' && item.id).map(item => item.id))];
    const mcpServiceIds = [...new Set((mentionedItems || []).filter(item => item.type === 'mcp' && item.id).map(item => item.id))];
    const skillNames = [...new Set((mentionedItems || []).filter(item => item.type === 'skill' && item.id).map(item => item.skill_name || item.id))];

    const endpoint = agentEnabled ? '/api/v1/agent-chat' : '/api/v1/knowledge-chat';

    const requestMcpServiceIds = agentEnabled ? mcpServiceIds : [];
    const requestSkillNames = agentEnabled ? skillNames : [];

    const suggestionAttribution = pendingSuggestionAttribution;
    pendingSuggestionAttribution = null;
    pendingSuggestionKnowledgeBaseIds = [];
    await startStream({
        session_id: session_id.value,
        knowledge_base_ids: kbIds,
        knowledge_ids: knowledgeIds,
        agent_enabled: agentEnabled,
        agent_id: selectedAgentId,
        web_search_enabled: webSearchEnabled,
        summary_model_id: modelId,
        mcp_service_ids: requestMcpServiceIds,
        skill_names: requestSkillNames,
        tag_ids: tagIds,
        mentioned_items: mentionedItems,
        images: imageAttachments.length > 0 ? imageAttachments : undefined,
        attachment_uploads: attachmentUploads.length > 0 ? attachmentUploads : undefined,
        attachment_ids: attachmentIds.length > 0 ? attachmentIds : undefined,
        query: value,
        suggestion_attribution: suggestionAttribution || undefined,
        method: 'POST',
        url: endpoint,
    });
}

// Quietly recover an in-flight IM reply we couldn't attach to (it's generated on
// the IM side, so it never streamed through this server). Poll until it completes,
// then reload the thread so it renders via the normal path. Bounded so an IM reply
// that genuinely died (e.g. the bot crashed) doesn't spin forever — on timeout we
// surface the original error so the failure isn't hidden.
const RECOVER_POLL_INTERVAL = 2500;
const RECOVER_POLL_MAX_ATTEMPTS = 48; // ~2 min
const recoverIncompleteMessage = () => {
    const targetSession = session_id.value;
    const targetMessageId = currentAssistantMessageId.value;
    if (recoverPollTimer) { clearTimeout(recoverPollTimer); recoverPollTimer = null; }
    if (!targetMessageId) { isReplying.value = false; isImRecovering.value = false; return; }
    isImRecovering.value = true; // show the "generating" indicator while we poll
    let attempts = 0;
    const poll = async () => {
        recoverPollTimer = null;
        if (session_id.value !== targetSession) { isReplying.value = false; isImRecovering.value = false; return; } // navigated away
        attempts++;
        try {
            const res = await getMessageList({ session_id: targetSession, limit: limit.value, created_at: '' });
            const target = (res?.data || []).find((m) => m.id === targetMessageId);
            if (target && target.is_completed) {
                created_at.value = '';
                messagesList.splice(0);
                getmsgList({ session_id: targetSession, limit: limit.value, created_at: '' });
                isReplying.value = false;
                isImRecovering.value = false;
                currentAssistantMessageId.value = '';
                return;
            }
        } catch (e) {
            console.warn('[Continue Stream] recovery poll failed:', e);
        }
        if (attempts >= RECOVER_POLL_MAX_ATTEMPTS) {
            // The IM reply never completed — don't hide it; surface the standard
            // stream-failure message (reuses the existing i18n key, no raw HTTP code).
            MessagePlugin.error(t('error.streamFailed'));
            isReplying.value = false;
            isImRecovering.value = false;
            currentAssistantMessageId.value = '';
            return;
        }
        recoverPollTimer = setTimeout(poll, RECOVER_POLL_INTERVAL);
    };
    recoverPollTimer = setTimeout(poll, RECOVER_POLL_INTERVAL);
};

// Watch for stream errors and show message
watch(error, (newError) => {
    if (!newError) return;
    // A failed attach to an in-flight IM reply isn't a real error — the answer is
    // produced on the IM side and never streams here. Recover quietly by polling to
    // completion instead of flashing a "stream failed" toast. Web/api replies fall
    // through to the normal error toast below, unchanged.
    if (isAttachingImStream.value) {
        isAttachingImStream.value = false;
        recoverIncompleteMessage();
        return;
    }
    MessagePlugin.error(newError);
    isReplying.value = false;
    loading.value = false;
    // 清空当前 assistant message ID
    currentAssistantMessageId.value = '';
});

onChunk((data) => {
    if (data.response_type === 'session_title') {
        const title = data.content || data.data?.title;
        if (title && data.data?.session_id) {
            console.log('[Session Title Update]', {
                session_id: data.data.session_id,
                title: title,
            });
            usemenuStore.updatasessionTitle(data.data.session_id, title);
            usemenuStore.changeIsFirstSession(false);
            notifySessionMutation({
                sessionId: data.data.session_id,
                patch: { title },
            });
        }
        return;
    }
    processStreamChunk(data);
});

const handleSessionMutation = (event) => {
    const detail = event.detail;
    if (detail?.sessionId !== session_id.value) return;

    if (detail.patch) {
        currentSession.value = {
            ...(currentSession.value || { id: session_id.value }),
            ...detail.patch,
        };
    }
    if (detail.messagesCleared) {
        messagesList.splice(0);
        created_at.value = '';
        hasMoreHistory.value = true;
        historyLoadingMore.value = false;
        fetchSuggestedQuestionsIfNeeded();
    }
};

onBeforeMount(async () => {
    // 若从智能体列表点击共享智能体进入，URL 带 agent_id 与 source_tenant_id，同步到 store
    const agentIdFromQuery = props.agentId || (route.query.agent_id && String(route.query.agent_id));
    const sourceTenantIdFromQuery = route.query.source_tenant_id && String(route.query.source_tenant_id);
    if (agentIdFromQuery && sourceTenantIdFromQuery) {
        useSettingsStoreInstance.selectAgent(agentIdFromQuery, sourceTenantIdFromQuery);
    } else if (agentIdFromQuery) {
        useSettingsStoreInstance.selectAgent(agentIdFromQuery, null);
    }

    if (props.kbIds && props.kbIds.length > 0) {
        useSettingsStoreInstance.selectKnowledgeBases(props.kbIds);
    }

    // 必须在 Input-field onMounted 之前完成：按 session.last_request_state 恢复输入栏
    await loadSessionAndHydrate(session_id.value);
});

onMounted(async () => {
    window.addEventListener(SESSION_MUTATION_EVENT, handleSessionMutation);
    messagesList.splice(0);

    // 初始化状态：加载历史消息时不应显示loading
    loading.value = false;
    isReplying.value = false;

    if (firstQuery.value) {
        scrollLock.value = true;
        historyLoading.value = false;
        if (firstModelId.value) {
            useSettingsStoreInstance.updateConversationModels({
                summaryModelId: firstModelId.value,
                selectedChatModelId: firstModelId.value,
                rerankModelId: '',
            });
        }
        sendMsg(firstQuery.value, firstModelId.value || '', firstMentionedItems.value || [], firstImageFiles.value || [], firstAttachmentFiles.value || []);
        usemenuStore.changeFirstQuery('', [], '', [], []);
    } else {
        scrollLock.value = false;
        hasMoreHistory.value = true;
        historyLoadingMore.value = false;
        let data = {
            session_id: session_id.value,
            created_at: '',
            limit: limit.value
        }
        getmsgList(data)
    }
})
const clearData = () => {
    stopStream();
    referencesDrawer.close();
    isReplying.value = false;
    fullContent.value = '';
    // Stop any IM-reply recovery poll for the session we're leaving/switching.
    if (recoverPollTimer) { clearTimeout(recoverPollTimer); recoverPollTimer = null; }
    isImRecovering.value = false;
}
onUnmounted(() => {
    window.removeEventListener(SESSION_MUTATION_EVENT, handleSessionMutation);
    if (recoverPollTimer) { clearTimeout(recoverPollTimer); recoverPollTimer = null; }
});
onBeforeRouteLeave((to, from, next) => {
    clearData()
    // 离开聊天会话 → 还原"用户全局默认"，避免旧会话的请求态泄漏到新建对话。
    useSettingsStoreInstance.restoreDefaultsIfSnapshotted();
    next()
})
onBeforeRouteUpdate((to, from, next) => {
    clearData()
    // 仅"会话 → 会话"会落到这里；跨会话覆盖的还原放到 route.params 的 watch 里，
    // 因为新会话的 getSession 也在那边触发，便于保证 restore→snapshot→apply 顺序。
    next()
})
</script>
<style lang="less" scoped>
.chat {
    font-size: 20px;
    // 右侧不留 padding，滚动条贴到内容区最右缘
    padding: 0 0 20px 20px;
    box-sizing: border-box;
    flex: 1;
    // The parent .platform-route-outlet is a flex column with min-height:0
    // and overflow:hidden — we also need min-height:0 here so that our
    // own flex:1 child (.chat_scroll_box) can shrink below its content
    // height and scroll instead of pushing .input-container out of view.
    min-height: 0;
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    max-width: calc(100vw - 260px);
    min-width: 400px;

    &.is-sidebar-collapsed {
        max-width: calc(100vw - 60px);
    }

    &.is-embedded {
        max-width: 100%;
        min-width: 100%;
        padding: 0;
        overflow-x: hidden;
    }

    &:not(.is-embedded) {
        @media (min-width: 960px) {
            transition: padding-right 0.3s cubic-bezier(0.22, 0.61, 0.36, 1);
        }
    }

    &.has-references-panel:not(.is-embedded) {
        @media (min-width: 960px) {
            padding-right: 420px;
            box-sizing: border-box;

            .chat_scroll_box {
                padding-top: 0;
            }
        }
    }

    &.is-embedded :deep(.answers-input) {
        position: relative;
        transform: translateX(0);
        width: 100%;
        left: 0;
        bottom: auto;
        display: flex;
        justify-content: center;
    }

    &.is-embedded :deep(.control-bar) {
        justify-content: flex-end;
    }

    &:not(.is-embedded) :deep(.answers-input) {
        position: static;
        transform: translateX(0);

        .t-textarea__inner {
            width: 100% !important;
        }
    }

    &.is-embedded :deep(.answers-input) .t-textarea__inner {
        width: 100% !important;
        min-height: 48px !important;
        padding: 10px 14px 48px 14px;
    }
}

.chat_scroll_box {
    flex: 1;
    // Without min-height: 0, a flex-column child defaults to min-height: auto
    // and expands to fit all inner content. When there are many messages,
    // that pushes .input-container out of the viewport. Clamping min-height
    // to 0 lets overflow-y: auto take effect so the messages scroll inside
    // this box instead of stretching it.
    min-height: 0;
    width: 100%;
    padding-top: 8px;
    box-sizing: border-box;
    overflow-y: auto;
    // 使用系统原生滚动条（macOS 滚动时自动显示 overlay 滚动条，类似 ChatGPT）
    scrollbar-width: auto;
    scrollbar-color: auto;
}

// 深色模式下 theme.css 对 * 做了 webkit 滚动条着色，这里恢复为系统默认
:global(:root[theme-mode="dark"]) .chat_scroll_box {
    &::-webkit-scrollbar-thumb {
        background-color: initial !important;
    }

    &::-webkit-scrollbar-thumb:hover {
        background-color: initial !important;
    }

    &::-webkit-scrollbar-track {
        background-color: initial !important;
    }
}

.scroll-to-bottom-btn {
    position: absolute;
    left: 50%;
    transform: translateX(-50%);
    bottom: 140px;
    z-index: 10;
    width: 36px;
    height: 36px;
    border-radius: 50%;
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.1);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    color: var(--td-text-color-secondary);
    transition: all 0.2s ease;

    &:hover {
        background: var(--td-bg-color-container-hover);
        color: var(--td-text-color-primary);
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
    }

    &:active {
        transform: translateX(-50%) scale(0.92);
    }
}

.scroll-btn-fade-enter-active,
.scroll-btn-fade-leave-active {
    transition: opacity 0.2s ease, transform 0.2s ease;
}

.scroll-btn-fade-enter-from,
.scroll-btn-fade-leave-to {
    opacity: 0;
    transform: translateX(-50%) translateY(8px);
}

@keyframes contentFadeIn {
    from {
        opacity: 0;
        transform: translateY(6px);
    }

    to {
        opacity: 1;
        transform: translateY(0);
    }
}

.msg-skeleton-list {
    display: flex;
    flex-direction: column;
    gap: 20px;
    max-width: 960px;
    padding: 16px 0;
    animation: contentFadeIn 0.3s ease-out;
}

.msg-skeleton-user {
    display: flex;
    justify-content: flex-end;
}

.msg-skeleton-bot {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-left: 4px;
}

.input-container {
    min-height: 115px;
    flex-shrink: 0;
    margin: 0 auto;
    width: 100%;
    max-width: 960px;
    box-sizing: border-box;
    position: relative;

    &.is-embedded {
        max-width: 100%;
        width: 100%;
        margin: 0;
        padding: 12px 16px 16px;
        min-height: auto;
        box-sizing: border-box;
        overflow-x: hidden;
    }
}

.msg_list {
    display: flex;
    flex-direction: column;
    gap: 16px;
    max-width: 960px;
    flex: 1;
    margin: 0 auto;
    width: 100%;

    /*
      给每条消息加 layout/style containment：
      - 一条消息的内部布局变化不再让浏览器去 invalidate 整个文档，
        这是修掉"hover 到 session 列表也变白"那个问题的关键。
      - 不要再用 content-visibility: auto / contain-intrinsic-size：
        agent 消息真实高度差异巨大（几百 ~ 数千 px），估的占位高度会让消息进入视口时
        反复发生"占位 -> 真实高度"的大幅 layout shift + 首次 paint 滞后，
        反而在向上滚动时制造"未画完"的白屏闪烁。
        当前 handleMsgList 全流程 ~50ms，根本无需跳过渲染，老老实实正常渲染最稳。
      - 不开 contain: paint：AgentStreamDisplay 里有 tooltip / popover 等会溢出的浮层，
        paint containment 会把它们裁掉。
    */
    .msg-item-wrapper {
        contain: layout style;
    }

    .botanswer_laoding_gif {
        width: 24px;
        height: 18px;
        margin-left: 16px;
    }

    .chat-global-wait {
        display: flex;
        align-items: center;
        min-height: 28px;
        padding-left: 4px;
    }

    .chat-global-wait__spinner {
        width: 12px;
        height: 12px;
        box-sizing: border-box;
        border: 1.5px solid var(--td-component-stroke);
        border-top-color: var(--td-text-color-secondary);
        border-radius: 50%;
        animation: chatGlobalWaitSpin 0.8s linear infinite;
    }
}

@keyframes chatGlobalWaitSpin {
    to {
        transform: rotate(360deg);
    }
}

@media (prefers-reduced-motion: reduce) {
    .chat-global-wait__spinner {
        animation: none;
    }
}

@import '../../components/css/suggested-questions.less';

.suggested-questions-container {
    transition: min-height 0.3s @suggested-ease;
}

.suggested-questions-inner {
    animation: contentFadeIn 0.3s ease-out;
}

.sq-fade-enter-active,
.sq-fade-leave-active {
    transition: opacity 0.25s @suggested-ease;
}

.sq-fade-enter-from,
.sq-fade-leave-to {
    opacity: 0;
}
</style>
