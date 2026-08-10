<template>
  <div class="pob">
    <div class="pob-toolbar">
      <t-select v-model="filter.category" :options="positionCategoryOptions" placeholder="Phân loại" clearable
        style="width:170px" @change="load" />
      <t-select v-model="filter.level" :options="positionLevelOptions" placeholder="Cấp độ" clearable
        style="width:120px" @change="load" />
      <t-input v-model="filter.eco" placeholder="ECO" clearable style="width:90px" @change="load" />
      <t-input v-model="filter.q" placeholder="Tìm theo tên/thẻ…" clearable style="width:180px" @change="load" />
      <div style="flex:1"></div>
      <t-button variant="outline" size="small" @click="doExport">
        <template #icon><t-icon name="download" /></template>Export
      </t-button>
      <t-button variant="outline" size="small" @click="doImport">
        <template #icon><t-icon name="upload" /></template>Import
      </t-button>
      <t-button theme="primary" size="small" @click="openDialog()">
        <template #icon><t-icon name="add" /></template>Tạo thế cờ
      </t-button>
    </div>

    <div class="pob-body">
      <div class="pob-list">
        <div v-if="positions.length === 0" class="pob-empty">Chưa có thế cờ. Nhấn "Tạo thế cờ".</div>
        <div v-for="p in positions" :key="p.id" class="pob-row" :class="{ active: selected && selected.id === p.id }"
          @click="select(p)">
          <div class="pob-row-main">
            <div class="pob-title">{{ p.title || '(không tiêu đề)' }}</div>
            <div class="pob-meta">
              <span v-if="p.category" class="pob-tag">{{ positionCategoryLabel(p.category) }}</span>
              <span v-if="p.level" class="pob-tag pob-tag--level">{{ positionLevelLabel(p.level) }}</span>
              <span v-if="p.eco" class="pob-tag pob-tag--eco">{{ p.eco }}</span>
            </div>
          </div>
          <span class="pob-actions">
            <t-button size="small" variant="text" :title="t('chess.ref.copyLink')" @click.stop="copyWikilink(p)">
              <t-icon name="link" />
            </t-button>
            <t-button size="small" variant="text" title="Đổi slug" @click.stop="renameSlug(p)"><t-icon name="tag" /></t-button>
            <t-button size="small" variant="text" @click.stop="openDialog(p)"><t-icon name="edit" /></t-button>
            <t-button size="small" variant="text" theme="danger" @click.stop="remove(p)"><t-icon name="delete" /></t-button>
          </span>
        </div>
      </div>

      <div class="pob-viewer">
        <template v-if="selected">
          <ChessBacklinks v-if="selected.slug" ref-type="position" :slug="selected.slug" show-empty class="pob-backlinks" />
          <div v-if="sourceGame" class="pob-source">
            Trích từ ván
            <a href="#" @click.prevent="openSourceGame">{{ sourceGame.white || '?' }} – {{ sourceGame.black || '?' }}</a>
            , sau nước {{ selected.source_ply }}
          </div>
          <ChessBoardDisplay :key="selected.id + revealKey" :data="viewerData" />
          <div v-if="selected.assessment" class="pob-assessment">Đánh giá: {{ selected.assessment }}</div>
          <div v-if="selected.annotation" class="pob-annotation" @click="onAnnotationClick">
            <template v-for="(seg, si) in annotationSegments(selected)" :key="si">
              <ChessBoardDisplay v-if="seg.type === 'board'" :data="seg.board" />
              <ChessRefEmbed v-else-if="seg.type === 'ref'" :ref-str="seg.refStr" />
              <div v-else class="pob-md-segment" v-html="seg.html"></div>
            </template>
          </div>
          <div class="pob-quick-actions">
            <t-button size="small" variant="outline" @click="createPuzzleFromPosition(selected)">
              Tạo bài tập từ thế cờ này
            </t-button>
          </div>
        </template>
        <div v-else class="pob-empty pob-empty--big">Chọn một thế cờ ở bên trái.</div>
      </div>
    </div>

    <t-dialog v-model:visible="dialog.visible" :header="dialog.id ? 'Sửa thế cờ' : 'Tạo thế cờ'"
      :on-confirm="save" width="640px">
      <div class="pob-form">
        <label>Tiêu đề</label>
        <t-input v-model="dialog.title" placeholder="VD: Vua+Xe đấu Vua — thế thắng cơ bản" />
        <div class="pob-fen-label">
          <label>Thế cờ FEN *</label>
          <t-button size="small" variant="outline" @click="editorVisible = true">
            <template #icon><t-icon name="grid" /></template>Dựng bàn cờ
          </t-button>
        </div>
        <t-input v-model="dialog.fen" placeholder="8/8/8/3p4/4P3/8/8/8 w (có thể không có quân Vua)" />
        <div class="pob-row2">
          <div>
            <label>Phân loại</label>
            <t-select v-model="dialog.category" :options="positionCategoryOptions" clearable filterable />
          </div>
          <div>
            <label>Cấp độ</label>
            <t-select v-model="dialog.level" :options="positionLevelOptions" clearable />
          </div>
        </div>
        <div class="pob-row2">
          <div>
            <label>Mã khai cuộc (ECO, tùy chọn)</label>
            <t-input v-model="dialog.eco" placeholder="B90" />
          </div>
          <div>
            <label>Hướng nhìn bàn cờ</label>
            <t-select v-model="dialog.orientation" :options="orientationOptions" clearable />
          </div>
        </div>
        <label>Đánh giá (tùy chọn)</label>
        <t-input v-model="dialog.assessment" placeholder="VD: Trắng thắng / Hòa / Trắng ưu" />
        <label>Thẻ (tùy chọn, cách nhau dấu phẩy)</label>
        <t-input v-model="dialog.tags" placeholder="tan-cuoc, vua-xe" />
        <div class="pob-content-label">
          <label>Chú giải (markdown — gõ [[ để chèn liên kết ván/thế cờ/bài giảng)</label>
        </div>
        <t-textarea ref="annotationRef" v-model="dialog.annotation" :autosize="{ minRows: 4 }"
          placeholder="Giải thích ý tưởng của thế cờ… Gõ [[ để gợi ý ván/thế cờ/bài/khóa." />
        <ChessWikiLinkSuggest :textarea="annotationTextareaEl" v-model="dialog.annotation" />
        <label>Nguồn (tùy chọn)</label>
        <t-input v-model="dialog.source" placeholder="VD: Giáo trình Nga, chương 3" />
      </div>
    </t-dialog>

    <ChessPositionEditor v-model:visible="editorVisible" :initial-fen="dialog.fen" @confirm="onEditorConfirm" />
    <ChessRefDialog v-model:visible="refDialog.visible" :ref-str="refDialog.refStr" />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { marked } from 'marked';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import ChessBoardDisplay from '@/views/chat/components/tool-results/ChessBoardDisplay.vue';
import ChessBacklinks from '@/views/chess/components/ChessBacklinks.vue';
import ChessRefEmbed from '@/views/chess/components/ChessRefEmbed.vue';
import ChessRefDialog from '@/views/chess/components/ChessRefDialog.vue';
import ChessWikiLinkSuggest from '@/views/chess/components/ChessWikiLinkSuggest.vue';
import ChessPositionEditor from '@/views/chess/components/ChessPositionEditor.vue';
import type { ChessBoardData } from '@/types/tool-results';
import {
  listPositions, getPositionBySlug, createPosition, updatePosition, deletePosition,
  renamePositionSlug, exportPositions, importPositions, getGame, createPuzzle,
  type ChessPosition, type ChessGame,
} from '@/api/chess';
import { downloadText, pickTextFile } from '@/utils/fileTransfer';
import { isValidFEN, splitChessContent, renderChessChips } from '@/utils/chessBlocks';
import {
  positionCategoryOptions, positionLevelOptions, positionCategoryLabel, positionLevelLabel,
} from '@/utils/chessPositionOptions';

const { t } = useI18n();
const router = useRouter();

// Deep-link "Mở trong thư viện": chọn sẵn thế cờ theo slug (từ [[position/<slug>]]).
const props = defineProps<{ focusSlug?: string }>();
async function focusBySlug(slug?: string) {
  if (!slug) return;
  try {
    const res: any = await getPositionBySlug(slug);
    if (res?.data) { selected.value = res.data; revealKey.value++; loadSourceGame(res.data); }
  } catch { /* không tìm thấy → bỏ qua */ }
}
onMounted(() => focusBySlug(props.focusSlug));
watch(() => props.focusSlug, (s) => focusBySlug(s));

const orientationOptions = [
  { label: 'Từ phía Trắng', value: 'w' },
  { label: 'Từ phía Đen', value: 'b' },
];

const positions = ref<ChessPosition[]>([]);
const selected = ref<ChessPosition | null>(null);
const filter = reactive({ category: '', level: '', eco: '', q: '' });
const revealKey = ref(0);
const editorVisible = ref(false);

const START_FEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';
const viewerData = computed<ChessBoardData>(() => ({
  display_type: 'chess_board',
  // CỐ Ý không set `pgn`: thế cờ có thể không có quân Vua — nếu truyền pgn,
  // ChessBoardDisplay sẽ đi qua chess.js (đòi đủ 2 Vua) để dựng danh sách nước.
  fen: selected.value?.fen || START_FEN,
  orientation: selected.value?.orientation === 'b' ? 'black' : 'white',
  caption: selected.value?.title || 'Thế cờ',
}));

async function load() {
  try {
    const res: any = await listPositions(filter);
    positions.value = res?.data || [];
  } catch { MessagePlugin.error('Tải ngân hàng thế cờ thất bại'); }
}
function select(p: ChessPosition) { selected.value = p; revealKey.value++; loadSourceGame(p); }

// ---- Ván gốc (nếu thế cờ được trích từ một ván) ----
const sourceGame = ref<ChessGame | null>(null);
async function loadSourceGame(p: ChessPosition) {
  sourceGame.value = null;
  if (!p.source_game_id) return;
  try {
    const res: any = await getGame(p.source_game_id);
    sourceGame.value = res?.data || null;
  } catch { sourceGame.value = null; }
}
function openSourceGame() {
  if (sourceGame.value?.slug) {
    router.push({ name: 'chessCourses', query: { ref: `game/${sourceGame.value.slug}` } });
  }
}

// Sao chép wikilink [[position/<slug>]] để dán vào nội dung wiki/bài giảng.
async function copyWikilink(p: ChessPosition) {
  if (!p.slug) { MessagePlugin.warning('Thế cờ chưa có slug'); return; }
  const link = `[[position/${p.slug}|${p.title || p.slug}]]`;
  try {
    await navigator.clipboard.writeText(link);
    MessagePlugin.success(t('chess.ref.copied'));
  } catch {
    MessagePlugin.info(link);
  }
}

// ---- Chú giải: markdown + wikilink chip/nhúng (giống nội dung bài giảng) ----
function renderAnnotationMarkdown(md: string): string {
  let pre = renderChessChips(md || '');
  pre = pre.replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
    const pipe = inner.indexOf('|');
    return (pipe > 0 ? inner.slice(pipe + 1) : inner).trim();
  });
  return marked.parse(pre, { breaks: true, async: false }) as string;
}
function annotationSegments(p: ChessPosition) {
  return splitChessContent(p.annotation || '').map((seg) => {
    if (seg.type === 'board') return { type: 'board' as const, board: seg.board! };
    if (seg.type === 'ref') return { type: 'ref' as const, refStr: seg.ref!.ref };
    return { type: 'markdown' as const, html: renderAnnotationMarkdown(seg.markdown || '') };
  });
}
const refDialog = reactive<{ visible: boolean; refStr: string }>({ visible: false, refStr: '' });
function onAnnotationClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a.chess-ref-link') as HTMLElement | null;
  if (!a) return;
  e.preventDefault();
  const ref = a.getAttribute('data-chess-ref');
  if (ref) { refDialog.refStr = ref; refDialog.visible = true; }
}

// ---- Tạo bài tập nhanh từ thế cờ đang xem ----
async function createPuzzleFromPosition(p: ChessPosition) {
  try {
    const res: any = await createPuzzle({ title: p.title, fen: p.fen, theme: p.category });
    const slug = res?.data?.slug;
    MessagePlugin.success('Đã tạo bài tập từ thế cờ này');
    if (slug) router.push({ name: 'chessCourses', query: { ref: `puzzle/${slug}` } });
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Tạo bài tập thất bại');
  }
}

// ---- Export / Import ----
async function doExport() {
  try {
    const res: any = await exportPositions(filter);
    const items = res?.data || [];
    if (!items.length) { MessagePlugin.info('Không có thế cờ để xuất'); return; }
    downloadText(`thecoban-${new Date().toISOString().slice(0, 10)}.json`, JSON.stringify(items, null, 2), 'application/json');
    MessagePlugin.success(`Đã xuất ${items.length} thế cờ`);
  } catch { MessagePlugin.error('Xuất thất bại'); }
}
async function doImport() {
  const text = await pickTextFile('.json,application/json');
  if (text == null) return;
  let arr: any;
  try { arr = JSON.parse(text); } catch { MessagePlugin.error('File JSON không hợp lệ'); return; }
  if (!Array.isArray(arr)) { MessagePlugin.error('File phải là một mảng JSON thế cờ'); return; }
  try {
    const res: any = await importPositions(arr);
    await load();
    MessagePlugin.success(`Đã nhập ${res?.data?.imported || 0} thế cờ`);
  } catch (e: any) { MessagePlugin.error(e?.error || e?.message || 'Import thất bại'); }
}

// ---- Dialog thêm/sửa ----
interface PositionDialogState {
  visible: boolean; id: string; title: string; fen: string;
  category: string; level: string; eco: string; orientation: string;
  assessment: string; annotation: string; tags: string; source: string;
}
const dialog = reactive<PositionDialogState>({
  visible: false, id: '', title: '', fen: '',
  category: '', level: '', eco: '', orientation: '',
  assessment: '', annotation: '', tags: '', source: '',
});
function openDialog(p?: ChessPosition) {
  dialog.visible = true;
  dialog.id = p?.id || '';
  dialog.title = p?.title || '';
  dialog.fen = p?.fen || '';
  dialog.category = p?.category || '';
  dialog.level = p?.level || '';
  dialog.eco = p?.eco || '';
  dialog.orientation = p?.orientation || '';
  dialog.assessment = p?.assessment || '';
  dialog.annotation = p?.annotation || '';
  dialog.tags = p?.tags || '';
  dialog.source = p?.source || '';
}
function onEditorConfirm(fen: string) { dialog.fen = fen; }

async function save() {
  if (!dialog.fen.trim()) { MessagePlugin.warning('Nhập thế cờ FEN'); return; }
  if (!isValidFEN(dialog.fen)) {
    MessagePlugin.warning('Thế cờ FEN không hợp lệ — kiểm tra lại (8 hàng, đủ 8 ô mỗi hàng). Vua là TÙY CHỌN.');
    return;
  }
  const payload = {
    title: dialog.title, fen: dialog.fen, category: dialog.category, level: dialog.level,
    eco: dialog.eco, orientation: dialog.orientation, assessment: dialog.assessment,
    annotation: dialog.annotation, tags: dialog.tags, source: dialog.source,
  };
  try {
    const editingId = dialog.id;
    if (editingId) await updatePosition(editingId, payload);
    else await createPosition(payload);
    dialog.visible = false;
    await load();
    if (editingId && selected.value?.id === editingId) {
      const updated = positions.value.find((p) => p.id === editingId);
      if (updated) { selected.value = updated; revealKey.value++; }
    }
    MessagePlugin.success('Đã lưu thế cờ');
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Lưu thất bại (kiểm tra FEN)');
  }
}

// Đổi slug thế cờ (power-feature cho HLV): link cũ [[position/<cũ>]] vẫn sống nhờ alias.
async function renameSlug(p: ChessPosition) {
  if (!p.slug) { MessagePlugin.warning('Thế cờ chưa có slug'); return; }
  const next = window.prompt(`Đổi slug cho "${p.title || p.slug}" (link cũ vẫn sống nhờ alias):`, p.slug);
  if (next == null) return;
  const v = next.trim();
  if (!v || v === p.slug) return;
  try {
    const res: any = await renamePositionSlug(p.id, v);
    await load();
    if (selected.value?.id === p.id && res?.data) selected.value = res.data;
    MessagePlugin.success(`Đã đổi slug → ${res?.data?.slug || v}`);
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Đổi slug thất bại');
  }
}

function remove(p: ChessPosition) {
  DialogPlugin.confirm({
    header: 'Xóa thế cờ', body: `Xóa "${p.title || 'thế cờ'}"?`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deletePosition(p.id);
        if (selected.value?.id === p.id) { selected.value = null; sourceGame.value = null; }
        await load();
        MessagePlugin.success('Đã xóa');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}

// ---- Autocomplete "[[" trong ô chú giải ----
const annotationRef = ref<any>(null);
const annotationTextareaEl = ref<HTMLTextAreaElement | null>(null);
watch(() => dialog.visible, async (open) => {
  if (!open) { annotationTextareaEl.value = null; return; }
  await nextTick();
  const root = annotationRef.value?.$el || annotationRef.value;
  annotationTextareaEl.value = (root?.querySelector?.('textarea') as HTMLTextAreaElement) || null;
});

load();
</script>

<style lang="less" scoped>
.pob { display: flex; flex-direction: column; height: 100%; }
.pob-toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.pob-body { display: flex; gap: 16px; flex: 1; min-height: 0; }
.pob-list { width: 340px; flex: 0 0 340px; overflow-y: auto; border-right: 1px solid var(--td-component-stroke); padding-right: 12px; }
.pob-viewer { flex: 1; overflow-y: auto; }
.pob-backlinks { margin: 0 0 12px; }
.pob-empty { color: var(--td-text-color-placeholder); font-size: 14px; padding: 16px 4px; }
.pob-empty--big { text-align: center; padding-top: 80px; }
.pob-row { display: flex; align-items: center; justify-content: space-between; padding: 8px 10px; border: 1px solid var(--td-component-stroke); border-radius: 8px; margin-bottom: 6px; cursor: pointer;
  &:hover { background: var(--td-bg-color-container-hover); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); } }
.pob-title { font-weight: 600; color: var(--td-text-color-primary); }
.pob-meta { display: flex; gap: 6px; margin-top: 4px; flex-wrap: wrap; }
.pob-tag { background: var(--td-brand-color-light); color: var(--td-brand-color); padding: 0 6px; border-radius: 4px; font-size: 12px; }
.pob-tag--level { background: #e6f4ff; color: #2275b4; }
.pob-tag--eco { background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-family: monospace; }
.pob-actions { display: flex; }
.pob-source { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 8px; }
.pob-assessment { margin-top: 8px; font-size: 13px; color: var(--td-text-color-primary); font-weight: 600; }
.pob-annotation { margin-top: 12px; padding-top: 10px; border-top: 1px solid var(--td-component-stroke); line-height: 1.6; color: var(--td-text-color-primary); }
.pob-md-segment :deep(.chess-ref-link) {
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
  padding: 0 6px; border-radius: 4px; text-decoration: none; font-weight: 600;
  &::before { content: '♟ '; }
  &:hover { text-decoration: underline; }
}
.pob-quick-actions { margin-top: 14px; }
.pob-form { display: flex; flex-direction: column; gap: 6px; label { font-size: 13px; color: var(--td-text-color-secondary); margin-top: 6px; } }
.pob-fen-label { display: flex; align-items: center; justify-content: space-between; margin-top: 6px; label { margin-top: 0; } }
.pob-content-label { margin-top: 6px; }
.pob-row2 { display: flex; gap: 12px; > div { flex: 1; min-width: 0; } }
</style>
