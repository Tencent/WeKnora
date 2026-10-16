<template>
  <div class="open-page-card" @click.stop @keydown.stop>
    <t-icon :name="icon" class="open-page-card__icon" />
    <div class="open-page-card__text">
      <div class="open-page-card__title">
        <span class="open-page-card__label">{{ link.label }}</span>
        <template v-if="link.detail">
          <span class="open-page-card__sep" aria-hidden="true">/</span>
          <span class="open-page-card__detail">{{ link.detail }}</span>
        </template>
      </div>
      <div v-if="link.note" class="open-page-card__note">{{ link.note }}</div>
      <div v-if="endpoint" class="open-page-card__endpoint">
        {{ t('agentStream.openPage.endpoint') }}<code>{{ endpoint }}</code>
      </div>
      <div v-if="draft" class="open-page-card__hint">{{ t('agentStream.openPage.draftHint') }}</div>
    </div>
    <t-button size="small" variant="outline" :disabled="!location" @click="open">
      {{ draft ? t('agentStream.openPage.openForm') : t('agentStream.openPage.open') }}
    </t-button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAssistantStore } from '@/stores/assistant'
import {
  pageLinkDraft,
  pageLinkEndpoint,
  pageLinkLocation,
  type AssistantPageLink,
} from '@/utils/assistantPageLink'

const props = defineProps<{ link: AssistantPageLink }>()

const { t } = useI18n()
const router = useRouter()
const assistant = useAssistantStore()

const location = computed(() => pageLinkLocation(props.link))
const draft = computed(() => pageLinkDraft(props.link))
const endpoint = computed(() => pageLinkEndpoint(props.link))

const icon = computed(() => {
  switch (props.link.page) {
    case 'agent_editor': return 'robot'
    case 'settings_models': return 'control-platform'
    case 'mcp_services': return 'tools'
    case 'wiki': return 'book-open'
    default: return props.link.knowledge_id ? 'file' : 'folder'
  }
})

function open() {
  if (!location.value) return
  assistant.setPageDraft(draft.value)
  void router.push(location.value)
}
</script>

<style lang="less" scoped>
.open-page-card {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 8px;
  padding: 10px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium);
  background: var(--td-bg-color-container);
  cursor: default;
}

.open-page-card__icon {
  flex-shrink: 0;
  font-size: var(--app-text-lg);
  color: var(--td-brand-color);
}

.open-page-card__text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: var(--app-text-sm);
}

.open-page-card__title {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  color: var(--td-text-color-primary);
  font-weight: 500;
}

.open-page-card__label,
.open-page-card__detail {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.open-page-card__label {
  color: var(--td-text-color-secondary);
  font-weight: 400;
}

.open-page-card__sep {
  color: var(--td-text-color-placeholder);
}

.open-page-card__note,
.open-page-card__hint,
.open-page-card__endpoint {
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
}

.open-page-card__endpoint code {
  font-family: var(--app-font-mono, monospace);
  color: var(--td-text-color-primary);
}
</style>
