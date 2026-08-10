<template>
  <div class="bkl">
    <div class="bkl-left">
      <div class="bkl-toolbar">
        <t-select v-model="filter.shelf_id" :options="shelfSelectOptions" placeholder="Kệ" clearable
          style="width:140px" @change="loadBooks" />
        <t-select v-model="filter.level" :options="bookLevelOptions" placeholder="Cấp độ" clearable
          style="width:110px" @change="loadBooks" />
        <t-select v-model="filter.phase" :options="bookPhaseOptions" placeholder="Giai đoạn" clearable
          style="width:130px" @change="loadBooks" />
        <t-select v-model="filter.status" :options="bookStatusOptions" placeholder="Trạng thái" clearable
          style="width:130px" @change="loadBooks" />
        <t-input v-model="filter.q" placeholder="Tìm theo tên/tác giả…" clearable style="width:170px" @change="loadBooks" />
      </div>
      <div class="bkl-toolbar">
        <t-button variant="outline" size="small" @click="shelfManagerVisible = true">
          <template #icon><t-icon name="layers" /></template>Quản lý kệ
        </t-button>
        <div style="flex:1"></div>
        <t-button variant="outline" size="small" @click="doExportBooks">
          <template #icon><t-icon name="download" /></template>Export
        </t-button>
        <t-button variant="outline" size="small" @click="doImportBooks">
          <template #icon><t-icon name="upload" /></template>Import
        </t-button>
        <t-button theme="primary" size="small" @click="openBookDialog()">
          <template #icon><t-icon name="add" /></template>Tạo sách
        </t-button>
      </div>

      <div v-if="books.length === 0" class="bkl-empty">Chưa có sách. Nhấn "Tạo sách".</div>
      <div v-for="b in books" :key="b.id" class="bkl-row" :class="{ active: selectedBook && selectedBook.id === b.id }"
        @click="selectBook(b)">
        <div class="bkl-row-main">
          <div class="bkl-row-title">{{ b.title }}</div>
          <div v-if="b.subtitle" class="bkl-row-subtitle">{{ b.subtitle }}</div>
          <div class="bkl-row-meta">
            <span class="bkl-tag" :class="b.status === 'published' ? 'bkl-tag--published' : 'bkl-tag--draft'">
              {{ bookStatusLabel(b.status) }}
            </span>
            <span v-if="b.level" class="bkl-tag">{{ bookLevelLabel(b.level) }}</span>
            <span v-if="b.phase" class="bkl-tag bkl-tag--phase">{{ bookPhaseLabel(b.phase) }}</span>
            <span class="bkl-count">{{ b.chapter_count || 0 }} chương</span>
          </div>
          <div v-if="b.author" class="bkl-row-author">{{ b.author }}<span v-if="b.year"> · {{ b.year }}</span></div>
        </div>
        <div class="bkl-row-actions">
          <t-button size="small" variant="text" :title="t('chess.ref.copyLink')" @click.stop="copyBookWikilink(b)">
            <t-icon name="link" />
          </t-button>
          <t-button size="small" variant="text" title="Đổi slug" @click.stop="renameBookSlugPrompt(b)"><t-icon name="tag" /></t-button>
          <t-button size="small" variant="text" @click.stop="openBookDialog(b)"><t-icon name="edit" /></t-button>
          <t-button size="small" variant="text" theme="danger" @click.stop="removeBook(b)"><t-icon name="delete" /></t-button>
        </div>
      </div>
    </div>

    <div class="bkl-right">
      <template v-if="selectedBook">
        <div class="bkl-header">
          <div>
            <h2>{{ selectedBook.title }}</h2>
            <div v-if="selectedBook.subtitle" class="bkl-subtitle">{{ selectedBook.subtitle }}</div>
            <div class="bkl-header-meta">
              <span class="bkl-tag" :class="selectedBook.status === 'published' ? 'bkl-tag--published' : 'bkl-tag--draft'">
                {{ bookStatusLabel(selectedBook.status) }}
              </span>
              <span v-if="selectedBook.author">{{ selectedBook.author }}</span>
              <span v-if="selectedBook.publisher">{{ selectedBook.publisher }}</span>
              <span v-if="selectedBook.year">{{ selectedBook.year }}</span>
            </div>
          </div>
          <div class="bkl-header-actions">
            <t-button variant="outline" size="small" @click="doExportMarkdown">
              <template #icon><t-icon name="download" /></template>Markdown
            </t-button>
            <t-button variant="outline" size="small" @click="openPrintView">
              <template #icon><t-icon name="print" /></template>Trang in
            </t-button>
            <t-button theme="primary" size="small" @click="openChapterDialog()">
              <template #icon><t-icon name="add" /></template>Thêm chương
            </t-button>
          </div>
        </div>

        <ChessBacklinks ref-type="book" :slug="selectedBook.slug" show-empty />
        <div v-if="bookShelves.length" class="bkl-shelves">
          Kệ:
          <span v-for="s in bookShelves" :key="s.id" class="bkl-shelf-chip">{{ s.title }}</span>
        </div>
        <div v-if="selectedBook.description" class="bkl-description" v-html="renderDescription(selectedBook.description)"></div>

        <div v-if="chapters.length === 0" class="bkl-empty">Chưa có chương.</div>
        <template v-for="(group, gi) in chapterGroups" :key="gi">
          <div v-if="group.part" class="bkl-part-title">{{ group.part }}</div>
          <div v-for="ch in group.items" :key="ch.id" class="bkl-chapter" :data-chapter-id="ch.id">
            <div class="bkl-chapter-head" @click="toggleChapter(ch.id)">
              <span class="bkl-chapter-title">{{ ch.title }}</span>
              <span class="bkl-chapter-actions">
                <t-button size="small" variant="text" :title="t('chess.ref.copyLink')" @click.stop="copyChapterWikilink(ch)"><t-icon name="link" /></t-button>
                <t-button size="small" variant="text" title="Đổi slug" @click.stop="renameChapterSlugPrompt(ch)"><t-icon name="tag" /></t-button>
                <t-button size="small" variant="text" title="Lịch sử phiên bản" @click.stop="openHistory(ch)"><t-icon name="history" /></t-button>
                <t-button size="small" variant="text" title="Lên" @click.stop="moveChapter(ch, -1)"><t-icon name="chevron-up" /></t-button>
                <t-button size="small" variant="text" title="Xuống" @click.stop="moveChapter(ch, 1)"><t-icon name="chevron-down" /></t-button>
                <t-button size="small" variant="text" @click.stop="openChapterDialog(ch)"><t-icon name="edit" /></t-button>
                <t-button size="small" variant="text" theme="danger" @click.stop="removeChapter(ch)"><t-icon name="delete" /></t-button>
                <t-icon :name="expandedChapters.has(ch.id) ? 'chevron-up' : 'chevron-down'" />
              </span>
            </div>
            <div v-if="expandedChapters.has(ch.id)" class="bkl-chapter-body">
              <div class="bkl-chapter-content" v-if="ch.content" @click="onChapterContentClick">
                <template v-for="(seg, si) in chapterSegments(ch)" :key="si">
                  <ChessBoardDisplay v-if="seg.type === 'board'" :data="seg.board" />
                  <ChessRefEmbed v-else-if="seg.type === 'ref'" :ref-str="seg.refStr" />
                  <div v-else class="bkl-md-segment" v-html="seg.html"></div>
                </template>
              </div>
              <ChessBoardDisplay v-if="ch.fen && !ch.content" :data="chapterBoardData(ch)" />
              <ChessBacklinks v-if="ch.slug" ref-type="chapter" :slug="ch.slug" />
            </div>
          </div>
        </template>
      </template>
      <div v-else class="bkl-empty bkl-empty--big">Chọn một sách bên trái để xem chương.</div>
    </div>

    <!-- Dialog sách -->
    <t-dialog v-model:visible="bookDialog.visible" :header="bookDialog.id ? 'Sửa sách' : 'Tạo sách'"
      :on-confirm="saveBook" width="600px">
      <div class="bkl-form">
        <label>Tên sách *</label>
        <t-input v-model="bookDialog.title" placeholder="VD: STEP Trainer — Tập 1" />
        <label>Phụ đề</label>
        <t-input v-model="bookDialog.subtitle" />
        <div class="bkl-row2">
          <div><label>Tác giả</label><t-input v-model="bookDialog.author" /></div>
          <div><label>Dịch giả</label><t-input v-model="bookDialog.translator" /></div>
        </div>
        <div class="bkl-row2">
          <div><label>Nhà xuất bản</label><t-input v-model="bookDialog.publisher" /></div>
          <div><label>Năm</label><t-input v-model="bookDialog.year" placeholder="2024" /></div>
        </div>
        <div class="bkl-row2">
          <div><label>ISBN</label><t-input v-model="bookDialog.isbn" /></div>
          <div><label>Ngôn ngữ</label><t-input v-model="bookDialog.language" placeholder="vi" /></div>
        </div>
        <div class="bkl-row2">
          <div><label>Cấp độ</label><t-select v-model="bookDialog.level" :options="bookLevelOptions" clearable /></div>
          <div><label>Giai đoạn</label><t-select v-model="bookDialog.phase" :options="bookPhaseOptions" clearable /></div>
        </div>
        <div class="bkl-row2">
          <div><label>Mã khai cuộc (ECO)</label><t-input v-model="bookDialog.eco" placeholder="B90" /></div>
          <div>
            <label>Trạng thái</label>
            <t-select v-model="bookDialog.status" :options="bookStatusOptions" />
          </div>
        </div>
        <label>Thẻ (cách nhau dấu phẩy)</label>
        <t-input v-model="bookDialog.tags" />
        <label>Ảnh bìa (URL)</label>
        <t-input v-model="bookDialog.cover_url" />
        <label>Mô tả</label>
        <t-textarea v-model="bookDialog.description" :autosize="{ minRows: 3 }" />
        <label>Thứ tự</label>
        <t-input-number v-model="bookDialog.sort_order" />
      </div>
    </t-dialog>

    <!-- Dialog chương -->
    <t-dialog v-model:visible="chapterDialog.visible" :header="chapterDialog.id ? 'Sửa chương' : 'Thêm chương'"
      :on-confirm="saveChapter" width="720px">
      <div class="bkl-form">
        <div class="bkl-row2">
          <div><label>Phần (tùy chọn, để gộp hiển thị)</label><t-input v-model="chapterDialog.part" placeholder="VD: Phần I — Tàn cuộc Tốt" /></div>
          <div><label>Cấp độ</label><t-select v-model="chapterDialog.level" :options="bookLevelOptions" clearable /></div>
        </div>
        <label>Tên chương *</label>
        <t-input v-model="chapterDialog.title" />
        <label>Thế cờ FEN minh họa (tùy chọn)</label>
        <t-input v-model="chapterDialog.fen" placeholder="rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1" />

        <div class="bkl-content-label">
          <label>Nội dung (markdown)</label>
          <div class="bkl-content-actions">
            <t-button size="small" variant="outline" @click="openPicker">
              <template #icon><t-icon name="chess" /></template>{{ t('chess.ref.insert') }}
            </t-button>
          </div>
        </div>
        <div class="bkl-editor-toolbar">
          <t-button v-for="btn in toolbarButtons" :key="btn.key" size="small" variant="text" :title="btn.title" @click="btn.action">
            <t-icon :name="btn.icon" />
          </t-button>
          <input ref="imageInputEl" type="file" accept="image/png,image/jpeg,image/gif,image/webp" style="display:none" @change="onImageInputChange" />
        </div>
        <t-textarea ref="chapterContentRef" v-model="chapterDialog.content" :autosize="{ minRows: 10 }"
          placeholder="Nội dung chương… Gõ [[ để gợi ý liên kết. Dán/kéo-thả ảnh để chèn trực tiếp." />
        <ChessWikiLinkSuggest :textarea="chapterTextareaEl" v-model="chapterDialog.content" />

        <template v-if="chapterDialog.id">
          <label>Ghi chú thay đổi (tùy chọn — lưu kèm bản phiên bản mới)</label>
          <t-input v-model="chapterDialog.summary" placeholder="VD: sửa lỗi chính tả, bổ sung ví dụ" />
        </template>
      </div>
    </t-dialog>

    <!-- Bộ chọn chèn tham chiếu cờ -->
    <t-dialog v-model:visible="picker.visible" :header="t('chess.ref.pickerTitle')" :footer="false" width="820px">
      <div class="bkl-picker">
        <t-tabs v-model="picker.tab">
          <t-tab-panel value="games" :label="t('chess.ref.tabGames')" />
          <t-tab-panel value="positions" :label="t('chess.ref.tabPositions')" />
          <t-tab-panel value="puzzles" :label="t('chess.ref.tabPuzzles')" />
          <t-tab-panel value="books" :label="t('chess.ref.tabBooks')" />
          <t-tab-panel value="chapters" :label="t('chess.ref.tabChapters')" />
        </t-tabs>
        <div class="bkl-picker-bar">
          <t-input v-model="picker.search" :placeholder="t('chess.ref.searchPlaceholder')" clearable />
          <t-checkbox v-model="picker.embed">{{ t('chess.ref.embedToggle') }}</t-checkbox>
        </div>
        <div class="bkl-picker-body">
          <div class="bkl-picker-list">
            <div v-if="picker.loading" class="bkl-empty">{{ t('chess.ref.loading') }}</div>
            <div v-else-if="pickerItems.length === 0" class="bkl-empty">{{ t('chess.ref.empty') }}</div>
            <div v-for="it in pickerItems" :key="it.type + '/' + it.slug" class="bkl-picker-row"
              :class="{ active: picker.previewRef === it.type + '/' + it.slug }"
              @mouseenter="previewItem(it)" @click="pickerInsert(it)">
              <span class="bkl-picker-label">{{ it.label }}</span>
              <span class="bkl-picker-slug">{{ it.type }}/{{ it.slug }}</span>
              <t-button size="small" variant="text" theme="primary">{{ t('chess.ref.insertAction') }}</t-button>
            </div>
          </div>
          <div class="bkl-picker-preview">
            <div v-if="picker.previewLoading" class="bkl-empty">{{ t('chess.ref.loading') }}</div>
            <template v-else-if="picker.previewRef">
              <div class="bkl-preview-title">{{ picker.previewTitle }}</div>
              <ChessBoardDisplay v-if="picker.previewBoard" :key="picker.previewRef" :data="picker.previewBoard" />
              <div v-else class="bkl-empty">{{ t('chess.ref.noBoard') }}</div>
            </template>
            <div v-else class="bkl-empty">{{ t('chess.ref.previewHint') }}</div>
          </div>
        </div>
      </div>
    </t-dialog>

    <ChessRefDialog v-model:visible="refDialog.visible" :ref-str="refDialog.refStr" />
    <ChessShelfManager v-model:visible="shelfManagerVisible" @changed="loadShelves" />
    <ChessChapterHistory v-model:visible="historyVisible" :chapter-id="historyChapterId" @restored="onRestored" />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, nextTick, watch } from 'vue';
import { marked } from 'marked';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import ChessBoardDisplay from '@/views/chat/components/tool-results/ChessBoardDisplay.vue';
import ChessRefEmbed from '@/views/chess/components/ChessRefEmbed.vue';
import ChessRefDialog from '@/views/chess/components/ChessRefDialog.vue';
import ChessBacklinks from '@/views/chess/components/ChessBacklinks.vue';
import ChessWikiLinkSuggest from '@/views/chess/components/ChessWikiLinkSuggest.vue';
import ChessShelfManager from '@/views/chess/components/ChessShelfManager.vue';
import ChessChapterHistory from '@/views/chess/components/ChessChapterHistory.vue';
import type { ChessBoardData } from '@/types/tool-results';
import { splitChessContent, renderChessChips, isValidFEN } from '@/utils/chessBlocks';
import { resolveChessRef } from '@/utils/chessRef';
import { safeMarkdownToHTML, sanitizeHTML } from '@/utils/security';
import {
  listShelves, listBooks, getBook, getBookBySlug, createBook, updateBook, renameBookSlug, deleteBook,
  exportBooks, importBooks, listShelvesOfBook, uploadBookImage,
  listChapters, createChapter, updateChapter, getChapterBySlug, renameChapterSlug, deleteChapter, reorderChapters,
  listGames, listPuzzles, listPositions,
  type ChessShelf, type ChessBook, type ChessBookChapter, type ChessGame, type ChessPuzzle, type ChessPosition,
} from '@/api/chess';
import { downloadText, pickTextFile } from '@/utils/fileTransfer';
import {
  bookLevelOptions, bookPhaseOptions, bookStatusOptions, bookLevelLabel, bookPhaseLabel, bookStatusLabel,
} from '@/utils/chessBookOptions';

const { t } = useI18n();
const router = useRouter();

// Deep-link "Mở trong thư viện": chọn sẵn sách theo slug, hoặc mở sách chứa
// chương + bung chương đó (từ [[book/<slug>]] / [[chapter/<slug>]]).
const props = defineProps<{ focusBookSlug?: string; focusChapterSlug?: string }>();
async function focusBook(slug?: string) {
  if (!slug) return;
  try {
    const res: any = await getBookBySlug(slug);
    const b = res?.data;
    if (!b) return;
    if (books.value.length === 0) await loadBooks();
    const book = books.value.find((x) => x.id === b.id) || b;
    await selectBook(book);
  } catch { /* không tìm thấy → bỏ qua */ }
}
async function focusChapter(slug?: string) {
  if (!slug) return;
  try {
    const res: any = await getChapterBySlug(slug);
    const ch = res?.data;
    if (!ch) return;
    if (books.value.length === 0) await loadBooks();
    let book = books.value.find((b) => b.id === ch.book_id) || null;
    if (!book) {
      const br: any = await getBook(ch.book_id);
      book = br?.data || null;
    }
    if (!book) return;
    await selectBook(book);
    expandedChapters.value = new Set([ch.id]);
    await nextTick();
    document.querySelector(`[data-chapter-id="${ch.id}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  } catch { /* không tìm thấy → bỏ qua */ }
}
watch(() => props.focusBookSlug, (s) => focusBook(s));
watch(() => props.focusChapterSlug, (s) => focusChapter(s));

const shelves = ref<ChessShelf[]>([]);
const shelfSelectOptions = computed(() => shelves.value.map((s) => ({ label: s.title, value: s.id })));
async function loadShelves() {
  try { const res: any = await listShelves(); shelves.value = res?.data || []; } catch { /* im lặng — không chặn trang */ }
}

const books = ref<ChessBook[]>([]);
const selectedBook = ref<ChessBook | null>(null);
const chapters = ref<ChessBookChapter[]>([]);
const bookShelves = ref<ChessShelf[]>([]);
const expandedChapters = ref<Set<string>>(new Set());
const filter = reactive({ shelf_id: '', level: '', phase: '', status: '', q: '' });
const shelfManagerVisible = ref(false);

async function loadBooks() {
  try {
    const res: any = await listBooks(filter);
    books.value = res?.data || [];
  } catch { MessagePlugin.error('Tải danh sách sách thất bại'); }
}

async function selectBook(b: ChessBook) {
  selectedBook.value = b;
  expandedChapters.value = new Set();
  try {
    const res: any = await listChapters(b.id);
    chapters.value = res?.data || [];
  } catch { chapters.value = []; }
  try {
    const res2: any = await listShelvesOfBook(b.id);
    bookShelves.value = res2?.data || [];
  } catch { bookShelves.value = []; }
}
function toggleChapter(id: string) {
  const next = new Set(expandedChapters.value);
  next.has(id) ? next.delete(id) : next.add(id);
  expandedChapters.value = next;
}

// Gộp chương THEO THỨ TỰ ĐÃ SẮP theo "Phần" liền kề (không gom rời rạc) — khớp
// đúng logic buildBookKnowledgeText ở backend để mục lục hiển thị nhất quán.
const chapterGroups = computed(() => {
  const groups: { part: string; items: ChessBookChapter[] }[] = [];
  let last: string | null = null;
  for (const ch of chapters.value) {
    const part = ch.part || '';
    if (last === null || part !== last || groups.length === 0) {
      groups.push({ part, items: [] });
    }
    groups[groups.length - 1].items.push(ch);
    last = part;
  }
  return groups;
});

const START_FEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';
function chapterBoardData(ch: ChessBookChapter): ChessBoardData {
  return { display_type: 'chess_board', fen: ch.fen || START_FEN, caption: ch.title };
}

// ---- Render nội dung: sanitize (khác ChessCourses.vue dùng marked.parse trần
// — sách là nội dung dài, nhiều nguồn, cần khử XSS chặt hơn). ----
function renderChapterMarkdown(md: string): string {
  const pre = renderChessChips(md || '');
  const html = marked.parse(safeMarkdownToHTML(pre), { breaks: true, async: false }) as string;
  return sanitizeHTML(html);
}
function chapterSegments(ch: ChessBookChapter) {
  return splitChessContent(ch.content || '').map((seg) => {
    if (seg.type === 'board') return { type: 'board' as const, board: seg.board! };
    if (seg.type === 'ref') return { type: 'ref' as const, refStr: seg.ref!.ref };
    return { type: 'markdown' as const, html: renderChapterMarkdown(seg.markdown || '') };
  });
}
function renderDescription(md: string): string {
  return sanitizeHTML(marked.parse(safeMarkdownToHTML(md || ''), { breaks: true, async: false }) as string);
}
const refDialog = reactive<{ visible: boolean; refStr: string }>({ visible: false, refStr: '' });
function onChapterContentClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a.chess-ref-link') as HTMLElement | null;
  if (!a) return;
  e.preventDefault();
  const ref = a.getAttribute('data-chess-ref');
  if (ref) { refDialog.refStr = ref; refDialog.visible = true; }
}

// ---- Dialog sách ----
interface BookDialogState {
  visible: boolean; id: string; title: string; subtitle: string; author: string; translator: string;
  publisher: string; year: string; isbn: string; language: string; level: string; phase: string;
  eco: string; status: string; description: string; cover_url: string; tags: string; sort_order: number;
}
const bookDialog = reactive<BookDialogState>({
  visible: false, id: '', title: '', subtitle: '', author: '', translator: '', publisher: '', year: '',
  isbn: '', language: '', level: '', phase: '', eco: '', status: 'draft', description: '', cover_url: '',
  tags: '', sort_order: 0,
});
function openBookDialog(b?: ChessBook) {
  bookDialog.visible = true;
  bookDialog.id = b?.id || '';
  bookDialog.title = b?.title || '';
  bookDialog.subtitle = b?.subtitle || '';
  bookDialog.author = b?.author || '';
  bookDialog.translator = b?.translator || '';
  bookDialog.publisher = b?.publisher || '';
  bookDialog.year = b?.year || '';
  bookDialog.isbn = b?.isbn || '';
  bookDialog.language = b?.language || '';
  bookDialog.level = b?.level || '';
  bookDialog.phase = b?.phase || '';
  bookDialog.eco = b?.eco || '';
  bookDialog.status = b?.status || 'draft';
  bookDialog.description = b?.description || '';
  bookDialog.cover_url = b?.cover_url || '';
  bookDialog.tags = b?.tags || '';
  bookDialog.sort_order = b?.sort_order || 0;
}
async function saveBook() {
  if (!bookDialog.title.trim()) { MessagePlugin.warning('Nhập tên sách'); return; }
  const payload = {
    title: bookDialog.title, subtitle: bookDialog.subtitle, author: bookDialog.author,
    translator: bookDialog.translator, publisher: bookDialog.publisher, year: bookDialog.year,
    isbn: bookDialog.isbn, language: bookDialog.language, level: bookDialog.level, phase: bookDialog.phase,
    eco: bookDialog.eco, status: bookDialog.status, description: bookDialog.description,
    cover_url: bookDialog.cover_url, tags: bookDialog.tags, sort_order: bookDialog.sort_order,
  };
  try {
    const editingId = bookDialog.id;
    if (editingId) await updateBook(editingId, payload);
    else await createBook(payload);
    bookDialog.visible = false;
    await loadBooks();
    if (editingId && selectedBook.value?.id === editingId) {
      const updated = books.value.find((b) => b.id === editingId);
      if (updated) selectedBook.value = updated;
    }
    MessagePlugin.success('Đã lưu sách');
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Lưu thất bại');
  }
}
async function copyBookWikilink(b: ChessBook) {
  if (!b.slug) { MessagePlugin.warning('Sách chưa có slug'); return; }
  const link = `[[book/${b.slug}|${b.title || b.slug}]]`;
  try { await navigator.clipboard.writeText(link); MessagePlugin.success(t('chess.ref.copied')); }
  catch { MessagePlugin.info(link); }
}
async function renameBookSlugPrompt(b: ChessBook) {
  if (!b.slug) { MessagePlugin.warning('Sách chưa có slug'); return; }
  const next = window.prompt(`Đổi slug cho "${b.title || b.slug}" (link cũ vẫn sống nhờ alias):`, b.slug);
  if (next == null) return;
  const v = next.trim();
  if (!v || v === b.slug) return;
  try {
    const res: any = await renameBookSlug(b.id, v);
    await loadBooks();
    if (selectedBook.value?.id === b.id && res?.data) selectedBook.value = res.data;
    MessagePlugin.success(`Đã đổi slug → ${res?.data?.slug || v}`);
  } catch (e: any) { MessagePlugin.error(e?.error || e?.message || 'Đổi slug thất bại'); }
}
function removeBook(b: ChessBook) {
  DialogPlugin.confirm({
    header: 'Xóa sách', body: `Xóa "${b.title}" và TOÀN BỘ chương, lịch sử phiên bản, ảnh đã chèn?`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteBook(b.id);
        if (selectedBook.value?.id === b.id) { selectedBook.value = null; chapters.value = []; bookShelves.value = []; }
        await loadBooks();
        MessagePlugin.success('Đã xóa');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}

// ---- Export / Import sách ----
async function doExportBooks() {
  try {
    const res: any = await exportBooks(filter);
    const items = res?.data || [];
    if (!items.length) { MessagePlugin.info('Không có sách để xuất'); return; }
    downloadText(`sach-${new Date().toISOString().slice(0, 10)}.json`, JSON.stringify(items, null, 2), 'application/json');
    MessagePlugin.success(`Đã xuất ${items.length} sách`);
  } catch { MessagePlugin.error('Xuất thất bại'); }
}
async function doImportBooks() {
  const text = await pickTextFile('.json,application/json');
  if (text == null) return;
  let arr: any;
  try { arr = JSON.parse(text); } catch { MessagePlugin.error('File JSON không hợp lệ'); return; }
  if (!Array.isArray(arr)) { MessagePlugin.error('File phải là một mảng JSON sách'); return; }
  try {
    const res: any = await importBooks(arr);
    await loadBooks();
    MessagePlugin.success(`Đã nhập ${res?.data?.imported || 0} sách`);
  } catch (e: any) { MessagePlugin.error(e?.error || e?.message || 'Import thất bại'); }
}

// Xuất Markdown 1 file (client-side, từ dữ liệu đã tải — không gọi API riêng).
function buildBookMarkdown(b: ChessBook, chs: ChessBookChapter[]): string {
  let out = `# ${b.title}\n\n`;
  if (b.subtitle) out += `*${b.subtitle}*\n\n`;
  const meta: string[] = [];
  if (b.author) meta.push(`- Tác giả: ${b.author}`);
  if (b.translator) meta.push(`- Dịch giả: ${b.translator}`);
  if (b.publisher) meta.push(`- Nhà xuất bản: ${b.publisher}`);
  if (b.year) meta.push(`- Năm: ${b.year}`);
  if (b.level) meta.push(`- Cấp độ: ${bookLevelLabel(b.level)}`);
  if (b.phase) meta.push(`- Giai đoạn: ${bookPhaseLabel(b.phase)}`);
  if (meta.length) out += meta.join('\n') + '\n\n';
  if (b.description) out += `${b.description}\n\n`;
  let lastPart = ' ';
  for (const ch of chs) {
    const part = ch.part || '';
    if (part !== lastPart) {
      if (part) out += `## ${part}\n\n`;
      lastPart = part;
    }
    out += `### ${ch.title}\n\n`;
    if (ch.fen) out += `\`\`\`chess\n${ch.fen}\n\`\`\`\n\n`;
    out += `${ch.content || ''}\n\n`;
  }
  return out;
}
function doExportMarkdown() {
  if (!selectedBook.value) return;
  const md = buildBookMarkdown(selectedBook.value, chapters.value);
  downloadText(`${selectedBook.value.slug || 'sach'}.md`, md, 'text/markdown;charset=utf-8');
}
function openPrintView() {
  if (!selectedBook.value) return;
  const routeData = router.resolve({ name: 'chessBookPrint', params: { id: selectedBook.value.id } });
  window.open(routeData.href, '_blank');
}

// ---- Dialog chương ----
interface ChapterDialogState {
  visible: boolean; id: string; part: string; title: string; content: string; fen: string; level: string; summary: string;
}
const chapterDialog = reactive<ChapterDialogState>({
  visible: false, id: '', part: '', title: '', content: '', fen: '', level: '', summary: '',
});
function openChapterDialog(ch?: ChessBookChapter) {
  chapterDialog.visible = true;
  chapterDialog.id = ch?.id || '';
  chapterDialog.part = ch?.part || '';
  chapterDialog.title = ch?.title || '';
  chapterDialog.content = ch?.content || '';
  chapterDialog.fen = ch?.fen || '';
  chapterDialog.level = ch?.level || '';
  chapterDialog.summary = '';
}
async function saveChapter() {
  if (!selectedBook.value) return;
  if (!chapterDialog.title.trim()) { MessagePlugin.warning('Nhập tên chương'); return; }
  if (chapterDialog.fen.trim() && !isValidFEN(chapterDialog.fen)) {
    MessagePlugin.warning('Thế cờ FEN không hợp lệ — kiểm tra lại hoặc để trống.');
    return;
  }
  try {
    if (chapterDialog.id) {
      await updateChapter(chapterDialog.id, {
        part: chapterDialog.part, title: chapterDialog.title, content: chapterDialog.content,
        fen: chapterDialog.fen, level: chapterDialog.level, summary: chapterDialog.summary,
      });
    } else {
      await createChapter(selectedBook.value.id, {
        part: chapterDialog.part, title: chapterDialog.title, content: chapterDialog.content,
        fen: chapterDialog.fen, level: chapterDialog.level,
      });
    }
    chapterDialog.visible = false;
    await selectBook(selectedBook.value);
    await loadBooks();
    MessagePlugin.success('Đã lưu chương');
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Lưu thất bại');
  }
}
function removeChapter(ch: ChessBookChapter) {
  DialogPlugin.confirm({
    header: 'Xóa chương', body: `Xóa "${ch.title}"?`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteChapter(ch.id);
        if (selectedBook.value) await selectBook(selectedBook.value);
        await loadBooks();
        MessagePlugin.success('Đã xóa');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}
async function moveChapter(ch: ChessBookChapter, dir: -1 | 1) {
  if (!selectedBook.value) return;
  const idx = chapters.value.findIndex((c) => c.id === ch.id);
  const swapIdx = idx + dir;
  if (idx < 0 || swapIdx < 0 || swapIdx >= chapters.value.length) return;
  const ids = chapters.value.map((c) => c.id);
  [ids[idx], ids[swapIdx]] = [ids[swapIdx], ids[idx]];
  try {
    await reorderChapters(selectedBook.value.id, ids);
    await selectBook(selectedBook.value);
  } catch { MessagePlugin.error('Đổi thứ tự thất bại'); }
}
async function renameChapterSlugPrompt(ch: ChessBookChapter) {
  if (!ch.slug) { MessagePlugin.warning('Chương chưa có slug'); return; }
  const next = window.prompt(`Đổi slug cho "${ch.title || ch.slug}" (link cũ vẫn sống nhờ alias):`, ch.slug);
  if (next == null) return;
  const v = next.trim();
  if (!v || v === ch.slug) return;
  try {
    await renameChapterSlug(ch.id, v);
    if (selectedBook.value) await selectBook(selectedBook.value);
    MessagePlugin.success('Đã đổi slug');
  } catch (e: any) { MessagePlugin.error(e?.error || e?.message || 'Đổi slug thất bại'); }
}
async function copyChapterWikilink(ch: ChessBookChapter) {
  if (!ch.slug) { MessagePlugin.warning('Chương chưa có slug'); return; }
  const link = `[[chapter/${ch.slug}|${ch.title || ch.slug}]]`;
  try { await navigator.clipboard.writeText(link); MessagePlugin.success(t('chess.ref.copied')); }
  catch { MessagePlugin.info(link); }
}

// ---- Lịch sử phiên bản ----
const historyVisible = ref(false);
const historyChapterId = ref('');
function openHistory(ch: ChessBookChapter) {
  if (!ch.id) return;
  historyChapterId.value = ch.id;
  historyVisible.value = true;
}
async function onRestored() {
  if (selectedBook.value) await selectBook(selectedBook.value);
}

// ---- Ô soạn nội dung: autocomplete "[[" + toolbar + dán/kéo-thả ảnh ----
const chapterContentRef = ref<any>(null);
const chapterTextareaEl = ref<HTMLTextAreaElement | null>(null);
const imageInputEl = ref<HTMLInputElement | null>(null);
let boundTextarea: HTMLTextAreaElement | null = null;

function getTextarea(): HTMLTextAreaElement | null {
  const root = chapterContentRef.value?.$el || chapterContentRef.value;
  return (root?.querySelector?.('textarea') as HTMLTextAreaElement) || null;
}

async function uploadAndInsertImage(file: File) {
  if (!selectedBook.value) return;
  try {
    const res: any = await uploadBookImage(selectedBook.value.id, file);
    const url = res?.data?.url;
    if (url) insertAtCursor(`![${file.name}](${url})`);
  } catch (e: any) {
    MessagePlugin.error(e?.error || e?.message || 'Tải ảnh thất bại');
  }
}
function onImageInputChange(e: Event) {
  const files = (e.target as HTMLInputElement).files;
  if (files && files[0]) uploadAndInsertImage(files[0]);
  (e.target as HTMLInputElement).value = '';
}
function onPaste(e: ClipboardEvent) {
  const items = e.clipboardData?.items;
  if (!items) return;
  for (const item of Array.from(items)) {
    if (item.type.startsWith('image/')) {
      const file = item.getAsFile();
      if (file) { e.preventDefault(); uploadAndInsertImage(file); }
      return;
    }
  }
}
function onDragOver(e: DragEvent) { e.preventDefault(); }
function onDrop(e: DragEvent) {
  const file = e.dataTransfer?.files?.[0];
  if (file && file.type.startsWith('image/')) { e.preventDefault(); uploadAndInsertImage(file); }
}

watch(() => chapterDialog.visible, async (open) => {
  if (!open) { chapterTextareaEl.value = null; return; }
  await nextTick();
  const ta = getTextarea();
  chapterTextareaEl.value = ta;
  if (ta && ta !== boundTextarea) {
    if (boundTextarea) {
      boundTextarea.removeEventListener('paste', onPaste);
      boundTextarea.removeEventListener('drop', onDrop);
      boundTextarea.removeEventListener('dragover', onDragOver);
    }
    ta.addEventListener('paste', onPaste);
    ta.addEventListener('drop', onDrop);
    ta.addEventListener('dragover', onDragOver);
    boundTextarea = ta;
  }
});

function insertAtCursor(text: string) {
  const ta = getTextarea();
  if (!ta) { chapterDialog.content = (chapterDialog.content || '') + text; return; }
  const start = ta.selectionStart ?? chapterDialog.content.length;
  const end = ta.selectionEnd ?? start;
  const v = chapterDialog.content || '';
  chapterDialog.content = v.slice(0, start) + text + v.slice(end);
  nextTick(() => { ta.focus(); const pos = start + text.length; ta.setSelectionRange(pos, pos); });
}
function wrapSelectionSimple(prefix: string, suffix: string, placeholder: string) {
  const ta = getTextarea();
  const v = chapterDialog.content || '';
  const start = ta?.selectionStart ?? v.length;
  const end = ta?.selectionEnd ?? start;
  const hasSel = end > start;
  const selected = hasSel ? v.slice(start, end) : placeholder;
  chapterDialog.content = v.slice(0, start) + prefix + selected + suffix + v.slice(end);
  nextTick(() => { ta?.focus(); const s = start + prefix.length; ta?.setSelectionRange(s, s + selected.length); });
}
function insertChessBlock() {
  insertAtCursor(`\n\`\`\`chess\n${START_FEN}\n\`\`\`\n`);
}
const toolbarButtons = [
  { key: 'bold', icon: 'textformat-bold', title: 'Đậm', action: () => wrapSelectionSimple('**', '**', 'đậm') },
  { key: 'italic', icon: 'textformat-italic', title: 'Nghiêng', action: () => wrapSelectionSimple('*', '*', 'nghiêng') },
  { key: 'h2', icon: 'numbers-2', title: 'Tiêu đề', action: () => insertAtCursor('\n## Tiêu đề\n') },
  { key: 'ul', icon: 'view-list', title: 'Danh sách', action: () => insertAtCursor('\n- mục\n') },
  { key: 'quote', icon: 'quote', title: 'Trích dẫn', action: () => insertAtCursor('\n> trích dẫn\n') },
  { key: 'code', icon: 'code-1', title: 'Khối mã', action: () => insertAtCursor('\n```\nmã\n```\n') },
  { key: 'table', icon: 'table', title: 'Bảng', action: () => insertAtCursor('\n| Cột 1 | Cột 2 |\n| --- | --- |\n| ô | ô |\n') },
  { key: 'chess', icon: 'grid', title: 'Khối bàn cờ (FEN/PGN)', action: insertChessBlock },
  { key: 'image', icon: 'image', title: 'Chèn ảnh', action: () => imageInputEl.value?.click() },
];

// ---- Bộ chọn chèn tham chiếu cờ ----
const picker = reactive<{
  visible: boolean; tab: string; search: string; embed: boolean; loading: boolean;
  games: ChessGame[]; puzzles: ChessPuzzle[]; positions: ChessPosition[]; books: ChessBook[]; chapters: ChessBookChapter[];
  previewRef: string; previewTitle: string; previewBoard: ChessBoardData | null; previewLoading: boolean;
}>({
  visible: false, tab: 'games', search: '', embed: false, loading: false,
  games: [], puzzles: [], positions: [], books: [], chapters: [],
  previewRef: '', previewTitle: '', previewBoard: null, previewLoading: false,
});
let previewSeq = 0;
async function previewItem(it: { slug: string; type: string; label: string }) {
  const ref = `${it.type}/${it.slug}`;
  if (picker.previewRef === ref) return;
  picker.previewRef = ref;
  picker.previewTitle = it.label;
  picker.previewBoard = null;
  picker.previewLoading = true;
  const seq = ++previewSeq;
  try {
    const r = await resolveChessRef(ref);
    if (seq !== previewSeq) return;
    picker.previewTitle = r.title || it.label;
    picker.previewBoard = r.board;
  } catch {
    if (seq === previewSeq) picker.previewBoard = null;
  } finally {
    if (seq === previewSeq) picker.previewLoading = false;
  }
}
async function openPicker() {
  picker.visible = true;
  picker.search = '';
  picker.loading = true;
  try {
    const [g, p, pos, bks]: any[] = await Promise.all([listGames(), listPuzzles(), listPositions(), listBooks()]);
    picker.games = g?.data || [];
    picker.puzzles = p?.data || [];
    picker.positions = pos?.data || [];
    picker.books = bks?.data || [];
    picker.chapters = selectedBook.value ? (await listChapters(selectedBook.value.id) as any)?.data || [] : [];
  } catch {
    MessagePlugin.error('Tải danh sách thất bại');
  } finally {
    picker.loading = false;
  }
}
const gameLabel = (g: ChessGame) =>
  `${g.white || '?'} – ${g.black || '?'}${g.event ? ', ' + g.event : ''}${g.date ? ' ' + g.date.slice(0, 4) : ''}`;
const pickerItems = computed(() => {
  const s = picker.search.trim().toLowerCase();
  if (picker.tab === 'games') {
    return picker.games.filter((g) => g.slug && (!s || `${g.white} ${g.black} ${g.event}`.toLowerCase().includes(s)))
      .map((g) => ({ slug: g.slug as string, type: 'game' as const, label: gameLabel(g) }));
  }
  if (picker.tab === 'puzzles') {
    return picker.puzzles.filter((p) => p.slug && (!s || `${p.title} ${p.theme}`.toLowerCase().includes(s)))
      .map((p) => ({ slug: p.slug as string, type: 'puzzle' as const, label: p.title || p.slug || '' }));
  }
  if (picker.tab === 'positions') {
    return picker.positions.filter((p) => p.slug && (!s || `${p.title} ${p.category}`.toLowerCase().includes(s)))
      .map((p) => ({ slug: p.slug as string, type: 'position' as const, label: p.title || p.slug || '' }));
  }
  if (picker.tab === 'books') {
    return picker.books.filter((b) => b.slug && (!s || (b.title || '').toLowerCase().includes(s)))
      .map((b) => ({ slug: b.slug as string, type: 'book' as const, label: b.title || b.slug || '' }));
  }
  return picker.chapters.filter((c) => c.slug && (!s || (c.title || '').toLowerCase().includes(s)))
    .map((c) => ({ slug: c.slug as string, type: 'chapter' as const, label: c.title || c.slug || '' }));
});
function pickerInsert(item: { slug: string; type: string; label: string }) {
  const ref = `${item.type}/${item.slug}`;
  const link = picker.embed ? `\n\n![[${ref}|${item.label}]]\n` : `[[${ref}|${item.label}]]`;
  insertAtCursor(link);
  picker.visible = false;
}

loadShelves();
loadBooks().then(() => {
  focusBook(props.focusBookSlug);
  focusChapter(props.focusChapterSlug);
});
</script>

<style lang="less" scoped>
.bkl { display: flex; height: 100%; gap: 16px; padding: 16px 20px; box-sizing: border-box; overflow: hidden; }
.bkl-left { width: 360px; flex: 0 0 360px; overflow-y: auto; border-right: 1px solid var(--td-component-stroke); padding-right: 12px; }
.bkl-right { flex: 1 1 auto; overflow-y: auto; }
.bkl-toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 10px; flex-wrap: wrap; }
.bkl-empty { color: var(--td-text-color-placeholder); font-size: 14px; padding: 16px 4px; }
.bkl-empty--big { text-align: center; padding-top: 80px; }
.bkl-row {
  display: flex; align-items: flex-start; justify-content: space-between;
  padding: 10px; border-radius: 8px; cursor: pointer; margin-bottom: 6px;
  border: 1px solid var(--td-component-stroke);
  &:hover { background: var(--td-bg-color-container-hover); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); }
}
.bkl-row-title { font-weight: 600; color: var(--td-text-color-primary); }
.bkl-row-subtitle { font-size: 12px; color: var(--td-text-color-secondary); margin-top: 2px; }
.bkl-row-meta { display: flex; gap: 6px; align-items: center; margin-top: 6px; flex-wrap: wrap; }
.bkl-row-author { font-size: 12px; color: var(--td-text-color-placeholder); margin-top: 4px; }
.bkl-tag { background: var(--td-brand-color-light); color: var(--td-brand-color); padding: 0 6px; border-radius: 4px; font-size: 12px; }
.bkl-tag--phase { background: #e6f4ff; color: #2275b4; }
.bkl-tag--draft { background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); }
.bkl-tag--published { background: #e6ffed; color: #18794e; }
.bkl-count { color: var(--td-text-color-secondary); font-size: 12px; }
.bkl-row-actions { display: flex; flex: none; }
.bkl-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 10px; gap: 12px;
  h2 { font-size: 18px; margin: 0; color: var(--td-text-color-primary); } }
.bkl-subtitle { font-size: 13px; color: var(--td-text-color-secondary); margin-top: 2px; }
.bkl-header-meta { display: flex; gap: 10px; align-items: center; margin-top: 6px; font-size: 12px; color: var(--td-text-color-secondary); }
.bkl-header-actions { display: flex; gap: 6px; flex-wrap: wrap; flex: none; }
.bkl-shelves { margin: 8px 0; font-size: 12px; color: var(--td-text-color-secondary); }
.bkl-shelf-chip { display: inline-block; background: var(--td-bg-color-secondarycontainer); padding: 0 8px; border-radius: 4px; margin-left: 6px; }
.bkl-description { margin: 10px 0; padding: 10px 12px; background: var(--td-bg-color-container); border-radius: 8px; line-height: 1.6; color: var(--td-text-color-primary); }
.bkl-part-title { font-weight: 700; font-size: 14px; color: var(--td-text-color-primary); margin: 16px 0 8px; }
.bkl-chapter { border: 1px solid var(--td-component-stroke); border-radius: 8px; margin-bottom: 8px; overflow: hidden; }
.bkl-chapter-head { display: flex; align-items: center; gap: 8px; padding: 10px 12px; cursor: pointer;
  &:hover { background: var(--td-bg-color-container-hover); } }
.bkl-chapter-title { font-weight: 600; flex: 1; color: var(--td-text-color-primary); }
.bkl-chapter-actions { display: flex; align-items: center; gap: 2px; }
.bkl-chapter-body { padding: 12px; border-top: 1px solid var(--td-component-stroke); }
.bkl-chapter-content { line-height: 1.6; color: var(--td-text-color-primary); margin-bottom: 8px; }
.bkl-md-segment :deep(.chess-ref-link) {
  color: var(--td-brand-color); background: var(--td-brand-color-light);
  padding: 0 6px; border-radius: 4px; text-decoration: none; font-weight: 600;
  &::before { content: '♟ '; }
  &:hover { text-decoration: underline; }
}
.bkl-form { display: flex; flex-direction: column; gap: 6px; label { font-size: 13px; color: var(--td-text-color-secondary); margin-top: 6px; } }
.bkl-row2 { display: flex; gap: 12px; > div { flex: 1; min-width: 0; } }
.bkl-content-label { display: flex; align-items: center; justify-content: space-between; margin-top: 6px; label { margin-top: 0; } }
.bkl-content-actions { display: flex; gap: 6px; }
.bkl-editor-toolbar { display: flex; gap: 2px; padding: 4px; border: 1px solid var(--td-component-stroke); border-bottom: none; border-radius: 6px 6px 0 0; background: var(--td-bg-color-container); }
.bkl-picker { display: flex; flex-direction: column; gap: 10px; }
.bkl-picker-bar { display: flex; align-items: center; gap: 12px; }
.bkl-picker-body { display: flex; gap: 12px; align-items: stretch; }
.bkl-picker-list { flex: 1 1 0; min-width: 0; max-height: 360px; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; }
.bkl-picker-preview { flex: 0 0 320px; max-width: 320px; max-height: 360px; overflow-y: auto; border-left: 1px solid var(--td-component-stroke); padding-left: 12px; }
.bkl-preview-title { font-weight: 600; margin-bottom: 8px; color: var(--td-text-color-primary); }
.bkl-picker-row {
  display: flex; align-items: center; gap: 10px; padding: 8px 10px; border-radius: 6px; cursor: pointer;
  border: 1px solid var(--td-component-stroke);
  &:hover { background: var(--td-bg-color-container-hover); border-color: var(--td-brand-color); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); }
}
.bkl-picker-label { font-weight: 600; color: var(--td-text-color-primary); flex: 1; }
.bkl-picker-slug { font-size: 12px; color: var(--td-text-color-placeholder); font-family: monospace; }
</style>
