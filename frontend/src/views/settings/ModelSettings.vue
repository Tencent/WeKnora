<template>
  <div class="model-settings">
    <div class="section-header">
      <h2>{{ $t('modelSettings.title') }}</h2>
      <p class="section-description">{{ $t('modelSettings.description') }}</p>
      
      <!-- 内置模型说明 -->
      <div class="builtin-models-info">
        <div class="info-box">
          <div class="info-header">
            <t-icon name="info-circle" class="info-icon" />
            <span class="info-title">{{ $t('modelSettings.builtinModels.title') }}</span>
          </div>
          <div class="info-content">
            <p>{{ $t('modelSettings.builtinModels.description') }}</p>
            <p class="doc-link">
              <t-icon name="link" class="link-icon" />
              <a href="https://github.com/Tencent/WeKnora/blob/main/docs/BUILTIN_MODELS.md" target="_blank" rel="noopener noreferrer">
                {{ $t('modelSettings.builtinModels.viewGuide') }}
              </a>
            </p>
          </div>
        </div>
      </div>
    </div>

    <!-- 对话模型 -->
    <div class="model-category-section" data-model-type="chat">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('modelSettings.chat.title') }}</h3>
          <p>{{ $t('modelSettings.chat.desc') }}</p>
        </div>
        <t-button size="small" theme="primary" @click="openAddDialog('chat')" class="add-model-btn">
          <template #icon>
            <t-icon name="add" class="add-icon" />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
      
      <div v-if="chatModels.length > 0" class="model-list-container">
        <div v-for="model in chatModels" :key="model.id" class="model-card" :class="{ 'builtin-model': model.isBuiltin }">
          <div class="model-info">
            <div class="model-name">
              {{ model.name }}
              <t-tag v-if="model.isBuiltin" theme="primary" size="small">{{ $t('modelSettings.builtinTag') }}</t-tag>
            </div>
            <div class="model-meta">
              <span class="source-tag">{{ model.source === 'local' ? 'Ollama' : $t('modelSettings.source.remote') }}</span>
              <!-- <span class="model-id">{{ model.modelName }}</span> -->
            </div>
          </div>
          <div class="model-actions">
            <t-dropdown 
              :options="getModelOptions('chat', model)" 
              @click="(data: any) => handleMenuAction(data, 'chat', model)"
              placement="bottom-right"
              attach="body"
            >
              <t-button variant="text" shape="square" size="small" class="more-btn">
                <t-icon name="more" />
              </t-button>
            </t-dropdown>
          </div>
        </div>
      </div>
      <div v-else class="empty-state">
        <p class="empty-text">{{ $t('modelSettings.chat.empty') }}</p>
        <t-button theme="default" variant="outline" size="small" @click="openAddDialog('chat')">
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- Embedding 模型 -->
    <div class="model-category-section" data-model-type="embedding">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('modelSettings.embedding.title') }}</h3>
          <p>{{ $t('modelSettings.embedding.desc') }}</p>
        </div>
        <t-button size="small" theme="primary" @click="openAddDialog('embedding')" class="add-model-btn">
          <template #icon>
            <t-icon name="add" class="add-icon" />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
      
      <div v-if="embeddingModels.length > 0" class="model-list-container">
        <div v-for="model in embeddingModels" :key="model.id" class="model-card" :class="{ 'builtin-model': model.isBuiltin }">
          <div class="model-info">
            <div class="model-name">
              {{ model.name }}
              <t-tag v-if="model.isBuiltin" theme="primary" size="small">{{ $t('modelSettings.builtinTag') }}</t-tag>
            </div>
            <div class="model-meta">
              <span class="source-tag">{{ model.source === 'local' ? 'Ollama' : $t('modelSettings.source.remote') }}</span>
              <!-- <span class="model-id">{{ model.modelName }}</span> -->
              <span v-if="model.dimension" class="dimension">{{ $t('model.editor.dimensionLabel') }}: {{ model.dimension }}</span>
            </div>
          </div>
          <div class="model-actions">
            <t-dropdown 
              :options="getModelOptions('embedding', model)" 
              @click="(data: any) => handleMenuAction(data, 'embedding', model)"
              placement="bottom-right"
              attach="body"
            >
              <t-button variant="text" shape="square" size="small" class="more-btn">
                <t-icon name="more" />
              </t-button>
            </t-dropdown>
          </div>
        </div>
      </div>
      <div v-else class="empty-state">
        <p class="empty-text">{{ $t('modelSettings.embedding.empty') }}</p>
        <t-button theme="default" variant="outline" size="small" @click="openAddDialog('embedding')">
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- ReRank 模型 -->
    <div class="model-category-section" data-model-type="rerank">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('modelSettings.rerank.title') }}</h3>
          <p>{{ $t('modelSettings.rerank.desc') }}</p>
        </div>
        <t-button size="small" theme="primary" @click="openAddDialog('rerank')" class="add-model-btn">
          <template #icon>
            <t-icon name="add" class="add-icon" />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
      
      <div v-if="rerankModels.length > 0" class="model-list-container">
        <div v-for="model in rerankModels" :key="model.id" class="model-card" :class="{ 'builtin-model': model.isBuiltin }">
          <div class="model-info">
            <div class="model-name">
              {{ model.name }}
              <t-tag v-if="model.isBuiltin" theme="primary" size="small">{{ $t('modelSettings.builtinTag') }}</t-tag>
            </div>
            <div class="model-meta">
              <span class="source-tag">{{ model.source === 'local' ? 'Ollama' : $t('modelSettings.source.remote') }}</span>
              <!-- <span class="model-id">{{ model.modelName }}</span> -->
            </div>
          </div>
          <div class="model-actions">
            <t-dropdown 
              :options="getModelOptions('rerank', model)" 
              @click="(data: any) => handleMenuAction(data, 'rerank', model)"
              placement="bottom-right"
              attach="body"
            >
              <t-button variant="text" shape="square" size="small" class="more-btn">
                <t-icon name="more" />
              </t-button>
            </t-dropdown>
          </div>
        </div>
      </div>
      <div v-else class="empty-state">
        <p class="empty-text">{{ $t('modelSettings.rerank.empty') }}</p>
        <t-button theme="default" variant="outline" size="small" @click="openAddDialog('rerank')">
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- VLLM 视觉模型 -->
    <div class="model-category-section" data-model-type="vllm">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('modelSettings.vllm.title') }}</h3>
          <p>{{ $t('modelSettings.vllm.desc') }}</p>
        </div>
        <t-button size="small" theme="primary" @click="openAddDialog('vllm')" class="add-model-btn">
          <template #icon>
            <t-icon name="add" class="add-icon" />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
      
      <div v-if="vllmModels.length > 0" class="model-list-container">
        <div v-for="model in vllmModels" :key="model.id" class="model-card" :class="{ 'builtin-model': model.isBuiltin }">
          <div class="model-info">
            <div class="model-name">
              {{ model.name }}
              <t-tag v-if="model.isBuiltin" theme="primary" size="small">{{ $t('modelSettings.builtinTag') }}</t-tag>
            </div>
            <div class="model-meta">
              <span class="source-tag">{{ model.source === 'local' ? 'Ollama' : $t('modelSettings.source.openaiCompatible') }}</span>
              <!-- <span class="model-id">{{ model.modelName }}</span> -->
            </div>
          </div>
          <div class="model-actions">
            <t-dropdown 
              :options="getModelOptions('vllm', model)" 
              @click="(data: any) => handleMenuAction(data, 'vllm', model)"
              placement="bottom-right"
              attach="body"
            >
              <t-button variant="text" shape="square" size="small" class="more-btn">
                <t-icon name="more" />
              </t-button>
            </t-dropdown>
          </div>
        </div>
      </div>
      <div v-else class="empty-state">
        <p class="empty-text">{{ $t('modelSettings.vllm.empty') }}</p>
        <t-button theme="default" variant="outline" size="small" @click="openAddDialog('vllm')">
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- STT 语音模型 -->
    <div class="model-category-section" data-model-type="asr">
      <div class="category-header">
        <div class="header-info">
          <h3>{{ $t('modelSettings.asr.title') }}</h3>
          <p>{{ $t('modelSettings.asr.desc') }}</p>
        </div>
        <t-button size="small" theme="primary" @click="openAddDialog('asr')" class="add-model-btn">
          <template #icon>
            <t-icon name="add" class="add-icon" />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>

      <div v-if="asrModels.length > 0" class="model-list-container">
        <div v-for="model in asrModels" :key="model.id" class="model-card" :class="{ 'builtin-model': model.isBuiltin }">
          <div class="model-info">
            <div class="model-name">
              {{ model.name }}
              <t-tag v-if="model.isBuiltin" theme="primary" size="small">{{ $t('modelSettings.builtinTag') }}</t-tag>
            </div>
            <div class="model-meta">
              <span class="source-tag">{{ model.source === 'local' ? 'Ollama' : $t('modelSettings.source.openaiCompatible') }}</span>
            </div>
          </div>
          <div class="model-actions">
            <t-dropdown
              :options="getModelOptions('asr', model)"
              @click="(data: any) => handleMenuAction(data, 'asr', model)"
              placement="bottom-right"
              attach="body"
            >
              <t-button variant="text" shape="square" size="small" class="more-btn">
                <t-icon name="more" />
              </t-button>
            </t-dropdown>
          </div>
        </div>
      </div>
      <div v-else class="empty-state">
        <p class="empty-text">{{ $t('modelSettings.asr.empty') }}</p>
        <t-button theme="default" variant="outline" size="small" @click="openAddDialog('asr')">
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- 模型编辑器弹窗 -->
    <ModelEditorDialog
      v-model:visible="showDialog"
      :model-type="currentModelType"
      :model-data="editingModel"
      @confirm="handleModelSave"
    />

  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import ModelEditorDialog from '@/components/ModelEditorDialog.vue'
import { listModels, createModel, updateModel as updateModelAPI, deleteModel as deleteModelAPI, type ModelConfig } from '@/api/model'

const { t } = useI18n()

const showDialog = ref(false)
const currentModelType = ref<'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'>('chat')
const editingModel = ref<any>(null)
const loading = ref(true)

// 模型列表数据
const allModels = ref<ModelConfig[]>([])

// 根据类型过滤模型
const chatModels = computed(() => 
  allModels.value
    .filter(m => m.type === 'KnowledgeQA')
    .map(convertToLegacyFormat)
)

const embeddingModels = computed(() => 
  allModels.value
    .filter(m => m.type === 'Embedding')
    .map(convertToLegacyFormat)
)

const rerankModels = computed(() => 
  allModels.value
    .filter(m => m.type === 'Rerank')
    .map(convertToLegacyFormat)
)

const vllmModels = computed(() =>
  allModels.value
    .filter(m => m.type === 'VLLM')
    .map(convertToLegacyFormat)
)

const asrModels = computed(() =>
  allModels.value
    .filter(m => m.type === 'ASR')
    .map(convertToLegacyFormat)
)

// 将后端模型格式转换为旧的前端格式
function convertToLegacyFormat(model: ModelConfig) {
  return {
    id: model.id!,
    name: model.name,
    source: model.source,
    modelName: model.name,  // 显示名称作为模型名
    baseUrl: model.parameters.base_url || '',
    apiKey: model.parameters.api_key || '',
    provider: model.parameters.provider || '', // 添加 provider 字段
    dimension: model.parameters.embedding_parameters?.dimension,
    isBuiltin: model.is_builtin || false,
    supportsVision: model.parameters.supports_vision || false
  }
}

// 加载模型列表
const loadModels = async () => {
  loading.value = true
  try {
    // 直接获取所有模型，不分类型
    const models = await listModels()
    allModels.value = models
  } catch (error: any) {
    console.error('加载模型列表失败:', error)
    MessagePlugin.error(error.message)
  } finally {
    loading.value = false
  }
}

// 打开添加对话框
const openAddDialog = (type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr') => {
  currentModelType.value = type
  editingModel.value = null
  showDialog.value = true
}

// 编辑模型
const editModel = (type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr', model: any) => {
  // 内置模型不能编辑
  if (model.isBuiltin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotEdit'))
    return
  }
  currentModelType.value = type
  editingModel.value = { ...model }
  showDialog.value = true
}

// 保存模型
const handleModelSave = async (modelData: any) => {
  try {
    // 字段校验
    if (!modelData.modelName || !modelData.modelName.trim()) {
      MessagePlugin.warning(t('modelSettings.toasts.nameRequired'))
      return
    }
    
    if (modelData.modelName.trim().length > 100) {
      MessagePlugin.warning(t('modelSettings.toasts.nameTooLong'))
      return
    }
    
    // Remote 类型必须填写 baseUrl
    if (modelData.source === 'remote') {
      if (!modelData.baseUrl || !modelData.baseUrl.trim()) {
        MessagePlugin.warning(t('modelSettings.toasts.baseUrlRequired'))
        return
      }
      
      // 校验 Base URL 格式
      try {
        new URL(modelData.baseUrl.trim())
      } catch {
        MessagePlugin.warning(t('modelSettings.toasts.baseUrlInvalid'))
        return
      }
    }
    
    // Embedding 模型必须填写维度
    if (currentModelType.value === 'embedding') {
      if (!modelData.dimension || modelData.dimension < 128 || modelData.dimension > 4096) {
        MessagePlugin.warning(t('modelSettings.toasts.dimensionInvalid'))
        return
      }
    }
    
    // 将前端格式转换为后端格式
    const apiModelData: ModelConfig = {
      name: modelData.modelName.trim(), // 使用 modelName 作为 name，并去除首尾空格
      type: getModelType(currentModelType.value),
      source: modelData.source,
      description: '',
      parameters: {
        base_url: modelData.baseUrl?.trim() || '',
        api_key: modelData.apiKey?.trim() || '',
        provider: modelData.provider || '', // 添加 provider 字段
        ...(currentModelType.value === 'embedding' && modelData.dimension ? {
          embedding_parameters: {
            dimension: modelData.dimension,
            truncate_prompt_tokens: 0
          }
        } : {}),
        ...(currentModelType.value === 'vllm' ? {
          supports_vision: true
        } : currentModelType.value === 'chat' ? {
          supports_vision: modelData.supportsVision ?? false
        } : {})
      }
    }

    if (editingModel.value && editingModel.value.id) {
      // 更新现有模型
      await updateModelAPI(editingModel.value.id, apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.updated'))
    } else {
      // 添加新模型
      await createModel(apiModelData)
      MessagePlugin.success(t('modelSettings.toasts.added'))
    }
    
    // 重新加载模型列表
    await loadModels()
  } catch (error: any) {
    console.error('保存模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.saveFailed'))
  }
}

// 删除模型
const deleteModel = async (type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr', modelId: string) => {
  // 检查是否是内置模型
  const model = allModels.value.find(m => m.id === modelId)
  if (model?.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotDelete'))
    return
  }
  
  try {
    await deleteModelAPI(modelId)
    MessagePlugin.success(t('modelSettings.toasts.deleted'))
    // 重新加载模型列表
    await loadModels()
  } catch (error: any) {
    console.error('删除模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.deleteFailed'))
  }
}

// 获取模型操作菜单选项
const getModelOptions = (type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr', model: any) => {
  const options: any[] = []
  
  // 内置模型不能编辑和删除
  if (model.isBuiltin) {
    return options
  }
  
  // 编辑选项
  options.push({
    content: t('common.edit'),
    value: `edit-${type}-${model.id}`
  })

  // 删除选项
  options.push({
    content: t('common.delete'),
    value: `delete-${type}-${model.id}`,
    theme: 'error'
  })
  
  return options
}

// 处理菜单操作
const handleMenuAction = (data: { value: string }, type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr', model: any) => {
  const value = data.value
  
  if (value.indexOf('edit-') === 0) {
    editModel(type, model)
  } else if (value.indexOf('delete-') === 0) {
    // 使用确认对话框进行确认
    if (confirm(t('modelSettings.confirmDelete'))) {
      deleteModel(type, model.id)
    }
  }
}

// 获取后端模型类型
function getModelType(type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr'): 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM' | 'ASR' {
  const typeMap = {
    chat: 'KnowledgeQA' as const,
    embedding: 'Embedding' as const,
    rerank: 'Rerank' as const,
    vllm: 'VLLM' as const,
    asr: 'ASR' as const
  }
  return typeMap[type]
}

// 组件挂载时加载模型列表
onMounted(() => {
  loadModels()
})
</script>

<style lang="less" scoped>
.model-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0 0 20px 0;
    line-height: 1.5;
  }
}

.model-category-section {
  margin-bottom: 32px;
  padding-bottom: 32px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    margin-bottom: 0;
    padding-bottom: 0;
    border-bottom: none;
  }
}

.category-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 16px;

  .header-info {
    flex: 1;

    h3 {
      font-size: 15px;
      font-weight: 500;
      color: var(--td-text-color-primary);
      margin: 0 0 4px 0;
    }

    p {
      font-size: 13px;
      color: var(--td-text-color-secondary);
      margin: 0;
      line-height: 1.5;
    }
  }
}

// 添加模型按钮样式优化
:deep(.add-model-btn) {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 500;
  height: 32px;
  padding: 0 16px;
  font-size: 14px;
  flex-shrink: 0;

  .add-icon {
    font-size: 14px;
    width: 14px;
    height: 14px;
  }
}

.model-list-container {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.model-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  transition: all 0.15s ease;
  position: relative;
  overflow: visible;

  &:hover {
    border-color: var(--td-brand-color);
    background: var(--td-bg-color-container);
    box-shadow: 0 1px 4px rgba(7, 192, 95, 0.08);
  }

  // 内置模型样式
  &.builtin-model {
    background: var(--td-bg-color-secondarycontainer);
    border-color: var(--td-component-border);

    &:hover {
      border-color: var(--td-component-stroke);
      background: var(--td-bg-color-secondarycontainer);
      box-shadow: none;
    }

    .model-info {
      .model-name {
        color: var(--td-text-color-secondary);
      }

      .model-meta {
        .source-tag {
          background: var(--td-bg-color-secondarycontainer);
          color: var(--td-text-color-placeholder);
        }
      }
    }
  }
}

.model-info {
  flex: 1;
  min-width: 0;

  .model-name {
    font-size: 14px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    margin-bottom: 6px;
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .model-meta {
    display: flex;
    align-items: center;
    gap: 12px;
    font-size: 12px;
    color: var(--td-text-color-secondary);

    .source-tag {
      padding: 2px 8px;
      background: var(--td-component-stroke);
      border-radius: 3px;
      font-size: 11px;
      font-weight: 500;
    }

    .model-id {
      font-family: monospace;
      color: var(--td-text-color-secondary);
    }

    .dimension {
      color: var(--td-text-color-placeholder);
    }
  }
}

.model-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
  opacity: 0;
  transition: opacity 0.15s ease;
  position: relative;
  z-index: 1001;

  .more-btn {
    color: var(--td-text-color-placeholder);
    padding: 4px;

    &:hover {
      background: var(--td-bg-color-secondarycontainer);
      color: var(--td-text-color-primary);
    }
  }
}

.model-card:hover .model-actions {
  opacity: 1;
}

.empty-state {
  padding: 48px 0;
  text-align: center;

  .empty-text {
    font-size: 13px;
    color: var(--td-text-color-placeholder);
    margin: 0 0 16px 0;
  }
}

.builtin-models-info {
  margin-top: 16px;

  .info-box {
    background: var(--td-success-color-light);
    border: 1px solid var(--td-success-color-focus);
    border-left: 3px solid var(--td-brand-color);
    border-radius: 6px;
    padding: 16px;
  }

  .info-header {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;

    .info-icon {
      font-size: 16px;
      color: var(--td-brand-color);
      flex-shrink: 0;
    }

    .info-title {
      font-size: 14px;
      font-weight: 500;
      color: var(--td-brand-color-active);
    }
  }

  .info-content {
    font-size: 13px;
    line-height: 1.6;
    color: var(--td-success-color);

    p {
      margin: 0 0 6px 0;

      &:last-child {
        margin-bottom: 0;
      }

      &.doc-link {
        margin-top: 10px;
        display: flex;
        align-items: center;
        gap: 6px;

        .link-icon {
          font-size: 13px;
          color: var(--td-brand-color);
          flex-shrink: 0;
        }

        a {
          color: var(--td-brand-color);
          text-decoration: none;
          font-weight: 500;
          transition: color 0.15s;

          &:hover {
            color: var(--td-brand-color-active);
            text-decoration: underline;
          }
        }
      }
    }
  }
}

// TDesign 组件样式覆盖
:deep(.t-button) {
  &.add-model-btn {
    border-radius: 6px;
    font-weight: 500;
    transition: all 0.15s ease;

    &:hover {
      background: var(--td-brand-color);
      border-color: var(--td-brand-color);
    }

    &:active {
      background: var(--td-brand-color-active);
      border-color: var(--td-brand-color-active);
    }
  }

  &.t-size-s {
    height: 32px;
    padding: 0 12px;
    font-size: 13px;
    border-radius: 6px;

    &.t-button--variant-outline {
      color: var(--td-text-color-secondary);
      border-color: var(--td-component-stroke);

      &:hover {
        color: var(--td-brand-color);
        border-color: var(--td-brand-color);
        background: rgba(7, 192, 95, 0.04);
      }
    }
  }
}

// Tag 样式优化
:deep(.t-tag) {
  border-radius: 3px;
  padding: 2px 8px;
  font-size: 11px;
  font-weight: 500;
  border: none;

  &.t-tag--theme-primary {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }

  &.t-tag--theme-success {
    background: var(--td-success-color-light);
    color: var(--td-brand-color-active);
  }

  &.t-size-s {
    height: 20px;
    line-height: 16px;
  }
}

// Dropdown 菜单样式已统一至 @/assets/dropdown-menu.less
</style>
