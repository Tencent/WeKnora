<template>
  <div class="prompt-template-selector" :class="{ 'position-corner': position === 'corner' }">
    <div class="template-btn-group">
      <!-- 恢复默认按钮 -->
      <t-button
        variant="text"
        size="small"
        class="template-default-btn"
        :loading="resettingDefault"
        @click="handleResetToDefault"
      >
        <t-icon name="rollback" />
        <span>{{ $t('promptTemplate.resetDefault') }}</span>
      </t-button>
      <!-- 选择模板按钮 -->
      <t-popup
        trigger="click"
        placement="top-right"
        :visible="popupVisible"
        @visible-change="handleVisibleChange"
      >
        <template #content>
          <div class="template-popup">
            <div class="template-header">
              <span class="template-title">{{ $t('promptTemplate.selectTemplate') }}</span>
            </div>
            <div v-if="loading" class="template-loading">
              <t-loading size="small" />
            </div>
            <div v-else-if="templates.length === 0" class="template-empty">
              {{ $t('promptTemplate.noTemplates') }}
            </div>
            <div v-else class="template-list">
              <div
                v-for="template in templates"
                :key="template.id"
                class="template-item"
                @click="selectTemplate(template)"
              >
                <div class="template-item-header">
                  <span class="template-name">{{ template.name }}</span>
                  <span v-if="template.default" class="template-tag default-tag">
                    {{ $t('promptTemplate.default') }}
                  </span>
                  <span v-if="template.has_knowledge_base" class="template-tag kb-tag">
                    <t-icon name="folder" size="12px" />
                    {{ $t('promptTemplate.withKnowledgeBase') }}
                  </span>
                  <span v-if="template.has_web_search" class="template-tag web-tag">
                    <t-icon name="internet" size="12px" />
                    {{ $t('promptTemplate.withWebSearch') }}
                  </span>
                </div>
                <p class="template-desc">{{ template.description }}</p>
              </div>
            </div>
          </div>
        </template>
        <t-button
          variant="outline"
          size="small"
          class="template-trigger-btn"
          :loading="loading"
        >
          <t-icon name="view-module" />
          <span>{{ $t('promptTemplate.useTemplate') }}</span>
        </t-button>
      </t-popup>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { getPromptTemplates, type PromptTemplate, type PromptTemplatesConfig } from '@/api/system';

const { t } = useI18n();

const props = defineProps<{
  type: 'systemPrompt' | 'contextTemplate' | 'rewrite' | 'fallback' | 'agentSystemPrompt';
  hasKnowledgeBase?: boolean;
  position?: 'inline' | 'corner';  // inline: 行内显示, corner: 输入框右下角
  /** 用于 fallback 场景：区分固定回复和模型 prompt */
  fallbackMode?: 'fixed' | 'model';
}>();

const emit = defineEmits<{
  (e: 'select', template: PromptTemplate): void;
  (e: 'reset-default', template: PromptTemplate): void;
}>();

const popupVisible = ref(false);
const loading = ref(false);
const resettingDefault = ref(false);
const templatesConfig = ref<PromptTemplatesConfig | null>(null);

const handleVisibleChange = async (visible: boolean) => {
  popupVisible.value = visible;
  // 首次打开时加载模板
  if (visible && !templatesConfig.value) {
    await loadTemplates();
  }
};

const loadTemplates = async () => {
  if (loading.value) return;
  loading.value = true;
  try {
    const response = await getPromptTemplates();
    templatesConfig.value = response.data;
  } catch (error) {
    console.error('Failed to load prompt templates:', error);
  } finally {
    loading.value = false;
  }
};

// 根据类型获取对应的模板列表
const templates = computed<PromptTemplate[]>(() => {
  if (!templatesConfig.value) return [];
  
  let list: PromptTemplate[] = [];
  switch (props.type) {
    case 'systemPrompt':
      list = templatesConfig.value.system_prompt || [];
      break;
    case 'contextTemplate':
      list = templatesConfig.value.context_template || [];
      break;
    case 'rewrite':
      list = templatesConfig.value.rewrite || [];
      break;
    case 'fallback':
      list = templatesConfig.value.fallback || [];
      // Filter by fallbackMode: "model" mode shows only mode:"model" templates, otherwise shows non-model templates
      if (props.fallbackMode === 'model') {
        list = list.filter(t => t.mode === 'model');
      } else if (props.fallbackMode === 'fixed') {
        list = list.filter(t => !t.mode || t.mode !== 'model');
      }
      break;
    case 'agentSystemPrompt':
      list = templatesConfig.value.agent_system_prompt || [];
      break;
    default:
      list = [];
  }
  return list;
});

const selectTemplate = (template: PromptTemplate) => {
  emit('select', template);
  popupVisible.value = false;
};

// Find the default template (marked with default: true, or the first one)
const findDefaultTemplate = (list: PromptTemplate[]): PromptTemplate | null => {
  if (!list || list.length === 0) return null;
  const defaultItem = list.find(t => t.default);
  return defaultItem || list[0];
};

// Reset to default template content
const handleResetToDefault = async () => {
  if (!templatesConfig.value) {
    resettingDefault.value = true;
    try {
      const response = await getPromptTemplates();
      templatesConfig.value = response.data;
    } catch (error) {
      console.error('Failed to load prompt templates:', error);
      resettingDefault.value = false;
      return;
    }
    resettingDefault.value = false;
  }

  const templateList = templates.value;
  const defaultTpl = findDefaultTemplate(templateList);
  if (defaultTpl) {
    emit('reset-default', defaultTpl);
  }
};

// 预加载模板（可选）
onMounted(() => {
  // 可以在这里预加载，也可以等用户点击时再加载
  // loadTemplates();
});
</script>

<style scoped lang="less">
.prompt-template-selector {
  display: inline-flex;
  
  &.position-corner {
    position: absolute;
    right: 8px;
    bottom: 8px;
    z-index: 10;
  }
}

.template-btn-group {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.template-default-btn {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  height: 26px;
  padding: 0 6px;

  &:hover {
    color: var(--td-brand-color);
  }
  
  :deep(.t-button__text) {
    display: inline-flex;
    align-items: center;
    gap: 3px;
  }
  
  :deep(.t-icon) {
    font-size: 14px;
    vertical-align: middle;
    line-height: 1;
  }
}

.template-trigger-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: var(--td-text-color-secondary);
  border-color: var(--td-component-stroke);
  font-size: 12px;
  height: 26px;
  padding: 0 8px;
  background: var(--td-bg-color-container);

  &:hover {
    color: var(--td-brand-color);
    border-color: var(--td-brand-color);
    background: var(--td-bg-color-secondarycontainer);
  }
  
  :deep(.t-button__text) {
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  
  :deep(.t-icon) {
    vertical-align: middle;
    line-height: 1;
  }
}

.template-popup {
  width: 420px;
  max-height: 400px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.template-header {
  padding: 12px 16px;
  border-bottom: 1px solid var(--td-component-stroke);
  flex-shrink: 0;
}

.template-title {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.template-loading,
.template-empty {
  padding: 40px 16px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
}

.template-list {
  overflow-y: auto;
  padding: 8px;
  flex: 1;
}

.template-item {
  padding: 12px;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.2s ease;
  margin-bottom: 4px;
  
  &:last-child {
    margin-bottom: 0;
  }
  
  &:hover {
    background: var(--td-bg-color-secondarycontainer);
  }
}

.template-item-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  flex-wrap: wrap;
}

.template-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.template-tag {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 11px;
  
  &.kb-tag {
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
  }
  
  &.web-tag {
    background: var(--td-success-color-light);
    color: var(--td-brand-color);
  }

  &.default-tag {
    background: var(--td-warning-color-light);
    color: var(--td-warning-color);
    font-weight: 500;
  }
}

.template-desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin: 0;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
</style>
