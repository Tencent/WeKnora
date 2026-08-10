<template>
  <t-dialog v-model:visible="localVisible" header="Lịch sử phiên bản chương" :footer="false" width="820px">
    <div class="cch">
      <div class="cch-list">
        <div v-if="loading" class="cch-empty">Đang tải…</div>
        <div v-else-if="revisions.length === 0" class="cch-empty">Chưa có bản cũ nào (chỉ lưu khi sửa tiêu đề/nội dung).</div>
        <div v-for="r in revisions" :key="r.id" class="cch-row" :class="{ active: selected && selected.id === r.id }"
          @click="select(r)">
          <div class="cch-row-title">Bản #{{ r.revision_number }}</div>
          <div class="cch-row-meta">{{ formatDate(r.created_at) }}<span v-if="r.created_by"> · {{ r.created_by }}</span></div>
          <div v-if="r.summary" class="cch-row-summary">{{ r.summary }}</div>
        </div>
      </div>
      <div class="cch-preview">
        <template v-if="selected">
          <div class="cch-preview-title">{{ selected.title }}</div>
          <div class="cch-preview-body" v-html="renderedContent"></div>
          <div class="cch-preview-actions">
            <t-button theme="primary" @click="confirmRestore">Khôi phục bản này</t-button>
          </div>
        </template>
        <div v-else class="cch-empty">Chọn một bản ở bên trái để xem nội dung.</div>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { marked } from 'marked';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import { listChapterRevisions, restoreChapterRevision, type ChessChapterRevision, type ChessBookChapter } from '@/api/chess';
import { safeMarkdownToHTML, sanitizeHTML } from '@/utils/security';

// Dialog lịch sử phiên bản chương: xem bản cũ + khôi phục. Mỗi lần khôi phục
// CŨNG tạo thêm một phiên bản mới ở backend (lưu bản đang có trước khi ghi đè).
const props = defineProps<{ visible: boolean; chapterId: string }>();
const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void;
  (e: 'restored', chapter: ChessBookChapter): void;
}>();

const localVisible = computed({
  get: () => props.visible,
  set: (v: boolean) => emit('update:visible', v),
});

const loading = ref(false);
const revisions = ref<ChessChapterRevision[]>([]);
const selected = ref<ChessChapterRevision | null>(null);

const renderedContent = computed(() => {
  if (!selected.value) return '';
  return sanitizeHTML(marked.parse(safeMarkdownToHTML(selected.value.content || ''), { breaks: true, async: false }) as string);
});

function formatDate(iso: string): string {
  if (!iso) return '';
  try { return new Date(iso).toLocaleString('vi-VN'); } catch { return iso; }
}

function select(r: ChessChapterRevision) { selected.value = r; }

async function load() {
  if (!props.chapterId) return;
  loading.value = true;
  selected.value = null;
  try {
    const res: any = await listChapterRevisions(props.chapterId);
    revisions.value = res?.data || [];
  } catch {
    revisions.value = [];
    MessagePlugin.error('Tải lịch sử phiên bản thất bại');
  } finally {
    loading.value = false;
  }
}

function confirmRestore() {
  if (!selected.value) return;
  const rev = selected.value;
  DialogPlugin.confirm({
    header: 'Khôi phục phiên bản',
    body: `Khôi phục về bản #${rev.revision_number}? Nội dung hiện tại sẽ được lưu lại thành một bản mới trước khi ghi đè.`,
    theme: 'warning',
    confirmBtn: { content: 'Khôi phục', theme: 'primary' },
    onConfirm: async () => {
      try {
        const res: any = await restoreChapterRevision(props.chapterId, rev.id);
        MessagePlugin.success('Đã khôi phục');
        emit('restored', res?.data);
        localVisible.value = false;
      } catch (e: any) {
        MessagePlugin.error(e?.error || e?.message || 'Khôi phục thất bại');
      }
    },
  });
}

watch(() => props.visible, (v) => { if (v) load(); });
</script>

<style scoped lang="less">
.cch { display: flex; gap: 12px; height: 60vh; }
.cch-list { width: 260px; flex: 0 0 260px; overflow-y: auto; border-right: 1px solid var(--td-component-stroke); padding-right: 10px; }
.cch-row {
  padding: 8px 10px; border-radius: 6px; cursor: pointer; margin-bottom: 6px;
  border: 1px solid var(--td-component-stroke);
  &:hover { background: var(--td-bg-color-container-hover); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); }
}
.cch-row-title { font-weight: 600; color: var(--td-text-color-primary); }
.cch-row-meta { font-size: 12px; color: var(--td-text-color-secondary); margin-top: 2px; }
.cch-row-summary { font-size: 12px; color: var(--td-text-color-placeholder); margin-top: 2px; }
.cch-preview { flex: 1; min-width: 0; overflow-y: auto; }
.cch-preview-title { font-size: 16px; font-weight: 600; margin-bottom: 8px; color: var(--td-text-color-primary); }
.cch-preview-body { line-height: 1.6; color: var(--td-text-color-primary); }
.cch-preview-actions { margin-top: 16px; }
.cch-empty { color: var(--td-text-color-placeholder); font-size: 14px; padding: 16px 4px; }
</style>
