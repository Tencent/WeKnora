// Danh sách phân loại + nhãn dùng chung cho Thư viện sách cờ vua
// (BookLibrary.vue, ChessShelfManager.vue) — tách riêng để nhiều nơi không lệch
// mã code khi mở rộng, cùng mẫu utils/chessPositionOptions.ts.

// level: 6 cấp lộ trình Dương Sinh Tốt→Vua (xem .claude/memory/01-du-an-duongsinh.md).
export const bookLevelOptions = [
  { label: 'Tốt', value: 'tot' },
  { label: 'Mã', value: 'ma' },
  { label: 'Tượng', value: 'tuong' },
  { label: 'Xe', value: 'xe' },
  { label: 'Hậu', value: 'hau' },
  { label: 'Vua', value: 'vua' },
];

// phase: giai đoạn ván cờ sách tập trung.
export const bookPhaseOptions = [
  { label: 'Khai cuộc', value: 'opening' },
  { label: 'Trung cuộc', value: 'middlegame' },
  { label: 'Tàn cuộc', value: 'endgame' },
  { label: 'Chiến thuật', value: 'tactics' },
  { label: 'Luật cờ', value: 'rules' },
  { label: 'Tổng quát', value: 'general' },
];

// status: trạng thái xuất bản NỘI BỘ — draft KHÔNG được index vào KB tri thức cờ.
export const bookStatusOptions = [
  { label: 'Bản thảo', value: 'draft' },
  { label: 'Đã xuất bản', value: 'published' },
];

// kind: loại kệ (chuyên đề/giai đoạn/bộ sách/cấp độ).
export const shelfKindOptions = [
  { label: 'Chuyên đề', value: 'theme' },
  { label: 'Giai đoạn ván cờ', value: 'phase' },
  { label: 'Bộ sách', value: 'series' },
  { label: 'Cấp độ', value: 'level' },
];

export function bookLevelLabel(v: string): string {
  return bookLevelOptions.find((o) => o.value === v)?.label || v;
}
export function bookPhaseLabel(v: string): string {
  return bookPhaseOptions.find((o) => o.value === v)?.label || v;
}
export function bookStatusLabel(v: string): string {
  return bookStatusOptions.find((o) => o.value === v)?.label || v;
}
export function shelfKindLabel(v: string): string {
  return shelfKindOptions.find((o) => o.value === v)?.label || v;
}
