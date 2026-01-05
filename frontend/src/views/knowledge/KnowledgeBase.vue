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
import KnowledgeBaseEditorModal from './KnowledgeBaseEditorModal.vue';
const usemenuStore = useMenuStore();
const uiStore = useUIStore();
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
} from "@/api/knowledge-base/index";
import FAQEntryManager from './components/FAQEntryManager.vue';
import { useI18n } from 'vue-i18n';
import { formatStringDate, kbFileTypeVerification } from '@/utils';
const route = useRoute();
const { t } = useI18n();
const kbId = computed(() => (route.params as any).kbId as string || '');
const kbInfo = ref<any>(null);
const uploadInputRef = ref<HTMLInputElement | null>(null);
const uploading = ref(false);
const kbLoading = ref(false);
const isFAQ = computed(() => (kbInfo.value?.type || '') === 'faq');
const knowledgeList = ref<Array<{ id: string; name: string; type?: string }>>([]);
let { cardList, total, moreIndex, details, getKnowled, delKnowledge, openMore, onVisibleChange, getCardDetails, getfDetails } = useKnowledgeBase(kbId.value)
let isCardDetails = ref(false);
let timeout: ReturnType<typeof setInterval> | null = null;
let delDialog = ref(false)
let knowledge = ref<KnowledgeCard>({ id: '', parse_status: '' })
let knowledgeIndex = ref(-1)
let knowledgeScroll = ref()
let page = 1;
let pageSize = 35;

const selectedTagId = ref<string>("__untagged__");
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
const UNTAGGED_TAG_ID = '__untagged__';
const fileTypeOptions = computed(() => [
  { content: t('knowledgeBase.allFileTypes') || '全部类型', value: '' },
  { content: 'PDF', value: 'pdf' },
  { content: 'DOCX', value: 'docx' },
  { content: 'DOC', value: 'doc' },
  { content: 'TXT', value: 'txt' },
  { content: 'MD', value: 'md' },
  { content: 'URL', value: 'url' },
  { content: t('knowledgeBase.typeManual') || '手动创建', value: 'manual' },
]);
type TagInputInstance = ComponentPublicInstance<{ focus: () => void; select: () => void }>;
const tagDropdownOptions = computed(() => {
  const options = [
    { content: t('knowledgeBase.untagged') || '未分类', value: UNTAGGED_TAG_ID },
    ...tagList.value
      .filter((tag: any) => tag.id !== UNTAGGED_TAG_ID)
      .map((tag: any) => ({
        content: tag.name,
        value: tag.id,
      })),
  ];
  return options;
});
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
  const itemHeight = 174;
  let itemsInView = Math.floor(viewportHeight / itemHeight) * 5;
  pageSize = Math.max(35, itemsInView);
}
getPageSize()
// 直接调用 API 获取知识库文件列表
const getTagName = (tagId?: string | number) => {
  if (!tagId && tagId !== 0) return t('knowledgeBase.untagged') || '未分类';
  if (tagId === UNTAGGED_TAG_ID) return t('knowledgeBase.untagged') || '未分类';
  const key = String(tagId);
  return tagMap.value[key]?.name || (t('knowledgeBase.untagged') || '未分类');
};

const formatDocTime = (time?: string) => {
  if (!time) return '--'
  const formatted = formatStringDate(new Date(time))
  return formatted.slice(2, 16) // "YY-MM-DD HH:mm"
}

// 获取知识条目的显示类型
const getKnowledgeType = (item: any) => {
  if (item.type === 'url') {
    return t('knowledgeBase.typeURL') || 'URL';
  }
  if (item.type === 'manual') {
    return t('knowledgeBase.typeManual') || '手动创建';
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
  page = 1;
  loadKnowledgeFiles(kbId.value);
};

const handleTagRowClick = (tagId: string) => {
  const normalizedId = String(tagId);
  if (editingTagId.value && editingTagId.value !== normalizedId) {
    editingTagId.value = null;
    editingTagName.value = '';
  }
  if (creatingTag.value) {
    creatingTag.value = false;
    newTagName.value = '';
  }
  if (selectedTagId.value === normalizedId) {
    return;
  }
  handleTagFilterChange(normalizedId);
};

const handleUntaggedClick = () => {
  if (creatingTag.value) {
    creatingTag.value = false;
    newTagName.value = '';
  }
  if (editingTagId.value) {
    editingTagId.value = null;
    editingTagName.value = '';
  }
  if (selectedTagId.value === UNTAGGED_TAG_ID) return;
  handleTagFilterChange(UNTAGGED_TAG_ID);
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
  const confirm = window.confirm(
    t('knowledgeBase.tagDeleteDesc', { name: tag.name }) as string,
  );
  if (!confirm) return;
  deleteKnowledgeBaseTag(kbId.value, tag.id, { force: true })
    .then(() => {
      MessagePlugin.success(t('knowledgeBase.tagDeleteSuccess'));
      if (selectedTagId.value === tag.id) {
        handleTagFilterChange(UNTAGGED_TAG_ID);
      }
      loadTags(kbId.value);
      loadKnowledgeFiles(kbId.value);
    })
    .catch((error: any) => {
      MessagePlugin.error(error?.message || t('common.operationFailed'));
    });
};

const handleKnowledgeTagChange = async (knowledgeId: string, tagValue: string) => {
  try {
    // When selecting "untagged", pass null to clear the tag
    const tagIdToUpdate = tagValue === UNTAGGED_TAG_ID ? null : (tagValue || null);
    await updateKnowledgeTagBatch({ updates: { [knowledgeId]: tagIdToUpdate } });
    MessagePlugin.success(t('knowledgeBase.tagUpdateSuccess') || '分类已更新');
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
    selectedTagId.value = UNTAGGED_TAG_ID;
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
    knowledgeList.value = (res?.data || []).map((item: any) => ({
      id: String(item.id),
      name: item.name,
      type: item.type || 'document',
    }));
  } catch (error) {
    console.error('Failed to load knowledge list:', error);
  }
};

// 监听路由参数变化，重新获取知识库内容
watch(() => kbId.value, (newKbId, oldKbId) => {
  if (newKbId && newKbId !== oldKbId) {
    tagSearchQuery.value = '';
    tagPage.value = 1;
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

onMounted(() => {
  loadKnowledgeBaseInfo(kbId.value);
  loadKnowledgeList();
  
  // 监听文件上传事件
  window.addEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  // 监听URL导入对话框打开事件
  window.addEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
});

onUnmounted(() => {
  window.removeEventListener('knowledgeFileUploaded', handleFileUploaded as EventListener);
  window.removeEventListener('openURLImportDialog', handleOpenURLImportDialog as EventListener);
});
watch(() => cardList.value, (newValue) => {
  if (isFAQ.value) return;
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

const delCard = (index: number, item: KnowledgeCard) => {
  knowledgeIndex.value = index;
  knowledge.value = item;
  delDialog.value = true;
};

const manualEditorSuccess = ({ kbId: savedKbId }: { kbId: string; knowledgeId: string; status: 'draft' | 'publish' }) => {
  if (savedKbId === kbId.value && !isFAQ.value) {
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
    MessagePlugin.warning('当前知识库类型不支持该操作');
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
  return true;
};


const handleDocumentUploadClick = () => {
  if (!ensureDocumentKbReady()) return;
  uploadInputRef.value?.click();
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
    MessagePlugin.error("缺少知识库ID");
    resetUploadInput();
    return;
  }

  // 过滤有效文件
  const validFiles: File[] = [];
  for (let i = 0; i < files.length; i++) {
    const file = files[i];
    if (!kbFileTypeVerification(file, files.length > 1)) {
      validFiles.push(file);
    }
  }

  if (validFiles.length === 0) {
    resetUploadInput();
    return;
  }

  // 批量上传
  let successCount = 0;
  let failCount = 0;
  const totalCount = validFiles.length;

  for (const file of validFiles) {
    try {
      const responseData: any = await uploadKnowledgeFile(kbId.value, { file });
      const isSuccess = responseData?.success || responseData?.code === 200 || responseData?.status === 'success' || (!responseData?.error && responseData);
      if (isSuccess) {
        successCount++;
      } else {
        failCount++;
        let errorMessage = "上传失败！";
        if (responseData?.error?.message) {
          errorMessage = responseData.error.message;
        } else if (responseData?.message) {
          errorMessage = responseData.message;
        }
        if (responseData?.code === 'duplicate_file' || responseData?.error?.code === 'duplicate_file') {
          errorMessage = "文件已存在";
        }
        if (totalCount === 1) {
          MessagePlugin.error(errorMessage);
        }
      }
    } catch (error: any) {
      failCount++;
      let errorMessage = error?.error?.message || error?.message || "上传失败！";
      if (error?.code === 'duplicate_file') {
        errorMessage = "文件已存在";
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
      MessagePlugin.success("上传成功！");
    }
  } else {
    if (failCount === 0) {
      MessagePlugin.success(`所有文件上传成功（${successCount}个）`);
    } else if (successCount > 0) {
      MessagePlugin.warning(`部分文件上传成功（成功：${successCount}，失败：${failCount}）`);
    } else {
      MessagePlugin.error(`所有文件上传失败（${failCount}个）`);
    }
  }

  resetUploadInput();
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
    MessagePlugin.warning(t('knowledgeBase.urlRequired') || '请输入URL');
    return;
  }
  
  // 简单的URL格式验证
  try {
    new URL(url);
  } catch (error) {
    MessagePlugin.warning(t('knowledgeBase.invalidURL') || '请输入有效的URL');
    return;
  }

  if (!kbId.value) {
    MessagePlugin.error("缺少知识库ID");
    return;
  }

  urlImporting.value = true;
  try {
    const responseData: any = await createKnowledgeFromURL(kbId.value, { url });
    window.dispatchEvent(new CustomEvent('knowledgeFileUploaded', {
      detail: { kbId: kbId.value }
    }));
    const isSuccess = responseData?.success || responseData?.code === 200 || responseData?.status === 'success' || (!responseData?.error && responseData);
    if (isSuccess) {
      MessagePlugin.success(t('knowledgeBase.urlImportSuccess') || 'URL导入成功！');
      urlDialogVisible.value = false;
      urlInputValue.value = '';
    } else {
      let errorMessage = t('knowledgeBase.urlImportFailed') || "URL导入失败！";
      if (responseData?.error?.message) {
        errorMessage = responseData.error.message;
      } else if (responseData?.message) {
        errorMessage = responseData.message;
      }
      if (responseData?.code === 'duplicate_url' || responseData?.error?.code === 'duplicate_url') {
        errorMessage = t('knowledgeBase.urlExists') || "该URL已存在";
      }
      MessagePlugin.error(errorMessage);
    }
  } catch (error: any) {
    let errorMessage = error?.error?.message || error?.message || t('knowledgeBase.urlImportFailed') || "URL导入失败！";
    if (error?.code === 'duplicate_url') {
      errorMessage = t('knowledgeBase.urlExists') || "该URL已存在";
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
  delKnowledge(knowledgeIndex.value, knowledge.value);
};

// 处理知识库编辑成功后的回调
const handleKBEditorSuccess = (kbIdValue: string) => {
  // 如果编辑的是当前知识库，刷新文件列表
  if (kbIdValue === kbId.value) {
    loadKnowledgeFiles(kbIdValue);
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
            <t-tooltip :content="$t('knowledgeBase.settings')" placement="top">
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
        </div>
      </div>
      
      <input
        ref="uploadInputRef"
        type="file"
        class="document-upload-input"
        accept=".pdf,.docx,.doc,.txt,.md,.jpg,.jpeg,.png"
        multiple
        @change="handleDocumentUpload"
      />
      <div class="knowledge-main">
        <aside class="tag-sidebar">
          <div class="sidebar-header">
            <div class="sidebar-title">
              <span>{{ $t('knowledgeBase.documentCategoryTitle') }}</span>
              <span class="sidebar-count">({{ sidebarCategoryCount }})</span>
            </div>
            <div class="sidebar-actions">
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
                  :class="{ active: selectedTagId === tag.id, editing: editingTagId === tag.id && tag.id !== UNTAGGED_TAG_ID }"
                  @click="tag.id === UNTAGGED_TAG_ID ? handleUntaggedClick() : handleTagRowClick(tag.id)"
                >
                  <div class="tag-list-left">
                    <t-icon name="folder" size="18px" />
                    <!-- Untagged pseudo-tag: show translated name, no editing -->
                    <template v-if="tag.id === UNTAGGED_TAG_ID">
                      <span class="tag-name">{{ $t('knowledgeBase.untagged') || '未分类' }}</span>
                    </template>
                    <template v-else-if="editingTagId === tag.id">
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
                    <!-- Untagged pseudo-tag: no edit/delete actions -->
                    <template v-if="tag.id === UNTAGGED_TAG_ID">
                      <!-- Empty placeholder for alignment -->
                    </template>
                    <template v-else-if="editingTagId === tag.id">
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
                      <div class="tag-more" @click.stop>
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
            <!-- 搜索栏和筛选 -->
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
                  >
                    <div class="card-content">
                      <div class="card-content-nav">
                        <span class="card-content-title">{{ item.file_name }}</span>
                        <t-popup
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
                            <img class="more" src="@/assets/img/more.png" alt="" />
                          </div>
                          <template #content>
                            <div class="card-menu">
                              <div
                                v-if="item.type === 'manual'"
                                class="card-menu-item"
                                @click.stop="handleManualEdit(index, item)"
                              >
                                <t-icon class="icon" name="edit" />
                                <span>{{ t('knowledgeBase.editDocument') }}</span>
                              </div>
                              <div class="card-menu-item danger" @click.stop="delCard(index, item)">
                                <t-icon class="icon" name="delete" />
                                <span>{{ t('knowledgeBase.deleteDocument') }}</span>
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
                            :options="tagDropdownOptions"
                            trigger="click"
                            @click="(data: any) => handleKnowledgeTagChange(item.id, data.value as string)"
                          >
                            <t-tag size="small" variant="light-outline">
                              <span class="tag-text">{{ getTagName(item.tag_id) }}</span>
                            </t-tag>
                          </t-dropdown>
                        </div>
                        <div class="card-type">
                          <span>{{ getKnowledgeType(item) }}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
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
          
          <!-- URL 导入对话框 -->
          <t-dialog
            v-model:visible="urlDialogVisible"
            :header="$t('knowledgeBase.importURLTitle') || '导入网页'"
            :confirm-btn="{
              content: $t('common.confirm') || '确认',
              theme: 'primary',
              loading: urlImporting,
            }"
            :cancel-btn="{ content: $t('common.cancel') || '取消' }"
            @confirm="handleURLImportConfirm"
            @cancel="handleURLImportCancel"
            width="500px"
          >
            <div class="url-import-form">
              <div class="url-input-label">{{ $t('knowledgeBase.urlLabel') || 'URL地址' }}</div>
              <t-input
                v-model="urlInputValue"
                :placeholder="$t('knowledgeBase.urlPlaceholder') || '请输入网页URL，例如：https://example.com'"
                clearable
                autofocus
                @keydown.enter="handleURLImportConfirm"
              />
              <div class="url-input-tip">{{ $t('knowledgeBase.urlTip') || '支持导入各类网页内容，系统会自动提取和解析网页中的文本内容' }}</div>
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
.card-more {
  z-index: 99 !important;
}

.card-more .t-popup__content {
  width: 180px;
  padding: 6px 0;
  margin-top: 4px !important;
  color: #000000e6;
}
.card-more .t-popup__content:hover {
  color: inherit !important;
}

.tag-more-popup {
  z-index: 99 !important;

  .t-popup__content {
    padding: 4px 0 !important;
    margin-top: 4px !important;
    min-width: 120px;
  }
}

/* 面包屑下拉菜单优化 */
.t-popup__content {
  .t-dropdown__menu {
    background: #ffffff;
    border: 1px solid #e7e9eb;
    border-radius: 10px;
    box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
    padding: 6px;
    min-width: 200px;
    max-width: 240px;
  }

  .t-dropdown__item {
    padding: 8px 12px;
    border-radius: 6px;
    margin: 2px 0;
    transition: all 0.12s ease;
    font-size: 13px;
    color: #0f172a;
    cursor: pointer;
    min-width: auto !important;
    max-width: 100% !important;
    display: flex !important;
    align-items: center;
    width: 100%;

    &:hover {
      background: #f6f8f7;
      color: #10b981;
    }

    .t-dropdown__item-icon {
      flex-shrink: 0;
      margin-right: 8px;
      color: inherit;
      display: flex;
      align-items: center;
      
      .t-icon {
        font-size: 16px;
      }
    }

    .t-dropdown__item-text {
      color: inherit !important;
      font-size: 13px !important;
      line-height: 1.5 !important;
      white-space: nowrap !important;
      overflow: hidden !important;
      text-overflow: ellipsis !important;
      flex: 1;
      min-width: 0;
      display: block;
    }
  }
}
</style>
<style scoped lang="less">
.knowledge-layout {
  display: flex;
  flex-direction: column;
  gap: 16px;
  height: 100%;
  flex: 1;
  width: 100%;
  min-width: 0;
  padding: 24px 44px 32px;
  box-sizing: border-box;
}

.knowledge-main {
  display: flex;
  gap: 12px;
  flex: 1;
  min-height: 0;
}

.tag-sidebar {
  width: 230px;
  background: #fff;
  border-radius: 12px;
  border: 1px solid #e7ebf0;
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
    margin-bottom: 12px;
    color: #1d2129;

    .sidebar-title {
      display: flex;
      align-items: baseline;
      gap: 4px;
      font-weight: 600;

      .sidebar-count {
        font-size: 12px;
        color: #86909c;
      }
    }

    .sidebar-actions {
      display: flex;
      gap: 8px;
      color: #c9ced6;
      align-items: center;

      .create-tag-btn {
        width: 28px;
        height: 28px;
        padding: 0;
        border-radius: 6px;
        display: flex;
        align-items: center;
        justify-content: center;
        font-size: 18px;
        font-weight: 600;
        color: #00a870;
        line-height: 1;
        transition: background 0.2s ease, color 0.2s ease;

        &:hover {
          background: #f3f5f7;
          color: #05a04f;
        }
      }

      .create-tag-plus {
        line-height: 1;
      }
    }
  }

  .tag-search-bar {
    margin-bottom: 12px;

    :deep(.t-input) {
      font-size: 12px;
      background-color: #f7f9fc;
      border-color: #e5e9f2;
    }
  }

  .tag-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1;
    min-height: 0;
    overflow-y: auto;

    .tag-load-more {
      padding: 8px 0 0;
      display: flex;
      justify-content: center;

      :deep(.t-button) {
        padding: 0;
        font-size: 12px;
        color: #00a870;
      }
    }

    .tag-list-item {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 8px 10px;
      border-radius: 6px;
      color: #4e5969;
      cursor: pointer;
      transition: all 0.2s ease;
      font-size: 13px;

      .tag-list-left {
        display: flex;
        align-items: center;
        gap: 8px;
        min-width: 0;
        flex: 1;

        .t-icon {
          flex-shrink: 0;
          color: #86909c;
          font-size: 16px;
          transition: color 0.2s ease;
        }
      }

      .tag-name {
        flex: 1;
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }

      .tag-list-right {
        display: flex;
        align-items: center;
        gap: 6px;
        margin-left: 8px;
        min-width: 0;
      }

      .tag-count {
        font-size: 12px;
        color: #86909c;
        font-weight: 500;
        padding: 2px 6px;
        border-radius: 10px;
        background: #f7f9fc;
        transition: all 0.2s ease;
      }

      &:hover {
        background: #f7f9fc;
        color: #1d2129;

        .tag-list-left .t-icon {
          color: #4e5969;
        }

        .tag-count {
          background: #e5e9f2;
          color: #4e5969;
        }
      }

      &.active {
        background: #e6f7ec;
        color: #00a870;
        font-weight: 500;

        .tag-list-left .t-icon {
          color: #00a870;
        }

        .tag-count {
          background: #b8f0d3;
          color: #00a870;
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
          background: #eefcf5;
          color: #059669;

          &:hover {
            background: #d9f7e9;
            color: #047857;
          }
        }

        :deep(.tag-action-btn.cancel) {
          background: #f9fafb;
          color: #6b7280;

          &:hover {
            background: #f3f4f6;
            color: #4b5563;
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
          border-bottom: 1px solid #d0d5dd;
          border-radius: 0;
          box-shadow: none;
          padding-left: 0;
          padding-right: 0;
        }

        :deep(.t-input__wrap) {
          background-color: transparent;
          border: none;
          border-bottom: 1px solid #d0d5dd;
          border-radius: 0;
          box-shadow: none;
        }

        :deep(.t-input__inner) {
          padding-left: 0;
          padding-right: 0;
          color: #1d2129;
          caret-color: #1d2129;
        }

        :deep(.t-input:hover),
        :deep(.t-input.t-is-focused),
        :deep(.t-input__wrap:hover),
        :deep(.t-input__wrap.t-is-focused) {
          border-bottom-color: #00a870;
        }
      }

      .tag-more {
        display: flex;
        align-items: center;
      }

      .tag-more-btn {
        width: 24px;
        height: 24px;
        display: flex;
        align-items: center;
        justify-content: center;
        border-radius: 4px;
        color: #86909c;
        transition: all 0.2s ease;
        opacity: 0.6;

        &:hover {
          background: #f3f5f7;
          color: #4e5969;
          opacity: 1;
        }
      }
    }

    .tag-empty-state {
      text-align: center;
      padding: 12px 8px;
      color: #a1a7b3;
      font-size: 12px;
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
  color: #000000e6;
  font-family: 'PingFang SC';
  font-size: 14px;
  font-weight: 400;

  .menu-icon {
    margin-right: 8px;
    font-size: 16px;
  }

  &:hover {
    background: #f5f5f5;
    color: #000000e6;
  }

  &.danger {
    color: #000000e6;

    &:hover {
      background: #fff1f0;
      color: #fa5151;

      .menu-icon {
        color: #fa5151;
      }
    }
  }
}

.tag-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.doc-card-area {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.doc-filter-bar {
  padding: 0px 0px 10px 0px;
  flex-shrink: 0;
  display: flex;
  gap: 10px;
  align-items: center;

  .doc-search-input {
    flex: 1;
  }

  .doc-type-select {
    width: 140px;
    flex-shrink: 0;
  }

  :deep(.t-input) {
    font-size: 13px;
    background-color: #f7f9fc;
    border-color: #e5e9f2;
    border-radius: 6px;

    &:hover,
    &:focus,
    &.t-is-focused {
      border-color: #4080ff;
      background-color: #fff;
    }
  }

  :deep(.t-select) {
    .t-input {
      font-size: 13px;
      background-color: #f7f9fc;
      border-color: #e5e9f2;
      border-radius: 6px;

      &:hover,
      &.t-is-focused {
        border-color: #4080ff;
        background-color: #fff;
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

// Header 样式
.document-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 16px;
  padding: 0 0 16px;
  flex-shrink: 0;
  border-bottom: 1px solid #e7ebf0;

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

  .document-breadcrumb {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    color: #1d2129;
  }

  .breadcrumb-link {
    border: none;
    background: transparent;
    padding: 4px 8px;
    margin: -4px -8px;
    font: inherit;
    color: #4e5969;
    cursor: pointer;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border-radius: 6px;
    transition: all 0.12s ease;

    &:hover:not(:disabled) {
      color: #10b981;
      background: #f6f8f7;
    }

    &:disabled {
      cursor: not-allowed;
      color: #c9ced6;
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
    color: #c9ced6;
  }

  .breadcrumb-current {
    color: #1d2129;
    font-weight: 600;
  }

  h2 {
    margin: 0;
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 24px;
    font-weight: 600;
    line-height: 32px;
  }

  .document-subtitle {
    margin: 0;
    color: #00000099;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
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
  background: #f5f6f8;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  color: #6b7280;
  cursor: pointer;
  transition: all 0.2s ease;
  padding: 0;

  &:hover:not(:disabled) {
    background: #e6f7ec;
    color: #07c05f;
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
    color: #00000099;
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
    border-color: #e5e7eb;
    color: #374151;
    padding: 0 10px;
    background: #f9fafb;
    transition: all 0.2s ease;

    &:hover {
      border-color: #07c05f;
      color: #059669;
      background: #ecfdf5;
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
  padding: 24px 44px;
  overflow-y: auto;
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
  gap: 20px;
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
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
  }

  .del-circle-txt {
    color: #00000099;
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
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
    line-height: 22px;
    cursor: pointer;
  }

  .confirm {
    color: #FA5151;
    margin-left: 40px;
  }
}

.card-menu {
  display: flex;
  flex-direction: column;
  min-width: 140px;
}

.card-menu-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  cursor: pointer;
  color: #000000e6;

  &:hover {
    background: #f5f5f5;
  }

  .icon {
    font-size: 16px;
  }

  &.danger {
    color: #fa5151;
  }
}

.card-draft {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 0;
}

.card-draft-tip {
  color: #b05b00;
  font-size: 12px;
}

.knowledge-card {
  border: 2px solid #fbfbfb;
  height: 174px;
  border-radius: 6px;
  overflow: hidden;
  box-sizing: border-box;
  box-shadow: 0 0 8px 0 #00000005;
  background: #fff;
  position: relative;
  cursor: pointer;

  .card-content {
    padding: 10px 20px 23px;
  }

  .card-analyze {
    height: 66px;
    display: flex;
  }

  .card-analyze-loading {
    display: block;
    color: #07c05f;
    font-size: 15px;
    margin-top: 2px;
  }

  .card-analyze-txt {
    color: #07c05f;
    font-family: "PingFang SC";
    font-size: 12px;
    margin-left: 9px;
  }

  .failure {
    color: #fa5151;
  }

  .card-content-nav {
    display: flex;
    justify-content: space-between;
    margin-bottom: 10px;
  }

  .card-content-title {
    width: 200px;
    height: 32px;
    line-height: 32px;
    display: inline-block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: #000000e6;
    font-family: "PingFang SC";
    font-size: 14px;
    font-weight: 400;
  }

  .more-wrap {
    display: flex;
    width: 32px;
    height: 32px;
    justify-content: center;
    align-items: center;
    border-radius: 3px;
    cursor: pointer;
  }

  .more-wrap:hover {
    background: #e7e7e7;
  }

  .more {
    width: 16px;
    height: 16px;
  }

  .active-more {
    background: #e7e7e7;
  }

  .card-content-txt {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 3;
    line-clamp: 3;
    overflow: hidden;
    color: #00000066;
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 400;
    line-height: 20px;
  }

  .card-bottom {
    position: absolute;
    bottom: 0;
    padding: 0 20px;
    box-sizing: border-box;
    height: 32px;
    width: 100%;
    display: flex;
    align-items: center;
    justify-content: space-between;
    background: rgba(48, 50, 54, 0.02);
  }

  .card-time {
    color: #00000066;
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 400;
  }

  .card-type {
    color: #00000066;
    font-family: "PingFang SC";
    font-size: 12px;
    font-weight: 400;
    padding: 2px 4px;
    background: #3032360f;
    border-radius: 4px;
  }
}

.knowledge-card:hover {
  border: 2px solid #07c05f;
}

.url-import-form {
  padding: 8px 0;

  .url-input-label {
    color: #1d2129;
    font-size: 14px;
    font-weight: 500;
    margin-bottom: 8px;
  }

  .url-input-tip {
    color: #86909c;
    font-size: 12px;
    margin-top: 8px;
    line-height: 1.5;
  }
}

.knowledge-card-upload {
  color: #000000e6;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 400;
  cursor: pointer;

  .btn-upload {
    margin: 33px auto 0;
    width: 112px;
    height: 32px;
    border: 1px solid #dcdcdc;
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
  color: #00000066;
  font-family: "PingFang SC";
  font-size: 12px;
  font-weight: 400;
  text-align: center;
  display: block;
  width: 188px;
  margin: 0 auto;
}

.doc-card-list {
  grid-template-columns: 1fr;
}

.del-card {
  vertical-align: middle;
}

/* 小屏幕平板 - 2列 */
@media (min-width: 900px) {
  .doc-card-list {
    grid-template-columns: repeat(2, 1fr);
  }
}

/* 中等屏幕 - 3列 */
@media (min-width: 1250px) {
  .doc-card-list {
    grid-template-columns: repeat(3, 1fr);
  }
}

/* 中等屏幕 - 3列 */
@media (min-width: 1600px) {
  .doc-card-list {
    grid-template-columns: repeat(4, 1fr);
  }
}

/* 大屏幕 - 4列 */
@media (min-width: 2000px) {
  .doc-card-list {
    grid-template-columns: repeat(5, 1fr);
  }
}
</style>
