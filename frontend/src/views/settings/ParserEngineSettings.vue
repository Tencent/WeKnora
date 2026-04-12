<template>
  <div class="parser-engine-settings">
    <div class="section-header">
      <h2>{{ $t('settings.parser.title') }}</h2>
      <p class="section-description">
        {{ $t('settings.parser.description') }}
      </p>
    </div>

    <div v-if="loading" class="loading-state">
      <t-loading size="small" />
      <span>{{ $t('settings.parser.loading') }}</span>
    </div>

    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadAll">{{ $t('settings.parser.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <template v-else>
      <div v-if="engines.length === 0 && !hasBuiltinEngine" class="empty-state">
        <p class="empty-text">{{ $t('settings.parser.noEngineDetected') }}</p>
      </div>

      <template v-else>
        <!-- 当后端未返回 builtin 引擎项时，仍展示 DocReader 状态卡片 -->
        <div v-if="!hasBuiltinEngine" class="engine-item first" data-model-type="builtin">
          <div class="engine-item-header">
            <div class="engine-title-row">
              <h3>builtin</h3>
              <t-tag
                :theme="connected ? 'success' : 'danger'"
                variant="light"
                size="small"
              >{{ connected ? $t('settings.parser.connected') : $t('settings.parser.disconnected') }}</t-tag>
            </div>
            <p>{{ $t('settings.parser.builtinDesc') }}</p>
          </div>
          <div class="docreader-inline">
            <div class="status-line">
              <t-tag
                :theme="connected ? 'success' : 'danger'"
                variant="light"
                size="small"
              >{{ connected ? $t('settings.parser.connected') : $t('settings.parser.disconnected') }}</t-tag>
              <t-tag theme="default" variant="light" size="small">{{ docreaderTransport === 'http' ? 'HTTP' : 'gRPC' }}</t-tag>
              <span v-if="docreaderAddrEnv" class="env-hint">{{ $t('settings.parser.currentAddr') }}: {{ docreaderAddrEnv }}</span>
            </div>
            <p class="docreader-desc">
              {{ $t('settings.parser.envVarHint') }}
            </p>
          </div>
        </div>

        <div
          v-for="(engine, idx) in sortedEngines"
          :key="engine.Name"
          :class="['engine-item', { first: idx === 0 && hasBuiltinEngine }]"
          :data-model-type="engine.Name"
        >
          <div class="engine-item-header">
            <div class="engine-title-row">
              <h3>{{ getEngineDisplayName(engine.Name) }}</h3>
              <t-tag v-if="engine.Available" theme="success" variant="light" size="small">{{ $t('settings.parser.available') }}</t-tag>
              <t-tooltip v-else-if="engine.UnavailableReason" :content="engine.UnavailableReason" placement="top">
                <t-tag theme="danger" variant="light" size="small" class="tag-with-tooltip">{{ $t('settings.parser.unavailable') }}</t-tag>
              </t-tooltip>
              <t-tag v-else theme="danger" variant="light" size="small">{{ $t('settings.parser.unavailable') }}</t-tag>
              <a
                v-if="engineDocLink(engine.Name)"
                :href="engineDocLink(engine.Name)"
                target="_blank"
                rel="noopener noreferrer"
                class="engine-doc-link"
              >{{ engineDocLabel(engine.Name) }} ↗</a>
            </div>
            <p>{{ getEngineDisplayDesc(engine.Name, engine.Description) }}</p>
          </div>

          <!-- builtin: DocReader 连接信息 -->
          <div v-if="engine.Name === 'builtin'" class="docreader-inline">
            <div class="status-line">
              <t-tag v-if="connected" theme="success" variant="light" size="small">{{ $t('settings.parser.connected') }}</t-tag>
              <t-tag v-else theme="danger" variant="light" size="small">{{ $t('settings.parser.disconnected') }}</t-tag>
              <t-tag theme="default" variant="light" size="small">{{ docreaderTransport === 'http' ? 'HTTP' : 'gRPC' }}</t-tag>
              <span v-if="docreaderAddrEnv" class="env-hint">{{ $t('settings.parser.currentAddr') }}: {{ docreaderAddrEnv }}</span>
            </div>
            <p class="docreader-desc">
              {{ $t('settings.parser.envVarHint') }}
            </p>
          </div>

          <div v-if="engine.FileTypes && engine.FileTypes.length" class="file-types">
            <t-tag
              v-for="ft in engine.FileTypes"
              :key="ft"
              size="small"
              variant="light"
              theme="default"
            >{{ ft }}</t-tag>
          </div>

          <!-- mineru 自建配置 -->
          <div v-if="engine.Name === 'mineru'" class="engine-form">
            <div class="form-field">
              <label>{{ t('settings.parser.selfHostedEndpoint') }}</label>
              <t-input
                v-model="config.mineru_endpoint"
                :placeholder="$t('settings.parser.mineruEndpointPlaceholder')"
                clearable
              />
            </div>
            <div class="form-field">
              <label>Backend</label>
              <t-select v-model="config.mineru_model" :placeholder="$t('settings.parser.defaultPipeline')" clearable>
                <t-option value="pipeline" label="pipeline" />
                <t-option value="vlm-auto-engine" label="vlm-auto-engine" />
                <t-option value="vlm-http-client" label="vlm-http-client" />
                <t-option value="hybrid-auto-engine" label="hybrid-auto-engine" />
                <t-option value="hybrid-http-client" label="hybrid-http-client" />
              </t-select>
            </div>
            <div class="form-toggles">
              <t-checkbox v-model="config.mineru_enable_formula">{{ $t('settings.parser.formulaRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_enable_table">{{ $t('settings.parser.tableRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_enable_ocr">OCR</t-checkbox>
            </div>
            <div class="form-field">
              <label>{{ t('settings.parser.language') }}</label>
              <t-input
                v-model="config.mineru_language"
                :placeholder="$t('settings.parser.languagePlaceholder')"
                clearable
              />
            </div>
          </div>

          <!-- mineru_cloud 云 API 配置 -->
          <div v-if="engine.Name === 'mineru_cloud'" class="engine-form">
            <div class="form-field">
              <label>API Key</label>
              <t-input
                v-model="config.mineru_api_key"
                type="password"
                :placeholder="$t('settings.parser.mineruCloudApiKeyPlaceholder')"
                clearable
              />
            </div>
            <div class="form-field">
              <label>Model Version</label>
              <t-select v-model="config.mineru_cloud_model" :placeholder="$t('settings.parser.defaultPipeline')" clearable>
                <t-option value="pipeline" label="pipeline" />
                <t-option value="vlm" :label="$t('settings.parser.vlmLabel')" />
                <t-option value="MinerU-HTML" :label="$t('settings.parser.mineruHtmlLabel')" />
              </t-select>
            </div>
            <div class="form-toggles">
              <t-checkbox v-model="config.mineru_cloud_enable_formula">{{ $t('settings.parser.formulaRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_cloud_enable_table">{{ $t('settings.parser.tableRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_cloud_enable_ocr">OCR</t-checkbox>
            </div>
            <div class="form-field">
              <label>{{ t('settings.parser.language') }}</label>
              <t-input
                v-model="config.mineru_cloud_language"
                :placeholder="$t('settings.parser.languagePlaceholder')"
                clearable
              />
            </div>
          </div>
        </div>
      </template>

      <!-- 检测与保存 -->
      <div class="save-bar">
        <t-button theme="default" variant="outline" :loading="checking" @click="onCheck">
          {{ $t('settings.parser.checkWithParams') }}
        </t-button>
        <t-button theme="primary" :loading="saving" @click="onSave">{{ $t('settings.parser.saveConfig') }}</t-button>
        <span v-if="checkMessage" class="save-msg hint">{{ checkMessage }}</span>
        <span v-else-if="saveMessage" :class="['save-msg', saveSuccess ? 'success' : 'error']">
          {{ saveMessage }}
        </span>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  getParserEngines,
  getParserEngineConfig,
  updateParserEngineConfig,
  checkParserEngines,
  type ParserEngineInfo,
  type ParserEngineConfig,
} from '@/api/system'

const { t } = useI18n()

const CONFIGURABLE_ENGINES = new Set(['mineru', 'mineru_cloud'])

/** 各解析引擎的项目/官方文档地址 */
const ENGINE_DOC_LINKS: Record<string, string> = {
  markitdown: 'https://github.com/microsoft/markitdown',
  mineru: 'https://github.com/opendatalab/MinerU',
  mineru_cloud: 'https://mineru.net/apiManage/docs',
}

/** 解析引擎配置默认值（与 DocReader/Python 侧一致） */
const DEFAULT_PARSER_CONFIG: ParserEngineConfig = {
  docreader_addr: '',
  docreader_transport: 'grpc',
  mineru_endpoint: '',
  mineru_api_key: '',
  mineru_model: 'pipeline',
  mineru_enable_formula: true,
  mineru_enable_table: true,
  mineru_enable_ocr: true,
  mineru_language: 'ch',
  mineru_cloud_model: 'pipeline',
  mineru_cloud_enable_formula: true,
  mineru_cloud_enable_table: true,
  mineru_cloud_enable_ocr: true,
  mineru_cloud_language: 'ch',
}

const engines = ref<ParserEngineInfo[]>([])
const docreaderAddrEnv = ref('')
const docreaderTransport = ref<'grpc' | 'http'>('grpc')
const connected = ref(false)
const loading = ref(true)
const error = ref('')

const config = ref<ParserEngineConfig>({ ...DEFAULT_PARSER_CONFIG })
const saving = ref(false)
const saveMessage = ref('')
const saveSuccess = ref(false)
const checking = ref(false)
const checkMessage = ref('')

const hasBuiltinEngine = computed(() => engines.value.some(e => e.Name === 'builtin'))

/** 固定展示顺序，未列出的引擎排在末尾按名称排序 */
const ENGINE_ORDER: Record<string, number> = {
  builtin: 0,
  simple: 1,
  markitdown: 2,
  mineru: 3,
  mineru_cloud: 4,
}

const sortedEngines = computed(() => {
  return [...engines.value].sort((a, b) => {
    const oa = ENGINE_ORDER[a.Name] ?? 100
    const ob = ENGINE_ORDER[b.Name] ?? 100
    if (oa !== ob) return oa - ob
    return a.Name.localeCompare(b.Name)
  })
})

function hasConfigFields(engineName: string): boolean {
  return CONFIGURABLE_ENGINES.has(engineName)
}

function engineDocLink(name: string): string | undefined {
  return ENGINE_DOC_LINKS[name]
}

function engineDocLabel(_name: string): string {
  return t('settings.parser.docs')
}

function getEngineDisplayName(engineName: string): string {
  const key = `kbSettings.parser.engines.${engineName}.name`
  const translated = t(key)
  return translated !== key ? translated : engineName
}

function getEngineDisplayDesc(engineName: string, fallback: string): string {
  const key = `kbSettings.parser.engines.${engineName}.desc`
  const translated = t(key)
  return translated !== key ? translated : fallback
}

async function loadEngines() {
  try {
    const res = await getParserEngines()
    engines.value = res?.data ?? []
    docreaderAddrEnv.value = res?.docreader_addr ?? ''
    const transport = (res?.docreader_transport ?? 'grpc').toLowerCase()
    docreaderTransport.value = transport === 'http' ? 'http' : 'grpc'
    connected.value = res?.connected ?? (engines.value.length > 0)
  } catch (e: any) {
    error.value = e?.message || t('settings.parser.loadFailed')
    engines.value = []
    connected.value = false
  }
}

async function loadConfig() {
  try {
    const res = await getParserEngineConfig()
    const data = res?.data
    config.value = {
      docreader_addr: data?.docreader_addr ?? DEFAULT_PARSER_CONFIG.docreader_addr ?? '',
      docreader_transport: data?.docreader_transport ?? DEFAULT_PARSER_CONFIG.docreader_transport ?? 'grpc',
      mineru_endpoint: data?.mineru_endpoint ?? DEFAULT_PARSER_CONFIG.mineru_endpoint ?? '',
      mineru_api_key: data?.mineru_api_key ?? DEFAULT_PARSER_CONFIG.mineru_api_key ?? '',
      mineru_model: data?.mineru_model ?? DEFAULT_PARSER_CONFIG.mineru_model ?? '',
      mineru_enable_formula: data?.mineru_enable_formula ?? DEFAULT_PARSER_CONFIG.mineru_enable_formula ?? true,
      mineru_enable_table: data?.mineru_enable_table ?? DEFAULT_PARSER_CONFIG.mineru_enable_table ?? true,
      mineru_enable_ocr: data?.mineru_enable_ocr ?? DEFAULT_PARSER_CONFIG.mineru_enable_ocr ?? true,
      mineru_language: data?.mineru_language ?? DEFAULT_PARSER_CONFIG.mineru_language ?? 'ch',
      mineru_cloud_model: data?.mineru_cloud_model ?? DEFAULT_PARSER_CONFIG.mineru_cloud_model ?? '',
      mineru_cloud_enable_formula: data?.mineru_cloud_enable_formula ?? DEFAULT_PARSER_CONFIG.mineru_cloud_enable_formula ?? true,
      mineru_cloud_enable_table: data?.mineru_cloud_enable_table ?? DEFAULT_PARSER_CONFIG.mineru_cloud_enable_table ?? true,
      mineru_cloud_enable_ocr: data?.mineru_cloud_enable_ocr ?? DEFAULT_PARSER_CONFIG.mineru_cloud_enable_ocr ?? true,
      mineru_cloud_language: data?.mineru_cloud_language ?? DEFAULT_PARSER_CONFIG.mineru_cloud_language ?? 'ch',
    }
  } catch {
    config.value = { ...DEFAULT_PARSER_CONFIG }
  }
}

async function loadAll() {
  loading.value = true
  error.value = ''
  await Promise.all([loadEngines(), loadConfig()])
  loading.value = false
}

function buildConfigPayload(): ParserEngineConfig {
  return {
    docreader_addr: config.value.docreader_addr?.trim() ?? '',
    docreader_transport: (config.value.docreader_transport ?? 'grpc').trim() || 'grpc',
    mineru_endpoint: config.value.mineru_endpoint?.trim() ?? '',
    mineru_api_key: config.value.mineru_api_key?.trim() ?? '',
    mineru_model: config.value.mineru_model?.trim() ?? '',
    mineru_enable_formula: config.value.mineru_enable_formula,
    mineru_enable_table: config.value.mineru_enable_table,
    mineru_enable_ocr: config.value.mineru_enable_ocr,
    mineru_language: config.value.mineru_language?.trim() ?? '',
    mineru_cloud_model: config.value.mineru_cloud_model?.trim() ?? '',
    mineru_cloud_enable_formula: config.value.mineru_cloud_enable_formula,
    mineru_cloud_enable_table: config.value.mineru_cloud_enable_table,
    mineru_cloud_enable_ocr: config.value.mineru_cloud_enable_ocr,
    mineru_cloud_language: config.value.mineru_cloud_language?.trim() ?? '',
  }
}

async function onCheck() {
  checking.value = true
  checkMessage.value = ''
  try {
    const res = await checkParserEngines(buildConfigPayload())
    engines.value = res?.data ?? []
    connected.value = res?.connected ?? false
    checkMessage.value = t('settings.parser.checkDoneStatusUpdated')
    setTimeout(() => { checkMessage.value = '' }, 3000)
  } catch (e: any) {
    checkMessage.value = e?.message || t('settings.parser.checkFailed')
  } finally {
    checking.value = false
  }
}

async function onSave() {
  saving.value = true
  saveMessage.value = ''
  try {
    await updateParserEngineConfig(buildConfigPayload())
    saveSuccess.value = true
    saveMessage.value = t('settings.parser.saveSuccess')
    loadEngines()
  } catch (e: any) {
    saveSuccess.value = false
    saveMessage.value = e?.message || t('settings.parser.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(loadAll)
</script>

<style lang="less" scoped>
.parser-engine-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 28px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.6;
  }
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 48px 0;
  color: var(--td-text-color-placeholder);
  font-size: 14px;
}

.error-inline {
  padding: 16px 0;
}

.empty-state {
  padding: 48px 0;
  text-align: center;

  .empty-text {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin: 0;
  }
}

// ---- 引擎条目 ----
.engine-item {
  padding-top: 24px;
  margin-top: 24px;
  border-top: 1px solid var(--td-component-stroke);

  &.first {
    margin-top: 0;
    padding-top: 0;
    border-top: none;
  }
}

.engine-item-header {
  margin-bottom: 16px;

  p {
    font-size: 13px;
    color: var(--td-text-color-placeholder);
    margin: 6px 0 0 0;
    line-height: 1.5;
  }
}

.engine-title-row {
  display: flex;
  align-items: center;
  gap: 10px;

  h3 {
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
    font-family: 'SF Mono', 'Monaco', 'Menlo', monospace;
  }
}

.engine-doc-link {
  margin-left: auto;
  font-size: 12px;
  color: var(--td-brand-color);
  text-decoration: none;
  white-space: nowrap;

  &:hover {
    opacity: 0.8;
  }
}

// ---- DocReader 连接信息 ----
.docreader-inline {
  padding: 10px 14px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
  margin-bottom: 12px;

  .status-line {
    margin-bottom: 6px;
  }
}

.docreader-desc {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.6;

  code {
    padding: 1px 5px;
    font-size: 11px;
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 3px;
  }
}

.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.env-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

// ---- 文件类型标签 ----
.file-types {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 4px;
}

// ---- 配置表单 ----
.engine-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px dashed var(--td-component-stroke);
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 6px;

  label {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-secondary);
  }
}

.form-toggles {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
}

// ---- 保存栏（sticky） ----
.save-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  position: sticky;
  bottom: 0;
  margin-top: 32px;
  padding: 16px 0 4px;
  background: linear-gradient(to bottom, transparent 0%, var(--td-bg-color-container) 12%);
  z-index: 10;
}

.save-msg {
  font-size: 13px;

  &.success {
    color: var(--td-success-color);
  }

  &.error {
    color: var(--td-error-color);
  }

  &.hint {
    color: var(--td-text-color-secondary);
  }
}

.tag-with-tooltip {
  cursor: help;
}
</style>
