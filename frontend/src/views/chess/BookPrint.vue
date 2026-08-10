<template>
  <div class="bkp">
    <div class="bkp-toolbar no-print">
      <t-button theme="primary" @click="doPrint">
        <template #icon><t-icon name="print" /></template>In / Lưu PDF (Ctrl+P)
      </t-button>
    </div>
    <div v-if="loading" class="bkp-empty">Đang tải…</div>
    <div v-else-if="!book" class="bkp-empty">Không tìm thấy sách.</div>
    <article v-else class="bkp-page">
      <h1>{{ book.title }}</h1>
      <p v-if="book.subtitle" class="bkp-subtitle">{{ book.subtitle }}</p>
      <ul class="bkp-meta">
        <li v-if="book.author">Tác giả: {{ book.author }}</li>
        <li v-if="book.translator">Dịch giả: {{ book.translator }}</li>
        <li v-if="book.publisher">Nhà xuất bản: {{ book.publisher }}</li>
        <li v-if="book.year">Năm: {{ book.year }}</li>
        <li v-if="book.level">Cấp độ: {{ bookLevelLabel(book.level) }}</li>
        <li v-if="book.phase">Giai đoạn: {{ bookPhaseLabel(book.phase) }}</li>
      </ul>
      <div v-if="book.description" class="bkp-description" v-html="renderMd(book.description)"></div>

      <template v-for="(group, gi) in chapterGroups" :key="gi">
        <h2 v-if="group.part" class="bkp-part">{{ group.part }}</h2>
        <section v-for="ch in group.items" :key="ch.id" class="bkp-chapter">
          <h3>{{ ch.title }}</h3>
          <ChessBoardDisplay v-if="ch.fen" :data="chapterBoardData(ch)" />
          <template v-for="(seg, si) in chapterSegments(ch)" :key="si">
            <ChessBoardDisplay v-if="seg.type === 'board'" :data="seg.board" />
            <div v-else-if="seg.type === 'markdown'" v-html="seg.html"></div>
          </template>
        </section>
      </template>
    </article>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import { marked } from 'marked';
import ChessBoardDisplay from '@/views/chat/components/tool-results/ChessBoardDisplay.vue';
import type { ChessBoardData } from '@/types/tool-results';
import { splitChessSegments } from '@/utils/chessBlocks';
import { safeMarkdownToHTML, sanitizeHTML } from '@/utils/security';
import { getBook, listChapters, type ChessBook, type ChessBookChapter } from '@/api/chess';
import { bookLevelLabel, bookPhaseLabel } from '@/utils/chessBookOptions';

// Trang IN sách: mọi chương trên một trang dài, mở tab mới rồi Ctrl+P → PDF.
// Không dùng generator PDF server-side (repo chưa có) — @media print lo layout.
const route = useRoute();
const loading = ref(true);
const book = ref<ChessBook | null>(null);
const chapters = ref<ChessBookChapter[]>([]);

const chapterGroups = computed(() => {
  const groups: { part: string; items: ChessBookChapter[] }[] = [];
  let last: string | null = null;
  for (const ch of chapters.value) {
    const part = ch.part || '';
    if (last === null || part !== last || groups.length === 0) groups.push({ part, items: [] });
    groups[groups.length - 1].items.push(ch);
    last = part;
  }
  return groups;
});

const START_FEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';
function chapterBoardData(ch: ChessBookChapter): ChessBoardData {
  return { display_type: 'chess_board', fen: ch.fen || START_FEN, caption: ch.title };
}
function renderMd(md: string): string {
  return sanitizeHTML(marked.parse(safeMarkdownToHTML(md || ''), { breaks: true, async: false }) as string);
}
// KHÔNG dùng renderChessChips ở đây — bản in tĩnh, chip [[...]] chỉ cần hiện nhãn.
function stripWikilinkSyntax(md: string): string {
  return (md || '').replace(/!?\[\[([^\]]+)\]\]/g, (_m, inner: string) => {
    const pipe = inner.indexOf('|');
    return (pipe > 0 ? inner.slice(pipe + 1) : inner).trim();
  });
}
function chapterSegments(ch: ChessBookChapter) {
  return splitChessSegments(ch.content || '').map((seg) => {
    if (seg.type === 'board') return { type: 'board' as const, board: seg.board! };
    return { type: 'markdown' as const, html: renderMd(stripWikilinkSyntax(seg.markdown || '')) };
  });
}

function doPrint() { window.print(); }

onMounted(async () => {
  const id = String(route.params.id || '');
  if (!id) { loading.value = false; return; }
  try {
    const [br, cr]: any[] = await Promise.all([getBook(id), listChapters(id)]);
    book.value = br?.data || null;
    chapters.value = cr?.data || [];
  } finally {
    loading.value = false;
  }
});
</script>

<style lang="less" scoped>
.bkp { max-width: 820px; margin: 0 auto; padding: 24px; }
.bkp-toolbar { position: sticky; top: 0; padding: 10px 0; background: var(--td-bg-color-page); z-index: 10; }
.bkp-empty { color: var(--td-text-color-placeholder); padding: 40px; text-align: center; }
.bkp-page { color: var(--td-text-color-primary); line-height: 1.7; }
.bkp-subtitle { color: var(--td-text-color-secondary); font-style: italic; }
.bkp-meta { font-size: 14px; color: var(--td-text-color-secondary); padding-left: 18px; }
.bkp-description { margin: 16px 0; padding: 12px; background: var(--td-bg-color-container); border-radius: 8px; }
.bkp-part { margin-top: 32px; border-bottom: 2px solid var(--td-brand-color); padding-bottom: 6px; }
.bkp-chapter { margin: 24px 0; break-inside: avoid-page; }

@media print {
  .no-print { display: none !important; }
  .bkp { max-width: 100%; padding: 0; }
  .bkp-chapter { break-inside: avoid; page-break-inside: avoid; }
}
</style>
