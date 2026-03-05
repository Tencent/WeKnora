<template>
  <div class="prompt-template-selector" :class="{ 'position-corner': position === 'corner' }">
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
            {{ $t('promptTemplate.noTemplates') || '暂无模板' }}
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
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { getPromptTemplates, type PromptTemplate, type PromptTemplatesConfig } from '@/api/system';

const props = defineProps<{
  type: 'systemPrompt' | 'contextTemplate' | 'rewriteSystem' | 'rewriteUser' | 'fallback';
  hasKnowledgeBase?: boolean;
  position?: 'inline' | 'corner';  // inline: 行内显示, corner: 输入框右下角
}>();

const emit = defineEmits<{
  (e: 'select', content: string): void;
}>();

const popupVisible = ref(false);
const loading = ref(false);
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
  
  switch (props.type) {
    case 'systemPrompt':
      return templatesConfig.value.system_prompt || [];
    case 'contextTemplate':
      return templatesConfig.value.context_template || [];
    case 'rewriteSystem':
      return templatesConfig.value.rewrite_system || [];
    case 'rewriteUser':
      return templatesConfig.value.rewrite_user || [];
    case 'fallback':
      return templatesConfig.value.fallback || [];
    default:
      return [];
  }
});

const selectTemplate = (template: PromptTemplate) => {
  emit('select', template.content);
  popupVisible.value = false;
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
