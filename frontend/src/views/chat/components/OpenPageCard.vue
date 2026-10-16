<template>
  <div class="open-page-card" @click.stop @keydown.stop>
    <div class="open-page-card__header">
      <span class="open-page-card__icon" aria-hidden="true"><t-icon :name="icon" /></span>
      <div class="open-page-card__heading">
        <div v-if="link.detail" class="open-page-card__eyebrow">{{ link.label }}</div>
        <div class="open-page-card__title">{{ link.detail || link.label }}</div>
      </div>
      <t-button class="open-page-card__action" size="small" theme="primary" variant="outline"
        :disabled="!location" @click="open">
        {{ draft ? t('agentStream.openPage.openForm') : t('agentStream.openPage.open') }}
        <template #suffix><t-icon name="chevron-right" /></template>
      </t-button>
    </div>
    <p v-if="link.note" class="open-page-card__note">{{ link.note }}</p>
    <div v-if="endpoint || draft" class="open-page-card__footer">
      <span v-if="endpoint" class="open-page-card__endpoint">
        <span class="open-page-card__endpoint-label">{{ t('agentStream.openPage.endpoint') }}</span>
        <code>{{ endpoint }}</code>
      </span>
      <span v-if="draft" class="open-page-card__hint">
        <t-icon name="info-circle" />{{ t('agentStream.openPage.draftHint') }}
      </span>
    </div>
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
  flex-direction: column;
  gap: 8px;
  max-width: 560px;
  margin-top: 8px;
  padding: 12px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-large);
  background: var(--td-bg-color-container);
  cursor: default;
  transition: border-color var(--app-motion-fast) ease;

  &:hover {
    border-color: var(--td-component-border);
  }
}

.open-page-card__header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.open-page-card__icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: var(--td-radius-medium);
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-size: var(--app-text-xl);
}

.open-page-card__heading {
  flex: 1;
  min-width: 0;
}

.open-page-card__eyebrow,
.open-page-card__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.open-page-card__eyebrow {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs, 12px);
  line-height: 18px;
}

.open-page-card__title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  font-weight: 600;
  line-height: 22px;
}

.open-page-card__action {
  flex-shrink: 0;
  border-radius: var(--td-radius-medium);
}

.open-page-card__note {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  line-height: 1.6;
  overflow-wrap: anywhere;
}

.open-page-card__footer {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding-top: 8px;
  border-top: 1px dashed var(--td-component-stroke);
  font-size: var(--app-text-xs, 12px);
  line-height: 18px;
}

.open-page-card__endpoint {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
  color: var(--td-text-color-secondary);

  code {
    min-width: 0;
    padding: 1px 6px;
    border-radius: var(--td-radius-small);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family-mono);
    overflow-wrap: anywhere;
  }
}

.open-page-card__endpoint-label {
  flex-shrink: 0;
}

.open-page-card__hint {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  color: var(--td-text-color-placeholder);

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}
</style>
