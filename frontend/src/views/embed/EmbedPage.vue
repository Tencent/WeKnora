<template>
  <div class="embed-page" :style="pageStyle">
    <div v-if="loadError" class="embed-error">{{ loadError }}</div>
    <template v-else-if="config">
      <div v-if="config.welcome_message && !hasMessages" class="embed-welcome">
        {{ config.welcome_message }}
      </div>
      <EmbedChatView
        v-if="sessionId"
        :session-id="sessionId"
        :channel-id="channelId"
        :token="token"
        :agent-id="config.agent_id"
        :kb-ids="kbIds"
        @messages-change="hasMessages = $event"
      />
      <div v-else class="embed-loading">{{ $t('embedPublish.loading') }}</div>
    </template>
    <div v-else-if="awaitingToken" class="embed-loading">{{ $t('embedPublish.awaitingToken') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import EmbedChatView from '@/views/embed/EmbedChatView.vue'
import { useEmbedBridge } from '@/composables/useEmbedBridge'

const route = useRoute()
const channelId = ref(String(route.params.channelId || ''))
const hasMessages = ref(false)

const {
  token,
  config,
  sessionId,
  loadError,
  awaitingToken,
} = useEmbedBridge(channelId)

const kbIds = computed(() => {
  const cfg = config.value
  if (!cfg) return []
  if (cfg.knowledge_base_ids?.length) return cfg.knowledge_base_ids
  return cfg.knowledge_base_id ? [cfg.knowledge_base_id] : []
})

const pageStyle = computed(() => {
  const color = config.value?.primary_color
  if (!color) return {}
  return { '--embed-primary': color } as Record<string, string>
})

watch(config, (cfg) => {
  if (cfg?.page_title) {
    document.title = cfg.page_title
  } else if (cfg?.name) {
    document.title = cfg.name
  }
}, { immediate: true })
</script>

<style scoped>
.embed-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container, #fff);
  overflow: hidden;
}
.embed-page :deep(.chat) {
  flex: 1;
  min-height: 0;
  height: 0;
}
.embed-page :deep(.control-btn.is-agent),
.embed-page :deep(.t-button--theme-primary) {
  --td-brand-color: var(--embed-primary, var(--td-brand-color));
}
.embed-welcome {
  padding: 12px 16px;
  font-size: 14px;
  color: var(--td-text-color-secondary);
  border-bottom: 1px solid var(--td-component-border);
  border-left: 3px solid var(--embed-primary, var(--td-brand-color));
}
.embed-error,
.embed-loading {
  padding: 24px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
