interface KnowledgeDeletionRow {
  id: string;
  parse_status?: string;
}

interface KnowledgeDeletionResponse {
  success?: boolean;
  data?: KnowledgeDeletionRow[] | null;
}

type DeletionResult = 'completed' | 'pending' | 'failed' | 'cancelled';

/** Query exact IDs, including deleting rows, instead of the filtered document list. */
export async function waitForKnowledgeDeletion(
  ids: string[],
  fetchRows: (ids: string[]) => Promise<KnowledgeDeletionResponse>,
  options: {
    isActive?: () => boolean;
    attempts?: number;
    delay?: () => Promise<void>;
  } = {},
): Promise<DeletionResult> {
  const isActive = options.isActive ?? (() => true);
  const delay = options.delay ?? (() => new Promise<void>(resolve => setTimeout(resolve, 1000)));
  const requested = new Set(ids);
  const deleting = new Set<string>();
  const attempts = options.attempts ?? 30;
  for (let i = 0; i < attempts; i++) {
    if (!isActive()) return 'cancelled';
    const rows: KnowledgeDeletionRow[] = [];
    // A supported 200-document delete exceeds common proxy request-line
    // limits if all UUIDs are put into a single GET query string.
    for (let start = 0; start < ids.length; start += 50) {
      if (!isActive()) return 'cancelled';
      const response = await fetchRows(ids.slice(start, start + 50));
      if (!isActive()) return 'cancelled';
      // A missing/invalid payload is not evidence of deletion. The Go batch
      // endpoint can encode an empty slice as either [] or null.
      if (response.success !== true || (response.data !== null && !Array.isArray(response.data))) {
        throw new Error('Invalid knowledge deletion status response');
      }
      rows.push(...(response.data ?? []));
    }
    const remaining = rows.filter(row => requested.has(row.id));
    if (remaining.length === 0) return 'completed';
    for (const row of remaining) {
      if (row.parse_status === 'deleting') deleting.add(row.id);
      // A document may already have a failed parse/delete before this request
      // starts. Only a failure after observing deletion belongs to this run.
      if (deleting.has(row.id) && row.parse_status === 'failed') return 'failed';
    }
    if (i + 1 < attempts) await delay();
  }
  return isActive() ? 'pending' : 'cancelled';
}
