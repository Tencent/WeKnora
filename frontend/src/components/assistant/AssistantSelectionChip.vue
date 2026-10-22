<template>
  <t-tooltip v-if="selection" placement="top" :content="tooltip" :overlay-inner-style="{ maxWidth: '360px' }">
    <span class="assistant-selection-chip">
      <t-icon name="cursor" class="assistant-selection-chip__icon" />
      <span class="assistant-selection-chip__label">{{ label }}</span>
      <span class="assistant-selection-chip__text">{{ excerpt }}</span>
      <span class="assistant-selection-chip__remove" role="button" :aria-label="t('assistant.selection.detach')"
        @click.stop="assistant.setPageSelection(null)">×</span>
    </span>
  </t-tooltip>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'

const { t } = useI18n()
const assistant = useAssistantStore()
const selection = computed(() => assistant.pageSelection)

const flat = (text: string) => text.replace(/\s+/g, ' ').trim()

const label = computed(() => {
  const s = selection.value
  if (!s) return ''
  if (s.startLine && s.endLine && s.endLine > s.startLine) {
    return t('assistant.selection.lines', { count: s.endLine - s.startLine + 1 })
  }
  return t('assistant.selection.label')
})

const excerpt = computed(() => {
  const text = flat(selection.value?.text || '')
  return text.length > 24 ? `${text.slice(0, 23)}…` : text
})

const tooltip = computed(() => {
  const s = selection.value
  if (!s) return ''
  const text = flat(s.text)
  const body = text.length > 300 ? `${text.slice(0, 299)}…` : text
  return s.field ? t('assistant.selection.tipField', { field: s.field, text: body }) : t('assistant.selection.tip', { text: body })
})
</script>

<style lang="less" scoped>
@import '@/components/css/chat-resource-chips.less';

.assistant-selection-chip {
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
  line-height: 18px;

  &:hover {
    .chat-resource-chip-hover();
  }
}

.assistant-selection-chip__icon {
  flex-shrink: 0;
  font-size: var(--app-text-base);
  color: var(--td-brand-color);
}

.assistant-selection-chip__label {
  flex-shrink: 0;
  font-weight: 500;
  white-space: nowrap;
}

.assistant-selection-chip__text {
  min-width: 0;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-secondary);
}

.assistant-selection-chip__remove {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  font-size: var(--app-text-base);
  line-height: 1;
  cursor: pointer;
  opacity: 0.5;
  transition: opacity var(--app-motion-fast), background var(--app-motion-fast);

  .assistant-selection-chip:hover & {
    opacity: 0.8;
  }

  &:hover {
    opacity: 1;
    background: var(--td-bg-color-component);
  }
}
</style>
