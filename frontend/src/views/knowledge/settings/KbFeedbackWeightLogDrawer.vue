<template>
  <t-drawer
    v-model:visible="visible"
    :header="title"
    :size="600"
    :footer="false"
    destroy-on-close
    @close="onClose"
  >
    <t-table
      :data="rows"
      :columns="columns"
      :pagination="pagination"
      :loading="loading"
      row-key="id"
      size="small"
      :hover="true"
      @page-change="onPageChange"
    >
      <template #created_at="{ row }">
        <span class="kb-feedback-weight-log-drawer__date">{{ formatDate(row.created_at) }}</span>
      </template>
    </t-table>
  </t-drawer>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import { useI18n } from 'vue-i18n';
import { getChunkWeightLogs } from '@/api/chat';

const props = defineProps({
  kbId: { type: String, required: true },
  chunkId: { type: String, default: '' },
  visible: { type: Boolean, default: false },
});
const emit = defineEmits(['update:visible']);
const { t } = useI18n();

const visible = computed({
  get: () => props.visible,
  set: (v) => emit('update:visible', v),
});

const title = computed(() =>
  props.chunkId
    ? `${t('feedback.kbStats.weightLogTitle')} · ${props.chunkId.slice(0, 8)}`
    : t('feedback.kbStats.weightLogTitle'),
);

const rows = ref([]);
const loading = ref(false);

const pagination = reactive({
  current: 1,
  pageSize: 20,
  total: 0,
});

const columns = [
  { colKey: 'chunk_id', title: 'Chunk', ellipsis: true, minWidth: 220 },
  { colKey: 'old_weight', title: 'Old', width: 80 },
  { colKey: 'new_weight', title: 'New', width: 80 },
  { colKey: 'reason', title: 'Reason', minWidth: 160 },
  { colKey: 'created_at', title: 'When', width: 180 },
];

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
    const resp = await getChunkWeightLogs({
      kb_id: props.kbId,
      chunk_id: props.chunkId,
      page: pagination.current,
      page_size: pagination.pageSize,
    });
    const page = resp?.data || {};
    rows.value = page.data || [];
    pagination.total = page.total || 0;
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

function onClose() {
  emit('update:visible', false);
}

watch(
  () => [props.visible, props.kbId, props.chunkId],
  ([v]) => {
    if (v) {
      pagination.current = 1;
      reload();
    }
  },
);
</script>

<style scoped>
.kb-feedback-weight-log-drawer__date {
  color: var(--td-text-color-secondary, #666);
}
</style>