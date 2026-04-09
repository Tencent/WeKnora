import { get, post, put } from '../../utils/request';
import i18n from '@/i18n'

const t = (key: string) => i18n.global.t(key)

// 初始化配置数据类型
export interface InitializationConfig {
    llm: {
        source: string;
        modelName: string;
        baseUrl?: string;
        apiKey?: string;
    };
    embedding: {
        source: string;
        modelName: string;
        baseUrl?: string;
        apiKey?: string;
        dimension?: number; // 添加embedding维度字段
    };
    rerank: {
        modelName: string;
        baseUrl: string;
        apiKey?: string;
        enabled: boolean;
    };
    multimodal: {
        enabled: boolean;
        storageType: 'cos' | 'minio';
        vlm?: {
            modelName: string;
            baseUrl: string;
            apiKey?: string;
            interfaceType?: string; // "ollama" or "openai"
        };
        cos?: {
            secretId: string;
            secretKey: string;
            region: string;
            bucketName: string;
            appId: string;
            pathPrefix?: string;
        };
        minio?: {
            bucketName: string;
            pathPrefix?: string;
        };
    };
    documentSplitting: {
        chunkSize: number;
        chunkOverlap: number;
        separators: string[];
    };
    // Frontend-only hint for storage selection UI
    storageType?: 'cos' | 'minio';
    nodeExtract: {
        enabled: boolean,
        text: string,
        tags: string[],
        nodes: Node[],
        relations: Relation[]
    }
}

// 下载任务状态类型
export interface DownloadTask {
    id: string;
    modelName: string;
    status: 'pending' | 'downloading' | 'completed' | 'failed';
    progress: number;
    message: string;
    startTime: string;
    endTime?: string;
}

// 简化版知识库配置更新接口（只传模型ID）
export interface KBModelConfigRequest {
    llmModelId: string
    embeddingModelId: string
    vlm_config?: {
        enabled: boolean
        model_id?: string
    }
    asr_config?: {
        enabled: boolean
        model_id?: string
        language?: string
    }
    documentSplitting: {
        chunkSize: number
        chunkOverlap: number
        separators: string[]
        parserEngineRules?: { file_types: string[]; engine: string }[]
        enableParentChild?: boolean
        parentChunkSize?: number
        childChunkSize?: number
    }
    multimodal: {
        enabled: boolean
    }
    /** 存储引擎选择："local" | "minio" | "cos"，影响文档上传与文档内图片存储 */
    storageProvider?: string
    nodeExtract: {
        enabled: boolean
        text: string
        tags: string[]
        nodes: Node[]
        relations: Relation[]
    }
    questionGeneration?: {
        enabled: boolean
        questionCount: number
    }
}

export function updateKBConfig(kbId: string, config: KBModelConfigRequest): Promise<any> {
    return new Promise((resolve, reject) => {
        console.log('Starting KB config update (simplified)...', kbId, config);
        put(`/api/v1/initialization/config/${kbId}`, config)
            .then((response: any) => {
                console.log('KB config update completed', response);
                resolve(response);
            })
            .catch((error: any) => {
                console.error('Failed to update KB config:', error);
                reject(error.error || error);
            });
    });
}

// 根据知识库ID执行配置更新（旧版，保留兼容性）
export function initializeSystemByKB(kbId: string, config: InitializationConfig): Promise<any> {
    return new Promise((resolve, reject) => {
        console.log('Starting KB config update...', kbId, config);
        post(`/api/v1/initialization/initialize/${kbId}`, config)
            .then((response: any) => {
                console.log('KB config update completed', response);
                resolve(response);
            })
            .catch((error: any) => {
                console.error('Failed to update KB config:', error);
                reject(error.error || error);
            });
    });
}

// 检查Ollama服务状态
export function checkOllamaStatus(): Promise<{ available: boolean; version?: string; error?: string; baseUrl?: string }> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/status')
            .then((response: any) => {
                resolve(response.data || { available: false });
            })
            .catch((error: any) => {
                console.error('Failed to check Ollama status:', error);
                resolve({ available: false, error: error.message || t('error.initialization.checkFailed') });
            });
    });
}

// Ollama 模型详细信息接口
export interface OllamaModelInfo {
    name: string;
    size: number;
    digest: string;
    modified_at: string;
}

// 列出已安装的 Ollama 模型（详细信息）
export function listOllamaModels(): Promise<OllamaModelInfo[]> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/models')
            .then((response: any) => {
                resolve((response.data && response.data.models) || []);
            })
            .catch((error: any) => {
                console.error('Failed to list Ollama models:', error);
                resolve([]);
            });
    });
}

// 检查Ollama模型状态
export function checkOllamaModels(models: string[]): Promise<{ models: Record<string, boolean> }> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/ollama/models/check', { models })
            .then((response: any) => {
                resolve(response.data || { models: {} });
            })
            .catch((error: any) => {
                console.error('Failed to check Ollama models:', error);
                reject(error);
            });
    });
}

// 启动Ollama模型下载（异步）
export function downloadOllamaModel(modelName: string): Promise<{ taskId: string; modelName: string; status: string; progress: number }> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/ollama/models/download', { modelName })
            .then((response: any) => {
                resolve(response.data || { taskId: '', modelName, status: 'failed', progress: 0 });
            })
            .catch((error: any) => {
                console.error('Failed to start Ollama model download:', error);
                reject(error);
            });
    });
}

// 查询下载进度
export function getDownloadProgress(taskId: string): Promise<DownloadTask> {
    return new Promise((resolve, reject) => {
        get(`/api/v1/initialization/ollama/download/progress/${taskId}`)
            .then((response: any) => {
                resolve(response.data);
            })
            .catch((error: any) => {
                console.error('Failed to get download progress:', error);
                reject(error);
            });
    });
}

// 获取所有下载任务
export function listDownloadTasks(): Promise<DownloadTask[]> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/download/tasks')
            .then((response: any) => {
                resolve(response.data || []);
            })
            .catch((error: any) => {
                console.error('Failed to list download tasks:', error);
                reject(error);
            });
    });
}


export function getCurrentConfigByKB(kbId: string): Promise<InitializationConfig & { hasFiles: boolean }> {
    return new Promise((resolve, reject) => {
        get(`/api/v1/initialization/config/${kbId}`)
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('Failed to get KB config:', error);
                reject(error);
            });
    });
}

// 检查远程API模型
export function checkRemoteModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
    provider?: string;
}): Promise<{
    available: boolean;
    message?: string;
}> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/remote/check', modelConfig)
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('Failed to check remote model:', error);
                reject(error);
            });
    });
}

// 测试 Embedding 模型（本地/远程）是否可用
export function testEmbeddingModel(modelConfig: {
    source: 'local' | 'remote';
    modelName: string;
    baseUrl?: string;
    apiKey?: string;
    dimension?: number;
    provider?: string;
}): Promise<{ available: boolean; message?: string; dimension?: number }> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/embedding/test', modelConfig)
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('Failed to test Embedding model:', error);
                reject(error);
            });
    });
}


export function checkRerankModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
}): Promise<{
    available: boolean;
    message?: string;
}> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/rerank/check', modelConfig)
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('Failed to check Rerank model:', error);
                reject(error);
            });
    });
}

// 检查 ASR 模型连接（通过 /v1/audio/transcriptions 端点测试）
export function checkASRModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
}): Promise<{
    available: boolean;
    message?: string;
}> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/asr/check', modelConfig)
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('Failed to check ASR model:', error);
                reject(error);
            });
    });
}

export function testMultimodalFunction(testData: {
    image: File;
    vlm_model: string;
    vlm_base_url: string;
    vlm_api_key?: string;
    vlm_interface_type?: string;
    storage_type?: 'cos' | 'minio';
    // COS optional fields (required only when storage_type === 'cos')
    cos_secret_id?: string;
    cos_secret_key?: string;
    cos_region?: string;
    cos_bucket_name?: string;
    cos_app_id?: string;
    cos_path_prefix?: string;
    // MinIO optional fields
    minio_bucket_name?: string;
    minio_path_prefix?: string;
    chunk_size: number;
    chunk_overlap: number;
    separators: string[];
}): Promise<{
    success: boolean;
    caption?: string;
    ocr?: string;
    processing_time?: number;
    message?: string;
}> {
    return new Promise((resolve, reject) => {
        const formData = new FormData();
        formData.append('image', testData.image);
        formData.append('vlm_model', testData.vlm_model);
        formData.append('vlm_base_url', testData.vlm_base_url);
        if (testData.vlm_api_key) {
            formData.append('vlm_api_key', testData.vlm_api_key);
        }
        if (testData.vlm_interface_type) {
            formData.append('vlm_interface_type', testData.vlm_interface_type);
        }
        if (testData.storage_type) {
            formData.append('storage_type', testData.storage_type);
        }
        // Append COS fields only when storage_type is COS
        if (testData.storage_type === 'cos') {
            if (testData.cos_secret_id) formData.append('cos_secret_id', testData.cos_secret_id);
            if (testData.cos_secret_key) formData.append('cos_secret_key', testData.cos_secret_key);
            if (testData.cos_region) formData.append('cos_region', testData.cos_region);
            if (testData.cos_bucket_name) formData.append('cos_bucket_name', testData.cos_bucket_name);
            if (testData.cos_app_id) formData.append('cos_app_id', testData.cos_app_id);
            if (testData.cos_path_prefix) formData.append('cos_path_prefix', testData.cos_path_prefix);
        }
        // MinIO fields
        if (testData.minio_bucket_name) formData.append('minio_bucket_name', testData.minio_bucket_name);
        if (testData.minio_path_prefix) formData.append('minio_path_prefix', testData.minio_path_prefix);
        formData.append('chunk_size', testData.chunk_size.toString());
        formData.append('chunk_overlap', testData.chunk_overlap.toString());
        formData.append('separators', JSON.stringify(testData.separators));

        // 获取鉴权Token
        const token = localStorage.getItem('weknora_token');
        const headers: Record<string, string> = {};
        if (token) {
            headers['Authorization'] = `Bearer ${token}`;
        }

        // 添加跨租户访问请求头（如果选择了其他租户）
        const selectedTenantId = localStorage.getItem('weknora_selected_tenant_id');
        const defaultTenantId = localStorage.getItem('weknora_tenant');
        if (selectedTenantId) {
            try {
                const defaultTenant = defaultTenantId ? JSON.parse(defaultTenantId) : null;
                const defaultId = defaultTenant?.id ? String(defaultTenant.id) : null;
                if (selectedTenantId !== defaultId) {
                    headers['X-Tenant-ID'] = selectedTenantId;
                }
            } catch (e) {
                console.error('Failed to parse tenant info', e);
            }
        }

        // 使用原生fetch因为需要发送FormData
        fetch('/api/v1/initialization/multimodal/test', {
            method: 'POST',
            headers,
            body: formData
        })
            .then(response => response.json())
            .then((data: any) => {
                if (data.success) {
                    resolve(data.data || {});
                } else {
                    resolve({ success: false, message: data.message || t('error.initialization.testFailed') });
                }
            })
            .catch((error: any) => {
                console.error('Failed multimodal test:', error);
                reject(error);
            });
    });
}

// 文本内容关系提取接口
export interface TextRelationExtractionRequest {
    text: string;
    tags: string[];
    model_id: string;
}

export interface Node {
    name: string;
    attributes: string[];
}

export interface Relation {
    node1: string;
    node2: string;
    type: string;
}

export interface TextRelationExtractionResponse {
    nodes: Node[];
    relations: Relation[];
}

// 文本内容关系提取
export function extractTextRelations(request: TextRelationExtractionRequest): Promise<TextRelationExtractionResponse> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/extract/text-relation', request, { timeout: 60000 })
            .then((response: any) => {
                resolve(response.data || { nodes: [], relations: [] });
            })
            .catch((error: any) => {
                console.error('Failed to extract text relations:', error);
                reject(error);
            });
    });
}

export interface FabriTextRequest {
    tags: string[];
    model_id: string;
}

export interface FabriTextResponse {
    text: string;
}

// 文本内容生成
export function fabriText(request: FabriTextRequest): Promise<FabriTextResponse> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/extract/fabri-text', request)
            .then((response: any) => {
                resolve(response.data || { text: '' });
            })
            .catch((error: any) => {
                console.error('Failed to generate text:', error);
                reject(error);
            });
    });
}

export interface FabriTagRequest {
}

export interface FabriTagResponse {
    tags: string[];
}

// 标签生成
export function fabriTag(request: FabriTagRequest): Promise<FabriTagResponse> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/extract/fabri-tag', request)
            .then((response: any) => {
                resolve(response.data || { tags: [] as string[] });
            })
            .catch((error: any) => {
                console.error('Failed to generate tags:', error);
                reject(error);
            });
    });
}

// 模型厂商信息类型
export interface ModelProviderOption {
    value: string;        // provider 标识符
    label: string;        // 显示名称
    description: string;  // 描述
    defaultUrls: Record<string, string>;  // 按模型类型区分的默认 URL
    modelTypes: string[]; // 支持的模型类型
}

// 获取模型厂商列表
export function listModelProviders(modelType?: string): Promise<ModelProviderOption[]> {
    return new Promise((resolve, reject) => {
        const url = modelType
            ? `/api/v1/models/providers?model_type=${encodeURIComponent(modelType)}`
            : '/api/v1/models/providers';
        get(url)
            .then((response: any) => {
                resolve(response.data || []);
            })
            .catch((error: any) => {
                console.error('Failed to list model providers:', error);
                resolve([]); // 失败时返回空数组，前端可以回退到默认值
            });
    });
}