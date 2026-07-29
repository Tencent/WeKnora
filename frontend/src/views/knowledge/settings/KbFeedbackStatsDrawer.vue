<template>
  <t-drawer
    v-model:visible="visible"
    :header="title"
    :size="900"
    :footer="false"
    :close-on-esc="true"
    destroy-on-close
    @close="onClose"
    class="kb-feedback-stats-drawer"
  >
    <div class="kb-feedback-stats-drawer__toolbar">
      <t-input
        v-model="filters.keyword"
        :placeholder="$t('feedback.kbStats.columnChunk')"
        clearable
        style="max-width: 240px;"
        @change="reload()"
      />
      <t-select
        v-model="filters.sortBy"
        :options="sortOptions"
        :placeholder="$t('common.sort')"
        @change="reload()"
        style="max-width: 220px;"
      />
      <t-checkbox v-model="filters.lowQualityOnly" @change="reload()">
        {{ $t('feedback.kbStats.lowQualityOnly') }}
      </t-checkbox>
      <t-button theme="danger" variant="outline" @click="onReset" data-test="kb-feedback-reset-btn">
        <template #icon><t-icon name="refresh" /></template>
        {{ $t('feedback.kbStats.reset') }}
      </t-button>
    </div>
    <t-table
      :data="rows"
      :columns="columns"
      :pagination="pagination"
      :loading="loading"
      row-key="chunk_id"
      :hover="true"
      size="small"
      @page-change="onPageChange"
    >
      <template #positive_rate="{ row }">
        <span :class="positiveRateClass(row.positive_rate)">
          {{ formatPercent(row.positive_rate) }}
        </span>
        <t-tag v-if="isLowQuality(row)" theme="warning" variant="light" size="small">
          {{ $t('feedback.kbStats.needsOptimization') }}
        </t-tag>
      </template>
      <template #last_feedback_at="{ row }">
        <span class="kb-feedback-stats-drawer__date">
          {{ row.last_feedback_at ? formatDate(row.last_feedback_at) : '—' }}
        </span>
      </template>
      <template #dislike_reasons="{ row }">
        <span class="kb-feedback-stats-drawer__reasons">
          <template v-if="row.dislike_reasons && Object.keys(row.dislike_reasons).length > 0">
            <t-tag
              v-for="(count, reason) in row.dislike_reasons"
              :key="reason"
              size="small"
              theme="danger"
              variant="light"
            >
              {{ t(`feedback.reasons.${reason}`) }} ({{ count }})
            </t-tag>
          </template>
          <template v-else>—</template>
        </span>
      </template>
      <template #op="{ row }">
        <t-link theme="primary" hover="color" @click="openWeightLogs(row.chunk_id)">
          {{ $t('feedback.kbStats.weightLogTitle') }}
        </t-link>
      </template>
    </t-table>

    <KbFeedbackWeightLogDrawer
      v-model:visible="weightLogDrawerVisible"
      :kb-id="kbId"
      :chunk-id="weightLogChunkId"
    />
  </t-drawer>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { getChunkFeedbackStats, resetKnowledgeBaseFeedback } from '@/api/chat';
import KbFeedbackWeightLogDrawer from './KbFeedbackWeightLogDrawer.vue';

const props = defineProps({
  kbId: { type: String, required: true },
  visible: { type: Boolean, default: false },
  title: { type: String, default: '' },
  // tenant-scoped "needs optimization" positive-rate ceiling. When a chunk's
  // positive rate is at or below this, it surfaces a tag in the listing.
  needsOptimizationThreshold: { type: Number, default: 0.2 },
});
const emit = defineEmits(['update:visible']);

const { t } = useI18n();
const visible = computed({
  get: () => props.visible,
  set: (v) => emit('update:visible', v),
});

const filters = reactive({
  keyword: '',
  sortBy: 'positive_rate_asc',
  lowQualityOnly: false,
});
const loading = ref(false);
const rows = ref([]);
const total = ref(0);

const pagination = reactive({
  current: 1,
  pageSize: 20,
  total: 0,
  defaultPageSize: 20,
  defaultCurrent: 1,
});

const sortOptions = computed(() => [
  { label: t('feedback.kbStats.sortPositiveRateAsc'), value: 'positive_rate_asc' },
  { label: t('feedback.kbStats.sortPositiveRateDesc'), value: 'positive_rate_desc' },
  { label: t('feedback.kbStats.sortFeedbackCountDesc'), value: 'feedback_count_desc' },
  { label: t('feedback.kbStats.sortLastFeedbackDesc'), value: 'last_feedback_desc' },
]);

const columns = computed(() => [
  { colKey: 'content_preview', title: t('feedback.kbStats.columnChunk'), ellipsis: true, minWidth: 180 },
  { colKey: 'like_count', title: t('feedback.kbStats.columnLikeCount'), width: 70 },
  { colKey: 'dislike_count', title: t('feedback.kbStats.columnDislikeCount'), width: 70 },
  { colKey: 'session_count', title: t('feedback.kbStats.columnSessionCount'), width: 80 },
  { colKey: 'positive_rate', title: t('feedback.kbStats.columnPositiveRate'), width: 140 },
  { colKey: 'dislike_reasons', title: t('feedback.kbStats.columnDislikeReasons'), minWidth: 160 },
  { colKey: 'last_feedback_at', title: t('feedback.kbStats.columnLastFeedback'), width: 160 },
  { colKey: 'op', title: '', width: 120 },
]);

function positiveRateClass(rate) {
  if (rate >= 0.8) return 'kb-feedback-stats-drawer__rate--good';
  if (rate <= props.needsOptimizationThreshold) return 'kb-feedback-stats-drawer__rate--bad';
  return 'kb-feedback-stats-drawer__rate--neutral';
}

function isLowQuality(row) {
  return row.positive_rate <= props.needsOptimizationThreshold;
}

function formatPercent(rate) {
  if (rate === undefined || rate === null) return '—';
  const pct = Math.max(0, Math.min(1, rate)) * 100;
  return `${pct.toFixed(1)}%`;
}

function formatDate(value) {
  if (!value) return '—';
  try {
    return new Date(value).toLocaleString();
  } catch (_) {
    return String(value);
  }
}

async function reload() {
  if (!props.kbId || !visible.value) return;
  loading.value = true;
  try {
    const resp = await getChunkFeedbackStats({
      kb_id: props.kbId,
      page: pagination.current,
      page_size: pagination.pageSize,
      sort_by: filters.sortBy || 'positive_rate_asc',
      low_quality: filters.lowQualityOnly,
      keyword: filters.keyword,
    });
    const page = resp?.data || {};
    rows.value = page.data || [];
    total.value = page.total || 0;
    pagination.total = total.value;
  } catch (err) {
    MessagePlugin.error(t('common.error'));
  } finally {
    loading.value = false;
  }
}

function onPageChange(pageInfo) {
  pagination.current = pageInfo.current;
  pagination.pageSize = pageInfo.pageSize;
  reload();
}

async function onReset() {
  const dialog = DialogPlugin.confirm({
    header: t('feedback.kbStats.reset'),
    body: t('feedback.kbStats.resetConfirm'),
    onConfirm: async () => {
      try {
        await resetKnowledgeBaseFeedback(props.kbId);
        MessagePlugin.success(t('feedback.kbStats.resetSuccess'));
        await reload();
        dialog.hide();
      } catch (err) {
        MessagePlugin.error(t('common.error'));
      }
    },
    onCancel: () => dialog.hide(),
  });
}

const weightLogDrawerVisible = ref(false);
const weightLogChunkId = ref('');

function openWeightLogs(chunkId) {
  weightLogChunkId.value = chunkId;
  weightLogDrawerVisible.value = true;
}

function onClose() {
  emit('update:visible', false);
}

watch(
  () => props.visible,
  (v) => {
    if (v) {
      pagination.current = 1;
      reload();
    }
  },
);
</script>

<style scoped>
.kb-feedback-stats-drawer__toolbar {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.kb-feedback-stats-drawer__rate--good { color: var(--td-success-color, #2ba471); font-weight: 600; }
.kb-feedback-stats-drawer__rate--bad { color: var(--td-error-color, #d54941); font-weight: 600; }
.kb-feedback-stats-drawer__rate--neutral { color: var(--td-text-color-secondary, #666); }
.kb-feedback-stats-drawer__date { color: var(--td-text-color-secondary, #666); }
.kb-feedback-stats-drawer__reasons { display: flex; flex-wrap: wrap; gap: 4px; }
</style>