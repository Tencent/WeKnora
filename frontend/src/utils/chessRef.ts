import type { ChessBoardData } from '@/types/tool-results';
import {
  getGameBySlug, getPuzzleBySlug, getLessonBySlug, getCourseBySlug, getPositionBySlug,
  getBookBySlug, getChapterBySlug,
  getGameBacklinks, getPuzzleBacklinks, getLessonBacklinks, getCourseBacklinks, getPositionBacklinks,
  getBookBacklinks, getChapterBacklinks,
} from '@/api/chess';

// Giải mã wikilink cờ vua [[game/<slug>]] / [[puzzle/<slug>]] / [[lesson/<slug>]]
// / [[course/<slug>]] / [[position/<slug>]] / [[book/<slug>]] / [[chapter/<slug>]]
// về đối tượng tương ứng + dữ liệu bàn cờ để render (khóa học/sách KHÔNG có bàn
// cờ → board=null). Có cache để một trang nhiều chip/embed không gọi API trùng.

export type ChessRefType = 'game' | 'puzzle' | 'lesson' | 'course' | 'position' | 'book' | 'chapter';

const REF_TYPES: ChessRefType[] = ['game', 'puzzle', 'lesson', 'course', 'position', 'book', 'chapter'];

const START_FEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';

export interface ResolvedChessRef {
  type: ChessRefType;
  slug: string; // slug trần (không tiền tố)
  ref: string; // "game/<slug>"
  title: string; // nhãn hiển thị
  board: ChessBoardData | null; // dữ liệu bàn cờ (lesson có thể null nếu không có FEN/PGN)
  found: boolean;
  raw?: any; // đối tượng gốc (game/puzzle/lesson)
}

// parseChessRef tách "game/abc" → { type:'game', slug:'abc' }. Trả null nếu
// không phải tham chiếu cờ hợp lệ.
export function parseChessRef(ref: string): { type: ChessRefType; slug: string } | null {
  if (!ref) return null;
  const i = ref.indexOf('/');
  if (i <= 0) return null;
  const type = ref.slice(0, i).toLowerCase() as ChessRefType;
  const slug = ref.slice(i + 1).trim();
  if (!slug || !REF_TYPES.includes(type)) return null;
  return { type, slug };
}

export function isChessRef(ref: string): boolean {
  return parseChessRef(ref) !== null;
}

function gameTitle(g: any): string {
  const white = g?.white || '?';
  const black = g?.black || '?';
  let t = `${white} – ${black}`;
  if (g?.event) t += `, ${g.event}`;
  const year = typeof g?.date === 'string' ? g.date.slice(0, 4) : '';
  if (year && /^\d{4}$/.test(year)) t += ` ${year}`;
  return t;
}

const cache = new Map<string, Promise<ResolvedChessRef>>();

function notFound(type: ChessRefType, slug: string): ResolvedChessRef {
  return { type, slug, ref: `${type}/${slug}`, title: `${type}/${slug}`, board: null, found: false };
}

async function doResolve(ref: string): Promise<ResolvedChessRef> {
  const parsed = parseChessRef(ref);
  if (!parsed) throw new Error(`không phải tham chiếu cờ: ${ref}`);
  const { type, slug } = parsed;
  if (type === 'game') {
    const res: any = await getGameBySlug(slug);
    const g = res?.data;
    if (!g) return notFound(type, slug);
    const title = gameTitle(g);
    return {
      type, slug, ref, title, found: true, raw: g,
      board: { display_type: 'chess_board', fen: START_FEN, pgn: g.pgn, caption: title },
    };
  }
  if (type === 'puzzle') {
    const res: any = await getPuzzleBySlug(slug);
    const p = res?.data;
    if (!p) return notFound(type, slug);
    const title = p.title || slug;
    return {
      type, slug, ref, title, found: true, raw: p,
      board: { display_type: 'chess_board', fen: p.fen || START_FEN, caption: title },
    };
  }
  if (type === 'course') {
    // Khóa học không có bàn cờ → board=null, chỉ là thẻ điều hướng.
    const res: any = await getCourseBySlug(slug);
    const c = res?.data;
    if (!c) return notFound(type, slug);
    return { type, slug, ref, title: c.title || slug, board: null, found: true, raw: c };
  }
  if (type === 'position') {
    const res: any = await getPositionBySlug(slug);
    const p = res?.data;
    if (!p) return notFound(type, slug);
    const title = p.title || slug;
    // CỐ Ý không set `pgn`: thế cờ trong Ngân hàng thế cờ có thể không có quân
    // Vua (dạy trẻ mới học) — ChessBoardDisplay chỉ đi qua chess.js khi có pgn,
    // và chess.js từ chối FEN thiếu Vua. Chỉ fen (+ orientation) là an toàn.
    return {
      type, slug, ref, title, found: true, raw: p,
      board: {
        display_type: 'chess_board',
        fen: p.fen || START_FEN,
        orientation: p.orientation === 'b' ? 'black' : 'white',
        caption: title,
      },
    };
  }
  if (type === 'book') {
    // Sách không có bàn cờ → board=null, chỉ là thẻ điều hướng (giống course).
    const res: any = await getBookBySlug(slug);
    const b = res?.data;
    if (!b) return notFound(type, slug);
    return { type, slug, ref, title: b.title || slug, board: null, found: true, raw: b };
  }
  if (type === 'chapter') {
    const res: any = await getChapterBySlug(slug);
    const ch = res?.data;
    if (!ch) return notFound(type, slug);
    const title = ch.title || slug;
    const board: ChessBoardData | null = ch.fen && String(ch.fen).trim()
      ? { display_type: 'chess_board', fen: ch.fen, caption: title }
      : null;
    return { type, slug, ref, title, board, found: true, raw: ch };
  }
  // lesson
  const res: any = await getLessonBySlug(slug);
  const l = res?.data;
  if (!l) return notFound(type, slug);
  const title = l.title || slug;
  let board: ChessBoardData | null = null;
  if (l.pgn && String(l.pgn).trim()) {
    board = { display_type: 'chess_board', fen: START_FEN, pgn: l.pgn, caption: title };
  } else if (l.fen && String(l.fen).trim()) {
    board = { display_type: 'chess_board', fen: l.fen, caption: title };
  }
  return { type, slug, ref, title, board, found: true, raw: l };
}

export function resolveChessRef(ref: string): Promise<ResolvedChessRef> {
  const cached = cache.get(ref);
  if (cached) return cached;
  const p = doResolve(ref);
  cache.set(ref, p);
  // Lỗi (vd 404/timeout) → bỏ cache để lần sau thử lại.
  p.catch(() => cache.delete(ref));
  return p;
}

export interface ChessBacklink {
  source_type?: string; // 'wiki' | 'lesson' | 'position' — quyết định cách điều hướng
  kb_id: string;
  page_slug: string;
  page_title: string;
}

const BACKLINK_FN: Record<ChessRefType, (slug: string) => Promise<any>> = {
  game: getGameBacklinks,
  puzzle: getPuzzleBacklinks,
  lesson: getLessonBacklinks,
  course: getCourseBacklinks,
  position: getPositionBacklinks,
  book: getBookBacklinks,
  chapter: getChapterBacklinks,
};

// Lấy danh sách trang wiki/bài giảng đang trỏ tới đối tượng cờ.
export async function resolveChessBacklinks(ref: string): Promise<ChessBacklink[]> {
  const parsed = parseChessRef(ref);
  if (!parsed) return [];
  const { type, slug } = parsed;
  const fn = BACKLINK_FN[type];
  try {
    const res: any = await fn(slug);
    return res?.data || [];
  } catch {
    return [];
  }
}
