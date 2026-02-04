import { defineStore } from "pinia";
import { BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from "@/api/agent";

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
  modelConfig: ModelConfig;  // 模型配置
  ollamaConfig: OllamaConfig;  // Ollama配置
  webSearchEnabled: boolean;  // 网络搜索是否启用
  conversationModels: ConversationModels;
  selectedAgentId: string;  // 当前选中的智能体ID
  selectedAgentSourceTenantId: string | null;  // 当使用共享智能体时，来源租户 ID（用于后端 model/KB/MCP 解析）
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
  endpoint: import.meta.env.VITE_IS_DOCKER ? "" : "http://localhost:8080",
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
  conversationModels: {
    summaryModelId: "",
    rerankModelId: "",
    selectedChatModelId: "",  // 用户当前选择的对话模型ID
  },
  selectedAgentId: BUILTIN_QUICK_ANSWER_ID,  // 默认选中快速问答模式
  selectedAgentSourceTenantId: null as string | null,  // 共享智能体来源租户 ID
};

export const useSettingsStore = defineStore("settings", {
  state: () => ({
    // 从本地存储加载设置，如果没有则使用默认设置
    settings: JSON.parse(localStorage.getItem("WeKnora_settings") || JSON.stringify(defaultSettings)),
  }),

  getters: {
    // Agent 是否启用
    isAgentEnabled: (state) => state.settings.isAgentEnabled || false,
    
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
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 获取选中的智能体ID
    getSelectedAgentId(): string {
      return this.settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID;
    },
  },
});
 