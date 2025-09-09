import { get, post } from '../../utils/request';

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

// 系统初始化状态检查
export function checkInitializationStatus(): Promise<{ initialized: boolean }> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/status')
            .then((response: any) => {
                resolve(response.data || { initialized: false });
            })
            .catch((error: any) => {
                console.warn('检查初始化状态失败，假设需要初始化:', error);
                resolve({ initialized: false });
            });
    });
}

// 执行系统初始化
export function initializeSystem(config: InitializationConfig): Promise<any> {
    return new Promise((resolve, reject) => {
        console.log('开始系统初始化...', config);
        post('/api/v1/initialization/initialize', config)
            .then((response: any) => {
                console.log('系统初始化完成', response);
                // 设置本地初始化状态标记
                localStorage.setItem('system_initialized', 'true');
                resolve(response);
            })
            .catch((error: any) => {
                console.error('系统初始化失败:', error);
                reject(error);
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
                console.error('检查Ollama状态失败:', error);
                resolve({ available: false, error: error.message || '检查失败' });
            });
    });
}

// 列出已安装的 Ollama 模型
export function listOllamaModels(): Promise<string[]> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/models')
            .then((response: any) => {
                resolve((response.data && response.data.models) || []);
            })
            .catch((error: any) => {
                console.error('获取 Ollama 模型列表失败:', error);
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
                console.error('检查Ollama模型状态失败:', error);
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
                console.error('启动Ollama模型下载失败:', error);
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
                console.error('查询下载进度失败:', error);
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
                console.error('获取下载任务列表失败:', error);
                reject(error);
            });
    });
}

// 获取当前系统配置
export function getCurrentConfig(): Promise<InitializationConfig & { hasFiles: boolean }> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/config')
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('获取当前配置失败:', error);
                reject(error);
            });
    });
}

// 检查远程API模型
export function checkRemoteModel(modelConfig: {
    modelName: string;
    baseUrl: string;
    apiKey?: string;
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
                console.error('检查远程模型失败:', error);
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
}): Promise<{ available: boolean; message?: string; dimension?: number }> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/embedding/test', modelConfig)
            .then((response: any) => {
                resolve(response.data || {});
            })
            .catch((error: any) => {
                console.error('测试Embedding模型失败:', error);
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
                console.error('检查Rerank模型失败:', error);
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
    storage_type?: 'cos'|'minio';
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

        // 使用原生fetch因为需要发送FormData
        fetch('/api/v1/initialization/multimodal/test', {
            method: 'POST',
            body: formData
        })
        .then(response => response.json())
        .then((data: any) => {
            if (data.success) {
                resolve(data.data || {});
            } else {
                resolve({ success: false, message: data.message || '测试失败' });
            }
        })
        .catch((error: any) => {
            console.error('多模态测试失败:', error);
            reject(error);
        });
    });
} 