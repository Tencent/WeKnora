<template>
  <t-dialog v-model:visible="visible" header="Dựng bàn cờ" width="480px" :footer="false" @close="close">
    <div class="cpe">
      <div class="cpe-board-wrap">
        <div ref="boardEl" class="cpe-board"></div>
      </div>

      <div class="cpe-palette">
        <div class="cpe-palette-row">
          <button v-for="p in whitePieces" :key="p" type="button" class="cpe-piece"
            :class="{ active: selected === p }" :title="pieceLabel(p)" @click="selected = p">
            {{ glyph(p) }}
          </button>
        </div>
        <div class="cpe-palette-row">
          <button v-for="p in blackPieces" :key="p" type="button" class="cpe-piece"
            :class="{ active: selected === p }" :title="pieceLabel(p)" @click="selected = p">
            {{ glyph(p) }}
          </button>
        </div>
        <button type="button" class="cpe-piece cpe-eraser" :class="{ active: selected === '' }"
          title="Xóa quân (bấm vào ô để xóa)" @click="selected = ''">
          ✕ Xóa quân
        </button>
      </div>

      <div class="cpe-controls">
        <t-radio-group v-model="side" variant="default-filled" size="small">
          <t-radio-button value="w">Trắng đi</t-radio-button>
          <t-radio-button value="b">Đen đi</t-radio-button>
        </t-radio-group>
        <t-button size="small" variant="outline" @click="flip">
          <template #icon><t-icon name="swap" /></template>Lật bàn
        </t-button>
        <t-button size="small" variant="outline" @click="resetStart">Thế cờ ban đầu</t-button>
        <t-button size="small" variant="outline" @click="clearBoard">Xóa hết quân</t-button>
      </div>

      <div class="cpe-hint">
        Bấm chọn quân ở trên rồi bấm vào ô trên bàn cờ để đặt/xóa. Cho phép bàn
        cờ không đủ 2 Vua (dùng để dạy thế cờ giản lược).
      </div>

      <div class="cpe-fen"><code>{{ previewFen }}</code></div>

      <div class="cpe-actions">
        <t-button variant="outline" @click="close">Hủy</t-button>
        <t-button theme="primary" @click="confirmFen">Dùng thế cờ này</t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, watch, onBeforeUnmount } from 'vue';
import { Chessboard, COLOR, POINTER_EVENTS, FEN } from 'cm-chessboard/src/Chessboard.js';
import piecesUrl from 'cm-chessboard/assets/pieces/standard.svg?url';
import 'cm-chessboard/assets/chessboard.css';

// Trình soạn thế cờ bằng chuột: đặt/xóa quân tự do, không kiểm tra luật cờ.
// Dùng cm-chessboard (setPiece/setPosition không validate luật — đã xác nhận
// khi khảo sát) để cho phép dựng thế cờ KHÔNG có quân Vua (dạy trẻ mới học).
// TUYỆT ĐỐI không import chess.js ở đây — chess.js đòi đúng 2 Vua và sẽ ném lỗi.
const props = defineProps<{ visible: boolean; initialFen?: string }>();
const emit = defineEmits<{
  (e: 'update:visible', v: boolean): void;
  (e: 'confirm', fen: string): void;
}>();

const boardEl = ref<HTMLElement | null>(null);
let board: any = null;

const visible = computed({
  get: () => props.visible,
  set: (v: boolean) => emit('update:visible', v),
});

const side = ref<'w' | 'b'>('w');
const orientation = ref<'white' | 'black'>('white');
const selected = ref<string>('wp'); // piece code "wp".."bk", hoặc '' = xóa quân

const WHITE_TYPES = ['k', 'q', 'r', 'b', 'n', 'p'];
const whitePieces = WHITE_TYPES.map((t) => 'w' + t);
const blackPieces = WHITE_TYPES.map((t) => 'b' + t);

const GLYPHS: Record<string, string> = {
  wk: '♔', wq: '♕', wr: '♖', wb: '♗', wn: '♘', wp: '♙',
  bk: '♚', bq: '♛', br: '♜', bb: '♝', bn: '♞', bp: '♟',
};
function glyph(p: string): string { return GLYPHS[p] || '?'; }
const NAMES: Record<string, string> = {
  k: 'Vua', q: 'Hậu', r: 'Xe', b: 'Tượng', n: 'Mã', p: 'Tốt',
};
function pieceLabel(p: string): string {
  const color = p[0] === 'w' ? 'Trắng' : 'Đen';
  return `${color} ${NAMES[p[1]] || ''}`;
}

// board.getPosition() không phản ứng (reactive) — bumper để ép previewFen tính
// lại mỗi khi ta chủ động sửa bàn cờ (đặt/xóa quân, xóa hết, thế cờ ban đầu).
const boardVersion = ref(0);
const previewFen = computed(() => {
  boardVersion.value; // eslint-disable-line @typescript-eslint/no-unused-expressions
  const placement = board ? board.getPosition() : FEN.empty;
  return `${placement} ${side.value} - - 0 1`;
});

function buildBoard() {
  if (!boardEl.value) return;
  const startPos = props.initialFen ? props.initialFen.split(/\s+/)[0] : FEN.empty;
  board = new Chessboard(boardEl.value, {
    position: startPos || FEN.empty,
    orientation: orientation.value === 'black' ? COLOR.black : COLOR.white,
    assetsUrl: '/',
    style: { pieces: { file: piecesUrl }, borderType: 'frame', showCoordinates: true },
    responsive: true,
  });
  board.enableSquareSelect(POINTER_EVENTS.pointerdown, onSquareSelect);
  boardVersion.value++;
}

function destroyBoard() {
  if (board) {
    try { board.destroy(); } catch { /* noop */ }
    board = null;
  }
}

function onSquareSelect(e: { square: string | null }) {
  if (!board || !e.square) return;
  const piece = selected.value ? selected.value : null;
  board.setPiece(e.square, piece);
  boardVersion.value++;
}

function flip() {
  orientation.value = orientation.value === 'white' ? 'black' : 'white';
  if (board) board.setOrientation(orientation.value === 'black' ? COLOR.black : COLOR.white);
}
function clearBoard() {
  if (board) board.setPosition(FEN.empty);
  boardVersion.value++;
}
function resetStart() {
  if (board) board.setPosition(FEN.start);
  side.value = 'w';
  boardVersion.value++;
}

function close() { emit('update:visible', false); }
function confirmFen() {
  emit('confirm', previewFen.value);
  emit('update:visible', false);
}

watch(() => props.visible, async (v) => {
  if (v) {
    await nextTick();
    buildBoard();
    side.value = (props.initialFen?.split(/\s+/)[1] as 'w' | 'b') || 'w';
  } else {
    destroyBoard();
  }
}, { immediate: true });

onBeforeUnmount(destroyBoard);
</script>

<style lang="less" scoped>
.cpe { display: flex; flex-direction: column; gap: 10px; align-items: center; }
.cpe-board-wrap { width: 100%; max-width: 360px; }
.cpe-board { width: 100%; }
.cpe-palette { display: flex; flex-direction: column; gap: 6px; align-items: center; }
.cpe-palette-row { display: flex; gap: 4px; }
.cpe-piece {
  width: 36px; height: 36px; font-size: 22px; line-height: 1;
  border: 1px solid var(--td-component-border, #dcdcdc);
  border-radius: 6px; background: var(--td-bg-color-container, #fff);
  cursor: pointer; display: flex; align-items: center; justify-content: center;
  &:hover { border-color: var(--td-brand-color); }
  &.active { background: var(--td-brand-color-light); border-color: var(--td-brand-color); }
}
.cpe-eraser { width: auto; padding: 0 10px; font-size: 13px; }
.cpe-controls { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; justify-content: center; }
.cpe-hint { font-size: 12px; color: var(--td-text-color-placeholder); text-align: center; max-width: 380px; }
.cpe-fen {
  width: 100%; padding: 6px 10px; border-radius: 6px; font-size: 12px;
  background: var(--td-bg-color-secondarycontainer); word-break: break-all; text-align: center;
}
.cpe-actions { display: flex; gap: 8px; justify-content: flex-end; width: 100%; }
</style>
