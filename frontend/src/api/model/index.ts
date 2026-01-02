import { get, post, put, del } from '../../utils/request';

// Model type definition
export interface ModelConfig {
  id?: string;
  tenant_id?: number;
  name: string;
  type: 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM';
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
    interface_type?: 'ollama' | 'openai'; // VLLM specific
    parameter_size?: string; // Ollama model parameter size (e.g., "7B", "13B", "70B")
    extra_config?: Record<string, string>; // Provider-specific configuration
  };
  is_default?: boolean;
  is_builtin?: boolean;
  status?: string;
  created_at?: string;
  updated_at?: string;
  deleted_at?: string | null;
}

// Create model
export function createModel(data: ModelConfig): Promise<ModelConfig> {
  return new Promise((resolve, reject) => {
    post('/api/v1/models', data)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          reject(new Error(response.message || '모델 생성 실패'));
        }
      })
      .catch((error: any) => {
        console.error('모델 생성 실패:', error);
        reject(error);
      });
  });
}

// Get model list
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
        console.error('모델 목록 가져오기 실패:', error);
        resolve([]);
      });
  });
}

// Get single model
export function getModel(id: string): Promise<ModelConfig> {
  return new Promise((resolve, reject) => {
    get(`/api/v1/models/${id}`)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          reject(new Error(response.message || '모델 가져오기 실패'));
        }
      })
      .catch((error: any) => {
        console.error('모델 가져오기 실패:', error);
        reject(error);
      });
  });
}

// Update model
export function updateModel(id: string, data: Partial<ModelConfig>): Promise<ModelConfig> {
  return new Promise((resolve, reject) => {
    put(`/api/v1/models/${id}`, data)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          reject(new Error(response.message || '모델 업데이트 실패'));
        }
      })
      .catch((error: any) => {
        console.error('모델 업데이트 실패:', error);
        reject(error);
      });
  });
}

// Delete model
export function deleteModel(id: string): Promise<void> {
  return new Promise((resolve, reject) => {
    del(`/api/v1/models/${id}`)
      .then((response: any) => {
        if (response.success) {
          resolve();
        } else {
          reject(new Error(response.message || '모델 삭제 실패'));
        }
      })
      .catch((error: any) => {
        console.error('모델 삭제 실패:', error);
        reject(error);
      });
  });
}
