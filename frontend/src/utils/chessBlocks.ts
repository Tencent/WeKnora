import type { ChessBoardData } from '@/types/tool-results';

// Tiện ích bóc tách khối ``` chess ``` trong nội dung câu trả lời để render bàn
// cờ tương tác. Dùng hàm THUẦN (không đụng DOM) — botmsg.vue render bàn cờ bằng
// component Vue thật trong template, nằm NGOÀI vùng v-stable-html, nên không bị
// directive đó morph/ghi đè.

const START_FEN = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';

/**
 * Kiểm tra nhanh tính hợp lệ CẤU TRÚC của một chuỗi FEN (8 hàng, mỗi hàng đủ 8 ô,
 * ký tự quân hợp lệ, lượt đi w/b nếu có). KHÔNG kiểm tra hợp lệ luật cờ đầy đủ —
 * backend làm việc đó. Mục đích: chặn lỗi gõ sai ở ô nhập trước khi lưu để báo lỗi
 * rõ ràng thay vì "Lưu thất bại". Hàm thuần, không import thư viện nặng (chess.js).
 */
export function isValidFEN(fen: string): boolean {
  const s = (fen || '').trim();
  if (!s) return false;
  const parts = s.split(/\s+/);
  const ranks = parts[0].split('/');
  if (ranks.length !== 8) return false;
  for (const rank of ranks) {
    if (!/^[pnbrqkPNBRQK1-8]+$/.test(rank)) return false;
    let count = 0;
    for (const ch of rank) count += /[1-8]/.test(ch) ? Number(ch) : 1;
    if (count !== 8) return false;
  }
  if (parts.length >= 2 && parts[1] !== 'w' && parts[1] !== 'b') return false;
  return true;
}

// Khớp một khối mã fenced ```chess ... ``` (đã đóng).
const CHESS_BLOCK_RE = /```[ \t]*chess[^\n]*\n([\s\S]*?)```/gi;

function looksLikePGN(text: string): boolean {
  return /\[\s*\w+\s+"[^"]*"\s*\]/.test(text) || /(^|\s)\d+\.(\.\.)?\s*\S/.test(text);
}

function parseChessSource(src: string): ChessBoardData | null {
  const text = (src || '').trim();
  if (!text) return null;
  if (looksLikePGN(text)) {
    return { display_type: 'chess_board', fen: START_FEN, pgn: text };
  }
  const firstLine = text.split('\n').map((s) => s.trim()).find(Boolean) || '';
  if (firstLine.includes('/')) {
    return { display_type: 'chess_board', fen: firstLine };
  }
  return null;
}

/** Bóc tất cả khối ```chess đã đóng trong markdown thành danh sách dữ liệu bàn cờ. */
export function extractChessBlocks(markdown: string): ChessBoardData[] {
  if (!markdown || typeof markdown !== 'string' || !markdown.includes('chess')) return [];
  const boards: ChessBoardData[] = [];
  CHESS_BLOCK_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = CHESS_BLOCK_RE.exec(markdown)) !== null) {
    const data = parseChessSource(m[1]);
    if (data) boards.push(data);
  }
  return boards;
}

/** Loại bỏ các khối ```chess đã đóng khỏi markdown (để không hiện thành code block trùng). */
export function stripChessBlocks(markdown: string): string {
  if (!markdown || typeof markdown !== 'string' || !markdown.includes('chess')) return markdown;
  return markdown.replace(CHESS_BLOCK_RE, '').replace(/\n{3,}/g, '\n\n');
}

// Một đoạn nội dung sau khi tách: markdown thuần, một bàn cờ (từ khối ```chess),
// hoặc một tham chiếu cờ NHÚNG (từ ![[game/<slug>]]).
export interface ChessRefSeg {
  refType: 'game' | 'puzzle' | 'lesson' | 'course' | 'position';
  slug: string; // slug trần
  ref: string; // "game/<slug>"
  label?: string; // nhãn tùy chọn từ cú pháp ![[ref|nhãn]]
}
export interface ChessSegment {
  type: 'markdown' | 'board' | 'ref';
  // markdown thô (người gọi tự render bằng renderer của mình); chỉ có khi type==='markdown'.
  markdown?: string;
  // dữ liệu bàn cờ; chỉ có khi type==='board'.
  board?: ChessBoardData;
  // tham chiếu cờ nhúng; chỉ có khi type==='ref'.
  ref?: ChessRefSeg;
}

/**
 * Tách markdown thành danh sách đoạn THEO ĐÚNG THỨ TỰ, để người gọi render bàn
 * cờ ngay tại vị trí khối ```chess (inline) thay vì dồn xuống cuối. Khối ```chess
 * không phân tích được sẽ giữ nguyên trong đoạn markdown (hiện thành code block —
 * fallback an toàn). Dùng cùng bộ phân tích với extractChessBlocks/stripChessBlocks.
 */
export function splitChessSegments(markdown: string): ChessSegment[] {
  if (!markdown || typeof markdown !== 'string') return [];
  if (!markdown.includes('chess')) return [{ type: 'markdown', markdown }];
  const segments: ChessSegment[] = [];
  let lastIndex = 0;
  CHESS_BLOCK_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = CHESS_BLOCK_RE.exec(markdown)) !== null) {
    const data = parseChessSource(m[1]);
    if (!data) continue; // không parse được → để nguyên trong markdown bao quanh
    const before = markdown.slice(lastIndex, m.index);
    if (before.trim()) segments.push({ type: 'markdown', markdown: before });
    segments.push({ type: 'board', board: data });
    lastIndex = CHESS_BLOCK_RE.lastIndex;
  }
  const tail = markdown.slice(lastIndex);
  if (tail.trim()) segments.push({ type: 'markdown', markdown: tail });
  if (segments.length === 0) segments.push({ type: 'markdown', markdown });
  return segments;
}

// Các loại tham chiếu cờ hợp lệ cho wikilink.
const CHESS_REF_TYPES = 'game|puzzle|lesson|course|position';
// NHÚNG inline: ![[game/<slug>]] hoặc ![[game/<slug>|nhãn]] → bàn cờ inline.
const CHESS_EMBED_RE = new RegExp(`!\\[\\[(${CHESS_REF_TYPES})/([^\\]|]+?)(?:\\|([^\\]]+))?\\]\\]`, 'g');
// CHIP inline: [[game/<slug>]] (KHÔNG có dấu ! phía trước) → liên kết <a> bấm mở popup.
const CHESS_CHIP_RE = new RegExp(`(?<!!)\\[\\[(${CHESS_REF_TYPES})/([^\\]|]+?)(?:\\|([^\\]]+))?\\]\\]`, 'g');

function escapeHtmlAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function escapeHtmlText(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function defaultRefLabel(slug: string): string {
  return slug.replace(/-/g, ' ');
}

/**
 * Thay các CHIP tham chiếu cờ [[game/<slug>]] trong markdown thành thẻ <a> để
 * render INLINE trong dòng chữ (tái dùng cơ chế click-delegation như wiki link).
 * Gọi TRƯỚC khi thay [[wiki-slug]] thường (chip cờ đã bị tiêu thụ nên không bị
 * nhầm thành link wiki). NHÚNG ![[...]] được splitChessContent tách riêng nên
 * không lọt vào đây.
 */
export function renderChessChips(md: string): string {
  if (!md || !md.includes('[[')) return md;
  return md.replace(CHESS_CHIP_RE, (_m, type: string, slug: string, label?: string) => {
    const ref = `${type}/${slug.trim()}`;
    const text = (label && label.trim()) || defaultRefLabel(slug.trim());
    return `<a href="#" class="chess-ref-link" data-chess-ref="${escapeHtmlAttr(ref)}" data-chess-type="${type}">${escapeHtmlText(text)}</a>`;
  });
}

/**
 * Tách markdown thành đoạn THEO THỨ TỰ, gồm: markdown, bàn cờ (khối ```chess), và
 * tham chiếu cờ NHÚNG (![[game/<slug>]]). CHIP [[game/<slug>]] KHÔNG tách ở đây —
 * nó được render inline trong đoạn markdown qua renderChessChips. Mở rộng của
 * splitChessSegments (giữ nguyên hàm cũ cho nơi khác).
 */
export function splitChessContent(markdown: string): ChessSegment[] {
  const base = splitChessSegments(markdown);
  const out: ChessSegment[] = [];
  for (const seg of base) {
    if (seg.type !== 'markdown' || !seg.markdown) {
      out.push(seg);
      continue;
    }
    out.push(...splitChessEmbeds(seg.markdown));
  }
  return out;
}

function splitChessEmbeds(md: string): ChessSegment[] {
  if (!md.includes('![[')) return [{ type: 'markdown', markdown: md }];
  const segs: ChessSegment[] = [];
  let last = 0;
  CHESS_EMBED_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = CHESS_EMBED_RE.exec(md)) !== null) {
    const before = md.slice(last, m.index);
    if (before) segs.push({ type: 'markdown', markdown: before });
    const slug = m[2].trim();
    segs.push({
      type: 'ref',
      ref: { refType: m[1] as ChessRefSeg['refType'], slug, ref: `${m[1]}/${slug}`, label: m[3]?.trim() },
    });
    last = CHESS_EMBED_RE.lastIndex;
  }
  const tail = md.slice(last);
  if (tail) segs.push({ type: 'markdown', markdown: tail });
  if (segs.length === 0) segs.push({ type: 'markdown', markdown: md });
  return segs;
}
