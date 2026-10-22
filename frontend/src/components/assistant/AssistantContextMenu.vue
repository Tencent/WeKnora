<template>
  <t-popup v-model:visible="open" trigger="click" placement="top-left"
    :overlay-inner-style="{ padding: '4px', borderRadius: 'var(--app-radius-lg)' }">
    <button type="button" class="assistant-context-trigger" :aria-label="t('assistant.context.currentPage')"
      :title="t('assistant.context.currentPage')" aria-haspopup="dialog" :aria-expanded="open"
      @keydown.esc="open = false">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor"
        stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M12 4v16M4 12h16" />
      </svg>
    </button>
    <template #content>
      <div class="assistant-context-menu" role="dialog" :aria-label="t('assistant.context.currentPage')"
        @keydown.esc="open = false">
        <div class="assistant-context-menu__page">
          <AssistantContextChip />
        </div>
        <button type="button" role="switch" class="assistant-context-menu__access"
          :aria-label="t('assistant.access.page.label')" :aria-checked="assistant.pageAccess"
          @click="assistant.setPageAccess(!assistant.pageAccess)">
          <t-icon name="window" class="assistant-context-menu__icon" aria-hidden="true" />
          <span class="assistant-context-menu__text">
            <span class="assistant-context-menu__label">{{ t('assistant.access.page.label') }}</span>
            <span class="assistant-context-menu__desc">{{ t('assistant.access.page.desc') }}</span>
          </span>
          <span class="assistant-context-menu__switch" :class="{ 'is-on': assistant.pageAccess }" aria-hidden="true" />
        </button>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'
import AssistantContextChip from './AssistantContextChip.vue'

const { t } = useI18n()
const assistant = useAssistantStore()
const open = ref(false)
</script>

<style lang="less" scoped>
.assistant-context-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 0;
  font: inherit;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: background var(--app-motion-instant), color var(--app-motion-instant);
  user-select: none;

  &:hover, &[aria-expanded='true'] {
    background: var(--td-bg-color-secondarycontainer-hover);
    color: var(--td-text-color-primary);
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 1px;
  }
}

.assistant-context-menu {
  width: 300px;
  max-width: calc(100vw - 40px);
}

.assistant-context-menu__page {
  display: flex;
  padding: 6px 8px;
  margin-bottom: 4px;
  border-bottom: .5px solid var(--td-component-stroke);
}

.assistant-context-menu__access {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  width: 100%;
  padding: 8px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:hover, &:focus-visible {
    background: var(--td-bg-color-secondarycontainer-hover);
  }
}

.assistant-context-menu__icon {
  flex-shrink: 0;
  margin-top: 2px;
  font-size: var(--app-text-base);
  color: var(--td-text-color-secondary);
}

.assistant-context-menu__text {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.assistant-context-menu__label {
  font-size: var(--app-text-sm);
  font-weight: 500;
  line-height: 20px;
}

.assistant-context-menu__desc {
  font-size: var(--app-text-xs);
  line-height: 16px;
  color: var(--td-text-color-placeholder);
}

.assistant-context-menu__switch {
  flex-shrink: 0;
  width: 28px;
  height: 16px;
  margin-top: 2px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-component);
  transition: background var(--app-motion-fast);

  &::after {
    content: '';
    display: block;
    width: 12px;
    height: 12px;
    margin: 2px;
    border-radius: 50%;
    background: var(--td-text-color-anti);
    transition: transform var(--app-motion-fast);
  }

  &.is-on {
    background: var(--td-brand-color);

    &::after {
      transform: translateX(12px);
    }
  }
}
</style>
