<template>
  <!-- Agent sidebar (agent-sidebar-inbox P1): a persistent chat panel bound
       to the KB view. Teleported to <body> and fixed to the viewport's right
       edge — the same coordinate system as the body-attached doc drawers —
       so the two always tile seamlessly, even when the page content
       overflows horizontally (scrollbar fallback). The page reserves the
       dock's live width via margin-right: var(--agent-sidebar-w), so
       opening it still narrows the main content instead of covering it. -->
  <teleport to="body">
    <aside class="agent-sidebar-dock" :class="{ 'is-open': visible, 'is-resizing': resizing }"
      :aria-hidden="!visible" :style="visible ? { width: `${sidebarWidth}px` } : undefined">
      <!-- 左缘拖拽条：按住左右拖动调宽（260–640），松手持久化 -->
      <div v-if="visible" class="agent-sidebar-resizer" role="separator" aria-orientation="vertical"
        :aria-label="$t('knowledgeBase.agentSidebar.title')" @mousedown.prevent="startResize"></div>
      <div class="agent-sidebar-panel" :style="{ width: `${sidebarWidth}px` }">
        <header class="agent-sidebar-header">
          <!-- Row 1: session switcher (click the title for recent chats) and
               the panel close button, sharing the full width. -->
          <div class="agent-sidebar-header-row">
            <t-popup trigger="click" placement="bottom-left" attach="body" @show="loadRecentSessions">
              <button type="button" class="agent-sidebar-session-trigger" :title="currentSessionTitle">
                <span class="agent-sidebar-title">{{ currentSessionTitle }}</span>
                <t-icon name="chevron-down" size="14px" />
              </button>
              <template #content>
                <div class="agent-sidebar-session-menu">
                  <button v-for="s in recentSessions" :key="s.id" type="button" class="agent-sidebar-session-item"
                    :class="{ 'is-active': s.id === sessionId }" @click="switchSession(s)">
                    <span class="agent-sidebar-session-item-title">
                      {{ s.title || t('knowledgeBase.agentSidebar.newSession') }}
                    </span>
                    <span class="agent-sidebar-session-item-time">{{ formatSessionTime(s.updated_at) }}</span>
                  </button>
                  <div v-if="!recentSessions.length" class="agent-sidebar-session-empty">
                    {{ t('knowledgeBase.agentSidebar.noHistory') }}
                  </div>
                </div>
              </template>
            </t-popup>
            <button type="button" class="agent-sidebar-close" :aria-label="$t('knowledgeBase.agentSidebar.title')"
              @click="visible = false">
              <t-icon name="close" size="16px" />
            </button>
          </div>
          <!-- Row 2: fixed assistant badge (the dock is locked to the
               dedicated sidebar agent — v2 decision, 2026-10-12) plus the
               new-chat action. History sessions loaded from the menu below
               keep their transcript; the sidebar agent takes over from the
               next turn, same as switching agents mid-chat in the main view. -->
          <div class="agent-sidebar-header-row agent-sidebar-header-tools">
            <span class="agent-sidebar-agent-badge">
              <t-icon name="usergroup" size="14px" />
              <span class="agent-sidebar-agent-name">{{ t('knowledgeBase.agentSidebar.title') }}</span>
            </span>
            <!-- 收件箱入口：贴底面板开关，徽标为未读数（共享已读语义）。 -->
            <button type="button" class="agent-sidebar-inbox-btn"
              :class="{ 'has-unread': inboxUnread > 0 }"
              :title="t('knowledgeBase.agentSidebar.inbox.title')" @click="toggleInbox">
              <t-icon name="notification" size="14px" />
              <span v-if="inboxUnread > 0" class="agent-sidebar-inbox-count">
                {{ inboxUnread > 99 ? '99+' : inboxUnread }}
              </span>
            </button>
            <button type="button" class="agent-sidebar-new-session"
              :title="t('knowledgeBase.agentSidebar.newSession')" @click="newSession">
              <t-icon name="add" size="14px" />
              <span>{{ t('knowledgeBase.agentSidebar.newSession') }}</span>
            </button>
          </div>
        </header>
        <div class="agent-sidebar-body">
          <t-loading :loading="sessionLoading" text="" size="small" show-overlay>
            <!-- v-show (not v-if) so the conversation and scroll position stay
                 alive while the panel is closed. -->
            <ChatView v-if="sessionId" v-show="visible" :key="sessionId" :session_id="sessionId"
              agentId="builtin-sidebar-assistant" :kbIds="props.kbId ? [props.kbId] : []" :embeddedMode="true" />
            <div v-else-if="!sessionLoading" class="agent-sidebar-empty">
              {{ $t('knowledgeBase.agentSidebar.sessionError') }}
            </div>
          </t-loading>
        </div>
        <!-- 收件箱贴底面板（Phase 3）：覆盖在聊天区下方的滑出层，
             关闭时聊天保持原位。事件 = 用户行为 + 解析生命周期 + agent 回执。 -->
        <section v-if="inboxOpen" class="agent-inbox-sheet">
          <header class="agent-inbox-sheet-header">
            <span class="agent-inbox-sheet-title">{{ t('knowledgeBase.agentSidebar.inbox.title') }}</span>
            <div class="agent-inbox-sheet-actions">
              <button type="button" class="agent-inbox-action" :disabled="!hasUnreadInbox"
                @click="markAllInboxRead">
                {{ t('knowledgeBase.agentSidebar.inbox.markAllRead') }}
              </button>
              <button type="button" class="agent-inbox-icon-action"
                :title="t('knowledgeBase.agentSidebar.inbox.refresh')" @click="loadInbox">
                <t-icon name="refresh" size="14px" />
              </button>
              <button type="button" class="agent-inbox-icon-action"
                :title="t('knowledgeBase.agentSidebar.inbox.collapse')" @click="inboxOpen = false">
                <t-icon name="chevron-down" size="14px" />
              </button>
            </div>
          </header>
          <div class="agent-inbox-list">
            <div v-if="!inboxEvents.length" class="agent-inbox-empty">
              {{ t('knowledgeBase.agentSidebar.inbox.empty') }}
            </div>
            <div v-for="e in inboxEvents" :key="e.id" class="agent-inbox-item"
              :class="{ 'is-unread': !e.read }">
              <span v-if="!e.read" class="agent-inbox-unread-dot"></span>
              <div class="agent-inbox-item-main">
                <div class="agent-inbox-item-row1">
                  <span class="agent-inbox-item-type">{{ inboxTypeLabel(e.type) }}</span>
                  <span class="agent-inbox-item-priority" :class="`prio-${(e.priority || 'p2').toLowerCase()}`">
                    {{ e.priority }}
                  </span>
                  <span class="agent-inbox-item-time">{{ formatInboxTime(e.time) }}</span>
                </div>
                <div v-if="inboxSummary(e)" class="agent-inbox-item-summary">{{ inboxSummary(e) }}</div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </aside>
  </teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import ChatView from '@/views/chat/index.vue';
import { createSessions, getSessionsList } from '@/api/chat';
import {
  fetchInboxEvents,
  markInboxRead,
  type AgentInboxDigest,
} from '@/api/agent-inbox';

const props = defineProps<{
  // Absent on the KB list page: the sidebar then runs a global session not
  // bound to any knowledge base (agent-sidebar-inbox design decision).
  kbId?: string;
  modelValue: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void;
}>();

const { t } = useI18n();
const visible = ref(props.modelValue);
const sessionId = ref('');
const sessionLoading = ref(false);

// Draggable width (agent-sidebar-inbox): default 420 (comfortable chat
// width), min 260 (three-pane floor agreed in the design review), max 640
// (never wider than half of a 1280 viewport). Persisted per device in
// localStorage — a UI metric, not a user preference contract.
const SIDEBAR_WIDTH_KEY = 'agentSidebarWidth';
const SIDEBAR_WIDTH_MIN = 260;
const SIDEBAR_WIDTH_MAX = 640;
const SIDEBAR_WIDTH_DEFAULT = 420;

const clampSidebarWidth = (w: number) =>
  Math.min(SIDEBAR_WIDTH_MAX, Math.max(SIDEBAR_WIDTH_MIN, Math.round(w)));

const storedWidth = Number(localStorage.getItem(SIDEBAR_WIDTH_KEY));
const sidebarWidth = ref(
  Number.isFinite(storedWidth) && storedWidth > 0
    ? clampSidebarWidth(storedWidth)
    : SIDEBAR_WIDTH_DEFAULT
);
const resizing = ref(false);

const onResizeMove = (e: MouseEvent) => {
  sidebarWidth.value = clampSidebarWidth(window.innerWidth - e.clientX);
};
const stopResize = () => {
  if (!resizing.value) return;
  resizing.value = false;
  document.removeEventListener('mousemove', onResizeMove);
  document.removeEventListener('mouseup', stopResize);
  document.body.style.cursor = '';
  localStorage.setItem(SIDEBAR_WIDTH_KEY, String(sidebarWidth.value));
};
const startResize = () => {
  resizing.value = true;
  document.addEventListener('mousemove', onResizeMove);
  document.addEventListener('mouseup', stopResize);
  document.body.style.cursor = 'col-resize';
};
onBeforeUnmount(stopResize);

// Publish the live dock width as a document-level CSS var so overlays that
// teleport to <body> (the doc-content drawers) can keep clear of the chat:
// they read var(--agent-sidebar-w) and shift left while the dock is open.
const syncWidthVar = () => {
  document.documentElement.style.setProperty(
    '--agent-sidebar-w',
    visible.value ? `${sidebarWidth.value}px` : '0px'
  );
};
watch([visible, sidebarWidth], syncWidthVar, { immediate: true });
onBeforeUnmount(() => {
  document.documentElement.style.setProperty('--agent-sidebar-w', '0px');
});

watch(() => props.modelValue, (val) => { visible.value = val; });
watch(visible, (val) => { emit('update:modelValue', val); });

// One persistent session for the whole sidebar (global binding): switching
// KBs only changes the surrounding view — the conversation keeps running
// (agreed 2026-10-12; per-KB sessions felt like the chat "disappeared" on
// every KB switch). Separate conversations remain available via the header
// dropdown and the new-chat button.
const SIDEBAR_SESSION_KEY = 'agentSidebarSession:global';

const ensureSession = async () => {
  if (sessionLoading.value) return;
  const stored = localStorage.getItem(SIDEBAR_SESSION_KEY);
  if (stored) {
    sessionId.value = stored;
    return;
  }
  sessionLoading.value = true;
  try {
    const res: any = await createSessions({});
    const id = res && res.data && res.data.id;
    if (id) {
      sessionId.value = id;
      localStorage.setItem(SIDEBAR_SESSION_KEY, id);
    } else {
      MessagePlugin.error(t('knowledgeBase.agentSidebar.sessionError'));
    }
  } catch (_) {
    MessagePlugin.error(t('knowledgeBase.agentSidebar.sessionError'));
  } finally {
    sessionLoading.value = false;
  }
};

watch(visible, (val) => {
  if (val && !sessionId.value) ensureSession();
});

// ── Session switcher (sidebar header v2) ────────────────────────────────
// Row 1 shows the current session title; clicking it opens the recent-chat
// list (same source as the main sidebar's history, plain recency order —
// agreed semantics, 2026-10-12). Picking one rebinds this dock to it; the
// loaded conversation keeps its transcript and the fixed sidebar agent
// answers from the next turn (main-view agent-switch semantics).
interface RecentSession {
  id: string;
  title: string;
  updated_at?: string;
}

const recentSessions = ref<RecentSession[]>([]);
// Title of the bound session as last seen in the menu — covers sessions
// picked before the list loaded or renamed elsewhere.
const activeTitle = ref('');
const currentSessionTitle = computed(() => {
  const found = recentSessions.value.find((s) => s.id === sessionId.value);
  return found?.title || activeTitle.value || t('knowledgeBase.agentSidebar.newSession');
});
const loadRecentSessions = async () => {
  try {
    const res: any = await getSessionsList(1, 20);
    recentSessions.value = res?.data || [];
  } catch (_) {
    recentSessions.value = [];
  }
};

// Every mutation of the panel's session goes through here so the per-KB
// localStorage binding and the panel state stay in sync.
const bindSession = (id: string, title = '') => {
  sessionId.value = id;
  activeTitle.value = title;
  localStorage.setItem(SIDEBAR_SESSION_KEY, id);
};
const switchSession = (s: RecentSession) => {
  if (s.id && s.id !== sessionId.value) bindSession(s.id, s.title);
};
const newSession = async () => {
  try {
    const res: any = await createSessions({});
    const id = res && res.data && res.data.id;
    if (!id) throw new Error('no session id');
    bindSession(id);
    recentSessions.value = recentSessions.value.filter((s) => s.id !== id);
  } catch (_) {
    MessagePlugin.error(t('knowledgeBase.agentSidebar.sessionError'));
  }
};

const formatSessionTime = (v?: string) => {
  if (!v) return '';
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return '';
  const now = new Date();
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  const sameDay = d.getFullYear() === now.getFullYear()
    && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return sameDay ? hm : `${d.getMonth() + 1}-${d.getDate()}`;
};

// Create the session eagerly so the panel is ready before the first open,
// and load the recency list once so the header title resolves immediately.
ensureSession();
loadRecentSessions();

// ── 收件箱贴底面板（agent-sidebar-inbox Phase 3）─────────────────────────
// 读取走 GET /agent-inbox/events（只读，身份取自登录态）；已读是共享状态
// （行级 read_at），agent 的 check_inbox 也会消费未读，徽标是尽力而为的
// 提示而非精确状态。显示范围 = 自己的 user.* 事件 + 租户级事件。
const inboxOpen = ref(false);
const inboxEvents = ref<AgentInboxDigest[]>([]);
const inboxUnread = ref(0);
const inboxLoading = ref(false);
const hasUnreadInbox = computed(() => inboxEvents.value.some((e) => !e.read));

const loadInbox = async () => {
  inboxLoading.value = true;
  try {
    const res = await fetchInboxEvents({ limit: 50 });
    inboxEvents.value = res.events;
    inboxUnread.value = res.unread;
  } catch (_) {
    /* 保留旧列表——面板是尽力而为的态势展示 */
  } finally {
    inboxLoading.value = false;
  }
};

const toggleInbox = () => {
  inboxOpen.value = !inboxOpen.value;
  if (inboxOpen.value) void loadInbox();
};

const markAllInboxRead = async () => {
  const ids = inboxEvents.value.filter((e) => !e.read).map((e) => e.id);
  if (!ids.length) return;
  try {
    await markInboxRead(ids);
    await loadInbox();
  } catch (_) {
    /* ignore — best-effort */
  }
};

// 抽屉打开时若面板已展开则刷新一次，保证事件新鲜。
watch(visible, (val) => {
  if (val && inboxOpen.value) void loadInbox();
});

// 类型 → i18n key 映射；未收录的类型原样展示（新类型无需改前端即可出现）。
const INBOX_TYPE_LABEL_KEYS: Record<string, string> = {
  'user.viewed_document': 'viewedDoc',
  'user.viewed_image': 'viewedImage',
  'user.switched_kb': 'switchedKb',
  'user.switched_tab': 'switchedTab',
  'user.viewed_page': 'viewedPage',
  'user.uploaded_doc': 'uploadedDoc',
  'user.deleted_doc': 'deletedDoc',
  'user.reparse_doc': 'reparseDoc',
  'user.cancelled_parse': 'cancelledParse',
  'user.updated_doc': 'updatedDoc',
  'user.edited_wiki': 'editedWiki',
  'user.edited_chunk': 'editedChunk',
  'user.edited_faq': 'editedFaq',
  'user.updated_kb': 'updatedKb',
  'user.deleted_kb': 'deletedKb',
  'user.error_toast': 'errorToast',
  'parse.completed': 'parseCompleted',
  'parse.failed': 'parseFailed',
  'agent_action.wiki_write': 'agentWikiWrite',
};

const inboxTypeLabel = (type: string): string => {
  const key = INBOX_TYPE_LABEL_KEYS[type];
  if (!key) return type;
  return t(`knowledgeBase.agentSidebar.inbox.types.${key}`);
};

// 摘要行：取 payload 里最有信息量的字段（标题优先，id 兜底）。
const inboxSummary = (e: AgentInboxDigest): string => {
  const p = e.payload || {};
  const candidates = ['title', 'message', 'name', 'slug', 'knowledge_id', 'error'];
  for (const k of candidates) {
    const v = p[k];
    if (typeof v === 'string' && v.trim()) return v.trim();
  }
  return '';
};

const formatInboxTime = (v?: string) => {
  if (!v) return '';
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return '';
  const now = new Date();
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
  const sameDay = d.getFullYear() === now.getFullYear()
    && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return sameDay ? hm : `${d.getMonth() + 1}-${d.getDate()} ${hm}`;
};
</script>

<style scoped>
.agent-sidebar-dock {
  /* Fixed to the viewport's right edge, one layer above the body-attached
     doc drawers (z 2000): the reading pane and the chat then tile the
     window edge-to-edge (drawer right == dock left) with no masked page
     showing through, regardless of horizontal overflow. */
  position: fixed;
  right: 0;
  top: 0;
  bottom: 0;
  z-index: 2002;
  width: 0;
  overflow: hidden;
  transition: width var(--app-motion-slow, 0.22s) ease;
}

/* Open width comes from an inline style (draggable); the class only
   signals state. Dragging disables the width transition for 1:1 tracking. */
.agent-sidebar-dock.is-resizing {
  transition: none;
}

.agent-sidebar-resizer {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 6px;
  cursor: col-resize;
  z-index: 5;
}

.agent-sidebar-resizer:hover,
.agent-sidebar-resizer:active {
  background: var(--td-brand-color-light);
}

.agent-sidebar-panel {
  /* Fixed inner width: the collapsing dock must not reflow the chat. */
  height: 100%;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container);
  border-radius: var(--app-radius-lg, 12px) 0 0 var(--app-radius-lg, 12px);
  overflow: hidden;
  /* 收件箱贴底面板的定位参照。 */
  position: relative;
}

.agent-sidebar-header {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 10px 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.agent-sidebar-header-row {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

/* Row 1: the session switcher takes the remaining width; the title
   truncates with an ellipsis when long. */
.agent-sidebar-session-trigger {
  flex: 1;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border: none;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-primary);
  cursor: pointer;
  text-align: left;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.agent-sidebar-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

/* Same round ghost icon button as the KB page's kb-settings-button (the
   class itself is scoped elsewhere, so restyle it locally). */
.agent-sidebar-close {
  width: 30px;
  height: 30px;
  flex-shrink: 0;
  border: none;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container);
    color: var(--td-text-color-primary);
  }
}

/* Row 2: fixed assistant badge + new chat, compact ghost visuals. */
.agent-sidebar-header-tools {
  gap: 4px;
}

.agent-sidebar-agent-badge {
  flex: 1;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border-radius: var(--app-radius-md);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  background: var(--td-bg-color-secondarycontainer);
}

.agent-sidebar-agent-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-sidebar-new-session {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 3px 8px;
  border: none;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }
}

/* Recent-chat menu (teleported to <body> by t-popup). */
.agent-sidebar-session-menu {
  width: 300px;
  max-height: 360px;
  overflow-y: auto;
  padding: 4px;
}

.agent-sidebar-session-item {
  width: 100%;
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border: none;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-primary);
  cursor: pointer;
  text-align: left;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.is-active {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }
}

.agent-sidebar-session-item-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-sm);
}

.agent-sidebar-session-item-time {
  flex-shrink: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.agent-sidebar-session-empty {
  padding: 20px 10px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.agent-sidebar-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

/* Chat metrics inside the dock (sidebar-scope overrides): ChatView ships
   main-chat sizing — 16px body text and no horizontal inset on
   .msg_list.is-embedded, because the main chat page supplies the gutter
   itself. Inside a 260-640px panel that reads oversized and flush against
   both edges, so normalize here instead of touching the shared chat
   components (main chat keeps its current metrics). The dock unifies all
   chat body copy at --app-text-base (14px) — the same size as the user
   bubbles in the transcript, so input box, answers, and history read as
   one scale. Selector specificity (0,4,0 via .agent-sidebar-panel) must
   beat the embedded chat components' own 16px rules —
   .answer-content.markdown-content (AgentStreamDisplay) and
   :deep(.t-textarea__inner) (Input-field) — whose styles inject later
   than this component's. */
.agent-sidebar-body :deep(.msg_list.is-embedded) {
  padding: 4px 16px 16px;
}

.agent-sidebar-panel .agent-sidebar-body :deep(.bot_msg),
.agent-sidebar-panel .agent-sidebar-body :deep(.user_msg),
.agent-sidebar-panel .agent-sidebar-body :deep(.markdown-content),
.agent-sidebar-panel .agent-sidebar-body :deep(.answer-content.markdown-content),
.agent-sidebar-panel .agent-sidebar-body :deep(.t-textarea__inner) {
  font-size: var(--app-text-base);
}

/* Input-field hardcodes the placeholder at --app-text-xl; keep it on the
   dock's unified body size too. */
.agent-sidebar-panel .agent-sidebar-body :deep(.t-textarea__inner::placeholder) {
  font-size: var(--app-text-base);
}

.agent-sidebar-empty {
  padding: 40px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}

/* ── 收件箱入口按钮（header row 2）────────────────────────────────── */
.agent-sidebar-inbox-btn {
  position: relative;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &.has-unread {
    color: var(--td-brand-color);
  }
}

.agent-sidebar-inbox-count {
  position: absolute;
  top: -3px;
  right: -5px;
  min-width: 14px;
  height: 14px;
  padding: 0 3px;
  border-radius: 7px;
  background: var(--td-error-color);
  color: #fff;
  font-size: 10px;
  line-height: 14px;
  text-align: center;
  font-weight: 600;
}

/* ── 收件箱贴底面板 ───────────────────────────────────────────────── */
.agent-inbox-sheet {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: min(55%, 420px);
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border-top: 1px solid var(--td-component-stroke);
  box-shadow: 0 -6px 18px rgba(0, 0, 0, 0.08);
  z-index: 10;
}

.agent-inbox-sheet-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.agent-inbox-sheet-title {
  flex: 1;
  min-width: 0;
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.agent-inbox-sheet-actions {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.agent-inbox-action {
  border: none;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  padding: 3px 8px;
  cursor: pointer;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  &:disabled {
    opacity: 0.5;
    cursor: default;
  }
}

.agent-inbox-icon-action {
  width: 26px;
  height: 26px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }
}

.agent-inbox-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 4px 8px 8px;
}

.agent-inbox-empty {
  padding: 28px 12px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.agent-inbox-item {
  position: relative;
  display: flex;
  gap: 8px;
  padding: 7px 8px 7px 14px;
  border-radius: var(--app-radius-md);

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.is-unread .agent-inbox-item-type {
    font-weight: 600;
  }
}

.agent-inbox-unread-dot {
  position: absolute;
  left: 4px;
  top: 13px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--td-brand-color);
}

.agent-inbox-item-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.agent-inbox-item-row1 {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
}

.agent-inbox-item-type {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.agent-inbox-item-priority {
  flex-shrink: 0;
  font-size: 10px;
  line-height: 16px;
  padding: 0 5px;
  border-radius: 4px;
  font-weight: 600;

  &.prio-p0 {
    background: var(--td-error-color-2, rgba(226, 60, 68, 0.12));
    color: var(--td-error-color);
  }

  &.prio-p1 {
    background: var(--td-warning-color-2, rgba(237, 123, 47, 0.12));
    color: var(--td-warning-color);
  }

  &.prio-p2 {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-placeholder);
  }
}

.agent-inbox-item-time {
  flex-shrink: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.agent-inbox-item-summary {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}
</style>

<style>
/* Same chat canvas sizing as .wiki-fix-drawer so the embedded ChatView
   fills the panel identically. */
.agent-sidebar-dock .t-loading__parent {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.agent-sidebar-dock .t-loading__parent > * {
  flex: 1;
  min-height: 0;
}
</style>
