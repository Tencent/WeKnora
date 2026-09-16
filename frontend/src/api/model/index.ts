import { get, post, postUpload, put, del } from '../../utils/request';
import i18n from '@/i18n'
import { ModelInUseError, modelInUseErrorFromRequest } from './modelUsage'

export * from './modelUsage'

const t = (key: string) => i18n.global.t(key)

// 模型类型定义
export interface ModelConfig {
  id?: string;
  tenant_id?: number;
  name: string;
  display_name?: string;
  type: 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'ASR';
  source: 'local' | 'remote';
  description?: string;
  parameters: {
    base_url?: string;
    api_key?: string;
    provider?: string; // Provider identifier: openai, aliyun, zhipu, generic
    embedding_parameters?: {
      dimension?: number;
      truncate_prompt_tokens?: number;
      supports_dimension_override?: boolean;
    };
    interface_type?: 'ollama' | 'openai'; // Ollama 专用
    parameter_size?: string; // Ollama模型参数大小 (e.g., "7B", "13B", "70B")
    extra_config?: Record<string, string>; // Provider-specific configuration
    // 自定义 HTTP 请求头（类似 Python OpenAI SDK 的 extra_headers），
    // 会在调用远程模型 API 时附加到每个请求上。Authorization、Content-Type 等保留头会被忽略。
    custom_headers?: Record<string, string>;
    supports_vision?: boolean; // Deprecated: use chat.input_modalities (migration window dual-read)
    // 对话模型的上下文窗口（token）。0 或不填表示使用后端默认 200000。
    context_window?: number;
    max_output_tokens?: number;
    // Chat 分片（对话模型专用）：思考档位、输入模态等新参数。
    // 后端读侧优先分片、回落顶层扁平字段（迁移期双读）。
    chat?: {
      context_window?: number;
      max_output_tokens?: number;
      thinking_enabled?: boolean;
      thinking_level?: string;
      selected_levels?: string[];
      parallel_tool_calls?: boolean;
      input_modalities?: string[];
    };
    // 后台任务（入库/富化）对该模型的并发上限，按模型 ID 全副本共享。
    // 0 或不填表示沿用全局默认（model.max_concurrency）；仅对 chat/embedding 生效。
    max_concurrency?: number;
    app_id?: string;
    // Secret fields (api_key, app_secret) are never returned by the server in
    // this shape — they live behind the /credentials subresource. They are
    // kept on the type so create-mode payloads can still carry them in the
    // initial POST body.
    app_secret?: string;
  };
  is_default?: boolean;
  is_builtin?: boolean;
  status?: string;
  // Per-field configured? metadata from the main response. For builtin
  // models it is returned only to system administrators.
  // 后端只构造 api_key/app_secret 两键（app_id 非密钥不进此 map）
  credentials?: Partial<Record<ModelCredentialField, { configured: boolean }>>;
  created_at?: string;
  updated_at?: string;
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
    // 服务端类型过滤（2026-09-14 裁定 #14）：?type= 下推 repo，不再全量拉取
    const url = type ? `/api/v1/models?type=${encodeURIComponent(type)}` : `/api/v1/models`;
    get(url)
      .then((response: any) => {
        if (response.success && response.data) {
          resolve(response.data);
        } else {
          resolve([]);
        }
      })
      .catch((error: any) => {
        console.error('Failed to list models:', error);
        // 抛出而非吞掉：调用方（含缓存层）才能区分「真失败」与「成功但无模型」，
        // 避免把一次瞬时失败的空结果缓存下来。各 UI 调用点均已 try/catch 兜底。
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
          const conflict = modelInUseErrorFromRequest(response)
          if (conflict) {
            reject(conflict)
            return
          }
          reject(new Error(response.message || t('error.model.deleteFailed')));
        }
      })
      .catch((error: any) => {
        console.error('Failed to delete model:', error);
        if (error instanceof ModelInUseError) {
          reject(error)
          return
        }
        const conflict = modelInUseErrorFromRequest(error)
        if (conflict) {
          reject(conflict)
          return
        }
        reject(error);
      });
  });
}

export interface ModelDebugOptions {
  system_prompt?: string
  temperature?: number
  top_p?: number
  max_tokens?: number
  thinking?: boolean
}

export interface ModelDebugResult {
  ok: boolean
  elapsed_ms: number
  request: Record<string, unknown>
  raw_response: unknown
  observations: Record<string, unknown>
  error?: string
}

export async function debugModel(
  id: string,
  data: {
    input?: string
    documents?: string[]
    options?: ModelDebugOptions
    file?: File | null
  },
): Promise<ModelDebugResult> {
  const form = new FormData()
  form.append('input', data.input || '')
  form.append('documents', JSON.stringify(data.documents || []))
  form.append('options', JSON.stringify(data.options || {}))
  if (data.file) form.append('file', data.file)
  const response: any = await postUpload(
    `/api/v1/models/${id}/debug`,
    form,
    undefined,
    { timeout: 300000 },
  )
  if (response?.success && response?.data) return response.data
  throw new Error(response?.message || t('error.model.getFailed'))
}

// ----------------------------------------------------------------------------
// Model credential subresource. See mcp-service.ts for the matching MCP API
// shape and the design notes in internal/handler/dto/mcp.go.
// ----------------------------------------------------------------------------

export type ModelCredentialField = 'api_key' | 'app_id' | 'app_secret'

export interface ModelCredentialsResponse {
  fields: Partial<Record<ModelCredentialField, { configured: boolean }>>
}

export async function putModelCredentials(
  id: string,
  body: Partial<Record<ModelCredentialField, string>>,
): Promise<ModelCredentialsResponse> {
  const response: any = await put(`/api/v1/models/${id}/credentials`, body)
  return (response.data ?? response) as ModelCredentialsResponse
}

export async function deleteModelCredentialField(
  id: string,
  field: ModelCredentialField,
): Promise<void> {
  await del(`/api/v1/models/${id}/credentials/${field}`)
}

export interface InitializeWeKnoraCloudRequest {
  app_id: string
  app_secret: string
}

// 仅保存 WeKnoraCloud 凭证，不自动创建模型
export function saveWeKnoraCloudCredentials(data: InitializeWeKnoraCloudRequest): Promise<{ success: boolean; message: string }> {
  return new Promise((resolve, reject) => {
    post('/api/v1/weknoracloud/credentials', data)
      .then((response: any) => {
        if (response.success) {
          resolve(response)
        } else {
          reject(new Error(response.message || response.error || '凭证保存失败'))
        }
      })
      .catch((error: any) => {
        console.error('Failed to save WeKnoraCloud credentials:', error)
        reject(error)
      })
  })
}

export interface WeKnoraCloudStatusResult {
  has_models: boolean
  needs_reinit: boolean
  reason?: string
}

export function getWeKnoraCloudStatus(): Promise<WeKnoraCloudStatusResult> {
  return new Promise((resolve, reject) => {
    get('/api/v1/models/weknoracloud/status')
      .then((response: any) => {
        // status 接口直接返回对象，不包在 success/data 中
        if (response && typeof response.has_models === 'boolean') {
          resolve(response)
        } else if (response?.success && response?.data) {
          resolve(response.data)
        } else {
          resolve({ has_models: false, needs_reinit: false })
        }
      })
      .catch(() => {
        resolve({ has_models: false, needs_reinit: false })
      })
  })
}

// ----------------------------------------------------------------------------
// 模型目录与远端列表（design §5.10）。取值链：接口元数据 → models.json 目录 → 留空。
// ----------------------------------------------------------------------------

// 厂商列表接口返回的模型条目（OpenAI /models 形状）。
export interface RemoteCatalogModel {
  id: string;
  display_name?: string;
  owned_by?: string;
  // 接口元数据（取值链第 1 级）——后端已摊平为扁平字段（invoke.RemoteModel，
  // v1 的恒空 meta 信封已删除）。多数 OpenAI 兼容列表只有 id，其余为空。
  context_window?: number;
  max_output_tokens?: number;
  modalities?: string[];
  thinking_levels?: string[];
  /** @deprecated 后端 meta 信封已删除，恒 undefined，勿再读取 */
  meta?: unknown;
}

// 后端代理探测厂商模型列表（密钥不出服务端）。失败降级 available:false，不阻断创建。
interface CatalogEnvelope<T> { success: boolean; data: T }

export async function probeRemoteCatalog(
  body: { provider: string; base_url?: string; api_key?: string; model_id?: string; model_type?: string },
): Promise<{ available: boolean; models?: RemoteCatalogModel[]; reason?: string }> {
  const response = await post<CatalogEnvelope<{ available: boolean; models?: RemoteCatalogModel[]; reason?: string }>>(
    '/api/v1/models/remote-catalog',
    body,
  )
  return response?.data ?? { available: false }
}

// models.json 目录条目（WeKnora 自有 schema，design §5.4）。
export interface CatalogModelEntry {
  context_window?: number;
  max_output_tokens?: number;
  input_modalities?: string[];
  thinking?: {
    supported: boolean;
    can_disable: boolean;
    levels?: string[];
    default_level?: string;
  };
}

// 内置 models.json 目录（按 provider 过滤）。目录不可用只降级预填，不报错。
export async function fetchModelCatalog(
  provider: string,
): Promise<{ available: boolean; version?: string; providers?: Record<string, { models: Record<string, CatalogModelEntry> }> }> {
  const response = await get<CatalogEnvelope<{ available: boolean; version?: string; providers?: Record<string, { models: Record<string, CatalogModelEntry> }> }>>(
    `/api/v1/models/catalog?provider=${encodeURIComponent(provider)}`,
  )
  return response?.data ?? { available: false }
}
