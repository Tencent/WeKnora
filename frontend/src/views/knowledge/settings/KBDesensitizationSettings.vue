<template>
  <div class="kb-desensitization-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.desensitization.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.desensitization.description') }}</p>
      <t-alert
        theme="warning"
        class="notice-alert"
        :title="$t('knowledgeEditor.desensitization.coverageTitle')"
        :message="$t('knowledgeEditor.desensitization.coverageNotice')"
      />
    </div>

    <div class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.desensitization.enableLabel') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.desensitization.enableDescription') }}</p>
        </div>
        <div class="setting-control">
          <t-switch v-model="local.enabled" size="medium" @change="emitChange" />
        </div>
      </div>

      <div v-if="local.enabled" class="subsection">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.desensitization.engineLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.desensitization.engineDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-select v-model="local.engine" style="width: 220px" @change="emitChange">
              <t-option value="builtin" :label="$t('knowledgeEditor.desensitization.engineBuiltin')" />
              <t-option value="presidio" :label="$t('knowledgeEditor.desensitization.enginePresidio')" />
            </t-select>
          </div>
        </div>

        <template v-if="local.engine === 'builtin'">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.desensitization.maskStyleLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.desensitization.maskStyleDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-radio-group v-model="local.maskStyle" @change="emitChange">
              <t-radio-button value="replace">{{ $t('knowledgeEditor.desensitization.maskReplace') }}</t-radio-button>
              <t-radio-button value="partial">{{ $t('knowledgeEditor.desensitization.maskPartial') }}</t-radio-button>
            </t-radio-group>
          </div>
        </div>

        <div class="setting-row setting-row-vertical">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.desensitization.entityTypesLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.desensitization.entityTypesDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-checkbox-group v-model="local.entityTypes" class="entity-types" @change="emitChange">
              <t-checkbox v-for="item in entityOptions" :key="item.value" :value="item.value">
                {{ item.label }}
              </t-checkbox>
            </t-checkbox-group>
            <div v-if="selectedPresetRules.length" class="rules preset-rules">
              <div v-for="(rule, index) in selectedPresetRules" :key="rule.entity + rule.pattern + index" class="rule-row">
                <t-input :model-value="rule.name" disabled :placeholder="$t('knowledgeEditor.desensitization.ruleName')" />
                <t-input :model-value="rule.pattern" disabled :placeholder="$t('knowledgeEditor.desensitization.rulePattern')" />
                <t-input :model-value="rule.replacement" disabled :placeholder="$t('knowledgeEditor.desensitization.ruleReplacement')" />
                <span class="preset-badge">{{ $t('knowledgeEditor.desensitization.presetReadonly') }}</span>
              </div>
            </div>
          </div>
        </div>

        <div class="setting-row setting-row-vertical">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.desensitization.customRulesLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.desensitization.customRulesDescription') }}</p>
          </div>
          <div class="setting-control rules">
            <div v-for="(rule, index) in local.rules" :key="index" class="rule-row">
              <t-input v-model="rule.name" :placeholder="$t('knowledgeEditor.desensitization.ruleName')" @change="emitChange" />
              <t-input v-model="rule.pattern" :placeholder="$t('knowledgeEditor.desensitization.rulePattern')" @change="emitChange" />
              <t-input v-model="rule.replacement" :placeholder="$t('knowledgeEditor.desensitization.ruleReplacement')" @change="emitChange" />
              <t-button variant="text" theme="danger" @click="removeRule(index)">{{ $t('common.delete') }}</t-button>
            </div>
            <t-button variant="outline" size="small" @click="addRule">{{ $t('knowledgeEditor.desensitization.addRule') }}</t-button>
            <t-alert
              theme="warning"
              class="notice-alert custom-rules-notice"
              :title="$t('knowledgeEditor.desensitization.customRulesTitle')"
              :message="$t('knowledgeEditor.desensitization.customRulesNotice')"
            />
          </div>
        </div>

        <div class="setting-row setting-row-vertical">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.desensitization.previewLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.desensitization.previewDescription') }}</p>
          </div>
          <div class="setting-control preview">
            <t-textarea v-model="previewInput" :placeholder="$t('knowledgeEditor.desensitization.previewPlaceholder')" :autosize="{ minRows: 3, maxRows: 8 }" />
            <t-button theme="default" :loading="previewing" @click="runPreview">{{ $t('knowledgeEditor.desensitization.previewRun') }}</t-button>
            <pre v-if="previewOutput !== null" class="preview-output">{{ previewOutput }}</pre>
          </div>
        </div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { previewDesensitization } from '@/api/desensitization'

export interface DesensitizationRule {
  name: string
  pattern: string
  replacement: string
}

export interface DesensitizationConfig {
  enabled: boolean
  engine: 'builtin' | 'presidio'
  maskStyle: 'replace' | 'partial'
  entityTypes: string[]
  rules: DesensitizationRule[]
  llmModelId: string
}

const ENTITY_VALUES = [
  'cn_id_card',
  'cn_mobile',
  'cn_landline',
  'cn_bank_card',
  'cn_uscc',
  'cn_plate',
  'email',
] as const

// Keep in sync with internal/desensitization/builtin/engine.go. Preset rows are
// read-only; checksums (ID / UnionPay Luhn / USCC) still run in the engine.
const PRESET_RULES: { entity: (typeof ENTITY_VALUES)[number]; nameKey?: string; pattern: string; replacement: string }[] = [
  { entity: 'cn_id_card', pattern: String.raw`[1-9](?:\s*\d){16}\s*[\dXx]`, replacement: '<身份证>' },
  { entity: 'cn_mobile', pattern: String.raw`(?:(?:\+86|0086)\s*)?1(?:3\d|4[5-9]|5[0-35-9]|6[2567]|7\d|8\d|9\d)(?:[\s-]?\d){8}`, replacement: '<手机号>' },
  { entity: 'cn_landline', pattern: String.raw`0\d{2,3}-?\d{7,8}`, replacement: '<固定电话>' },
  { entity: 'cn_bank_card', pattern: String.raw`62(?:[\s\-]*\d){14,17}`, replacement: '<银行卡>' },
  { entity: 'cn_uscc', pattern: String.raw`[0-9A-HJ-NPQRTUWXY]{2}\d{6}[0-9A-HJ-NPQRTUWXY]{10}`, replacement: '<统一社会信用代码>' },
  { entity: 'cn_plate', nameKey: 'cn_plate_nev', pattern: String.raw`[京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤川青藏琼宁][A-Z](?:[0-9A-HJ-NP-Z]{5}[DF]|[DF][0-9A-HJ-NP-Z]{5})`, replacement: '<车牌号>' },
  { entity: 'cn_plate', nameKey: 'cn_plate_std', pattern: String.raw`[京津沪渝冀豫云辽黑湘皖鲁新苏浙赣鄂桂甘晋蒙陕吉闽贵粤川青藏琼宁][A-Z][A-HJ-NP-Z0-9]{4}[A-HJ-NP-Z0-9挂学警港澳]`, replacement: '<车牌号>' },
  { entity: 'email', pattern: String.raw`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`, replacement: '<邮箱>' },
]

interface Props {
  config?: DesensitizationConfig
  allModels?: any[]
}

const props = defineProps<Props>()
const emit = defineEmits<{
  'update:config': [value: DesensitizationConfig]
}>()

const { t } = useI18n()

const defaultConfig = (): DesensitizationConfig => ({
  enabled: false,
  engine: 'builtin',
  maskStyle: 'replace',
  entityTypes: ['cn_id_card', 'cn_mobile', 'cn_bank_card', 'cn_uscc'],
  rules: [],
  llmModelId: '',
})

const normalizeEngine = (engine?: string): DesensitizationConfig['engine'] =>
  engine === 'presidio' ? 'presidio' : 'builtin'

const hydrate = (config?: DesensitizationConfig): DesensitizationConfig => {
  const base = defaultConfig()
  if (!config) return base
  return {
    ...base,
    ...config,
    engine: normalizeEngine(config.engine),
    rules: [...(config.rules || [])],
  }
}

const local = ref<DesensitizationConfig>(hydrate(props.config))
const previewInput = ref('')
const previewOutput = ref<string | null>(null)
const previewing = ref(false)

const entityOptions = computed(() =>
  ENTITY_VALUES.map((value) => ({
    value,
    label: t(`knowledgeEditor.desensitization.entities.${value}`),
  }))
)

const selectedPresetRules = computed(() => {
  const enabled = new Set(local.value.entityTypes)
  return PRESET_RULES.filter((rule) => enabled.has(rule.entity)).map((rule) => ({
    ...rule,
    name: t(`knowledgeEditor.desensitization.entities.${rule.nameKey || rule.entity}`),
  }))
})

watch(() => props.config, (value) => {
  if (!value) return
  local.value = hydrate(value)
}, { deep: true })

const emitChange = () => {
  emit('update:config', {
    ...local.value,
    engine: normalizeEngine(local.value.engine),
    rules: local.value.rules.map((rule) => ({ ...rule })),
    entityTypes: [...local.value.entityTypes],
  })
}

const addRule = () => {
  local.value.rules.push({ name: '', pattern: '', replacement: '' })
  emitChange()
}

const removeRule = (index: number) => {
  local.value.rules.splice(index, 1)
  emitChange()
}

const runPreview = async () => {
  const text = previewInput.value.trim()
  if (!text) {
    MessagePlugin.warning(t('knowledgeEditor.desensitization.previewEmpty'))
    return
  }
  previewing.value = true
  try {
    const resp: any = await previewDesensitization({
      text,
      desensitization_config: {
        enabled: true,
        engine: 'builtin',
        mask_style: local.value.maskStyle,
        entity_types: local.value.entityTypes,
        rules: local.value.rules.filter((rule) => rule.pattern.trim()),
      },
    })
    previewOutput.value = resp?.data?.text ?? ''
  } catch (err: any) {
    MessagePlugin.error(err?.message || t('knowledgeEditor.desensitization.previewFailed'))
  } finally {
    previewing.value = false
  }
}
</script>

<style scoped>
.section-header {
  margin-bottom: 16px;
}
.section-header h2 {
  font-size: 18px;
  font-weight: 600;
  margin: 0 0 8px;
}
.section-description {
  color: var(--td-text-color-secondary);
  margin: 0 0 12px;
}
.notice-alert {
  margin-top: 4px;
}
.custom-rules-notice {
  margin-top: 8px;
}
.settings-group {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.setting-row {
  display: flex;
  justify-content: space-between;
  gap: 24px;
  padding: 16px 0;
  border-bottom: 1px solid var(--td-border-level-1-color);
}
.setting-info {
  flex: 1;
  min-width: 0;
}
.setting-info label {
  display: block;
  font-weight: 500;
  margin-bottom: 4px;
}
.desc {
  margin: 0;
  color: var(--td-text-color-secondary);
  font-size: 13px;
  line-height: 1.5;
}
.setting-control {
  flex: 0 0 55%;
  max-width: 55%;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}
.setting-row-vertical {
  flex-direction: column;
  gap: 12px;
}
.setting-row-vertical .setting-info,
.setting-row-vertical .setting-control {
  flex: none;
  width: 100%;
  max-width: none;
}
.setting-row-vertical .setting-control {
  flex-direction: column;
  align-items: stretch;
  justify-content: flex-start;
}
.subsection {
  padding: 16px 20px;
  margin: 12px 0 0;
  background: var(--td-bg-color-container);
  border-radius: 8px;
  border-left: 3px solid var(--td-brand-color);
}
.entity-types {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
}
.preset-rules {
  margin-top: 12px;
}
.preset-badge {
  color: var(--td-text-color-placeholder);
  font-size: 12px;
  white-space: nowrap;
  padding: 0 4px;
}
.rules {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
}
.rule-row {
  display: grid;
  grid-template-columns: 1fr 1.4fr 1fr auto;
  gap: 8px;
  align-items: center;
}
.preview {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
}
.preview-output {
  margin: 0;
  padding: 12px;
  background: var(--td-bg-color-page);
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13px;
}
</style>
