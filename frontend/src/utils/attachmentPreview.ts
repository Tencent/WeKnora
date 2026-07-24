const previewSupportedExtensions = new Set([
  'pdf',
  'docx', 'doc',
  'pptx', 'ppt',
  'xlsx', 'xls', 'csv',
  'md', 'markdown',
  'txt', 'json', 'xml', 'html', 'css', 'js', 'ts', 'py', 'java', 'go',
  'cpp', 'c', 'h', 'sh', 'yaml', 'yml', 'ini', 'conf', 'log', 'sql', 'rs', 'rb', 'php',
  'swift', 'kt', 'scala', 'r', 'lua', 'pl', 'toml',
  'jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tiff', 'svg',
  'mp3', 'wav', 'm4a', 'flac', 'ogg',
]);

export type ChatAttachmentLike = {
  id?: string;
  file_name?: string;
  file_type?: string;
};

export function resolveAttachmentFileType(fileName?: string, fileType?: string): string {
  const normalizedType = String(fileType || '')
    .trim()
    .replace(/^\./, '')
    .toLowerCase();
  if (normalizedType) return normalizedType;
  return String(fileName || '')
    .split('.')
    .pop()
    ?.toLowerCase() || '';
}

export function isPreviewableAttachment(attachment: ChatAttachmentLike | null | undefined): boolean {
  if (!attachment?.id) return false;
  const fileType = resolveAttachmentFileType(attachment.file_name, attachment.file_type);
  return previewSupportedExtensions.has(fileType);
}
