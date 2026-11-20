import type { KnowledgeFolderNode, KnowledgeFolderTree } from '../api/knowledge-base';
import type { MentionItem } from '../types/mention';

export interface BrowserKnowledgeBase { id: string; name: string; orgName?: string }

/** Reserve the browser's maximum height inside the visual viewport (including mobile zoom). */
export function fitBrowserViewport(anchor: { left: number; top: number; width: number },
  viewport: { left: number; top: number; width: number; height: number }, zoom = 1) {
  const height = Math.min(360 * zoom, Math.max(0, viewport.height - 16));
  const width = Math.min(anchor.width, Math.max(0, viewport.width - 16));
  return {
    left: `${Math.max(viewport.left + 8, Math.min(anchor.left, viewport.left + viewport.width - width - 8)) / zoom}px`,
    top: `${Math.max(viewport.top + 8, Math.min(anchor.top, viewport.top + viewport.height - height - 8)) / zoom}px`,
    bottom: 'auto', minHeight: '0', maxHeight: `${height / zoom}px`, maxWidth: `${Math.max(0, viewport.width - 16) / zoom}px`,
  };
}

export interface BrowserFile extends MentionItem {
  type: 'file';
  kbId: string;
  folderPath: string;
  fileName: string;
  fileType: string;
  sourceType: string;
  parseStatus: string;
}

const record = (value: unknown): value is Record<string, any> => !!value && typeof value === 'object' && !Array.isArray(value);
const count = (value: unknown): value is number => Number.isSafeInteger(value) && (value as number) >= 0;

export function adaptFolderTree(response: unknown): KnowledgeFolderTree {
  if (!record(response) || response.success !== true || !record(response.data)) throw new Error('Invalid folder response');
  const tree = response.data;
  const validNodes = (nodes: unknown): nodes is KnowledgeFolderNode[] => Array.isArray(nodes) && nodes.every(node =>
    record(node) && typeof node.path === 'string' && !!node.path && typeof node.name === 'string' &&
    count(node.document_count) && count(node.total_count) && (node.children === undefined || validNodes(node.children)));
  if (!count(tree.root_document_count) || !count(tree.total_document_count) || !validNodes(tree.folders)) throw new Error('Invalid folder tree');
  return tree as KnowledgeFolderTree;
}

export function adaptFilePage(response: unknown, kb: BrowserKnowledgeBase) {
  if (!record(response) || response.success !== true || !Array.isArray(response.data) ||
    !count(response.total) || !count(response.page) || response.page < 1 ||
    !count(response.page_size) || response.page_size < 1) throw new Error('Invalid file page');
  const items: BrowserFile[] = response.data.map((file: unknown) => {
    if (!record(file) || typeof file.id !== 'string' || !file.id || file.knowledge_base_id !== kb.id ||
      typeof file.folder_path !== 'string' || typeof (file.title || file.file_name) !== 'string') throw new Error('Invalid file');
    return {
      id: file.id, name: file.title || file.file_name, type: 'file', kbId: kb.id, kbName: kb.name,
      orgName: kb.orgName, folderPath: file.folder_path, fileName: typeof file.file_name === 'string' ? file.file_name : '',
      fileType: file.file_type || '',
      sourceType: file.type || '', parseStatus: file.parse_status || '',
    };
  });
  return { items: [...new Map(items.map(item => [item.id, item])).values()], total: response.total,
    page: response.page, pageSize: response.page_size, hasMore: response.page * response.page_size < response.total };
}

/** Match the extension aliases used by the existing global file search. */
export function fileDisabledReason(file: BrowserFile, supported: readonly string[], selected: readonly string[] = []) {
  if (selected.includes(file.id)) return 'selected';
  if (file.parseStatus === 'deleting') return 'deleting';
  if (!supported.length) return '';
  const aliases: Record<string, string[]> = { doc: ['docx'], docx: ['doc'], xls: ['xlsx'], xlsx: ['xls'],
    jpg: ['jpeg', 'png'], jpeg: ['jpg', 'png'], png: ['jpg', 'jpeg'] };
  const types = new Set(supported.flatMap(type => {
    const ext = type.replace(/^\./, '').toLowerCase();
    return [ext, ...(aliases[ext] || [])];
  }));
  // SearchKnowledgeInScopes treats url/html as URL sources, and all other
  // formats as filename suffixes. Display titles and parser types are editable
  // metadata and must not make an otherwise incompatible file selectable.
  if ((types.has('url') || types.has('html')) && file.sourceType === 'url') return '';
  types.delete('url');
  types.delete('html');
  const dot = file.fileName.lastIndexOf('.');
  const extension = dot >= 0 ? file.fileName.slice(dot + 1).toLowerCase() : '';
  return types.has(extension) ? '' : 'unsupported';
}

export function browserErrorKind(error: unknown): 'forbidden' | 'missing' | 'error' {
  const status = record(error) ? error.$httpStatus ?? error.status : undefined;
  return status === 401 || status === 403 ? 'forbidden' : status === 404 ? 'missing' : 'error';
}
