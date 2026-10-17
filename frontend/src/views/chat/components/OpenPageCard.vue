<template>
  <button type="button" class="open-page-card" :disabled="!location" :title="actionLabel" @click.stop="activate" @keydown.stop>
    <span class="open-page-card__main">
      <span class="open-page-card__icon" aria-hidden="true"><t-icon :name="icon" size="18px" /></span>
      <span class="open-page-card__body">
        <span class="open-page-card__title">{{ link.detail || link.label }}</span>
        <span v-if="meta" class="open-page-card__meta">{{ meta }}</span>
      </span>
      <span class="open-page-card__go">
        <t-icon v-if="canReturn" name="rollback" size="14px" aria-hidden="true" />
        <span>{{ actionLabel }}</span>
        <t-icon v-if="!canReturn" name="chevron-right" size="14px" aria-hidden="true" />
      </span>
    </span>
    <span v-if="link.note || endpoint || draft" class="open-page-card__extra">
      <span v-if="link.note" class="open-page-card__note">{{ link.note }}</span>
      <span v-if="endpoint" class="open-page-card__endpoint">
        <span class="open-page-card__endpoint-label">{{ t('agentStream.openPage.endpoint') }}</span>
        <code>{{ endpoint }}</code>
      </span>
      <span v-if="draft" class="open-page-card__hint">
        <t-icon name="info-circle" />{{ t('agentStream.openPage.draftHint') }}
      </span>
    </span>
  </button>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
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
const openedFrom = ref('')
const returnPath = computed(() => openedFrom.value || pageLinkReturnPath(props.link))
const atTarget = computed(() => {
  if (!location.value) return false
  const target = router.resolve(location.value)
  const current = router.currentRoute.value
  return current.path === target.path
    && Object.entries(target.query).every(([key, value]) => current.query[key] === value)
})
const canReturn = computed(() => atTarget.value && !!returnPath.value
  && router.resolve(returnPath.value).fullPath !== router.currentRoute.value.fullPath)
const actionLabel = computed(() => canReturn.value
  ? t('agentStream.openPage.back')
  : draft.value ? t('agentStream.openPage.openForm') : t('agentStream.openPage.open'))

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
    case 'settings': return props.link.section === 'system' ? 'info-circle' : 'setting-1'
    case 'toolbox': return 'tools'
    case 'agents': return 'robot'
    case 'organizations': return 'usergroup'
    default: return props.link.knowledge_id ? 'file' : 'folder'
  }
})

function open() {
  if (!location.value) return
  if (!atTarget.value) openedFrom.value = router.currentRoute.value.fullPath
  assistant.setPageDraft(draft.value)
  void router.push(location.value)
}

function activate() {
  if (canReturn.value) {
    void router.push(returnPath.value)
  } else {
    open()
  }
}

</script>

<style lang="less" scoped>
.open-page-card {
  appearance: none;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  width: 360px;
  max-width: 100%;
  margin-top: 8px;
  padding: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.03);
  font: inherit;
  text-align: left;
  color: inherit;
  cursor: pointer;
  transition: background var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer-hover);
    border-color: var(--td-component-border);

    .open-page-card__go {
      color: var(--td-text-color-primary);
    }
  }

  &:focus-visible {
    outline: 2px solid var(--td-brand-color-focus);
    outline-offset: 2px;
  }

  &:disabled {
    cursor: default;

    .open-page-card__go {
      visibility: hidden;
    }
  }
}

.open-page-card__main {
  display: flex;
  align-items: center;
  box-sizing: border-box;
  gap: 10px;
  width: 100%;
  min-width: 0;
  padding: 12px;
}

.open-page-card__icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
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
  font-size: var(--app-text-base, 14px);
  font-weight: 600;
  line-height: 1.5;
}

.open-page-card__meta {
  margin-top: 2px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs, 12px);
  line-height: 1.5;
}

.open-page-card__go {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 2px;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  white-space: nowrap;
  transition: color var(--app-motion-fast) ease;
}

.open-page-card__extra {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  margin: 0 12px;
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

</style>
