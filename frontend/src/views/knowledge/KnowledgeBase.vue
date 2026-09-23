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
import { useUIStore } from '@/stores/ui';
import { useOrganizationStore } from '@/stores/organization';
import { useAuthStore } from '@/stores/auth';
import { permissionCanEditKB, permissionCanManageKB } from '@/utils/kbPermission';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useEditorResourcesStore } from '@/stores/editorResources';
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue';
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
  createKnowledgeFromURL,
  reparseKnowledge,
  cancelKnowledgeParse,
  batchDeleteKnowledge,
  batchDownloadKnowledge,
  delKnowledgeDetails,
  batchReparseKnowledge,
  getKnowledgeSpans,
  getKnowledgeDetails,
  listKnowledgeFolders,
  moveKnowledgeToFolder,
  renameKnowledgeFolder,
  downKnowledgeDetails,
  type KnowledgeFolderTree,
} from "@/api/knowledge-base/index";
import { isBatchDownloadableKnowledge } from './knowledgeDownloadFileName';
import { waitForKnowledgeDeletion } from '@/utils/knowledgeDeletion';
import { knowledgeSpansPayloadHasTrace } from '@/utils/knowledgeTrace';
import FAQEntryManager from './components/FAQEntryManager.vue';
import DocumentListView from './components/DocumentListView.vue';
import DocumentCardView from './components/DocumentCardView.vue';
import DocumentBatchBar from './components/DocumentBatchBar.vue';
import KbUploadSourceDropdown from './components/KbUploadSourceDropdown.vue';
import KbFolderTree from './components/KbFolderTree.vue';
import BatchTagDialog from './components/BatchTagDialog.vue';
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess';
import { useUploadConfirmStore, type UploadConfirmResult } from '@/stores/uploadConfirm';
import { useUploadTasksStore } from '@/stores/uploadTasks';
import WikiBrowser from './wiki/WikiBrowser.vue';
import { getWikiStats } from '@/api/wiki';
import {
  isKnowledgeParseInFlight,
  knowledgeNeedsStatusPolling,
  shouldRefreshWikiStatusAfterKnowledgePoll,
} from './wikiStatusRefresh';
import { stalledMinutes, STALLED_POLL_INTERVAL_MS } from '@/utils/knowledgeProcessingStall';
import { listMoveTargets, moveKnowledge, getKnowledgeMoveProgress } from '@/api/knowledge-base';
import { resolveKnowledgeDownloadFileName } from './knowledgeDownloadFileName';
import {
  buildUploadFileName,
  canMoveFolderTo,
  folderBreadcrumbs as buildFolderBreadcrumbs,
  folderPathExists as folderExistsInTree,
  isFilteringDocuments,
  ROOT_FOLDER_PATH,
} from './folderTree';
import { useI18n } from 'vue-i18n';
import { useMarqueeSelect } from '@/hooks/useMarqueeSelect';
import type { ParserEngineInfo } from '@/api/system';
const route = useRoute();
const { t } = useI18n();
const kbId = computed(() => (route.params as any).kbId as string || '');
const kbInfo = ref<any>(null);
const uploadSourceRef = ref<InstanceType<typeof KbUploadSourceDropdown> | null>(null);
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

// Effective permission: from direct org share list or from GET /knowledge-bases/:id (e.g. agent-visible KB).
// Declared first: the canEdit / canManage gates below treat an explicit
// cross-tenant grant as the single source of truth.
const effectiveKBPermission = computed(() => orgStore.getKBPermission(kbId.value) || kbInfo.value?.my_permission || '');

// Can edit: when accessed via an organization share, ONLY the share grant
// counts — even if the current user happens to be the original creator of
// the KB. The backend's RBAC middleware authorizes based on the active
// tenant, not on creator_id, so a creator viewing their own KB from a
// different tenant context will be 403'd on write. Otherwise: KB creator
// (any role) or tenant Admin+ in the home tenant.
//
// A resolved cross-tenant permission (org share or shared-agent visibility,
// already capped server-side by effective = min(share, org_role, tenant_role))
// outranks every local signal: without this priority rule a personal-workspace
// admin browsing a read-only shared KB saw edit entries that only led to 403s
// (#3098).
//
// hasRole('contributor') is intentionally NOT here — being a Contributor
// in a tenant does not by itself grant edit on someone else's KB.
const canEdit = computed(() => {
  const permission = effectiveKBPermission.value;
  if (permission) return permissionCanEditKB(permission);
  if (isViaShare.value) return orgStore.canEditKB(kbId.value, false);
  if (isOwner.value) return true;
  if (authStore.hasRole('admin')) return true;
  return orgStore.canEditKB(kbId.value, false);
});

// Can manage (delete, settings, etc.): same permission-first rule. For
// shared KBs only an 'admin' share grant qualifies — editor/viewer (and
// even being the creator viewed via share) never grant delete/settings.
const canManage = computed(() => {
  const permission = effectiveKBPermission.value;
  if (permission) return permissionCanManageKB(permission);
  if (isViaShare.value) return orgStore.canManageKB(kbId.value, false);
  if (isOwner.value) return true;
  if (authStore.hasRole('admin')) return true;
  return orgStore.canManageKB(kbId.value, false);
});

// The activity feed exposes owner-side actor and configuration summaries.
// It lives in KB settings (KnowledgeBaseEditorModal) for Owner/Admin in the home tenant.

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

// Downloading returns the original source file, which is intentionally more
// restrictive than viewing parsed content or using the preview tab. A tenant
// Viewer can never download; for cross-tenant KBs the effective share
// permission must additionally be Editor or Admin.
const canDownloadKnowledge = computed(() => {
  if (!authStore.hasRole('contributor')) return false;
  const permission = effectiveKBPermission.value;
  return !permission || permission === 'owner' || permission === 'admin' || permission === 'editor';
});

const knowledgeList = ref<Array<{ id: string; name: string; type?: string }>>([]);
let { cardList, total, moreIndex, details, getKnowled, onVisibleChange: _onVisibleChange, getCardDetails, getfDetails } = useKnowledgeBase(kbId.value)

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
    return localStorage.getItem(VIEW_MODE_KEY) === 'grid' ? 'grid' : 'list';
  } catch { return 'list'; }
};
const viewMode = ref<DocViewMode>(initViewMode());
watch(viewMode, (v) => {
  try { localStorage.setItem(VIEW_MODE_KEY, v); } catch { /* ignore */ }
});

// Multi-select state — shared between grid and list views.
// Vue 3.5 tracks Set#add/delete natively, so direct mutation is reactive.
const selectedIds = ref<Set<string>>(new Set());
let lastSelectedIndex = -1;
const batchDeleting = ref(false);
const batchReparsing = ref(false);
const batchTagging = ref(false);
const batchDownloading = ref(false);
let batchDownloadController: AbortController | undefined;

const MAX_BATCH_DOWNLOAD_FILES = 200;
const MAX_BATCH_DOWNLOAD_BYTES = 512 * 1024 * 1024;

const handleBatchDownload = async () => {
  if (batchDownloading.value || batchDeleting.value || batchReparsing.value || batchTagging.value || selectedIds.value.size === 0) return;
  const selected = Array.from(selectedIds.value);
  const itemsById = new Map((cardList.value || []).map((item) => [item.id, item]));
  const ids = selected.filter((id) => isBatchDownloadableKnowledge(itemsById.get(id)));
  const skipped = selected.length - ids.length;
  if (ids.length === 0) {
    MessagePlugin.warning(t('knowledgeBase.batchDownloadNoFiles'));
    return;
  }
  if (ids.length > MAX_BATCH_DOWNLOAD_FILES) {
    MessagePlugin.warning(t('knowledgeBase.batchDownloadHint'));
    return;
  }
  let knownBytes = 0;
  for (const id of ids) {
    const size = Number(itemsById.get(id)?.file_size);
    if (Number.isFinite(size) && size > 0) knownBytes += size;
  }
  if (knownBytes > MAX_BATCH_DOWNLOAD_BYTES) {
    MessagePlugin.warning(t('knowledgeBase.batchDownloadTooLarge'));
    return;
  }
  if (skipped > 0) {
    MessagePlugin.warning(t('knowledgeBase.batchDownloadSkipped', { count: skipped }));
  }
  const controller = new AbortController();
  batchDownloadController = controller;
  batchDownloading.value = true;
  try {
    const blob = await batchDownloadKnowledge(kbId.value, ids, controller.signal);
    if (controller.signal.aborted) return;
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `knowledge-files-${Date.now()}.zip`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    // 延迟释放，给浏览器留出开始保存文件的时间。
    window.setTimeout(() => URL.revokeObjectURL(url), 60000);
    MessagePlugin.success(t('knowledgeBase.batchDownloadStarted'));
  } catch (error: any) {
    if (!controller.signal.aborted) {
      MessagePlugin.error(error?.message || t('knowledgeBase.batchDownloadFailed'));
    }
  } finally {
    if (batchDownloadController === controller) {
      batchDownloading.value = false;
      batchDownloadController = undefined;
    }
  }
};
watch(kbId, () => batchDownloadController?.abort());
onUnmounted(() => batchDownloadController?.abort());
const batchTagDialogVisible = ref(false);
const batchTagPreSelectedIds = computed(() => {
  const ids = Array.from(selectedIds.value);
  if (ids.length === 0) return [];
  const cards = ids
    .map((id) => cardList.value.find((c) => c.id === id))
    .filter((c): c is KnowledgeCard => Boolean(c));
  if (cards.length === 0) return [];
  const firstTagIds = new Set((cards[0].tags || []).map((t) => t.id));
  for (let i = 1; i < cards.length; i++) {
    const cur = new Set((cards[i].tags || []).map((t) => t.id));
    for (const tid of firstTagIds) {
      if (!cur.has(tid)) firstTagIds.delete(tid);
    }
  }
  return Array.from(firstTagIds);
});
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

const confirmBatchReparse = async () => {
  if (batchReparsing.value || batchDeleting.value || batchDownloading.value || selectedIds.value.size === 0) return;
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
    const res: any = await batchReparseKnowledge(kbId.value, ids);
    if (res?.success) {
      MessagePlugin.success(t('knowledgeBase.batchReparseSuccess', { count: ids.length }));
      applyOptimisticBatchReparse(ids);
      clearSelection();
      batchMode.value = false;
      scheduleWikiStatusProbes();
      void awaitBatchReparseReflection(ids);
    } else {
      MessagePlugin.error(res?.message || t('knowledgeBase.batchReparseFailed'));
    }
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.batchReparseFailed'));
  } finally {
    batchReparsing.value = false;
  }
};


const selectedTagIds = ref<string[]>([]);
const selectedTagNames = ref<Record<string, string>>({});
const tagList = ref<any[]>([]);
// Keep selected tags reachable even when the search or loaded page changes.
const filterTagOptions = computed(() => [
  ...tagList.value,
  ...selectedTagIds.value.filter(id => !tagList.value.some(tag => tag.id === id))
    .map(id => ({ id, name: selectedTagNames.value[id] || t('knowledgeBase.columnTag') })),
]);
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
  { label: t('knowledgeBase.channelFeishuDrive'), value: 'feishu_drive' },
  { label: t('knowledgeBase.channelNotion'), value: 'notion' },
  { label: t('knowledgeBase.channelYuque'), value: 'yuque' },
  { label: t('knowledgeBase.channelConfluence'), value: 'confluence' },
  { label: t('knowledgeBase.channelGitLab'), value: 'gitlab' },
  { label: t('knowledgeBase.channelIma'), value: 'ima' },
  { label: t('knowledgeBase.channelWechat'), value: 'wechat' },
  { label: t('knowledgeBase.channelWecom'), value: 'wecom' },
  { label: t('knowledgeBase.channelDingtalk'), value: 'dingtalk' },
  { label: t('knowledgeBase.channelSlack'), value: 'slack' },
  { label: t('knowledgeBase.channelIm'), value: 'im' },
]);
// Date range as [start, end] in "YYYY-MM-DD" form (t-date-range-picker default).
const updatedTimeRange = ref<string[]>([]);
const filtersExpanded = ref(false);
const activeFilterCount = computed(() =>
  [selectedFileType.value, selectedParseStatus.value, selectedSource.value,
    updatedTimeRange.value?.some(Boolean)].filter(Boolean).length + selectedTagIds.value.length,
);
const clearDocumentFilters = () => {
  selectedFileType.value = '';
  selectedParseStatus.value = '';
  selectedSource.value = '';
  updatedTimeRange.value = [];
  handleTagFilterChange([]);
};
// Disable any date after today so users cannot filter into the future.
const disableFutureDate = { after: new Date(new Date().setHours(23, 59, 59, 999)) };

// ── Folder tree (documents uploaded as a folder keep their relative path) ──
// The directory sidebar is now the sole folder navigation and starts expanded.
const FOLDER_TREE_COLLAPSED_KEY = 'weknora.kbDirectorySidebarCollapsed';
const readStoredFlag = (key: string, fallback = false) => {
  try {
    const raw = localStorage.getItem(key);
    return raw === null ? fallback : raw === 'true';
  } catch {
    return fallback;
  }
};
const writeStoredFlag = (key: string, value: boolean) => {
  try {
    localStorage.setItem(key, String(value));
  } catch {
    // Private-mode storage failures must not break navigation.
  }
};
const folderTree = ref<KnowledgeFolderTree | null>(null);
const folderTreeLoading = ref(false);
// The folder being browsed; ROOT_FOLDER_PATH ('') is the knowledge base top
// level, a real node of the tree rather than a separate mode.
const selectedFolderPath = ref<string>(ROOT_FOLDER_PATH);
const folderTreeCollapsed = ref(readStoredFlag(FOLDER_TREE_COLLAPSED_KEY));
const hasFolders = computed(() => (folderTree.value?.folders?.length ?? 0) > 0);
// The folder column only earns its space once the knowledge base actually has
// folders, so knowledge bases filled with single-file uploads look unchanged.
const showFolderTree = computed(() => !isFAQ.value && hasFolders.value);
// Browsing lists one folder's own contents; filtering searches its whole
// subtree. There is no mode switch: the list follows what the user is doing.
const isFiltering = computed(() =>
  isFilteringDocuments({
    keyword: docSearchKeyword.value,
    tagIds: selectedTagIds.value,
    fileType: selectedFileType.value,
    parseStatus: selectedParseStatus.value,
    source: selectedSource.value,
    timeRange: updatedTimeRange.value,
  }),
);
// A row's folder is worth showing only when the list can span folders.
const showDocumentFolderPath = computed(() => hasFolders.value && isFiltering.value);
const folderBreadcrumbs = computed(() => buildFolderBreadcrumbs(selectedFolderPath.value));

const filterParams = computed(() => {
  const [start, end] = updatedTimeRange.value || [];
  return {
    tag_ids: selectedTagIds.value.length > 0 ? selectedTagIds.value.join(',') : undefined,
    keyword: docSearchKeyword.value ? docSearchKeyword.value.trim() : undefined,
    file_type: selectedFileType.value || undefined,
    parse_status: selectedParseStatus.value || undefined,
    source: selectedSource.value || undefined,
    start_time: start ? `${start} 00:00:00` : undefined,
    end_time: end ? `${end} 23:59:59` : undefined,
    folder_path: selectedFolderPath.value,
    // Filtering searches descendants; browsing shows this folder's documents.
    folder_recursive: isFiltering.value,
  };
});
const tagMap = computed<Record<string, any>>(() => {
  const map: Record<string, any> = {};
  tagList.value.forEach((tag) => {
    map[tag.id] = tag;
  });
  return map;
});

const getPageSize = () => {
  const viewportHeight = window.innerHeight || document.documentElement.clientHeight;
  const itemHeight = 148;
  let itemsInView = Math.floor(viewportHeight / itemHeight) * 5;
  pageSize = Math.max(35, itemsInView);
}
getPageSize()

const loadKnowledgeFiles = (kbIdValue: string): Promise<void> => {
  if (!kbIdValue) return Promise.resolve();
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
    if (isCurrentKb(kbIdValue) && !isFAQ.value) {
      docListLoading.value = false;
    }
  });
};

const isCurrentKb = (targetKbId: string) => targetKbId === kbId.value;

const loadFolderTree = async (kbIdValue: string) => {
  if (!kbIdValue || isFAQ.value) {
    folderTree.value = null;
    return;
  }
  folderTreeLoading.value = true;
  try {
    const res: any = await listKnowledgeFolders(kbIdValue);
    if (!isCurrentKb(kbIdValue)) return;
    folderTree.value = (res?.data as KnowledgeFolderTree) || null;
    // A folder can disappear (its last document was deleted or moved); fall
    // back to the root instead of leaving an empty, unreachable view.
    if (!folderExistsInTree(folderTree.value?.folders || [], selectedFolderPath.value)) {
      selectedFolderPath.value = ROOT_FOLDER_PATH;
    }
  } catch (error) {
    if (!isCurrentKb(kbIdValue)) return;
    console.error('Failed to load knowledge folders', error);
    folderTree.value = null;
  } finally {
    if (isCurrentKb(kbIdValue)) {
      folderTreeLoading.value = false;
    }
  }
};

const handleFolderSelect = (path: string) => {
  if (selectedFolderPath.value === path) return;
  selectedFolderPath.value = path;
};

// ── Re-filing documents and renaming folders ──
// folder_path is display-only, so both operations are a plain column update:
// nothing is re-parsed, re-chunked or re-embedded.

// Flat folder list shared by every "move to folder" picker.
const folderOptions = computed(() => {
  const result: Array<{ path: string; name: string; depth: number }> = [];
  const walk = (nodes: KnowledgeFolderTree['folders'], depth: number) => {
    nodes.forEach((node) => {
      result.push({ path: node.path, name: node.name, depth });
      walk(node.children || [], depth + 1);
    });
  };
  walk(folderTree.value?.folders || [], 0);
  return result;
});

const moveKnowledgeIntoFolder = async (ids: string[], folderPath: string) => {
  if (!kbId.value || ids.length === 0) return;
  try {
    await moveKnowledgeToFolder(kbId.value, ids, folderPath);
    MessagePlugin.success(t('knowledgeBase.moveToFolder.success', { count: ids.length }));
    clearSelection();
    batchMode.value = false;
    resetPage();
    await loadKnowledgeFiles(kbId.value);
    await loadFolderTree(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.moveToFolder.failed'));
  }
};

const handleFolderRename = async ({ from, to }: { from: string; to: string }) => {
  if (!kbId.value || !to || from === to) return;
  if (!canMoveFolderTo(from, to)) {
    MessagePlugin.warning(t('knowledgeBase.folderTree.renameInvalid'));
    return;
  }
  try {
    const res: any = await renameKnowledgeFolder(kbId.value, from, to);
    const movedCount = res?.data?.moved_count ?? 0;
    if (movedCount === 0) {
      MessagePlugin.warning(t('knowledgeBase.folderTree.renameFailed'));
      await loadFolderTree(kbId.value);
      return;
    }
    MessagePlugin.success(t('knowledgeBase.folderTree.renameSuccess'));
    // Follow the folder to its new path so the user stays where they were.
    if (selectedFolderPath.value === from) {
      selectedFolderPath.value = to;
    } else if (selectedFolderPath.value.startsWith(`${from}/`)) {
      selectedFolderPath.value = to + selectedFolderPath.value.slice(from.length);
    }
    resetPage();
    await loadKnowledgeFiles(kbId.value);
    await loadFolderTree(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.folderTree.renameFailed'));
  }
};

const handleFolderTreeCollapsedChange = (value: boolean) => {
  folderTreeCollapsed.value = value;
  writeStoredFlag(FOLDER_TREE_COLLAPSED_KEY, value);
};

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
  selectedTagNames.value = Object.fromEntries(tagIds.map(id => [
    id, tagMap.value[id]?.name || selectedTagNames.value[id] || t('knowledgeBase.columnTag'),
  ]));
  selectedTagIds.value = tagIds;
  // 同步更新 store 中的 selectedTagIds，供 menu.vue 上传时使用
  uiStore.clearSelectedTagIds();
  tagIds.forEach(id => uiStore.toggleSelectedTagId(id));
  resetPage();
};

const onTagCatalogChanged = (payload?: { deletedTagId?: string }) => {
  void loadTags(kbId.value, true);
  if (payload?.deletedTagId) {
    handleTagFilterChange(selectedTagIds.value.filter(id => id !== payload.deletedTagId));
  }
  resetPage();
  void loadKnowledgeFiles(kbId.value);
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
    selectedTagNames.value = {};
    uiStore.clearSelectedTagIds();
    // 重置store中的标签选择状态，避免上传文档时自动带上之前选择的标签
    uiStore.clearSelectedTagIds();
    if (!isFAQ.value) {
      loadKnowledgeFiles(targetKbId);
      void loadFolderTree(targetKbId);
    } else {
      cardList.value = [];
      total.value = 0;
      folderTree.value = null;
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
    folderTree.value = null;
    selectedFolderPath.value = ROOT_FOLDER_PATH;
  }
  loadKnowledgeBaseInfo(newKbId);
}, { immediate: true });

watch(selectedTagIds, (newVal, oldVal) => {
  if (oldVal === undefined) return;
  if (kbId.value) {
    loadKnowledgeFiles(kbId.value);
  }
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

// 监听文档搜索关键词变化
watch(docSearchKeyword, (newVal, oldVal) => {
  if (newVal === oldVal) return;
  if (docSearchDebounce) {
    window.clearTimeout(docSearchDebounce);
  }
  docSearchDebounce = window.setTimeout(() => {
    if (kbId.value) {
      resetPage();
      loadKnowledgeFiles(kbId.value);
    }
  }, 300);
});

// 监听文件类型筛选变化
watch(selectedFileType, (newVal, oldVal) => {
  if (newVal === oldVal) return;
  if (kbId.value) {
    resetPage();
    loadKnowledgeFiles(kbId.value);
  }
});

// 监听解析状态/来源/更新时间范围筛选变化（与文件类型行为一致）
watch([selectedParseStatus, selectedSource, updatedTimeRange], () => {
  if (kbId.value) {
    resetPage();
    loadKnowledgeFiles(kbId.value);
  }
}, { deep: true });

// 切换目录只改变列表范围，行为与其他筛选一致。浏览态与筛选态之间的切换由各筛选项
// 自身的 watcher 触发刷新，这里不重复请求。
watch(selectedFolderPath, () => {
  if (!kbId.value || isFAQ.value) return;
  clearSelection();
  resetPage();
  loadKnowledgeFiles(kbId.value);
});

// 监听文件上传事件
const handleFileUploaded = (event: CustomEvent) => {
  const uploadedKbId = event.detail.kbId;
  console.log('接收到文件上传事件，上传的知识库ID:', uploadedKbId, '当前知识库ID:', kbId.value);
  if (uploadedKbId && uploadedKbId === kbId.value && !isFAQ.value) {
    // Mid-batch refreshes (settled === false) only bring new rows and folders
    // in, and are skipped once the user scrolled past the first page because
    // reloading resets the list to it. The batch's final event refreshes fully.
    const midBatch = event.detail.settled === false;
    if (midBatch && page > 1) return;
    console.log('匹配当前知识库，开始刷新文件列表');
    // 如果上传的文件属于当前知识库，使用 loadKnowledgeFiles 刷新文件列表
    resetPage(); // Reset page counter when reloading files after upload
    loadKnowledgeFiles(uploadedKbId);
    void loadFolderTree(uploadedKbId);
    if (midBatch) return;
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

// Global file drops are captured by the platform shell. Route them back into
// the same confirmation flow as the page upload button so tags and per-batch
// processing settings are never skipped.
const handleKnowledgeFileDrop = (event: CustomEvent) => {
  const eventKbId = event.detail?.kbId;
  const files = Array.isArray(event.detail?.files) ? event.detail.files : [];
  if (eventKbId !== kbId.value || isFAQ.value || files.length === 0) return;
  handleUploadSourceFiles(files);
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

let autoOpenRequest = 0;

const tryAutoOpenDocument = async () => {
  if (!pendingKnowledgeId.value) return;
  const targetId = pendingKnowledgeId.value;
  pendingKnowledgeId.value = null;
  const request = ++autoOpenRequest;
  const card = cardList.value.find((c: KnowledgeCard) => c.id === targetId);

  // The current card list only contains the folder being browsed. A document
  // opened from chat references may live in any nested folder, so resolve its
  // folder from the detail endpoint before opening the drawer. Otherwise the
  // drawer opens correctly while the page misleadingly remains at KB root.
  let target = card || ({ id: targetId } as KnowledgeCard);
  try {
    const response: any = await getKnowledgeDetails(targetId);
    if (request !== autoOpenRequest) return;
    const detail = response?.data || response;
    if (detail && typeof detail === 'object') {
      target = { ...target, ...detail, id: targetId } as KnowledgeCard;
      selectedFolderPath.value = detail.folder_path || ROOT_FOLDER_PATH;
    }
  } catch (error) {
    // Keep the previous ID-only fallback: getCardDetails will surface the
    // normal detail loading error, while links to root-level files still work.
    console.error('Failed to resolve referenced document folder', error);
  }

  if (request !== autoOpenRequest) return;
  await nextTick();
  openCardDetails(target);
};

// React to later ?knowledge_id= changes on the same KB route (no remount).
watch(
  () => route.query.knowledge_id,
  (newId) => {
    if (typeof newId !== 'string' || !newId) return;
    pendingKnowledgeId.value = newId;
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
  window.addEventListener('weknora:knowledge-file-drop', handleKnowledgeFileDrop as EventListener);
  window.addEventListener('weknora:open-knowledge', handleOpenKnowledgeEvent as EventListener);
});

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  window.removeEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
  window.removeEventListener('weknora:knowledge-file-drop', handleKnowledgeFileDrop as EventListener);
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

  // Auto-open document if navigated with ?knowledge_id=xxx.
  if (pendingKnowledgeId.value) {
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
    // The deep watch refires as stalled_minutes ticks; keep the backoff.
    updateStatus(analyzeList, pollDelayFor(analyzeList))
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
  last_activity_at?: string;
  // Minutes without progress while in flight; 0 unless it looks stuck.
  stalled_minutes?: number;
  // Server verdict on a quiet row: 'queued' (backlogged) or 'stalled'.
  stall_state?: string;
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

// Back off once every in-flight row looks stuck: nothing is moving, and a
// page left open on it should not keep hammering the batch endpoint.
const pollDelayFor = (items: KnowledgeCard[]) =>
  items.length > 0 && items.every(item => (item.stalled_minutes ?? 0) > 0)
    ? STALLED_POLL_INTERVAL_MS
    : 1500;

const updateStatus = (analyzeList: KnowledgeCard[], delay = 1500) => {
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

          const card = cardList.value[index];
          card.last_activity_at = item.last_activity_at;
          card.stall_state = item.stall_state;
          card.stalled_minutes = stalledMinutes({ parse_status: parseStatus, last_activity_at: item.last_activity_at });

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
        updateStatus(stillPending, pollDelayFor(stillPending));
      }
    }).catch((_err) => {
      // 错误处理
      const stillPending = cardList.value.filter(needsStatusPolling);
      if (stillPending.length > 0) {
        updateStatus(stillPending, pollDelayFor(stillPending));
      }
    });
  }, delay);
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
  void deleteKnowledgeDocuments([item.id], () => delKnowledgeDetails(item.id), false);
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
      void loadFolderTree(kbId.value);
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
        void loadFolderTree(kbId.value);
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
    void loadFolderTree(savedKbId);
  }
};

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

const uploadConfirmStore = useUploadConfirmStore();

const uploadTasksStore = useUploadTasksStore();

// Hands the batch to the global upload queue, which runs the transfers,
// reports progress in its floating panel and refreshes this page as files land.
const enqueueUploads = (
  files: File[],
  options: {
    processConfig?: KnowledgeProcessOverrides;
    tagIds?: string[];
    /** Destination folder confirmed in the upload dialog; '' is the root. */
    targetFolder?: string;
  } = {},
) => {
  const targetKbId = kbId.value;
  if (!targetKbId || files.length === 0) return;
  const targetFolder = options.targetFolder || ROOT_FOLDER_PATH;
  uploadTasksStore.enqueue({
    kbId: targetKbId,
    kbName: kbInfo.value?.name || '',
    targetFolder,
    tagIds: options.tagIds,
    processConfig: options.processConfig,
    uploads: files.map(file => ({ file, fileName: buildUploadFileName(file, targetFolder) })),
  });
};

const executeUrlImport = async (
  url: string,
  processConfig?: KnowledgeProcessOverrides,
  tagIds?: string[],
) => {
  const targetKbId = kbId.value;
  if (!targetKbId) {
    MessagePlugin.error(t('error.missingKbId'));
    return;
  }

  const tagIdsToUpload = tagIds && tagIds.length > 0 ? [...tagIds] : undefined;
  try {
    const responseData: any = await createKnowledgeFromURL(targetKbId, {
      url,
      tag_ids: tagIdsToUpload,
      process_config: processConfig,
    });
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: targetKbId },
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

const handleUploadConfirmResult = async (result: UploadConfirmResult) => {
  if (result.mode === 'manual') {
    return;
  }

  const files = result.files || [];
  const urls = result.urls || [];
  const processConfig = result.processConfig;
  const tagIds = result.tagIds || [];

  if (files.length > 0) {
    enqueueUploads(files, {
      processConfig,
      tagIds,
      targetFolder: result.targetFolder || ROOT_FOLDER_PATH,
    });
  }

  for (const url of urls) {
    await executeUrlImport(url, processConfig, tagIds);
  }
};

const openUploadConfirmDialog = async (files: File[], urls: string[] = []) => {
  if (!kbInfo.value) return;
  if (files.length === 0 && urls.length === 0) return;
  try {
    const result = await uploadConfirmStore.open({
      mode: 'file',
      kbInfo: kbInfo.value,
      tagIds: [...selectedTagIds.value],
      files,
      urls,
      acceptFileTypes: acceptFileTypes.value,
      supportedFileTypes: [...supportedFileTypes.value],
      // Pre-fill the destination with the folder being browsed; the dialog shows
      // it and lets the user pick another folder (or the root) before confirming.
      targetFolder: selectedFolderPath.value,
      folderOptions: folderOptions.value,
    });
    await handleUploadConfirmResult(result);
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

const syncDocumentSummaryState = (state: { id?: string; summary_status?: string; description?: string }) => {
  if (!state?.id) return;
  const card = cardList.value.find((item: KnowledgeCard) => item.id === state.id);
  if (!card) return;
  if (typeof state.summary_status === 'string' && state.summary_status) {
    card.summary_status = state.summary_status;
  }
  if (typeof state.description === 'string') {
    card.description = state.description;
  }
};

const toggleSelectRow = (id: string, checked: boolean, shiftKey?: boolean) => {
  const items = cardList.value || [];
  const idx = items.findIndex((i: KnowledgeCard) => i.id === id);
  if (shiftKey && lastSelectedIndex >= 0 && idx >= 0) {
    const [s, e] = idx < lastSelectedIndex
      ? [idx, lastSelectedIndex]
      : [lastSelectedIndex, idx];
    for (let i = s; i <= e; i++) {
      if (checked) selectedIds.value.add(items[i].id);
      else selectedIds.value.delete(items[i].id);
    }
  } else {
    if (checked) selectedIds.value.add(id);
    else selectedIds.value.delete(id);
  }
  lastSelectedIndex = idx;
};

const onCardGridCheckboxChange = (id: string, checked: boolean, ctx?: { e?: Event }) => {
  const me = ctx?.e as MouseEvent | undefined;
  toggleSelectRow(id, checked, !!me?.shiftKey);
};

const toggleSelectAll = (checked: boolean) => {
  if (checked) {
    for (const item of cardList.value || []) selectedIds.value.add(item.id);
  } else {
    for (const item of cardList.value || []) selectedIds.value.delete(item.id);
  }
};

const clearSelection = () => {
  selectedIds.value.clear();
  lastSelectedIndex = -1;
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
watch(viewMode, (mode) => {
  if (mode === 'grid' && selectedIds.value.size > 0) {
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
const {
  onContainerMouseDown: onDocMarqueeMouseDown,
  marqueeVisible: docMarqueeVisible,
  marqueeMode: docMarqueeMode,
  boxStyle: docMarqueeBoxStyle,
  shouldSuppressClick: shouldSuppressDocClick,
} = useMarqueeSelect({
  containerRef: knowledgeScroll,
  itemSelector: '.knowledge-card[data-select-id], .doc-list-row[data-select-id]',
  selectedIds,
  getItemId: (el) => el.dataset.selectId || null,
  enabled: computed(() => (canEdit.value || canDownloadKnowledge.value) && !isFAQ.value && cardList.value.length > 0),
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

// Stop observing a previous KB when navigating, including away and back.
let deleteGeneration = 0;
watch(kbId, () => {
  deleteGeneration++;
  batchDeleting.value = false;
});
onUnmounted(() => { deleteGeneration++; });

const deleteKnowledgeDocuments = async (
  ids: string[],
  submit: () => Promise<any>,
  batch: boolean,
) => {
  if (batchDeleting.value || batchReparsing.value || batchDownloading.value || ids.length === 0) return;
  const targetKbId = kbId.value;
  const generation = ++deleteGeneration;
  const isActive = () => generation === deleteGeneration && isCurrentKb(targetKbId);
  batchDeleting.value = true;
  let submitted = false;
  try {
    const res = await submit();
    if (!isActive()) return;
    if (!res?.success) {
      MessagePlugin.error(res?.message || t('knowledgeBase.batchDeleteFailed'));
      return;
    }
    submitted = true;
    MessagePlugin.info(t('knowledgeBase.deleteSubmitted'));
    if (batch) {
      clearSelection();
      batchMode.value = false;
    }
    const result = await waitForKnowledgeDeletion(
      ids,
      async (queryIds) => {
        const query = new URLSearchParams();
        queryIds.forEach(id => query.append('ids', id));
        return await batchQueryKnowledge(query.toString(), targetKbId) as any;
      },
      { isActive },
    );
    if (!isActive() || result === 'cancelled') return;
    if (result === 'completed') {
      MessagePlugin.success(batch
        ? t('knowledgeBase.batchDeleteSuccess', { count: ids.length })
        : t('knowledgeBase.deleteSuccess'));
    } else if (result === 'failed') {
      MessagePlugin.error(t('knowledgeBase.deleteTaskFailed'));
    } else {
      MessagePlugin.info(t('knowledgeBase.deletePending'));
    }
  } catch (e: any) {
    if (!isActive()) return;
    if (submitted) {
      MessagePlugin.warning(t('knowledgeBase.deleteStatusUnavailable'));
    } else {
      MessagePlugin.error(e?.message || t('knowledgeBase.batchDeleteFailed'));
    }
  } finally {
    if (isActive()) {
      batchDeleting.value = false;
      if (submitted) {
        resetPage();
        await loadKnowledgeFiles(targetKbId);
        if (isActive()) {
          void loadTags(targetKbId, true);
          void loadFolderTree(targetKbId);
        }
      }
    }
  }
};

const confirmBatchDelete = () => {
  const targetKbId = kbId.value;
  const ids = Array.from(selectedIds.value);
  return deleteKnowledgeDocuments(ids, () => batchDeleteKnowledge(targetKbId, ids), true);
};

const handleBatchTag = () => {
  if (batchDeleting.value || batchReparsing.value || batchTagging.value || batchDownloading.value || selectedIds.value.size === 0) return;
  batchTagDialogVisible.value = true;
};

const onBatchTagConfirm = async (tagIds: string[]) => {
  if (batchTagging.value || selectedIds.value.size === 0) return;
  const ids = Array.from(selectedIds.value);
  const updateMap: Record<string, string[]> = {};
  for (const id of ids) {
    updateMap[id] = tagIds;
  }
  batchTagging.value = true;
  try {
    await updateKnowledgeTagBatch({ updates: updateMap });
    MessagePlugin.success(t('knowledgeBase.batchTagSuccess', { count: ids.length }));
    batchTagDialogVisible.value = false;
    clearSelection();
    batchMode.value = false;
    resetPage();
    loadKnowledgeFiles(kbId.value);
    loadTags(kbId.value, true);
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('knowledgeBase.batchTagFailed'));
  } finally {
    batchTagging.value = false;
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

const downloadKnowledge = async (item: KnowledgeCard) => {
  if (!item?.id) return;
  try {
    const file = await downKnowledgeDetails(item.id);
    const objectUrl = URL.createObjectURL(file);
    const link = document.createElement('a');
    const fileName = resolveKnowledgeDownloadFileName(item);
    link.style.display = 'none';
    link.href = objectUrl;
    link.download = fileName;
    document.body.appendChild(link);
    link.click();
    nextTick(() => {
      link.remove();
      URL.revokeObjectURL(objectUrl);
    });
  } catch {
    MessagePlugin.error(t('file.downloadFailed'));
  }
};

// Bridge card-view actions back to existing per-card handlers.
const handleCardAction = (
  action: 'download' | 'edit' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'delete' | 'view-trace' | 'batch-manage',
  item: KnowledgeCard,
) => {
  const idx = (cardList.value || []).findIndex((i: KnowledgeCard) => i.id === item.id);
  if (action === 'download') return downloadKnowledge(item);
  if (action === 'edit') return handleManualEdit(idx, item);
  if (action === 'reparse') {
    if (isParseInFlight(item.parse_status)) return onReparseMenuClick(idx, item);
    return confirmRebuildKnowledge(idx, item);
  }
  if (action === 'cancel-parse') return confirmCancelParseKnowledge(item);
  if (action === 'move') return handleMoveKnowledge(item);
  if (action === 'delete') return confirmDeleteKnowledge(idx, item);
  if (action === 'view-trace') return handleViewTrace(idx, item);
  if (action === 'batch-manage') return handleEnterBatchFromCard(item);
};

// Bridge list-view actions back to existing per-card handlers.
const handleListAction = (
  action: 'download' | 'edit' | 'reparse' | 'cancel-parse' | 'move' | 'move-folder' | 'delete' | 'view-trace' | 'batch-manage',
  item: KnowledgeCard,
) => {
  const idx = (cardList.value || []).findIndex((i: KnowledgeCard) => i.id === item.id);
  if (action === 'download') return downloadKnowledge(item);
  if (action === 'edit') return handleManualEdit(idx, item);
  if (action === 'reparse') return confirmRebuildKnowledge(idx, item);
  if (action === 'cancel-parse') return confirmCancelParseKnowledge(item);
  if (action === 'move') return handleMoveKnowledge(item);
  if (action === 'delete') return confirmDeleteKnowledge(idx, item);
  if (action === 'view-trace') return handleViewTrace(idx, item);
  if (action === 'batch-manage') return handleEnterBatchFromCard(item);
};

// Clear selection on filter/tag/kb change to avoid acting on hidden items.
watch(
  [selectedTagIds, docSearchKeyword, selectedFileType, selectedParseStatus, selectedSource, updatedTimeRange, kbId],
  () => {
    clearSelection();
  },
);

// After cardList reloads: stable keys rely on correct indices for shift-range; clamp anchor index.
watch(cardList, () => {
  const items = cardList.value || [];
  const n = items.length;
  if (lastSelectedIndex >= n) {
    lastSelectedIndex = n > 0 ? n - 1 : -1;
  }
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
                <button type="button" class="kb-settings-button" :aria-label="$t('knowledgeBase.settings')" :disabled="!kbId" @click="handleOpenKBSettings">
                  <t-icon name="setting" size="16px" />
                </button>
              </t-tooltip>
            </div>
          </div>
          <p v-if="kbInfo?.description" class="document-subtitle">{{ kbInfo.description }}</p>
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
          <KbFolderTree v-if="showFolderTree && !folderTreeCollapsed" :tree="folderTree" :selected-path="selectedFolderPath"
            :loading="folderTreeLoading" :can-edit="canEdit" :root-label="kbInfo?.name"
            @select="handleFolderSelect" @update:collapsed="handleFolderTreeCollapsedChange"
            @rename="handleFolderRename" />
          <div class="tag-content">
            <div class="doc-card-area">
              <div class="doc-filter-bar">
                <nav class="doc-folder-path" :aria-label="$t('knowledgeBase.folderTree.title')">
                  <button v-if="showFolderTree && folderTreeCollapsed" type="button" class="doc-folder-path__tree-toggle"
                    :aria-expanded="false" :title="$t('knowledgeBase.folderTree.expand')"
                    :aria-label="$t('knowledgeBase.folderTree.expand')"
                    @click="handleFolderTreeCollapsedChange(false)">
                    <t-icon name="view-list" size="16px" />
                  </button>
                  <button v-if="folderBreadcrumbs.length" type="button" class="doc-folder-path__crumb" :title="kbInfo?.name" @click="handleFolderSelect('')">
                    {{ kbInfo?.name }}
                  </button>
                  <span v-else class="doc-folder-path__crumb is-current" :title="kbInfo?.name" aria-current="page">{{ kbInfo?.name }}</span>
                  <template v-for="(crumb, index) in folderBreadcrumbs" :key="crumb.path">
                    <t-icon name="chevron-right" class="doc-folder-path__sep" />
                    <span v-if="index === folderBreadcrumbs.length - 1" class="doc-folder-path__crumb is-current" :title="crumb.name" aria-current="page">{{ crumb.name }}</span>
                    <button v-else type="button" class="doc-folder-path__crumb" :title="crumb.name" @click="handleFolderSelect(crumb.path)">{{ crumb.name }}</button>
                  </template>
                  <span v-if="!docListLoading" class="doc-folder-path__count">{{ $t(isFiltering ? 'knowledgeBase.folderTree.filteredCount' : 'knowledgeBase.documentCount', { count: total }) }}</span>
                  <t-tooltip v-if="showFolderTree && isFiltering" :content="$t('knowledgeBase.folderTree.searchingSubtree')">
                    <t-icon name="info-circle" class="doc-folder-path__sep" />
                  </t-tooltip>
                </nav>
                <div class="doc-filter-bar__trailing">
                  <t-input v-model.trim="docSearchKeyword" :placeholder="$t('knowledgeBase.docSearchPlaceholder')"
                    :aria-label="$t('knowledgeBase.docSearchPlaceholder')" clearable class="doc-search-input" @clear="loadKnowledgeFiles(kbId)"
                    @enter="loadKnowledgeFiles(kbId)">
                    <template #prefix-icon>
                      <t-icon name="search" size="16px" />
                    </template>
                  </t-input>
                  <t-popup v-model:visible="filtersExpanded" trigger="click" placement="bottom-right"
                    overlay-class-name="document-filter-popup" :overlay-inner-style="{ padding: 0 }">
                    <button type="button" class="doc-filter-toggle" :class="{ active: filtersExpanded || activeFilterCount > 0 }"
                      :aria-expanded="filtersExpanded" aria-controls="document-filters">
                      <t-icon name="filter" size="16px" />
                      {{ $t('knowledgeBase.filters') }}
                      <span v-if="activeFilterCount" class="doc-filter-count">{{ activeFilterCount }}</span>
                    </button>
                    <template #content>
                      <section id="document-filters" class="doc-filter-panel" :aria-label="$t('knowledgeBase.filters')">
                        <header class="doc-filter-panel__header">
                          <strong>{{ $t('knowledgeBase.filters') }}</strong>
                          <button type="button" :disabled="!activeFilterCount" @click="clearDocumentFilters">{{ $t('knowledgeBase.clearFilters') }}</button>
                        </header>
                        <div class="doc-filter-panel__fields">
                          <div class="doc-filter-field">
                            <span>{{ $t('knowledgeBase.fileTypeFilter') }}</span>
                            <t-select v-model="selectedFileType" :options="fileTypeOptions" :placeholder="$t('knowledgeBase.fileTypeFilter')" clearable />
                          </div>
                          <div class="doc-filter-field">
                            <span>{{ $t('knowledgeBase.parseStatusFilter') }}</span>
                            <t-select v-model="selectedParseStatus" :options="parseStatusOptions" :placeholder="$t('knowledgeBase.parseStatusFilter')" clearable />
                          </div>
                          <div class="doc-filter-field">
                            <span>{{ $t('knowledgeBase.sourceFilter') }}</span>
                            <t-select v-model="selectedSource" :options="sourceOptions" :placeholder="$t('knowledgeBase.sourceFilter')" clearable />
                          </div>
                          <div class="doc-filter-field">
                            <span>{{ $t('knowledgeBase.columnUpdatedAt') }}</span>
                            <t-date-range-picker v-model="updatedTimeRange"
                              :placeholder="[$t('knowledgeBase.updatedTimeFrom'), $t('knowledgeBase.updatedTimeTo')]"
                              :disable-date="disableFutureDate" clearable allow-input />
                          </div>
                        </div>
                        <div class="doc-filter-tags">
                          <div class="doc-filter-tags__heading">
                            <span>{{ $t('knowledgeBase.columnTag') }}<span v-if="selectedTagIds.length" class="doc-filter-tags__count">{{ selectedTagIds.length }}</span></span>
                          </div>
                          <t-input v-model.trim="tagSearchQuery" :placeholder="$t('knowledgeBase.tagSearchPlaceholder')" clearable>
                            <template #prefix-icon><t-icon name="search" size="14px" /></template>
                          </t-input>
                          <div class="doc-filter-tags__list">
                            <t-loading v-if="tagLoading && !tagList.length" size="small" />
                            <t-checkbox v-for="tag in filterTagOptions" :key="tag.id" class="doc-filter-tag" :title="tag.name"
                              :checked="selectedTagIds.includes(tag.id)"
                              @change="(checked: boolean) => handleTagFilterChange(checked ? [...selectedTagIds, tag.id] : selectedTagIds.filter(id => id !== tag.id))">
                              <span>{{ tag.name }}</span>
                            </t-checkbox>
                            <span v-if="!tagLoading && !filterTagOptions.length" class="doc-filter-tags__empty">{{ $t(tagSearchQuery ? 'knowledgeBase.tagEmptyResult' : 'knowledgeBase.noTags') }}</span>
                          </div>
                          <t-button v-if="tagHasMore" variant="text" size="small" :loading="tagLoadingMore" @click="kbId && loadTags(kbId)">{{ $t('tenant.loadMore') }}</t-button>
                        </div>
                      </section>
                    </template>
                  </t-popup>
                  <button v-if="viewMode === 'grid' && (canDownloadKnowledge || canMutateKnowledge) && cardList.length"
                    type="button" class="doc-filter-toggle doc-batch-toggle" :class="{ active: batchMode }" :aria-pressed="batchMode"
                    :disabled="batchDeleting || batchReparsing || batchTagging || batchDownloading"
                    @click="toggleBatchMode">
                    <t-icon :name="batchMode ? 'close' : 'check-rectangle'" size="16px" />
                    {{ $t(batchMode ? 'common.cancel' : 'menu.batchManage') }}
                  </button>
                  <div class="doc-view-toggle" role="group" :aria-label="$t('knowledgeBase.viewModeToggle')">
                    <t-tooltip :content="$t('knowledgeBase.viewModeGrid')" placement="top">
                      <button type="button" class="doc-view-toggle-btn" :class="{ active: viewMode === 'grid' }"
                        :aria-label="$t('knowledgeBase.viewModeGrid')" @click="viewMode = 'grid'" :aria-pressed="viewMode === 'grid'">
                        <t-icon name="view-module" size="16px" />
                      </button>
                    </t-tooltip>
                    <t-tooltip :content="$t('knowledgeBase.viewModeList')" placement="top">
                      <button type="button" class="doc-view-toggle-btn" :class="{ active: viewMode === 'list' }"
                        :aria-label="$t('knowledgeBase.viewModeList')" @click="viewMode = 'list'" :aria-pressed="viewMode === 'list'">
                        <t-icon name="view-list" size="16px" />
                      </button>
                    </t-tooltip>
                  </div>
                  <div v-if="canEdit" class="doc-filter-actions">
                    <KbUploadSourceDropdown ref="uploadSourceRef" :accept-file-types="acceptFileTypes"
                      :supported-file-types="[...supportedFileTypes]" include-manual trigger-icon="add" :trigger-label="t('knowledgeBase.addDocument')"
                      trigger-class="content-bar-icon-btn" data-guide="kb-detail-add-doc"
                      :tooltip="t('knowledgeBase.addDocument')" placement="bottom-right" @files="handleUploadSourceFiles"
                      @url="handleUploadSourceUrl" @manual="handleManualCreate" />
                  </div>
                </div>
              </div>
              <div class="doc-scroll-container"
                :class="{
                  'is-empty': !cardList.length && !docListLoading,
                  'is-marquee-active': docMarqueeVisible,
                }"
                ref="knowledgeScroll" @scroll="handleScroll" @mousedown="onDocMarqueeMouseDown">
                <div v-if="docMarqueeVisible" class="doc-marquee-box"
                  :class="{ 'is-add': docMarqueeMode === 'add', 'is-subtract': docMarqueeMode === 'subtract' }"
                  :style="docMarqueeBoxStyle" aria-hidden="true" />
                <!-- 文档骨架屏 -->
                <div v-if="docListLoading && !cardList.length && viewMode === 'list'" class="doc-list-skeleton">
                  <div v-for="n in 8" :key="n" class="doc-list-skeleton-row">
                    <t-skeleton animation="gradient" :row-col="[[{ width: '32px', height: '38px', type: 'rect' }, { width: '55%', height: '16px' }]]" />
                  </div>
                </div>
                <div v-else-if="docListLoading && cardList.length === 0" class="doc-card-list doc-card-list-animated">
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
                <template v-else-if="cardList.length && viewMode === 'grid'">
                  <DocumentCardView :kb-id="kbId"
                    :items="cardList"
                    :folder-options="folderOptions"
                    :selected-ids="selectedIds"
                    :batch-mode="batchMode"
                    :can-edit="canEdit"
                    :can-download="canDownloadKnowledge"
                    :can-mutate-knowledge="canMutateKnowledge"
                    :trace-available-by-id="traceAvailableById"
                    :move-menu-mode="moveMenuMode"
                    :move-target-kbs="moveTargetKbs"
                    :move-targets-loading="moveTargetsLoading"
                    :move-selected-target-name="moveSelectedTargetName"
                    :move-mode="moveMode"
                    :move-submitting="moveSubmitting"
                    :show-folder-path="showDocumentFolderPath"
                    @open="(item: any) => openKnowledgeItem(item)"
                    @open-folder="handleFolderSelect"
                    @move-to-folder="(item: any, path: string) => moveKnowledgeIntoFolder([item.id], path)"
                    @toggle-checkbox="onCardGridCheckboxChange"
                    @menu-visible-change="(visible: boolean, item: any) => onCardMoreVisibleChange(visible, item)"
                    @action="(action: any, item: any) => handleCardAction(action, item)"
                    @tags-changed="onTagCatalogChanged"
                    @move-select-target="(kb: any) => handleMoveSelectTarget(kb)"
                    @move-back="handleMoveBack"
                    @move-confirm="handleMoveConfirm"
                    @update:move-mode="(mode: any) => moveMode = mode"
                  />
                </template>
                <template v-else-if="cardList.length && viewMode === 'list'">
                  <DocumentListView :kb-id="kbId" :items="cardList" :folder-options="folderOptions"
                    :selected-ids="selectedIds"
                    :can-edit="canEdit" :can-download="canDownloadKnowledge" :can-mutate-knowledge="canMutateKnowledge"
                    :trace-visible-ids="traceAvailableById"
                    :move-menu-mode="moveMenuMode"
                    :move-target-kbs="moveTargetKbs"
                    :move-targets-loading="moveTargetsLoading"
                    :move-selected-target-name="moveSelectedTargetName"
                    :move-mode="moveMode"
                    :move-submitting="moveSubmitting"
                    :show-folder-path="showDocumentFolderPath"
                    @open-folder="handleFolderSelect"
                    @move-to-folder="(item: any, path: string) => moveKnowledgeIntoFolder([item.id], path)"
                    @open="(item: any) => openKnowledgeItem(item)" @toggle-row="toggleSelectRow"
                    @toggle-all="toggleSelectAll" @action="(action: any, item: any) => handleListAction(action, item)"
                    @probe-trace="(item: any) => probeTraceAvailable(item)"
                    @tags-changed="onTagCatalogChanged"
                    @move-select-target="(kb: any) => handleMoveSelectTarget(kb)"
                    @move-back="handleMoveBack"
                    @move-confirm="handleMoveConfirm"
                    @update:move-mode="(mode: any) => moveMode = mode"
                    @reset-move-state="moveMenuMode = 'normal'" />
                </template>
                <template v-else-if="!docListLoading">
                  <div class="doc-empty-state">
                    <p v-if="hasFolders || selectedFolderPath || isFiltering" class="doc-empty-folder">
                      {{ isFiltering
                        ? $t('knowledgeBase.folderTree.emptySearch')
                        : $t('knowledgeBase.folderTree.emptyFolder') }}
                    </p>
                    <EmptyKnowledge v-else />
                  </div>
                </template>
              </div>
              <div class="doc-batch-bar-anchor" v-show="batchMode || selectedIds.size > 0">
                <DocumentBatchBar :count="selectedIds.size" :delete-loading="batchDeleting"
                  :reparse-loading="batchReparsing" :tag-loading="batchTagging" :download-loading="batchDownloading"
                  :can-download="canDownloadKnowledge" :can-mutate="canMutateKnowledge"
                  :visible="batchMode || selectedIds.size > 0"
                  :show-move-to-folder="canEdit" :folder-options="folderOptions"
                  @cancel="handleBatchCancel" @delete="confirmBatchDelete" @reparse="confirmBatchReparse"
                  @batch-tag="handleBatchTag" @download="handleBatchDownload" @select-loaded="toggleSelectAll(true)"
                  @move-to-folder="(path: string) => moveKnowledgeIntoFolder(Array.from(selectedIds), path)" />
              </div>
            </div>
          </div>
        </div>
      </template>

      <!-- DocContent drawer (shared by documents tab and wiki source refs) -->
      <DocContent ref="docContentRef" :visible="isCardDetails" :details="details" :canEditKB="canEdit"
        :canDownloadKB="canDownloadKnowledge" :kbId="kbId"
        @closeDoc="closeDoc" @getDoc="getDoc" @summaryStateChange="syncDocumentSummaryState">
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

  <!-- 批量打标签弹窗 -->
  <BatchTagDialog @tags-changed="onTagCatalogChanged" :visible="batchTagDialogVisible"
    :count="selectedIds.size" :kb-id="kbId"
    :pre-selected-tag-ids="batchTagPreSelectedIds"
    :confirm-loading="batchTagging"
    @update:visible="batchTagDialogVisible = $event" @confirm="onBatchTagConfirm" />

</template>
<style>
/* 下拉菜单容器样式已统一至 @/assets/dropdown-menu.less */
.document-filter-popup .t-popup__content {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  box-shadow: 0 8px 32px rgb(0 0 0 / 10%);
}

</style>
<style scoped lang="less">
.knowledge-layout {
  display: flex;
  flex-direction: column;
  margin: 0 16px 0 0;
  gap: 16px;
  height: 100%;
  flex: 1;
  width: 100%;
  min-width: 0;
  padding: 20px 28px 0;
  box-sizing: border-box;
}

// Breadcrumb tab switch (文档/Wiki in breadcrumb)
.breadcrumb-tab {
  cursor: pointer;
  color: var(--td-text-color-placeholder);
  font-weight: 400;
  transition: color var(--app-motion-fast);
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
  font-size: var(--app-text-sm);
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

// Directory navigation and the document content share the available width.
.knowledge-main {
  display: flex;
  flex: 1;
  min-height: 0;
  background: transparent;
  border: none;
}

@media (max-width: 1000px) {
  .knowledge-main {
    flex-direction: column;
    :deep(.kb-folder-tree) {
      width: 100%;
      max-height: 200px;
      padding: 0 0 12px;
      margin: 0 0 16px;
      border-right: 0;
      border-bottom: 1px solid var(--td-component-stroke);
    }
    :deep(.kb-folder-tree__header) { height: 28px; padding-bottom: 4px; }
  }
}

// 标签筛选浮层：点击工具栏入口展开，不占文档列表横向空间

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
  min-width: 0;
  position: relative;
  container-type: inline-size;
  container-name: doc-card-area;
}

// One navigation row; filters open in a single anchored panel.
.doc-filter-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-shrink: 0;
  padding: 0 0 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  &__trailing { display: flex; align-items: center; gap: 8px; min-width: 0; }
  .doc-search-input { width: 220px; min-width: 100px; }
  :deep(.doc-search-input .t-input) {
    background: transparent;
    border-color: var(--td-component-stroke);
    border-radius: var(--app-radius-md);
    font-size: var(--app-text-md);
  }
  .doc-filter-actions :deep(.content-bar-icon-btn) {
    height: 32px;
    padding: 0 12px;
    border-radius: var(--app-radius-md);
    background: var(--td-brand-color);
    color: var(--td-text-color-anti);
    &:hover { background: var(--td-brand-color-hover); }
  }
}
.doc-folder-path {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  min-height: 32px;
  line-height: 20px;
  flex-wrap: wrap;
  &__tree-toggle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    align-self: center;
    gap: 6px;
    height: 32px;
    padding: 0 10px;
    font: inherit;
    font-size: var(--app-text-md);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-container);
    color: var(--td-text-color-secondary);
    cursor: pointer;
    &:hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
  }
  &__crumb {
    max-width: 160px;
    padding: 6px 2px;
    border: 0;
    background: transparent;
    color: var(--td-text-color-secondary);
    font: inherit;
    font-size: var(--app-text-md);
    line-height: 20px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    cursor: pointer;
    &:is(button):hover { color: var(--td-brand-color); }
    &.is-current { color: var(--td-text-color-primary); font-weight: 600; cursor: default; }
  }
  &__count { padding: 6px 0; line-height: 20px; margin-left: 6px; color: var(--td-text-color-placeholder); font-size: var(--app-text-sm); font-variant-numeric: tabular-nums; white-space: nowrap; }
  &__sep, &__scope { align-self: center; flex-shrink: 0; color: var(--td-text-color-placeholder); font-size: var(--app-text-sm); }
}
.doc-filter-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  height: 32px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: transparent;
  color: var(--td-text-color-secondary);
  font: inherit;
  font-size: var(--app-text-md);
  white-space: nowrap;
  cursor: pointer;
  &:hover, &.active { background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-primary); }
}
.doc-filter-count {
  display: inline-flex; align-items: center; justify-content: center; min-width: 17px; height: 17px;
  padding: 0 2px; color: var(--td-brand-color); font-weight: 600;
  font-size: var(--app-text-xs); font-variant-numeric: tabular-nums;
}
.doc-view-toggle {
  display: inline-flex;
  align-items: center;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid transparent;
  border-radius: var(--app-radius-md);
  padding: 2px;
  .doc-view-toggle-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 26px;
    padding: 0;
    border: 0;
    border-radius: var(--app-radius-xs);
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    &:hover { color: var(--td-text-color-primary); }
    &.active { background: var(--td-bg-color-container); color: var(--td-brand-color); box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08); }
  }
}
.doc-filter-panel {
  width: 340px;
  max-width: calc(100vw - 32px);
  max-height: min(600px, 80vh);
  overflow-y: auto;
  padding: 16px;
  box-sizing: border-box;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  button { font: inherit; cursor: pointer; }
  &__header {
    display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px;
    strong { font-size: var(--app-text-base); font-weight: 600; }
    button { border: 0; padding: 0; background: transparent; color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
    button:disabled { color: var(--td-text-color-disabled); cursor: default; }
  }
  &__fields { display: flex; flex-direction: column; gap: 10px; }
  .doc-filter-field {
    display: grid; grid-template-columns: 72px minmax(0, 1fr); align-items: center; gap: 8px;
    > span { color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
    :deep(.t-date-range-picker) { width: 100%; }
  }
}
.doc-filter-tags {
  margin-top: 16px;
  padding-top: 14px;
  border-top: 1px solid var(--td-component-stroke);
  &__heading {
    display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;
    button { padding: 0; border: 0; background: transparent; color: var(--td-text-color-secondary); font-size: var(--app-text-sm); }
  }
  &__count { margin-left: 6px; color: var(--td-text-color-placeholder); font-variant-numeric: tabular-nums; }
  &__list { display: flex; flex-direction: column; gap: 2px; max-height: 168px; overflow-y: auto; margin-top: 8px; }
  &__empty { color: var(--td-text-color-placeholder); padding: 8px 0; }
}
.doc-filter-tag {
  display: flex; align-items: center; flex-shrink: 0; width: 100%; min-height: 28px; margin: 0;
  padding: 4px 6px; box-sizing: border-box; border-radius: var(--app-radius-xs);
  &:hover { background: var(--td-bg-color-container-hover); }
  :deep(.t-checkbox__label) { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  :deep(.t-checkbox__input) { flex-shrink: 0; }
}
.doc-batch-toggle {
  &:disabled { cursor: not-allowed; opacity: 0.5; }
  &.active { color: var(--app-selection-text); border-color: var(--td-component-border); background: var(--app-selection-bg); }
}
.doc-filter-bar button:focus-visible, .doc-filter-panel button:focus-visible {
  outline: 2px solid var(--app-focus-border); outline-offset: 2px;
}
.doc-list-skeleton-row { padding: 16px 40px; }
@container doc-card-area (max-width: 780px) {
  .doc-filter-bar { flex-wrap: wrap; gap: 12px; }
  .doc-filter-bar__trailing { width: 100%; }
  .doc-filter-bar .doc-search-input { flex: 1; width: auto; }
}

@container doc-card-area (max-width: 540px) {
  .doc-filter-bar__trailing { flex-wrap: wrap; }
  .doc-filter-bar .doc-search-input { flex: 1 0 100%; }
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

/* Reserve space for the batch actions so the last document stays reachable. */
.doc-batch-bar-anchor {
  position: relative;
  flex-shrink: 0;
  z-index: 6;
  display: flex;
  justify-content: center;
  padding: 12px 0 0;
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
    font-size: var(--app-text-3xl);
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
    border-radius: var(--app-radius-sm);
    transition: all var(--app-motion-instant) ease;

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
        font-size: var(--app-text-base);
        transition: transform var(--app-motion-instant) ease;
      }

      &:hover:not(:disabled) {
        :deep(.t-icon) {
          transform: translateY(1px);
        }
      }
    }
  }

  .breadcrumb-separator {
    font-size: var(--app-text-base);
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
    font-size: var(--app-text-4xl);
    font-weight: 600;
    line-height: 32px;
  }

  .document-subtitle {
    margin: 0;
    color: var(--td-text-color-placeholder);
    font-family: var(--app-font-family);
    font-size: var(--app-text-base);
    font-weight: 400;
    line-height: 20px;
  }

  .parser-hint {
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 2px 0 0;
    color: var(--td-warning-color);
    font-size: var(--app-text-sm);
    line-height: 1.4;
    cursor: pointer;
    transition: color var(--app-motion-fast) ease;

    &:hover {
      color: var(--td-warning-color-active);

      .parser-hint-link {
        text-decoration: underline;
      }
    }

    .parser-hint-icon {
      font-size: var(--app-text-sm);
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
    font-size: var(--app-text-sm);
    line-height: 1.4;
    cursor: pointer;
    transition: color var(--app-motion-fast) ease;

    &:hover {
      color: var(--td-warning-color-active);

      .warning-link {
        text-decoration: underline;
      }
    }

    .warning-icon {
      font-size: var(--app-text-sm);
      flex-shrink: 0;
    }

    .warning-link {
      color: var(--td-brand-color);
      margin-left: 2px;
      white-space: nowrap;
    }
  }
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
  transition: all var(--app-motion-base) ease;
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
    font-size: var(--app-text-2xl);
  }
}

.faq-manager-wrapper {
  flex: 1;
  min-height: 0;
  padding: 24px 32px;
  overflow-y: auto;
  margin: 0 16px 0 4px;
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
  grid-template-columns: repeat(auto-fill, minmax(min(260px, 100%), 1fr));
  gap: 10px;
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
    padding: 10px;
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

.knowledge-card {
  min-width: 0;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--td-component-border);
  height: 164px;
  border-radius: var(--app-radius-md);
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: border-color var(--app-motion-base) ease, box-shadow var(--app-motion-base) ease, background-color var(--app-motion-base) ease;

  .card-content {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding: 12px;
  }

  .card-content-nav {
    flex-shrink: 0;
    display: flex;
    align-items: flex-start;
    gap: 0;
    margin-bottom: 6px;
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

}

.knowledge-card:hover {
  border-color: color-mix(in srgb, var(--td-component-stroke) 55%, var(--td-brand-color));
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.07);
}

</style>
