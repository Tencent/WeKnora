<template>
  <div class="collection-config">
    <div class="setting-line">
      <div><strong>用户信息采集</strong><p>先从对话自动提取；关闭集中采集时，每次最多自然补问一个相关选填项。</p></div>
      <t-switch v-model="config.collection_enabled" :disabled="readOnly" />
    </div>
    <template v-if="config.collection_enabled">
      <label class="field-label">采集目标（用于信息识别）</label>
      <t-textarea v-model="config.collection_goal" :disabled="readOnly" :maxlength="500" autosize
        placeholder="例如：了解员工被辞退的事实，以便判断劳动争议处理方向" />
      <div class="options-grid">
        <label><span>从自然语言中自动提取</span><t-switch v-model="config.collection_extract_from_messages" :disabled="readOnly" /></label>
        <label><span>首次集中采集选填项</span><t-switch v-model="config.collection_collect_optional_during_intake" :disabled="readOnly" /></label>
        <label><span>提取置信度</span><t-input-number v-model="config.collection_extraction_threshold" :min="0.5" :max="1" :step="0.05" :disabled="readOnly" /></label>
      </div>
      <div class="fields-header">
        <div><strong>需要采集的字段</strong><span>{{ fields.length }}/100</span></div>
        <t-button size="small" variant="outline" :disabled="readOnly || fields.length >= 100" @click="addField">
          <template #icon><t-icon name="add" /></template>添加字段
        </t-button>
      </div>
      <div v-if="!fields.length" class="empty">尚未配置字段</div>
      <article v-for="(field, index) in fields" :key="field.key + index" class="field-item">
        <header>
          <span class="order">{{ index + 1 }}</span><strong>{{ field.label || '未命名字段' }}</strong>
          <div class="field-actions">
            <t-tooltip content="上移"><t-button shape="square" variant="text" :disabled="readOnly || index === 0" @click="move(index, -1)"><t-icon name="chevron-up" /></t-button></t-tooltip>
            <t-tooltip content="下移"><t-button shape="square" variant="text" :disabled="readOnly || index === fields.length - 1" @click="move(index, 1)"><t-icon name="chevron-down" /></t-button></t-tooltip>
            <t-tooltip content="复制"><t-button shape="square" variant="text" :disabled="readOnly || fields.length >= 100" @click="copyField(index)"><t-icon name="file-copy" /></t-button></t-tooltip>
            <t-tooltip content="删除（历史数据保留为已停用字段）"><t-button shape="square" variant="text" theme="danger" :disabled="readOnly" @click="removeField(index)"><t-icon name="delete" /></t-button></t-tooltip>
          </div>
        </header>
        <div class="field-grid">
          <label><span>名称</span><t-input v-model="field.label" :disabled="readOnly" placeholder="例如：辞退方式" /></label>
          <label><span>字段标识</span><t-input v-model="field.key" :disabled="readOnly" placeholder="dismissal_method" /></label>
          <label><span>类型</span><t-select v-model="field.type" :options="fieldTypes" :disabled="readOnly || isPublished(field)" @change="onTypeChange(field)" /></label>
          <label class="switches"><t-checkbox v-model="field.required" :disabled="readOnly">必须回答</t-checkbox><t-checkbox v-model="field.enabled" :disabled="readOnly">启用</t-checkbox></label>
        </div>
        <label class="field-label">字段说明（用于信息识别）</label>
        <t-input v-model="field.description" :disabled="readOnly" placeholder="帮助提取模型识别该字段，不会作为问题展示给用户" />
        <div v-if="isChoice(field)" class="option-list">
          <label class="field-label">选项</label>
          <div v-for="(option, optionIndex) in field.options" :key="optionIndex" class="option-row">
            <t-input v-model="option.id" :disabled="readOnly" placeholder="选项标识" />
            <t-input v-model="option.label" :disabled="readOnly" placeholder="显示文字" />
            <t-button shape="square" variant="text" :disabled="readOnly" @click="field.options?.splice(optionIndex, 1)"><t-icon name="close" /></t-button>
          </div>
          <t-button size="small" variant="text" :disabled="readOnly || (field.options?.length ?? 0) >= 50" @click="addOption(field)"><template #icon><t-icon name="add" /></template>添加选项</t-button>
        </div>
        <div v-if="field.type === 'short_text' || field.type === 'long_text'" class="rule-row">
          <label><span>最小长度</span><t-input-number v-model="field.validation!.min_length" :min="0" :disabled="readOnly" /></label>
          <label><span>最大长度</span><t-input-number v-model="field.validation!.max_length" :min="0" :disabled="readOnly" /></label>
        </div>
        <div v-if="field.type === 'number'" class="rule-row">
          <label><span>最小值</span><t-input-number v-model="field.validation!.min_number" :disabled="readOnly" /></label>
          <label><span>最大值</span><t-input-number v-model="field.validation!.max_number" :disabled="readOnly" /></label>
        </div>
        <div v-if="field.type === 'date'" class="rule-row">
          <label><span>最早日期</span><t-date-picker v-model="field.validation!.min_date" :disabled="readOnly" /></label>
          <label><span>最晚日期</span><t-date-picker v-model="field.validation!.max_date" :disabled="readOnly" /></label>
        </div>
        <div class="condition-row">
          <label><span>显示条件</span><t-select :value="field.visible_when?.field || ''" :disabled="readOnly || index === 0" :options="conditionFields(index)" clearable placeholder="始终显示" @change="setConditionField(field, $event as string)" /></label>
          <label v-if="field.visible_when"><span>条件</span><t-select v-model="field.visible_when.operator" :disabled="readOnly" :options="conditionOperators" /></label>
          <label v-if="field.visible_when && needsConditionValue(field)"><span>值</span><t-input v-model="field.visible_when.value" :disabled="readOnly" /></label>
        </div>
      </article>
      <section v-if="previewField" class="question-preview">
        <header><span>提问预览</span><small>剩余 {{ fields.filter((field) => field.enabled).length }} 个问题</small></header>
        <strong>{{ previewField.label || '未命名问题' }}</strong>
        <p v-if="previewField.description">{{ previewField.description }}</p>
        <div v-if="isChoice(previewField)" class="preview-options">
          <span v-for="option in previewField.options" :key="option.id">{{ option.label || '未命名选项' }}</span>
        </div>
        <div v-else class="preview-input">{{ previewPlaceholder }}</div>
      </section>
      <t-alert v-if="errors.length" theme="error" :message="errors.join('；')" />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import type { AgentCollectionField, CustomAgentConfig } from '@/api/agent'
import {
  createCollectionField,
  ensureCollectionDefaults,
  nextCollectionFieldKey,
  normalizeCollectionFields,
  validateCollectionConfig,
} from '../agentCollectionConfig'

const props = defineProps<{ modelValue: CustomAgentConfig; readOnly?: boolean }>()
const emit = defineEmits<{ (e: 'update:modelValue', value: CustomAgentConfig): void; (e: 'validity', valid: boolean): void }>()
const publishedFields = new Map(
  (props.modelValue.collection_schema_version ?? 0) > 0
    ? (props.modelValue.collection_fields ?? []).map((field) => [field.key, field.type])
    : [],
)
const config = computed(() => ensureCollectionDefaults(props.modelValue))
const fields = computed(() => config.value.collection_fields ?? [])
const errors = computed(() => validateCollectionConfig(config.value))
const previewField = computed(() => fields.value.find((field) => field.enabled))
const previewPlaceholder = computed(() => ({ short_text: '短文本回答', long_text: '详细回答', number: '输入数字', date: '选择日期' } as Record<string, string>)[previewField.value?.type ?? ''] ?? '')
const fieldTypes = [
  { value: 'single_choice', label: '单选' }, { value: 'multiple_choice', label: '多选' },
  { value: 'short_text', label: '短文本' }, { value: 'long_text', label: '长文本' },
  { value: 'number', label: '数字' }, { value: 'date', label: '日期' },
]
const conditionOperators = [
  { value: 'equals', label: '等于' }, { value: 'not_equals', label: '不等于' },
  { value: 'contains', label: '包含' }, { value: 'not_empty', label: '不为空' },
  { value: 'empty', label: '为空' },
]

watch(errors, (value) => emit('validity', value.length === 0), { immediate: true })
watch(config, (value) => emit('update:modelValue', value), { deep: true })

function addField() {
  fields.value.push(createCollectionField(fields.value, fields.value.length))
}
function removeField(index: number) { fields.value.splice(index, 1); normalizeCollectionFields(fields.value) }
function move(index: number, offset: number) {
  const [field] = fields.value.splice(index, 1)
  fields.value.splice(index + offset, 0, field)
  normalizeCollectionFields(fields.value)
}
function copyField(index: number) {
  const clone = structuredClone(fields.value[index])
  clone.key = nextCollectionFieldKey(fields.value)
  clone.label = `${clone.label} 副本`
  fields.value.splice(index + 1, 0, clone)
  normalizeCollectionFields(fields.value)
}
function isChoice(field: AgentCollectionField) { return field.type === 'single_choice' || field.type === 'multiple_choice' }
function isPublished(field: AgentCollectionField) { return publishedFields.get(field.key) === field.type }
function onTypeChange(field: AgentCollectionField) {
  if (isChoice(field) && !field.options?.length) field.options = [{ id: 'option_1', label: '' }, { id: 'option_2', label: '' }]
  if (!isChoice(field)) field.options = undefined
}
function addOption(field: AgentCollectionField) { field.options ??= []; field.options.push({ id: `option_${field.options.length + 1}`, label: '' }) }
function conditionFields(index: number) { return fields.value.slice(0, index).filter((field) => field.enabled).map((field) => ({ value: field.key, label: field.label || field.key })) }
function setConditionField(field: AgentCollectionField, key: string) { field.visible_when = key ? { field: key, operator: 'equals', value: '' } : undefined }
function needsConditionValue(field: AgentCollectionField) { return !['empty', 'not_empty'].includes(field.visible_when?.operator ?? '') }
</script>

<style scoped lang="less">
.collection-config { display: grid; gap: 16px; }
.setting-line, .fields-header, article header { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.setting-line p { margin: 4px 0 0; color: var(--td-text-color-secondary); }
.field-label, label { display: grid; gap: 6px; color: var(--td-text-color-secondary); font-size: 13px; }
.options-grid, .field-grid, .condition-row, .rule-row { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.options-grid label { display: flex; align-items: center; justify-content: space-between; padding: 10px 12px; border: 1px solid var(--td-component-stroke); border-radius: 6px; }
.fields-header span { margin-left: 8px; color: var(--td-text-color-placeholder); font-size: 12px; }
.field-item { display: grid; gap: 12px; padding: 14px; border: 1px solid var(--td-component-stroke); border-radius: 6px; }
.field-item header { min-height: 32px; }
.order { width: 24px; height: 24px; display: inline-grid; place-items: center; border-radius: 50%; background: var(--td-bg-color-secondarycontainer); }
.field-item header strong { margin-right: auto; }
.field-actions, .option-row { display: flex; align-items: center; gap: 4px; }
.switches { display: flex; align-items: end; gap: 16px; padding-bottom: 6px; }
.option-list { display: grid; gap: 8px; }
.option-row .t-input:first-child { max-width: 180px; }
.empty { padding: 28px; text-align: center; color: var(--td-text-color-placeholder); border: 1px dashed var(--td-component-stroke); border-radius: 6px; }
.question-preview { display: grid; gap: 10px; padding: 14px 0; border-block: 1px solid var(--td-component-stroke); }
.question-preview header { display: flex; justify-content: space-between; color: var(--td-text-color-secondary); }
.question-preview p { margin: 0; color: var(--td-text-color-secondary); }
.preview-options { display: grid; gap: 6px; }
.preview-options span, .preview-input { padding: 9px 10px; border: 1px solid var(--td-component-stroke); border-radius: 4px; color: var(--td-text-color-secondary); }
@media (max-width: 760px) { .options-grid, .field-grid, .condition-row, .rule-row { grid-template-columns: 1fr; } }
</style>
