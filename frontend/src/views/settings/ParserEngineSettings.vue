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

      <div v-else class="engine-cards">
        <!-- 当后端未返回 builtin 引擎项时，仍展示 DocReader 状态卡片 -->
        <div
          v-if="!hasBuiltinEngine"
          :class="['engine-card', { active: drawerVisible && currentEngine?.Name === 'builtin' }]"
          @click="openDrawer({ Name: 'builtin' } as any)"
        >
          <div class="engine-card-header">
            <h3>builtin</h3>
            <t-tag
              :theme="connected ? 'success' : 'danger'"
              variant="light"
              size="small"
            >{{ connected ? $t('settings.parser.connected') : $t('settings.parser.disconnected') }}</t-tag>
          </div>
          <p class="engine-card-desc">{{ $t('settings.parser.builtinDesc') }}</p>
        </div>

        <div
          v-for="engine in sortedEngines"
          :key="engine.Name"
          :class="['engine-card', { active: drawerVisible && currentEngine?.Name === engine.Name }]"
          @click="openDrawer(engine)"
        >
          <div class="engine-card-header">
            <h3>{{ getEngineDisplayName(engine.Name) }}</h3>
            <t-tag v-if="engine.Available" theme="success" variant="light" size="small">{{ $t('settings.parser.available') }}</t-tag>
            <t-tooltip v-else-if="engine.UnavailableReason" :content="engine.UnavailableReason" placement="top">
              <t-tag theme="danger" variant="light" size="small" class="tag-with-tooltip">{{ $t('settings.parser.unavailable') }}</t-tag>
            </t-tooltip>
            <t-tag v-else theme="danger" variant="light" size="small">{{ $t('settings.parser.unavailable') }}</t-tag>
          </div>
          <p class="engine-card-desc">{{ getEngineDisplayDesc(engine.Name, engine.Description) }}</p>
        </div>
      </div>
    </template>

    <!-- 配置抽屉 -->
    <t-drawer
      v-model:visible="drawerVisible"
      :header="drawerTitle"
      size="500px"
    >
      <div v-if="currentEngine" class="drawer-content">
        <div class="engine-info-block">
          <p class="engine-desc">{{ getEngineDisplayDesc(currentEngine.Name, currentEngine.Description) }}
          <a
            v-if="engineDocLink(currentEngine.Name)"
            :href="engineDocLink(currentEngine.Name)"
            target="_blank"
            rel="noopener noreferrer"
            class="doc-link"
          >
            {{ engineDocLabel(currentEngine.Name) }}
            <t-icon name="link" class="link-icon" />
          </a>
          </p>
        </div>

        <!-- builtin: DocReader 连接信息 -->
        <div v-if="currentEngine.Name === 'builtin'" class="docreader-inline">
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

        <!-- weknoracloud: 凭证状态 -->
        <template v-if="currentEngine.Name === 'weknoracloud'">
          <div v-if="wkcState === 'configured'" class="wkc-status wkc-status--ok">
            <t-icon name="check-circle" style="font-size: 15px; color: var(--td-success-color); flex-shrink: 0;" />
            <span>{{ $t('settings.weknoraCloud.credentialConfigured') }}</span>
          </div>
          <div v-else-if="wkcState === 'loading'" class="wkc-status">
            <t-loading size="small" />
            <span>{{ $t('settings.weknoraCloud.checkingStatus') }}</span>
          </div>
          <div v-else class="wkc-status wkc-status--warn">
            <t-icon name="error-circle" style="font-size: 15px; color: #f97316; flex-shrink: 0;" />
            <div style="flex: 1;">
              <span v-if="wkcState === 'expired'">{{ $t('settings.weknoraCloud.credentialExpired') }}</span>
              <span v-else>{{ $t('settings.weknoraCloud.unconfigured') }}</span>
              <div style="margin-top: 6px;">
                <t-button
                  variant="text"
                  size="small"
                  theme="primary"
                  @click="goToWkcSettings"
                  style="padding: 0; height: auto;"
                >{{ $t('settings.weknoraCloud.goToSettings') }}</t-button>
              </div>
            </div>
          </div>
        </template>

        <div v-if="currentEngine.FileTypes && currentEngine.FileTypes.length" class="file-types">
          <t-tag
            v-for="ft in currentEngine.FileTypes"
            :key="ft"
            size="small"
            variant="light"
            theme="default"
          >{{ ft }}</t-tag>
        </div>

        <!-- mineru 自建配置 -->
        <div v-if="currentEngine.Name === 'mineru'" class="engine-form">
          <div class="form-item">
            <label class="form-label">{{ t('settings.parser.selfHostedEndpoint') }}</label>
            <t-input
              v-model="config.mineru_endpoint"
              :placeholder="$t('settings.parser.mineruEndpointPlaceholder')"
              clearable
            />
          </div>
          <div class="form-item">
            <label class="form-label">Backend</label>
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
          <div class="form-item" style="margin-top: 16px;">
            <label class="form-label">{{ t('settings.parser.language') }}</label>
            <t-input
              v-model="config.mineru_language"
              :placeholder="$t('settings.parser.languagePlaceholder')"
              clearable
            />
          </div>
        </div>

        <!-- mineru_cloud 云 API 配置 -->
        <div v-if="currentEngine.Name === 'mineru_cloud'" class="engine-form">
          <div class="form-item">
            <label class="form-label">API Key</label>
            <t-input
              v-model="config.mineru_api_key"
              type="password"
              :placeholder="$t('settings.parser.mineruCloudApiKeyPlaceholder')"
              clearable
            />
          </div>
          <div class="form-item">
            <label class="form-label">Model Version</label>
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
          <div class="form-item" style="margin-top: 16px;">
            <label class="form-label">{{ t('settings.parser.language') }}</label>
            <t-input
              v-model="config.mineru_cloud_language"
              :placeholder="$t('settings.parser.languagePlaceholder')"
              clearable
            />
          </div>
        </div>
        <div class="form-item" v-if="currentEngine && (hasConfigFields(currentEngine.Name) || currentEngine.Name === 'builtin')">
          <label class="form-label">{{ $t('settings.parser.testConnection', '测试连接') }}</label>
          <div class="api-test-section">
            <t-button variant="outline" :loading="checking" @click="onCheck">
              {{ $t('settings.parser.testConnection', '测试连接') }}
            </t-button>
            <span v-if="checkMessage || saveMessage" :class="['test-message', saveSuccess && !checkMessage ? 'success' : (checkMessage ? 'hint' : 'error')]">
              {{ checkMessage || saveMessage }}
            </span>
          </div>
        </div>
      </div>
      
      <template #footer>
        <div class="drawer-footer-actions">
          <t-button theme="default" variant="outline" @click="drawerVisible = false">{{ $t('common.cancel') }}</t-button>
          <t-button theme="primary" :loading="saving" @click="onSave">{{ $t('common.save') }}</t-button>
        </div>
      </template>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'
import {
  getParserEngines,
  getParserEngineConfig,
  updateParserEngineConfig,
  checkParserEngines,
  type ParserEngineInfo,
  type ParserEngineConfig,
} from '@/api/system'
import { getWeKnoraCloudStatus } from '@/api/model'

const { t } = useI18n()
const uiStore = useUIStore()

const CONFIGURABLE_ENGINES = new Set(['mineru', 'mineru_cloud'])

/** 各解析引擎的项目/官方文档地址 */
const ENGINE_DOC_LINKS: Record<string, string> = {
  weknoracloud: 'https://developers.weixin.qq.com/doc/aispeech/knowledge/atomic_capability/atomic_interface.html',
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

const drawerVisible = ref(false)
const currentEngine = ref<ParserEngineInfo | null>(null)
const drawerTitle = computed(() => {
  return currentEngine.value ? getEngineDisplayName(currentEngine.value.Name) : ''
})

/** 固定展示顺序，未列出的引擎排在末尾按名称排序 */
const ENGINE_ORDER: Record<string, number> = {
  builtin: 0,
  weknoracloud: 1,
  simple: 2,
  markitdown: 3,
  mineru: 4,
  mineru_cloud: 5,
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

function openDrawer(engine: ParserEngineInfo) {
  currentEngine.value = engine
  drawerVisible.value = true
  saveMessage.value = ''
  checkMessage.value = ''
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
  await Promise.all([loadEngines(), loadConfig(), checkWkcStatus()])
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
  if (!connected) {
    checkMessage.value = t('settings.parser.ensureDocreaderConnected')
    return
  }
  checking.value = true
  checkMessage.value = ''
  saveMessage.value = ''
  try {
    const res = await checkParserEngines(buildConfigPayload())
    engines.value = res?.data ?? []
    if (res?.connected !== undefined) {
      connected.value = res.connected
    }

    if (currentEngine.value) {
      if (currentEngine.value.Name === 'builtin') {
        if (connected.value) {
          checkMessage.value = t('settings.parser.checkSuccess', '测试连接成功')
          saveSuccess.value = true
        } else {
          checkMessage.value = t('settings.parser.checkFailed', '测试连接失败')
          saveSuccess.value = false
        }
      } else {
        const updatedEngine = engines.value.find(e => e.Name === currentEngine.value!.Name)
        if (updatedEngine) {
          if (updatedEngine.Available) {
            checkMessage.value = t('settings.parser.checkSuccess', '测试连接成功')
            saveSuccess.value = true
          } else {
            checkMessage.value = updatedEngine.UnavailableReason || t('settings.parser.checkFailed', '测试连接失败')
            saveSuccess.value = false
          }
        } else {
          checkMessage.value = t('settings.parser.checkFailed', '引擎状态未知')
          saveSuccess.value = false
        }
      }
    } else {
      checkMessage.value = t('settings.parser.checkDoneStatusUpdated', '检测已完成，状态已更新')
      saveSuccess.value = true
    }

    setTimeout(() => { checkMessage.value = '' }, 3000)
  } catch (e: any) {
    checkMessage.value = e?.message || t('settings.parser.checkFailed', '测试连接失败')
    saveSuccess.value = false
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
    drawerVisible.value = false
    loadEngines()
  } catch (e: any) {
    saveSuccess.value = false
    saveMessage.value = e?.message || t('settings.parser.saveFailed')
  } finally {
    saving.value = false
  }
}

// ---- WeKnoraCloud 凭证状态 ----
const wkcState = ref<'loading' | 'unconfigured' | 'configured' | 'expired'>('loading')

async function checkWkcStatus() {
  wkcState.value = 'loading'
  try {
    const status = await getWeKnoraCloudStatus()
    if (status.needs_reinit) {
      wkcState.value = 'expired'
    } else if (status.has_models) {
      wkcState.value = 'configured'
    } else {
      wkcState.value = 'unconfigured'
    }
  } catch {
    wkcState.value = 'unconfigured'
  }
}

async function goToWkcSettings() {
  if (uiStore.showSettingsModal) {
    uiStore.closeSettings()
    await nextTick()
  }
  uiStore.openSettings('weknoracloud')
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

// ---- 引擎卡片布局 ----
.engine-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
  margin-top: 24px;
}

.engine-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 16px;
  cursor: pointer;
  transition: all 0.2s ease;
  background: var(--td-bg-color-container);
  display: flex;
  flex-direction: column;

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
  }

  &.active {
    border-color: var(--td-brand-color);
    background: rgba(var(--td-brand-color-5-rgba), 0.05);
  }
}

.engine-card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;

  h3 {
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
    font-family: var(--app-font-family-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.engine-card-desc {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  margin: 0;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

// ---- 抽屉内容 ----
.drawer-content {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.engine-info-block {
  .engine-desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0 0 8px 0;
    line-height: 1.5;
  }
}

// 输入框样式
:deep(.t-input),
:deep(.t-select) {
  width: 100%;
  font-size: 13px;

  .t-input__inner,
  .t-input__wrap,
  input {
    font-size: 13px;
    border-radius: 6px;
    border-color: var(--td-component-stroke);
    transition: all 0.15s ease;
  }

  &:hover .t-input__inner,
  &:hover .t-input__wrap,
  &:hover input {
    border-color: var(--td-component-stroke);
  }

  &.t-is-focused .t-input__inner,
  &.t-is-focused .t-input__wrap,
  &.t-is-focused input {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 2px rgba(7, 192, 95, 0.1);
  }
}

// ---- DocReader 连接信息 ----
.docreader-inline {
  padding: 12px 16px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;

  .status-line {
    margin-bottom: 8px;
  }
}

.docreader-desc {
  margin: 0;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  line-height: 1.6;
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
}

// ---- 配置表单 ----
.engine-form {
  display: flex;
  flex-direction: column;
  gap: 0;
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
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);

  &.required::after {
    content: '*';
    color: var(--td-error-color);
    margin-left: 4px;
    font-weight: 600;
  }
}

.form-toggles {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  margin-bottom: 20px;
}

.tag-with-tooltip {
  cursor: help;
}

// ---- WeKnoraCloud 凭证状态 ----
.wkc-status {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 12px 16px;
  border-radius: 6px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);

  &--ok {
    background: var(--td-success-color-light);
    border: 1px solid var(--td-success-color-focus);
    color: var(--td-success-color);
  }

  &--warn {
    background: #fff7ed;
    border: 1px solid #fed7aa;
    border-left: 3px solid #f97316;
  }
}

.api-test-section {
  display: flex;
  align-items: center;
  gap: 12px;

  .test-message {
    font-size: 13px;
    line-height: 1.5;
    flex: 1;

    &.success {
      color: var(--td-brand-color-active);
    }

    &.error {
      color: var(--td-error-color);
    }

    &.hint {
      color: var(--td-text-color-secondary);
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
      color: var(--td-brand-color);
    }

    &.unavailable {
      color: var(--td-error-color);
    }
  }
}

.drawer-footer-actions {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
  width: 100%;

  :deep(.t-button) {
    min-width: 80px;
    height: 36px;
    font-weight: 500;
    font-size: 14px;
    border-radius: 6px;
    transition: all 0.15s ease;

    &.t-button--variant-outline {
      color: var(--td-text-color-secondary);
      border-color: var(--td-component-stroke);

      &:hover {
        border-color: var(--td-brand-color);
        color: var(--td-brand-color);
        background: rgba(7, 192, 95, 0.04);
      }
    }
  }
}
</style>
