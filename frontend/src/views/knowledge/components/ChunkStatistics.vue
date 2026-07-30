<template>
  <div class="chunk-stats">
    <!-- 统计概览 -->
    <div class="stats-overview">
      <t-card :bordered="true" class="summary-card">
        <div class="summary-grid">
          <div class="summary-item">
            <div class="summary-value">{{ summary.total_chunks || 0 }}</div>
            <div class="summary-label">{{ $t('knowledgeBase.statistics.totalChunks') }}</div>
          </div>
          <div class="summary-item">
            <div class="summary-value">{{ summary.total_feedbacks || 0 }}</div>
            <div class="summary-label">{{ $t('knowledgeBase.statistics.totalFeedbacks') }}</div>
          </div>
          <div class="summary-item">
            <div class="summary-value positive">{{ summary.total_likes || 0 }}</div>
            <div class="summary-label">{{ $t('knowledgeBase.statistics.totalLikes') }}</div>
          </div>
          <div class="summary-item">
            <div class="summary-value negative">{{ summary.total_dislikes || 0 }}</div>
            <div class="summary-label">{{ $t('knowledgeBase.statistics.totalDislikes') }}</div>
          </div>
          <div class="summary-item">
            <div class="summary-value">{{ formatPercent(summary.average_like_rate) }}</div>
            <div class="summary-label">{{ $t('knowledgeBase.statistics.averageLikeRate') }}</div>
          </div>
          <div class="summary-item warning">
            <div class="summary-value">{{ summary.pending_optimization_count || 0 }}</div>
            <div class="summary-label">{{ $t('knowledgeBase.statistics.pendingOptimization') }}</div>
          </div>
        </div>
      </t-card>
    </div>

    <!-- 筛选和操作栏 -->
    <div class="stats-toolbar">
      <div class="filter-section">
        <t-input
          v-model="filters.keyword"
          :placeholder="$t('knowledgeBase.statistics.searchPlaceholder')"
          clearable
          @enter="loadChunks"
          class="search-input"
        >
          <template #prefix-icon>
            <t-icon name="search" size="16px" />
          </template>
        </t-input>

        <t-select
          v-model="filters.pendingOnly"
          :placeholder="$t('knowledgeBase.statistics.filterByPending')"
          clearable
          class="filter-select"
        >
          <t-option :value="true" :label="$t('knowledgeBase.statistics.pendingOnly')" />
        </t-select>

        <t-select
          v-model="filters.sortBy"
          :placeholder="$t('knowledgeBase.statistics.sortBy')"
          class="filter-select"
        >
          <t-option value="like_rate" :label="$t('knowledgeBase.statistics.sortByLikeRate')" />
          <t-option value="like_count" :label="$t('knowledgeBase.statistics.sortByLikeCount')" />
          <t-option value="dislike_count" :label="$t('knowledgeBase.statistics.sortByDislikeCount')" />
          <t-option value="recall_weight" :label="$t('knowledgeBase.statistics.sortByWeight')" />
        </t-select>
      </div>

      <div class="action-section">
        <t-button
          theme="primary"
          variant="outline"
          @click="batchAdjustWeights"
          :loading="adjustingWeights"
        >
          {{ $t('knowledgeBase.statistics.batchAdjust') }}
        </t-button>
      </div>
    </div>

    <!-- 片段列表 -->
    <div class="chunk-list">
      <t-table
        :data="chunks"
        :columns="columns"
        :loading="loading"
        :pagination="pagination"
        row-key="id"
        hover
        stripe
        @page-change="handlePageChange"
        @sort-change="handleSortChange"
      >
        <template #like_count="{ row }">
          <span class="stat-value positive">
            <t-icon name="thumb-up" size="14px" />
            {{ row.like_count }}
          </span>
        </template>

        <template #dislike_count="{ row }">
          <span class="stat-value negative">
            <t-icon name="thumb-down" size="14px" />
            {{ row.dislike_count }}
          </span>
        </template>

        <template #like_rate="{ row }">
          <span :class="['rate-badge', getRateClass(row.like_rate)]">
            {{ formatPercent(row.like_rate) }}
          </span>
        </template>

        <template #recall_weight="{ row }">
          <t-progress
            :percentage="(row.recall_weight / 2) * 100"
            :label="false"
            :status="getWeightStatus(row.recall_weight)"
            theme="line"
            class="weight-progress"
          />
          <span class="weight-value">{{ row.recall_weight.toFixed(2) }}</span>
        </template>

        <template #is_pending_optimization="{ row }">
          <t-tag v-if="row.is_pending_optimization" theme="warning" variant="light">
            {{ $t('knowledgeBase.statistics.pendingOptimizationTag') }}
          </t-tag>
          <span v-else class="text-muted">-</span>
        </template>

        <template #operations="{ row }">
          <t-space>
            <t-button size="small" variant="text" @click="viewChunkDetail(row)">
              {{ $t('knowledgeBase.statistics.viewDetail') }}
            </t-button>
            <t-button
              v-if="hasAdminPermission"
              size="small"
              variant="text"
              theme="warning"
              @click="resetChunk(row)"
            >
              {{ $t('knowledgeBase.statistics.reset') }}
            </t-button>
          </t-space>
        </template>
      </t-table>
    </div>

    <!-- 片段详情弹窗 -->
    <t-dialog
      v-model:visible="detailDialogVisible"
      :header="$t('knowledgeBase.statistics.chunkDetail')"
      width="700px"
      :footer="false"
    >
      <div v-if="selectedChunk" class="chunk-detail">
        <t-descriptions :column="2" bordered>
          <t-descriptions-item :label="$t('knowledgeBase.statistics.chunkId')">
            {{ selectedChunk.id }}
          </t-descriptions-item>
          <t-descriptions-item :label="$t('knowledgeBase.statistics.likeCount')">
            {{ selectedChunk.like_count }}
          </t-descriptions-item>
          <t-descriptions-item :label="$t('knowledgeBase.statistics.dislikeCount')">
            {{ selectedChunk.dislike_count }}
          </t-descriptions-item>
          <t-descriptions-item :label="$t('knowledgeBase.statistics.likeRate')">
            {{ formatPercent(selectedChunk.like_rate) }}
          </t-descriptions-item>
          <t-descriptions-item :label="$t('knowledgeBase.statistics.recallWeight')">
            {{ selectedChunk.recall_weight.toFixed(2) }}
          </t-descriptions-item>
          <t-descriptions-item :label="$t('knowledgeBase.statistics.status')">
            <t-tag v-if="selectedChunk.is_pending_optimization" theme="warning">
              {{ $t('knowledgeBase.statistics.pendingOptimizationTag') }}
            </t-tag>
            <span v-else>{{ $t('knowledgeBase.statistics.normal') }}</span>
          </t-descriptions-item>
        </t-descriptions>

        <div class="detail-section">
          <h4>{{ $t('knowledgeBase.statistics.dislikeReasons') }}</h4>
          <div class="reason-stats">
            <div
              v-for="(count, reason) in selectedChunkStats?.dislike_reason_stats"
              :key="reason"
              class="reason-item"
            >
              <span class="reason-label">{{ getDislikeReasonLabel(reason) }}</span>
              <span class="reason-count">{{ count }}</span>
            </div>
            <div v-if="!selectedChunkStats?.dislike_reason_stats?.length" class="no-data">
              {{ $t('knowledgeBase.statistics.noDislikeReasons') }}
            </div>
          </div>
        </div>

        <div class="detail-section">
          <h4>{{ $t('knowledgeBase.statistics.weightLogs') }}</h4>
          <t-table :data="weightLogs" :columns="weightLogColumns" row-key="id" size="small">
            <template #trigger_type="{ row }">
              <t-tag :theme="getTriggerTypeTheme(row.trigger_type)">
                {{ getTriggerTypeLabel(row.trigger_type) }}
              </t-tag>
            </template>
          </t-table>
        </div>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listChunksByStats,
  getChunkStats,
  getChunkWeightLogs,
  resetChunkFeedback,
  batchAdjustWeights as apiBatchAdjustWeights,
  getFeedbackSummary,
  type ChunkStatsDetail,
  type ChunkWeightLog,
} from '@/api/chunk-feedback';
import { useI18n } from 'vue-i18n';
import { useAuthStore } from '@/stores/auth';

const props = defineProps<{
  kbId: string;
}>();

const { t } = useI18n();
const authStore = useAuthStore();

const loading = ref(false);
const adjustingWeights = ref(false);
const summary = ref<any>({});
const chunks = ref<any[]>([]);
const filters = ref({
  keyword: '',
  pendingOnly: null as boolean | null,
  sortBy: 'like_rate',
});
const pagination = ref({
  current: 1,
  pageSize: 20,
  total: 0,
});

const detailDialogVisible = ref(false);
const selectedChunk = ref<any>(null);
const selectedChunkStats = ref<ChunkStatsDetail | null>(null);
const weightLogs = ref<ChunkWeightLog[]>([]);

const hasAdminPermission = computed(() => {
  return authStore.isAdmin;
});

const columns = computed(() => [
  {
    colKey: 'id',
    title: 'ID',
    width: 200,
    ellipsis: true,
  },
  {
    colKey: 'like_count',
    title: t('knowledgeBase.statistics.likes'),
    width: 100,
    sorter: true,
  },
  {
    colKey: 'dislike_count',
    title: t('knowledgeBase.statistics.dislikes'),
    width: 100,
    sorter: true,
  },
  {
    colKey: 'like_rate',
    title: t('knowledgeBase.statistics.likeRate'),
    width: 120,
    sorter: true,
  },
  {
    colKey: 'recall_weight',
    title: t('knowledgeBase.statistics.weight'),
    width: 150,
    sorter: true,
  },
  {
    colKey: 'is_pending_optimization',
    title: t('knowledgeBase.statistics.status'),
    width: 120,
  },
  {
    colKey: 'operations',
    title: t('knowledgeBase.statistics.actions'),
    width: 180,
    fixed: 'right',
  },
]);

const weightLogColumns = [
  { colKey: 'trigger_type', title: t('knowledgeBase.statistics.triggerType') },
  { colKey: 'trigger_reason', title: t('knowledgeBase.statistics.triggerReason') },
  { colKey: 'old_weight', title: t('knowledgeBase.statistics.oldWeight') },
  { colKey: 'new_weight', title: t('knowledgeBase.statistics.newWeight') },
  { colKey: 'created_at', title: t('knowledgeBase.statistics.createdAt') },
];

onMounted(() => {
  loadSummary();
  loadChunks();
});

watch(() => props.kbId, () => {
  loadSummary();
  loadChunks();
});

async function loadSummary() {
  try {
    const res = await getFeedbackSummary(props.kbId);
    summary.value = res;
  } catch (error) {
    console.error('Failed to load summary:', error);
  }
}

async function loadChunks() {
  loading.value = true;
  try {
    const params = {
      keyword: filters.value.keyword || undefined,
      pending_optimization: filters.value.pendingOnly || undefined,
      sort_by: filters.value.sortBy as any,
      sort_order: 'desc',
      page: pagination.value.current,
      page_size: pagination.value.pageSize,
    };

    const res = await listChunksByStats(props.kbId, params);
    chunks.value = res.items;
    pagination.value.total = res.total;
  } catch (error) {
    console.error('Failed to load chunks:', error);
    MessagePlugin.error(t('knowledgeBase.statistics.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function handlePageChange(pageInfo: any) {
  pagination.value.current = pageInfo.current;
  pagination.value.pageSize = pageInfo.pageSize;
  await loadChunks();
}

async function handleSortChange(sorter: any) {
  if (sorter?.field) {
    filters.value.sortBy = sorter.field;
    await loadChunks();
  }
}

async function viewChunkDetail(chunk: any) {
  selectedChunk.value = chunk;
  detailDialogVisible.value = true;

  try {
    const statsRes = await getChunkStats(chunk.id);
    selectedChunkStats.value = statsRes;

    const logsRes = await getChunkWeightLogs(chunk.id);
    weightLogs.value = logsRes.items;
  } catch (error) {
    console.error('Failed to load chunk detail:', error);
  }
}

async function resetChunk(chunk: any) {
  try {
    await resetChunkFeedback(chunk.id);
    MessagePlugin.success(t('knowledgeBase.statistics.resetSuccess'));
    await loadChunks();
    await loadSummary();
  } catch (error) {
    console.error('Failed to reset chunk:', error);
    MessagePlugin.error(t('knowledgeBase.statistics.resetFailed'));
  }
}

async function batchAdjustWeights() {
  adjustingWeights.value = true;
  try {
    await apiBatchAdjustWeights(props.kbId);
    MessagePlugin.success(t('knowledgeBase.statistics.adjustSuccess'));
    await loadChunks();
    await loadSummary();
  } catch (error) {
    console.error('Failed to batch adjust weights:', error);
    MessagePlugin.error(t('knowledgeBase.statistics.adjustFailed'));
  } finally {
    adjustingWeights.value = false;
  }
}

function formatPercent(value: number): string {
  if (value === null || value === undefined) return '-';
  return `${(value * 100).toFixed(1)}%`;
}

function getRateClass(rate: number): string {
  if (rate >= 0.8) return 'rate-high';
  if (rate >= 0.5) return 'rate-medium';
  return 'rate-low';
}

function getWeightStatus(weight: number): string {
  if (weight >= 1.5) return 'success';
  if (weight >= 1.0) return 'primary';
  return 'warning';
}

function getDislikeReasonLabel(reason: string): string {
  const labels: Record<string, string> = {
    inaccurate: t('knowledgeBase.statistics.reasonInaccurate'),
    incomplete: t('knowledgeBase.statistics.reasonIncomplete'),
    irrelevant: t('knowledgeBase.statistics.reasonIrrelevant'),
    other: t('knowledgeBase.statistics.reasonOther'),
  };
  return labels[reason] || reason;
}

function getTriggerTypeTheme(type: string): string {
  const themes: Record<string, string> = {
    user_feedback: 'primary',
    auto_adjust: 'success',
    manual_reset: 'warning',
    batch_update: 'default',
  };
  return themes[type] || 'default';
}

function getTriggerTypeLabel(type: string): string {
  const labels: Record<string, string> = {
    user_feedback: t('knowledgeBase.statistics.triggerUserFeedback'),
    auto_adjust: t('knowledgeBase.statistics.triggerAutoAdjust'),
    manual_reset: t('knowledgeBase.statistics.triggerManualReset'),
    batch_update: t('knowledgeBase.statistics.triggerBatchUpdate'),
  };
  return labels[type] || type;
}
</script>

<style lang="less" scoped>
.chunk-stats {
  padding: 20px;

  .stats-overview {
    margin-bottom: 20px;

    .summary-card {
      :deep(.t-card__body) {
        padding: 20px;
      }
    }

    .summary-grid {
      display: grid;
      grid-template-columns: repeat(6, 1fr);
      gap: 20px;

      @media (max-width: 1200px) {
        grid-template-columns: repeat(3, 1fr);
      }

      @media (max-width: 768px) {
        grid-template-columns: repeat(2, 1fr);
      }

      .summary-item {
        text-align: center;
        padding: 16px;
        border-radius: 8px;
        background: var(--td-bg-color-container-hover);

        .summary-value {
          font-size: 28px;
          font-weight: 600;
          color: var(--td-text-color-primary);

          &.positive {
            color: var(--td-success-color);
          }

          &.negative {
            color: var(--td-error-color);
          }
        }

        .summary-label {
          margin-top: 8px;
          font-size: 14px;
          color: var(--td-text-color-secondary);
        }

        &.warning .summary-value {
          color: var(--td-warning-color);
        }
      }
    }
  }

  .stats-toolbar {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 20px;
    gap: 16px;
    flex-wrap: wrap;

    .filter-section {
      display: flex;
      gap: 12px;
      flex-wrap: wrap;
      flex: 1;

      .search-input {
        width: 240px;
      }

      .filter-select {
        width: 160px;
      }
    }

    .action-section {
      display: flex;
      gap: 12px;
    }
  }

  .chunk-list {
    background: var(--td-bg-color-container);
    border-radius: 8px;
    overflow: hidden;
  }

  .stat-value {
    display: inline-flex;
    align-items: center;
    gap: 4px;

    &.positive {
      color: var(--td-success-color);
    }

    &.negative {
      color: var(--td-error-color);
    }
  }

  .rate-badge {
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 500;

    &.rate-high {
      background: rgba(46, 194, 113, 0.1);
      color: var(--td-success-color);
    }

    &.rate-medium {
      background: rgba(240, 160, 48, 0.1);
      color: var(--td-warning-color);
    }

    &.rate-low {
      background: rgba(242, 98, 75, 0.1);
      color: var(--td-error-color);
    }
  }

  .weight-progress {
    width: 80px;
    display: inline-block;
    margin-right: 8px;
  }

  .weight-value {
    font-size: 12px;
    color: var(--td-text-color-secondary);
  }

  .text-muted {
    color: var(--td-text-color-disabled);
  }

  .chunk-detail {
    .detail-section {
      margin-top: 20px;

      h4 {
        margin-bottom: 12px;
        font-size: 14px;
        font-weight: 500;
        color: var(--td-text-color-primary);
      }

      .reason-stats {
        display: flex;
        flex-wrap: wrap;
        gap: 12px;

        .reason-item {
          display: flex;
          align-items: center;
          gap: 8px;
          padding: 8px 12px;
          background: var(--td-bg-color-container-hover);
          border-radius: 4px;

          .reason-label {
            font-size: 13px;
            color: var(--td-text-color-secondary);
          }

          .reason-count {
            font-weight: 600;
            color: var(--td-error-color);
          }
        }

        .no-data {
          color: var(--td-text-color-disabled);
          font-size: 13px;
        }
      }
    }
  }
}
</style>
