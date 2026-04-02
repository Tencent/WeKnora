<template>
  <div class="model-selector">
    <t-select
      :value="selectedModelId"
      @change="handleModelChange"
      :placeholder="placeholderText"
      :disabled="disabled"
      :loading="loading"
      filterable
      style="width: 100%;"
    >
      <!-- 已有的模型选项 -->
      <t-option
        v-for="model in models"
        :key="model.id"
        :value="model.id"
        :label="model.name"
      >
        <div class="model-option">
          <t-icon name="check-circle-filled" class="model-icon" />
          <span class="model-name">{{ model.name }}</span>
          <t-tag v-if="model.is_builtin" size="small" theme="primary">{{ $t('model.builtinTag') }}</t-tag>
          <t-tag v-if="model.is_default" size="small" theme="success">{{ $t('model.defaultTag') }}</t-tag>
        </div>
      </t-option>
      
      <!-- 添加模型选项（在底部） -->
      <t-option
        v-if="!disabled"
        value="__add_model__"
        class="add-model-option"
      >
        <div class="model-option add">
          <t-icon name="add" class="add-icon" />
          <span class="model-name">{{ $t('model.addModelInSettings') }}</span>
        </div>
      </t-option>
    </t-select>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { listModels, type ModelConfig } from '@/api/model'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'

interface Props {
  modelType: 'KnowledgeQA' | 'Embedding' | 'Rerank' | 'VLLM' | 'ASR'
  selectedModelId?: string
  disabled?: boolean
  placeholder?: string
  // 可选：外部传入的所有模型列表，如果提供则不调用API
  allModels?: ModelConfig[]
}

const props = withDefaults(defineProps<Props>(), {
  disabled: false,
  placeholder: ''
})

const emit = defineEmits<{
  'update:selectedModelId': [value: string]
  'add-model': []
}>()

const models = ref<ModelConfig[]>([])
const loading = ref(false)
const { t } = useI18n()

const placeholderText = computed(() => {
  return props.placeholder || t('model.selectModelPlaceholder')
})

// 监听 allModels 变化，自动过滤当前类型的模型
watch(() => props.allModels, (newModels) => {
  if (newModels && Array.isArray(newModels)) {
    models.value = newModels.filter(m => m.type === props.modelType)
  }
}, { immediate: true })

const selectedModel = computed(() => {
  if (!props.selectedModelId) return null
  return models.value.find(m => m.id === props.selectedModelId)
})

// 加载模型列表（仅在未提供 allModels 时调用）
const loadModels = async () => {
  // 如果外部提供了 allModels，则不需要加载
  if (props.allModels) {
    return
  }
  
  loading.value = true
  try {
    const result = await listModels()
    // 前端按类型筛选模型
    if (result && Array.isArray(result)) {
      models.value = result.filter(m => m.type === props.modelType)
    } else {
      models.value = []
    }
  } catch (error) {
    console.error(t('model.loadFailed'), error)
    MessagePlugin.error(t('model.loadFailed'))
    models.value = []
  } finally {
    loading.value = false
  }
}

// 处理模型选择变化
const handleModelChange = (value: string) => {
  // 如果选择的是添加模型选项，触发添加事件而不更新选中值
  if (value === '__add_model__') {
    emit('add-model')
    return
  }
  emit('update:selectedModelId', value)
}

// 暴露刷新方法给父组件
defineExpose({
  refresh: loadModels
})

onMounted(() => {
  // 只有在没有提供 allModels 时才加载
  if (!props.allModels) {
    loadModels()
  }
})
</script>

<style lang="less" scoped>
.model-selector {
  width: 100%;
}

.model-option {
  display: flex;
  align-items: center;
  gap: 8px;
  
  .model-icon {
    font-size: 14px;
    color: var(--td-brand-color);
  }
  
  .add-icon {
    font-size: 14px;
    color: var(--td-brand-color);
  }
  
  .model-name {
    flex: 1;
    font-size: 13px;
  }
  
  &.add {
    .model-name {
      color: var(--td-brand-color);
      font-weight: 500;
    }
  }
}
</style>

