<template>
  <div class="chunk-stats-page">
    <div class="chunk-stats-header">
      <div class="chunk-stats-header-left">
        <span class="chunk-stats-title">{{ $t('knowledgeBase.chunkStats.title') }}</span>
      </div>
      <div class="chunk-stats-header-right">
        <t-input-number
          v-model="maxPositiveRate"
          size="small"
          :min="0"
          :max="1"
          :step="0.05"
          :placeholder="$t('knowledgeBase.chunkStats.maxPositiveRate')"
          class="chunk-stats-filter"
        />
        <t-switch v-model="onlyNeedsOptimization" size="small" class="chunk-stats-filter">
          <template #label>{{ $t('knowledgeBase.chunkStats.onlyNeedsOptimization') }}</template>
        </t-switch>
        <t-button theme="primary" variant="outline" size="small" :loading="loading" @click="reload">
          {{ $t('common.refresh') }}
        </t-button>
      </div>
    </div>

    <div class="data-table-shell data-table-shell--with-footer">
      <div class="data-table-shell__scroll">
        <t-table row-key="chunk_id" :data="rows" :columns="columns" size="medium" hover :loading="loading">
          <template #positive_rate="{ row }">
            <span>{{ formatRate(row.positive_rate) }}</span>
          </template>
          <template #needs_optimization="{ row }">
            <t-tag v-if="row.needs_optimization" theme="warning" size="small">
              {{ $t('knowledgeBase.chunkStats.needsOptimization') }}
            </t-tag>
            <t-tag v-else theme="success" size="small">
              {{ $t('knowledgeBase.chunkStats.ok') }}
            </t-tag>
          </template>
          <template #dislike_reasons="{ row }">
            <div class="chunk-stats-reasons">
              <span v-if="!row.dislike_reasons || row.dislike_reasons.length === 0" class="chunk-stats-muted">
                {{ $t('knowledgeBase.chunkStats.noReasons') }}
              </span>
              <template v-else>
                <span v-for="item in row.dislike_reasons" :key="item.reason" class="chunk-stats-reason">
                  {{ item.reason }} ({{ item.count }})
                </span>
              </template>
            </div>
          </template>
          <template #actions="{ row }">
            <t-button variant="text" size="small" @click="openLogs(row)">
              {{ $t('knowledgeBase.chunkStats.viewLogs') }}
            </t-button>
            <t-button variant="text" size="small" @click="openEditWeight(row)">
              {{ $t('knowledgeBase.chunkStats.editWeight') }}
            </t-button>
            <t-popconfirm
              theme="warning"
              :content="$t('knowledgeBase.chunkStats.resetConfirm')"
              :confirm-btn="{ content: $t('common.confirm'), theme: 'danger' }"
              :cancel-btn="$t('common.cancel')"
              placement="left"
              @confirm="() => handleReset(row)"
            >
              <t-button variant="text" size="small" theme="danger">
                {{ $t('knowledgeBase.chunkStats.reset') }}
              </t-button>
            </t-popconfirm>
          </template>
        </t-table>
      </div>
      <div v-if="total > 0" class="data-table-shell__pager">
        <t-pagination
          v-model="page"
          v-model:page-size="pageSize"
          :total="total"
          size="small"
          show-jumper
          show-page-number
          show-page-size
          :page-size-options="[10, 20, 50, 100]"
          @change="reload"
        />
      </div>
    </div>

    <t-drawer v-model:visible="logsVisible" :header="$t('knowledgeBase.chunkStats.logsTitle')" size="520px" :footer="false">
      <div class="chunk-stats-logs-body">
        <t-table row-key="id" :data="logs" :columns="logColumns" size="small" hover :loading="logsLoading" />
      </div>
    </t-drawer>

    <t-dialog
      v-model:visible="editWeightVisible"
      :header="$t('knowledgeBase.chunkStats.editWeightTitle')"
      width="400px"
      :close-on-overlay-click="false"
      destroy-on-close
      @close="handleCloseEditWeight"
    >
      <div class="edit-weight-body">
        <div class="edit-weight-label">{{ $t('knowledgeBase.chunkStats.editWeightLabel') }}</div>
        <t-input-number
          v-model="editWeightValue"
          :min="0.1"
          :max="10.0"
          :step="0.05"
          :decimal-places="2"
          class="edit-weight-input"
        />
      </div>
      <template #footer>
        <t-button variant="outline" size="small" @click="handleCloseEditWeight">
          {{ $t('common.cancel') }}
        </t-button>
        <t-button theme="primary" size="small" :loading="editWeightLoading" @click="submitEditWeight">
          {{ $t('common.confirm') }}
        </t-button>
      </template>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { getChunkFeedbackStats, getChunkRecallWeightLogs, resetChunkFeedback, updateChunkWeight } from '@/api/knowledge-base';

const props = defineProps<{ kbId: string }>();

const { t } = useI18n();

const loading = ref(false);
const rows = ref<any[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);

const maxPositiveRate = ref<number | undefined>(0.5);
const onlyNeedsOptimization = ref(false);

const logsVisible = ref(false);
const logsLoading = ref(false);
const logs = ref<any[]>([]);
const activeChunkId = ref('');

const columns = computed(() => [
  { colKey: 'knowledge_title', title: t('knowledgeBase.chunkStats.knowledgeTitle'), width: 220, ellipsis: true },
  { colKey: 'chunk_index', title: t('knowledgeBase.chunkStats.chunkIndex'), width: 90 },
  { colKey: 'content_preview', title: t('knowledgeBase.chunkStats.preview'), ellipsis: true },
  { colKey: 'like_count', title: t('knowledgeBase.chunkStats.likeCount'), width: 90 },
  { colKey: 'dislike_count', title: t('knowledgeBase.chunkStats.dislikeCount'), width: 90 },
  { colKey: 'positive_rate', title: t('knowledgeBase.chunkStats.positiveRate'), width: 110 },
  { colKey: 'session_count', title: t('knowledgeBase.chunkStats.sessionCount'), width: 110 },
  { colKey: 'recall_weight', title: t('knowledgeBase.chunkStats.recallWeight'), width: 110 },
  { colKey: 'needs_optimization', title: t('knowledgeBase.chunkStats.status'), width: 110 },
  { colKey: 'dislike_reasons', title: t('knowledgeBase.chunkStats.dislikeReasons'), width: 260 },
  { colKey: 'actions', title: t('common.actions'), width: 160, fixed: 'right' },
]);

const logColumns = computed(() => [
  { colKey: 'created_at', title: t('knowledgeBase.chunkStats.logTime'), width: 160 },
  { colKey: 'trigger_type', title: t('knowledgeBase.chunkStats.logTrigger'), width: 120 },
  { colKey: 'old_weight', title: t('knowledgeBase.chunkStats.oldWeight'), width: 100 },
  { colKey: 'new_weight', title: t('knowledgeBase.chunkStats.newWeight'), width: 100 },
  { colKey: 'positive_rate', title: t('knowledgeBase.chunkStats.positiveRate'), width: 100 },
]);

const formatRate = (value: any) => {
  const n = Number(value);
  if (!Number.isFinite(n)) return '-';
  return `${Math.round(n * 100)}%`;
};

const reload = async () => {
  if (!props.kbId) return;
  loading.value = true;
  try {
    const res: any = await getChunkFeedbackStats(props.kbId, {
      page: page.value,
      page_size: pageSize.value,
      max_positive_rate: maxPositiveRate.value,
      needs_optimization: onlyNeedsOptimization.value ? true : undefined,
    });
    rows.value = res?.data || [];
    total.value = res?.total || 0;
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('common.failed'));
  } finally {
    loading.value = false;
  }
};

const openLogs = async (row: any) => {
  activeChunkId.value = row?.chunk_id || '';
  if (!activeChunkId.value) return;
  logsVisible.value = true;
  logsLoading.value = true;
  try {
    const res: any = await getChunkRecallWeightLogs(props.kbId, activeChunkId.value, 100);
    logs.value = res?.data || [];
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('common.failed'));
  } finally {
    logsLoading.value = false;
  }
};

const handleReset = async (row: any) => {
  const chunkId = row?.chunk_id || '';
  if (!chunkId) return;
  try {
    await resetChunkFeedback(props.kbId, chunkId);
    MessagePlugin.success(t('common.success'));
    await reload();
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('common.failed'));
  }
};

const editWeightVisible = ref(false);
const editWeightValue = ref(1.0);
const editWeightLoading = ref(false);
const editWeightTarget = ref<any>(null);

const openEditWeight = (row: any) => {
  editWeightTarget.value = row;
  editWeightValue.value = row?.recall_weight ?? 1.0;
  editWeightVisible.value = true;
};

const handleCloseEditWeight = () => {
  if (editWeightLoading.value) return;
  editWeightVisible.value = false;
  editWeightTarget.value = null;
};

const submitEditWeight = async () => {
  const chunkId = editWeightTarget.value?.chunk_id;
  if (!chunkId) return;
  editWeightLoading.value = true;
  try {
    await updateChunkWeight(props.kbId, chunkId, editWeightValue.value);
    MessagePlugin.success(t('knowledgeBase.chunkStats.weightUpdated'));
    editWeightVisible.value = false;
    editWeightTarget.value = null;
    await reload();
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('common.failed'));
  } finally {
    editWeightLoading.value = false;
  }
};

watch(
  () => props.kbId,
  () => {
    page.value = 1;
    reload();
  },
  { immediate: true },
);
</script>
