<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, reactive, computed, nextTick } from "vue";
import { MessagePlugin } from "tdesign-vue-next";
import DocContent from "@/components/doc-content.vue";
import useKnowledgeBase from '@/hooks/useKnowledgeBase';
import { useRoute, useRouter } from 'vue-router';
import EmptyKnowledge from '@/components/empty-knowledge.vue';
import ContextualGuide from '@/components/ContextualGuide.vue';
import KBInfoPopover from '@/components/KBInfoPopover.vue';
import KBSwitcherDropdown from '@/components/KBSwitcherDropdown.vue';
import { getSessionsList, createSessions, generateSessionsTitle } from "@/api/chat/index";
import { useMenuStore } from '@/stores/menu';
import { useUIStore } from '@/stores/ui';
import { useOrganizationStore } from '@/stores/organization';
import { useAuthStore } from '@/stores/auth';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useEditorResourcesStore } from '@/stores/editorResources';
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue';
const usemenuStore = useMenuStore();
const uiStore = useUIStore();
const orgStore = useOrganizationStore();
const authStore = useAuthStore();
const chatResources = useChatResourcesStore();
const editorResources = useEditorResourcesStore();
const router = useRouter();
import {
  batchQueryKnowledge,
  listKnowledgeTags,
  updateKnowledgeTagBatch,
  uploadKnowledgeFile,
  createKnowledgeFromURL,
  reparseKnowledge,
  cancelKnowledgeParse,
  getKnowledgeSpans,
  getKnowledgeDetails,
} from "@/api/knowledge-base/index";
import { knowledgeSpansPayloadHasTrace } from '@/utils/knowledgeTrace';
import FAQEntryManager from './components/FAQEntryManager.vue';
import DocumentListView from './components/DocumentListView.vue';
import DocumentCardView from './components/DocumentCardView.vue';
import FileSystemBatchBar from './components/FileSystemBatchBar.vue';
import KbUploadSourceDropdown from './components/KbUploadSourceDropdown.vue';
import TagEditDialog from './components/TagEditDialog.vue';
import KbTagManageDrawer from './components/KbTagManageDrawer.vue';
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess';
import { knowledgeFolderApi } from '@/api/knowledge-base/folders';
import { buildUploadDirectoryManifest } from './utils/uploadDirectoryManifest';
import { useUploadConfirmStore, type UploadConfirmResult } from '@/stores/uploadConfirm';
import WikiBrowser from './wiki/WikiBrowser.vue';
import { getWikiStats } from '@/api/wiki';
import {
  isKnowledgeParseInFlight,
  knowledgeNeedsStatusPolling,
  shouldRefreshWikiStatusAfterKnowledgePoll,
} from './wikiStatusRefresh';
import { listMoveTargets, moveKnowledge, getKnowledgeMoveProgress } from '@/api/knowledge-base';
import { useI18n } from 'vue-i18n';
import { useMarqueeSelect } from '@/hooks/useMarqueeSelect';
import type { ParserEngineInfo } from '@/api/system';
import useKnowledgeFolders from '@/hooks/useKnowledgeFolders';
import useFolderEditing from './useFolderEditing';
import useFolderOperations, { evaluateReparseLimit } from './useFolderOperations';
import FolderPickerDialog from './components/FolderPickerDialog.vue';
import FolderBreadcrumb from './components/FolderBreadcrumb.vue';
import FolderNavigationPanel from './components/FolderNavigationPanel.vue';
import FolderGridItems from './components/FolderGridItems.vue';
import FolderListRows from './components/FolderListRows.vue';
import type { FolderActionType } from './components/FolderTree.vue';
import type { FileSystemSelection, KnowledgeFolder } from '@/types/knowledgeFolder';
import { serializeFolderForBrowse } from '@/types/knowledgeFolder';
import {
  createQueryGeneration,
  formatKnowledgeFolderRouteQuery,
  parseKnowledgeFolderRouteQuery,
  shouldCommitQueryResult,
  stableFiltersSignature,
} from './queryContext';
import { buildRenderedSelectionKeys, descendantIds, folderPathItems, searchFolders, sortDirectFolders, selectionCount } from './folderModel';
const route = useRoute();
const { t } = useI18n();
const kbId = computed(() => (route.params as any).kbId as string || '');
const kbInfo = ref<any>(null);
const uploadSourceRef = ref<InstanceType<typeof KbUploadSourceDropdown> | null>(null);
const uploading = ref(false);
const kbLoading = ref(false);
const docListLoading = ref(true);
const isFAQ = computed(() => (kbInfo.value?.type || '') === 'faq');
const isWiki = computed(() => !!kbInfo.value?.indexing_strategy?.wiki_enabled);
const validTabs = ['documents', 'wiki', 'graph'] as const
type KbTab = typeof validTabs[number]
const initTab = validTabs.includes(route.query.tab as any) ? (route.query.tab as KbTab) : 'documents'
const activeKbTab = ref<KbTab>(initTab);

// Wiki 状态用于面包屑上的索引中指示。父组件自行拉取，避免依赖 WikiBrowser 挂载状态
// （用户切到"文档" tab 时 WikiBrowser 会卸载，这里仍需持续反映后台索引进度）。
const wikiStatus = ref<{ pendingTasks: number; isActive: boolean; pendingIssues: number }>({
  pendingTasks: 0,
  isActive: false,
  pendingIssues: 0,
})
const wikiIsIndexing = computed(() => wikiStatus.value.isActive || wikiStatus.value.pendingTasks > 0)
const wikiIndexingTip = computed(() => {
  if (!wikiIsIndexing.value) return ''
  return t('knowledgeEditor.wikiBrowser.queueStatus', { count: wikiStatus.value.pendingTasks || 0 })
})
const onWikiStatusChange = (payload: { pendingTasks: number; isActive: boolean; pendingIssues: number }) => {
  wikiStatus.value = payload
}
const onViewWikiInGraph = async (slug: string) => {
  // Write tab+slug first so the activeKbTab watcher's later replace
  // (which spreads route.query) preserves slug instead of clobbering it.
  await router.replace({ query: { ...route.query, tab: 'graph', slug } })
  activeKbTab.value = 'graph'
}

let wikiStatusTimer: ReturnType<typeof setInterval> | null = null
let wikiStatusProbeTimers: Array<ReturnType<typeof setTimeout>> = []
const stopWikiStatusPolling = () => {
  if (wikiStatusTimer) {
    clearInterval(wikiStatusTimer)
    wikiStatusTimer = null
  }
}
const clearWikiStatusProbes = () => {
  wikiStatusProbeTimers.forEach(t => clearTimeout(t))
  wikiStatusProbeTimers = []
}
const fetchWikiStatusOnce = async () => {
  if (!kbId.value || !isWiki.value) return
  try {
    const res: any = await getWikiStats(kbId.value)
    const data = res?.data || res
    if (!data) return
    wikiStatus.value = {
      pendingTasks: data.pending_tasks || 0,
      isActive: !!data.is_active,
      pendingIssues: data.pending_issues || 0,
    }
    // 活跃时轮询，空闲时停掉定时器，避免无谓请求
    if (wikiIsIndexing.value) {
      if (!wikiStatusTimer) {
        wikiStatusTimer = setInterval(fetchWikiStatusOnce, 5000)
      }
    } else {
      stopWikiStatusPolling()
    }
  } catch (_) { /* ignore */ }
}
// 用户刚触发了一个上传 / reparse / URL 导入之类的动作后，后台通常要过
// 一小段时间才会把 wiki 任务真正塞进队列；如果这时空闲轮询刚好停了，
// 面包屑的"索引中"会延迟很久才亮起。所以这里安排几次退避重试，
// 主动把面包屑的 loading 尽快点亮，一旦探测到任务就会走正常的 5s 轮询。
const scheduleWikiStatusProbes = () => {
  if (!kbId.value || !isWiki.value) return
  clearWikiStatusProbes()
  const delays = [500, 2000, 5000, 10000]
  delays.forEach(delay => {
    const timer = setTimeout(() => { fetchWikiStatusOnce() }, delay)
    wikiStatusProbeTimers.push(timer)
  })
}
watch([kbId, isWiki], ([newKbId, newIsWiki]) => {
  stopWikiStatusPolling()
  clearWikiStatusProbes()
  wikiStatus.value = { pendingTasks: 0, isActive: false, pendingIssues: 0 }
  if (newKbId && newIsWiki) {
    fetchWikiStatusOnce()
  }
}, { immediate: true })
onUnmounted(() => {
  stopWikiStatusPolling()
  clearWikiStatusProbes()
})
const missingStorageEngine = computed(() => {
  if (!kbInfo.value || isFAQ.value) return false
  // storage_backend_id is authoritative; storage_provider_config.provider is a
  // compatibility projection for older clients. Either being present means the
  // KB has a bound storage instance and uploads should not be blocked.
  if (kbInfo.value.storage_backend_id) return false
  const spc = kbInfo.value.storage_provider_config
  return !spc || !spc.provider
})
const parserEngines = computed<ParserEngineInfo[]>(() => editorResources.parserEngines);

const supportedFileTypes = computed<Set<string>>(() => {
  const engines = parserEngines.value
  if (!engines.length) return new Set<string>()

  const rules: { file_types: string[]; engine: string }[] =
    kbInfo.value?.chunking_config?.parser_engine_rules || []

  const ruleMap = new Map<string, string>()
  for (const r of rules) {
    for (const ft of r.file_types) ruleMap.set(ft, r.engine)
  }

  const available = new Set<string>()
  const availableEngineNames = new Set(
    engines.filter(e => e.Available !== false).map(e => e.Name)
  )

  for (const engine of engines) {
    for (const ft of engine.FileTypes || []) {
      if (available.has(ft)) continue

      const explicitEngine = ruleMap.get(ft)
      if (explicitEngine) {
        if (availableEngineNames.has(explicitEngine)) available.add(ft)
      } else {
        if (engine.Available !== false) available.add(ft)
      }
    }
  }
  return available
})

const acceptFileTypes = computed(() =>
  [...supportedFileTypes.value].map(t => '.' + t).join(',')
)

const unsupportedFileTypes = computed<string[]>(() => {
  const engines = parserEngines.value
  if (!engines.length) return []

  const allTypes = new Set<string>()
  for (const engine of engines) {
    for (const ft of engine.FileTypes || []) allTypes.add(ft)
  }

  const supported = supportedFileTypes.value
  return [...allTypes].filter(ft => !supported.has(ft)).sort()
})

const goToParserSettings = () => {
  if (kbId.value) {
    uiStore.openKBSettings(kbId.value, 'parser')
  }
}

// Permission control: check if current user owns this KB or has edit/manage permission
//
// "Owner" here is "the original creator of this KB" (PR 5 introduced
// CreatorID). The previous version compared kb.tenant_id to the active
// tenant id, which only answers "is this KB inside our tenant" — that
// is true even for a Viewer in someone else's tenant, so the gate
// silently bypassed every role check below. Now we require an explicit
// creator match, and the role-aware fallbacks below decide whether a
// non-creator may edit / manage.
const isOwner = computed(() => {
  if (!kbInfo.value) return false;
  const creatorId = (kbInfo.value as any).creator_id || '';
  const userId = authStore.user?.id || '';
  // creator_id may be empty for legacy KBs created before PR 5; treat
  // those as tenant-owned so the role gate applies (Admin+ can manage,
  // Viewer cannot).
  if (!creatorId) return false;
  return creatorId === userId;
});

// Current KB's shared record (when accessed via organization share)
const currentSharedKb = computed(() =>
  orgStore.sharedKnowledgeBases.find((s) => s.knowledge_base?.id === kbId.value) ?? null,
);

// Accessed via organization share: when the KB shows up in our
// sharedKnowledgeBases list it means we reached it through a shared space,
// not because we own/manage it in our tenant. In that case the user's local
// tenant role does NOT grant edit/manage — only the share grant does.
// Without this guard a local tenant Admin would see edit/upload entries on
// a read-only shared KB and get 403'd by the backend on click.
//
// Note: tenant_id comparison alone is unreliable — a user can be a member of
// both the source and receiving tenants, and currentTenantId reflects the
// active switcher rather than "how this KB became visible to me". Presence
// in the share list is the authoritative signal.
const isViaShare = computed(() => !!currentSharedKb.value);

// Can edit: when accessed via an organization share, ONLY the share grant
// counts — even if the current user happens to be the original creator of
// the KB. The backend's RBAC middleware authorizes based on the active
// tenant, not on creator_id, so a creator viewing their own KB from a
// different tenant context will be 403'd on write. Otherwise: KB creator
// (any role) or tenant Admin+ in the home tenant.
//
// hasRole('contributor') is intentionally NOT here — being a Contributor
// in a tenant does not by itself grant edit on someone else's KB.
const canEdit = computed(() => {
  if (isViaShare.value) return orgStore.canEditKB(kbId.value, false);
  if (isOwner.value) return true;
  if (authStore.hasRole('admin')) return true;
  return orgStore.canEditKB(kbId.value, false);
});

// Can manage (delete, settings, etc.): same isViaShare-first rule. For
// shared KBs only an 'admin' share grant qualifies — editor/viewer (and
// even being the creator viewed via share) never grant delete/settings.
const canManage = computed(() => {
  if (isViaShare.value) return orgStore.canManageKB(kbId.value, false);
  if (isOwner.value) return true;
  if (authStore.hasRole('admin')) return true;
  return orgStore.canManageKB(kbId.value, false);
});

// Can mutate knowledge (move / batch-delete): the backend gate for these
// two endpoints is g.Contributor(), so the caller MUST be Contributor+
// in their tenant on top of having KB edit permission. Without the extra
// role check, an org-share-editor whose tenant role is Viewer would see
// the "Move" / "Batch manage" entries and 403 on click. For shared KBs
// the local tenant role is irrelevant — canEdit already encodes the share
// grant, so trust it.
const canMutateKnowledge = computed(() => {
  if (!canEdit.value) return false;
  if (isViaShare.value) return true;
  if (isOwner.value) return true;
  if (authStore.hasRole('admin')) return true;
  return authStore.hasRole('contributor');
});

// Effective permission: from direct org share list or from GET /knowledge-bases/:id (e.g. agent-visible KB)
const effectiveKBPermission = computed(() => orgStore.getKBPermission(kbId.value) || kbInfo.value?.my_permission || '');

const knowledgeList = ref<Array<{ id: string; name: string; type?: string }>>([]);
let { cardList, total, moreIndex, details, getKnowled, delKnowledge, openMore, onVisibleChange: _onVisibleChange, getCardDetails, getfDetails } = useKnowledgeBase(kbId.value)

// --- Folder navigation + inline editing ---
// The folder composable owns direct-folders / tree / breadcrumb data and the
// refresh helpers. The editing composable owns the page-level inline edit
// state machine ({ mode, folderId, value, error } | null). Full folder
// rendering integration (FolderGridItems / FolderListRows / nav shell slots)
// is wired across the integration; this adds the state machine, entry-point
// handlers, query-transition cancel, and the Viewer gate.
const folders = useKnowledgeFolders();
const folderEditing = useFolderEditing();
const folderOperations = useFolderOperations();
// Destructure the refs/computeds the template needs as top-level bindings so
// Vue auto-unwraps them. `folders` / `folderOperations` are plain objects
// returned from composables; nested refs on a plain object do NOT auto-unwrap
// in the template, so binding folders.tree directly would pass the Ref object.
const {
  tree: folderTree,
  index: folderIndex,
  treeLoading: folderTreeLoading,
  directFolders: folderDirectFolders,
  treeVisible: folderTreeVisible,
  expandedFolderIds: folderExpandedIds,
  breadcrumb: folderBreadcrumb,
  directLoading: folderDirectLoading,
  currentError: folderCurrentError,
  currentErrorStatus: folderCurrentErrorStatus,
  treeError: folderTreeError,
  isFolderNotFoundStatus,
  setTreeVisible: setFolderTreeVisible,
  toggleExpanded: toggleFolderExpanded,
} = folders;
const { moving: folderMoving } = folderOperations;
// Folder-editing computed views (template auto-unwraps top-level bindings).
const {
  renamingFolderId: folderRenamingId,
  renameError: folderRenameError,
  creatingParentId: folderCreatingParentId,
  creatingSurface: folderCreatingSurface,
  createError: folderCreateError,
} = folderEditing;

// Current folder / search derived from the route query (root is "" - the
// __root__ sentinel is an API-boundary concern only and never surfaces into
// UI state per queryContext). These drive folder navigation and the
// query-transition edit-cancel watcher below.
const routeFolderState = computed(() => parseKnowledgeFolderRouteQuery(route.query));
const currentFolderId = computed(() => routeFolderState.value.folderId);
const currentSearchTerm = computed(() => routeFolderState.value.searchTerm);

// Viewer gate: only users who can edit the KB may enter folder edit mode.
// The folder components also hide their action menus / checkboxes when
// `editable` is false, so the entry points are not reachable for Viewers.
const folderEditable = computed(() => canEdit.value);

// --- Query coordinator ---
// URL `q` is the single source of truth for the search term; `currentSearchTerm`
// (derived from route `q`) drives both the document request `keyword` and the
// folder name search. Search mode is active iff `q` is non-empty. In search
// mode the document request OMITS `folder_id` (whole-KB); in browse mode it
// sends `__root__` or the real folder UUID (serialization stays at this layer
// via serializeFolderForBrowse; `__root__` never reaches UI/URL/logs).
const isSearchMode = computed(() => currentSearchTerm.value.trim().length > 0);
const currentFolderTargetLabel = computed(() => {
  if (!currentFolderId.value) return t('knowledgeBase.rootFolder');
  return folderIndex.value.byId.get(currentFolderId.value)?.name || t('knowledgeBase.rootFolder');
});

// Folders rendered in the content flow (before documents, same grid/list).
// Browse mode: direct children of the current folder, name-sorted.
// Search mode: whole-tree name matches from the tree index — tag/
// type/status/source/date filters do NOT narrow folder results, only documents.
const displayedFolders = computed<KnowledgeFolder[]>(() => {
  if (isSearchMode.value) {
    return searchFolders(folderIndex.value, currentSearchTerm.value).map((r) => r.folder);
  }
  return sortDirectFolders(folderDirectFolders.value);
});

// True when there is anything to render in the content flow (folders OR docs).
// Used to gate the document view vs. the empty state.
const hasContent = computed(
  () => displayedFolders.value.length > 0 || cardList.value.length > 0,
);

// Folder selection (checkbox-driven; selection only comes from the checkbox,
// never from body click). Kept separate from document `selectedIds`; cleared on
// every query transition. The unified typed selection below bridges the two
// Sets with the marquee hook and the FileSystemBatchBar.
const selectedFolderIds = ref<Set<string>>(new Set());

// Typed Shift anchor: stores the last checkbox-selected item as a typed key
// (`folder:<id>` / `knowledge:<id>`) so Shift+click can compute the range from
// the rendered order (buildRenderedSelectionKeys) regardless of whether the
// anchor is a folder or a document. Replaces the old cardList-index anchor.
let lastSelectedKey: string | null = null;

// Unified FileSystemSelection consumed by FileSystemBatchBar and the
// useFolderOperations handlers. Built fresh on every access so it always
// reflects the latest selectedFolderIds + selectedIds.
const unifiedSelection = computed<FileSystemSelection>(() => ({
  folderIds: new Set(selectedFolderIds.value),
  knowledgeIds: new Set(selectedIds.value),
}));

// Writable bridge between the typed-key marquee path and the two Set sources
// of truth. Reads merge folder:<id> + knowledge:<id> keys; writes split them
// back into selectedFolderIds / selectedIds. The marquee hook assigns to
// .value, which routes through the setter.
const selectedKeys = computed<Set<string>>({
  get: () => {
    const keys = new Set<string>();
    for (const id of selectedFolderIds.value) keys.add(`folder:${id}`);
    for (const id of selectedIds.value) keys.add(`knowledge:${id}`);
    return keys;
  },
  set: (next: Set<string>) => {
    const folders = new Set<string>();
    const docs = new Set<string>();
    for (const key of next) {
      if (key.startsWith('folder:')) folders.add(key.slice(7));
      else if (key.startsWith('knowledge:')) docs.add(key.slice(10));
    }
    selectedFolderIds.value = folders;
    selectedIds.value = docs;
  },
});

// Apply a typed-key range [s, e] from the rendered order to the selection
// Sets. `add` true adds the range (Shift+check on unselected), false removes
// (Shift+uncheck on selected). Mutates both Sets in place - Vue 3.5 tracks
// Set#add/delete natively.
function applyRenderedKeyRange(keys: string[], s: number, e: number, add: boolean) {
  for (let i = s; i <= e; i++) {
    const k = keys[i];
    if (!k) continue;
    if (k.startsWith('folder:')) {
      const id = k.slice(7);
      if (add) selectedFolderIds.value.add(id);
      else selectedFolderIds.value.delete(id);
    } else if (k.startsWith('knowledge:')) {
      const id = k.slice(10);
      if (add) selectedIds.value.add(id);
      else selectedIds.value.delete(id);
    }
  }
}

const onFolderToggleSelection = (folderId: string, checked: boolean, shiftKey?: boolean) => {
  if (!folderEditable.value) return;
  const key = `folder:${folderId}`;
  if (shiftKey && lastSelectedKey && lastSelectedKey !== key) {
    const renderedKeys = buildRenderedSelectionKeys(displayedFolders.value, cardList.value);
    const anchorIdx = renderedKeys.indexOf(lastSelectedKey);
    const currentIdx = renderedKeys.indexOf(key);
    if (anchorIdx >= 0 && currentIdx >= 0) {
      const [s, e] = currentIdx < anchorIdx ? [currentIdx, anchorIdx] : [anchorIdx, currentIdx];
      applyRenderedKeyRange(renderedKeys, s, e, checked);
    }
  } else {
    const next = new Set(selectedFolderIds.value);
    if (checked) next.add(folderId);
    else next.delete(folderId);
    selectedFolderIds.value = next;
  }
  lastSelectedKey = key;
};

// Monotonic query generation: bumped on every folder/q/filter transition so a
// stale async result from a previous context is dropped. The document list is
// guarded by useKnowledgeBase's own generation counter (same intent); the
// folder composable guards direct/tree/breadcrumb. This page-level generation
// coordinates same-KB transitions and is checked via shouldCommitQueryResult
// before any page-direct async commit.
const queryGeneration = createQueryGeneration();
function nextQueryGeneration() {
  return queryGeneration.next({
    kbId: kbId.value,
    folderId: currentFolderId.value,
    searchTerm: currentSearchTerm.value,
    filtersSignature: stableFiltersSignature(filterParams.value),
  });
}

// cancelTransientInteraction: on any folder/q/filter change,
// close menus, cancel inline editing, close non-submitting dialogs, clear
// selection (docs + folders), clear the Shift anchor, and bump the generation.
// Viewer never enters selecting/marquee/editing/mutation-dialog flows, so the
// selection clears are no-ops for them but still safe to run.
function cancelTransientInteraction() {
  moveMenuMode.value = 'normal';
  folderEditing.cancelEdit();
  if (!folderMoving.value) moveFlow.value = null;
  clearSelection();
  selectedFolderIds.value = new Set();
  lastSelectedKey = null; // typed Shift anchor (replaces lastSelectedIndex)
  nextQueryGeneration();
}

// Entry-point handlers. These are bound to the folder components' emits when
// rendered. Each handler is a no-op for Viewers (folderEditable gate) so the
// editing state can never be entered read-only.
const handleFolderCreate = (parentId: string, surface: "tree" | "content" = "content") => {
  if (!folderEditable.value) return;
  // Tree surface: expand the parent so the inline input row (rendered under the
  // parent) is visible. Root ('') is always expanded implicitly, so skip it.
  // toggleFolderExpanded persists the expansion to prefs; only call when the
  // parent is collapsed so we never accidentally collapse it.
  if (surface === "tree" && parentId !== "" && !folderExpandedIds.value.has(parentId)) {
    toggleFolderExpanded(parentId);
  }
  folderEditing.startCreate(parentId, surface);
};
// Card/row "new subfolder" action: navigate INTO the target folder first so
// the content-area create input appears in the folder it will actually be
// created under (fixes trigger/input位置脱节). The route watcher's
// cancelTransientInteraction fires on navigation; we MUST await router.replace
// (vue-router updates route reactively, async) AND then nextTick so the pre-
// flush watcher's cancelEdit runs to completion BEFORE startCreate sets the
// new editState - otherwise the watcher fires after startCreate and clears it
// (create input never shows). executeFolderDelete uses the same await pattern.
const handleCardCreateSubfolder = async (folderId: string) => {
  if (!folderEditable.value) return;
  if (currentFolderId.value !== folderId) {
    await router.replace({ query: formatKnowledgeFolderRouteQuery(folderId, currentSearchTerm.value) });
    await nextTick();
  }
  handleFolderCreate(folderId, 'content');
};
const handleFolderRename = (folderId: string) => {
  if (!folderEditable.value) return;
  folderEditing.startRename(folderId);
};
const handleFolderRenameCommit = (folderId: string, name: string) => {
  if (!folderEditable.value || !kbId.value) return;
  void folderEditing.commitRename(kbId.value, folderId, name, () =>
    folders.refreshAll(kbId.value, currentFolderId.value),
  );
};
const handleFolderRenameCancel = (_folderId: string) => {
  folderEditing.cancelEdit();
};
// Single-folder delete: the FolderActionMenu popconfirm is the only confirmation
// gate (cascading delete is announced in its content), so we skip the second
// dialog and go straight to the batch-delete submit. ensureTree is awaited so
// descendantIds (used to decide whether the current folder is inside the
// deletion set) is authoritative; after first load it is a lazy no-op.
const handleFolderDelete = async (folderId: string) => {
  if (!folderEditable.value || !kbId.value) return;
  await folders.ensureTree(kbId.value);
  await executeFolderDelete(folderId);
};

// Find the closest ancestor of `folderId` that is NOT in the deletion set, via
// folderPathItems (the breadcrumb chain). Returns '' (root) when no ancestor
// survives - e.g. deleting a top-level folder while viewing it or one of its
// descendants. Navigate to the closest surviving parent before refresh.
function closestSurvivingParent(folderId: string, deletedIds: Set<string>): string {
  const path = folderPathItems(folderIndex.value, folderId);
  // folderPathItems is root-first and includes folderId itself; walk from the
  // nearest ancestor (len-2) up to the root, returning the first survivor.
  for (let i = path.length - 2; i >= 0; i--) {
    if (!deletedIds.has(path[i].id)) return path[i].id;
  }
  return ''; // root
}

// Single-folder recursive delete. The FolderActionMenu popconfirm is the only
// confirmation gate, so we go straight to the batch-delete submit. If the
// deleted folder IS the current folder (or an ancestor of it), the page
// navigates to the closest surviving parent BEFORE refreshing so the URL never
// points at a soon-to-be-gone folder. Submitted, not done: the backend runs the
// recursive delete async; onDone invalidates the local caches and we toast
// "submitted".
async function executeFolderDelete(deletedId: string) {
  if (!kbId.value) return;
  const selection: FileSystemSelection = {
    knowledgeIds: new Set<string>(),
    folderIds: new Set([deletedId]),
  };
  // The deletion set is the target folder + its whole subtree. If the current
  // folder is inside that set, the URL must move to the closest surviving
  // ancestor before refresh so a stale folder is never rendered.
  const deletedIds = descendantIds(folderIndex.value, deletedId);
  const needNavigate = deletedIds.has(currentFolderId.value);
  const targetParent = needNavigate
    ? closestSurvivingParent(currentFolderId.value, deletedIds)
    : currentFolderId.value;
  try {
    await folderOperations.deleteFolders(kbId.value, selection, async () => {
      // onDone: navigate BEFORE refresh. router.replace triggers the
      // query-transition watcher's loadCurrent for the surviving folder; the
      // subsequent refreshAll force-reloads direct + tree. Generation guards in
      // useKnowledgeFolders ensure a stale async result from the old folder
      // never overwrites the new navigation (old generation results don't
      // mutate the current list).
      if (needNavigate) {
        await router.replace({
          query: formatKnowledgeFolderRouteQuery(targetParent, currentSearchTerm.value),
        });
      }
      await folders.refreshAll(kbId.value, targetParent);
    });
    // Submitted, not done: the backend runs the recursive delete async. The
    // toast says "submitted"; onDone already invalidated the folder caches.
    MessagePlugin.success(t('knowledgeBase.folderDeleteSubmitted'));
    // 后端将批量删除放入异步队列，立刻拉列表仍可能包含待删文件夹；短轮询直到
    // 被删子树从 folder index / direct folders 消失或超时。与 confirmBatchDelete
    // 对齐——文档列表每轮一并刷新，确保子树下的文档也同步离开视图。
    resetPage();
    {
      const maxPolls = 30;
      const delayMs = 400;
      for (let i = 0; i < maxPolls; i++) {
        await loadKnowledgeFiles(kbId.value);
        await folders.refreshAll(kbId.value, currentFolderId.value);
        const stillInTree = [...deletedIds].some(
          (id) => folderIndex.value.byId.has(id) || folderDirectFolders.value.some((f) => f.id === id),
        );
        if (!stillInTree) break;
        await new Promise<void>((r) => setTimeout(r, delayMs));
      }
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.folderDeleteFailed'));
  }
}
// Create-commit helper: called by the create input UI on Enter.
const handleFolderCreateCommit = (name: string) => {
  if (!folderEditable.value || !kbId.value) return;
  void folderEditing.commitCreate(kbId.value, name, () =>
    folders.refreshAll(kbId.value, currentFolderId.value),
  );
};

// --- Folder navigation / open ---
// Body click on a folder (grid card, list row, breadcrumb item, tree node)
// navigates into it and clears `q`: formatKnowledgeFolderRouteQuery omits `q`
// when searchTerm is empty, so the URL becomes just `folder_id=<id>`. The route
// watcher then reloads that folder's direct children + documents (browse mode).
// In search mode this is the "click folder result -> navigate + clear q" path.
const handleFolderOpen = (folderId: string) => {
  const merged: Record<string, string> = {};
  for (const [k, v] of Object.entries(route.query)) {
    if (k === 'folder_id' || k === 'q') continue;
    if (typeof v === 'string') merged[k] = v;
  }
  Object.assign(merged, formatKnowledgeFolderRouteQuery(folderId, ''));
  router.replace({ query: merged });
};

// Tree panel action dispatch (FolderNavigationPanel -> FolderTree). The tree's
// context menu is limited to rename / delete / add-subfolder (FolderActionType).
// These reuse the existing page handlers; 'add-subfolder' with an empty id
// creates at root.
function handleFolderTreeAction(action: FolderActionType, folderId: string) {
  if (action === 'add-subfolder') {
    handleFolderCreate(folderId, 'tree');
  } else if (action === 'rename') {
    handleFolderRename(folderId);
  } else if (action === 'delete') {
    handleFolderDelete(folderId);
  }
}

// Inline folder create input draft (page-owned; the folder card/row components
// only own the rename input). Seeded empty when create mode starts.
const folderCreateDraft = ref('');
const folderCreateInputEl = ref<HTMLInputElement | null>(null);
const isCreatingFolder = computed(
  () =>
    folderEditing.isEditing.value &&
    folderEditing.editState.value?.mode === 'create' &&
    folderCreatingSurface.value === 'content',
);
const setFolderCreateInput = (el: any) => {
  folderCreateInputEl.value = (el as HTMLInputElement | null) || null;
};
watch(isCreatingFolder, (active) => {
  if (active) {
    folderCreateDraft.value = '';
    nextTick(() => {
      folderCreateInputEl.value?.focus();
    });
  }
});
const commitFolderCreate = (name: string) => {
  if (!folderEditing.isEditing.value || folderEditing.editState.value?.mode !== 'create') return;
  const trimmed = name.trim();
  if (!trimmed) {
    folderEditing.cancelEdit();
    return;
  }
  handleFolderCreateCommit(trimmed);
};
const cancelFolderCreate = () => {
  if (!folderEditing.isEditing.value || folderEditing.editState.value?.mode !== 'create') return;
  folderEditing.cancelEdit();
};

// --- Move-to-folder flow ---
// Two DISTINCT move operations share this page (do not conflate):
//   - 移动到文件夹 (same-KB, files AND folders) -> FolderPickerDialog + batchMove
//   - 转移到其他知识库 (cross-KB, docs only)    -> existing async transfer flow
// The active flow state records the item key being moved + the flow kind, so
// the page knows which dialog to render and how to derive the selection. The
// cross-KB transfer keeps its own inline card-menu sub-flow (moveMenuMode) and
// is NOT represented here - it stays document-only.
interface MoveFlowState {
  kind: 'move-folder' | 'transfer-kb';
  // Full typed selection being moved. Single-item entry points convert their
  // `folder:<id>` / `knowledge:<id>` key into a one-element selection via
  // selectionFromItemKey before storing it here, so the batch-bar move action
  // (which carries a multi-id selection) and the per-card/per-folder menu
  // action share the same confirm path.
  selection: FileSystemSelection;
  // Current parent of the moved items, used to disable "move to same parent"
  // in the picker (isMoveTargetDisabled). For a browse-context document/folder
  // this is currentFolderId; a future folder-action entry point can pass the
  // folder's real parent_id.
  currentParentId: string;
}
const moveFlow = ref<MoveFlowState | null>(null);

const folderPickerVisible = computed(() => moveFlow.value?.kind === 'move-folder');
const moveFolderSelectedFolderIds = computed<Set<string>>(() => {
  if (!moveFlow.value || moveFlow.value.kind !== 'move-folder') return new Set();
  return moveFlow.value.selection.folderIds;
});
const moveFolderCurrentParentId = computed(() => moveFlow.value?.currentParentId ?? '');

// Parse 'knowledge:<id>' / 'folder:<id>' back into a FileSystemSelection. The
// three cases (file-only / folder-only / mixed) all flow through the same
// batchMove call - here a single itemKey only ever produces one populated set,
// but a future batch entry point can build a multi-id selection the same way.
function selectionFromItemKey(itemKey: string): FileSystemSelection {
  const sepIdx = itemKey.indexOf(':');
  const kind = sepIdx >= 0 ? itemKey.slice(0, sepIdx) : '';
  const id = sepIdx >= 0 ? itemKey.slice(sepIdx + 1) : '';
  if (kind === 'folder') {
    return { knowledgeIds: new Set(), folderIds: new Set([id]) };
  }
  return { knowledgeIds: new Set([id]), folderIds: new Set() };
}

// Single-item entry point: per-card / per-folder action menu emits a typed
// itemKey. Converts to a one-element selection so the confirm path is shared
// with the batch-bar move action.
function openMoveFolderFlow(itemKey: string) {
  openMoveFolderFlowFromSelection(selectionFromItemKey(itemKey));
}

// Batch-bar entry point: carries the full unified selection (folders + docs).
function openMoveFolderFlowFromSelection(selection: FileSystemSelection) {
  if (!canMutateKnowledge.value) return;
  if (selection.folderIds.size === 0 && selection.knowledgeIds.size === 0) return;
  // Ensure the tree is cached so the picker has targets to show. ensureTree is
  // a lazy no-op after the first load for a KB, so this is cheap.
  if (kbId.value) void folders.ensureTree(kbId.value);
  moveFlow.value = {
    kind: 'move-folder',
    selection,
    currentParentId: currentFolderId.value,
  };
}

function handleFolderPickerVisibleChange(val: boolean) {
  // Clear flow state when the dialog closes from user action (cancel / overlay
  // / esc / post-confirm). A move in flight keeps the dialog alive (the dialog
  // itself blocks close while submitting), so this only fires once idle.
  if (!val && !folderOperations.moving.value) {
    moveFlow.value = null;
  }
}

async function handleFolderPickerConfirm(targetFolderId: string) {
  const flow = moveFlow.value;
  if (!flow || flow.kind !== 'move-folder' || !kbId.value) return;
  const selection = flow.selection;
  try {
    await folderOperations.moveWithinKnowledgeBase(
      kbId.value,
      selection,
      targetFolderId,
      () => folders.refreshAll(kbId.value, currentFolderId.value),
    );
    MessagePlugin.success(t('knowledgeBase.folderMoveSuccess'));
    moveFlow.value = null; // closes the picker
    // Refresh the document list so moved docs leave the current view.
    resetPage();
    loadKnowledgeFiles(kbId.value);
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.folderMoveFailed'));
  }
}

const showKbDetailContextualGuide = computed(() => {
  return Boolean(kbId.value)
    && !isFAQ.value
    && canEdit.value
    && !docListLoading.value
    && cardList.value.length === 0;
});

const onVisibleChange = (visible: boolean) => {
  _onVisibleChange(visible);
  if (!visible) {
    moveMenuMode.value = 'normal';
  }
};

/** Per-knowledge cache: whether /spans has a real trace (see knowledgeSpansPayloadHasTrace). */
const traceAvailableById = reactive<Record<string, boolean>>({});
const traceProbeInflight = new Set<string>();

function clearTraceAvailabilityCache() {
  for (const key of Object.keys(traceAvailableById)) {
    delete traceAvailableById[key];
  }
  traceProbeInflight.clear();
}

// Parse phases where the backend pipeline is still actively running
// (primary parse OR post-process fan-out). Trace data exists and the
// UI should treat the row as "in flight" rather than terminal.
function isParseInFlight(status?: string): boolean {
  return isKnowledgeParseInFlight(status);
}

function isTraceMenuVisible(item: KnowledgeCard): boolean {
  if (!item?.id) return false;
  if (isParseInFlight(item.parse_status)) {
    return true;
  }
  return traceAvailableById[item.id] === true;
}

async function probeTraceAvailable(item: KnowledgeCard) {
  const id = item.id;
  if (!id || traceProbeInflight.has(id)) return;
  if (isParseInFlight(item.parse_status)) {
    traceAvailableById[id] = true;
    return;
  }
  if (Object.prototype.hasOwnProperty.call(traceAvailableById, id)) return;
  traceProbeInflight.add(id);
  try {
    const res: any = await getKnowledgeSpans(id);
    traceAvailableById[id] = !!(res?.success && knowledgeSpansPayloadHasTrace(res.data));
  } catch {
    traceAvailableById[id] = false;
  } finally {
    traceProbeInflight.delete(id);
  }
}

const onCardMoreVisibleChange = (visible: boolean, item: KnowledgeCard) => {
  onVisibleChange(visible);
  if (visible) {
    probeTraceAvailable(item);
  }
};
let isCardDetails = ref(false);
let timeout: ReturnType<typeof setTimeout> | null = null;
let knowledgeScroll = ref()
let page = 1;
let pageSize = 35;
let scrollLoading = false;
const resetPage = () => { page = 1; scrollLoading = false; };

// Move state — inline in card menu
const moveMenuMode = ref<'normal' | 'targets' | 'confirm'>('normal');
const moveKnowledgeId = ref('');
const moveTargetKbs = ref<any[]>([]);
const moveTargetsLoading = ref(false);
const moveSelectedTargetId = ref('');
const moveSelectedTargetName = ref('');
const moveMode = ref<'reuse_vectors' | 'reparse'>('reuse_vectors');
const moveSubmitting = ref(false);
let movePollTimer: ReturnType<typeof setInterval> | null = null;

// View mode (grid / list) — persisted per browser
type DocViewMode = 'grid' | 'list';
const VIEW_MODE_KEY = 'weknora.kb.docs.viewMode';
const initViewMode = (): DocViewMode => {
  try {
    return localStorage.getItem(VIEW_MODE_KEY) === 'list' ? 'list' : 'grid';
  } catch { return 'grid'; }
};
const viewMode = ref<DocViewMode>(initViewMode());
watch(viewMode, (v) => {
  try { localStorage.setItem(VIEW_MODE_KEY, v); } catch { /* ignore */ }
});

// Multi-select state — shared between grid and list views.
// Vue 3.5 tracks Set#add/delete natively, so direct mutation is reactive.
const selectedIds = ref<Set<string>>(new Set());
// lastSelectedKey is declared above (with selectedFolderIds) - typed anchor
// shared by document and folder Shift+click range selection.
const batchDeleting = ref(false);
const batchReparsing = ref(false);
// IDs submitted for async batch reparse; hold optimistic pending until the worker updates DB.
const pendingReparseAck = ref<Set<string>>(new Set());

const applyOptimisticBatchReparse = (ids: string[]) => {
  const idSet = new Set(ids);
  for (const card of cardList.value) {
    if (!idSet.has(card.id)) continue;
    pendingReparseAck.value.add(card.id);
    card.parse_status = 'pending';
    card.summary_status = undefined;
    card.description = '';
    delete traceAvailableById[card.id];
    traceAvailableById[card.id] = true;
  }
};

const syncReparseAckFromServer = (ids: string[]) => {
  for (const id of ids) {
    if (!pendingReparseAck.value.has(id)) continue;
    const card = cardList.value.find((c) => c.id === id);
    if (card && isParseInFlight(card.parse_status)) {
      pendingReparseAck.value.delete(id);
    }
  }
};

const awaitBatchReparseReflection = async (ids: string[]) => {
  const maxPolls = 30;
  const delayMs = 400;
  for (let i = 0; i < maxPolls && pendingReparseAck.value.size > 0; i++) {
    await loadKnowledgeFiles(kbId.value);
    syncReparseAckFromServer(ids);
    applyOptimisticBatchReparse(Array.from(pendingReparseAck.value));
    await new Promise<void>((r) => setTimeout(r, delayMs));
  }
  pendingReparseAck.value.clear();
};

// Pre-disable the reparse action when the known (filtered) document count
// exceeds the backend per-request cap (REPARSE_LIMIT). Uses
// evaluateReparseLimit from useFolderOperations. Reparse is documents-only -
// folder selections disable the button in FileSystemBatchBar instead.
const reparseOverLimit = computed(() => {
  // Filter in-flight docs from the count first - the backend rejects them and
  // the page skips them, so they should not contribute to the pre-disable.
  const filteredSelection: FileSystemSelection = {
    folderIds: new Set(),
    knowledgeIds: new Set(
      [...selectedIds.value].filter((id) => {
        const item = cardList.value.find((c) => c.id === id);
        return !item || !isParseInFlight(item.parse_status);
      }),
    ),
  };
  return evaluateReparseLimit(filteredSelection).overLimit;
});

const confirmBatchReparse = async () => {
  if (batchReparsing.value || batchDeleting.value || selectionCount(unifiedSelection.value) === 0) return;
  if (reparseOverLimit.value) {
    // Backend 200-limit guard: pre-disable should have blocked the click, but
    // defend in depth in case the bar was triggered another way.
    MessagePlugin.warning(t('knowledgeBase.folderReparseLimit'));
    return;
  }
  const allIds = Array.from(selectedIds.value);
  const ids = allIds.filter((id) => {
    const item = cardList.value.find((c) => c.id === id);
    return !item || !isParseInFlight(item.parse_status);
  });
  const skipped = allIds.length - ids.length;
  if (ids.length === 0) {
    MessagePlugin.info(t('knowledgeBase.rebuildInProgress'));
    return;
  }
  if (skipped > 0) {
    MessagePlugin.warning(t('knowledgeBase.batchReparseSkippedInFlight', { count: skipped }));
  }
  batchReparsing.value = true;
  try {
    // Route through useFolderOperations.reparseSelection so the composable owns
    // the batch-reparse submit path (knowledge_ids, in-flight filtering,
    // backend 200-limit error surfacing). The page keeps the optimistic +
    // reflection polling UX. No task-completion guarantee: the toast says
    // "submitted". Reparse is documents-only; folder selections disable the
    // button in FileSystemBatchBar.
    const selection: FileSystemSelection = {
      knowledgeIds: new Set(ids),
      folderIds: new Set(),
    };
    const submittedIds = await folderOperations.reparseSelection(
      kbId.value,
      selection,
      () => {
        // onDone: reparse does not move documents across folders, so folder
        // caches need no refresh. The document list is refreshed via the
        // optimistic + reflection poll below.
        return Promise.resolve();
      },
      {
        isInFlight: (id: string) => {
          const item = cardList.value.find((c) => c.id === id);
          return !!item && isParseInFlight(item.parse_status);
        },
      },
    );
    MessagePlugin.success(t('knowledgeBase.batchReparseSuccess', { count: submittedIds.length }));
    applyOptimisticBatchReparse(submittedIds);
    clearSelection();
    batchMode.value = false;
    scheduleWikiStatusProbes();
    void awaitBatchReparseReflection(submittedIds);
  } catch (e: any) {
    // Includes backend 200-limit rejections (surfaced via reparseSelection's
    // rethrow) - the message from the server is shown as-is.
    MessagePlugin.error(e?.message || t('knowledgeBase.batchReparseFailed'));
  } finally {
    batchReparsing.value = false;
  }
};

const tagFilterPanelVisible = ref(false);
const tagFilterTriggerHover = ref(false);
const tagFilterCleared = ref(false);
const tagManageDrawerVisible = ref(false);

const showTagFilterClear = computed(
  () => selectedTagIds.value.length > 0 && tagFilterTriggerHover.value,
);

const isTagFilterPlaceholder = computed(
  () => selectedTagIds.value.length === 0 && tagFilterCleared.value,
);

const selectedTagIds = ref<string[]>([]);
const tagList = ref<any[]>([]);
const tagLoading = ref(false);
const tagSearchQuery = ref('');
const TAG_PAGE_SIZE = 50;
const tagPage = ref(1);
const tagHasMore = ref(false);
const tagLoadingMore = ref(false);
const tagTotal = ref(0);
let tagSearchDebounce: number | null = null;
let docSearchDebounce: number | null = null;
const docSearchKeyword = ref('');

// --- URL `q` coordination ---
// The URL `q` is the source of truth for the search term. The input writes to
// the URL with a 300ms debounce; Enter flushes the debounce immediately (no
// second request - the route watcher fires once); clearing the input removes
// `q`. The route watcher (below) reloads documents + folders on URL change, so
// the input never triggers a request directly. `docSearchKeyword` is a pure
// input mirror, synced from the URL so back/forward reflects in the field.
const SEARCH_DEBOUNCE_MS = 300;
function writeSearchToUrl() {
  const term = docSearchKeyword.value.trim();
  const merged: Record<string, string> = {};
  // Preserve unrelated query fragments (tab, slug, knowledge_id, ...); replace
  // folder_id/q via formatKnowledgeFolderRouteQuery so root stays implicit.
  for (const [k, v] of Object.entries(route.query)) {
    if (k === 'folder_id' || k === 'q') continue;
    if (typeof v === 'string') merged[k] = v;
  }
  Object.assign(merged, formatKnowledgeFolderRouteQuery(currentFolderId.value, term));
  router.replace({ query: merged });
}
function scheduleSearchDebounce() {
  // Skip a redundant write when the input already matches the URL (programmatic
  // sync from currentSearchTerm) - avoids a no-op replace on back/forward.
  if (docSearchKeyword.value.trim() === currentSearchTerm.value) return;
  if (docSearchDebounce !== null) window.clearTimeout(docSearchDebounce);
  docSearchDebounce = window.setTimeout(() => {
    docSearchDebounce = null;
    writeSearchToUrl();
  }, SEARCH_DEBOUNCE_MS);
}
function flushSearchDebounce() {
  // Enter: flush the pending debounce (if any) so the URL updates immediately.
  // The route watcher performs the single reload; Enter never fires a second
  // request on top of the debounce.
  if (docSearchDebounce !== null) {
    window.clearTimeout(docSearchDebounce);
    docSearchDebounce = null;
  }
  writeSearchToUrl();
}
function clearSearchInput() {
  // Clear button: empty the field and remove `q` from the URL immediately.
  // Clearing `q` returns to the URL `folder_id` folder.
  if (docSearchDebounce !== null) {
    window.clearTimeout(docSearchDebounce);
    docSearchDebounce = null;
  }
  if (currentSearchTerm.value === '') return; // already browse mode
  writeSearchToUrl();
}
const selectedFileType = ref('');
const fileTypeOptions = computed(() => [
  { label: t('knowledgeBase.allFileTypes'), value: '' },
  { label: 'PDF', value: 'pdf' },
  { label: 'DOCX', value: 'docx' },
  { label: 'DOC', value: 'doc' },
  { label: 'PPTX', value: 'pptx' },
  { label: 'PPT', value: 'ppt' },
  { label: 'EPUB', value: 'epub' },
  { label: 'MHTML', value: 'mhtml' },
  { label: 'TXT', value: 'txt' },
  { label: 'MD', value: 'md' },
  { label: 'URL', value: 'url' },
  { label: t('knowledgeBase.typeManual'), value: 'manual' },
  { label: 'MP3', value: 'mp3' },
  { label: 'WAV', value: 'wav' },
  { label: 'M4A', value: 'm4a' },
  { label: 'FLAC', value: 'flac' },
  { label: 'OGG', value: 'ogg' },
]);
const selectedParseStatus = ref('');
const parseStatusOptions = computed(() => [
  { label: t('knowledgeBase.allParseStatuses'), value: '' },
  { label: t('knowledgeBase.parseStatusPending'), value: 'pending' },
  { label: t('knowledgeBase.parseStatusProcessing'), value: 'processing' },
  { label: t('knowledgeBase.parseStatusCompleted'), value: 'completed' },
  { label: t('knowledgeBase.parseStatusFailed'), value: 'failed' },
  { label: t('knowledgeBase.parseStatusCancelled'), value: 'cancelled' },
  { label: t('knowledgeBase.parseStatusFinalizing'), value: 'finalizing' },
  { label: t('knowledgeBase.parseStatusDraft'), value: 'draft' },
]);
const selectedSource = ref('');
// Source filter combines ingestion channels and the "manual"/"url" virtual
// sources that the backend routes onto the `type` column.
const sourceOptions = computed(() => [
  { label: t('knowledgeBase.allSources'), value: '' },
  { label: t('knowledgeBase.sourceUpload'), value: 'web' },
  { label: t('knowledgeBase.sourceUrl'), value: 'url' },
  { label: t('knowledgeBase.sourceManual'), value: 'manual' },
  { label: t('knowledgeBase.sourceApi'), value: 'api' },
  { label: t('knowledgeBase.sourceBrowserExtension'), value: 'browser_extension' },
  { label: t('knowledgeBase.channelFeishu'), value: 'feishu' },
  { label: t('knowledgeBase.channelNotion'), value: 'notion' },
  { label: t('knowledgeBase.channelYuque'), value: 'yuque' },
  { label: t('knowledgeBase.channelWechat'), value: 'wechat' },
  { label: t('knowledgeBase.channelWecom'), value: 'wecom' },
  { label: t('knowledgeBase.channelDingtalk'), value: 'dingtalk' },
  { label: t('knowledgeBase.channelSlack'), value: 'slack' },
  { label: t('knowledgeBase.channelIm'), value: 'im' },
]);
// Date range as [start, end] in "YYYY-MM-DD" form (t-date-range-picker default).
const updatedTimeRange = ref<string[]>([]);
// Disable any date after today so users cannot filter into the future.
const disableFutureDate = { after: new Date(new Date().setHours(23, 59, 59, 999)) };
const filterParams = computed(() => {
  const [start, end] = updatedTimeRange.value || [];
  // keyword comes from the URL `q` (source of truth); in search mode the
  // document request OMITS folder_id (whole-KB search), in browse mode it sends
  // __root__ or the real folder UUID via serializeFolderForBrowse. The
  // __root__ sentinel is confined to this serialization and never reaches the
  // UI/URL.
  return {
    tag_ids: selectedTagIds.value.length > 0 ? selectedTagIds.value.join(',') : undefined,
    keyword: currentSearchTerm.value.trim() || undefined,
    folder_id: serializeFolderForBrowse(currentFolderId.value, isSearchMode.value),
    file_type: selectedFileType.value || undefined,
    parse_status: selectedParseStatus.value || undefined,
    source: selectedSource.value || undefined,
    start_time: start ? `${start} 00:00:00` : undefined,
    end_time: end ? `${end} 23:59:59` : undefined,
  };
});
const tagMap = computed<Record<string, any>>(() => {
  const map: Record<string, any> = {};
  tagList.value.forEach((tag) => {
    map[tag.id] = tag;
  });
  return map;
});
const sidebarCategoryCount = computed(() => tagTotal.value || tagList.value.length);
const sidebarTags = computed(() => {
  const list = tagList.value;
  const selectedIds = selectedTagIds.value;
  if (selectedIds.length === 0) {
    return list;
  }
  const missing = selectedIds
    .filter((id) => !list.some((tag) => tag.id === id))
    .map((id) => tagMap.value[id])
    .filter(Boolean);
  if (missing.length === 0) {
    return list;
  }
  return [...missing, ...list];
});

const activeTagFilterLabel = computed(() => {
  if (selectedTagIds.value.length === 0) {
    return tagFilterCleared.value
      ? t('knowledgeBase.tagFilterPlaceholder')
      : t('knowledgeBase.allTags');
  }
  if (selectedTagIds.value.length === 1) {
    const id = selectedTagIds.value[0];
    return tagMap.value[id]?.name || t('knowledgeBase.allTags');
  }
  return t('knowledgeBase.tagFilterMulti', { count: selectedTagIds.value.length });
});

const activeTagFilterTitle = computed(() => {
  if (selectedTagIds.value.length === 0) {
    return t('knowledgeBase.tagFilterTitle');
  }
  const names = selectedTagIds.value
    .map((id) => tagMap.value[id]?.name)
    .filter(Boolean);
  return names.length > 0 ? names.join('、') : t('knowledgeBase.tagFilterTitle');
});

const isTagFilterActive = (tagId: string) => selectedTagIds.value.includes(tagId);

// 标签编辑弹窗
const tagEditDialogVisible = ref(false);
const tagEditTarget = ref<KnowledgeCard | null>(null);

function openTagEditDialog(item: KnowledgeCard) {
  tagEditTarget.value = item;
  tagEditDialogVisible.value = true;
}

function onTagEditConfirm(tagIds: string[]) {
  if (tagEditTarget.value) {
    handleKnowledgeTagChange(tagEditTarget.value.id, tagIds);
  }
}
const getPageSize = () => {
  const viewportHeight = window.innerHeight || document.documentElement.clientHeight;
  const itemHeight = 148;
  let itemsInView = Math.floor(viewportHeight / itemHeight) * 5;
  pageSize = Math.max(35, itemsInView);
}
getPageSize()
// 直接调用 API 获取知识库文件列表
const getTagName = (tagId?: string | number) => {
  if (!tagId && tagId !== 0) return '';
  const key = String(tagId);
  return tagMap.value[key]?.name || '';
};

const loadKnowledgeFiles = (kbIdValue: string): Promise<void> => {
  if (!kbIdValue) return Promise.resolve();
  // Stamp this load with the current query generation. The finally block only
  // clears `docListLoading` if this load is still the current context, so a
  // stale load from a previous folder/q/filter cannot prematurely clear the
  // flag while a newer load is still in flight.
  const ctx = nextQueryGeneration();
  if (!isFAQ.value) {
    docListLoading.value = true;
  }
  return getKnowled(
    {
      page: 1,
      page_size: pageSize,
      ...filterParams.value,
    },
    kbIdValue,
  ).finally(() => {
    if (shouldCommitQueryResult(queryGeneration.current(), ctx) && isCurrentKb(kbIdValue) && !isFAQ.value) {
      docListLoading.value = false;
    }
  });
};

const isCurrentKb = (targetKbId: string) => targetKbId === kbId.value;

const loadTags = async (kbIdValue: string, reset = false) => {
  if (!kbIdValue) {
    tagList.value = [];
    tagTotal.value = 0;
    tagHasMore.value = false;
    tagPage.value = 1;
    return;
  }

  if (reset) {
    tagPage.value = 1;
    tagList.value = [];
    tagTotal.value = 0;
    tagHasMore.value = false;
  } else if (tagLoading.value || tagLoadingMore.value) {
    return;
  }

  const currentPage = tagPage.value || 1;
  tagLoading.value = currentPage === 1;
  tagLoadingMore.value = currentPage > 1;

  try {
    const res: any = await listKnowledgeTags(kbIdValue, {
      page: currentPage,
      page_size: TAG_PAGE_SIZE,
      keyword: tagSearchQuery.value || undefined,
    });
    if (!isCurrentKb(kbIdValue)) return;

    const pageData = (res?.data || {}) as {
      data?: any[];
      total?: number;
    };
    const pageTags = (pageData.data || []).map((tag: any) => ({
      ...tag,
      id: String(tag.id),
    }));

    if (currentPage === 1) {
      tagList.value = pageTags;
    } else {
      tagList.value = [...tagList.value, ...pageTags];
    }

    tagTotal.value = pageData.total || tagList.value.length;
    tagHasMore.value = tagList.value.length < tagTotal.value;
    if (tagHasMore.value) {
      tagPage.value = currentPage + 1;
    }
  } catch (error) {
    if (!isCurrentKb(kbIdValue)) return;
    console.error('Failed to load tags', error);
  } finally {
    if (isCurrentKb(kbIdValue)) {
      tagLoading.value = false;
      tagLoadingMore.value = false;
    }
  }
};

const handleTagFilterChange = (tagIds: string[]) => {
  selectedTagIds.value = tagIds;
  // 同步更新 store 中的 selectedTagIds，供 menu.vue 上传时使用
  uiStore.clearSelectedTagIds();
  tagIds.forEach(id => uiStore.toggleSelectedTagId(id));
  resetPage();
};

const handleTagRowClick = (tagId: string) => {
  const next = new Set(selectedTagIds.value);
  if (next.has(tagId)) {
    next.delete(tagId);
  } else {
    next.add(tagId);
  }
  if (next.size > 0) {
    tagFilterCleared.value = false;
  }
  handleTagFilterChange([...next]);
};

const clearTagFilter = () => {
  tagFilterCleared.value = true;
  handleTagFilterChange([]);
};

const openTagManageDrawer = () => {
  tagFilterPanelVisible.value = false;
  tagManageDrawerVisible.value = true;
};

const openTagManageFromEditDialog = () => {
  tagEditDialogVisible.value = false;
  tagManageDrawerVisible.value = true;
};

const onTagManageChanged = (payload?: { deletedTagId?: string }) => {
  if (!kbId.value) return;
  void loadTags(kbId.value, true);
  if (payload?.deletedTagId && selectedTagIds.value.includes(payload.deletedTagId)) {
    selectedTagIds.value = [];
    handleTagFilterChange([]);
    resetPage();
    loadKnowledgeFiles(kbId.value);
    return;
  }
  if (payload?.deletedTagId) {
    void (async () => {
      await new Promise((resolve) => setTimeout(resolve, 800));
      if (!kbId.value) return;
      resetPage();
      await loadKnowledgeFiles(kbId.value);
      await loadTags(kbId.value, true);
    })();
    return;
  }
  resetPage();
  loadKnowledgeFiles(kbId.value);
};

const handleKnowledgeTagChange = async (knowledgeId: string, tagIds: string[]) => {
  try {
    await updateKnowledgeTagBatch({ updates: { [knowledgeId]: tagIds } });
    MessagePlugin.success(t('knowledgeBase.tagUpdateSuccess'));
    resetPage(); // Reset page counter to 1 when reloading files after tag change
    loadKnowledgeFiles(kbId.value);
    loadTags(kbId.value, true);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  }
};

const loadKnowledgeBaseInfo = async (targetKbId: string, force = false) => {
  if (!targetKbId) {
    kbInfo.value = null;
    cardList.value = [];
    total.value = 0;
    return;
  }
  kbLoading.value = true;
  try {
    const data = await chatResources.fetchKnowledgeBaseById(targetKbId, force);
    if (!isCurrentKb(targetKbId)) return;

    kbInfo.value = data;
    selectedTagIds.value = [];
    tagFilterCleared.value = false;
    uiStore.clearSelectedTagIds();
    // 重置store中的标签选择状态，避免上传文档时自动带上之前选择的标签
    uiStore.clearSelectedTagIds();
    if (!isFAQ.value) {
      loadKnowledgeFiles(targetKbId);
    } else {
      cardList.value = [];
      total.value = 0;
    }
    loadTags(targetKbId, true);
  } catch (error) {
    if (!isCurrentKb(targetKbId)) return;

    console.error('Failed to load knowledge base info:', error);
    kbInfo.value = null;
    cardList.value = [];
    total.value = 0;
  } finally {
    if (isCurrentKb(targetKbId)) {
      kbLoading.value = false;
    }
  }
};

const loadKnowledgeList = async () => {
  try {
    await chatResources.ensureKnowledgeBases();
    const myKbs = chatResources.rawKnowledgeBases.map((item: any) => ({
      id: String(item.id),
      name: item.name,
      type: item.type || 'document',
    }));

    // Also include shared knowledge bases from orgStore
    const sharedKbs = (orgStore.sharedKnowledgeBases || [])
      .filter(s => s.knowledge_base != null)
      .map(s => ({
        id: String(s.knowledge_base.id),
        name: s.knowledge_base.name,
        type: s.knowledge_base.type || 'document',
      }));

    // Merge and deduplicate by id (my KBs take precedence)
    const myKbIds = new Set(myKbs.map(kb => kb.id));
    const uniqueSharedKbs = sharedKbs.filter(kb => !myKbIds.has(kb.id));

    knowledgeList.value = [...myKbs, ...uniqueSharedKbs];
  } catch (error) {
    console.error('Failed to load knowledge list:', error);
  }
};

// 监听路由参数变化，重新获取知识库内容
// Sync activeKbTab to URL query so it survives page refresh
watch(activeKbTab, (tab) => {
  const query = { ...route.query }
  if (tab === 'documents') {
    delete query.tab
  } else {
    query.tab = tab
  }
  router.replace({ query })
})

// Clear all document + folder selection and reset the typed Shift anchor.
// Defined before the kbId watcher below: that watcher is `immediate`, so its
// first synchronous run needs clearSelection already initialized (const TDZ).
const clearSelection = () => {
  selectedIds.value.clear();
  selectedFolderIds.value = new Set();
  lastSelectedKey = null;
};

watch(() => kbId.value, (newKbId, oldKbId) => {
  if (!newKbId) {
    kbInfo.value = null;
    cardList.value = [];
    total.value = 0;
    return;
  }
  if (newKbId === oldKbId && kbInfo.value) return;

  if (newKbId !== oldKbId) {
    clearTraceAvailabilityCache();
    cardList.value = [];
    total.value = 0;
    docListLoading.value = true;
    resetPage();
    tagSearchQuery.value = '';
    tagPage.value = 1;
    uiStore.clearSelectedTagIds();
    // Reset folder composable state for the new KB (clears direct folders /
    // tree / breadcrumb, invalidates in-flight requests, restores tree prefs).
    // Also cancel any inline folder edit in progress - a KB switch is a query
    // transition (cancels edit state).
    folders.resetForKnowledgeBase(newKbId);
    folderEditing.cancelEdit();
    // Clear same-KB transient state so a stale folder selection / move flow from
    // the previous KB cannot survive the switch.
    selectedFolderIds.value = new Set();
    moveFlow.value = null;
    moveMenuMode.value = 'normal';
    clearSelection();
  }
  loadKnowledgeBaseInfo(newKbId);
  // Load the current folder's direct children + ensure the tree is cached so
  // the folder editing flow has data to refresh against after create/rename.
  // The URL stays on the current folder; loadCurrent does not change the URL.
  void folders.loadCurrent(newKbId, currentFolderId.value);
  void folders.ensureTree(newKbId);
}, { immediate: true });

// Unified reload after a filter change (tag/type/status/source/date). A filter
// change is a query transition: cancel transient state
// (menus, inline edit, non-submitting dialogs, selection, Shift anchor), bump
// the generation, then reset the page and reload documents. Folder search
// results are unaffected (they come from the tree index, name-only).
function reloadAfterFilterChange() {
  if (!kbId.value) return;
  cancelTransientInteraction();
  resetPage();
  loadKnowledgeFiles(kbId.value);
}

// Query-transition watcher: when the user navigates to a different folder or
// the search term changes (folder_id / q in the route query) WITHOUT a KB
// switch, cancel transient state, load the new folder's direct children, and
// reload documents with the new folder/search context. KB switches are handled
// by the kbId watcher above (which resets folder state and loads docs via
// loadKnowledgeBaseInfo), so they are skipped here to avoid a double load.
// Not immediate - the kbId watcher handles the initial load.
watch(
  [kbId, currentFolderId, currentSearchTerm],
  ([newKbId, newFolderId, newSearch], [oldKbId, oldFolderId, oldSearch]) => {
    if (newKbId !== oldKbId) return; // KB switch handled by kbId watcher
    if (newFolderId === oldFolderId && newSearch === oldSearch) return;
    cancelTransientInteraction();
    if (newKbId) {
      // Only reload direct folders when the folder actually changed; a search-
      // only change keeps the current direct folders (they are not rendered in
      // search mode - search renders tree-index matches instead).
      if (newFolderId !== oldFolderId) {
        void folders.loadCurrent(newKbId, newFolderId);
      }
      resetPage();
      loadKnowledgeFiles(newKbId);
    }
  },
);

// Invalid folder_id recovery: when loadCurrent fails with a
// 404/403 for a non-empty currentFolderId, the URL points at a folder that no
// longer exists or the user cannot access. Show a brief toast and router.replace
// to root (folder_id omitted) while preserving `q` via formatKnowledgeFolderRouteQuery.
// The watcher reuses the composable's isFolderNotFoundStatus helper so the
// status check stays in sync with the composable's error mapping. Other errors
// (5xx, network) leave the URL alone and surface as a retry state in the
// template via folderCurrentError + folderDirectLoading.
watch(folderCurrentErrorStatus, (status) => {
  if (!isFolderNotFoundStatus(status)) return;
  if (currentFolderId.value === '') return; // already at root
  // The URL pointed at an inaccessible folder; surface a brief message and
  // replace to root preserving q. The route watcher then reloads root content.
  MessagePlugin.warning(t('knowledgeBase.folderNotFoundHint'));
  const merged: Record<string, string> = {};
  for (const [k, v] of Object.entries(route.query)) {
    if (k === 'folder_id' || k === 'q') continue;
    if (typeof v === 'string') merged[k] = v;
  }
  Object.assign(merged, formatKnowledgeFolderRouteQuery('', currentSearchTerm.value));
  router.replace({ query: merged });
});

// Retry button for transient folder load failures: re-runs loadCurrent for the
// current folder. Used by the error-state template branch below.
function retryFolderLoad() {
  if (!kbId.value) return;
  void folders.loadCurrent(kbId.value, currentFolderId.value);
}
// Minimal tree retry: treeError surfaces as a non-blocking retry on the tree
// panel. refreshAll force-reloads both direct and tree; the tree is the
// relevant half here.
function retryFolderTree() {
  if (!kbId.value) return;
  void folders.refreshAll(kbId.value, currentFolderId.value);
}

// Keep the search input field in sync with the URL `q` (back/forward/refresh
// and programmatic clears). Only update when different so an in-progress
// keystroke cycle is not clobbered.
watch(currentSearchTerm, (term) => {
  if (term !== docSearchKeyword.value) {
    docSearchKeyword.value = term;
  }
}, { immediate: true });

watch(selectedTagIds, (newVal, oldVal) => {
  if (oldVal === undefined) return;
  reloadAfterFilterChange();
}, { deep: true });

watch(tagSearchQuery, (newVal, oldVal) => {
  if (newVal === oldVal) return;
  if (tagSearchDebounce) {
    window.clearTimeout(tagSearchDebounce);
  }
  tagSearchDebounce = window.setTimeout(() => {
    if (kbId.value) {
      loadTags(kbId.value, true);
    }
  }, 300);
});

// Search input -> URL `q` (debounced). The input never triggers a request
// directly; the URL change drives the query-transition watcher above, which
// performs the single reload. Enter flushes; clear removes `q`.
watch(docSearchKeyword, () => {
  scheduleSearchDebounce();
});

// 监听文件类型筛选变化
watch(selectedFileType, (newVal, oldVal) => {
  if (newVal === oldVal) return;
  reloadAfterFilterChange();
});

// 监听解析状态/来源/更新时间范围筛选变化（与文件类型行为一致）
watch([selectedParseStatus, selectedSource, updatedTimeRange], () => {
  reloadAfterFilterChange();
}, { deep: true });


// 监听文件上传事件
const handleFileUploaded = (event: CustomEvent) => {
  const uploadedKbId = event.detail.kbId;
  console.log('接收到文件上传事件，上传的知识库ID:', uploadedKbId, '当前知识库ID:', kbId.value);
  if (uploadedKbId && uploadedKbId === kbId.value && !isFAQ.value) {
    console.log('匹配当前知识库，开始刷新文件列表');
    // 如果上传的文件属于当前知识库，使用 loadKnowledgeFiles 刷新文件列表
    resetPage(); // Reset page counter when reloading files after upload
    loadKnowledgeFiles(uploadedKbId);
    loadTags(uploadedKbId);
    // 启动几次探测，尽快让面包屑的"索引中"亮起。
    scheduleWikiStatusProbes();
  }
};


// 监听从菜单触发的URL导入事件
const handleOpenURLImportDialog = (event: CustomEvent) => {
  const eventKbId = event.detail.kbId;
  console.log('接收到URL导入对话框打开事件，知识库ID:', eventKbId, '当前知识库ID:', kbId.value);
  if (eventKbId && eventKbId === kbId.value && !isFAQ.value) {
    if (ensureDocumentKbReady()) {
      uploadSourceRef.value?.openUrlDialog();
    }
  }
};

// Auto-open document detail when navigated with ?knowledge_id=xxx.
// Note: this runs both when the KB page mounts with a query param AND when a
// subsequent in-page navigation (e.g. from the global command palette) only
// changes the query without re-mounting the component — in that case kbId is
// the same and cardList may already be populated, so relying solely on the
// cardList watcher misses the trigger.
const pendingKnowledgeId = ref<string | null>(
  (route.query.knowledge_id as string) || null
);

const tryAutoOpenDocument = () => {
  if (!pendingKnowledgeId.value || !cardList.value?.length) return;
  const targetId = pendingKnowledgeId.value;
  pendingKnowledgeId.value = null;
  const card = cardList.value.find((c: KnowledgeCard) => c.id === targetId);
  if (card) {
    nextTick(() => openCardDetails(card));
  } else {
    nextTick(() => {
      openCardDetails({ id: targetId } as KnowledgeCard);
    });
  }
};

// React to later ?knowledge_id= changes on the same KB route (no remount).
watch(
  () => route.query.knowledge_id,
  (newId) => {
    if (typeof newId !== 'string' || !newId) return;
    pendingKnowledgeId.value = newId;
    // cardList is almost always already loaded at this point; if not, the
    // cardList watcher below will pick it up.
    tryAutoOpenDocument();
  },
);

// Dispatched by the global command palette when the user picks a chunk that
// lives in the KB they are already viewing — vue-router dedupes identical
// navigations, so we rely on this event instead of a URL change.
const handleOpenKnowledgeEvent = (e: Event) => {
  const detail = (e as CustomEvent<{ kbId: string; knowledgeId: string }>).detail;
  if (!detail || !detail.knowledgeId) return;
  if (detail.kbId && detail.kbId !== kbId.value) return;
  pendingKnowledgeId.value = detail.knowledgeId;
  tryAutoOpenDocument();
};

onMounted(() => {
  loadKnowledgeList();
  editorResources.ensureParserEngines();

  window.addEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  window.addEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
  window.addEventListener('weknora:open-knowledge', handleOpenKnowledgeEvent as EventListener);
});

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  window.removeEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
  window.removeEventListener('weknora:open-knowledge', handleOpenKnowledgeEvent as EventListener);
  stopMovePoll();
  if (timeout !== null) {
    clearTimeout(timeout);
    timeout = null;
  }
});
watch(() => cardList.value, (newValue) => {
  if (isFAQ.value) return;
  docListLoading.value = false;

  // Auto-open document if navigated with ?knowledge_id=xxx
  if (pendingKnowledgeId.value && newValue?.length) {
    tryAutoOpenDocument();
  }

  let analyzeList = [];
  // Filter items that need polling: parsing in progress OR summary generation in progress
  analyzeList = newValue.filter(needsStatusPolling);
  if (timeout !== null) {
    clearTimeout(timeout);
    timeout = null;
  }
  if (analyzeList.length) {
    updateStatus(analyzeList)
  }

}, { deep: true })
type KnowledgeCard = {
  id: string;
  knowledge_base_id?: string;
  parse_status: string;
  summary_status?: string;
  description?: string;
  file_name?: string;
  original_file_name?: string;
  display_name?: string;
  title?: string;
  type?: string;
  updated_at?: string;
  file_type?: string;
  isMore?: boolean;
  metadata?: any;
  error_message?: string;
  tags?: Array<{ id: string; name: string; color?: string }>;
};
// needsStatusPolling decides whether a card row is still "in flight"
// enough that the doc list should keep refreshing it. Keep in sync with
// the backend lifecycle: pending / processing are the primary parse
// phase, finalizing is the post-process fan-out (summary / question /
// graph extract still running), and a `completed` row whose summary
// hasn't landed yet keeps polling so the description fills in.
const needsStatusPolling = (item: KnowledgeCard) => {
  return knowledgeNeedsStatusPolling(item);
};

const updateStatus = (analyzeList: KnowledgeCard[]) => {
  if (timeout !== null) {
    clearTimeout(timeout);
    timeout = null;
  }
  if (!analyzeList.length) return;

  let query = ``;
  for (let i = 0; i < analyzeList.length; i++) {
    query += `ids=${analyzeList[i].id}&`;
  }
  timeout = setTimeout(() => {
    batchQueryKnowledge(query).then((result: any) => {
      let hasChanges = false;
      let shouldRefreshWikiStatus = false;
      if (result.success && result.data) {
        (result.data as KnowledgeCard[]).forEach((item: KnowledgeCard) => {
          const index = cardList.value.findIndex(card => card.id == item.id);
          if (index == -1) return;

          let parseStatus = item.parse_status;
          if (pendingReparseAck.value.has(item.id)) {
            if (isParseInFlight(item.parse_status)) {
              pendingReparseAck.value.delete(item.id);
            } else {
              parseStatus = 'pending';
            }
          }

          if (cardList.value[index].parse_status !== parseStatus ||
            cardList.value[index].summary_status !== item.summary_status ||
            cardList.value[index].description !== item.description) {
            shouldRefreshWikiStatus ||= shouldRefreshWikiStatusAfterKnowledgePoll(
              cardList.value[index],
              { ...item, parse_status: parseStatus },
            );

            // Always update the card data
            cardList.value[index].parse_status = parseStatus;
            cardList.value[index].summary_status = item.summary_status;
            cardList.value[index].description = item.description;
            delete traceAvailableById[item.id];
            hasChanges = true;
          }
        });
      }
      if (shouldRefreshWikiStatus) {
        void fetchWikiStatusOnce();
      }
      // If there are no changes, the watch won't trigger, so we must manually poll again
      // Even if there are changes, we can manually poll again just to be safe.
      // The watch will clear this timeout if it triggers.
      const stillPending = cardList.value.filter(needsStatusPolling);
      if (stillPending.length > 0) {
        updateStatus(stillPending);
      }
    }).catch((_err) => {
      // 错误处理
      const stillPending = cardList.value.filter(needsStatusPolling);
      if (stillPending.length > 0) {
        updateStatus(stillPending);
      }
    });
  }, 1500);
};


// 恢复文档处理状态（用于刷新后恢复）

const closeDoc = () => {
  isCardDetails.value = false;
};
const openCardDetails = (item: KnowledgeCard) => {
  isCardDetails.value = true;
  getCardDetails(item);
};

// Open source document preview from WikiBrowser
const openSourceDoc = (knowledgeId: string) => {
  isCardDetails.value = true;
  getCardDetails({ id: knowledgeId });
};

const closeCardMoreMenu = (index: number) => {
  if (cardList.value?.[index]) {
    cardList.value[index].isMore = false;
  }
  moreIndex.value = -1;
};

const confirmDeleteKnowledge = (index: number, item: KnowledgeCard) => {
  closeCardMoreMenu(index);
  const deletedId = item?.id;
  delKnowledge(index, item, async () => {
    resetPage();
    const maxPolls = 30;
    const delayMs = 400;
    for (let i = 0; i < maxPolls; i++) {
      await loadKnowledgeFiles(kbId.value);
      const stillPresent = (cardList.value || []).some((c: KnowledgeCard) => c.id === deletedId);
      if (!stillPresent) break;
      await new Promise<void>((r) => setTimeout(r, delayMs));
    }
    loadTags(kbId.value, true);
  });
};

const onReparseMenuClick = (index: number, item: KnowledgeCard) => {
  if (isParseInFlight(item.parse_status)) {
    MessagePlugin.info(t('knowledgeBase.rebuildInProgress'));
  }
};

const handleMoveKnowledge = async (item: KnowledgeCard) => {
  moveKnowledgeId.value = item.id;
  moveMenuMode.value = 'targets';
  moveTargetsLoading.value = true;
  moveTargetKbs.value = [];
  try {
    const res: any = await listMoveTargets(kbId.value);
    moveTargetKbs.value = res.data || [];
  } catch {
    moveTargetKbs.value = [];
  } finally {
    moveTargetsLoading.value = false;
  }
};

const handleMoveSelectTarget = (kb: any) => {
  moveSelectedTargetId.value = kb.id;
  moveSelectedTargetName.value = kb.name;
  moveMode.value = 'reuse_vectors';
  moveMenuMode.value = 'confirm';
};

const handleMoveBack = () => {
  if (moveMenuMode.value === 'confirm') {
    moveMenuMode.value = 'targets';
  } else {
    moveMenuMode.value = 'normal';
  }
};

const handleMoveConfirm = async () => {
  if (!moveSelectedTargetId.value || moveSubmitting.value) return;
  moveSubmitting.value = true;
  try {
    const res: any = await moveKnowledge({
      knowledge_ids: [moveKnowledgeId.value],
      source_kb_id: kbId.value,
      target_kb_id: moveSelectedTargetId.value,
      mode: moveMode.value,
    });
    const taskId = res.data?.task_id;
    MessagePlugin.info(t('knowledgeBase.moveStarted'));
    // Close the card menu
    moveMenuMode.value = 'normal';
    cardList.value.forEach(c => { c.isMore = false; });

    if (taskId) {
      startMovePoll(taskId);
    } else {
      moveSubmitting.value = false;
      resetPage(); // Reset page counter when reloading files after move
      loadKnowledgeFiles(kbId.value);
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.moveFailed'));
    moveSubmitting.value = false;
  }
};

const startMovePoll = (taskId: string) => {
  if (movePollTimer) clearInterval(movePollTimer);
  movePollTimer = setInterval(async () => {
    try {
      const res: any = await getKnowledgeMoveProgress(taskId);
      const data = res.data;
      if (!data) return;
      if (data.status === 'completed') {
        stopMovePoll();
        moveSubmitting.value = false;
        const failed = data.failed || 0;
        if (failed > 0) {
          MessagePlugin.warning(t('knowledgeBase.moveCompletedWithErrors', { success: (data.processed || 0) - failed, failed }));
        } else {
          MessagePlugin.success(t('knowledgeBase.moveCompleted'));
        }
        resetPage(); // Reset page counter when reloading files after move completion
        loadKnowledgeFiles(kbId.value);
      } else if (data.status === 'failed') {
        stopMovePoll();
        moveSubmitting.value = false;
        MessagePlugin.error(t('knowledgeBase.moveFailed'));
      }
    } catch {
      // ignore poll errors
    }
  }, 2000);
};

const stopMovePoll = () => {
  if (movePollTimer) {
    clearInterval(movePollTimer);
    movePollTimer = null;
  }
};

const manualEditorSuccess = ({ kbId: savedKbId }: { kbId: string; knowledgeId: string; status: 'draft' | 'publish' }) => {
  if (savedKbId === kbId.value && !isFAQ.value) {
    resetPage(); // Reset page counter when reloading files after manual edit
    loadKnowledgeFiles(savedKbId);
  }
};

const documentTitle = computed(() => {
  if (kbInfo.value?.name) {
    return `${kbInfo.value.name} · ${t('knowledgeEditor.document.title')}`;
  }
  return t('knowledgeEditor.document.title');
});

const ensureDocumentKbReady = () => {
  if (isFAQ.value) {
    MessagePlugin.warning(t('knowledgeBase.operationNotSupportedForType'));
    return false;
  }
  if (!kbId.value) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return false;
  }
  if (!kbInfo.value || !kbInfo.value.summary_model_id) {
    MessagePlugin.warning(t('knowledgeBase.notInitialized'));
    return false;
  }
  // Embedding model only required when RAG indexing is enabled
  const strategy = (kbInfo.value as any).indexing_strategy
  const needsEmbedding = !strategy || strategy.vector_enabled || strategy.keyword_enabled
  if (needsEmbedding && !kbInfo.value.embedding_model_id) {
    MessagePlugin.warning(t('knowledgeBase.notInitialized'));
    return false;
  }
  if (missingStorageEngine.value) {
    MessagePlugin.warning(t('knowledgeBase.missingStorageEngineUpload'));
    return false;
  }
  return true;
};


const IMAGE_EXTENSIONS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp'];
const AUDIO_EXTENSIONS = ['mp3', 'wav', 'm4a', 'flac', 'ogg'];

const uploadConfirmStore = useUploadConfirmStore();

const showUploadResultMessages = (
  successCount: number,
  failCount: number,
  totalCount: number,
  mode: 'document' | 'folder',
) => {
  if (mode === 'folder') {
    if (failCount === 0) {
      MessagePlugin.success(t('knowledgeBase.uploadAllSuccess', { count: successCount }));
    } else if (successCount > 0) {
      MessagePlugin.warning(t('knowledgeBase.uploadPartialSuccess', { success: successCount, fail: failCount }));
    } else {
      MessagePlugin.error(t('knowledgeBase.uploadAllFailed'));
    }
    return;
  }

  if (totalCount === 1) {
    if (successCount === 1) {
      MessagePlugin.success(t('knowledgeBase.uploadSuccess'));
    }
    return;
  }

  if (failCount === 0) {
    MessagePlugin.success(t('knowledgeBase.allUploadSuccess', { count: successCount }));
  } else if (successCount > 0) {
    MessagePlugin.warning(t('knowledgeBase.partialUploadSuccess', { success: successCount, fail: failCount }));
  } else {
    MessagePlugin.error(t('knowledgeBase.allUploadFailed', { count: failCount }));
  }
};

interface StableUploadTarget {
  kbId: string
  folderId: string
  tagIds?: string[]
}

const executeUploadBatch = async (
  files: File[],
  target: StableUploadTarget,
  options: { processConfig?: KnowledgeProcessOverrides } = {},
) => {
  if (!target.kbId || files.length === 0) {
    return { successCount: 0, failCount: files.length };
  }

  let manifest;
  let folderIdByPath = new Map<string, string>();
  let foldersMutated = false;
  try {
    manifest = buildUploadDirectoryManifest(files);
    if (manifest.directoryPaths.length > 0) {
      const resolved = await knowledgeFolderApi.resolvePaths(target.kbId, {
        current_folder_id: target.folderId,
        paths: manifest.directoryPaths,
      });
      folderIdByPath = new Map(
        (resolved.paths || []).map((entry) => [entry.relative_path, entry.folder_id]),
      );
      for (const path of manifest.directoryPaths) {
        if (!folderIdByPath.get(path)) {
          throw new Error(`Folder path was not resolved: ${path}`);
        }
      }
      foldersMutated = true;
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.uploadFailed'));
    return { successCount: 0, failCount: files.length };
  }

  let successCount = 0;
  let failCount = 0;
  const totalCount = files.length;
  const isDirectoryBatch = manifest.directoryPaths.length > 0;

  for (const entry of manifest.files) {
    try {
      const uploadData: {
        file: File
        tag_ids?: string[]
        process_config?: KnowledgeProcessOverrides
        folder_id: string
      } = {
        file: entry.file,
        tag_ids: target.tagIds,
        folder_id: entry.relativeDirectoryPath
          ? folderIdByPath.get(entry.relativeDirectoryPath)!
          : target.folderId,
      };
      if (options.processConfig) {
        uploadData.process_config = options.processConfig;
      }

      const responseData: any = await uploadKnowledgeFile(target.kbId, uploadData);
      const isSuccess = responseData?.success || responseData?.code === 200 || responseData?.status === 'success' || (!responseData?.error && responseData);
      if (isSuccess) {
        successCount++;
      } else {
        failCount++;
        if (totalCount === 1) {
          let errorMessage = t('knowledgeBase.uploadFailed');
          if (responseData?.error?.message) {
            errorMessage = responseData.error.message;
          } else if (responseData?.message) {
            errorMessage = responseData.message;
          }
          if (responseData?.code === 'duplicate_file' || responseData?.error?.code === 'duplicate_file') {
            errorMessage = t('knowledgeBase.fileExists');
          }
          MessagePlugin.error(errorMessage);
        }
      }
    } catch (error: any) {
      failCount++;
      if (totalCount === 1) {
        let errorMessage = error?.error?.message || error?.message || t('knowledgeBase.uploadFailed');
        if (error?.code === 'duplicate_file') {
          errorMessage = t('knowledgeBase.fileExists');
        }
        MessagePlugin.error(errorMessage);
      }
    }
  }

  if (foldersMutated && target.kbId === kbId.value) {
    // Refresh the folder currently being viewed, not the captured upload target:
    // navigation may have changed while resolution/uploads were in flight.
    try {
      await folders.refreshAll(target.kbId, currentFolderId.value);
    } catch (error: any) {
      MessagePlugin.error(error?.message || t('knowledgeBase.folderLoadFailed'));
    }
  }
  if (successCount > 0) {
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: target.kbId },
    }));
  }

  showUploadResultMessages(successCount, failCount, totalCount, isDirectoryBatch ? 'folder' : 'document');
  return { successCount, failCount };
};

const executeUrlImport = async (
  url: string,
  target: StableUploadTarget,
  processConfig?: KnowledgeProcessOverrides,
) => {
  if (!target.kbId) {
    MessagePlugin.error(t('error.missingKbId'));
    return;
  }

  try {
    const responseData: any = await createKnowledgeFromURL(target.kbId, {
      url,
      tag_ids: target.tagIds,
      process_config: processConfig,
      folder_id: target.folderId,
    });
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: target.kbId },
    }));
    const isSuccess = responseData?.success || responseData?.code === 200 || responseData?.status === 'success' || (!responseData?.error && responseData);
    if (isSuccess) {
      MessagePlugin.success(t('knowledgeBase.urlImportSuccess'));
    } else {
      let errorMessage = t('knowledgeBase.urlImportFailed');
      if (responseData?.error?.message) {
        errorMessage = responseData.error.message;
      } else if (responseData?.message) {
        errorMessage = responseData.message;
      }
      if (responseData?.code === 'duplicate_url' || responseData?.error?.code === 'duplicate_url') {
        errorMessage = t('knowledgeBase.urlExists');
      }
      MessagePlugin.error(errorMessage);
    }
  } catch (error: any) {
    let errorMessage = error?.error?.message || error?.message || t('knowledgeBase.urlImportFailed');
    if (error?.code === 'duplicate_url') {
      errorMessage = t('knowledgeBase.urlExists');
    }
    MessagePlugin.error(errorMessage);
  }
};

const handleUploadConfirmResult = async (
  result: UploadConfirmResult,
  target: StableUploadTarget,
) => {
  if (result.mode === 'manual') {
    return;
  }

  const files = result.files || [];
  const urls = result.urls || [];
  const processConfig = result.processConfig;

  if (files.length > 0) {
    const hasFolderPaths = files.some((file) =>
      !!(file as File & { webkitRelativePath?: string }).webkitRelativePath,
    );
    if (hasFolderPaths) {
      MessagePlugin.info(t('knowledgeBase.uploadingFolder', { total: files.length }));
    }
    await executeUploadBatch(files, target, { processConfig });
  }

  for (const url of urls) {
    await executeUrlImport(url, target, processConfig);
  }
};

const openUploadConfirmDialog = async (files: File[], urls: string[] = []) => {
  if (!kbInfo.value) return;
  if (files.length === 0 && urls.length === 0) return;
  const target: StableUploadTarget = {
    kbId: kbId.value,
    folderId: currentFolderId.value,
    tagIds: selectedTagIds.value.length > 0 ? [...selectedTagIds.value] : undefined,
  };
  try {
    const result = await uploadConfirmStore.open({
      mode: 'file',
      kbInfo: kbInfo.value,
      files,
      urls,
      acceptFileTypes: acceptFileTypes.value,
      supportedFileTypes: [...supportedFileTypes.value],
      includeFolderUpload: true,
      targetLocationLabel: currentFolderTargetLabel.value,
    });
    await handleUploadConfirmResult(result, target);
  } catch {
    // cancelled
  }
};

const handleUploadSourceFiles = (files: File[]) => {
  if (!ensureDocumentKbReady()) return;
  if (files.length === 0) return;
  openUploadConfirmDialog(files);
};

const handleUploadSourceUrl = (url: string) => {
  if (!ensureDocumentKbReady()) return;
  openUploadConfirmDialog([], [url]);
};

const handleManualCreate = () => {
  if (!ensureDocumentKbReady()) return;
  uiStore.openManualEditor({
    mode: 'create',
    kbId: kbId.value,
    status: 'draft',
    folderId: currentFolderId.value,
    targetLocationLabel: currentFolderTargetLabel.value,
    onSuccess: manualEditorSuccess,
  });
};

const handleOpenKBSettings = () => {
  if (!kbId.value) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return;
  }
  uiStore.openKBSettings(kbId.value);
};

const handleNavigateToKbList = () => {
  router.push('/platform/knowledge-bases');
};

const handleNavigateToCurrentKB = () => {
  if (!kbId.value) return;
  router.push(`/platform/knowledge-bases/${kbId.value}`);
};

const handleKnowledgeDropdownSelect = (data: { value: string }) => {
  if (!data?.value) return;
  if (data.value === kbId.value) return;
  router.push(`/platform/knowledge-bases/${data.value}`);
};

const handleManualEdit = (index: number, item: KnowledgeCard) => {
  if (isFAQ.value) return;
  if (cardList.value[index]) {
    cardList.value[index].isMore = false;
  }
  uiStore.openManualEditor({
    mode: 'edit',
    kbId: item.knowledge_base_id || kbId.value,
    knowledgeId: item.id,
    onSuccess: manualEditorSuccess,
  });
};

// Opens ONLY the trace drawer for this card — does NOT pop the
// document detail drawer behind it. The trace drawer attaches to
// body so it renders independent of its host's visibility; we just
// need `details` populated so the timeline component knows which
// knowledge_id to fetch. getCardDetails resets details synchronously
// then fills asynchronously, so we re-stamp the id/parse_status
// right after the call to avoid the brief empty-id window that
// would otherwise prevent the drawer from mounting.
const docContentRef = ref<any>(null);
const handleViewTrace = (index: number, item: KnowledgeCard) => {
  if (cardList.value[index]) {
    cardList.value[index].isMore = false;
  }
  moreIndex.value = -1;
  getCardDetails(item);
  details.id = item.id;
  details.parse_status = item.parse_status;
  nextTick(() => {
    docContentRef.value?.openTimeline?.();
  });
};

const confirmRebuildKnowledge = async (index: number, item: KnowledgeCard) => {
  if (isFAQ.value) return;
  if (!canEdit.value) return;
  if (!item?.id) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return;
  }
  if (isParseInFlight(item.parse_status)) {
    MessagePlugin.info(t('knowledgeBase.rebuildInProgress'));
    return;
  }
  closeCardMoreMenu(index);

  // No KB context to seed the dialog defaults — fall back to a direct reparse
  // that reuses the overrides stored at upload time.
  if (!kbInfo.value) {
    await submitReparse(item.id);
    return;
  }

  // Prefill the confirm dialog with the overrides this doc was last parsed with.
  let processOverrides: KnowledgeProcessOverrides | null = item.metadata?.process_overrides ?? null;
  let fileName = item.file_name || item.title || '';
  let fileType = item.file_type || '';
  try {
    const detail: any = await getKnowledgeDetails(item.id);
    if (detail?.success && detail.data) {
      processOverrides = detail.data.metadata?.process_overrides ?? processOverrides;
      fileName = detail.data.file_name || detail.data.title || fileName;
      fileType = detail.data.file_type || fileType;
    }
  } catch {
    // fall back to the list item's fields
  }

  try {
    const result = await uploadConfirmStore.open({
      mode: 'reparse',
      kbInfo: kbInfo.value,
      reparse: { knowledgeId: item.id, fileName, fileType, processOverrides },
    });
    if (result.mode === 'reparse' && result.reparse) {
      await submitReparse(result.reparse.knowledgeId, result.processConfig);
    }
  } catch {
    // cancelled
  }
};

const submitReparse = async (id: string, processConfig?: KnowledgeProcessOverrides) => {
  try {
    await reparseKnowledge(id, processConfig ? { process_config: processConfig } : undefined);
    delete traceAvailableById[id];
    traceAvailableById[id] = true;
    MessagePlugin.success(t('knowledgeBase.rebuildSubmitted'));
    resetPage();
    loadKnowledgeFiles(kbId.value);
    scheduleWikiStatusProbes();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.rebuildFailed'));
  }
};

const handleScroll = () => {
  if (isFAQ.value) return;
  if (docListLoading.value) return;
  if (scrollLoading) return;
  const currentKbId = kbId.value;
  if (!currentKbId) return;
  const element = knowledgeScroll.value;
  if (element) {
    let pageNum = Math.ceil(total.value / pageSize)
    const { scrollTop, scrollHeight, clientHeight } = element;
    if (scrollTop + clientHeight >= scrollHeight - 10) {
      if (cardList.value.length < total.value && page < pageNum) {
        page++;
        scrollLoading = true;
        getKnowled({ page, page_size: pageSize, ...filterParams.value }, currentKbId).finally(() => {
          if (isCurrentKb(currentKbId)) {
            scrollLoading = false;
          }
        });
      }
    }
  }
};
const getDoc = (page: number) => {
  getfDetails(details.id, page)
};

const toggleSelectRow = (id: string, checked: boolean, shiftKey?: boolean) => {
  const key = `knowledge:${id}`;
  if (shiftKey && lastSelectedKey && lastSelectedKey !== key) {
    const renderedKeys = buildRenderedSelectionKeys(displayedFolders.value, cardList.value);
    const anchorIdx = renderedKeys.indexOf(lastSelectedKey);
    const currentIdx = renderedKeys.indexOf(key);
    if (anchorIdx >= 0 && currentIdx >= 0) {
      const [s, e] = currentIdx < anchorIdx ? [currentIdx, anchorIdx] : [anchorIdx, currentIdx];
      applyRenderedKeyRange(renderedKeys, s, e, checked);
    }
  } else {
    if (checked) selectedIds.value.add(id);
    else selectedIds.value.delete(id);
  }
  lastSelectedKey = key;
};

const onCardGridCheckboxChange = (id: string, checked: boolean, ctx?: { e?: Event }) => {
  const me = ctx?.e as MouseEvent | undefined;
  toggleSelectRow(id, checked, !!me?.shiftKey);
};

const toggleSelectAll = (checked: boolean) => {
  if (checked) {
    for (const item of cardList.value || []) selectedIds.value.add(item.id);
    for (const folder of displayedFolders.value) selectedFolderIds.value.add(folder.id);
  } else {
    for (const item of cardList.value || []) selectedIds.value.delete(item.id);
    for (const folder of displayedFolders.value) selectedFolderIds.value.delete(folder.id);
  }
};

// Batch (multi-select) mode mirrors the session list's "批量管理" UX: while off,
// no checkbox is rendered so the title doesn't jitter on hover; while on,
// checkboxes are persistent and clicking a card toggles its selection.
const batchMode = ref(false);
const toggleBatchMode = () => {
  batchMode.value = !batchMode.value;
  if (!batchMode.value) clearSelection();
};
// "取消选择" / 退出批量管理：清空选择，并退出 grid 视图下的批量模式。
const handleBatchCancel = () => {
  clearSelection();
  batchMode.value = false;
};
// 切到卡片视图时，如果列表视图里已经勾选过文档，需要自动开启批量管理模式，
// 否则卡片视图默认不渲染 checkbox，会看不到勾选态。
// 切到卡片视图时，如果列表视图里已经勾选过文档或文件夹，需要自动开启批量管理模式，
// 否则卡片视图默认不渲染 checkbox，会看不到勾选态。
watch(viewMode, (mode) => {
  if (mode === 'grid' && selectionCount(unifiedSelection.value) > 0) {
    batchMode.value = true;
  }
});
// Triggered from a card / row "..." menu — match the session-list UX where
// the menu item simply opens batch mode (no auto-selection).
const handleEnterBatchFromCard = (item: any) => {
  if (item) item.isMore = false;
  moreIndex.value = -1;
  clearSelection();
  batchMode.value = true;
};
// 从文件夹菜单进入批量管理：与文档版对齐（清空当前选择 + 进多选模式，不预选该文件夹）。
const handleFolderBatchManage = (_folderId: string) => {
  moreIndex.value = -1;
  clearSelection();
  batchMode.value = true;
};
const {
  onContainerMouseDown: onDocMarqueeMouseDown,
  marqueeVisible: docMarqueeVisible,
  marqueeMode: docMarqueeMode,
  boxStyle: docMarqueeBoxStyle,
  shouldSuppressClick: shouldSuppressDocClick,
} = useMarqueeSelect({
  containerRef: knowledgeScroll,
  // Typed-key marquee path: include folder cards/rows alongside document
  // cards/rows. The typed `data-select-key` attribute (folder:<id> /
  // knowledge:<id>) is what marquee reads back, so folder and document IDs
  // can never collide. Reads/writes flow through the selectedKeys writable
  // computed, which splits back into selectedFolderIds / selectedIds.
  itemSelector:
    '.knowledge-card[data-select-key], .doc-list-row[data-select-key], .folder-card[data-select-key], .folder-list-row[data-select-key]',
  selectedKeys,
  getItemKey: (el) => el.dataset.selectKey || null,
  enabled: computed(
    () => canEdit.value && !isFAQ.value && (cardList.value.length > 0 || displayedFolders.value.length > 0),
  ),
  onSelectionStart: () => {
    batchMode.value = true;
  },
});

const isManualDraftKnowledge = (item: KnowledgeCard) =>
  item.type === 'manual' && item.parse_status === 'draft';

const openKnowledgeItem = (item: KnowledgeCard) => {
  if (shouldSuppressDocClick()) return;
  if (canEdit.value && isManualDraftKnowledge(item)) {
    const index = cardList.value.findIndex((c) => c.id === item.id);
    if (index >= 0) {
      handleManualEdit(index, item);
      return;
    }
  }
  openCardDetails(item);
};

const confirmBatchDelete = async () => {
  if (batchDeleting.value || batchReparsing.value || selectionCount(unifiedSelection.value) === 0) return;
  const selection = unifiedSelection.value;
  const ids = Array.from(selection.knowledgeIds);
  const folderIds = Array.from(selection.folderIds);
  const deletedIdSet = new Set(ids);
  const deletedFolderSet = new Set(folderIds);
  const totalCount = ids.length + folderIds.length;
  batchDeleting.value = true;
  try {
    // Route through useFolderOperations.deleteFolders so the same batch-delete
    // endpoint carries both knowledge_ids and folder_ids (mixed selections are
    // one request). The backend expands folder_ids to their full descendant
    // subtrees async; the polling below waits for documents to leave the list.
    await folderOperations.deleteFolders(kbId.value, selection, async () => {
      // onDone: refresh folder caches so the deleted folders leave the tree and
      // direct-folders list. Document list is refreshed via the poll below.
      if (deletedFolderSet.size > 0 && kbId.value) {
        await folders.refreshAll(kbId.value, currentFolderId.value);
      }
    });
    MessagePlugin.success(t('knowledgeBase.batchDeleteSuccess', { count: totalCount }));
    clearSelection();
    batchMode.value = false;
    resetPage();
    // 后端将批量删除放入异步队列，立刻拉列表仍可能包含待删项；短轮询直到列表与后端一致或超时
    if (deletedIdSet.size > 0) {
      const maxPolls = 30;
      const delayMs = 400;
      for (let i = 0; i < maxPolls; i++) {
        await loadKnowledgeFiles(kbId.value);
        const stillPresent = (cardList.value || []).some((c: KnowledgeCard) => deletedIdSet.has(c.id));
        if (!stillPresent) break;
        await new Promise<void>((r) => setTimeout(r, delayMs));
      }
    }
    loadTags(kbId.value, true);
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.batchDeleteFailed'));
  } finally {
    batchDeleting.value = false;
  }
};

const confirmCancelParseKnowledge = async (item: KnowledgeCard) => {
  if (!item?.id) return;
  try {
    await cancelKnowledgeParse(item.id);
    MessagePlugin.success(t('knowledgeBase.cancelParseSubmitted'));
    loadKnowledgeFiles(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.cancelParseFailed'));
  }
};

// Bridge card-view actions back to existing per-card handlers.
const handleCardAction = (
  action: 'edit' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'delete' | 'view-trace' | 'batch-manage',
  item: KnowledgeCard,
) => {
  const idx = (cardList.value || []).findIndex((i: KnowledgeCard) => i.id === item.id);
  if (action === 'edit') return handleManualEdit(idx, item);
  if (action === 'reparse') {
    if (isParseInFlight(item.parse_status)) return onReparseMenuClick(idx, item);
    return confirmRebuildKnowledge(idx, item);
  }
  if (action === 'cancel-parse') return confirmCancelParseKnowledge(item);
  if (action === 'move') return handleMoveKnowledge(item);
  if (action === 'move-folder') return openMoveFolderFlow(`knowledge:${item.id}`);
  if (action === 'delete') return confirmDeleteKnowledge(idx, item);
  if (action === 'view-trace') return handleViewTrace(idx, item);
  if (action === 'batch-manage') return handleEnterBatchFromCard(item);
};

// Bridge list-view actions back to existing per-card handlers.
const handleListAction = (
  action: 'edit' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'delete' | 'view-trace' | 'batch-manage',
  item: KnowledgeCard,
) => {
  const idx = (cardList.value || []).findIndex((i: KnowledgeCard) => i.id === item.id);
  if (action === 'edit') return handleManualEdit(idx, item);
  if (action === 'reparse') return confirmRebuildKnowledge(idx, item);
  if (action === 'cancel-parse') return confirmCancelParseKnowledge(item);
  if (action === 'move') return handleMoveKnowledge(item);
  if (action === 'move-folder') return openMoveFolderFlow(`knowledge:${item.id}`);
  if (action === 'delete') return confirmDeleteKnowledge(idx, item);
  if (action === 'view-trace') return handleViewTrace(idx, item);
  if (action === 'batch-manage') return handleEnterBatchFromCard(item);
};

// Clear selection on filter/kb change to avoid acting on hidden items. NOTE:
// `docSearchKeyword` is intentionally NOT watched here - the URL `q` (not the
// keystroke) is the query transition; selection is cleared by the route
// watcher's cancelTransientInteraction when `q` actually changes.
watch(
  [selectedTagIds, selectedFileType, selectedParseStatus, selectedSource, updatedTimeRange, kbId],
  () => {
    clearSelection();
  },
);

// After cardList reloads: drop stale document selections (deleted items that
// are no longer visible). The typed Shift anchor (lastSelectedKey) is looked
// up by key on the next Shift+click via buildRenderedSelectionKeys, so it
// does not need index-based clamping - if the anchor is no longer rendered
// the range computation is a no-op.
watch(cardList, () => {
  const items = cardList.value || [];
  const n = items.length;
  if (moreIndex.value >= n) {
    moreIndex.value = -1;
  }
  if (selectedIds.value.size === 0) return;
  const visible = new Set(items.map((i: KnowledgeCard) => i.id));
  for (const id of selectedIds.value) {
    if (!visible.has(id)) selectedIds.value.delete(id);
  }
}, { deep: false });

// 处理知识库编辑成功后的回调
const handleKBEditorSuccess = (kbIdValue: string) => {
  chatResources.invalidateKnowledgeBaseDetail(kbIdValue);
  chatResources.invalidate('knowledgeBases');
  loadKnowledgeList();
  if (kbIdValue === kbId.value) {
    loadKnowledgeBaseInfo(kbIdValue, true);
  }
};

const getTitle = (session_id: string, value: string) => {
  const now = new Date().toISOString();
  let obj = {
    title: t('knowledgeBase.newSession'),
    path: `chat/${session_id}`,
    id: session_id,
    isMore: false,
    isNoTitle: true,
    created_at: now,
    updated_at: now
  };
  usemenuStore.updataMenuChildren(obj);
  usemenuStore.changeIsFirstSession(true);
  usemenuStore.changeFirstQuery(value);
  router.push(`/platform/chat/${session_id}`);
};

async function createNewSession(value: string): Promise<void> {
  // Session 不再和知识库绑定，直接创建 Session
  createSessions({}).then(res => {
    if (res.data && res.data.id) {
      getTitle(res.data.id, value);
    } else {
      // 错误处理
      console.error(t('knowledgeBase.createSessionFailed'));
    }
  }).catch(error => {
    console.error(t('knowledgeBase.createSessionError'), error);
  });
}
</script>

<template>
  <template v-if="!isFAQ">
    <div class="knowledge-layout">
      <div class="document-header">
        <div class="document-header-title">
          <div class="document-title-row">
            <h2 class="document-breadcrumb">
              <button type="button" class="breadcrumb-link" @click="handleNavigateToKbList">
                {{ $t('menu.knowledgeBase') }}
              </button>
              <t-icon name="chevron-right" class="breadcrumb-separator" />
              <KBSwitcherDropdown v-if="knowledgeList.length" :kb-list="knowledgeList" :current-kb-id="kbId"
                @select="(id) => handleKnowledgeDropdownSelect({ value: id })">
                <button type="button" class="breadcrumb-link dropdown" :disabled="!kbId">
                  <template v-if="!kbInfo">
                    <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '20px' }]" />
                  </template>
                  <template v-else>
                    <span>{{ kbInfo.name }}</span>
                    <t-icon name="chevron-down" />
                  </template>
                </button>
              </KBSwitcherDropdown>
              <button v-else type="button" class="breadcrumb-link" :disabled="!kbId" @click="handleNavigateToCurrentKB">
                <template v-if="!kbInfo">
                  <t-skeleton animation="gradient" :row-col="[{ width: '120px', height: '20px' }]" />
                </template>
                <template v-else>
                  {{ kbInfo.name }}
                </template>
              </button>
              <t-icon name="chevron-right" class="breadcrumb-separator" />
              <template v-if="isWiki">
                <span :class="['breadcrumb-tab', { active: activeKbTab === 'documents' }]"
                  @click="activeKbTab = 'documents'">{{ $t('knowledgeEditor.wikiBrowser.tabDocuments') }}</span>
                <span class="breadcrumb-tab-sep">/</span>
                <span :class="['breadcrumb-tab', { active: activeKbTab === 'wiki', indexing: wikiIsIndexing }]"
                  @click="activeKbTab = 'wiki'">
                  Wiki
                  <t-tooltip v-if="wikiIsIndexing" :content="wikiIndexingTip" placement="bottom">
                    <t-loading size="small" class="breadcrumb-tab-indicator" />
                  </t-tooltip>
                </span>
                <span class="breadcrumb-tab-sep">/</span>
                <t-tooltip :content="$t('knowledgeEditor.wikiBrowser.tabGraphTip')" placement="bottom">
                  <span :class="['breadcrumb-tab', { active: activeKbTab === 'graph', indexing: wikiIsIndexing }]"
                    @click="activeKbTab = 'graph'">
                    {{ $t('knowledgeEditor.wikiBrowser.tabGraph') }}
                    <t-tooltip v-if="wikiIsIndexing" :content="wikiIndexingTip" placement="bottom">
                      <t-loading size="small" class="breadcrumb-tab-indicator" />
                    </t-tooltip>
                  </span>
                </t-tooltip>
              </template>
              <span v-else class="breadcrumb-current">{{ $t('knowledgeEditor.document.title') }}</span>
            </h2>
            <!-- 标题行右侧的动作锚点：聚拢"信息"和"设置"两个圆形按钮。 -->
            <div class="kb-title-actions">
              <KBInfoPopover v-if="kbInfo && !authStore.isLiteMode" :kb-info="kbInfo"
                :supported-file-types="[...supportedFileTypes]" />
              <t-tooltip v-if="canManage" :content="$t('knowledgeBase.settings')" placement="top">
                <button type="button" class="kb-settings-button" :disabled="!kbId" @click="handleOpenKBSettings">
                  <t-icon name="setting" size="16px" />
                </button>
              </t-tooltip>
            </div>
          </div>
          <p class="document-subtitle">{{ $t('knowledgeEditor.document.subtitle') }}</p>
          <p v-if="unsupportedFileTypes.length" class="parser-hint" @click="goToParserSettings">
            <t-icon name="info-circle" class="parser-hint-icon" />
            <span>{{$t('knowledgeBase.unsupportedTypesHint', {
              types: unsupportedFileTypes.map(t => '.' + t).join('、')
            })
              }}</span>
            <span class="parser-hint-link">{{ $t('knowledgeBase.goToParserSettings') }} →</span>
          </p>
          <p v-if="missingStorageEngine" class="storage-engine-warning" @click="handleOpenKBSettings">
            <t-icon name="info-circle" class="warning-icon" />
            <span>{{ $t('knowledgeBase.missingStorageEngine') }}</span>
            <span class="warning-link">{{ $t('knowledgeBase.goToStorageSettings') }} →</span>
          </p>
        </div>
      </div>

      <!-- Wiki Browser / Graph (shown when wiki or graph tab is active) -->
      <div v-if="isWiki && (activeKbTab === 'wiki' || activeKbTab === 'graph')" class="wiki-main-area">
        <WikiBrowser v-if="kbId" :knowledge-base-id="kbId" :view="activeKbTab === 'graph' ? 'graph' : 'browser'"
          :can-edit="canEdit" @open-source-doc="openSourceDoc" @status-change="onWikiStatusChange"
          @view-graph="onViewWikiInGraph" />
      </div>

      <template v-if="activeKbTab === 'documents' || !isWiki">
        <div class="knowledge-main">
          <!-- Folder tree sidebar: sibling of the content area, visible toggled
               by the breadcrumb's tree-reveal button. No DnD. -->
          <FolderNavigationPanel
            :tree="folderTree"
            :current-folder-id="currentFolderId"
            :expanded-folder-ids="folderExpandedIds"
            :editable="folderEditable"
            :visible="folderTreeVisible"
            :loading="folderTreeLoading"
            :error="folderTreeError"
            :creating-parent-id="folderCreatingParentId"
            :create-error="folderCreateError"
            @navigate="handleFolderOpen"
            @toggle-expand="toggleFolderExpanded"
            @action="handleFolderTreeAction"
            @toggle="setFolderTreeVisible(!folderTreeVisible)"
            @retry="retryFolderTree"
            @create-commit="(name: string) => commitFolderCreate(name)"
            @create-cancel="cancelFolderCreate"
          />
          <div class="tag-content">
            <div class="doc-card-area">
              <!-- Folder path breadcrumb + search status + tree-reveal button.
                   Search status overrides the path. -->
              <FolderBreadcrumb
                :items="folderBreadcrumb"
                :tree-visible="folderTreeVisible"
                :search-term="currentSearchTerm"
                @navigate="handleFolderOpen"
                @toggle-tree="setFolderTreeVisible(!folderTreeVisible)"
              />
              <!-- Search-mode hint: other filters apply only to documents.
                   Folder results come from the tree index and are not narrowed
                   by these filters. -->
              <div v-if="isSearchMode" class="doc-search-filter-note" role="status">
                <t-icon name="info-circle" size="14px" aria-hidden="true" />
                <span>{{ $t('knowledgeBase.searchFiltersApplyOnlyToDocs') }}</span>
              </div>
              <div class="doc-filter-bar">
                <t-input v-model.trim="docSearchKeyword" :placeholder="$t('knowledgeBase.docSearchPlaceholder')"
                  clearable class="doc-search-input" @clear="clearSearchInput"
                  @enter="flushSearchDebounce">
                  <template #prefix-icon>
                    <t-icon name="search" size="16px" />
                  </template>
                </t-input>
                <div class="doc-filter-bar__filters">
                <t-popup v-model:visible="tagFilterPanelVisible" trigger="click" placement="bottom-left"
                  overlay-class-name="tag-filter-popup" :overlay-inner-style="{ padding: 0 }">
                  <template #content>
                    <div class="tag-filter-panel" @click.stop>
                      <div class="tag-filter-panel__header">
                        <div class="tag-filter-panel__title">
                          <span>{{ $t('knowledgeBase.tagFilterTitle') }}</span>
                          <span class="tag-filter-panel__count">({{ sidebarCategoryCount }})</span>
                        </div>
                      </div>
                      <div class="tag-search-bar">
                        <t-input v-model.trim="tagSearchQuery" size="small"
                          :placeholder="$t('knowledgeBase.tagSearchPlaceholder')" clearable>
                          <template #prefix-icon>
                            <t-icon name="search" size="14px" />
                          </template>
                        </t-input>
                      </div>
                      <div class="tag-filter-panel__body">
                        <template v-if="tagLoading && !sidebarTags.length">
                          <div class="tag-filter-chips">
                            <div v-for="n in 8" :key="'skel-tag-' + n" class="tag-filter-chip-skeleton">
                              <t-skeleton animation="gradient"
                                :row-col="[{ width: '56px', height: '24px', type: 'rect' }]" />
                            </div>
                          </div>
                        </template>
                        <template v-else>
                          <div class="tag-filter-chips">
                            <button
                              v-for="tag in sidebarTags"
                              :key="tag.id"
                              type="button"
                              class="tag-filter-chip"
                              :class="{ active: isTagFilterActive(tag.id) }"
                              :title="`${tag.name} (${tag.knowledge_count || 0})`"
                              @click="handleTagRowClick(tag.id)"
                            >
                              <span class="tag-filter-chip__label">{{ tag.name }}</span>
                              <span class="tag-filter-chip__count">{{ tag.knowledge_count || 0 }}</span>
                            </button>
                          </div>
                          <div v-if="!sidebarTags.length" class="tag-empty-state">
                            {{ $t('knowledgeBase.tagEmptyResult') }}
                          </div>
                          <div v-if="tagHasMore" class="tag-load-more">
                            <t-button variant="text" size="small" :loading="tagLoadingMore"
                              @click.stop="kbId && loadTags(kbId)">
                              {{ $t('tenant.loadMore') }}
                            </t-button>
                          </div>
                        </template>
                      </div>
                      <div v-if="canEdit" class="tag-filter-panel__footer">
                        <t-button variant="text" size="small" class="tag-manage-link" @click="openTagManageDrawer">
                          {{ $t('knowledgeBase.tagManageLink') }}
                        </t-button>
                      </div>
                    </div>
                  </template>
                  <div class="doc-filter-field">
                    <button type="button" class="doc-tag-filter-trigger doc-filter-field__control"
                      :class="{ open: tagFilterPanelVisible, 'is-placeholder': isTagFilterPlaceholder }"
                      :aria-label="$t('knowledgeBase.tagFilterTitle')"
                      :title="activeTagFilterTitle"
                      @mouseenter="tagFilterTriggerHover = true"
                      @mouseleave="tagFilterTriggerHover = false">
                      <span class="doc-tag-filter-trigger__prefix" aria-hidden="true">
                        <t-icon name="discount" size="16px" />
                      </span>
                      <span class="doc-tag-filter-trigger__label">{{ activeTagFilterLabel }}</span>
                      <span class="doc-tag-filter-trigger__suffix">
                        <span
                          v-if="showTagFilterClear"
                          class="t-input__suffix t-input__suffix-icon t-input__clear"
                          :aria-label="$t('common.clear')"
                          @click.stop="clearTagFilter"
                          @mousedown.stop
                        >
                          <t-icon name="close-circle-filled" class="t-input__suffix-clear" />
                        </span>
                        <t-icon
                          v-else
                          name="chevron-down"
                          size="16px"
                          class="doc-tag-filter-trigger__caret"
                          :class="{ open: tagFilterPanelVisible }"
                        />
                      </span>
                    </button>
                  </div>
                </t-popup>
                <div class="doc-filter-field">
                  <t-select v-model="selectedFileType" :options="fileTypeOptions"
                    :placeholder="$t('knowledgeBase.fileTypeFilter')" class="doc-type-select doc-filter-field__control"
                    clearable>
                    <template #prefixIcon>
                      <t-icon name="file" size="16px" />
                    </template>
                  </t-select>
                </div>
                <div class="doc-filter-field">
                  <t-select v-model="selectedParseStatus" :options="parseStatusOptions"
                    :placeholder="$t('knowledgeBase.parseStatusFilter')" class="doc-type-select doc-filter-field__control"
                    clearable>
                    <template #prefixIcon>
                      <t-icon name="check-circle" size="16px" />
                    </template>
                  </t-select>
                </div>
                <div class="doc-filter-field">
                  <t-select v-model="selectedSource" :options="sourceOptions"
                    :placeholder="$t('knowledgeBase.sourceFilter')" class="doc-type-select doc-filter-field__control"
                    clearable>
                    <template #prefixIcon>
                      <t-icon name="link" size="16px" />
                    </template>
                  </t-select>
                </div>
                <div class="doc-filter-field doc-filter-field--wide">
                  <t-date-range-picker v-model="updatedTimeRange"
                    :placeholder="[$t('knowledgeBase.updatedTimeFrom'), $t('knowledgeBase.updatedTimeTo')]"
                    :disable-date="disableFutureDate" class="doc-date-range doc-filter-field__control" clearable
                    allow-input>
                    <template #prefixIcon>
                      <t-icon name="time" size="16px" />
                    </template>
                  </t-date-range-picker>
                </div>
                </div>
                <div class="doc-filter-bar__trailing">
                  <div class="doc-view-toggle" role="group" :aria-label="$t('knowledgeBase.viewModeToggle')">
                    <t-tooltip :content="$t('knowledgeBase.viewModeGrid')" placement="top">
                      <button type="button" class="doc-view-toggle-btn" :class="{ active: viewMode === 'grid' }"
                        @click="viewMode = 'grid'" :aria-pressed="viewMode === 'grid'">
                        <t-icon name="view-module" size="16px" />
                      </button>
                    </t-tooltip>
                    <t-tooltip :content="$t('knowledgeBase.viewModeList')" placement="top">
                      <button type="button" class="doc-view-toggle-btn" :class="{ active: viewMode === 'list' }"
                        @click="viewMode = 'list'" :aria-pressed="viewMode === 'list'">
                        <t-icon name="view-list" size="16px" />
                      </button>
                    </t-tooltip>
                  </div>
                  <div v-if="canEdit" class="doc-filter-actions">
                    <KbUploadSourceDropdown ref="uploadSourceRef" :accept-file-types="acceptFileTypes"
                      :supported-file-types="[...supportedFileTypes]" include-manual :include-folder-upload="true" trigger-icon="file-add"
                      trigger-class="content-bar-icon-btn" data-guide="kb-detail-add-doc"
                      :tooltip="t('knowledgeBase.addDocument')" placement="bottom-right" @files="handleUploadSourceFiles"
                      @url="handleUploadSourceUrl" @manual="handleManualCreate" />
                  </div>
                </div>
              </div>
              <div class="doc-scroll-container"
                :class="{ 'is-empty': !hasContent && !docListLoading && !isCreatingFolder, 'is-marquee-active': docMarqueeVisible }"
                ref="knowledgeScroll" @scroll="handleScroll" @mousedown="onDocMarqueeMouseDown">
                <div v-if="docMarqueeVisible" class="doc-marquee-box"
                  :class="{ 'is-add': docMarqueeMode === 'add', 'is-subtract': docMarqueeMode === 'subtract' }"
                  :style="docMarqueeBoxStyle" aria-hidden="true" />
                <!-- 文档骨架屏 -->
                <div v-if="docListLoading && !hasContent && !isCreatingFolder" class="doc-card-list doc-card-list-animated">
                  <div v-for="n in 8" :key="'doc-skel-' + n" class="knowledge-card knowledge-card-skeleton">
                    <div class="card-content">
                      <div class="card-content-nav">
                        <t-skeleton animation="gradient" :row-col="[{ width: '70%', height: '18px' }]" />
                      </div>
                      <t-skeleton animation="gradient"
                        :row-col="[{ width: '100%', height: '14px' }, { width: '60%', height: '14px' }]" />
                    </div>
                    <div class="card-bottom">
                      <t-skeleton animation="gradient"
                        :row-col="[[{ width: '80px', height: '14px' }, { width: '40px', height: '18px', type: 'rect' }]]" />
                    </div>
                  </div>
                </div>
                <template v-else-if="(hasContent || isCreatingFolder) && viewMode === 'grid'">
                  <DocumentCardView
                    :items="cardList"
                    :selected-ids="selectedIds"
                    :batch-mode="batchMode"
                    :can-edit="canEdit"
                    :can-mutate-knowledge="canMutateKnowledge"
                    :trace-available-by-id="traceAvailableById"
                    :tag-list="tagList"
                    :move-menu-mode="moveMenuMode"
                    :move-target-kbs="moveTargetKbs"
                    :move-targets-loading="moveTargetsLoading"
                    :move-selected-target-name="moveSelectedTargetName"
                    :move-mode="moveMode"
                    :move-submitting="moveSubmitting"
                    @open="(item: any) => openKnowledgeItem(item)"
                    @toggle-checkbox="onCardGridCheckboxChange"
                    @menu-visible-change="(visible: boolean, item: any) => onCardMoreVisibleChange(visible, item)"
                    @action="(action: any, item: any) => handleCardAction(action, item)"
                    @tag-edit="(item: any) => openTagEditDialog(item)"
                    @move-select-target="(kb: any) => handleMoveSelectTarget(kb)"
                    @move-back="handleMoveBack"
                    @move-confirm="handleMoveConfirm"
                    @update:move-mode="(mode: any) => moveMode = mode"
                  >
                    <template #before-items>
                      <!-- Inline folder create input (page-owned; folder card/row
                           components only own the rename input). Shown at the top
                           of the folder area when create mode is active. -->
                      <div v-if="isCreatingFolder" class="folder-card folder-create-card" @click.stop>
                        <input :ref="setFolderCreateInput" v-model.trim="folderCreateDraft"
                          class="folder-card-rename-input folder-create-input" type="text"
                          :placeholder="$t('knowledgeBase.newFolder')" :aria-label="$t('knowledgeBase.newFolder')"
                          @click.stop @keydown.enter.prevent="commitFolderCreate(folderCreateDraft)"
                          @keydown.esc.prevent="cancelFolderCreate" @blur="commitFolderCreate(folderCreateDraft)" />
                      </div>
                      <FolderGridItems v-if="displayedFolders.length" :folders="displayedFolders"
                        :selected-folder-ids="selectedFolderIds" :editable="folderEditable"
                        :batch-mode="batchMode"
                        :renaming-folder-id="folderRenamingId" :rename-error="folderRenameError"
                        @open="handleFolderOpen" @toggle-selection="onFolderToggleSelection"
                        @create="handleCardCreateSubfolder" @rename="handleFolderRename"
                        @rename-commit="handleFolderRenameCommit" @rename-cancel="handleFolderRenameCancel"
                        @move-folder="(id: string) => openMoveFolderFlow(`folder:${id}`)"
                        @batch-manage="handleFolderBatchManage"
                        @delete="handleFolderDelete" />
                    </template>
                  </DocumentCardView>
                </template>
                <template v-else-if="(hasContent || isCreatingFolder) && viewMode === 'list'">
                  <DocumentListView :items="cardList" :selected-ids="selectedIds" :tag-list="tagList"
                    :can-edit="canEdit" :can-mutate-knowledge="canMutateKnowledge"
                    :trace-visible-ids="traceAvailableById"
                    :move-menu-mode="moveMenuMode"
                    :move-target-kbs="moveTargetKbs"
                    :move-targets-loading="moveTargetsLoading"
                    :move-selected-target-name="moveSelectedTargetName"
                    :move-mode="moveMode"
                    :move-submitting="moveSubmitting"
                    @open="(item: any) => openKnowledgeItem(item)" @toggle-row="toggleSelectRow"
                    @toggle-all="toggleSelectAll" @action="(action: any, item: any) => handleListAction(action, item)"
                    @probe-trace="(item: any) => probeTraceAvailable(item)"
                    @tag-edit="(item: any) => openTagEditDialog(item)"
                    @move-select-target="(kb: any) => handleMoveSelectTarget(kb)"
                    @move-back="handleMoveBack"
                    @move-confirm="handleMoveConfirm"
                    @update:move-mode="(mode: any) => moveMode = mode"
                    @reset-move-state="moveMenuMode = 'normal'">
                    <template #before-rows>
                      <div v-if="isCreatingFolder" class="folder-list-row folder-create-row" role="row" @click.stop>
                        <div class="cell cell-check"></div>
                        <div class="cell cell-name">
                          <span class="row-folder-icon-wrap" aria-hidden="true"><t-icon name="folder-add" /></span>
                          <div class="row-folder-text">
                            <input :ref="setFolderCreateInput" v-model.trim="folderCreateDraft"
                              class="folder-list-rename-input folder-create-input" type="text"
                              :placeholder="$t('knowledgeBase.newFolder')" :aria-label="$t('knowledgeBase.newFolder')"
                              @click.stop @keydown.enter.prevent="commitFolderCreate(folderCreateDraft)"
                              @keydown.esc.prevent="cancelFolderCreate" @blur="commitFolderCreate(folderCreateDraft)" />
                          </div>
                        </div>
                        <div class="cell cell-tag"><span class="row-muted">--</span></div>
                        <div class="cell cell-source"><span class="row-muted">--</span></div>
                        <div class="cell cell-size"><span class="row-muted">--</span></div>
                        <div class="cell cell-status"><span class="row-muted">--</span></div>
                        <div class="cell cell-time"></div>
                        <div class="cell cell-actions"></div>
                      </div>
                      <FolderListRows v-if="displayedFolders.length" :folders="displayedFolders"
                        :selected-folder-ids="selectedFolderIds" :editable="folderEditable"
                        :renaming-folder-id="folderRenamingId" :rename-error="folderRenameError"
                        @open="handleFolderOpen" @toggle-selection="onFolderToggleSelection"
                        @create="handleCardCreateSubfolder" @rename="handleFolderRename"
                        @rename-commit="handleFolderRenameCommit" @rename-cancel="handleFolderRenameCancel"
                        @move-folder="(id: string) => openMoveFolderFlow(`folder:${id}`)"
                        @batch-manage="handleFolderBatchManage"
                        @delete="handleFolderDelete" />
                    </template>
                  </DocumentListView>
                </template>
                <template v-else-if="!docListLoading">
                  <!-- Error / empty states:
                       - folder load failed (non-404/403): error state with retry,
                         NOT the empty-folder copy. 404/403 are handled by the
                         folderCurrentErrorStatus watcher (replace to root).
                       - search mode, no results -> no-results copy (no upload CTA);
                       - browse root, no content -> EmptyKnowledge upload copy for
                         editors; viewer gets a plain no-CTA message;
                       - named folder, nothing inside -> folder-empty copy, NEVER the
                         upload copy (upload targets root/current folder only). -->
                  <div v-if="!isSearchMode && folderCurrentError && !hasContent
                    && !isFolderNotFoundStatus(folderCurrentErrorStatus)"
                    class="doc-empty-state doc-empty-state--error" role="alert">
                    <t-icon name="info-circle" size="40px" class="doc-empty-icon" aria-hidden="true" />
                    <span class="doc-empty-title">{{ $t('knowledgeBase.folderLoadFailed') }}</span>
                    <t-button theme="primary" variant="outline" size="small"
                      :loading="folderDirectLoading" @click="retryFolderLoad">
                      {{ $t('knowledgeBase.folderRetry') }}
                    </t-button>
                  </div>
                  <div v-else-if="isSearchMode" class="doc-empty-state doc-empty-state--no-results">
                    <t-icon name="search" size="40px" class="doc-empty-icon" aria-hidden="true" />
                    <span class="doc-empty-title">{{ $t('knowledgeBase.searchNoResults') }}</span>
                  </div>
                  <div v-else-if="currentFolderId === '' && canEdit" class="doc-empty-state">
                    <EmptyKnowledge />
                  </div>
                  <div v-else class="doc-empty-state doc-empty-state--plain">
                    <t-icon name="folder-open" size="40px" class="doc-empty-icon" aria-hidden="true" />
                    <span class="doc-empty-title">
                      {{ currentFolderId === '' ? $t('knowledgeBase.emptyRootViewer') : $t('knowledgeBase.emptyFolderHint') }}
                    </span>
                  </div>
                </template>
              </div>
              <div class="doc-batch-bar-anchor"
                v-show="batchMode || selectionCount(unifiedSelection) > 0">
                <FileSystemBatchBar :selection="unifiedSelection"
                  :delete-loading="batchDeleting" :reparse-loading="batchReparsing"
                  :move-loading="folderMoving" :reparse-over-limit="reparseOverLimit"
                  :visible="batchMode || selectionCount(unifiedSelection) > 0"
                  @clear="handleBatchCancel"
                  @move-folder="openMoveFolderFlowFromSelection(unifiedSelection)"
                  @delete="confirmBatchDelete" @reparse="confirmBatchReparse" />
              </div>
            </div>
          </div>
        </div>
      </template>

      <!-- DocContent drawer (shared by documents tab and wiki source refs) -->
      <DocContent ref="docContentRef" :visible="isCardDetails" :details="details" :canEditKB="canEdit" :kbId="kbId"
        @closeDoc="closeDoc" @getDoc="getDoc">
      </DocContent>
    </div>
  </template>
  <template v-else>
    <div class="faq-manager-wrapper">
      <FAQEntryManager v-if="kbId" :kb-id="kbId" />
    </div>
  </template>

  <!-- 知识库编辑器（创建/编辑统一组件） -->
  <KnowledgeBaseEditorModal :visible="uiStore.showKBEditorModal" :mode="uiStore.kbEditorMode"
    :kb-id="uiStore.currentKBId || undefined" :initial-type="uiStore.kbEditorType"
    @update:visible="(val) => val ? null : uiStore.closeKBEditor()" @success="handleKBEditorSuccess" />

  <ContextualGuide tour="kbDetail" :when="showKbDetailContextualGuide" />

  <!-- 标签编辑弹窗 -->
  <TagEditDialog :visible="tagEditDialogVisible"
    :knowledge-name="tagEditTarget?.display_name || tagEditTarget?.file_name || tagEditTarget?.title || ''"
    :kb-id="kbId" :tag-list="tagList" :selected-tags="tagEditTarget?.tags || []" :can-manage="canEdit"
    @update:visible="tagEditDialogVisible = $event" @confirm="onTagEditConfirm" @tag-created="loadTags(kbId, true)"
    @open-manage="openTagManageFromEditDialog" />

  <KbTagManageDrawer
    v-model:visible="tagManageDrawerVisible"
    :kb-id="kbId"
    :is-faq="isFAQ"
    @changed="onTagManageChanged"
  />

  <!-- 移动到文件夹 (same-KB move picker; distinct from cross-KB transfer) -->
  <FolderPickerDialog
    :visible="folderPickerVisible"
    :tree="folderTree"
    :index="folderIndex"
    :selected-folder-ids="moveFolderSelectedFolderIds"
    :current-parent-id="moveFolderCurrentParentId"
    :loading="folderTreeLoading"
    :submitting="folderMoving"
    @update:visible="handleFolderPickerVisibleChange"
    @confirm="handleFolderPickerConfirm"
  />
</template>
<style>
/* 下拉菜单容器样式已统一至 @/assets/dropdown-menu.less */
.tag-filter-popup {
  z-index: 5500 !important;
}

.tag-filter-popup .t-popup__content {
  padding: 0 !important;
  border-radius: 8px !important;
  background: var(--td-bg-color-container) !important;
  border: 0.5px solid var(--td-component-stroke) !important;
  box-shadow:
    0 0 0 0.5px rgba(0, 0, 0, 0.03),
    0 2px 4px rgba(0, 0, 0, 0.04),
    0 8px 24px rgba(0, 0, 0, 0.1) !important;
}

.tag-more-popup .tag-menu {
  display: flex;
  flex-direction: column;
}

.tag-more-popup .tag-menu-item {
  display: flex;
  align-items: center;
  padding: 8px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 400;
}

.tag-more-popup .tag-menu-item .menu-icon {
  margin-right: 8px;
  font-size: 16px;
}

.tag-more-popup .tag-menu-item:hover {
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
}
</style>
<style scoped lang="less">
.knowledge-layout {
  display: flex;
  flex-direction: column;
  margin: 0 16px 0 4px;
  gap: 12px;
  height: 100%;
  flex: 1;
  width: 100%;
  min-width: 0;
  padding: 24px 32px 0px;
  box-sizing: border-box;
}

// Breadcrumb tab switch (文档/Wiki in breadcrumb)
.breadcrumb-tab {
  cursor: pointer;
  color: var(--td-text-color-placeholder);
  font-weight: 400;
  transition: color 0.15s;
  display: inline-flex;
  align-items: center;
  gap: 4px;

  &:hover {
    color: var(--td-text-color-primary);
  }

  &.active {
    color: var(--td-brand-color);
    font-weight: 600;
  }

  &.indexing {
    color: var(--td-brand-color);
  }
}

.breadcrumb-tab-indicator {
  display: inline-flex;
  align-items: center;
  color: var(--td-brand-color);
  font-size: 12px;
  line-height: 1;
}

.breadcrumb-tab-sep {
  margin: 0 6px;
  color: var(--td-text-color-disabled);
  font-weight: 400;
}

.wiki-main-area {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

// 与列表页一致：浅灰底圆角区，左侧筛选为白底卡片
.knowledge-main {
  display: flex;
  flex: 1;
  min-height: 0;
  background: transparent;
  border: none;
}

// 标签筛选浮层：点击工具栏入口展开，不占文档列表横向空间
.tag-filter-panel {
  width: 320px;
  max-width: min(320px, calc(100vw - 32px));
  max-height: min(70vh, 480px);
  display: flex;
  flex-direction: column;
  padding: 12px 14px;
  box-sizing: border-box;
  font-size: 12px;
  color: var(--td-text-color-primary);

  .tag-filter-panel__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
    padding: 0;
    color: var(--td-text-color-primary);
  }

  .tag-filter-panel__title {
    display: flex;
    align-items: baseline;
    gap: 6px;
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.5px;
  }

  .tag-filter-panel__count {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    font-weight: 400;
  }

  .tag-search-bar {
    margin-bottom: 10px;
    padding: 0;

    :deep(.t-input) {
      font-size: 13px;
      background-color: var(--td-bg-color-secondarycontainer);
      border-color: transparent;
      border-radius: 6px;
      box-shadow: none !important;

      &:hover,
      &:focus,
      &.t-is-focused {
        border-color: var(--td-component-border);
        background-color: var(--td-bg-color-container);
        box-shadow: none !important;
      }
    }

    :deep(.t-input__inner) {
      font-size: 13px;
    }

    :deep(.t-input__prefix-icon) {
      margin-right: 0;
    }
  }

  .tag-filter-panel__body {
    display: flex;
    flex-direction: column;
    gap: 8px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
    scrollbar-width: thin;

    &::-webkit-scrollbar {
      width: 4px;
    }

    &::-webkit-scrollbar-thumb {
      border-radius: 2px;
      background: var(--td-scrollbar-color);
    }
  }

  .tag-filter-chips {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 6px;
  }

  .tag-filter-chip-skeleton {
    flex-shrink: 0;
  }

  .tag-filter-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    box-sizing: border-box;
    max-width: 100%;
    height: 24px;
    padding: 0 8px;
    border: 1px solid var(--td-component-stroke);
    border-radius: 4px;
    background: transparent;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 11px;
    font-weight: 400;
    line-height: 24px;
    cursor: pointer;
    outline: none;
    transition: background 0.15s ease, color 0.15s ease, border-color 0.15s ease;
    -webkit-font-smoothing: antialiased;

    &:hover:not(.active) {
      border-color: var(--td-component-border);
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-primary);
    }

    &:focus-visible {
      box-shadow: 0 0 0 2px color-mix(in srgb, var(--td-component-stroke) 60%, transparent);
    }

    &.active {
      border-color: color-mix(in srgb, var(--td-brand-color) 35%, var(--td-component-stroke));
      color: var(--td-brand-color);
      font-weight: 500;
      background-color: color-mix(in srgb, var(--td-brand-color) 6%, transparent);

      .tag-filter-chip__count {
        color: color-mix(in srgb, var(--td-brand-color) 72%, var(--td-text-color-secondary));
      }

      &:hover {
        background-color: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      }
    }
  }

  .tag-filter-chip__label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 120px;
  }

  .tag-filter-chip__count {
    flex-shrink: 0;
    font-size: 10px;
    font-weight: 400;
    font-variant-numeric: tabular-nums;
    color: var(--td-text-color-placeholder);

    &::before {
      content: '·';
      margin-right: 2px;
      opacity: 0.65;
    }
  }

  .tag-filter-panel__footer {
    margin-top: 10px;
    padding-top: 10px;
    border-top: 1px solid var(--td-component-stroke);
    display: flex;
    justify-content: flex-start;

    :deep(.tag-manage-link.t-button) {
      padding: 0;
      height: auto;
      min-height: 0;
      font-size: 13px;
      color: var(--td-text-color-secondary);
      border: none !important;
      background: transparent !important;
      box-shadow: none !important;
      transition: color 0.15s ease;

      &:hover,
      &:focus-visible {
        color: var(--td-brand-color) !important;
        background: transparent !important;
        border-color: transparent !important;
        text-decoration: none;
      }
    }
  }

  .tag-load-more {
    display: flex;
    justify-content: center;
    padding-top: 2px;

    :deep(.t-button) {
      padding: 0;
      font-size: 12px;
      color: var(--td-text-color-placeholder);
    }
  }

  .tag-empty-state {
    text-align: center;
    padding: 6px 0;
    color: var(--td-text-color-placeholder);
    font-size: 12px;
  }
}

.tag-content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 0;
  border: none;
  overflow: hidden;
  background: transparent;
}

.doc-card-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  position: relative;
  /* 作为批量工具栏悬浮的定位上下文 */
}

.doc-card-area > :deep(.folder-breadcrumb) {
  margin-bottom: 12px;
}

.doc-filter-bar {
  padding: 0 0 12px 0;
  flex-shrink: 0;
  display: grid;
  grid-template-columns: 1fr auto;
  grid-template-areas:
    'search trailing'
    'filters filters';
  gap: 8px 12px;
  align-items: center;

  .doc-search-input {
    grid-area: search;
    min-width: 0;
    width: 100%;
  }

  &__filters {
    grid-area: filters;
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
    overflow-x: auto;
    flex-wrap: nowrap;
    -webkit-overflow-scrolling: touch;
    scrollbar-width: thin;
    scrollbar-color: rgba(0, 0, 0, 0.15) transparent;

    &::-webkit-scrollbar {
      height: 4px;
    }

    &::-webkit-scrollbar-thumb {
      background-color: rgba(0, 0, 0, 0.15);
      border-radius: 2px;
    }
  }

  &__trailing {
    grid-area: trailing;
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }

  @media (min-width: 1280px) {
    display: flex;
    flex-direction: row;
    flex-wrap: nowrap;
    gap: 12px;

    &__filters {
      flex: 0 1 auto;
      overflow-x: visible;
    }
  }

  .doc-filter-field {
    width: 140px;
    flex-shrink: 0;

    &--wide {
      width: 280px;
    }

    &__control {
      width: 100%;
    }
  }

  .doc-tag-filter-trigger {
    display: inline-flex;
    align-items: center;
    box-sizing: border-box;
    width: 100%;
    height: 32px;
    padding: 0 8px;
    border: 1px solid transparent;
    border-radius: var(--td-radius-default);
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
    transition: background 0.2s ease, border-color 0.2s ease;

    &:hover,
    &.open {
      background: var(--td-bg-color-secondarycontainer);
      border-color: transparent;
    }

    &.is-placeholder {
      color: var(--td-text-color-placeholder);
    }

    &__prefix {
      flex-shrink: 0;
      display: inline-flex;
      align-items: center;
      margin-right: var(--td-comp-margin-s);
      color: var(--td-text-color-placeholder);
    }

    &__label {
      flex: 1;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      text-align: left;
    }

    &__suffix {
      flex-shrink: 0;
      display: inline-flex;
      align-items: center;
      margin-left: var(--td-comp-margin-s);

      :deep(.t-input__suffix) {
        margin-left: 0;
      }

      :deep(.t-input__suffix-clear) {
        font-size: 16px;
      }
    }

    &__caret {
      flex-shrink: 0;
      color: var(--td-text-color-placeholder);
      transition: transform 0.2s ease, color 0.2s ease;

      &.open {
        color: var(--td-brand-color);
        transform: rotate(180deg);
      }
    }
  }

  @media (min-width: 1280px) {
    .doc-search-input {
      flex: 1 1 220px;
      min-width: 220px;
    }
  }

  .doc-type-select {
    width: 100%;
  }

  .doc-date-range {
    width: 100%;

    // TDesign focuses both the outer popup reference and inner inputs, which
    // visually stacks into a "double border" — drop the inner shadow.
    :deep(.t-input--focused),
    :deep(.t-is-focused) {
      box-shadow: none;
    }
  }

  .doc-view-toggle {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    padding: 2px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 6px;
    gap: 0;

    .doc-view-toggle-btn {
      width: 28px;
      height: 24px;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border: 0;
      background: transparent;
      border-radius: 4px;
      color: var(--td-text-color-secondary, #888);
      cursor: pointer;
      transition: background-color 0.12s ease, color 0.12s ease;

      &:hover {
        color: var(--td-text-color-primary, #232323);
      }

      &.active {
        background: var(--td-bg-color-container, #fff);
        color: var(--td-brand-color, #0052d9);
        box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
      }
    }
  }

  .doc-filter-actions {
    flex-shrink: 0;

    :deep(.content-bar-icon-btn) {
      color: var(--td-text-color-secondary);
      background: transparent;
      border: none;

      &:hover {
        color: var(--td-brand-color);
        background: var(--td-bg-color-secondarycontainer);
      }
    }
  }

  :deep(.t-input) {
    font-size: 13px;
    background-color: var(--td-bg-color-secondarycontainer);
    border-color: transparent;
    border-radius: 6px;
    box-shadow: none !important;

    &:hover,
    &:focus,
    &.t-is-focused {
      border-color: var(--td-brand-color);
      background-color: var(--td-bg-color-container);
      box-shadow: none !important;
    }
  }

  :deep(.t-select) {
    .t-input {
      font-size: 13px;
      background-color: var(--td-bg-color-secondarycontainer);
      border-color: transparent;
      border-radius: 6px;
      box-shadow: none !important;

      &:hover,
      &.t-is-focused {
        border-color: var(--td-brand-color);
        background-color: var(--td-bg-color-container);
        box-shadow: none !important;
      }
    }
  }
}

.doc-scroll-container {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overflow-x: hidden;
  padding-right: 4px;

  &.is-empty {
    display: flex;
    align-items: center;
    justify-content: center;
    overflow-y: hidden;
  }

  &.is-marquee-active {
    cursor: crosshair;
  }
}

.doc-marquee-box {
  position: absolute;
  z-index: 4;
  pointer-events: none;
  border: 1px solid var(--td-brand-color);
  background: color-mix(in srgb, var(--td-brand-color) 12%, transparent);
  border-radius: 2px;

  &.is-add {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 14%, transparent);
  }

  &.is-subtract {
    border-color: var(--td-error-color-6);
    background: color-mix(in srgb, var(--td-error-color-6) 12%, transparent);
  }
}

/* 批量条悬浮在滚动区底部，不挤占列表高度 */
.doc-batch-bar-anchor {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 12px;
  z-index: 6;
  display: flex;
  justify-content: center;
  padding: 0 16px;
  pointer-events: none;

  &>* {
    pointer-events: auto;
  }
}

// Header 样式（无底部分割线，留更多空间给下方内容区）
.document-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  flex-shrink: 0;

  .document-header-title {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .document-title-row {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .kb-title-actions {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
    margin-left: 4px;
  }

  .document-breadcrumb {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  .breadcrumb-link {
    border: none;
    background: transparent;
    padding: 4px 8px;
    margin: -4px -8px;
    font: inherit;
    color: var(--td-text-color-secondary);
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border-radius: 6px;
    transition: all 0.12s ease;

    &:hover:not(:disabled) {
      color: var(--td-success-color);
      background: var(--td-bg-color-container);
    }

    &:disabled {
      cursor: not-allowed;
      color: var(--td-text-color-placeholder);
    }

    &.dropdown {
      padding-right: 6px;

      :deep(.t-icon) {
        font-size: 14px;
        transition: transform 0.12s ease;
      }

      &:hover:not(:disabled) {
        :deep(.t-icon) {
          transform: translateY(1px);
        }
      }
    }
  }

  .breadcrumb-separator {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
  }

  .breadcrumb-current {
    color: var(--td-text-color-primary);
    font-weight: 600;
  }

  h2 {
    margin: 0;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }

  .document-subtitle {
    margin: 0;
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
  }

  .parser-hint {
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 2px 0 0;
    color: var(--td-warning-color);
    font-size: 12px;
    line-height: 1.4;
    cursor: pointer;
    transition: color 0.15s ease;

    &:hover {
      color: var(--td-warning-color-active);

      .parser-hint-link {
        text-decoration: underline;
      }
    }

    .parser-hint-icon {
      font-size: 12px;
      flex-shrink: 0;
    }

    .parser-hint-link {
      color: var(--td-brand-color);
      margin-left: 2px;
      white-space: nowrap;
    }
  }

  .storage-engine-warning {
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 2px 0 0;
    color: var(--td-warning-color);
    font-size: 12px;
    line-height: 1.4;
    cursor: pointer;
    transition: color 0.15s ease;

    &:hover {
      color: var(--td-warning-color-active);

      .warning-link {
        text-decoration: underline;
      }
    }

    .warning-icon {
      font-size: 12px;
      flex-shrink: 0;
    }

    .warning-link {
      color: var(--td-brand-color);
      margin-left: 2px;
      white-space: nowrap;
    }
  }
}


.document-upload-input {
  display: none;
}

.kb-settings-button {
  width: 30px;
  height: 30px;
  border: none;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: all 0.2s ease;
  padding: 0;

  &:hover:not(:disabled) {
    background: var(--td-success-color-light);
    color: var(--td-brand-color);
    box-shadow: none;
  }

  &:disabled {
    cursor: not-allowed;
    opacity: 0.4;
  }

  :deep(.t-icon) {
    font-size: 18px;
  }
}

.tag-filter-bar {
  display: flex;
  align-items: center;
  gap: 16px;

  .tag-filter-label {
    color: var(--td-text-color-placeholder);
    font-size: 14px;
  }
}

.card-tag-selector {
  display: flex;
  align-items: center;

  .card-tag-chips {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    flex-wrap: nowrap;
    cursor: pointer;
  }

  .card-tag-overflow {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    height: 18px;
    min-width: 18px;
    padding: 0 5px;
    border-radius: 999px;
    border: 1px solid var(--td-component-stroke);
    color: var(--td-text-color-placeholder);
    font-size: 10px;
    line-height: 1;
    cursor: pointer;
    transition: all 0.2s ease;

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color);
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  :deep(.t-tag) {
    cursor: pointer;
    max-width: 120px;
    height: 18px;
    line-height: 18px;
    border-radius: 999px;
    border-color: var(--td-component-stroke);
    color: var(--td-text-color-secondary);
    padding: 0 6px;
    background: transparent;
    transition: all 0.2s ease;

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color-active);
      background: var(--td-bg-color-secondarycontainer);
    }
  }

  .tag-text {
    display: inline-block;
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
    font-size: 11px;
  }

  .card-tag-add {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    height: 18px;
    padding: 0 6px;
    border-radius: 999px;
    border: 1px dashed var(--td-component-stroke);
    color: var(--td-text-color-placeholder);
    font-size: 11px;
    cursor: pointer;
    transition: all 0.2s ease;

    .t-icon {
      font-size: 12px;
    }

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color-active);
      background: var(--td-bg-color-secondarycontainer);
      border-style: solid;
    }
  }
}


.card-bottom-right {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  overflow: hidden;
}

.faq-manager-wrapper {
  flex: 1;
  min-height: 0;
  padding: 24px 32px;
  overflow-y: auto;
  margin: 0 16px 0 4px;
}

@media (max-width: 1250px) and (min-width: 1045px) {
  .answers-input {
    transform: translateX(-329px);
  }

  :deep(.t-textarea__inner) {
    width: 654px !important;
  }
}

@media (max-width: 1045px) {
  .answers-input {
    transform: translateX(-250px);
  }

  :deep(.t-textarea__inner) {
    width: 500px !important;
  }
}

@media (max-width: 750px) {
  .answers-input {
    transform: translateX(-182px);
  }

  :deep(.t-textarea__inner) {
    width: 340px !important;
  }
}

@media (max-width: 600px) {
  .answers-input {
    transform: translateX(-164px);
  }

  :deep(.t-textarea__inner) {
    width: 300px !important;
  }
}

@keyframes contentFadeIn {
  from {
    opacity: 0;
    transform: translateY(6px);
  }

  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.doc-card-list {
  box-sizing: border-box;
  display: grid;
  // 文档卡片信息量较大（标题 + 摘要 + 标签/类型），保持稍宽的最小列宽，避免一行塞太多导致内容拥挤。
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 12px;
  align-content: flex-start;
  width: 100%;

  &.doc-card-list-animated {
    animation: contentFadeIn 0.32s ease-out;
  }
}

.knowledge-card-skeleton {
  cursor: default;

  .card-content {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 10px 14px 8px;
  }

  .card-content-nav {
    margin-bottom: 8px;
  }

  .card-bottom {
    flex-shrink: 0;
    margin-top: auto;
    width: 100%;
    padding: 0 14px;
    box-sizing: border-box;
    height: 32px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    border-top: 1px solid var(--td-component-stroke);
  }
}

.doc-empty-state {
  flex: 1;
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;
  min-height: 100%;
}

// Search-mode filter hint + plain empty states (no-results / named folder /
// viewer root). The root-empty editor case keeps <EmptyKnowledge/>.
.doc-empty-state--no-results,
.doc-empty-state--plain,
.doc-empty-state--error {
  flex-direction: column;
  gap: 12px;
}

.doc-empty-state--error .doc-empty-icon {
  color: var(--td-error-color-6);
}

.doc-empty-icon {
  color: var(--td-text-color-placeholder);
}

.doc-empty-title {
  color: var(--td-text-color-placeholder);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 500;
  line-height: 22px;
  text-align: center;
}

.doc-search-filter-note {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  margin-bottom: 8px;
  border-radius: 6px;
  background: var(--td-brand-color-light);
  color: var(--td-brand-color);
  font-family: var(--app-font-family);
  font-size: 12px;
  line-height: 18px;
}

// Inline folder create input (page-owned; folder card/row components only own
// the rename input). Grid variant matches the .folder-card footprint.
.folder-create-card {
  min-width: 240px;
  height: 136px;
  border: 1px dashed var(--td-brand-color);
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 14px;
  box-sizing: border-box;
  background: var(--td-brand-color-light);
}

.folder-create-input {
  width: 100%;
  height: 28px;
  padding: 0 6px;
  border: 1px solid var(--td-brand-color);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 600;
  outline: none;
  box-sizing: border-box;

  &:focus {
    box-shadow: 0 0 0 2px var(--td-brand-color-light);
  }
}

// List-variant create row mirrors FolderListRows' grid so the input aligns
// column-for-column with document/folder rows. (.cell base styles are scoped
// to FolderListRows, so they are re-declared here for the slot content.)
.folder-create-row {
  display: grid;
  grid-template-columns:
    44px
    minmax(260px, 2.6fr)
    minmax(100px, 0.9fr)
    minmax(96px, 0.8fr)
    96px
    minmax(96px, 0.7fr)
    140px
    48px;
  align-items: center;
  padding: 0 16px;
  min-height: 60px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-brand-color-light);

  .cell {
    display: flex;
    align-items: center;
    min-width: 0;
    padding: 0 8px;
  }

  .cell-name {
    gap: 10px;
  }

  .row-folder-icon-wrap {
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    border-radius: 6px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 16px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
  }

  .row-folder-text {
    flex: 1;
    min-width: 0;
  }

  .row-muted {
    color: var(--td-text-color-disabled, #bbb);
    font-size: 12px;
  }
}

.card-menu {
  display: flex;
  flex-direction: column;
  min-width: 140px;
  gap: 1px;
}

.card-menu-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 12px;
  cursor: pointer;
  color: var(--td-text-color-primary);
  transition: all 0.15s cubic-bezier(0.2, 0, 0, 1);
  border-radius: 6px;
  font-size: 14px;
  line-height: 20px;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &:active {
    background: var(--td-bg-color-container-active);
    transform: scale(0.98);
  }

  .icon {
    font-size: 16px;
    color: var(--td-text-color-secondary);
    transition: all 0.15s cubic-bezier(0.2, 0, 0, 1);
  }

  &:hover .icon {
    color: var(--td-text-color-primary);
  }

  &.danger {
    color: var(--td-error-color-6);
    margin-top: 4px;
    position: relative;

    &::before {
      content: '';
      position: absolute;
      top: -3px;
      left: 8px;
      right: 8px;
      height: 1px;
      background: var(--td-component-stroke);
    }

    .icon {
      color: var(--td-error-color-6);
    }

    &:hover {
      background: var(--td-error-color-1);
      color: var(--td-error-color-6);

      .icon {
        color: var(--td-error-color-6);
      }
    }

    &:active {
      background: var(--td-error-color-2);
    }
  }
}

.move-menu {
  min-width: 220px;
  max-width: 280px;
  max-height: 360px;
  overflow-y: auto;

  .move-menu-header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 12px;
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    border-bottom: 1px solid var(--td-component-stroke);
    cursor: pointer;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }
  }

  .move-menu-loading {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 20px 0;
  }

  .move-menu-empty {
    padding: 12px 16px;
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    text-align: center;
    line-height: 1.5;
  }

  .move-target-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .move-target-count {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }

  .move-confirm-body {
    padding: 8px;

    .move-target-info {
      display: flex;
      align-items: center;
      gap: 6px;
      padding: 6px 8px;
      background: var(--td-bg-color-container-hover);
      border-radius: 6px;
      font-size: 13px;
      color: var(--td-text-color-secondary);
      margin-bottom: 8px;
    }

    .move-mode-item {
      display: flex;
      align-items: flex-start;
      gap: 6px;
      padding: 6px 8px;
      border-radius: 6px;
      cursor: pointer;
      margin-bottom: 4px;

      &:hover {
        background: var(--td-bg-color-container-hover);
      }

      &.active {
        background: var(--td-brand-color-light);
      }

      .move-mode-text {
        display: flex;
        flex-direction: column;
        gap: 2px;

        .move-mode-label {
          font-size: 13px;
          font-weight: 500;
          color: var(--td-text-color-primary);
        }

        .move-mode-desc {
          font-size: 11px;
          color: var(--td-text-color-placeholder);
          line-height: 1.4;
        }
      }
    }

    .move-confirm-actions {
      display: flex;
      justify-content: flex-end;
      gap: 8px;
      margin-top: 8px;
    }
  }
}

.card-draft {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 0;
  flex-shrink: 0;
}

.card-draft-tip {
  color: var(--td-warning-color);
  font-size: 11px;
}

.knowledge-card {
  min-width: 240px;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-border);
  height: 136px;
  border-radius: 8px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease, background-color 0.2s ease;

  /* 仅在批量管理模式下渲染 checkbox，常态下不占位，避免标题在 hover 时右滑 */
  .card-nav-check {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 29px;
    margin-right: 8px;
    cursor: pointer;

    .card-select-checkbox {
      margin: 0;
      line-height: 0;

      :deep(.t-checkbox) {
        align-items: center;
      }

      :deep(.t-checkbox__label) {
        display: none !important;
        width: 0 !important;
        min-width: 0 !important;
        margin: 0 !important;
        padding: 0 !important;
      }

      :deep(.t-checkbox__input) {
        margin: 0;
      }

      :deep(.t-checkbox__input-wrapper) {
        margin: 0;
      }
    }
  }

  .card-content {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 10px 14px 8px;
  }

  .card-analyze {
    flex-shrink: 0;
    height: 52px;
    display: flex;
    align-items: flex-start;
  }

  .card-analyze-loading {
    display: block;
    color: var(--td-brand-color);
    font-size: 14px;
    margin-top: 2px;
  }

  .card-analyze-txt {
    color: var(--td-brand-color);
    font-family: var(--app-font-family);
    font-size: 11px;
    margin-left: 8px;
  }

  // In-flight / failed: only status text + trace icon open the drawer.
  .card-analyze-trace {
    height: auto;
    min-height: 0;
    align-items: center;
    gap: 2px;
  }

  .card-analyze-trace-link {
    cursor: pointer;

    &:hover {
      text-decoration: underline;
    }
  }

  .card-analyze-trace-btn {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    margin: 0;
    padding: 2px;
    border: none;
    background: transparent;
    color: var(--td-brand-color);
    cursor: pointer;
    line-height: 1;
    border-radius: 4px;

    :deep(.t-icon) {
      font-size: 14px;
    }

    &:hover {
      background: var(--td-bg-color-component-hover);
    }
  }

  .card-analyze.failure .card-analyze-trace-btn {
    color: var(--td-error-color);
  }

  .failure {
    color: var(--td-error-color);
  }

  .card-content-nav {
    flex-shrink: 0;
    display: flex;
    align-items: flex-start;
    gap: 0;
    margin-bottom: 6px;
  }

  .card-content-title {
    flex: 1;
    min-width: 0;
    height: 24px;
    line-height: 24px;
    display: inline-block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--td-text-color-primary);
    font-family: var(--app-font-family);
    font-size: 14px;
    font-weight: 600;
    letter-spacing: 0.01em;
    margin-right: 8px;
  }

  .more-wrap {
    flex-shrink: 0;
    display: flex;
    width: 25px;
    height: 25px;
    justify-content: center;
    align-items: center;
    border-radius: 5px;
    cursor: pointer;
  }

  .more-wrap:hover {
    background: var(--td-component-stroke);
  }

  .more-icon {
    width: 14px;
    height: 14px;
  }

  .active-more {
    background: var(--td-component-stroke);
  }

  .card-content-txt {
    flex: 1;
    min-height: 0;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    line-height: 19px;
  }

  .card-bottom {
    flex-shrink: 0;
    margin-top: auto;
    padding: 0 14px;
    box-sizing: border-box;
    height: 32px;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--td-bg-color-container);
    border-top: 1px solid var(--td-component-stroke);
  }

  .card-time {
    flex-shrink: 0;
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);
    font-size: 12px;
    font-weight: 400;
    white-space: nowrap;
  }

  .card-type {
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: 11px;
    font-weight: 500;
    padding: 0;
    background: transparent;
    letter-spacing: 0.02em;
  }
}

.card-bottom-right {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  overflow: hidden;
}

.knowledge-card:hover {
  border-color: color-mix(in srgb, var(--td-component-stroke) 55%, var(--td-brand-color));
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.07);
}

/* 悬停知识卡片时跟随鼠标的详情气泡 */
.knowledge-card-hover-popover {
  position: fixed;
  z-index: 9999;
  pointer-events: none;
  min-width: 220px;
  max-width: 360px;
  padding: 12px 14px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  font-family: var(--app-font-family);
  transition: opacity 0.15s ease;
  will-change: transform;

  /* 防止气泡内容抖动 */
  backface-visibility: hidden;
  -webkit-backface-visibility: hidden;
  transform: translateZ(0);
  -webkit-transform: translateZ(0);

  .card-popover-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin-bottom: 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .card-popover-status {
    font-size: 12px;
    margin-bottom: 6px;
    display: flex;
    align-items: center;
    gap: 6px;

    &.parsing {
      color: var(--td-brand-color);
    }

    &.failure {
      color: var(--td-error-color);
    }

    &.draft {
      color: var(--td-warning-color);
    }
  }

  .card-popover-desc {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    line-height: 1.5;
    margin-bottom: 8px;
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 5;
    line-clamp: 5;
    overflow: hidden;
  }

  .card-popover-error-msg {
    display: block;
    margin-top: 4px;
    font-size: 11px;
    color: var(--td-error-color);
    opacity: 0.95;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 280px;
  }

  .card-popover-source {
    font-size: 11px;
    color: var(--td-brand-color);
    margin-bottom: 6px;
    display: flex;
    align-items: center;
    gap: 4px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 100%;
  }

  .card-popover-extra {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 10px;
    font-size: 11px;
    color: var(--td-text-color-secondary);
    margin-bottom: 6px;
  }

  .card-popover-created,
  .card-popover-size {
    flex-shrink: 0;
  }

  .card-popover-meta {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 8px;
    font-size: 11px;
    color: var(--td-text-color-secondary);
  }

  .card-popover-channel {
    padding: 1px 6px;
    background: var(--td-warning-color-light);
    color: var(--td-warning-color);
    border-radius: 4px;
  }

  .card-popover-tags {
    display: inline-flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
    max-width: 100%;
  }

  .card-popover-tag-chip {
    max-width: 120px;
    height: 18px;
    line-height: 18px;
    border-radius: 999px;
    border-color: var(--td-component-stroke);
    color: var(--td-text-color-secondary);
    padding: 0 6px;
    background: transparent;

    .tag-text {
      display: inline-block;
      max-width: 80px;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      vertical-align: middle;
      font-size: 11px;
    }
  }

  .card-popover-type {
    padding: 1px 6px;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    border-radius: 4px;
  }

  .card-popover-hint {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--td-component-stroke);
    font-size: 11px;
    color: var(--td-text-color-secondary);
  }
}

.url-import-form {
  padding: 8px 0;

  .url-input-label {
    color: var(--td-text-color-primary);
    font-size: 14px;
    font-weight: 500;
    margin-bottom: 8px;
  }

  .url-input-tip {
    color: var(--td-text-color-secondary);
    font-size: 12px;
    margin-top: 8px;
    line-height: 1.5;
  }
}

.knowledge-card-upload {
  color: var(--td-text-color-primary);
  font-family: var(--app-font-family);
  font-size: 14px;
  font-weight: 400;
  cursor: pointer;

  .btn-upload {
    margin: 33px auto 0;
    width: 112px;
    height: 32px;
    border: 1px solid var(--td-component-border);
    display: flex;
    justify-content: center;
    align-items: center;
    margin-bottom: 24px;
  }

  .svg-icon-download {
    margin-right: 8px;
  }
}

.upload-described {
  color: var(--td-text-color-disabled);
  font-family: var(--app-font-family);
  font-size: 12px;
  font-weight: 400;
  text-align: center;
  display: block;
  width: 188px;
  margin: 0 auto;
}

.del-card {
  vertical-align: middle;
}
</style>
