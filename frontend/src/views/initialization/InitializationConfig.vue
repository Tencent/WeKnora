<template>
    <div class="initialization-container">
        <div class="initialization-header">
            <h1>WeKnora 系统初始化配置</h1>
            <p>首次使用需要配置模型和服务信息，完成后即可开始使用系统</p>
        </div>
        
        <t-form ref="form" :data="formData" :rules="rules" @submit.prevent layout="vertical">
            <!-- LLM 模型配置 -->
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
                        <t-input v-model="formData.llm.modelName" placeholder="例如: qwen3:0.6b" 
                                 @blur="onModelNameChange('llm')" 
                                 @input="onModelNameInput('llm')"
                                 @keyup.enter="onModelNameChange('llm')" />
                    </t-form-item>
                </div>
                
                <!-- Ollama模型状态检查 -->
                <div v-if="formData.llm.source === 'local'" class="ollama-status">
                    <div class="status-item">
                        <div class="status-header">
                            <span class="status-label">Ollama服务状态:</span>
                            <t-tag v-if="ollamaStatus.checked" :theme="ollamaStatus.available ? 'success' : 'danger'">
                                {{ ollamaStatus.available ? `运行中 (${ollamaStatus.version})` : '未运行' }}
                            </t-tag>
                            <t-tag v-else theme="default">检查中...</t-tag>
                            <t-button v-if="!ollamaStatus.available && ollamaStatus.checked" 
                                      theme="primary" variant="text" size="small" @click="checkOllama">
                                重新检查
                            </t-button>
                        </div>
                    </div>
                    
                    <div v-if="formData.llm.modelName" class="status-item">
                        <div class="status-header">
                            <span class="status-label">LLM模型状态:</span>
                            <t-tag v-if="modelStatus.llm.checked" :theme="modelStatus.llm.available ? 'success' : 'warning'">
                                {{ modelStatus.llm.available ? '已安装' : '未安装' }}
                            </t-tag>
                            <t-tag v-else theme="default">检查中...</t-tag>
                            
                            <t-button v-if="!modelStatus.llm.available && modelStatus.llm.checked && ollamaStatus.available && !modelStatus.llm.downloading" 
                                      theme="primary" size="small"
                                      @click="downloadModel('llm', formData.llm.modelName)">
                                下载模型
                            </t-button>
                        </div>
                        
                        <!-- 下载进度显示 -->
                        <div v-if="modelStatus.llm.downloading" class="download-progress">
                            <t-progress 
                                :percentage="modelStatus.llm.progress" 
                                :label="false"
                                size="small"
                                status="active">
                            </t-progress>
                            <span class="progress-text">{{ modelStatus.llm.message }} ({{ modelStatus.llm.progress.toFixed(1) }}%)</span>
                        </div>
                    </div>
                </div>
                
                <div v-if="formData.llm.source === 'remote'" class="remote-config">
                    <div class="form-row">
                        <t-form-item label="Base URL" name="llm.baseUrl">
                            <t-input v-model="formData.llm.baseUrl" placeholder="例如: https://api.openai.com/v1, 去除末尾/chat/completions路径后的URL的前面部分" 
                                     @blur="onRemoteConfigChange('llm')"
                                     @input="onRemoteConfigInput('llm')" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="API Key (可选)" name="llm.apiKey">
                            <t-input v-model="formData.llm.apiKey" type="password" placeholder="请输入API Key (可选)" 
                                     @blur="onRemoteConfigChange('llm')"
                                     @input="onRemoteConfigInput('llm')" />
                        </t-form-item>
                    </div>
                    
                    <!-- Remote API模型状态检查 -->
                    <div v-if="formData.llm.modelName && formData.llm.baseUrl" class="remote-status">
                        <t-tag v-if="modelStatus.llm.checked" :theme="modelStatus.llm.available ? 'success' : 'danger'" size="small">
                            <t-icon :name="modelStatus.llm.available ? 'check-circle' : 'close-circle'" />
                            {{ modelStatus.llm.available ? '连接正常' : '连接失败' }}
                        </t-tag>
                        <t-tag v-else theme="default" size="small">
                            <t-icon name="loading" />
                            检查连接中...
                        </t-tag>
                        
                        <t-button v-if="!modelStatus.llm.available && modelStatus.llm.checked" 
                                  theme="primary" variant="text" size="small" 
                                  @click="checkRemoteModelStatus('llm')">
                            重新检查
                        </t-button>
                        
                        <div v-if="modelStatus.llm.checked && !modelStatus.llm.available && modelStatus.llm.message" 
                             class="error-message">
                            <t-icon name="error-circle" />
                            {{ modelStatus.llm.message }}
                        </div>
                    </div>
                </div>
            </div>

            <!-- Embedding 模型配置 -->
            <div class="config-section">
                <h3><t-icon name="layers" class="section-icon" />Embedding 嵌入模型配置</h3>
                
                <!-- 禁用提示 -->
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
                        <t-input v-model="formData.embedding.modelName" placeholder="例如: nomic-embed-text:latest" 
                                 @blur="onModelNameChange('embedding')" 
                                 @input="onModelNameInput('embedding')"
                                 @keyup.enter="onModelNameChange('embedding')"
                                 :disabled="hasFiles" />
                    </t-form-item>
                </div>
                
                <!-- Ollama模型状态检查 -->
                <div v-if="formData.embedding.source === 'local' && formData.embedding.modelName" class="ollama-status">
                    <div class="status-item">
                        <div class="status-header">
                            <span class="status-label">Embedding模型状态:</span>
                            <t-tag v-if="modelStatus.embedding.checked" :theme="modelStatus.embedding.available ? 'success' : 'warning'">
                                {{ modelStatus.embedding.available ? '已安装' : '未安装' }}
                            </t-tag>
                            <t-tag v-else theme="default">检查中...</t-tag>
                            
                            <t-button v-if="!modelStatus.embedding.available && modelStatus.embedding.checked && ollamaStatus.available && !modelStatus.embedding.downloading" 
                                      theme="primary" size="small"
                                      @click="downloadModel('embedding', formData.embedding.modelName)">
                                下载模型
                            </t-button>
                        </div>
                        
                        <!-- 下载进度显示 -->
                        <div v-if="modelStatus.embedding.downloading" class="download-progress">
                            <t-progress 
                                :percentage="modelStatus.embedding.progress" 
                                :label="false"
                                size="small"
                                status="active">
                            </t-progress>
                            <span class="progress-text">{{ modelStatus.embedding.message }} ({{ modelStatus.embedding.progress.toFixed(1) }}%)</span>
                        </div>
                    </div>
                </div>
                
                <!-- 维度设置 -->
                <div class="form-row">
                    <t-form-item label="维度" name="embedding.dimension">
                        <t-input v-model="formData.embedding.dimension" 
                                 :disabled="hasFiles" 
                                 placeholder="请输入向量维度" 
                                 style="width: 100px;"
                                 @input="onDimensionInput" />
                    </t-form-item>
                </div>
                
                <div v-if="formData.embedding.source === 'remote'" class="remote-config">
                    <div class="form-row">
                        <t-form-item label="Base URL" name="embedding.baseUrl">
                            <t-input v-model="formData.embedding.baseUrl" placeholder="例如: https://api.openai.com/v1, 去除末尾/embeddings路径后的URL的前面部分" 
                                     :disabled="hasFiles" @blur="onRemoteConfigChange('embedding')"
                                     @input="onRemoteConfigInput('embedding')" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="API Key (可选)" name="embedding.apiKey">
                            <t-input v-model="formData.embedding.apiKey" type="password" placeholder="请输入API Key (可选)" 
                                     :disabled="hasFiles" @blur="onRemoteConfigChange('embedding')"
                                     @input="onRemoteConfigInput('embedding')" />
                        </t-form-item>
                    </div>
                    
                    <!-- Remote API模型状态检查 -->
                    <div v-if="formData.embedding.modelName && formData.embedding.baseUrl && !hasFiles" class="remote-status">
                        <t-tag v-if="modelStatus.embedding.checked" :theme="modelStatus.embedding.available ? 'success' : 'danger'" size="small">
                            <t-icon :name="modelStatus.embedding.available ? 'check-circle' : 'close-circle'" />
                            {{ modelStatus.embedding.available ? '连接正常' : '连接失败' }}
                        </t-tag>
                        <t-tag v-else theme="default" size="small">
                            <t-icon name="loading" />
                            检查连接中...
                        </t-tag>
                        
                        <t-button v-if="!modelStatus.embedding.available && modelStatus.embedding.checked" 
                                  theme="primary" variant="text" size="small" 
                                  @click="checkRemoteModelStatus('embedding')">
                            重新检查
                        </t-button>
                        
                        <div v-if="modelStatus.embedding.checked && !modelStatus.embedding.available && modelStatus.embedding.message" 
                             class="error-message">
                            <t-icon name="error-circle" />
                            {{ modelStatus.embedding.message }}
                        </div>
                    </div>
                </div>
            </div>

            <!-- Rerank 模型配置 -->
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
                
                <div v-if="formData.rerank.enabled" class="rerank-config">
                    <div class="form-row">
                        <t-form-item label="模型名称" name="rerank.modelName">
                            <t-input v-model="formData.rerank.modelName" placeholder="例如: bge-reranker-v2-m3" 
                                     @blur="onRerankConfigChange"
                                     @input="onRerankConfigInput" />
                        </t-form-item>
                    </div>
                    
                    <div class="form-row">
                        <t-form-item label="Base URL" name="rerank.baseUrl">
                            <t-input v-model="formData.rerank.baseUrl" placeholder="例如: http://localhost:11434, 去除末尾/rerank路径后的URL的前面部分" 
                                     @blur="onRerankConfigChange"
                                     @input="onRerankConfigInput" />
                        </t-form-item>
                    </div>
                    
                    <div class="form-row">
                        <t-form-item label="API Key" name="rerank.apiKey">
                            <t-input v-model="formData.rerank.apiKey" type="password" placeholder="请输入API Key (可选)" 
                                     @blur="onRerankConfigChange"
                                     @input="onRerankConfigInput" />
                        </t-form-item>
                    </div>
                    
                    <!-- Rerank模型状态检查 -->
                    <div v-if="formData.rerank.modelName && formData.rerank.baseUrl" class="remote-status">
                        <t-tag v-if="modelStatus.rerank.checked" :theme="modelStatus.rerank.available ? 'success' : 'danger'" size="small">
                            <t-icon :name="modelStatus.rerank.available ? 'check-circle' : 'close-circle'" />
                            {{ modelStatus.rerank.available ? '连接正常' : '连接失败' }}
                        </t-tag>
                        <t-tag v-else theme="default" size="small">
                            <t-icon name="loading" />
                            检查连接中...
                        </t-tag>
                        
                        <t-button v-if="!modelStatus.rerank.available && modelStatus.rerank.checked" 
                                  theme="primary" variant="text" size="small" 
                                  @click="checkRerankModelStatus">
                            重新检查
                        </t-button>
                        
                        <div v-if="modelStatus.rerank.checked && !modelStatus.rerank.available && modelStatus.rerank.message" 
                             class="error-message">
                            <t-icon name="error-circle" />
                            {{ modelStatus.rerank.message }}
                        </div>
                    </div>
                </div>
            </div>

            <!-- 多模态配置 -->
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
                
                <div v-if="formData.multimodal.enabled" class="multimodal-config">
                    <!-- VLM 模型配置 -->
                    <h4>视觉语言模型配置</h4>
                    <div class="form-row">
                        <t-form-item label="VLM 模型名称" name="multimodal.vlm.modelName">
                            <t-input v-model="formData.multimodal.vlm.modelName" placeholder="例如: llava:13b" 
                                     @blur="onModelNameChange('vlm')" 
                                     @input="onModelNameInput('vlm')"
                                     @keyup.enter="onModelNameChange('vlm')" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="VLM 接口类型" name="multimodal.vlm.interfaceType">
                            <t-radio-group v-model="formData.multimodal.vlm.interfaceType" @change="onVlmInterfaceTypeChange">
                                <t-radio value="ollama">Ollama (本地)</t-radio>
                                <t-radio value="openai">OpenAI 兼容接口</t-radio>
                            </t-radio-group>
                        </t-form-item>
                    </div>
                    <div class="form-row" v-if="formData.multimodal.vlm.interfaceType === 'openai'">
                        <t-form-item label="VLM Base URL" name="multimodal.vlm.baseUrl">
                            <t-input v-model="formData.multimodal.vlm.baseUrl" placeholder="例如: http://localhost:11434/v1，去除末尾/chat/completions路径后的URL的前面部分"
                                     @blur="onVlmBaseUrlChange"
                                     @input="onVlmBaseUrlInput" />
                        </t-form-item>
                    </div>
                    <div class="form-row" v-if="formData.multimodal.vlm.interfaceType === 'openai'">
                        <t-form-item label="VLM API Key" name="multimodal.vlm.apiKey">
                            <t-input v-model="formData.multimodal.vlm.apiKey" type="password" placeholder="请输入API Key (可选)"
                                     @blur="onVlmApiKeyChange" />
                        </t-form-item>
                    </div>
                    
                    <!-- VLM模型状态检查 -->
                    <div v-if="formData.multimodal.vlm.modelName && isVlmOllama" class="ollama-status">
                        <div class="status-item">
                            <div class="status-header">
                                <span class="status-label">VLM模型状态:</span>
                                <t-tag v-if="modelStatus.vlm.checked" :theme="modelStatus.vlm.available ? 'success' : 'warning'">
                                    {{ modelStatus.vlm.available ? '已安装' : '未安装' }}
                                </t-tag>
                                <t-tag v-else theme="default">检查中...</t-tag>
                                
                                <t-button v-if="!modelStatus.vlm.available && modelStatus.vlm.checked && ollamaStatus.available && !modelStatus.vlm.downloading" 
                                          theme="primary" size="small"
                                          @click="downloadModel('vlm', formData.multimodal.vlm.modelName)">
                                    下载模型
                                </t-button>
                            </div>
                            
                            <!-- 下载进度显示 -->
                            <div v-if="modelStatus.vlm.downloading" class="download-progress">
                                <t-progress 
                                    :percentage="modelStatus.vlm.progress" 
                                    :label="false"
                                    size="small"
                                    status="active">
                                </t-progress>
                                <span class="progress-text">{{ modelStatus.vlm.message }} ({{ modelStatus.vlm.progress.toFixed(1) }}%)</span>
                            </div>
                        </div>
                    </div>
                    
                    <!-- COS 配置 -->
                    <h4>对象存储服务 (COS) 配置</h4>
                    <div class="form-row">
                        <t-form-item label="Secret ID" name="multimodal.cos.secretId">
                            <t-input v-model="formData.multimodal.cos.secretId" placeholder="请输入COS Secret ID"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="Secret Key" name="multimodal.cos.secretKey">
                            <t-input v-model="formData.multimodal.cos.secretKey" type="password" placeholder="请输入COS Secret Key"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="Region" name="multimodal.cos.region">
                            <t-input v-model="formData.multimodal.cos.region" placeholder="例如: ap-beijing"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="Bucket Name" name="multimodal.cos.bucketName">
                            <t-input v-model="formData.multimodal.cos.bucketName" placeholder="请输入Bucket名称"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="App ID" name="multimodal.cos.appId">
                            <t-input v-model="formData.multimodal.cos.appId" placeholder="请输入App ID"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    <div class="form-row">
                        <t-form-item label="Path Prefix" name="multimodal.cos.pathPrefix">
                            <t-input v-model="formData.multimodal.cos.pathPrefix" placeholder="例如: images/"
                                     @blur="onCosConfigChange" />
                        </t-form-item>
                    </div>
                    
                    <!-- 多模态测试功能 -->
                    <div v-if="canTestMultimodal" class="multimodal-test">
                        <h5>多模态功能测试</h5>
                        <div class="test-description">
                            <p>上传一张测试图片，验证VLM模型的Caption生成和OCR文字识别功能</p>
                        </div>
                        
                        <!-- 测试操作区 -->
                        <div class="multimodal-test-container">
                            <!-- 左侧：图片上传和预览区 -->
                            <div class="test-image-section">
                                <div class="test-upload">
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
                                            选择测试图片
                                        </t-button>
                                    </t-upload>
                                    
                                    <t-button 
                                        v-if="multimodalTest.selectedFile"
                                        theme="primary" 
                                        size="small" 
                                        :loading="multimodalTest.testing"
                                        @click="startMultimodalTest"
                                        style="margin-left: 10px;"
                                    >
                                        开始测试
                                    </t-button>
                                </div>
                                
                                <!-- 选中的图片预览 -->
                                <div v-if="multimodalTest.selectedFile" class="image-preview-container">
                                    <div class="image-preview">
                                        <img :src="multimodalTest.previewUrl" alt="测试图片" />
                                    </div>
                                    <div class="image-info">
                                        <span>{{ multimodalTest.selectedFile.name }}</span>
                                        <span>{{ formatFileSize(multimodalTest.selectedFile.size) }}</span>
                                    </div>
                                </div>
                            </div>
                            
                            <!-- 右侧：测试结果显示 -->
                            <div class="test-result-section" v-if="multimodalTest.result">
                                <div v-if="multimodalTest.result.success" class="result-success">
                                    <h6>测试结果</h6>
                                    
                                    <div v-if="multimodalTest.result.caption" class="result-item">
                                        <label>图片描述 (Caption):</label>
                                        <div class="result-content">{{ multimodalTest.result.caption }}</div>
                                    </div>
                                    
                                    <div v-if="multimodalTest.result.ocr" class="result-item">
                                        <label>文字识别 (OCR):</label>
                                        <div class="result-content">{{ multimodalTest.result.ocr }}</div>
                                    </div>
                                    
                                    <div v-if="multimodalTest.result.processing_time" class="result-item time-item">
                                        <label>处理时间:</label>
                                        <div class="result-content">{{ multimodalTest.result.processing_time }}ms</div>
                                    </div>
                                </div>
                                
                                <div v-else class="result-error">
                                    <h6>测试失败</h6>
                                    <div class="error-content">
                                        <t-icon name="error-circle" />
                                        {{ multimodalTest.result.message || '多模态处理失败' }}
                                    </div>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
            </div>

            <!-- 文档分割配置 -->
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
                        <div class="form-tip">选择适合您场景的文档分割策略</div>
                    </t-form-item>
                </div>
                
                <div class="parameters-grid" :class="{ 'disabled-grid': selectedPreset !== 'custom' }">
                    <div class="parameter-group">
                        <div class="parameter-label">分块大小</div>
                        <div class="parameter-control">
                            <t-slider 
                                v-model="formData.documentSplitting.chunkSize" 
                                :min="100" 
                                :max="4000" 
                                :step="100"
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
                                :step="50"
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
                        <div class="parameter-desc">选择文档分割的优先级分隔符，支持自定义</div>
                    </div>
                </div>
                
            </div>

            <!-- 提交按钮 -->
            <div class="submit-section">
                <t-button theme="primary" type="button" size="large" 
                          :loading="submitting" :disabled="!canSubmit"
                          @click="handleSubmit">
                    {{ isUpdateMode ? '更新初始化配置' : '完成初始化配置' }}
                </t-button>
                
                <!-- 提交状态提示 -->
                <div v-if="!canSubmit && hasOllamaModels" class="submit-tips">
                    <t-icon name="info-circle" class="tip-icon" />
                    <span>请等待所有Ollama模型下载完成后再进行初始化</span>
                </div>
            </div>
        </t-form>
    </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import { MessagePlugin, FormRule } from 'tdesign-vue-next';
import { 
    initializeSystem, 
    checkOllamaStatus, 
    checkOllamaModels, 
    downloadOllamaModel,
    getDownloadProgress,
    getCurrentConfig,
    checkRemoteModel,
    type DownloadTask,
    checkRerankModel,
    testMultimodalFunction
} from '@/api/initialization';

const router = useRouter();
const form = ref(null);
const submitting = ref(false);
const hasFiles = ref(false);
const isUpdateMode = ref(false); // 是否为更新模式

// Ollama服务状态
const ollamaStatus = reactive({
    checked: false,
    available: false,
    version: '',
    error: ''
});

// 下载任务管理
const downloadTasks = reactive<Record<string, DownloadTask>>({});
const progressTimers = reactive<Record<string, any>>({});

// 模型状态管理
const modelStatus = reactive({
    llm: {
        checked: false,
        available: false,
        downloading: false,
        taskId: '',
        progress: 0,
        message: ''
    },
    embedding: {
        checked: false,
        available: false,
        downloading: false,
        taskId: '',
        progress: 0,
        message: ''
    },
    vlm: {
        checked: false,
        available: false,
        downloading: false,
        taskId: '',
        progress: 0,
        message: ''
    },
    rerank: {
        checked: false,
        available: false,
        downloading: false,
        taskId: '',
        progress: 0,
        message: ''
    }
});

// 表单数据
const formData = reactive({
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
        }
    },
    documentSplitting: {
        chunkSize: 1000, 
        chunkOverlap: 200,
        separators: ['\n\n', '\n', '。', '！', '？', ';', '；']
    }
});

// 预设配置选择
const selectedPreset = ref('balanced');

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

// 输入防抖定时器
const inputDebounceTimers = reactive<Record<string, any>>({});

// 多模态测试状态
const multimodalTest = reactive({
    uploadedFiles: [],
    selectedFile: null as File | null,
    previewUrl: '',
    testing: false,
    result: null as any
});

const imageUpload = ref(null);

// 配置回填
const loadCurrentConfig = async () => {
    try {
        const config = await getCurrentConfig();
        
        // 设置hasFiles状态
        hasFiles.value = config.hasFiles || false;
        
        // 检查是否已有配置（判断是否为更新模式）
        const hasExistingConfig = config.llm?.modelName || config.embedding?.modelName || config.rerank?.modelName;
        isUpdateMode.value = !!hasExistingConfig;
        
        // 回填表单数据
        if (config.llm) {
            Object.assign(formData.llm, config.llm);
        }
        if (config.embedding) {
            Object.assign(formData.embedding, config.embedding);
        }
        if (config.rerank) {
            Object.assign(formData.rerank, config.rerank);
        }
        if (config.multimodal) {
            formData.multimodal.enabled = config.multimodal.enabled || false;
            if (config.multimodal.vlm) {
                Object.assign(formData.multimodal.vlm, config.multimodal.vlm);
                // 如果没有接口类型，设置默认值
                if (!formData.multimodal.vlm.interfaceType) {
                    formData.multimodal.vlm.interfaceType = 'ollama';
                }
            }
            if (config.multimodal.cos) {
                Object.assign(formData.multimodal.cos, config.multimodal.cos);
            }
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
        // 检查Rerank模型状态
        if (formData.rerank.enabled && formData.rerank.modelName && formData.rerank.baseUrl) {
            await checkRerankModelStatus();
        }
        
        // 如果有多模态配置，也检查VLM模型状态
        if (formData.multimodal.enabled && formData.multimodal.vlm.modelName) {
            if (isVlmOllama.value && ollamaStatus.available) {
                await checkVlmModelStatus();
            }
        }
    }, 300);
};

// 计算属性：是否为Ollama VLM
const isVlmOllama = computed(() => {
    return formData.multimodal.vlm.interfaceType === 'ollama';
});

const hasOllamaModels = computed(() => {
    return (formData.llm.source === 'local' && formData.llm.modelName) ||
           (formData.embedding.source === 'local' && formData.embedding.modelName) ||
           (formData.multimodal.enabled && isVlmOllama.value && formData.multimodal.vlm.modelName);
});

const canSubmit = computed(() => {
    if (!hasOllamaModels.value) return true;
    
    if (!ollamaStatus.available) return false;
    
    // 检查所有需要的Ollama模型是否都已下载完成
    const checks = [];
    
    if (formData.llm.source === 'local' && formData.llm.modelName) {
        checks.push(modelStatus.llm.available && !modelStatus.llm.downloading);
    }
    
    if (formData.embedding.source === 'local' && formData.embedding.modelName) {
        checks.push(modelStatus.embedding.available && !modelStatus.embedding.downloading);
    }
    
    if (formData.multimodal.enabled && isVlmOllama.value && formData.multimodal.vlm.modelName) {
        checks.push(modelStatus.vlm.available && !modelStatus.vlm.downloading);
    }
    
    return checks.length === 0 || checks.every(check => check);
});

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
    'rerank.modelName': [
        { required: (t: any) => formData.rerank.enabled, message: '请输入Rerank模型名称', type: 'error' }
    ],
    'rerank.baseUrl': [
        { required: (t: any) => formData.rerank.enabled, message: '请输入Rerank BaseURL', type: 'error' }
    ],
    'multimodal.vlm.modelName': [
        { required: (t: any) => formData.multimodal.enabled, message: '请输入VLM模型名称', type: 'error' }
    ],
    'multimodal.vlm.baseUrl': [
        { required: (t: any) => formData.multimodal.enabled && formData.multimodal.vlm.interfaceType === 'openai', message: '请输入VLM BaseURL', type: 'error' }
    ],
    'multimodal.cos.secretId': [
        { required: (t: any) => formData.multimodal.enabled, message: '请输入COS Secret ID', type: 'error' }
    ],
    'multimodal.cos.secretKey': [
        { required: (t: any) => formData.multimodal.enabled, message: '请输入COS Secret Key', type: 'error' }
    ],
    'multimodal.cos.region': [
        { required: (t: any) => formData.multimodal.enabled, message: '请输入COS Region', type: 'error' }
    ],
    'multimodal.cos.bucketName': [
        { required: (t: any) => formData.multimodal.enabled, message: '请输入COS Bucket Name', type: 'error' }
    ],
    'multimodal.cos.appId': [
        { required: (t: any) => formData.multimodal.enabled, message: '请输入COS App ID', type: 'error' }
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

// 检查所有Ollama模型状态
const checkAllOllamaModels = async () => {
    const modelsToCheck = [];
    
    if (formData.llm.source === 'local' && formData.llm.modelName) {
        modelsToCheck.push(formData.llm.modelName);
    }
    
    if (formData.embedding.source === 'local' && formData.embedding.modelName) {
        modelsToCheck.push(formData.embedding.modelName);
    }
    
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
    try {
        // 启动下载任务
        const result = await downloadOllamaModel(modelName);
        
        // 更新模型状态
        modelStatus[type].downloading = true;
        modelStatus[type].taskId = result.taskId;
        modelStatus[type].progress = result.progress;
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
});

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
        // 总是重置VLM模型状态
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        modelStatus.vlm.downloading = false;
        
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
        // 总是重置模型状态
        modelStatus[type].checked = false;
        modelStatus[type].available = false;
        modelStatus[type].downloading = false;
        
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
    
    // 重置模型状态
    if (type === 'vlm') {
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        modelStatus.vlm.message = '';
    } else {
        modelStatus[type].checked = false;
        modelStatus[type].available = false;
        modelStatus[type].message = '';
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
                await checkRemoteModelStatus('embedding');
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
        
        // 检查VLM模型状态
        if (formData.multimodal.vlm.modelName && isVlmOllama.value) {
            await checkVlmModelStatus();
        }
    }
};

// 远程配置改变时的处理
const onRemoteConfigChange = async (type: 'llm' | 'embedding') => {
    // 重置模型状态
    modelStatus[type].checked = false;
    modelStatus[type].available = false;
    modelStatus[type].message = '';
    
    // 如果配置完整，检查模型
    if (formData[type].modelName && formData[type].baseUrl) {
        await checkRemoteModelStatus(type);
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
            await checkRemoteModelStatus(type);
        }
    }, 500); // 500ms防抖延迟
};

// 检查远程模型
const checkRemoteModelStatus = async (type: 'llm' | 'embedding') => {
    if (!formData[type].modelName || !formData[type].baseUrl) {
        return;
    }
    
    try {
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
        modelStatus[type].message = error.message || '网络连接失败';
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

// 处理分隔符输入
const onSeparatorsChange = (value: string) => {
    formData.documentSplitting.separators = value.split(',').map(s => s.trim()).filter(s => s);
};

// 最终模型检查
const performFinalModelCheck = async () => {
    // 收集需要检查的本地模型
    const modelsToCheck = [];
    
    if (formData.llm.source === 'local' && formData.llm.modelName) {
        modelsToCheck.push({ name: formData.llm.modelName, type: 'LLM' });
    }
    
    if (formData.embedding.source === 'local' && formData.embedding.modelName) {
        modelsToCheck.push({ name: formData.embedding.modelName, type: 'Embedding' });
    }
    
    if (formData.multimodal.enabled && isVlmOllama.value && formData.multimodal.vlm.modelName) {
        modelsToCheck.push({ name: formData.multimodal.vlm.modelName, type: 'VLM' });
    }
    
    if (modelsToCheck.length === 0) {
        return { success: true };
    }
    
    // 检查Ollama服务状态
    if (!ollamaStatus.available) {
        return { 
            success: false, 
            message: 'Ollama服务未运行，无法使用本地模型。请启动Ollama服务或改用远程API。' 
        };
    }
    
    try {
        // 重新检查所有模型状态
        const modelNames = modelsToCheck.map(m => m.name);
        const result = await checkOllamaModels(modelNames);
        
        // 检查是否有未安装的模型
        const unavailableModels = [];
        modelsToCheck.forEach(model => {
            if (!result.models[model.name]) {
                unavailableModels.push(model);
            }
        });
        
        if (unavailableModels.length > 0) {
            const modelList = unavailableModels.map(m => `${m.type}模型 "${m.name}"`).join('、');
            return { 
                success: false, 
                message: `以下模型未安装：${modelList}。请先下载这些模型或选择其他已安装的模型。` 
            };
        }
        
        // 检查是否有正在下载的模型
        const downloadingModels = [];
        if (formData.llm.source === 'local' && modelStatus.llm.downloading) {
            downloadingModels.push('LLM');
        }
        if (formData.embedding.source === 'local' && modelStatus.embedding.downloading) {
            downloadingModels.push('Embedding');
        }
        if (formData.multimodal.enabled && isVlmOllama.value && modelStatus.vlm.downloading) {
            downloadingModels.push('VLM');
        }
        
        if (downloadingModels.length > 0) {
            return { 
                success: false, 
                message: `${downloadingModels.join('、')}模型正在下载中，请等待下载完成后再提交配置。` 
            };
        }
        
        return { success: true };
        
    } catch (error) {
        console.error('最终模型检查失败:', error);
        return { 
            success: false, 
            message: '无法验证模型状态，请检查网络连接后重试。' 
        };
    }
};

// 处理按钮提交
const handleSubmit = async () => {
    // 先进行表单验证
    const validateResult = await form.value?.validate();
    
    if (validateResult === true) {
        // 确保embedding.dimension是数字类型
        if (formData.embedding.dimension) {
            formData.embedding.dimension = Number(formData.embedding.dimension);
        }
        
        // 检查多模态配置
        if (formData.multimodal.enabled) {
            // 检查是否进行了图片测试
            if (!multimodalTest.result) {
                MessagePlugin.warning('您启用了多模态配置，请先上传一张图片进行测试');
                return;
            }
            
            // 检查是否至少有OCR或Caption有结果
            if (!multimodalTest.result.success || 
                (!multimodalTest.result.caption && !multimodalTest.result.ocr) || 
                (multimodalTest.result.caption === "无法生成图片描述" && multimodalTest.result.ocr === "图片中未检测到文字内容")) {
                MessagePlugin.warning('多模态测试未能正确识别图片内容，请重新测试或检查配置');
                return;
            }
        }
        
        submitting.value = true;
        try {
            // 最终检查：确保所有本地模型都已下载完成
            const finalCheck = await performFinalModelCheck();
            if (!finalCheck.success) {
                MessagePlugin.error(finalCheck.message);
                submitting.value = false;
                return;
            }
            
            // 调用初始化API
            await initializeSystem(formData);
            
            const successMessage = isUpdateMode.value ? '配置更新成功！' : '系统初始化成功！';
            MessagePlugin.success(successMessage);
            
            // 记录初始化状态，强制刷新路由状态
            localStorage.setItem('system_initialized', 'true');
            
            // 跳转到知识库页面
            setTimeout(() => {
                router.replace('/platform/knowledgeBase');
            }, 300);
        } catch (error) {
            console.error('初始化失败:', error);
            const errorMessage = isUpdateMode.value ? '配置更新失败，请检查配置并重试' : '初始化失败，请检查配置并重试';
            MessagePlugin.error(error.message || errorMessage);
        } finally {
            submitting.value = false;
        }
    }
};

// 监听表单变化
watch(() => formData.llm.source, () => onModelSourceChange('llm'));
watch(() => formData.embedding.source, () => onModelSourceChange('embedding'));

// 组件挂载时检查Ollama状态
onMounted(async () => {
    // 加载当前配置
    await loadCurrentConfig();

    // 检查Ollama状态
    const needOllamaCheck = 
        formData.llm.source === 'local' || 
        formData.embedding.source === 'local' || 
        (formData.multimodal.enabled && formData.multimodal.vlm.interfaceType === 'ollama');
    
    if (needOllamaCheck) {
        await checkOllama();
    }
    
    // 检查已配置模型状态
    await checkAllConfiguredModels();
});

const onRerankChange = () => {
    // Add any additional logic you want to execute when rerank.enabled changes
    console.log('Rerank enabled:', formData.rerank.enabled);
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
        modelStatus.rerank.message = error.message || '网络连接失败';
    }
};

// 多模态测试相关方法
const getTestUploadData = () => {
    return {
        vlm_model: formData.multimodal.vlm.modelName,
        vlm_base_url: formData.multimodal.vlm.baseUrl,
        vlm_api_key: formData.multimodal.vlm.apiKey || ''
    };
};

const onImageChange = (files: any) => {
    if (files && files.length > 0) {
        // 检查多模态配置是否完整
        const missingConfigs = [];
        
        // 检查COS配置
        if (!formData.multimodal.cos.secretId) {
            missingConfigs.push('COS Secret ID');
        }
        if (!formData.multimodal.cos.secretKey) {
            missingConfigs.push('COS Secret Key');
        }
        if (!formData.multimodal.cos.region) {
            missingConfigs.push('COS Region');
        }
        if (!formData.multimodal.cos.bucketName) {
            missingConfigs.push('COS Bucket Name');
        }
        if (!formData.multimodal.cos.appId) {
            missingConfigs.push('COS App ID');
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
            cos_secret_id: string;
            cos_secret_key: string;
            cos_region: string;
            cos_bucket_name: string;
            cos_app_id: string;
            cos_path_prefix: string;
            chunk_size: number;
            chunk_overlap: number;
            separators: string[];
        } = {
            image: multimodalTest.selectedFile,
            vlm_model: formData.multimodal.vlm.modelName,
            vlm_base_url: '',  // 将在下方设置
            vlm_api_key: '',   // 将在下方设置
            vlm_interface_type: formData.multimodal.vlm.interfaceType,
            cos_secret_id: formData.multimodal.cos.secretId,
            cos_secret_key: formData.multimodal.cos.secretKey,
            cos_region: formData.multimodal.cos.region,
            cos_bucket_name: formData.multimodal.cos.bucketName,
            cos_app_id: formData.multimodal.cos.appId,
            cos_path_prefix: formData.multimodal.cos.pathPrefix || '',
            chunk_size: formData.documentSplitting.chunkSize,
            chunk_overlap: formData.documentSplitting.chunkOverlap,
            separators: formData.documentSplitting.separators
        };
        
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
            message: error.message || '测试过程中发生错误'
        };
        MessagePlugin.error('多模态测试失败');
    } finally {
        multimodalTest.testing = false;
    }
};

const onTestSuccess = (response: any) => {
    console.log('Upload success:', response);
};

const onTestFail = (error: any) => {
    console.error('Upload failed:', error);
    MessagePlugin.error('图片上传失败');
};

const formatFileSize = (bytes: number): string => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
};

const canTestMultimodal = computed(() => {
    const baseUrlValid = formData.multimodal.vlm.interfaceType === 'ollama' || 
                        (formData.multimodal.vlm.interfaceType === 'openai' && formData.multimodal.vlm.baseUrl);
                        
    return formData.multimodal.cos.secretId && 
           formData.multimodal.cos.secretKey && 
           formData.multimodal.cos.region && 
           formData.multimodal.cos.bucketName && 
           formData.multimodal.cos.appId && 
           formData.multimodal.vlm.modelName && 
           baseUrlValid;
});

const onVlmInterfaceTypeChange = () => {
    // 当接口类型改变时，重置相关状态
    if (formData.multimodal.vlm.interfaceType === 'ollama') {
        // 如果是Ollama，设置默认的Base URL
        formData.multimodal.vlm.apiKey = ''; // 清空API Key
        
        // 重置模型状态检查
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        
        // 如果有模型名称，检查模型状态
        if (formData.multimodal.vlm.modelName && ollamaStatus.available) {
            checkVlmModelStatus();
        }
    } else {
        // 如果是OpenAI兼容接口，清空Base URL让用户输入
        if (formData.multimodal.vlm.baseUrl.includes('localhost:11434')) {
            formData.multimodal.vlm.baseUrl = '';
        }
        // 重置模型状态检查
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
    }
    
    console.log('VLM interface type changed:', formData.multimodal.vlm.interfaceType);
};

const checkVlmModelStatus = async () => {
    if (!formData.multimodal.vlm.modelName || !formData.multimodal.vlm.baseUrl) {
        return;
    }
    
    try {
        modelStatus.vlm.checked = false;
        modelStatus.vlm.available = false;
        modelStatus.vlm.message = '';
        
        const result = await checkRemoteModel({
            modelName: formData.multimodal.vlm.modelName,
            baseUrl: formData.multimodal.vlm.baseUrl,
            apiKey: formData.multimodal.vlm.apiKey
        });
        
        modelStatus.vlm.checked = true;
        modelStatus.vlm.available = result.available;
        modelStatus.vlm.message = result.message || '';
        
        // 触发表单验证
        setTimeout(() => {
            form.value?.validate(['multimodal.vlm.modelName']);
        }, 100);
        
    } catch (error) {
        console.error('检查VLM模型状态失败:', error);
        modelStatus.vlm.checked = true;
        modelStatus.vlm.available = false;
        modelStatus.vlm.message = error.message || '网络连接失败';
    }
};

const onVlmBaseUrlChange = async () => {
    // 清除之前的定时器
    if (inputDebounceTimers['vlm_base_url']) {
        clearTimeout(inputDebounceTimers['vlm_base_url']);
    }
    
    // 重置VLM模型状态
    modelStatus.vlm.checked = false;
    modelStatus.vlm.available = false;
    modelStatus.vlm.message = '';
    
    // 如果配置完整，检查模型状态
    if (formData.multimodal.vlm.modelName && formData.multimodal.vlm.baseUrl) {
        if (isVlmOllama.value) {
            // 如果是Ollama接口，检查模型是否已安装
            if (ollamaStatus.available) {
                await checkAllOllamaModels();
            }
        } else {
            // 如果是OpenAI兼容接口，可以在这里添加远程API检查
            console.log('VLM使用OpenAI兼容接口，跳过本地模型检查');
        }
        
        // 触发表单验证
        setTimeout(() => {
            form.value?.validate(['multimodal.vlm.baseUrl']);
        }, 100);
    }
};

const onVlmBaseUrlInput = () => {
    // 清除之前的定时器
    if (inputDebounceTimers['vlm_base_url']) {
        clearTimeout(inputDebounceTimers['vlm_base_url']);
    }
    
    // 重置VLM模型状态
    modelStatus.vlm.checked = false;
    modelStatus.vlm.available = false;
    modelStatus.vlm.message = '';
    
    // 设置防抖延迟
    inputDebounceTimers['vlm_base_url'] = setTimeout(async () => {
        // 只有在有模型名称和Base URL时才进行校验
        if (formData.multimodal.vlm.modelName && formData.multimodal.vlm.modelName.trim() && 
            formData.multimodal.vlm.baseUrl && formData.multimodal.vlm.baseUrl.trim()) {
            
            // 触发表单验证
            form.value?.validate(['multimodal.vlm.baseUrl']);
            
            // 自动检查VLM模型状态
            await onVlmBaseUrlChange();
        }
    }, 500); // 500ms防抖延迟
};

const onVlmApiKeyChange = () => {
    // 清除之前的定时器
    if (inputDebounceTimers['vlm_api_key']) {
        clearTimeout(inputDebounceTimers['vlm_api_key']);
    }
    
    // 重置VLM模型状态
    modelStatus.vlm.checked = false;
    modelStatus.vlm.available = false;
    modelStatus.vlm.message = '';
    
    // 设置防抖延迟
    inputDebounceTimers['vlm_api_key'] = setTimeout(async () => {
        // 只有在有模型名称和Base URL时才进行校验
        if (formData.multimodal.vlm.modelName && formData.multimodal.vlm.modelName.trim() && 
            formData.multimodal.vlm.baseUrl && formData.multimodal.vlm.baseUrl.trim()) {
            
            // 触发表单验证
            form.value?.validate(['multimodal.vlm.apiKey']);
            
            // 自动检查VLM模型状态
            await checkVlmModelStatus();
        }
    }, 500); // 500ms防抖延迟
};

const onCosConfigChange = () => {
    // 触发表单验证，确保COS配置的完整性
    setTimeout(() => {
        form.value?.validate([
            'multimodal.cos.secretId',
            'multimodal.cos.secretKey',
            'multimodal.cos.region',
            'multimodal.cos.bucketName',
            'multimodal.cos.appId'
        ]);
    }, 100);
    
    console.log('COS config changed:', formData.multimodal.cos);
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
            await checkRemoteModelStatus('embedding');
        }
    }
    
    // 检查Rerank模型
    if (formData.rerank.enabled && formData.rerank.modelName && formData.rerank.baseUrl) {
        await checkRerankModelStatus();
    }
    
    // 检查VLM模型
    if (formData.multimodal.enabled && formData.multimodal.vlm.modelName) {
        if (isVlmOllama.value && ollamaStatus.available) {
            await checkVlmModelStatus();
        } else if (!isVlmOllama.value && formData.multimodal.vlm.baseUrl) {
            // 这里可以添加检查远程VLM的逻辑（如果有）
        }
    }
};

const onDimensionInput = (event: any) => {
    formData.embedding.dimension = Number(event.target.value);
};
</script>

<style lang="less" scoped>
.initialization-container {
    min-height: 100vh;
    background: linear-gradient(135deg, #f0f9f0 0%, #e8f5e8 100%);
    padding: 40px 20px;
    
    .initialization-header {
        text-align: center;
        margin-bottom: 40px;
        color: #2c5234;
        
        .logo-container {
            margin-bottom: 20px;
            
            .logo {
                height: 60px;
                width: auto;
                max-width: 200px;
                object-fit: contain;
                filter: drop-shadow(0 4px 8px rgba(44, 82, 52, 0.1));
                transition: transform 0.3s ease;
                
                &:hover {
                    transform: scale(1.05);
                }
            }
        }
        
        h1 {
            font-size: 32px;
            font-weight: 600;
            margin-bottom: 10px;
            text-shadow: 0 2px 4px rgba(44, 82, 52, 0.1);
        }
        
        p {
            font-size: 16px;
            opacity: 0.8;
            margin: 0;
        }
    }
    
    :deep(.t-form) {
        max-width: 800px;
        margin: 0 auto;
        background: white;
        padding: 40px;
        border-radius: 16px;
        box-shadow: 0 20px 60px rgba(0, 0, 0, 0.15);
        backdrop-filter: blur(10px);
    }
    
    .config-section {
        margin-bottom: 40px;
        border-bottom: 1px solid #e8f5e8;
        padding-bottom: 30px;
        
        &:last-of-type {
            border-bottom: none;
            margin-bottom: 20px;
        }
        
        h3 {
            color: #07c05f;
            font-size: 18px;
            font-weight: 600;
            margin-bottom: 20px;
            display: flex;
            align-items: center;
            padding: 10px 16px;
            background: linear-gradient(90deg, #e8f5e8, #f0faf0);
            border-radius: 8px;
            border-left: 4px solid #07c05f;
            
            .section-icon {
                margin-right: 8px;
                color: #07c05f;
                font-size: 20px;
            }
        }
        
        h4 {
            color: #333;
            font-size: 16px;
            font-weight: 500;
            margin: 20px 0 15px 0;
            padding-left: 12px;
            border-left: 3px solid #07c05f;
        }
    }
    
    .remote-config, .multimodal-config {
        margin-top: 20px;
        padding: 20px;
        background: #f8fdf8;
        border-radius: 8px;
        border-left: 4px solid #07c05f;
    }
    
    .ollama-status {
        margin-top: 15px;
        padding: 15px;
        background: #f0faf0;
        border-radius: 8px;
        border-left: 4px solid #52c41a;
        
        .status-item {
            display: flex;
            align-items: center;
            margin-bottom: 8px;
            flex-direction: column;
            align-items: flex-start;
            
            &:last-child {
                margin-bottom: 0;
            }
            
            .status-header {
                display: flex;
                align-items: center;
                margin-bottom: 5px;
                
                .status-label {
                    font-weight: 500;
                    margin-right: 10px;
                    min-width: 120px;
                    color: #333;
                }
                
                :deep(.t-tag) {
                    margin-right: 10px;
                    
                    &.t-tag--success {
                        background-color: #f6ffed;
                        border-color: #b7eb8f;
                        color: #52c41a;
                    }
                }
                
                :deep(.t-button) {
                    margin-bottom: 5px;
                    
                    &.t-button--theme-primary {
                        background-color: #07c05f;
                        border-color: #07c05f;
                        
                        &:hover {
                            background-color: #00a651;
                            border-color: #00a651;
                        }
                    }
                }
            }
            
            .download-progress {
                margin-top: 8px;
                width: 100%;
                max-width: 400px;
                
                :deep(.t-progress) {
                    .t-progress__bar {
                        background-color: #07c05f;
                    }
                }
                
                .progress-text {
                    font-size: 12px;
                    color: #666;
                    margin-top: 4px;
                    display: block;
                }
            }
        }
    }
    
    .remote-status {
        margin-top: 15px;
        padding: 12px 16px;
        transition: all 0.3s ease;
        display: flex;
        align-items: center;
        gap: 12px;
        flex-wrap: wrap;
        
        :deep(.t-tag) {
            display: flex;
            align-items: center;
            gap: 4px;
            padding: 4px 8px;
            border-radius: 6px;
            font-size: 12px;
            font-weight: 500;
            transition: all 0.3s ease;
            
            &.t-tag--success {
                background-color: #f6ffed;
                border-color: #b7eb8f;
                color: #52c41a;
                animation: statusFadeIn 0.5s ease;
            }
            
            &.t-tag--danger {
                background-color: #fff2f0;
                border-color: #ffccc7;
                color: #ff4d4f;
                animation: statusFadeIn 0.5s ease;
            }
            
            &.t-tag--default {
                background-color: #fafafa;
                border-color: #d9d9d9;
                color: #666;
            }
            
            .t-icon {
                font-size: 14px;
                
                &[name="loading"] {
                    animation: spin 1s linear infinite;
                }
            }
        }
        
        :deep(.t-button) {
            padding: 4px 8px;
            font-size: 12px;
            height: auto;
            transition: all 0.3s ease;
            
            &.t-button--theme-primary {
                color: #1890ff;
                
                &:hover {
                    color: #40a9ff;
                    background-color: #f0f9ff;
                    transform: translateY(-1px);
                }
            }
        }
        
        .error-message {
            width: 100%;
            margin-top: 8px;
            padding: 8px 12px;
            background: #fff2f0;
            border-radius: 6px;
            border-left: 3px solid #ff4d4f;
            font-size: 12px;
            color: #ff4d4f;
            display: flex;
            align-items: flex-start;
            gap: 6px;
            line-height: 1.4;
            animation: errorSlideIn 0.4s ease;
            
            .t-icon {
                font-size: 14px;
                color: #ff4d4f;
                margin-top: 1px;
                flex-shrink: 0;
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
    
    @keyframes statusFadeIn {
        from {
            opacity: 0;
            transform: scale(0.8);
        }
        to {
            opacity: 1;
            transform: scale(1);
        }
    }
    
    @keyframes errorSlideIn {
        from {
            opacity: 0;
            transform: translateY(-10px);
        }
        to {
            opacity: 1;
            transform: translateY(0);
        }
    }
    
    .submit-section {
        text-align: center;
        padding-top: 20px;
        border-top: 1px solid #e8f5e8;
        
        :deep(.t-button--theme-primary) {
            background: linear-gradient(135deg, #07c05f, #00a651);
            border: none;
            border-radius: 8px;
            font-weight: 600;
            padding: 12px 32px;
            font-size: 16px;
            transition: all 0.3s ease;
            
            &:hover:not(.t-is-disabled) {
                background: linear-gradient(135deg, #00a651, #009645);
                transform: translateY(-2px);
                box-shadow: 0 8px 25px rgba(7, 192, 95, 0.3);
            }
            
            &.t-is-disabled {
                background: #ccc;
                color: #999;
            }
        }
        
        :deep(.t-button--theme-default) {
            border-color: #d9d9d9;
            color: #666;
            
            &:hover {
                border-color: #07c05f;
                color: #07c05f;
            }
        }
        
        .submit-tips {
            margin-top: 15px;
            display: flex;
            align-items: center;
            justify-content: center;
            color: #fa8c16;
            font-size: 14px;
            
            .tip-icon {
                margin-right: 5px;
            }
        }
    }
    
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
    
    :deep(.t-radio-button) {
        .t-radio-button__inner {
            border-color: #d9d9d9;
            
            &:hover {
                border-color: #07c05f;
                color: #07c05f;
            }
        }
        
        &.t-is-checked .t-radio-button__inner {
            background-color: #07c05f;
            border-color: #07c05f;
            color: white;
        }
    }
    
    :deep(.t-switch) {
        &.t-is-checked .t-switch__handle::before {
            background-color: #07c05f;
        }
    }
    
    :deep(.t-button) {
        min-width: 120px;
        border-radius: 6px;
        font-weight: 500;
    }
    
    .form-tip {
        font-size: 12px;
        color: #888;
        margin-top: 4px;
        line-height: 1.4;
    }

    .form-row {
        margin-bottom: 20px;
    }

    .embedding-dimension {
        display: flex;
        align-items: center;
        gap: 10px;
        
        .t-input-number {
            width: 160px;
        }
        
        .dimension-help {
            color: #666;
            font-size: 12px;
        }
    }

    .switch-container {
        display: flex;
        align-items: center;
        margin-top: 10px;

        .switch-label {
            margin-left: 10px;
            font-size: 14px;
            color: #333;
            font-weight: 500;
        }
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

    .preset-row {
        margin-bottom: 30px;
        
        .preset-radio-group {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 15px;
            margin-top: 15px;
            
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
                    transform: translateY(-2px);
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
        padding: 24px;
        background: white;
        border-radius: 12px;
        border: 2px solid #e8f5e8;
        box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
        
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
                margin-bottom: 12px;
                
                .parameter-slider {
                    width: 100%;
                    max-width: 300px;
                    
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
                    width: 100%;
                    max-width: 400px;
                }
            }
            
            .parameter-desc {
                font-size: 12px;
                color: #999;
                line-height: 1.4;
                padding-left: 4px;
                margin-top: 8px;
            }
        }
    }

    .rerank-config {
        margin-top: 20px;
        padding: 20px;
        background: #f8fdf8;
        border-radius: 8px;
        border-left: 4px solid #07c05f;
    }
    
    .multimodal-test {
        margin-top: 20px;
        padding: 15px;
        background: #fcf3ea;
        border-radius: 6px;
        border-left: 3px solid #ff7000;
        
        h5 {
            font-size: 16px;
            color: #d46b08;
            margin-bottom: 10px;
        }
        
        .test-description {
            margin-bottom: 15px;
            font-size: 13px;
            color: #666;
        }
        
        .multimodal-test-container {
            display: flex;
            flex-direction: row;
            gap: 20px;
            
            @media (max-width: 768px) {
                flex-direction: column;
            }
            
            .test-image-section {
                flex: 1;
                min-width: 0;
                
                .test-upload {
                    margin-bottom: 15px;
                    display: flex;
                    align-items: center;
                }
            }
            
            .test-result-section {
                flex: 1.5;
                min-width: 0;
                background: white;
                border-radius: 6px;
                padding: 15px;
                box-shadow: 0 2px 5px rgba(0,0,0,0.05);
                
                h6 {
                    font-size: 15px;
                    margin-bottom: 12px;
                    color: #262626;
                    font-weight: 500;
                }
                
                .result-item {
                    margin-bottom: 12px;
                    
                    &.time-item {
                        color: #666;
                        font-size: 12px;
                        text-align: right;
                    }
                    
                    label {
                        display: block;
                        font-weight: 500;
                        margin-bottom: 4px;
                        color: #666;
                    }
                    
                    .result-content {
                        background: #f5f5f5;
                        padding: 8px 10px;
                        border-radius: 4px;
                        color: #262626;
                        white-space: pre-wrap;
                        max-height: 120px;
                        overflow-y: auto;
                    }
                }
            }

            .image-preview-container {
                border: 2px dashed #ffcba4;
                border-radius: 8px;
                background: #fffbf7;
                padding: 10px;
                text-align: center;
                
                .image-preview {
                    margin-bottom: 10px;
                    
                    img {
                        max-width: 100%;
                        max-height: 200px;
                        object-fit: contain;
                        border-radius: 4px;
                        box-shadow: 0 2px 8px rgba(255, 107, 53, 0.15);
                    }
                }
                
                .image-info {
                    display: flex;
                    justify-content: space-between;
                    align-items: center;
                    padding: 0 5px;
                    font-size: 12px;
                    color: #d46b08;
                    
                    span {
                        &:first-child {
                            font-weight: 500;
                            text-overflow: ellipsis;
                            overflow: hidden;
                            white-space: nowrap;
                            max-width: 70%;
                        }
                    }
                }
            }
            
            .result-error {
                color: #d54941;
                
                .error-content {
                    display: flex;
                    align-items: center;
                    gap: 8px;
                    background: #fff2f0;
                    padding: 10px;
                    border-radius: 4px;
                    border-left: 3px solid #d54941;
                }
            }
        }
    }
}
</style> 