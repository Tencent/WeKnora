<template>
  <div class="assistant-context-chip" :class="{ 'is-detached': !assistant.contextAttached }">
    <t-icon name="window" class="assistant-context-chip__icon" aria-hidden="true" />
    <span class="assistant-context-chip__label">{{ t(segments.length && !assistant.contextAttached
      ? 'assistant.context.detachedLabel' : 'assistant.context.currentPage') }}</span>
    <span class="assistant-context-chip__sep" aria-hidden="true">·</span>
    <t-tooltip :content="tooltip" placement="top">
      <span class="assistant-context-chip__path">{{ segments.join(' / ') || '—' }}</span>
    </t-tooltip>
    <button v-if="segments.length" type="button" class="assistant-context-chip__toggle"
      :aria-label="assistant.contextAttached ? t('assistant.context.detach') : tooltip"
      :title="assistant.contextAttached ? t('assistant.context.detach') : tooltip"
      @click.stop="assistant.contextAttached ? assistant.dismissPageContext() : assistant.restorePageContext()">
      <t-icon :name="assistant.contextAttached ? 'close' : 'add'" size="12px" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'

const { t } = useI18n()
const assistant = useAssistantStore()
const context = computed(() => assistant.pageContext)
const view = computed(() => assistant.pageView)

const segments = computed(() => {
  const base = pageSegments()
  if (!view.value) return base
  // The page stays as the parent of the dialog open over it, once.
  const parent = base.slice(0, 1)
  const viewParts = view.value.split(' / ')
  return viewParts[0] === parent[0] ? [...parent, ...viewParts.slice(1)] : [...parent, ...viewParts]
})

function pageSegments(): string[] {
  const c = context.value
  if (!c) return assistant.pageTitle ? assistant.pageTitle.split(' / ') : []
  if (c.messageId) return [t('assistant.context.chatMessage')]
  if (c.modelId) return [t('assistant.context.pages.settings_models'), c.modelName || c.modelId]
  if (c.mcpServiceId) return [t('assistant.context.pages.mcp_services'), c.mcpServiceName || c.mcpServiceId]
  if (c.agentId) return [c.agentName || c.agentId]
  if (!c.knowledgeBaseId && c.page) return [t(`assistant.context.pages.${c.page}`)]
  const leaf = c.wikiTitle || c.wikiSlug || c.knowledgeTitle
  const path = [c.knowledgeBaseName, leaf].filter((s): s is string => !!s)
  return path.length ? path : [t('assistant.context.currentPage')]
}

const tooltip = computed(() => {
  const path = segments.value.join(' / ')
  if (!path) return t('assistant.context.currentPage')
  return assistant.contextAttached
    ? t('assistant.context.attachedTip', { path })
    : t('assistant.context.detachedTip', { path })
})
</script>

<style lang="less" scoped>
.assistant-context-chip {
  display: flex;
  align-items: center;
  gap: 6px;
  flex: 1;
  min-width: 0;
  height: 28px;
  box-sizing: border-box;
  font-size: var(--app-text-sm, 13px);
  line-height: 20px;
  color: var(--td-text-color-secondary);
}

.assistant-context-chip__icon {
  flex-shrink: 0;
  font-size: var(--app-text-base);
  color: var(--td-text-color-placeholder);
}

.assistant-context-chip__label {
  flex-shrink: 0;
  white-space: nowrap;
}

.assistant-context-chip__path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);

  .is-detached & {
    color: var(--td-text-color-placeholder);
  }
}

.assistant-context-chip__sep {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.assistant-context-chip__toggle {
  appearance: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}
</style>
