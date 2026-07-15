export interface RelativeFolderFile { file: File; clientKey: string; segments: string[] }

export interface FolderSelection {
  id: string;
  name: string;
  kbId: string;
  kbName?: string;
}

export interface FolderScope {
  knowledge_base_id: string;
  folder_ids: string[];
}

export interface FolderMentionState {
  id: string;
  name?: string;
  type: string;
  kb_id?: string;
  kb_name?: string;
}

export interface FolderPage<T> {
  items: T[];
  page: number;
  total: number;
  loading: boolean;
}

export type FolderTreeRow<T> =
  | { kind: 'folder'; folder: T; depth: number }
  | { kind: 'more'; parentId: string; depth: number };

export function folderIDFromQuery(value: unknown): string {
  return typeof value === 'string' && value !== 'root' ? value : '';
}

export function serializeFolderScopes(folders: FolderSelection[]): FolderScope[] | undefined {
  const grouped = new Map<string, Set<string>>();
  for (const folder of folders) {
    if (!folder.kbId || !folder.id) continue;
    const ids = grouped.get(folder.kbId) || new Set<string>();
    ids.add(folder.id);
    grouped.set(folder.kbId, ids);
  }
  if (!grouped.size) return undefined;
  return [...grouped].map(([knowledge_base_id, folderIds]) => ({
    knowledge_base_id,
    folder_ids: [...folderIds],
  }));
}

export function removeFolderSelection(
  folders: FolderSelection[],
  folderId: string,
  kbId?: string,
): FolderSelection[] {
  return folders.filter(folder => !(folder.id === folderId && (!kbId || folder.kbId === kbId)));
}

export function restoreFolderSelections(
  mentionedItems?: FolderMentionState[],
  folderScopes?: FolderScope[],
): FolderSelection[] {
  const mentions: FolderSelection[] = (mentionedItems || [])
    .filter(item => item.type === 'folder' && item.id && item.kb_id)
    .map(item => ({
      id: item.id,
      name: item.name || item.id,
      kbId: item.kb_id!,
      kbName: item.kb_name,
    }));
  const fallback: FolderSelection[] = (folderScopes || []).flatMap(scope =>
    (scope.folder_ids || []).map(id => ({ id, name: id, kbId: scope.knowledge_base_id })),
  );
  const unique = new Map<string, FolderSelection>(mentions.map(folder => [`${folder.kbId}\u0000${folder.id}`, folder]));
  for (const folder of fallback) {
    const key = `${folder.kbId}\u0000${folder.id}`;
    if (!unique.has(key)) unique.set(key, folder);
  }
  return [...unique.values()];
}

export function filterFolderMentionKnowledgeBases<T extends { id: string; type?: string }>(
  knowledgeBases: T[],
  allowedKbIds: ReadonlySet<string> | null,
): T[] {
  return knowledgeBases.filter(kb =>
    kb.type !== 'faq' && (!allowedKbIds || allowedKbIds.has(String(kb.id))),
  );
}

export function mergeFolderPage<T extends { id: string }>(
  current: FolderPage<T> | undefined,
  nextItems: T[],
  page: number,
  total: number | undefined,
  reset = false,
): FolderPage<T> {
  const prior = reset ? [] : current?.items || [];
  const byId = new Map([...prior, ...nextItems].map(folder => [folder.id, folder]));
  return {
    items: [...byId.values()],
    page,
    total: Number(total ?? byId.size),
    loading: false,
  };
}

export function flattenFolderPages<T extends { id: string }>(
  pages: ReadonlyMap<string, FolderPage<T>>,
  expanded: ReadonlySet<string>,
  rootParentId = '',
): FolderTreeRow<T>[] {
  const result: FolderTreeRow<T>[] = [];
  const walk = (parentId: string, depth: number) => {
    const state = pages.get(parentId);
    for (const folder of state?.items || []) {
      result.push({ kind: 'folder', folder, depth });
      if (expanded.has(folder.id)) walk(folder.id, depth + 1);
    }
    if (state && state.items.length < state.total) result.push({ kind: 'more', parentId, depth });
  };
  walk(rootParentId, 0);
  return result;
}

export function parseRelativeFolderFiles(files: File[]): RelativeFolderFile[] {
  return files.map((file, index) => {
    const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name;
    const parts = relativePath.split('/').filter(Boolean);
    return { file, clientKey: `path-${index}`, segments: parts.slice(0, -1) };
  });
}

export function dedupeFolderPaths(items: RelativeFolderFile[]): Array<{ client_key: string; segments: string[] }> {
  const unique = new Map<string, { client_key: string; segments: string[] }>();
  for (const item of items) {
    if (!item.segments.length) continue;
    const key = JSON.stringify(item.segments);
    if (!unique.has(key)) unique.set(key, { client_key: key, segments: item.segments });
  }
  return [...unique.values()];
}

export function mapFilesToFolderIDs(items: RelativeFolderFile[], ensured: Array<{ client_key: string; folder_id: string }>): Map<File, string> {
  const byKey = new Map(ensured.map(item => [item.client_key, item.folder_id]));
  const result = new Map<File, string>();
  for (const item of items) result.set(item.file, item.segments.length ? (byKey.get(JSON.stringify(item.segments)) || '') : '');
  return result;
}

export async function runWithConcurrency<T>(tasks: Array<() => Promise<T>>, limit = 4): Promise<PromiseSettledResult<T>[]> {
  const results: PromiseSettledResult<T>[] = new Array(tasks.length);
  let cursor = 0;
  async function worker() {
    while (cursor < tasks.length) {
      const index = cursor++;
      try { results[index] = { status: 'fulfilled', value: await tasks[index]() }; }
      catch (reason) { results[index] = { status: 'rejected', reason }; }
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, tasks.length) }, worker));
  return results;
}
