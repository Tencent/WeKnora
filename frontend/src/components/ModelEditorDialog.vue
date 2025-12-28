<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="dialogVisible" class="model-editor-overlay" @click.self="handleCancel">
        <div class="model-editor-modal">
          <!-- 关闭按钮 -->
          <button class="close-btn" @click="handleCancel" :aria-label="$t('common.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </button>

          <!-- 标题区域 -->
          <div class="modal-header">
            <h2 class="modal-title">{{ isEdit ? $t('model.editor.editTitle') : $t('model.editor.addTitle') }}</h2>
            <p class="modal-desc">{{ getModalDescription() }}</p>
          </div>

          <!-- 表单内容区域 -->
          <div class="modal-body">
            <t-form ref="formRef" :data="formData" :rules="rules" layout="vertical">
        <!-- 模型来源 -->
        <div class="form-item">
          <label class="form-label required">{{ $t('model.editor.sourceLabel') }}</label>
          <t-radio-group v-model="formData.source">
            <t-radio
              value="local"
              :disabled="ollamaServiceStatus === false || modelType === 'rerank'"
            >
              {{ $t('model.editor.sourceLocal') }}
            </t-radio>
            <t-radio value="remote">{{ $t('model.editor.sourceRemote') }}</t-radio>
          </t-radio-group>

          <!-- ReRank模型不支持Ollama的提示信息 -->
          <div v-if="modelType === 'rerank'" class="ollama-unavailable-tip rerank-tip">
            <t-icon name="info-circle-filled" class="tip-icon info" />
            <span class="tip-text">{{ $t('model.editor.ollamaNotSupportRerank') }}</span>
          </div>

          <!-- Ollama不可用时的提示信息 -->
          <div v-else-if="ollamaServiceStatus === false" class="ollama-unavailable-tip">
            <t-icon name="error-circle-filled" class="tip-icon" />
            <span class="tip-text">{{ $t('model.editor.ollamaUnavailable') }}</span>
            <t-button
              variant="text"
              size="small"
              theme="primary"
              @click="goToOllamaSettings"
              class="tip-link"
            >
              {{ $t('model.editor.goToOllamaSettings') }}
            </t-button>
          </div>
        </div>

        <!-- Ollama 本地模型选择器 -->
        <div v-if="formData.source === 'local'" class="form-item">
          <label class="form-label required">{{ $t('model.modelName') }}</label>
          <div class="model-select-row">
            <t-select
              v-model="formData.modelName"
              :loading="loadingOllamaModels"
              :class="{ 'downloading': downloading }"
              :style="downloading ? `--progress: ${downloadProgress}%` : ''"
              filterable
              :filter="handleModelFilter"
              :placeholder="$t('model.searchPlaceholder')"
              @focus="loadOllamaModels"
              @visible-change="handleDropdownVisibleChange"
            >
              <!-- 已下载的模型 -->
              <t-option
                v-for="model in filteredOllamaModels"
                :key="model.name"
                :value="model.name"
                :label="model.name"
              >
                <div class="model-option">
                  <t-icon name="check-circle-filled" class="downloaded-icon" />
                  <span class="model-name">{{ model.name }}</span>
                  <span class="model-size">{{ formatModelSize(model.size) }}</span>
                </div>
              </t-option>
              
              <!-- 下载新模型选项（仅当搜索词不在列表中时显示） -->
              <t-option
                v-if="showDownloadOption"
                :value="`__download__${searchKeyword}`"
                :label="$t('model.editor.downloadLabel', { keyword: searchKeyword })"
                class="download-option"
              >
                <div class="model-option download">
                  <t-icon name="download" class="download-icon" />
                  <span class="model-name">{{ $t('model.editor.downloadLabel', { keyword: searchKeyword }) }}</span>
                </div>
              </t-option>
              
              <!-- 下载进度后缀 -->
              <template v-if="downloading" #suffix>
                <div class="download-suffix">
                  <t-icon name="loading" class="spinning" />
                  <span class="progress-text">{{ downloadProgress.toFixed(1) }}%</span>
                </div>
              </template>
            </t-select>
            
            <!-- 刷新按钮 -->
            <t-button
              variant="text"
              size="small"
              :loading="loadingOllamaModels"
              @click="refreshOllamaModels"
              class="refresh-btn"
            >
              <t-icon name="refresh" />
              {{ $t('model.editor.refreshList') }}
            </t-button>
          </div>
        </div>

        <!-- Remote API 配置 -->
        <template v-if="formData.source === 'remote'">
          <!-- 厂商选择器 -->
          <div class="form-item">
            <label class="form-label">{{ $t('model.editor.providerLabel') }}</label>
            <t-select 
              v-model="formData.provider" 
              :placeholder="$t('model.editor.providerPlaceholder')"
              @change="handleProviderChange"
            >
              <t-option 
                v-for="opt in providerOptions" 
                :key="opt.value" 
                :value="opt.value" 
                :label="opt.label"
              >
                <div class="provider-option">
                  <span class="provider-name">{{ opt.label }}</span>
                  <span class="provider-desc">{{ opt.description }}</span>
                </div>
              </t-option>
            </t-select>
          </div>

          <!-- 模型名称 -->
          <div class="form-item">
            <label class="form-label required">{{ $t('model.modelName') }}</label>
            <t-input 
              v-model="formData.modelName" 
              :placeholder="getModelNamePlaceholder()"
            />
          </div>

          <div class="form-item">
            <label class="form-label required">{{ $t('model.editor.baseUrlLabel') }}</label>
            <t-input 
              v-model="formData.baseUrl" 
              :placeholder="getBaseUrlPlaceholder()"
            />
          </div>

          <div class="form-item">
            <label class="form-label">{{ $t('model.editor.apiKeyOptional') }}</label>
            <t-input 
              v-model="formData.apiKey" 
              type="password"
              :placeholder="$t('model.editor.apiKeyPlaceholder')"
            />
          </div>

          <!-- Remote API 校验 -->
          <div class="form-item">
            <label class="form-label">{{ $t('model.editor.connectionTest') }}</label>
            <div class="api-test-section">
              <t-button 
                variant="outline" 
                @click="checkRemoteAPI"
                :loading="checking"
                :disabled="!formData.modelName || !formData.baseUrl"
              >
                <template #icon>
                  <t-icon 
                    v-if="!checking && remoteChecked && remoteAvailable"
                    name="check-circle-filled" 
                    class="status-icon available"
                  />
                  <t-icon 
                    v-else-if="!checking && remoteChecked && !remoteAvailable"
                    name="close-circle-filled" 
                    class="status-icon unavailable"
                  />
                </template>
                {{ checking ? $t('model.editor.testing') : $t('model.editor.testConnection') }}
              </t-button>
              <span v-if="remoteChecked" :class="['test-message', remoteAvailable ? 'success' : 'error']">
                {{ remoteMessage }}
              </span>
            </div>
          </div>
        </template>

        <!-- Embedding 专用：维度 -->
        <div v-if="modelType === 'embedding'" class="form-item">
          <label class="form-label">{{ $t('model.editor.dimensionLabel') }}</label>
          <div class="dimension-control">
            <t-input 
              v-model.number="formData.dimension" 
              type="number"
            :min="128"
            :max="4096"
            :placeholder="$t('model.editor.dimensionPlaceholder')"
              :disabled="formData.source === 'local' && checking"
            />
            <!-- Ollama 本地模型：自动检测维度按钮 -->
            <t-button 
              v-if="formData.source === 'local' && formData.modelName"
              variant="outline"
              size="small"
              :loading="checking"
              @click="checkOllamaDimension"
              class="dimension-check-btn"
            >
              <t-icon name="refresh" />
              {{ $t('model.editor.checkDimension') }}
            </t-button>
          </div>
          <p v-if="dimensionChecked && dimensionMessage" class="dimension-hint" :class="{ success: dimensionSuccess }">
            {{ dimensionMessage }}
          </p>
        </div>

      </t-form>
          </div>

          <!-- 底部按钮区域 -->
          <div class="modal-footer">
            <t-button theme="default" variant="outline" @click="handleCancel">
              {{ $t('common.cancel') }}
            </t-button>
            <t-button theme="primary" @click="handleConfirm" :loading="saving">
              {{ $t('common.save') }}
            </t-button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch, computed, onUnmounted, nextTick } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { checkOllamaModels, checkRemoteModel, testEmbeddingModel, checkRerankModel, listOllamaModels, downloadOllamaModel, getDownloadProgress, checkOllamaStatus, listModelProviders, type OllamaModelInfo, type ModelProviderOption } from '@/api/initialization'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'

interface ModelFormData {
  id: string
  name: string
  source: 'local' | 'remote'
  provider?: string // Provider identifier: openai, aliyun, zhipu, generic, etc.
  modelName: string
  baseUrl?: string
  apiKey?: string
  dimension?: number
  interfaceType?: 'ollama' | 'openai'
  isDefault: boolean
}

interface Props {
  visible: boolean
  modelType: 'chat' | 'embedding' | 'rerank' | 'vllm'
  modelData?: ModelFormData | null
}

const { t } = useI18n()
const uiStore = useUIStore()

const props = withDefaults(defineProps<Props>(), {
  visible: false,
  modelData: null
})

const emit = defineEmits<{
  'update:visible': [value: boolean]
  'confirm': [data: ModelFormData]
}>()

// API 返回的 Provider 列表
const apiProviderOptions = ref<ModelProviderOption[]>([])
const loadingProviders = ref(false)

// 硬编码的后备 Provider 配置 (当 API 不可用时使用)
const fallbackProviderOptions = computed(() => [
  { 
    value: 'openai', 
    label: t('model.editor.providers.openai.label'), 
    defaultUrls: {
      chat: 'https://api.openai.com/v1',
      embedding: 'https://api.openai.com/v1',
      rerank: 'https://api.openai.com/v1',
      vllm: 'https://api.openai.com/v1'
    },
    description: t('model.editor.providers.openai.description'),
    modelTypes: ['chat', 'embedding', 'vllm']
  },
  { 
    value: 'aliyun', 
    label: t('model.editor.providers.aliyun.label'), 
    defaultUrls: {
      chat: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
      embedding: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
      rerank: 'https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank',
      vllm: 'https://dashscope.aliyuncs.com/compatible-mode/v1'
    },
    description: t('model.editor.providers.aliyun.description'),
    modelTypes: ['chat', 'embedding', 'rerank', 'vllm']
  },
  { 
    value: 'zhipu', 
    label: t('model.editor.providers.zhipu.label'), 
    defaultUrls: {
      chat: 'https://open.bigmodel.cn/api/paas/v4',
      embedding: 'https://open.bigmodel.cn/api/paas/v4/embeddings',
      vllm: 'https://open.bigmodel.cn/api/paas/v4'
    },
    description: t('model.editor.providers.zhipu.description'),
    modelTypes: ['chat', 'embedding', 'vllm']
  },
  { 
    value: 'openrouter', 
    label: t('model.editor.providers.openrouter.label'), 
    defaultUrls: { chat: 'https://openrouter.ai/api/v1' },
    description: t('model.editor.providers.openrouter.description'),
    modelTypes: ['chat']
  },
  { 
    value: 'siliconflow', 
    label: t('model.editor.providers.siliconflow.label'), 
    defaultUrls: {
      chat: 'https://api.siliconflow.cn/v1',
      embedding: 'https://api.siliconflow.cn/v1',
      rerank: 'https://api.siliconflow.cn/v1'
    },
    description: t('model.editor.providers.siliconflow.description'),
    modelTypes: ['chat', 'embedding', 'rerank']
  },
  { 
    value: 'jina', 
    label: t('model.editor.providers.jina.label'), 
    defaultUrls: {
      embedding: 'https://api.jina.ai/v1',
      rerank: 'https://api.jina.ai/v1'
    },
    description: t('model.editor.providers.jina.description'),
    modelTypes: ['embedding', 'rerank']
  },
  { 
    value: 'generic', 
    label: t('model.editor.providers.generic.label'), 
    defaultUrls: {},
    description: t('model.editor.providers.generic.description'),
    modelTypes: ['chat', 'embedding', 'rerank', 'vllm']
  },
])

// 从 API 获取 Provider 列表
const loadProviders = async () => {
  loadingProviders.value = true
  try {
    const providers = await listModelProviders(props.modelType)
    if (providers.length > 0) {
      apiProviderOptions.value = providers
    }
  } catch (error) {
    console.error('Failed to load providers from API, using fallback', error)
  } finally {
    loadingProviders.value = false
  }
}

// 根据当前模型类型过滤的 Provider 列表
const providerOptions = computed(() => {
  // 优先使用 API 返回的数据
  if (apiProviderOptions.value.length > 0) {
    return apiProviderOptions.value
  }
  // 回退到硬编码值，按 modelTypes 过滤
  return fallbackProviderOptions.value.filter(p => 
    p.modelTypes.includes(props.modelType)
  )
})

const dialogVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val)
})

const isEdit = computed(() => !!props.modelData)

const formRef = ref()
const saving = ref(false)
const modelChecked = ref(false)
const modelAvailable = ref(false)
const checking = ref(false)
const remoteChecked = ref(false)
const remoteAvailable = ref(false)
const remoteMessage = ref('')
const dimensionChecked = ref(false)
const dimensionSuccess = ref(false)
const dimensionMessage = ref('')

// Ollama 模型状态
const ollamaModelList = ref<OllamaModelInfo[]>([])
const loadingOllamaModels = ref(false)
const searchKeyword = ref('')
const downloading = ref(false)
const downloadProgress = ref(0)
const currentDownloadModel = ref('')
let downloadInterval: any = null

// Ollama 服务状态
const ollamaServiceStatus = ref<boolean | null>(null)
const checkingOllamaStatus = ref(false)

const formData = ref<ModelFormData>({
  id: '',
  name: '',
  source: 'local',
  provider: 'openai',
  modelName: '',
  baseUrl: '',
  apiKey: '',
  dimension: undefined,
  interfaceType: 'ollama',
  isDefault: false
})

const rules = computed(() => ({
  modelName: [
    { required: true, message: t('model.editor.validation.modelNameRequired') },
    { 
      validator: (val: string) => {
        if (!val || !val.trim()) {
          return { result: false, message: t('model.editor.validation.modelNameEmpty') }
        }
        if (val.trim().length > 100) {
          return { result: false, message: t('model.editor.validation.modelNameMax') }
        }
        return { result: true }
      },
      trigger: 'blur'
    }
  ],
  baseUrl: [
    { 
      required: true, 
      message: t('model.editor.validation.baseUrlRequired'),
      trigger: 'blur'
    },
    {
      validator: (val: string) => {
        if (!val || !val.trim()) {
          return { result: false, message: t('model.editor.validation.baseUrlEmpty') }
        }
        // 简单的 URL 格式校验
        try {
          new URL(val.trim())
          return { result: true }
        } catch {
          return { result: false, message: t('model.editor.validation.baseUrlInvalid') }
        }
      },
      trigger: 'blur'
    }
  ]
}))

// 获取弹窗描述文字
const getModalDescription = () => {
  const key = `model.editor.description.${props.modelType}` as const
  return t(key) || t('model.editor.description.default')
}

// 获取模型名称占位符
const getModelNamePlaceholder = () => {
  if (props.modelType === 'vllm') {
    return formData.value.source === 'local'
      ? t('model.editor.modelNamePlaceholder.localVllm')
      : t('model.editor.modelNamePlaceholder.remoteVllm')
  }
  return formData.value.source === 'local'
    ? t('model.editor.modelNamePlaceholder.local')
    : t('model.editor.modelNamePlaceholder.remote')
}

const getBaseUrlPlaceholder = () => {
  return props.modelType === 'vllm'
    ? t('model.editor.baseUrlPlaceholderVllm')
    : t('model.editor.baseUrlPlaceholder')
}

// 检查Ollama服务状态
const checkOllamaServiceStatus = async () => {
  console.log('开始检查Ollama服务状态...')
  checkingOllamaStatus.value = true
  try {
    const result = await checkOllamaStatus()
    ollamaServiceStatus.value = result.available
    console.log('Ollama服务状态检查完成:', result.available)
  } catch (error) {
    console.error('检查Ollama服务状态失败:', error)
    ollamaServiceStatus.value = false
  } finally {
    checkingOllamaStatus.value = false
  }
}

// 打开Ollama设置窗口
const goToOllamaSettings = async () => {
  console.log('点击跳转到Ollama设置按钮')
  // 关闭当前弹窗
  emit('update:visible', false)
  
  // 先关闭设置弹窗（如果已打开）
  if (uiStore.showSettingsModal) {
    uiStore.closeSettings()
    // 等待 DOM 更新
    await nextTick()
  }
  
  // 打开设置窗口并直接跳转到Ollama设置
  console.log('调用uiStore.openSettings')
  uiStore.openSettings('ollama')
  console.log('uiStore.openSettings调用完成')
}

// 监听 visible 变化，初始化表单
watch(() => props.visible, (val) => {
  if (val) {
    // 锁定背景滚动
    document.body.style.overflow = 'hidden'

    // 检查Ollama服务状态
    checkOllamaServiceStatus()

    // 从 API 加载 Model Provider 列表
    loadProviders()

    if (props.modelData) {
      formData.value = { ...props.modelData }
    } else {
      resetForm()
    }

    // ReRank 模型强制使用 remote 来源（Ollama 不支持 ReRank）
    if (props.modelType === 'rerank') {
      formData.value.source = 'remote'
    }
  } else {
    // 恢复背景滚动
    document.body.style.overflow = ''
  }
})

// 重置表单
const resetForm = () => {
  formData.value = {
    id: generateId(),
    name: '', // 保留字段但不使用，保存时用 modelName
    source: 'local',
    provider: 'generic',
    modelName: '',
    baseUrl: '',
    apiKey: '',
    dimension: undefined, // 默认不填，让用户手动输入或通过检测按钮获取
    interfaceType: undefined,
    isDefault: false
  }
  modelChecked.value = false
  modelAvailable.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
}

// 处理厂商选择变化 (自动填充默认 URL)
const handleProviderChange = (value: string) => {
  const provider = providerOptions.value.find(opt => opt.value === value)
  if (provider && provider.defaultUrls) {
    // 根据当前模型类型获取对应的默认 URL
    const defaultUrl = provider.defaultUrls[props.modelType]
    if (defaultUrl) {
      formData.value.baseUrl = defaultUrl
    }
    // 重置校验状态
    remoteChecked.value = false
    remoteAvailable.value = false
    remoteMessage.value = ''
  }
}

// 监听来源变化，重置校验状态（已合并到下面的 watch）

// 生成唯一ID
const generateId = () => {
  return `model_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
}

// 过滤后的模型列表
const filteredOllamaModels = computed(() => {
  if (!searchKeyword.value) return ollamaModelList.value
  return ollamaModelList.value.filter(model => 
    model.name.toLowerCase().includes(searchKeyword.value.toLowerCase())
  )
})

// 是否显示"下载模型"选项
const showDownloadOption = computed(() => {
  if (!searchKeyword.value.trim()) return false
  // 检查搜索词是否已存在于模型列表中
  const exists = ollamaModelList.value.some(model => 
    model.name.toLowerCase() === searchKeyword.value.toLowerCase()
  )
  return !exists
})

// 自定义过滤逻辑（捕获搜索关键词）
const handleModelFilter = (filterWords: string) => {
  searchKeyword.value = filterWords
  return true // 让 TDesign 使用我们的 filteredOllamaModels
}

// 加载 Ollama 模型列表
const loadOllamaModels = async () => {
  // 只在选择 local 来源时加载
  if (formData.value.source !== 'local') return
  
  loadingOllamaModels.value = true
  try {
    const models = await listOllamaModels()
    ollamaModelList.value = models
  } catch (error) {
    console.error(t('model.editor.loadModelListFailed'), error)
    MessagePlugin.error(t('model.editor.loadModelListFailed'))
  } finally {
    loadingOllamaModels.value = false
  }
}

// 刷新模型列表
const refreshOllamaModels = async () => {
  ollamaModelList.value = [] // 清空以强制重新加载
  await loadOllamaModels()
  MessagePlugin.success(t('model.editor.listRefreshed'))
}

// 监听下拉框可见性变化
const handleDropdownVisibleChange = (visible: boolean) => {
  if (!visible) {
    searchKeyword.value = ''
  }
}

// 格式化模型大小
const formatModelSize = (bytes: number): string => {
  if (!bytes || bytes === 0) return ''
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${(bytes / (1024 * 1024)).toFixed(0)} MB`
}

// 检查模型状态（Ollama本地模型）
const checkModelStatus = async () => {
  if (!formData.value.modelName || formData.value.source !== 'local') {
    return
  }
  
  try {
    // 调用真实 Ollama API 检查模型是否存在
    const result = await checkOllamaModels([formData.value.modelName])
    modelChecked.value = true
    modelAvailable.value = result.models[formData.value.modelName] || false
  } catch (error) {
    console.error('检查模型状态失败:', error)
    modelChecked.value = false
    modelAvailable.value = false
  }
}

// 检查 Ollama 本地 Embedding 模型维度
const checkOllamaDimension = async () => {
  if (!formData.value.modelName || formData.value.source !== 'local' || props.modelType !== 'embedding') {
    return
  }
  
  checking.value = true
  dimensionChecked.value = false
  dimensionMessage.value = ''
  
  try {
    const result = await testEmbeddingModel({
      source: 'local',
      modelName: formData.value.modelName,
      dimension: formData.value.dimension
    })
    
    dimensionChecked.value = true
    dimensionSuccess.value = result.available || false
    
    if (result.available && result.dimension) {
      formData.value.dimension = result.dimension
      dimensionMessage.value = t('model.editor.dimensionDetected', { value: result.dimension })
      MessagePlugin.success(dimensionMessage.value)
    } else {
      dimensionMessage.value = result.message || t('model.editor.dimensionFailed')
      MessagePlugin.warning(dimensionMessage.value)
    }
  } catch (error: any) {
    console.error('检测 Ollama 模型维度失败:', error)
    dimensionChecked.value = true
    dimensionSuccess.value = false
    dimensionMessage.value = error.message || t('model.editor.dimensionFailed')
    MessagePlugin.error(dimensionMessage.value)
  } finally {
    checking.value = false
  }
}

// 检查 Remote API 连接（根据模型类型调用不同的接口）
const checkRemoteAPI = async () => {
  if (!formData.value.modelName || !formData.value.baseUrl) {
    MessagePlugin.warning(t('model.editor.fillModelAndUrl'))
    return
  }
  
  checking.value = true
  remoteChecked.value = false
  remoteMessage.value = ''
  
  try {
    let result: any
    
    // 根据模型类型调用不同的校验接口
    switch (props.modelType) {
      case 'chat':
        // 对话模型（KnowledgeQA）
        result = await checkRemoteModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl,
          apiKey: formData.value.apiKey || ''
        })
        break
        
      case 'embedding':
        // Embedding 模型
        result = await testEmbeddingModel({
          source: 'remote',
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl,
          apiKey: formData.value.apiKey || '',
          dimension: formData.value.dimension,
          provider: formData.value.provider
        })
        // 如果测试成功且返回了维度，自动填充
        if (result.available && result.dimension) {
          formData.value.dimension = result.dimension
        MessagePlugin.info(t('model.editor.remoteDimensionDetected', { value: result.dimension }))
        }
        break
        
      case 'rerank':
        // Rerank 模型
        result = await checkRerankModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl,
          apiKey: formData.value.apiKey || ''
        })
        break
        
      case 'vllm':
        // VLLM 模型（多模态）
        // VLLM 使用 checkRemoteModel 进行基础连接测试
        result = await checkRemoteModel({
          modelName: formData.value.modelName,
          baseUrl: formData.value.baseUrl,
          apiKey: formData.value.apiKey || ''
        })
        break
        
      default:
        MessagePlugin.error(t('model.editor.unsupportedModelType'))
        return
    }
    
    remoteChecked.value = true
    remoteAvailable.value = result.available || false
    remoteMessage.value = result.message || (result.available ? t('model.editor.connectionSuccess') : t('model.editor.connectionFailed'))
    
    if (result.available) {
      MessagePlugin.success(remoteMessage.value)
    } else {
      MessagePlugin.error(remoteMessage.value)
    }
  } catch (error: any) {
    console.error('Remote API 校验失败:', error)
    remoteChecked.value = true
    remoteAvailable.value = false
    remoteMessage.value = error.message || t('model.editor.connectionConfigError')
    MessagePlugin.error(remoteMessage.value)
  } finally {
    checking.value = false
  }
}

// 确认保存
const handleConfirm = async () => {
  try {
    // 手动校验必填字段
    if (!formData.value.modelName || !formData.value.modelName.trim()) {
      MessagePlugin.warning(t('model.editor.validation.modelNameRequired'))
      return
    }
    
    if (formData.value.modelName.trim().length > 100) {
      MessagePlugin.warning(t('model.editor.validation.modelNameMax'))
      return
    }
    
    // 如果是 remote 类型，必须填写 baseUrl
    if (formData.value.source === 'remote') {
      if (!formData.value.baseUrl || !formData.value.baseUrl.trim()) {
        MessagePlugin.warning(t('model.editor.remoteBaseUrlRequired'))
        return
      }
      
      // 校验 Base URL 格式
      try {
        new URL(formData.value.baseUrl.trim())
      } catch {
        MessagePlugin.warning(t('model.editor.validation.baseUrlInvalid'))
        return
      }
    }
    
    // 执行表单验证
    await formRef.value?.validate()
    saving.value = true
    
    // 如果是新增且没有 id，生成一个
    if (!formData.value.id) {
      formData.value.id = generateId()
    }
    
    emit('confirm', { ...formData.value })
    dialogVisible.value = false
    // 移除此处的成功提示，由父组件统一处理
  } catch (error) {
    console.error('表单验证失败:', error)
  } finally {
    saving.value = false
  }
}

// 监听模型选择变化（处理下载逻辑和自动维度检测提示）
watch(() => formData.value.modelName, async (newValue, oldValue) => {
  if (!newValue) return
  
  // 处理下载逻辑
  if (newValue.startsWith('__download__')) {
  // 提取模型名称
  const modelName = newValue.replace('__download__', '')
  
  // 重置选择（避免显示 __download__ 前缀）
  formData.value.modelName = ''
  
  // 开始下载
  await startDownload(modelName)
    return
  }
  
  // 如果是 embedding 模型且选择的是 Ollama 本地模型，且模型名称发生了实际变化
  if (props.modelType === 'embedding' && 
      formData.value.source === 'local' && 
      newValue !== oldValue && 
      oldValue !== '') {
    // 提示用户可以检测维度
    MessagePlugin.info(t('model.editor.dimensionHint'))
  }
})

// 开始下载模型
const startDownload = async (modelName: string) => {
  downloading.value = true
  downloadProgress.value = 0
  currentDownloadModel.value = modelName
  
  try {
    // 启动下载
    const result = await downloadOllamaModel(modelName)
    const taskId = result.taskId
    
    MessagePlugin.success(t('model.editor.downloadStarted', { name: modelName }))
    
    // 轮询下载进度
    downloadInterval = setInterval(async () => {
      try {
        const progress = await getDownloadProgress(taskId)
        downloadProgress.value = progress.progress
        
        if (progress.status === 'completed') {
          // 下载完成
          clearInterval(downloadInterval)
          downloadInterval = null
          downloading.value = false
          
          MessagePlugin.success(t('model.editor.downloadCompleted', { name: modelName }))
          
          // 刷新模型列表
          await loadOllamaModels()
          
          // 自动选中新下载的模型
          formData.value.modelName = modelName
          
          // 重置状态
          downloadProgress.value = 0
          currentDownloadModel.value = ''
          
        } else if (progress.status === 'failed') {
          // 下载失败
          clearInterval(downloadInterval)
          downloadInterval = null
          downloading.value = false
          MessagePlugin.error(progress.message || t('model.editor.downloadFailed', { name: modelName }))
          downloadProgress.value = 0
          currentDownloadModel.value = ''
        }
      } catch (error) {
        console.error('获取下载进度失败:', error)
      }
    }, 1000) // 每秒查询一次
    
  } catch (error: any) {
    downloading.value = false
    downloadProgress.value = 0
    currentDownloadModel.value = ''
    MessagePlugin.error(error.message || t('model.editor.downloadStartFailed'))
  }
}

// 组件卸载时清理定时器
onUnmounted(() => {
  if (downloadInterval) {
    clearInterval(downloadInterval)
  }
})

// 监听来源变化，清理所有状态
watch(() => formData.value.source, () => {
  // 重置校验状态
  modelChecked.value = false
  modelAvailable.value = false
  remoteChecked.value = false
  remoteAvailable.value = false
  remoteMessage.value = ''
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
  
  // 清理下载状态
  searchKeyword.value = ''
  if (downloadInterval) {
    clearInterval(downloadInterval)
    downloadInterval = null
  }
  downloading.value = false
  downloadProgress.value = 0
  currentDownloadModel.value = ''
})

// 监听模型名称变化，清理维度检测状态
watch(() => formData.value.modelName, () => {
  dimensionChecked.value = false
  dimensionSuccess.value = false
  dimensionMessage.value = ''
})

// 取消
const handleCancel = () => {
  dialogVisible.value = false
}
</script>

<style lang="less" scoped>
// 遮罩层
.model-editor-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1200;
  backdrop-filter: blur(4px);
  overflow: hidden;
  padding: 20px;
}

// 弹窗主体
.model-editor-modal {
  position: relative;
  width: 100%;
  max-width: 560px;
  max-height: 90vh;
  background: #fff;
  border-radius: 12px;
  box-shadow: 0 6px 28px rgba(15, 23, 42, 0.08);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

// 关闭按钮
.close-btn {
  position: absolute;
  top: 16px;
  right: 16px;
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #666666;
  transition: all 0.15s ease;
  z-index: 10;

  &:hover {
    background: #f5f5f5;
    color: #333333;
  }
}

// 标题区域
.modal-header {
  padding: 24px 24px 16px;
  border-bottom: 1px solid #e5e7eb;
  flex-shrink: 0;
}

.modal-title {
  margin: 0 0 6px 0;
  font-size: 18px;
  font-weight: 600;
  color: #333333;
}

.modal-desc {
  margin: 0;
  font-size: 13px;
  color: #666666;
  line-height: 1.5;
}

// 内容区域
.modal-body {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
  background: #ffffff;

  // 自定义滚动条
  &::-webkit-scrollbar {
    width: 6px;
  }

  &::-webkit-scrollbar-track {
    background: #f5f5f5;
    border-radius: 3px;
  }

  &::-webkit-scrollbar-thumb {
    background: #d0d0d0;
    border-radius: 3px;
    transition: background 0.15s;

    &:hover {
      background: #b0b0b0;
    }
  }

  :deep(.t-form) {
    .t-form-item {
      display: none;
    }
  }
}

// 表单项样式
.form-item {
  margin-bottom: 20px;

  &:last-child {
    margin-bottom: 0;
  }
}

.form-label {
  display: block;
  margin-bottom: 8px;
  font-size: 14px;
  font-weight: 500;
  color: #333333;

  &.required::after {
    content: '*';
    color: #f56c6c;
    margin-left: 4px;
    font-weight: 600;
  }
}

// 输入框样式
:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea),
:deep(.t-input-number) {
  width: 100%;
  font-size: 13px;

  .t-input__inner,
  .t-input__wrap,
  input,
  textarea {
    font-size: 13px;
    border-radius: 6px;
    border-color: #d9d9d9;
    transition: all 0.15s ease;
  }

  &:hover .t-input__inner,
  &:hover .t-input__wrap,
  &:hover input,
  &:hover textarea {
    border-color: #b3b3b3;
  }

  &.t-is-focused .t-input__inner,
  &.t-is-focused .t-input__wrap,
  &.t-is-focused input,
  &.t-is-focused textarea {
    border-color: #07C05F;
    box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.1);
  }
}

// 厂商选择器样式
.provider-option {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 4px 0;

  .provider-name {
    font-size: 14px;
    font-weight: 500;
    color: #333333;
  }

  .provider-desc {
    font-size: 12px;
    color: #999999;
  }
}

// 单选按钮组
:deep(.t-radio-group) {
  display: flex;
  gap: 24px;

  .t-radio {
    margin-right: 0;
    font-size: 13px;

    &:hover {
      .t-radio__label {
        color: #07C05F;
      }
    }
  }

  .t-radio__label {
    font-size: 13px;
    color: #333333;
    transition: color 0.15s ease;
  }

  .t-radio__input:checked + .t-radio__label {
    color: #07C05F;
    font-weight: 500;
  }
}

// 复选框
:deep(.t-checkbox) {
  font-size: 13px;

  .t-checkbox__label {
    font-size: 13px;
    color: #333333;
  }
}

// 底部按钮区域
.modal-footer {
  padding: 16px 24px;
  border-top: 1px solid #e5e7eb;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
  background: #fafafa;

  :deep(.t-button) {
    min-width: 80px;
    height: 36px;
    font-weight: 500;
    font-size: 14px;
    border-radius: 6px;
    transition: all 0.15s ease;

    &.t-button--theme-primary {
      background: #07C05F;
      border-color: #07C05F;

      &:hover {
        background: #06b04d;
        border-color: #06b04d;
      }

      &:active {
        background: #059642;
        border-color: #059642;
      }
    }

    &.t-button--variant-outline {
      color: #666666;
      border-color: #d9d9d9;

      &:hover {
        border-color: #07C05F;
        color: #07C05F;
        background: rgba(7, 192, 95, 0.04);
      }
    }
  }
}

// 过渡动画
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;

  .model-editor-modal {
    transition: transform 0.2s ease, opacity 0.2s ease;
  }
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .model-editor-modal {
    transform: scale(0.95);
    opacity: 0;
  }
}

// API 测试区域
.api-test-section {
  display: flex;
  align-items: center;
  gap: 12px;

  .test-message {
    font-size: 13px;
    line-height: 1.5;
    flex: 1;

    &.success {
      color: #059669;
    }

    &.error {
      color: #f56c6c;
    }
  }

  :deep(.t-button) {
    min-width: 88px;
    height: 32px;
    font-size: 13px;
    border-radius: 6px;
    flex-shrink: 0;
  }

  .status-icon {
    font-size: 16px;
    flex-shrink: 0;

    &.available {
      color: #07C05F;
    }

    &.unavailable {
      color: #f56c6c;
    }
  }
}

// Ollama 模型选择器样式
.model-option {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 4px 0;
  
  .downloaded-icon {
    font-size: 14px;
    color: #07C05F;
    flex-shrink: 0;
  }
  
  .download-icon {
    font-size: 14px;
    color: #07C05F;
    flex-shrink: 0;
  }
  
  .model-name {
    flex: 1;
    font-size: 13px;
    color: #333333;
  }
  
  .model-size {
    font-size: 12px;
    color: #999999;
    margin-left: auto;
  }
  
  &.download {
    .model-name {
      color: #07C05F;
      font-weight: 500;
    }
  }
}

// 下载进度后缀样式
.download-suffix {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 4px;
  
  .spinning {
    animation: spin 1s linear infinite;
    font-size: 14px;
    color: #07C05F;
  }
  
  .progress-text {
    font-size: 12px;
    font-weight: 500;
    color: #07C05F;
  }
}

// 下载中的选择框进度条效果
:deep(.t-select.downloading) {
  .t-input {
    position: relative;
    overflow: hidden;
    
    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 0;
      bottom: 0;
      width: var(--progress, 0%);
      background: linear-gradient(90deg, rgba(7, 192, 95, 0.08), rgba(7, 192, 95, 0.15));
      transition: width 0.3s ease;
      z-index: 0;
      border-radius: 5px 0 0 5px;
    }
    
    .t-input__inner,
    input {
      position: relative;
      z-index: 1;
      background: transparent !important;
    }
  }
}

.model-select-row {
  display: flex;
  align-items: center;
  gap: 8px;

  .t-select {
    flex: 1;
  }

  :deep(.t-button) {
    height: 32px;
    font-size: 13px;
    border-radius: 6px;
    flex-shrink: 0;
  }
}

.refresh-btn {
  margin-top: 0;
  font-size: 13px;
  color: #666666;
  flex-shrink: 0;

  &:hover {
    color: #07C05F;
    background: rgba(7, 192, 95, 0.04);
  }
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

// 维度控制样式
.dimension-control {
  display: flex;
  align-items: center;
  gap: 8px;

  :deep(.t-input) {
    flex: 1;
  }
}

.dimension-check-btn {
  flex-shrink: 0;
  font-size: 12px;
}

.dimension-hint {
  margin: 8px 0 0 0;
  font-size: 13px;
  line-height: 1.5;
  color: #e34d59;

  &.success {
    color: #07C05F;
  }
}

// Ollama不可用提示样式
.ollama-unavailable-tip {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
  padding: 10px 12px;
  background: #fef2f2;
  border: 1px solid #fecaca;
  border-radius: 6px;
  font-size: 13px;

  .tip-icon {
    color: #f56c6c;
    font-size: 16px;
    flex-shrink: 0;
    margin-right: 2px;

    &.info {
      color: #07C05F;
    }
  }

  .tip-text {
    color: #dc2626;
    flex: 1;
    line-height: 1.5;
  }

  // ReRank提示使用主题绿色风格，与主页面保持一致
  &.rerank-tip {
    background: #f0fdf6;
    border: 1px solid #d1fae5;
    border-left: 3px solid #07C05F;

    .tip-text {
      color: #166534;
    }
  }

  :deep(.tip-link) {
    color: #07C05F;
    font-size: 13px;
    font-weight: 500;
    padding: 4px 6px 4px 10px !important;
    min-height: auto !important;
    height: auto !important;
    line-height: 1.4 !important;
    text-decoration: none;
    white-space: nowrap;
    display: inline-flex !important;
    align-items: center !important;
    gap: 1px;
    border-radius: 4px;
    transition: all 0.2s ease;

    &:hover {
      background: rgba(7, 192, 95, 0.08) !important;
      color: #05a04f !important;
    }

    &:active {
      background: rgba(7, 192, 95, 0.12) !important;
    }

    .t-icon {
      font-size: 14px !important;
      margin: 0 !important;
      line-height: 1 !important;
      display: inline-flex !important;
      align-items: center !important;
    }
  }
}
</style>

