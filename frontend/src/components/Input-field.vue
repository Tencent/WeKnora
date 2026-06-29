<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick, h } from "vue";
import { storeToRefs } from 'pinia';
import { useRoute, useRouter } from 'vue-router';
import { onBeforeRouteUpdate } from 'vue-router';
import { MessagePlugin } from "tdesign-vue-next";
import { useSettingsStore } from '@/stores/settings';
import { useUIStore } from '@/stores/ui';
import { useMenuStore } from '@/stores/menu';
import { listKnowledgeBases, searchKnowledge, batchQueryKnowledge, listKnowledgeTags } from '@/api/knowledge-base';
import { listMCPServices, type MCPService } from '@/api/mcp-service';
import { stopSession } from '@/api/chat';
import { useOrganizationStore } from '@/stores/organization';
import KnowledgeBaseSelector from './KnowledgeBaseSelector.vue';
import MentionSelector from './MentionSelector.vue';
import AgentSelector from './AgentSelector.vue';
import { getCaretCoordinates } from '@/utils/caret';
import { getRootZoom, rectToCssPx, cssViewportSize } from '@/utils/zoom';
import { type ModelConfig } from '@/api/model';
import { type CustomAgent, BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from '@/api/agent';
import { useChatResourcesStore } from '@/stores/chatResources';
import { useEditorResourcesStore } from '@/stores/editorResources';
import { useI18n } from 'vue-i18n';
import AttachmentUpload, { type AttachmentFile } from './AttachmentUpload.vue';
import {
  kbSatisfiesAgentRequirements,
  deriveKbFilterForAgent,
  toolsConsumeFiles,
  type ScopeCapabilities,
} from '@/utils/tool-capabilities';
import {
  getAgentNotReadyReasonKeys,
  resolveAgentNotReadySection,
  resolveAgentNotReadyHighlight,
  canLocallyConfigureAgent,
  type AgentNotReadyReasonKey,
} from '@/utils/agent-readiness';
import { formatLocalizedList } from '@/utils/format-list';
import type { MentionItem, MentionItemType, MentionRequestItem } from '@/types/mention';

const route = useRoute();
const router = useRouter();
const settingsStore = useSettingsStore();
const uiStore = useUIStore();
const orgStore = useOrganizationStore();
const menuStore = useMenuStore();
const chatResources = useChatResourcesStore();
const editorResources = useEditorResourcesStore();
const {
  agents,
  disabledOwnAgentIds,
  allModels,
  chatModels: availableModels,
  webSearchProviders,
} = storeToRefs(chatResources);
const { t, locale } = useI18n();

let query = ref("");
const showKbSelector = ref(false);

// Image upload state
const uploadedImages = ref<Array<{ file: File; preview: string }>>([]);
const imageInputRef = ref<HTMLInputElement>();
const imageUploading = ref(false);

// Attachment upload state
const attachmentUploadRef = ref<InstanceType<typeof AttachmentUpload>>();
const uploadedAttachments = ref<AttachmentFile[]>([]);
const CHAT_FILE_DROP_EVENT = 'weknora:chat-file-drop';

const isImageFile = (file: File) => {
  if (file.type.startsWith('image/')) {
    return true;
  }
  const fileName = file.name.toLowerCase();
  return ['.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'].some(ext => fileName.endsWith(ext));
};

const handleDroppedFiles = (files: File[]) => {
  if (!files.length) return;

  const imageFiles = files.filter(isImageFile);
  const attachmentFiles = files.filter(file => !isImageFile(file));

  if (imageFiles.length > 0) {
    if (isImageUploadEnabledByAgent.value) {
      addImageFiles(imageFiles);
    } else {
      MessagePlugin.warning(t('input.imageUploadDisabledByAgent'));
    }
  }

  if (attachmentFiles.length > 0) {
    attachmentUploadRef.value?.addFiles(attachmentFiles);
  }
};

const handleChatFileDrop = (event: Event) => {
  const customEvent = event as CustomEvent<{ files?: File[] }>;
  const files = customEvent.detail?.files;
  if (!files || files.length === 0) return;
  handleDroppedFiles(files);
};

const handleImageSelect = (event: Event) => {
  const input = event.target as HTMLInputElement;
  if (!input.files) return;
  addImageFiles(Array.from(input.files));
  input.value = '';
};

const addImageFiles = (files: File[]) => {
  if (!isImageUploadEnabledByAgent.value) return;
  const allowed = ['image/jpeg', 'image/png', 'image/gif', 'image/webp'];
  const maxSize = 10 * 1024 * 1024;
  for (const file of files) {
    if (uploadedImages.value.length >= 5) {
      MessagePlugin.warning(t('chat.imageTooMany'));
      break;
    }
    if (!allowed.includes(file.type)) {
      MessagePlugin.warning(t('chat.imageTypeSizeError'));
      continue;
    }
    if (file.size > maxSize) {
      MessagePlugin.warning(t('chat.imageTypeSizeError'));
      continue;
    }
    uploadedImages.value.push({ file, preview: URL.createObjectURL(file) });
  }
};

const removeImage = (index: number) => {
  const removed = uploadedImages.value.splice(index, 1);
  if (removed.length > 0) URL.revokeObjectURL(removed[0].preview);
};

const triggerImageUpload = () => {
  imageInputRef.value?.click();
};
const atButtonRef = ref<HTMLElement>();
const showAgentModeSelector = ref(false);
const agentModeButtonRef = ref<HTMLElement>();
const agentModeDropdownStyle = ref<Record<string, string>>({});

const selectedAgentId = computed({
  get: () => settingsStore.selectedAgentId || BUILTIN_QUICK_ANSWER_ID,
  set: (val: string) => settingsStore.selectAgent(val)
});
const selectedAgent = computed(() => {
  // When a shared-agent source tenant is set, resolve from sharedAgents FIRST.
  // Builtin agents (e.g. builtin-smart-reasoning) use the same constant ID across
  // tenants, so falling back to agents.value first would incorrectly return the
  // current tenant's own builtin instead of the shared one.
  const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
  if (sourceTenantId && orgStore.sharedAgents?.length) {
    const shared = orgStore.sharedAgents.find(
      s => s.agent.id === selectedAgentId.value && String(s.source_tenant_id) === sourceTenantId
    );
    if (shared?.agent) return shared.agent as CustomAgent;
  }
  const mine = agents.value.find(a => a.id === selectedAgentId.value);
  if (mine) return mine;
  return {
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
  // 共享智能体的 config 来自源租户，直接使用 agent.config，避免被本租户同 ID 的 builtin 覆盖
  const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
  if (agent?.is_builtin && !sourceTenantId) {
    const builtinAgent = agents.value.find(a => a.id === agent.id);
    return !!builtinAgent?.config;
  }
  return !!agent?.config;
});

// 获取当前智能体的实际配置（内置智能体从 agents 列表获取）
const currentAgentConfig = computed(() => {
  const agent = selectedAgent.value;
  // For shared agents, agent.config already carries the source tenant's settings.
  // Re-looking it up by ID in the local agents.value would clobber it with the
  // current tenant's own builtin config (same constant ID for builtins).
  const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
  if (agent?.is_builtin && !sourceTenantId) {
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

// 智能体的知识库选择模式
const agentKBSelectionMode = computed(() => {
  if (!hasAgentConfig.value) return null; // null 表示不受智能体控制
  return currentAgentConfig.value?.kb_selection_mode || 'all';
});

// 共享智能体下的知识库列表（来自 listKnowledgeBases(agent_id)），用于已选知识库展示与 org 角标
const sharedAgentKbList = ref<Array<{ id: string; name: string; type?: string; knowledge_count?: number; chunk_count?: number }>>([]);

// 当智能体改变时，模型、网络搜索、可@知识库列表均跟随新智能体配置
// 知识库：用新智能体配置的列表替换当前选中，使已选与可@列表一致（含共享智能体）
watch([selectedAgentId, agentKnowledgeBases, agentKBSelectionMode], ([newAgentId, newAgentKbs, newKbMode], [oldAgentId]) => {
  if (settingsStore._isApplyingSessionState) return;
  if (newAgentId !== oldAgentId && oldAgentId !== undefined) {
    if (newKbMode === 'none') {
      settingsStore.selectKnowledgeBases([]);
    } else {
      settingsStore.selectKnowledgeBases(newAgentKbs && newAgentKbs.length > 0 ? [...newAgentKbs] : []);
    }
    // 若 @ 面板已打开，刷新可@列表以立即反映新智能体的知识库范围
    if (showMention.value) {
      loadMentionItems(mentionQuery.value, true);
    }
    // Clear images when switching to an agent that doesn't support image upload
    if (!isImageUploadEnabledByAgent.value && uploadedImages.value.length > 0) {
      uploadedImages.value.forEach(img => URL.revokeObjectURL(img.preview));
      uploadedImages.value = [];
    }
  }
}, { immediate: true });

// 共享智能体时预取该智能体知识库列表，使已选标签在未打开 @ 时也能显示共享空间角标
watch([selectedAgentId, () => settingsStore.selectedAgentSourceTenantId], async ([agentId, sourceTenantId]) => {
  if (sourceTenantId && agentId) {
    try {
      const list = await chatResources.ensureAgentKnowledgeBases(agentId);
      sharedAgentKbList.value = list.map((kb: any) => ({
        id: kb.id,
        name: kb.name,
        type: kb.type || 'document',
        knowledge_count: kb.knowledge_count,
        chunk_count: kb.chunk_count
      }));
    } catch {
      sharedAgentKbList.value = [];
    }
  } else {
    sharedAgentKbList.value = [];
  }
}, { immediate: true });

// 智能体是否启用了网络搜索
const agentWebSearchEnabled = computed(() => {
  if (!hasAgentConfig.value) return null; // null 表示不受智能体控制
  return currentAgentConfig.value?.web_search_enabled ?? true;
});

const agentWebSearchProviderId = computed(() => {
  if (!hasAgentConfig.value) return '';
  return currentAgentConfig.value?.web_search_provider_id || '';
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
const isMentionDisabled = computed(() => {
  if (settingsStore.isAgentStreamMode && isKnowledgeBaseDisabledByAgent.value) {
    return agentMCPSelectionMode.value === 'none' && agentSkillsSelectionMode.value === 'none';
  }
  return isKnowledgeBaseLockedByAgent.value && !settingsStore.isAgentStreamMode;
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

// 智能体配置的工具列表，驱动 @ 菜单的 KB 兼容性过滤
const agentAllowedTools = computed<string[]>(() => {
  if (!hasAgentConfig.value) return [];
  return currentAgentConfig.value?.allowed_tools || [];
});

type SelectionMode = 'all' | 'selected' | 'none';
const normalizeSelectionMode = (mode?: string): SelectionMode => {
  return mode === 'all' || mode === 'selected' || mode === 'none' ? mode : 'none';
};

const agentMCPSelectionMode = computed<SelectionMode>(() => {
  if (!settingsStore.isAgentStreamMode || !hasAgentConfig.value) return 'none';
  return normalizeSelectionMode(currentAgentConfig.value?.mcp_selection_mode);
});

const agentMCPServiceIds = computed<string[]>(() => {
  if (agentMCPSelectionMode.value !== 'selected') return [];
  return currentAgentConfig.value?.mcp_services || [];
});

const isMCPAllowedByAgent = (service: MCPService) => {
  if (!settingsStore.isAgentStreamMode || !service.enabled) return false;
  const mode = agentMCPSelectionMode.value;
  if (mode === 'none') return false;
  if (mode === 'selected') return agentMCPServiceIds.value.includes(service.id);
  return true;
};

const agentSkillsSelectionMode = computed<SelectionMode>(() => {
  if (!settingsStore.isAgentStreamMode || !hasAgentConfig.value) return 'none';
  return normalizeSelectionMode(currentAgentConfig.value?.skills_selection_mode);
});

const agentSelectedSkills = computed<string[]>(() => {
  if (agentSkillsSelectionMode.value !== 'selected') return [];
  return currentAgentConfig.value?.selected_skills || [];
});

const isSkillAllowedByAgent = (skillName: string) => {
  if (!settingsStore.isAgentStreamMode || !editorResources.skillsAvailable) return false;
  const mode = agentSkillsSelectionMode.value;
  if (mode === 'none') return false;
  if (mode === 'selected') return agentSelectedSkills.value.includes(skillName);
  return true;
};

// 切换智能体时清理不允许的 MCP / Skill @mention
watch([selectedAgentId, agentMCPSelectionMode, agentSkillsSelectionMode], ([newAgentId], [oldAgentId]) => {
  if (settingsStore._isApplyingSessionState) return;
  if (newAgentId === oldAgentId || oldAgentId === undefined) return;

  const mcpMode = agentMCPSelectionMode.value;
  if (mcpMode === 'none') {
    settingsStore.settings.selectedMCPServices = [];
  } else if (mcpMode === 'selected') {
    const allowed = new Set(agentMCPServiceIds.value);
    settingsStore.settings.selectedMCPServices = (settingsStore.settings.selectedMCPServices || [])
      .filter(id => allowed.has(id));
  }

  const skillsMode = agentSkillsSelectionMode.value;
  if (skillsMode === 'none') {
    settingsStore.settings.selectedSkills = [];
  } else if (skillsMode === 'selected') {
    const allowed = new Set(agentSelectedSkills.value);
    settingsStore.settings.selectedSkills = (settingsStore.settings.selectedSkills || [])
      .filter(name => allowed.has(name));
  }
});

// 从 KB 对象里抽能力位，优先用 backend 显式的 capabilities 字段；否则回退到 indexing_strategy，
// 最后拿 kb.type === 'faq' 兜底。shared / owned / agent-scope 三路的 KB 响应结构一致。
const kbToScopeCaps = (kb: any): Partial<ScopeCapabilities> => {
  if (kb?.capabilities) {
    return {
      vector: !!kb.capabilities.vector,
      keyword: !!kb.capabilities.keyword,
      wiki: !!kb.capabilities.wiki,
      graph: !!kb.capabilities.graph,
      faq: !!kb.capabilities.faq,
    };
  }
  const s = kb?.indexing_strategy;
  return {
    vector: s ? !!s.vector_enabled : false,
    keyword: s ? !!s.keyword_enabled : false,
    wiki: s ? !!s.wiki_enabled : false,
    graph: s ? !!s.graph_enabled : false,
    faq: kb?.type === 'faq',
  };
};

// 当前智能体的 agent_mode（quick-answer / smart-reasoning），用于把
// "RAG-only 模式不能 @ wiki-only 知识库"这种隐式约束带进 KB 过滤。
const agentMode = computed(() => {
  if (!hasAgentConfig.value) return '';
  return currentAgentConfig.value?.agent_mode || '';
});

// "all" 模式 + 智能体工具有 KB 依赖时的兼容性过滤；'selected'/'none' 不在这里二次过滤
// （selected 由编辑器负责，none 已经空表）。
const isKbCompatibleWithAgent = (kb: any): boolean => {
  if (!hasAgentConfig.value) return true;
  if (agentKBSelectionMode.value !== 'all') return true;
  return kbSatisfiesAgentRequirements(kbToScopeCaps(kb), agentMode.value, agentAllowedTools.value);
};

// 仅在用户没输入搜索词、且是因智能体工具兼容性把列表清空的场景展示专用空态文案
const mentionEmptyHint = computed(() => {
  if (mentionQuery.value) return '';
  if (!hasAgentConfig.value) return '';
  if (agentKBSelectionMode.value !== 'all') return '';
  // 列表为空 && 兼容性过滤器其实是有效的（否则"全部"不会被剔空）
  if (mentionItems.value.length !== 0) return '';
  const filter = deriveKbFilterForAgent(agentMode.value, agentAllowedTools.value);
  if (!filter) return '';
  return t('mentionDetail.noCompatibleKbForAgent');
});

// 智能体是否启用了图片上传（多模态）
const isImageUploadEnabledByAgent = computed(() => {
  if (!hasAgentConfig.value) return false;
  return currentAgentConfig.value?.image_upload_enabled === true;
});

// 模型选择是否被智能体锁定 - 已移除锁定逻辑，允许用户自由切换模型
const isModelLockedByAgent = computed(() => {
  return false;
});

// Mention related state
const showMention = ref(false);
const mentionQuery = ref("");
const mentionItems = ref<MentionItem[]>([]);
/** 文件 ID -> 知识库 ID（用于批量查询时传 kb_id，支持共享知识库下的文档） */
const fileIdToKbId = ref<Record<string, string>>({});
const mcpServices = ref<MCPService[]>([]);
const mentionActiveIndex = ref(0);
const mentionStyle = ref<Record<string, string>>({});
const textareaRef = ref<any>(null); // Ref to t-textarea component
const mentionSelectorRef = ref<any>(null);
const mentionStartPos = ref(0);
const isComposing = ref(false);
const isMentionTriggeredByButton = ref(false);
const mentionHasMore = ref(false);
const mentionGroupCounts = ref<Partial<Record<MentionItemType, number>>>({});
// 当前 @ 会话可见的 KB ID 集合（含工具兼容性过滤），分页加载文件时复用，
// 避免 append 请求把不兼容 KB 的文件漏进来。`null` 表示"不受限制"（非智能体场景）
const mentionAllowedKbIds = ref<Set<string> | null>(null);
const mentionLoading = ref(false);
const mentionOffset = ref(0);
const MENTION_PAGE_SIZE = 20;

// 共享智能体时用于标识「共享空间」的展示名（组织名或共享者），供 @ 列表与已选标签显示角标
const sharedAgentOrgName = computed(() => {
  const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
  const agentId = selectedAgentId.value;
  if (!sourceTenantId || !agentId || !orgStore.sharedAgents?.length) return '';
  const shared = orgStore.sharedAgents.find(
    (s: any) => s.agent?.id === agentId && String(s.source_tenant_id) === sourceTenantId
  );
  return shared?.org_name || shared?.shared_by_username || '';
});

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
  },
  embeddedMode: {
    type: Boolean,
    default: false
  }
});

const isAgentEnabled = computed(() => settingsStore.isAgentEnabled);
const isWebSearchEnabled = computed(() => settingsStore.isWebSearchEnabled);
const selectedKbIds = computed(() => settingsStore.settings.selectedKnowledgeBases || []);
const selectedFileIds = computed(() => settingsStore.settings.selectedFiles || []);
const selectedTags = computed(() => settingsStore.settings.selectedTags || []);
const selectedMCPServiceIds = computed(() => settingsStore.settings.selectedMCPServices || []);
const selectedSkillNames = computed(() => settingsStore.settings.selectedSkills || []);

// 已就绪的知识库（来自租户级缓存）
const knowledgeBases = computed(() => chatResources.validKnowledgeBases);
const fileList = ref<Array<{ id: string; name: string }>>([]);

// 选中的知识库：包含自己的 + 组织共享的 + 共享智能体下的（用于展示已选列表与 org 角标）
const selectedKbs = computed(() => {
  const own = knowledgeBases.value.filter(kb => selectedKbIds.value.includes(kb.id));
  const sharedList = orgStore.sharedKnowledgeBases || [];
  const sharedMapped = sharedList
    .filter((s: any) => s.knowledge_base != null && selectedKbIds.value.includes(s.knowledge_base.id))
    .map((s: any) => ({
      id: s.knowledge_base.id,
      name: s.knowledge_base.name,
      type: s.knowledge_base.type || 'document',
      knowledge_count: s.knowledge_base.knowledge_count,
      chunk_count: s.knowledge_base.chunk_count,
      org_name: s.org_name || ''
    }));
  const ownIds = new Set(own.map(kb => kb.id));
  const sharedOnly = sharedMapped.filter((kb: any) => !ownIds.has(kb.id));
  const sharedOnlyIds = new Set(sharedOnly.map((kb: any) => kb.id));
  // 共享智能体下的知识库：从 sharedAgentKbList 中取在选中列表里的，并打上共享空间标识
  const agentOrg = sharedAgentOrgName.value;
  const sharedFromAgent = (sharedAgentKbList.value || []).filter(kb => selectedKbIds.value.includes(kb.id) && !ownIds.has(kb.id) && !sharedOnlyIds.has(kb.id)).map(kb => ({
    id: kb.id,
    name: kb.name,
    type: kb.type || 'document',
    knowledge_count: kb.knowledge_count,
    chunk_count: kb.chunk_count,
    org_name: agentOrg || ''
  }));
  return [...own, ...sharedOnly, ...sharedFromAgent];
});

const selectedFiles = computed(() => {
  // If we have file details in fileList, use them.
  // Otherwise we might show ID or Loading...
  return selectedFileIds.value.map((id: string) => {
    const found = fileList.value.find(f => f.id === id);
    return found || { id, name: 'Loading...' };
  });
});

const skillMentionItems = computed<MentionItem[]>(() => {
  return selectedSkillNames.value
    .filter((name: string) => isSkillAllowedByAgent(name))
    .map((name: string) => {
    const skill = editorResources.skills.find(s => s.name === name);
    return {
      id: name,
      name: skill?.name || name,
      type: 'skill' as const,
      skillName: name,
      description: skill?.description || '',
    };
  });
});

const selectedMCPItems = computed<MentionItem[]>(() => {
  return selectedMCPServiceIds.value
    .map((id: string) => mcpServices.value.find(service => service.id === id))
    .filter((svc): svc is MCPService => !!svc && isMCPAllowedByAgent(svc))
    .map((svc) => {
      return {
        id: svc.id,
        name: svc.name,
        type: 'mcp' as const,
        description: svc.description || '',
      };
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

  // 用户选择的文件（根据 fileIdToKbId + 共享列表/共享智能体补全 org_name，用于角标）
  const sharedKbOrgMap: Record<string, string> = {};
  (orgStore.sharedKnowledgeBases || []).forEach((s: any) => {
    if (s.knowledge_base?.id != null && s.org_name) {
      sharedKbOrgMap[String(s.knowledge_base.id)] = s.org_name;
    }
  });
  if (sharedAgentOrgName.value) {
    (sharedAgentKbList.value || []).forEach((kb) => {
      sharedKbOrgMap[String(kb.id)] = sharedAgentOrgName.value;
    });
  }
  const files = selectedFiles.value.map((f: { id: string; name: string }) => {
    const kbId = fileIdToKbId.value[f.id];
    const org_name = kbId ? sharedKbOrgMap[String(kbId)] || '' : '';
    return {
      ...f,
      type: 'file' as const,
      isAgentConfigured: false,
      org_name
    };
  });

  // 智能体配置的放在前面
  const agentConfiguredKbs = allKbs.filter(kb => kb.isAgentConfigured);
  const userSelectedKbs = allKbs.filter(kb => !kb.isAgentConfigured);
  const tags = selectedTags.value.map((tag: any) => ({
    id: tag.id,
    name: tag.name,
    type: 'tag' as const,
    kbId: tag.kbId,
    kbName: tag.kbName,
    description: tag.kbName || '',
    isAgentConfigured: false,
  }));

  return [...agentConfiguredKbs, ...userSelectedKbs, ...files, ...tags, ...selectedMCPItems.value, ...skillMentionItems.value];
});

// 移除选中项（智能体配置的项也可以移除）
const removeSelectedItem = (item: MentionItem) => {
  if (item.type === 'kb') {
    settingsStore.removeKnowledgeBase(item.id);
  } else if (item.type === 'file') {
    settingsStore.removeFile(item.id);
  } else if (item.type === 'tag') {
    settingsStore.removeTag(item.id, item.kbId);
  } else if (item.type === 'mcp') {
    settingsStore.removeMCPService(item.id);
  } else if (item.type === 'skill') {
    settingsStore.removeSkill(item.skillName || item.id);
  }
};

const getMentionIcon = (item: MentionItem) => {
  switch (item.type) {
    case 'file': return 'file';
    case 'tag': return 'tag';
    case 'mcp': return 'tools';
    case 'skill': return 'bookmark';
    default: return 'folder';
  }
};

const getMentionChipClass = (item: MentionItem) => {
  if (item.type === 'kb') return item.kbType === 'faq' ? 'mention-chip--faq' : 'mention-chip--kb';
  return `mention-chip--${item.type}`;
};

// 使用 computed 从 store 读取，并通过 setter 同步回 store
const selectedModelId = computed({
  get: () => settingsStore.conversationModels.selectedChatModelId || '',
  set: (val: string) => settingsStore.updateConversationModels({ selectedChatModelId: val })
});
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

// 加载知识库列表（自己的 + 共享的，用于 @ 提及等）
const loadKnowledgeBases = async (force = false) => {
  try {
    await chatResources.ensureKnowledgeBases(force);
    const validKbs = knowledgeBases.value;

    const validKbIds = new Set(validKbs.map((kb: any) => kb.id));
    const sharedKbIds = new Set(
      (orgStore.sharedKnowledgeBases || []).map((s: any) => s.knowledge_base?.id).filter(Boolean)
    );
    let sharedAgentKbIdSet = new Set<string>();
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    const agentId = settingsStore.selectedAgentId;
    if (sourceTenantId && agentId) {
      try {
        const list = await chatResources.ensureAgentKnowledgeBases(agentId, force);
        list.forEach((kb: any) => kb?.id && sharedAgentKbIdSet.add(kb.id));
      } catch {
        sharedAgentKbIdSet = new Set();
      }
    }
    const currentSelectedIds = settingsStore.settings.selectedKnowledgeBases || [];
    const validSelectedIds = currentSelectedIds.filter(
      (id: string) => validKbIds.has(id) || sharedKbIds.has(id) || sharedAgentKbIdSet.has(id)
    );

    if (validSelectedIds.length !== currentSelectedIds.length) {
      settingsStore.selectKnowledgeBases(validSelectedIds);
    }
  } catch (error) {
    console.error('Failed to load knowledge bases:', error);
  }
};

const loadFiles = async () => {
  const ids = selectedFileIds.value;
  if (ids.length === 0) return;

  const missingIds = ids.filter((id: string) => !fileList.value.find(f => f.id === id));
  if (missingIds.length === 0) return;

  try {
    // 按 kb_id 分组：共享知识库下的文档需带 kb_id 才能正确查询
    const byKbId = new Map<string, string[]>();
    const noKbId: string[] = [];
    missingIds.forEach((id: string) => {
      const kbId = fileIdToKbId.value[id];
      if (kbId) {
        if (!byKbId.has(kbId)) byKbId.set(kbId, []);
        byKbId.get(kbId)!.push(id);
      } else {
        noKbId.push(id);
      }
    });

    const allNewFiles: Array<{ id: string; name: string }> = [];
    const agentIdForBatch = settingsStore.selectedAgentSourceTenantId ? settingsStore.selectedAgentId : undefined;
    const runBatch = async (batchIds: string[], kbId?: string, agentId?: string) => {
      const query = new URLSearchParams();
      batchIds.forEach((id: string) => query.append('ids', id));
      const res: any = await batchQueryKnowledge(query.toString(), kbId, agentId);
      if (res.data && Array.isArray(res.data)) {
        res.data.forEach((f: any) => allNewFiles.push({ id: f.id, name: f.title || f.file_name }));
      }
    };

    for (const [kbId, batchIds] of byKbId) {
      await runBatch(batchIds, kbId);
    }
    if (noKbId.length > 0) {
      await runBatch(noKbId, undefined, agentIdForBatch);
    }
    if (allNewFiles.length > 0) {
      fileList.value = [...fileList.value, ...allNewFiles];
    }
  } catch (e) {
    console.error("Failed to load files", e);
  }
};

const loadMCPServices = async () => {
  try {
    mcpServices.value = await listMCPServices();
  } catch (error) {
    console.error('Failed to load MCP services:', error);
    mcpServices.value = [];
  }
};

watch(selectedFileIds, () => {
  loadFiles();
}, { immediate: true });

const isWebSearchConfigured = computed(() => {
  const agentProviderId = agentWebSearchProviderId.value;
  if (agentProviderId) {
    return webSearchProviders.value.some(p => p.id === agentProviderId);
  }

  return webSearchProviders.value.some(p => p.is_default);
});

const loadWebSearchConfig = async (force = false) => {
  try {
    await chatResources.ensureWebSearchProviders(force);

    if (!isWebSearchConfigured.value && settingsStore.isWebSearchEnabled) {
      settingsStore.toggleWebSearch(false);
    }
  } catch (error) {
    console.error('Failed to load web search config:', error);
    chatResources.invalidate('webSearchProviders');
    if (settingsStore.isWebSearchEnabled) {
      settingsStore.toggleWebSearch(false);
    }
  }
};

// 加载智能体列表（我的 + 共享，供选中态与就绪检查用）
const loadAgents = async (force = false) => {
  try {
    await chatResources.ensureAgents(force);
    ensureSelectedAgentNotDisabled();
  } catch (error) {
    console.error('Failed to load agents:', error);
  }
};

// 默认选中的 builtin（builtin-quick-answer）也可能被当前租户管理员停用。
// 列表加载完后做一次纠偏：若当前选中的是本租户停用的 agent（仅限「我的/builtin」，
// 共享智能体由源租户决定，本地停用列表不适用），按 智能推理 → 快速问答 →
// 第一个可用 的顺序兜底切换。全部都被停用时保持原选择不动（极端场景，UI 仍会
// 在 enabledAgents 过滤后显示空，由用户在智能体页恢复任意一个）。
const ensureSelectedAgentNotDisabled = () => {
  if (settingsStore.selectedAgentSourceTenantId) return
  const currentId = settingsStore.selectedAgentId || BUILTIN_QUICK_ANSWER_ID
  if (!disabledOwnAgentIds.value.includes(currentId)) return

  const isEnabled = (id: string) =>
    agents.value.some(a => a.id === id) && !disabledOwnAgentIds.value.includes(id)

  let fallback: CustomAgent | undefined
  if (isEnabled(BUILTIN_SMART_REASONING_ID)) {
    fallback = agents.value.find(a => a.id === BUILTIN_SMART_REASONING_ID)
  } else if (isEnabled(BUILTIN_QUICK_ANSWER_ID)) {
    fallback = agents.value.find(a => a.id === BUILTIN_QUICK_ANSWER_ID)
  } else {
    fallback = agents.value.find(a => !disabledOwnAgentIds.value.includes(a.id))
  }
  if (!fallback) return

  settingsStore.selectAgent(fallback.id)
  // selectAgent 内部仅对两个 builtin 常量自动切 isAgentEnabled；自定义 agent 兜底时
  // 需要按其 agent_mode 显式同步一次，保证模式徽标与对话行为一致。
  if (fallback.id !== BUILTIN_QUICK_ANSWER_ID && fallback.id !== BUILTIN_SMART_REASONING_ID) {
    settingsStore.toggleAgent(fallback.config?.agent_mode === 'smart-reasoning')
  }
}

// 对话下拉中展示的「我的」智能体（排除当前租户已停用的）
const enabledAgents = computed(() =>
  agents.value.filter(a => !disabledOwnAgentIds.value.includes(a.id))
);

// LAST_CHAT_MODEL_KEY scopes the per-user "last selected chat model"
// to localStorage. The previous implementation wrote this back to the
// tenant-level KV /tenants/kv/conversation-config — which (a) required
// Admin+ to mutate, so a Viewer/Contributor switching models in the
// chat input got a 403, and (b) silently overwrote the tenant default
// for everyone else. localStorage is per-user-per-browser, which is
// what "remember my last pick" actually wants.
const LAST_CHAT_MODEL_KEY = 'weknora_last_chat_model_id'

const readLastChatModelID = (): string => {
  try {
    return localStorage.getItem(LAST_CHAT_MODEL_KEY) || ''
  } catch {
    return ''
  }
}

const writeLastChatModelID = (id: string) => {
  try {
    if (id) {
      localStorage.setItem(LAST_CHAT_MODEL_KEY, id)
    } else {
      localStorage.removeItem(LAST_CHAT_MODEL_KEY)
    }
  } catch {
    // localStorage may be disabled in incognito mode; ignore.
  }
}

// Initial chat-model selection priority: per-user last pick
// (localStorage) > current store value (e.g. carried over from
// settings page) > first available model. The tenant-level
// conversation-config used to feed summary_model_id/rerank_model_id
// into the dropdown, but those fields were removed: per-user last pick
// belongs in localStorage, agent-level model belongs on the agent.
const initChatModelSelection = () => {
  const lastPick = readLastChatModelID();
  const currentSelectedModel = settingsStore.conversationModels.selectedChatModelId;
  const initialSelection = lastPick || currentSelectedModel || '';
  settingsStore.updateConversationModels({
    summaryModelId: initialSelection,
    selectedChatModelId: initialSelection,
    rerankModelId: '',
  });
  if (!selectedModelId.value) {
    selectedModelId.value = initialSelection;
  }
  ensureModelSelection();
};

const loadChatModels = async (force = false) => {
  if (modelsLoading.value) return;
  modelsLoading.value = true;
  try {
    await chatResources.ensureChatModels(force);
    ensureModelSelection();
  } catch (error) {
    console.error('Failed to load chat models:', error);
    chatResources.invalidate('models');
  } finally {
    modelsLoading.value = false;
  }
};

const ensureModelSelection = () => {
  if (selectedModelId.value) {
    return;
  }
  const lastPick = readLastChatModelID();
  if (lastPick) {
    selectedModelId.value = lastPick;
    return;
  }
  if (availableModels.value.length > 0) {
    selectedModelId.value = availableModels.value[0].id || '';
  }
};

// 智能体身份或其数据到位时，把对话模型同步到智能体配置的 model_id。
// 修复场景：导航离开再返回时，initChatModelSelection 会用 localStorage 的 lastPick
// 覆盖共享智能体绑定的源租户 model_id，UI 显示「未配置」——此时需要拉回 agent 模型。
// 但若用户在本页手动改过模型（lastPick 与 agent 默认不同且当前选中即为 lastPick），
// 则保留用户选择，避免 creatChat → chat 跳转后把模型 B 冲回智能体默认 A。
watch(
  [selectedAgentId, () => settingsStore.selectedAgentSourceTenantId, agentModelId],
  ([, sourceTenantId, newModelId]) => {
    if (!newModelId || newModelId.trim() === '') return;

    const lastPick = readLastChatModelID();
    const isSharedAgent = !!sourceTenantId;
    const agentModelInList = availableModels.value.some(m => m.id === newModelId);

    if (
      lastPick &&
      selectedModelId.value === lastPick &&
      lastPick !== newModelId &&
      (!isSharedAgent || agentModelInList)
    ) {
      return;
    }

    if (newModelId !== selectedModelId.value) {
      selectedModelId.value = newModelId;
    }
  },
  { immediate: true }
);

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

const handleModelChange = (value: string | number | Array<string | number> | undefined) => {
  const normalized = Array.isArray(value) ? value[0] : value;
  const val = normalized !== undefined && normalized !== null ? String(normalized) : '';

  if (!val) {
    selectedModelId.value = '';
    return;
  }
  if (val === '__add_model__') {
    selectedModelId.value = readLastChatModelID();
    handleGoToConversationModels();
    return;
  }

  // The chat-level model picker now persists per-user-per-browser via
  // localStorage instead of writing to the tenant-shared KV. This is what
  // "remember my last pick" should always have meant — the previous PUT
  // /tenants/kv/conversation-config required Admin+, so a Viewer or
  // Contributor switching models from the chat input got a 403.
  writeLastChatModelID(val);
  selectedModelId.value = val;
  showModelSelector.value = false;

  settingsStore.updateConversationModels({
    summaryModelId: val,
    selectedChatModelId: val,
    rerankModelId: '',
  });
};

const selectedModel = computed(() => {
  return availableModels.value.find(model => model.id === selectedModelId.value);
});

// 模型展示名：本租户列表中有则用名称；若为共享智能体且其 model_id 不在本租户列表中则显示“共享智能体配置的模型”
const selectedModelDisplayName = computed(() => {
  if (selectedModel.value) return modelDisplayName(selectedModel.value);
  if (!selectedModelId.value) return t('input.notConfigured');
  const isSharedAgent = !!settingsStore.selectedAgentSourceTenantId;
  const modelFromAgent = agentModelId.value && agentModelId.value === selectedModelId.value;
  if (isSharedAgent && modelFromAgent) return t('input.sharedAgentModelLabel');
  return t('input.notConfigured');
});

const modelDisplayName = (model: ModelConfig) => {
  const displayName = model.display_name?.trim();
  return displayName || model.name;
};

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

  // Normalize coordinates to CSS pixels so they are interpreted the same way
  // the browser will render them under the root `zoom` (see utils/zoom.ts).
  const zoom = getRootZoom();
  const rect = rectToCssPx(anchor.getBoundingClientRect(), zoom);
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
  const { width: vw, height: vh } = cssViewportSize(zoom);

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

  // 根据智能体的 kb_selection_mode 过滤知识库；选中共享智能体时使用该租户下的知识库，否则使用本租户 + 共享给自己的
  let kbItems: any[] = [];
  let tagItems: MentionItem[] = [];
  let mcpItems: MentionItem[] = [];
  let skillItems: MentionItem[] = [];
  if (!append) {
    let availableKbs: any[];
    const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
    const agentId = selectedAgentId.value;
    if (sourceTenantId && agentId) {
      // 共享智能体：按 agent_id 拉取该智能体配置的知识库范围（后端从共享关系解析租户）
      try {
        const list = await chatResources.ensureAgentKnowledgeBases(agentId);
        const orgLabel = sharedAgentOrgName.value || '';
        // 保留 capabilities / indexing_strategy，后面过滤时要用
        availableKbs = list.map((kb: any) => ({
          id: kb.id,
          name: kb.name,
          type: kb.type || 'document',
          knowledge_count: kb.knowledge_count,
          chunk_count: kb.chunk_count,
          org_name: orgLabel,
          capabilities: kb.capabilities,
          indexing_strategy: kb.indexing_strategy,
        }));
        sharedAgentKbList.value = list.map((kb: any) => ({
          id: kb.id,
          name: kb.name,
          type: kb.type || 'document',
          knowledge_count: kb.knowledge_count,
          chunk_count: kb.chunk_count
        }));
      } catch (e) {
        console.error('[Mention] listKnowledgeBases(agent_id) error:', e);
        availableKbs = [];
        sharedAgentKbList.value = [];
      }
    } else {
      sharedAgentKbList.value = [];
      availableKbs = [...knowledgeBases.value];
      const sharedList = orgStore.sharedKnowledgeBases || [];
      const sharedKbsForMention = sharedList
        .filter((s: any) => s.knowledge_base != null)
        .map((s: any) => ({
          id: s.knowledge_base.id,
          name: s.knowledge_base.name,
          type: s.knowledge_base.type || 'document',
          knowledge_count: s.knowledge_base.knowledge_count,
          chunk_count: s.knowledge_base.chunk_count,
          org_name: s.org_name || '',
          capabilities: s.knowledge_base.capabilities,
          indexing_strategy: s.knowledge_base.indexing_strategy,
        }));
      const ownIds = new Set(availableKbs.map((kb: any) => kb.id));
      sharedKbsForMention.forEach((kb: any) => {
        if (!ownIds.has(kb.id)) {
          availableKbs.push(kb);
          ownIds.add(kb.id);
        }
      });
    }

    if (hasAgentConfig.value) {
      const kbMode = agentKBSelectionMode.value;
      // 共享智能体路径：`availableKbs` 已经来自 `listKnowledgeBases({agent_id})`,
      // 后端按 kb_selection_mode + allowed_tools 做过权威过滤；前端不再重复一遍。
      // 本人智能体路径：走 own KBs + user-shared KBs 合并，后端拿不到 agent 上下文，
      // 所以 'selected' 要收敛到配置集合，'all' 要按工具派生的能力过滤。
      const isSharedAgent = !!(sourceTenantId && agentId);
      if (kbMode === 'none') {
        availableKbs = [];
      } else if (!isSharedAgent) {
        if (kbMode === 'selected') {
          // 'selected' 完全信任用户在编辑器里的勾选；编辑器已经用 kb_filter 灰显
          // 不兼容项，这里不再二次过滤，避免越权擦除用户明确的选择。
          const configuredKbIds = agentKnowledgeBases.value;
          availableKbs = availableKbs.filter((kb: any) => configuredKbIds.includes(kb.id));
        } else if (kbMode === 'all') {
          // 'all' 的语义是"全部兼容的 KB"——按工具派生的能力集合过滤，
          // 避免 wiki-qa 选"全部"后 @ 出来一堆 wiki 工具跑不动的 KB。
          availableKbs = availableKbs.filter((kb: any) => isKbCompatibleWithAgent(kb));
        }
      }
    }

    // 非智能体场景不限制文件过滤；智能体场景按当前 availableKbs 的 ID 集合过滤文件
    mentionAllowedKbIds.value = hasAgentConfig.value
      ? new Set(availableKbs.map((kb: any) => String(kb.id)))
      : null;

    const kbs = availableKbs.filter((kb: any) =>
      !q || (kb.name && kb.name.toLowerCase().includes(q.toLowerCase()))
    );
    kbItems = await Promise.all(kbs.map(async (kb: any) => {
      const kbType = kb.type || 'document';
      let count = kbType === 'faq' ? Number(kb.chunk_count || 0) : Number(kb.knowledge_count || 0);
      if (!count) {
        const detail = await chatResources.fetchKnowledgeBaseById(kb.id);
        if (detail) {
          count = detail.type === 'faq'
            ? Number(detail.chunk_count || 0)
            : Number(detail.knowledge_count || 0);
        }
      }
      return {
        id: kb.id,
        name: kb.name,
        type: 'kb' as const,
        kbType: kbType === 'faq' ? 'faq' as const : 'document' as const,
        count,
        orgName: kb.org_name || sharedAgentOrgName.value || undefined
      };
    }));
    mentionGroupCounts.value.kb = kbItems.length;

    const tagKeyword = q.trim();
    const tagSources = availableKbs;
    try {
      const tagResults = await Promise.all(tagSources.map(async (kb: any) => {
        const res: any = await listKnowledgeTags(kb.id, { page: 1, page_size: 20, keyword: tagKeyword || undefined });
        const payload = res?.data ?? res;
        const list = Array.isArray(payload?.data) ? payload.data : (Array.isArray(payload) ? payload : []);
        return list.map((tag: any) => ({
          id: tag.id,
          name: tag.name,
          type: 'tag' as const,
          kbId: kb.id,
          kbName: kb.name,
        }));
      }));
      tagItems = tagResults.flat();
      mentionGroupCounts.value.tag = tagItems.length;
    } catch (e) {
      console.error('[Mention] listKnowledgeTags error:', e);
      tagItems = [];
    }

    const mcpMode = agentMCPSelectionMode.value;
    if (mcpMode !== 'none') {
      mcpItems = mcpServices.value
        .filter(service => isMCPAllowedByAgent(service))
        .filter(service => !q || service.name?.toLowerCase().includes(q.toLowerCase()) || (service.description || '').toLowerCase().includes(q.toLowerCase()))
        .map(service => ({
          id: service.id,
          name: service.name,
          type: 'mcp' as const,
          description: service.description || '',
        }));
    }

    const skillsMode = agentSkillsSelectionMode.value;
    if (skillsMode !== 'none') {
      await editorResources.ensureSkills();
      skillItems = editorResources.skills
        .filter(skill => isSkillAllowedByAgent(skill.name))
        .map(skill => ({
          id: skill.name,
          name: skill.name,
          type: 'skill' as const,
          skillName: skill.name,
          description: skill.description || '',
        }))
        .filter(skill => {
          if (!q) return true;
          const keyword = q.toLowerCase();
          return skill.name.toLowerCase().includes(keyword)
            || (skill.description || '').toLowerCase().includes(keyword);
        });
    }
  }

  // Fetch Files from API
  // 仅当满足以下两点才加载文件：
  //   1. 智能体确实会用到知识库（kb_selection_mode !== 'none'）；
  //   2. 智能体启用的工具里至少有一个能消费 @ 的文件 ID
  //      （比如 wiki-qa 全是 wiki_* 工具，用户 @ 的文件根本进不到任何工具里，就没必要展示）。
  let fileItems: any[] = [];
  const kbModeAllowsFiles = !hasAgentConfig.value || agentKBSelectionMode.value !== 'none';
  const toolsAllowFiles = !hasAgentConfig.value || toolsConsumeFiles(agentAllowedTools.value);
  const shouldLoadFiles = kbModeAllowsFiles && toolsAllowFiles;

  // 空关键词时显式请求最近文件；有关键词时返回匹配文件。
  // `recent=true` 只用于浏览态，避免其他搜索调用漏传关键词时静默退化为最近列表。
  const fileSearchKeyword = q.trim();
  if (shouldLoadFiles) {
    mentionLoading.value = true;
    try {
      const fileTypesParam = agentSupportedFileTypes.value.length > 0 ? agentSupportedFileTypes.value : undefined;
      const sourceTenantId = settingsStore.selectedAgentSourceTenantId;
      const agentId = selectedAgentId.value;
      const searchOptions = {
        ...(sourceTenantId && agentId ? { agent_id: agentId } : {}),
        recent: !fileSearchKeyword,
      };
      const res: any = await searchKnowledge(
        fileSearchKeyword,
        mentionOffset.value,
        MENTION_PAGE_SIZE,
        fileTypesParam,
        searchOptions
      );
      console.log('[Mention] searchKnowledge response:', res);
      if (res.data && Array.isArray(res.data)) {
        let files = res.data;
        const rawTotal = typeof res.total === 'number' ? res.total : undefined;
        const apiPageSize = res.data.length;
        // 按当前 @ 会话的兼容 KB 集合过滤：
        //   - 非智能体场景：`mentionAllowedKbIds` 为 null，跳过；
        //   - 智能体场景（含 shared agent）：'selected' 会把 ID 收敛到用户勾的 KB，
        //     'all' 会收敛到"兼容"的 KB，'none' 根本走不到这里（shouldLoadFiles=false）。
        //   这样分页 append 也能用同一份集合，不再只兜住 'selected' + 非共享的分支。
        if (mentionAllowedKbIds.value) {
          const allowed = mentionAllowedKbIds.value;
          files = files.filter((f: any) => {
            const kbId = f.knowledge_base_id ?? f.kb_id;
            return kbId != null && allowed.has(String(kbId));
          });
        }
        const sharedKbOrgMap: Record<string, string> = {};
        (orgStore.sharedKnowledgeBases || []).forEach((s: any) => {
          if (s.knowledge_base?.id != null && s.org_name) {
            sharedKbOrgMap[String(s.knowledge_base.id)] = s.org_name;
          }
        });
        const agentOrgLabel = sourceTenantId && agentId ? sharedAgentOrgName.value : '';
        fileItems = files.map((f: any) => {
          const kbId = f.knowledge_base_id ?? f.kb_id;
          const kbIdStr = kbId != null ? String(kbId) : '';
          const fileOrgName = agentOrgLabel || (kbIdStr ? sharedKbOrgMap[kbIdStr] : undefined);
          return {
            id: f.id,
            name: f.title || f.file_name,
            type: 'file' as const,
            kbName: f.knowledge_base_name || '',
            kbId: kbId || undefined,
            orgName: fileOrgName || undefined
          };
        });
        if (!append) {
          const clientFiltered = !!mentionAllowedKbIds.value && fileItems.length < apiPageSize;
          if (!clientFiltered && rawTotal != null) {
            mentionGroupCounts.value.file = rawTotal;
          } else {
            delete mentionGroupCounts.value.file;
          }
        }
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
    mentionItems.value = [...kbItems, ...tagItems, ...mcpItems, ...skillItems, ...fileItems];
  }
  console.log('[Mention] Total items:', mentionItems.value.length, { kbItems: kbItems.length, fileItems: fileItems.length, tagItems: tagItems.length, mcpItems: mcpItems.length, skillItems: skillItems.length });

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
      if (isMentionDisabled.value) {
        return;
      }

      console.log('[Mention] @ detected, opening menu');
      isMentionTriggeredByButton.value = false;
      mentionStartPos.value = cursor - 1;
      showMention.value = true;
      mentionQuery.value = "";

      const coords = getCaretCoordinates(textarea, cursor);
      // Normalize coordinates to CSS pixels (root <html> may carry `zoom`).
      const zoom = getRootZoom();
      const rect = rectToCssPx(textarea.getBoundingClientRect(), zoom);
      const { width: vw, height: vh } = cssViewportSize(zoom);
      const scrollTop = textarea.scrollTop;
      const menuHeight = 320; // 预估最大高度

      let left = rect.left + coords.left;
      // Prevent menu from going off-screen horizontally
      if (left + 300 > vw) {
        left = vw - 300 - 10;
      }

      // 光标相对于视口的实际 top 位置（CSS 像素）
      const cursorAbsoluteTop = rect.top + coords.top - scrollTop;
      const lineHeight = coords.height; // 光标高度

      // Check vertical space below cursor
      const spaceBelow = vh - (cursorAbsoluteTop + lineHeight);

      if (spaceBelow < menuHeight && cursorAbsoluteTop > menuHeight) {
        // Show above cursor (using bottom positioning)
        const bottom = vh - cursorAbsoluteTop;
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
  // 如果当前没有任何可提及资源，不允许打开选择器
  if (isMentionDisabled.value) {
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

  // Normalize coordinates to CSS pixels (root <html> may carry `zoom`).
  const zoom = getRootZoom();
  const rect = rectToCssPx(textarea.getBoundingClientRect(), zoom);
  const { height: vh } = cssViewportSize(zoom);
  const menuHeight = 320;

  // 判断输入框上方空间
  const spaceAbove = rect.top;
  const spaceBelow = vh - rect.bottom;

  // 优先显示在上方，除非上方空间不足且下方空间充足
  if (spaceAbove > menuHeight || spaceAbove > spaceBelow) {
    // Show above textarea
    mentionStyle.value = {
      left: `${rect.left}px`,
      bottom: `${vh - rect.top + 8}px`, // 8px padding
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
    if (item.kbId) {
      fileIdToKbId.value[item.id] = item.kbId;
      settingsStore.setFileKbMap({ [item.id]: item.kbId });
    }
    // Add to local cache immediately
    if (!fileList.value.find(f => f.id === item.id)) {
      fileList.value.push({ id: item.id, name: item.name });
    }
  } else if (item.type === 'tag') {
    if (item.kbId) {
      settingsStore.addTag({ id: item.id, name: item.name, kbId: item.kbId, kbName: item.kbName });
    }
  } else if (item.type === 'mcp') {
    settingsStore.addMCPService(item.id);
  } else if (item.type === 'skill') {
    settingsStore.addSkill(item.skillName || item.id);
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
  delete fileIdToKbId.value[id];
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
  // Embed 渠道由宿主注入 agent/KB，勿拉取需 JWT 的平台资源
  if (props.embeddedMode) return;

  // 并行拉取；若 platform 已预取且缓存未过期则直接复用
  initChatModelSelection();
  void Promise.all([
    loadKnowledgeBases(),
    loadWebSearchConfig(),
    loadChatModels(),
    loadAgents(),
    loadMCPServices(),
  ]);
  window.addEventListener(CHAT_FILE_DROP_EVENT, handleChatFileDrop as EventListener);

  // 从持久化恢复 fileId -> kbId，刷新后共享知识库文件可带 kb_id 拉取（仅保留当前仍选中的文件）
  const persisted = settingsStore.settings.selectedFileKbMap;
  const ids = settingsStore.settings.selectedFiles || [];
  if (persisted && typeof persisted === 'object' && ids.length > 0) {
    const next: Record<string, string> = {};
    ids.forEach((id: string) => {
      if (persisted[id]) next[id] = persisted[id];
    });
    fileIdToKbId.value = next;
  }

  // 如果从知识库内部进入，自动选中该知识库
  const kbId = (route.params as any)?.kbId as string;
  if (kbId && !selectedKbIds.value.includes(kbId)) {
    settingsStore.addKnowledgeBase(kbId);
  }

  const prefill = menuStore.consumePrefillQuery();
  if (prefill) {
    query.value = prefill;
    nextTick(() => {
      const textarea = getTextareaEl();
      if (textarea) textarea.focus();
    });
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
  window.removeEventListener(CHAT_FILE_DROP_EVENT, handleChatFileDrop as EventListener);
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
    loadWebSearchConfig(true);
    loadChatModels(true);
  }
});

watch([selectedKbIds, selectedFileIds], ([kbIds, fileIds]) => {
  if (!kbIds.length && !fileIds.length) {
    closeModelSelector();
  }
}, { deep: true });

const emit = defineEmits<{
  (e: 'send-msg', query: string, modelId: string, mentionedItems: MentionRequestItem[], imageFiles: File[], attachmentFiles: AttachmentFile[]): void;
  (e: 'stop-generation'): void;
}>();

const createSession = async (val: string) => {
  if (!val.trim()) {
    MessagePlugin.info(t('input.messages.enterContent'));
    return;
  }
  if (props.isReplying) {
    return MessagePlugin.error(t('input.messages.replying'));
  }

  // Embed 渠道由后端绑定 agent/KB，勿走平台侧 agent 列表与就绪校验
  if (props.embeddedMode) {
    const textarea = getTextareaEl();
    if (textarea) textarea.blur();
    emit('send-msg', val, selectedModelId.value || '', [], [], []);
    clearvalue();
    return;
  }

  if (!chatResources.isFresh('models')) {
    await loadChatModels()
  }

  // 发送前校验当前选中的智能体（含默认快速问答）是否已配置完成
  const agentToCheck = selectedAgent.value;
  let actualAgent = agentToCheck;
  if (agentToCheck.is_builtin && !settingsStore.selectedAgentSourceTenantId) {
    let builtin = agents.value.find(a => a.id === selectedAgentId.value);
    if (!builtin) {
      await loadAgents();
      builtin = agents.value.find(a => a.id === selectedAgentId.value);
    }
    actualAgent = builtin || agentToCheck;
  }
  const isAgentMode = actualAgent.config?.agent_mode === 'smart-reasoning';
  const { keys: notReadyKeys, labels: notReadyReasons } = collectAgentNotReadyReasons(
    actualAgent,
    isAgentMode,
    settingsStore.selectedAgentSourceTenantId ?? undefined,
  );
  if (notReadyReasons.length > 0) {
    showAgentNotReadyMessage(
      actualAgent,
      notReadyReasons,
      notReadyKeys,
      settingsStore.selectedAgentSourceTenantId ?? undefined,
    );
    return;
  }
  // 获取@提及的知识库和文件信息
  const mentionedItems: MentionRequestItem[] = allSelectedItems.value.map(item => ({
    id: item.id,
    name: item.name,
    type: item.type,
    kb_type: item.type === 'kb' ? (item.kbType || 'document') : undefined,
    kb_id: item.kbId,
    kb_name: item.kbName,
    service_id: item.serviceId,
    skill_name: item.skillName,
  }));
  const imageFiles = uploadedImages.value.map(img => img.file);
  const attachmentFiles = uploadedAttachments.value;

  // Blur the textarea BEFORE emitting, so that when the parent navigates away
  // and Vue unmounts this component, TDesign's blur handler won't fire on a
  // detached DOM element (which causes getComputedStyle to throw).
  const textarea = getTextareaEl();
  if (textarea) textarea.blur();
  emit('send-msg', val, selectedModelId.value, mentionedItems, imageFiles, attachmentFiles);

  // Clean up image previews
  uploadedImages.value.forEach(img => URL.revokeObjectURL(img.preview));
  uploadedImages.value = [];

  // Clean up attachments
  attachmentUploadRef.value?.clear();
  uploadedAttachments.value = [];

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

  // Normalize coordinates to CSS pixels (root <html> may carry `zoom`).
  const zoom = getRootZoom();
  const rect = rectToCssPx(anchor.getBoundingClientRect(), zoom);
  const dropdownWidth = 200;
  const offsetY = 8;
  const { width: vw, height: vh } = cssViewportSize(zoom);

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
    if (!chatResources.isFresh('agents')) {
      void loadAgents(true);
    }
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

const selectAgentMode = async (mode: 'quick-answer' | 'smart-reasoning') => {
  if (!chatResources.isFresh('models')) {
    await loadChatModels()
  }

  const builtinAgentId = mode === 'smart-reasoning' ? BUILTIN_SMART_REASONING_ID : BUILTIN_QUICK_ANSWER_ID;
  const builtinAgent = agents.value.find(a => a.id === builtinAgentId);

  if (builtinAgent) {
    const { keys: notReadyKeys, labels: notReadyReasons } = collectAgentNotReadyReasons(
      builtinAgent,
      mode === 'smart-reasoning',
    );
    if (notReadyReasons.length > 0) {
      showAgentModeSelector.value = false;
      showAgentNotReadyMessage(builtinAgent, notReadyReasons, notReadyKeys);
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

// 选择智能体（新版）；sourceTenantId 为共享智能体时传入
const handleAgentNotReady = (
  agent: CustomAgent,
  labels: string[],
  keys: AgentNotReadyReasonKey[],
  sourceTenantId?: string,
) => {
  showAgentNotReadyMessage(agent, labels, keys, sourceTenantId);
};

const handleSelectAgent = async (agent: CustomAgent, sourceTenantId?: string) => {
  if (!chatResources.isFresh('models')) {
    await loadChatModels()
  }

  // 根据智能体的 agent_mode 判断是否为 Agent 模式
  const isAgentType = agent.config?.agent_mode === 'smart-reasoning';

  // 统一检查智能体是否就绪（内置和自定义智能体使用相同逻辑）
  const actualAgent = agent.is_builtin && !sourceTenantId
    ? (agents.value.find(a => a.id === agent.id) || agent)
    : agent;

  const { keys: notReadyKeys, labels: notReadyReasons } = collectAgentNotReadyReasons(
    actualAgent,
    isAgentType,
    sourceTenantId,
  );

  if (notReadyReasons.length > 0) {
    showAgentModeSelector.value = false;
    showAgentNotReadyMessage(actualAgent, notReadyReasons, notReadyKeys, sourceTenantId);
    return;
  }

  settingsStore.selectAgent(agent.id, sourceTenantId);
  settingsStore.toggleAgent(!!isAgentType);

  // 同步智能体的配置状态（含内置、自定义、共享智能体）：模型、网络搜索、知识库由 watch 同步
  // 1. 同步网络搜索状态
  const agentWebSearch = agent.config?.web_search_enabled;
  if (agentWebSearch !== undefined) {
    settingsStore.toggleWebSearch(agentWebSearch);
  } else if (agent.is_builtin) {
    // 内置智能体未配置时保留当前用户设置
  }

  // 2. 同步模型（选中的对话模型随智能体切换，含共享智能体）
  const agentModel = agent.config?.model_id;
  if (agentModel && agentModel.trim() !== '') {
    selectedModelId.value = agentModel;
  } else {
    const lastPick = readLastChatModelID();
    if (lastPick) {
      selectedModelId.value = lastPick;
    }
  }

  showAgentModeSelector.value = false;

  // Only the two "mode-entry" built-ins are re-branded as "Normal / Agent Mode"
  // in the dropdown — the switched-on/off toasts only make sense for them.
  // Other built-ins (wiki researcher, data analyst, etc.) share `is_builtin`
  // but should fall back to the generic agentSelected toast like custom agents,
  // otherwise selecting e.g. the Wiki Questioner incorrectly says
  // "Switched to Intelligent Reasoning".
  const isModeBuiltin =
    agent.id === BUILTIN_QUICK_ANSWER_ID || agent.id === BUILTIN_SMART_REASONING_ID;
  const message = isModeBuiltin
    ? (isAgentType ? t('input.messages.agentSwitchedOn') : t('input.messages.agentSwitchedOff'))
    : t('input.messages.agentSelected', { name: agent.name });
  MessagePlugin.success(message);
}

const clearvalue = () => {
  // Guard: only clear when the textarea DOM element is still mounted,
  // otherwise TDesign's autosize will call getComputedStyle on a non-Element.
  if (!getTextareaEl()) return;
  query.value = "";
}

const onKeydown = (val: string, event: { e: { preventDefault(): unknown; keyCode: number; shiftKey: any; ctrlKey: any; }; }) => {
  if (showMention.value) {
    if (event.e.keyCode === 38) { // Up
      event.e.preventDefault();
      mentionSelectorRef.value?.moveActive(-1);
      return;
    }
    if (event.e.keyCode === 40) { // Down
      event.e.preventDefault();
      mentionSelectorRef.value?.moveActive(1);
      return;
    }
    if (event.e.keyCode === 13) { // Enter
      event.e.preventDefault();
      mentionSelectorRef.value?.confirmActive();
      return;
    }
    if (event.e.keyCode === 27) { // Esc
      if (mentionSelectorRef.value?.leaveGroup()) {
        return;
      }
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

const onPaste = (e: ClipboardEvent) => {
  const items = e.clipboardData?.items;
  if (!items) return;
  const imageFiles: File[] = [];
  for (const item of items) {
    if (item.type.startsWith('image/')) {
      const file = item.getAsFile();
      if (file) imageFiles.push(file);
    }
  }
  if (imageFiles.length > 0 && isImageUploadEnabledByAgent.value) {
    e.preventDefault();
    addImageFiles(imageFiles);
  }
};

const onDrop = (e: DragEvent) => {
  e.preventDefault();
  const files = e.dataTransfer?.files;
  if (!files || files.length === 0) return;
  handleDroppedFiles(Array.from(files));
};

const onDragOver = (e: DragEvent) => {
  e.preventDefault();
};

const handleGoToWebSearchSettings = () => {
  uiStore.openSettings('websearch');
  if (route.path !== '/platform/settings') {
    router.push('/platform/settings');
  }
};

const handleGoToAgentSettings = (section?: string) => {
  const agent = selectedAgent.value;
  if (!agent) {
    router.push('/platform/agents');
    return;
  }
  const query: Record<string, string> = { edit: agent.id };
  if (section) {
    query.section = section;
  }
  router.push({ path: '/platform/agents', query });
};

const formatAgentNotReadyReasons = (
  reasonKeys: AgentNotReadyReasonKey[],
  isBuiltin: boolean,
): string[] => {
  return reasonKeys.map((key) => {
    if (key === 'summary_model') {
      return isBuiltin
        ? t('input.agentMissingSummaryModel')
        : t('input.customAgentMissingSummaryModel');
    }
    if (key === 'rerank_model') {
      return isBuiltin
        ? t('input.agentMissingRerankModel')
        : t('input.customAgentMissingRerankModel');
    }
    return t('input.agentMissingAllowedTools');
  });
};

const collectAgentNotReadyReasons = (
  agent: CustomAgent,
  isAgentMode: boolean,
  sourceTenantId?: string,
): { keys: AgentNotReadyReasonKey[]; labels: string[] } => {
  const isSharedAgent = !!sourceTenantId;
  const keys = getAgentNotReadyReasonKeys(agent.config, allModels.value, {
    isAgentMode,
    isSharedAgent,
  });
  return {
    keys,
    labels: formatAgentNotReadyReasons(keys, agent.is_builtin),
  };
};

const goToAgentEditor = (
  agent: CustomAgent,
  section = 'model',
  highlight?: AgentNotReadyReasonKey,
  sourceTenantId?: string,
) => {
  router.push({
    path: '/platform/agents',
    query: {
      edit: agent.id,
      section,
      ...(highlight ? { highlight } : {}),
      ...(sourceTenantId ? { sourceTenantId } : {}),
    },
  });
};

// 显示智能体未就绪的消息（统一处理内置和自定义智能体）
const showAgentNotReadyMessage = (
  agent: CustomAgent,
  reasons: string[],
  reasonKeys?: AgentNotReadyReasonKey[],
  sourceTenantId?: string,
) => {
  const reasonsText = formatLocalizedList(reasons, locale.value)
  const isRemoteShared = !canLocallyConfigureAgent(sourceTenantId)

  const messageContent = h('div', { style: 'display: flex; flex-direction: column; gap: 8px; max-width: 320px;' }, [
    h(
      'span',
      { style: 'color: var(--td-text-color-primary); line-height: 1.5;' },
      isRemoteShared
        ? t('input.sharedAgentNotReadyDetail', { agentName: agent.name, reasons: reasonsText })
        : t('input.agentNotReadyDetail', { agentName: agent.name, reasons: reasonsText }),
    ),
    ...(isRemoteShared ? [] : [
      h('a', {
        href: '#',
        onClick: (e: Event) => {
          e.preventDefault();
          const section = resolveAgentNotReadySection(reasonKeys || ['summary_model'])
          const highlight = resolveAgentNotReadyHighlight(reasonKeys || ['summary_model'])
          goToAgentEditor(agent, section, highlight, sourceTenantId);
        },
        style: 'color: var(--td-brand-color); text-decoration: none; font-weight: 500; cursor: pointer; align-self: flex-start;',
        onMouseenter: (e: Event) => {
          (e.target as HTMLElement).style.textDecoration = 'underline';
        },
        onMouseleave: (e: Event) => {
          (e.target as HTMLElement).style.textDecoration = 'none';
        }
      }, t('input.goToAgentEditor')),
    ]),
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
      h('span', { style: 'color: var(--td-text-color-primary); line-height: 1.5;' }, t('input.messages.webSearchNotConfigured')),
      h('a', {
        href: '#',
        onClick: (e: Event) => {
          e.preventDefault();
          handleGoToWebSearchSettings();
        },
        style: 'color: var(--td-brand-color); text-decoration: none; font-weight: 500; cursor: pointer; align-self: flex-start;',
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

defineExpose({
  triggerSend(text: string) {
    if (!text.trim()) return;
    query.value = text;
    nextTick(() => createSession(text));
  }
});

</script>
<template>
  <div class="answers-input" :class="{ 'is-embedded': embeddedMode }" @drop="onDrop" @dragover="onDragOver">
    <!-- Hidden file input for image upload -->
    <input ref="imageInputRef" type="file" accept="image/jpeg,image/png,image/gif,image/webp" multiple
      style="display:none" @change="handleImageSelect" />
    <!-- 富文本输入框容器 -->
    <div class="rich-input-container" data-guide="chat-input">
      <!-- 图片预览区域 -->
      <div v-if="uploadedImages.length > 0" class="image-preview-bar">
        <div v-for="(img, idx) in uploadedImages" :key="idx" class="image-preview-item">
          <img :src="img.preview" class="image-preview-thumb" />
          <span class="image-preview-remove" @click="removeImage(idx)">×</span>
        </div>
      </div>

      <!-- 附件列表区域 (由 AttachmentUpload 组件渲染) -->
      <AttachmentUpload ref="attachmentUploadRef" :max-files="5" :max-size="20"
        @update:files="uploadedAttachments = $event" />

      <!-- 选中的知识库和文件标签（显示在输入框内顶部） -->
      <div v-if="allSelectedItems.length > 0" class="selected-tags-inline">
        <span v-for="item in allSelectedItems" :key="`${item.type}:${item.id}`" class="mention-chip" :class="[
          getMentionChipClass(item),
          { 'mention-chip--agent': item.isAgentConfigured }
        ]">
          <span class="mention-chip__icon-wrap" :class="{ 'has-org': item.org_name }">
            <span class="mention-chip__icon">
              <t-icon v-if="item.type === 'kb'" :name="item.kbType === 'faq' ? 'chat-bubble-help' : 'folder'" />
              <t-icon v-else :name="getMentionIcon(item)" />
            </span>
            <span v-if="item.org_name" class="mention-chip__org-badge">
              <img :src="getImgSrc(item.type === 'file' ? 'organization-grey.svg' : 'organization-green.svg')"
                class="mention-chip__org-img" alt="" aria-hidden="true" />
            </span>
          </span>
          <span class="mention-chip__name" :title="item.name">{{ item.name }}</span>
          <span class="mention-chip__remove" @click.stop="removeSelectedItem(item)"
            :aria-label="$t('common.remove')">×</span>
        </span>
      </div>

      <!-- 实际输入框 -->
      <t-textarea ref="textareaRef" v-model="query" :placeholder="inputPlaceholder" name="description" :autosize="true"
        @keydown="onKeydown" @input="onInput" @compositionstart="onCompositionStart" @compositionend="onCompositionEnd"
        @paste="onPaste" />

      <!-- 控制栏（放在 rich-input-container 内，相对输入框边框定位） -->
      <div class="control-bar" :class="{ 'is-embedded': embeddedMode }">
        <!-- 左侧控制按钮 -->
        <div class="control-left" v-if="!embeddedMode">
          <!-- Agent 模式切换按钮 -->
          <div ref="agentModeButtonRef" class="control-btn agent-mode-btn" :class="{
            'is-normal': !isCustomAgent && !isAgentEnabled,
            'is-agent': !isCustomAgent && isAgentEnabled,
            'is-custom': isCustomAgent
          }" @click.stop="toggleAgentModeSelector">
            <span class="agent-mode-text">
              {{ selectedAgent.name || (isAgentEnabled ? $t('input.agentMode') : $t('input.normalMode')) }}
            </span>
            <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" class="dropdown-arrow"
              :class="{ 'rotate': showAgentModeSelector }">
              <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
            </svg>
          </div>

          <!-- Agent 选择器下拉菜单 -->
          <AgentSelector :visible="showAgentModeSelector" :anchorEl="agentModeButtonRef"
            :currentAgentId="selectedAgentId" :agents="enabledAgents" :all-models="allModels"
            @close="closeAgentModeSelector" @select="handleSelectAgent" @not-ready="handleAgentNotReady" />

          <!-- WebSearch 开关按钮 -->
          <t-tooltip placement="top" theme="light" :popupProps="{ overlayClassName: 'input-field-tooltip' }">
            <template #content>
              <div v-if="isWebSearchDisabledByAgent" class="tooltip-with-link">
                <span>{{ $t('input.webSearchDisabledByAgent') }}</span>
                <a href="#" @click.prevent="handleGoToAgentSettings('websearch')">{{ $t('input.goToAgentSettings')
                }}</a>
              </div>
              <span v-else-if="isWebSearchConfigured">{{ isWebSearchEnabled ? $t('input.webSearch.toggleOff') :
                $t('input.webSearch.toggleOn') }}</span>
              <div v-else class="tooltip-with-link">
                <span>{{ $t('input.webSearch.notConfigured') }}</span>
                <a href="#" @click.prevent="handleGoToWebSearchSettings">{{ $t('input.goToSettings') }}</a>
              </div>
            </template>
            <div class="control-btn websearch-btn" :class="{
              'active': isWebSearchEnabled && isWebSearchConfigured,
              'disabled': !isWebSearchConfigured || isWebSearchDisabledByAgent
            }" @click.stop="toggleWebSearch">
              <svg width="18" height="18" viewBox="0 0 18 18" fill="none" xmlns="http://www.w3.org/2000/svg"
                class="control-icon websearch-icon" :class="{ 'active': isWebSearchEnabled && isWebSearchConfigured }">
                <circle cx="9" cy="9" r="7" stroke="currentColor" stroke-width="1.2" fill="none" />
                <path d="M 9 2 A 3.5 7 0 0 0 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
                <path d="M 9 2 A 3.5 7 0 0 1 9 16" stroke="currentColor" stroke-width="1.2" fill="none" />
                <line x1="2.94" y1="5.5" x2="15.06" y2="5.5" stroke="currentColor" stroke-width="1.2"
                  stroke-linecap="round" />
                <line x1="2.94" y1="12.5" x2="15.06" y2="12.5" stroke="currentColor" stroke-width="1.2"
                  stroke-linecap="round" />
              </svg>
            </div>
          </t-tooltip>

          <!-- 图片上传按钮 -->
          <t-tooltip placement="top" theme="light" :popupProps="{ overlayClassName: 'input-field-tooltip' }">
            <template #content>
              <div v-if="!isImageUploadEnabledByAgent" class="tooltip-with-link">
                <span>{{ $t('input.imageUploadDisabledByAgent') }}</span>
                <a href="#" @click.prevent="handleGoToAgentSettings('model')">{{ $t('input.goToAgentSettings') }}</a>
              </div>
              <span v-else>{{ $t('chat.imageUploadTooltip') }}</span>
            </template>
            <div class="control-btn image-upload-btn" :class="{
              'active': uploadedImages.length > 0,
              'disabled': !isImageUploadEnabledByAgent
            }" @click.stop="isImageUploadEnabledByAgent && triggerImageUpload()">
              <svg width="18" height="18" viewBox="0 0 1024 1024" fill="currentColor" class="control-icon">
                <path
                  d="M896 128H128c-35.3 0-64 28.7-64 64v640c0 35.3 28.7 64 64 64h768c35.3 0 64-28.7 64-64V192c0-35.3-28.7-64-64-64zM128 832V192h768l0.1 640H128z" />
                <path d="M352 448a96 96 0 1 0 0-192 96 96 0 0 0 0 192z" />
                <path d="M128 768l224-288 160 160 192-256L896 640v128H128z" />
              </svg>
              <span v-if="uploadedImages.length > 0" class="image-count">{{ uploadedImages.length }}</span>
            </div>
          </t-tooltip>

          <!-- 附件上传按钮 -->
          <t-tooltip placement="top" theme="light" :popupProps="{ overlayClassName: 'input-field-tooltip' }">
            <template #content>
              <span>{{ uploadedAttachments.length > 0 ? $t('chat.attachmentWithCount', {
                count: uploadedAttachments.length
              }) : $t('chat.attachmentUploadTooltip') }}</span>
            </template>
            <div class="control-btn attachment-upload-btn" :class="{ 'active': uploadedAttachments.length > 0 }"
              @click.stop="attachmentUploadRef?.triggerFileSelect()">
              <!-- 回形针图标 -->
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"
                stroke-linecap="round" stroke-linejoin="round" class="control-icon">
                <path
                  d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48" />
              </svg>
              <span v-if="uploadedAttachments.length > 0" class="attachment-count">{{ uploadedAttachments.length
              }}</span>
            </div>
          </t-tooltip>

          <!-- @ 知识库/文件选择按钮 -->
          <t-tooltip placement="top" theme="light" :popupProps="{ overlayClassName: 'input-field-tooltip' }">
            <template #content>
              <div v-if="isMentionDisabled && isKnowledgeBaseDisabledByAgent" class="tooltip-with-link">
                <span>{{ $t('input.kbDisabledByAgent') }}</span>
                <a href="#" @click.prevent="handleGoToAgentSettings('knowledge')">{{ $t('input.goToAgentSettings')
                }}</a>
              </div>
              <span v-else>{{ allSelectedItems.length > 0 ? $t('input.knowledgeBaseWithCount', {
                count:
                  allSelectedItems.length
              }) : $t('input.knowledgeBase') }}</span>
            </template>
            <div ref="atButtonRef" class="control-btn kb-btn" data-guide="chat-kb-mention" :class="{
              'active': allSelectedItems.length > 0,
              'disabled': isMentionDisabled
            }" @click.stop @mousedown.prevent="triggerMention">
              <svg width="18" height="18" viewBox="0 0 20 20" fill="none" xmlns="http://www.w3.org/2000/svg"
                class="control-icon at-icon">
                <circle cx="10" cy="10" r="3.5" stroke="currentColor" stroke-width="1.8" />
                <path
                  d="M13.5 10V11.5C13.5 12.163 13.7634 12.7989 14.2322 13.2678C14.7011 13.7366 15.337 14 16 14C16.663 14 17.2989 13.7366 17.7678 13.2678C18.2366 12.7989 18.5 12.163 18.5 11.5V10C18.5 7.74566 17.6045 5.58365 16.0104 3.98959C14.4163 2.39553 12.2543 1.5 10 1.5C7.74566 1.5 5.58365 2.39553 3.98959 3.98959C2.39553 5.58365 1.5 7.74566 1.5 10C1.5 12.2543 2.39553 14.4163 3.98959 16.0104C5.58365 17.6045 7.74566 18.5 10 18.5H12"
                  stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
              <span v-if="allSelectedItems.length > 0" class="kb-count">{{ allSelectedItems.length }}</span>
            </div>
          </t-tooltip>

          <!-- 模型显示 -->
          <t-tooltip :content="isModelLockedByAgent ? $t('input.modelLockedByAgent') : ''"
            :disabled="!isModelLockedByAgent">
            <div class="model-display" :class="{ 'agent-controlled': isModelLockedByAgent }">
              <div ref="modelButtonRef" class="model-selector-trigger" @click.stop="toggleModelSelector">
                <span class="model-selector-name">
                  {{ selectedModelDisplayName }}
                </span>
                <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" class="model-dropdown-arrow"
                  :class="{ 'rotate': showModelSelector }">
                  <path d="M2.5 4.5L6 8L9.5 4.5H2.5Z" />
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
                <div v-for="model in availableModels" :key="model.id" class="model-option"
                  :class="{ selected: model.id === selectedModelId }" @click="handleModelChange(model.id || '')">
                  <div class="model-option-left">
                    <div class="model-option-icon">
                      <t-icon name="chat" size="14px" />
                    </div>
                    <div class="model-option-name-wrap">
                      <span class="model-option-name">{{ modelDisplayName(model) }}</span>
                      <span v-if="model.display_name" class="model-option-raw-name">{{ model.name }}</span>
                    </div>
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
          <t-tooltip v-if="isReplying" :content="$t('input.stopGeneration')" placement="top">
            <div @click="handleStop" class="control-btn stop-btn">
              <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                <rect x="5" y="5" width="6" height="6" rx="1" />
              </svg>
            </div>
          </t-tooltip>

          <!-- 发送按钮 -->
          <div v-if="!isReplying" @click="createSession(query)" class="control-btn send-btn" data-guide="chat-send"
            :class="{ 'disabled': !query.length }">
            <img src="../assets/img/sending-aircraft.svg" :alt="$t('input.send')" />
          </div>
        </div>
      </div>
    </div>

    <!-- Mention Selector -->
    <Teleport to="body">
      <MentionSelector ref="mentionSelectorRef" :visible="showMention" :style="mentionStyle" :items="mentionItems" :hasMore="mentionHasMore"
        :loading="mentionLoading" :emptyHint="mentionEmptyHint" :query="mentionQuery" :group-counts="mentionGroupCounts" v-model:activeIndex="mentionActiveIndex"
        @select="onMentionSelect" @loadMore="loadMoreMentionItems" />
    </Teleport>

    <!-- 知识库选择下拉（使用 Teleport 传送到 body，避免父容器定位影响） -->
    <Teleport to="body">
      <KnowledgeBaseSelector v-model:visible="showKbSelector" :anchorEl="atButtonRef" @close="showKbSelector = false" />
    </Teleport>
  </div>
</template>
<script lang="ts">
const getImgSrc = (url: string) => {
  return new URL(`/src/assets/img/${url}`, import.meta.url).href;
}
</script>
<style scoped lang="less">
@import './css/chat-resource-chips.less';

.answers-input {
  position: absolute;
  z-index: 99;
  bottom: 60px;
  left: 50%;
  transform: translateX(-50%);
  width: 100%;
  display: flex;
  justify-content: center;

  &.is-embedded {
    position: relative;
    bottom: auto;
    left: auto;
    transform: none;
    z-index: auto;

    .rich-input-container {
      max-width: 100%;
    }
  }
}

/* 富文本输入框容器 */
.rich-input-container {
  position: relative;
  width: 100%;
  max-width: 800px;
  background: var(--td-bg-color-container, #FFF);
  border-radius: 12px;
  border: 1px solid var(--td-component-stroke, #dcdcdc);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04), 0 8px 16px -4px rgba(0, 0, 0, 0.06);

  &:focus-within {
    border-color: var(--td-brand-color, #07C05F);
  }
}

/* 选中的知识库/文件标签（mention list 已选项） */
.selected-tags-inline {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
  padding: 6px 12px 6px;
  border-bottom: 1px solid var(--td-component-stroke, #dcdcdc);
  background: var(--td-bg-color-container, #fff);
  border-radius: 11px 11px 0 0;
  /* 与 .rich-input-container 内缘上边圆角一致（12px - 1px 边框） */
}

.mention-chip {
  .chat-resource-chip-surface();

  display: inline-flex;
  align-items: center;
  gap: 5px;
  min-height: 26px;
  padding: 3px 7px 3px 6px;
  border-radius: var(--td-radius-medium, 6px);
  box-sizing: border-box;
  font-size: 12px;
  font-weight: 500;
  cursor: default;
  transition: background 0.15s, border-color 0.15s;
  line-height: 18px;

  &:hover {
    .chat-resource-chip-hover();
  }
}

.mention-chip__icon-wrap {
  position: relative;
  display: inline-flex;
  width: 16px;
  height: 16px;
  flex: 0 1 auto;
  min-width: 0;
  align-items: center;
  justify-content: center;
}

.mention-chip__icon {
  font-size: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: inherit;
}

.mention-chip__org-badge {
  position: absolute;
  right: -1px;
  bottom: -1px;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--td-bg-color-secondarycontainer, #f0f2f5);
  box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.06);
  display: flex;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

.mention-chip__org-img {
  width: 5px;
  height: 5px;
  object-fit: contain;
}

.mention-chip__name {
  max-width: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: currentColor;
}

.mention-chip__remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  margin-left: 1px;
  border-radius: 50%;
  font-size: 14px;
  line-height: 1;
  font-weight: 400;
  cursor: pointer;
  opacity: 0.5;
  transition: opacity 0.15s, background 0.15s, color 0.15s;
  color: currentColor;
  flex-shrink: 0;
}

.mention-chip:hover .mention-chip__remove {
  opacity: 0.85;
}

.mention-chip__remove:hover {
  opacity: 1;
  background: var(--td-bg-color-component);
  color: var(--td-text-color-primary, #1f2937);
}

/* 标签表面保持中性，仅用图标颜色表达资源类型。 */
.mention-chip--kb {
  color: var(--td-text-color-primary);
}

.mention-chip--kb .mention-chip__icon-wrap {
  color: var(--td-brand-color, #07c05f);
}

.mention-chip--faq {
  color: var(--td-text-color-primary);
}

.mention-chip--faq .mention-chip__icon-wrap {
  color: var(--weknora-faq-color, #0052d9);
}

.mention-chip--file {
  color: var(--td-text-color-primary);
}

.mention-chip--file .mention-chip__icon-wrap {
  color: var(--td-text-color-secondary, #6b7280);
}

.mention-chip--tag,
.mention-chip--mcp,
.mention-chip--tool {
  color: var(--td-text-color-primary);
}

.mention-chip--tag .mention-chip__icon-wrap {
  color: #9f7aea;
}

.mention-chip--mcp .mention-chip__icon-wrap {
  color: #0f766e;
}

.mention-chip--tool .mention-chip__icon-wrap {
  color: #b7791f;
}

/* 智能体预配置：虚线边框区分 */
.mention-chip--agent {
  border-style: dashed;
  border-color: var(--td-component-border);
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
  font-family: var(--app-font-family);
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
    font-family: var(--app-font-family);
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

  &.is-embedded {
    justify-content: flex-end;
  }
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
  color: var(--td-text-color-secondary, #666);
  cursor: pointer;
  transition: background 0.12s, color 0.12s;
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
  position: relative;
  border: .5px solid var(--td-component-border, #e7e7e7);
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
  color: var(--td-text-color-secondary, #666);
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
  width: 30px;
  padding: 0;
  min-width: 30px;
  position: relative;

  &.active {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-brand-color);
    box-shadow: inset 0 0 0 1px var(--td-component-stroke);

    &:hover {
      background: var(--td-bg-color-secondarycontainer-hover);
    }
  }

  &.agent-controlled {
    cursor: not-allowed;
    opacity: 0.85;

    &:hover {
      background: var(--td-bg-color-secondarycontainer, #f5f5f5);
    }

    &.active:hover {
      background: var(--td-bg-color-secondarycontainer);
    }
  }
}

.kb-count {
  position: absolute;
  top: -5px;
  right: -5px;
  min-width: 15px;
  height: 15px;
  padding: 0 3px;
  background: var(--td-brand-color);
  color: var(--td-text-color-anti, #fff);
  font-size: 9px;
  font-weight: 600;
  line-height: 15px;
  border: 2px solid var(--td-bg-color-container);
  border-radius: var(--td-radius-round, 999px);
  box-sizing: content-box;
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
  color: var(--td-brand-color);
}

/* Image upload */
.image-upload-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  min-width: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  color: var(--td-text-color-secondary, #666);

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover, #f0f0f0);
    color: var(--td-text-color-primary, #333);
  }

  &.active {
    background: rgba(16, 185, 129, 0.1);
    color: #07C05F;
  }

  .image-count {
    position: absolute;
    top: -2px;
    right: -2px;
    background: #07C05F;
    color: #fff;
    font-size: 10px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }
}

/* Attachment upload */
.attachment-upload-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  min-width: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  color: var(--td-text-color-secondary, #666);

  &:hover {
    background: var(--td-bg-color-secondarycontainer-hover, #f0f0f0);
    color: var(--td-text-color-primary, #333);
  }

  &.active {
    background: rgba(16, 185, 129, 0.1);
    color: #07C05F;
  }

  .attachment-count {
    position: absolute;
    top: -2px;
    right: -2px;
    background: #07C05F;
    color: #fff;
    font-size: 10px;
    width: 14px;
    height: 14px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }
}

.image-preview-bar {
  display: flex;
  gap: 8px;
  padding: 8px 12px 4px;
  flex-wrap: wrap;
}

.image-preview-item {
  position: relative;
  width: 60px;
  height: 60px;
  border-radius: 8px;
  overflow: hidden;
  border: 1px solid var(--td-border-level-1-color, #e7e7e7);

  .image-preview-thumb {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  .image-preview-remove {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 16px;
    height: 16px;
    background: rgba(0, 0, 0, 0.5);
    color: #fff;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    cursor: pointer;
    line-height: 1;

    &:hover {
      background: rgba(0, 0, 0, 0.7);
    }
  }
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
      color: var(--td-brand-color);
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
    box-shadow: var(--td-shadow-2);
    border: .5px solid var(--td-component-border, #e7e7e7);
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
  color: var(--td-brand-color);
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
  color: var(--td-brand-color);
  border: 1.5px solid rgba(16, 185, 129, 0.2);
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;

  &:hover {
    background: rgba(16, 185, 129, 0.12);
    border-color: var(--td-brand-color);
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
    background: var(--td-brand-color);
    border-radius: 50%;
    display: block;
    animation: stopBtnPulse 1.5s ease-in-out infinite;
  }
}

@keyframes stopBtnPulse {

  0%,
  100% {
    transform: scale(1);
    opacity: 1;
  }

  50% {
    transform: scale(0.75);
    opacity: 0.6;
  }
}

.send-btn {
  width: 28px;
  height: 28px;
  padding: 0;
  background-color: var(--td-brand-color);

  &:hover:not(.disabled) {
    background-color: var(--td-brand-color-active);
  }

  &.disabled {
    background-color: var(--td-success-color-light);
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
      opacity: 0.5;
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
  border: .5px solid var(--td-component-border, #e7e7e7);
  transition: background 0.12s, border-color 0.12s;
  cursor: pointer;

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

.model-selector-name {
  flex: 1;
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-secondary, #666);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.model-dropdown-arrow {
  width: 10px;
  height: 10px;
  color: var(--td-text-color-placeholder, #999);
  flex-shrink: 0;
  transition: transform 0.12s;

  &.rotate {
    transform: rotate(180deg);
  }
}

.model-selector-trigger.disabled .model-dropdown-arrow {
  color: var(--td-text-color-placeholder, #999);
}

.model-selector-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  background: transparent;
  touch-action: none;
}

.model-selector-dropdown {
  position: fixed !important;
  z-index: 10000;
  background: var(--td-bg-color-container);
  border: .5px solid var(--td-component-border);
  border-radius: 10px;
  box-shadow: var(--td-shadow-2);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  margin: 0 !important;
  padding: 0 !important;
  transform: none !important;
  transform-origin: top left;
  animation: modelSelectorFadeIn 0.15s ease-out;
}

@keyframes modelSelectorFadeIn {
  from {
    opacity: 0;
    transform: scale(0.98);
  }

  to {
    opacity: 1;
    transform: scale(1);
  }
}

.model-selector-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-bottom: .5px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
}

.model-selector-content {
  flex: 1;
  min-height: 0;
  max-height: 260px;
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
  border-radius: 6px;
  border: .5px solid transparent;
  background: transparent;
  color: var(--td-brand-color);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.12s;

  .add-icon {
    font-size: 14px;
    line-height: 1;
    font-weight: 400;
  }

  &:hover {
    color: var(--td-brand-color-hover);
    background: var(--td-bg-color-secondarycontainer);
  }
}

.model-option {
  display: flex;
  align-items: center;
  padding: 6px 8px;
  cursor: pointer;
  transition: background 0.12s;
  border-radius: 6px;
  margin-bottom: 4px;

  &:last-child {
    margin-bottom: 0;
  }

  &:hover,
  &.selected {
    background: var(--td-bg-color-secondarycontainer);
  }

  &.empty {
    color: var(--td-text-color-placeholder);
    cursor: default;
    text-align: center;
    padding: 20px 8px;

    &:hover {
      background: transparent;
    }
  }
}

.model-option-left {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-width: 0;
}

.model-option-icon {
  width: 16px;
  height: 16px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.model-option-name-wrap {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.model-option-name {
  font-size: 12px;
  color: var(--td-text-color-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.4;
}

.model-option-raw-name {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
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
      color: var(--td-success-color);
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
  color: var(--td-success-color);
  flex-shrink: 0;
  margin-left: 6px;
}

.agent-mode-warning {
  display: flex;
  align-items: center;
  margin-left: 6px;

  .warning-icon {
    color: var(--td-warning-color);
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
  color: var(--td-success-color);
  text-decoration: none;
  font-size: 11px;
  font-weight: 500;
  display: inline-flex;
  align-items: center;
  gap: 3px;
  transition: all 0.12s;

  &:hover {
    color: var(--td-brand-color-active);
    text-decoration: underline;
  }
}
</style>
