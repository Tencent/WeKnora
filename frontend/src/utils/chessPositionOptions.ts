// Danh sách phân loại + cấp độ dùng chung cho Ngân hàng thế cờ (PositionBank.vue,
// và nút "Lưu thế cờ này" trong GameLibrary.vue) — tách riêng để hai nơi không
// lệch mã code khi mở rộng.

// category: nhóm mục đích dạy (khớp WS mô tả trong plan tính năng).
export const positionCategoryOptions = [
  { label: 'Tabiya khai cuộc', value: 'tabiya' },
  { label: 'Tàn cuộc lý thuyết', value: 'endgame' },
  { label: 'Cấu trúc tốt', value: 'structure' },
  { label: 'Mô-típ chiến thuật', value: 'motif' },
  { label: 'Thế mẫu (trích từ ván)', value: 'model' },
  { label: 'Cơ bản (dạy trẻ mới học)', value: 'basic' },
];

// level: 6 cấp lộ trình Dương Sinh Tốt→Vua (xem .claude/memory/01-du-an-duongsinh.md).
export const positionLevelOptions = [
  { label: 'Tốt', value: 'tot' },
  { label: 'Mã', value: 'ma' },
  { label: 'Tượng', value: 'tuong' },
  { label: 'Xe', value: 'xe' },
  { label: 'Hậu', value: 'hau' },
  { label: 'Vua', value: 'vua' },
];

export function positionCategoryLabel(v: string): string {
  return positionCategoryOptions.find((o) => o.value === v)?.label || v;
}
export function positionLevelLabel(v: string): string {
  return positionLevelOptions.find((o) => o.value === v)?.label || v;
}
