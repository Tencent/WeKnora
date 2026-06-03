<template>
    <div class="chat" :class="{ 'is-embedded': embeddedMode, 'is-sidebar-collapsed': uiStore.sidebarCollapsed }">
        <div ref="scrollContainer" class="chat_scroll_box" @scroll="handleScroll">
            <div class="msg_list" :class="{ 'is-embedded': embeddedMode }">
                <!-- 消息列表骨架屏 -->
                <div v-if="historyLoading && messagesList.length === 0" class="msg-skeleton-list">
                    <div class="msg-skeleton msg-skeleton-user">
                        <t-skeleton animation="gradient" :row-col="[{ width: '45%', height: '36px', type: 'rect' }]" />
                    </div>
                    <div class="msg-skeleton msg-skeleton-bot">
                        <t-skeleton animation="gradient" :row-col="[{ width: '80%', height: '16px' }, { width: '100%', height: '16px' }, { width: '60%', height: '16px' }]" />
                    </div>
                    <div class="msg-skeleton msg-skeleton-user">
                        <t-skeleton animation="gradient" :row-col="[{ width: '35%', height: '36px', type: 'rect' }]" />
                    </div>
                    <div class="msg-skeleton msg-skeleton-bot">
                        <t-skeleton animation="gradient" :row-col="[{ width: '70%', height: '16px' }, { width: '90%', height: '16px' }]" />
                    </div>
                </div>
                <!-- 推荐问题卡片 - 仅在新会话（无消息）时展示 -->
                <div v-if="messagesList.length === 0 && !loading" class="suggested-questions-container" :class="{ 'has-questions': suggestedQuestions.length > 0 || suggestedQuestionsLoading }">
                    <!-- 骨架屏占位 -->
                    <div v-if="suggestedQuestionsLoading && suggestedQuestions.length === 0" class="suggested-questions-inner">
                        <div class="suggested-questions-title"><t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '18px' }]" /></div>
                        <div class="suggested-questions-grid">
                            <div v-for="n in 6" :key="'sq-skel-'+n" class="suggested-question-card sq-card-skeleton">
                                <t-skeleton animation="gradient" :row-col="[{ width: '90%', height: '14px' }, { width: '60%', height: '14px' }]" />
                            </div>
                        </div>
                    </div>
                    <transition v-else appear name="sq-fade">
                        <div v-if="suggestedQuestions.length > 0" class="suggested-questions-inner">
                            <div class="suggested-questions-title">{{ t('chat.suggestedQuestions') }}</div>
                            <div class="suggested-questions-grid">
                                <div
                                    v-for="(item, index) in suggestedQuestions"
                                    :key="item.question"
                                    class="suggested-question-card"
                                    @click="handleSuggestedQuestionClick(item.question)"
                                >
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
                <div
                    v-for="(session, index) in messagesList"
                    :key="session.id || `${session.role}-${session.created_at}-${index}`"
                    class="msg-item-wrapper"
                >

                    <div v-if="session.role == 'user'">
                        <usermsg :content="session.content" :mentioned_items="session.mentioned_items" :images="session.images" :attachments="session.attachments" :embeddedMode="embeddedMode"></usermsg>
                    </div>
                    <div v-if="session.role == 'assistant' && shouldRenderAssistantMessage(session)">
                        <botmsg :content="session.content" :session="session" :session-id="session_id"
                            :user-query="getUserQuery(index)" @scroll-bottom="scrollToBottom"
                            :isFirstEnter="isFirstEnter" :embeddedMode="embeddedMode"></botmsg>
                    </div>
                </div>
                <div v-if="loading"
                    style="height: 41px;display: flex;align-items: center;padding-left: 4px;">
                    <div class="loading-typing">
                        <span></span>
                        <span></span>
                        <span></span>
                    </div>
                </div>
            </div>
        </div>
        <transition name="scroll-btn-fade">
            <div v-show="userHasScrolledUp" class="scroll-to-bottom-btn" @click="onClickScrollToBottom">
                <t-icon name="chevron-down" size="20px" />
            </div>
        </transition>
        <div class="input-container" :class="{ 'is-embedded': embeddedMode }">
            <InputField
                ref="inputFieldRef"
                @send-msg="(query, modelId, mentionedItems, imageFiles, attachmentFiles) => sendMsg(query, modelId, mentionedItems, imageFiles, attachmentFiles)"
                @stop-generation="handleStopGeneration"
                :isReplying="isReplying"
                :sessionId="session_id"
                :assistantMessageId="currentAssistantMessageId"
                :embeddedMode="embeddedMode"
            ></InputField>
        </div>
    </div>
    <KnowledgeBaseEditorModal 
        :visible="uiStore.showKBEditorModal"
        :mode="uiStore.kbEditorMode"
        :kb-id="uiStore.currentKBId || undefined"
        :initial-type="uiStore.kbEditorType"
        @update:visible="(val) => val ? null : uiStore.closeKBEditor()"
        @success="handleKBEditorSuccess"
    />
</template>
<script setup>
import { storeToRefs } from 'pinia';
import { ref, onMounted, onUnmounted, nextTick, watch, reactive, markRaw, onBeforeUnmount, defineProps } from 'vue';
import { useRoute, useRouter, onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router';
import InputField from '../../components/Input-field.vue';
import botmsg from './components/botmsg.vue';
import usermsg from './components/usermsg.vue';
import { getMessageList, generateSessionsTitle, getSession } from "@/api/chat/index";
import { getSuggestedQuestions } from "@/api/agent/index";
import { useStream } from '../../api/chat/streame'
import { useMenuStore } from '@/stores/menu';
import { useSettingsStore } from '@/stores/settings';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { useUIStore } from '@/stores/ui';
import KnowledgeBaseEditorModal from '@/views/knowledge/KnowledgeBaseEditorModal.vue';
import { useKnowledgeBaseCreationNavigation } from '@/hooks/useKnowledgeBaseCreationNavigation';

const props = defineProps({
  session_id: { type: String, default: '' },
  agentId: { type: String, default: '' },
  kbIds: { type: Array, default: () => [] },
  embeddedMode: { type: Boolean, default: false }
});

const usemenuStore = useMenuStore();
const useSettingsStoreInstance = useSettingsStore();

// Whether the active chat session is using the Agent pipeline (not quick-answer).
const isAgentStreamSession = () => {
    if (props.embeddedMode) {
        return !!(props.agentId && props.agentId !== 'builtin-quick-answer');
    }
    return useSettingsStoreInstance.isAgentEnabled;
};

const ensureAgentMessageShell = (message, requestId) => {
    message.isAgentMode = true;
    if (!message.agentEventStream) message.agentEventStream = [];
    if (!message._eventMap) message._eventMap = new Map();
    if (!message._pendingToolCalls) message._pendingToolCalls = new Map();
    if (requestId) {
        if (!message.id) message.id = requestId;
        if (!message.request_id) message.request_id = requestId;
    }
};

// Agent 占位消息在 agent_query 时仅有空 eventStream。若立刻渲染 botmsg，会把列表
// 底部的全局 Loading 顶下去造成跳动；等有首个事件再渲染，Loading 位置保持稳定。
const shouldRenderAssistantMessage = (session) => {
    if (!session?.isAgentMode) return true;
    const stream = session.agentEventStream;
    return Array.isArray(stream) && stream.length > 0;
};
const uiStore = useUIStore();
const { navigateToKnowledgeBaseList } = useKnowledgeBaseCreationNavigation();
const { t } = useI18n();
const { menuArr, isFirstSession, firstQuery, firstMentionedItems, firstModelId, firstImageFiles, firstAttachmentFiles } = storeToRefs(usemenuStore);
const { output, onChunk, isStreaming, isLoading, error, startStream, stopStream, lastStreamRequest } = useStream();
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
const router = useRouter();
const session_id = ref(props.session_id || route.params.chatid);
const sessionData = ref(null);

// 拉 session 详情，并按其 last_request_state 把输入栏状态恢复到当时的发起态。
// 嵌入式（embeddedMode）由宿主页面注入 agent/KB，所以跳过整套恢复逻辑，
// 避免污染宿主的 settings store。
const loadSessionAndHydrate = async (sid) => {
    if (!sid || props.embeddedMode) return;
    try {
        const sessionRes = await getSession(sid);
        if (sessionRes?.data) {
            sessionData.value = sessionRes.data;
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
const scrollLock = ref(false);
const isNeedTitle = ref(false);
const isFirstEnter = ref(true);
const loading = ref(false);
const historyLoading = ref(true);
const historyLoadingMore = ref(false);
const hasMoreHistory = ref(true);
let fullContent = ref('')
let userquery = ref('')
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

const fetchSuggestedQuestions = async () => {
    const fetchId = ++suggestedQuestionsFetchId;
    suggestedQuestionsLoading.value = true;
    // 加载期间保留旧数据，不清空，避免布局抖动
    try {
        const agentId = props.embeddedMode ? props.agentId : useSettingsStoreInstance.selectedAgentId;
        if (!agentId) return;
        const selectedKBs = props.embeddedMode ? props.kbIds : useSettingsStoreInstance.getSelectedKnowledgeBases();
        const selectedFiles = props.embeddedMode ? [] : useSettingsStoreInstance.getSelectedFiles();
        const res = await getSuggestedQuestions(agentId, {
            knowledge_base_ids: selectedKBs.length > 0 ? selectedKBs : undefined,
            knowledge_ids: selectedFiles.length > 0 ? selectedFiles : undefined,
            limit: 6,
        });
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

// 防抖包装，切换知识库/文件时300ms内不重复请求
const debouncedFetchSuggestions = () => {
    if (suggestedDebounceTimer) clearTimeout(suggestedDebounceTimer);
    suggestedDebounceTimer = setTimeout(() => { fetchSuggestedQuestions(); }, 300);
};

// 监听 Agent / 知识库 / 文件切换，重新获取推荐问题
watch(
    () => useSettingsStoreInstance.selectedAgentId,
    debouncedFetchSuggestions,
);
watch(
    () => useSettingsStoreInstance.settings.selectedKnowledgeBases,
    debouncedFetchSuggestions,
    { deep: true },
);
watch(
    () => useSettingsStoreInstance.settings.selectedFiles,
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

const findLastMessage = (predicate) => {
    for (let i = messagesList.length - 1; i >= 0; i--) {
        const item = messagesList[i];
        if (predicate(item)) {
            return item;
        }
    }
    return undefined;
};
watch([() => route.params], (newvalue) => {
    isFirstEnter.value = true;
    if (newvalue[0].chatid) {
        if (!firstQuery.value) {
            scrollLock.value = false;
        }
        messagesList.splice(0);
        session_id.value = newvalue[0].chatid;
        
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

        checkmenuTitle(session_id.value)
        loadSessionAndHydrate(session_id.value);
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
const handleScroll = () => {
    userHasScrolledUp.value = !isNearBottom();
    debouncedScrollTop();
};

const getmsgList = (data, isScrollType = false, scrollHeight) => {
    if (isScrollType) {
        if (historyLoadingMore.value || !hasMoreHistory.value) return;
        historyLoadingMore.value = true;
    }
    getMessageList(data).then(res => {
        const batch = res?.data;
        if (!batch?.length) {
            if (isScrollType) {
                hasMoreHistory.value = false;
            }
            return;
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
        handleMsgList(batch, isScrollType, scrollHeight);
    }).catch((err) => {
        console.error('Failed to load messages:', err);
        if (isScrollType) {
            hasMoreHistory.value = false;
        }
    }).finally(() => {
        historyLoading.value = false;
        historyLoadingMore.value = false;
    })
}

// Recompose the visible answer from the agent event stream: concatenate every
// non-superseded `answer` event, in arrival order. Superseded events are
// per-round preambles that were retracted from the answer area (and relocated
// into the steps tree), so they must not leak into message.content.
const recomposeAgentAnswer = (message) => {
    if (!message.agentEventStream) return '';
    let out = '';
    for (const e of message.agentEventStream) {
        if (e.type === 'answer' && !e.superseded && e.content) {
            out += e.content;
        }
    }
    return out;
};

// Reconstruct agentEventStream from agent_steps stored in database
// This allows the frontend to restore the exact conversation state including all agent reasoning steps
const reconstructEventStreamFromSteps = (agentSteps, messageContent, isCompleted = false, isFallback = false, agentDurationMs = 0) => {
    const events = [];

    // Process agent steps if they exist
    if (agentSteps && Array.isArray(agentSteps) && agentSteps.length > 0) {
    agentSteps.forEach((step) => {
        // Compute step timestamp (milliseconds) from step.timestamp if available
        const stepTimestamp = step.timestamp ? new Date(step.timestamp).getTime() : 0;

        const hasToolCalls = step.tool_calls && Array.isArray(step.tool_calls) && step.tool_calls.length > 0;

        // Mirror what the user saw live, as two channels of one round (same
        // order as live: reasoning streams before the plain-content preamble):
        //   - `reasoning_content` (e.g. DeepSeek / MiMo thinking-mode) → the
        //     thinking card body.
        //   - plain `content` narration (`step.thought`) → only a preamble when
        //     this round went on to call tools. We replay it as a *superseded*
        //     answer event, exactly like the live supersede signal, so
        //     buildFullEventList relocates it into this round's thinking card
        //     (as its title) instead of the answer area. For a terminal round
        //     (no tool calls) `step.thought` is the final answer itself and
        //     already lives in messageContent, so we never duplicate it here.
        const reasoningText = step.reasoning_content && step.reasoning_content.trim()
            ? step.reasoning_content
            : '';
        if (reasoningText) {
            events.push({
                type: 'thinking',
                event_id: `step-${step.iteration}-thought`,
                content: reasoningText,
                done: true,
                thinking: false,
                timestamp: stepTimestamp || undefined,
                duration_ms: step.duration || undefined,
            });
        }
        const preambleText = step.thought && step.thought.trim() ? step.thought : '';
        if (preambleText && hasToolCalls) {
            events.push({
                type: 'answer',
                event_id: `step-${step.iteration}-preamble`,
                content: preambleText,
                done: true,
                superseded: true,
                timestamp: stepTimestamp || undefined,
            });
        }

        // Add tool call and result events. Legacy histories may still contain a
        // final_answer tool call (the tool has since been removed); skip it since
        // its content is replayed as the answer event.
        if (step.tool_calls && Array.isArray(step.tool_calls)) {
            step.tool_calls.forEach((toolCall) => {
                if (toolCall.name === 'final_answer') return; // legacy data — shown as answer event
                events.push({
                    type: 'tool_call',
                    tool_call_id: toolCall.id,
                    tool_name: toolCall.name,
                    arguments: toolCall.args,
                    pending: false,
                    success: toolCall.result?.success !== false,
                    output: toolCall.result?.output || '',
                    error: toolCall.result?.error || undefined,
                    timestamp: stepTimestamp || undefined,
                    // Use both duration and duration_ms for compatibility
                    duration: toolCall.duration,
                    duration_ms: toolCall.duration,
                    display_type: toolCall.result?.data?.display_type,
                    tool_data: toolCall.result?.data,
                });
            });
        }
    });
    }
    
    // Add agent_complete event with duration info (before answer event)
    if (agentDurationMs > 0) {
        events.push({
            type: 'agent_complete',
            total_duration_ms: agentDurationMs,
        });
    }

    // 总是添加 answer 事件如果有内容（无论是否有 agent_steps）
    // 这样可以确保最终答案始终被渲染
    if (messageContent && messageContent.trim()) {
        const answerEvent = {
            type: 'answer',
            content: messageContent,
            done: true
        };
        if (isFallback) answerEvent.is_fallback = true;
        events.push(answerEvent);
    } else if (isCompleted) {
        // 消息已完成但 content 为空：说明是"停止时尚未产出最终答案"的场景。
        // Push 一个 stop 事件，让 AgentStreamDisplay 的 isConversationDone 返回 true，
        // 但不产生 answer 内容，也就不会渲染最终答案的 toolbar。与实时 stop 分支保持一致。
        events.push({
            type: 'stop',
            timestamp: Date.now(),
            reason: 'user_requested'
        });
    }
    
    return events;
};
const handleMsgList = async (data, isScrollType = false, newScrollHeight) => {
    // API 返回 created_at 升序（同秒时 user 在 assistant 前），保持该顺序渲染。
    const chatlist = [...data];
    const existingIds = new Set(messagesList.map(m => m.id).filter(Boolean));
    const processed = [];
    for (let i = 0, len = chatlist.length; i < len; i++) {
        let item = chatlist[i];
        if (item.id && existingIds.has(item.id)) {
            continue;
        }
        if (item.id) {
            existingIds.add(item.id);
        }
        item.isAgentMode = false; // Agent 模式标记
        // 历史消息的 agent_steps / agentEventStream 体量大、嵌套深，且是只读的，
        // 用 markRaw 跳过 Vue 的深响应式转换，避免一次性 unshift 多条时主线程被 Proxy 转换卡住造成白屏。
        //
        // 例外：最后一条「未完成」的消息会通过 continue-stream 继续增量推流，
        // handleAgentChunk 会持续 push/改写 agentEventStream、_eventMap、_pendingToolCalls。
        // 若对它 markRaw，Vue 不会追踪这些变更，前端就「后台在推但不渲染」（只能看到刷新前的快照），
        // 直到生成结束再次刷新才从 agent_steps 静态重建出完整内容。因此这条保持响应式。
        const willContinueStream = !item.is_completed;
        if (willContinueStream) {
            item.agentEventStream = item.agentEventStream || [];
            item._eventMap = new Map();
            item._pendingToolCalls = new Map();
        } else {
            item.agent_steps = item.agent_steps ? markRaw(item.agent_steps) : item.agent_steps;
            item.agentEventStream = markRaw(item.agentEventStream || []);
            item._eventMap = markRaw(new Map());
            item._pendingToolCalls = markRaw(new Map());
        }

        // Check if this message has agent_steps from database (historical agent conversation)
        // If so, reconstruct the agentEventStream to restore the exact conversation state
        if (item.agent_steps && Array.isArray(item.agent_steps) && item.agent_steps.length > 0) {
            item.isAgentMode = true;
            item.agentEventStream = markRaw(reconstructEventStreamFromSteps(item.agent_steps, item.content, item.is_completed, item.is_fallback, item.agent_duration_ms || 0));
            // 隐藏最终答案内容，因为它已经包含在 agentEventStream 的 answer 事件中
            item.hideContent = true;
        }
        
        if (item.content) {
            if (!item.content.includes('<think>') && !item.content.includes('<\/think>')) {
                item.thinkContent = "";
                item.content = item.content;
                item.showThink = false;
                item.thinking = false;
            } else if (item.content.includes('<\/think>')) {
                // 历史消息中包含完整的 <think>...</think> 标签，说明 thinking 已完成
                item.showThink = true;
                item.thinking = false;  // 关键：标记 thinking 已完成，使 deepThink 默认折叠
                const index = item.content.trim().lastIndexOf('<\/think>');
                item.thinkContent = item.content.trim().substring(0, index).replace('<think>', '').trim();
                item.content = item.content.trim().substring(index + 8);
            } else if (item.content.includes('<think>')) {
                // 内容包含 <think> 但没有 </think>，说明 thinking 还在进行中（不太可能出现在历史消息中）
                item.showThink = true;
                item.thinking = true;
                item.thinkContent = item.content.replace('<think>', '').trim();
                item.content = '';
            }
        }
        
        // 非 Agent 模式下若 content 为空（例如用户停止时尚未产出任何文字），
        // 保持为空；botmsg.vue 会因 hasActualContent=false 不渲染内容区和 toolbar。
        // 此前这里会兜底为 "chat.cannotAnswer"，会让停止场景显示误导性文案并出现复制按钮。
        processed.push(item);
    }
    if (processed.length > 0) {
        if (isScrollType) {
            // 逆序逐个 unshift，才能保持 user → assistant 的对话顺序。
            for (let i = processed.length - 1; i >= 0; i--) {
                messagesList.unshift(processed[i]);
            }
        } else {
            messagesList.push(...processed);
        }
    }
    if (isFirstEnter.value) {
        scrollToBottom(true);
    } else if (isScrollType && scrollContainer.value && typeof newScrollHeight === 'number') {
        nextTick(() => {
            if (!scrollContainer.value) return;
            const { scrollHeight } = scrollContainer.value;
            scrollContainer.value.scrollTop = scrollHeight - newScrollHeight;
        });
    }
    if (messagesList[messagesList.length - 1] && !messagesList[messagesList.length - 1].is_completed) {
        isReplying.value = true;
        // 保存正在 stream 的消息 ID，以便停止时使用
        const lastMessage = messagesList[messagesList.length - 1];
        if (lastMessage.role === 'assistant') {
            currentAssistantMessageId.value = lastMessage.id;
            console.log('[Continue Stream] Set assistant message ID:', lastMessage.id);
        }
        await startStream({ session_id: session_id.value, query: lastMessage.id, method: 'GET', url: '/api/v1/sessions/continue-stream' });
    }

}
const checkmenuTitle = (session_id) => {
    menuArr.value[1].children?.forEach(item => {
        if (item.id == session_id) {
            isNeedTitle.value = item.isNoTitle;
        }
    });
}
// 发送消息
// 处理停止生成事件 - 立即清除 loading 状态
const handleStopGeneration = () => {
    console.log('[Stop Generation] Immediately clearing loading state');
    loading.value = false;
    isReplying.value = false;
    // 注意：不在这里清空 currentAssistantMessageId，因为需要它来调用 API
    // API 调用成功后，后端的 stop 事件会清空它
};

const sendMsg = async (value, modelId = '', mentionedItems = [], imageFiles = [], attachmentFiles = []) => {
    userquery.value = value;
    isReplying.value = true;
    loading.value = true;

    // Convert images to base64 data URIs for backend processing and local display
    let imageAttachments = [];
    let userImages = [];
    if (imageFiles && imageFiles.length > 0) {
        try {
            for (const file of imageFiles) {
                const dataURI = await fileToBase64(file);
                imageAttachments.push({ data: dataURI });
                userImages.push({ url: dataURI });
            }
        } catch (e) {
            console.error('[Image] Failed to read images:', e);
            loading.value = false;
            isReplying.value = false;
            return;
        }
    }

    // Convert attachment files to base64 for backend processing
    let attachmentUploads = [];
    if (attachmentFiles && attachmentFiles.length > 0) {
        try {
            for (const attachment of attachmentFiles) {
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
     messagesList.push({ content: value, role: 'user', mentioned_items: mentionedItems, images: userImages, attachments: attachmentFiles.map(a => ({ file_name: a.name, file_size: a.size, file_type: '.' + a.name.split('.').pop()?.toLowerCase() })), channel: 'web' });
    userHasScrolledUp.value = false;
    scrollToBottom(true);
    
    // Get agent mode status from settings store
    const agentEnabled = props.embeddedMode ? (props.agentId && props.agentId !== 'builtin-quick-answer') : useSettingsStoreInstance.isAgentEnabled;
    
    // Get web search status from settings store
    const webSearchEnabled = props.embeddedMode ? false : useSettingsStoreInstance.isWebSearchEnabled;
    
    // Memory toggle is now a server-side per-user preference (see PUT
    // /auth/me/preferences). For the normal logged-in chat we leave the
    // field unset so the backend reads `user.preferences.enable_memory`;
    // for embedded widgets we still send an explicit `false` so a user's
    // personal "memory on" setting doesn't leak into a KB-embed context.
    const enableMemoryOverride = props.embeddedMode ? false : undefined;
    
    // Get knowledge_base_ids from settings store (selected by user via KnowledgeBaseSelector)
    // Merge @mentioned KB/file IDs so retrieval uses the same targets user @mentioned (including shared KBs)
    const sidebarKbIds = props.embeddedMode ? props.kbIds : (useSettingsStoreInstance.settings.selectedKnowledgeBases || []);
    const sidebarFileIds = props.embeddedMode ? [] : (useSettingsStoreInstance.settings.selectedFiles || []);
    const kbIdSet = new Set(sidebarKbIds);
    const fileIdSet = new Set(sidebarFileIds);
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

    // Get selected agent ID (backend resolves shared agent and its tenant from share relation)
    const selectedAgentId = props.embeddedMode ? props.agentId : (useSettingsStoreInstance.selectedAgentId || '');

    // Use agent-chat endpoint when agent is enabled, otherwise use knowledge-chat
    const endpoint = agentEnabled ? '/api/v1/agent-chat' : '/api/v1/knowledge-chat';
    
    // Get selected MCP services from settings store (if available)
    const mcpServiceIds = props.embeddedMode ? [] : (useSettingsStoreInstance.settings.selectedMCPServices || []);
    
    await startStream({ 
        session_id: session_id.value, 
        knowledge_base_ids: kbIds,
        knowledge_ids: knowledgeIds,
        agent_enabled: agentEnabled,
        agent_id: selectedAgentId,
        web_search_enabled: webSearchEnabled,
        enable_memory: enableMemoryOverride,
        summary_model_id: modelId,
        mcp_service_ids: mcpServiceIds,
        mentioned_items: mentionedItems,
        images: imageAttachments.length > 0 ? imageAttachments : undefined,
        attachment_uploads: attachmentUploads.length > 0 ? attachmentUploads : undefined,
        query: value, 
        method: 'POST', 
        url: endpoint
    });
}

// Watch for stream errors and show message
watch(error, (newError) => {
    if (newError) {
        MessagePlugin.error(newError);
        isReplying.value = false;
        loading.value = false;
        // 清空当前 assistant message ID
        currentAssistantMessageId.value = '';
    }
});

// 处理流式数据
onChunk((data) => {
    // 日志：打印接收到的事件
    console.log('[Agent Event Received]', {
        response_type: data.response_type,
        id: data.id,
        done: data.done,
        content_length: data.content?.length || 0,
        content_preview: data.content ? data.content.substring(0, 50) : '',
        data: data.data,
        session_id: data.session_id,
        assistant_message_id: data.assistant_message_id
    });
    
    // 处理 agent query 事件 - 保存 assistant message ID 并保持 loading 状态
    if (data.response_type === 'agent_query') {
        pendingStreamDebug.value = buildStreamDebugPayload();
        if (data.id) {
            const earlyMsg = findLastMessage((item) => item.role === 'assistant' && !item.is_completed);
            if (earlyMsg) {
                earlyMsg.request_id = data.id;
                attachStreamDebugToMessage(earlyMsg);
            }
        }
        if (data.assistant_message_id) {
            currentAssistantMessageId.value = data.assistant_message_id;
            console.log('[Agent Query] Saved assistant message ID:', data.assistant_message_id);
        }
        console.log('[Agent Query Event]', {
            session_id: data.session_id || data.data?.session_id,
            assistant_message_id: data.assistant_message_id,
            query: data.data?.query,
            request_id: data.data?.request_id
        });
        
        // 检查是否是继续流式传输（消息已存在）
        let existingMessage = findLastMessage((item) => item.id === data.id || item.request_id === data.id);
        if (!existingMessage) {
            // 预建 Agent 占位消息，确保紧随其后的 answer 分片走 handleAgentChunk
            // 写入 agentEventStream，而不是非 Agent 路径的 message.content。
            existingMessage = {
                id: data.id,
                request_id: data.id,
                role: 'assistant',
                content: '',
                isAgentMode: true,
                is_completed: false,
                agentEventStream: [],
                _eventMap: new Map(),
                _pendingToolCalls: new Map(),
                knowledge_references: [],
            };
            messagesList.push(existingMessage);
            attachStreamDebugToMessage(existingMessage);
            // 保持全局 Loading，直到首个 thinking/answer/tool 到达（handleAgentChunk
            // 会关闭）。若此处过早 loading=false，eventStream 仍为空时 Agent 内也无
            // 指示器，会出现「闪一下消失 → 空白 → 再出答案/再 Loading」。
            scrollToBottom(true);
            console.log('[Agent Query] Created agent placeholder message');
        } else {
            ensureAgentMessageShell(existingMessage, data.id);
            console.log('[Agent Query] Continuing stream for existing message');
        }
        return;
    }
    
    // 处理会话标题更新事件 - 不关闭 loading
    if (data.response_type === 'session_title') {
        const title = data.content || data.data?.title;
        if (title && data.data?.session_id) {
            console.log('[Session Title Update]', {
                session_id: data.data.session_id,
                title: title
            });
            usemenuStore.updatasessionTitle(data.data.session_id, title);
            usemenuStore.changeIsFirstSession(false);
            isNeedTitle.value = false;
        }
        // 不关闭 loading，等待实际内容
        return;
    }
    
    // 判断是否是 Agent 模式的响应
    // 注意：'answer', 'complete', 'references' 类型可能在两种模式下都存在
    // 只有 'thinking', 'tool_call', 'tool_result', 'reflection' 是 Agent 专有的
    const isAgentOnlyResponse = data.response_type === 'thinking' || 
                               data.response_type === 'tool_call' || 
                               data.response_type === 'tool_result' ||
                               data.response_type === 'reflection';
    
    // 检查当前消息是否已经是 Agent 模式
    const lastMessage = messagesList[messagesList.length - 1];
    const isCurrentlyAgentMode = lastMessage?.isAgentMode === true;
    const targetsActiveAgentRequest =
        isAgentStreamSession() &&
        !!data.id &&
        (data.id === currentAssistantMessageId.value ||
            lastMessage?.request_id === data.id ||
            lastMessage?.id === data.id);
    // Agent 开场白会先以 answer 分片流出；若此时尚未打上 isAgentMode，
    // 会误走非 Agent 路径写入 message.content，tool_call 后才切到 Agent，
    // AgentStreamDisplay 读不到 agentEventStream 中的开场白。
    const isAgentAnswerChunk =
        data.response_type === 'answer' && (isAgentStreamSession() || targetsActiveAgentRequest);
    const isAgentCompleteChunk =
        data.response_type === 'complete' && (isAgentStreamSession() || targetsActiveAgentRequest);
    
    // 如果是 Agent 专有的响应类型，或者当前消息已经是 Agent 模式，则走 Agent 处理
    const shouldHandleAsAgent =
        isAgentOnlyResponse ||
        isCurrentlyAgentMode ||
        isAgentAnswerChunk ||
        isAgentCompleteChunk;
    
    // 处理 references 事件 - 在两种模式下都需要处理，但不改变模式
    if (data.response_type === 'references') {
        // 如果当前是 Agent 模式，走 Agent 处理
        if (isCurrentlyAgentMode) {
            handleAgentChunk(data);
            return;
        }
        // 非 Agent 模式：将 references 保存到消息中供 botmsg 使用
        let existingMessage = findLastMessage((item) => item.request_id === data.id || item.id === data.id);
        
        // 如果消息还不存在，先创建一个空的 assistant 消息
        if (!existingMessage) {
            existingMessage = {
                id: data.id,
                request_id: data.id,
                role: 'assistant',
                content: '',
                showThink: false,
                thinkContent: '',
                thinking: false,
                is_completed: false,
                knowledge_references: []
            };
            messagesList.push(existingMessage);
            attachStreamDebugToMessage(existingMessage);
            loading.value = false; // 消息已创建，关闭 loading
            scrollToBottom(true);
        }
        
        existingMessage.knowledge_references = data.knowledge_references || data.data?.references || [];
        console.log('[References] Saved to message, count:', existingMessage.knowledge_references.length);
        return;
    }
    
    // Agent 模式处理（包括 stop 事件）
    if (shouldHandleAsAgent) {
        // 在 handleAgentChunk 中处理 loading 状态
        handleAgentChunk(data);
        
        // 对于 stop 事件，额外处理全局状态
        if (data.response_type === 'stop') {
            console.log('[Stop Event] Generation stopped');
            loading.value = false;
            isReplying.value = false;
            // 清空当前 assistant message ID
            currentAssistantMessageId.value = '';
        }
        return;
    }
    
    // 原有的知识库 QA 处理逻辑（非 Agent 模式）
    // answer 内容中可能包含 <think>...</think> 标签

    // 非 Agent 模式下的 stop 事件：只更新状态，不把后端附带的 "Generation stopped by user"
    // 文案拼进 content，保留用户点停止时已经流式输出的内容不变。
    if (data.response_type === 'stop') {
        console.log('[Stop Event] Non-agent generation stopped');
        const stoppedMessage = findLastMessage((item) => {
            if (item.request_id === data.id) return true;
            return item.id === data.id;
        });
        if (stoppedMessage) {
            stoppedMessage.is_completed = true;
        }
        loading.value = false;
        isReplying.value = false;
        fullContent.value = '';
        currentAssistantMessageId.value = '';
        return;
    }

    // 检查消息是否已经完成，如果已完成则忽略后续的完成事件（防止空内容覆盖）
    const existingMessage = findLastMessage((item) => {
        if (item.request_id === data.id) {
            return true
        }
        return item.id === data.id;
    });
    
    // 如果消息已完成且当前事件是完成事件（done=true 且无内容），直接忽略
    if (existingMessage?.is_completed && data.done && !data.content) {
        console.log('[Non-Agent] Ignoring duplicate completion event for completed message');
        return;
    }
    
    fullContent.value += data.content;
    let obj = { ...data, content: '', role: 'assistant', showThink: false, is_completed: false };

    // 检查是否为 fallback 回答（未从知识库检索到内容）
    if (data.data?.is_fallback) {
        obj.is_fallback = true;
    }

    if (fullContent.value.includes('<think>') && !fullContent.value.includes('<\/think>')) {
        obj.thinking = true;
        obj.showThink = true;
        obj.content = '';
        obj.thinkContent = fullContent.value.replace('<think>', '').trim();
    } else if (fullContent.value.includes('<think>') && fullContent.value.includes('<\/think>')) {
        obj.thinking = false;
        obj.showThink = true;
        // Use lastIndexOf to handle edge cases with multiple </think> occurrences,
        // consistent with history loading logic (line 280)
        const index = fullContent.value.lastIndexOf('<\/think>');
        obj.thinkContent = fullContent.value.substring(0, index).replace('<think>', '').trim();
        obj.content = fullContent.value.substring(index + 8).trim();
    } else {
        obj.content = fullContent.value;
    }
    
    if (!existingMessage) {
        loading.value = false; // 消息即将创建，关闭 loading
    }
    
    if (data.done) {
        // 标记消息已完成
        obj.is_completed = true;
        // 标题生成已改为异步事件推送，不再需要在这里手动调用
        // 如果标题还未生成，前端会通过 SSE 事件接收
        isReplying.value = false;
        fullContent.value = "";
        // 清空当前 assistant message ID
        currentAssistantMessageId.value = '';
    }
    updateAssistantSession(obj);
})
// 处理 Agent 流式数据 (Cursor-style UI)
const handleAgentChunk = (data) => {
    let message = findLastMessage((item) => item.request_id === data.id || item.id === data.id);
    
    if (!message) {
        // 创建新的 Assistant 消息 - 此时开始显示内容，关闭 loading
        const newMsg = {
            id: data.id,
            request_id: data.id,
            role: 'assistant',
            content: '',
            isAgentMode: true,
            // Event stream: ordered list of all agent events (thinking, tool calls, etc)
            agentEventStream: [],
            // Map to track event by event_id for quick lookup
            _eventMap: new Map(),
            knowledge_references: []
        };
        messagesList.push(newMsg);
        attachStreamDebugToMessage(newMsg);
        pendingStreamDebug.value = null;
        loading.value = false; // 消息已创建，关闭 loading
        scrollToBottom(true);
        // Don't return - continue to process the current event data
        message = newMsg;
    } else {
        attachStreamDebugToMessage(message);
        pendingStreamDebug.value = null;
    }
    
    message.isAgentMode = true;
    
    // 确保在继续流式传输时（刷新页面场景），一旦接收到实际内容就关闭 loading
    // 这是一个保护措施，防止任何边缘情况导致 loading 残留
    if (loading.value && (data.response_type === 'thinking' || data.response_type === 'answer' || data.response_type === 'tool_call' || data.response_type === 'tool_approval_required')) {
        console.log('[Agent Chunk] Closing loading for continued stream');
        loading.value = false;
    }
    
    switch(data.response_type) {
        case 'thinking':
            {
                const eventId = data.data?.event_id;
                console.log('[Thinking Event]', {
                    event_id: eventId,
                    done: data.done,
                    content_length: data.content?.length || 0
                });
                
                // Initialize structures
                if (!message.agentEventStream) message.agentEventStream = [];
                if (!message._eventMap) message._eventMap = new Map();
                
                if (!data.done) {
                    // Check if this thinking event already exists
                    let thinkingEvent = message._eventMap.get(eventId);
                    
                    if (!thinkingEvent) {
                        // Create new thinking event
                        console.log('[Thinking] Creating new thinking event, event_id:', eventId);
                        thinkingEvent = {
                            type: 'thinking',
                            event_id: eventId,
                            content: '',
                            done: false,
                            startTime: Date.now(),
                            thinking: true
                        };
                        
                        // Add to event stream
                        message.agentEventStream.push(thinkingEvent);
                        message._eventMap.set(eventId, thinkingEvent);
                    }
                    
                    // Accumulate content
                    if (data.content) {
                        thinkingEvent.content += data.content;
                        console.log('[Thinking] Event', eventId, 'accumulated:', thinkingEvent.content.length, 'chars');
                    }
                    
                } else {
                    // Thinking completed
                    const thinkingEvent = message._eventMap.get(eventId);
                    if (thinkingEvent) {
                        console.log('[Thinking] Completing event, event_id:', eventId, 'content length:', thinkingEvent.content.length);
                        
                        // Mark as done
                        thinkingEvent.done = true;
                        thinkingEvent.thinking = false;
                        thinkingEvent.duration_ms = data.data?.duration_ms || (Date.now() - thinkingEvent.startTime);
                        thinkingEvent.completed_at = data.data?.completed_at || Date.now();
                        
                        console.log('[Thinking] Event completed, duration:', thinkingEvent.duration_ms, 'ms');
                    } else {
                        console.warn('[Thinking] Received done for unknown event_id:', eventId);
                    }
                }
            }
            break;

        case 'tool_approval_required': {
            if (!message.agentEventStream) message.agentEventStream = [];
            const d = data.data || {};
            message.agentEventStream.push({
                type: 'tool_approval_required',
                pending_id: d.pending_id,
                service_name: d.service_name,
                mcp_tool_name: d.mcp_tool_name,
                description: d.description,
                args_json: d.args_json,
                timeout_seconds: d.timeout_seconds,
                requested_at: d.requested_at,
                tool_call_id: d.tool_call_id,
                resolved: false,
            });
            break;
        }
        case 'tool_approval_resolved': {
            const d = data.data || {};
            const pid = d.pending_id;
            const ev = message.agentEventStream?.find(
                (e) => e.type === 'tool_approval_required' && e.pending_id === pid
            );
            if (ev) {
                ev.resolved = true;
                ev.approved = d.approved;
                ev.resolve_reason = d.reason;
                ev.timed_out = d.timed_out;
                ev.canceled = d.canceled;
            }
            break;
        }
        case 'tool_call':
            // Legacy guard: the final_answer tool has been removed, but old
            // streamed/replayed data may still carry it — its content appears
            // as answer events, so skip the tool-call rendering.
            if (data.data && data.data.tool_name === 'final_answer') {
                break;
            }
            // 任何在本工具调用之前流入答案区的纯文本都是这一轮的开场白，而非最终
            // 答案（Agent 只会以"纯文本、无工具调用"自然结束）。因此一旦出现工具
            // 调用，就把先前的答案片段从答案区回撤、交给步骤树重新定位，等效于旧的
            // 后端 superseded 标记，并用剩余片段重组 message.content。
            if (message.agentEventStream) {
                let retracted = false;
                for (const ev of message.agentEventStream) {
                    if (ev.type === 'answer' && !ev.superseded && ev.content && ev.content.trim()) {
                        ev.superseded = true;
                        ev.done = true;
                        retracted = true;
                    }
                }
                if (retracted) {
                    message.content = recomposeAgentAnswer(message);
                    fullContent.value = message.content;
                }
            }
            // Store or update pending tool call to pair with result later
            if (data.data && (data.data.tool_name || data.data.tool_call_id)) {
                const incomingToolName = data.data.tool_name;
                const incomingArguments = data.data.arguments;
                
                if (!message.agentEventStream) message.agentEventStream = [];
                if (!message._pendingToolCalls) message._pendingToolCalls = new Map();
                
                const toolCallId = data.data.tool_call_id || (incomingToolName ? (incomingToolName + '_' + Date.now()) : null);
                if (!toolCallId) {
                    console.warn('[Tool Call] Received event without identifiable tool_call_id:', data.data);
                    break;
                }
                
                console.log('[Tool Call]', {
                    tool_call_id: toolCallId,
                    tool_name: incomingToolName,
                    has_arguments: Boolean(incomingArguments)
                });
                
                let toolCallEvent = message._pendingToolCalls.get(toolCallId);
                if (!toolCallEvent) {
                    toolCallEvent = message.agentEventStream.find(
                        (event) => event.type === 'tool_call' && event.tool_call_id === toolCallId
                    );
                }
                
                if (toolCallEvent) {
                    if (incomingToolName) toolCallEvent.tool_name = incomingToolName;
                    if (incomingArguments) toolCallEvent.arguments = incomingArguments;
                    toolCallEvent.pending = true;
                    if (!toolCallEvent.timestamp) {
                        toolCallEvent.timestamp = Date.now();
                    }
                    message._pendingToolCalls.set(toolCallId, toolCallEvent);
                } else {
                    const newToolCallEvent = {
                        type: 'tool_call',
                        tool_call_id: toolCallId,
                        tool_name: incomingToolName,
                        arguments: incomingArguments,
                        timestamp: Date.now(),
                        pending: true
                    };
                    message.agentEventStream.push(newToolCallEvent);
                    message._pendingToolCalls.set(toolCallId, newToolCallEvent);
                }
            }
            break;
            
        case 'tool_result':
        case 'error':
            // Tool result - update the corresponding tool call event
            if (data.data) {
                const toolCallId = data.data.tool_call_id;
                const toolName = data.data.tool_name;
                const success = data.response_type !== 'error' && data.data.success !== false;
                
                console.log('[Tool Result]', {
                    tool_call_id: toolCallId,
                    tool_name: toolName,
                    success: success
                });
                
                // Find and update the pending tool call event
                let toolCallEvent = null;
                if (message._pendingToolCalls) {
                    if (toolCallId && message._pendingToolCalls.has(toolCallId)) {
                        toolCallEvent = message._pendingToolCalls.get(toolCallId);
                        message._pendingToolCalls.delete(toolCallId);
                    } else {
                        // Try to find by tool_name if no tool_call_id match
                        for (const [key, value] of message._pendingToolCalls.entries()) {
                            if (value.tool_name === toolName) {
                                toolCallEvent = value;
                                message._pendingToolCalls.delete(key);
                                break;
                            }
                        }
                    }
                }
                
                if (toolCallEvent) {
                    // Update the existing event with result
                    toolCallEvent.pending = false;
                    toolCallEvent.success = success;
                    toolCallEvent.output = success ? (data.data.output || data.content) : (data.data.error || data.content);
                    toolCallEvent.error = !success ? (data.data.error || data.content) : undefined;
                    // Set both duration and duration_ms for compatibility
                    const duration = data.data.duration_ms !== undefined ? data.data.duration_ms : data.data.duration;
                    toolCallEvent.duration = duration;
                    toolCallEvent.duration_ms = duration;
                    toolCallEvent.display_type = data.data.display_type;
                    toolCallEvent.tool_data = data.data;
                    
                    console.log('[Tool Result] Updated event in stream');
                } else {
                    console.warn('[Tool Result] No pending tool call found for', toolCallId || toolName);
                }
                
                // If this is an error response without tool data, handle it
                if (data.response_type === 'error' && !toolName) {
                    const errorMsg = data.content || t('chat.processError');
                    message.content = errorMsg;
                    isReplying.value = false;
                    loading.value = false;
                    MessagePlugin.error(errorMsg);
                    console.error('[Chat Error]', errorMsg);
                }
            } else if (data.response_type === 'error') {
                // Generic error without tool context
                const errorMsg = data.content || t('chat.processError');
                message.content = errorMsg;
                isReplying.value = false;
                loading.value = false;
                MessagePlugin.error(errorMsg);
                console.error('[Chat Error]', errorMsg);
            }
            break;
            

        case 'references':
            // 知识引用
            if (data.data?.references) {
                message.knowledge_references = data.data.references;
            } else if (data.knowledge_references) {
                // 兼容旧格式
                message.knowledge_references = data.knowledge_references;
            }
            break;
            
        case 'answer': {
            // 最终答案（乐观流式）。普通 content 先按答案样式流入答案区；若这一轮
            // 其实调用了工具（是开场白），后续的 tool_call 事件会把该片段从答案区
            // 回撤、交给步骤树重新定位（见 'tool_call' 分支），并用剩余未被回撤的
            // 片段重组 message.content。每轮答案各自一个事件（按 event_id 区分），
            // 这样开场白不会和最终答案合并。
            message.thinking = false;
            const eventId = data.data?.event_id;
            if (!message.agentEventStream) message.agentEventStream = [];
            if (!message._eventMap) message._eventMap = new Map();

            let answerEvent = eventId
                ? message._eventMap.get(eventId)
                : message.agentEventStream.find((e) => e.type === 'answer' && !e.event_id);
            if (!answerEvent) {
                answerEvent = { type: 'answer', event_id: eventId, content: '', done: false };
                message.agentEventStream.push(answerEvent);
                if (eventId) message._eventMap.set(eventId, answerEvent);
            }

            // 若 answer 分片曾误走非 Agent 路径，首次进入 Agent 处理时迁入事件流
            if (!answerEvent.content && message.content && message.content.trim()) {
                answerEvent.content = message.content;
            }

            // 只有当有实际内容时才追加，避免空内容覆盖
            if (data.content) {
                answerEvent.content += data.content;
                message.content = recomposeAgentAnswer(message);
                fullContent.value = message.content;
            }

            // 检查是否为 fallback 回答
            if (data.data?.is_fallback) {
                answerEvent.is_fallback = true;
                message.is_fallback = true;
            }

            // 只在第一次收到 done:true 时标记完成，忽略后续重复的完成事件
            if (data.done && !answerEvent.done) {
                answerEvent.done = true;
                attachStreamDebugToMessage(message);
                pendingStreamDebug.value = null;

                // 完成 - 关闭所有状态
                loading.value = false;
                isReplying.value = false;
                fullContent.value = '';
                // 清空当前 assistant message ID
                currentAssistantMessageId.value = '';
            }
            break;
        }
            
        case 'complete':
            // 整个流式响应完成事件 - 确保状态正确关闭
            console.log('[Agent] Complete event received');
            loading.value = false;
            isReplying.value = false;
            message.is_completed = true;
            fullContent.value = '';
            currentAssistantMessageId.value = '';
            // 将 total_duration_ms 存入事件流供 AgentStreamDisplay 使用
            if (message.agentEventStream) {
                message.agentEventStream.push({
                    type: 'agent_complete',
                    total_duration_ms: data.data?.total_duration_ms || 0,
                    total_steps: data.data?.total_steps || 0,
                });
            }
            break;
            
        case 'stop':
            // 停止事件 - 添加到事件流并标记对话完成
            console.log('[Agent] Stop event received');
            if (!message.agentEventStream) message.agentEventStream = [];
            
            // Add stop event to stream
            message.agentEventStream.push({
                type: 'stop',
                timestamp: Date.now(),
                reason: data.data?.reason || 'user_requested'
            });
            
            // Mark conversation as stopped
            isReplying.value = false;
            fullContent.value = '';
            break;
    }
    
    scrollToBottom();
};

const updateAssistantSession = (payload) => {
    const message = findLastMessage((item) => {
        if (item.request_id === payload.id) {
            return true
        }
        return item.id === payload.id;
    });
    if (message) {
        if (payload.id && !message.request_id) {
            message.request_id = payload.id;
        }
        message.content = payload.content;
        message.thinking = payload.thinking;
        message.thinkContent = payload.thinkContent;
        message.showThink = payload.showThink;
        message.knowledge_references = message.knowledge_references ? message.knowledge_references : payload.knowledge_references;
        // 更新 fallback 状态
        if (payload.is_fallback) {
            message.is_fallback = true;
        }
        // 更新完成状态
        if (payload.is_completed) {
            message.is_completed = true;
        }
        attachStreamDebugToMessage(message);
        if (payload.is_completed) {
            pendingStreamDebug.value = null;
        }
    } else {
        const entry = { ...payload };
        if (entry.id && !entry.request_id) {
            entry.request_id = entry.id;
        }
        messagesList.push(entry);
        attachStreamDebugToMessage(entry);
        if (payload.is_completed) {
            pendingStreamDebug.value = null;
        }
    }
    scrollToBottom();
}
const handleSessionCleared = (e) => {
    if (e.detail?.sessionId === session_id.value) {
        messagesList.splice(0);
        created_at.value = '';
        hasMoreHistory.value = true;
        historyLoadingMore.value = false;
    }
};

onMounted(async () => {
    window.addEventListener('session-messages-cleared', handleSessionCleared);
    messagesList.splice(0);
    
    // 若从智能体列表点击共享智能体进入，URL 带 agent_id 与 source_tenant_id，同步到 store
    const agentIdFromQuery = props.embeddedAgentId || (route.query.agent_id && String(route.query.agent_id));
    const sourceTenantIdFromQuery = route.query.source_tenant_id && String(route.query.source_tenant_id);
    if (agentIdFromQuery && sourceTenantIdFromQuery) {
        useSettingsStoreInstance.selectAgent(agentIdFromQuery, sourceTenantIdFromQuery);
    } else if (agentIdFromQuery) {
        useSettingsStoreInstance.selectAgent(agentIdFromQuery, null);
    }
    
    if (props.embeddedKbIds && props.embeddedKbIds.length > 0) {
        useSettingsStoreInstance.selectKnowledgeBases(props.embeddedKbIds);
    }
    
    // 初始化状态：加载历史消息时不应显示loading
    loading.value = false;
    isReplying.value = false;
    
    // 拉会话详情；若服务端记录了 last_request_state，则按其恢复输入栏状态。
    await loadSessionAndHydrate(session_id.value);

    checkmenuTitle(session_id.value)
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

    // 初始加载推荐问题
    fetchSuggestedQuestions();
})
const clearData = () => {
    stopStream();
    isReplying.value = false;
    fullContent.value = '';
    userquery.value = '';

}
onUnmounted(() => {
    window.removeEventListener('session-messages-cleared', handleSessionCleared);
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
    padding: 20px;
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

    &.is-embedded :deep(.answers-input) {
        transform: translateX(0);
        width: 100%;
        left: 0;
        display: flex;
        justify-content: center;
    }

    :deep(.answers-input) {
        position: static;
        transform: translateX(0);

        .t-textarea__inner {
            width: 100% !important;
        }
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
    overflow-y: auto;

    &::-webkit-scrollbar {
        width: 0;
        height: 0;
        color: transparent;
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

.agent-mode-indicator {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 16px;
    background: var(--td-brand-color-light);
    border: 1px solid var(--td-brand-color-focus);
    border-radius: 6px;
    margin-bottom: 12px;
    max-width: 800px;
    width: 100%;

    .agent-icon {
        font-size: 20px;
    }

    .agent-text {
        font-size: 14px;
        font-weight: 500;
        color: var(--td-brand-color);
        flex: 1;
    }
}

@keyframes contentFadeIn {
    from { opacity: 0; transform: translateY(6px); }
    to { opacity: 1; transform: translateY(0); }
}

.msg-skeleton-list {
    display: flex;
    flex-direction: column;
    gap: 20px;
    max-width: 800px;
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
    // Keep the input visible when messages overflow: without flex-shrink: 0
    // a tall .chat_scroll_box can squeeze this container down to 0 height.
    flex-shrink: 0;
    margin: 16px auto 4px;
    width: 100%;
    max-width: 800px;
    box-sizing: border-box;

    &.is-embedded {
        max-width: 100%;
        width: 100%;
        margin: 0;
        overflow-x: hidden;
    }
}

.msg_list {
    display: flex;
    flex-direction: column;
    gap: 16px;
    max-width: 800px;
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
    
    .loading-typing {
        display: flex;
        align-items: center;
        gap: 4px;
        
        span {
            width: 6px;
            height: 6px;
            border-radius: 50%;
            background: var(--td-brand-color);
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
}

@keyframes typingBounce {
    0%, 60%, 100% {
        transform: translateY(0);
    }
    30% {
        transform: translateY(-8px);
    }
}

.suggested-questions-container {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 32px 16px 16px;
    max-width: 800px;
    margin: 0 auto;
    width: 100%;
    min-height: 0;
    transition: min-height 0.3s ease;

    &.has-questions {
        min-height: 80px;
    }
}

.suggested-questions-inner {
    display: flex;
    flex-direction: column;
    align-items: center;
    width: 100%;
    animation: contentFadeIn 0.3s ease-out;
}

.sq-fade-enter-active,
.sq-fade-leave-active {
    transition: opacity 0.25s ease;
}
.sq-fade-enter-from,
.sq-fade-leave-to {
    opacity: 0;
}

.suggested-questions-title {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin-bottom: 16px;
    font-weight: 500;
}

.suggested-questions-grid {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    justify-content: center;
    width: 100%;
}

.suggested-question-card {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 10px 16px;
    border-radius: 20px;
    border: 1px solid var(--td-component-stroke);
    background: var(--td-bg-color-container);
    cursor: pointer;
    transition: all 0.2s ease;
    max-width: 100%;

    &:hover {
        border-color: var(--td-brand-color);
        background: var(--td-brand-color-light);
        box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
    }
}

.suggested-question-text {
    font-size: 13px;
    color: var(--td-text-color-primary);
    line-height: 1.4;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
}

.suggested-question-badge {
    font-size: 10px;
    padding: 1px 5px;
    border-radius: 4px;
    flex-shrink: 0;
    font-weight: 500;

    &.faq {
        background: var(--td-success-color-1);
        color: var(--td-success-color);
    }
}
</style>
