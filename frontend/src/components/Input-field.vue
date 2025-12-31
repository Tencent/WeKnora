<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick, h } from "vue";
import { useRoute, useRouter } from 'vue-router';
import { onBeforeRouteUpdate } from 'vue-router';
import { MessagePlugin } from "tdesign-vue-next";
import { useSettingsStore } from '@/stores/settings';
import { useUIStore } from '@/stores/ui';
import { listKnowledgeBases, searchKnowledge, batchQueryKnowledge } from '@/api/knowledge-base';
import { stopSession } from '@/api/chat';
import KnowledgeBaseSelector from './KnowledgeBaseSelector.vue';
import MentionSelector from './MentionSelector.vue';
import AgentSelector from './AgentSelector.vue';
import { getCaretCoordinates } from '@/utils/caret';
import { listModels, type ModelConfig } from '@/api/model';
import { listAgents, type CustomAgent, BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from '@/api/agent';
import { getTenantWebSearchConfig } from '@/api/web-search';
import { getConversationConfig, updateConversationConfig, type ConversationConfig } from '@/api/system';
import { useI18n } from 'vue-i18n';

const route = useRoute();
const router = useRouter();
const settingsStore = useSettingsStore();
const uiStore = useUIStore();
const { t } = useI18n();

let query = ref("");
const showKbSelector = ref(false);
const atButtonRef = ref<HTMLElement>();
const showAgentModeSelector = ref(false);
const agentModeButtonRef = ref<HTMLElement>();
const agentModeDropdownStyle = ref<Record<string, string>>({});

// 智能体相关状态
const agents = ref<CustomAgent[]>([]);
const selectedAgentId = computed({
  get: () => settingsStore.selectedAgentId || BUILTIN_QUICK_ANSWER_ID,
  set: (val: string) => settingsStore.selectAgent(val)
});
const selectedAgent = computed(() => {
  return agents.value.find(a => a.id === selectedAgentId.value) || {
    id: BUILTIN_QUICK_ANSWER_ID,
    name: t('input.normalMode'),
    is_builtin: true,
    config: { agent_mode: 'quick-answer' as const }
  } as CustomAgent;
});

// 判断是否为自定义智能体（非内置）
const isCustomAgent = computed(() => {
  const agent = selectedAgent.value;
  return agent && !agent.is_builtin;
});

// 判断是否有智能体配置（包括内置智能体）
const hasAgentConfig = computed(() => {
  const agent = selectedAgent.value;
  // 内置智能体需要从 agents 列表中获取实际配置
  if (agent?.is_builtin) {
    const builtinAgent = agents.value.find(a => a.id === agent.id);
    return !!builtinAgent?.config;
  }
  return !!agent?.config;
});

// 获取当前智能体的实际配置（内置智能体从 agents 列表获取）
const currentAgentConfig = computed(() => {
  const agent = selectedAgent.value;
  if (agent?.is_builtin) {
    const builtinAgent = agents.value.find(a => a.id === agent.id);
    return builtinAgent?.config || {};
  }
  return agent?.config || {};
});

// 智能体预配置的知识库 IDs
const agentKnowledgeBases = computed(() => {
  if (!hasAgentConfig.value) return [];
  return currentAgentConfig.value?.knowledge_bases || [];
});

// 当智能体改变时，将智能体配置的知识库同步到 store
// 这样用户可以移除这些知识库
watch([selectedAgentId, agentKnowledgeBases], ([newAgentId, newAgentKbs], [oldAgentId]) => {
  if (newAgentId !== oldAgentId && newAgentKbs && newAgentKbs.length > 0) {
    // 智能体切换了，将新智能体配置的知识库添加到 store
    const currentSelected = settingsStore.settings.selectedKnowledgeBases || [];
    const toAdd = newAgentKbs.filter((id: string) => !currentSelected.includes(id));
    if (toAdd.length > 0) {
      settingsStore.selectKnowledgeBases([...currentSelected, ...toAdd]);
    }
  }
}, { immediate: true });

// 智能体的知识库选择模式
const agentKBSelectionMode = computed(() => {
  if (!hasAgentConfig.value) return null; // null 表示不受智能体控制
  return currentAgentConfig.value?.kb_selection_mode || 'all';
});

// 智能体是否启用了网络搜索
const agentWebSearchEnabled = computed(() => {
  if (!hasAgentConfig.value) return null; // null 表示不受智能体控制
  return currentAgentConfig.value?.web_search_enabled ?? true;
});

// 网络搜索是否被智能体禁用（只读状态）- 只有明确设置为 false 时才禁用
const isWebSearchDisabledByAgent = computed(() => {
  return hasAgentConfig.value && agentWebSearchEnabled.value === false;
});

// 知识库选择是否被智能体锁定
// 1. 如果智能体配置了 kb_selection_mode = 'none' → 完全禁用知识库
// 其他情况用户都可以在允许的范围内通过 @ 选择知识库
const isKnowledgeBaseLockedByAgent = computed(() => {
  if (!hasAgentConfig.value) return false;
  // 只有禁用了知识库才锁定
  return agentKBSelectionMode.value === 'none';
});

// 知识库是否被智能体完全禁用（kb_selection_mode = 'none'）
const isKnowledgeBaseDisabledByAgent = computed(() => {
  if (!hasAgentConfig.value) return false;
  return agentKBSelectionMode.value === 'none';
});

// 智能体配置的模型 ID
const agentModelId = computed(() => {
  if (!hasAgentConfig.value) return null;
  return currentAgentConfig.value?.model_id || null;
});

// 智能体支持的文件类型（空数组表示支持所有类型）
const agentSupportedFileTypes = computed(() => {
  if (!hasAgentConfig.value) return [];
  return currentAgentConfig.value?.supported_file_types || [];
});

// 模型选择是否被智能体锁定 - 已移除锁定逻辑，允许用户自由切换模型
const isModelLockedByAgent = computed(() => {
  return false;
});

// Mention related state
const showMention = ref(false);
const mentionQuery = ref("");
const mentionItems = ref<Array<{ id: string; name: string; type: 'kb' | 'file'; kbType?: 'document' | 'faq'; count?: number; kbName?: string }>>([]);
const mentionActiveIndex = ref(0);
const mentionStyle = ref<Record<string, string>>({});
const textareaRef = ref<any>(null); // Ref to t-textarea component
const mentionStartPos = ref(0);
const isComposing = ref(false);
const isMentionTriggeredByButton = ref(false);
const mentionHasMore = ref(false);
const mentionLoading = ref(false);
const mentionOffset = ref(0);
const MENTION_PAGE_SIZE = 20;

const props = defineProps({
  isReplying: {
    type: Boolean,
    required: false
  },
  sessionId: {
    type: String,
    required: false
  },
  assistantMessageId: {
    type: String,
    required: false
  }
});

const isAgentEnabled = computed(() => settingsStore.isAgentEnabled);
const isWebSearchEnabled = computed(() => settingsStore.isWebSearchEnabled);
const selectedKbIds = computed(() => settingsStore.settings.selectedKnowledgeBases || []);
const selectedFileIds = computed(() => settingsStore.settings.selectedFiles || []);
const isWebSearchConfigured = ref(false);

// 获取已选择的知识库信息
const knowledgeBases = ref<Array<{ id: string; name: string; type?: 'document' | 'faq'; knowledge_count?: number; chunk_count?: number }>>([]);
const fileList = ref<Array<{ id: string; name: string }>>([]);

const selectedKbs = computed(() => {
  return knowledgeBases.value.filter(kb => selectedKbIds.value.includes(kb.id));
});

const selectedFiles = computed(() => {
  // If we have file details in fileList, use them.
  // Otherwise we might show ID or Loading...
  return selectedFileIds.value.map((id: string) => {
    const found = fileList.value.find(f => f.id === id);
    return found || { id, name: 'Loading...' };
  });
});

  // 合并所有选中项（用于输入框内显示）
  // 现在智能体配置的知识库也在 store 中，统一从 selectedKbs 获取
  const allSelectedItems = computed(() => {
    // 获取智能体预配置的知识库 IDs（用于标记和排序）
    const agentKbIds = agentKnowledgeBases.value;
    
    // 所有选中的知识库，标记是否为智能体配置
    const allKbs = selectedKbs.value.map(kb => ({ 
      ...kb, 
      type: 'kb' as const,
      kbType: kb.type,
      isAgentConfigured: agentKbIds.includes(kb.id)
    }));
    
    // 用户选择的文件
    const files = selectedFiles.value.map((f: { id: string; name: string }) => ({ 
      ...f, 
      type: 'file' as const,
      isAgentConfigured: false
    }));
    
    // 智能体配置的放在前面
    const agentConfiguredKbs = allKbs.filter(kb => kb.isAgentConfigured);
    const userSelectedKbs = allKbs.filter(kb => !kb.isAgentConfigured);
    
    return [...agentConfiguredKbs, ...userSelectedKbs, ...files];
  });

// 移除选中项（智能体配置的项也可以移除）
const removeSelectedItem = (item: { id: string; type: 'kb' | 'file'; isAgentConfigured?: boolean }) => {
  if (item.type === 'kb') {
    settingsStore.removeKnowledgeBase(item.id);
  } else {
    settingsStore.removeFile(item.id);
  }
};

// 模型相关状态
const availableModels = ref<ModelConfig[]>([]);
// 使用 computed 从 store 读取，并通过 setter 同步回 store
const selectedModelId = computed({
  get: () => settingsStore.conversationModels.selectedChatModelId || '',
  set: (val: string) => settingsStore.updateConversationModels({ selectedChatModelId: val })
});
const conversationConfig = ref<ConversationConfig | null>(null);
const modelsLoading = ref(false);
const showModelSelector = ref(false);
const modelButtonRef = ref<HTMLElement>();
const modelDropdownStyle = ref<Record<string, string>>({});

// 显示的知识库标签（最多显示2个）
const displayedKbs = computed(() => selectedKbs.value.slice(0, 2));
const remainingCount = computed(() => Math.max(0, selectedKbs.value.length - 2));

// 根据不同状态组合计算输入框的 placeholder
const inputPlaceholder = computed(() => {
  // 如果选择了自定义智能体
  if (isCustomAgent.value && selectedAgent.value) {
    // 有描述时显示描述，否则显示"向 [名称] 提问"
    if (selectedAgent.value.description) {
      return selectedAgent.value.description;
    }
    return t('input.placeholderAgent', { name: selectedAgent.value.name });
  }
  
  const hasKnowledge = allSelectedItems.value.length > 0;
  const hasWebSearch = isWebSearchEnabled.value && isWebSearchConfigured.value;
  
  if (hasKnowledge && hasWebSearch) {
    // 有知识库 + 有网络搜索
    return t('input.placeholderKbAndWeb');
  } else if (hasKnowledge) {
    // 有知识库 + 无网络搜索
    return t('input.placeholderWithContext');
  } else if (hasWebSearch) {
    // 无知识库 + 有网络搜索
    return t('input.placeholderWebOnly');
  } else {
    // 无知识库 + 无网络搜索（纯模型对话）
    return t('input.placeholder');
  }
});

// 加载知识库列表
const loadKnowledgeBases = async () => {
  try {
    const response: any = await listKnowledgeBases();
    if (response.data && Array.isArray(response.data)) {
      const validKbs = response.data.filter((kb: any) => 
        kb.embedding_model_id && kb.embedding_model_id !== '' &&
        kb.summary_model_id && kb.summary_model_id !== ''
      );
      knowledgeBases.value = validKbs;
      
      // 清理无效的知识库ID（已删除或不存在于有效知识库列表中的）
      const validKbIds = new Set(validKbs.map((kb: any) => kb.id));
      const currentSelectedIds = settingsStore.settings.selectedKnowledgeBases || [];
      const validSelectedIds = currentSelectedIds.filter((id: string) => validKbIds.has(id));
      
      // 如果有无效的ID，更新store
      if (validSelectedIds.length !== currentSelectedIds.length) {
        settingsStore.selectKnowledgeBases(validSelectedIds);
      }
    }
  } catch (error) {
    console.error('Failed to load knowledge bases:', error);
  }
};

const loadFiles = async () => {
  const ids = selectedFileIds.value;
  if (ids.length === 0) return;
  
  // Filter out files we already have info for
  const missingIds = ids.filter((id: string) => !fileList.value.find(f => f.id === id));
  if (missingIds.length === 0) return;

  try {
    const query = new URLSearchParams();
    missingIds.forEach((id: string) => query.append('ids', id));
    const res: any = await batchQueryKnowledge(query.toString());
    if (res.data) {
      const newFiles = res.data.map((f: any) => ({ id: f.id, name: f.title || f.file_name }));
      fileList.value = [...fileList.value, ...newFiles];
    }
  } catch (e) {
    console.error("Failed to load files", e);
  }
};

watch(selectedFileIds, () => {
  loadFiles();
}, { immediate: true });

const loadWebSearchConfig = async () => {
  try {
    const response: any = await getTenantWebSearchConfig();
    const config = response?.data;
    const configured = !!(config && config.provider);
    isWebSearchConfigured.value = configured;

    if (!configured && settingsStore.isWebSearchEnabled) {
      settingsStore.toggleWebSearch(false);
    }
  } catch (error) {
    console.error('Failed to load web search config:', error);
    isWebSearchConfigured.value = false;
    if (settingsStore.isWebSearchEnabled) {
      settingsStore.toggleWebSearch(false);
    }
  }
};

// 加载智能体列表
const loadAgents = async () => {
  try {
    const response = await listAgents();
    agents.value = response.data || [];
  } catch (error) {
    console.error('Failed to load agents:', error);
  }
};

const loadConversationConfig = async () => {
  try {
    const response = await getConversationConfig();
    conversationConfig.value = response.data;
    const modelId = response.data?.summary_model_id || '';
    
    // 保留当前已选择的模型（如果有），避免覆盖从其他页面传递的模型选择
    const currentSelectedModel = settingsStore.conversationModels.selectedChatModelId;
    settingsStore.updateConversationModels({
      summaryModelId: modelId,
      selectedChatModelId: currentSelectedModel || modelId,  // 优先保留当前选择
      rerankModelId: response.data?.rerank_model_id || '',
    });
    if (!selectedModelId.value) {
      selectedModelId.value = modelId;
    }
    ensureModelSelection();
  } catch (error) {
    console.error('Failed to load conversation config:', error);
  }
};

const loadChatModels = async () => {
  if (modelsLoading.value) return;
  modelsLoading.value = true;
  try {
    const models = await listModels('KnowledgeQA');
    availableModels.value = Array.isArray(models) ? models : [];
    ensureModelSelection();
  } catch (error) {
    console.error('Failed to load chat models:', error);
    availableModels.value = [];
  } finally {
    modelsLoading.value = false;
  }
};

const ensureModelSelection = () => {
  if (selectedModelId.value) {
    return;
  }
  if (conversationConfig.value?.summary_model_id) {
    selectedModelId.value = conversationConfig.value.summary_model_id;
    return;
  }
  if (availableModels.value.length > 0) {
    selectedModelId.value = availableModels.value[0].id || '';
  }
};

const handleGoToConversationModels = () => {
  showModelSelector.value = false;
  router.push('/platform/settings');
  setTimeout(() => {
    const event = new CustomEvent('settings-nav', {
      detail: { section: 'models', subsection: 'chat' },
    });
    window.dispatchEvent(event);
  }, 100);
};

const handleModelChange = async (value: string | number | Array<string | number> | undefined) => {
  const normalized = Array.isArray(value) ? value[0] : value;
  const val = normalized !== undefined && normalized !== null ? String(normalized) : '';

  if (!val) {
    selectedModelId.value = '';
    return;
  }
  if (val === '__add_model__') {
    selectedModelId.value = conversationConfig.value?.summary_model_id || '';
    handleGoToConversationModels();
    return;
  }
  
  // 保存到后端
  try {
    if (conversationConfig.value) {
      const updatedConfig = {
        ...conversationConfig.value,
        summary_model_id: val
      };
      const response = await updateConversationConfig(updatedConfig);
      
      // 更新本地状态
      conversationConfig.value = response.data;
      selectedModelId.value = val;
      showModelSelector.value = false;
      
      // 同步到 store
      settingsStore.updateConversationModels({
        summaryModelId: val,
        selectedChatModelId: val,
        rerankModelId: conversationConfig.value?.rerank_model_id || '',
      });
      
      MessagePlugin.success(t('conversationSettings.toasts.chatModelSaved'));
    }
  } catch (error) {
    console.error('保存模型配置失败:', error);
    MessagePlugin.error(t('conversationSettings.toasts.saveFailed'));
    // 恢复到之前的值
    selectedModelId.value = conversationConfig.value?.summary_model_id || '';
  }
};

const selectedModel = computed(() => {
  return availableModels.value.find(model => model.id === selectedModelId.value);
});

const updateModelDropdownPosition = () => {
  const anchor = modelButtonRef.value;
  if (!anchor) {
    modelDropdownStyle.value = {
      position: 'fixed',
      top: '50%',
      left: '50%',
      transform: 'translate(-50%, -50%)',
    };
    return;
  }
  
  // 获取按钮相对于视口的位置
  const rect = anchor.getBoundingClientRect();
  console.log('[Model Dropdown] Button rect:', {
    top: rect.top,
    bottom: rect.bottom,
    left: rect.left,
    right: rect.right,
    width: rect.width,
    height: rect.height
  });
  
  const dropdownWidth = 280;
  const offsetY = 8;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  
  // 左对齐到触发元素的左边缘
  // 使用 Math.floor 而不是 Math.round，避免像素对齐问题
  let left = Math.floor(rect.left);
  
  // 边界处理：不超出视口左右（留 16px margin）
  const minLeft = 16;
  const maxLeft = Math.max(16, vw - dropdownWidth - 16);
  left = Math.max(minLeft, Math.min(maxLeft, left));

  // 垂直定位：紧贴按钮，使用合理的高度避免空白
  const preferredDropdownHeight = 280; // 优选高度（紧凑且够用）
  const maxDropdownHeight = 360; // 最大高度
  const minDropdownHeight = 200; // 最小高度
  const topMargin = 20; // 顶部留白
  const spaceBelow = vh - rect.bottom; // 下方剩余空间
  const spaceAbove = rect.top; // 上方剩余空间
  
  console.log('[Model Dropdown] Space check:', {
    spaceBelow,
    spaceAbove,
    windowHeight: vh
  });
  
  let actualHeight: number;
  let shouldOpenBelow: boolean;
  
  // 优先考虑下方空间
  if (spaceBelow >= minDropdownHeight + offsetY) {
    // 下方有足够空间，向下弹出
    actualHeight = Math.min(preferredDropdownHeight, spaceBelow - offsetY - 16);
    shouldOpenBelow = true;
    console.log('[Model Dropdown] Position: below button', { actualHeight });
  } else {
    // 向上弹出，优先使用 preferredHeight，必要时才扩展到 maxHeight
    const availableHeight = spaceAbove - offsetY - topMargin;
    if (availableHeight >= preferredDropdownHeight) {
      // 有足够空间显示优选高度
      actualHeight = preferredDropdownHeight;
    } else {
      // 空间不够，使用可用空间（但不小于最小高度）
      actualHeight = Math.max(minDropdownHeight, availableHeight);
    }
    shouldOpenBelow = false;
    console.log('[Model Dropdown] Position: above button', { actualHeight });
  }
  
  // 根据弹出方向使用不同的定位方式
  if (shouldOpenBelow) {
    // 向下弹出：使用 top 定位，左对齐
    const top = Math.floor(rect.bottom + offsetY);
    console.log('[Model Dropdown] Opening below, top:', top);
    modelDropdownStyle.value = {
      position: 'fixed !important',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      top: `${top}px`,
      maxHeight: `${actualHeight}px`,
      transform: 'none !important',
      margin: '0 !important',
      padding: '0 !important'
    };
  } else {
    // 向上弹出：使用 bottom 定位，左对齐
    const bottom = vh - rect.top + offsetY;
    console.log('[Model Dropdown] Opening above, bottom:', bottom);
    modelDropdownStyle.value = {
      position: 'fixed !important',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      bottom: `${bottom}px`,
      maxHeight: `${actualHeight}px`,
      transform: 'none !important',
      margin: '0 !important',
      padding: '0 !important'
    };
  }
  
  console.log('[Model Dropdown] Applied style:', modelDropdownStyle.value);
};

// Mention Logic
let lastMentionQuery = '';
const loadMentionItems = async (q: string, resetIndex = true, append = false) => {
  console.log('[Mention] loadMentionItems called with query:', q, 'append:', append);
  
  if (!append) {
    mentionOffset.value = 0;
  }
  
  // 根据智能体的 kb_selection_mode 过滤知识库
  let kbItems: any[] = [];
  if (!append) {
    // 获取可选的知识库列表
    let availableKbs = knowledgeBases.value;
    
    // 如果智能体有配置，根据 kb_selection_mode 过滤
    if (hasAgentConfig.value) {
      const kbMode = agentKBSelectionMode.value;
      if (kbMode === 'none') {
        // 不使用知识库，不显示任何知识库
        availableKbs = [];
      } else if (kbMode === 'selected') {
        // 仅显示智能体配置的知识库
        const configuredKbIds = agentKnowledgeBases.value;
        availableKbs = knowledgeBases.value.filter(kb => configuredKbIds.includes(kb.id));
      }
      // kbMode === 'all' 时显示全部知识库
    }
    
    // 按查询过滤
    const kbs = availableKbs.filter(kb => 
      !q || kb.name.toLowerCase().includes(q.toLowerCase())
    );
    kbItems = kbs.map(kb => ({ 
      id: kb.id, 
      name: kb.name, 
      type: 'kb' as const, 
      kbType: kb.type || 'document',
      count: kb.type === 'faq' ? (kb.chunk_count || 0) : (kb.knowledge_count || 0)
    }));
  }
  
  // Fetch Files from API
  // 如果智能体禁用了知识库，也不显示文件
  let fileItems: any[] = [];
  const shouldLoadFiles = !hasAgentConfig.value || agentKBSelectionMode.value !== 'none';
  
  if (shouldLoadFiles) {
    mentionLoading.value = true;
    try {
      // 将文件类型过滤传递给后端
      const fileTypesParam = agentSupportedFileTypes.value.length > 0 ? agentSupportedFileTypes.value : undefined;
      const res: any = await searchKnowledge(q || '', mentionOffset.value, MENTION_PAGE_SIZE, fileTypesParam);
      console.log('[Mention] searchKnowledge response:', res);
      if (res.data && Array.isArray(res.data)) {
        let files = res.data;
        
        // 如果智能体配置了 kb_selection_mode === 'selected'，只显示指定知识库中的文件
        if (hasAgentConfig.value && agentKBSelectionMode.value === 'selected') {
          const configuredKbIds = agentKnowledgeBases.value;
          files = files.filter((f: any) => configuredKbIds.includes(f.knowledge_base_id));
        }
        
        fileItems = files.map((f: any) => ({ 
          id: f.id, 
          name: f.title || f.file_name, 
          type: 'file' as const,
          kbName: f.knowledge_base_name || ''
        }));
      }
      mentionHasMore.value = res.has_more || false;
      mentionOffset.value += fileItems.length;
    } catch (e) {
      console.error('[Mention] searchKnowledge error:', e);
      mentionHasMore.value = false;
    } finally {
      mentionLoading.value = false;
    }
  } else {
    mentionHasMore.value = false;
  }
  
  if (append) {
    // Append file items to existing list
    mentionItems.value = [...mentionItems.value, ...fileItems];
  } else {
    mentionItems.value = [...kbItems, ...fileItems];
  }
  console.log('[Mention] Total items:', mentionItems.value.length, { kbItems: kbItems.length, fileItems: fileItems.length });
  
  // Only reset index if query changed or explicitly requested
  if (resetIndex || q !== lastMentionQuery) {
    mentionActiveIndex.value = 0;
  }
  // Ensure index is within bounds
  if (mentionActiveIndex.value >= mentionItems.value.length) {
    mentionActiveIndex.value = Math.max(0, mentionItems.value.length - 1);
  }
  lastMentionQuery = q;
};

const loadMoreMentionItems = () => {
  if (mentionHasMore.value && !mentionLoading.value) {
    loadMentionItems(lastMentionQuery, false, true);
  }
};

const getTextareaEl = () => {
  if (!textareaRef.value) return null;
  // If it's a native element
  if (textareaRef.value instanceof HTMLTextAreaElement) return textareaRef.value;
  // If it's a component wrapper
  const el = textareaRef.value.$el || textareaRef.value;
  if (!el) return null;
  if (el.tagName === 'TEXTAREA') return el as HTMLTextAreaElement;
  return el.querySelector('textarea');
};

const onInput = (val: string | InputEvent) => {
  // 如果正在输入法组合中，不处理搜索逻辑，等待 compositionend
  if (isComposing.value) return;

  // TDesign t-textarea passes the value directly, not an event
  const inputVal = typeof val === 'string' ? val : query.value;
  
  const textarea = getTextareaEl();
  if (!textarea) {
    console.warn('[Mention] Could not get textarea element');
    return;
  }
  
  const cursor = textarea.selectionStart;
  const textBeforeCursor = inputVal.slice(0, cursor);
  
  console.log('[Mention] onInput called', { inputVal, cursor, textBeforeCursor, showMention: showMention.value });
  
  if (showMention.value) {
    // 如果不是按钮触发的，检查 @ 符号
    if (!isMentionTriggeredByButton.value) {
      if (!inputVal || inputVal.length <= mentionStartPos.value || inputVal.charAt(mentionStartPos.value) !== '@') {
        showMention.value = false;
        return;
      }
    }

    // 如果是按钮触发的，mentionStartPos 指向的是光标位置（即虚拟的 @ 位置前），所以实际上不应该往左删
    // 但如果用户删除了前面的内容导致长度变短，也需要处理
    if (cursor < mentionStartPos.value) {
      showMention.value = false;
      return;
    }
    
    // Get query
    // 如果是按钮触发，mentionStartPos 是起始位置，不需要 +1 跳过 @
    const start = isMentionTriggeredByButton.value ? mentionStartPos.value : mentionStartPos.value + 1;
    const q = inputVal.slice(start, cursor);
    
    if (q.includes(' ')) {
      showMention.value = false;
      return;
    }
    // Only reload if query changed
    if (q !== mentionQuery.value) {
      mentionQuery.value = q;
      loadMentionItems(q, true); // Reset index when query changes
    }
  } else {
    if (textBeforeCursor.endsWith('@')) {
      // 如果智能体禁用了知识库，不触发 @ 菜单
      if (isKnowledgeBaseDisabledByAgent.value) {
        return;
      }
      // 如果智能体锁定了知识库且不允许用户选择，也不触发 @ 菜单
      if (isKnowledgeBaseLockedByAgent.value) {
        return;
      }
      
      console.log('[Mention] @ detected, opening menu');
      isMentionTriggeredByButton.value = false;
      mentionStartPos.value = cursor - 1;
      showMention.value = true;
      mentionQuery.value = "";
      
      const coords = getCaretCoordinates(textarea, cursor);
      const rect = textarea.getBoundingClientRect();
      const scrollTop = textarea.scrollTop;
      const menuHeight = 320; // 预估最大高度
      
      let left = rect.left + coords.left;
      // Prevent menu from going off-screen horizontally
      if (left + 300 > window.innerWidth) {
        left = window.innerWidth - 300 - 10;
      }
      
      // 光标相对于视口的实际 top 位置
      const cursorAbsoluteTop = rect.top + coords.top - scrollTop;
      const lineHeight = coords.height; // 光标高度

      // Check vertical space below cursor
      const spaceBelow = window.innerHeight - (cursorAbsoluteTop + lineHeight);
      
      if (spaceBelow < menuHeight && cursorAbsoluteTop > menuHeight) {
         // Show above cursor (using bottom positioning)
         // bottom distance = viewport height - cursor top position
         const bottom = window.innerHeight - cursorAbsoluteTop;
         mentionStyle.value = {
           left: `${left}px`,
           bottom: `${bottom}px`,
           top: 'auto'
         };
      } else {
         // Show below cursor (using top positioning)
         const top = cursorAbsoluteTop + lineHeight;
         mentionStyle.value = {
           left: `${left}px`,
           top: `${top}px`,
           bottom: 'auto'
         };
      }
      
      loadMentionItems("");
    }
  }
};

const onCompositionStart = () => {
  isComposing.value = true;
};

const onCompositionEnd = (e: CompositionEvent) => {
  isComposing.value = false;
  // 手动触发 onInput 逻辑
  // 注意：在 compositionend 时，v-model 可能还没更新，或者已经更新但我们需要用最新值
  // TDesign textarea 可能需要 nextTick
  nextTick(() => {
    onInput(query.value);
  });
};

const triggerMention = () => {
  // 如果智能体锁定或禁用了知识库，不允许打开选择器
  if (isKnowledgeBaseLockedByAgent.value) {
    const msgKey = isKnowledgeBaseDisabledByAgent.value ? 'input.kbDisabledByAgent' : 'input.kbLockedByAgent';
    MessagePlugin.warning(t(msgKey));
    return;
  }
  
  const textarea = getTextareaEl();
  if (!textarea) return;
  
  // 关闭其他选择器
  showAgentModeSelector.value = false;
  showModelSelector.value = false;

  textarea.focus();
  
  // 直接显示菜单，不插入 @
  showMention.value = true;
  isMentionTriggeredByButton.value = true;
  mentionQuery.value = "";
  mentionStartPos.value = textarea.selectionStart;
  
  const rect = textarea.getBoundingClientRect();
  const menuHeight = 320;
  
  // 判断输入框上方空间
  const spaceAbove = rect.top;
  const spaceBelow = window.innerHeight - rect.bottom;
  
  // 优先显示在上方，除非上方空间不足且下方空间充足
  if (spaceAbove > menuHeight || spaceAbove > spaceBelow) {
    // Show above textarea
    mentionStyle.value = {
      left: `${rect.left}px`,
      bottom: `${window.innerHeight - rect.top + 8}px`, // 8px padding
      top: 'auto'
    };
  } else {
    // Show below textarea
    mentionStyle.value = {
      left: `${rect.left}px`,
      top: `${rect.bottom + 8}px`,
      bottom: 'auto'
    };
  }
  
  loadMentionItems("");
};

const onMentionSelect = (item: any) => {
  if (item.type === 'kb') {
      settingsStore.addKnowledgeBase(item.id);
  } else if (item.type === 'file') {
      settingsStore.addFile(item.id);
      // Add to local cache immediately
      if (!fileList.value.find(f => f.id === item.id)) {
        fileList.value.push(item);
      }
  }
  
  const textarea = getTextareaEl();
  if (textarea) {
    // 如果是通过输入 @ 触发的，需要删除 @ 和后面的查询文字
    if (!isMentionTriggeredByButton.value) {
      const cursor = textarea.selectionStart;
      const textBeforeAt = query.value.slice(0, mentionStartPos.value);
      const textAfterCursor = query.value.slice(cursor);
      query.value = textBeforeAt + textAfterCursor;
      
      nextTick(() => {
        textarea.selectionStart = textarea.selectionEnd = mentionStartPos.value;
        textarea.focus();
      });
    } else {
      // 通过按钮触发的，如果用户输入了查询词，需要删除查询词
      const cursor = textarea.selectionStart;
      if (cursor > mentionStartPos.value) {
         const textBeforeStart = query.value.slice(0, mentionStartPos.value);
         const textAfterCursor = query.value.slice(cursor);
         query.value = textBeforeStart + textAfterCursor;
         
         nextTick(() => {
           textarea.selectionStart = textarea.selectionEnd = mentionStartPos.value;
           textarea.focus();
         });
      } else {
         // 直接聚焦
         textarea.focus();
      }
    }
  }
  
  showMention.value = false;
};

const removeFile = (id: string) => {
  settingsStore.removeFile(id);
};

const toggleModelSelector = () => {
  // 如果智能体锁定了模型，不允许打开选择器
  if (isModelLockedByAgent.value) {
    MessagePlugin.warning(t('input.modelLockedByAgent'));
    return;
  }
  
  // 互斥：关闭其他
  showMention.value = false;
  showAgentModeSelector.value = false;

  showModelSelector.value = !showModelSelector.value;
  if (showModelSelector.value) {
    if (!availableModels.value.length) {
      loadChatModels();
    }
    // 多次更新位置确保准确
    nextTick(() => {
      updateModelDropdownPosition();
      requestAnimationFrame(() => {
        updateModelDropdownPosition();
        setTimeout(() => {
          updateModelDropdownPosition();
        }, 50);
      });
    });
  }
};

const closeModelSelector = () => {
  showModelSelector.value = false;
};

// 关闭 Agent 模式选择器（点击外部）
const closeAgentModeSelector = () => {
  showAgentModeSelector.value = false;
};

const closeMentionSelector = (e: MouseEvent) => {
  const target = e.target as HTMLElement;
  // 如果点击的是输入框区域，不关闭 Mention 列表（由光标逻辑控制）
  if (target.closest('.rich-input-container')) {
    return;
  }
  showMention.value = false;
};

// 窗口事件处理器
let resizeHandler: (() => void) | null = null;
let scrollHandler: (() => void) | null = null;

onMounted(() => {
  loadKnowledgeBases();
  loadWebSearchConfig();
  loadConversationConfig();
  loadChatModels();
  loadAgents();
  
  // 如果从知识库内部进入，自动选中该知识库
  const kbId = (route.params as any)?.kbId as string;
  if (kbId && !selectedKbIds.value.includes(kbId)) {
    settingsStore.addKnowledgeBase(kbId);
  }

  // 监听点击外部关闭下拉菜单
  document.addEventListener('click', closeAgentModeSelector);
  document.addEventListener('click', closeModelSelector);
  document.addEventListener('click', closeMentionSelector);
  
  // 监听窗口大小变化和滚动，重新计算位置
  resizeHandler = () => {
    if (showModelSelector.value) {
      updateModelDropdownPosition();
    }
    if (showAgentModeSelector.value) {
      updateAgentModeDropdownPosition();
    }
  };
  scrollHandler = () => {
    if (showModelSelector.value) {
      updateModelDropdownPosition();
    }
    if (showAgentModeSelector.value) {
      updateAgentModeDropdownPosition();
    }
  };
  
  window.addEventListener('resize', resizeHandler, { passive: true });
  window.addEventListener('scroll', scrollHandler, { passive: true, capture: true });
});

onUnmounted(() => {
  document.removeEventListener('click', closeAgentModeSelector);
  document.removeEventListener('click', closeModelSelector);
  document.removeEventListener('click', closeMentionSelector);
  if (resizeHandler) {
    window.removeEventListener('resize', resizeHandler);
  }
  if (scrollHandler) {
    window.removeEventListener('scroll', scrollHandler, { capture: true });
  }
});

// 监听路由变化
watch(() => route.params.kbId, (newKbId) => {
  if (newKbId && typeof newKbId === 'string' && !selectedKbIds.value.includes(newKbId)) {
    settingsStore.addKnowledgeBase(newKbId);
  }
});

watch(() => uiStore.showSettingsModal, (visible, prevVisible) => {
  if (prevVisible && !visible) {
    loadWebSearchConfig();
  }
});

watch([selectedKbIds, selectedFileIds], ([kbIds, fileIds]) => {
  if (!kbIds.length && !fileIds.length) {
    closeModelSelector();
  }
}, { deep: true });

const emit = defineEmits(['send-msg', 'stop-generation']);

const createSession = (val: string) => {
  if (!val.trim()) {
    MessagePlugin.info(t('input.messages.enterContent'));
    return;
  }
  if (props.isReplying) {
    return MessagePlugin.error(t('input.messages.replying'));
  }
  // 获取@提及的知识库和文件信息
  const mentionedItems = allSelectedItems.value.map(item => ({
    id: item.id,
    name: item.name,
    type: item.type,
    kb_type: item.type === 'kb' ? (item.kbType || 'document') : undefined
  }));
  emit('send-msg', val, selectedModelId.value, mentionedItems);
  clearvalue();
}

const updateAgentModeDropdownPosition = () => {
  const anchor = agentModeButtonRef.value;
  
  if (!anchor) {
    agentModeDropdownStyle.value = {
      position: 'fixed',
      top: '50%',
      left: '50%',
      transform: 'translate(-50%, -50%)'
    };
    return;
  }

  const rect = anchor.getBoundingClientRect();
  const dropdownWidth = 200;
  const offsetY = 8;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  
  // 水平位置：左对齐
  let left = Math.floor(rect.left);
  const minLeft = 16;
  const maxLeft = Math.max(16, vw - dropdownWidth - 16);
  left = Math.max(minLeft, Math.min(maxLeft, left));
  
  // 垂直位置：紧贴按钮，使用合理的高度避免空白
  const preferredDropdownHeight = 140; // Agent 模式选择器内容较少，用更小的优选高度
  const maxDropdownHeight = 150;
  const minDropdownHeight = 100;
  const topMargin = 20;
  const spaceBelow = vh - rect.bottom;
  const spaceAbove = rect.top;
  
  console.log('[Agent Dropdown] Space check:', {
    spaceBelow,
    spaceAbove,
    windowHeight: vh
  });
  
  let actualHeight: number;
  
  // 优先考虑下方空间
  if (spaceBelow >= minDropdownHeight + offsetY) {
    // 下方有足够空间，向下弹出
    actualHeight = Math.min(preferredDropdownHeight, spaceBelow - offsetY - 16);
    const top = Math.floor(rect.bottom + offsetY);
    
    agentModeDropdownStyle.value = {
      position: 'fixed !important',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      top: `${top}px`,
      maxHeight: `${actualHeight}px`,
      transform: 'none !important',
      margin: '0 !important',
      padding: '0 !important',
    };
    console.log('[Agent Dropdown] Position: below button', { actualHeight });
  } else {
    // 向上弹出，使用 bottom 定位确保紧贴按钮
    const availableHeight = spaceAbove - offsetY - topMargin;
    if (availableHeight >= preferredDropdownHeight) {
      actualHeight = preferredDropdownHeight;
    } else {
      actualHeight = Math.max(minDropdownHeight, availableHeight);
    }
    
    const bottom = vh - rect.top + offsetY;
    
    agentModeDropdownStyle.value = {
      position: 'fixed !important',
      width: `${dropdownWidth}px`,
      left: `${left}px`,
      bottom: `${bottom}px`, // 使用 bottom 定位，确保紧贴按钮
      maxHeight: `${actualHeight}px`,
      transform: 'none !important',
      margin: '0 !important',
      padding: '0 !important',
    };
    console.log('[Agent Dropdown] Position: above button', { actualHeight, bottom });
  }
};

const toggleAgentModeSelector = () => {
  // 互斥
  showMention.value = false;
  showModelSelector.value = false;

  showAgentModeSelector.value = !showAgentModeSelector.value;
  if (showAgentModeSelector.value) {
    // 重新加载智能体列表
    loadAgents();
    // 多次更新位置确保准确
    nextTick(() => {
      updateAgentModeDropdownPosition();
      requestAnimationFrame(() => {
        updateAgentModeDropdownPosition();
        setTimeout(() => {
          updateAgentModeDropdownPosition();
        }, 50);
      });
    });
  }
}

const selectAgentMode = (mode: 'quick-answer' | 'smart-reasoning') => {
  const builtinAgentId = mode === 'smart-reasoning' ? BUILTIN_SMART_REASONING_ID : BUILTIN_QUICK_ANSWER_ID;
  const builtinAgent = agents.value.find(a => a.id === builtinAgentId);
  
  if (builtinAgent) {
    const notReadyReasons = getBuiltinAgentNotReadyReasons(builtinAgent, mode === 'smart-reasoning');
    if (notReadyReasons.length > 0) {
      showAgentNotReadyMessage(builtinAgent, notReadyReasons);
      showAgentModeSelector.value = false;
      return;
    }
  }
  
  const shouldEnableAgent = mode === 'smart-reasoning';
  if (shouldEnableAgent !== isAgentEnabled.value) {
    settingsStore.toggleAgent(shouldEnableAgent);
    // 同时更新选中的智能体
    settingsStore.selectAgent(shouldEnableAgent ? BUILTIN_SMART_REASONING_ID : BUILTIN_QUICK_ANSWER_ID);
    MessagePlugin.success(shouldEnableAgent ? t('input.messages.agentSwitchedOn') : t('input.messages.agentSwitchedOff'));
  }
  showAgentModeSelector.value = false;
}

// 选择智能体（新版）
const handleSelectAgent = (agent: CustomAgent) => {
  // 根据智能体的 agent_mode 判断是否为 Agent 模式
  const isAgentType = agent.config?.agent_mode === 'smart-reasoning';
  
  // 统一检查智能体是否就绪（内置和自定义智能体使用相同逻辑）
  const actualAgent = agent.is_builtin 
    ? (agents.value.find(a => a.id === agent.id) || agent)
    : agent;
  
  const notReadyReasons = agent.is_builtin
    ? getBuiltinAgentNotReadyReasons(actualAgent, isAgentType)
    : getCustomAgentNotReadyReasons(actualAgent);
  
  if (notReadyReasons.length > 0) {
    showAgentNotReadyMessage(agent, notReadyReasons);
    return;
  }
  
  selectedAgentId.value = agent.id;
  settingsStore.toggleAgent(!!isAgentType);
  
  // 同步智能体的配置状态（包括内置和自定义智能体）
  // 1. 同步网络搜索状态
  const agentWebSearch = agent.config?.web_search_enabled;
  if (agentWebSearch !== undefined) {
    // 智能体配置了网络搜索设置，同步到 store
    settingsStore.toggleWebSearch(agentWebSearch);
  } else if (agent.is_builtin) {
    // 如果是内置智能体且未配置网络搜索，不强制修改，保留当前用户设置
    // 或者可以考虑恢复默认值，视需求而定
  }
  
  // 2. 同步模型
  const agentModel = agent.config?.model_id;
  if (agentModel) {
    selectedModelId.value = agentModel;
  } else if (agent.is_builtin) {
    // 如果是内置智能体且未配置特定模型，恢复为系统默认模型
    // 这样可以确保从专用模型切换回普通模式时，模型也切回通用模型
    if (conversationConfig.value?.summary_model_id) {
      selectedModelId.value = conversationConfig.value.summary_model_id;
    }
  }
  
  showAgentModeSelector.value = false;
  
  const message = agent.is_builtin 
    ? (isAgentType ? t('input.messages.agentSwitchedOn') : t('input.messages.agentSwitchedOff'))
    : t('input.messages.agentSelected', { name: agent.name });
  MessagePlugin.success(message);
}

const clearvalue = () => {
  query.value = "";
}

const onKeydown = (val: string, event: { e: { preventDefault(): unknown; keyCode: number; shiftKey: any; ctrlKey: any; }; }) => {
  if (showMention.value) {
    if (event.e.keyCode === 38) { // Up
      event.e.preventDefault();
      mentionActiveIndex.value = Math.max(0, mentionActiveIndex.value - 1);
      return;
    }
    if (event.e.keyCode === 40) { // Down
      event.e.preventDefault();
      mentionActiveIndex.value = Math.min(mentionItems.value.length - 1, mentionActiveIndex.value + 1);
      return;
    }
    if (event.e.keyCode === 13) { // Enter
      event.e.preventDefault();
      if (mentionItems.value[mentionActiveIndex.value]) {
        onMentionSelect(mentionItems.value[mentionActiveIndex.value]);
      }
      return;
    }
    if (event.e.keyCode === 27) { // Esc
        showMention.value = false;
        return;
    }
  }

  // 退格键：当输入框为空且有选中项时，删除最后一个选中项
  if (event.e.keyCode === 8) { // Backspace
    const textarea = getTextareaEl();
    if (textarea && textarea.selectionStart === 0 && textarea.selectionEnd === 0 && query.value === '') {
      const items = allSelectedItems.value;
      if (items.length > 0) {
        event.e.preventDefault();
        const lastItem = items[items.length - 1];
        removeSelectedItem(lastItem);
        return;
      }
    }
  }

  if ((event.e.keyCode == 13 && event.e.shiftKey) || (event.e.keyCode == 13 && event.e.ctrlKey)) {
    return;
  }
  if (event.e.keyCode == 13) {
    event.e.preventDefault();
    createSession(val)
  }
}

const handleGoToWebSearchSettings = () => {
  uiStore.openSettings('websearch');
  if (route.path !== '/platform/settings') {
    router.push('/platform/settings');
  }
};

const handleGoToAgentSettings = (section?: string) => {
  // 跳转到智能体列表页并打开编辑弹窗
  if (selectedAgent.value && !selectedAgent.value.is_builtin) {
    const query: Record<string, string> = { edit: selectedAgent.value.id };
    if (section) {
      query.section = section;
    }
    router.push({ path: '/platform/agents', query });
  } else {
    router.push('/platform/agents');
  }
};

// 获取内置智能体不就绪的原因
const getBuiltinAgentNotReadyReasons = (agent: CustomAgent, isAgentMode: boolean): string[] => {
  const reasons: string[] = []
  const config = agent.config || {}
  
  // 检查对话模型（Summary Model）
  if (!config.model_id || config.model_id.trim() === '') {
    reasons.push(t('input.customAgentMissingSummaryModel'))
  }
  
  // 检查重排模型（Rerank Model）- 如果使用知识库则需要
  if (config.kb_selection_mode !== 'none') {
    if (!config.rerank_model_id || config.rerank_model_id.trim() === '') {
      reasons.push(t('input.customAgentMissingRerankModel'))
    }
  }
  
  // Agent 模式还需要检查允许的工具
  if (isAgentMode) {
    if (!config.allowed_tools || config.allowed_tools.length === 0) {
      reasons.push(t('input.agentMissingAllowedTools'))
    }
  }
  
  return reasons
}

// 获取自定义智能体不就绪的原因（非 Agent 模式，快速回答）
const getCustomAgentNotReadyReasons = (agent: CustomAgent): string[] => {
  const reasons: string[] = []
  const config = agent.config || {}
  
  // 检查对话模型（Summary Model）
  if (!config.model_id || config.model_id.trim() === '') {
    reasons.push(t('input.customAgentMissingSummaryModel'))
  }
  // 检查重排模型（Rerank Model）- 如果使用知识库则需要
  if (config.kb_selection_mode !== 'none') {
    if (!config.rerank_model_id || config.rerank_model_id.trim() === '') {
      reasons.push(t('input.customAgentMissingRerankModel'))
    }
  }
  
  return reasons
}

// 显示智能体未就绪的消息（统一处理内置和自定义智能体）
const showAgentNotReadyMessage = (agent: CustomAgent, reasons: string[]) => {
  const reasonsText = reasons.join('、')
  
  const messageContent = h('div', { style: 'display: flex; flex-direction: column; gap: 8px; max-width: 320px;' }, [
    h('span', { style: 'color: #333; line-height: 1.5;' }, t('input.agentNotReadyDetail', { agentName: agent.name, reasons: reasonsText })),
    h('a', {
      href: '#',
      onClick: (e: Event) => {
        e.preventDefault();
        router.push(`/platform/agents?edit=${agent.id}`);
      },
      style: 'color: #07C05F; text-decoration: none; font-weight: 500; cursor: pointer; align-self: flex-start;',
      onMouseenter: (e: Event) => {
        (e.target as HTMLElement).style.textDecoration = 'underline';
      },
      onMouseleave: (e: Event) => {
        (e.target as HTMLElement).style.textDecoration = 'none';
      }
    }, t('input.goToAgentEditor'))
  ]);
  
  MessagePlugin.warning({
    content: () => messageContent,
    duration: 5000
  });
}

const toggleWebSearch = () => {
  // 互斥：虽然不是弹出层，但操作时关闭其他弹出层体验更好
  showMention.value = false;
  showModelSelector.value = false;
  showAgentModeSelector.value = false;

  // 如果智能体禁用了网络搜索，不允许开启
  if (isWebSearchDisabledByAgent.value) {
    MessagePlugin.warning(t('input.webSearchDisabledByAgent'));
    return;
  }

  if (!isWebSearchConfigured.value) {
    const messageContent = h('div', { style: 'display: flex; flex-direction: column; gap: 6px; max-width: 280px;' }, [
      h('span', { style: 'color: #333; line-height: 1.5;' }, t('input.messages.webSearchNotConfigured')),
      h('a', {
        href: '#',
        onClick: (e: Event) => {
          e.preventDefault();
          handleGoToWebSearchSettings();
        },
        style: 'color: #07C05F; text-decoration: none; font-weight: 500; cursor: pointer; align-self: flex-start;',
        onMouseenter: (e: Event) => {
          (e.target as HTMLElement).style.textDecoration = 'underline';
        },
        onMouseleave: (e: Event) => {
          (e.target as HTMLElement).style.textDecoration = 'none';
        }
      }, t('input.goToSettings'))
    ]);
    MessagePlugin.warning({
      content: () => messageContent,
      duration: 5000
    });
    return;
  }

  const currentValue = settingsStore.isWebSearchEnabled;
  const newValue = !currentValue;
  settingsStore.toggleWebSearch(newValue);
  MessagePlugin.success(newValue ? t('input.messages.webSearchEnabled') : t('input.messages.webSearchDisabled'));
};

const toggleKbSelector = () => {
  showKbSelector.value = !showKbSelector.value;
}

const removeKb = (kbId: string) => {
  settingsStore.removeKnowledgeBase(kbId);
}

const handleStop = async () => {
  if (!props.sessionId) {
    MessagePlugin.warning(t('input.messages.sessionMissing'));
    return;
  }
  
  if (!props.assistantMessageId) {
    console.error('[Stop] Assistant message ID is empty');
    MessagePlugin.warning(t('input.messages.messageMissing'));
    return;
  }
  
  console.log('[Stop] Stopping generation for message:', props.assistantMessageId);
  
  // 发送 stop 事件，通知父组件立即清除 loading 状态
  emit('stop-generation');
  
  try {
    await stopSession(props.sessionId, props.assistantMessageId);
    MessagePlugin.success(t('input.messages.stopSuccess'));
  } catch (error) {
    console.error('Failed to stop session:', error);
    MessagePlugin.error(t('input.messages.stopFailed'));
  }
}

onBeforeRouteUpdate((to, from, next) => {
  clearvalue()
  next()
})

</script>
<template>
  <div class="answers-input">
    <!-- 富文本输入框容器 -->
    <div class="rich-input-container">
        <!-- 选中的知识库和文件标签（显示在输入框内顶部） -->
      <div v-if="allSelectedItems.length > 0" class="selected-tags-inline">
        <span 
          v-for="item in allSelectedItems" 
          :key="item.id" 
          class="inline-tag"
          :class="[
            item.type === 'kb' ? (item.kbType === 'faq' ? 'faq-tag' : 'kb-tag') : 'file-tag',
            { 'agent-configured': item.isAgentConfigured }
          ]"
        >
          <span class="tag-icon">
            <t-icon v-if="item.type === 'kb'" :name="item.kbType === 'faq' ? 'chat-bubble-help' : 'folder'" />
            <t-icon v-else name="file" />
          </span>
          <span class="tag-name">{{ item.name }}</span>
          <span class="tag-remove" @click="removeSelectedItem(item)">×</span>
        </span>
      </div>
      
      <!-- 实际输入框 -->
      <t-textarea 
        ref="textareaRef"
        v-model="query" 
        :placeholder="inputPlaceholder" 
        name="description" 
        :autosize="true" 
        @keydown="onKeydown" 
        @input="onInput"
        @compositionstart="onCompositionStart"
        @compositionend="onCompositionEnd"
      />
    </div>
    
    <!-- Mention Selector -->
    <Teleport to="body">
      <MentionSelector
        :visible="showMention"
        :style="mentionStyle"
        :items="mentionItems"
        :hasMore="mentionHasMore"
        :loading="mentionLoading"
        v-model:activeIndex="mentionActiveIndex"
        @select="onMentionSelect"
        @loadMore="loadMoreMentionItems"
      />
    </Teleport>
    
    <!-- 控制栏 -->
    <div class="control-bar">
      <!-- 左侧控制按钮 -->
      <div class="control-left">
        <!-- Agent 模式切换按钮 -->
        <div 
          ref="agentModeButtonRef"
          class="control-btn agent-mode-btn"
          :class="{ 
            'is-normal': !isCustomAgent && !isAgentEnabled,
            'is-agent': !isCustomAgent && isAgentEnabled,
            'is-custom': isCustomAgent
          }"
          @click.stop="toggleAgentModeSelector"
        >
          <span class="agent-mode-text">
            {{ selectedAgent.name || (isAgentEnabled ? $t('input.agentMode') : $t('input.normalMode')) }}
          </span>
          <svg 
            width="12" 
            height="12" 
            viewBox="0 0 12 12" 
            fill="currentColor"
            class="dropdown-arrow"
            :class="{ 'rotate': showAgentModeSelector }"
          >
            <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z"/>
          </svg>
        </div>

        <!-- Agent 选择器下拉菜单 -->
        <AgentSelector
          :visible="showAgentModeSelector"
          :anchorEl="agentModeButtonRef"
          :currentAgentId="selectedAgentId"
          @close="closeAgentModeSelector"
          @select="handleSelectAgent"
        />

        <!-- WebSearch 开关按钮 -->
        <t-tooltip placement="top" theme="light" :popupProps="{ overlayClassName: 'input-field-tooltip' }">
          <template #content>
            <div v-if="isWebSearchDisabledByAgent" class="tooltip-with-link">
              <span>{{ $t('input.webSearchDisabledByAgent') }}</span>
              <a href="#" @click.prevent="handleGoToAgentSettings('websearch')">{{ $t('input.goToAgentSettings') }}</a>
            </div>
            <span v-else-if="isWebSearchConfigured">{{ isWebSearchEnabled ? $t('input.webSearch.toggleOff') : $t('input.webSearch.toggleOn') }}</span>
            <div v-else class="tooltip-with-link">
              <span>{{ $t('input.webSearch.notConfigured') }}</span>
              <a href="#" @click.prevent="handleGoToWebSearchSettings">{{ $t('input.goToSettings') }}</a>
            </div>
          </template>
          <div 
            class="control-btn websearch-btn"
            :class="{ 
              'active': isWebSearchEnabled && isWebSearchConfigured, 
              'disabled': !isWebSearchConfigured || isWebSearchDisabledByAgent
            }"
            @click.stop="toggleWebSearch"
          >
            <svg 
              width="18" 
              height="18" 
              viewBox="0 0 18 18" 
              fill="none"
              xmlns="http://www.w3.org/2000/svg"
              class="control-icon websearch-icon"
              :class="{ 'active': isWebSearchEnabled && isWebSearchConfigured }"
            >
              <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none"/>
              <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none"/>
              <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none"/>
              <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
              <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
            </svg>
          </div>
        </t-tooltip>

        <!-- @ 知识库/文件选择按钮 -->
        <t-tooltip placement="top" theme="light" :popupProps="{ overlayClassName: 'input-field-tooltip' }">
          <template #content>
            <div v-if="isKnowledgeBaseDisabledByAgent" class="tooltip-with-link">
              <span>{{ $t('input.kbDisabledByAgent') }}</span>
              <a href="#" @click.prevent="handleGoToAgentSettings('knowledge')">{{ $t('input.goToAgentSettings') }}</a>
            </div>
            <span v-else>{{ allSelectedItems.length > 0 ? $t('input.knowledgeBaseWithCount', { count: allSelectedItems.length }) : $t('input.knowledgeBase') }}</span>
          </template>
          <div 
            ref="atButtonRef"
            class="control-btn kb-btn"
            :class="{ 
              'active': allSelectedItems.length > 0,
              'disabled': isKnowledgeBaseDisabledByAgent
            }"
            @click.stop
            @mousedown.prevent="triggerMention"
          >
            <img :src="getImgSrc('at-icon.svg')" alt="@" class="control-icon" />
            <span v-if="allSelectedItems.length > 0" class="kb-count">{{ allSelectedItems.length }}</span>
          </div>
        </t-tooltip>

        <!-- 模型显示 -->
        <t-tooltip :content="isModelLockedByAgent ? $t('input.modelLockedByAgent') : ''" :disabled="!isModelLockedByAgent">
          <div class="model-display" :class="{ 'agent-controlled': isModelLockedByAgent }">
            <div
              ref="modelButtonRef"
              class="model-selector-trigger"
              @click.stop="toggleModelSelector"
            >
              <span class="model-selector-name">
                {{ selectedModel?.name || $t('input.notConfigured') }}
              </span>
              <svg 
                width="12" 
                height="12" 
                viewBox="0 0 12 12" 
                fill="currentColor"
                class="model-dropdown-arrow"
                :class="{ 'rotate': showModelSelector }"
              >
                <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z"/>
              </svg>
            </div>
          </div>
        </t-tooltip>
      </div>

      <Teleport to="body">
        <div v-if="showModelSelector" class="model-selector-overlay" @click="closeModelSelector">
            <div class="model-selector-dropdown" :style="modelDropdownStyle" @click.stop>
            <div class="model-selector-header">
              <span>{{ $t('conversationSettings.models.chatGroupLabel') }}</span>
              <button class="model-selector-add" type="button" @click="handleModelChange('__add_model__')">
                <span class="add-icon">+</span>
                  <span class="add-text">{{ $t('input.addModel') }}</span>
              </button>
            </div>
            <div class="model-selector-content">
              <div
                v-for="model in availableModels"
                :key="model.id"
                class="model-option"
                :class="{ selected: model.id === selectedModelId }"
                @click="handleModelChange(model.id || '')"
              >
                <div class="model-option-main">
                  <span class="model-option-name">{{ model.name }}</span>
                  <span v-if="model.source === 'remote'" class="model-badge-remote">{{ $t('input.remote') }}</span>
                  <span v-else-if="model.parameters?.parameter_size" class="model-badge-local">
                    {{ model.parameters.parameter_size }}
                  </span>
                </div>
                <div v-if="model.description" class="model-option-desc">
                  {{ model.description }}
                </div>
              </div>
              <div v-if="availableModels.length === 0" class="model-option empty">
                {{ $t('input.noModel') }}
              </div>
            </div>
          </div>
        </div>
      </Teleport>

      <!-- 右侧控制按钮组 -->
      <div class="control-right">
        <!-- 停止按钮（仅在回复中时显示） -->
        <t-tooltip 
          v-if="isReplying"
          :content="$t('input.stopGeneration')"
          placement="top"
        >
          <div 
            @click="handleStop" 
            class="control-btn stop-btn"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
              <rect x="5" y="5" width="6" height="6" rx="1" />
            </svg>
          </div>
        </t-tooltip>

        <!-- 发送按钮 -->
      <div 
          v-if="!isReplying"
        @click="createSession(query)" 
        class="control-btn send-btn"
        :class="{ 'disabled': !query.length }"
      >
        <img src="../assets/img/sending-aircraft.svg" :alt="$t('input.send')" />
        </div>
      </div>
    </div>

    <!-- 知识库选择下拉（使用 Teleport 传送到 body，避免父容器定位影响） -->
    <Teleport to="body">
    <KnowledgeBaseSelector
      v-model:visible="showKbSelector"
        :anchorEl="atButtonRef"
      @close="showKbSelector = false"
    />
    </Teleport>
  </div>
</template>
<script lang="ts">
const getImgSrc = (url: string) => {
  return new URL(`/src/assets/img/${url}`, import.meta.url).href;
}
</script>
<style scoped lang="less">
.answers-input {
  position: absolute;
  z-index: 99;
  bottom: 60px;
  left: 50%;
  transform: translateX(-400px);
}

/* 富文本输入框容器 */
.rich-input-container {
  position: relative;
  width: 800px;
  background: var(--td-bg-color-container, #FFF);
  border-radius: 12px;
  border: 1px solid var(--td-component-border, #E7E7E7);
  box-shadow: 0 6px 6px 0 rgba(0, 0, 0, 0.04), 0 12px 12px -1px rgba(0, 0, 0, 0.08);
  
  &:focus-within {
    border-color: var(--td-brand-color, #07C05F);
  }
}

/* 选中的标签（输入框内顶部） */
.selected-tags-inline {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 12px 16px 8px;
  border-bottom: 1px solid var(--td-component-border, #f0f0f0);
}

.inline-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 8px;
  border-radius: 6px;
  font-size: 12px;
  font-weight: 500;
  cursor: default;
  transition: all 0.15s;
  background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  border: 1px solid transparent;
  color: var(--td-text-color-primary, #333);
  
  /* KB - Document (Greenish tint) */
  &.kb-tag {
    background: rgba(16, 185, 129, 0.08);
    color: #059669;
    
    .tag-icon {
      color: #10b981;
    }
  }

  /* KB - FAQ (Blueish tint) */
  &.faq-tag {
    background: rgba(0, 82, 217, 0.08);
    color: #0052d9;
    
    .tag-icon {
      color: #0052d9;
    }
  }
  
  /* File (Orange tint) */
  &.file-tag {
    background: rgba(237, 123, 47, 0.08);
    color: #e65100;
    
    .tag-icon {
      color: #ed7b2f;
    }
  }
  
  .tag-icon {
    font-size: 14px;
    display: flex;
    align-items: center;
  }
  
  .tag-name {
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: currentColor;
  }
  
  .tag-remove {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 14px;
    height: 14px;
    margin-left: 2px;
    border-radius: 50%;
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
    opacity: 0.5;
    transition: opacity 0.15s, background 0.15s;
    color: currentColor;
    
    &:hover {
      opacity: 1;
      background: rgba(0, 0, 0, 0.1);
    }
  }
  
  // 智能体配置的标签样式（用虚线边框区分，不显示锁图标）
  &.agent-configured {
    border-style: dashed;
    opacity: 0.9;
  }
}

:deep(.t-textarea__inner) {
  width: 100%;
  max-height: 200px !important;
  min-height: 120px !important;
  resize: none;
  color: var(--td-text-color-primary, #000000e6);
  font-size: 16px;
  font-weight: 400;
  line-height: 24px;
  font-family: var(--td-font-family, "PingFang SC");
  padding: 12px 16px 56px 16px;
  border-radius: 0 0 12px 12px;
  border: none;
  box-sizing: border-box;
  background: transparent;
  box-shadow: none;

  &:focus {
    border: none;
    box-shadow: none;
  }

  &::placeholder {
    color: var(--td-text-color-placeholder, #00000066);
    font-family: var(--td-font-family, "PingFang SC");
    font-size: 16px;
    font-weight: 400;
    line-height: 24px;
  }
}

/* 当没有选中标签时，textarea 样式 */
.rich-input-container:not(:has(.selected-tags-inline)) :deep(.t-textarea__inner) {
  border-radius: 12px;
  padding-top: 16px;
}

/* 控制栏 */
.control-bar {
  position: absolute;
  bottom: 12px;
  left: 16px;
  right: 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
  max-height: 56px;
  z-index: 10;
  background: linear-gradient(to bottom, rgba(255, 255, 255, 0) 0%, var(--td-bg-color-container, #fff) 40%, var(--td-bg-color-container, #fff) 100%);
  pointer-events: auto;
  padding-top: 8px;
}

.control-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  flex-wrap: wrap;
  min-width: 0;
}

.control-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 4px;
  padding: 6px 10px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer, #f5f5f5);
  cursor: pointer;
  transition: background 0.12s;
  user-select: none;
  flex-shrink: 0;

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover, #e6e6e6);
  }

  &.disabled {
    opacity: 0.5;
    cursor: not-allowed;
    
    &:hover {
      background: var(--td-bg-color-secondarycontainer, #f5f5f5);
    }
  }
}

.agent-mode-btn {
  height: 28px;
  padding: 0 10px;
  min-width: auto;
  font-weight: 500;
  border: 1px solid transparent;
  transition: background 0.12s, border-color 0.12s;
  position: relative;
  
  // 内置普通模式 - 绿色
  &.is-normal {
    background: linear-gradient(135deg, rgba(16, 185, 129, 0.12) 0%, rgba(16, 185, 129, 0.08) 100%);
    border-color: rgba(16, 185, 129, 0.35);
    
    .agent-mode-text {
      color: #059669;
      font-weight: 600;
    }
    
    .dropdown-arrow {
      color: #059669;
    }
    
    &:hover {
      background: linear-gradient(135deg, rgba(16, 185, 129, 0.18) 0%, rgba(16, 185, 129, 0.12) 100%);
      border-color: rgba(16, 185, 129, 0.5);
    }
  }
  
  // 内置 Agent 模式 - 紫色
  &.is-agent {
    background: linear-gradient(135deg, rgba(124, 77, 255, 0.12) 0%, rgba(124, 77, 255, 0.08) 100%);
    border-color: rgba(124, 77, 255, 0.35);
    
    .agent-mode-text {
      color: #7c4dff;
      font-weight: 600;
    }
    
    .dropdown-arrow {
      color: #7c4dff;
    }
    
    &:hover {
      background: linear-gradient(135deg, rgba(124, 77, 255, 0.18) 0%, rgba(124, 77, 255, 0.12) 100%);
      border-color: rgba(124, 77, 255, 0.5);
    }
  }
  
  // 自定义智能体 - 蓝色
  &.is-custom {
    background: linear-gradient(135deg, rgba(59, 130, 246, 0.12) 0%, rgba(59, 130, 246, 0.08) 100%);
    border-color: rgba(59, 130, 246, 0.35);
    
    .agent-mode-text {
      color: #3b82f6;
      font-weight: 600;
    }
    
    .dropdown-arrow {
      color: #3b82f6;
    }
    
    &:hover {
      background: linear-gradient(135deg, rgba(59, 130, 246, 0.18) 0%, rgba(59, 130, 246, 0.12) 100%);
      border-color: rgba(59, 130, 246, 0.5);
    }
  }
}

.agent-icon {
  width: 18px;
  height: 18px;
  flex-shrink: 0;
}

.agent-btn-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 5px;
  flex-shrink: 0;
  
  &.normal {
    background: rgba(7, 192, 95, 0.12);
    color: #059669;
  }
  
  &.agent {
    background: rgba(124, 77, 255, 0.12);
    color: #7c4dff;
  }
}

.agent-mode-text {
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
  font-weight: 500;
  white-space: nowrap;
  margin: 0 4px;
}

.control-icon {
  width: 18px;
  height: 18px;
}

.kb-btn {
  height: 28px;
  padding: 0 10px;
  min-width: auto;
  position: relative;
  
  &.active {
    background: rgba(16, 185, 129, 0.1);
    color: #07C05F;
    
    &:hover {
      background: rgba(16, 185, 129, 0.15);
    }
  }
  
  &.agent-controlled {
    cursor: not-allowed;
    opacity: 0.85;
    
    &:hover {
      background: var(--td-bg-color-secondarycontainer, #f5f5f5);
    }
    
    &.active:hover {
      background: rgba(16, 185, 129, 0.1);
    }
  }
}

.kb-count {
  position: absolute;
  top: -4px;
  right: -4px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  background: #07C05F;
  color: white;
  font-size: 10px;
  font-weight: 600;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.kb-btn-text {
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
  font-weight: 500;
  white-space: nowrap;
}

.kb-btn.active .kb-btn-text {
  color: #07C05F;
}

.websearch-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  min-width: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  
  &.active {
    background: rgba(16, 185, 129, 0.1);
    
    .websearch-icon {
      color: #07C05F;
    }
    
    &:hover {
      background: rgba(16, 185, 129, 0.15);
    }
  }
  
  &:not(.active) {
    .websearch-icon {
      color: var(--td-text-color-secondary, #666);
    }
    
    &:hover {
      background: var(--td-bg-color-secondarycontainer-hover, #f0f0f0);
      
      .websearch-icon {
        color: var(--td-text-color-primary, #333);
      }
    }
  }
  
  &.agent-controlled {
    cursor: not-allowed;
    opacity: 0.85;
    
    &:hover {
      background: var(--td-bg-color-secondarycontainer, #f5f5f5);
    }
    
    &.active:hover {
      background: rgba(16, 185, 129, 0.1);
    }
  }
}

:global(.input-field-tooltip) {
  .t-popup__content {
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
    border: 1px solid var(--td-component-border, #e7e7e7);
  }
}

:global(.tooltip-with-link) {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-width: 220px;
  font-size: 12px;
  color: var(--td-text-color-primary, #333);
}

:global(.tooltip-with-link a) {
  color: #07C05F;
  font-weight: 500;
  text-decoration: none;
}

:global(.tooltip-with-link a:hover) {
  text-decoration: underline;
}

.websearch-icon {
  width: 18px;
  height: 18px;
}

.dropdown-arrow {
  width: 10px;
  height: 10px;
  margin-left: 2px;
  transition: transform 0.12s;
  
  &.rotate {
    transform: rotate(180deg);
  }
}

.control-right {
  display: flex;
  align-items: center;
  gap: 8px;
}

.stop-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  background: rgba(16, 185, 129, 0.08);
  color: #07C05F;
  border: 1.5px solid rgba(16, 185, 129, 0.2);
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  
  &:hover {
    background: rgba(16, 185, 129, 0.12);
    border-color: #07C05F;
  }
  
  &:active {
    background: rgba(16, 185, 129, 0.15);
  }
  
  svg {
    display: none;
  }
  
  &::before {
    content: '';
    width: 12px;
    height: 12px;
    background: #07C05F;
    border-radius: 50%;
    display: block;
  }
}

.send-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  background-color: #07C05F;
  
  &:hover:not(.disabled) {
    background-color: #059669;
  }
  
  &.disabled {
    background-color: #b5eccf;
  }
  
  img {
    width: 16px;
    height: 16px;
  }
}

/* 模型显示样式 */
.model-display {
  display: flex;
  align-items: center;
  margin-left: auto;
  flex-shrink: 0;
  
  &.agent-controlled {
    .model-selector-trigger {
      cursor: not-allowed;
      opacity: 0.85;
      
      &:hover {
        background: rgba(16, 185, 129, 0.1);
        border-color: rgba(16, 185, 129, 0.3);
      }
    }
  }
}

.model-selector-trigger {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  min-width: 100px;
  height: 22px;
  border-radius: 6px;
  border: 1px solid rgba(16, 185, 129, 0.3);
  background: rgba(16, 185, 129, 0.1);
  transition: background 0.12s, border-color 0.12s;
  cursor: pointer;
}

.model-selector-trigger:hover {
  background: rgba(16, 185, 129, 0.15);
  border-color: rgba(16, 185, 129, 0.45);
}

.model-selector-trigger.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.model-selector-trigger.disabled:hover {
  background: rgba(16, 185, 129, 0.1);
  border-color: rgba(16, 185, 129, 0.3);
}

.model-selector-name {
  flex: 1;
  font-size: 12px;
  font-weight: 600;
  color: #07C05F;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-dropdown-arrow {
  width: 10px;
  height: 10px;
  color: #07C05F;
  flex-shrink: 0;
  transition: transform 0.12s;
  
  &.rotate {
    transform: rotate(180deg);
  }
}

.model-selector-trigger.disabled .model-dropdown-arrow {
  color: rgba(16, 185, 129, 0.4);
}

.model-selector-overlay {
  position: fixed;
  inset: 0;
  z-index: 9998;
  background: transparent;
  touch-action: none;
}

.model-selector-dropdown {
  position: fixed !important;
  z-index: 9999;
  background: var(--td-bg-color-container, #fff);
  border-radius: 10px;
  box-shadow: var(--td-shadow-2, 0 6px 28px rgba(15, 23, 42, 0.08));
  border: 1px solid var(--td-component-border, #e7e9eb);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  margin: 0 !important;
  padding: 0 !important;
  transform: none !important;
}

.model-selector-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  border-bottom: 1px solid var(--td-component-stroke, #f0f0f0);
  background: var(--td-bg-color-container, #fff);
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-secondary, #666);
}

.model-selector-content {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
  padding: 6px 8px;
}

.model-selector-add {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--td-brand-color, #07c05f);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s;
  
  .add-icon {
    font-size: 14px;
    line-height: 1;
    font-weight: 400;
  }
  
  &:hover {
    color: var(--td-brand-color-hover, #05a04f);
    background: var(--td-bg-color-secondarycontainer, #f3f3f3);
  }
}

.model-option {
  padding: 6px 8px;
  cursor: pointer;
  transition: background 0.12s;
  border-radius: 6px;
  margin-bottom: 4px;
  
  &:last-child {
    margin-bottom: 0;
  }
  
  &:hover {
    background: var(--td-bg-color-container-hover, #f6f8f7);
  }
  
  &.selected {
    background: var(--td-brand-color-light, #eefdf5);
    
    .model-option-name {
      color: #10b981;
      font-weight: 600;
    }
  }
  
  &.empty {
    color: var(--td-text-color-disabled, #9aa0a6);
    cursor: default;
    text-align: center;
    padding: 20px 8px;
    
    &:hover {
      background: transparent;
    }
  }
}

.model-option-main {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 1px;
}

.model-option-name {
  font-size: 12px;
  color: var(--td-text-color-primary, #222);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.4;
}

.model-option-desc {
  font-size: 11px;
  color: var(--td-text-color-secondary, #8b9196);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin-top: 1px;
}

.model-badge-remote,
.model-badge-local {
  display: inline-block;
  padding: 1px 5px;
  font-size: 10px;
  border-radius: 3px;
  font-weight: 500;
  flex-shrink: 0;
}

.model-badge-remote {
  background: rgba(16, 185, 129, 0.1);
  color: #10b981;
}

.model-badge-local {
  background: rgba(139, 145, 150, 0.1);
  color: #52575a;
}

/* Agent 模式选择下拉菜单 */
.agent-mode-selector-overlay {
  position: fixed;
  inset: 0;
  z-index: 9998;
  background: transparent;
  touch-action: none;
}

.agent-mode-selector-dropdown {
  position: fixed !important;
  z-index: 9999;
  background: var(--td-bg-color-container, #fff);
  border-radius: 10px;
  box-shadow: var(--td-shadow-2, 0 6px 28px rgba(15, 23, 42, 0.08));
  border: 1px solid var(--td-component-border, #e7e9eb);
  overflow: hidden;
  padding: 6px 8px;
  min-width: 200px;
  display: flex;
  flex-direction: column;
  margin: 0 !important;
  padding: 0 !important;
  transform: none !important;
}

.agent-mode-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  cursor: pointer;
  transition: background 0.12s;
  border-radius: 6px;
  position: relative;
  margin: 4px 6px;
  
  &:hover:not(.disabled) {
    background: var(--td-bg-color-container-hover, #f6f8f7);
  }
  
  &.disabled {
    opacity: 0.6;
    cursor: not-allowed;
    
    &:hover {
      background: transparent;
    }
  }
  
  &.selected {
    background: var(--td-brand-color-light, #eefdf5);
    
    .agent-mode-option-name {
      color: #10b981;
      font-weight: 700;
    }
  }
}

.agent-mode-option-main {
  display: flex;
  flex-direction: column;
  gap: 1px;
  flex: 1;
  min-width: 0;
}

.agent-mode-option-name {
  font-size: 12px;
  font-weight: 600;
  color: var(--td-text-color-primary, #222);
  line-height: 1.4;
  transition: color 0.12s;
}

.agent-mode-option-desc {
  font-size: 11px;
  color: var(--td-text-color-secondary, #8b9196);
  line-height: 1.3;
}

.check-icon {
  width: 14px;
  height: 14px;
  color: #10b981;
  flex-shrink: 0;
  margin-left: 6px;
}

.agent-mode-warning {
  display: flex;
  align-items: center;
  margin-left: 6px;
  
  .warning-icon {
    color: #ff9800;
    font-size: 14px;
  }
}

.agent-mode-footer {
  padding: 6px 10px;
  border-top: 1px solid var(--td-component-border, #f2f4f5);
  margin-top: 2px;
  background: var(--td-bg-color-secondarycontainer, #fafcfc);
}

.agent-mode-link {
  color: #10b981;
  text-decoration: none;
  font-size: 11px;
  font-weight: 500;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  transition: all 0.12s;
  
  &:hover {
    color: #059669;
    text-decoration: underline;
  }
}
</style>


