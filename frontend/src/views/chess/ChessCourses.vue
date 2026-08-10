<template>
  <div class="chess-courses">
    <!-- Cột trái: danh sách khóa học -->
    <div class="cc-left">
      <div class="cc-header">
        <h2>Khóa học cờ vua</h2>
        <div class="cc-header-actions">
          <t-button variant="outline" size="small" @click="doExport">
            <template #icon><t-icon name="download" /></template>Export
          </t-button>
          <t-button variant="outline" size="small" @click="doImport">
            <template #icon><t-icon name="upload" /></template>Import
          </t-button>
          <t-button theme="primary" size="small" @click="openCourseDialog()">
            <template #icon><t-icon name="add" /></template>
            Tạo khóa học
          </t-button>
        </div>
      </div>
      <div v-if="courses.length === 0" class="cc-empty">Chưa có khóa học. Nhấn "Tạo khóa học".</div>
      <div v-for="c in courses" :key="c.id" class="cc-course-row"
        :class="{ active: selectedCourse && selectedCourse.id === c.id }" @click="selectCourse(c)">
        <div class="cc-course-main">
          <div class="cc-course-title">{{ c.title }}</div>
          <div class="cc-course-meta">
            <span v-if="c.level" class="cc-tag">{{ levelLabel(c.level) }}</span>
            <span class="cc-count">{{ c.lesson_count || 0 }} bài học</span>
          </div>
        </div>
        <div class="cc-course-actions">
          <t-button size="small" variant="text" :title="t('chess.ref.copyLink')" @click.stop="copyCourseWikilink(c)"><t-icon name="link" /></t-button>
          <t-button size="small" variant="text" @click.stop="openCourseDialog(c)"><t-icon name="edit" /></t-button>
          <t-button size="small" variant="text" theme="danger" @click.stop="removeCourse(c)"><t-icon name="delete" /></t-button>
        </div>
      </div>
    </div>

    <!-- Cột phải: bài học của khóa đang chọn -->
    <div class="cc-right">
      <template v-if="selectedCourse">
        <div class="cc-header">
          <div>
            <h2>{{ selectedCourse.title }}</h2>
            <div class="cc-desc" v-if="selectedCourse.description">{{ selectedCourse.description }}</div>
          </div>
          <t-button theme="primary" size="small" @click="openLessonDialog()">
            <template #icon><t-icon name="add" /></template>
            Thêm bài học
          </t-button>
        </div>
        <ChessBacklinks v-if="selectedCourse.slug" ref-type="course" :slug="selectedCourse.slug" />
        <div v-if="lessons.length === 0" class="cc-empty">Chưa có bài học.</div>
        <div v-for="(l, idx) in lessons" :key="l.id" class="cc-lesson" :data-lesson-id="l.id">
          <div class="cc-lesson-head" @click="toggleLesson(l.id)">
            <span class="cc-lesson-idx">{{ idx + 1 }}.</span>
            <span class="cc-lesson-title">{{ l.title }}</span>
            <span class="cc-lesson-actions">
              <t-button size="small" variant="text" @click.stop="openLessonDialog(l)"><t-icon name="edit" /></t-button>
              <t-button size="small" variant="text" theme="danger" @click.stop="removeLesson(l)"><t-icon name="delete" /></t-button>
              <t-icon :name="expandedLessons.has(l.id) ? 'chevron-up' : 'chevron-down'" />
            </span>
          </div>
          <div v-if="expandedLessons.has(l.id)" class="cc-lesson-body">
            <!-- Nội dung markdown + bàn cờ tương tác nhúng từ khối ```chess
                 (cùng engine với trang wiki). -->
            <div class="cc-lesson-content" v-if="l.content" @click="onLessonContentClick">
              <template v-for="(seg, si) in lessonSegments(l)" :key="si">
                <ChessBoardDisplay v-if="seg.type === 'board'" :data="seg.board" />
                <ChessRefEmbed v-else-if="seg.type === 'ref'" :ref-str="seg.refStr" />
                <div v-else class="cc-md-segment" v-html="seg.html"></div>
              </template>
            </div>
            <!-- Bàn cờ chính của bài (từ ô FEN/PGN riêng), giữ tương thích cũ. -->
            <ChessBoardDisplay v-if="l.fen || l.pgn" :data="boardData(l)" />
            <ChessBacklinks v-if="l.slug" ref-type="lesson" :slug="l.slug" />
          </div>
        </div>
      </template>
      <div v-else class="cc-empty cc-empty--big">Chọn một khóa học bên trái để xem bài học.</div>
    </div>

    <!-- Dialog khóa học -->
    <t-dialog v-model:visible="courseDialog.visible" :header="courseDialog.id ? 'Sửa khóa học' : 'Tạo khóa học'"
      :on-confirm="saveCourse" width="520px">
      <div class="cc-form">
        <label>Tên khóa học *</label>
        <t-input v-model="courseDialog.title" placeholder="VD: Khai cuộc cho người mới" />
        <label>Mô tả</label>
        <t-textarea v-model="courseDialog.description" :autosize="{ minRows: 2 }" />
        <label>Trình độ</label>
        <t-select v-model="courseDialog.level" :options="levelOptions" clearable />
        <label>Thứ tự</label>
        <t-input-number v-model="courseDialog.sort_order" />
      </div>
    </t-dialog>

    <!-- Dialog bài học -->
    <t-dialog v-model:visible="lessonDialog.visible" :header="lessonDialog.id ? 'Sửa bài học' : 'Thêm bài học'"
      :on-confirm="saveLesson" width="640px">
      <div class="cc-form">
        <label>Tên bài học *</label>
        <t-input v-model="lessonDialog.title" />
        <div class="cc-content-label">
          <label>Nội dung (văn bản/markdown)</label>
          <t-button size="small" variant="outline" @click="openPicker">
            <template #icon><t-icon name="chess" /></template>
            {{ t('chess.ref.insert') }}
          </t-button>
        </div>
        <t-textarea ref="lessonContentRef" v-model="lessonDialog.content" :autosize="{ minRows: 4 }"
          placeholder="Nội dung bài giảng... Gõ [[ để gợi ý ván/thế cờ/bài/khóa." />
        <ChessWikiLinkSuggest :textarea="lessonTextareaEl" v-model="lessonDialog.content" />
        <label>Thế cờ FEN (tùy chọn — sẽ hiện bàn cờ)</label>
        <t-input v-model="lessonDialog.fen" placeholder="rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1" />
        <label>Ván minh họa PGN (tùy chọn)</label>
        <t-textarea v-model="lessonDialog.pgn" :autosize="{ minRows: 2 }" placeholder="1. e4 e5 2. Nf3 Nc6 ..." />
        <label>Thứ tự</label>
        <t-input-number v-model="lessonDialog.sort_order" />
      </div>
    </t-dialog>

    <!-- Bộ chọn chèn tham chiếu cờ ([[game/<slug>]] hoặc ![[…]] nhúng) -->
    <t-dialog v-model:visible="picker.visible" :header="t('chess.ref.pickerTitle')" :footer="false" width="820px">
      <div class="cc-picker">
        <t-tabs v-model="picker.tab">
          <t-tab-panel value="games" :label="t('chess.ref.tabGames')" />
          <t-tab-panel value="positions" :label="t('chess.ref.tabPositions')" />
          <t-tab-panel value="puzzles" :label="t('chess.ref.tabPuzzles')" />
          <t-tab-panel value="lessons" :label="t('chess.ref.tabLessons')" />
          <t-tab-panel value="courses" :label="t('chess.ref.tabCourses')" />
        </t-tabs>
        <div class="cc-picker-bar">
          <t-input v-model="picker.search" :placeholder="t('chess.ref.searchPlaceholder')" clearable />
          <t-checkbox v-model="picker.embed">{{ t('chess.ref.embedToggle') }}</t-checkbox>
        </div>
        <div class="cc-picker-body">
          <div class="cc-picker-list">
            <div v-if="picker.loading" class="cc-empty">{{ t('chess.ref.loading') }}</div>
            <div v-else-if="pickerItems.length === 0" class="cc-empty">{{ t('chess.ref.empty') }}</div>
            <div v-for="it in pickerItems" :key="it.type + '/' + it.slug" class="cc-picker-row"
              :class="{ active: picker.previewRef === it.type + '/' + it.slug }"
              @mouseenter="previewItem(it)" @click="pickerInsert(it)">
              <span class="cc-picker-label">{{ it.label }}</span>
              <span class="cc-picker-slug">{{ it.type }}/{{ it.slug }}</span>
              <t-button size="small" variant="text" theme="primary">{{ t('chess.ref.insertAction') }}</t-button>
            </div>
          </div>
          <div class="cc-picker-preview">
            <div v-if="picker.previewLoading" class="cc-empty">{{ t('chess.ref.loading') }}</div>
            <template v-else-if="picker.previewRef">
              <div class="cc-preview-title">{{ picker.previewTitle }}</div>
              <ChessBoardDisplay v-if="picker.previewBoard" :key="picker.previewRef" :data="picker.previewBoard" />
              <div v-else class="cc-empty">{{ t('chess.ref.noBoard') }}</div>
            </template>
            <div v-else class="cc-empty">{{ t('chess.ref.previewHint') }}</div>
          </div>
        </div>
      </div>
    </t-dialog>

    <!-- Popup bàn cờ khi bấm chip trong nội dung bài giảng -->
    <ChessRefDialog v-model:visible="refDialog.visible" :ref-str="refDialog.refStr" />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, nextTick, watch } from 'vue';
import { marked } from 'marked';
import { useI18n } from 'vue-i18n';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import ChessBoardDisplay from '@/views/chat/components/tool-results/ChessBoardDisplay.vue';
import ChessRefEmbed from '@/views/chess/components/ChessRefEmbed.vue';
import ChessRefDialog from '@/views/chess/components/ChessRefDialog.vue';
import ChessBacklinks from '@/views/chess/components/ChessBacklinks.vue';
import ChessWikiLinkSuggest from '@/views/chess/components/ChessWikiLinkSuggest.vue';
import type { ChessBoardData } from '@/types/tool-results';
import { splitChessContent, renderChessChips, isValidFEN } from '@/utils/chessBlocks';
import { resolveChessRef } from '@/utils/chessRef';
import { useChessWikiDraftStore } from '@/stores/chessWikiDraft';
import {
  listCourses, createCourse, updateCourse, deleteCourse, getCourseBySlug,
  listLessons, createLesson, updateLesson, deleteLesson, getLessonBySlug,
  listGames, listPuzzles, listPositions, exportCourses, importCourses,
  type ChessCourse, type ChessLesson, type ChessGame, type ChessPuzzle, type ChessPosition,
} from '@/api/chess';
import { downloadText, pickTextFile } from '@/utils/fileTransfer';

const { t } = useI18n();

// Deep-link "Mở trong thư viện": mở khóa học chứa bài giảng + bung bài đó (lesson),
// hoặc chọn sẵn khóa học (course).
const props = defineProps<{ focusLessonSlug?: string; focusCourseSlug?: string }>();
async function focusLesson(slug?: string) {
  if (!slug) return;
  try {
    const res: any = await getLessonBySlug(slug);
    const l = res?.data;
    if (!l) return;
    if (courses.value.length === 0) await loadCourses();
    const course = courses.value.find((c) => c.id === l.course_id);
    if (!course) return;
    await selectCourse(course);
    expandedLessons.value = new Set([l.id]);
    await nextTick();
    document.querySelector(`[data-lesson-id="${l.id}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  } catch { /* không tìm thấy → bỏ qua */ }
}
async function focusCourse(slug?: string) {
  if (!slug) return;
  try {
    const res: any = await getCourseBySlug(slug);
    const c = res?.data;
    if (!c) return;
    if (courses.value.length === 0) await loadCourses();
    const course = courses.value.find((x) => x.id === c.id) || c;
    await selectCourse(course);
  } catch { /* không tìm thấy → bỏ qua */ }
}
watch(() => props.focusLessonSlug, (s) => focusLesson(s));
watch(() => props.focusCourseSlug, (s) => focusCourse(s));

const chessWikiDraft = useChessWikiDraftStore();

// Render nội dung bài giảng: markdown (qua marked) + bàn cờ tương tác nhúng từ
// khối ```chess, theo đúng thứ tự. [[slug|tên]] trong bài giảng chỉ hiển thị tên
// (bài giảng không có ngữ cảnh KB để điều hướng wiki như trong WikiBrowser).
function renderLessonMarkdown(md: string): string {
  // 1) CHIP tham chiếu cờ [[game/<slug>]] → <a class="chess-ref-link"> (giữ inline).
  let pre = renderChessChips(md || '');
  // 2) Link wiki thường [[slug|tên]] → chỉ hiện tên (bài giảng không có ngữ cảnh KB).
  pre = pre.replace(/\[\[([^\]]+)\]\]/g, (_, inner: string) => {
    const pipe = inner.indexOf('|');
    return (pipe > 0 ? inner.slice(pipe + 1) : inner).trim();
  });
  return marked.parse(pre, { breaks: true, async: false }) as string;
}
function lessonSegments(l: ChessLesson) {
  return splitChessContent(l.content || '').map((seg) => {
    if (seg.type === 'board') return { type: 'board' as const, board: seg.board! };
    if (seg.type === 'ref') return { type: 'ref' as const, refStr: seg.ref!.ref };
    return { type: 'markdown' as const, html: renderLessonMarkdown(seg.markdown || '') };
  });
}

// Popup hiển thị bàn cờ khi bấm chip trong nội dung bài giảng.
const refDialog = reactive<{ visible: boolean; refStr: string }>({ visible: false, refStr: '' });
function onLessonContentClick(e: MouseEvent) {
  const a = (e.target as HTMLElement).closest('a.chess-ref-link') as HTMLElement | null;
  if (!a) return;
  e.preventDefault();
  const ref = a.getAttribute('data-chess-ref');
  if (ref) {
    refDialog.refStr = ref;
    refDialog.visible = true;
  }
}

const courses = ref<ChessCourse[]>([]);
const selectedCourse = ref<ChessCourse | null>(null);
const lessons = ref<ChessLesson[]>([]);
const expandedLessons = ref<Set<string>>(new Set());

const levelOptions = [
  { label: 'Cơ bản', value: 'co-ban' },
  { label: 'Trung cấp', value: 'trung-cap' },
  { label: 'Nâng cao', value: 'nang-cao' },
];
const levelLabel = (v: string) => levelOptions.find(o => o.value === v)?.label || v;

const STARTFEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';
function boardData(l: ChessLesson): ChessBoardData {
  if (l.pgn && l.pgn.trim()) {
    return { display_type: 'chess_board', fen: STARTFEN, pgn: l.pgn, caption: l.title };
  }
  return { display_type: 'chess_board', fen: l.fen || STARTFEN, caption: l.title };
}

async function loadCourses() {
  try {
    const res: any = await listCourses();
    courses.value = res?.data || [];
  } catch {
    MessagePlugin.error('Tải khóa học thất bại');
  }
}

async function selectCourse(c: ChessCourse) {
  selectedCourse.value = c;
  expandedLessons.value = new Set();
  try {
    const res: any = await listLessons(c.id);
    lessons.value = res?.data || [];
  } catch {
    lessons.value = [];
  }
}

function toggleLesson(id: string) {
  const next = new Set(expandedLessons.value);
  next.has(id) ? next.delete(id) : next.add(id);
  expandedLessons.value = next;
}

// ---- Course dialog ----
interface CourseDialogState {
  visible: boolean;
  id: string;
  title: string;
  description: string;
  level: string;
  sort_order: number;
}
const courseDialog = reactive<CourseDialogState>({ visible: false, id: '', title: '', description: '', level: '', sort_order: 0 });
function openCourseDialog(c?: ChessCourse) {
  courseDialog.visible = true;
  courseDialog.id = c?.id || '';
  courseDialog.title = c?.title || '';
  courseDialog.description = c?.description || '';
  courseDialog.level = c?.level || '';
  courseDialog.sort_order = c?.sort_order || 0;
}
async function saveCourse() {
  if (!courseDialog.title.trim()) { MessagePlugin.warning('Nhập tên khóa học'); return; }
  const payload = {
    title: courseDialog.title, description: courseDialog.description,
    level: courseDialog.level, sort_order: courseDialog.sort_order,
  };
  try {
    if (courseDialog.id) await updateCourse(courseDialog.id, payload);
    else await createCourse(payload);
    courseDialog.visible = false;
    await loadCourses();
    MessagePlugin.success('Đã lưu khóa học');
  } catch {
    MessagePlugin.error('Lưu thất bại');
  }
}
// Sao chép wikilink [[course/<slug>]] để dán vào nội dung wiki/bài giảng.
async function copyCourseWikilink(c: ChessCourse) {
  if (!c.slug) { MessagePlugin.warning('Khóa học chưa có slug'); return; }
  const link = `[[course/${c.slug}|${c.title || c.slug}]]`;
  try {
    await navigator.clipboard.writeText(link);
    MessagePlugin.success(t('chess.ref.copied'));
  } catch {
    MessagePlugin.info(link);
  }
}

function removeCourse(c: ChessCourse) {
  DialogPlugin.confirm({
    header: 'Xóa khóa học', body: `Xóa "${c.title}" và toàn bộ bài học?`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteCourse(c.id);
        if (selectedCourse.value?.id === c.id) { selectedCourse.value = null; lessons.value = []; }
        await loadCourses();
        MessagePlugin.success('Đã xóa');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}

// ---- Export / Import khóa học (kèm bài học) ----
async function doExport() {
  try {
    const res: any = await exportCourses();
    const items = res?.data || [];
    if (!items.length) { MessagePlugin.info('Không có khóa học để xuất'); return; }
    downloadText(`khoahoc-${new Date().toISOString().slice(0, 10)}.json`, JSON.stringify(items, null, 2), 'application/json');
    MessagePlugin.success(`Đã xuất ${items.length} khóa học`);
  } catch { MessagePlugin.error('Xuất thất bại'); }
}
async function doImport() {
  const text = await pickTextFile('.json,application/json');
  if (text == null) return;
  let arr: any;
  try { arr = JSON.parse(text); } catch { MessagePlugin.error('File JSON không hợp lệ'); return; }
  if (!Array.isArray(arr)) { MessagePlugin.error('File phải là một mảng JSON khóa học'); return; }
  try {
    const res: any = await importCourses(arr);
    await loadCourses();
    MessagePlugin.success(`Đã nhập ${res?.data?.imported || 0} khóa học`);
  } catch (e: any) { MessagePlugin.error(e?.error || e?.message || 'Import thất bại'); }
}

// ---- Lesson dialog ----
interface LessonDialogState {
  visible: boolean;
  id: string;
  title: string;
  content: string;
  fen: string;
  pgn: string;
  sort_order: number;
}
const lessonDialog = reactive<LessonDialogState>({ visible: false, id: '', title: '', content: '', fen: '', pgn: '', sort_order: 0 });
function openLessonDialog(l?: ChessLesson) {
  lessonDialog.visible = true;
  lessonDialog.id = l?.id || '';
  lessonDialog.title = l?.title || '';
  lessonDialog.content = l?.content || '';
  lessonDialog.fen = l?.fen || '';
  lessonDialog.pgn = l?.pgn || '';
  lessonDialog.sort_order = l?.sort_order || lessons.value.length;
}
async function saveLesson() {
  if (!selectedCourse.value) return;
  if (!lessonDialog.title.trim()) { MessagePlugin.warning('Nhập tên bài học'); return; }
  if (lessonDialog.fen.trim() && !isValidFEN(lessonDialog.fen)) {
    MessagePlugin.warning('Thế cờ FEN không hợp lệ — kiểm tra lại hoặc để trống.');
    return;
  }
  const payload = {
    title: lessonDialog.title, content: lessonDialog.content,
    fen: lessonDialog.fen, pgn: lessonDialog.pgn, sort_order: lessonDialog.sort_order,
  };
  try {
    if (lessonDialog.id) await updateLesson(lessonDialog.id, payload);
    else await createLesson(selectedCourse.value.id, payload);
    lessonDialog.visible = false;
    await selectCourse(selectedCourse.value);
    await loadCourses(); // cập nhật số bài học
    MessagePlugin.success('Đã lưu bài học');
  } catch {
    MessagePlugin.error('Lưu thất bại');
  }
}
function removeLesson(l: ChessLesson) {
  DialogPlugin.confirm({
    header: 'Xóa bài học', body: `Xóa "${l.title}"?`,
    theme: 'warning', confirmBtn: { content: 'Xóa', theme: 'danger' },
    onConfirm: async () => {
      try {
        await deleteLesson(l.id);
        if (selectedCourse.value) await selectCourse(selectedCourse.value);
        await loadCourses();
        MessagePlugin.success('Đã xóa');
      } catch { MessagePlugin.error('Xóa thất bại'); }
    },
  });
}

// ---- Bộ chọn chèn tham chiếu cờ vào nội dung bài giảng ----
const lessonContentRef = ref<any>(null);
// Phần tử <textarea> native (lấy từ t-textarea) để gắn autocomplete "[[".
const lessonTextareaEl = ref<HTMLTextAreaElement | null>(null);
watch(() => lessonDialog.visible, async (open) => {
  if (!open) { lessonTextareaEl.value = null; return; }
  await nextTick();
  const root = lessonContentRef.value?.$el || lessonContentRef.value;
  lessonTextareaEl.value = (root?.querySelector?.('textarea') as HTMLTextAreaElement) || null;
});
const picker = reactive<{
  visible: boolean; tab: string; search: string; embed: boolean; loading: boolean;
  games: ChessGame[]; puzzles: ChessPuzzle[]; lessons: ChessLesson[]; courses: ChessCourse[]; positions: ChessPosition[];
  previewRef: string; previewTitle: string; previewBoard: ChessBoardData | null; previewLoading: boolean;
}>({
  visible: false, tab: 'games', search: '', embed: false, loading: false,
  games: [], puzzles: [], lessons: [], courses: [], positions: [],
  previewRef: '', previewTitle: '', previewBoard: null, previewLoading: false,
});

// Xem trước bàn cờ của mục đang trỏ trong picker (tái dùng resolveChessRef + cache).
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
    const [g, p, pos]: any[] = await Promise.all([listGames(), listPuzzles(), listPositions()]);
    picker.games = g?.data || [];
    picker.puzzles = p?.data || [];
    picker.positions = pos?.data || [];
    picker.lessons = selectedCourse.value ? (await listLessons(selectedCourse.value.id) as any)?.data || [] : [];
    picker.courses = courses.value;
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
    return picker.games
      .filter((g) => g.slug && (!s || `${g.white} ${g.black} ${g.event}`.toLowerCase().includes(s)))
      .map((g) => ({ slug: g.slug as string, type: 'game' as const, label: gameLabel(g) }));
  }
  if (picker.tab === 'puzzles') {
    return picker.puzzles
      .filter((p) => p.slug && (!s || `${p.title} ${p.theme}`.toLowerCase().includes(s)))
      .map((p) => ({ slug: p.slug as string, type: 'puzzle' as const, label: p.title || p.slug || '' }));
  }
  if (picker.tab === 'courses') {
    return picker.courses
      .filter((c) => c.slug && (!s || (c.title || '').toLowerCase().includes(s)))
      .map((c) => ({ slug: c.slug as string, type: 'course' as const, label: c.title || c.slug || '' }));
  }
  if (picker.tab === 'positions') {
    return picker.positions
      .filter((p) => p.slug && (!s || `${p.title} ${p.category} ${p.tags}`.toLowerCase().includes(s)))
      .map((p) => ({ slug: p.slug as string, type: 'position' as const, label: p.title || p.slug || '' }));
  }
  return picker.lessons
    .filter((l) => l.slug && (!s || (l.title || '').toLowerCase().includes(s)))
    .map((l) => ({ slug: l.slug as string, type: 'lesson' as const, label: l.title || l.slug || '' }));
});

// Chèn văn bản tại vị trí con trỏ trong ô nội dung (fallback: nối vào cuối).
function insertAtCursor(text: string) {
  const root = lessonContentRef.value?.$el || lessonContentRef.value;
  const ta = root?.querySelector?.('textarea') as HTMLTextAreaElement | undefined;
  if (!ta) {
    lessonDialog.content = (lessonDialog.content || '') + text;
    return;
  }
  const start = ta.selectionStart ?? lessonDialog.content.length;
  const end = ta.selectionEnd ?? start;
  const v = lessonDialog.content || '';
  lessonDialog.content = v.slice(0, start) + text + v.slice(end);
  nextTick(() => {
    ta.focus();
    const pos = start + text.length;
    ta.setSelectionRange(pos, pos);
  });
}

function pickerInsert(item: { slug: string; type: string; label: string }) {
  const ref = `${item.type}/${item.slug}`;
  const link = picker.embed ? `\n\n![[${ref}|${item.label}]]\n` : `[[${ref}|${item.label}]]`;
  insertAtCursor(link);
  picker.visible = false;
}

// Nếu vừa bấm "Tạo bài giảng từ trang này" trong wiki: mở sẵn hộp thoại thêm bài
// giảng với tiêu đề + nội dung của trang. Cần một khóa học để chứa bài giảng.
async function initFromWikiDraft() {
  const d = chessWikiDraft.takeDraft();
  if (!d) return;
  if (!selectedCourse.value && courses.value.length > 0) {
    await selectCourse(courses.value[0]);
  }
  if (!selectedCourse.value) {
    MessagePlugin.info('Hãy tạo một khóa học trước, rồi tạo lại bài giảng từ trang wiki.');
    return;
  }
  openLessonDialog();
  lessonDialog.title = d.title;
  lessonDialog.content = `${d.content}\n\n---\n*Bài giảng tạo từ trang wiki: ${d.title}*`;
  MessagePlugin.success(`Đã nạp nội dung từ trang wiki vào khóa "${selectedCourse.value.title}".`);
}

loadCourses().then(initFromWikiDraft).then(() => {
  focusLesson(props.focusLessonSlug);
  focusCourse(props.focusCourseSlug);
});
</script>

<style lang="less" scoped>
.chess-courses {
  display: flex;
  height: 100%;
  gap: 16px;
  padding: 16px 20px;
  box-sizing: border-box;
  overflow: hidden;
}
.cc-left {
  width: 340px;
  flex: 0 0 340px;
  overflow-y: auto;
  border-right: 1px solid var(--td-component-stroke);
  padding-right: 12px;
}
.cc-right { flex: 1 1 auto; overflow-y: auto; }
.cc-header {
  display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px;
  h2 { font-size: 18px; margin: 0; color: var(--td-text-color-primary); }
}
.cc-header-actions { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.cc-desc { font-size: 13px; color: var(--td-text-color-secondary); margin-top: 2px; }
.cc-empty { color: var(--td-text-color-placeholder); font-size: 14px; padding: 16px 4px; }
.cc-empty--big { text-align: center; padding-top: 80px; }
.cc-course-row {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px; border-radius: 8px; cursor: pointer; margin-bottom: 6px;
  border: 1px solid var(--td-component-stroke);
  &:hover { background: var(--td-bg-color-container-hover); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); }
}
.cc-course-title { font-weight: 600; color: var(--td-text-color-primary); }
.cc-course-meta { display: flex; gap: 8px; align-items: center; margin-top: 4px; font-size: 12px; }
.cc-tag { background: var(--td-brand-color-light); color: var(--td-brand-color); padding: 0 6px; border-radius: 4px; }
.cc-count { color: var(--td-text-color-secondary); }
.cc-course-actions { display: flex; }
.cc-lesson { border: 1px solid var(--td-component-stroke); border-radius: 8px; margin-bottom: 8px; overflow: hidden; }
.cc-lesson-head {
  display: flex; align-items: center; gap: 8px; padding: 10px 12px; cursor: pointer;
  &:hover { background: var(--td-bg-color-container-hover); }
}
.cc-lesson-idx { color: var(--td-text-color-secondary); }
.cc-lesson-title { font-weight: 600; flex: 1; color: var(--td-text-color-primary); }
.cc-lesson-actions { display: flex; align-items: center; gap: 2px; }
.cc-lesson-body { padding: 12px; border-top: 1px solid var(--td-component-stroke); }
.cc-lesson-content { white-space: pre-wrap; color: var(--td-text-color-primary); margin-bottom: 12px; line-height: 1.6; }
.cc-form {
  display: flex; flex-direction: column; gap: 6px;
  label { font-size: 13px; color: var(--td-text-color-secondary); margin-top: 6px; }
}
.cc-content-label {
  display: flex; align-items: center; justify-content: space-between; margin-top: 6px;
  label { margin-top: 0; }
}
.cc-picker { display: flex; flex-direction: column; gap: 10px; }
.cc-picker-bar { display: flex; align-items: center; gap: 12px; }
.cc-picker-body { display: flex; gap: 12px; align-items: stretch; }
.cc-picker-list { flex: 1 1 0; min-width: 0; max-height: 360px; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; }
.cc-picker-preview {
  flex: 0 0 320px; max-width: 320px; max-height: 360px; overflow-y: auto;
  border-left: 1px solid var(--td-component-stroke); padding-left: 12px;
}
.cc-preview-title { font-weight: 600; margin-bottom: 8px; color: var(--td-text-color-primary); }
.cc-picker-row {
  display: flex; align-items: center; gap: 10px; padding: 8px 10px; border-radius: 6px; cursor: pointer;
  border: 1px solid var(--td-component-stroke);
  &:hover { background: var(--td-bg-color-container-hover); border-color: var(--td-brand-color); }
  &.active { background: var(--td-bg-color-secondarycontainer); border-color: var(--td-brand-color); }
}
.cc-picker-label { font-weight: 600; color: var(--td-text-color-primary); flex: 1; }
.cc-picker-slug { font-size: 12px; color: var(--td-text-color-placeholder); font-family: monospace; }
/* Chip tham chiếu cờ inline trong nội dung bài giảng */
.cc-lesson-content :deep(.chess-ref-link) {
  color: var(--td-brand-color);
  background: var(--td-brand-color-light);
  padding: 0 6px; border-radius: 4px; text-decoration: none; font-weight: 600;
  &::before { content: '♟ '; }
  &:hover { text-decoration: underline; }
}
</style>
