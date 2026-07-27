import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { knowledgeFolderApi } from "@/api/knowledge-base/folders";
import type {
  FolderBreadcrumbItem,
  KnowledgeFolder,
} from "@/types/knowledgeFolder";
import {
  buildFolderIndex,
  type FolderIndex,
} from "@/views/knowledge/folderModel";
import {
  createQueryGeneration,
  shouldCommitQueryResult,
} from "@/views/knowledge/queryContext";

// Per-KB tree preference persistence. Tree visibility and expanded-node set are
// stored under a stable localStorage key scoped to each knowledge base so that
// collapsing/expanding the tree on one KB does not leak into another.
const STORAGE_PREFIX = "weknora:knowledge-folder-tree:";
const DEFAULT_ROOT_LABEL = "根目录";

interface FolderTreePrefs {
  visible: boolean;
  expandedIds: string[];
}

function defaultPrefs(): FolderTreePrefs {
  return { visible: false, expandedIds: [] };
}

// Read persisted prefs with defensive JSON parsing: any invalid shape, syntax
// error, or storage failure falls back to defaults rather than crashing the
// tree panel. The tree starts collapsed.
function readPrefs(kbId: string): FolderTreePrefs {
  if (typeof window === "undefined") return defaultPrefs();
  try {
    const raw = window.localStorage.getItem(STORAGE_PREFIX + kbId);
    if (!raw) return defaultPrefs();
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object") return defaultPrefs();
    const obj = parsed as Record<string, unknown>;
    return {
      visible: typeof obj.visible === "boolean" ? obj.visible : false,
      expandedIds: Array.isArray(obj.expandedIds)
        ? obj.expandedIds.filter((id): id is string => typeof id === "string")
        : [],
    };
  } catch {
    return defaultPrefs();
  }
}

function writePrefs(kbId: string, prefs: FolderTreePrefs): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(STORAGE_PREFIX + kbId, JSON.stringify(prefs));
  } catch {
    // Ignore quota / serialization failures; tree prefs are non-critical UX.
  }
}

// Reduce the varied request-util reject shape ({ status, message, ...data }) to
// a non-empty string the page can show as an error detail. Never returns "" so
// that `currentError != null` reliably signals an error state to v-if checks.
function extractErrorMessage(err: unknown): string {
  if (err && typeof err === "object") {
    const e = err as { message?: unknown; status?: unknown };
    if (typeof e.message === "string" && e.message) return e.message;
    if (typeof e.status === "number") return `HTTP ${e.status}`;
  }
  if (typeof err === "string" && err) return err;
  return "error";
}

// Extract the HTTP status code from the request-util reject shape, so the page
// can distinguish a 404/403 (invalid/unauthorized folder_id -> replace to root)
// from a transient network/5xx error (show retry). Returns null when the error
// has no numeric status (e.g. network failure, timeout).
function extractErrorStatus(err: unknown): number | null {
  if (err && typeof err === "object") {
    const e = err as { status?: unknown };
    if (typeof e.status === "number") return e.status;
  }
  return null;
}

// HTTP statuses that indicate the folder_id in the URL is invalid or
// unauthorized for the current user. These trigger a router.replace to
// root (preserving q) instead of a retry state.
function isFolderNotFoundStatus(status: number | null): boolean {
  return status === 404 || status === 403;
}

export default function useKnowledgeFolders(rootLabel?: string) {
  const { t, te } = useI18n();

  // The root node is a local virtual breadcrumb item, never returned by the
  // breadcrumb API. Its label is i18n-driven: an explicit parameter wins,
  // otherwise the knowledgeBase.rootFolder key is used when present (added by
  // the locale task), with a final fallback for safety.
  const resolveRootLabel = (): string => {
    if (rootLabel) return rootLabel;
    if (te("knowledgeBase.rootFolder")) return t("knowledgeBase.rootFolder");
    return DEFAULT_ROOT_LABEL;
  };

  // Raw direct children from knowledgeFolderApi.list. These may lack
  // knowledge_count; the public directFolders computed joins counts from the
  // tree index below.
  const rawDirectFolders = ref<KnowledgeFolder[]>([]);
  const tree = ref<KnowledgeFolder[]>([]);
  const breadcrumb = ref<FolderBreadcrumbItem[]>([]);

  const directLoading = ref(false);
  const treeLoading = ref(false);
  const operationLoading = ref(false);

  const currentError = ref<string | null>(null);
  const currentErrorStatus = ref<number | null>(null);
  const treeError = ref<string | null>(null);
  const treeErrorStatus = ref<number | null>(null);

  const treeVisible = ref(false);
  const expandedFolderIds = ref<Set<string>>(new Set());

  // treeLoaded gates the count-join (only the tree is an authoritative recursive
  // count source). treeKbId guards against serving a previous KB's index after
  // a KB switch that bypassed resetForKnowledgeBase.
  const treeLoaded = ref(false);
  let treeKbId: string | null = null;
  let currentKbId: string | null = null;

  // Two independent generation guards: direct/breadcrumb share a folder-level
  // context (they move together on navigation); the tree is KB-scoped and
  // loaded independently. Each async commit checks shouldCommitQueryResult
  // before writing, so a stale response from an older navigation/KB is dropped
  // and can never overwrite a newer one.
  const directGeneration = createQueryGeneration();
  const treeGeneration = createQueryGeneration();

  const index = computed<FolderIndex>(() => buildFolderIndex(tree.value));

  // Count-joined view models. Only tree-returned nodes are authoritative
  // recursive-count sources. When the tree is loaded, fill knowledge_count from
  // the tree's byId; when the count is genuinely unknown (direct-only, no tree),
  // expose null (NOT 0) so the UI renders a skeleton/--.
  const directFolders = computed<KnowledgeFolder[]>(() => {
    if (!treeLoaded.value) {
      return rawDirectFolders.value.map((folder) => ({
        ...folder,
        knowledge_count: null,
      }));
    }
    const byId = index.value.byId;
    return rawDirectFolders.value.map((folder) => {
      const fromTree = byId.get(folder.id);
      return {
        ...folder,
        knowledge_count: fromTree ? (fromTree.knowledge_count ?? null) : null,
      };
    });
  });

  function persistPrefs(): void {
    if (!currentKbId) return;
    writePrefs(currentKbId, {
      visible: treeVisible.value,
      expandedIds: [...expandedFolderIds.value],
    });
  }

  function buildBreadcrumbItems(
    folderId: string,
    apiBreadcrumb: KnowledgeFolder[] | null,
  ): FolderBreadcrumbItem[] {
    const root: FolderBreadcrumbItem = {
      id: "",
      name: resolveRootLabel(),
      isRoot: true,
    };
    if (!folderId) return [root];
    const trail: FolderBreadcrumbItem[] = apiBreadcrumb
      ? apiBreadcrumb.map((folder) => ({
          id: folder.id,
          name: folder.name,
          isRoot: false,
        }))
      : [];
    // The breadcrumb API returns the path root-exclusive and current-inclusive;
    // prepend the local root node so the UI never has to call the API for root.
    return [root, ...trail];
  }

  function loadCurrent(kbId: string, folderId: string): Promise<void> {
    currentKbId = kbId;
    const ctx = directGeneration.next({
      kbId,
      folderId,
      searchTerm: "",
      filtersSignature: "",
    });

    directLoading.value = true;
    currentError.value = null;
    currentErrorStatus.value = null;

    // Root is expressed to the folders-list API as an empty parent_id: the
    // backend treats "" as root (DB schema `parent_id DEFAULT ''`, handler doc
    // "empty parent_id lists root folders"). Non-root uses the real folder
    // UUID. The __root__ sentinel is ONLY for the documents-list endpoint
    // (serializeFolderForBrowse in KnowledgeBase.vue) and URL parsing - not here.
    const directPromise: Promise<void> = knowledgeFolderApi
      .list(kbId, folderId)
      .then((folders: KnowledgeFolder[]) => {
        if (!shouldCommitQueryResult(directGeneration.current(), ctx)) return;
        rawDirectFolders.value = folders;
      })
      .catch((err: unknown) => {
        if (!shouldCommitQueryResult(directGeneration.current(), ctx)) return;
        // Keep last stable directFolders and expose the error. Never fall back
        // to unfiltered whole-KB document APIs.
        currentError.value = extractErrorMessage(err);
        currentErrorStatus.value = extractErrorStatus(err);
      });

    // Root breadcrumb is a local virtual node, never fetched from the API.
    const breadcrumbPromise: Promise<void> =
      folderId === ""
        ? Promise.resolve().then(() => {
            if (shouldCommitQueryResult(directGeneration.current(), ctx)) {
              breadcrumb.value = buildBreadcrumbItems("", null);
            }
          })
        : knowledgeFolderApi
            .breadcrumb(kbId, folderId)
            .then((trail: KnowledgeFolder[]) => {
              if (shouldCommitQueryResult(directGeneration.current(), ctx)) {
                breadcrumb.value = buildBreadcrumbItems(folderId, trail);
              }
            })
            .catch((err: unknown) => {
              if (shouldCommitQueryResult(directGeneration.current(), ctx)) {
                currentError.value = extractErrorMessage(err);
                currentErrorStatus.value = extractErrorStatus(err);
              }
            });

    return Promise.all([directPromise, breadcrumbPromise])
      .then(() => undefined)
      .finally(() => {
        if (shouldCommitQueryResult(directGeneration.current(), ctx)) {
          directLoading.value = false;
        }
      });
  }

  function loadTree(kbId: string, force: boolean): Promise<void> {
    if (!force && treeLoaded.value && treeKbId === kbId) {
      return Promise.resolve();
    }
    const ctx = treeGeneration.next({
      kbId,
      folderId: "",
      searchTerm: "",
      filtersSignature: "",
    });
    treeLoading.value = true;
    treeError.value = null;
    treeErrorStatus.value = null;
    return knowledgeFolderApi
      .tree(kbId)
      .then((treeData: KnowledgeFolder[]) => {
        if (!shouldCommitQueryResult(treeGeneration.current(), ctx)) return;
        tree.value = treeData;
        treeLoaded.value = true;
        treeKbId = kbId;
      })
      .catch((err: unknown) => {
        if (!shouldCommitQueryResult(treeGeneration.current(), ctx)) return;
        // Keep last stable tree and expose the error. Never fall back to
        // unfiltered whole-KB document APIs.
        treeError.value = extractErrorMessage(err);
        treeErrorStatus.value = extractErrorStatus(err);
      })
      .finally(() => {
        if (shouldCommitQueryResult(treeGeneration.current(), ctx)) {
          treeLoading.value = false;
        }
      });
  }

  // Lazy tree load with caching. Safe to call repeatedly; only the first call
  // for a given KB triggers a request unless force-refreshed via refreshAll.
  function ensureTree(kbId: string): Promise<void> {
    return loadTree(kbId, false);
  }

  // Reload direct + tree + breadcrumb. The tree is force-refreshed because
  // callers use this after mutations that may have changed counts/structure.
  function refreshAll(kbId: string, folderId: string): Promise<void> {
    return Promise.all([
      loadCurrent(kbId, folderId),
      loadTree(kbId, true),
    ]).then(() => undefined);
  }

  function setTreeVisible(visible: boolean): void {
    treeVisible.value = visible;
    persistPrefs();
  }

  function toggleExpanded(id: string): void {
    if (expandedFolderIds.value.has(id)) {
      expandedFolderIds.value.delete(id);
    } else {
      expandedFolderIds.value.add(id);
    }
    persistPrefs();
  }

  function resetForKnowledgeBase(kbId: string): void {
    currentKbId = kbId;
    // Invalidate any in-flight requests from the previous KB so their commits
    // are dropped by the generation guard.
    directGeneration.next({
      kbId,
      folderId: "",
      searchTerm: "",
      filtersSignature: "",
    });
    treeGeneration.next({
      kbId,
      folderId: "",
      searchTerm: "",
      filtersSignature: "",
    });

    rawDirectFolders.value = [];
    tree.value = [];
    breadcrumb.value = [];
    treeLoaded.value = false;
    treeKbId = null;
    currentError.value = null;
    currentErrorStatus.value = null;
    treeError.value = null;
    treeErrorStatus.value = null;
    directLoading.value = false;
    treeLoading.value = false;
    operationLoading.value = false;

    const prefs = readPrefs(kbId);
    treeVisible.value = prefs.visible;
    expandedFolderIds.value = new Set(prefs.expandedIds);
  }

  return {
    directFolders,
    tree,
    index,
    breadcrumb,
    directLoading,
    treeLoading,
    operationLoading,
    currentError,
    currentErrorStatus,
    treeError,
    treeErrorStatus,
    isFolderNotFoundStatus,
    treeVisible,
    expandedFolderIds,
    loadCurrent,
    ensureTree,
    refreshAll,
    setTreeVisible,
    toggleExpanded,
    resetForKnowledgeBase,
  };
}
