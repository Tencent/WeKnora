<template>
    <div class="settings-page-container">
        <div class="initialization-content">
        <!-- 顶部Ollama服务状态 -->
        <div class="ollama-summary-card">
            <div class="summary-header">
                <span class="title"><t-icon name="server" />Ollama 服务状态</span>
                <t-tag :theme="ollamaStatus.available ? 'success' : 'danger'" size="small" class="state">
                    {{ ollamaStatus.available ? `正常 (${ollamaStatus.version||'v?'} )` : (ollamaStatus.error || '未运行') }}
                </t-tag>
                <t-tooltip content="刷新状态">
                    <t-icon name="refresh" class="refresh-icon" :class="{ spinning: summaryRefreshing }" @click="refreshOllamaSummary" />
                </t-tooltip>
            </div>
            <div class="summary-body">
                <div class="models">
                    <span class="label">Ollama 服务地址</span>
                    <div class="model-list">
                        <t-tag size="small" theme="default" class="model-pill">{{ ollamaStatus.baseUrl || '未配置' }}</t-tag>
                    </div>
                </div>
                <div class="models">
                    <span class="label">已安装模型</span>
                    <div class="model-list">
                        <t-tag v-for="m in installedModels" :key="m" size="small" theme="default" class="model-pill">{{ m }}</t-tag>
                        <span v-if="installedModels.length===0" class="empty">暂无</span>
                    </div>
                </div>
            </div>
        </div>

        <!-- 主配置表单 -->
        <t-form ref="form" :data="formData" :rules="rules" @submit.prevent layout="vertical">
            <!-- 知识库基本信息配置区域 (仅在知识库设置模式下显示) -->
            <div v-if="props.isKbSettings" class="config-section">
                <h3><t-icon name="folder" class="section-icon" />知识库基本信息</h3>
                <div class="form-row">
                    <t-form-item label="知识库名称" name="kbName" :required="true">
                        <t-input v-model="formData.kbName" placeholder="请输入知识库名称" maxlength="50" show-word-limit />
                    </t-form-item>
                </div>
                <div class="form-row">
                    <t-form-item label="知识库描述" name="kbDescription">
                        <t-textarea v-model="formData.kbDescription" placeholder="请输入知识库描述" maxlength="200" show-word-limit :autosize="{ minRows: 3, maxRows: 6 }" />
                    </t-form-item>
                </div>
            </div>

            <!-- LLM 大语言模型配置区域 -->
            <div class="config-section">
                <h3><t-icon name="chat" class="section-icon" />LLM 大语言模型配置</h3>
                <div class="form-row">
                    <t-form-item label="模型来源" name="llm.source">
                        <t-radio-group v-model="formData.llm.source" @change="onModelSourceChange('llm')">
                            <t-radio value="local">Ollama (本地)</t-radio>
                            <t-radio value="remote">Remote API (远程)</t-radio>
                        </t-radio-group>
                    </t-form-item>
                </div>
                
                <div class="form-row">
                    <t-form-item label="模型名称" name="llm.modelName">
                        <div class="model-input-with-status">
                            <t-input v-model="formData.llm.modelName" placeholder="例如: qwen3:0.6b" 
                                     @blur="onModelNameChange('llm')" 
                                     @input="onModelNameInput('llm')"
                                     @keyup.enter="onModelNameChange('llm')"
                                     :clearable="!modelStatus.llm.downloading" />
                            <div class="model-status-icon">
                                <!-- 下载状态：优先显示 -->
                                <div v-if="formData.llm.source === 'local' && formData.llm.modelName && modelStatus.llm.downloading" class="model-download-status">
                                    <span class="download-percentage">{{ modelStatus.llm.progress.toFixed(1) }}%</span>
                                </div>
                                <!-- 其他状态：非下载时显示 -->
                                <t-icon 
                                    v-else-if="formData.llm.source === 'local' && formData.llm.modelName && modelStatus.llm.checked" 
                                    :name="modelStatus.llm.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                    :class="['status-icon', modelStatus.llm.available ? 'installed' : 'not-installed']" 
                                    :title="modelStatus.llm.available ? '已安装' : '未安装'"
                                />
                                <t-icon 
                                    v-else-if="formData.llm.source === 'local' && formData.llm.modelName && !modelStatus.llm.checked" 
                                    name="help-circle" 
                                    class="status-icon unknown" 
                                    title="未检查"
                                />
                            </div>
                            <!-- 下载按钮：未安装时显示 -->
                            <div v-if="formData.llm.source === 'local' && formData.llm.modelName && modelStatus.llm.checked && !modelStatus.llm.available && !modelStatus.llm.downloading" class="download-action">
                                <t-tooltip content="下载模型">
                                    <t-button 
                                        size="small" 
                                        theme="primary" 
                                        @click="downloadModel('llm', formData.llm.modelName)"
                                        class="download-btn"
                                        :disabled="modelStatus.llm.downloading"
                                    >
                                        <t-icon name="download" />
                                    </t-button>
                                </t-tooltip>
                            </div>
                        </div>
                    </t-form-item>
                    
                </div>
                
                <!-- 远程 API 配置区域 -->
                <div v-if="formData.llm.source === 'remote'" class="remote-config">
                    <div class="form-row">
                        <t-form-item label="Base URL" name="llm.baseUrl">
                            <div class="url-input-with-check">
                                <t-input v-model="formData.llm.baseUrl" placeholder="例如: https://api.openai.com/v1, 去除末尾/chat/completions路径后的URL的前面部分" 
                                         @blur="onRemoteConfigChange('llm')"
                                         @input="onRemoteConfigInput('llm')" />
                                <div v-if="formData.llm.modelName && formData.llm.baseUrl" class="check-action">
                                    <t-tooltip v-if="!modelStatus.llm.available && modelStatus.llm.checked" content="重新检查连接">
                                        <t-icon 
                                            name="refresh" 
                                            class="refresh-icon" 
                                            :class="{ spinning: modelStatus.llm.checking }"
                                            @click="checkRemoteModelStatus('llm')" 
                                        />
                                    </t-tooltip>
                                    <t-icon 
                                        v-else-if="modelStatus.llm.checked" 
                                        :name="modelStatus.llm.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                        :class="['status-icon', modelStatus.llm.available ? 'installed' : 'not-installed']" 
                                        :title="modelStatus.llm.available ? '连接正常' : '连接失败'"
                                    />
                                    <t-icon 
                                        v-else 
                                        name="loading" 
                                        class="status-icon checking spinning" 
                                        title="检查连接中"
                                    />
                                </div>
                            </div>
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="API Key (可选)" name="llm.apiKey">
                            <t-input v-model="formData.llm.apiKey" type="password" placeholder="请输入API Key (可选)" 
                                     @blur="onRemoteConfigChange('llm')"
                                     @input="onRemoteConfigInput('llm')" />
                        </t-form-item>
                    </div>
                    
                    <!-- 错误信息显示 -->
                    <div v-if="modelStatus.llm.checked && !modelStatus.llm.available && modelStatus.llm.message" class="error-message">
                        <t-icon name="error-circle" />
                        <span>{{ modelStatus.llm.message }}</span>
                    </div>
                </div>
            </div>

            <!-- Embedding 嵌入模型配置区域 -->
            <div class="config-section">
                <h3><t-icon name="layers" class="section-icon" />Embedding 嵌入模型配置</h3>
                
                <!-- 已有文件时的禁用提示 -->
                <div v-if="hasFiles" class="embedding-warning">
                    <t-alert theme="warning" message="知识库中已有文件，无法修改Embedding模型配置" />
                </div>
                
                <div class="form-row">
                    <t-form-item label="模型来源" name="embedding.source">
                        <t-radio-group v-model="formData.embedding.source" @change="onModelSourceChange('embedding')" :disabled="hasFiles">
                            <t-radio value="local">Ollama (本地)</t-radio>
                            <t-radio value="remote">Remote API (远程)</t-radio>
                        </t-radio-group>
                    </t-form-item>
                </div>
                
                <div class="form-row">
                    <t-form-item label="模型名称" name="embedding.modelName">
                        <div class="model-input-with-status">
                            <t-input v-model="formData.embedding.modelName" placeholder="例如: nomic-embed-text:latest" 
                                     @blur="onModelNameChange('embedding')" 
                                     @input="onModelNameInput('embedding')"
                                     @keyup.enter="onModelNameChange('embedding')"
                                     :disabled="hasFiles"
                                     :clearable="!modelStatus.embedding.downloading" />
                            <div class="model-status-icon">
                                <!-- 下载状态：优先显示 -->
                                <div v-if="formData.embedding.source === 'local' && formData.embedding.modelName && modelStatus.embedding.downloading" class="model-download-status">
                                    <span class="download-percentage">{{ modelStatus.embedding.progress.toFixed(1) }}%</span>
                                </div>
                                <!-- 其他状态：非下载时显示 -->
                                <t-icon 
                                    v-else-if="formData.embedding.source === 'local' && formData.embedding.modelName && modelStatus.embedding.checked" 
                                    :name="modelStatus.embedding.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                    :class="['status-icon', modelStatus.embedding.available ? 'installed' : 'not-installed']" 
                                    :title="modelStatus.embedding.available ? '已安装' : '未安装'"
                                />
                                <t-icon 
                                    v-else-if="formData.embedding.source === 'local' && formData.embedding.modelName && !modelStatus.embedding.checked" 
                                    name="help-circle" 
                                    class="status-icon unknown" 
                                    title="未检查"
                                />
                            </div>
                            <!-- 下载按钮：未安装时显示 -->
                            <div v-if="formData.embedding.source === 'local' && formData.embedding.modelName && modelStatus.embedding.checked && !modelStatus.embedding.available && !modelStatus.embedding.downloading" class="download-action">
                                <t-tooltip content="下载模型">
                                    <t-button 
                                        size="small" 
                                        theme="primary" 
                                        @click="downloadModel('embedding', formData.embedding.modelName)"
                                        class="download-btn"
                                        :disabled="modelStatus.embedding.downloading"
                                    >
                                        <t-icon name="download" />
                                    </t-button>
                                </t-tooltip>
                            </div>
                        </div>
                    </t-form-item>
                </div>
                
                <!-- 向量维度设置 -->
                <div class="form-row">
                    <t-form-item label="维度" name="embedding.dimension">
                        <div class="dimension-input-with-action">
                            <t-input v-model="formData.embedding.dimension" 
                                     :disabled="hasFiles" 
                                     placeholder="请输入向量维度" 
                                     style="width: 100px;"
                                     @input="onDimensionInput" />
                            <t-button 
                                size="small" 
                                variant="outline" 
                                class="detect-dim-btn"
                                :loading="embeddingDimDetecting"
                                :disabled="hasFiles"
                                @click="detectEmbeddingDimension"
                            >
                                检测维度
                            </t-button>
                        </div>
                    </t-form-item>

                </div>
                
                <!-- 远程 Embedding API 配置 -->
                <div v-if="formData.embedding.source === 'remote'" class="remote-config">
                    <div class="form-row">
                        <t-form-item label="Base URL" name="embedding.baseUrl">
                            <div class="url-input-with-check">
                                <t-input v-model="formData.embedding.baseUrl" placeholder="例如: https://api.openai.com/v1, 去除末尾/embeddings路径后的URL的前面部分" 
                                         :disabled="hasFiles" @blur="onRemoteConfigChange('embedding')"
                                         @input="onRemoteConfigInput('embedding')" />
                                <div v-if="formData.embedding.modelName && formData.embedding.baseUrl && !hasFiles" class="check-action">
                                    <t-tooltip v-if="!modelStatus.embedding.available && modelStatus.embedding.checked" content="重新检查连接">
                                        <t-icon 
                                            name="refresh" 
                                            class="refresh-icon" 
                                            :class="{ spinning: modelStatus.embedding.checking }"
                                            @click="checkEmbeddingModelStatus()" 
                                        />
                                    </t-tooltip>
                                    <t-icon 
                                        v-else-if="modelStatus.embedding.checked" 
                                        :name="modelStatus.embedding.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                        :class="['status-icon', modelStatus.embedding.available ? 'installed' : 'not-installed']" 
                                        :title="modelStatus.embedding.available ? '连接正常' : '连接失败'"
                                    />
                                    <t-icon 
                                        v-else 
                                        name="loading" 
                                        class="input-icon checking spinning" 
                                        title="检查连接中"
                                    />
                                </div>
                            </div>
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="API Key (可选)" name="embedding.apiKey">
                            <t-input v-model="formData.embedding.apiKey" type="password" placeholder="请输入API Key (可选)" 
                                     :disabled="hasFiles" @blur="onRemoteConfigChange('embedding')"
                                     @input="onRemoteConfigInput('embedding')" />
                        </t-form-item>
                    </div>
                    
                    <!-- 错误信息显示 -->
                    <div v-if="modelStatus.embedding.checked && !modelStatus.embedding.available && modelStatus.embedding.message" class="error-message">
                        <t-icon name="error-circle" />
                        <span>{{ modelStatus.embedding.message }}</span>
                    </div>
                </div>
            </div>

            <!-- Rerank 重排模型配置区域 -->
            <div class="config-section">
                <h3><t-icon name="swap" class="section-icon" />Rerank 重排模型配置</h3>
                
                <div class="form-row">
                    <t-form-item name="rerank.enabled">
                        <div class="switch-container">
                            <t-switch v-model="formData.rerank.enabled" @change="onRerankChange" />
                            <span class="switch-label">启用Rerank重排模型</span>
                        </div>
                    </t-form-item>
                </div>
                
                <!-- Rerank 详细配置 -->
                <div v-if="formData.rerank.enabled" class="rerank-config">
                    <div class="form-row">
                        <t-form-item label="模型名称" name="rerank.modelName">
                            <div class="model-input-with-status">
                                <t-input v-model="formData.rerank.modelName" placeholder="例如: bge-reranker-v2-m3" 
                                         @blur="onRerankConfigChange"
                                         @input="onRerankConfigInput" />
                                <div class="model-status-icon">
                                    <t-icon 
                                        v-if="formData.rerank.modelName && modelStatus.rerank.checked" 
                                        :name="modelStatus.rerank.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                        :class="['status-icon', modelStatus.rerank.available ? 'installed' : 'not-installed']" 
                                        :title="modelStatus.rerank.available ? '连接正常' : '连接失败'"
                                    />
                                    <t-icon 
                                        v-else-if="formData.rerank.modelName && !modelStatus.rerank.checked" 
                                        name="help-circle" 
                                        class="status-icon unknown" 
                                        title="未检查"
                                    />
                                </div>
                            </div>
                        </t-form-item>
                    </div>
                    
                    <div class="form-row">
                        <t-form-item label="Base URL" name="rerank.baseUrl">
                            <div class="url-input-with-check">
                                <t-input v-model="formData.rerank.baseUrl" placeholder="例如: http://localhost:11434, 去除末尾/rerank路径后的URL的前面部分" 
                                         @blur="onRerankConfigChange"
                                         @input="onRerankConfigInput" />
                                <div v-if="formData.rerank.modelName && formData.rerank.baseUrl" class="check-action">
                                    <t-tooltip v-if="!modelStatus.rerank.available && modelStatus.rerank.checked" content="重新检查连接">
                                        <t-icon 
                                            name="refresh" 
                                            class="refresh-icon" 
                                            :class="{ spinning: modelStatus.rerank.checking }"
                                            @click="checkRerankModelStatus" 
                                        />
                                    </t-tooltip>
                                    <t-icon 
                                        v-else-if="modelStatus.rerank.checked" 
                                        :name="modelStatus.rerank.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                        :class="['status-icon', modelStatus.rerank.available ? 'installed' : 'not-installed']" 
                                        :title="modelStatus.rerank.available ? '连接正常' : '连接失败'"
                                    />
                                    <t-icon 
                                        v-else 
                                        name="loading" 
                                        class="input-icon checking spinning" 
                                        title="检查连接中"
                                    />
                                </div>
                            </div>
                        </t-form-item>
                    </div>
                    
                    <div class="form-row">
                        <t-form-item label="API Key" name="rerank.apiKey">
                            <t-input v-model="formData.rerank.apiKey" type="password" placeholder="请输入API Key (可选)" 
                                     @blur="onRerankConfigChange"
                                     @input="onRerankConfigInput" />
                        </t-form-item>
                    </div>
                    
                    <!-- 错误信息显示 -->
                    <div v-if="modelStatus.rerank.checked && !modelStatus.rerank.available && modelStatus.rerank.message" class="error-message">
                        <t-icon name="error-circle" />
                        <span>{{ modelStatus.rerank.message }}</span>
                    </div>
                </div>
            </div>

            <!-- 多模态配置区域 -->
            <div class="config-section">
                <h3><t-icon name="image" class="section-icon" />多模态配置</h3>
                <div class="form-row">
                    <t-form-item name="multimodal.enabled">
                        <div class="switch-container">
                            <t-switch v-model="formData.multimodal.enabled" @change="onMultimodalChange" />
                            <span class="switch-label">启用多模态图片信息提取</span>
                        </div>
                    </t-form-item>
                </div>
                
                <!-- 多模态详细配置 -->
                <div v-if="formData.multimodal.enabled" class="multimodal-config">
                    <!-- VLM 视觉语言模型配置 -->
                    <h4>视觉语言模型配置</h4>
                    <div class="form-row">
                        <t-form-item label="模型名称" name="multimodal.vlm.modelName">
                            <div class="model-input-with-status">
                                <t-input v-model="formData.multimodal.vlm.modelName" placeholder="例如: qwen2.5vl:3b" 
                                         @blur="onModelNameChange('vlm')" 
                                         @input="onModelNameInput('vlm')"
                                         @keyup.enter="onModelNameChange('vlm')"
                                         :clearable="!modelStatus.vlm.downloading" />
                                <div class="model-status-icon">
                                    <!-- 下载状态：优先显示环形进度条 -->
                                    <div v-if="formData.multimodal.vlm.interfaceType === 'ollama' && formData.multimodal.vlm.modelName && modelStatus.vlm.downloading" class="model-download-status">
                                        <span class="download-percentage">{{ modelStatus.vlm.progress.toFixed(1) }}%</span>
                                    </div>
                                    <!-- 其他状态：非下载时显示 -->
                                    <t-icon 
                                        v-else-if="formData.multimodal.vlm.interfaceType === 'ollama' && formData.multimodal.vlm.modelName && modelStatus.vlm.checked" 
                                        :name="modelStatus.vlm.available ? 'check-circle-filled' : 'close-circle-filled'" 
                                        :class="['status-icon', modelStatus.vlm.available ? 'installed' : 'not-installed']" 
                                        :title="modelStatus.vlm.available ? '已安装' : '未安装'"
                                    />
                                    <t-icon 
                                        v-else-if="formData.multimodal.vlm.interfaceType === 'ollama' && formData.multimodal.vlm.modelName && !modelStatus.vlm.checked" 
                                        name="help-circle" 
                                        class="status-icon unknown" 
                                        title="未检查"
                                    />
                                </div>
                                <!-- 下载按钮：未安装时显示 -->
                                <div v-if="formData.multimodal.vlm.interfaceType === 'ollama' && formData.multimodal.vlm.modelName && modelStatus.vlm.checked && !modelStatus.vlm.available && !modelStatus.vlm.downloading" class="download-action">
                                    <t-tooltip content="下载模型">
                                        <t-button 
                                            size="small" 
                                            theme="primary" 
                                            @click="downloadModel('vlm', formData.multimodal.vlm.modelName)"
                                            class="download-btn"
                                            :disabled="modelStatus.vlm.downloading"
                                        >
                                            <t-icon name="download" />
                                        </t-button>
                                    </t-tooltip>
                                </div>
                            </div>
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="接口类型" name="multimodal.vlm.interfaceType">
                            <t-radio-group v-model="formData.multimodal.vlm.interfaceType" @change="onVlmInterfaceTypeChange">
                                <t-radio value="ollama">Ollama (本地)</t-radio>
                                <t-radio value="openai">OpenAI 兼容接口</t-radio>
                            </t-radio-group>
                        </t-form-item>
                    </div>
                    <div class="form-row" v-if="formData.multimodal.vlm.interfaceType === 'openai'">
                        <t-form-item label="Base URL" name="multimodal.vlm.baseUrl">
                            <t-input v-model="formData.multimodal.vlm.baseUrl" placeholder="例如: http://localhost:11434/v1，去除末尾/chat/completions路径后的URL的前面部分"
                                     @blur="onVlmBaseUrlChange"
                                     @input="onVlmBaseUrlInput" />
                        </t-form-item>
                    </div>
                    <div class="form-row" v-if="formData.multimodal.vlm.interfaceType === 'openai'">
                        <t-form-item label="API Key" name="multimodal.vlm.apiKey">
                            <t-input v-model="formData.multimodal.vlm.apiKey" type="password" placeholder="请输入API Key (可选)"
                                     @blur="onVlmApiKeyChange" />
                        </t-form-item>
                    </div>
                    
                    <!-- 对象存储服务配置 -->
                    <h4>对象存储服务配置</h4>
                    <div class="form-row">
                        <t-form-item label="存储类型">
                            <t-radio-group v-model="formData.multimodal.storageType" @change="onStorageTypeChange">
                                <t-radio value="cos">COS</t-radio>
                                <t-radio value="minio">MinIO</t-radio>
                            </t-radio-group>
                        </t-form-item>
                    </div>
                    
                    <!-- MinIO 配置区域 -->
                    <div v-if="formData.multimodal.storageType === 'minio'">
                        <div class="form-row">
                            <t-form-item label="Bucket Name" name="multimodal.minio.bucketName">
                                <t-input v-model="formData.multimodal.minio.bucketName" placeholder="请输入Bucket名称" />
                            </t-form-item>
                        </div>

                        <div class="form-row">
                            <t-form-item label="Path Prefix" name="multimodal.minio.pathPrefix">
                                <t-input v-model="formData.multimodal.minio.pathPrefix" placeholder="例如: images" />
                            </t-form-item>
                        </div>
                    </div>
                    
                    <!-- COS 配置区域 -->
                    <div class="form-row">
                        <t-form-item v-if="formData.multimodal.storageType === 'cos'" label="Secret ID" name="multimodal.cos.secretId">
                            <t-input v-model="formData.multimodal.cos.secretId" placeholder="请输入COS Secret ID"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item v-if="formData.multimodal.storageType === 'cos'" label="Secret Key" name="multimodal.cos.secretKey">
                            <t-input v-model="formData.multimodal.cos.secretKey" type="password" placeholder="请输入COS Secret Key"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item v-if="formData.multimodal.storageType === 'cos'" label="Region" name="multimodal.cos.region">
                            <t-input v-model="formData.multimodal.cos.region" placeholder="例如: ap-beijing"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item v-if="formData.multimodal.storageType === 'cos'" label="Bucket Name" name="multimodal.cos.bucketName">
                            <t-input v-model="formData.multimodal.cos.bucketName" placeholder="请输入Bucket名称"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item v-if="formData.multimodal.storageType === 'cos'" label="App ID" name="multimodal.cos.appId">
                            <t-input v-model="formData.multimodal.cos.appId" placeholder="请输入App ID"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item v-if="formData.multimodal.storageType === 'cos'" label="Path Prefix" name="multimodal.cos.pathPrefix">
                            <t-input v-model="formData.multimodal.cos.pathPrefix" placeholder="例如: images"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    
                    <!-- 多模态功能测试区域 -->
                    <div v-if="canTestMultimodal" class="multimodal-test">
                        <h5>功能测试</h5>
                        <p class="test-desc">上传图片测试VLM模型的图片描述和文字识别功能</p>
                        
                        <div class="test-area">
                            <!-- 上传区域 -->
                            <div class="upload-section">
                                <div class="upload-buttons">
                                    <t-upload
                                        ref="imageUpload"
                                        v-model="multimodalTest.uploadedFiles"
                                        :show-upload-list="false"
                                        :auto-upload="false"
                                        :accept="'image/*'"
                                        :size-limit="10485760"
                                        @change="onImageChange"
                                    >
                                        <t-button theme="default" variant="outline" size="small">
                                            <t-icon name="upload" />
                                            选择图片
                                        </t-button>
                                    </t-upload>
                                </div>
                            </div>
                            
                            <!-- 图片预览 -->
                            <div v-if="multimodalTest.selectedFile" class="image-preview">
                                <img :src="multimodalTest.previewUrl" alt="测试图片" />
                                <div class="image-meta">
                                    <span class="file-name">{{ multimodalTest.selectedFile.name }}</span>
                                    <span class="file-size">{{ formatFileSize(multimodalTest.selectedFile.size) }}</span>
                                </div>
                            </div>
                            
                            <div class="test-button-wrapper">
                                <t-button 
                                    v-if="multimodalTest.selectedFile"
                                    theme="primary" 
                                    size="small" 
                                    :loading="multimodalTest.testing"
                                    @click="startMultimodalTest"
                                >
                                    开始测试
                                </t-button>
                            </div>
                            <!-- 测试结果 -->
                            <div v-if="multimodalTest.result" class="test-result">
                                <div v-if="multimodalTest.result.success" class="result-success">
                                    <h6>测试结果</h6>
                                    
                                    <div v-if="multimodalTest.result.caption" class="result-item">
                                        <label>图片描述:</label>
                                        <div class="result-text">{{ multimodalTest.result.caption }}</div>
                                    </div>
                                    
                                    <div v-if="multimodalTest.result.ocr" class="result-item">
                                        <label>文字识别:</label>
                                        <div class="result-text">{{ multimodalTest.result.ocr }}</div>
                                    </div>
                                    
                                    <div v-if="multimodalTest.result.processing_time" class="result-time">
                                        处理时间: {{ multimodalTest.result.processing_time }}ms
                                    </div>
                                </div>
                                
                                <div v-else class="result-error">
                                    <h6>测试失败</h6>
                                    <div class="error-msg">
                                        <t-icon name="error-circle" />
                                        {{ multimodalTest.result.message || '多模态处理失败' }}
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- 文档分割配置区域 -->
            <div class="config-section">
                <h3><t-icon name="cut" class="section-icon" />文档分割配置</h3>
                
                <!-- 预设配置选择 -->
                <div class="form-row preset-row">
                    <t-form-item label="分割策略">
                        <t-radio-group v-model="selectedPreset" @change="onPresetChange" class="preset-radio-group">
                            <t-radio value="balanced" class="preset-radio">
                                <div class="preset-content">
                                    <div class="preset-title">均衡模式</div>
                                    <div class="preset-desc">块大小: 1000 / 重叠: 200</div>
                                </div>
                            </t-radio>
                            <t-radio value="precision" class="preset-radio">
                                <div class="preset-content">
                                    <div class="preset-title">精准模式</div>
                                    <div class="preset-desc">块大小: 512 / 重叠: 100</div>
                                </div>
                            </t-radio>
                            <t-radio value="context" class="preset-radio">
                                <div class="preset-content">
                                    <div class="preset-title">上下文模式</div>
                                    <div class="preset-desc">块大小: 2048 / 重叠: 400</div>
                                </div>
                            </t-radio>
                            <t-radio value="custom" class="preset-radio">
                                <div class="preset-content">
                                    <div class="preset-title">自定义</div>
                                    <div class="preset-desc">手动配置参数</div>
                                </div>
                            </t-radio>
                        </t-radio-group>
                    </t-form-item>
                </div>
                
                <!-- 参数配置网格 -->
                <div class="parameters-grid" :class="{ 'disabled-grid': selectedPreset !== 'custom' }">
                    <div class="parameter-group">
                        <div class="parameter-label">分块大小</div>
                        <div class="parameter-control">
                            <t-slider 
                                v-model="formData.documentSplitting.chunkSize" 
                                :min="100" 
                                :max="4000" 
                                :step="1"
                                :disabled="selectedPreset !== 'custom'"
                                :marks="{ 100: '100', 1000: '1000', 2000: '2000', 4000: '4000' }"
                                class="parameter-slider"
                            />
                            <div class="parameter-value">{{ formData.documentSplitting.chunkSize }} 字符</div>
                        </div>
                        <div class="parameter-desc">控制每个文档分块的大小，影响检索精度</div>
                    </div>
                    
                    <div class="parameter-group">
                        <div class="parameter-label">分块重叠</div>
                        <div class="parameter-control">
                            <t-slider 
                                v-model="formData.documentSplitting.chunkOverlap" 
                                :min="0" 
                                :max="1000" 
                                :step="1"
                                :disabled="selectedPreset !== 'custom'"
                                :marks="{ 0: '0', 200: '200', 500: '500', 1000: '1000' }"
                                class="parameter-slider"
                            />
                            <div class="parameter-value">{{ formData.documentSplitting.chunkOverlap }} 字符</div>
                        </div>
                        <div class="parameter-desc">分块间重叠的字符数，保持上下文连贯性</div>
                    </div>
                    
                    <div class="parameter-group">
                        <div class="parameter-label">分隔符设置</div>
                        <div class="parameter-control">
                            <t-select 
                                v-model="formData.documentSplitting.separators" 
                                multiple
                                :disabled="selectedPreset !== 'custom'"
                                placeholder="选择或自定义分隔符"
                                class="parameter-select"
                                clearable
                                creatable
                                :options="separatorOptions"
                            />
                        </div>
                    </div>
                </div>
            </div>

            <!-- 实体关系提取 -->
            <div class="config-section">
                <h3><t-icon name="transform" class="section-icon" />实体关系提取</h3>
                
                <div class="form-row">
                    <t-form-item name="nodeExtract.enabled">
                        <div class="switch-container">
                            <t-switch v-model="formData.nodeExtract.enabled" @change="clearExtractExample" />
                            <span class="switch-label">启用实体关系提取</span>
                        </div>
                    </t-form-item>
                </div>

                <div v-if="formData.nodeExtract.enabled" class="node-config">
                    <h4>关系标签配置</h4>
                    <!-- 关系标签配置区域 -->
                    <div class="form-row">
                        <t-form-item label="关系类型" name="tags">
                            <div class="tags-grid">
                                <div class="btn-tips-form">
                                    <div class="tags-gen-btn">
                                        <t-button 
                                            theme="default" 
                                            size="medium"
                                            :disabled="!modelStatus.llm.available"
                                            :loading="tagFabring"
                                            @click="handleFabriTag" 
                                            class="gen-tags-btn"
                                        >
                                            随机生成标签
                                        </t-button>
                                    </div>
                                    <div v-if="!modelStatus.llm.available" class="btn-tips">
                                        <t-icon name="info-circle" class="tip-icon" />
                                        <span>请完善模型配置信息</span>
                                    </div>
                                </div>
                                <div class="tags-config">
                                    <t-select 
                                        v-model="formData.nodeExtract.tags" 
                                        v-model:input-value="tagInput"
                                        multiple
                                        placeholder="系统将根据选定的关系类型从文本中提取相应的实体关系"
                                        :options="tagOptions"
                                        clearable
                                        @clear="clearTags"
                                        creatable
                                        @create="addTag"
                                        filterable
                                    />
                                </div>
                            </div>
                        </t-form-item>
                    </div>

                    <h4>提取示例</h4>
                    <!-- 文本内容输入区域 -->
                    <div class="form-row">
                        <t-form-item label="示例文本" name="text" :required="true">
                            <div class="sample-text-form">
                                <div class="btn-tips-form">
                                    <div class="tags-gen-btn">
                                        <t-button 
                                            theme="default" 
                                            size="medium"
                                            :disabled="!modelStatus.llm.available"
                                            :title="!modelStatus.llm.available ? 'LLM 模型不可用' : ''"
                                            :loading="textFabring"
                                            @click="handleFabriText" 
                                            class="tags-gen-btn"
                                        >
                                            随机生成文本
                                        </t-button>
                                    </div>
                                    <div v-if="!modelStatus.llm.available" class="btn-tips">
                                        <t-icon name="info-circle" class="tip-icon" />
                                        <span>请完善模型配置信息</span>
                                    </div>
                                </div>
                                <div class="sample-text">
                                    <t-textarea 
                                        v-model="formData.nodeExtract.text" 
                                        placeholder="请输入需要分析的文本内容，例如：《红楼梦》，又名《石头记》，是清代作家曹雪芹创作的中国古典四大名著之一..." 
                                        :autosize="{ minRows: 8, maxRows: 15 }"
                                        show-word-limit
                                        maxlength="5000"
                                    />
                                </div>
                            </div>
                        </t-form-item>
                    </div>

                    <!-- 提取实体 -->
                    <div class="form-row">
                        <!-- 实体列表 -->
                        <t-form-item v-if="formData.nodeExtract.nodes.length > 0" label="实体列表" name="node-form">
                            <div class="node-list">
                                <div v-for="(node, nodeIndex) in formData.nodeExtract.nodes" :key="nodeIndex" class="node-item">
                                    <div class="node-header">
                                        <span class="node-icon"><t-icon name="user" class="node-icon-svg" /></span>
                                        <!-- 节点名称输入 -->
                                        <t-input 
                                            type="text" 
                                            v-model="node.name" 
                                            class="node-name-input" 
                                            placeholder="节点名称"
                                        />
                                        <!-- 删除节点按钮 -->
                                        <t-button 
                                            class="delete-node-btn" 
                                            theme="default"
                                            @click="removeNode(nodeIndex)"
                                            :disabled="formData.nodeExtract.nodes.length === 0"
                                            size="small"
                                        >
                                            <t-icon name="delete" />
                                        </t-button>
                                    </div>
                                    
                                    <div class="node-attributes">
                                        <!-- 属性列表 -->
                                        <div v-for="(attribute, attrIndex) in node.attributes" :key="attrIndex" class="attribute-item">
                                            <t-input 
                                                type="text" 
                                                v-model="node.attributes[attrIndex]" 
                                                class="attribute-input" 
                                                placeholder="属性值"
                                            />
                                            <t-button 
                                                class="delete-attr-btn" 
                                                theme="default"
                                                @click="removeAttribute(nodeIndex, attrIndex)"
                                                :disabled="node.attributes.length === 0"
                                                size="small"
                                            >
                                                <t-icon name="close" />
                                            </t-button>
                                        </div>
                                        
                                        <!-- 添加属性按钮 -->
                                        <t-button class="add-attr-btn" @click="addAttribute(nodeIndex)" size="small">
                                            添加属性
                                        </t-button>
                                    </div>
                                </div>
                            </div>
                        </t-form-item>
                        <!-- 添加实体按钮 -->
                        <div class="btn-tips-form">
                            <div class="tags-gen-btn">
                                <t-button class="add-node-btn" @click="addNode">
                                    添加实体
                                </t-button>
                            </div>
                            <div v-if="!readyNode" class="btn-tips">
                                <t-icon name="info-circle" class="tip-icon" />
                                <span>请完善实体信息</span>
                            </div>
                        </div>
                    </div>

                    <!-- 提取关系 -->
                    <div class="form-row">
                        <t-form-item v-if="formData.nodeExtract.relations.length > 0" label="关系连接" name="node-relation">
                            <div class="relation-list">
                                <div v-for="(relation, index) in formData.nodeExtract.relations" :key="index" class="relation-item">
                                    <div class="relation-line">
                                        <t-select-input 
                                            :value="formData.nodeExtract.relations[index].node1"
                                            :popup-visible="popupVisibleNode1[index]"
                                            placeholder="请选择实体"
                                            clearable
                                            @popup-visible-change="onPopupVisibleNode1Change(index, $event)"
                                            @clear="relationOnClearNode1(index)"
                                            @focus="onFocus"
                                        >
                                            <template #panel>
                                            <ul class="select-input-node">
                                                <li v-for="item in formData.nodeExtract.nodes" :key="item.name" @click="onRelationNode1OptionClick(index, item)">
                                                    {{ item.name }}
                                                </li>
                                            </ul>
                                            </template>
                                            <template #suffixIcon>
                                                <ChevronDownIcon />
                                            </template>
                                        </t-select-input>
                                        <t-icon name="arrow-right" class="relation-arrow"/>
                                        <t-select 
                                            v-model="formData.nodeExtract.relations[index].type" 
                                            placeholder="请选择关系类型"
                                            :options="tagOptions"
                                            clearable
                                            creatable
                                            filterable
                                        />
                                        <t-icon name="arrow-right" class="relation-arrow"/>
                                        <t-select-input 
                                            :value="formData.nodeExtract.relations[index].node2"
                                            :popup-visible="popupVisibleNode2[index]"
                                            placeholder="请选择实体"
                                            clearable
                                            @popup-visible-change="onPopupVisibleNode2Change(index, $event)"
                                            @clear="relationOnClearNode2(index)"
                                            @focus="onFocus"
                                        >
                                            <template #panel>
                                            <ul class="select-input-node">
                                                <li v-for="item in formData.nodeExtract.nodes" :key="item.name" @click="onRelationNode2OptionClick(index, item)">
                                                    {{ item.name }}
                                                </li>
                                            </ul>
                                            </template>
                                            <template #suffixIcon>
                                                <ChevronDownIcon />
                                            </template>
                                        </t-select-input>
                                        <t-button 
                                            class="delete-node-btn" 
                                            theme="default"
                                            @click="removeRelation(index)"
                                            :disabled="formData.nodeExtract.relations.length === 0"
                                            size="small"
                                        >
                                            <t-icon name="delete" />
                                        </t-button>
                                    </div>
                                </div>
                            </div>
                        </t-form-item>

                        <!-- 添加关系按钮 -->
                        <div class="btn-tips-form">
                            <div class="tags-gen-btn">
                                <t-button class="add-node-btn" @click="addRelation">
                                    添加关系
                                </t-button>
                            </div>
                            <div v-if="!readyRelation" class="btn-tips">
                                <t-icon name="info-circle" class="tip-icon" />
                                <span>请完善关系信息</span>
                            </div>
                        </div>
                    </div>

                    <!-- 重置按钮区域 -->
                    <div class="extract-button">
                        <t-button 
                            theme="primary" 
                            size="medium" 
                            :disabled="!modelStatus.llm.available"
                            :title="!modelStatus.llm.available ? 'LLM 模型不可用' : ''"
                            :loading="extracting" 
                            @click="handleExtract"
                        >
                            {{ extracting ? '正在提取...' : '开始提取' }}
                        </t-button>

                        <t-button 
                            theme="default" 
                            size="medium" 
                            @click="defaultExtractExample"
                            class="default-extract-btn"
                        >
                            默认示例
                        </t-button>

                        <t-button 
                            theme="default" 
                            size="medium" 
                            @click="clearExtractExample"
                            class="clear-extract-btn"
                        >
                            清空示例
                        </t-button>
                    </div>
                </div>
            </div>

            <!-- 提交按钮区域 -->
            <div class="submit-section">
                <t-button theme="primary" type="button" size="large" 
                          :loading="submitting" :disabled="!canSubmit || isSubmitDebounced"
                          @click="handleSubmit">
                    {{ props.isKbSettings ? '更新知识库设置' : (isUpdateMode ? '更新配置信息' : '完成配置') }}
                </t-button>
                
                <!-- 提交状态提示 -->
                <div v-if="!canSubmit && hasOllamaModels" class="submit-tips">
                    <t-icon name="info-circle" class="tip-icon" />
                    <span>请等待所有Ollama模型下载完成后再进行配置更新</span>
                </div>
                
                <!-- 远程API配置提示 -->
                <div v-if="!canSubmit && !hasOllamaModels" class="submit-tips">
                    <t-icon name="info-circle" class="tip-icon" />
                    <span>请完善模型配置信息</span>
                </div>
            </div>
        </t-form>
        </div>
    </div>
</template>

<script setup lang="ts">
/**
 * 导入必要的 Vue 组合式 API 和外部依赖
 */
import { ref, reactive, computed, watch, onMounted, onUnmounted, nextTick } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { ChevronDownIcon } from 'tdesign-icons-vue-next';
import { 
    initializeSystemByKB,
    checkOllamaStatus, 
    checkOllamaModels, 
    downloadOllamaModel,
    getDownloadProgress,
    getCurrentConfigByKB,
    checkRemoteModel,
    type DownloadTask,
    checkRerankModel,
    testMultimodalFunction,
    listOllamaModels,
    testEmbeddingModel,
    extractTextRelations,
    fabriText,
    fabriTag,
    type TextRelationExtractionRequest,
    type Node,
    type Relation,
    type FabriTagRequest,
    type FabriTextRequest
} from '@/api/initialization';
import { getKnowledgeBaseById } from '@/api/knowledge-base';
import { useAuthStore } from '@/stores/auth';

const router = useRouter();
const route = useRoute();
const authStore = useAuthStore();

// 接收props，判断是否为知识库设置模式
const props = defineProps<{
    isKbSettings?: boolean;
}>();

// 获取当前知识库ID（如果是知识库设置模式）
const currentKbId = computed(() => {
    return props.isKbSettings ? (route.params.kbId as string) : null;
});
type TFormRef = {
    validate: (fields?: string[] | undefined) => Promise<true | any>;
    clearValidate?: (fields?: string | string[]) => void;
} | null;
const form = ref<TFormRef>(null);
const submitting = ref(false);
const hasFiles = ref(false);
const isUpdateMode = ref(false); // 是否为更新模式
const tagOptionsDefault = [
    { label: '内容', value: '内容' },
    { label: '文化', value: '文化' },
    { label: '人物', value: '人物' },
    { label: '事件', value: '事件' },
    { label: '时间', value: '时间' },
    { label: '地点', value: '地点' },
    { label: '作品', value: '作品' },
    { label: '作者', value: '作者' },
    { label: '关系', value: '关系' },
    { label: '属性', value: '属性' }
];
const tagOptions = ref([] as {label: string, value: string}[]);
const tagInput = ref('');
const popupVisibleNode1 = ref<boolean[]>([]);
const popupVisibleNode2 = ref<boolean[]>([]);
const tagFabring = ref(false);
const textFabring = ref(false);
const extracting = ref(false);

// 防抖机制：防止按钮快速重复点击
const submitDebounceTimer = ref<ReturnType<typeof setTimeout> | null>(null);
const isSubmitDebounced = ref(false);

// Ollama服务状态
const ollamaStatus = reactive({
    checked: false,
    available: false,
    version: '',
    error: '',
    baseUrl: ''
});
// 顶部摘要：已安装模型
const installedModels = ref<string[]>([]);
const summaryRefreshing = ref<boolean>(false);

// 下载任务管理
const downloadTasks = reactive<Record<string, DownloadTask>>({});
const progressTimers = reactive<Record<string, any>>({});

// 模型状态管理
const modelStatus = reactive({
    llm: {
        checked: false,
        available: false,
        downloading: false,
        checking: false,
        taskId: '',
        progress: 0,
        message: ''
    },
    embedding: {
        checked: false,
        available: false,
        downloading: false,
        checking: false,
        taskId: '',
        progress: 0,
        message: ''
    },
    vlm: {
        checked: false,
        available: false,
        downloading: false,
        checking: false,
        taskId: '',
        progress: 0,
        message: ''
    },
    rerank: {
        checked: false,
        available: false,
        downloading: false,
        checking: false,
        taskId: '',
        progress: 0,
        message: ''
    }
});

// 表单数据
const formData = reactive({
    // 知识库基本信息 (仅在知识库设置模式下使用)
    kbName: '',
    kbDescription: '',
    llm: {
        source: 'local',
        modelName: '',
        baseUrl: '',
        apiKey: ''
    },
    embedding: {
        source: 'local',
        modelName: '',
        baseUrl: '',
        apiKey: '',
        dimension: 0 // 默认嵌入维度值
    },
    rerank: {
        enabled: false,
        modelName: '',
        baseUrl: '',
        apiKey: ''
    },
    multimodal: {
        enabled: false,
        storageType: 'minio' as 'minio' | 'cos',
        vlm: {
            modelName: '',
            baseUrl: '',
            apiKey: '',
            interfaceType: 'ollama'
        },
        cos: {
            secretId: '',
            secretKey: '',
            region: '',
            bucketName: '',
            appId: '',
            pathPrefix: ''
        },
        minio: {
            bucketName: '',
            useSSL: false,
            pathPrefix: ''
        }
    },
    documentSplitting: {
        chunkSize: 512, 
        chunkOverlap: 100,
        separators: ['\n\n', '\n', '。', '！', '？', ';', '；']
    },
    nodeExtract: {
        enabled: false,
        text: '',
        tags: [] as string[],
        nodes: [] as Node[],
        relations: [] as Relation[]
    }
});

// 输入防抖定时器
const inputDebounceTimers = reactive<Record<string, any>>({});

// Embedding 维度检测状态
const embeddingDimDetecting = ref(false);

// 预设配置选择
const selectedPreset = ref('precision');

// 分隔符选项
const separatorOptions = [
    { label: '段落分隔符 (\\n\\n)', value: '\n\n' },
    { label: '换行符 (\\n)', value: '\n' },
    { label: '句号 (。)', value: '。' },
    { label: '感叹号 (！)', value: '！' },
    { label: '问号 (？)', value: '？' },
    { label: '分号 (;)', value: ';' },
    { label: '中文分号 (；)', value: '；' },
    { label: '逗号 (,)', value: ',' },
    { label: '中文逗号 (，)', value: '，' }
];

// 多模态测试状态
const multimodalTest = reactive({
    uploadedFiles: [],
    selectedFile: null as File | null,
    previewUrl: '',
    testing: false,
    result: null as any
});

// 计算属性
const isVlmOllama = computed(() => {
    return formData.multimodal.vlm.interfaceType === 'ollama';
});

const hasOllamaModels = computed(() => {
    // 检查是否真正需要Ollama模型
    const needsLlmOllama = formData.llm.source === 'local' && formData.llm.modelName;
    const needsEmbeddingOllama = formData.embedding.source === 'local' && formData.embedding.modelName;
    const needsVlmOllama = formData.multimodal.enabled && 
                          formData.multimodal.vlm.interfaceType === 'ollama' && 
                          formData.multimodal.vlm.modelName;
    
    return needsLlmOllama || needsEmbeddingOllama || needsVlmOllama;
});

const canTestMultimodal = computed(() => {
    // 检查VLM模型配置是否完整
    const vlmConfigComplete = formData.multimodal.vlm.modelName && 
        (formData.multimodal.vlm.interfaceType === 'ollama' || 
         (formData.multimodal.vlm.interfaceType === 'openai' && formData.multimodal.vlm.baseUrl));
    
    // 检查存储配置是否完整
    let storageConfigComplete = false;
    if (formData.multimodal.storageType === 'cos') {
        storageConfigComplete = !!(formData.multimodal.cos.secretId && 
                              formData.multimodal.cos.secretKey && 
                              formData.multimodal.cos.region && 
                              formData.multimodal.cos.bucketName && 
                              formData.multimodal.cos.appId);
    } else if (formData.multimodal.storageType === 'minio') {
        storageConfigComplete = !!formData.multimodal.minio.bucketName;
    }
    
    return vlmConfigComplete && storageConfigComplete;
});

const canSubmit = computed(() => {
    // 基本必填项检查
    const basicRequired = formData.llm.modelName && 
                         formData.embedding.modelName;
    
    if (!basicRequired) return false;
    
    // LLM模型检查
    let llmOk = true;
    if (formData.llm.source === 'local') {
        // Ollama模型需要检查可用性
        llmOk = modelStatus.llm.available && !modelStatus.llm.downloading;
    } else {
        // 远程API只需要有BaseURL即可（如果需要的话，在表单验证中处理）
        llmOk = true;
    }
    
    // Embedding模型检查
    let embeddingOk = true;
    if (formData.embedding.source === 'local') {
        // Ollama模型需要检查可用性
        embeddingOk = modelStatus.embedding.available && !modelStatus.embedding.downloading;
    } else {
        // 远程API只需要有BaseURL即可
        embeddingOk = true;
    }
    
    // Rerank模型检查（如果启用）
    let rerankOk = true;
    if (formData.rerank.enabled && formData.rerank.modelName) {
        if (formData.rerank.baseUrl) {
            // 远程API，不需要特殊检查
            rerankOk = true;
        } else {
            // 本地Ollama模型需要检查可用性
            rerankOk = modelStatus.rerank.available && !modelStatus.rerank.downloading;
        }
    }
    
    // VLM模型检查（如果启用多模态）
    let vlmOk = true;
    if (formData.multimodal.enabled && formData.multimodal.vlm.modelName) {
        if (formData.multimodal.vlm.interfaceType === 'ollama') {
            // Ollama模型需要检查可用性
            vlmOk = modelStatus.vlm.available && !modelStatus.vlm.downloading;
        } else {
            // OpenAI兼容API，不需要特殊检查
            vlmOk = true;
        }
    }

    let extractOk = true;
    if (formData.nodeExtract.enabled) {
        if (formData.nodeExtract.text === '') {
            extractOk = false;
        }
        for (let i = 0; i < formData.nodeExtract.tags.length; i++) {
            const tag = formData.nodeExtract.tags[i];
            if (tag == '') {
                extractOk = false;
                break;
            }
        }

        if (!readyNode.value){
            extractOk = false;
        }
        if (!readyRelation.value){
            extractOk = false;
        }
    }
    
    return llmOk && embeddingOk && rerankOk && vlmOk && extractOk;
});

const imageUpload = ref(null);

// 清理完成，开始函数定义

// 验证Embedding维度
const validateEmbeddingDimension = (val: any) => {
    val = Number(val);
    if (!val || isNaN(val)) {
        return false;
    }
    // 验证是否为整数且在合理范围内
    return Number.isInteger(val) ;
};

// 表单验证规则
const rules = {
    // 知识库基本信息验证 (仅在知识库设置模式下使用)
    'kbName': [
        { required: (t: any) => props.isKbSettings, message: '请输入知识库名称', type: 'error' },
        { min: 1, max: 50, message: '知识库名称长度应在1-50个字符之间', type: 'error' }
    ],
    'kbDescription': [
        { max: 200, message: '知识库描述长度不能超过200个字符', type: 'error' }
    ],
    'llm.modelName': [{ required: true, message: '请输入LLM模型名称', type: 'error' }],
    'llm.baseUrl': [
        { required: (t: any) => formData.llm.source === 'remote', message: '请输入BaseURL', type: 'error' }
    ],
    'embedding.modelName': [{ required: true, message: '请输入Embedding模型名称', type: 'error' }],
    'embedding.baseUrl': [
        { required: (t: any) => formData.embedding.source === 'remote', message: '请输入BaseURL', type: 'error' }
    ],
    'embedding.dimension': [
        { required: true, message: '请输入Embedding维度', type: 'error' },
        { validator: validateEmbeddingDimension, message: '维度必须为有效整数值，常见取值为768, 1024, 1536, 3584等', type: 'error' }
    ],
    'nodeExtract.text': [
        { required: true, message: '请输入文本内容', type: 'error' },
        { min: 10, message: '文本内容至少需要10个字符', type: 'error' }
    ]
};

// 检查Ollama服务状态
const checkOllama = async () => {
    try {
        const result = await checkOllamaStatus();
        ollamaStatus.checked = true;
        ollamaStatus.available = result.available;
        ollamaStatus.version = result.version || '';
        ollamaStatus.error = result.error || '';
        ollamaStatus.baseUrl = result.baseUrl || '';
        
        if (ollamaStatus.available) {
            // 如果Ollama可用，检查已配置的模型
            await checkAllOllamaModels();
        }
    } catch (error) {
        console.error('检查Ollama状态失败:', error);
        ollamaStatus.checked = true;
        ollamaStatus.available = false;
        ollamaStatus.error = '检查失败';
    }
};

// 刷新顶部摘要（状态 + 已安装模型）
const refreshOllamaSummary = async () => {
    if (summaryRefreshing.value) return;
    summaryRefreshing.value = true;
    try {
        await checkOllama();
        const models = await listOllamaModels();
        installedModels.value = models;
    } catch (e) {
        installedModels.value = [];
    } finally {
        // 略延时以保证旋转动画可见
        setTimeout(() => { summaryRefreshing.value = false; }, 300);
    }
};

// 检查所有Ollama模型状态
const checkAllOllamaModels = async () => {
    const modelsToCheck = [];
    
    if (formData.llm.source === 'local' && formData.llm.modelName) {
        modelsToCheck.push(formData.llm.modelName);
    }
    
    if (formData.embedding.source === 'local' && formData.embedding.modelName) {
        modelsToCheck.push(formData.embedding.modelName);
    }
    
    // 添加VLM模型检查
    if (formData.multimodal.enabled && isVlmOllama.value && formData.multimodal.vlm.modelName) {
        modelsToCheck.push(formData.multimodal.vlm.modelName);
    }
    
    if (modelsToCheck.length === 0) return;
    
    try {
        const result = await checkOllamaModels(modelsToCheck);
        
        // 更新模型状态
        if (formData.llm.source === 'local' && formData.llm.modelName) {
            modelStatus.llm.checked = true;
            modelStatus.llm.available = result.models[formData.llm.modelName] || false;
        }
        
        if (formData.embedding.source === 'local' && formData.embedding.modelName) {
            modelStatus.embedding.checked = true;
            modelStatus.embedding.available = result.models[formData.embedding.modelName] || false;
        }
        
        // 更新VLM模型状态
        if (formData.multimodal.enabled && isVlmOllama.value && formData.multimodal.vlm.modelName) {
            modelStatus.vlm.checked = true;
            modelStatus.vlm.available = result.models[formData.multimodal.vlm.modelName] || false;
        }
    } catch (error) {
        console.error('检查模型状态失败:', error);
    }
};

// 下载模型
const downloadModel = async (type: 'llm' | 'embedding' | 'vlm', modelName: string) => {
    // 防止重复点击
    if (modelStatus[type].downloading) {
        console.log(`模型 ${modelName} 正在下载中，忽略重复点击`);
        return;
    }
    
    console.log(`开始下载模型: ${type} - ${modelName}`);
    
    try {
        // 立即更新状态，防止重复点击
        modelStatus[type].downloading = true;
        modelStatus[type].progress = 0;
        modelStatus[type].message = '正在启动下载...';
        
        // 启动下载任务
        const result = await downloadOllamaModel(modelName);
        
        // 更新任务ID和初始进度
        modelStatus[type].taskId = result.taskId;
        modelStatus[type].progress = result.progress || 0;
        modelStatus[type].message = '下载已开始';
        
        // 如果已经完成，直接更新状态
        if (result.status === 'completed') {
            modelStatus[type].available = true;
            modelStatus[type].downloading = false;
            modelStatus[type].progress = 100;
            modelStatus[type].message = '下载完成';
            MessagePlugin.success(`模型 ${modelName} 下载成功`);
            return;
        }
        
        // 开始轮询进度
        startProgressPolling(type, result.taskId, modelName);
        
    } catch (error) {
        console.error(`启动模型 ${modelName} 下载失败:`, error);
        MessagePlugin.error(`启动模型 ${modelName} 下载失败`);
        modelStatus[type].downloading = false;
        modelStatus[type].message = '下载启动失败';
    }
};

// 开始轮询下载进度
const startProgressPolling = (type: 'llm' | 'embedding' | 'vlm', taskId: string, modelName: string) => {
    // 清除之前的定时器
    if (progressTimers[taskId]) {
        clearInterval(progressTimers[taskId]);
    }
    
    // 每2秒查询一次进度
    progressTimers[taskId] = setInterval(async () => {
        try {
            const task = await getDownloadProgress(taskId);
            
            // 更新模型状态
            modelStatus[type].progress = task.progress;
            modelStatus[type].message = task.message;
            
            // 检查是否完成
            if (task.status === 'completed') {
                modelStatus[type].available = true;
                modelStatus[type].downloading = false;
                modelStatus[type].progress = 100;
                modelStatus[type].message = '下载完成';
                
                // 清除定时器
                clearInterval(progressTimers[taskId]);
                delete progressTimers[taskId];
                
                MessagePlugin.success(`模型 ${modelName} 下载成功`);
                
            } else if (task.status === 'failed') {
                modelStatus[type].downloading = false;
                modelStatus[type].message = task.message || '下载失败';
                
                // 清除定时器
                clearInterval(progressTimers[taskId]);
                delete progressTimers[taskId];
                
                MessagePlugin.error(`模型 ${modelName} 下载失败: ${task.message}`);
            }
            
        } catch (error) {
            console.error('查询下载进度失败:', error);
            // 如果查询失败，停止轮询
            clearInterval(progressTimers[taskId]);
            delete progressTimers[taskId];
            modelStatus[type].downloading = false;
            modelStatus[type].message = '查询进度失败';
        }
    }, 2000);
};

// 配置回填
const loadCurrentConfig = async () => {
    try {
        // 如果是知识库设置模式，先加载知识库基本信息
        if (props.isKbSettings && currentKbId.value) {
            try {
                const kbInfo = await getKnowledgeBaseById(currentKbId.value);
                if (kbInfo && kbInfo.data) {
                    formData.kbName = kbInfo.data.name || '';
                    formData.kbDescription = kbInfo.data.description || '';
                }
            } catch (error) {
                console.error('获取知识库信息失败:', error);
                MessagePlugin.error('获取知识库信息失败');
            }
        }
        
        // 根据是否为知识库设置模式选择不同的API
        if (props.isKbSettings && !currentKbId.value) {
            console.error('知识库设置模式下缺少知识库ID');
            return;
        }
        const config = await getCurrentConfigByKB(currentKbId.value!);
        
        // 设置hasFiles状态
        hasFiles.value = config.hasFiles || false;
        
        // 检查是否已有配置（判断是否为更新模式）
        const hasExistingConfig = config.llm?.modelName || config.embedding?.modelName || config.rerank?.modelName;
        isUpdateMode.value = !!hasExistingConfig;
        
        // 回填表单数据
        if (config.llm) {
            Object.assign(formData.llm, config.llm);
            console.log('LLM config loaded:', formData.llm);
        }
        if (config.embedding) {
            Object.assign(formData.embedding, config.embedding);
            console.log('Embedding config loaded:', formData.embedding);
        }
        if (config.rerank) {
            Object.assign(formData.rerank, config.rerank);
            console.log('Rerank config loaded:', formData.rerank);
        }
        if (config.multimodal) {
            Object.assign(formData.multimodal, config.multimodal);
            // 强制同步 storageType 到单选框
            await nextTick();
            formData.multimodal.storageType = (config.multimodal.storageType || 'minio') as 'minio' | 'cos';
            console.log('Multimodal config loaded:', formData.multimodal);
        }
        if (config.documentSplitting) {
            Object.assign(formData.documentSplitting, config.documentSplitting);
            
            // 根据回填的配置设置预设模式
            const { chunkSize, chunkOverlap } = config.documentSplitting;
            if (chunkSize === 1000 && chunkOverlap === 200) {
                selectedPreset.value = 'balanced';
            } else if (chunkSize === 512 && chunkOverlap === 100) {
                selectedPreset.value = 'precision';
            } else if (chunkSize === 2048 && chunkOverlap === 400) {
                selectedPreset.value = 'context';
            } else {
                selectedPreset.value = 'custom';
            }
        } else {
            // 如果没有文档分割配置，确保使用默认的precision模式
            selectedPreset.value = 'precision';
        }
        if (config.nodeExtract.enabled) {
            formData.nodeExtract.enabled = true;
            formData.nodeExtract.text = config.nodeExtract.text;
            formData.nodeExtract.tags = config.nodeExtract.tags;
            formData.nodeExtract.nodes = config.nodeExtract.nodes;
            formData.nodeExtract.relations = config.nodeExtract.relations;
            tagOptions.value = [];
            for (const tag of config.nodeExtract.tags) {
                if (tagOptions.value.find((item) => item.value === tag)) {
                    continue;
                }
                tagOptions.value.push({ label: tag, value: tag });
            }
        }
        
        // 在配置加载完成后，检查模型状态
        await checkModelsAfterLoading(config);
        
    } catch (error) {
        console.warn('Failed to load current configuration:', error);
        // 如果加载失败，使用默认配置
        isUpdateMode.value = false;
    }
};

// 加载配置后检查模型状态
const checkModelsAfterLoading = async (config: any) => {
    // 延迟一点执行，确保DOM已经更新
    setTimeout(async () => {
        // 检查所有已配置模型的状态
        await checkAllConfiguredModels();
        
        // 检查Rerank模型状态
        if (formData.rerank.enabled && formData.rerank.modelName && formData.rerank.baseUrl) {
            await checkRerankModelStatus();
        }
    }, 300);
};

// 事件处理
const onModelSourceChange = async (type: 'llm' | 'embedding') => {
    // 重置模型状态
    modelStatus[type].checked = false;
    modelStatus[type].available = false;
    modelStatus[type].downloading = false;
    
    // 如果切换到本地，检查Ollama状态
    if (formData[type].source === 'local' && !ollamaStatus.checked) {
        await checkOllama();
    }
};

const onModelNameChange = async (type: 'llm' | 'embedding' | 'vlm') => {
    if (type === 'vlm') {
        // 总是重置VLM模型状态（但不清除下载状态）
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        // 不清除 downloading 状态，避免中断正在进行的下载
        
        if (formData.multimodal.enabled && isVlmOllama.value && formData.multimodal.vlm.modelName) {
            if (ollamaStatus.available) {
                await checkAllOllamaModels();
                
                // 触发表单验证
                setTimeout(() => {
                    form.value?.validate(['multimodal.vlm.modelName']);
                }, 100);
            }
        }
    } else {
        // 总是重置模型状态（但不清除下载状态）
        modelStatus[type].checked = false;
        modelStatus[type].available = false;
        // 不清除 downloading 状态，避免中断正在进行的下载
        
        if (formData[type].source === 'local' && formData[type].modelName) {
            if (ollamaStatus.available) {
                await checkAllOllamaModels();
                
                // 触发表单验证
                setTimeout(() => {
                    form.value?.validate([`${type}.modelName`]);
                }, 100);
            }
        }
    }
};

const onModelNameInput = (type: 'llm' | 'embedding' | 'vlm') => {
    // 清除之前的定时器
    if (inputDebounceTimers[type]) {
        clearTimeout(inputDebounceTimers[type]);
    }
    
    // 重置模型状态（但不清除下载状态）
    if (type === 'vlm') {
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        modelStatus.vlm.message = '';
        // 不清除 downloading 状态，避免中断正在进行的下载
    } else {
        modelStatus[type].checked = false;
        modelStatus[type].available = false;
        modelStatus[type].message = '';
        // 不清除 downloading 状态，避免中断正在进行的下载
    }
    
    // 设置防抖延迟
    inputDebounceTimers[type] = setTimeout(async () => {
        const modelName = type === 'vlm' ? formData.multimodal.vlm.modelName : formData[type].modelName;
        
        // 只有输入了模型名称才进行校验
        if (modelName && modelName.trim()) {
            // 触发表单验证
            form.value?.validate([type === 'vlm' ? 'multimodal.vlm.modelName' : `${type}.modelName`]);
            
            // 如果是远程API，自动检查模型状态
            if (type === 'llm' && formData.llm.source === 'remote' && formData.llm.baseUrl) {
                await checkRemoteModelStatus('llm');
            } else if (type === 'embedding' && formData.embedding.source === 'remote' && formData.embedding.baseUrl) {
                await checkEmbeddingModelStatus();
            } else if (type === 'vlm' && !isVlmOllama.value) {
                // VLM远程API校验可以在这里添加
            }
            
            // 如果是本地模型且Ollama可用，检查模型状态
            if ((type === 'llm' || type === 'embedding') && formData[type].source === 'local' && ollamaStatus.available) {
                await checkAllOllamaModels();
            } else if (type === 'vlm' && isVlmOllama.value && ollamaStatus.available) {
                await checkAllOllamaModels();
            }
        }
    }, 500); // 500ms防抖延迟
};

// 远程配置改变时的处理
const onRemoteConfigChange = async (type: 'llm' | 'embedding') => {
    // 重置模型状态
    modelStatus[type].checked = false;
    modelStatus[type].available = false;
    modelStatus[type].message = '';
    
    // 如果配置完整，检查模型
    if (formData[type].modelName && formData[type].baseUrl) {
        if (type === 'llm') {
            await checkRemoteModelStatus(type);
        } else if (type === 'embedding') {
            await checkEmbeddingModelStatus();
        }
    }
};

// 远程配置输入时的处理
const onRemoteConfigInput = async (type: 'llm' | 'embedding') => {
    // 清除之前的定时器
    const timerKey = `${type}_remote`;
    if (inputDebounceTimers[timerKey]) {
        clearTimeout(inputDebounceTimers[timerKey]);
    }
    
    // 重置模型状态
    modelStatus[type].checked = false;
    modelStatus[type].available = false;
    modelStatus[type].message = '';
    
    // 设置防抖延迟
    inputDebounceTimers[timerKey] = setTimeout(async () => {
        // 只有在有模型名称和Base URL时才进行校验
        if (formData[type].modelName && formData[type].modelName.trim() && 
            formData[type].baseUrl && formData[type].baseUrl.trim()) {
            
            // 触发表单验证
            form.value?.validate([`${type}.modelName`, `${type}.baseUrl`]);
            
            // 自动检查远程API模型状态
            if (type === 'llm') {
                await checkRemoteModelStatus(type);
            } else if (type === 'embedding') {
                await checkEmbeddingModelStatus();
            }
        }
    }, 500); // 500ms防抖延迟
};

// 检查远程模型
const checkRemoteModelStatus = async (type: 'llm') => {
    if (!formData[type].modelName || !formData[type].baseUrl) {
        return;
    }
    
    try {
        modelStatus[type].checking = true;
        modelStatus[type].checked = false;
        modelStatus[type].available = false;
        modelStatus[type].message = '';
        
        const result = await checkRemoteModel({
            modelName: formData[type].modelName,
            baseUrl: formData[type].baseUrl,
            apiKey: formData[type].apiKey
        });
        
        modelStatus[type].checked = true;
        modelStatus[type].available = result.available;
        modelStatus[type].message = result.message || '';
        
        // 触发表单验证
        setTimeout(() => {
            form.value?.validate([`${type}.modelName`]);
        }, 100);
        
    } catch (error) {
        console.error(`检查远程${type}模型失败:`, error);
        modelStatus[type].checked = true;
        modelStatus[type].available = false;
        const err = error as any;
        modelStatus[type].message = (err && err.message) || '网络连接失败';
    } finally {
        modelStatus[type].checking = false;
    }
};

// 检查所有已配置模型的状态
const checkAllConfiguredModels = async () => {
    // 检查LLM模型
    if (formData.llm.source === 'local' && formData.llm.modelName && ollamaStatus.available) {
        await checkAllOllamaModels();
    } else if (formData.llm.source === 'remote' && formData.llm.modelName && formData.llm.baseUrl) {
        await checkRemoteModelStatus('llm');
    }
    
    // 检查Embedding模型（如果没有文件）
    if (!hasFiles.value) {
        if (formData.embedding.source === 'local' && formData.embedding.modelName && ollamaStatus.available) {
            await checkAllOllamaModels();
        } else if (formData.embedding.source === 'remote' && formData.embedding.modelName && formData.embedding.baseUrl) {
            await checkEmbeddingModelStatus();
        }
    }
    
    // 检查VLM模型
    if (formData.multimodal.enabled && formData.multimodal.vlm.modelName) {
        if (isVlmOllama.value && ollamaStatus.available) {
            await checkAllOllamaModels();
        }
        // VLM远程API校验可以在这里添加
    }
};

const onDimensionInput = (event: any) => {
    formData.embedding.dimension = Number(event.target.value);
};

// 检查Embedding模型状态
const checkEmbeddingModelStatus = async () => {
    if (!formData.embedding.modelName) {
        return;
    }
    
    try {
        modelStatus.embedding.checking = true;
        modelStatus.embedding.checked = false;
        modelStatus.embedding.available = false;
        modelStatus.embedding.message = '';
        
        const result = await testEmbeddingModel({
            source: formData.embedding.source as 'local' | 'remote',
            modelName: formData.embedding.modelName,
            baseUrl: formData.embedding.source === 'remote' ? formData.embedding.baseUrl : undefined,
            apiKey: formData.embedding.apiKey || undefined,
            dimension: formData.embedding.dimension || undefined,
        });
        
        modelStatus.embedding.checked = true;
        modelStatus.embedding.available = result.available || false;
        modelStatus.embedding.message = result.message || '';
        
        // 如果检测到维度信息，自动更新
        if (result.available && result.dimension && result.dimension > 0) {
            formData.embedding.dimension = result.dimension;
        }
        
        // 触发表单验证
        setTimeout(() => {
            form.value?.validate(['embedding.modelName']);
        }, 100);
        
    } catch (error) {
        console.error('检查Embedding模型失败:', error);
        modelStatus.embedding.checked = true;
        modelStatus.embedding.available = false;
        const err = error as any;
        modelStatus.embedding.message = (err && err.message) || '检查失败';
    } finally {
        modelStatus.embedding.checking = false;
    }
};

// 检测并自动填写 Embedding 维度
const detectEmbeddingDimension = async () => {
    if (hasFiles.value) return;
    // 校验必填：模型来源 + 模型名称；若远程还需 BaseURL
    const source = (formData.embedding.source || '').trim();
    const modelName = (formData.embedding.modelName || '').trim();
    const baseUrl = (formData.embedding.baseUrl || '').trim();

    if (!source || !modelName || (source === 'remote' && !baseUrl)) {
        // 触发对应字段校验提示
        const fields: string[] = ['embedding.source', 'embedding.modelName'];
        if (source === 'remote') fields.push('embedding.baseUrl');
        try { await form.value?.validate(fields); } catch {}
        MessagePlugin.warning('请先完整填写Embedding配置');
        return;
    }

    embeddingDimDetecting.value = true;
    try {
        const res = await testEmbeddingModel({
            source: source as 'local' | 'remote',
            modelName,
            baseUrl: source === 'remote' ? baseUrl : undefined,
            apiKey: formData.embedding.apiKey || undefined,
            dimension: formData.embedding.dimension || undefined,
        });
        const available = !!res.available;
        const message = res.message || '';
        const dim = Number(res.dimension || 0);
        if (available && dim > 0) {
            formData.embedding.dimension = dim;
            MessagePlugin.success(`检测成功，维度已自动填写为 ${dim}`);
        } else {
            MessagePlugin.error(message || '检测失败');
        }
    } catch (e: any) {
        const msg = e?.message || '检测失败，请检查配置';
        MessagePlugin.error(msg);
    } finally {
        embeddingDimDetecting.value = false;
    }
};

// 第一个handleSubmit函数已删除，使用下面完整的版本

// 停止所有进度轮询
const stopAllProgressPolling = () => {
    Object.keys(progressTimers).forEach(taskId => {
        clearInterval(progressTimers[taskId]);
        delete progressTimers[taskId];
    });
};

// 组件卸载时清理定时器
onUnmounted(() => {
    stopAllProgressPolling();
    
    // 清理输入防抖定时器
    Object.keys(inputDebounceTimers).forEach(key => {
        if (inputDebounceTimers[key]) {
            clearTimeout(inputDebounceTimers[key]);
            delete inputDebounceTimers[key];
        }
    });
    
    // 清理提交防抖定时器
    if (submitDebounceTimer.value) {
        clearTimeout(submitDebounceTimer.value);
        submitDebounceTimer.value = null;
    }
});

// 监听表单变化
watch(() => formData.llm.source, () => onModelSourceChange('llm'));
watch(() => formData.embedding.source, () => onModelSourceChange('embedding'));

// 监听路由参数变化，当知识库ID变化时重新加载配置
watch(() => route.params.kbId, async (newKbId, oldKbId) => {
    // 只有在知识库设置模式下且ID确实发生变化时才重新加载
    if (props.isKbSettings && newKbId && newKbId !== oldKbId) {
        console.log('知识库ID变化，重新加载配置:', { oldKbId, newKbId });
        await loadCurrentConfig();
        await checkAllConfiguredModels();
    }
}, { immediate: false });

// 添加缺失的函数
const onRerankChange = () => {
    console.log('Rerank enabled:', formData.rerank.enabled);
};

const onMultimodalChange = async () => {
    if (formData.multimodal.enabled) {
        // 设置默认的VLM接口类型
        if (!formData.multimodal.vlm.interfaceType) {
            formData.multimodal.vlm.interfaceType = 'ollama';
        }
        
        // 如果是Ollama接口，设置默认的Base URL
        if (formData.multimodal.vlm.interfaceType === 'ollama' && !formData.multimodal.vlm.baseUrl) {
            formData.multimodal.vlm.baseUrl = 'http://localhost:11434/v1';
        }
        
        // 如果选择了Ollama接口，检查Ollama状态
        if (formData.multimodal.vlm.interfaceType === 'ollama' && !ollamaStatus.checked) {
            await checkOllama();
        }
    } else {
        // 如果禁用多模态，重置VLM模型状态
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        modelStatus.vlm.downloading = false;
        modelStatus.vlm.message = '';
    }
};

const onPresetChange = () => {
    if (selectedPreset.value === 'balanced') {
        formData.documentSplitting.chunkSize = 1000;
        formData.documentSplitting.chunkOverlap = 200;
        formData.documentSplitting.separators = ['\n\n', '\n', '。', '！', '？', ';', '；'];
    } else if (selectedPreset.value === 'precision') {
        formData.documentSplitting.chunkSize = 512;
        formData.documentSplitting.chunkOverlap = 100;
        formData.documentSplitting.separators = ['\n\n', '\n', '。', '！', '？', ';', '；'];
    } else if (selectedPreset.value === 'context') {
        formData.documentSplitting.chunkSize = 2048;
        formData.documentSplitting.chunkOverlap = 400;
        formData.documentSplitting.separators = ['\n\n', '\n', '。', '！', '？', ';', '；'];
    }
};

const onRerankConfigChange = async () => {
    // 重置模型状态
    modelStatus.rerank.checked = false;
    modelStatus.rerank.available = false;
    modelStatus.rerank.message = '';
    
    // 如果配置完整，检查模型
    if (formData.rerank.modelName && formData.rerank.baseUrl) {
        await checkRerankModelStatus();
    }
};

const onRerankConfigInput = async () => {
    // 清除之前的定时器
    const timerKey = 'rerank_remote';
    if (inputDebounceTimers[timerKey]) {
        clearTimeout(inputDebounceTimers[timerKey]);
    }
    
    // 重置模型状态
    modelStatus.rerank.checked = false;
    modelStatus.rerank.available = false;
    modelStatus.rerank.message = '';
    
    // 设置防抖延迟
    inputDebounceTimers[timerKey] = setTimeout(async () => {
        // 只有在有模型名称和Base URL时才进行校验
        if (formData.rerank.modelName && formData.rerank.modelName.trim() && 
            formData.rerank.baseUrl && formData.rerank.baseUrl.trim()) {
            
            // 触发表单验证
            form.value?.validate(['rerank.modelName', 'rerank.baseUrl']);
            
            // 自动检查Rerank模型状态
            await checkRerankModelStatus();
        }
    }, 500); // 500ms防抖延迟
};

const checkRerankModelStatus = async () => {
    if (!formData.rerank.modelName || !formData.rerank.baseUrl) {
        return;
    }
    
    try {
        modelStatus.rerank.checking = true;
        modelStatus.rerank.checked = false;
        modelStatus.rerank.available = false;
        modelStatus.rerank.message = '';
        
        const result = await checkRerankModel({
            modelName: formData.rerank.modelName,
            baseUrl: formData.rerank.baseUrl,
            apiKey: formData.rerank.apiKey
        });
        
        modelStatus.rerank.checked = true;
        modelStatus.rerank.available = result.available;
        modelStatus.rerank.message = result.message || '';
        
        // 触发表单验证
        setTimeout(() => {
            form.value?.validate(['rerank.modelName']);
        }, 100);
        
    } catch (error) {
        console.error('检查Rerank模型失败:', error);
        modelStatus.rerank.checked = true;
        modelStatus.rerank.available = false;
        const err = error as any;
        modelStatus.rerank.message = (err && err.message) || '网络连接失败';
    } finally {
        modelStatus.rerank.checking = false;
    }
};

// 添加一些基本的多模态函数（简化版）
const onVlmInterfaceTypeChange = async () => {
    console.log('VLM interface type changed:', formData.multimodal.vlm.interfaceType);
    
    // 重置VLM模型状态
    modelStatus.vlm.checked = false;
    modelStatus.vlm.available = false;
    modelStatus.vlm.downloading = false;
    modelStatus.vlm.message = '';
    
    // 如果切换到Ollama接口，检查Ollama状态
    if (formData.multimodal.vlm.interfaceType === 'ollama') {
        if (!ollamaStatus.checked) {
            await checkOllama();
        }
        
        // 如果已经有模型名称，立即检查模型状态
        if (formData.multimodal.vlm.modelName && ollamaStatus.available) {
            await checkAllOllamaModels();
        }
    }
};

const onVlmBaseUrlChange = async () => {
    console.log('VLM base URL changed:', formData.multimodal.vlm.baseUrl);
};

const onVlmBaseUrlInput = () => {
    console.log('VLM base URL input');
};

const onVlmApiKeyChange = () => {
    console.log('VLM API key changed');
};

const onCosConfigChange = () => {
    console.log('COS config changed');
};

const onStorageTypeChange = () => {
    console.log('Storage type changed:', formData.multimodal.storageType);
};

const onImageChange = (files: any) => {
    if (files && files.length > 0) {
        // 检查多模态配置是否完整
        const missingConfigs: string[] = [];
        
        // 根据存储类型检查必填项
        if (formData.multimodal.storageType === 'cos') {
            if (!formData.multimodal.cos.secretId) missingConfigs.push('COS Secret ID');
            if (!formData.multimodal.cos.secretKey) missingConfigs.push('COS Secret Key');
            if (!formData.multimodal.cos.region) missingConfigs.push('COS Region');
            if (!formData.multimodal.cos.bucketName) missingConfigs.push('COS Bucket Name');
            if (!formData.multimodal.cos.appId) missingConfigs.push('COS App ID');
        } else if (formData.multimodal.storageType === 'minio') {
            if (!formData.multimodal.minio.bucketName) missingConfigs.push('MinIO Bucket Name');
        }
        
        // 检查VLM配置
        if (!formData.multimodal.vlm.modelName) {
            missingConfigs.push('VLM 模型名称');
        }
        
        // 如果是OpenAI兼容接口，还需要检查Base URL
        if (formData.multimodal.vlm.interfaceType === 'openai' && !formData.multimodal.vlm.baseUrl) {
            missingConfigs.push('VLM Base URL');
        }
        
        if (missingConfigs.length > 0) {
            const missingList = missingConfigs.join('、');
            MessagePlugin.error(`多模态配置不完整，请先完成多模态配置后再上传图片`);
            return;
        }
        
        const file = files[0].raw || files[0];
        multimodalTest.selectedFile = file;
        
        // 创建预览URL
        if (multimodalTest.previewUrl) {
            URL.revokeObjectURL(multimodalTest.previewUrl);
        }
        multimodalTest.previewUrl = URL.createObjectURL(file);
        
        // 清除之前的测试结果
        multimodalTest.result = null;
        
        MessagePlugin.success('图片上传成功，可以开始测试');
    }
};

const startMultimodalTest = async () => {
    if (!multimodalTest.selectedFile) {
        MessagePlugin.warning('请先选择一张图片');
        return;
    }
    
    multimodalTest.testing = true;
    multimodalTest.result = null;
    
    try {
        // 准备API参数
        const apiParams: {
            image: File;
            vlm_model: string;
            vlm_base_url: string;
            vlm_api_key: string;
            vlm_interface_type: string;
            storage_type?: 'cos' | 'minio';
            cos_secret_id?: string;
            cos_secret_key?: string;
            cos_region?: string;
            cos_bucket_name?: string;
            cos_app_id?: string;
            cos_path_prefix?: string;
            minio_bucket_name?: string;
            minio_use_ssl?: boolean;
            minio_path_prefix?: string;
            chunk_size: number;
            chunk_overlap: number;
            separators: string[];
        } = {
            image: multimodalTest.selectedFile,
            vlm_model: formData.multimodal.vlm.modelName,
            vlm_base_url: '',  // 将在下方设置
            vlm_api_key: '',   // 将在下方设置
            vlm_interface_type: formData.multimodal.vlm.interfaceType,
            chunk_size: formData.documentSplitting.chunkSize,
            chunk_overlap: formData.documentSplitting.chunkOverlap,
            separators: formData.documentSplitting.separators
        };

        // 根据存储类型附带不同的参数
        if (formData.multimodal.storageType === 'cos') {
            apiParams.storage_type = 'cos';
            apiParams.cos_secret_id = formData.multimodal.cos.secretId;
            apiParams.cos_secret_key = formData.multimodal.cos.secretKey;
            apiParams.cos_region = formData.multimodal.cos.region;
            apiParams.cos_bucket_name = formData.multimodal.cos.bucketName;
            apiParams.cos_app_id = formData.multimodal.cos.appId;
            apiParams.cos_path_prefix = formData.multimodal.cos.pathPrefix || '';
        } else if (formData.multimodal.storageType === 'minio') {
            apiParams.storage_type = 'minio';
            apiParams.minio_bucket_name = formData.multimodal.minio.bucketName;
            apiParams.minio_use_ssl = formData.multimodal.minio.useSSL;
            apiParams.minio_path_prefix = formData.multimodal.minio.pathPrefix;
        }
        
        // 如果是OpenAI兼容接口，设置baseUrl和apiKey
        if (formData.multimodal.vlm.interfaceType === 'openai') {
            apiParams.vlm_base_url = formData.multimodal.vlm.baseUrl;
            apiParams.vlm_api_key = formData.multimodal.vlm.apiKey || '';
        } else {
            // Ollama接口使用默认的本地URL
            apiParams.vlm_base_url = 'http://localhost:11434/v1';
            apiParams.vlm_api_key = '';
        }
        
        const result = await testMultimodalFunction(apiParams);
        
        multimodalTest.result = result;
        
        if (result.success) {
            MessagePlugin.success('多模态测试成功');
        } else {
            MessagePlugin.error(`多模态测试失败: ${result.message}`);
        }
    } catch (error) {
        console.error('多模态测试失败:', error);
        multimodalTest.result = {
            success: false,
            message: (error as any)?.message || '测试过程中发生错误'
        };
        MessagePlugin.error('多模态测试失败');
    } finally {
        multimodalTest.testing = false;
    }
};

// 处理按钮提交
const handleSubmit = async () => {
    // 防止重复提交和防抖
    if (submitting.value || isSubmitDebounced.value) {
        return;
    }

    // 启动防抖机制
    isSubmitDebounced.value = true;
    if (submitDebounceTimer.value) {
        clearTimeout(submitDebounceTimer.value);
    }
    submitDebounceTimer.value = setTimeout(() => {
        isSubmitDebounced.value = false;
    }, 3000); // 3秒防抖

    try {
        // 表单验证
        const isValid = await form.value?.validate();
        if (!isValid) {
            MessagePlugin.error('请检查表单填写是否正确');
            return;
        }

        submitting.value = true;
        
        // 如果是知识库设置模式，先更新知识库基本信息
        if (props.isKbSettings && currentKbId.value) {
            try {
                const { updateKnowledgeBase } = await import('@/api/knowledge-base');
                await updateKnowledgeBase(currentKbId.value, {
                    name: formData.kbName,
                    description: formData.kbDescription,
                    config: {} // 空的config对象，因为这里只更新基本信息
                });
            } catch (error) {
                console.error('更新知识库基本信息失败:', error);
                MessagePlugin.error('更新知识库基本信息失败');
                return;
            }
        }
        
        // 确保embedding.dimension是数字类型
        if (formData.embedding.dimension) {
            formData.embedding.dimension = Number(formData.embedding.dimension);
        }
        
        // 根据是否为知识库设置模式选择不同的API
        if (props.isKbSettings && !currentKbId.value) {
            console.error('知识库设置模式下缺少知识库ID');
            MessagePlugin.error('知识库ID缺失，无法保存配置');
            return;
        }
        const result = await initializeSystemByKB(currentKbId.value!, formData);
        
        if (result.success) {
            MessagePlugin.success(props.isKbSettings ? '知识库设置更新成功' : (isUpdateMode.value ? '配置更新成功' : '系统初始化完成'));
            
            // 根据不同模式进行跳转
            if (props.isKbSettings && currentKbId.value) {
                // 知识库设置模式，跳转回知识库详情页面
                setTimeout(() => {
                    router.push(`/platform/knowledge-bases/${currentKbId.value}`);
                }, 1500);
            } else if (!isUpdateMode.value) {
                // 初始化模式，跳转到知识库列表页面
                setTimeout(() => {
                    router.push('/platform/knowledge-bases');
                }, 1500);
            }
        } else {
            MessagePlugin.error(result.message || '操作失败');
        }
    } catch (error: any) {
        console.error('提交失败:', error);
        MessagePlugin.error(error.message || '操作失败，请检查网络连接');
    } finally {
        submitting.value = false;
        
        // 清理防抖定时器
        if (submitDebounceTimer.value) {
            clearTimeout(submitDebounceTimer.value);
            submitDebounceTimer.value = null;
        }
        isSubmitDebounced.value = false;
    }
};

const formatFileSize = (bytes: number): string => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
};

const addTag = async (val: string) => {
    val = val.trim();
    if (val === '') {
        MessagePlugin.error('请输入有效的标签');
        return;
    }
    if (!tagOptions.value.find(item => item.value === val)){
        tagOptions.value.push({ label: val, value: val });
    }
    if (!formData.nodeExtract.tags.includes(val)) {
        formData.nodeExtract.tags.push(val);
    }else {
        MessagePlugin.error('该标签已存在');
    }
    tagInput.value = '';
}

const clearTags = async () => {
    formData.nodeExtract.tags = [];
}

const defaultExtractExample = async () => {
    formData.nodeExtract.tags = ['作者', '别名'];
    formData.nodeExtract.text = `《红楼梦》，又名《石头记》，是清代作家曹雪芹创作的中国古典四大名著之一，被誉为中国封建社会的百科全书。该书前80回由曹雪芹所著，后40回一般认为是高鹗所续。小说以贾、史、王、薛四大家族的兴衰为背景，以贾宝玉、林黛玉和薛宝钗的爱情悲剧为主线，刻画了以贾宝玉和金陵十二钗为中心的正邪两赋、贤愚并出的高度复杂的人物群像。成书于乾隆年间（1743年前后），是中国文学史上现实主义的高峰，对后世影响深远。`;
    formData.nodeExtract.nodes = [
        {name: '红楼梦', attributes: ['中国古典四大名著之一', '又名《石头记》', '被誉为中国封建社会的百科全书']},
        {name: '石头记', attributes: ['《红楼梦》的别名']},
        {name: '曹雪芹', attributes: ['清代作家', '《红楼梦》前 80 回的作者']},
        {name: '高鹗', attributes: ['一般认为是《红楼梦》后 40 回的续写者']}
    ];
    formData.nodeExtract.relations = [
        {node1: '红楼梦', node2: '石头记', type: '别名'},
        {node1: '红楼梦', node2: '曹雪芹', type: '作者'},
        {node1: '红楼梦', node2: '高鹗', type: '作者'}
    ];
    tagOptions.value = [];
    tagOptions.value.push({ label: '作者', value: '作者' });
    tagOptions.value.push({ label: '别名', value: '别名' });
    popupVisibleNode1.value = Array(formData.nodeExtract.nodes.length).fill(false);
    popupVisibleNode2.value = Array(formData.nodeExtract.nodes.length).fill(false);
}

const clearExtractExample = async () => {
    formData.nodeExtract.tags = [];
    formData.nodeExtract.text = '';
    formData.nodeExtract.nodes = [];
    formData.nodeExtract.relations = [];
    tagOptions.value = [...tagOptionsDefault];
    popupVisibleNode1.value = [];
    popupVisibleNode2.value = [];
}

const addNode = async () =>{
    formData.nodeExtract.nodes.push({
        name: '',
        attributes: []
    });
}
        
const removeNode = async (index: number) => {
    formData.nodeExtract.nodes.splice(index, 1);
}

const addAttribute = async (nodeIndex: number) => {
    formData.nodeExtract.nodes[nodeIndex].attributes.push('');
}

const removeAttribute = async(nodeIndex: number, attrIndex: number) => {
    formData.nodeExtract.nodes[nodeIndex].attributes.splice(attrIndex, 1);
}

const onRelationNode1OptionClick = async (index: number, item: Node) => {
    formData.nodeExtract.relations[index].node1 = item.name;
    popupVisibleNode1.value[index] = false;
}

const onRelationNode2OptionClick = async (index: number, item: Node) => {
    formData.nodeExtract.relations[index].node2 = item.name;
    popupVisibleNode2.value[index] = false;
}

const relationOnClearNode1 = async (index: number) => {
    formData.nodeExtract.relations[index].node1 = '';
}

const relationOnClearNode2 = async (index: number) => {
    formData.nodeExtract.relations[index].node2 = '';
}

const onPopupVisibleNode1Change = async (index: number, val: boolean) => {
    popupVisibleNode1.value[index] = val;
};

const onPopupVisibleNode2Change = async (index: number, val: boolean) => {
    popupVisibleNode2.value[index] = val;
};

const addRelation = async () => {
    formData.nodeExtract.relations.push({
        node1: '',
        node2: '',
        type: ''
    });
    popupVisibleNode1.value.push(false);
    popupVisibleNode2.value.push(false);
}

const removeRelation = async (index: number) => {
    formData.nodeExtract.relations.splice(index, 1);
}

const onFocus = async () => {};

const canExtract = async (): Promise<boolean> =>{
    if (formData.nodeExtract.text === '') {
        MessagePlugin.error('请输入示例文本');
        return false;
    }
    if (formData.nodeExtract.tags.length === 0) {
        MessagePlugin.error('请输入关系类型');
        return false;
    }
    for (let i = 0; i < formData.nodeExtract.tags.length; i++) {
        if (formData.nodeExtract.tags[i] === '') {
            MessagePlugin.error('请输入关系类型');
            return false;
        }
    }
    if (!modelStatus.llm.available) {
        MessagePlugin.error('请输入 LLM 大语言模型配置');
        return false;
    }
    return true;
}

const readyNode = computed(() => {
    for (let i = 0; i < formData.nodeExtract.nodes.length; i++) {
        let node = formData.nodeExtract.nodes[i];
        if (node.name === '') {
            return false;
        }
        if (node.attributes){
            for (let j = 0; j < node.attributes.length; j++) {
                if (node.attributes[j] === '') {
                    return false;
                }
            }
        }
    }
    return formData.nodeExtract.nodes.length > 0;
})

const readyRelation = computed(() => {
    for (let i = 0; i < formData.nodeExtract.relations.length; i++) {
        let relation = formData.nodeExtract.relations[i];
        if (relation.node1 == '' || relation.node2 == '' || relation.type == '' ) {
            return false
        }
    }
    return formData.nodeExtract.relations.length > 0;
})

// 处理提取
const handleExtract = async () => {
    if (extracting.value) return;

    try {
        // 表单验证
        const isValid = await form.value?.validate();
        if (!isValid) {
            MessagePlugin.error('请检查表单填写是否正确');
            return;
        }
        if (!canExtract()){
            return;
        }

        extracting.value = true;

        const request: TextRelationExtractionRequest = {
            text: formData.nodeExtract.text.trim(),
            tags: formData.nodeExtract.tags,
            llmConfig: {
                source: formData.llm.source as 'local' | 'remote',
                modelName: formData.llm.modelName,
                baseUrl: formData.llm.baseUrl,
                apiKey: formData.llm.apiKey,
            },
        };

        const result = await extractTextRelations(request);
        if (result.nodes.length === 0 ) {
            MessagePlugin.info('未提取有效节点');
        } else {
            formData.nodeExtract.nodes = result.nodes;
        }
        if ( result.relations.length === 0) {
            MessagePlugin.info('未提取有效关系');
        } else {
            formData.nodeExtract.relations = result.relations;
        }
    } catch (error) {
        console.error('文本内容关系提取失败:', error);
        MessagePlugin.error('提取失败，请检查网络连接或文本内容格式');
    } finally {
        extracting.value = false;
    }
};

// 处理标签
const handleFabriTag = async () => {
    if (tagFabring.value) return;

    try {
        // 表单验证
        const isValid = await form.value?.validate();
        if (!isValid) {
            MessagePlugin.error('请检查表单填写是否正确');
            return;
        }

        tagFabring.value = true;

        const request: FabriTagRequest = {
            llmConfig: {
                source: formData.llm.source as 'local' | 'remote',
                modelName: formData.llm.modelName,
                baseUrl: formData.llm.baseUrl,
                apiKey: formData.llm.apiKey,
            },
        };

        const result = await fabriTag(request);
        formData.nodeExtract.tags = result.tags;
        tagOptions.value = [];
        for (let i = 0; i < result.tags.length; i++) {
            tagOptions.value.push({ label: result.tags[i], value: result.tags[i] });
        }

    } catch (error) {
        console.error('随机生成标签:', error);
        MessagePlugin.error('生成失败，请重试');
    } finally {
        tagFabring.value = false;
    }
};

// 处理示例文本
const handleFabriText = async () => {
    if (textFabring.value) return;

    try {
        // 表单验证
        const isValid = await form.value?.validate();
        if (!isValid) {
            MessagePlugin.error('请检查表单填写是否正确');
            return;
        }

        textFabring.value = true;

        const request: FabriTextRequest = {
            tags: formData.nodeExtract.tags,
            llmConfig: {
                source: formData.llm.source as 'local' | 'remote',
                modelName: formData.llm.modelName,
                baseUrl: formData.llm.baseUrl,
                apiKey: formData.llm.apiKey,
            },
        };

        const result = await fabriText(request);
        formData.nodeExtract.text = result.text;
    } catch (error) {
        console.error('生成示例文本失败:', error);
        MessagePlugin.error('生成失败，请重试');
    } finally {
        textFabring.value = false;
    }
};


// 组件挂载时检查Ollama状态
onMounted(async () => {
    // 加载当前配置
    await loadCurrentConfig();

    // 总是检查Ollama状态，因为这是独立于具体配置的
    await refreshOllamaSummary();
    
    // 检查已配置模型状态
    await checkAllConfiguredModels();
});
</script>

<style lang="less" scoped>
.settings-page-container {
    width: 100%;
    height: 100vh;
    overflow-y: auto;
    background-color: #f5f7fa;
    padding: 24px;
    box-sizing: border-box;
}

.initialization-content {
    max-width: 1200px;
    margin: 0 auto;
    padding: 0;
    
    .ollama-summary-card {
        margin-bottom: 24px;
        background: #ffffff;
        border: 1px solid #e9edf5;
        border-radius: 12px;
        box-shadow: 0 8px 24px rgba(15, 23, 42, 0.05);
        padding: 16px 20px;

        .summary-header {
            display: flex;
            align-items: center;
            gap: 8px;
            margin-bottom: 12px;

            .title {
                display: inline-flex;
                align-items: center;
                gap: 6px;
                font-weight: 600;
                color: #1f2937;
                font-size: 16px;
            }
            .state{ margin-left: 8px; }
            .refresh-icon{
                margin-left: 8px;
                cursor: pointer;
                color: #6b7280;
                transition: transform .2s ease, color .2s ease;
            }
            .refresh-icon:hover{ color: #0ea5e9; }
            .refresh-icon.spinning{ animation: spin 1s linear infinite; }
        }

        .summary-body {
            .models {
                display: flex;
                align-items: flex-start;
                gap: 12px;
                margin-bottom: 12px;
                
                &:last-child {
                    margin-bottom: 0;
                }
                
                .label { 
                    color: #6b7280; 
                    font-size: 14px; 
                    margin-top: 2px; 
                    min-width: 100px;
                }
                .model-list { 
                    display: flex; 
                    gap: 8px; 
                    flex-wrap: wrap; 
                }
                .model-pill { 
                    border-radius: 8px; 
                }
                .empty { 
                    color: #9ca3af; 
                    font-size: 14px; 
                }
            }
        }
    }
    
    .config-section {
        margin-bottom: 32px;
        background: #fff;
        border: 1px solid #eef4f0;
        border-radius: 12px;
        box-shadow: 0 6px 18px rgba(7, 192, 95, 0.04);
        padding: 24px;

        h3 {
            display: flex;
            align-items: center;
            gap: 8px;
            margin: 0 0 20px;
            font-size: 18px;
            font-weight: 600;
            color: #0f172a;
            
            .section-icon {
                color: #07c05f;
                font-size: 20px;
            }
        }

        .add-tag-container {
            display: flex;
            align-items: center; /* 垂直居中 */
            justify-content: flex-start; /* 水平起始对齐 */
            gap: 8px;
        }
        .extract-button {
            display: flex;
            justify-content: center;
            align-items: center;
            gap: 12px;
            text-align: center;
        }

        .node-list {
            display: flex;
            flex-wrap: wrap;
            gap: 12px;
        }

        .node-header {
            display: flex;
            align-items: center;
            justify-content: flex-start;
            gap: 4px;
            margin-bottom: 8px;
        }

        .attribute-item {
            display: flex;
            align-items: center;
            justify-content: flex-start;
            margin-bottom: 4px;
        }

        .relation-line {
            display: flex;
            align-items: center;
            justify-content: flex-start;
            gap: 4px;
        }

        .relation-arrow {
            font-size: 50px;
        }

        .sample-text-form {
            display: flex;
            flex-direction: column;
            width: 100%;
        }
    }

    .btn-tips-form {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-bottom: 12px;

        .btn-tips {
            display: flex;
            align-items: center;
            justify-content: center;
            color: #fa8c16;
            
            .tip-icon {
                margin-right: 6px;
            }
        }
    }

    .form-row {
        margin-bottom: 20px;
    }

    .model-input-with-status {
        display: flex;
        gap: 8px;
        align-items: center;
    }
    
    .model-input-with-status :deep(.t-input) {
        flex: 1;
        min-width: 300px;
    }
    
    .model-status-icon {
        flex-shrink: 0;
        min-width: 24px;
        display: flex;
        align-items: center;
        justify-content: center;
    }
    
    .model-status-icon .status-icon {
        font-size: 16px;
        cursor: help;
        padding: 4px;
        border-radius: 50%;
        transition: all 0.2s ease;
    }
    
    .model-status-icon .status-icon.installed {
        color: #16a34a;
        background: #f0fdf4;
        border: 1px solid #bbf7d0;
    }
    
    .model-status-icon .status-icon.not-installed {
        color: #dc2626;
        background: #fef2f2;
        border: 1px solid #fecaca;
    }
    
    .model-status-icon .status-icon.unknown {
        color: #d97706;
        background: #fffbeb;
        border: 1px solid #fed7aa;
    }
    
    .model-status-icon .status-icon.downloading {
        color: #16a34a;
        background: #f0fdf4;
        border: 1px solid #bbf7d0;
        animation: pulse 2s infinite;
    }

    .download-action {
        flex-shrink: 0;
    }
    
    .download-action .download-btn {
        height: 24px;
        width: 24px;
        min-width: 24px;
        padding: 0;
        border-radius: 4px;
        display: flex;
        align-items: center;
        justify-content: center;
        background: #16a34a;
        border: 1px solid #15803d;
        color: white;
        transition: all 0.2s ease;
        box-shadow: 0 1px 3px rgba(22, 163, 74, 0.2);
        
        &:hover:not(:disabled) {
            background: #15803d;
            border-color: #166534;
            box-shadow: 0 2px 6px rgba(22, 163, 74, 0.3);
            transform: translateY(-1px);
        }
        
        &:disabled {
            background: #e5e7eb;
            color: #9ca3af;
            cursor: not-allowed;
            box-shadow: none;
        }
        
        .t-icon {
            font-size: 14px;
        }
    }

    .model-download-status {
        display: inline-flex;
        align-items: center;
        gap: 6px;
        
        .download-progress-circle {
            flex-shrink: 0;
        }
        
        .download-percentage {
            font-size: 11px;
            font-weight: 600;
            color: #15803d;
            font-family: 'SF Mono', 'Monaco', 'Inconsolata', 'Roboto Mono', monospace;
            white-space: nowrap;
        }
    }

    .remote-config {
        margin-top: 20px;
        // padding: 20px;
        // background: #f9fcff;
        // border-radius: 12px;
        // border: 1px solid #edf2f7;
    }

    .url-input-with-check {
        display: flex;
        align-items: center;
        gap: 8px;
        width: 100%;
    }

    .url-input-with-check :deep(.t-input) {
        flex: 1;
        min-width: 0;
        width: 100%;
    }

    .check-action {
        flex-shrink: 0;
        display: flex;
        align-items: center;
        justify-content: center;
        min-width: 24px;
    }
    
    .check-action .status-icon,
    .check-action .input-icon {
        font-size: 18px;
        cursor: help;
        color: #666;
    }
    
    .check-action .status-icon.installed,
    .check-action .input-icon.installed {
        color: #00a870;
    }
    
    .check-action .status-icon.not-installed,
    .check-action .input-icon.not-installed {
        color: #e34d59;
    }
    
    .check-action .status-icon.checking,
    .check-action .input-icon.checking {
        color: #0052d9;
    }

    .error-message {
        display: flex;
        align-items: center;
        gap: 8px;
        margin-top: 12px;
        padding: 12px 16px;
        background-color: #fff2f0;
        border: 1px solid #ffccc7;
        border-radius: 8px;
        color: #d4380d;
        font-size: 14px;
        
        .t-icon {
            color: #d4380d;
            font-size: 16px;
            flex-shrink: 0;
        }
        
        span {
            line-height: 1.4;
        }
    }

    .dimension-input-with-action {
        display: flex;
        align-items: center;
        gap: 12px;
    }
    .dimension-input-with-action :deep(.t-input) {
        flex: 0 0 auto;
    }
    .detect-dim-btn {
        flex: 0 0 auto;
    }

    .embedding-warning {
        margin-bottom: 20px;
        
        :deep(.t-alert) {
            background-color: #fffbe6;
            border-color: #ffe58f;
            border-radius: 8px;
            
            .t-alert__message {
                color: #d48806;
                font-weight: 500;
            }
            
            .t-alert__icon {
                color: #faad14;
            }
        }
    }

    .rerank-config, .multimodal-config, .node-config {
        // margin-top: 20px;
        // padding: 20px;
        // background: #f9fcff;
        // border-radius: 12px;
        // border: 1px solid #edf2f7;
        
        h4 {
            color: #333;
            font-size: 16px;
            font-weight: 500;
            margin: 20px 0 15px 0;
            padding-left: 12px;
            border-left: 3px solid #07c05f;
        }
    }

    .switch-container {
        display: flex;
        align-items: center;
        justify-content: flex-start;
        margin-left: 0;

        .switch-label {
            margin-left: 10px;
            font-size: 14px;
            color: #333;
            font-weight: 500;
        }
    }

    .preset-row {
        margin-bottom: 30px;
        
        .preset-radio-group {
            display: grid;
            grid-template-columns: repeat(2, 1fr);
            gap: 15px;
            
            :deep(.t-radio) {
                border: 2px solid #e8f5e8;
                border-radius: 8px;
                padding: 15px;
                transition: all 0.3s ease;
                cursor: pointer;
                background: white;
                
                &:hover {
                    border-color: #07c05f;
                    background-color: #f8fdf8;
                    box-shadow: 0 4px 12px rgba(7, 192, 95, 0.1);
                }
                
                &.t-is-checked {
                    border-color: #07c05f;
                    background: linear-gradient(135deg, #07c05f, #00a651);
                    color: white;
                    transform: translateY(-2px);
                    box-shadow: 0 6px 20px rgba(7, 192, 95, 0.2);
                    
                    .preset-title {
                        color: white !important;
                    }
                    
                    .preset-desc {
                        color: rgba(255, 255, 255, 0.9) !important;
                    }
                }
                
                .preset-content {
                    display: flex;
                    flex-direction: column;
                    align-items: flex-start;
                    width: 100%;
                }
                
                .preset-title {
                    font-weight: 600;
                    font-size: 14px;
                    color: #333;
                    margin-bottom: 4px;
                }
                
                .preset-desc {
                    font-size: 12px;
                    color: #666;
                    line-height: 1.4;
                }
            }
        }
    }

    .parameters-grid {
        display: flex;
        flex-direction: column;
        gap: 24px;
        margin-top: 20px;
        padding: 20px 16px;
        background: #fff;
        border-radius: 12px;
        border: 1px solid #edf2f7;
        box-shadow: 0 6px 16px rgba(15, 23, 42, 0.04);
        
        &.disabled-grid {
            opacity: 0.6;
            pointer-events: none;
            background-color: #f8f9fa;
            border-color: #ddd;
        }
        
        .parameter-group {
            .parameter-label {
                font-weight: 600;
                color: #333;
                margin-bottom: 12px;
                font-size: 14px;
            }
            
            .parameter-control {
                display: flex;
                align-items: center;
                gap: 12px;
                margin-bottom: 20px;
                
                .parameter-slider {
                    flex: 1;
                    
                    :deep(.t-slider__rail) {
                        background-color: #e8f5e8;
                    }
                    
                    :deep(.t-slider__track) {
                        background-color: #07c05f;
                    }
                    
                    :deep(.t-slider__handle) {
                        background-color: #07c05f;
                        border-color: #07c05f;
                    }
                }

                .parameter-value {
                    font-size: 14px;
                    color: #666;
                    font-weight: 500;
                    min-width: 80px;
                    text-align: right;
                    white-space: nowrap;
                }

                .parameter-select {
                    flex: 1;
                }
            }
            
            .parameter-desc {
                font-size: 12px;
                color: #999;
                line-height: 1.4;
                margin-top: 8px;
            }
        }
    }

    .multimodal-test {
        // margin-top: 20px;
        // padding: 20px;
        // background: #f8fff9;
        // border-radius: 10px;
        // border: 1px solid #e8f5e8;
        
        h5 {
            font-size: 16px;
            color: #07c05f;
            margin-bottom: 8px;
            font-weight: 600;
        }
        
        .test-desc {
            margin-bottom: 16px;
            font-size: 13px;
            color: #6b7280;
            line-height: 1.4;
        }
        
        .test-area {
            display: flex;
            flex-direction: column;
            gap: 16px;
        }
        
        .upload-section {
            display: flex;
            flex-direction: column;
            gap: 12px;
            
            .upload-buttons {
                display: flex;
                align-items: center;
                gap: 12px;
                flex-wrap: wrap;
            }
        }
        
        .image-preview {
            // text-align: center;
            // padding: 20px;
            // background: white;
            // border-radius: 10px;
            // border: 1px solid #e8f5e8;
            // box-shadow: 0 2px 8px rgba(7, 192, 95, 0.08);
            
            img {
                max-width: 100%;
                max-height: 200px;
                object-fit: contain;
                border-radius: 8px;
                margin-bottom: 12px;
                border: 1px solid #f0f0f0;
            }
            
            .image-meta {
                display: flex;
                justify-content: space-between;
                align-items: center;
                padding: 8px 12px;
                background: #f8fff9;
                border-radius: 6px;
                border: 1px solid #e8f5e8;
                
                .file-name {
                    font-weight: 500;
                    text-overflow: ellipsis;
                    overflow: hidden;
                    white-space: nowrap;
                    max-width: 70%;
                    color: #333;
                    font-size: 13px;
                }
                
                .file-size {
                    color: #6b7280;
                    font-size: 12px;
                    font-weight: 500;
                }
            }
        }
        
        .test-button-wrapper {
            text-align: center;
            // margin-top: 16px;
            // padding: 12px 20px;
            // background: #f8fff9;
            // border-radius: 8px;
            // border: 1px solid #e8f5e8;
            
            .t-button {
                min-width: 100px;
                height: 32px;
                font-weight: 500;
                font-size: 13px;
            }
        }
        
        .test-result {
            background: white;
            border-radius: 10px;
            padding: 20px;
            border: 1px solid #e8f5e8;
            box-shadow: 0 2px 8px rgba(7, 192, 95, 0.08);
            
            h6 {
                font-size: 15px;
                margin-bottom: 16px;
                color: #07c05f;
                font-weight: 600;
                padding-bottom: 8px;
                border-bottom: 2px solid #f0fdf4;
            }
            
            .result-item {
                margin-bottom: 16px;
                
                label {
                    display: block;
                    font-weight: 600;
                    margin-bottom: 8px;
                    color: #333;
                    font-size: 14px;
                }
                
                .result-text {
                    background: #f8fff9;
                    padding: 12px 16px;
                    border-radius: 8px;
                    color: #333;
                    white-space: pre-wrap;
                    max-height: 120px;
                    overflow-y: auto;
                    border: 1px solid #e8f5e8;
                    font-size: 13px;
                    line-height: 1.5;
                }
            }
            
            .result-time {
                text-align: right;
                color: #6b7280;
                font-size: 12px;
                margin-top: 12px;
                padding: 8px 12px;
                background: #f8f9fa;
                border-radius: 6px;
                border: 1px solid #e9ecef;
            }
            
            .result-error {
                color: #e34d59;
                
                .error-msg {
                    display: flex;
                    align-items: center;
                    gap: 8px;
                    background: #fff2f0;
                    padding: 12px 16px;
                    border-radius: 8px;
                    border: 1px solid #ffccc7;
                    font-size: 13px;
                    line-height: 1.4;
                }
            }
        }
    }

    .submit-section {
        text-align: center;
        padding: 32px 0 0;
        // border-top: 1px solid #f0f0f0;
        // margin-top: 32px;
        
        :deep(.t-button--theme-primary) {
            background: linear-gradient(135deg, #07c05f, #00a651);
            border: none;
            border-radius: 8px;
            font-weight: 600;
            padding: 12px 32px;
            font-size: 16px;
            transition: all 0.2s ease;
            min-height: 44px;
            min-width: 160px;
            
            &:hover:not(.t-is-disabled) {
                background: linear-gradient(135deg, #00a651, #009645);
                box-shadow: 0 8px 25px rgba(7, 192, 95, 0.3);
            }
            
            &.t-is-disabled {
                background: #ccc;
                color: #999;
                box-shadow: none;
            }
            
            &.t-is-loading {
                background: linear-gradient(135deg, #07c05f, #00a651);
                color: white;
                box-shadow: 0 4px 15px rgba(7, 192, 95, 0.2);
            }
        }
        
        .submit-tips {
            margin-top: 16px;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #fa8c16;
            font-size: 14px;
            
            .tip-icon {
                margin-right: 6px;
            }
        }
    }

    // 动画定义
    @keyframes spin {
        from {
            transform: rotate(0deg);
        }
        to {
            transform: rotate(360deg);
        }
    }

    @keyframes pulse {
        0%, 100% { 
            opacity: 1;
            transform: scale(1);
        }
        50% { 
            opacity: 0.8;
            transform: scale(1.05);
        }
    }

    // 表单样式优化
    :deep(.t-form-item__label) {
        font-weight: 500;
        color: #333;
        font-size: 14px;
    }
    
    :deep(.t-input), :deep(.t-radio-group), :deep(.t-input-number) {
        width: 100%;
        
        .t-input__inner {
            border-radius: 6px;
            transition: all 0.3s ease;
            
            &:focus {
                border-color: #07c05f;
                box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.2);
            }
        }
    }

    :deep(.t-input) {
        border-radius: 8px;
        
        &.t-is-focused {
            border-color: #07c05f;
            box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.1);
        }
        
        .t-input__inner {
            border-radius: 8px;
            transition: all 0.2s ease;
            
            &:focus {
                border-color: #07c05f;
                box-shadow: none;
                outline: none;
            }
            
            &:hover {
                border-color: #07c05f;
            }
        }
    }
}

.select-input-node {
    display: flex;
    flex-direction: column;
    padding: 0;
    gap: 2px;
}

.select-input-node > li {
    display: block;
    border-radius: 3px;
    line-height: 22px;
    cursor: pointer;
    padding: 3px 8px;
    color: var(--td-text-color-primary);
    transition: background-color 0.2s linear;
    white-space: nowrap;
    word-wrap: normal;
    overflow: hidden;
    text-overflow: ellipsis;
}

.select-input-node > li:hover {
    background-color: var(--td-bg-color-container-hover);
}
</style>