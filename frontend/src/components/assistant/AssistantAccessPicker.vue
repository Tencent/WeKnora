<template>
  <t-popup v-model:visible="open" trigger="click" placement="top-left"
    :overlay-inner-style="{ padding: '4px', borderRadius: 'var(--app-radius-lg)' }">
    <button type="button" class="assistant-access-trigger" :class="`is-${assistant.effectiveAccessLevel}`"
      :aria-label="`${t('assistant.access.title')}: ${t(`assistant.access.levels.${assistant.effectiveAccessLevel}.label`)}`"
      aria-haspopup="menu" :aria-expanded="open" @keydown.esc="open = false">
      <t-icon :name="LEVEL_ICONS[assistant.effectiveAccessLevel]" class="assistant-access-trigger__icon" />
      <span class="assistant-access-trigger__label">
        {{ t(`assistant.access.levels.${assistant.effectiveAccessLevel}.label`) }}
      </span>
      <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" class="assistant-access-trigger__arrow"
        :class="{ rotate: open }" aria-hidden="true">
        <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
      </svg>
    </button>
    <template #content>
      <div class="assistant-access-menu" role="menu" :aria-label="t('assistant.access.title')"
        @keydown.esc="open = false">
        <div class="assistant-access-menu__title" role="presentation">{{ t('assistant.access.title') }}</div>
        <button v-for="level in ASSISTANT_ACCESS_LEVELS" :key="level" type="button" role="menuitemradio"
          class="assistant-access-option" :class="{ selected: level === assistant.effectiveAccessLevel }"
          :aria-checked="level === assistant.effectiveAccessLevel" :disabled="!allowed(level)"
          @click="pick(level)">
          <t-icon :name="LEVEL_ICONS[level]" class="assistant-access-option__icon" />
          <span class="assistant-access-option__text">
            <span class="assistant-access-option__label">{{ t(`assistant.access.levels.${level}.label`) }}</span>
            <span class="assistant-access-option__desc">
              {{ allowed(level) ? t(`assistant.access.levels.${level}.desc`) : t('assistant.access.capped') }}
            </span>
          </span>
          <t-icon v-if="level === assistant.effectiveAccessLevel" name="check" size="14px"
            class="assistant-access-option__check" />
        </button>
        <button type="button" role="menuitemcheckbox" class="assistant-access-option assistant-access-page"
          :aria-checked="assistant.pageAccess" @click="assistant.setPageAccess(!assistant.pageAccess)">
          <t-icon name="window" class="assistant-access-option__icon" />
          <span class="assistant-access-option__text">
            <span class="assistant-access-option__label">{{ t('assistant.access.page.label') }}</span>
            <span class="assistant-access-option__desc">{{ t('assistant.access.page.desc') }}</span>
          </span>
          <t-switch :value="assistant.pageAccess" size="small" class="assistant-access-page__switch"
            aria-hidden="true" tabindex="-1" />
        </button>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'
import {
  ASSISTANT_ACCESS_LEVELS,
  capAssistantAccessLevel,
  type AssistantAccessLevel,
} from '@/api/assistant'

const LEVEL_ICONS: Record<AssistantAccessLevel, string> = {
  read_only: 'browse',
  confirm: 'secured',
  auto_write: 'edit',
}

const { t } = useI18n()
const assistant = useAssistantStore()
const open = ref(false)

const allowed = (level: AssistantAccessLevel) => capAssistantAccessLevel(level, assistant.accessCeiling) === level

function pick(level: AssistantAccessLevel) {
  if (!allowed(level)) return
  assistant.setAccessLevel(level)
  open.value = false
}
</script>

<style lang="less" scoped>
.assistant-access-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  height: 28px;
  padding: 0 8px;
  box-sizing: border-box;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-sm);
  font-weight: 500;
  cursor: pointer;
  transition: background var(--app-motion-instant);

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover);
  }

  // The level that acts without asking stands out, so it is never on by accident.
  &.is-auto_write {
    color: var(--td-warning-color);
  }
}

.assistant-access-trigger__icon {
  font-size: var(--app-text-base);
}

.assistant-access-trigger__label {
  white-space: nowrap;
}

.assistant-access-trigger__arrow {
  width: 10px;
  height: 10px;
  color: var(--td-text-color-placeholder);
  transition: transform var(--app-motion-instant);

  &.rotate {
    transform: rotate(180deg);
  }
}

.assistant-access-menu {
  width: 260px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.assistant-access-menu__title {
  padding: 6px 8px;
  margin-bottom: 2px;
  border-bottom: .5px solid var(--td-component-stroke);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  font-weight: 500;
  line-height: 20px;
}

.assistant-access-option {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 6px 8px;
  border: 0;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;

  &:hover:not(:disabled), &:focus-visible {
    background: var(--td-bg-color-secondarycontainer-hover);
  }

  &.selected {
    background: var(--td-bg-color-secondarycontainer);
  }

  &:disabled {
    cursor: not-allowed;
    color: var(--td-text-color-disabled);
  }
}

.assistant-access-option__icon {
  flex-shrink: 0;
  margin-top: 2px;
  font-size: var(--app-text-base);
  color: var(--td-text-color-secondary);

  .assistant-access-option:disabled & {
    color: inherit;
  }
}

.assistant-access-option__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.assistant-access-option__label {
  font-size: var(--app-text-sm);
  font-weight: 500;
  line-height: 20px;
}

.assistant-access-option__desc {
  font-size: var(--app-text-xs);
  line-height: 16px;
  color: var(--td-text-color-placeholder);
}

.assistant-access-page {
  margin-top: 2px;
  padding-top: 8px;
  border-top: .5px solid var(--td-component-stroke);
  border-radius: 0 0 var(--app-radius-sm) var(--app-radius-sm);
}

.assistant-access-page__switch {
  flex-shrink: 0;
  margin-top: 2px;
  pointer-events: none;
}

.assistant-access-option__check {
  flex-shrink: 0;
  margin-top: 3px;
  color: var(--td-brand-color);
}
</style>
