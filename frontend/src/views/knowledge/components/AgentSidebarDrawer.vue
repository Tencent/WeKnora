<template>
  <!-- Agent sidebar (agent-sidebar-inbox P1): a persistent chat panel bound
       to the KB view, styled after the existing wiki fix drawer so the UI
       language stays identical. Mounted at the KnowledgeBase layout level —
       it survives tab switches by construction. -->
  <t-drawer v-model:visible="visible" :header="$t('knowledgeBase.agentSidebar.title')" size="700px" :footer="false"
    class="agent-sidebar-drawer" :close-btn="true">
    <div class="agent-sidebar-body">
      <t-loading :loading="sessionLoading" text="" size="small" show-overlay>
        <ChatView v-if="sessionId && visible" :session_id="sessionId" agentId="builtin-sidebar-assistant"
          :kbIds="[props.kbId]" :embeddedMode="true" />
        <div v-else-if="!sessionLoading" class="agent-sidebar-empty">
          {{ $t('knowledgeBase.agentSidebar.sessionError') }}
        </div>
      </t-loading>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import ChatView from '@/views/chat/index.vue';
import { createSessions } from '@/api/chat';

const props = defineProps<{
  kbId: string;
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

// One persistent session per KB: reopening the sidebar continues the same
// conversation, and the menu store keeps it reachable like any chat.
const sidebarSessionKey = (kbId: string) => `agentSidebarSession:${kbId}`;

const ensureSession = async () => {
  if (!props.kbId || sessionLoading.value) return;
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
  if (props.kbId && visible.value) ensureSession();
}, { immediate: false });

watch(visible, (val) => {
  if (val && props.kbId && !sessionId.value) ensureSession();
});

// First open when the parent mounts with visible=true.
if (props.modelValue && props.kbId) ensureSession();
</script>

<style scoped>
.agent-sidebar-body {
  height: 100%;
}

.agent-sidebar-empty {
  padding: 40px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>

<style>
/* Same chat canvas sizing as .wiki-fix-drawer so the embedded ChatView
   fills the drawer identically. */
.agent-sidebar-drawer .t-drawer__content-wrapper {
  display: flex;
  flex-direction: column;
}

.agent-sidebar-drawer .t-loading__parent {
  height: 100%;
}
</style>
