import { get, post, put, del, postUpload } from "../../utils/request";

// API quản lý khóa học & bài học cờ vua (Phase 4 LMS).
// Lưu ý: path phải bao gồm tiền tố /api/v1 (giống các api khác — baseURL không chứa nó).

export interface ChessCourse {
  id: string;
  title: string;
  description: string;
  level: string;
  cover_url: string;
  sort_order: number;
  lesson_count?: number;
  slug?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ChessLesson {
  id: string;
  course_id: string;
  title: string;
  content: string;
  fen: string;
  pgn: string;
  sort_order: number;
  slug?: string;
  created_at?: string;
  updated_at?: string;
}

// ---- Tìm kiếm hợp nhất tham chiếu cờ (autocomplete wikilink khi gõ "[[") ----
export interface ChessRefSearchItem {
  type: "game" | "puzzle" | "lesson" | "course" | "position" | "book" | "chapter";
  slug: string;
  ref: string; // "<type>/<slug>"
  title: string;
  subtitle: string;
}
// Tìm gợi ý tham chiếu cờ theo từ khóa. type rỗng = mọi loại; limit = số mục mỗi loại.
export const searchChessRefs = (
  q: string,
  opts: Partial<{ type: string; limit: number }> = {},
) => {
  const params: Record<string, string> = {};
  if (q) params.q = q;
  if (opts.type) params.type = opts.type;
  if (opts.limit) params.limit = String(opts.limit);
  return get(`/api/v1/chess/refs/search${qs(params)}`);
};

// ---- Khóa học ----
export const listCourses = () => get("/api/v1/chess/courses");
export const getCourse = (id: string) => get(`/api/v1/chess/courses/${id}`);
// Giải mã wikilink [[course/<slug>]] → khóa học.
export const getCourseBySlug = (slug: string) => get(`/api/v1/chess/courses/by-slug/${encodeURIComponent(slug)}`);
export const getCourseBacklinks = (slug: string) => get(`/api/v1/chess/courses/by-slug/${encodeURIComponent(slug)}/backlinks`);
export const createCourse = (data: Partial<ChessCourse>) => post("/api/v1/chess/courses", data);
export const updateCourse = (id: string, data: Partial<ChessCourse>) => put(`/api/v1/chess/courses/${id}`, data);
export const deleteCourse = (id: string) => del(`/api/v1/chess/courses/${id}`);
// Export/Import khóa học (kèm bài học) dạng JSON — sao lưu/chia sẻ. Import luôn tạo mới.
export const exportCourses = () => get("/api/v1/chess/courses/export");
export const importCourses = (courses: any[]) => post("/api/v1/chess/courses/import", { courses });

// ---- Bài học ----
export const listLessons = (courseId: string) => get(`/api/v1/chess/courses/${courseId}/lessons`);
export const createLesson = (courseId: string, data: Partial<ChessLesson>) =>
  post(`/api/v1/chess/courses/${courseId}/lessons`, data);
export const getLesson = (lessonId: string) => get(`/api/v1/chess/lessons/${lessonId}`);
// Giải mã wikilink [[lesson/<slug>]] → bài giảng.
export const getLessonBySlug = (slug: string) => get(`/api/v1/chess/lessons/by-slug/${encodeURIComponent(slug)}`);
export const getLessonBacklinks = (slug: string) => get(`/api/v1/chess/lessons/by-slug/${encodeURIComponent(slug)}/backlinks`);
export const updateLesson = (lessonId: string, data: Partial<ChessLesson>) =>
  put(`/api/v1/chess/lessons/${lessonId}`, data);
export const deleteLesson = (lessonId: string) => del(`/api/v1/chess/lessons/${lessonId}`);

// ---- Kho ván đấu ----
export interface ChessGame {
  id: string;
  white: string; black: string; result: string;
  eco: string; event: string; date: string;
  pgn: string; ply_count: number;
  slug?: string;
  created_at?: string;
}
function qs(params: Record<string, string>): string {
  const p = Object.entries(params).filter(([, v]) => v).map(([k, v]) => `${k}=${encodeURIComponent(v)}`);
  return p.length ? `?${p.join("&")}` : "";
}
export const listGames = (f: Partial<{ white: string; black: string; eco: string; result: string }> = {}) =>
  get(`/api/v1/chess/games${qs(f as Record<string, string>)}`);
export const getGame = (id: string) => get(`/api/v1/chess/games/${id}`);
// Giải mã wikilink [[game/<slug>]] → ván cờ.
export const getGameBySlug = (slug: string) => get(`/api/v1/chess/games/by-slug/${encodeURIComponent(slug)}`);
export const getGameBacklinks = (slug: string) => get(`/api/v1/chess/games/by-slug/${encodeURIComponent(slug)}/backlinks`);
export const createGame = (data: Partial<ChessGame>) => post("/api/v1/chess/games", data);
export const updateGame = (id: string, data: Partial<ChessGame>) => put(`/api/v1/chess/games/${id}`, data);
// Đổi slug ván (giữ link cũ qua alias). slug sẽ được chuẩn hóa ở backend.
export const renameGameSlug = (id: string, slug: string) => put(`/api/v1/chess/games/${id}/slug`, { slug });
export const deleteGame = (id: string) => del(`/api/v1/chess/games/${id}`);
export const importGames = (pgn: string) => post("/api/v1/chess/games/import", { pgn });
// Export ván đấu (theo bộ lọc) thành PGN nhiều ván.
export const exportGamesPGN = (f: Partial<{ white: string; black: string; eco: string; result: string }> = {}) =>
  get(`/api/v1/chess/games/export${qs(f as Record<string, string>)}`);

// ---- Ngân hàng bài tập ----
export interface ChessPuzzle {
  id: string;
  title: string; fen: string; solution: string;
  theme: string; difficulty: string; source: string;
  slug?: string;
  created_at?: string;
}
export const listPuzzles = (f: Partial<{ theme: string; difficulty: string }> = {}) =>
  get(`/api/v1/chess/puzzles${qs(f as Record<string, string>)}`);
export const getPuzzle = (id: string) => get(`/api/v1/chess/puzzles/${id}`);
// Giải mã wikilink [[puzzle/<slug>]] → thế cờ/bài tập.
export const getPuzzleBySlug = (slug: string) => get(`/api/v1/chess/puzzles/by-slug/${encodeURIComponent(slug)}`);
export const getPuzzleBacklinks = (slug: string) => get(`/api/v1/chess/puzzles/by-slug/${encodeURIComponent(slug)}/backlinks`);
export const createPuzzle = (data: Partial<ChessPuzzle>) => post("/api/v1/chess/puzzles", data);
export const updatePuzzle = (id: string, data: Partial<ChessPuzzle>) => put(`/api/v1/chess/puzzles/${id}`, data);
// Đổi slug bài tập (giữ link cũ qua alias).
export const renamePuzzleSlug = (id: string, slug: string) => put(`/api/v1/chess/puzzles/${id}/slug`, { slug });
export const deletePuzzle = (id: string) => del(`/api/v1/chess/puzzles/${id}`);
export const randomPuzzle = (f: Partial<{ theme: string; difficulty: string }> = {}) =>
  get(`/api/v1/chess/puzzles/random${qs(f as Record<string, string>)}`);
// Export/Import bài tập dạng JSON — sao lưu/chia sẻ. Import luôn tạo mới.
export const exportPuzzles = (f: Partial<{ theme: string; difficulty: string }> = {}) =>
  get(`/api/v1/chess/puzzles/export${qs(f as Record<string, string>)}`);
export const importPuzzles = (puzzles: any[]) => post("/api/v1/chess/puzzles/import", { puzzles });

// ---- Ngân hàng thế cờ ----
// Khác ChessPuzzle (bài TẬP có lời giải, để LUYỆN): ChessPosition là thế cờ
// THAM CHIẾU để DẠY/trích dẫn/phân tích — CỐ Ý cho phép fen không có quân Vua
// (thế cờ giản lược dạy trẻ mới học). annotation là markdown CÓ THỂ chứa
// wikilink [[...]] (khác solution của puzzle, chỉ là chuỗi nước đi).
export interface ChessPosition {
  id: string;
  title: string; fen: string; fen_key: string;
  category: string; level: string; eco: string;
  side_to_move: string; orientation: string; assessment: string;
  annotation: string; tags: string; source: string;
  source_game_id: string; source_ply: number;
  slug?: string;
  created_at?: string;
}
export const listPositions = (
  f: Partial<{ category: string; level: string; eco: string; source_game_id: string; q: string }> = {},
) => get(`/api/v1/chess/positions${qs(f as Record<string, string>)}`);
export const getPosition = (id: string) => get(`/api/v1/chess/positions/${id}`);
// Giải mã wikilink [[position/<slug>]] → thế cờ.
export const getPositionBySlug = (slug: string) => get(`/api/v1/chess/positions/by-slug/${encodeURIComponent(slug)}`);
export const getPositionBacklinks = (slug: string) => get(`/api/v1/chess/positions/by-slug/${encodeURIComponent(slug)}/backlinks`);
export const createPosition = (data: Partial<ChessPosition>) => post("/api/v1/chess/positions", data);
export const updatePosition = (id: string, data: Partial<ChessPosition>) => put(`/api/v1/chess/positions/${id}`, data);
// Đổi slug thế cờ (giữ link cũ qua alias).
export const renamePositionSlug = (id: string, slug: string) => put(`/api/v1/chess/positions/${id}/slug`, { slug });
export const deletePosition = (id: string) => del(`/api/v1/chess/positions/${id}`);
// Thế cờ đã trích từ MỘT ván cụ thể (panel trong Kho ván).
export const listPositionsByGame = (gameId: string) =>
  get(`/api/v1/chess/positions${qs({ source_game_id: gameId })}`);
// Export/Import thế cờ dạng JSON — sao lưu/chia sẻ. Import luôn tạo mới.
export const exportPositions = (f: Partial<{ category: string; level: string; eco: string }> = {}) =>
  get(`/api/v1/chess/positions/export${qs(f as Record<string, string>)}`);
export const importPositions = (positions: any[]) => post("/api/v1/chess/positions/import", { positions });

// ---- Thư viện sách cờ vua (Kệ → Sách → Chương) ----
// Biên soạn NỘI BỘ (markdown + FEN + ván cờ + ảnh), KHÔNG phải kho ebook
// PDF/EPUB để đọc. Chỉ sách status="published" được index vào KB tri thức cờ.

export interface ChessShelf {
  id: string;
  title: string; kind: string; description: string; cover_url: string; sort_order: number;
  book_count?: number;
  slug?: string;
  created_at?: string;
}
export interface ChessBook {
  id: string;
  title: string; subtitle: string;
  author: string; translator: string; publisher: string; year: string; isbn: string; language: string;
  level: string; phase: string; eco: string; status: string;
  description: string; cover_url: string; tags: string; sort_order: number;
  chapter_count?: number;
  slug?: string;
  created_at?: string; updated_at?: string;
}
export interface ChessBookChapter {
  id: string; book_id: string;
  part: string; title: string; content: string; fen: string; level: string; sort_order: number;
  slug?: string;
  created_at?: string; updated_at?: string;
}
export interface ChessChapterRevision {
  id: string; chapter_id: string; revision_number: number;
  title: string; content: string; summary: string; created_by: string; created_at: string;
}

// ---- Kệ ----
export const listShelves = (f: Partial<{ kind: string; q: string }> = {}) =>
  get(`/api/v1/chess/shelves${qs(f as Record<string, string>)}`);
export const getShelf = (id: string) => get(`/api/v1/chess/shelves/${id}`);
export const getShelfBySlug = (slug: string) => get(`/api/v1/chess/shelves/by-slug/${encodeURIComponent(slug)}`);
export const createShelf = (data: Partial<ChessShelf>) => post("/api/v1/chess/shelves", data);
export const updateShelf = (id: string, data: Partial<ChessShelf>) => put(`/api/v1/chess/shelves/${id}`, data);
export const renameShelfSlug = (id: string, slug: string) => put(`/api/v1/chess/shelves/${id}/slug`, { slug });
export const deleteShelf = (id: string) => del(`/api/v1/chess/shelves/${id}`);
// Ghi đè toàn bộ danh sách sách trên một kệ (nhiều-nhiều) theo đúng thứ tự truyền vào.
export const setShelfBooks = (id: string, bookIds: string[]) => put(`/api/v1/chess/shelves/${id}/books`, { book_ids: bookIds });

// ---- Sách ----
export const listBooks = (
  f: Partial<{ shelf_id: string; level: string; phase: string; status: string; q: string }> = {},
) => get(`/api/v1/chess/books${qs(f as Record<string, string>)}`);
export const getBook = (id: string) => get(`/api/v1/chess/books/${id}`);
// Giải mã wikilink [[book/<slug>]] → sách.
export const getBookBySlug = (slug: string) => get(`/api/v1/chess/books/by-slug/${encodeURIComponent(slug)}`);
export const getBookBacklinks = (slug: string) => get(`/api/v1/chess/books/by-slug/${encodeURIComponent(slug)}/backlinks`);
// Kệ đang chứa sách (hiển thị ở form sửa sách).
export const listShelvesOfBook = (id: string) => get(`/api/v1/chess/books/${id}/shelves`);
export const createBook = (data: Partial<ChessBook>) => post("/api/v1/chess/books", data);
export const updateBook = (id: string, data: Partial<ChessBook>) => put(`/api/v1/chess/books/${id}`, data);
// Đổi slug sách (giữ link cũ qua alias).
export const renameBookSlug = (id: string, slug: string) => put(`/api/v1/chess/books/${id}/slug`, { slug });
// Xóa sách — cascade chương/kệ/ảnh/lịch sử ở backend.
export const deleteBook = (id: string) => del(`/api/v1/chess/books/${id}`);
// Export/Import sách (kèm chương) dạng JSON — sao lưu/chia sẻ. Import luôn tạo mới.
export const exportBooks = (f: Partial<{ shelf_id: string; level: string; phase: string; status: string }> = {}) =>
  get(`/api/v1/chess/books/export${qs(f as Record<string, string>)}`);
export const importBooks = (books: any[]) => post("/api/v1/chess/books/import", { books });

// ---- Ảnh chèn trong chương ----
// Upload ảnh (multipart) → {id, url}. url là đường dẫn ổn định
// (GET /api/v1/chess/books/images/:id), dùng trực tiếp trong markdown chương —
// KHÔNG phải presigned URL có hạn dùng.
export const uploadBookImage = (bookId: string, file: File, onProgress?: (e: any) => void) => {
  const form = new FormData();
  form.append("file", file);
  return postUpload(`/api/v1/chess/books/${bookId}/images`, form, onProgress);
};
export const bookImageURL = (imageId: string) => `/api/v1/chess/books/images/${imageId}`;

// ---- Chương ----
export const listChapters = (bookId: string) => get(`/api/v1/chess/books/${bookId}/chapters`);
export const createChapter = (bookId: string, data: Partial<ChessBookChapter>) =>
  post(`/api/v1/chess/books/${bookId}/chapters`, data);
// Ghi lại thứ tự chương theo đúng danh sách chapterIds truyền vào.
export const reorderChapters = (bookId: string, chapterIds: string[]) =>
  put(`/api/v1/chess/books/${bookId}/chapters/reorder`, { chapter_ids: chapterIds });
export const getChapter = (id: string) => get(`/api/v1/chess/chapters/${id}`);
// Giải mã wikilink [[chapter/<slug>]] → chương.
export const getChapterBySlug = (slug: string) => get(`/api/v1/chess/chapters/by-slug/${encodeURIComponent(slug)}`);
export const getChapterBacklinks = (slug: string) => get(`/api/v1/chess/chapters/by-slug/${encodeURIComponent(slug)}/backlinks`);
// summary (tùy chọn): ghi chú thay đổi, lưu kèm bản phiên bản mới nếu title/content đổi.
export const updateChapter = (id: string, data: Partial<ChessBookChapter> & { summary?: string }) =>
  put(`/api/v1/chess/chapters/${id}`, data);
// Đổi slug chương (giữ link cũ qua alias).
export const renameChapterSlug = (id: string, slug: string) => put(`/api/v1/chess/chapters/${id}/slug`, { slug });
export const deleteChapter = (id: string) => del(`/api/v1/chess/chapters/${id}`);

// ---- Lịch sử phiên bản chương ----
export const listChapterRevisions = (chapterId: string) => get(`/api/v1/chess/chapters/${chapterId}/revisions`);
export const getChapterRevision = (chapterId: string, revId: string) =>
  get(`/api/v1/chess/chapters/${chapterId}/revisions/${revId}`);
export const restoreChapterRevision = (chapterId: string, revId: string) =>
  post(`/api/v1/chess/chapters/${chapterId}/revisions/${revId}/restore`, {});
