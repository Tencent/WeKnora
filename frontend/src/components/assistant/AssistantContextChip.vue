<template>
  <t-tooltip v-if="segments.length" :content="tooltip" placement="top">
    <span v-if="assistant.contextAttached" class="assistant-context-chip">
      <t-icon :name="icon" class="assistant-context-chip__icon" />
      <span class="assistant-context-chip__path">
        <template v-for="(segment, index) in segments" :key="index">
          <span v-if="index > 0" class="assistant-context-chip__sep" aria-hidden="true">/</span>
          <span class="assistant-context-chip__segment" :class="{ 'is-leaf': index === segments.length - 1 }">
            {{ segment }}
          </span>
        </template>
      </span>
      <span class="assistant-context-chip__remove" role="button" :aria-label="t('assistant.context.detach')"
        @click.stop="assistant.dismissPageContext()">×</span>
    </span>
    <button v-else type="button" class="assistant-context-chip is-detached" @click="assistant.restorePageContext()">
      <t-icon name="add" class="assistant-context-chip__icon" />
      <span class="assistant-context-chip__path">
        <span class="assistant-context-chip__segment is-leaf">{{ segments[segments.length - 1] }}</span>
      </span>
    </button>
  </t-tooltip>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'

const { t } = useI18n()
const assistant = useAssistantStore()
const context = computed(() => assistant.pageContext)
const view = computed(() => assistant.pageView)

const icon = computed(() => {
  if (view.value) return 'edit-1'
  const c = context.value
  if (!c) return 'browse'
  if (c.messageId) return 'chat-message'
  if (c.modelId || c.page === 'settings_models') return 'control-platform'
  if (c.mcpServiceId || c.page === 'mcp_services') return 'tools'
  if (c.agentId) return 'robot'
  if (c.wikiSlug) return 'book-open'
  if (c.knowledgeId) return 'file'
  return 'folder'
})

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
  return assistant.contextAttached
    ? t('assistant.context.attachedTip', { path })
    : t('assistant.context.detachedTip', { path })
})
</script>

<style lang="less" scoped>
@import '@/components/css/chat-resource-chips.less';

.assistant-context-chip {
  .chat-resource-chip-surface();

  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 26px;
  max-width: 100%;
  min-width: 0;
  padding: 3px 6px;
  border-radius: var(--td-radius-medium);
  box-sizing: border-box;
  font-size: var(--app-text-sm);
  font-weight: 500;
  line-height: 18px;
  transition: background var(--app-motion-fast), border-color var(--app-motion-fast);

  &:hover {
    .chat-resource-chip-hover();
  }

  &.is-detached {
    border-style: dashed;
    background: transparent;
    box-shadow: none;
    color: var(--td-text-color-secondary);
    cursor: pointer;
    font-family: inherit;
  }
}

.assistant-context-chip__icon {
  flex-shrink: 0;
  font-size: var(--app-text-base);
  color: var(--td-brand-color);

  .is-detached & {
    color: inherit;
  }
}

.assistant-context-chip__path {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.assistant-context-chip__segment {
  min-width: 0;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-secondary);
  font-weight: 400;

  &.is-leaf {
    flex-shrink: 0.5;
    max-width: 240px;
    color: inherit;
    font-weight: 500;
  }
}

.assistant-context-chip__sep {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  font-weight: 400;
}

.assistant-context-chip__remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 1px;
  border-radius: 50%;
  font-size: var(--app-text-base);
  line-height: 1;
  font-weight: 400;
  cursor: pointer;
  opacity: 0.5;
  transition: opacity var(--app-motion-fast), background var(--app-motion-fast), color var(--app-motion-fast);
  color: currentColor;
  flex-shrink: 0;

  .assistant-context-chip:hover & {
    opacity: 0.8;
  }

  &:hover {
    opacity: 1;
    background: var(--td-bg-color-component);
    color: var(--td-text-color-primary);
  }
}
</style>
