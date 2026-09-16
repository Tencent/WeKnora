export interface KnowledgeDownloadItem {
  id: string;
  original_file_name?: string;
  file_name?: string;
  title?: string;
  type?: string;
}

export function resolveKnowledgeDownloadFileName(item: KnowledgeDownloadItem): string {
  const baseName = item.original_file_name || item.file_name || item.title || item.id;
  if (item.type === 'manual' && !baseName.toLowerCase().endsWith('.md')) {
    return `${baseName}.md`;
  }
  return baseName;
}

export function isBatchDownloadableKnowledge(item?: { type?: string; file_path?: string } | null): boolean {
  if (!item) return false;
  if (item.type === 'manual') return true;
  return Boolean(item.file_path);
}
