<template>
  <div class="model-settings">
    <div class="section-header">
      <h2>{{ $t('modelSettings.title') }}</h2>
      <p class="section-description">{{ $t('modelSettings.description') }}</p>

      <div class="builtin-models-hint" role="note">
        <p class="builtin-hint-label">{{ $t('modelSettings.builtinModels.title') }}</p>
        <p class="builtin-hint-text">{{ $t('modelSettings.builtinModels.description') }}</p>
        <a
          class="builtin-hint-link"
          href="https://github.com/Tencent/WeKnora/blob/main/docs/BUILTIN_MODELS.md"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ $t('modelSettings.builtinModels.viewGuide') }}
        </a>
      </div>
    </div>

    <!-- 对话模型 -->
    <div class="settings-group model-type-group" data-model-type="chat">
      <div class="section-subheader">
        <div class="subheader-text">
          <h3>{{ $t('modelSettings.chat.title') }}</h3>
          <p class="section-desc">{{ $t('modelSettings.chat.desc') }}</p>
        </div>
        <t-button theme="primary" size="small" @click="openAddDialog('chat')">
          <template #icon>
            <add-icon />
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
      <div v-else class="empty-models">
        <p>{{ $t('modelSettings.chat.empty') }}</p>
        <t-button theme="primary" size="small" @click="openAddDialog('chat')">
          <template #icon>
            <add-icon />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- Embedding 模型 -->
    <div class="settings-group model-type-group" data-model-type="embedding">
      <div class="section-subheader">
        <div class="subheader-text">
          <h3>{{ $t('modelSettings.embedding.title') }}</h3>
          <p class="section-desc">{{ $t('modelSettings.embedding.desc') }}</p>
        </div>
        <t-button theme="primary" size="small" @click="openAddDialog('embedding')">
          <template #icon>
            <add-icon />
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
      <div v-else class="empty-models">
        <p>{{ $t('modelSettings.embedding.empty') }}</p>
        <t-button theme="primary" size="small" @click="openAddDialog('embedding')">
          <template #icon>
            <add-icon />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- ReRank 模型 -->
    <div class="settings-group model-type-group" data-model-type="rerank">
      <div class="section-subheader">
        <div class="subheader-text">
          <h3>{{ $t('modelSettings.rerank.title') }}</h3>
          <p class="section-desc">{{ $t('modelSettings.rerank.desc') }}</p>
        </div>
        <t-button theme="primary" size="small" @click="openAddDialog('rerank')">
          <template #icon>
            <add-icon />
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
      <div v-else class="empty-models">
        <p>{{ $t('modelSettings.rerank.empty') }}</p>
        <t-button theme="primary" size="small" @click="openAddDialog('rerank')">
          <template #icon>
            <add-icon />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- VLLM 视觉模型 -->
    <div class="settings-group model-type-group" data-model-type="vllm">
      <div class="section-subheader">
        <div class="subheader-text">
          <h3>{{ $t('modelSettings.vllm.title') }}</h3>
          <p class="section-desc">{{ $t('modelSettings.vllm.desc') }}</p>
        </div>
        <t-button theme="primary" size="small" @click="openAddDialog('vllm')">
          <template #icon>
            <add-icon />
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
      <div v-else class="empty-models">
        <p>{{ $t('modelSettings.vllm.empty') }}</p>
        <t-button theme="primary" size="small" @click="openAddDialog('vllm')">
          <template #icon>
            <add-icon />
          </template>
          {{ $t('modelSettings.actions.addModel') }}
        </t-button>
      </div>
    </div>

    <!-- STT 语音模型 -->
    <div class="settings-group model-type-group" data-model-type="asr">
      <div class="section-subheader">
        <div class="subheader-text">
          <h3>{{ $t('modelSettings.asr.title') }}</h3>
          <p class="section-desc">{{ $t('modelSettings.asr.desc') }}</p>
        </div>
        <t-button theme="primary" size="small" @click="openAddDialog('asr')">
          <template #icon>
            <add-icon />
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
      <div v-else class="empty-models">
        <p>{{ $t('modelSettings.asr.empty') }}</p>
        <t-button theme="primary" size="small" @click="openAddDialog('asr')">
          <template #icon>
            <add-icon />
          </template>
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
import { AddIcon } from 'tdesign-icons-vue-next'
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
    supportsVision: model.parameters.supports_vision || false,
    // 将后端 map 形式转换为前端可编辑的数组形式
    customHeaders: model.parameters.custom_headers
      ? Object.entries(model.parameters.custom_headers).map(([key, value]) => ({ key, value: String(value) }))
      : []
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
    
    // 将前端 Key-Value 数组形式的自定义 Header 转换成后端期望的 map
    const customHeadersMap: Record<string, string> = {}
    if (Array.isArray(modelData.customHeaders)) {
      for (const item of modelData.customHeaders) {
        const key = (item?.key ?? '').trim()
        const value = (item?.value ?? '').trim()
        if (key && value) {
          customHeadersMap[key] = value
        }
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
        ...(Object.keys(customHeadersMap).length > 0 ? { custom_headers: customHeadersMap } : {}),
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

  // 复制选项
  options.push({
    content: t('common.copy'),
    value: `copy-${type}-${model.id}`
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
  } else if (value.indexOf('copy-') === 0) {
    copyModel(type, model.id)
  } else if (value.indexOf('delete-') === 0) {
    // 使用确认对话框进行确认
    if (confirm(t('modelSettings.confirmDelete'))) {
      deleteModel(type, model.id)
    }
  }
}

// 生成不重复的复制名称：原名 + 复制后缀（若已存在则追加序号）
const generateCopyName = (originalName: string): string => {
  const suffix = t('modelSettings.copySuffix')
  const existingNames = new Set(allModels.value.map(m => m.name))
  let candidate = `${originalName}${suffix}`
  let counter = 2
  while (existingNames.has(candidate)) {
    candidate = `${originalName}${suffix} ${counter}`
    counter += 1
  }
  return candidate
}

// 复制模型
const copyModel = async (_type: 'chat' | 'embedding' | 'rerank' | 'vllm' | 'asr', modelId: string) => {
  const source = allModels.value.find(m => m.id === modelId)
  if (!source) {
    return
  }
  if (source.is_builtin) {
    MessagePlugin.warning(t('modelSettings.toasts.builtinCannotCopy'))
    return
  }

  try {
    const newModel: ModelConfig = {
      name: generateCopyName(source.name),
      type: source.type,
      source: source.source,
      description: source.description || '',
      parameters: JSON.parse(JSON.stringify(source.parameters || {}))
    }

    await createModel(newModel)
    MessagePlugin.success(t('modelSettings.toasts.copied'))
    await loadModels()
  } catch (error: any) {
    console.error('复制模型失败:', error)
    MessagePlugin.error(error.message || t('modelSettings.toasts.copyFailed'))
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
    margin: 0 0 16px 0;
    line-height: 1.5;
  }
}

.builtin-models-hint {
  margin-top: 4px;
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
}

.builtin-hint-label {
  margin: 0 0 4px 0;
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-placeholder);
  letter-spacing: 0.02em;
}

.builtin-hint-text {
  margin: 0 0 6px 0;
  font-size: 13px;
  line-height: 1.55;
  color: var(--td-text-color-secondary);
}

.builtin-hint-link {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  text-decoration: none;

  &:hover {
    color: var(--td-brand-color);
    text-decoration: underline;
  }
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.model-type-group {
  margin-bottom: 32px;
  padding-bottom: 32px;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-of-type {
    margin-bottom: 0;
    padding-bottom: 0;
    border-bottom: none;
  }
}

.section-subheader {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;

  .subheader-text {
    flex: 1;
    min-width: 0;
  }

  h3 {
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 4px 0;
  }

  .section-desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }

  :deep(.t-button) {
    flex-shrink: 0;
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
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  transition: border-color 0.2s ease, background-color 0.2s ease;
  position: relative;
  overflow: visible;

  &:hover {
    border-color: var(--td-brand-color);
  }

  &.builtin-model {
    background: var(--td-bg-color-secondarycontainer);
    border-color: var(--td-component-stroke);

    &:hover {
      border-color: var(--td-brand-color-light);
    }

    .model-info {
      .model-name {
        color: var(--td-text-color-secondary);
      }

      .model-meta {
        .source-tag {
          background: var(--td-bg-color-container);
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

.empty-models {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 32px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;
  font-size: 14px;

  p {
    margin: 0;
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
