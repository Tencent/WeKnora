<template>
  <div class="section-content chunk-feedback-settings">
    <div class="section-header">
      <div class="chunk-feedback-title-row">
        <div>
          <h3 class="section-title">{{ t('chunkFeedback.title') }}</h3>
          <p class="section-desc">{{ t('chunkFeedback.description') }}</p>
        </div>
        <t-button variant="text" shape="square" :loading="loading" @click="load">
          <t-icon name="refresh" />
        </t-button>
      </div>
    </div>

    <div class="section-body">
      <div class="chunk-feedback-filters">
        <t-input
          v-model.trim="keyword"
          clearable
          :placeholder="t('chunkFeedback.searchPlaceholder')"
          @enter="applyFilters"
          @clear="applyFilters"
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select
          v-model="qualityFilter"
          :options="qualityOptions"
          @change="applyFilters"
        />
        <t-select
          v-model="statusFilter"
          :options="statusOptions"
          @change="applyFilters"
        />
      </div>

      <t-alert v-if="error" theme="error" :message="error" class="chunk-feedback-error" />

      <div class="chunk-feedback-table">
        <t-table
          row-key="chunk_id"
          :data="rows"
          :columns="columns"
          :loading="loading"
          size="medium"
          hover
          table-layout="fixed"
        >
          <template #empty>
            <t-empty :description="t('chunkFeedback.empty')" />
          </template>
          <template #chunk="{ row }">
            <div class="chunk-cell">
              <strong :title="row.knowledge_title">{{ row.knowledge_title || '—' }}</strong>
              <span :title="row.content">{{ contentPreview(row.content) }}</span>
              <code>#{{ row.chunk_index + 1 }}</code>
            </div>
          </template>
          <template #positive_rate="{ row }">
            <span v-if="feedbackTotal(row) > 0" :class="rateClass(row.positive_rate)">
              {{ formatRate(row.positive_rate) }}
            </span>
            <span v-else>—</span>
          </template>
          <template #votes="{ row }">
            <div class="vote-counts">
              <span class="vote-like"><t-icon name="thumb-up" /> {{ row.positive_count }}</span>
              <span class="vote-dislike"><t-icon name="thumb-down" /> {{ row.negative_count }}</span>
            </div>
          </template>
          <template #recall_weight="{ row }">
            <t-tag :theme="weightTheme(row.recall_weight)" variant="light">
              ×{{ Number(row.recall_weight || 1).toFixed(2) }}
            </t-tag>
          </template>
          <template #feedback_status="{ row }">
            <t-tag
              :theme="row.feedback_status === 'pending_optimization' ? 'warning' : 'success'"
              variant="light-outline"
            >
              {{ statusLabel(row.feedback_status) }}
            </t-tag>
          </template>
          <template #reason_counts="{ row }">
            <div v-if="row.reason_counts?.length" class="reason-counts">
              <t-tag
                v-for="reason in row.reason_counts.slice(0, 2)"
                :key="reason.reason_code"
                size="small"
                variant="light"
              >
                {{ reasonLabel(reason.reason_code) }} {{ reason.count }}
              </t-tag>
            </div>
            <span v-else>—</span>
          </template>
          <template #actions="{ row }">
            <div class="row-actions">
              <t-button size="small" variant="text" @click="openLogs(row)">
                {{ t('chunkFeedback.viewLogs') }}
              </t-button>
              <t-button size="small" variant="text" theme="danger" @click="confirmReset(row)">
                {{ t('chunkFeedback.reset') }}
              </t-button>
            </div>
          </template>
        </t-table>
      </div>

      <t-pagination
        v-if="total > pageSize"
        v-model="page"
        :total="total"
        :page-size="pageSize"
        :show-page-size="false"
        class="chunk-feedback-pagination"
        @current-change="load"
      />
    </div>

    <SettingDrawer
      v-model:visible="logsVisible"
      :title="t('chunkFeedback.logsTitle')"
      :description="selectedRow ? contentPreview(selectedRow.content, 120) : ''"
      icon="history"
      width="720px"
      :min-width="520"
      :max-width="960"
      storage-key="setting-drawer:width:chunk-feedback-logs"
      hide-footer
    >
      <t-loading v-if="logsLoading" />
      <t-empty v-else-if="!logs.length" :description="t('chunkFeedback.logsEmpty')" />
      <div v-else class="weight-log-list">
        <article v-for="log in logs" :key="log.id" class="weight-log-item">
          <div class="weight-log-head">
            <t-tag size="small" :theme="log.trigger_source === 'admin_reset' ? 'warning' : 'primary'">
              {{ triggerLabel(log.trigger_source, log.trigger_action) }}
            </t-tag>
            <time>{{ formatDate(log.created_at) }}</time>
          </div>
          <div class="weight-log-change">
            <span>{{ t('chunkFeedback.weight') }}: ×{{ log.old_recall_weight.toFixed(2) }}</span>
            <t-icon name="arrow-right" />
            <strong>×{{ log.new_recall_weight.toFixed(2) }}</strong>
          </div>
          <div class="weight-log-meta">
            {{ t('chunkFeedback.rate') }}:
            {{ formatRate(log.old_positive_rate) }} → {{ formatRate(log.new_positive_rate) }}
          </div>
        </article>
      </div>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { DialogPlugin, MessagePlugin, type PrimaryTableCol } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  listChunkFeedbackStats,
  listChunkFeedbackWeightLogs,
  resetChunkFeedback,
  type ChunkFeedbackStats,
  type ChunkFeedbackWeightLog,
} from '@/api/knowledge-base'

const props = defineProps<{ kbId: string; active?: boolean }>()
const { t, locale } = useI18n()

const rows = ref<ChunkFeedbackStats[]>([])
const logs = ref<ChunkFeedbackWeightLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const logsLoading = ref(false)
const error = ref('')
const keyword = ref('')
const qualityFilter = ref('all')
const statusFilter = ref('all')
const logsVisible = ref(false)
const selectedRow = ref<ChunkFeedbackStats | null>(null)

const columns = computed<PrimaryTableCol[]>(() => [
  { colKey: 'chunk', title: t('chunkFeedback.columns.chunk'), width: 280 },
  { colKey: 'positive_rate', title: t('chunkFeedback.columns.rate'), width: 88, align: 'center' },
  { colKey: 'votes', title: t('chunkFeedback.columns.votes'), width: 120, align: 'center' },
  { colKey: 'related_session_count', title: t('chunkFeedback.columns.sessions'), width: 88, align: 'center' },
  { colKey: 'recall_weight', title: t('chunkFeedback.columns.weight'), width: 90, align: 'center' },
  { colKey: 'feedback_status', title: t('chunkFeedback.columns.status'), width: 110, align: 'center' },
  { colKey: 'reason_counts', title: t('chunkFeedback.columns.reasons'), width: 180 },
  { colKey: 'actions', title: t('knowledgeBase.actions'), width: 130, fixed: 'right' },
])

const qualityOptions = computed(() => [
  { label: t('chunkFeedback.filters.allQuality'), value: 'all' },
  { label: t('chunkFeedback.filters.below50'), value: 'below50' },
  { label: t('chunkFeedback.filters.below80'), value: 'below80' },
])
const statusOptions = computed(() => [
  { label: t('chunkFeedback.filters.allStatus'), value: 'all' },
  { label: t('chunkFeedback.status.normal'), value: 'normal' },
  { label: t('chunkFeedback.status.pending_optimization'), value: 'pending_optimization' },
])

const load = async () => {
  if (!props.kbId || !props.active) return
  loading.value = true
  error.value = ''
  try {
    const response: any = await listChunkFeedbackStats(props.kbId, {
      page: page.value,
      page_size: pageSize,
      keyword: keyword.value || undefined,
      max_positive_rate:
        qualityFilter.value === 'below50' ? 0.499999 :
        qualityFilter.value === 'below80' ? 0.799999 :
        undefined,
      status: statusFilter.value === 'all' ? undefined : statusFilter.value,
      sort_by: 'positive_rate',
      sort_order: 'asc',
    })
    rows.value = response?.data || []
    total.value = Number(response?.total || 0)
  } catch (loadError: any) {
    error.value = loadError?.message || t('chunkFeedback.loadFailed')
  } finally {
    loading.value = false
  }
}

const applyFilters = () => {
  page.value = 1
  void load()
}

const openLogs = async (row: ChunkFeedbackStats) => {
  selectedRow.value = row
  logsVisible.value = true
  logsLoading.value = true
  logs.value = []
  try {
    const response: any = await listChunkFeedbackWeightLogs(props.kbId, row.chunk_id)
    logs.value = response?.data || []
  } catch (loadError: any) {
    MessagePlugin.error(loadError?.message || t('chunkFeedback.logsLoadFailed'))
  } finally {
    logsLoading.value = false
  }
}

const confirmReset = (row: ChunkFeedbackStats) => {
  const dialog = DialogPlugin.confirm({
    header: t('chunkFeedback.resetTitle'),
    body: t('chunkFeedback.resetConfirm'),
    confirmBtn: { content: t('chunkFeedback.reset'), theme: 'danger' },
    onConfirm: async () => {
      dialog.destroy()
      try {
        await resetChunkFeedback(props.kbId, row.chunk_id)
        MessagePlugin.success(t('chunkFeedback.resetSuccess'))
        await load()
      } catch (resetError: any) {
        MessagePlugin.error(resetError?.message || t('chunkFeedback.resetFailed'))
      }
    },
    onCancel: () => dialog.destroy(),
  })
}

const feedbackTotal = (row: ChunkFeedbackStats) => row.positive_count + row.negative_count
const formatRate = (rate: number) => `${(Number(rate || 0) * 100).toFixed(1)}%`
const contentPreview = (content: string, limit = 72) => {
  const normalized = String(content || '').replace(/\s+/g, ' ').trim()
  return normalized.length > limit ? `${normalized.slice(0, limit)}…` : normalized || '—'
}
const rateClass = (rate: number) => ({
  'rate-good': rate >= 0.8,
  'rate-medium': rate >= 0.5 && rate < 0.8,
  'rate-low': rate < 0.5,
})
const weightTheme = (weight: number) => weight > 1 ? 'success' : weight < 1 ? 'warning' : 'default'
const statusLabel = (status: string) => t(`chunkFeedback.status.${status || 'normal'}`)
const reasonLabel = (reason: string) => t(`messageFeedback.reasons.${reason}`)
const triggerLabel = (source: string, action: string) =>
  source === 'admin_reset'
    ? t('chunkFeedback.triggers.adminReset')
    : t(`chunkFeedback.triggers.${action}`)
const formatDate = (value: string) => new Intl.DateTimeFormat(locale.value, {
  dateStyle: 'medium',
  timeStyle: 'short',
}).format(new Date(value))

watch(
  () => [props.active, props.kbId],
  ([active]) => {
    if (active) void load()
  },
  { immediate: true },
)
</script>

<style scoped lang="less">
.chunk-feedback-title-row,
.chunk-feedback-filters,
.vote-counts,
.row-actions,
.weight-log-head,
.weight-log-change {
  display: flex;
  align-items: center;
}

.chunk-feedback-title-row {
  justify-content: space-between;
}

.chunk-feedback-filters {
  gap: 12px;
  margin-bottom: 16px;

  :deep(.t-input__wrap) {
    flex: 1;
  }

  :deep(.t-select__wrap) {
    width: 180px;
  }
}

.chunk-feedback-error {
  margin-bottom: 12px;
}

.chunk-feedback-table {
  border: 1px solid var(--td-component-border);
  border-radius: 8px;
  overflow: hidden;
}

.chunk-cell {
  display: grid;
  gap: 4px;

  strong,
  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  span,
  code {
    color: var(--td-text-color-secondary);
    font-size: 12px;
  }
}

.vote-counts {
  justify-content: center;
  gap: 12px;
}

.vote-like {
  color: var(--td-success-color);
}

.vote-dislike,
.rate-low {
  color: var(--td-error-color);
}

.rate-good {
  color: var(--td-success-color);
}

.rate-medium {
  color: var(--td-warning-color);
}

.reason-counts {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.row-actions {
  gap: 2px;
}

.chunk-feedback-pagination {
  justify-content: flex-end;
  margin-top: 16px;
}

.weight-log-list {
  display: grid;
  gap: 12px;
}

.weight-log-item {
  padding: 14px;
  border: 1px solid var(--td-component-border);
  border-radius: 8px;
}

.weight-log-head {
  justify-content: space-between;
  margin-bottom: 12px;

  time {
    color: var(--td-text-color-secondary);
    font-size: 12px;
  }
}

.weight-log-change {
  gap: 8px;
  font-size: 15px;
}

.weight-log-meta {
  margin-top: 8px;
  color: var(--td-text-color-secondary);
  font-size: 13px;
}
</style>
