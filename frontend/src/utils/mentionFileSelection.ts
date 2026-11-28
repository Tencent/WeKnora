import { KNOWLEDGE_BATCH_SIZE, type KnowledgeReadOptions } from '../api/knowledge-base/knowledgeReadOptions';
import {
  adaptBrowserFile,
  browserErrorKind,
  fileDisabledReason,
  type BrowserKnowledgeBase,
} from './mentionFileBrowser';

export interface DraftFile {
  id: string;
  name: string;
  kbId?: string;
  folderPath?: string;
}

export interface FileSelection {
  contextKey: string;
  files: DraftFile[];
  replaceWholeKbIds: string[];
}

type ReadBatch = (ids: string[], kbId: string, options: KnowledgeReadOptions) => Promise<unknown>;

export class UnavailableFilesError extends Error {
  constructor(readonly ids: string[]) {
    super('Some selected files are no longer available');
  }
}

/** Reauthorize the entire draft before committing. Never return a partial selection. */
export async function validateFileDraft(
  files: readonly DraftFile[],
  knowledgeBases: readonly BrowserKnowledgeBase[],
  supportedFileTypes: readonly string[],
  readBatch: ReadBatch,
  options: KnowledgeReadOptions,
): Promise<DraftFile[]> {
  const kbById = new Map(knowledgeBases.map(kb => [kb.id, kb]));
  const groups = new Map<string, DraftFile[]>();
  for (const file of files) {
    const key = file.kbId || '';
    const group = groups.get(key);
    if (group) group.push(file);
    else groups.set(key, [file]);
  }

  const verified = new Map<string, DraftFile>();
  const unavailable: string[] = [];
  for (const [kbId, group] of groups) {
    for (let offset = 0; offset < group.length; offset += KNOWLEDGE_BATCH_SIZE) {
      const ids = group.slice(offset, offset + KNOWLEDGE_BATCH_SIZE).map(file => file.id);
      let response: unknown;
      try {
        options.signal?.throwIfAborted();
        response = await readBatch(ids, kbId, options);
        options.signal?.throwIfAborted();
      } catch (error) {
        const kind = browserErrorKind(error);
        if (options.signal?.aborted || kind === 'error') throw error;
        unavailable.push(...ids);
        continue;
      }
      const result = response as { success?: boolean; data?: Array<{ id?: string; knowledge_base_id?: string }> } | undefined;
      if (result?.success !== true || !Array.isArray(result.data)) throw new Error('Invalid file batch');
      const byId = new Map(result.data.filter(row => typeof row?.id === 'string').map(row => [row.id, row]));
      for (const id of ids) {
        const row = byId.get(id);
        const kb = row?.knowledge_base_id ? kbById.get(row.knowledge_base_id) : undefined;
        if (!kb || (kbId && kb.id !== kbId)) {
          unavailable.push(id);
          continue;
        }
        const file = adaptBrowserFile(row, kb);
        if (fileDisabledReason(file, supportedFileTypes)) {
          unavailable.push(id);
          continue;
        }
        verified.set(id, { id, name: file.name, kbId: file.kbId, folderPath: file.folderPath });
      }
    }
  }
  if (unavailable.length) throw new UnavailableFilesError(unavailable);
  return files.map(file => verified.get(file.id)!);
}
