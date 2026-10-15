<template>
  <t-tooltip v-if="context" :content="tooltip" placement="top">
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

const icon = computed(() => {
  if (context.value?.agentId) return 'robot'
  if (context.value?.wikiSlug) return 'book-open'
  if (context.value?.knowledgeId) return 'file'
  return 'folder'
})

const segments = computed(() => {
  const c = context.value
  if (!c) return []
  if (c.agentId) return [c.agentName || c.agentId]
  const leaf = c.wikiTitle || c.wikiSlug || c.knowledgeTitle
  const path = [c.knowledgeBaseName, leaf].filter((s): s is string => !!s)
  return path.length ? path : [t('assistant.context.currentPage')]
})

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
