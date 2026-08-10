<template>
  <t-dialog v-model:visible="localVisible" header="Quản lý kệ sách" :footer="false" width="640px">
    <div class="csm">
      <div class="csm-toolbar">
        <t-button theme="primary" size="small" @click="openShelfDialog()">
          <template #icon><t-icon name="add" /></template>Tạo kệ
        </t-button>
      </div>
      <div v-if="shelves.length === 0" class="csm-empty">Chưa có kệ nào.</div>
      <div v-for="s in shelves" :key="s.id" class="csm-row">
        <div class="csm-row-main">
          <div class="csm-row-title">{{ s.title }}</div>
          <div class="csm-row-meta">
            <span v-if="s.kind" class="csm-tag">{{ shelfKindLabel(s.kind) }}</span>
            <span class="csm-count">{{ s.book_count || 0 }} sách</span>
          </div>
        </div>
        <div class="csm-row-actions">
          <t-button size="small" variant="text" title="Gán sách vào kệ" @click="openAssignDialog(s)"><t-icon name="link" /></t-button>
          <t-button size="small" variant="text" @click="openShelfDialog(s)"><t-icon name="edit" /></t-button>
          <t-button size="small" variant="text" theme="danger" @click="removeShelf(s)"><t-icon name="delete" /></t-button>
        </div>
      </div>
    </div>

    <t-dialog v-model:visible="shelfDialog.visible" :header="shelfDialog.id ? 'Sửa kệ' : 'Tạo kệ'" :on-confirm="saveShelf" width="480px">
      <div class="csm-form">
        <label>Tên kệ *</label>
        <t-input v-model="shelfDialog.title" placeholder="VD: Tàn cuộc, Bộ STEP Trainer" />
        <label>Loại kệ</label>
        <t-select v-model="shelfDialog.kind" :options="shelfKindOptions" clearable />
        <label>Mô tả</label>
        <t-textarea v-model="shelfDialog.description" :autosize="{ minRows: 2 }" />
        <label>Thứ tự</label>
        <t-input-number v-model="shelfDialog.sort_order" />
      </div>
    </t-dialog>

    <t-dialog v-model:visible="assignDialog.visible" :header="`Gán sách vào kệ「${assignDialog.title}」`"
      :on-confirm="saveAssign" width="520px">
      <div class="csm-assign">
        <t-input v-model="assignDialog.search" placeholder="Tìm sách…" clearable style="margin-bottom:8px" />
        <div class="csm-assign-list">
          <t-checkbox v-for="b in filteredBooks" :key="b.id" v-model="assignDialog.selected[b.id]">
            {{ b.title }}
          </t-checkbox>
        </div>
      </div>
    </t-dialog>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import {
  listShelves, createShelf, updateShelf, deleteShelf, setShelfBooks,
  listBooks, type ChessShelf, type ChessBook,
} from '@/api/chess';
import { shelfKindOptions, shelfKindLabel } from '@/utils/chessBookOptions';

// Quản lý kệ (CRUD) + gán sách vào kệ (nhiều-nhiều). Gán sách thực hiện TỪ PHÍA
// KỆ (chọn nhiều sách cho 1 kệ, gọi setShelfBooks 1 lần) — đơn giản hơn nhiều so
// với gán từ phía sách (phải diff nhiều kệ).
const props = defineProps<{ visible: boolean }>();
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void; (e: 'changed'): void }>();

const localVisible = computed({
  get: () => props.visible,
  set: (v: boolean) => emit('update:visible', v),
});

const shelves = ref<ChessShelf[]>([]);

async function load() {
  try {
    const res: any = await listShelves();
    shelves.value = res?.data || [];
  } catch {
    MessagePlugin.error('Tải danh sách kệ thất bại');
  }
}
watch(() => props.visible, (v) => { if (v) load(); });

// ---- Dialog tạo/sửa kệ ----
interface ShelfDialogState {
  visible: boolean; id: string; title: string; kind: string; description: string;
  cover_url: string; sort_order: number;
}
const shelfDialog = reactive<ShelfDialogState>({
  visible: false, id: '', title: '', kind: '', description: '', cover_url: '', sort_order: 0,
});
function openShelfDialog(s?: ChessShelf) {
  shelfDialog.visible = true;
  shelfDialog.id = s?.id || '';
  shelfDialog.title = s?.title || '';
  shelfDialog.kind = s?.kind || '';
  shelfDialog.description = s?.description || '';
  shelfDialog.cover_url = s?.cover_url || '';
  shelfDialog.sort_order = s?.sort_order || 0;
}
async function saveShelf() {
  if (!shelfDialog.title.trim()) { MessagePlugin.warning('Nhập tên kệ'); return; }
  const payload = {
    title: shelfDialog.title, kind: shelfDialog.kind, description: shelfDialog.description,
    cover_url: shelfDialog.cover_url, sort_order: shelfDialog.sort_order,
  };
  try {
    if (shelfDialog.id) await updateShelf(shelfDialog.id, payload);
    else await createShelf(payload);
    shelfDialog.visible = false;
    await load();
    emit('changed');
    MessagePlugin.success('Đã lưu kệ');
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Lưu thất bại');
  }
}
function removeShelf(s: ChessShelf) {
  DialogPlugin.confirm({
    header: 'Xóa kệ', body: `Xóa kệ "${s.title}"? Sách trên kệ KHÔNG bị xóa, chỉ gỡ khỏi kệ này.`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteShelf(s.id);
        await load();
        emit('changed');
        MessagePlugin.success('Đã xóa kệ');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}

// ---- Dialog gán sách ----
interface AssignDialogState {
  visible: boolean; shelfId: string; title: string; search: string; selected: Record<string, boolean>;
}
const assignDialog = reactive<AssignDialogState>({ visible: false, shelfId: '', title: '', search: '', selected: {} });
const allBooks = ref<ChessBook[]>([]);

async function openAssignDialog(s: ChessShelf) {
  assignDialog.visible = true;
  assignDialog.shelfId = s.id;
  assignDialog.title = s.title;
  assignDialog.search = '';
  assignDialog.selected = {};
  try {
    const [all, onShelf]: any[] = await Promise.all([listBooks(), listBooks({ shelf_id: s.id })]);
    allBooks.value = all?.data || [];
    const onShelfIds = new Set((onShelf?.data || []).map((b: ChessBook) => b.id));
    const sel: Record<string, boolean> = {};
    for (const b of allBooks.value) sel[b.id] = onShelfIds.has(b.id);
    assignDialog.selected = sel;
  } catch {
    MessagePlugin.error('Tải danh sách sách thất bại');
  }
}
const filteredBooks = computed(() => {
  const q = assignDialog.search.trim().toLowerCase();
  if (!q) return allBooks.value;
  return allBooks.value.filter((b) => (b.title || '').toLowerCase().includes(q));
});
async function saveAssign() {
  const bookIds = Object.entries(assignDialog.selected).filter(([, v]) => v).map(([id]) => id);
  try {
    await setShelfBooks(assignDialog.shelfId, bookIds);
    assignDialog.visible = false;
    await load();
    emit('changed');
    MessagePlugin.success(`Đã gán ${bookIds.length} sách vào kệ`);
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Gán sách thất bại');
  }
}
</script>

<style scoped lang="less">
.csm-toolbar { display: flex; justify-content: flex-end; margin-bottom: 10px; }
.csm-empty { color: var(--td-text-color-placeholder); font-size: 14px; padding: 16px 4px; }
.csm-row {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px; border-radius: 8px; margin-bottom: 6px; border: 1px solid var(--td-component-stroke);
  &:hover { background: var(--td-bg-color-container-hover); }
}
.csm-row-title { font-weight: 600; color: var(--td-text-color-primary); }
.csm-row-meta { display: flex; gap: 8px; align-items: center; margin-top: 4px; font-size: 12px; }
.csm-tag { background: var(--td-brand-color-light); color: var(--td-brand-color); padding: 0 6px; border-radius: 4px; }
.csm-count { color: var(--td-text-color-secondary); }
.csm-row-actions { display: flex; }
.csm-form { display: flex; flex-direction: column; gap: 6px; label { font-size: 13px; color: var(--td-text-color-secondary); margin-top: 6px; } }
.csm-assign-list { max-height: 320px; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
</style>
