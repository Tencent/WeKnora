import { get, post, put, del } from '../../utils/request';
import i18n from '@/i18n'

const t = (key: string) => i18n.global.t(key)

// 模型类型定义
export interface ModelConfig {
  id?: string;
  tenant_id?: number;
  name: string;
  type: 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM' | 'ASR';
  source: 'local' | 'remote';
  description?: string;
  parameters: {
    base_url?: string;
    api_key?: string;
    provider?: string; // Provider identifier: openai, aliyun, zhipu, generic
    embedding_parameters?: {
      dimension?: number;
      truncate_prompt_tokens?: number;
    };
    interface_type?: 'ollama' | 'openai'; // VLLM专用
    parameter_size?: string; // Ollama模型参数大小 (e.g., "7B", "13B", "70B")
    extra_config?: Record<string, string>; // Provider-specific configuration
    supports_vision?: boolean; // Whether the model accepts image/multimodal input
  };
  is_default?: boolean;
  is_builtin?: boolean;
  status?: string;
  created_at?: string;
  updated_at?: string;
  deleted_at?: string | null;
}

// 创建模型
export function createModel(data: ModelConfig): Promise<ModelConfig> {
  return new Promise((resolve, reject) => {
    post('/api/v1/models', data)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          reject(new Error(response.message || t('error.model.createFailed')));
        }
      })
      .catch((error: any) => {
        console.error('Failed to create model:', error);
        reject(error);
      });
  });
}

// 获取模型列表
export function listModels(type?: string): Promise<ModelConfig[]> {
  return new Promise((resolve, reject) => {
    const url = `/api/v1/models`;
    get(url)
      .then((response: any) => {
        if (response.success && response.data) {
          if (type) {
            response.data = response.data.filter((item: ModelConfig) => item.type === type);
          }
          resolve(response.data);
        } else {
          resolve([]);
        }
      })
      .catch((error: any) => {
        console.error('Failed to list models:', error);
        resolve([]);
      });
  });
}

// 获取单个模型
export function getModel(id: string): Promise<ModelConfig> {
  return new Promise((resolve, reject) => {
    get(`/api/v1/models/${id}`)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          reject(new Error(response.message || t('error.model.getFailed')));
        }
      })
      .catch((error: any) => {
        console.error('Failed to get model:', error);
        reject(error);
      });
  });
}

// 更新模型
export function updateModel(id: string, data: Partial<ModelConfig>): Promise<ModelConfig> {
  return new Promise((resolve, reject) => {
    put(`/api/v1/models/${id}`, data)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          reject(new Error(response.message || t('error.model.updateFailed')));
        }
      })
      .catch((error: any) => {
        console.error('Failed to update model:', error);
        reject(error);
      });
  });
}

// 删除模型
export function deleteModel(id: string): Promise<void> {
  return new Promise((resolve, reject) => {
    del(`/api/v1/models/${id}`)
      .then((response: any) => {
        if (response.success) {
          resolve();
        } else {
          reject(new Error(response.message || t('error.model.deleteFailed')));
        }
      })
      .catch((error: any) => {
        console.error('Failed to delete model:', error);
        reject(error);
      });
  });
}

