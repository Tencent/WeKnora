import { defineStore } from "pinia";
import { BUILTIN_QUICK_ANSWER_ID, BUILTIN_SMART_REASONING_ID } from "@/api/agent";

// 설정 인터페이스 정의
interface Settings {
  endpoint: string;
  apiKey: string;
  knowledgeBaseId: string;
  isAgentEnabled: boolean;
  agentConfig: AgentConfig;
  selectedKnowledgeBases: string[];  // 현재 선택된 지식 베이스 ID 목록
  selectedFiles: string[]; // 현재 선택된 파일 ID 목록
  modelConfig: ModelConfig;  // 모델 구성
  ollamaConfig: OllamaConfig;  // Ollama 구성
  webSearchEnabled: boolean;  // 웹 검색 활성화 여부
  conversationModels: ConversationModels;
  selectedAgentId: string;  // 현재 선택된 에이전트 ID
}

// Agent 구성 인터페이스
interface AgentConfig {
  maxIterations: number;
  temperature: number;
  allowedTools: string[];
  system_prompt?: string;  // Unified system prompt (uses {{web_search_status}} placeholder)
}

interface ConversationModels {
  summaryModelId: string;
  rerankModelId: string;
  selectedChatModelId: string;  // 사용자가 현재 선택한 대화 모델 ID
}

// 단일 모델 항목 인터페이스
interface ModelItem {
  id: string;  // 고유 ID
  name: string;  // 표시 이름
  source: 'local' | 'remote';  // 모델 출처
  modelName: string;  // 모델 식별자
  baseUrl?: string;  // 원격 API URL
  apiKey?: string;  // 원격 API Key
  dimension?: number;  // Embedding 전용: 벡터 차원
  interfaceType?: 'ollama' | 'openai';  // VLLM 전용: 인터페이스 유형
  isDefault?: boolean;  // 기본 모델 여부
}

// 모델 구성 인터페이스 - 다중 모델 지원
interface ModelConfig {
  chatModels: ModelItem[];
  embeddingModels: ModelItem[];
  rerankModels: ModelItem[];
  vllmModels: ModelItem[];  // VLLM 시각 모델
}

// Ollama 구성 인터페이스
interface OllamaConfig {
  baseUrl: string;  // Ollama 서비스 주소
  enabled: boolean;  // 활성화 여부
}

// 기본 설정
const defaultSettings: Settings = {
  endpoint: import.meta.env.VITE_IS_DOCKER ? "" : "http://localhost:8080",
  apiKey: "",
  knowledgeBaseId: "",
  isAgentEnabled: false,
  agentConfig: {
    maxIterations: 5,
    temperature: 0.7,
    allowedTools: [],  // 기본값은 비어 있음, 백엔드에서 API를 통해 로드해야 함
    system_prompt: "",
  },
  selectedKnowledgeBases: [],  // 기본값은 빈 배열
  selectedFiles: [], // 기본값은 빈 배열
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
  webSearchEnabled: false,  // 기본적으로 웹 검색 비활성화
  conversationModels: {
    summaryModelId: "",
    rerankModelId: "",
    selectedChatModelId: "",  // 사용자가 현재 선택한 대화 모델 ID
  },
  selectedAgentId: BUILTIN_QUICK_ANSWER_ID,  // 기본적으로 빠른 질의응답 모드 선택
};

export const useSettingsStore = defineStore("settings", {
  state: () => ({
    // 로컬 저장소에서 설정을 로드하고, 없으면 기본 설정 사용
    settings: JSON.parse(localStorage.getItem("WeKnora_settings") || JSON.stringify(defaultSettings)),
  }),

  getters: {
    // Agent 활성화 여부
    isAgentEnabled: (state) => state.settings.isAgentEnabled || false,
    
    // Agent 준비 여부 (구성이 완료되었는지)
    // 만족해야 할 조건: 1) 허용된 도구 구성 2) 대화 모델 설정 3) 재정렬 모델 설정
    isAgentReady: (state) => {
      const config = state.settings.agentConfig || defaultSettings.agentConfig
      const models = state.settings.conversationModels || defaultSettings.conversationModels
      return Boolean(
        config.allowedTools && config.allowedTools.length > 0 &&
        models.summaryModelId && models.summaryModelId.trim() !== '' &&
        models.rerankModelId && models.rerankModelId.trim() !== ''
      )
    },
    
    // 일반 모드 (빠른 답변) 준비 여부
    // 만족해야 할 조건: 1) 대화 모델 설정 2) 재정렬 모델 설정
    isNormalModeReady: (state) => {
      const models = state.settings.conversationModels || defaultSettings.conversationModels
      return Boolean(
        models.summaryModelId && models.summaryModelId.trim() !== '' &&
        models.rerankModelId && models.rerankModelId.trim() !== ''
      )
    },
    
    // Agent 구성 가져오기
    agentConfig: (state) => state.settings.agentConfig || defaultSettings.agentConfig,

    conversationModels: (state) => state.settings.conversationModels || defaultSettings.conversationModels,
    
    // 모델 구성 가져오기
    modelConfig: (state) => state.settings.modelConfig || defaultSettings.modelConfig,
    
    // 웹 검색 활성화 여부
    isWebSearchEnabled: (state) => state.settings.webSearchEnabled || false,
    
    // 현재 선택된 에이전트 ID
    selectedAgentId: (state) => state.settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID,
  },

  actions: {
    // 설정 저장
    saveSettings(settings: Settings) {
      this.settings = { ...settings };
      // localStorage에 저장
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    // 설정 가져오기
    getSettings(): Settings {
      return this.settings;
    },

    // API 엔드포인트 가져오기
    getEndpoint(): string {
      return this.settings.endpoint || defaultSettings.endpoint;
    },

    // API Key 가져오기
    getApiKey(): string {
      return this.settings.apiKey;
    },

    // 지식 베이스 ID 가져오기
    getKnowledgeBaseId(): string {
      return this.settings.knowledgeBaseId;
    },
    
    // Agent 활성화/비활성화
    toggleAgent(enabled: boolean) {
      this.settings.isAgentEnabled = enabled;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // Agent 구성 업데이트
    updateAgentConfig(config: Partial<AgentConfig>) {
      this.settings.agentConfig = { ...this.settings.agentConfig, ...config };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    updateConversationModels(models: Partial<ConversationModels>) {
      const current = this.settings.conversationModels || defaultSettings.conversationModels;
      this.settings.conversationModels = { ...current, ...models };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 모델 구성 업데이트
    updateModelConfig(config: Partial<ModelConfig>) {
      this.settings.modelConfig = { ...this.settings.modelConfig, ...config };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 모델 추가
    addModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', model: ModelItem) {
      const key = `${type}Models` as keyof ModelConfig;
      const models = [...this.settings.modelConfig[key]] as ModelItem[];
      // 기본값으로 설정된 경우 다른 모델의 기본값 상태 해제
      if (model.isDefault) {
        models.forEach(m => m.isDefault = false);
      }
      // 첫 번째 모델인 경우 자동으로 기본값 설정
      if (models.length === 0) {
        model.isDefault = true;
      }
      models.push(model);
      this.settings.modelConfig[key] = models as any;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 모델 업데이트
    updateModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', modelId: string, updates: Partial<ModelItem>) {
      const key = `${type}Models` as keyof ModelConfig;
      const models = [...this.settings.modelConfig[key]] as ModelItem[];
      const index = models.findIndex(m => m.id === modelId);
      if (index !== -1) {
        // 기본값으로 설정하려는 경우 다른 모델의 기본값 상태 해제
        if (updates.isDefault) {
          models.forEach(m => m.isDefault = false);
        }
        models[index] = { ...models[index], ...updates };
        this.settings.modelConfig[key] = models as any;
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },
    
    // 모델 삭제
    deleteModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', modelId: string) {
      const key = `${type}Models` as keyof ModelConfig;
      let models = [...this.settings.modelConfig[key]] as ModelItem[];
      const deletedModel = models.find(m => m.id === modelId);
      models = models.filter(m => m.id !== modelId);
      // 삭제된 모델이 기본 모델인 경우 첫 번째 모델을 기본으로 설정
      if (deletedModel?.isDefault && models.length > 0) {
        models[0].isDefault = true;
      }
      this.settings.modelConfig[key] = models as any;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 기본 모델 설정
    setDefaultModel(type: 'chat' | 'embedding' | 'rerank' | 'vllm', modelId: string) {
      const key = `${type}Models` as keyof ModelConfig;
      const models = [...this.settings.modelConfig[key]] as ModelItem[];
      models.forEach(m => m.isDefault = (m.id === modelId));
      this.settings.modelConfig[key] = models as any;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // Ollama 구성 업데이트
    updateOllamaConfig(config: Partial<OllamaConfig>) {
      this.settings.ollamaConfig = { ...this.settings.ollamaConfig, ...config };
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 지식 베이스 선택 (전체 목록 교체)
    selectKnowledgeBases(kbIds: string[]) {
      this.settings.selectedKnowledgeBases = kbIds;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 단일 지식 베이스 추가
    addKnowledgeBase(kbId: string) {
      if (!this.settings.selectedKnowledgeBases.includes(kbId)) {
        this.settings.selectedKnowledgeBases.push(kbId);
        localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
      }
    },
    
    // 단일 지식 베이스 제거
    removeKnowledgeBase(kbId: string) {
      this.settings.selectedKnowledgeBases = 
        this.settings.selectedKnowledgeBases.filter((id: string) => id !== kbId);
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 지식 베이스 선택 지우기
    clearKnowledgeBases() {
      this.settings.selectedKnowledgeBases = [];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 선택된 지식 베이스 목록 가져오기
    getSelectedKnowledgeBases(): string[] {
      return this.settings.selectedKnowledgeBases || [];
    },
    
    // 웹 검색 활성화/비활성화
    toggleWebSearch(enabled: boolean) {
      this.settings.webSearchEnabled = enabled;
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    // 파일 선택 작업
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
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },

    clearFiles() {
      this.settings.selectedFiles = [];
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    getSelectedFiles(): string[] {
      return this.settings.selectedFiles || [];
    },
    
    // 에이전트 선택
    selectAgent(agentId: string) {
      this.settings.selectedAgentId = agentId;
      // 에이전트 유형에 따라 자동으로 Agent 모드 전환
      if (agentId === BUILTIN_QUICK_ANSWER_ID) {
        this.settings.isAgentEnabled = false;
      } else if (agentId === BUILTIN_SMART_REASONING_ID) {
        this.settings.isAgentEnabled = true;
      }
      // 사용자 정의 에이전트는 구성에 따라 결정해야 함
      
      // 에이전트 전환 시 지식 베이스 및 파일 선택 상태 재설정
      // 다른 에이전트는 다른 지식 베이스와 연결되므로 사용자의 이전 선택을 지워야 함
      this.settings.selectedKnowledgeBases = [];
      this.settings.selectedFiles = [];
      
      localStorage.setItem("WeKnora_settings", JSON.stringify(this.settings));
    },
    
    // 선택된 에이전트 ID 가져오기
    getSelectedAgentId(): string {
      return this.settings.selectedAgentId || BUILTIN_QUICK_ANSWER_ID;
    },
  },
});
