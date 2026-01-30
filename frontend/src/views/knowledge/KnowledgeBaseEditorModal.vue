<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="visible" class="settings-overlay" @click.self="handleClose">
        <div class="settings-modal">
          <!-- 关闭按钮 -->
          <button class="close-btn" @click="handleClose" :aria-label="$t('general.close')">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M15 5L5 15M5 5L15 15" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
          </button>

          <div class="settings-container">
            <!-- 左侧导航 -->
            <div class="settings-sidebar">
              <div class="sidebar-header">
                <h2 class="sidebar-title">{{ mode === 'create' ? $t('knowledgeEditor.titleCreate') : $t('knowledgeEditor.titleEdit') }}</h2>
              </div>
              <div class="settings-nav">
                <div 
                  v-for="(item, index) in navItems" 
                  :key="index"
                  :class="['nav-item', { 'active': currentSection === item.key }]"
                  @click="currentSection = item.key"
                >
                  <t-icon :name="item.icon" class="nav-icon" />
                  <span class="nav-label">{{ item.label }}</span>
                </div>
              </div>
            </div>

            <!-- 右侧内容区域 -->
            <div class="settings-content">
              <div class="content-wrapper">
                <!-- 基本信息 -->
                <div v-show="currentSection === 'basic'" class="section">
                  <div v-if="formData" class="section-content">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('knowledgeEditor.basic.title') }}</h3>
                      <p class="section-desc">{{ $t('knowledgeEditor.basic.description') }}</p>
                    </div>
                    <div class="section-body">
                      <div class="form-item">
                        <label class="form-label required">{{ $t('knowledgeEditor.basic.typeLabel') }}</label>
                        <t-radio-group
                          v-model="formData.type"
                          :disabled="mode === 'edit'"
                        >
                          <t-radio-button value="document">{{ $t('knowledgeEditor.basic.typeDocument') }}</t-radio-button>
                          <t-radio-button value="faq">{{ $t('knowledgeEditor.basic.typeFAQ') }}</t-radio-button>
                        </t-radio-group>
                        <p class="form-tip">{{ $t('knowledgeEditor.basic.typeDescription') }}</p>
                      </div>
                      <div class="form-item">
                        <label class="form-label required">{{ $t('knowledgeEditor.basic.nameLabel') }}</label>
                        <t-input 
                          v-model="formData.name" 
                          :placeholder="$t('knowledgeEditor.basic.namePlaceholder')"
                          :maxlength="50"
                        />
                      </div>
                      <div class="form-item">
                        <label class="form-label">{{ $t('knowledgeEditor.basic.descriptionLabel') }}</label>
                        <t-textarea 
                          v-model="formData.description" 
                          :placeholder="$t('knowledgeEditor.basic.descriptionPlaceholder')"
                          :maxlength="200"
                          :autosize="{ minRows: 3, maxRows: 6 }"
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 模型配置 -->
                <div v-show="currentSection === 'models'" class="section">
                  <KBModelConfig
                    ref="modelConfigRef"
                    v-if="formData"
                    :config="formData.modelConfig"
                    :has-files="hasFiles"
                    :all-models="allModels"
                    @update:config="handleModelConfigUpdate"
                  />
                </div>

                <!-- FAQ 配置 -->
                <div v-if="isFAQ && formData" v-show="currentSection === 'faq'" class="section">
                  <div class="section-content">
                    <div class="section-header">
                      <h3 class="section-title">{{ $t('knowledgeEditor.faq.title') }}</h3>
                      <p class="section-desc">{{ $t('knowledgeEditor.faq.description') }}</p>
                    </div>
                    <div class="section-body">
                      <div class="form-item">
                        <label class="form-label required">{{ $t('knowledgeEditor.faq.indexModeLabel') }}</label>
                        <t-radio-group
                          v-model="formData.faqConfig.indexMode"
                        >
                          <t-radio-button value="question_only">{{ $t('knowledgeEditor.faq.modes.questionOnly') }}</t-radio-button>
                          <t-radio-button value="question_answer">{{ $t('knowledgeEditor.faq.modes.questionAnswer') }}</t-radio-button>
                        </t-radio-group>
                        <p class="form-tip">{{ $t('knowledgeEditor.faq.indexModeDescription') }}</p>
                      </div>
                      <div class="form-item">
                        <label class="form-label required">{{ $t('knowledgeEditor.faq.questionIndexModeLabel') }}</label>
                        <t-radio-group
                          v-model="formData.faqConfig.questionIndexMode"
                        >
                          <t-radio-button value="combined">{{ $t('knowledgeEditor.faq.modes.combined') }}</t-radio-button>
                          <t-radio-button value="separate">{{ $t('knowledgeEditor.faq.modes.separate') }}</t-radio-button>
                        </t-radio-group>
                        <p class="form-tip">{{ $t('knowledgeEditor.faq.questionIndexModeDescription') }}</p>
                      </div>
                      <div class="faq-guide">
                        <p>{{ $t('knowledgeEditor.faq.entryGuide') }}</p>
                      </div>
                    </div>
                  </div>
                </div>

                <!-- 分块设置 -->
                <div v-if="!isFAQ" v-show="currentSection === 'chunking'" class="section">
                  <KBChunkingSettings
                    v-if="formData"
                    :config="formData.chunkingConfig"
                    @update:config="handleChunkingConfigUpdate"
                  />
                </div>

                <!-- 图谱设置 -->
                <div v-if="!isFAQ" v-show="currentSection === 'graph'" class="section">
                  <GraphSettings
                    v-if="formData"
                    :graph-extract="formData.nodeExtractConfig"
                    :all-models="allModels"
                    @update:graphExtract="handleNodeExtractUpdate"
                  />
                </div>

                <!-- 高级设置 -->
                <div v-if="!isFAQ" v-show="currentSection === 'advanced'" class="section">
                  <KBAdvancedSettings
                    ref="advancedSettingsRef"
                    v-if="formData"
                    :multimodal="formData.multimodalConfig"
                    :question-generation="formData.questionGenerationConfig"
                    :all-models="allModels"
                    @update:multimodal="handleMultimodalUpdate"
                    @update:question-generation="handleQuestionGenerationUpdate"
                  />
                </div>

                <!-- 共享设置（仅编辑模式） -->
                <div v-if="mode === 'edit' && kbId" v-show="currentSection === 'share'" class="section">
                  <KBShareSettings :kb-id="kbId" />
                </div>
              </div>

              <!-- 保存按钮 -->
              <div class="settings-footer">
                <t-button theme="default" variant="outline" @click="handleClose">
                  {{ $t('common.cancel') }}
                </t-button>
                <t-button theme="primary" @click="handleSubmit" :loading="saving">
                  {{ mode === 'create' ? $t('knowledgeEditor.buttons.create') : $t('knowledgeEditor.buttons.save') }}
                </t-button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { createKnowledgeBase, getKnowledgeBaseById, listKnowledgeFiles, updateKnowledgeBase } from '@/api/knowledge-base'
import { updateKBConfig, type KBModelConfigRequest } from '@/api/initialization'
import { listModels } from '@/api/model'
import { useUIStore } from '@/stores/ui'
import KBModelConfig from './settings/KBModelConfig.vue'
import KBChunkingSettings from './settings/KBChunkingSettings.vue'
import KBAdvancedSettings from './settings/KBAdvancedSettings.vue'
import GraphSettings from './settings/GraphSettings.vue'
import KBShareSettings from './settings/KBShareSettings.vue'
import { useI18n } from 'vue-i18n'

const uiStore = useUIStore()
const { t } = useI18n()

// Props
const props = defineProps<{
  visible: boolean
  mode: 'create' | 'edit'
  kbId?: string
  initialType?: 'document' | 'faq'
}>()

// Emits
const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'success', kbId: string): void
}>()

const currentSection = ref<string>('basic')
const saving = ref(false)
const loading = ref(false)
const allModels = ref<any[]>([])
const hasFiles = ref(false)

const navItems = computed(() => {
  const items = [
    { key: 'basic', icon: 'info-circle', label: t('knowledgeEditor.sidebar.basic') },
    { key: 'models', icon: 'control-platform', label: t('knowledgeEditor.sidebar.models') }
  ]
  if (formData.value?.type === 'faq') {
    items.push({ key: 'faq', icon: 'help-circle', label: t('knowledgeEditor.sidebar.faq') })
  } else {
    items.push(
      { key: 'chunking', icon: 'file-copy', label: t('knowledgeEditor.sidebar.chunking') },
      { key: 'graph', icon: 'chart-bubble', label: t('knowledgeEditor.sidebar.graph') },
      { key: 'advanced', icon: 'setting', label: t('knowledgeEditor.sidebar.advanced') }
    )
  }
  // 只在编辑模式下显示共享标签页
  if (props.mode === 'edit' && props.kbId) {
    items.push({ key: 'share', icon: 'share', label: t('knowledgeEditor.sidebar.share') })
  }
  return items
})

// 模型配置引用
const modelConfigRef = ref<InstanceType<typeof KBModelConfig>>()
const advancedSettingsRef = ref<InstanceType<typeof KBAdvancedSettings>>()

// 表单数据
const formData = ref<any>(null)
const isFAQ = computed(() => formData.value?.type === 'faq')

watch(
  () => formData.value?.type,
  (newType, oldType) => {
    if (!formData.value) return
    if (newType === 'faq') {
      if (!formData.value.faqConfig) {
        formData.value.faqConfig = { indexMode: 'question_only', questionIndexMode: 'separate' }
      }
      if (!['basic', 'models', 'faq'].includes(currentSection.value)) {
        currentSection.value = 'faq'
      }
    } else if (oldType === 'faq' && currentSection.value === 'faq') {
      currentSection.value = 'basic'
    }
  }
)

// 初始化表单数据
const initFormData = (type: 'document' | 'faq' = 'document') => {
  return {
    type,
    name: '',
    description: '',
    faqConfig: {
      indexMode: 'question_only',
      questionIndexMode: 'separate'
    },
    modelConfig: {
      llmModelId: '',
      embeddingModelId: ''
    },
    chunkingConfig: {
      chunkSize: 512,
      chunkOverlap: 100,
      separators: ['\n\n', '\n', '。', '！', '？', ';', '；']
    },
    multimodalConfig: {
      enabled: false,
      storageType: 'minio' as 'minio' | 'cos',
      vllmModelId: '',
      minio: {
        bucketName: '',
        useSSL: false,
        pathPrefix: ''
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
    nodeExtractConfig: {
      enabled: false,
      text: '',
      tags: [] as string[],
      nodes: [] as Array<{
        name: string
        attributes: string[]
      }>,
      relations: [] as Array<{
        node1: string
        node2: string
        type: string
      }>
    },
    questionGenerationConfig: {
      enabled: false,
      questionCount: 3
    },
  }
}

// 加载所有模型
const loadAllModels = async () => {
  try {
    const models = await listModels()
    allModels.value = models || []
  } catch (error) {
    console.error('Failed to load model list:', error)
    MessagePlugin.error(t('knowledgeEditor.messages.loadModelsFailed'))
    allModels.value = []
  }
}

// 加载知识库数据（编辑模式）
const loadKBData = async () => {
  if (props.mode !== 'edit' || !props.kbId) return
  
  loading.value = true
  try {
    const [kbInfo, models, filesResult] = await Promise.all([
      getKnowledgeBaseById(props.kbId),
      loadAllModels(),
      listKnowledgeFiles(props.kbId, { page: 1, page_size: 1 })
    ])
    
    if (!kbInfo || !kbInfo.data) {
      throw new Error(t('knowledgeEditor.messages.notFound'))
    }

    const kb = kbInfo.data
    hasFiles.value = (filesResult as any)?.total > 0
    
    // 设置表单数据
    const kbType = (kb.type as 'document' | 'faq') || 'document'
    formData.value = {
      type: kbType,
      name: kb.name || '',
      description: kb.description || '',
      faqConfig: {
        indexMode: kb.faq_config?.index_mode || 'question_only',
        questionIndexMode: kb.faq_config?.question_index_mode || 'separate'
      },
      modelConfig: {
        llmModelId: kb.summary_model_id || '',
        embeddingModelId: kb.embedding_model_id || ''
      },
      chunkingConfig: {
        chunkSize: kb.chunking_config?.chunk_size || 512,
        chunkOverlap: kb.chunking_config?.chunk_overlap || 100,
        separators: kb.chunking_config?.separators || ['\n\n', '\n', '。', '！', '？', ';', '；']
      },
      multimodalConfig: {
        enabled: !!(kb.vlm_config?.enabled || (kb.cos_config?.provider && kb.cos_config?.bucket_name)),
        storageType: (kb.cos_config?.provider || 'minio') as 'minio' | 'cos',
        vllmModelId: kb.vlm_config?.model_id || '',
        minio: {
          bucketName: kb.cos_config?.bucket_name || '',
          useSSL: kb.cos_config?.use_ssl || false,
          pathPrefix: kb.cos_config?.path_prefix || ''
        },
        cos: {
          secretId: kb.cos_config?.secret_id || '',
          secretKey: kb.cos_config?.secret_key || '',
          region: kb.cos_config?.region || '',
          bucketName: kb.cos_config?.bucket_name || '',
          appId: kb.cos_config?.app_id || '',
          pathPrefix: kb.cos_config?.path_prefix || ''
        }
      },
      nodeExtractConfig: {
        enabled: kb.extract_config?.enabled || false,
        text: kb.extract_config?.text || '',
        tags: kb.extract_config?.tags || [],
        nodes: (kb.extract_config?.nodes || []).map((node: any) => ({
          name: node.name,
          attributes: node.attributes || []
        })),
        relations: kb.extract_config?.relations || []
      },
      questionGenerationConfig: {
        enabled: kb.question_generation_config?.enabled || false,
        questionCount: kb.question_generation_config?.question_count || 3
      },
    }
  } catch (error) {
    console.error('Failed to load knowledge base data:', error)
    MessagePlugin.error(t('knowledgeEditor.messages.loadDataFailed'))
    handleClose()
  } finally {
    loading.value = false
  }
}

// 处理配置更新
const handleModelConfigUpdate = (config: any) => {
  if (formData.value) {
    formData.value.modelConfig = { ...config }
  }
}

const handleChunkingConfigUpdate = (config: any) => {
  if (formData.value) {
    formData.value.chunkingConfig = { ...config }
  }
}

const handleMultimodalUpdate = (config: any) => {
  if (formData.value) {
    formData.value.multimodalConfig = { ...config }
  }
}

const handleQuestionGenerationUpdate = (config: any) => {
  if (formData.value) {
    formData.value.questionGenerationConfig = { ...config }
  }
}

const handleNodeExtractUpdate = (config: any) => {
  if (formData.value) {
    formData.value.nodeExtractConfig = { ...config }
  }
}

// 验证表单
const validateForm = (): boolean => {
  if (!formData.value) return false

  // 验证基本信息
  if (!formData.value.name || !formData.value.name.trim()) {
    MessagePlugin.warning(t('knowledgeEditor.messages.nameRequired'))
    currentSection.value = 'basic'
    return false
  }

  // 验证模型配置 - 必须配置 embedding 和 summary 模型
  if (!formData.value.modelConfig.embeddingModelId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.embeddingRequired'))
    currentSection.value = 'models'
    return false
  }

  if (!formData.value.modelConfig.llmModelId) {
    MessagePlugin.warning(t('knowledgeEditor.messages.summaryRequired'))
    currentSection.value = 'models'
    return false
  }

  // 验证多模态配置（如果启用）
  if (formData.value.multimodalConfig.enabled) {
    const validation = (advancedSettingsRef.value as any)?.validateMultimodal?.()
    if (validation && !validation.valid) {
      MessagePlugin.warning(validation.message || t('knowledgeEditor.messages.multimodalInvalid'))
      currentSection.value = 'advanced'
      return false
    }
  }

  if (formData.value.type === 'faq' && !formData.value.faqConfig?.indexMode) {
    MessagePlugin.warning(t('knowledgeEditor.messages.indexModeRequired'))
    currentSection.value = 'faq'
    return false
  }

  return true
}

// 构建提交数据
const buildSubmitData = () => {
  if (!formData.value) return null

  const data: any = {
    name: formData.value.name,
    description: formData.value.description,
    type: formData.value.type,
    chunking_config: {
      chunk_size: formData.value.chunkingConfig.chunkSize,
      chunk_overlap: formData.value.chunkingConfig.chunkOverlap,
      separators: formData.value.chunkingConfig.separators,
      enable_multimodal: formData.value.multimodalConfig.enabled
    },
    embedding_model_id: formData.value.modelConfig.embeddingModelId,
    summary_model_id: formData.value.modelConfig.llmModelId
  }

  // 添加多模态配置
  data.vlm_config = {
    enabled: formData.value.multimodalConfig.enabled,
    model_id: formData.value.multimodalConfig.enabled
      ? (formData.value.multimodalConfig.vllmModelId || '')
      : ''
  }

  if (formData.value.multimodalConfig.enabled) {
    const storageType = formData.value.multimodalConfig.storageType
    if (storageType === 'minio') {
      data.cos_config = {
        provider: 'minio',
        bucket_name: formData.value.multimodalConfig.minio.bucketName,
        use_ssl: formData.value.multimodalConfig.minio.useSSL,
        path_prefix: formData.value.multimodalConfig.minio.pathPrefix || undefined
      }
    } else {
      data.cos_config = {
        provider: 'cos',
        secret_id: formData.value.multimodalConfig.cos.secretId,
        secret_key: formData.value.multimodalConfig.cos.secretKey,
        region: formData.value.multimodalConfig.cos.region,
        bucket_name: formData.value.multimodalConfig.cos.bucketName,
        app_id: formData.value.multimodalConfig.cos.appId,
        path_prefix: formData.value.multimodalConfig.cos.pathPrefix || undefined
      }
    }
  }

  // 添加知识图谱配置
  if (formData.value.nodeExtractConfig.enabled) {
    data.extract_config = {
      enabled: true,
      text: formData.value.nodeExtractConfig.text,
      tags: formData.value.nodeExtractConfig.tags,
      nodes: formData.value.nodeExtractConfig.nodes,
      relations: formData.value.nodeExtractConfig.relations
    }
  }

  // 添加问题生成配置
  if (formData.value.questionGenerationConfig?.enabled) {
    data.question_generation_config = {
      enabled: true,
      question_count: formData.value.questionGenerationConfig.questionCount || 3
    }
  }

  if (formData.value.type === 'faq') {
    data.faq_config = {
      index_mode: formData.value.faqConfig?.indexMode || 'question_only',
      question_index_mode: formData.value.faqConfig?.questionIndexMode || 'separate'
    }
  }

  return data
}

// 提交表单
const handleSubmit = async () => {
  if (!validateForm()) {
    return
  }

  saving.value = true
  try {
    const data = buildSubmitData()
    if (!data) {
      throw new Error(t('knowledgeEditor.messages.buildDataFailed'))
    }

    if (props.mode === 'create') {
      // 创建模式：一次性创建知识库及所有配置
      const result: any = await createKnowledgeBase(data)
      if (!result.success || !result.data?.id) {
        throw new Error(result.message || t('knowledgeEditor.messages.createFailed'))
      }
      MessagePlugin.success(t('knowledgeEditor.messages.createSuccess'))
      emit('success', result.data.id)
    } else {
      // 编辑模式：分别更新基本信息和配置
      if (!props.kbId) {
        throw new Error(t('knowledgeEditor.messages.missingId'))
      }

      // 1. 更新基本信息（名称、描述）和 FAQ 配置
      const updateConfig: any = {}
      if (formData.value.type === 'faq' && formData.value.faqConfig) {
        updateConfig.faq_config = {
          index_mode: formData.value.faqConfig.indexMode || 'question_only',
          question_index_mode: formData.value.faqConfig.questionIndexMode || 'separate'
        }
      }
      await updateKnowledgeBase(props.kbId, {
        name: data.name,
        description: data.description,
        config: updateConfig
      })

      // 2. 更新完整配置（模型、分块、多模态、知识图谱等）
      const config: KBModelConfigRequest = {
        llmModelId: data.summary_model_id,
        embeddingModelId: data.embedding_model_id,
        vlm_config: data.vlm_config,
        documentSplitting: {
          chunkSize: data.chunking_config.chunk_size,
          chunkOverlap: data.chunking_config.chunk_overlap,
          separators: data.chunking_config.separators
        },
        multimodal: {
          enabled: !!data.cos_config || !!data.vlm_config?.enabled,
          storageType: data.cos_config?.provider || 'minio',
          cos: data.cos_config?.provider === 'cos' ? {
            secretId: data.cos_config.secret_id,
            secretKey: data.cos_config.secret_key,
            region: data.cos_config.region,
            bucketName: data.cos_config.bucket_name,
            appId: data.cos_config.app_id,
            pathPrefix: data.cos_config.path_prefix || ''
          } : undefined,
          minio: data.cos_config?.provider === 'minio' ? {
            bucketName: data.cos_config.bucket_name,
            useSSL: data.cos_config.use_ssl || false,
            pathPrefix: data.cos_config.path_prefix || ''
          } : undefined
        },
        nodeExtract: {
          enabled: data.extract_config?.enabled || false,
          text: data.extract_config?.text || '',
          tags: data.extract_config?.tags || [],
          nodes: data.extract_config?.nodes || [],
          relations: data.extract_config?.relations || []
        },
        questionGeneration: {
          enabled: data.question_generation_config?.enabled || false,
          questionCount: data.question_generation_config?.question_count || 3
        }
      }

      await updateKBConfig(props.kbId, config)
      MessagePlugin.success(t('knowledgeEditor.messages.updateSuccess'))
      emit('success', props.kbId)
    }
    
    handleClose()
  } catch (error: any) {
    console.error('Knowledge base operation failed:', error)
    MessagePlugin.error(error?.message || t('common.operationFailed'))
  } finally {
    saving.value = false
  }
}

// 重置所有状态
const resetState = () => {
  currentSection.value = 'basic'
  formData.value = null
  hasFiles.value = false
  saving.value = false
  loading.value = false
}

// 关闭弹窗
const handleClose = () => {
  emit('update:visible', false)
  setTimeout(() => {
    resetState()
  }, 300)
}

// 监听弹窗打开/关闭
watch(() => props.visible, async (newVal) => {
  if (newVal) {
    // 打开弹窗时，先重置状态
    resetState()
    
    // 检查是否有初始 section，如果有则跳转
    if (uiStore.kbEditorInitialSection) {
      currentSection.value = uiStore.kbEditorInitialSection
    }
    
    // 加载模型列表
    await loadAllModels()
    
    // 根据模式加载数据
    if (props.mode === 'edit' && props.kbId) {
      await loadKBData()
    } else {
      // 创建模式：初始化空表单
      formData.value = initFormData(props.initialType || 'document')
      hasFiles.value = false
    }
  } else {
    // 关闭弹窗时，延迟重置状态（等待动画结束）
    setTimeout(() => {
      resetState()
      currentSection.value = 'basic' // 重置为默认 section
    }, 300)
  }
})

// 监听全局设置弹窗关闭后刷新模型列表
watch(
  () => uiStore.showSettingsModal,
  async (visible, previous) => {
    if (!visible && previous && props.visible) {
      await loadAllModels()
    }
  }
)
</script>

<style scoped lang="less">
// 复用创建知识库的样式
.settings-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.settings-modal {
  position: relative;
  width: 90vw;
  max-width: 1100px;
  height: 85vh;
  max-height: 750px;
  background: #fff;
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.close-btn {
  position: absolute;
  top: 20px;
  right: 20px;
  width: 32px;
  height: 32px;
  border: none;
  background: #f5f5f5;
  border-radius: 6px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #666;
  transition: all 0.2s ease;
  z-index: 10;

  &:hover {
    background: #e5e5e5;
    color: #000;
  }
}

.settings-container {
  display: flex;
  height: 100%;
  overflow: hidden;
}

.settings-sidebar {
  width: 200px;
  background: #fafafa;
  border-right: 1px solid #e5e5e5;
  display: flex;
  flex-direction: column;
  flex-shrink: 0;
}

.sidebar-header {
  padding: 24px 20px;
  border-bottom: 1px solid #e5e5e5;
}

.sidebar-title {
  margin: 0;
  font-family: "PingFang SC";
  font-size: 18px;
  font-weight: 600;
  color: #000000e6;
}

.settings-nav {
  flex: 1;
  padding: 12px 8px;
  overflow-y: auto;
}

.nav-item {
  display: flex;
  align-items: center;
  padding: 10px 12px;
  margin-bottom: 4px;
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s ease;
  font-family: "PingFang SC";
  font-size: 14px;
  color: #00000099;

  &:hover {
    background: #f0f0f0;
  }

  &.active {
    background: #07c05f1a;
    color: #07c05f;
    font-weight: 500;
  }
}

.nav-icon {
  margin-right: 8px;
  font-size: 18px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.nav-label {
  flex: 1;
}

.settings-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.content-wrapper {
  flex: 1;
  overflow-y: auto;
  padding: 24px 32px;
}

.section {
  margin-bottom: 32px;

  &:last-child {
    margin-bottom: 0;
  }
}

.section-content {
  .section-header {
    margin-bottom: 20px;
  }

  .section-title {
    margin: 0 0 8px 0;
    font-family: "PingFang SC";
    font-size: 16px;
    font-weight: 600;
    color: #000000e6;
  }

  .section-desc {
    margin: 0;
    font-family: "PingFang SC";
    font-size: 14px;
    color: #00000066;
    line-height: 22px;
  }

  .section-body {
    background: #fff;
  }
}

.form-item {
  margin-bottom: 20px;

  &:last-child {
    margin-bottom: 0;
  }
}

.form-label {
  display: block;
  margin-bottom: 8px;
  font-family: "PingFang SC";
  font-size: 14px;
  font-weight: 500;
  color: #000000e6;

  &.required::after {
    content: '*';
    color: #FA5151;
    margin-left: 4px;
  }
}

.form-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #00000066;
}

.faq-guide {
  margin-top: 20px;
  padding: 12px 16px;
  border-radius: 8px;
  background: #f5f5f5;
  color: #00000099;
  font-size: 13px;
  line-height: 20px;
}

.settings-footer {
  padding: 16px 32px;
  border-top: 1px solid #e5e5e5;
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  flex-shrink: 0;
}

// 过渡动画
.modal-enter-active,
.modal-leave-active {
  transition: all 0.3s ease;
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;

  .settings-modal {
    transform: scale(0.95);
  }
}

// Radio-group 样式优化，符合项目主题风格
:deep(.t-radio-group) {
  .t-radio-group--filled {
    background: #f5f5f5;
  }
  .t-radio-button {
    border-color: #d9d9d9;
    // color: #00000099;

    &:hover:not(.t-is-disabled) {
      border-color: #07c05f;
      color: #07c05f;
    }

    &.t-is-checked {
      background: #07c05f;
      border-color: #07c05f;
      color: #fff;

      &:hover:not(.t-is-disabled) {
        background: #05a04f;
        border-color: #05a04f;
        color: #fff;
      }
    }

    // 禁用状态样式
    &.t-is-disabled {
      background: #f5f5f5;
      border-color: #d9d9d9;
      color: #00000040;
      cursor: not-allowed;
      opacity: 0.6;

      &.t-is-checked {
        background: #f0f0f0;
        border-color: #d9d9d9;
        color: #00000066;
      }
    }
  }
}
</style>

