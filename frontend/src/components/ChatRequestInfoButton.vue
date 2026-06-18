<template>
  <t-popup
    v-model:visible="visible"
    trigger="click"
    placement="top"
    :show-arrow="true"
    destroy-on-close
    overlay-class-name="chat-request-info-popup"
    :overlay-inner-style="{ padding: 0 }"
  >
    <t-button
      size="small"
      variant="outline"
      shape="round"
      :title="$t('chat.requestInfoTitle')"
    >
      <t-icon name="info-circle" />
    </t-button>
    <template #content>
      <div class="chat-request-card" @click.stop>
        <div class="chat-request-card-header">
          <span class="chat-request-card-title">{{ $t('chat.requestInfoTitle') }}</span>
          <t-button
            v-if="rows.length > 0"
            size="small"
            variant="text"
            shape="square"
            :title="$t('common.copy')"
            @click="copyAll"
          >
            <t-icon name="copy" />
          </t-button>
        </div>
        <div v-if="rows.length === 0" class="chat-request-empty">
          {{ $t('chat.requestInfoEmpty') }}
        </div>
        <div v-else class="chat-request-card-body">
          <div v-for="row in rows" :key="row.key" class="chat-request-row">
            <span class="chat-request-label">{{ row.label }}</span>
            <span class="chat-request-value">{{ row.value }}</span>
          </div>
        </div>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  buildChatRequestDebugPayload,
  type ChatRequestDebugInfo,
} from '@/utils/chatRequestDebug';
import { copyTextToClipboard } from '@/utils/chatMessageShared';

const props = defineProps<{
  session: Record<string, unknown>;
  sessionId?: string;
}>();

const { t } = useI18n();
const visible = ref(false);

const debugInfo = computed((): ChatRequestDebugInfo => {
  const s = props.session;
  const dr = s.debugRequest as ChatRequestDebugInfo | undefined;
  return {
    requestId: (s.request_id as string) || dr?.requestId,
    messageId: (s.id as string) || undefined,
    sessionId: props.sessionId || dr?.sessionId,
    url: dr?.url,
    method: dr?.method,
    body: dr?.body ?? null,
    sentAt: dr?.sentAt,
  };
});

const rows = computed(() => {
  const info = debugInfo.value;
  const list: { key: string; label: string; value: string }[] = [];
  const add = (key: string, labelKey: string, val?: string) => {
    if (!val) return;
    list.push({ key, label: t(labelKey), value: val });
  };
  add('requestId', 'chat.requestInfoRequestId', info.requestId);
  add('messageId', 'chat.requestInfoMessageId', info.messageId);
  add('sessionId', 'chat.requestInfoSessionId', info.sessionId);
  if (info.method && info.url) {
    list.push({ key: 'url', label: t('chat.requestInfoUrl'), value: `${info.method} ${info.url}` });
  }
  if (info.sentAt) {
    add('sentAt', 'chat.requestInfoSentAt', new Date(info.sentAt).toLocaleString());
  }
  return list;
});

const copyAll = async () => {
  try {
    await copyTextToClipboard(buildChatRequestDebugPayload(debugInfo.value));
    MessagePlugin.success(t('common.copied'));
    visible.value = false;
  } catch {
    MessagePlugin.error(t('common.copyFailed'));
  }
};
</script>

<style lang="less">
.chat-request-info-popup {
  .t-popup__content {
    padding: 0;
    border-radius: 8px;
    box-shadow: var(--td-shadow-2);
  }
}
</style>

<style scoped lang="less">
.chat-request-card {
  min-width: 260px;
  max-width: 360px;
  padding: 10px 12px;
  font-size: 12px;
  color: var(--td-text-color-primary);
}

.chat-request-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--td-component-stroke);
}

.chat-request-card-title {
  font-size: 12px;
  font-weight: 600;
}

.chat-request-card-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.chat-request-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 3px 0;
  line-height: 1.5;
}

.chat-request-label {
  flex: 0 0 72px;
  color: var(--td-text-color-secondary);
}

.chat-request-value {
  flex: 1;
  font-family: var(--td-font-family-mono, ui-monospace, SFMono-Regular, Menlo, monospace);
  font-size: 11px;
  color: var(--td-text-color-primary);
  word-break: break-all;
}

.chat-request-empty {
  color: var(--td-text-color-placeholder);
  padding: 4px 0 8px;
}

</style>
