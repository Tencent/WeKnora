import type { DocumentFolderSearchResult } from '@/api/knowledge-base';
import type { MentionItem } from '@/types/mention';

interface MentionFolderSearchOptions {
  allowedKbIds: Iterable<string>;
  orgNameByKbId?: Record<string, string>;
  agentOrgName?: string;
}

export function mapMentionFolderSearchResults(
  rows: DocumentFolderSearchResult[],
  options: MentionFolderSearchOptions,
): MentionItem[] {
  const allowedKbIds = new Set(Array.from(options.allowedKbIds, String));
  return rows
    .filter(folder => allowedKbIds.has(String(folder.knowledge_base_id)))
    .map(folder => ({
      id: folder.id,
      name: folder.name,
      type: 'folder',
      kbId: folder.knowledge_base_id,
      kbName: folder.knowledge_base_name,
      orgName: options.agentOrgName
        || options.orgNameByKbId?.[String(folder.knowledge_base_id)]
        || undefined,
      folderPath: folder.path || folder.name,
      parentId: folder.parent_id,
      hasChildren: false,
    }));
}

export function mapMentionFolderSearchPage(
  response: {
    data?: DocumentFolderSearchResult[];
    total?: number;
    has_more?: boolean;
  },
  options: MentionFolderSearchOptions,
) {
  const rows = Array.isArray(response.data) ? response.data : [];
  const items = mapMentionFolderSearchResults(rows, options);
  return {
    items,
    total: typeof response.total === 'number' ? response.total : items.length,
    hasMore: response.has_more === true,
    // Advance by the server page size, not by the client-filtered item count,
    // so an unexpected out-of-scope row cannot make the next page overlap.
    consumed: rows.length,
  };
}
