<template>
  <div class="embed-page" :style="pageStyle">
    <div v-if="loadError" class="embed-error">{{ loadError }}</div>
    <template v-else-if="config">
      <header v-if="sessionId" class="embed-header">
        <span class="embed-header__badge" :style="badgeStyle">
          <span v-if="config.agent_avatar" class="embed-header__avatar">{{ config.agent_avatar }}</span>
          <t-icon v-else :name="headerIcon" size="18px" />
        </span>
        <div class="embed-header__text">
          <h1 class="embed-header__title">{{ headerTitle }}</h1>
          <p v-if="headerSubtitle" class="embed-header__subtitle">{{ headerSubtitle }}</p>
        </div>
        <t-button
          variant="text"
          shape="square"
          size="small"
          class="embed-header__action"
          :disabled="!chatHasMessages"
          :title="$t('embedPublish.newChat')"
          :aria-label="$t('embedPublish.newChat')"
          @click="handleNewChat"
        >
          <template #icon><t-icon name="add" /></template>
        </t-button>
      </header>

      <EmbedChatView
        v-if="sessionId"
        :session-id="sessionId"
        :session-sig="sessionSig"
        :channel-id="channelId"
        :token="token"
        :agent-id="config.agent_id"
        :kb-ids="kbIds"
        :welcome-message="config.welcome_message"
        :show-suggested-questions="config.show_suggested_questions !== false"
        :allow-web-search="config.allow_web_search === true"
        :agent-web-search-enabled="config.agent_web_search_enabled === true"
        :allow-file-upload="config.allow_file_upload === true"
        :agent-image-upload-enabled="config.agent_image_upload_enabled === true"
        :use-session-header-title="useSessionHeaderTitle"
        :host-context="hostContext"
        @session-title="sessionTitle = $event"
        @messages-state="chatHasMessages = $event"
      />
      <div v-else class="embed-loading">{{ $t('embedPublish.loading') }}</div>
    </template>
    <div v-else-if="awaitingToken" class="embed-loading">{{ $t('embedPublish.awaitingToken') }}</div>
    <div v-else-if="bootstrapping" class="embed-loading">{{ $t('embedPublish.loading') }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import EmbedChatView from '@/views/embed/EmbedChatView.vue'
import { useEmbedBridge } from '@/composables/useEmbedBridge'

const { t } = useI18n()
const route = useRoute()
const channelId = ref(String(route.params.channelId || ''))
const sessionTitle = ref('')
const chatHasMessages = ref(false)

const {
  token,
  config,
  sessionId,
  sessionSig,
  loadError,
  awaitingToken,
  bootstrapping,
  hostContext,
  startNewSession,
} = useEmbedBridge(channelId)

const handleNewChat = () => {
  // The current session is already empty — reuse it instead of spawning yet
  // another blank session (which would otherwise pile up server-side).
  if (!chatHasMessages.value) return
  sessionTitle.value = ''
  startNewSession()
}

const kbIds = computed(() => config.value?.knowledge_base_ids ?? [])

const pageStyle = computed(() => {
  const color = config.value?.primary_color
  if (!color) return {}
  return {
    '--embed-primary': color,
    '--td-brand-color': color,
    '--td-brand-color-hover': color,
    '--td-brand-color-active': color,
  } as Record<string, string>
})

const badgeStyle = computed(() => {
  const color = config.value?.primary_color
  if (!color) return {}
  return {
    background: `color-mix(in srgb, ${color} 12%, transparent)`,
    color,
  } as Record<string, string>
})

const channelDisplayTitle = computed(() => {
  const cfg = config.value
  if (!cfg) return ''
  return (
    cfg.display_title?.trim()
    || cfg.page_title?.trim()
    || cfg.name?.trim()
    || cfg.agent_name?.trim()
    || t('embedPublish.defaultChatTitle')
  )
})

const useSessionHeaderTitle = computed(
  () => config.value?.header_title_mode === 'session',
)

const headerTitle = computed(() => {
  if (useSessionHeaderTitle.value && sessionTitle.value.trim()) {
    return sessionTitle.value.trim()
  }
  return channelDisplayTitle.value
})

const headerSubtitle = computed(() => {
  const cfg = config.value
  if (!cfg?.agent_name) return ''
  if (useSessionHeaderTitle.value && sessionTitle.value.trim()) {
    const fallback = channelDisplayTitle.value
    if (fallback && fallback !== sessionTitle.value.trim()) {
      return fallback
    }
    return cfg.agent_name
  }
  const channelName = cfg.name?.trim()
  if (!channelName || channelName === channelDisplayTitle.value) return ''
  return cfg.agent_name
})

const headerIcon = computed(() => {
  const agentId = config.value?.agent_id || ''
  return agentId && agentId !== 'builtin-quick-answer' ? 'control-platform' : 'chat'
})

watch(headerTitle, (title) => {
  if (title) document.title = title
}, { immediate: true })
</script>

<style scoped lang="less">
.embed-page {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container, #fff);
  overflow: hidden;
  /* 子组件（含 AgentStreamDisplay）内凡用 --td-brand-color 的 loading / 强调色均跟随渠道主题 */
  --td-brand-color: var(--embed-primary, var(--td-brand-color));
  --td-brand-color-hover: var(--embed-primary, var(--td-brand-color-hover));
  --td-brand-color-active: var(--embed-primary, var(--td-brand-color-active));

  :deep(.t-button--theme-primary) {
    --td-brand-color: var(--embed-primary, var(--td-brand-color));
    --td-brand-color-hover: var(--embed-primary, var(--td-brand-color-hover));
    --td-brand-color-active: var(--embed-primary, var(--td-brand-color-active));
  }

  :deep(.embed-input-box:focus-within) {
    border-color: var(--embed-primary, var(--td-brand-color));
  }

  :deep(.embed-send-btn:not(.disabled)) {
    background: var(--embed-primary, var(--td-brand-color));
  }

  :deep(.loading-typing span) {
    background: var(--embed-primary, var(--td-brand-color));
  }
}

.embed-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  flex-shrink: 0;

  &__badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    border-radius: 10px;
    flex-shrink: 0;
    background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    color: var(--td-brand-color);
  }

  &__avatar {
    font-size: 20px;
    line-height: 1;
  }

  &__text {
    min-width: 0;
    flex: 1;
  }

  &__action {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);

    &:hover {
      color: var(--td-brand-color);
    }
  }

  &__title {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    line-height: 1.35;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__subtitle {
    margin: 2px 0 0;
    font-size: 12px;
    line-height: 1.4;
    color: var(--td-text-color-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.embed-error,
.embed-loading {
  padding: 24px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}
</style>
