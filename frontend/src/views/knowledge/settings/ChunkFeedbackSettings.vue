<template>
  <div class="chunk-feedback-settings">
    <div v-if="!embedded" class="section-header">
      <h2>{{ $t('knowledgeEditor.feedback.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.feedback.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- 低质量片段列表 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.feedback.lowQualityChunks') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.feedback.lowQualityChunksDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-button variant="outline" @click="openLowQualityDialog">
            {{ $t('knowledgeEditor.feedback.viewLowQuality') }}
          </t-button>
        </div>
      </div>

      <!-- 统计概览 -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.feedback.statsOverview') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.feedback.statsOverviewDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-button variant="outline" :loading="statsLoading" @click="loadStats">
            {{ $t('common.refresh') }}
          </t-button>
        </div>
      </div>

      <div v-if="statsLoading && !stats" class="inline-state">
        <t-loading size="small" />
        <span>{{ $t('common.loading') }}</span>
      </div>
      <t-alert v-else-if="statsError" theme="error" :message="statsError">
        <template #operation>
          <t-button size="small" variant="text" @click="loadStats">
            {{ $t('common.retry') }}
          </t-button>
        </template>
      </t-alert>

      <!-- 统计卡片 -->
      <div v-if="stats" class="stats-cards">
        <t-card :bordered="true" class="stat-card">
          <div class="stat-value">{{ stats.totalChunks }}</div>
          <div class="stat-label">{{ $t('knowledgeEditor.feedback.totalChunks') }}</div>
        </t-card>
        <t-card :bordered="true" class="stat-card">
          <div class="stat-value">{{ stats.highQualityCount }}</div>
          <div class="stat-label">{{ $t('knowledgeEditor.feedback.highQualityChunks') }}</div>
        </t-card>
        <t-card :bordered="true" class="stat-card">
          <div class="stat-value">{{ stats.lowQualityCount }}</div>
          <div class="stat-label">{{ $t('knowledgeEditor.feedback.lowQualityChunks') }}</div>
        </t-card>
        <t-card :bordered="true" class="stat-card">
          <div class="stat-value">{{ stats.totalFeedbacks }}</div>
          <div class="stat-label">{{ $t('knowledgeEditor.feedback.totalFeedbacks') }}</div>
        </t-card>
      </div>
    </div>

    <!-- 低质量片段弹窗 -->
    <t-dialog v-model:visible="showLowQualityDialog" :header="$t('knowledgeEditor.feedback.lowQualityList')" width="800px" :footer="null">
      <div class="low-quality-list">
        <div class="filter-bar">
          <t-select
            v-model="filterMaxRate"
            :options="rateOptions"
            :placeholder="$t('knowledgeEditor.feedback.maxRate')"
            style="width: 200px"
            @change="handleFilterChange"
          />
          <t-pagination
            v-model:current="currentPage"
            v-model:pageSize="pageSize"
            :total="totalCount"
            @change="loadLowQualityChunks"
          />
        </div>

        <t-table
          :data="lowQualityChunks"
          :columns="columns"
          row-key="chunk_id"
          :pagination="false"
          :loading="listLoading"
          stripe
        >
          <template #content="{ row }">
            <span :title="row.content">{{ truncateContent(row.content, 50) }}</span>
          </template>
          <template #positiveRate="{ row }">
            <t-tag :theme="getRateTheme(row.positive_rate)">
              {{ (row.positive_rate * 100).toFixed(1) }}%
            </t-tag>
          </template>
          <template #recallWeight="{ row }">
            <span :class="{ 'weight-boosted': row.recall_weight > 1, 'weight-penalized': row.recall_weight < 1 }">
              {{ row.recall_weight.toFixed(2) }}
            </span>
          </template>
          <template #qualityStatus="{ row }">
            <t-tag :theme="getStatusTheme(row.quality_status)">
              {{ getStatusLabel(row.quality_status) }}
            </t-tag>
          </template>
          <template #operations="{ row }">
            <t-button size="small" variant="text" @click="viewChunkStats(row.chunk_id)">
              {{ $t('knowledgeEditor.feedback.viewDetails') }}
            </t-button>
            <t-popconfirm
              theme="warning"
              :content="$t('knowledgeEditor.feedback.resetConfirm')"
              :confirm-btn="{ content: $t('knowledgeEditor.feedback.reset'), theme: 'danger' }"
              :cancel-btn="{ content: $t('common.cancel') }"
              @confirm="resetChunkFeedbackHandler(row.chunk_id)"
            >
              <t-button
                size="small"
                variant="text"
                theme="danger"
                :loading="resettingChunkId === row.chunk_id"
                :disabled="!!resettingChunkId"
              >
                {{ $t('knowledgeEditor.feedback.reset') }}
              </t-button>
            </t-popconfirm>
          </template>
        </t-table>
      </div>
    </t-dialog>

    <!-- 片段详情弹窗 -->
    <t-dialog v-model:visible="showChunkDetailDialog" :header="$t('knowledgeEditor.feedback.chunkDetail')" width="600px" :footer="null">
      <div class="chunk-detail">
        <div v-if="detailStatsLoading" class="inline-state">
          <t-loading size="small" />
          <span>{{ $t('common.loading') }}</span>
        </div>
        <t-alert v-else-if="detailStatsError" theme="error" :message="detailStatsError">
          <template #operation>
            <t-button size="small" variant="text" @click="retryChunkDetail">
              {{ $t('common.retry') }}
            </t-button>
          </template>
        </t-alert>
        <template v-else-if="chunkStats">
          <t-descriptions :column="2" bordered>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.chunkId')">
              {{ chunkStats.chunk_id }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.likeCount')">
              {{ chunkStats.like_count }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.dislikeCount')">
              {{ chunkStats.dislike_count }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.positiveRate')">
              {{ (chunkStats.positive_rate * 100).toFixed(1) }}%
            </t-descriptions-item>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.recallWeight')">
              {{ chunkStats.recall_weight.toFixed(2) }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.qualityStatus')">
              {{ getStatusLabel(chunkStats.quality_status) }}
            </t-descriptions-item>
            <t-descriptions-item :label="$t('knowledgeEditor.feedback.relatedSessions')">
              {{ chunkStats.related_session_count }}
            </t-descriptions-item>
          </t-descriptions>

          <div v-if="chunkStats.dislike_reason_stats?.length" class="dislike-reasons">
            <h4>{{ $t('knowledgeEditor.feedback.dislikeReasons') }}</h4>
            <div class="tag-list">
              <t-tag v-for="item in chunkStats.dislike_reason_stats" :key="item.reason" theme="danger">
                {{ formatReasonLabel(item.reason) }} × {{ item.count }}
              </t-tag>
            </div>
          </div>
        </template>

        <div class="weight-logs">
          <div class="logs-heading">
            <h4>{{ $t('knowledgeEditor.feedback.weightLogs') }}</h4>
            <span v-if="!detailLogsLoading && !detailLogsError" class="log-summary">
              {{ $t('knowledgeEditor.feedback.weightLogsSummary', { shown: weightLogs.length, total: weightLogTotal }) }}
            </span>
          </div>
          <div v-if="detailLogsLoading" class="inline-state">
            <t-loading size="small" />
            <span>{{ $t('common.loading') }}</span>
          </div>
          <t-alert v-else-if="detailLogsError" theme="error" :message="detailLogsError">
            <template #operation>
              <t-button size="small" variant="text" @click="retryChunkDetail">
                {{ $t('common.retry') }}
              </t-button>
            </template>
          </t-alert>
          <t-empty v-else-if="weightLogs.length === 0" :description="$t('common.noData')" />
          <t-table
            v-else
            :data="weightLogs"
            :columns="weightLogColumns"
            row-key="id"
            :pagination="false"
            size="small"
          >
            <template #action="{ row }">
              {{ getWeightLogActionLabel(row.action) }}
            </template>
            <template #trigger="{ row }">
              {{ getWeightLogTriggerLabel(row.trigger_type) }}
            </template>
            <template #operator="{ row }">
              {{ row.operator || '-' }}
            </template>
            <template #weightChange="{ row }">
              <span :class="{ 'weight-boosted': row.new_weight > row.old_weight, 'weight-penalized': row.new_weight < row.old_weight }">
                {{ row.old_weight.toFixed(2) }} → {{ row.new_weight.toFixed(2) }}
              </span>
            </template>
            <template #createdAt="{ row }">
              {{ formatDateTime(row.created_at) }}
            </template>
          </t-table>
        </div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import {
  listLowQualityChunks,
  getChunkStats,
  getFeedbackOverview,
  resetChunkFeedback,
  getChunkWeightLogs,
  type ChunkQualityStats,
  type ChunkStatsResponse,
  type WeightLogItem,
} from '@/api/feedback'
import { createLatestRequestGuard, getLastPage } from './chunkFeedbackViewState'

interface Props {
  embedded?: boolean
  kbId?: string
}

const props = withDefaults(defineProps<Props>(), {
  embedded: false,
  kbId: '',
})
const { locale, t } = useI18n()

// 状态
const showLowQualityDialog = ref(false)
const showChunkDetailDialog = ref(false)
const lowQualityChunks = ref<ChunkQualityStats[]>([])
const chunkStats = ref<ChunkStatsResponse | null>(null)
const weightLogs = ref<WeightLogItem[]>([])
const weightLogTotal = ref(0)
const currentPage = ref(1)
const pageSize = ref(10)
const totalCount = ref(0)
const filterMaxRate = ref(1.01)
const listLoading = ref(false)
const statsLoading = ref(false)
const statsError = ref('')
const detailStatsLoading = ref(false)
const detailStatsError = ref('')
const detailLogsLoading = ref(false)
const detailLogsError = ref('')
const selectedChunkId = ref('')
const resettingChunkId = ref('')
const listRequestGuard = createLatestRequestGuard()
const statsRequestGuard = createLatestRequestGuard()
const detailRequestGuard = createLatestRequestGuard()

const stats = ref<{
  totalChunks: number
  highQualityCount: number
  lowQualityCount: number
  totalFeedbacks: number
} | null>(null)

// 筛选选项
const rateOptions = computed(() => [
  { label: t('knowledgeEditor.feedback.allRatedChunks'), value: 1.01 },
  { label: '< 30%', value: 0.3 },
  { label: '< 50%', value: 0.5 },
  { label: '< 70%', value: 0.7 },
])

// 表格列
const columns = computed(() => [
  { colKey: 'content', title: t('knowledgeEditor.feedback.columns.content'), width: '30%' },
  { colKey: 'like_count', title: t('knowledgeEditor.feedback.columns.likeCount'), width: '10%' },
  { colKey: 'dislike_count', title: t('knowledgeEditor.feedback.columns.dislikeCount'), width: '10%' },
  { colKey: 'positiveRate', title: t('knowledgeEditor.feedback.columns.positiveRate'), width: '15%' },
  { colKey: 'recallWeight', title: t('knowledgeEditor.feedback.columns.recallWeight'), width: '10%' },
  { colKey: 'qualityStatus', title: t('knowledgeEditor.feedback.columns.qualityStatus'), width: '15%' },
  { colKey: 'operations', title: t('knowledgeEditor.feedback.columns.operations'), width: '10%' },
])

const weightLogColumns = computed(() => [
  { colKey: 'action', title: t('knowledgeEditor.feedback.columns.action'), width: '18%' },
  { colKey: 'trigger', title: t('knowledgeEditor.feedback.columns.trigger'), width: '18%' },
  { colKey: 'operator', title: t('knowledgeEditor.feedback.columns.operator'), width: '18%' },
  { colKey: 'weightChange', title: t('knowledgeEditor.feedback.columns.weightChange'), width: '23%' },
  { colKey: 'createdAt', title: t('knowledgeEditor.feedback.columns.createdAt'), width: '23%' },
])

// 加载统计数据
const loadStats = async () => {
  const requestId = statsRequestGuard.begin()
  statsLoading.value = true
  statsError.value = ''
  try {
    const res = await getFeedbackOverview({ knowledge_base_id: props.kbId || undefined })
    if (!statsRequestGuard.isLatest(requestId)) return
    if (!res.data) {
      stats.value = null
      statsError.value = t('knowledgeEditor.feedback.messages.overviewLoadFailed')
      return
    }
    stats.value = {
      totalChunks: res.data.total_chunks,
      highQualityCount: res.data.high_quality_count,
      lowQualityCount: res.data.low_quality_count,
      totalFeedbacks: res.data.total_feedbacks,
    }
  } catch (error) {
    if (!statsRequestGuard.isLatest(requestId)) return
    console.error('加载统计数据失败:', error)
    stats.value = null
    statsError.value = t('knowledgeEditor.feedback.messages.overviewLoadFailed')
  } finally {
    if (statsRequestGuard.isLatest(requestId)) {
      statsLoading.value = false
    }
  }
}

// 加载低质量片段
const loadLowQualityChunks = async (): Promise<boolean> => {
  const requestId = listRequestGuard.begin()
  listLoading.value = true
  try {
    const parsedMaxRate = Number(filterMaxRate.value)
    const maxRate = Number.isFinite(parsedMaxRate) && parsedMaxRate > 0 ? parsedMaxRate : 1.01
    const res = await listLowQualityChunks({
      max_rate: maxRate,
      limit: pageSize.value,
      offset: (currentPage.value - 1) * pageSize.value,
      knowledge_base_id: props.kbId || undefined,
    })
    if (!listRequestGuard.isLatest(requestId)) return false
    lowQualityChunks.value = res.data ?? []
    totalCount.value = res.total ?? res.data?.length ?? 0
    return true
  } catch (error) {
    if (!listRequestGuard.isLatest(requestId)) return false
    console.error('load feedback chunks failed:', error)
    lowQualityChunks.value = []
    totalCount.value = 0
    MessagePlugin.error(t('knowledgeEditor.feedback.messages.loadFailed'))
    return false
  } finally {
    if (listRequestGuard.isLatest(requestId)) {
      listLoading.value = false
    }
  }
}

const handleFilterChange = () => {
  currentPage.value = 1
  loadLowQualityChunks()
}

const openLowQualityDialog = () => {
  showLowQualityDialog.value = true
  currentPage.value = 1
  loadLowQualityChunks()
}

// 查看片段详情
const viewChunkStats = async (chunkId: string) => {
  const requestId = detailRequestGuard.begin()
  selectedChunkId.value = chunkId
  showChunkDetailDialog.value = true
  chunkStats.value = null
  weightLogs.value = []
  weightLogTotal.value = 0
  detailStatsError.value = ''
  detailLogsError.value = ''
  detailStatsLoading.value = true
  detailLogsLoading.value = true

  const statsRequest = (async () => {
    try {
      const statsRes = await getChunkStats(chunkId)
      if (!detailRequestGuard.isLatest(requestId)) return
      chunkStats.value = statsRes.data ?? null
      if (!statsRes.data) {
        detailStatsError.value = t('knowledgeEditor.feedback.messages.chunkStatsLoadFailed')
      }
    } catch (error) {
      if (!detailRequestGuard.isLatest(requestId)) return
      console.error('加载片段统计失败:', error)
      chunkStats.value = null
      detailStatsError.value = t('knowledgeEditor.feedback.messages.chunkStatsLoadFailed')
    } finally {
      if (detailRequestGuard.isLatest(requestId)) {
        detailStatsLoading.value = false
      }
    }
  })()

  const logsRequest = (async () => {
    try {
      const logsRes = await getChunkWeightLogs(chunkId, 20)
      if (!detailRequestGuard.isLatest(requestId)) return
      if (!logsRes.data) {
        detailLogsError.value = t('knowledgeEditor.feedback.messages.weightLogsLoadFailed')
        return
      }
      weightLogs.value = logsRes.data.logs ?? []
      weightLogTotal.value = logsRes.data.total ?? weightLogs.value.length
    } catch (error) {
      if (!detailRequestGuard.isLatest(requestId)) return
      console.error('加载权重日志失败:', error)
      weightLogs.value = []
      weightLogTotal.value = 0
      detailLogsError.value = t('knowledgeEditor.feedback.messages.weightLogsLoadFailed')
    } finally {
      if (detailRequestGuard.isLatest(requestId)) {
        detailLogsLoading.value = false
      }
    }
  })()

  await Promise.all([statsRequest, logsRequest])
}

const retryChunkDetail = () => {
  if (selectedChunkId.value) {
    void viewChunkStats(selectedChunkId.value)
  }
}

const reloadLowQualityChunksAfterReset = async () => {
  if (!(await loadLowQualityChunks())) return

  const lastPage = getLastPage(totalCount.value, pageSize.value)
  if (currentPage.value > lastPage) {
    currentPage.value = lastPage
    await loadLowQualityChunks()
  }
}

// 重置片段反馈
const resetChunkFeedbackHandler = async (chunkId: string) => {
  try {
    resettingChunkId.value = chunkId
    await resetChunkFeedback(chunkId)
    MessagePlugin.success(t('knowledgeEditor.feedback.messages.resetSuccess'))
    await Promise.all([reloadLowQualityChunksAfterReset(), loadStats()])
  } catch (error) {
    console.error('reset chunk feedback failed:', error)
    MessagePlugin.error(t('knowledgeEditor.feedback.messages.resetFailed'))
  } finally {
    resettingChunkId.value = ''
  }
}

// 辅助函数
const truncateContent = (content: string, maxLen: number) => {
  return content?.length > maxLen ? content.slice(0, maxLen) + '...' : content
}

const getRateTheme = (rate: number) => {
  if (rate >= 0.8) return 'success'
  if (rate >= 0.5) return 'warning'
  return 'danger'
}

const getStatusTheme = (status: string) => {
  const map: Record<string, string> = {
    normal: 'default',
    pending_optimization: 'warning',
    needs_optimization: 'danger',
    optimizing: 'primary',
    optimized: 'success',
  }
  return map[status] || 'default'
}

const getStatusLabel = (status: string) => {
  return t(`knowledgeEditor.feedback.status.${status}`)
}

const getWeightLogActionLabel = (action: string) => {
  return t(`knowledgeEditor.feedback.weightLogActions.${action}`)
}

const getWeightLogTriggerLabel = (trigger: string) => {
  return t(`knowledgeEditor.feedback.weightLogTriggers.${trigger}`)
}

const formatReasonLabel = (reason: string) => {
  const keyMap: Record<string, string> = {
    inaccurate: 'inaccurate',
    incomplete: 'incomplete',
    unclear: 'unclear',
    irrelevant: 'unrelated',
    other: 'other',
  }
  const key = keyMap[reason]
  return key ? t(`chunkFeedback.dislikeReasons.${key}`) : reason
}

const formatDateTime = (value?: string) => {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(locale.value)
}

onMounted(() => {
  loadStats()
})

onBeforeUnmount(() => {
  listRequestGuard.invalidate()
  statsRequestGuard.invalidate()
  detailRequestGuard.invalidate()
})
</script>

<style scoped lang="less">
.chunk-feedback-settings {
  .section-header {
    margin-bottom: 24px;

    h2 {
      margin: 0 0 8px 0;
      font-size: 18px;
      font-weight: 600;
    }

    .section-description {
      color: var(--td-text-color-secondary);
      margin: 0;
    }
  }

  .stats-cards {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 16px;
    margin: 16px 0;

    .stat-card {
      text-align: center;

      .stat-value {
        font-size: 24px;
        font-weight: 600;
        color: var(--td-brand-color);
      }

      .stat-label {
        font-size: 12px;
        color: var(--td-text-color-secondary);
        margin-top: 4px;
      }
    }
  }

  .filter-bar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 16px;
  }

  .inline-state {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 64px;
    color: var(--td-text-color-secondary);
  }

  .weight-boosted {
    color: var(--td-brand-color);
    font-weight: 600;
  }

  .weight-penalized {
    color: var(--td-error-color);
    font-weight: 600;
  }

  .chunk-detail {
    .dislike-reasons,
    .weight-logs {
      margin-top: 16px;

      h4 {
        margin: 0 0 8px 0;
        font-size: 14px;
        font-weight: 500;
      }
    }

    .logs-heading {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;

      .log-summary {
        color: var(--td-text-color-secondary);
        font-size: 12px;
        white-space: nowrap;
      }
    }

    .tag-list {
      display: flex;
      flex-wrap: wrap;
      gap: 8px;
    }
  }
}
</style>
