// Pure helpers backing the "All" scope of the knowledge-base list.
//
// The list template renders every card with `:key="kb.id"`. Vue requires
// `v-for` keys to be unique within a list — duplicate keys corrupt the
// virtual-DOM patch and the list renders empty or partial. The same
// knowledge base can legitimately show up more than once in the source
// data:
//
//   1. A KB the caller owns can also be shared back to them (e.g. they
//      belong to an org the KB was shared into), so it appears in both the
//      owned list and `sharedKnowledgeBases`.
//   2. The very same KB can be shared into the caller's view through more
//      than one organization. Each share is a distinct row (its own
//      `share_id`) but carries the identical `knowledge_base.id`.
//
// Either case yields two entries with the same `kb.id` once there are ≥2
// knowledge bases, which is the #795 symptom: a single KB renders fine
// (no collision) while two or more blank the page. De-duplicating by KB
// id here keeps the keys unique. Owned rows win over shared ones, and
// among multiple shares of the same KB we keep the most-privileged
// permission so the card stays as capable as the caller's best grant.

export interface OwnedKnowledgeBase {
  id: string;
  is_pinned?: boolean;
  pinned_at?: string;
  created_at?: string;
  creator_id?: string;
  [key: string]: unknown;
}

export interface SharedKnowledgeBaseLike {
  knowledge_base: {
    id: string;
    knowledge_count?: number;
    chunk_count?: number;
    [key: string]: unknown;
  } | null;
  permission: string;
  shared_at: string;
  share_id: string;
  org_name?: string;
  [key: string]: unknown;
}

export type MergedOwnedKnowledgeBase = OwnedKnowledgeBase & { isMine: true };
export type MergedSharedKnowledgeBase = Record<string, unknown> & {
  id: string;
  isMine: false;
  permission: string;
  shared_at: string;
  share_id: string;
  org_name?: string;
};
export type MergedKnowledgeBase =
  | MergedOwnedKnowledgeBase
  | MergedSharedKnowledgeBase;

// Permissions that grant write access. Mirrors EDITABLE_PERMS in
// KnowledgeBaseList.vue — kept local so this module stays free of any
// component/store imports and remains trivially unit-testable.
const EDITABLE_PERMS = new Set(['admin', 'editor']);

export function isSharedKbEditable(perm: string | undefined): boolean {
  return !!perm && EDITABLE_PERMS.has(perm);
}

// Higher rank = more privileged. Unknown permissions rank lowest so they
// never shadow a real grant when collapsing duplicate shares.
const PERMISSION_RANK: Record<string, number> = { admin: 3, editor: 2, viewer: 1 };

function permissionRank(perm: string | undefined): number {
  return (perm && PERMISSION_RANK[perm]) || 0;
}

function isMyKb(kb: { creator_id?: string }, currentUserId: string | undefined): boolean {
  return !!(kb.creator_id && currentUserId && kb.creator_id === currentUserId);
}

function pinnedTime(kb: OwnedKnowledgeBase): number {
  return kb.pinned_at ? Date.parse(kb.pinned_at) : 0;
}

/**
 * Build the de-duplicated, ordered list rendered by the "All" scope.
 *
 * Ordering (unchanged from the previous inline logic):
 *   1. pinned KBs (any creator the caller pinned), newest pin first
 *   2. the caller's own non-pinned KBs
 *   3. teammate non-pinned KBs (own tenant, someone else created)
 *   4. shared KBs, editable grants before view-only
 *
 * On top of that, every entry is unique by knowledge-base id: owned rows
 * win over shared duplicates, and repeated shares of one KB collapse to
 * the most-privileged permission.
 */
export function mergeAllScopeKnowledgeBases(
  owned: OwnedKnowledgeBase[],
  shared: SharedKnowledgeBaseLike[],
  currentUserId: string | undefined,
): MergedKnowledgeBase[] {
  const result: MergedKnowledgeBase[] = [];

  const pinned: OwnedKnowledgeBase[] = [];
  const ownMine: OwnedKnowledgeBase[] = [];
  const teammateMine: OwnedKnowledgeBase[] = [];
  const ownedIds = new Set<string>();
  for (const kb of owned) {
    ownedIds.add(kb.id);
    if (kb.is_pinned) pinned.push(kb);
    else if (isMyKb(kb, currentUserId)) ownMine.push(kb);
    else teammateMine.push(kb);
  }
  pinned.sort((a, b) => pinnedTime(b) - pinnedTime(a));

  for (const kb of pinned) result.push({ ...kb, isMine: true as const });
  for (const kb of ownMine) result.push({ ...kb, isMine: true as const });
  for (const kb of teammateMine) result.push({ ...kb, isMine: true as const });

  // Collapse the shared rows by KB id, keeping the most-privileged grant,
  // and drop any KB the caller already owns. This is what guarantees a
  // unique `:key` per rendered card.
  const dedupedShared = new Map<string, SharedKnowledgeBaseLike>();
  for (const entry of shared) {
    const kb = entry?.knowledge_base;
    if (!kb) continue;
    if (ownedIds.has(kb.id)) continue;
    const existing = dedupedShared.get(kb.id);
    if (!existing || permissionRank(entry.permission) > permissionRank(existing.permission)) {
      dedupedShared.set(kb.id, entry);
    }
  }

  const sortedShared = [...dedupedShared.values()].sort((a, b) => {
    const aE = isSharedKbEditable(a.permission) ? 0 : 1;
    const bE = isSharedKbEditable(b.permission) ? 0 : 1;
    return aE - bE;
  });

  for (const shared of sortedShared) {
    const kb = shared.knowledge_base!;
    result.push({
      ...kb,
      isMine: false as const,
      permission: shared.permission,
      shared_at: shared.shared_at,
      share_id: shared.share_id,
      org_name: shared.org_name,
      knowledge_count: kb.knowledge_count,
      chunk_count: kb.chunk_count,
    } as MergedSharedKnowledgeBase);
  }

  return result;
}
