<template>
  <div class="chess-board-display">
    <div v-if="caption" class="chess-caption">{{ caption }}</div>

    <div class="chess-body">
      <!-- Thanh đánh giá (nếu có điểm engine) -->
      <div v-if="hasEval" class="eval-bar" :title="evalText">
        <div class="eval-bar-fill" :style="evalFillStyle"></div>
      </div>

      <div ref="boardEl" class="chess-board"></div>
    </div>

    <!-- Thông tin đánh giá engine -->
    <div v-if="hasEval" class="chess-eval-line">
      <span class="eval-score" :class="{ 'eval-mate': data.is_mate }">{{ evalText }}</span>
      <span v-if="bestMoveLabel" class="eval-best">
        {{ t('chess.bestMove') }}: <strong>{{ bestMoveLabel }}</strong>
      </span>
      <span v-if="data.depth" class="eval-depth">{{ t('chess.depth') }} {{ data.depth }}</span>
    </div>

    <!-- Điều hướng nước đi (khi có nhiều thế cờ) -->
    <div v-if="positions.length > 1" class="chess-nav">
      <div class="chess-nav-controls">
        <button class="nav-btn" :disabled="currentIndex === 0" @click="goTo(0)" :title="t('chess.start')">⏮</button>
        <button class="nav-btn" :disabled="currentIndex === 0" @click="goTo(currentIndex - 1)" :title="t('chess.prev')">◀</button>
        <span class="nav-label">{{ currentLabel }}</span>
        <button class="nav-btn" :disabled="currentIndex === positions.length - 1" @click="goTo(currentIndex + 1)" :title="t('chess.next')">▶</button>
        <button class="nav-btn" :disabled="currentIndex === positions.length - 1" @click="goTo(positions.length - 1)" :title="t('chess.end')">⏭</button>
        <button class="nav-btn" @click="flip" :title="t('chess.flip')">⇅</button>
      </div>

      <div class="move-list">
        <span
          v-for="(pos, idx) in positions"
          :key="idx"
          v-show="idx > 0"
          class="move-item"
          :class="{ active: idx === currentIndex }"
          @click="goTo(idx)"
        >{{ pos.label }}</span>
      </div>
    </div>

    <div v-else class="chess-nav-single">
      <button class="nav-btn" @click="flip" :title="t('chess.flip')">⇅ {{ t('chess.flip') }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch, nextTick } from 'vue';
import { useI18n } from 'vue-i18n';
import { Chess } from 'chess.js';
import { Chessboard, COLOR } from 'cm-chessboard/src/Chessboard.js';
import piecesUrl from 'cm-chessboard/assets/pieces/standard.svg?url';
import 'cm-chessboard/assets/chessboard.css';
import type { ChessBoardData } from '@/types/tool-results';

const props = defineProps<{ data: ChessBoardData }>();
const { t } = useI18n();

const START_FEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';

interface PosItem {
  fen: string;
  label: string;
}

const caption = computed(() => props.data.caption || '');

// Xây danh sách thế cờ: ưu tiên plies từ backend, rồi tới parse PGN ở client,
// cuối cùng là một FEN đơn lẻ.
const positions = computed<PosItem[]>(() => {
  const list: PosItem[] = [];
  const startFen = props.data.fen || START_FEN;

  if (props.data.plies && props.data.plies.length) {
    list.push({ fen: startFen, label: t('chess.startPosition') });
    for (const p of props.data.plies) {
      const prefix = p.side === 'w' ? `${p.move_number}.` : `${p.move_number}...`;
      list.push({ fen: p.fen_after, label: `${prefix} ${p.san}` });
    }
    return list;
  }

  if (props.data.pgn) {
    try {
      const game = new Chess();
      game.loadPgn(props.data.pgn);
      const hist = game.history({ verbose: true }) as Array<{ san: string; after: string; before: string; color: string }>;
      list.push({ fen: hist.length ? hist[0].before : startFen, label: t('chess.startPosition') });
      hist.forEach((h, i) => {
        const num = Math.floor(i / 2) + 1;
        const prefix = h.color === 'w' ? `${num}.` : `${num}...`;
        list.push({ fen: h.after, label: `${prefix} ${h.san}` });
      });
      return list;
    } catch {
      // PGN lỗi → rơi về FEN đơn
    }
  }

  list.push({ fen: startFen, label: t('chess.startPosition') });
  return list;
});

const currentIndex = ref(0);
const currentLabel = computed(() => positions.value[currentIndex.value]?.label ?? '');
// FEN của thế cờ đang xem (nước hiện tại nếu đang tua ván). Lộ ra qua
// defineExpose bên dưới để component cha (vd GameLibrary) "kéo" FEN tại đúng
// thời điểm bấm nút — thay vì phát emit mỗi lần đổi nước, tránh sinh state
// trùng và tự miễn nhiễm với watch(positions) reset currentIndex bên dưới.
const currentFen = computed(() => positions.value[currentIndex.value]?.fen ?? '');

// ---- Đánh giá engine ----
const hasEval = computed(() =>
  props.data.is_mate === true ||
  typeof props.data.eval_cp === 'number' ||
  !!props.data.best_move,
);

const evalText = computed(() => {
  if (props.data.is_mate) {
    const n = props.data.mate_in ?? 0;
    return n >= 0 ? `#${n}` : `#-${Math.abs(n)}`;
  }
  if (typeof props.data.eval_cp === 'number') {
    // Quy về góc nhìn Trắng cho nhất quán hiển thị.
    const white = props.data.side_to_move === 'b' ? -props.data.eval_cp : props.data.eval_cp;
    const pawns = white / 100;
    return (pawns > 0 ? '+' : '') + pawns.toFixed(2);
  }
  return '';
});

const bestMoveLabel = computed(() => props.data.best_move_san || props.data.best_move || '');

// Vị trí con trỏ thanh đánh giá: 0% (Đen thắng) → 100% (Trắng thắng).
const evalFillStyle = computed(() => {
  let whiteAdvantage = 0.5;
  if (props.data.is_mate) {
    const n = props.data.mate_in ?? 0;
    const sideWhite = props.data.side_to_move !== 'b';
    const whiteMating = (n >= 0) === sideWhite;
    whiteAdvantage = whiteMating ? 1 : 0;
  } else if (typeof props.data.eval_cp === 'number') {
    const white = props.data.side_to_move === 'b' ? -props.data.eval_cp : props.data.eval_cp;
    // Hàm sigmoid mềm để giới hạn về [0,1].
    whiteAdvantage = 1 / (1 + Math.exp(-white / 400));
  }
  return { height: `${(whiteAdvantage * 100).toFixed(1)}%` };
});

// ---- cm-chessboard ----
const boardEl = ref<HTMLElement | null>(null);
let board: any = null;
const orientation = ref<'white' | 'black'>(props.data.orientation || 'white');

function buildBoard() {
  if (!boardEl.value) return;
  board = new Chessboard(boardEl.value, {
    position: positions.value[currentIndex.value]?.fen || START_FEN,
    orientation: orientation.value === 'black' ? COLOR.black : COLOR.white,
    assetsUrl: '/',
    style: {
      pieces: { file: piecesUrl },
      borderType: 'frame',
      showCoordinates: true,
    },
    responsive: true,
  });
}

function applyPosition() {
  const fen = positions.value[currentIndex.value]?.fen;
  if (board && fen) {
    board.setPosition(fen, true);
  }
}

function goTo(idx: number) {
  if (idx < 0 || idx >= positions.value.length) return;
  currentIndex.value = idx;
  applyPosition();
}

function flip() {
  orientation.value = orientation.value === 'white' ? 'black' : 'white';
  if (board) {
    board.setOrientation(orientation.value === 'black' ? COLOR.black : COLOR.white);
  }
}

onMounted(async () => {
  await nextTick();
  buildBoard();
});

onBeforeUnmount(() => {
  if (board) {
    try { board.destroy(); } catch { /* noop */ }
    board = null;
  }
});

// Khi dữ liệu đổi (stream cập nhật), dựng lại danh sách và đồng bộ bàn cờ.
watch(positions, () => {
  currentIndex.value = 0;
  applyPosition();
});

// Cho phép component cha đọc thế cờ đang xem (vd nút "Lưu thế cờ này" trong
// GameLibrary khi tua ván) mà không cần biết chỉ số nước nội bộ.
defineExpose({ currentFen, currentIndex, currentLabel });
</script>

<style lang="less" scoped>
.chess-board-display {
  margin: 12px 0;
  max-width: 420px;
}

.chess-caption {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin-bottom: 8px;
}

.chess-body {
  display: flex;
  align-items: stretch;
  gap: 8px;
}

.eval-bar {
  width: 12px;
  border-radius: 4px;
  background: #3a3a3a;
  overflow: hidden;
  display: flex;
  flex-direction: column-reverse;
  flex: 0 0 auto;
}

.eval-bar-fill {
  width: 100%;
  background: #f0f0f0;
  transition: height 0.3s ease;
}

.chess-board {
  flex: 1 1 auto;
  width: 100%;
  min-width: 0;
}

.chess-eval-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-top: 8px;
  font-size: 13px;
  color: var(--td-text-color-secondary);

  .eval-score {
    font-weight: 700;
    font-family: var(--app-font-family-mono);
    color: var(--td-text-color-primary);
  }

  .eval-mate {
    color: var(--td-error-color, #d54941);
  }
}

.chess-nav {
  margin-top: 10px;
}

.chess-nav-controls {
  display: flex;
  align-items: center;
  gap: 6px;
}

.chess-nav-single {
  margin-top: 8px;
}

.nav-btn {
  border: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  border-radius: 6px;
  padding: 2px 8px;
  cursor: pointer;
  font-size: 13px;
  line-height: 1.6;

  &:hover:not(:disabled) {
    background: var(--td-bg-color-secondarycontainer);
  }

  &:disabled {
    opacity: 0.4;
    cursor: default;
  }
}

.nav-label {
  flex: 1 1 auto;
  text-align: center;
  font-family: var(--app-font-family-mono);
  font-size: 13px;
  color: var(--td-text-color-primary);
}

.move-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  margin-top: 8px;
  max-height: 120px;
  overflow-y: auto;

  .move-item {
    font-family: var(--app-font-family-mono);
    font-size: 12px;
    color: var(--td-text-color-secondary);
    cursor: pointer;
    padding: 0 2px;
    border-radius: 3px;

    &:hover {
      background: var(--td-bg-color-secondarycontainer);
    }

    &.active {
      background: var(--td-brand-color, #0052d9);
      color: #fff;
    }
  }
}
</style>
