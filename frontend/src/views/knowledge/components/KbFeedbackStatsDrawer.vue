<template>
  <SettingDrawer
    v-model:visible="drawerVisible"
    :title="$t('kbFeedback.title')"
    :description="$t('kbFeedback.description')"
    icon="thumb-up"
    width="760px"
    :min-width="560"
    :max-width="960"
    resizable
    storage-key="setting-drawer:width:kb-feedback-stats"
    :hide-footer="true"
  >
    <t-tabs v-model="activeTab" class="feedback-tabs">
      <t-tab-panel value="stats" :label="$t('kbFeedback.tabStats')">
        <div class="feedback-toolbar">
          <t-checkbox v-model="needsOptOnly" @change="reloadStats">
            {{ $t('kbFeedback.needsOptOnly') }}
          </t-checkbox>
          <t-select v-model="rateFilter" size="small" class="feedback-rate-filter" @change="reloadStats">
            <t-option value="all" :label="$t('kbFeedback.rateFilterAll')" />
            <t-option value="low" :label="$t('kbFeedback.rateFilterLow')" />
            <t-option value="high" :label="$t('kbFeedback.rateFilterHigh')" />
          </t-select>
          <div class="feedback-toolbar__spacer" />
          <t-popconfirm :content="$t('kbFeedback.resetConfirm')" theme="danger" @confirm="handleReset">
            <t-button size="small" theme="danger" variant="outline" :loading="resetting"
              :disabled="statsLoading">
              {{ $t('kbFeedback.resetAction') }}
            </t-button>
          </t-popconfirm>
        </div>

        <t-table
          row-key="chunk_id"
          size="small"
          :data="stats"
          :columns="statsColumns"
          :loading="statsLoading"
          :pagination="statsPagination"
          :sort="statsSort"
          @page-change="onStatsPageChange"
          @sort-change="onStatsSortChange"
        >
          <template #content_preview="{ row }">
            <t-tooltip :content="row.content_preview" placement="top-left">
              <span class="feedback-chunk-preview">{{ row.content_preview }}</span>
            </t-tooltip>
          </template>
          <template #positive_rate="{ row }">
            {{ (row.positive_rate * 100).toFixed(0) }}%
          </template>
          <template #recall_weight="{ row }">
            <span :class="weightClass(row.recall_weight)">{{ row.recall_weight.toFixed(2) }}</span>
          </template>
          <template #needs_optimization="{ row }">
            <t-tag v-if="row.needs_optimization" theme="warning" size="small">
              {{ $t('kbFeedback.needsOptTag') }}
            </t-tag>
          </template>
          <template #dislike_reasons="{ row }">
            <span v-if="!row.dislike_reasons || !Object.keys(row.dislike_reasons).length">-</span>
            <div v-else class="feedback-reason-tags">
              <t-tag v-for="(count, reason) in row.dislike_reasons" :key="reason" size="small" variant="light">
                {{ $t(`chat.feedback.reason.${reason}`) }} ×{{ count }}
              </t-tag>
            </div>
          </template>
        </t-table>
      </t-tab-panel>

      <t-tab-panel value="logs" :label="$t('kbFeedback.tabWeightLogs')">
        <t-table
          row-key="id"
          size="small"
          :data="logs"
          :columns="logColumns"
          :loading="logsLoading"
          :pagination="logsPagination"
          @page-change="onLogsPageChange"
        >
          <template #weight_change="{ row }">
            {{ row.old_weight.toFixed(2) }} → {{ row.new_weight.toFixed(2) }}
          </template>
          <template #positive_rate="{ row }">
            {{ (row.positive_rate * 100).toFixed(0) }}%
          </template>
          <template #trigger_source="{ row }">
            <t-tag size="small" :theme="triggerTheme(row.trigger_source)" variant="light">
              {{ $t(`kbFeedback.trigger.${row.trigger_source}`) }}
            </t-tag>
          </template>
          <template #created_at="{ row }">
            {{ formatTime(row.created_at) }}
          </template>
        </t-table>
      </t-tab-panel>
    </t-tabs>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import SettingDrawer from '@/components/settings/SettingDrawer.vue';
import {
  getChunkFeedbackStats,
  getChunkWeightLogs,
  resetKbFeedback,
  type ChunkFeedbackStat,
  type ChunkWeightLog,
} from '@/api/knowledge-base/feedback';

const props = defineProps<{
  visible: boolean;
  kbId: string;
}>();

const emit = defineEmits<{
  'update:visible': [boolean];
}>();

const { t } = useI18n();

const drawerVisible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
});

const activeTab = ref('stats');

// ---- stats tab ----
const stats = ref<ChunkFeedbackStat[]>([]);
const statsLoading = ref(false);
const statsTotal = ref(0);
const statsPage = ref(1);
const statsPageSize = ref(10);
const statsSort = ref<{ sortBy: string; descending: boolean }>({ sortBy: 'dislike_count', descending: true });
const needsOptOnly = ref(false);
const rateFilter = ref<'all' | 'low' | 'high'>('all');
const resetting = ref(false);
// Generation guards: the KB can switch (route param reuse) while a request is
// in flight, so every load stamps a token + its target KB and a late response
// is dropped unless it is still the newest request for the current KB.
let statsLoadToken = 0;
let logsLoadToken = 0;

const statsColumns = computed(() => [
  { colKey: 'content_preview', title: t('kbFeedback.colChunk'), width: 220 },
  { colKey: 'knowledge_title', title: t('kbFeedback.colDocument'), width: 130, ellipsis: true },
  { colKey: 'like_count', title: t('kbFeedback.colLikes'), width: 76, sorter: true },
  { colKey: 'dislike_count', title: t('kbFeedback.colDislikes'), width: 76, sorter: true },
  { colKey: 'positive_rate', title: t('kbFeedback.colRate'), width: 88, sorter: true },
  { colKey: 'recall_weight', title: t('kbFeedback.colWeight'), width: 84, sorter: true },
  { colKey: 'needs_optimization', title: t('kbFeedback.colNeedsOpt'), width: 90 },
  { colKey: 'dislike_reasons', title: t('kbFeedback.colReasons'), width: 160 },
  { colKey: 'session_count', title: t('kbFeedback.colSessions'), width: 80 },
]);

const statsPagination = computed(() => ({
  current: statsPage.value,
  pageSize: statsPageSize.value,
  total: statsTotal.value,
  showJumper: false,
  pageSizeOptions: [10, 20, 50],
}));

const loadStats = async () => {
  if (!props.kbId) return;
  const token = ++statsLoadToken;
  const kbAtRequest = props.kbId;
  statsLoading.value = true;
  try {
    const res: any = await getChunkFeedbackStats(kbAtRequest, {
      page: statsPage.value,
      page_size: statsPageSize.value,
      sort_by: statsSort.value.sortBy as any,
      order: statsSort.value.descending ? 'desc' : 'asc',
      min_rate: rateFilter.value === 'high' ? 0.8 : undefined,
      max_rate: rateFilter.value === 'low' ? 0.5 : undefined,
      needs_optimization: needsOptOnly.value || undefined,
    });
    if (token !== statsLoadToken || props.kbId !== kbAtRequest) return;
    stats.value = res?.data?.data || [];
    statsTotal.value = res?.data?.total || 0;
  } catch (err) {
    if (token !== statsLoadToken || props.kbId !== kbAtRequest) return;
    console.error('Failed to load chunk feedback stats:', err);
    MessagePlugin.error(t('kbFeedback.loadFailed'));
  } finally {
    if (token === statsLoadToken) statsLoading.value = false;
  }
};

const reloadStats = () => {
  statsPage.value = 1;
  loadStats();
};

const onStatsPageChange = (pageInfo: { current: number; pageSize: number }) => {
  statsPage.value = pageInfo.current;
  statsPageSize.value = pageInfo.pageSize;
  loadStats();
};

const onStatsSortChange = (sort: { sortBy: string; descending: boolean } | Array<any> | undefined) => {
  const next = Array.isArray(sort) ? sort[0] : sort;
  statsSort.value = next?.sortBy
    ? { sortBy: next.sortBy, descending: next.descending !== false }
    : { sortBy: 'dislike_count', descending: true };
  reloadStats();
};

const handleReset = async () => {
  if (!props.kbId || resetting.value) return;
  resetting.value = true;
  try {
    const res: any = await resetKbFeedback(props.kbId);
    MessagePlugin.success(t('kbFeedback.resetSuccess', { count: res?.data?.reset_chunks ?? 0 }));
    reloadStats();
    if (activeTab.value === 'logs') loadLogs();
  } catch (err) {
    console.error('Failed to reset KB feedback:', err);
    MessagePlugin.error(t('kbFeedback.resetFailed'));
  } finally {
    resetting.value = false;
  }
};

const weightClass = (weight: number) => {
  if (weight > 1) return 'feedback-weight--boost';
  if (weight < 1) return 'feedback-weight--penalty';
  return '';
};

// ---- weight logs tab ----
const logs = ref<ChunkWeightLog[]>([]);
const logsLoading = ref(false);
const logsTotal = ref(0);
const logsPage = ref(1);
const logsPageSize = ref(10);

const logColumns = computed(() => [
  { colKey: 'chunk_id', title: t('kbFeedback.colChunkId'), width: 120, ellipsis: true },
  { colKey: 'weight_change', title: t('kbFeedback.colWeightChange'), width: 120 },
  { colKey: 'positive_rate', title: t('kbFeedback.colRate'), width: 88 },
  { colKey: 'trigger_source', title: t('kbFeedback.colTrigger'), width: 100 },
  { colKey: 'created_at', title: t('kbFeedback.colTime'), width: 150 },
]);

const logsPagination = computed(() => ({
  current: logsPage.value,
  pageSize: logsPageSize.value,
  total: logsTotal.value,
  showJumper: false,
  pageSizeOptions: [10, 20, 50],
}));

const loadLogs = async () => {
  if (!props.kbId) return;
  const token = ++logsLoadToken;
  const kbAtRequest = props.kbId;
  logsLoading.value = true;
  try {
    const res: any = await getChunkWeightLogs(kbAtRequest, {
      page: logsPage.value,
      page_size: logsPageSize.value,
    });
    if (token !== logsLoadToken || props.kbId !== kbAtRequest) return;
    logs.value = res?.data?.data || [];
    logsTotal.value = res?.data?.total || 0;
  } catch (err) {
    if (token !== logsLoadToken || props.kbId !== kbAtRequest) return;
    console.error('Failed to load chunk weight logs:', err);
    MessagePlugin.error(t('kbFeedback.loadFailed'));
  } finally {
    if (token === logsLoadToken) logsLoading.value = false;
  }
};

const onLogsPageChange = (pageInfo: { current: number; pageSize: number }) => {
  logsPage.value = pageInfo.current;
  logsPageSize.value = pageInfo.pageSize;
  loadLogs();
};

const formatTime = (raw: string) => {
  if (!raw) return '-';
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return raw;
  return date.toLocaleString();
};

const triggerTheme = (trigger: string) => {
  if (trigger === 'reset') return 'danger';
  if (trigger === 'config') return 'primary';
  return 'default';
};

// Clears both tabs' data and pagination. Bumping the load tokens invalidates
// any in-flight response so it cannot repopulate the cleared view.
const resetViewState = () => {
  statsLoadToken++;
  logsLoadToken++;
  stats.value = [];
  statsTotal.value = 0;
  statsPage.value = 1;
  logs.value = [];
  logsTotal.value = 0;
  logsPage.value = 1;
};

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      reloadStats();
      if (activeTab.value === 'logs') loadLogs();
    }
  },
);

// The KnowledgeBase route component is reused across KB switches, so the drawer
// can stay mounted while props.kbId changes. Drop stale data immediately and
// reload for the new KB, otherwise reset would target a KB the user no longer
// sees (F5).
watch(
  () => props.kbId,
  () => {
    resetViewState();
    if (props.visible) {
      loadStats();
      if (activeTab.value === 'logs') loadLogs();
    }
  },
);

watch(activeTab, (tab) => {
  if (tab === 'logs' && props.visible) {
    logsPage.value = 1;
    loadLogs();
  }
});
</script>

<style lang="less" scoped>
.feedback-tabs {
  :deep(.t-tabs__content) {
    padding-top: 12px;
  }
}

.feedback-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 12px;

  .feedback-toolbar__spacer {
    flex: 1;
  }

  .feedback-rate-filter {
    width: 160px;
  }
}

.feedback-chunk-preview {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  word-break: break-all;
  font-size: 12px;
}

.feedback-reason-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.feedback-weight--boost {
  color: var(--td-success-color);
  font-weight: 600;
}

.feedback-weight--penalty {
  color: var(--td-error-color);
  font-weight: 600;
}
</style>
