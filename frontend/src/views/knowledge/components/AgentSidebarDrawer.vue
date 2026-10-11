<template>
  <!-- Agent sidebar (agent-sidebar-inbox P1): a persistent chat panel bound
       to the KB view. Docked inside the KB body row — opening it narrows the
       main content instead of covering it, and it survives tab switches by
       construction. Collapsed state is width:0 + hidden, so the panel keeps
       its DOM (and the chat scrollback) across toggles. -->
  <aside class="agent-sidebar-dock" :class="{ 'is-open': visible }" :aria-hidden="!visible">
    <div class="agent-sidebar-panel">
      <header class="agent-sidebar-header">
        <span class="agent-sidebar-title">{{ $t('knowledgeBase.agentSidebar.title') }}</span>
        <button type="button" class="kb-settings-button" :aria-label="$t('knowledgeBase.agentSidebar.title')"
          @click="visible = false">
          <t-icon name="close" size="16px" />
        </button>
      </header>
      <div class="agent-sidebar-body">
        <t-loading :loading="sessionLoading" text="" size="small" show-overlay>
          <!-- v-show (not v-if) so the conversation and scroll position stay
               alive while the panel is closed. -->
          <ChatView v-if="sessionId" v-show="visible" :session_id="sessionId" agentId="builtin-sidebar-assistant"
            :kbIds="props.kbId ? [props.kbId] : []" :embeddedMode="true" />
          <div v-else-if="!sessionLoading" class="agent-sidebar-empty">
            {{ $t('knowledgeBase.agentSidebar.sessionError') }}
          </div>
        </t-loading>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import ChatView from '@/views/chat/index.vue';
import { createSessions } from '@/api/chat';

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

watch(() => props.modelValue, (val) => { visible.value = val; });
watch(visible, (val) => { emit('update:modelValue', val); });

// One persistent session per binding: each KB keeps its own conversation,
// and the KB list page (no kbId) shares one global session.
const sidebarSessionKey = (kbId?: string) => kbId ? `agentSidebarSession:${kbId}` : 'agentSidebarSession:global';

const ensureSession = async () => {
  if (sessionLoading.value) return;
  const stored = localStorage.getItem(sidebarSessionKey(props.kbId));
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
      localStorage.setItem(sidebarSessionKey(props.kbId), id);
    } else {
      MessagePlugin.error(t('knowledgeBase.agentSidebar.sessionError'));
    }
  } catch (_) {
    MessagePlugin.error(t('knowledgeBase.agentSidebar.sessionError'));
  } finally {
    sessionLoading.value = false;
  }
};

watch(() => props.kbId, () => {
  // KB switch: drop the previous session from the panel; the localStorage
  // binding survives so each KB keeps its own conversation.
  sessionId.value = '';
  if (visible.value) ensureSession();
}, { immediate: false });

watch(visible, (val) => {
  if (val && !sessionId.value) ensureSession();
});

// Create the session eagerly so the panel is ready before the first open.
ensureSession();
</script>

<style scoped>
.agent-sidebar-dock {
  flex: 0 0 auto;
  width: 0;
  height: 100%;
  overflow: hidden;
  transition: width var(--app-motion-slow, 0.22s) ease;
}

.agent-sidebar-dock.is-open {
  width: 260px;
}

.agent-sidebar-panel {
  /* Fixed inner width: the collapsing dock must not reflow the chat. */
  width: 260px;
  height: 100%;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--td-component-border);
  background: var(--td-bg-color-container);
  border-radius: var(--app-radius-lg, 12px) 0 0 var(--app-radius-lg, 12px);
  overflow: hidden;
}

.agent-sidebar-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px 10px 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.agent-sidebar-title {
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.agent-sidebar-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.agent-sidebar-empty {
  padding: 40px;
  text-align: center;
  color: var(--td-text-color-placeholder);
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
