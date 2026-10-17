<template>
  <div class="page-edit-card" @click.stop @keydown.stop>
    <div class="page-edit-card__header">
      <span class="page-edit-card__icon" aria-hidden="true"><t-icon name="edit-1" size="18px" /></span>
      <div class="page-edit-card__heading">
        <div v-if="fill.location" class="page-edit-card__eyebrow">{{ fill.location }}</div>
        <div class="page-edit-card__title">
          {{ undone ? t('agentStream.pageEdit.undone') : t('agentStream.pageEdit.title', { count: applied.length }) }}
        </div>
      </div>
      <t-button v-if="undoable" class="page-edit-card__action" size="small" variant="outline" :loading="undoing"
        @click="undo">
        <template #icon><t-icon name="rollback" /></template>
        {{ t('agentStream.pageEdit.undo') }}
      </t-button>
    </div>
    <ul v-if="applied.length && !undone" class="page-edit-card__list">
      <li v-for="item in applied" :key="item.ref">
        <span class="page-edit-card__label">{{ item.label || item.ref }}</span>
        <span class="page-edit-card__summary">{{ item.summary }}</span>
      </li>
    </ul>
    <div v-if="!undone" class="page-edit-card__footer">
      <t-icon name="info-circle" />{{ t('agentStream.pageEdit.unsaved') }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { canUndoFill, undoFill } from '@/utils/pageFill'

export interface PageFillResult {
  location?: string
  applied?: Array<{ ref: string; label?: string; summary?: string }>
}

const props = defineProps<{ fill: PageFillResult; toolCallId: string }>()

const { t } = useI18n()
const undoing = ref(false)
const undone = ref(false)

const applied = computed(() => props.fill.applied || [])
// Only this tab, before a reload, still holds what the fields held before.
const undoable = computed(() => !undone.value && canUndoFill(props.toolCallId))

async function undo() {
  undoing.value = true
  try {
    const { restored, skipped } = await undoFill(props.toolCallId)
    undone.value = restored > 0
    if (skipped > 0) MessagePlugin.warning(t('agentStream.pageEdit.undoSkipped', { count: skipped }))
  } finally {
    undoing.value = false
  }
}
</script>

<style lang="less" scoped>
.page-edit-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  box-sizing: border-box;
  width: 360px;
  max-width: 100%;
  margin-top: 8px;
  padding: 8px 10px 10px 8px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
  cursor: default;
}

.page-edit-card__header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.page-edit-card__icon {
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

.page-edit-card__heading {
  flex: 1;
  min-width: 0;
}

.page-edit-card__eyebrow,
.page-edit-card__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.page-edit-card__eyebrow {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs, 12px);
  line-height: 18px;
}

.page-edit-card__title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 500;
  line-height: 1.35;
}

.page-edit-card__action {
  flex-shrink: 0;
  border-radius: var(--td-radius-medium);
}

.page-edit-card__list {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 0 0 0 2px;
  padding: 0;
  list-style: none;
  font-size: var(--app-text-sm);
  line-height: 1.6;

  li {
    display: flex;
    gap: 8px;
    min-width: 0;
  }
}

.page-edit-card__label {
  flex-shrink: 0;
  max-width: 40%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-secondary);
}

.page-edit-card__summary {
  min-width: 0;
  color: var(--td-text-color-primary);
  overflow-wrap: anywhere;
}

.page-edit-card__footer {
  display: flex;
  align-items: flex-start;
  gap: 4px;
  padding-top: 8px;
  margin-left: 2px;
  border-top: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs, 12px);
  line-height: 18px;

  .t-icon {
    flex-shrink: 0;
    margin-top: 2px;
  }
}
</style>
