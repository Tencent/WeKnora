<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, reactive, computed, nextTick, h, type ComponentPublicInstance } from "vue";
import { MessagePlugin, Icon as TIcon } from "tdesign-vue-next";
import DocContent from "@/components/doc-content.vue";
import useKnowledgeBase from '@/hooks/useKnowledgeBase';
import { useRoute, useRouter } from 'vue-router';
import EmptyKnowledge from '@/components/empty-knowledge.vue';
import { getSessionsList, createSessions, generateSessionsTitle } from "@/api/chat/index";
import { useMenuStore } from '@/stores/menu';
import { useUIStore } from '@/stores/ui';
import { useOrganizationStore } from '@/stores/organization';
import { useAuthStore } from '@/stores/auth';
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue';
const usemenuStore = useMenuStore();
const uiStore = useUIStore();
const orgStore = useOrganizationStore();
const authStore = useAuthStore();
const router = useRouter();
import {
  batchQueryKnowledge,
  getKnowledgeBaseById,
  listKnowledgeTags,
  updateKnowledgeTagBatch,
  createKnowledgeBaseTag,
  updateKnowledgeBaseTag,
  deleteKnowledgeBaseTag,
  uploadKnowledgeFile,
  createKnowledgeFromURL,
  listKnowledgeBases,
  reparseKnowledge,
} from "@/api/knowledge-base/index";
import FAQEntryManager from './components/FAQEntryManager.vue';
import { listMoveTargets, moveKnowledge, getKnowledgeMoveProgress } from '@/api/knowledge-base';
import { useI18n } from 'vue-i18n';
import { formatStringDate, kbFileTypeVerification } from '@/utils';
import { getParserEngines, type ParserEngineInfo } from '@/api/system';
const route = useRoute();
const { t } = useI18n();
const kbId = computed(() => (route.params as any).kbId as string || '');
const kbInfo = ref<any>(null);
const uploadInputRef = ref<HTMLInputElement | null>(null);
const folderUploadInputRef = ref<HTMLInputElement | null>(null);
const uploading = ref(false);
const kbLoading = ref(false);
const isFAQ = computed(() => (kbInfo.value?.type || '') === 'faq');
const missingStorageEngine = computed(() => {
  if (!kbInfo.value || isFAQ.value) return false
  const spc = kbInfo.value.storage_provider_config
  return !spc || !spc.provider
})
const parserEngines = ref<ParserEngineInfo[]>([]);

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
const isOwner = computed(() => {
  if (!kbInfo.value) return false;
  // Check if the current user's tenant ID matches the KB's tenant ID
  const userTenantId = authStore.effectiveTenantId;
  return kbInfo.value.tenant_id === userTenantId;
});

// Can edit: owner, admin, or editor
const canEdit = computed(() => {
  return orgStore.canEditKB(kbId.value, isOwner.value);
});

// Can manage (delete, settings, etc.): owner or admin
const canManage = computed(() => {
  return orgStore.canManageKB(kbId.value, isOwner.value);
});

// Current KB's shared record (when accessed via organization share)
const currentSharedKb = computed(() =>
  orgStore.sharedKnowledgeBases.find((s) => s.knowledge_base?.id === kbId.value) ?? null,
);

// Effective permission: from direct org share list or from GET /knowledge-bases/:id (e.g. agent-visible KB)
const effectiveKBPermission = computed(() => orgStore.getKBPermission(kbId.value) || kbInfo.value?.my_permission || '');

// Display role label: owner or org role (admin/editor/viewer)
const accessRoleLabel = computed(() => {
  if (isOwner.value) return t('knowledgeBase.accessInfo.roleOwner');
  const perm = effectiveKBPermission.value;
  if (perm) return t(`organization.role.${perm}`);
  return '--';
});

// Permission summary text for current role
const accessPermissionSummary = computed(() => {
  if (isOwner.value) return t('knowledgeBase.accessInfo.permissionOwner');
  const perm = effectiveKBPermission.value;
  if (perm === 'admin') return t('knowledgeBase.accessInfo.permissionAdmin');
  if (perm === 'editor') return t('knowledgeBase.accessInfo.permissionEditor');
  if (perm === 'viewer') return t('knowledgeBase.accessInfo.permissionViewer');
  return '--';
});

// Last updated time from kbInfo
const kbLastUpdated = computed(() => {
  const raw = kbInfo.value?.updated_at;
  if (!raw) return null;
  return formatStringDate(new Date(raw));
});

const knowledgeList = ref<Array<{ id: string; name: string; type?: string }>>([]);
let { cardList, total, moreIndex, details, getKnowled, delKnowledge, openMore, onVisibleChange: _onVisibleChange, getCardDetails, getfDetails } = useKnowledgeBase(kbId.value)
const onVisibleChange = (visible: boolean) => {
  _onVisibleChange(visible);
  if (!visible) {
    moveMenuMode.value = 'normal';
  }
};
let isCardDetails = ref(false);
let timeout: ReturnType<typeof setInterval> | null = null;
let delDialog = ref(false)
let rebuildDialog = ref(false)
let rebuildKnowledgeItem = ref<KnowledgeCard>({ id: '', parse_status: '' })
let knowledge = ref<KnowledgeCard>({ id: '', parse_status: '' })
let knowledgeIndex = ref(-1)
let knowledgeScroll = ref()
let page = 1;
let pageSize = 35;

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

const selectedTagId = ref<string>('');
const tagList = ref<any[]>([]);
const tagLoading = ref(false);
const tagSearchQuery = ref('');
const TAG_PAGE_SIZE = 50;
const tagPage = ref(1);
const tagHasMore = ref(false);
const tagLoadingMore = ref(false);
const tagTotal = ref(0);
let tagSearchDebounce: ReturnType<typeof setTimeout> | null = null;
let docSearchDebounce: ReturnType<typeof setTimeout> | null = null;
const docSearchKeyword = ref('');
const selectedFileType = ref('');
const fileTypeOptions = computed(() => [
  { content: t('knowledgeBase.allFileTypes'), value: '' },
  { content: 'PDF', value: 'pdf' },
  { content: 'DOCX', value: 'docx' },
  { content: 'DOC', value: 'doc' },
  { content: 'PPTX', value: 'pptx' },
  { content: 'PPT', value: 'ppt' },
  { content: 'TXT', value: 'txt' },
  { content: 'MD', value: 'md' },
  { content: 'URL', value: 'url' },
  { content: t('knowledgeBase.typeManual'), value: 'manual' },
    { content: 'MP4', value: 'mp4' },
  { content: 'MOV', value: 'mov' },
  { content: 'AVI', value: 'avi' },
  { content: 'MKV', value: 'mkv' },
  { content: 'WEBM', value: 'webm' },
  { content: 'WMV', value: 'wmv' },
  { content: 'FLV', value: 'flv' },
  { content: 'MP3', value: 'mp3' },
  { content: 'WAV', value: 'wav' },
  { content: 'M4A', value: 'm4a' },
  { content: 'FLAC', value: 'flac' },
  { content: 'OGG', value: 'ogg' },
]);
type TagInputInstance = ComponentPublicInstance<{ focus: () => void; select: () => void }>;
const tagDropdownOptions = computed(() =>
  tagList.value.map((tag: any) => ({
    content: tag.name,
    value: tag.id,
  })),
);
const tagMap = computed<Record<string, any>>(() => {
  const map: Record<string, any> = {};
  tagList.value.forEach((tag) => {
    map[tag.id] = tag;
  });
  return map;
});
const sidebarCategoryCount = computed(() => tagList.value.length);
const filteredTags = computed(() => {
  const query = tagSearchQuery.value.trim().toLowerCase();
  if (!query) return tagList.value;
  return tagList.value.filter((tag) => (tag.name || '').toLowerCase().includes(query));
});

const editingTagInputRefs = new Map<string, TagInputInstance | null>();
const setEditingTagInputRef = (el: TagInputInstance | null, tagId: string) => {
  if (el) {
    editingTagInputRefs.set(tagId, el);
  } else {
    editingTagInputRefs.delete(tagId);
  }
};
const setEditingTagInputRefByTag = (tagId: string) => (el: TagInputInstance | null) => {
  setEditingTagInputRef(el, tagId);
};
const newTagInputRef = ref<TagInputInstance | null>(null);
const creatingTag = ref(false);
const creatingTagLoading = ref(false);
const newTagName = ref('');
const editingTagId = ref<string | null>(null);
const editingTagName = ref('');
const editingTagSubmitting = ref(false);
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

const formatDocTime = (time?: string) => {
  if (!time) return '--'
  const formatted = formatStringDate(new Date(time))
  return formatted.slice(2, 16) // "YY-MM-DD HH:mm"
}

// 格式化文件大小，用于气泡等展示
const formatFileSize = (bytes?: number | string) => {
  if (bytes == null || bytes === '') return ''
  const n = typeof bytes === 'string' ? parseInt(bytes, 10) : bytes
  if (Number.isNaN(n) || n <= 0) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

const channelLabelMap: Record<string, string> = {
  web: 'knowledgeBase.channelWeb',
  api: 'knowledgeBase.channelApi',
  browser_extension: 'knowledgeBase.channelBrowserExtension',
  wechat: 'knowledgeBase.channelWechat',
  wecom: 'knowledgeBase.channelWecom',
  feishu: 'knowledgeBase.channelFeishu',
  dingtalk: 'knowledgeBase.channelDingtalk',
  slack: 'knowledgeBase.channelSlack',
  im: 'knowledgeBase.channelIm',
};

const getChannelLabel = (channel: string) => {
  const key = channelLabelMap[channel];
  return key ? t(key) : t('knowledgeBase.channelUnknown');
};

// 获取知识条目的显示类型
const getKnowledgeType = (item: any) => {
  if (item.type === 'url') {
    return t('knowledgeBase.typeURL') || 'URL';
  }
  if (item.type === 'manual') {
    return t('knowledgeBase.typeManual');
  }
  if (item.file_type) {
    return item.file_type.toUpperCase();
  }
  return '--';
}

const loadKnowledgeFiles = (kbIdValue: string) => {
  if (!kbIdValue) return;
  getKnowled(
    {
      page: 1,
      page_size: pageSize,
      tag_id: selectedTagId.value || undefined,
      keyword: docSearchKeyword.value ? docSearchKeyword.value.trim() : undefined,
      file_type: selectedFileType.value || undefined,
    },
    kbIdValue,
  );
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
    console.error('Failed to load tags', error);
  } finally {
    tagLoading.value = false;
    tagLoadingMore.value = false;
  }
};

const handleTagFilterChange = (value: string) => {
  selectedTagId.value = value;
  // 同步更新 store 中的 selectedTagId，供 menu.vue 上传时使用
  uiStore.setSelectedTagId(value);
  page = 1;
  loadKnowledgeFiles(kbId.value);
};

const handleTagRowClick = (tagId: string) => {
  if (creatingTag.value) {
    creatingTag.value = false;
    newTagName.value = '';
  }
  if (editingTagId.value) {
    editingTagId.value = null;
    editingTagName.value = '';
  }
  if (selectedTagId.value === tagId) {
    handleTagFilterChange('');
    return;
  }
  handleTagFilterChange(tagId);
};

const startCreateTag = () => {
  if (!kbId.value) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return;
  }
  if (creatingTag.value) {
    return;
  }
  editingTagId.value = null;
  editingTagName.value = '';
  creatingTag.value = true;
  nextTick(() => {
    newTagInputRef.value?.focus?.();
    newTagInputRef.value?.select?.();
  });
};

const cancelCreateTag = () => {
  creatingTag.value = false;
  newTagName.value = '';
};

const submitCreateTag = async () => {
  if (!kbId.value) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return;
  }
  const name = newTagName.value.trim();
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'));
    return;
  }
  creatingTagLoading.value = true;
  try {
    await createKnowledgeBaseTag(kbId.value, { name });
    MessagePlugin.success(t('knowledgeBase.tagCreateSuccess'));
    cancelCreateTag();
    await loadTags(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    creatingTagLoading.value = false;
  }
};

const startEditTag = (tag: any) => {
  creatingTag.value = false;
  newTagName.value = '';
  editingTagId.value = tag.id;
  editingTagName.value = tag.name;
  nextTick(() => {
    const inputRef = editingTagInputRefs.get(tag.id);
    inputRef?.focus?.();
    inputRef?.select?.();
  });
};

const cancelEditTag = () => {
  editingTagId.value = null;
  editingTagName.value = '';
};

const submitEditTag = async () => {
  if (!kbId.value || !editingTagId.value) {
    return;
  }
  const name = editingTagName.value.trim();
  if (!name) {
    MessagePlugin.warning(t('knowledgeBase.tagNameRequired'));
    return;
  }
  if (name === tagMap.value[editingTagId.value]?.name) {
    cancelEditTag();
    return;
  }
  editingTagSubmitting.value = true;
  try {
    await updateKnowledgeBaseTag(kbId.value, editingTagId.value, { name });
    MessagePlugin.success(t('knowledgeBase.tagEditSuccess'));
    cancelEditTag();
    await loadTags(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  } finally {
    editingTagSubmitting.value = false;
  }
};

const confirmDeleteTag = (tag: any) => {
  if (!kbId.value) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return;
  }
  if (creatingTag.value) {
    cancelCreateTag();
  }
  if (editingTagId.value) {
    cancelEditTag();
  }
  const deleteDescKey = isFAQ.value ? 'knowledgeBase.tagDeleteDesc' : 'knowledgeBase.tagDeleteDescDoc';
  const confirm = window.confirm(
    t(deleteDescKey, { name: tag.name }) as string,
  );
  if (!confirm) return;
  deleteKnowledgeBaseTag(kbId.value, tag.seq_id, { force: true })
    .then(() => {
      MessagePlugin.success(t('knowledgeBase.tagDeleteSuccess'));
      if (selectedTagId.value === tag.id) {
        // Reset to show all entries when current tag is deleted
        selectedTagId.value = '';
        handleTagFilterChange('');
      }
      loadTags(kbId.value);
      // 由于后端是异步删除文档，延迟刷新以确保看到最新数据
      setTimeout(() => {
        page = 1; // Reset page counter when reloading files after tag deletion
        loadKnowledgeFiles(kbId.value);
      }, 500);
    })
    .catch((error: any) => {
      MessagePlugin.error(error?.message || t('common.operationFailed'));
    });
};

const handleKnowledgeTagChange = async (knowledgeId: string, tagValue: string) => {
  try {
    // Pass the tag value directly (empty string means no tag)
    const tagIdToUpdate = tagValue || null;
    await updateKnowledgeTagBatch({ updates: { [knowledgeId]: tagIdToUpdate } });
    MessagePlugin.success(t('knowledgeBase.tagUpdateSuccess'));
    page = 1; // Reset page counter to 1 when reloading files after tag change
    loadKnowledgeFiles(kbId.value);
    loadTags(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('common.operationFailed'));
  }
};

const loadKnowledgeBaseInfo = async (targetKbId: string) => {
  if (!targetKbId) {
    kbInfo.value = null;
    return;
  }
  kbLoading.value = true;
  try {
    const res: any = await getKnowledgeBaseById(targetKbId);
    kbInfo.value = res?.data || null;
    selectedTagId.value = '';
    // 重置store中的标签选择状态，避免上传文档时自动带上之前选择的标签
    uiStore.setSelectedTagId('');
    if (!isFAQ.value) {
      loadKnowledgeFiles(targetKbId);
    } else {
      cardList.value = [];
      total.value = 0;
    }
    loadTags(targetKbId, true);
  } catch (error) {
    console.error('Failed to load knowledge base info:', error);
    kbInfo.value = null;
  } finally {
    kbLoading.value = false;
  }
};

const loadKnowledgeList = async () => {
  try {
    const res: any = await listKnowledgeBases();
    const myKbs = (res?.data || []).map((item: any) => ({
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
watch(() => kbId.value, (newKbId, oldKbId) => {
  if (newKbId && newKbId !== oldKbId) {
    tagSearchQuery.value = '';
    tagPage.value = 1;
    // 重置标签选择状态，避免在不同知识库间保持标签选择
    uiStore.setSelectedTagId('');
    loadKnowledgeBaseInfo(newKbId);
  }
}, { immediate: false });

watch(selectedTagId, (newVal, oldVal) => {
  if (oldVal === undefined) return
  if (newVal !== oldVal && kbId.value) {
    loadKnowledgeFiles(kbId.value);
  }
});

watch(tagSearchQuery, (newVal, oldVal) => {
  if (newVal === oldVal) return;
  if (tagSearchDebounce) {
    clearTimeout(tagSearchDebounce);
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
    clearTimeout(docSearchDebounce);
  }
  docSearchDebounce = window.setTimeout(() => {
    if (kbId.value) {
      page = 1;
      loadKnowledgeFiles(kbId.value);
    }
  }, 300);
});

// 监听文件类型筛选变化
watch(selectedFileType, (newVal, oldVal) => {
  if (newVal === oldVal) return;
  if (kbId.value) {
    page = 1;
    loadKnowledgeFiles(kbId.value);
  }
});

// 监听文件上传事件
const handleFileUploaded = (event: CustomEvent) => {
  const uploadedKbId = event.detail.kbId;
  console.log('接收到文件上传事件，上传的知识库ID:', uploadedKbId, '当前知识库ID:', kbId.value);
  if (uploadedKbId && uploadedKbId === kbId.value && !isFAQ.value) {
    console.log('匹配当前知识库，开始刷新文件列表');
    // 如果上传的文件属于当前知识库，使用 loadKnowledgeFiles 刷新文件列表
    page = 1; // Reset page counter when reloading files after upload
    loadKnowledgeFiles(uploadedKbId);
    loadTags(uploadedKbId);
  }
};


// 监听从菜单触发的URL导入事件
const handleOpenURLImportDialog = (event: CustomEvent) => {
  const eventKbId = event.detail.kbId;
  console.log('接收到URL导入对话框打开事件，知识库ID:', eventKbId, '当前知识库ID:', kbId.value);
  if (eventKbId && eventKbId === kbId.value && !isFAQ.value) {
    urlDialogVisible.value = true;
  }
};

// Auto-open document detail when navigated with ?knowledge_id=xxx
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

onMounted(() => {
  loadKnowledgeBaseInfo(kbId.value);
  loadKnowledgeList();
  orgStore.fetchSharedKnowledgeBases();

  getParserEngines()
    .then(res => { parserEngines.value = res?.data || [] })
    .catch(() => { parserEngines.value = [] })

  window.addEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  window.addEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
});

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  window.removeEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
  stopMovePoll();
});
watch(() => cardList.value, (newValue) => {
  if (isFAQ.value) return;

  // Auto-open document if navigated with ?knowledge_id=xxx
  if (pendingKnowledgeId.value && newValue?.length) {
    tryAutoOpenDocument();
  }

  let analyzeList = [];
  // Filter items that need polling: parsing in progress OR summary generation in progress
  analyzeList = newValue.filter(item => {
    const isParsing = item.parse_status == 'pending' || item.parse_status == 'processing';
    const isSummaryPending = item.parse_status == 'completed' && 
      (item.summary_status == 'pending' || item.summary_status == 'processing');
    return isParsing || isSummaryPending;
  })
  if (timeout !== null) {
    clearInterval(timeout);
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
  tag_id?: string;
};
const updateStatus = (analyzeList: KnowledgeCard[]) => {
  let query = ``;
  for (let i = 0; i < analyzeList.length; i++) {
    query += `ids=${analyzeList[i].id}&`;
  }
  timeout = setInterval(() => {
    batchQueryKnowledge(query).then((result: any) => {
      if (result.success && result.data) {
        (result.data as KnowledgeCard[]).forEach((item: KnowledgeCard) => {
          const index = cardList.value.findIndex(card => card.id == item.id);
          if (index == -1) return;
          
          // Always update the card data
          cardList.value[index].parse_status = item.parse_status;
          cardList.value[index].summary_status = item.summary_status;
          cardList.value[index].description = item.description;
        });
      }
    }).catch((_err) => {
      // 错误处理
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

// 悬停知识卡片时跟随鼠标显示详情气泡
const hoveredCardItem = ref<KnowledgeCard | null>(null);
const cardPopoverPos = ref({ x: 0, y: 0 });
const CARD_POPOVER_OFFSET = 16;
const cardHoverShowDelay = 300;
let cardHoverTimer: ReturnType<typeof setTimeout> | null = null;

const onCardMouseEnter = (ev: MouseEvent, item: KnowledgeCard) => {
  if (cardHoverTimer) {
    clearTimeout(cardHoverTimer);
    cardHoverTimer = null;
  }
  cardHoverTimer = setTimeout(() => {
    cardHoverTimer = null;
    hoveredCardItem.value = item;
    cardPopoverPos.value = {
      x: ev.clientX + CARD_POPOVER_OFFSET,
      y: ev.clientY + CARD_POPOVER_OFFSET,
    };
  }, cardHoverShowDelay);
};

const onCardMouseMove = (ev: MouseEvent) => {
  if (hoveredCardItem.value) {
    cardPopoverPos.value = {
      x: ev.clientX + CARD_POPOVER_OFFSET,
      y: ev.clientY + CARD_POPOVER_OFFSET,
    };
  }
};

const onCardMouseLeave = () => {
  if (cardHoverTimer) {
    clearTimeout(cardHoverTimer);
    cardHoverTimer = null;
  }
  hoveredCardItem.value = null;
};

const delCard = (index: number, item: KnowledgeCard) => {
  knowledgeIndex.value = index;
  knowledge.value = item;
  delDialog.value = true;
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
      page = 1; // Reset page counter when reloading files after move
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
        page = 1; // Reset page counter when reloading files after move completion
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
    page = 1; // Reset page counter when reloading files after manual edit
    loadKnowledgeFiles(savedKbId);
  }
};

const documentTitle = computed(() => {
  if (kbInfo.value?.name) {
    return `${kbInfo.value.name} · ${t('knowledgeEditor.document.title')}`;
  }
  return t('knowledgeEditor.document.title');
});

// 文档操作下拉菜单选项
const documentActionOptions = computed(() => [
  { content: t('upload.uploadDocument'), value: 'upload', prefixIcon: () => h(TIcon, { name: 'upload', size: '16px' }) },
  { content: t('upload.uploadFolder'), value: 'uploadFolder', prefixIcon: () => h(TIcon, { name: 'folder-add', size: '16px' }) },
  { content: t('knowledgeBase.importURL'), value: 'importURL', prefixIcon: () => h(TIcon, { name: 'link', size: '16px' }) },
  { content: t('upload.onlineEdit'), value: 'manualCreate', prefixIcon: () => h(TIcon, { name: 'edit', size: '16px' }) },
]);

// 处理文档操作下拉菜单选择
const handleDocumentActionSelect = (data: { value: string }) => {
  switch (data.value) {
    case 'upload':
      handleDocumentUploadClick();
      break;
    case 'uploadFolder':
      handleFolderUploadClick();
      break;
    case 'importURL':
      handleURLImportClick();
      break;
    case 'manualCreate':
      handleManualCreate();
      break;
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
  if (!kbInfo.value || !kbInfo.value.embedding_model_id || !kbInfo.value.summary_model_id) {
    MessagePlugin.warning(t('knowledgeBase.notInitialized'));
    return false;
  }
  if (missingStorageEngine.value) {
    MessagePlugin.warning(t('knowledgeBase.missingStorageEngineUpload'));
    return false;
  }
  return true;
};


const handleDocumentUploadClick = () => {
  if (!ensureDocumentKbReady()) return;
  uploadInputRef.value?.click();
};

const handleFolderUploadClick = () => {
  if (!ensureDocumentKbReady()) return;
  folderUploadInputRef.value?.click();
};

const resetUploadInput = () => {
  if (uploadInputRef.value) {
    uploadInputRef.value.value = '';
  }
};

const handleDocumentUpload = async (event: Event) => {
  const input = event.target as HTMLInputElement;
  const files = input?.files;
  if (!files || files.length === 0) return;
  
  if (!kbId.value) {
    MessagePlugin.error(t('error.missingKbId'));
    resetUploadInput();
    return;
  }

  const dynamicTypes = supportedFileTypes.value.size > 0 ? supportedFileTypes.value : undefined
  const validFiles: File[] = [];
  let skippedCount = 0;
  for (let i = 0; i < files.length; i++) {
    const file = files[i];
    if (!kbFileTypeVerification(file, files.length > 1, dynamicTypes)) {
      validFiles.push(file);
    } else {
      skippedCount++;
    }
  }

  if (validFiles.length === 0) {
    if (skippedCount > 0) {
      MessagePlugin.warning(t('knowledgeBase.allFilesSkippedNoEngine'));
    }
    resetUploadInput();
    return;
  }
  if (skippedCount > 0) {
    MessagePlugin.warning(t('knowledgeBase.filesSkippedNoEngine', { count: skippedCount }));
  }

  let successCount = 0;
  let failCount = 0;
  const totalCount = validFiles.length;

  // 获取当前选中的分类ID（如果不是"未分类"则传递）
  const tagIdToUpload = selectedTagId.value !== '__untagged__' ? selectedTagId.value : undefined;

  for (const file of validFiles) {
    try {
      const responseData: any = await uploadKnowledgeFile(kbId.value, { file, tag_id: tagIdToUpload });
      const isSuccess = responseData?.success || responseData?.code === 200 || responseData?.status === 'success' || (!responseData?.error && responseData);
      if (isSuccess) {
        successCount++;
      } else {
        failCount++;
        let errorMessage = t('knowledgeBase.uploadFailed');
        if (responseData?.error?.message) {
          errorMessage = responseData.error.message;
        } else if (responseData?.message) {
          errorMessage = responseData.message;
        }
        if (responseData?.code === 'duplicate_file' || responseData?.error?.code === 'duplicate_file') {
          errorMessage = t('knowledgeBase.fileExists');
        }
        if (totalCount === 1) {
          MessagePlugin.error(errorMessage);
        }
      }
    } catch (error: any) {
      failCount++;
      let errorMessage = error?.error?.message || error?.message || t('knowledgeBase.uploadFailed');
      if (error?.code === 'duplicate_file') {
        errorMessage = t('knowledgeBase.fileExists');
      }
      if (totalCount === 1) {
        MessagePlugin.error(errorMessage);
      }
    }
  }

  // 显示上传结果
  if (successCount > 0) {
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: kbId.value }
    }));
  }

  if (totalCount === 1) {
    if (successCount === 1) {
      MessagePlugin.success(t('knowledgeBase.uploadSuccess'));
    }
  } else {
    if (failCount === 0) {
      MessagePlugin.success(t('knowledgeBase.allUploadSuccess', { count: successCount }));
    } else if (successCount > 0) {
      MessagePlugin.warning(t('knowledgeBase.partialUploadSuccess', { success: successCount, fail: failCount }));
    } else {
      MessagePlugin.error(t('knowledgeBase.allUploadFailed', { count: failCount }));
    }
  }

  resetUploadInput();
};

// 处理文件夹上传
const handleFolderUpload = async (event: Event) => {
  const input = event.target as HTMLInputElement;
  const files = input?.files;
  if (!files || files.length === 0) return;

  if (!kbId.value) {
    MessagePlugin.error(t('error.missingKbId'));
    if (input) input.value = '';
    return;
  }

  const vlmEnabled = kbInfo.value?.vlm_config?.enabled || false;
  const asrEnabled = kbInfo.value?.asr_config?.enabled || false;
  const dynamicTypes = supportedFileTypes.value.size > 0 ? supportedFileTypes.value : undefined

  const validFiles: File[] = [];
  let hiddenFileCount = 0;
  let imageFilteredCount = 0;
  let videoFilteredCount = 0;
  let audioFilteredCount = 0;

  for (let i = 0; i < files.length; i++) {
    const file = files[i];
    const relativePath = (file as any).webkitRelativePath || file.name;
    
    const pathParts = relativePath.split('/');
    const hasHiddenComponent = pathParts.some((part: string) => part.startsWith('.'));
    if (hasHiddenComponent) {
      hiddenFileCount++;
      continue;
    }
    
    const fileExt = file.name.substring(file.name.lastIndexOf('.') + 1).toLowerCase();
    const imageTypes = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp'];
    const videoTypes = ['mp4', 'mov', 'avi', 'mkv', 'webm', 'wmv', 'flv'];
    const audioTypes = ['mp3', 'wav', 'm4a', 'flac', 'ogg'];
    
    if (!vlmEnabled) {
      if (imageTypes.includes(fileExt)) {
        imageFilteredCount++;
        continue;
      }
      if (videoTypes.includes(fileExt)) {
        videoFilteredCount++;
        continue;
      }
    }
    
    if (!asrEnabled && audioTypes.includes(fileExt)) {
      audioFilteredCount++;
      continue;
    }
    
    if (!kbFileTypeVerification(file, true, dynamicTypes)) {
      validFiles.push(file);
    }
  }

  if (validFiles.length === 0) {
    MessagePlugin.warning(t('knowledgeBase.noValidFilesInFolder', { total: files.length }));
    if (input) input.value = '';
    return;
  }
  MessagePlugin.info(t('knowledgeBase.uploadingFolder', { total: validFiles.length }));

  // 批量上传
  let successCount = 0;
  let failCount = 0;
  const tagIdToUpload = selectedTagId.value !== '__untagged__' ? selectedTagId.value : undefined;

  for (const file of validFiles) {
    const relativePath = (file as any).webkitRelativePath;
    let fileName = file.name;
    if (relativePath) {
      const pathParts = relativePath.split('/');
      if (pathParts.length > 2) {
        const subPath = pathParts.slice(1, -1).join('/');
        fileName = `${subPath}/${file.name}`;
      }
    }

    try {
      await uploadKnowledgeFile(kbId.value, { file, fileName, tag_id: tagIdToUpload });
      successCount++;
    } catch (error: any) {
      failCount++;
    }
  }

  if (successCount > 0) {
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: kbId.value }
    }));
  }

  if (failCount === 0) {
    MessagePlugin.success(t('knowledgeBase.uploadAllSuccess', { count: successCount }));
  } else if (successCount > 0) {
    MessagePlugin.warning(t('knowledgeBase.uploadPartialSuccess', { success: successCount, fail: failCount }));
  } else {
    MessagePlugin.error(t('knowledgeBase.uploadAllFailed'));
  }

  if (input) input.value = '';
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

// URL 导入相关
const urlDialogVisible = ref(false);
const urlInputValue = ref('');
const urlImporting = ref(false);

const handleURLImportClick = () => {
  if (!ensureDocumentKbReady()) return;
  urlInputValue.value = '';
  urlDialogVisible.value = true;
};

const handleURLImportCancel = () => {
  urlDialogVisible.value = false;
  urlInputValue.value = '';
};

const handleURLImportConfirm = async () => {
  const url = urlInputValue.value.trim();
  if (!url) {
    MessagePlugin.warning(t('knowledgeBase.urlRequired'));
    return;
  }
  
  // 简单的URL格式验证
  try {
    new URL(url);
  } catch (error) {
    MessagePlugin.warning(t('knowledgeBase.invalidURL'));
    return;
  }

  if (!kbId.value) {
    MessagePlugin.error(t('error.missingKbId'));
    return;
  }

  urlImporting.value = true;
  try {
    // 获取当前选中的分类ID
    const tagIdToUpload = selectedTagId.value !== '__untagged__' ? selectedTagId.value : undefined;
    const responseData: any = await createKnowledgeFromURL(kbId.value, { url, tag_id: tagIdToUpload });
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: kbId.value }
    }));
    const isSuccess = responseData?.success || responseData?.code === 200 || responseData?.status === 'success' || (!responseData?.error && responseData);
    if (isSuccess) {
      MessagePlugin.success(t('knowledgeBase.urlImportSuccess'));
      urlDialogVisible.value = false;
      urlInputValue.value = '';
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
  } finally {
    urlImporting.value = false;
  }
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

const knowledgeDropdownOptions = computed(() =>
  knowledgeList.value.map((item) => ({
    content: item.name,
    value: item.id,
    prefixIcon: () => h(TIcon, { name: item.type === 'faq' ? 'chat-bubble-help' : 'folder', size: '16px' }),
  }))
);

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

const handleKnowledgeReparse = (index: number, item: KnowledgeCard) => {
  if (isFAQ.value) return;
  if (!canEdit.value) return;
  if (!item?.id) {
    MessagePlugin.warning(t('knowledgeEditor.messages.missingId'));
    return;
  }
  if (item.parse_status === 'pending' || item.parse_status === 'processing') {
    MessagePlugin.info(t('knowledgeBase.rebuildInProgress'));
    return;
  }
  if (cardList.value[index]) {
    cardList.value[index].isMore = false;
  }
  rebuildKnowledgeItem.value = item;
  rebuildDialog.value = true;
};

const rebuildConfirm = async () => {
  rebuildDialog.value = false;
  const item = rebuildKnowledgeItem.value;
  if (!item?.id) return;
  try {
    await reparseKnowledge(item.id);
    MessagePlugin.success(t('knowledgeBase.rebuildSubmitted'));
    page = 1; // Reset page counter when reloading files after reparse
    loadKnowledgeFiles(kbId.value);
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('knowledgeBase.rebuildFailed'));
  }
};

const handleScroll = () => {
  if (isFAQ.value) return;
  const element = knowledgeScroll.value;
  if (element) {
    let pageNum = Math.ceil(total.value / pageSize)
    const { scrollTop, scrollHeight, clientHeight } = element;
    if (scrollTop + clientHeight >= scrollHeight) {
      page++;
      if (cardList.value.length < total.value && page <= pageNum) {
        getKnowled({ page, page_size: pageSize, tag_id: selectedTagId.value, keyword: docSearchKeyword.value ? docSearchKeyword.value.trim() : undefined, file_type: selectedFileType.value || undefined });
      }
    }
  }
};
const getDoc = (page: number) => {
  getfDetails(details.id, page)
};

const delCardConfirm = () => {
  delDialog.value = false;
  delKnowledge(knowledgeIndex.value, knowledge.value, () => {
    // 删除成功后刷新文档列表和分类数量
    page = 1; // Reset page counter when reloading files after deletion
    loadKnowledgeFiles(kbId.value);
    loadTags(kbId.value);
  });
};

// 处理知识库编辑成功后的回调
const handleKBEditorSuccess = (kbIdValue: string) => {
  if (kbIdValue === kbId.value) {
    loadKnowledgeBaseInfo(kbIdValue);
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
              <t-dropdown
                v-if="knowledgeDropdownOptions.length"
                :options="knowledgeDropdownOptions"
                trigger="click"
                placement="bottom-left"
                @click="handleKnowledgeDropdownSelect"
              >
                <button
                  type="button"
                  class="breadcrumb-link dropdown"
                  :disabled="!kbId"
                  @click.stop="handleNavigateToCurrentKB"
                >
                  <span>{{ kbInfo?.name || '--' }}</span>
                  <t-icon name="chevron-down" />
                </button>
              </t-dropdown>
              <button
                v-else
                type="button"
                class="breadcrumb-link"
                :disabled="!kbId"
                @click="handleNavigateToCurrentKB"
              >
                {{ kbInfo?.name || '--' }}
              </button>
              <t-icon name="chevron-right" class="breadcrumb-separator" />
              <span class="breadcrumb-current">{{ $t('knowledgeEditor.document.title') }}</span>
            </h2>
            <!-- 身份与最后更新：紧凑单行，置于标题行右侧，悬停显示权限说明 -->
            <div v-if="kbInfo" class="kb-access-meta">
              <t-tooltip :content="accessPermissionSummary" placement="top">
                <span class="kb-access-meta-inner">
                  <t-tag size="small" :theme="isOwner ? 'success' : (effectiveKBPermission === 'admin' ? 'primary' : effectiveKBPermission === 'editor' ? 'warning' : 'default')" class="kb-access-role-tag">
                    {{ accessRoleLabel }}
                  </t-tag>
                  <template v-if="currentSharedKb">
                    <span class="kb-access-meta-sep">·</span>
                    <span class="kb-access-meta-text">
                      {{ $t('knowledgeBase.accessInfo.fromOrg') }}「{{ currentSharedKb.org_name }}」
                      {{ $t('knowledgeBase.accessInfo.sharedAt') }} {{ formatStringDate(new Date(currentSharedKb.shared_at)) }}
                    </span>
                  </template>
                  <template v-else-if="effectiveKBPermission">
                    <span class="kb-access-meta-sep">·</span>
                    <span class="kb-access-meta-text">{{ $t('knowledgeList.detail.sourceTypeAgent') }}</span>
                  </template>
                  <template v-else-if="kbLastUpdated">
                    <span class="kb-access-meta-sep">·</span>
                    <span class="kb-access-meta-text">{{ $t('knowledgeBase.accessInfo.lastUpdated') }} {{ kbLastUpdated }}</span>
                  </template>
                </span>
              </t-tooltip>
            </div>
            <t-tooltip v-if="canManage" :content="$t('knowledgeBase.settings')" placement="top">
              <button
                type="button"
                class="kb-settings-button"
                :disabled="!kbId"
                @click="handleOpenKBSettings"
              >
                <t-icon name="setting" size="16px" />
              </button>
            </t-tooltip>
          </div>
          <p class="document-subtitle">{{ $t('knowledgeEditor.document.subtitle') }}</p>
          <p v-if="unsupportedFileTypes.length" class="parser-hint" @click="goToParserSettings">
            <t-icon name="info-circle" class="parser-hint-icon" />
            <span>{{ $t('knowledgeBase.unsupportedTypesHint', { types: unsupportedFileTypes.map(t => '.' + t).join('、') }) }}</span>
            <span class="parser-hint-link">{{ $t('knowledgeBase.goToParserSettings') }} →</span>
          </p>
          <p v-if="missingStorageEngine" class="storage-engine-warning" @click="handleOpenKBSettings">
            <t-icon name="info-circle" class="warning-icon" />
            <span>{{ $t('knowledgeBase.missingStorageEngine') }}</span>
            <span class="warning-link">{{ $t('knowledgeBase.goToStorageSettings') }} →</span>
          </p>
        </div>
      </div>
      
      <input
        ref="uploadInputRef"
        type="file"
        class="document-upload-input"
          :accept="acceptFileTypes || '.pdf,.docx,.doc,.txt,.md,.json,.jpg,.jpeg,.png,.csv,.xlsx,.xls,.pptx,.ppt,.mp3,.wav,.m4a,.flac,.ogg,.mp4,.mov,.avi,.mkv,.webm,.wmv,.flv'"
        multiple
        @change="handleDocumentUpload"
      />
      <input
        ref="folderUploadInputRef"
        type="file"
        class="document-upload-input"
        webkitdirectory
        @change="handleFolderUpload"
      />
      <div class="knowledge-main">
        <aside class="tag-sidebar">
          <div class="sidebar-header">
            <div class="sidebar-title">
              <span>{{ $t('knowledgeBase.documentCategoryTitle') }}</span>
              <span class="sidebar-count">({{ sidebarCategoryCount }})</span>
            </div>
            <div v-if="canEdit" class="sidebar-actions">
              <t-button
                size="small"
                variant="text"
                class="create-tag-btn"
                :aria-label="$t('knowledgeBase.tagCreateAction')"
                :title="$t('knowledgeBase.tagCreateAction')"
                @click="startCreateTag"
              >
                <span class="create-tag-plus" aria-hidden="true">+</span>
              </t-button>
            </div>
          </div>
          <div class="tag-search-bar">
            <t-input
              v-model.trim="tagSearchQuery"
              size="small"
              :placeholder="$t('knowledgeBase.tagSearchPlaceholder')"
              clearable
            >
              <template #prefix-icon>
                <t-icon name="search" size="14px" />
              </template>
            </t-input>
          </div>
          <t-loading :loading="tagLoading" size="small">
            <div class="tag-list">
              <div v-if="creatingTag" class="tag-list-item tag-editing" @click.stop>
                <div class="tag-list-left">
                  <t-icon name="folder" size="18px" />
                  <div class="tag-edit-input">
                    <t-input
                      ref="newTagInputRef"
                      v-model="newTagName"
                      size="small"
                      :maxlength="40"
                      :placeholder="$t('knowledgeBase.tagNamePlaceholder')"
                      @keydown.enter.stop.prevent="submitCreateTag"
                      @keydown.esc.stop.prevent="cancelCreateTag"
                    />
                  </div>
                </div>
                <div class="tag-inline-actions">
                  <t-button
                    variant="text"
                    theme="default"
                    size="small"
                    class="tag-action-btn confirm"
                    :loading="creatingTagLoading"
                    @click.stop="submitCreateTag"
                  >
                    <t-icon name="check" size="16px" />
                  </t-button>
                  <t-button
                    variant="text"
                    theme="default"
                    size="small"
                    class="tag-action-btn cancel"
                    @click.stop="cancelCreateTag"
                  >
                    <t-icon name="close" size="16px" />
                  </t-button>
                </div>
              </div>

              <template v-if="filteredTags.length">
                <div
                  v-for="tag in filteredTags"
                  :key="tag.id"
                  class="tag-list-item"
                  :class="{ active: selectedTagId === tag.id, editing: editingTagId === tag.id }"
                  @click="handleTagRowClick(tag.id)"
                >
                  <div class="tag-list-left">
                    <t-icon name="folder" size="18px" />
                    <template v-if="editingTagId === tag.id">
                      <div class="tag-edit-input" @click.stop>
                        <t-input
                          :ref="setEditingTagInputRefByTag(tag.id)"
                          v-model="editingTagName"
                          size="small"
                          :maxlength="40"
                          @keydown.enter.stop.prevent="submitEditTag"
                          @keydown.esc.stop.prevent="cancelEditTag"
                        />
                      </div>
                    </template>
                    <template v-else>
                      <span class="tag-name" :title="tag.name">{{ tag.name }}</span>
                    </template>
                  </div>
                  <div class="tag-list-right">
                    <span class="tag-count">{{ tag.knowledge_count || 0 }}</span>
                    <template v-if="editingTagId === tag.id">
                      <div class="tag-inline-actions" @click.stop>
                        <t-button
                          variant="text"
                          theme="default"
                          size="small"
                          class="tag-action-btn confirm"
                          :loading="editingTagSubmitting"
                          @click.stop="submitEditTag"
                        >
                          <t-icon name="check" size="16px" />
                        </t-button>
                        <t-button
                          variant="text"
                          theme="default"
                          size="small"
                          class="tag-action-btn cancel"
                          @click.stop="cancelEditTag"
                        >
                          <t-icon name="close" size="16px" />
                        </t-button>
                      </div>
                    </template>
                    <template v-else>
                      <div v-if="canEdit" class="tag-more" @click.stop>
                        <t-popup trigger="click" placement="top-right" overlayClassName="tag-more-popup">
                          <div class="tag-more-btn">
                            <t-icon name="more" size="14px" />
                          </div>
                          <template #content>
                            <div class="tag-menu">
                              <div class="tag-menu-item" @click="startEditTag(tag)">
                                <t-icon class="menu-icon" name="edit" />
                                <span>{{ $t('knowledgeBase.tagEditAction') }}</span>
                              </div>
                              <div class="tag-menu-item danger" @click="confirmDeleteTag(tag)">
                                <t-icon class="menu-icon" name="delete" />
                                <span>{{ $t('knowledgeBase.tagDeleteAction') }}</span>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                    </template>
                  </div>
                </div>
              </template>
              <div v-else class="tag-empty-state">
                {{ $t('knowledgeBase.tagEmptyResult') }}
              </div>
              <div v-if="tagHasMore" class="tag-load-more">
                <t-button
                  variant="text"
                  size="small"
                  :loading="tagLoadingMore"
                  @click.stop="kbId && loadTags(kbId)"
                >
                  {{ $t('tenant.loadMore') }}
                </t-button>
              </div>
            </div>
          </t-loading>
        </aside>
        <div class="tag-content">
          <div class="doc-card-area">
            <!-- 搜索栏、筛选与添加文档 -->
            <div class="doc-filter-bar">
              <t-input
                v-model.trim="docSearchKeyword"
                :placeholder="$t('knowledgeBase.docSearchPlaceholder')"
                clearable
                class="doc-search-input"
                @clear="loadKnowledgeFiles(kbId)"
                @keydown.enter="loadKnowledgeFiles(kbId)"
              >
                <template #prefix-icon>
                  <t-icon name="search" size="16px" />
                </template>
              </t-input>
              <t-select
                v-model="selectedFileType"
                :options="fileTypeOptions"
                :placeholder="$t('knowledgeBase.fileTypeFilter')"
                class="doc-type-select"
                clearable
              />
              <div v-if="canEdit" class="doc-filter-actions">
                <t-tooltip :content="$t('knowledgeBase.addDocument')" placement="top">
                  <t-dropdown
                    :options="documentActionOptions"
                    trigger="click"
                    placement="bottom-right"
                    @click="handleDocumentActionSelect"
                  >
                    <t-button variant="text" theme="default" class="content-bar-icon-btn" size="small">
                      <template #icon><t-icon name="file-add" size="16px" /></template>
                    </t-button>
                  </t-dropdown>
                </t-tooltip>
              </div>
            </div>
            <div
              class="doc-scroll-container"
              :class="{ 'is-empty': !cardList.length }"
              ref="knowledgeScroll"
              @scroll="handleScroll"
            >
              <template v-if="cardList.length">
                <div class="doc-card-list">
                  <!-- 现有文档卡片 -->
                  <div
                    class="knowledge-card"
                    v-for="(item, index) in cardList"
                    :key="index"
                    @click="openCardDetails(item)"
                    @mouseenter="onCardMouseEnter($event, item)"
                    @mousemove="onCardMouseMove($event)"
                    @mouseleave="onCardMouseLeave"
                  >
                    <div class="card-content">
                      <div class="card-content-nav">
                        <span class="card-content-title" :title="item.file_name">{{ item.file_name }}</span>
                        <t-popup
                          v-if="canEdit"
                          v-model="item.isMore"
                          overlayClassName="card-more"
                          :on-visible-change="onVisibleChange"
                          trigger="click"
                          destroy-on-close
                          placement="bottom-right"
                        >
                          <div
                            variant="outline"
                            class="more-wrap"
                            @click.stop="openMore(index)"
                            :class="[moreIndex == index ? 'active-more' : '']"
                          >
                            <img class="more-icon" src="@/assets/img/more.png" alt="" />
                          </div>
                          <template #content>
                            <!-- Normal menu -->
                            <div v-if="moveMenuMode === 'normal'" class="card-menu">
                              <div
                                v-if="item.type === 'manual'"
                                class="card-menu-item"
                                @click.stop="handleManualEdit(index, item)"
                              >
                                <t-icon class="icon" name="edit" />
                                <span>{{ t('knowledgeBase.editDocument') }}</span>
                              </div>
                              <div class="card-menu-item" @click.stop="handleKnowledgeReparse(index, item)">
                                <t-icon class="icon" name="refresh" />
                                <span>{{ t('knowledgeBase.rebuildDocument') }}</span>
                              </div>
                              <div class="card-menu-item" @click.stop="handleMoveKnowledge(item)">
                                <t-icon class="icon" name="swap" />
                                <span>{{ t('knowledgeBase.moveDocument') }}</span>
                              </div>
                              <div class="card-menu-item danger" @click.stop="delCard(index, item)">
                                <t-icon class="icon" name="delete" />
                                <span>{{ t('knowledgeBase.deleteDocument') }}</span>
                              </div>
                            </div>

                            <!-- Move: target KB list -->
                            <div v-else-if="moveMenuMode === 'targets'" class="card-menu move-menu">
                              <div class="move-menu-header" @click.stop="handleMoveBack">
                                <t-icon name="chevron-left" size="16px" />
                                <span>{{ t('knowledgeBase.moveToKnowledgeBase') }}</span>
                              </div>
                              <div v-if="moveTargetsLoading" class="move-menu-loading">
                                <t-loading size="small" />
                              </div>
                              <div v-else-if="moveTargetKbs.length === 0" class="move-menu-empty">
                                {{ t('knowledgeBase.moveNoTargets') }}
                              </div>
                              <template v-else>
                                <div
                                  v-for="kb in moveTargetKbs"
                                  :key="kb.id"
                                  class="card-menu-item"
                                  @click.stop="handleMoveSelectTarget(kb)"
                                >
                                  <t-icon class="icon" name="root-list" />
                                  <span class="move-target-name">{{ kb.name }}</span>
                                  <span v-if="kb.knowledge_count !== undefined" class="move-target-count">{{ kb.knowledge_count }}</span>
                                </div>
                              </template>
                            </div>

                            <!-- Move: confirm with mode selection -->
                            <div v-else-if="moveMenuMode === 'confirm'" class="card-menu move-menu">
                              <div class="move-menu-header" @click.stop="handleMoveBack">
                                <t-icon name="chevron-left" size="16px" />
                                <span>{{ t('knowledgeBase.moveConfirmTitle') }}</span>
                              </div>
                              <div class="move-confirm-body">
                                <div class="move-target-info">
                                  <t-icon name="arrow-right" size="14px" />
                                  <span>{{ moveSelectedTargetName }}</span>
                                </div>
                                <div
                                  class="move-mode-item"
                                  :class="{ active: moveMode === 'reuse_vectors' }"
                                  @click.stop="moveMode = 'reuse_vectors'"
                                >
                                  <t-radio :checked="moveMode === 'reuse_vectors'" />
                                  <div class="move-mode-text">
                                    <span class="move-mode-label">{{ t('knowledgeBase.moveModeReuseVectors') }}</span>
                                    <span class="move-mode-desc">{{ t('knowledgeBase.moveModeReuseVectorsDesc') }}</span>
                                  </div>
                                </div>
                                <div
                                  class="move-mode-item"
                                  :class="{ active: moveMode === 'reparse' }"
                                  @click.stop="moveMode = 'reparse'"
                                >
                                  <t-radio :checked="moveMode === 'reparse'" />
                                  <div class="move-mode-text">
                                    <span class="move-mode-label">{{ t('knowledgeBase.moveModeReparse') }}</span>
                                    <span class="move-mode-desc">{{ t('knowledgeBase.moveModeReparseDesc') }}</span>
                                  </div>
                                </div>
                                <div class="move-confirm-actions">
                                  <t-button size="small" variant="outline" @click.stop="handleMoveBack">{{ t('common.cancel') }}</t-button>
                                  <t-button size="small" theme="primary" :loading="moveSubmitting" @click.stop="handleMoveConfirm">{{ t('knowledgeBase.moveConfirm') }}</t-button>
                                </div>
                              </div>
                            </div>
                          </template>
                        </t-popup>
                      </div>
                      <div
                        v-if="item.parse_status === 'processing' || item.parse_status === 'pending'"
                        class="card-analyze"
                      >
                        <t-icon name="loading" class="card-analyze-loading"></t-icon>
                        <span class="card-analyze-txt">{{ t('knowledgeBase.parsingInProgress') }}</span>
                      </div>
                      <div v-else-if="item.parse_status === 'failed'" class="card-analyze failure">
                        <t-icon name="close-circle" class="card-analyze-loading failure"></t-icon>
                        <span class="card-analyze-txt failure">{{ t('knowledgeBase.parsingFailed') }}</span>
                      </div>
                      <div v-else-if="item.parse_status === 'draft'" class="card-draft">
                        <t-tag size="small" theme="warning" variant="light-outline">{{ t('knowledgeBase.draft') }}</t-tag>
                        <span class="card-draft-tip">{{ t('knowledgeBase.draftTip') }}</span>
                      </div>
                      <div 
                        v-else-if="item.parse_status === 'completed' && (item.summary_status === 'pending' || item.summary_status === 'processing')" 
                        class="card-analyze"
                      >
                        <t-icon name="loading" class="card-analyze-loading"></t-icon>
                        <span class="card-analyze-txt">{{ t('knowledgeBase.generatingSummary') }}</span>
                      </div>
                      <div v-else-if="item.parse_status === 'completed'" class="card-content-txt">
                        {{ item.description }}
                      </div>
                    </div>
                    <div class="card-bottom">
                      <span class="card-time">{{ formatDocTime(item.updated_at) }}</span>
                      <div class="card-bottom-right">
                        <div v-if="tagList.length" class="card-tag-selector" @click.stop>
                          <t-dropdown
                            v-if="canEdit"
                            :options="tagDropdownOptions"
                            trigger="click"
                            @click="(data: any) => handleKnowledgeTagChange(item.id, data.value as string)"
                          >
                            <t-tag size="small" variant="light-outline">
                              <span class="tag-text">{{ getTagName(item.tag_id) }}</span>
                            </t-tag>
                          </t-dropdown>
                          <t-tag v-else size="small" variant="light-outline">
                            <span class="tag-text">{{ getTagName(item.tag_id) }}</span>
                          </t-tag>
                        </div>
                        <div class="card-type">
                          <span>{{ getKnowledgeType(item) }}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
                <!-- 悬停卡片时跟随鼠标的详情气泡 -->
                <Teleport to="body">
                  <div
                    v-show="hoveredCardItem"
                    class="knowledge-card-hover-popover"
                    :style="{ left: cardPopoverPos.x + 'px', top: cardPopoverPos.y + 'px' }"
                  >
                    <template v-if="hoveredCardItem">
                      <div class="card-popover-title">{{ hoveredCardItem.file_name }}</div>
                      <div v-if="hoveredCardItem.parse_status === 'processing' || hoveredCardItem.parse_status === 'pending'" class="card-popover-status parsing">
                        <t-icon name="loading" size="14px" /> {{ t('knowledgeBase.parsingInProgress') }}
                      </div>
                      <div v-else-if="hoveredCardItem.parse_status === 'failed'" class="card-popover-status failure">
                        <t-icon name="close-circle" size="14px" /> {{ t('knowledgeBase.parsingFailed') }}
                        <span v-if="(hoveredCardItem as any).error_message" class="card-popover-error-msg">{{ (hoveredCardItem as any).error_message }}</span>
                      </div>
                      <div v-else-if="hoveredCardItem.parse_status === 'draft'" class="card-popover-status draft">
                        {{ t('knowledgeBase.draft') }}
                      </div>
                      <template v-else>
                        <div v-if="hoveredCardItem.description" class="card-popover-desc">{{ hoveredCardItem.description }}</div>
                        <div v-if="(hoveredCardItem as any).source" class="card-popover-source" :title="(hoveredCardItem as any).source">
                          <t-icon name="link" size="12px" /> {{ (hoveredCardItem as any).source }}
                        </div>
                        <div class="card-popover-extra">
                          <span v-if="(hoveredCardItem as any).created_at" class="card-popover-created">
                            {{ t('knowledgeBase.createdAt') }}：{{ formatDocTime((hoveredCardItem as any).created_at) }}
                          </span>
                          <span v-if="formatFileSize((hoveredCardItem as any).file_size)" class="card-popover-size">
                            {{ formatFileSize((hoveredCardItem as any).file_size) }}
                          </span>
                        </div>
                      </template>
                      <div class="card-popover-meta">
                        <span class="card-popover-time">{{ t('knowledgeBase.updatedAt') }}：{{ formatDocTime(hoveredCardItem.updated_at) }}</span>
                        <span v-if="(hoveredCardItem as any).channel && (hoveredCardItem as any).channel !== 'web'" class="card-popover-channel">{{ getChannelLabel((hoveredCardItem as any).channel) }}</span>
                        <span v-if="getTagName(hoveredCardItem.tag_id)" class="card-popover-tag">{{ getTagName(hoveredCardItem.tag_id) }}</span>
                        <span class="card-popover-type">{{ getKnowledgeType(hoveredCardItem) }}</span>
                      </div>
                      <div class="card-popover-hint">{{ t('knowledgeBase.clickToViewFull') }}</div>
                    </template>
                  </div>
                </Teleport>
              </template>
              <template v-else>
                <div class="doc-empty-state">
                  <EmptyKnowledge />
                </div>
              </template>
            </div>
          </div>
          <t-dialog
            v-model:visible="delDialog"
            dialogClassName="del-knowledge"
            :closeBtn="false"
            :cancelBtn="null"
            :confirmBtn="null"
          >
            <div class="circle-wrap">
              <div class="header">
                <img class="circle-img" src="@/assets/img/circle.png" alt="" />
                <span class="circle-title">{{ t('knowledgeBase.deleteConfirmation') }}</span>
              </div>
              <span class="del-circle-txt">
                {{ t('knowledgeBase.confirmDeleteDocument', { fileName: knowledge.file_name || '' }) }}
              </span>
              <div class="circle-btn">
                <span class="circle-btn-txt" @click="delDialog = false">{{ t('common.cancel') }}</span>
                <span class="circle-btn-txt confirm" @click="delCardConfirm">
                  {{ t('knowledgeBase.confirmDelete') }}
                </span>
              </div>
            </div>
          </t-dialog>

          <!-- 重建知识确认弹窗 -->
          <t-dialog
            v-model:visible="rebuildDialog"
            dialogClassName="del-knowledge"
            :closeBtn="false"
            :cancelBtn="null"
            :confirmBtn="null"
          >
            <div class="circle-wrap">
              <div class="header">
                <img class="circle-img" src="@/assets/img/circle.png" alt="" />
                <span class="circle-title">{{ t('knowledgeBase.rebuildDocument') }}</span>
              </div>
              <span class="del-circle-txt">
                {{ t('knowledgeBase.rebuildConfirm', { fileName: rebuildKnowledgeItem.file_name || rebuildKnowledgeItem.title || '' }) }}
              </span>
              <div class="circle-btn">
                <span class="circle-btn-txt" @click="rebuildDialog = false">{{ t('common.cancel') }}</span>
                <span class="circle-btn-txt confirm" @click="rebuildConfirm">
                  {{ t('common.confirm') }}
                </span>
              </div>
            </div>
          </t-dialog>

          <!-- URL 导入对话框 -->
          <t-dialog
            v-model:visible="urlDialogVisible"
            :header="$t('knowledgeBase.importURLTitle')"
            :confirm-btn="{
              content: $t('common.confirm'),
              theme: 'primary',
              loading: urlImporting,
            }"
            :cancel-btn="{ content: $t('common.cancel') }"
            @confirm="handleURLImportConfirm"
            @cancel="handleURLImportCancel"
            width="500px"
          >
            <div class="url-import-form">
              <div class="url-input-label">{{ $t('knowledgeBase.urlLabel') }}</div>
              <t-input
                v-model="urlInputValue"
                :placeholder="$t('knowledgeBase.urlPlaceholder')"
                clearable
                autofocus
                @keydown.enter="handleURLImportConfirm"
              />
              <div class="url-input-tip">{{ $t('knowledgeBase.urlTip') }}</div>
            </div>
          </t-dialog>
          
          <DocContent :visible="isCardDetails" :details="details" @closeDoc="closeDoc" @getDoc="getDoc"></DocContent>
        </div>
      </div>
    </div>
  </template>
  <template v-else>
    <div class="faq-manager-wrapper">
      <FAQEntryManager v-if="kbId" :kb-id="kbId" />
    </div>
  </template>

  <!-- 知识库编辑器（创建/编辑统一组件） -->
  <KnowledgeBaseEditorModal 
    :visible="uiStore.showKBEditorModal"
    :mode="uiStore.kbEditorMode"
    :kb-id="uiStore.currentKBId || undefined"
    :initial-type="uiStore.kbEditorType"
    @update:visible="(val) => val ? null : uiStore.closeKBEditor()"
    @success="handleKBEditorSuccess"
  />
</template>
<style>
/* 下拉菜单容器样式已统一至 @/assets/dropdown-menu.less */
</style>
<style scoped lang="less">
.knowledge-layout {
  display: flex;
  flex-direction: column;
  margin: 0 16px 0 4px;
  gap: 20px;
  height: 100%;
  flex: 1;
  width: 100%;
  min-width: 0;
  padding: 24px 32px 32px;
  box-sizing: border-box;
}

// 与列表页一致：浅灰底圆角区，左侧筛选为白底卡片
.knowledge-main {
  display: flex;
  flex: 1;
  min-height: 0;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  overflow: hidden;
}

// 与列表页筛选区一致：白底卡片感、细分界
.tag-sidebar {
  width: 200px;
  background: var(--td-bg-color-container);
  border-right: 1px solid var(--td-component-stroke);
  box-shadow: 2px 0 8px rgba(0, 0, 0, 0.04);
  padding: 16px;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
  max-height: 100%;
  min-height: 0;

  .sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
    color: var(--td-text-color-primary);

    .sidebar-title {
      display: flex;
      align-items: baseline;
      gap: 4px;
      font-size: 13px;
      font-weight: 600;

      .sidebar-count {
        font-size: 12px;
        color: var(--td-text-color-secondary);
      }
    }

    .sidebar-actions {
      display: flex;
      gap: 6px;
      color: var(--td-text-color-placeholder);
      align-items: center;

      .create-tag-btn {
        width: 24px;
        height: 24px;
        padding: 0;
        border-radius: 6px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 16px;
        font-weight: 600;
        color: var(--td-success-color);
        line-height: 1;
        transition: background 0.2s ease, color 0.2s ease;

        &:hover {
          background: var(--td-bg-color-secondarycontainer);
          color: var(--td-brand-color-active);
        }
      }

      .create-tag-plus {
        line-height: 1;
      }
    }
  }

  .tag-search-bar {
    margin-bottom: 10px;

    :deep(.t-input) {
      font-size: 13px;
      background-color: var(--td-bg-color-container);
      border-color: var(--td-component-stroke);
      border-radius: 6px;
    }
  }

  .tag-list {
    display: flex;
    flex-direction: column;
    gap: 5px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
    scrollbar-width: none;

    &::-webkit-scrollbar {
      display: none;
    }

    .tag-load-more {
      padding: 8px 0 0;
      display: flex;
      justify-content: center;

      :deep(.t-button) {
        padding: 0;
        font-size: 12px;
        color: var(--td-success-color);
      }
    }

    .tag-list-item {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 9px 12px;
      border-radius: 6px;
      color: var(--td-text-color-primary);
      cursor: pointer;
      transition: all 0.2s ease;
      font-family: "PingFang SC", -apple-system, BlinkMacSystemFont, sans-serif;
      font-size: 14px;
      -webkit-font-smoothing: antialiased;

      .tag-list-left {
        display: flex;
        align-items: center;
        gap: 8px;
        min-width: 0;
        flex: 1;

        .t-icon {
          flex-shrink: 0;
          color: var(--td-text-color-secondary);
          font-size: 14px;
          transition: color 0.2s ease;
        }
      }

      .tag-name {
        flex: 1;
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        font-family: "PingFang SC", -apple-system, BlinkMacSystemFont, sans-serif;
        font-size: 14px;
        font-weight: 450;
        line-height: 1.4;
        letter-spacing: 0.01em;
      }

      .tag-list-right {
        display: flex;
        align-items: center;
        gap: 6px;
        margin-left: 8px;
        flex-shrink: 0;
      }

      .tag-count {
        font-size: 12px;
        color: var(--td-text-color-secondary);
        font-weight: 500;
        min-width: 28px;
        padding: 3px 7px;
        border-radius: 8px;
        background: var(--td-bg-color-secondarycontainer);
        transition: all 0.2s ease;
        text-align: center;
        box-sizing: border-box;
      }

      &:hover {
        background: var(--td-bg-color-secondarycontainer);
        color: var(--td-text-color-primary);

        .tag-list-left .t-icon {
          color: var(--td-text-color-primary);
        }

        .tag-count {
          background: var(--td-bg-color-secondarycontainer);
          color: var(--td-text-color-primary);
        }
      }

      &.active {
        background: var(--td-success-color-light);
        color: var(--td-brand-color);
        font-weight: 500;

        .tag-list-left .t-icon {
          color: var(--td-brand-color);
        }

        .tag-name {
          font-weight: 500;
        }

        .tag-count {
          background: var(--td-success-color-light);
          color: var(--td-brand-color);
          font-weight: 600;
        }
      }

      &.editing {
        background: transparent;
        border: none;
      }

      &.tag-editing {
        cursor: default;
        padding-right: 8px;
        background: transparent;
        border: none;

        .tag-edit-input {
          flex: 1;
        }
      }

      &.tag-editing .tag-edit-input {
        width: 100%;
      }

      .tag-inline-actions {
        display: flex;
        gap: 4px;
        margin-left: auto;

        :deep(.t-button) {
          padding: 0 4px;
          height: 24px;
        }

        :deep(.tag-action-btn) {
          border-radius: 4px;
          transition: all 0.2s ease;

          .t-icon {
            font-size: 14px;
          }
        }

        :deep(.tag-action-btn.confirm) {
          background: var(--td-success-color-light);
          color: var(--td-brand-color-active);

          &:hover {
            background: var(--td-success-color-light);
            color: var(--td-success-color);
          }
        }

        :deep(.tag-action-btn.cancel) {
          background: var(--td-bg-color-secondarycontainer);
          color: var(--td-text-color-secondary);

          &:hover {
            background: var(--td-bg-color-secondarycontainer);
            color: var(--td-text-color-secondary);
          }
        }
      }

      .tag-edit-input {
        flex: 1;
        min-width: 0;
        max-width: 100%;

        :deep(.t-input) {
          font-size: 12px;
          background-color: transparent;
          border: none;
          border-bottom: 1px solid var(--td-component-stroke);
          border-radius: 0;
          box-shadow: none;
          padding-left: 0;
          padding-right: 0;
        }

        :deep(.t-input__wrap) {
          background-color: transparent;
          border: none;
          border-bottom: 1px solid var(--td-component-stroke);
          border-radius: 0;
          box-shadow: none;
        }

        :deep(.t-input__inner) {
          padding-left: 0;
          padding-right: 0;
          color: var(--td-text-color-primary);
          caret-color: var(--td-text-color-primary);
        }

        :deep(.t-input:hover),
        :deep(.t-input.t-is-focused),
        :deep(.t-input__wrap:hover),
        :deep(.t-input__wrap.t-is-focused) {
          border-bottom-color: var(--td-success-color);
        }
      }

      .tag-more {
        display: flex;
        align-items: center;
        opacity: 0;
        transition: opacity 0.2s ease;
      }

      &:hover .tag-more {
        opacity: 1;
      }

      .tag-more-btn {
        width: 22px;
        height: 22px;
        display: flex;
        align-items: center;
        justify-content: center;
        border-radius: 4px;
        color: var(--td-text-color-secondary);
        transition: all 0.2s ease;

        &:hover {
          background: var(--td-bg-color-secondarycontainer);
          color: var(--td-text-color-secondary);
        }
      }
    }

    .tag-empty-state {
      text-align: center;
      padding: 10px 6px;
      color: var(--td-text-color-placeholder);
      font-size: 11px;
    }
  }
}

:deep(.tag-menu) {
  display: flex;
  flex-direction: column;
}

:deep(.tag-menu-item) {
  display: flex;
  align-items: center;
  padding: 8px 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  color: var(--td-text-color-primary);
  font-family: 'PingFang SC';
  font-size: 14px;
  font-weight: 400;

  .menu-icon {
    margin-right: 8px;
    font-size: 16px;
  }

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }

  &.danger {
    color: var(--td-text-color-primary);

    &:hover {
      background: var(--td-error-color-light);
      color: var(--td-error-color);

      .menu-icon {
        color: var(--td-error-color);
      }
    }
  }
}

.tag-content {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
  padding: 12px;
  overflow: hidden;
  background: var(--td-bg-color-container);
}

.doc-card-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.doc-filter-bar {
  padding: 0 0 12px 0;
  flex-shrink: 0;
  display: flex;
  gap: 12px;
  align-items: center;

  .doc-search-input {
    flex: 1;
    min-width: 0;
  }

  .doc-type-select {
    width: 140px;
    flex-shrink: 0;
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
    background-color: var(--td-bg-color-container);
    border-color: var(--td-component-stroke);
    border-radius: 6px;

    &:hover,
    &:focus,
    &.t-is-focused {
      border-color: var(--td-brand-color);
      background-color: var(--td-bg-color-container);
    }
  }

  :deep(.t-select) {
    .t-input {
      font-size: 13px;
      background-color: var(--td-bg-color-container);
      border-color: var(--td-component-stroke);
      border-radius: 6px;

      &:hover,
      &.t-is-focused {
        border-color: var(--td-brand-color);
        background-color: var(--td-bg-color-container);
      }
    }
  }
}

.doc-scroll-container {
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

  .kb-access-meta {
    margin-left: auto;
    flex-shrink: 0;
  }

  .kb-access-meta-inner {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--td-text-color-secondary);
    cursor: default;
  }

  .kb-access-role-tag {
    flex-shrink: 0;
  }

  .kb-access-meta-sep {
    color: var(--td-text-color-placeholder);
    user-select: none;
  }

  .kb-access-meta-text {
    white-space: nowrap;
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
    font-family: "PingFang SC";
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }

  .document-subtitle {
    margin: 0;
    color: var(--td-text-color-placeholder);
    font-family: "PingFang SC";
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

  :deep(.t-tag) {
    cursor: pointer;
    max-width: 160px;
    border-radius: 999px;
    border-color: var(--td-component-stroke);
    color: var(--td-text-color-primary);
    padding: 0 10px;
    background: var(--td-bg-color-secondarycontainer);
    transition: all 0.2s ease;

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color-active);
      background: var(--td-success-color-light);
    }
  }

  .tag-text {
    display: inline-block;
    max-width: 110px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    vertical-align: middle;
    font-size: 12px;
  }
}

.card-bottom-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.faq-manager-wrapper {
  flex: 1;
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

.doc-card-list {
  box-sizing: border-box;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(248px, 1fr));
  gap: 14px;
  align-content: flex-start;
  width: 100%;
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


:deep(.del-knowledge) {
  padding: 0px !important;
  border-radius: 6px !important;

  .t-dialog__header {
    display: none;
  }

  .t-dialog__body {
    padding: 16px;
  }

  .t-dialog__footer {
    padding: 0;
  }
}

:deep(.t-dialog__position.t-dialog--top) {
  padding-top: 40vh !important;
}

.circle-wrap {
  .header {
    display: flex;
    align-items: center;
    margin-bottom: 8px;
  }

  .circle-img {
    width: 20px;
    height: 20px;
    margin-right: 8px;
  }

  .circle-title {
    color: var(--td-text-color-primary);
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
  }

  .del-circle-txt {
    color: var(--td-text-color-placeholder);
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    display: inline-block;
    margin-left: 29px;
    margin-bottom: 21px;
  }

  .circle-btn {
    height: 22px;
    width: 100%;
    display: flex;
    justify-content: end;
  }

  .circle-btn-txt {
    color: var(--td-text-color-primary);
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    cursor: pointer;
  }

  .confirm {
    color: var(--td-error-color);
    margin-left: 40px;
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
}

.card-draft-tip {
  color: var(--td-warning-color);
  font-size: 11px;
}

.knowledge-card {
  min-width: 248px;
  border: 1px solid var(--td-component-stroke);
  height: 148px;
  border-radius: 9px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  background: var(--td-bg-color-container);
  position: relative;
  cursor: pointer;
  transition: border-color 0.2s ease, box-shadow 0.2s ease;

  .card-content {
    padding: 15px 17px 13px;
  }

  .card-analyze {
    height: 52px;
    display: flex;
  }

  .card-analyze-loading {
    display: block;
    color: var(--td-brand-color);
    font-size: 14px;
    margin-top: 2px;
  }

  .card-analyze-txt {
    color: var(--td-brand-color);
    font-family: "PingFang SC";
    font-size: 11px;
    margin-left: 8px;
  }

  .failure {
    color: var(--td-error-color);
  }

  .card-content-nav {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 11px;
    gap: 8px;
  }

  .card-content-title {
    flex: 1;
    min-width: 0;
    height: 29px;
    line-height: 29px;
    display: inline-block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--td-text-color-primary);
    font-family: "PingFang SC", -apple-system, sans-serif;
    font-size: 15px;
    font-weight: 600;
    letter-spacing: 0.01em;
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
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
    color: var(--td-text-color-secondary);
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 400;
    line-height: 19px;
  }

  .card-bottom {
    position: absolute;
    bottom: 0;
    padding: 0 17px;
    box-sizing: border-box;
    height: 34px;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: var(--td-bg-color-container);
    border-top: 1px solid var(--td-component-stroke);
  }

  .card-time {
    color: var(--td-text-color-secondary);
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 400;
  }

  .card-type {
    color: var(--td-text-color-secondary);
    font-family: "PingFang SC";
    font-size: 11px;
    font-weight: 500;
    padding: 3px 8px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 4px;
  }
}

.knowledge-card:hover {
  border-color: var(--td-brand-color);
  box-shadow: 0 2px 8px rgba(7, 192, 95, 0.12);
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
  font-family: "PingFang SC", -apple-system, sans-serif;
  transition: opacity 0.15s ease;

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

  .card-popover-tag {
    padding: 1px 6px;
    background: var(--td-success-color-light);
    color: var(--td-brand-color);
    border-radius: 4px;
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
  font-family: "PingFang SC";
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
  font-family: "PingFang SC";
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
