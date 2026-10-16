<template>
  <div class="open-page-card" @click.stop @keydown.stop>
    <button type="button" class="open-page-card__main" :disabled="!location" :title="actionLabel" @click="open">
      <span class="open-page-card__icon" aria-hidden="true"><t-icon :name="icon" size="18px" /></span>
      <span class="open-page-card__body">
        <span class="open-page-card__title">{{ link.detail || link.label }}</span>
        <span v-if="meta" class="open-page-card__meta">
          <t-icon v-if="link.navigated" name="check-circle-filled" class="open-page-card__opened" />{{ meta }}
        </span>
      </span>
      <span class="open-page-card__go">
        <span v-if="draft" class="open-page-card__go-label">{{ actionLabel }}</span>
        <t-icon name="chevron-right" size="16px" />
      </span>
    </button>
    <div v-if="link.note || endpoint || draft || returnPath" class="open-page-card__extra">
      <p v-if="link.note" class="open-page-card__note">{{ link.note }}</p>
      <div v-if="endpoint" class="open-page-card__endpoint">
        <span class="open-page-card__endpoint-label">{{ t('agentStream.openPage.endpoint') }}</span>
        <code>{{ endpoint }}</code>
      </div>
      <div v-if="draft" class="open-page-card__hint">
        <t-icon name="info-circle" />{{ t('agentStream.openPage.draftHint') }}
      </div>
      <button v-if="returnPath" type="button" class="open-page-card__back" @click="goBack">
        <t-icon name="rollback" />{{ t('agentStream.openPage.back') }}
      </button>
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
  pageLinkReturnPath,
  type AssistantPageLink,
} from '@/utils/assistantPageLink'

const props = defineProps<{ link: AssistantPageLink }>()

const { t } = useI18n()
const router = useRouter()
const assistant = useAssistantStore()

const location = computed(() => pageLinkLocation(props.link))
const draft = computed(() => pageLinkDraft(props.link))
const endpoint = computed(() => pageLinkEndpoint(props.link))
const returnPath = computed(() => pageLinkReturnPath(props.link))
const actionLabel = computed(() => draft.value ? t('agentStream.openPage.openForm') : t('agentStream.openPage.open'))

const meta = computed(() => {
  const parent = props.link.detail ? props.link.label : ''
  if (!props.link.navigated) return parent
  return [t('agentStream.openPage.opened'), parent].filter(Boolean).join(' · ')
})

const icon = computed(() => {
  switch (props.link.page) {
    case 'agent_editor': return 'robot'
    case 'settings_models': return 'control-platform'
    case 'mcp_services': return 'tools'
    case 'wiki': return 'book-open'
    case 'settings': return 'setting'
    case 'toolbox': return 'tools'
    case 'agents': return 'robot'
    case 'organizations': return 'usergroup'
    default: return props.link.knowledge_id ? 'file' : 'folder'
  }
})

function open() {
  if (!location.value) return
  assistant.setPageDraft(draft.value)
  void router.push(location.value)
}

function goBack() {
  if (returnPath.value) void router.push(returnPath.value)
}
</script>

<style lang="less" scoped>
.open-page-card {
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  width: 360px;
  max-width: 100%;
  margin-top: 8px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  cursor: default;
}

.open-page-card__main {
  appearance: none;
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-width: 0;
  margin: 0;
  padding: 8px 10px 8px 8px;
  border: 0;
  border-radius: inherit;
  background: transparent;
  font: inherit;
  text-align: left;
  color: inherit;
  cursor: pointer;
  transition: background var(--app-motion-fast) ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer-hover);

    .open-page-card__go {
      color: var(--td-text-color-secondary);
    }
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: -2px;
  }

  &:disabled {
    cursor: default;

    .open-page-card__go {
      visibility: hidden;
    }
  }
}

.open-page-card__icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  color: var(--td-brand-color);
}

.open-page-card__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.open-page-card__title,
.open-page-card__meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.open-page-card__title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 500;
  line-height: 1.35;
}

.open-page-card__meta {
  margin-top: 2px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  line-height: 1.3;
}

.open-page-card__opened {
  margin-right: 4px;
  color: var(--td-success-color);
  vertical-align: -2px;
}

.open-page-card__go {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 2px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  transition: color var(--app-motion-fast) ease;
}

.open-page-card__extra {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  margin: 0 10px;
  padding: 8px 0 10px;
  border-top: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-sm);
  line-height: 1.5;
}

.open-page-card__note {
  margin: 0;
  color: var(--td-text-color-secondary);
  overflow-wrap: anywhere;
}

.open-page-card__endpoint {
  display: flex;
  align-items: baseline;
  gap: 4px;
  max-width: 100%;
  color: var(--td-text-color-placeholder);

  code {
    min-width: 0;
    color: var(--td-text-color-secondary);
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
    margin-top: 3px;
  }
}

.open-page-card__back {
  appearance: none;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0;
  border: 0;
  background: transparent;
  font: inherit;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  transition: color var(--app-motion-fast) ease;

  &:hover {
    color: var(--td-text-color-secondary);
  }
}
</style>
