<template>
  <t-dialog v-model:visible="visible" width="520px" placement="center"
    dialog-class-name="assistant-create-dialog" :z-index="2300" :close-on-overlay-click="false" :close-btn="false">
    <template #header>
      <div class="assistant-create__heading">
        <ResourceIcon type="assistant" :size="18" class="assistant-create__icon" aria-hidden="true" />
        <span>{{ t(`assistant.create.${kind}Title`) }}</span>
        <button type="button" class="assistant-create__close" :aria-label="t('common.close')" @click="visible = false">
          <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
            <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
          </svg>
        </button>
      </div>
    </template>
    <div class="assistant-create">
      <p class="assistant-create__hint">{{ t(`assistant.create.${kind}Hint`) }}</p>
      <label class="assistant-create__field">
        <span class="assistant-create__label">{{ t('assistant.create.requestLabel') }}</span>
        <t-textarea v-model="description"
          :placeholder="t(`assistant.create.${kind}Placeholder`)" :autosize="{ minRows: 4, maxRows: 8 }"
          :maxlength="2000" autofocus />
      </label>
    </div>
    <template #footer>
      <div class="assistant-create__actions">
        <t-button variant="outline" theme="default" @click="manual">{{ t('assistant.create.manual') }}</t-button>
        <t-button theme="primary" :disabled="!description.trim()" :loading="submitting" @click="create">
          <template #icon><ResourceIcon type="assistant" :size="16" /></template>
          {{ t('assistant.create.start') }}
        </t-button>
      </div>
    </template>
  </t-dialog>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAssistantStore } from '@/stores/assistant'
import ResourceIcon from '@/components/icons/ResourceIcon.vue'

const props = defineProps<{ kind: 'agent' | 'automation' }>()
const visible = defineModel<boolean>('visible', { default: false })
const emit = defineEmits<{ manual: [] }>()
const { t } = useI18n()
const assistant = useAssistantStore()
const description = ref('')
const submitting = ref(false)
watch(visible, value => { if (value) description.value = '' })

function manual() {
  visible.value = false
  emit('manual')
}

async function create() {
  if (!description.value.trim() || submitting.value) return
  submitting.value = true
  const query = t(`assistant.prompts.create${props.kind === 'agent' ? 'Agent' : 'Automation'}`, {
    request: description.value.trim(), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  })
  visible.value = false
  await nextTick()
  assistant.setPageSelection(null)
  assistant.restorePageContext()
  assistant.ask(query)
  submitting.value = false
}
</script>

<style lang="less">
.t-dialog.assistant-create-dialog {
  display: flex;
  flex-direction: column;
  max-width: calc(100vw - var(--assistant-dock-width, 0px) - 32px);
  max-height: calc(100dvh - 48px);
  box-sizing: border-box;
  padding: 0;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);

  .t-dialog__header {
    flex-shrink: 0;
    padding: 20px 52px 0 24px;
  }

  .t-dialog__body {
    min-height: 0;
    padding: 12px 24px 24px;
  }

  .t-dialog__footer {
    box-sizing: border-box;
    flex-shrink: 0;
    padding: 16px 24px;
    border-top: 1px solid var(--td-component-stroke);
  }

  .assistant-create__actions .t-button + .t-button {
    margin-left: 0;
  }

}
</style>

<style scoped lang="less">
.assistant-create {
  &__close {
    position: absolute;
    top: 16px;
    right: 16px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    padding: 0;
    border: 0;
    border-radius: var(--app-radius-sm);
    background: transparent;
    color: var(--td-text-color-secondary);
    cursor: pointer;
    transition: background var(--app-motion-base) ease, color var(--app-motion-base) ease;

    &:hover {
      background: var(--td-bg-color-container-hover);
      color: var(--td-text-color-primary);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }

  &__heading {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-xl);
    font-weight: 600;
    line-height: 24px;
  }

  &__icon {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
  }

  &__hint {
    margin: 0 0 20px;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
    line-height: 1.6;
  }

  &__label {
    display: block;
    margin-bottom: 8px;
    color: var(--td-text-color-primary);
    font-size: var(--app-text-base);
    font-weight: 500;
  }

  &__field {
    display: block;
  }

  :deep(.t-textarea__inner) {
    padding: 10px 12px;
    border-radius: var(--app-radius-sm);
    font-size: var(--app-text-base);
    line-height: 22px;
  }

  &__actions {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 8px;
  }
}
</style>
