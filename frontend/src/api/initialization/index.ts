import { get, post, put } from '../../utils/request';

// 초기화 구성 데이터 유형
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
        dimension?: number; // 임베딩 차원 필드 추가
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

// 다운로드 작업 상태 유형
export interface DownloadTask {
    id: string;
    modelName: string;
    status: 'pending' | 'downloading' | 'completed' | 'failed';
    progress: number;
    message: string;
    startTime: string;
    endTime?: string;
}

// 간소화된 지식 베이스 구성 업데이트 인터페이스 (모델 ID만 전달)
export interface KBModelConfigRequest {
    llmModelId: string
    embeddingModelId: string
    vlm_config?: {
        enabled: boolean
        model_id?: string
    }
    documentSplitting: {
        chunkSize: number
        chunkOverlap: number
        separators: string[]
    }
    multimodal: {
        enabled: boolean
        storageType?: 'cos' | 'minio'
        cos?: {
            secretId: string
            secretKey: string
            region: string
            bucketName: string
            appId: string
            pathPrefix: string
        }
        minio?: {
            bucketName: string
            useSSL: boolean
            pathPrefix: string
        }
    }
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
        console.log('지식 베이스 구성 업데이트 시작 (간소화 버전)...', kbId, config);
        put(`/api/v1/initialization/config/${kbId}`, config)
            .then((response: any) => {
                console.log('지식 베이스 구성 업데이트 완료', response);
                resolve(response);
            })
            .catch((error: any) => {
                console.error('지식 베이스 구성 업데이트 실패:', error);
                reject(error.error || error);
            });
    });
}

// 지식 베이스 ID에 따라 구성 업데이트 실행 (이전 버전, 호환성 유지)
export function initializeSystemByKB(kbId: string, config: InitializationConfig): Promise<any> {
    return new Promise((resolve, reject) => {
        console.log('지식 베이스 구성 업데이트 시작...', kbId, config);
        post(`/api/v1/initialization/initialize/${kbId}`, config)
            .then((response: any) => {
                console.log('지식 베이스 구성 업데이트 완료', response);
                resolve(response);
            })
            .catch((error: any) => {
                console.error('지식 베이스 구성 업데이트 실패:', error);
                reject(error.error || error);
            });
    });
}

// Ollama 서비스 상태 확인
export function checkOllamaStatus(): Promise<{ available: boolean; version?: string; error?: string; baseUrl?: string }> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/status')
            .then((response: any) => {
                resolve(response.data || { available: false });
            })
            .catch((error: any) => {
                console.error('Ollama 상태 확인 실패:', error);
                resolve({ available: false, error: error.message || '확인 실패' });
            });
    });
}

// Ollama 모델 상세 정보 인터페이스
export interface OllamaModelInfo {
    name: string;
    size: number;
    digest: string;
    modified_at: string;
}

// 설치된 Ollama 모델 목록 (상세 정보)
export function listOllamaModels(): Promise<OllamaModelInfo[]> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/models')
            .then((response: any) => {
                resolve((response.data && response.data.models) || []);
            })
            .catch((error: any) => {
                console.error('Ollama 모델 목록 가져오기 실패:', error);
                resolve([]);
            });
    });
}

// Ollama 모델 상태 확인
export function checkOllamaModels(models: string[]): Promise<{ models: Record<string, boolean> }> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/ollama/models/check', { models })
            .then((response: any) => {
                resolve(response.data || { models: {} });
            })
            .catch((error: any) => {
                console.error('Ollama 모델 상태 확인 실패:', error);
                reject(error);
            });
    });
}

// Ollama 모델 다운로드 시작 (비동기)
export function downloadOllamaModel(modelName: string): Promise<{ taskId: string; modelName: string; status: string; progress: number }> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/ollama/models/download', { modelName })
            .then((response: any) => {
                resolve(response.data || { taskId: '', modelName, status: 'failed', progress: 0 });
            })
            .catch((error: any) => {
                console.error('Ollama 모델 다운로드 시작 실패:', error);
                reject(error);
            });
    });
}

// 다운로드 진행 상황 조회
export function getDownloadProgress(taskId: string): Promise<DownloadTask> {
    return new Promise((resolve, reject) => {
        get(`/api/v1/initialization/ollama/download/progress/${taskId}`)
            .then((response: any) => {
                resolve(response.data);
            })
            .catch((error: any) => {
                console.error('다운로드 진행 상황 조회 실패:', error);
                reject(error);
            });
    });
}

// 모든 다운로드 작업 가져오기
export function listDownloadTasks(): Promise<DownloadTask[]> {
    return new Promise((resolve, reject) => {
        get('/api/v1/initialization/ollama/download/tasks')
            .then((response: any) => {
                resolve(response.data || []);
            })
            .catch((error: any) => {
                console.error('다운로드 작업 목록 가져오기 실패:', error);
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
                console.error('지식 베이스 구성 가져오기 실패:', error);
                reject(error);
            });
    });
}

// 원격 API 모델 확인
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
                console.error('원격 모델 확인 실패:', error);
                reject(error);
            });
    });
}

// 임베딩 모델(로컬/원격) 사용 가능 여부 테스트
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
                console.error('임베딩 모델 테스트 실패:', error);
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
                console.error('Rerank 모델 확인 실패:', error);
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

        // 인증 토큰 가져오기
        const token = localStorage.getItem('weknora_token');
        const headers: Record<string, string> = {};
        if (token) {
            headers['Authorization'] = `Bearer ${token}`;
        }

        // 테넌트 간 액세스 요청 헤더 추가 (다른 테넌트를 선택한 경우)
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
                console.error('테넌트 정보 파싱 실패', e);
            }
        }

        // FormData를 보내야 하므로 네이티브 fetch 사용
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
                    resolve({ success: false, message: data.message || '테스트 실패' });
                }
            })
            .catch((error: any) => {
                console.error('멀티모달 테스트 실패:', error);
                reject(error);
            });
    });
}

// 텍스트 내용 관계 추출 인터페이스
export interface TextRelationExtractionRequest {
    text: string;
    tags: string[];
    llm_config: LLMConfig;
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

export interface LLMConfig {
    source: 'local' | 'remote';
    model_name: string;
    base_url: string;
    api_key: string;
}

export interface TextRelationExtractionResponse {
    nodes: Node[];
    relations: Relation[];
}

// 텍스트 내용 관계 추출
export function extractTextRelations(request: TextRelationExtractionRequest): Promise<TextRelationExtractionResponse> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/extract/text-relation', request, { timeout: 60000 })
            .then((response: any) => {
                resolve(response.data || { nodes: [], relations: [] });
            })
            .catch((error: any) => {
                console.error('텍스트 내용 관계 추출 실패:', error);
                reject(error);
            });
    });
}

export interface FabriTextRequest {
    tags: string[];
    llm_config: LLMConfig;
}

export interface FabriTextResponse {
    text: string;
}

// 텍스트 내용 생성
export function fabriText(request: FabriTextRequest): Promise<FabriTextResponse> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/extract/fabri-text', request)
            .then((response: any) => {
                resolve(response.data || { text: '' });
            })
            .catch((error: any) => {
                console.error('텍스트 내용 생성 실패:', error);
                reject(error);
            });
    });
}

export interface FabriTagRequest {
    llm_config: LLMConfig;
}

export interface FabriTagResponse {
    tags: string[];
}

// 태그 생성
export function fabriTag(request: FabriTagRequest): Promise<FabriTagResponse> {
    return new Promise((resolve, reject) => {
        post('/api/v1/initialization/extract/fabri-tag', request)
            .then((response: any) => {
                resolve(response.data || { tags: [] as string[] });
            })
            .catch((error: any) => {
                console.error('태그 생성 실패:', error);
                reject(error);
            });
    });
}

// 모델 공급업체 정보 유형
export interface ModelProviderOption {
    value: string;        // provider 식별자
    label: string;        // 표시 이름
    description: string;  // 설명
    defaultUrls: Record<string, string>;  // 모델 유형별 기본 URL
    modelTypes: string[]; // 지원되는 모델 유형
}

// 모델 공급업체 목록 가져오기
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
                console.error('모델 공급업체 목록 가져오기 실패:', error);
                resolve([]); // 실패 시 빈 배열 반환, 프론트엔드는 기본값으로 되돌릴 수 있음
            });
    });
}
