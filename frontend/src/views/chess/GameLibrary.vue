<template>
  <div class="gl">
    <div class="gl-toolbar">
      <t-input v-model="filter.white" placeholder="Trắng" clearable style="width:140px" @change="load" />
      <t-input v-model="filter.black" placeholder="Đen" clearable style="width:140px" @change="load" />
      <t-input v-model="filter.eco" placeholder="ECO" clearable style="width:90px" @change="load" />
      <t-select v-model="filter.result" :options="resultOptions" placeholder="Kết quả" clearable
        style="width:120px" @change="load" />
      <div style="flex:1"></div>
      <t-button variant="outline" size="small" @click="doExport">
        <template #icon><t-icon name="download" /></template>Export PGN
      </t-button>
      <t-button theme="primary" size="small" @click="importDialog.visible = true">
        <template #icon><t-icon name="upload" /></template>Import PGN
      </t-button>
    </div>

    <div class="gl-body">
      <div class="gl-list">
        <div v-if="games.length === 0" class="gl-empty">Chưa có ván cờ. Nhấn "Import PGN" để thêm.</div>
        <div v-for="g in games" :key="g.id" class="gl-row" :class="{ active: selected && selected.id === g.id }"
          @click="select(g)">
          <div class="gl-row-main">
            <div class="gl-players"><b>{{ g.white || '?' }}</b> – <b>{{ g.black || '?' }}</b>
              <span class="gl-result">{{ g.result || '*' }}</span>
            </div>
            <div class="gl-meta">
              <span v-if="g.eco" class="gl-tag">{{ g.eco }}</span>
              <span v-if="g.event">{{ g.event }}</span>
              <span>{{ g.ply_count }} nửa-nước</span>
            </div>
          </div>
          <span class="gl-row-actions">
            <t-button size="small" variant="text" :title="t('chess.ref.copyLink')" @click.stop="copyWikilink(g)">
              <t-icon name="link" />
            </t-button>
            <t-button size="small" variant="text" title="Đổi slug" @click.stop="renameSlug(g)"><t-icon name="tag" /></t-button>
            <t-button size="small" variant="text" theme="danger" @click.stop="remove(g)"><t-icon name="delete" /></t-button>
          </span>
        </div>
      </div>
      <div class="gl-viewer">
        <template v-if="selected">
          <ChessBacklinks v-if="selected.slug" ref-type="game" :slug="selected.slug" show-empty class="gl-backlinks" />
          <div class="gl-viewer-actions">
            <t-button size="small" variant="outline" @click="openSavePositionDialog">
              <template #icon><t-icon name="save" /></template>Lưu thế cờ này
            </t-button>
          </div>
          <ChessBoardDisplay ref="boardRef" :key="selected.id" :data="viewerData" />
          <div v-if="extractedPositions.length" class="gl-extracted">
            <div class="gl-extracted-title">Thế cờ đã trích từ ván này ({{ extractedPositions.length }})</div>
            <div class="gl-extracted-chips">
              <a v-for="p in extractedPositions" :key="p.id" href="#" class="gl-extracted-chip"
                @click.prevent="openPosition(p)">
                {{ p.title || p.slug }}<span class="gl-extracted-ply">sau nước {{ p.source_ply }}</span>
              </a>
            </div>
          </div>
        </template>
        <div v-else class="gl-empty gl-empty--big">Chọn một ván để xem lại (lật từng nước).</div>
      </div>
    </div>

    <t-dialog v-model:visible="importDialog.visible" header="Import PGN (nhiều ván)" :on-confirm="doImport" width="640px">
      <div class="gl-form">
        <label>Dán nội dung PGN (có thể nhiều ván):</label>
        <t-textarea v-model="importDialog.pgn" :autosize="{ minRows: 8 }"
          placeholder='[Event "..."]...&#10;1. e4 e5 ... 1-0' />
      </div>
    </t-dialog>

    <t-dialog v-model:visible="savePosDialog.visible" header="Lưu thế cờ vào Ngân hàng thế cờ"
      :on-confirm="doSavePosition" width="560px">
      <div class="gl-form">
        <label>Thế cờ (FEN) — nước đang xem</label>
        <t-input :model-value="savePosDialog.fen" readonly />
        <label>Tiêu đề</label>
        <t-input v-model="savePosDialog.title" placeholder="VD: Vua+Xe đấu Vua sau nước 42" />
        <label>Phân loại</label>
        <t-select v-model="savePosDialog.category" :options="positionCategoryOptions" clearable filterable creatable />
        <label>Cấp độ</label>
        <t-select v-model="savePosDialog.level" :options="positionLevelOptions" clearable />
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import ChessBoardDisplay from '@/views/chat/components/tool-results/ChessBoardDisplay.vue';
import ChessBacklinks from '@/views/chess/components/ChessBacklinks.vue';
import type { ChessBoardData } from '@/types/tool-results';
import {
  listGames, getGameBySlug, deleteGame, importGames, exportGamesPGN, renameGameSlug, type ChessGame,
  listPositionsByGame, createPosition, type ChessPosition,
} from '@/api/chess';
import { downloadText } from '@/utils/fileTransfer';
import { positionCategoryOptions, positionLevelOptions } from '@/utils/chessPositionOptions';
import { useRouter } from 'vue-router';

const { t } = useI18n();
const router = useRouter();
const boardRef = ref<InstanceType<typeof ChessBoardDisplay> | null>(null);

// Deep-link "Mở trong thư viện": chọn sẵn ván theo slug (từ wikilink [[game/<slug>]]).
const props = defineProps<{ focusSlug?: string }>();
async function focusBySlug(slug?: string) {
  if (!slug) return;
  try {
    const res: any = await getGameBySlug(slug);
    if (res?.data) { selected.value = res.data; loadExtractedPositions(res.data.id); }
  } catch { /* không tìm thấy → bỏ qua */ }
}
onMounted(() => focusBySlug(props.focusSlug));
watch(() => props.focusSlug, (s) => focusBySlug(s));

// Sao chép wikilink [[game/<slug>]] để dán vào nội dung wiki/bài giảng.
async function copyWikilink(g: ChessGame) {
  if (!g.slug) { MessagePlugin.warning('Ván chưa có slug'); return; }
  const link = `[[game/${g.slug}|${g.white || '?'} – ${g.black || '?'}]]`;
  try {
    await navigator.clipboard.writeText(link);
    MessagePlugin.success(t('chess.ref.copied'));
  } catch {
    MessagePlugin.info(link);
  }
}

const STARTFEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';
const resultOptions = [
  { label: '1-0', value: '1-0' }, { label: '0-1', value: '0-1' },
  { label: '½-½', value: '1/2-1/2' }, { label: 'Chưa xong', value: '*' },
];

const games = ref<ChessGame[]>([]);
const selected = ref<ChessGame | null>(null);
const filter = reactive({ white: '', black: '', eco: '', result: '' });
const importDialog = reactive({ visible: false, pgn: '' });

interface SavePositionDialogState {
  visible: boolean;
  fen: string;
  ply: number;
  title: string;
  category: string;
  level: string;
}
const savePosDialog = reactive<SavePositionDialogState>({
  visible: false, fen: '', ply: 0, title: '', category: '', level: '',
});
function openSavePositionDialog() {
  const fen = boardRef.value?.currentFen || '';
  if (!fen) { MessagePlugin.warning('Chưa có thế cờ để lưu'); return; }
  savePosDialog.visible = true;
  savePosDialog.fen = fen;
  savePosDialog.ply = boardRef.value?.currentIndex ?? 0;
  const label = boardRef.value?.currentLabel || '';
  const players = selected.value ? `${selected.value.white || '?'} – ${selected.value.black || '?'}` : '';
  savePosDialog.title = label ? `${players} — ${label}` : players;
  savePosDialog.category = '';
  savePosDialog.level = '';
}
async function doSavePosition() {
  if (!selected.value) return;
  try {
    await createPosition({
      title: savePosDialog.title,
      fen: savePosDialog.fen,
      category: savePosDialog.category,
      level: savePosDialog.level,
      source_game_id: selected.value.id,
      source_ply: savePosDialog.ply,
    });
    savePosDialog.visible = false;
    await loadExtractedPositions(selected.value.id);
    MessagePlugin.success('Đã lưu thế cờ vào Ngân hàng thế cờ');
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Lưu thế cờ thất bại');
  }
}

const viewerData = computed<ChessBoardData>(() => ({
  display_type: 'chess_board',
  fen: STARTFEN,
  pgn: selected.value?.pgn || '',
  caption: selected.value ? `${selected.value.white} – ${selected.value.black}` : '',
}));

async function load() {
  try {
    const res: any = await listGames(filter);
    games.value = res?.data || [];
  } catch { MessagePlugin.error('Tải kho ván thất bại'); }
}

// Thế cờ đã trích từ ván đang xem (Ngân hàng thế cờ, nguồn source_game_id).
const extractedPositions = ref<ChessPosition[]>([]);
async function loadExtractedPositions(gameId: string) {
  extractedPositions.value = [];
  try {
    const res: any = await listPositionsByGame(gameId);
    extractedPositions.value = res?.data || [];
  } catch { /* best-effort, không chặn xem ván */ }
}
function openPosition(p: ChessPosition) {
  router.push({ name: 'chessCourses', query: { ref: `position/${p.slug}` } });
}

function select(g: ChessGame) { selected.value = g; loadExtractedPositions(g.id); }
function remove(g: ChessGame) {
  DialogPlugin.confirm({
    header: 'Xóa ván', body: `Xóa ván ${g.white} – ${g.black}?`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteGame(g.id);
        if (selected.value?.id === g.id) selected.value = null;
        await load();
        MessagePlugin.success('Đã xóa');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}
// Đổi slug ván (power-feature cho HLV): link cũ [[game/<cũ>]] vẫn sống nhờ alias.
async function renameSlug(g: ChessGame) {
  if (!g.slug) { MessagePlugin.warning('Ván chưa có slug'); return; }
  const next = window.prompt(`Đổi slug cho ván "${g.white || '?'} – ${g.black || '?'}" (link cũ vẫn sống nhờ alias):`, g.slug);
  if (next == null) return;
  const v = next.trim();
  if (!v || v === g.slug) return;
  try {
    const res: any = await renameGameSlug(g.id, v);
    await load();
    if (selected.value?.id === g.id && res?.data) selected.value = res.data;
    MessagePlugin.success(`Đã đổi slug → ${res?.data?.slug || v}`);
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Đổi slug thất bại');
  }
}

async function doExport() {
  try {
    const res: any = await exportGamesPGN(filter);
    const pgn = (res?.data?.pgn || '').trim();
    if (!pgn) { MessagePlugin.info('Không có ván nào để xuất'); return; }
    downloadText(`vandau-${new Date().toISOString().slice(0, 10)}.pgn`, pgn, 'application/x-chess-pgn');
    MessagePlugin.success('Đã xuất PGN');
  } catch { MessagePlugin.error('Xuất thất bại'); }
}
async function doImport() {
  if (!importDialog.pgn.trim()) { MessagePlugin.warning('Dán PGN'); return; }
  try {
    const res: any = await importGames(importDialog.pgn);
    importDialog.visible = false;
    importDialog.pgn = '';
    await load();
    MessagePlugin.success(`Đã nhập ${res?.data?.imported || 0} ván`);
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Import thất bại');
  }
}
load();
</script>

<style lang="less" scoped>
.gl { display: flex; flex-direction: column; height: 100%; }
.gl-toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.gl-body { display: flex; gap: 16px; flex: 1; min-height: 0; }
.gl-list { width: 380px; flex: 0 0 380px; overflow-y: auto; border-right: 1px solid var(--td-component-stroke); padding-right: 12px; }
.gl-viewer { flex: 1; overflow-y: auto; }
.gl-backlinks { margin: 0 0 12px; }
.gl-empty { color: var(--td-text-color-placeholder); font-size: 14px; padding: 16px 4px; }
.gl-empty--big { text-align: center; padding-top: 80px; }
.gl-row { display: flex; align-items: center; justify-content: space-between; padding: 8px 10px; border: 1px solid var(--td-component-stroke); border-radius: 8px; margin-bottom: 6px; cursor: pointer;
  &:hover { background: var(--td-bg-color-container-hover); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); } }
.gl-row-actions { display: flex; align-items: center; }
.gl-players { font-size: 14px; color: var(--td-text-color-primary); }
.gl-result { margin-left: 6px; color: var(--td-text-color-secondary); font-weight: 600; }
.gl-meta { display: flex; gap: 8px; margin-top: 3px; font-size: 12px; color: var(--td-text-color-secondary); }
.gl-tag { background: var(--td-brand-color-light); color: var(--td-brand-color); padding: 0 6px; border-radius: 4px; }
.gl-form { display: flex; flex-direction: column; gap: 6px; label { font-size: 13px; color: var(--td-text-color-secondary); } }
.gl-viewer-actions { margin-bottom: 8px; }
.gl-extracted { margin-top: 14px; padding-top: 10px; border-top: 1px solid var(--td-component-stroke); }
.gl-extracted-title { font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 6px; }
.gl-extracted-chips { display: flex; flex-wrap: wrap; gap: 6px; }
.gl-extracted-chip {
  display: inline-flex; align-items: center; gap: 6px;
  padding: 3px 8px; border-radius: 6px; font-size: 12px; text-decoration: none;
  background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary);
  &:hover { color: var(--td-brand-color); }
}
.gl-extracted-ply { color: var(--td-text-color-placeholder); font-size: 11px; }
</style>
