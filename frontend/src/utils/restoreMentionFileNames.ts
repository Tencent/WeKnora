import type { batchQueryKnowledge } from '../api/knowledge-base';
import { KNOWLEDGE_BATCH_SIZE } from '../api/knowledge-base/knowledgeReadOptions';
import type { DraftFile } from './mentionFileSelection';

interface SharedFileScope {
  agentId: string;
  sourceTenantId: string;
}

export async function restoreMentionFileNames(
  ids: readonly string[],
  kbByFile: Readonly<Record<string, string>>,
  scope: SharedFileScope | undefined,
  queryBatch: typeof batchQueryKnowledge,
  signal: AbortSignal,
): Promise<DraftFile[]> {
  const groups = new Map<string | undefined, string[]>();
  for (const id of new Set(ids)) {
    const kbId = kbByFile[id];
    const group = groups.get(kbId);
    if (group) group.push(id);
    else groups.set(kbId, [id]);
  }
  const files = new Map<string, DraftFile>();
  const read = async (ids: string[], kbId?: string, agent?: SharedFileScope) => {
    const query = new URLSearchParams();
    for (const id of ids) query.append('ids', id);
    const requested = new Set(ids);
    try {
      const response = await queryBatch(query.toString(), kbId, agent?.agentId, agent?.sourceTenantId, signal);
      if (signal.aborted || response.success !== true || !Array.isArray(response.data)) return;
      for (const row of response.data) {
        const name = row?.title || row?.file_name;
        const owner = row?.knowledge_base_id;
        if (!requested.has(row?.id) || typeof owner !== 'string' || !owner ||
            (kbId && owner !== kbId) || typeof name !== 'string' || !name) continue;
        files.set(row.id, { id: row.id, name, kbId: owner, folderPath: row.folder_path });
      }
    } catch {
      // One inaccessible or failed batch must not discard other restored names.
    }
  };
  for (const [kbId, group] of groups) {
    for (let offset = 0; offset < group.length; offset += KNOWLEDGE_BATCH_SIZE) {
      if (signal.aborted) return [];
      const batch = group.slice(offset, offset + KNOWLEDGE_BATCH_SIZE);
      await read(batch, kbId);
      if (signal.aborted) return [];
      const missing = () => batch.filter(id => !files.has(id));
      if (scope && missing().length) await read(missing(), kbId, scope);
      // A history hint can predate a move that retained the document ID.
      if (!signal.aborted && kbId && missing().length) await read(missing());
      if (!signal.aborted && kbId && scope && missing().length) await read(missing(), undefined, scope);
    }
  }
  return signal.aborted ? [] : [...files.values()];
}
