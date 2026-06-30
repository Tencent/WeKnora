import { defineStore } from "pinia";
import { nextTick } from "vue";
import { BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from "@/api/agent";
import { getApiBaseUrl } from "@/utils/api-base";
import { updateMyPreferences, type UserPreferences } from "@/api/auth";
import { isAgentStreamAgentId, reconcileBuiltinAgentMode } from "@/utils/agent-mode";

// 定义设置接口
interface Settings {
  endpoint: string;
  apiKey: string;
  knowledgeBaseId: string;
  isAgentEnabled: boolean;
  agentConfig: AgentConfig;
  selectedKnowledgeBases: string[];  // 当前选中的知识库ID列表
  selectedFiles: string[]; // 当前选中的文件ID列表
  selectedFileKbMap: Record<string, string>; // 文件ID -> 知识库ID，用于刷新后带 kb_id 拉取共享知识库文件
  selectedTags: Array<{ id: string; name: string; kbId: string; kbName?: string }>;
  selectedMCPServices: string[];
  selectedSkills: string[];
  modelConfig: ModelConfig;  // 模型配置
  ollamaConfig: OllamaConfig;  // Ollama配置
  webSearchEnabled: boolean;  // 网络搜索是否启用
  enableMemory: boolean;      // 是否开启记忆功能
  conversationModels: ConversationModels;
  selectedAgentId: string;  // 当前选中的智能体ID
  selectedAgentSourceTenantId: string | null;  // 当使用共享智能体时，来源租户 ID（用于后端 model/KB/MCP 解析）
  autoCheckUpdate?: boolean; // 是否自动检查并下载更新
}

// Agent 配置接口
interface AgentConfig {
  maxIterations: number;
  temperature: number;
  allowedTools: string[];
  system_prompt?: string;  // Unified system prompt (uses {{web_search_status}} placeholder)
}

interface ConversationModels {
  summaryModelId: string;
  rerankModelId: string;
  selectedChatModelId: string;  // 用户当前选择的对话模型ID
}

// 单个模型项接口
interface ModelItem {
  id: string;  // 唯一ID
  name: string;  // 显示名称
  source: 'local' | 'remote';  // 模型来源
  modelName: string;  // 模型标识
  baseUrl?: string;  // 远程API URL
  apiKey?: string;  // 远程API Key
  dimension?: number;  // Embedding专用：向量维度
  interfaceType?: 'ollama' | 'openai';  // VLLM专用：接口类型
  isDefault?: boolean;  // 是否为默认模型
}

// 模型配置接口 - 支持多模型
interface ModelConfig {
  chatModels: ModelItem[];
  embeddingModels: ModelItem[];
  rerankModels: ModelItem[];
  vllmModels: ModelItem[];  // VLLM视觉模型
}

// Ollama 配置接口
interface OllamaConfig {
  baseUrl: string;  // Ollama 服务地址
  enabled: boolean;  // 是否启用
}

// 默认设置
const defaultSettings: Settings = {
  endpoint: getApiBaseUrl(),
  apiKey: "",
  knowledgeBaseId: "",
  isAgentEnabled: false,
  agentConfig: {
    maxIterations: 5,
    temperature: 0.7,
    allowedTools: [],  // 默认为空，需要通过 API 从后端加载
    system_prompt: "",
  },
  selectedKnowledgeBases: [],  // 默认为空数组
  selectedFiles: [], // 默认为空数组
  selectedFileKbMap: {},  // 文件ID -> 知识库ID
  selectedTags: [],
  selectedMCPServices: [],
  selectedSkills: [],
  modelConfig: {
    chatModels: [],
    embeddingModels: [],
    rerankModels: [],
    vllmModels: []
  },
  ollamaConfig: {
    baseUrl: "http://localhost:11434",
    enabled: true
  },
  webSearchEnabled: false,  // 默认关闭网络搜索
  enableMemory: false,       // 默认关闭记忆功能
  conversationModels: {
    summaryModelId: "",
    rerankModelId: "",
    selectedChatModelId: "",  // 用户当前选择的对话模型ID
  },
  selectedAgentId: BUILTIN_QUICK_ANSWER_ID,  // 默认选中快速问答模式
  selectedAgentSourceTenantId: null as string | null,  // 共享智能体来源租户 ID
  autoCheckUpdate: true,
};

/** Keep builtin agent id and isAgentEnabled in sync after localStorage reload. */
function loadAndReconcileSettings(): Settings {
  const loaded = JSON.parse(
    localStorage.getItem("WeKnora_settings") || JSON.stringify(defaultSettings),
  ) as Settings;
  loaded.selectedTags ||= [];
  loaded.selectedMCPServices ||= [];
  loaded.selectedSkills ||= loaded.selectedTools || [];
  loaded.selectedFileKbMap ||= {};
  if (reconcileBuiltinAgentMode(loaded)) {
    localStorage.setItem("WeKnora_settings", JSON.stringify(loaded));
  }
  return loaded;
}

export const useSettingsStore = defineStore("settings", {
  state: () => ({
    // 从本地存储加载设置，如果没有则使用默认设置
    settings: loadAndReconcileSettings(),
    // 进入会话时拍下"全局默认"的快照；离开会话时还原。非持久化字段：
    // 刷新页面相当于重新走"进入会话"流程，自然会重新拍快照。
    _defaultsSnapshot: null as Settings | null,
    /** 正在从 session.last_request_state 恢复输入栏，避免 agent 切换 watch 覆盖 KB 选择 */
    _isApplyingSessionState: false,
  }),

  getters: {
    // Agent 是否启用
    isAgentEnabled: (state) => state.settings.isAgentEnabled || false,

    // 当前是否为内置快速问答（优先看 selectedAgentId，避免与 isAgentEnabled 漂移）
    isQuickAnswerMode: (state) =>
      (state.settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID) === BUILTIN_QUICK_ANSWER_ID,

    // 是否走 Agent 流式管线（智能推理 / 自定义 Agent）；快速问答走 RAG 管线
    isAgentStreamMode: (state) =>
      isAgentStreamAgentId(
        state.settings.selectedAgentId,
        state.settings.isAgentEnabled || false,
      ),
    
    // Agent 是否就绪（配置完整）
    // 需要满足：1) 配置了允许的工具 2) 设置了对话模型 3) 设置了重排模型
    isAgentReady: (state) => {
      const config = state.settings.agentConfig || defaultSettings.agentConfig
      const models = state.settings.conversationModels || defaultSettings.conversationModels
      return Boolean(
        config.allowedTools && config.allowedTools.length > 0 &&
        models.summaryModelId && models.summaryModelId.trim() !== '' &&
        models.rerankModelId && models.rerankModelId.trim() !== ''
      )
    },
    
    // 普通模式（快速回答）是否就绪
    // 需要满足：1) 设置了对话模型 2) 设置了重排模型
    isNormalModeReady: (state) => {
      const models = state.settings.conversationModels || defaultSettings.conversationModels
      return Boolean(
        models.summaryModelId && models.summaryModelId.trim() !== '' &&
        models.rerankModelId && models.rerankModelId.trim() !== ''
      )
    },
    
    // 获取 Agent 配置
    agentConfig: (state) => state.settings.agentConfig || defaultSettings.agentConfig,

    conversationModels: (state) => state.settings.conversationModels || defaultSettings.conversationModels,
    
    // 获取模型配置
    modelConfig: (state) => state.settings.modelConfig || defaultSettings.modelConfig,
    
    // 网络搜索是否启用
    isWebSearchEnabled: (state) => state.settings.webSearchEnabled || false,
    
    // 记忆功能是否启用
    isMemoryEnabled: (state) => state.settings.enableMemory || false,

    // 是否自动检查并下载更新
    isAutoCheckUpdateEnabled: (state) => state.settings.autoCheckUpdate ?? true,

    // 当前选中的智能体ID
    selectedAgentId: (state) => state.settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID,
    // 共享智能体来源租户 ID（可选）
    selectedAgentSourceTenantId: (state) => state.settings.selectedAgentSourceTenantId ?? null,
  },

  actions: {
    // 保存设置
    saveSettings(settings: Settings) {
      this.settings = { ...settings };
      // 保存到localStorage
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    // 获取设置
    getSettings(): Settings {
      return this.settings;
    },

    // 获取API端点
    getEndpoint(): string {
      return this.settings.endpoint || defaultSettings.endpoint;
    },

    // 获取API Key
    getApiKey(): string {
      return this.settings.apiKey;
    },

    // 获取知识库ID
    getKnowledgeBaseId(): string {
      return this.settings.knowledgeBaseId;
    },
    
    // 启用/禁用 Agent
    toggleAgent(enabled: boolean) {
      this.settings.isAgentEnabled = enabled;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 更新 Agent 配置
    updateAgentConfig(config: Partial<AgentConfig>) {
      this.settings.agentConfig = { ...this.settings.agentConfig, ...config };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    updateConversationModels(models: Partial<ConversationModels>) {
      const current = this.settings.conversationModels || defaultSettings.conversationModels;
      this.settings.conversationModels = { ...current, ...models };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 更新模型配置
    updateModelConfig(config: Partial<ModelConfig>) {
      this.settings.modelConfig = { ...this.settings.modelConfig, ...config };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 添加模型
    addModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', model: ModelItem) {
      const key = `${type}Models` as keyof ModelConfig;
      const models = [...this.settings.modelConfig[key]] as ModelItem[];
      // 如果设为默认，取消其他模型的默认状态
      if (model.isDefault) {
        models.forEach(m => m.isDefault = false);
      }
      // 如果是第一个模型，自动设为默认
      if (models.length === 0) {
        model.isDefault = true;
      }
      models.push(model);
      this.settings.modelConfig[key] = models as any;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 更新模型
    updateModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', modelId: string, updates: Partial<ModelItem>) {
      const key = `${type}Models` as keyof ModelConfig;
      const models = [...this.settings.modelConfig[key]] as ModelItem[];
      const index = models.findIndex(m => m.id === modelId);
      if (index !== -1) {
        // 如果要设为默认，取消其他模型的默认状态
        if (updates.isDefault) {
          models.forEach(m => m.isDefault = false);
        }
        models[index] = { ...models[index], ...updates };
        this.settings.modelConfig[key] = models as any;
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },
    
    // 删除模型
    deleteModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', modelId: string) {
      const key = `${type}Models` as keyof ModelConfig;
      let models = [...this.settings.modelConfig[key]] as ModelItem[];
      const deletedModel = models.find(m => m.id === modelId);
      models = models.filter(m => m.id !== modelId);
      // 如果删除的是默认模型，设置第一个为默认
      if (deletedModel?.isDefault && models.length > 0) {
        models[0].isDefault = true;
      }
      this.settings.modelConfig[key] = models as any;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 设置默认模型
    setDefaultModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', modelId: string) {
      const key = `${type}Models` as keyof ModelConfig;
      const models = [...this.settings.modelConfig[key]] as ModelItem[];
      models.forEach(m => m.isDefault = (m.id === modelId));
      this.settings.modelConfig[key] = models as any;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 更新 Ollama 配置
    updateOllamaConfig(config: Partial<OllamaConfig>) {
      this.settings.ollamaConfig = { ...this.settings.ollamaConfig, ...config };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 选择知识库（替换整个列表）
    selectKnowledgeBases(kbIds: string[]) {
      this.settings.selectedKnowledgeBases = kbIds;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 添加单个知识库
    addKnowledgeBase(kbId: string) {
      if (!this.settings.selectedKnowledgeBases.includes(kbId)) {
        this.settings.selectedKnowledgeBases.push(kbId);
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },
    
    // 移除单个知识库
    removeKnowledgeBase(kbId: string) {
      this.settings.selectedKnowledgeBases = 
        this.settings.selectedKnowledgeBases.filter((id: string) => id !== kbId);
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 清空知识库选择
    clearKnowledgeBases() {
      this.settings.selectedKnowledgeBases = [];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 获取选中的知识库列表
    getSelectedKnowledgeBases(): string[] {
      return this.settings.selectedKnowledgeBases || [];
    },
    
    // 启用/禁用网络搜索
    toggleWebSearch(enabled: boolean) {
      this.settings.webSearchEnabled = enabled;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    // 启用/禁用记忆功能。
    // 现在是"真用户级"开关：
    //   - 本地缓存 (localStorage) 用作 UI 首屏 / 离线兜底；
    //   - PUT /auth/me/preferences 是真正的持久化，跨设备/浏览器同步。
    //
    // 乐观更新：先翻本地状态让 UI 立刻响应，再异步写后端；失败则回滚 + throw
    // 让调用方（GeneralSettings.vue 的 t-switch）可以提示并把开关复位。
    async toggleMemory(enabled: boolean): Promise<void> {
      const previous = !!this.settings.enableMemory;
      this.settings.enableMemory = enabled;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));

      try {
        const resp = await updateMyPreferences({ enable_memory: enabled });
        if (!resp.success) {
          throw new Error(resp.message || "update failed");
        }
      } catch (err) {
        // 回滚本地状态，让 UI 复位到旧值。
        this.settings.enableMemory = previous;
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
        throw err;
      }
    },

    // 从 /auth/me 或 /auth/login 返回的 user.preferences 同步到本地 settings。
    // 调用方：authStore.setUser（每次登录 / 刷新 user / 切租户后都会触发）。
    // 不写后端，纯本地状态 + localStorage 写入，避免把后端的值再原路 PUT 回去。
    hydrateFromUserPreferences(prefs: UserPreferences | undefined | null) {
      if (!prefs) return;
      let changed = false;
      if (typeof prefs.enable_memory === "boolean" &&
          this.settings.enableMemory !== prefs.enable_memory) {
        this.settings.enableMemory = prefs.enable_memory;
        changed = true;
      }
      if (changed) {
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },

    // 启用/禁用自动检查更新
    toggleAutoCheckUpdate(enabled: boolean) {
      this.settings.autoCheckUpdate = enabled;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    // File selection actions
    addFile(fileId: string) {
      if (!this.settings.selectedFiles) this.settings.selectedFiles = [];
      if (!this.settings.selectedFiles.includes(fileId)) {
        this.settings.selectedFiles.push(fileId);
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },

    removeFile(fileId: string) {
      if (!this.settings.selectedFiles) return;
      this.settings.selectedFiles = this.settings.selectedFiles.filter((id: string) => id !== fileId);
      if (this.settings.selectedFileKbMap) delete this.settings.selectedFileKbMap[fileId];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    clearFiles() {
      this.settings.selectedFiles = [];
      this.settings.selectedFileKbMap = {};
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    addTag(tag: { id: string; name: string; kbId: string; kbName?: string }) {
      if (!this.settings.selectedTags) this.settings.selectedTags = [];
      if (!this.settings.selectedTags.some(t => t.id === tag.id && t.kbId === tag.kbId)) {
        this.settings.selectedTags.push(tag);
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },

    removeTag(tagId: string, kbId?: string) {
      if (!this.settings.selectedTags) return;
      this.settings.selectedTags = this.settings.selectedTags.filter(t => !(t.id === tagId && (!kbId || t.kbId === kbId)));
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    clearTags() {
      this.settings.selectedTags = [];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    addMCPService(serviceId: string) {
      if (!this.settings.selectedMCPServices) this.settings.selectedMCPServices = [];
      if (!this.settings.selectedMCPServices.includes(serviceId)) {
        this.settings.selectedMCPServices.push(serviceId);
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },

    removeMCPService(serviceId: string) {
      if (!this.settings.selectedMCPServices) return;
      this.settings.selectedMCPServices = this.settings.selectedMCPServices.filter(id => id !== serviceId);
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    addSkill(skillName: string) {
      if (!this.settings.selectedSkills) this.settings.selectedSkills = [];
      if (!this.settings.selectedSkills.includes(skillName)) {
        this.settings.selectedSkills.push(skillName);
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },

    removeSkill(skillName: string) {
      if (!this.settings.selectedSkills) return;
      this.settings.selectedSkills = this.settings.selectedSkills.filter(name => name !== skillName);
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    setFileKbMap(updates: Record<string, string>) {
      if (!this.settings.selectedFileKbMap) this.settings.selectedFileKbMap = {};
      Object.assign(this.settings.selectedFileKbMap, updates);
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    removeFileKbId(fileId: string) {
      if (this.settings.selectedFileKbMap) delete this.settings.selectedFileKbMap[fileId];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    getSelectedFiles(): string[] {
      return this.settings.selectedFiles || [];
    },

    /** Scope for suggested-questions API (KB / file / tag @mentions). */
    getSuggestedQuestionsParams(limit = 6) {
      const selectedKBs = this.getSelectedKnowledgeBases();
      const selectedFiles = this.getSelectedFiles();
      const tags = this.settings.selectedTags || [];
      const tagIds = [...new Set(tags.map((t) => t.id).filter(Boolean))];
      const tagKbIds = [...new Set(tags.map((t) => t.kbId).filter(Boolean))];
      const kbIds = [...new Set([...selectedKBs, ...tagKbIds])];
      return {
        knowledge_base_ids: kbIds.length > 0 ? kbIds : undefined,
        knowledge_ids: selectedFiles.length > 0 ? selectedFiles : undefined,
        tag_ids: tagIds.length > 0 ? tagIds : undefined,
        limit,
      };
    },
    
    // 选择智能体（sourceTenantId 仅在使用共享智能体时传入）
    selectAgent(agentId: string, sourceTenantId?: string | null) {
      this.settings.selectedAgentId = agentId;
      this.settings.selectedAgentSourceTenantId = (sourceTenantId != null && sourceTenantId !== "") ? sourceTenantId : null;
      // 根据智能体类型自动切换 Agent 模式
      if (agentId === BUILTIN_QUICK_ANSWER_ID) {
        this.settings.isAgentEnabled = false;
      } else if (agentId === BUILTIN_SMART_REASONING_ID) {
        this.settings.isAgentEnabled = true;
      }
      // 自定义智能体需要根据其配置来决定
      
      // 切换智能体时重置知识库和文件选择状态
      // 因为不同智能体关联的知识库不同，需要清空用户之前的选择
      this.settings.selectedKnowledgeBases = [];
      this.settings.selectedFiles = [];
      this.settings.selectedFileKbMap = {};
      this.settings.selectedTags = [];
      this.settings.selectedMCPServices = [];
      this.settings.selectedSkills = [];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 获取选中的智能体ID
    getSelectedAgentId(): string {
      return this.settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID;
    },

    // —— 会话级输入态恢复 —— //
    //
    // 输入栏的 agent / 模型 / KB / 联网 / MCP 等选择由本 store 持有，跨会话共享。
    // 但用户的诉求是：点开旧会话时，能看到当时发起请求的那一套状态。
    // 实现策略：进入会话时把"当前的全局默认"暂存到一个非持久化的 `_defaultsSnapshot`
    // 字段里，然后用 session.last_request_state 覆盖 store；离开会话时从快照还原。
    // 快照不写 localStorage，因为它只在「正处于某个旧会话」这段路由期内有意义；
    // 刷新页面相当于"重新进入会话" → 重新拍快照 + 覆盖，不会丢失用户的全局默认。

    // 拍下当前 settings 作为"离开会话后要还原回去的默认"。
    // 已存在快照时不覆盖，避免会话间切换（B→B'）把已恢复的 store 错当成默认。
    snapshotAsDefaultsIfNeeded() {
      if (this._defaultsSnapshot) return;
      this._defaultsSnapshot = JSON.parse(JSON.stringify(this.settings));
    },

    // 还原默认（如果有快照），用于离开会话或跨会话切换时。
    restoreDefaultsIfSnapshotted() {
      if (!this._defaultsSnapshot) return;
      this.settings = this._defaultsSnapshot;
      this._defaultsSnapshot = null;
      // 不写 localStorage：默认值在快照之前已经写过 localStorage，这里恢复
      // 的就是 localStorage 中既有的值，再写一次只会增加无意义的 IO。
    },

    // 根据 session.last_request_state 覆盖输入栏相关字段。
    // 只触碰本次记录的字段，**不**清空 store 中其它无关字段（如模型列表）。
    // 任何字段缺失则保留 store 现值，做"尽力恢复"。
    applyLastRequestState(state: SessionLastRequestStatePayload | null | undefined) {
      if (!state) return;
      this._isApplyingSessionState = true;
      try {
        if (typeof state.agent_enabled === "boolean") {
          this.settings.isAgentEnabled = state.agent_enabled;
        }
        if (typeof state.agent_id === "string" && state.agent_id) {
          this.settings.selectedAgentId = state.agent_id;
          // 上次记录是自有 agent 还是共享 agent，目前服务端不区分回传 sourceTenantId。
          // 与 selectAgent() 不同，这里**不**重置 KB/文件选择 —— 因为我们紧接着
          // 就要用 state 里的 KB/文件覆盖，不需要先清空再写。
        }
        if (state.model_id !== undefined) {
          const current = this.settings.conversationModels || defaultSettings.conversationModels;
          this.settings.conversationModels = { ...current, selectedChatModelId: state.model_id || "" };
        }
        if (Array.isArray(state.knowledge_base_ids)) {
          this.settings.selectedKnowledgeBases = [...state.knowledge_base_ids];
        }
        if (Array.isArray(state.knowledge_ids)) {
          this.settings.selectedFiles = [...state.knowledge_ids];
          // selectedFileKbMap 此时无法重建（state 里没存 KB 归属），交给前端按
          // 需要 lazy 拉取。保留 store 现值，避免误删用户刚加进来的文件映射。
        }
        if (Array.isArray(state.mentioned_items)) {
          const fromMentions = state.mentioned_items
            .filter(item => item.type === "tag" && item.id && item.kb_id)
            .map(item => ({ id: item.id, name: item.name || item.id, kbId: item.kb_id!, kbName: item.kb_name }));
          const covered = new Set(fromMentions.map(t => t.id));
          const orphanTagIds = (state.tag_ids || []).filter(id => id && !covered.has(id));
          if (orphanTagIds.length > 0 && Array.isArray(state.knowledge_base_ids) && state.knowledge_base_ids.length === 1) {
            const kbId = state.knowledge_base_ids[0];
            orphanTagIds.forEach(id => {
              fromMentions.push({ id, name: id, kbId, kbName: undefined });
            });
          }
          this.settings.selectedTags = fromMentions;
        } else if (Array.isArray(state.tag_ids)) {
          const existing = this.settings.selectedTags || [];
          this.settings.selectedTags = existing.filter(tag => state.tag_ids?.includes(tag.id));
        }
        if (Array.isArray(state.mcp_service_ids)) {
          this.settings.selectedMCPServices = [...state.mcp_service_ids];
        } else if (Array.isArray(state.mentioned_items)) {
          this.settings.selectedMCPServices = state.mentioned_items
            .filter(item => item.type === "mcp" && item.id)
            .map(item => item.id);
        }
        if (Array.isArray(state.skill_names)) {
          this.settings.selectedSkills = [...state.skill_names];
        } else if (Array.isArray(state.mentioned_items)) {
          this.settings.selectedSkills = state.mentioned_items
            .filter(item => item.type === "skill" && item.id)
            .map(item => item.skill_name || item.id);
        }
        if (typeof state.web_search_enabled === "boolean") {
          this.settings.webSearchEnabled = state.web_search_enabled;
        }
      } finally {
        // 复位必须延后到下一次 flush 之后：监听 selectedAgentId 的 watcher 默认
        // flush:'pre'，是异步执行的；若在此处同步复位，watcher 真正运行时标志早已
        // 为 false，守卫形同虚设、恢复出来的 KB 仍会被 agent 配置覆盖。放到 nextTick
        // 可保证本次状态变更触发的 watcher 在标志仍为 true 时执行。
        nextTick(() => {
          this._isApplyingSessionState = false;
        });
      }
      // 注意：故意不写 localStorage —— 旧会话的状态不应污染"用户默认"。
      // 离开会话时 restoreDefaultsIfSnapshotted 会把 localStorage 里那份完整
      // 的默认值再次同步回 this.settings。
    },
  },
});

// 后端 sessions.last_request_state JSON 形状（与 SessionLastRequestState 对齐）。
// 字段全部可选——历史会话或新建会话首发前的请求没有这条记录。
export interface SessionLastRequestStatePayload {
  agent_id?: string;
  agent_enabled?: boolean;
  model_id?: string;
  knowledge_base_ids?: string[];
  knowledge_ids?: string[];
  tag_ids?: string[];
  mcp_service_ids?: string[];
  skill_names?: string[];
  mentioned_items?: Array<{
    id: string;
    name?: string;
    type: string;
    kb_id?: string;
    kb_name?: string;
    skill_name?: string;
  }>;
  web_search_enabled?: boolean;
}
