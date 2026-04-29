/**
 * Format a byte count as a human-readable string (B / KB / MB).
 * Returns '' for null/undefined/non-positive values so callers can use it as a v-if guard.
 */
export function formatFileSize(bytes?: number | string | null): string {
  if (bytes == null || bytes === '') return '';
  const n = typeof bytes === 'string' ? parseInt(bytes, 10) : bytes;
  if (Number.isNaN(n) || n <= 0) return '';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * Map a knowledge item or filename to a TDesign icon name.
 * Accepts either a string (filename) or a knowledge-like object with `type`/`file_type`/`file_name`.
 */
export function getFileIcon(input: string | { type?: string; file_type?: string; file_name?: string }): string {
  let type: string | undefined;
  let ext: string | undefined;
  if (typeof input === 'string') {
    ext = input.split('.').pop()?.toLowerCase();
  } else {
    type = input.type;
    if (type === 'manual') return 'edit';
    if (type === 'url') return 'link';
    ext = (input.file_type || input.file_name?.split('.').pop() || '').toLowerCase();
  }
  if (!ext) return 'file';
  if (['pdf'].includes(ext)) return 'file-pdf';
  if (['doc', 'docx'].includes(ext)) return 'file-word';
  if (['xls', 'xlsx', 'csv'].includes(ext)) return 'file-excel';
  if (['ppt', 'pptx'].includes(ext)) return 'file-powerpoint';
  // TDesign 无稳定 `file-text` 字形时会导致空白，纯文类统一用 `file`
  if (['txt', 'md', 'markdown', 'json', 'log', 'yaml', 'yml', 'xml'].includes(ext)) return 'file';
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp'].includes(ext)) return 'image';
  if (['mp3', 'wav', 'm4a', 'flac', 'ogg', 'aac'].includes(ext)) return 'sound';
  return 'file';
}
