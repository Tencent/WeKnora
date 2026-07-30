<template>
  <div class="feedback-governance">
    <header>
      <h3>{{ t('feedback.governance.title') }}</h3>
      <p>{{ t('feedback.governance.description') }}</p>
    </header>

    <div class="toolbar">
      <t-input
        v-model="filters.keyword"
        clearable
        :placeholder="t('feedback.governance.searchPlaceholder')"
        @enter="reload"
      />
      <t-select v-model="filters.feedback_status" @change="reload">
        <t-option
          v-for="status in statuses"
          :key="status"
          :value="status"
          :label="t(`feedback.governance.status.${status}`)"
        />
      </t-select>
      <t-select v-model="filters.optimization" @change="reload">
        <t-option value="all" :label="t('feedback.governance.optimization.all')" />
        <t-option value="yes" :label="t('feedback.governance.optimization.yes')" />
        <t-option value="no" :label="t('feedback.governance.optimization.no')" />
      </t-select>
      <t-button variant="outline" :loading="loading" @click="reload">
        {{ t('common.refresh') }}
      </t-button>
    </div>

    <t-table
      row-key="chunk_id"
      :data="items"
      :columns="columns"
      :loading="loading"
      table-content-width="900px"
      hover
    >
      <template #document="{ row }">
        <strong>{{ row.knowledge_title || t('feedback.governance.untitled') }}</strong>
        <small>#{{ row.chunk_index }}</small>
      </template>
      <template #content="{ row }">
        <span class="preview" :title="row.content_preview">{{ row.content_preview }}</span>
      </template>
      <template #ratings="{ row }">
        <span>👍 {{ row.like_count }}</span>
        <span>👎 {{ row.dislike_count }}</span>
      </template>
      <template #weights="{ row }">
        <span>{{ t('feedback.effectiveWeight') }} {{ formatWeight(row.effective_recall_weight) }}</span>
        <small>{{ t('feedback.storedWeight') }} {{ formatWeight(row.stored_recall_weight) }}</small>
      </template>
      <template #status="{ row }">
        <t-tag :theme="row.needs_optimization ? 'danger' : 'success'" variant="light">
          {{ t(row.needs_optimization
            ? 'feedback.governance.needsOptimization'
            : 'feedback.governance.healthy') }}
        </t-tag>
      </template>
      <template #actions="{ row }">
        <t-button variant="text" size="small" @click="openRow(row.chunk_id)">
          {{ t('feedback.governance.viewDetail') }}
        </t-button>
      </template>
    </t-table>

    <div v-if="error" class="error">
      {{ t('feedback.governance.loadFailed') }}
    </div>
    <t-pagination
      v-if="total"
      v-model="page"
      v-model:page-size="pageSize"
      :total="total"
      :page-size-options="[10, 20, 50, 100]"
      @change="load(false)"
    />

    <t-drawer
      :visible="Boolean(selected)"
      :header="t('feedback.governance.detailTitle')"
      size="min(680px, 100vw)"
      :footer="false"
      @close="close"
    >
      <t-loading v-if="detailLoading" />
      <div v-else-if="selected" class="detail">
        <div class="detail-heading">
          <div>
            <strong>{{ selected.knowledge_title || t('feedback.governance.untitled') }}</strong>
            <span>#{{ selected.chunk_index }}</span>
          </div>
          <t-popconfirm
            :content="t('feedback.governance.resetConfirm')"
            @confirm="confirmReset"
          >
            <t-button theme="danger" variant="outline" :loading="resetting">
              {{ t('feedback.governance.reset') }}
            </t-button>
          </t-popconfirm>
        </div>

        <div class="metrics">
          <div><span>{{ t('feedback.governance.likes') }}</span><strong>{{ selected.like_count }}</strong></div>
          <div><span>{{ t('feedback.governance.dislikes') }}</span><strong>{{ selected.dislike_count }}</strong></div>
          <div><span>{{ t('feedback.effectiveWeight') }}</span><strong>{{ formatWeight(selected.effective_recall_weight) }}</strong></div>
          <div><span>{{ t('feedback.storedWeight') }}</span><strong>{{ formatWeight(selected.stored_recall_weight) }}</strong></div>
        </div>

        <section>
          <h4>{{ t('feedback.governance.chunkContent') }}</h4>
          <pre>{{ selected.content }}</pre>
        </section>
        <section>
          <h4>{{ t('feedback.governance.dislikeReasons') }}</h4>
          <div v-if="Object.keys(selected.reason_counts || {}).length">
            <div v-for="(count, reason) in selected.reason_counts" :key="reason" class="reason">
              <span>{{ t(`feedback.reasons.${reason}`) }}</span><strong>{{ count }}</strong>
            </div>
          </div>
          <t-empty v-else :description="t('feedback.governance.noReasons')" />
        </section>
        <section>
          <h4>{{ t('feedback.governance.history') }}</h4>
          <t-table row-key="id" :data="history" :columns="historyColumns" size="small">
            <template #change="{ row }">
              {{ formatWeight(row.old_weight) }} → {{ formatWeight(row.new_weight) }}
            </template>
            <template #created_at="{ row }">
              {{ new Date(row.created_at).toLocaleString() }}
            </template>
          </t-table>
        </section>
      </div>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, toRef } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { useChunkFeedbackGovernance } from '@/composables/useChunkFeedbackGovernance'

const props = defineProps<{ kbId: string }>()
const { t } = useI18n()
const statuses = ['all', 'rated', 'high', 'normal', 'low', 'unrated'] as const
const {
  filters,
  items,
  total,
  page,
  pageSize,
  loading,
  error,
  selected,
  history,
  detailLoading,
  resetting,
  load,
  open,
  reset,
  close,
} = useChunkFeedbackGovernance({ kbId: toRef(props, 'kbId') })

const columns = computed(() => [
  { colKey: 'document', title: t('feedback.governance.columns.document'), width: 170 },
  { colKey: 'content', title: t('feedback.governance.columns.content'), minWidth: 250 },
  { colKey: 'ratings', title: t('feedback.governance.columns.ratings'), width: 120 },
  { colKey: 'weights', title: t('feedback.governance.columns.weights'), width: 170 },
  { colKey: 'status', title: t('feedback.governance.columns.status'), width: 130 },
  { colKey: 'actions', title: '', width: 80 },
])
const historyColumns = computed(() => [
  { colKey: 'created_at', title: t('feedback.governance.columns.time'), width: 180 },
  { colKey: 'trigger_source', title: t('feedback.governance.columns.trigger'), width: 120 },
  { colKey: 'change', title: t('feedback.governance.columns.change'), width: 140 },
])

const formatWeight = (value: number) => Number(value || 1).toFixed(2)
const reload = () => void load(true)
const openRow = async (chunkId: string) => {
  if (!await open(chunkId)) MessagePlugin.error(t('feedback.governance.loadFailed'))
}
const confirmReset = async () => {
  if (await reset()) MessagePlugin.success(t('feedback.resetSuccess'))
  else MessagePlugin.error(t('feedback.resetFailed'))
}
</script>

<style scoped lang="less">
.feedback-governance {
  display: flex;
  flex-direction: column;
  gap: 18px;
}
header h3, section h4 {
  margin: 0;
}
header p {
  margin: 6px 0 0;
  color: var(--td-text-color-secondary);
}
.toolbar {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) 160px 160px auto;
  gap: 10px;
}
.preview {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
small {
  display: block;
  color: var(--td-text-color-secondary);
}
.error {
  color: var(--td-error-color);
}
.detail, .detail section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.detail-heading, .reason {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.metrics {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}
.metrics div {
  padding: 12px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}
.metrics span {
  display: block;
  color: var(--td-text-color-secondary);
}
pre {
  max-height: 280px;
  overflow: auto;
  white-space: pre-wrap;
  padding: 12px;
  border-radius: 8px;
  background: var(--td-bg-color-secondarycontainer);
}
@media (max-width: 900px) {
  .toolbar {
    grid-template-columns: 1fr;
  }
}
</style>
