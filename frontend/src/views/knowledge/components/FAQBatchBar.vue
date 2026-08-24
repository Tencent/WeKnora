<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{
  count: number
  enabledCount: number
  disabledCount: number
  canEdit: boolean
  canManage: boolean
  tagLoading?: boolean
  statusAction?: 'enable' | 'disable' | null
  deleteLoading?: boolean
}>()

const emit = defineEmits<{
  (event: 'cancel'): void
  (event: 'batchTag'): void
  (event: 'enable'): void
  (event: 'disable'): void
  (event: 'delete'): void
}>()

const { t } = useI18n()

const actionLoading = computed(() => (
  props.tagLoading
  || props.statusAction != null
  || props.deleteLoading
))
</script>

<template>
  <transition name="faq-batch-bar-fade">
    <div v-if="count > 0 && (canEdit || canManage)" class="faq-batch-bar" role="region"
      :aria-label="t('knowledgeBase.selectedCount', { count })">
      <div class="faq-batch-bar__inner">
        <div class="faq-batch-bar__selection">
          <span class="faq-batch-bar__count">{{ t('knowledgeBase.selectedCount', { count }) }}</span>
          <t-button variant="text" theme="default" size="small" :disabled="actionLoading"
            @click="emit('cancel')">
            {{ t('knowledgeBase.clearSelection') }}
          </t-button>
        </div>

        <div class="faq-batch-bar__actions">
          <t-button v-if="canEdit" theme="default" variant="outline" size="small"
            :disabled="actionLoading" :loading="tagLoading" @click="emit('batchTag')">
            <template #icon><t-icon name="discount" size="14px" /></template>
            {{ t('knowledgeEditor.faq.batchUpdateTag') }}
          </t-button>

          <t-button v-if="canEdit && disabledCount > 0" theme="default" variant="outline" size="small"
            :disabled="actionLoading" :loading="statusAction === 'enable'" @click="emit('enable')">
            <template #icon><t-icon name="check-circle" size="14px" /></template>
            {{ t('knowledgeEditor.faq.batchEnable') }}
          </t-button>

          <t-button v-if="canEdit && enabledCount > 0" theme="default" variant="outline" size="small"
            :disabled="actionLoading" :loading="statusAction === 'disable'" @click="emit('disable')">
            <template #icon><t-icon name="minus-circle" size="14px" /></template>
            {{ t('knowledgeEditor.faq.batchDisable') }}
          </t-button>

          <t-popconfirm v-if="canManage" theme="warning"
            :content="t('knowledgeEditor.faq.confirmBatchDelete', { count })"
            :confirm-btn="{ content: t('knowledgeBase.confirmDelete'), theme: 'danger' }"
            :cancel-btn="{ content: t('common.cancel') }" placement="top" @confirm="emit('delete')">
            <t-button theme="danger" variant="outline" size="small" :disabled="actionLoading"
              :loading="deleteLoading" @click.stop>
              <template #icon><t-icon name="delete" size="14px" /></template>
              {{ t('knowledgeEditor.faq.batchDelete') }}
            </t-button>
          </t-popconfirm>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped lang="less">
.faq-batch-bar {
  width: 100%;
  max-width: 760px;
  padding: 0 4px;
  box-sizing: border-box;
}

.faq-batch-bar__inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 12px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.08);
}

.faq-batch-bar__selection {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex-shrink: 0;
}

.faq-batch-bar__count {
  color: var(--td-text-color-secondary);
  font-size: 13px;
  font-weight: 500;
  white-space: nowrap;
}

.faq-batch-bar__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.faq-batch-bar-fade-enter-active,
.faq-batch-bar-fade-leave-active {
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.faq-batch-bar-fade-enter-from,
.faq-batch-bar-fade-leave-to {
  opacity: 0;
  transform: translateY(6px);
}

@media (max-width: 760px) {
  .faq-batch-bar__inner {
    align-items: stretch;
    flex-direction: column;
  }

  .faq-batch-bar__actions {
    justify-content: flex-start;
  }
}
</style>
