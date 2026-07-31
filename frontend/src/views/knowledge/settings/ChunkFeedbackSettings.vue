<template>
  <div class="section-content chunk-feedback-settings">
    <div class="section-header">
      <h3 class="section-title">{{ t('knowledgeEditor.feedback.title') }}</h3>
      <p class="section-desc">{{ t('knowledgeEditor.feedback.description') }}</p>
    </div>

    <div class="section-body">
      <t-alert v-if="error" theme="error" :message="error" class="chunk-feedback-error">
        <template #operation>
          <t-button size="small" @click="reload">{{ t('knowledgeEditor.feedback.retry') }}</t-button>
        </template>
      </t-alert>

      <div class="chunk-feedback-filter-bar">
        <t-input
          v-model="keyword"
          class="chunk-feedback-filter chunk-feedback-filter--keyword"
          :placeholder="t('knowledgeEditor.feedback.keywordPlaceholder')"
          clearable
          @enter="applyFilters"
          @clear="applyFilters"
        />
        <t-select
          v-model="approvalFilter"
          class="chunk-feedback-filter"
          :placeholder="t('knowledgeEditor.feedback.approvalPlaceholder')"
          :options="approvalOptions"
          @change="applyFilters"
        />
        <t-select
          v-model="sortBy"
          class="chunk-feedback-filter chunk-feedback-filter--sort"
          :options="sortOptions"
          @change="applyFilters"
        />
        <t-button theme="default" variant="outline" @click="applyFilters">
          <t-icon name="search" />
          {{ t('knowledgeEditor.feedback.apply') }}
        </t-button>
        <t-button
          theme="danger"
          variant="outline"
          :disabled="selectedChunkIds.length === 0"
          @click="openResetConfirm"
        >
          <t-icon name="rollback" />
          {{ t('knowledgeEditor.feedback.resetSelected') }}
        </t-button>
        <span v-if="total > 0" class="chunk-feedback-total">
          {{ t('knowledgeEditor.feedback.totalCount', { count: total }) }}
        </span>
      </div>

      <div class="data-table-shell chunk-feedback-table-shell">
        <t-table
          row-key="chunk_id"
          :data="stats"
          :columns="columns"
          :selected-row-keys="selectedChunkIds"
          size="medium"
          hover
          :loading="loading"
          :pagination="null"
          @select-change="onSelectChange"
        >
          <template #content_preview="{ row }">
            <div class="chunk-feedback-content" :title="row.content_preview">
              {{ row.content_preview }}
            </div>
          </template>
          <template #approval_rate="{ row }">
            <span :class="['chunk-feedback-rate', approvalRateClass(row.approval_rate)]">
              {{ formatRate(row.approval_rate) }}
            </span>
          </template>
          <template #recall_weight="{ row }">
            <span class="chunk-feedback-weight">{{ row.recall_weight?.toFixed(2) ?? '1.00' }}</span>
          </template>
          <template #needs_optimization="{ row }">
            <t-tag v-if="row.needs_optimization" theme="danger" size="small" variant="light">
              {{ t('knowledgeEditor.feedback.statusNeedsOptimization') }}
            </t-tag>
            <t-tag v-else theme="success" size="small" variant="light">
              {{ t('knowledgeEditor.feedback.statusNormal') }}
            </t-tag>
          </template>
          <template #op="{ row }">
            <t-button theme="primary" variant="text" size="small" @click="openDetail(row)">
              {{ t('knowledgeEditor.feedback.detail') }}
            </t-button>
            <t-button theme="danger" variant="text" size="small" @click="openResetConfirm([row.chunk_id])">
              {{ t('knowledgeEditor.feedback.reset') }}
            </t-button>
          </template>
          <template #empty>
            <t-empty :description="t('knowledgeEditor.feedback.empty')" />
          </template>
        </t-table>
      </div>

      <div v-if="total > 0" class="data-table-shell__pager chunk-feedback-pager">
        <t-pagination
          v-model="page"
          v-model:page-size="pageSize"
          :total="total"
          size="small"
          show-jumper
          show-page-number
          show-page-size
          :page-size-options="PAGE_SIZE_OPTIONS"
          @change="onPageChange"
        />
      </div>

      <div class="chunk-feedback-config">
        <div class="chunk-feedback-config__header">
          <div>
            <h4 class="chunk-feedback-config__title">{{ t('knowledgeEditor.feedback.config.title') }}</h4>
            <p class="chunk-feedback-config__desc">{{ t('knowledgeEditor.feedback.config.description') }}</p>
          </div>
          <t-button theme="primary" size="small" :loading="configSaving" :disabled="!configLoaded" @click="saveConfig">
            {{ t('knowledgeEditor.feedback.config.save') }}
          </t-button>
        </div>
        <p class="chunk-feedback-config__hint">
          <t-icon name="info-circle" />
          {{ t('knowledgeEditor.feedback.config.tenantHint') }}
        </p>
        <div v-if="config" class="chunk-feedback-config__grid">
          <div v-for="field in configFields" :key="field.key" class="chunk-feedback-config__field">
            <label class="chunk-feedback-config__label">{{ field.label }}</label>
            <t-input-number
              v-model="config[field.key]"
              :min="field.min"
              :max="field.max"
              :step="field.step"
              :precision="field.precision"
              size="small"
            />
            <p class="chunk-feedback-config__tip">{{ field.tip }}</p>
          </div>
        </div>
      </div>

      <div class="chunk-feedback-logs">
        <div class="chunk-feedback-logs__header">
          <div>
            <h4 class="chunk-feedback-logs__title">{{ t('knowledgeEditor.feedback.logs.title') }}</h4>
            <p class="chunk-feedback-logs__desc">{{ t('knowledgeEditor.feedback.logs.description') }}</p>
          </div>
          <t-select
            v-model="logSource"
            class="chunk-feedback-logs__filter"
            :options="logSourceOptions"
            @change="loadLogs(true)"
          />
        </div>
        <div class="data-table-shell chunk-feedback-table-shell">
          <t-table
            row-key="id"
            :data="logs"
            :columns="logColumns"
            size="medium"
            hover
            :loading="logsLoading"
            :pagination="null"
          >
            <template #source="{ row }">
              <t-tag :theme="logSourceTheme(row.source)" size="small" variant="light-outline">
                {{ logSourceLabel(row.source) }}
              </t-tag>
            </template>
            <template #created_at="{ row }">
              <span class="chunk-feedback-log-time">{{ formatDateTime(row.created_at) }}</span>
            </template>
            <template #old_weight="{ row }">{{ Number(row.old_weight ?? 1).toFixed(2) }}</template>
            <template #new_weight="{ row }">
              <span :class="{ 'chunk-feedback-weight-up': Number(row.new_weight) > Number(row.old_weight ?? 1), 'chunk-feedback-weight-down': Number(row.new_weight) < Number(row.old_weight ?? 1) }">
                {{ Number(row.new_weight ?? 1).toFixed(2) }}
              </span>
            </template>
            <template #empty>
              <t-empty :description="t('knowledgeEditor.feedback.logs.empty')" />
            </template>
          </t-table>
        </div>
        <div v-if="logsTotal > 0" class="data-table-shell__pager chunk-feedback-pager">
          <t-pagination
            v-model="logPage"
            v-model:page-size="logPageSize"
            :total="logsTotal"
            size="small"
            show-jumper
            show-page-number
            show-page-size
            :page-size-options="PAGE_SIZE_OPTIONS"
            @change="onLogPageChange"
          />
        </div>
      </div>
    </div>

    <SettingDrawer
      v-model:visible="detailVisible"
      class="chunk-feedback-detail-drawer"
      :title="detailTitle"
      :description="detailDescription"
      icon="thumb-up"
      width="640px"
      :min-width="480"
      :max-width="960"
      storage-key="setting-drawer:width:kb-chunk-feedback-detail"
      hide-footer
    >
      <template v-if="selected">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('knowledgeEditor.feedback.drawer.summary') }}</h4>
          <dl class="chunk-feedback-detail-fields">
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.content') }}</dt>
              <dd class="chunk-feedback-detail-content" :title="selected.content_preview">{{ selected.content_preview }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.likeCount') }}</dt>
              <dd>{{ selected.like_count }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.dislikeCount') }}</dt>
              <dd>{{ selected.dislike_count }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.approvalRate') }}</dt>
              <dd>{{ formatRate(selected.approval_rate) }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.recallWeight') }}</dt>
              <dd>{{ (selected.recall_weight ?? 1).toFixed(2) }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.sessionCount') }}</dt>
              <dd>{{ selected.session_count }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.feedbackCount') }}</dt>
              <dd>{{ selected.feedback_count }}</dd>
            </div>
            <div class="chunk-feedback-detail-field">
              <dt>{{ t('knowledgeEditor.feedback.columns.status') }}</dt>
              <dd>{{ selected.needs_optimization ? t('knowledgeEditor.feedback.statusNeedsOptimization') : t('knowledgeEditor.feedback.statusNormal') }}</dd>
            </div>
          </dl>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('knowledgeEditor.feedback.drawer.dislikeReasons') }}</h4>
          <div v-if="detail && detail.dislike_reasons && detail.dislike_reasons.length" class="chunk-feedback-reasons">
            <div v-for="item in detail.dislike_reasons" :key="item.reason" class="chunk-feedback-reason">
              <span class="chunk-feedback-reason__label">{{ reasonLabel(item.reason) }}</span>
              <span class="chunk-feedback-reason__count">{{ item.count }}</span>
            </div>
          </div>
          <p v-else class="chunk-feedback-empty-hint">{{ t('knowledgeEditor.feedback.drawer.noReasons') }}</p>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('knowledgeEditor.feedback.drawer.relatedSessions') }}</h4>
          <div v-if="detail && detail.related_sessions && detail.related_sessions.length" class="chunk-feedback-sessions">
            <div v-for="session in detail.related_sessions" :key="session.session_id" class="chunk-feedback-session">
              <span class="chunk-feedback-session__title" :title="session.title">{{ session.title || session.session_id }}</span>
              <span class="chunk-feedback-session__meta">
                {{ t('knowledgeEditor.feedback.drawer.sessionMessages', { count: session.message_count }) }}
                <template v-if="session.last_active_at">
                  · {{ formatDateTime(session.last_active_at) }}
                </template>
              </span>
            </div>
          </div>
          <p v-else class="chunk-feedback-empty-hint">{{ t('knowledgeEditor.feedback.drawer.noSessions') }}</p>
        </section>
      </template>
    </SettingDrawer>

    <t-dialog
      v-model:visible="resetVisible"
      :header="t('knowledgeEditor.feedback.resetConfirmTitle')"
      :body="resetBody"
      :confirm-btn="t('knowledgeEditor.feedback.resetConfirm')"
      :cancel-btn="t('common.cancel')"
      :confirm-loading="resetting"
      @confirm="confirmReset"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import SettingDrawer from '@/components/settings/SettingDrawer.vue';
import {
  getChunkFeedbackStats,
  getChunkFeedbackDetail,
  getChunkWeightLogs,
  resetChunkFeedback,
  getChunkFeedbackConfig,
  updateChunkFeedbackConfig,
  type ChunkFeedbackStat,
  type ChunkFeedbackDetail,
  type ChunkWeightLog,
  type ChunkFeedbackConfig,
  type FeedbackStatsQuery,
} from '@/api/chat/feedback';

const props = defineProps<{
  kbId: string;
  active?: boolean;
}>();

const { t, te } = useI18n();

const PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

const stats = ref<ChunkFeedbackStat[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(20);
const loading = ref(false);
const error = ref('');
const loadedOnce = ref(false);

const keyword = ref('');
const approvalFilter = ref('all');
const sortBy = ref('approval_rate');

const selectedChunkIds = ref<string[]>([]);

const detailVisible = ref(false);
const selected = ref<ChunkFeedbackStat | null>(null);
const detail = ref<ChunkFeedbackDetail | null>(null);
const detailLoading = ref(false);

const resetVisible = ref(false);
const resetting = ref(false);
const resetChunkIds = ref<string[]>([]);

const config = ref<ChunkFeedbackConfig | null>(null);
const configLoaded = ref(false);
const configSaving = ref(false);

const logs = ref<ChunkWeightLog[]>([]);
const logsTotal = ref(0);
const logPage = ref(1);
const logPageSize = ref(20);
const logsLoading = ref(false);
const logSource = ref('');

const approvalOptions = computed(() => [
  { label: t('knowledgeEditor.feedback.approvalAll'), value: 'all' },
  { label: t('knowledgeEditor.feedback.approvalHigh'), value: 'high' },
  { label: t('knowledgeEditor.feedback.approvalNormal'), value: 'normal' },
  { label: t('knowledgeEditor.feedback.approvalLow'), value: 'low' },
  { label: t('knowledgeEditor.feedback.approvalNeedsOptimization'), value: 'needs_optimization' },
]);

const sortOptions = computed(() => [
  { label: t('knowledgeEditor.feedback.sortApprovalRate'), value: 'approval_rate' },
  { label: t('knowledgeEditor.feedback.sortLikeCount'), value: 'like_count' },
  { label: t('knowledgeEditor.feedback.sortDislikeCount'), value: 'dislike_count' },
  { label: t('knowledgeEditor.feedback.sortRecallWeight'), value: 'recall_weight' },
  { label: t('knowledgeEditor.feedback.sortSessionCount'), value: 'session_count' },
  { label: t('knowledgeEditor.feedback.sortFeedbackCount'), value: 'feedback_count' },
]);

const columns = computed(() => [
  { colKey: 'content_preview', title: t('knowledgeEditor.feedback.columns.content'), minWidth: 220, ellipsis: true },
  { colKey: 'like_count', title: t('knowledgeEditor.feedback.columns.likeCount'), width: 80, align: 'center' as const },
  { colKey: 'dislike_count', title: t('knowledgeEditor.feedback.columns.dislikeCount'), width: 80, align: 'center' as const },
  { colKey: 'approval_rate', title: t('knowledgeEditor.feedback.columns.approvalRate'), width: 110, align: 'center' as const },
  { colKey: 'recall_weight', title: t('knowledgeEditor.feedback.columns.recallWeight'), width: 100, align: 'center' as const },
  { colKey: 'session_count', title: t('knowledgeEditor.feedback.columns.sessionCount'), width: 90, align: 'center' as const },
  { colKey: 'feedback_count', title: t('knowledgeEditor.feedback.columns.feedbackCount'), width: 90, align: 'center' as const },
  { colKey: 'needs_optimization', title: t('knowledgeEditor.feedback.columns.status'), width: 110, align: 'center' as const },
  { colKey: 'op', title: t('knowledgeEditor.feedback.columns.actions'), width: 140, align: 'center' as const },
]);

const configFields = computed(() => [
  {
    key: 'boost_threshold' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.boostThreshold'),
    tip: t('knowledgeEditor.feedback.config.boostThresholdTip'),
    min: 0.01, max: 1, step: 0.05, precision: 2,
  },
  {
    key: 'degrade_threshold' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.degradeThreshold'),
    tip: t('knowledgeEditor.feedback.config.degradeThresholdTip'),
    min: 0.01, max: 0.99, step: 0.05, precision: 2,
  },
  {
    key: 'optimize_threshold' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.optimizeThreshold'),
    tip: t('knowledgeEditor.feedback.config.optimizeThresholdTip'),
    min: 0.01, max: 0.99, step: 0.05, precision: 2,
  },
  {
    key: 'min_votes' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.minVotes'),
    tip: t('knowledgeEditor.feedback.config.minVotesTip'),
    min: 0, max: 10000, step: 1, precision: 0,
  },
  {
    key: 'weight_step' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.weightStep'),
    tip: t('knowledgeEditor.feedback.config.weightStepTip'),
    min: 0.01, max: 1, step: 0.05, precision: 2,
  },
  {
    key: 'max_weight' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.maxWeight'),
    tip: t('knowledgeEditor.feedback.config.maxWeightTip'),
    min: 0.1, max: 10, step: 0.1, precision: 2,
  },
  {
    key: 'min_weight' as keyof ChunkFeedbackConfig,
    label: t('knowledgeEditor.feedback.config.minWeight'),
    tip: t('knowledgeEditor.feedback.config.minWeightTip'),
    min: 0.01, max: 1, step: 0.05, precision: 2,
  },
]);

const logColumns = computed(() => [
  { colKey: 'created_at', title: t('knowledgeEditor.feedback.logs.columns.time'), width: 150 },
  { colKey: 'chunk_id', title: t('knowledgeEditor.feedback.logs.columns.chunk'), minWidth: 160, ellipsis: true },
  { colKey: 'old_weight', title: t('knowledgeEditor.feedback.logs.columns.oldWeight'), width: 90, align: 'center' as const },
  { colKey: 'new_weight', title: t('knowledgeEditor.feedback.logs.columns.newWeight'), width: 90, align: 'center' as const },
  { colKey: 'source', title: t('knowledgeEditor.feedback.logs.columns.source'), width: 130, align: 'center' as const },
  { colKey: 'user_id', title: t('knowledgeEditor.feedback.logs.columns.operator'), width: 110, ellipsis: true },
  { colKey: 'reason', title: t('knowledgeEditor.feedback.logs.columns.reason'), minWidth: 180, ellipsis: true },
]);

const logSourceOptions = computed(() => [
  { label: t('knowledgeEditor.feedback.logs.sourceAll'), value: '' },
  { label: t('knowledgeEditor.feedback.logs.sourceFeedback'), value: 'feedback' },
  { label: t('knowledgeEditor.feedback.logs.sourceManualReset'), value: 'manual_reset' },
  { label: t('knowledgeEditor.feedback.logs.sourceManualAdjust'), value: 'manual_adjust' },
]);

const detailTitle = computed(() =>
  selected.value
    ? `${t('knowledgeEditor.feedback.drawer.title')} · ${selected.value.chunk_id.slice(0, 8)}`
    : t('knowledgeEditor.feedback.drawer.title'),
);

const detailDescription = computed(() => {
  if (!selected.value) return '';
  const parts: string[] = [];
  if (selected.value.knowledge_title) parts.push(selected.value.knowledge_title);
  if (selected.value.knowledge_filename) parts.push(selected.value.knowledge_filename);
  if (selected.value.chunk_index !== undefined && selected.value.chunk_index !== null) {
    parts.push(`#${selected.value.chunk_index}`);
  }
  return parts.join(' · ');
});

const resetBody = computed(() => {
  const count = resetChunkIds.value.length;
  return t('knowledgeEditor.feedback.resetConfirmBody', { count });
});

function buildQuery(): FeedbackStatsQuery {
  const query: FeedbackStatsQuery = {
    knowledge_base_id: props.kbId,
    page,
    page_size: pageSize,
    sort_by: sortBy.value || undefined,
    sort_order: sortBy.value === 'approval_rate' ? 'desc' : 'desc',
  };
  const kw = keyword.value.trim();
  if (kw) query.keyword = kw;
  if (approvalFilter.value === 'high') query.min_approval_rate = 0.8;
  else if (approvalFilter.value === 'normal') {
    query.min_approval_rate = 0.5;
    query.max_approval_rate = 0.8;
  } else if (approvalFilter.value === 'low') query.max_approval_rate = 0.5;
  else if (approvalFilter.value === 'needs_optimization') query.needs_optimization = true;
  return query;
}

async function loadStats(reset = false) {
  if (!props.kbId || loading.value) return;
  loading.value = true;
  error.value = '';
  if (reset) {
    page.value = 1;
    selectedChunkIds.value = [];
  }
  try {
    const res = await getChunkFeedbackStats(buildQuery());
    stats.value = res?.data || [];
    total.value = Number(res?.total || 0);
    loadedOnce.value = true;
  } catch (err: any) {
    error.value = err?.message || t('knowledgeEditor.feedback.loadFailed');
  } finally {
    loading.value = false;
  }
}

function reload() {
  void loadStats(true);
  void loadLogs(true);
  void loadConfig();
}

function applyFilters() {
  void loadStats(true);
}

function onPageChange() {
  void loadStats(false);
}

function onSelectChange(value: (string | number)[]) {
  selectedChunkIds.value = (value || []).map(String);
}

function openDetail(row: ChunkFeedbackStat) {
  selected.value = row;
  detail.value = null;
  detailVisible.value = true;
  void loadDetail(row.chunk_id);
}

async function loadDetail(chunkId: string) {
  if (!chunkId) return;
  detailLoading.value = true;
  try {
    const res = await getChunkFeedbackDetail(chunkId);
    detail.value = res?.data || null;
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('knowledgeEditor.feedback.loadFailed'));
  } finally {
    detailLoading.value = false;
  }
}

function openResetConfirm(chunkIds?: string[]) {
  resetChunkIds.value = chunkIds?.length ? chunkIds : [...selectedChunkIds.value];
  if (resetChunkIds.value.length === 0) return;
  resetVisible.value = true;
}

async function confirmReset() {
  if (resetting.value || resetChunkIds.value.length === 0) return;
  resetting.value = true;
  try {
    await resetChunkFeedback(resetChunkIds.value);
    MessagePlugin.success(t('knowledgeEditor.feedback.resetSuccess'));
    resetVisible.value = false;
    await loadStats(true);
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('knowledgeEditor.feedback.resetFailed'));
  } finally {
    resetting.value = false;
  }
}

async function loadConfig() {
  if (configLoaded.value) return;
  try {
    const res = await getChunkFeedbackConfig();
    config.value = res?.data || null;
    configLoaded.value = true;
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('knowledgeEditor.feedback.config.loadFailed'));
  }
}

async function saveConfig() {
  if (!config.value || configSaving.value) return;
  configSaving.value = true;
  try {
    await updateChunkFeedbackConfig({ ...config.value });
    MessagePlugin.success(t('knowledgeEditor.feedback.config.saved'));
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('knowledgeEditor.feedback.config.saveFailed'));
  } finally {
    configSaving.value = false;
  }
}

async function loadLogs(reset = false) {
  if (!props.kbId || logsLoading.value) return;
  logsLoading.value = true;
  if (reset) logPage.value = 1;
  try {
    const res = await getChunkWeightLogs({
      source: logSource.value || undefined,
      page: logPage.value,
      page_size: logPageSize.value,
    });
    logs.value = res?.data || [];
    logsTotal.value = Number(res?.total || 0);
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('knowledgeEditor.feedback.logs.loadFailed'));
  } finally {
    logsLoading.value = false;
  }
}

function onLogPageChange() {
  void loadLogs(false);
}

function formatRate(value?: number): string {
  if (value === undefined || value === null) return '—';
  return `${(value * 100).toFixed(1)}%`;
}

function approvalRateClass(value?: number): string {
  if (value === undefined || value === null) return '';
  if (value >= 0.8) return 'chunk-feedback-rate--high';
  if (value < 0.5) return 'chunk-feedback-rate--low';
  return 'chunk-feedback-rate--normal';
}

function reasonLabel(reason: string): string {
  const key = `chat.feedback.reason${reason
    .split('_')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join('')}`;
  return te(key) ? t(key) : reason;
}

function logSourceLabel(source: string): string {
  const key = `knowledgeEditor.feedback.logs.source${source
    .split('_')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join('')}`;
  return te(key) ? t(key) : source;
}

function logSourceTheme(source: string): 'primary' | 'success' | 'warning' | 'default' {
  if (source === 'feedback') return 'primary';
  if (source === 'manual_reset') return 'warning';
  if (source === 'manual_adjust') return 'success';
  return 'default';
}

function formatDateTime(value?: string): string {
  if (!value) return '—';
  try {
    return new Date(value).toLocaleString();
  } catch {
    return value;
  }
}

watch(
  () => props.active,
  (active) => {
    if (active && !loadedOnce.value) reload();
  },
  { immediate: true },
);

watch(
  () => props.kbId,
  () => {
    loadedOnce.value = false;
    stats.value = [];
    total.value = 0;
    if (props.active) reload();
  },
);
</script>

<style scoped lang="less">
.section-content {
  width: 100%;

  .section-header {
    margin-bottom: 16px;
  }

  .section-title {
    margin: 0;
    font-family: var(--app-font-family);
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .section-desc {
    margin: 0;
    font-family: var(--app-font-family);
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    line-height: 22px;
  }
}

.chunk-feedback-error {
  margin-bottom: 12px;
}

.chunk-feedback-filter-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 12px;

  .chunk-feedback-filter {
    width: 180px;
  }

  .chunk-feedback-filter--keyword {
    width: 220px;
  }

  .chunk-feedback-filter--sort {
    width: 160px;
  }
}

.chunk-feedback-total {
  margin-left: auto;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
}

.data-table-shell.chunk-feedback-table-shell {
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background-color: var(--td-bg-color-container);

  &:deep(thead th) {
    height: 40px;
    color: var(--td-text-color-secondary);
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.01em;
    white-space: nowrap;
    background-color: var(--td-bg-color-secondarycontainer) !important;
  }

  &:deep(.t-table td) {
    height: 52px;
    padding-top: 8px;
    padding-bottom: 8px;
    font-size: 13px;
  }

  &:deep(th.t-align-center),
  &:deep(td.t-align-center) {
    text-align: center;
  }

  &:deep(.t-table__body tr:last-child td) {
    border-bottom: 0;
  }
}

.chunk-feedback-content {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
  font-size: 13px;
}

.chunk-feedback-rate {
  font-variant-numeric: tabular-nums;
  font-weight: 500;

  &--high {
    color: var(--td-success-color);
  }

  &--normal {
    color: var(--td-warning-color);
  }

  &--low {
    color: var(--td-error-color);
  }
}

.chunk-feedback-weight {
  font-variant-numeric: tabular-nums;
}

.chunk-feedback-pager {
  display: flex;
  justify-content: flex-end;
  margin: 12px 0 0;
}

.chunk-feedback-config,
.chunk-feedback-logs {
  margin-top: 20px;
  padding: 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
}

.chunk-feedback-config__header,
.chunk-feedback-logs__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.chunk-feedback-config__title,
.chunk-feedback-logs__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.chunk-feedback-config__desc,
.chunk-feedback-logs__desc {
  margin: 4px 0 0;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
  line-height: 20px;
}

.chunk-feedback-config__hint {
  display: flex;
  align-items: center;
  gap: 4px;
  margin: 10px 0 0;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--td-brand-color-light, rgba(0, 82, 217, 0.08));
  color: var(--td-text-color-secondary);
  font-size: 12px;
  line-height: 18px;
}

.chunk-feedback-config__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 14px;
  margin-top: 14px;
}

.chunk-feedback-config__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.chunk-feedback-config__label {
  font-size: 13px;
  color: var(--td-text-color-primary);
}

.chunk-feedback-config__tip {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 16px;
}

.chunk-feedback-logs__filter {
  width: 170px;
}

.chunk-feedback-logs .data-table-shell {
  margin-top: 12px;
}

.chunk-feedback-log-time {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}

.chunk-feedback-weight-up {
  color: var(--td-success-color);
  font-weight: 500;
}

.chunk-feedback-weight-down {
  color: var(--td-error-color);
  font-weight: 500;
}

.chunk-feedback-detail-fields {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin: 0;
}

.chunk-feedback-detail-field {
  display: flex;
  align-items: flex-start;
  gap: 12px;

  dt {
    flex: 0 0 120px;
    color: var(--td-text-color-placeholder);
    font-size: 13px;
  }

  dd {
    flex: 1;
    margin: 0;
    color: var(--td-text-color-primary);
    font-size: 13px;
    word-break: break-all;
  }
}

.chunk-feedback-detail-content {
  max-height: 120px;
  overflow-y: auto;
  white-space: pre-wrap;
}

.chunk-feedback-reasons {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.chunk-feedback-reason {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);

  &__label {
    font-size: 13px;
    color: var(--td-text-color-primary);
  }

  &__count {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-error-color);
  }
}

.chunk-feedback-sessions {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.chunk-feedback-session {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);

  &__title {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__meta {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }
}

.chunk-feedback-empty-hint {
  margin: 0;
  padding: 8px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}
</style>
