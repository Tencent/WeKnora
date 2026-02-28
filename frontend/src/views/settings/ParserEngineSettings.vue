<template>
  <div class="parser-engine-settings">
    <div class="section-header">
      <h2>解析引擎</h2>
      <p class="section-description">
        文档解析引擎状态及配置。此处设置优先于服务端环境变量，留空则使用环境变量默认值。
      </p>
    </div>

    <div v-if="loading" class="loading-state">
      <t-loading size="small" />
      <span>加载中...</span>
    </div>

    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadAll">重试</t-button>
        </template>
      </t-alert>
    </div>

    <template v-else>
      <!-- 引擎列表 -->
      <div class="engine-section">
        <div v-if="engines.length === 0" class="empty-state">
          <p class="empty-text">未检测到解析引擎，请确认 DocReader 服务正常运行。</p>
        </div>

        <div v-else class="engine-grid">
          <!-- DocReader 未连接时显示占位卡片 -->
          <div v-if="!hasBuiltinEngine" class="engine-card unavailable">
            <div class="card-header">
              <span class="card-name">builtin</span>
              <t-tag theme="danger" variant="light" size="small">未连接</t-tag>
            </div>
            <p class="card-desc">DocReader 内置解析引擎（docx/pdf/xlsx 等复杂格式）</p>
            <div class="docreader-inline">
              <div class="status-line">
                <t-tag theme="danger" variant="light" size="small">未连接</t-tag>
                <t-tag theme="default" variant="light" size="small">{{ docreaderTransport === 'http' ? 'HTTP' : 'gRPC' }}</t-tag>
                <span v-if="docreaderAddrEnv" class="env-hint">当前: {{ docreaderAddrEnv }}</span>
              </div>
              <p class="desc docreader-desc">
                修改请设置环境变量 <code>DOCREADER_ADDR</code>、<code>DOCREADER_TRANSPORT</code>（grpc/http），重启服务生效。
              </p>
            </div>
          </div>

          <div
            v-for="engine in sortedEngines"
            :key="engine.Name"
            :class="['engine-card', { unavailable: !engine.Available }]"
          >
            <div class="card-header">
              <span class="card-name">{{ engine.Name }}</span>
              <t-tag v-if="engine.Available" theme="success" variant="light" size="small">可用</t-tag>
              <t-tooltip v-else-if="engine.UnavailableReason" :content="engine.UnavailableReason" placement="top">
                <t-tag theme="danger" variant="light" size="small" class="tag-with-tooltip">不可用</t-tag>
              </t-tooltip>
              <t-tag v-else theme="danger" variant="light" size="small">不可用</t-tag>
              <a
                v-if="engineDocLink(engine.Name)"
                :href="engineDocLink(engine.Name)"
                target="_blank"
                rel="noopener noreferrer"
                class="card-doc-link"
              >{{ engineDocLabel(engine.Name) }} ↗</a>
            </div>

            <p class="card-desc">{{ engine.Description }}</p>

            <!-- builtin 引擎：内嵌 DocReader 连接信息 -->
            <div v-if="engine.Name === 'builtin'" class="docreader-inline">
              <div class="status-line">
                <t-tag v-if="connected" theme="success" variant="light" size="small">已连接</t-tag>
                <t-tag v-else theme="danger" variant="light" size="small">未连接</t-tag>
                <t-tag theme="default" variant="light" size="small">{{ docreaderTransport === 'http' ? 'HTTP' : 'gRPC' }}</t-tag>
                <span v-if="docreaderAddrEnv" class="env-hint">当前: {{ docreaderAddrEnv }}</span>
              </div>
              <p class="desc docreader-desc">
                修改请设置环境变量 <code>DOCREADER_ADDR</code>、<code>DOCREADER_TRANSPORT</code>（grpc/http），重启服务生效。
              </p>
            </div>

            <div class="card-types">
              <t-tag
                v-for="ft in (engine.FileTypes || [])"
                :key="ft"
                size="small"
                variant="light"
                theme="default"
              >{{ ft }}</t-tag>
            </div>

            <!-- mineru 自建配置 -->
            <div v-if="engine.Name === 'mineru'" class="card-config">
              <div class="config-item">
                <label>自建端点</label>
                <t-input
                  v-model="config.mineru_endpoint"
                  size="small"
                  placeholder="如 https://your-mineru.example.com"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Backend</label>
                <t-select v-model="config.mineru_model" size="small" placeholder="默认 pipeline" clearable>
                  <t-option value="pipeline" label="pipeline" />
                  <t-option value="vlm-auto-engine" label="vlm-auto-engine" />
                  <t-option value="vlm-http-client" label="vlm-http-client" />
                  <t-option value="hybrid-auto-engine" label="hybrid-auto-engine" />
                  <t-option value="hybrid-http-client" label="hybrid-http-client" />
                </t-select>
              </div>
              <div class="config-toggles">
                <t-checkbox v-model="config.mineru_enable_formula">公式识别</t-checkbox>
                <t-checkbox v-model="config.mineru_enable_table">表格识别</t-checkbox>
                <t-checkbox v-model="config.mineru_enable_ocr">OCR</t-checkbox>
              </div>
              <div class="config-item">
                <label>语言</label>
                <t-input
                  v-model="config.mineru_language"
                  size="small"
                  placeholder="如 ch、en、ja（默认 ch）"
                  clearable
                />
              </div>
            </div>

            <!-- mineru_cloud 云 API 配置 -->
            <div v-if="engine.Name === 'mineru_cloud'" class="card-config">
              <div class="config-item">
                <label>API Key</label>
                <t-input
                  v-model="config.mineru_api_key"
                  size="small"
                  type="password"
                  placeholder="MinerU 云服务 API Key"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>API 地址</label>
                <t-input
                  v-model="config.mineru_api_base_url"
                  size="small"
                  placeholder="如 https://api.mineru.net"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Model Version</label>
                <t-select v-model="config.mineru_cloud_model" size="small" placeholder="默认 pipeline" clearable>
                  <t-option value="pipeline" label="pipeline" />
                  <t-option value="vlm" label="vlm（视觉语言模型）" />
                  <t-option value="MinerU-HTML" label="MinerU-HTML（HTML 解析）" />
                </t-select>
              </div>
              <div class="config-toggles">
                <t-checkbox v-model="config.mineru_cloud_enable_formula">公式识别</t-checkbox>
                <t-checkbox v-model="config.mineru_cloud_enable_table">表格识别</t-checkbox>
                <t-checkbox v-model="config.mineru_cloud_enable_ocr">OCR</t-checkbox>
              </div>
              <div class="config-item">
                <label>语言</label>
                <t-input
                  v-model="config.mineru_cloud_language"
                  size="small"
                  placeholder="如 ch、en、ja（默认 ch）"
                  clearable
                />
              </div>
            </div>
          </div>
        </div>
      </div>

      <!-- 检测与保存 -->
      <div class="save-bar">
        <t-button theme="default" variant="outline" :loading="checking" @click="onCheck">
          使用当前参数检测
        </t-button>
        <t-button theme="primary" :loading="saving" @click="onSave">保存配置</t-button>
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
import {
  getParserEngines,
  getParserEngineConfig,
  updateParserEngineConfig,
  checkParserEngines,
  type ParserEngineInfo,
  type ParserEngineConfig,
} from '@/api/system'

const CONFIGURABLE_ENGINES = new Set(['mineru', 'mineru_cloud'])

/** 各解析引擎的项目/官方文档地址 */
const ENGINE_DOC_LINKS: Record<string, { url: string; label: string }> = {
  markitdown: { url: 'https://github.com/microsoft/markitdown', label: 'Markitdown 文档' },
  mineru: { url: 'https://github.com/opendatalab/MinerU', label: 'MinerU 文档' },
  mineru_cloud: { url: 'https://mineru.net/apiManage/docs', label: 'MinerU 文档' },
}

/** 解析引擎配置默认值（与 DocReader/Python 侧一致） */
const DEFAULT_PARSER_CONFIG: ParserEngineConfig = {
  docreader_addr: '',
  docreader_transport: 'grpc',
  mineru_endpoint: '',
  mineru_api_key: '',
  mineru_api_base_url: 'https://mineru.net/api/v4',
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
  return ENGINE_DOC_LINKS[name]?.url
}

function engineDocLabel(name: string): string {
  return ENGINE_DOC_LINKS[name]?.label ?? '文档'
}

async function loadEngines() {
  try {
    const res = await getParserEngines()
    engines.value = res?.data ?? []
    docreaderAddrEnv.value = res?.docreader_addr ?? ''
    const t = (res?.docreader_transport ?? 'grpc').toLowerCase()
    docreaderTransport.value = t === 'http' ? 'http' : 'grpc'
    connected.value = res?.connected ?? (engines.value.length > 0)
  } catch (e: any) {
    error.value = e?.message || '加载解析引擎列表失败'
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
      mineru_api_base_url: data?.mineru_api_base_url ?? DEFAULT_PARSER_CONFIG.mineru_api_base_url ?? '',
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
    mineru_api_base_url: config.value.mineru_api_base_url?.trim() ?? '',
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
    checkMessage.value = '请先确保 DocReader 服务已通过环境变量配置并已连接'
    return
  }
  checking.value = true
  checkMessage.value = ''
  try {
    const res = await checkParserEngines(buildConfigPayload())
    engines.value = res?.data ?? []
    checkMessage.value = '已使用当前填写参数检测，上方状态已更新'
    setTimeout(() => { checkMessage.value = '' }, 3000)
  } catch (e: any) {
    checkMessage.value = e?.message || '检测失败'
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
    saveMessage.value = '保存成功'
    loadEngines()
  } catch (e: any) {
    saveSuccess.value = false
    saveMessage.value = e?.message || '保存失败'
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

// ---- 页面标题 ----
.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #333;
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: #666;
    margin: 0;
    line-height: 1.5;
  }
}

// ---- 加载 / 错误 ----
.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 48px 0;
  color: #999;
  font-size: 14px;
}

.error-inline {
  padding: 16px 0;
}

.docreader-inline {
  padding: 10px 12px;
  background: #f7f8fa;
  border-radius: 6px;

  .status-line {
    margin-bottom: 6px;
  }
}

.docreader-desc {
  margin: 0;
  font-size: 12px;
  color: #888;
  line-height: 1.6;

  code {
    padding: 1px 5px;
    font-size: 11px;
    background: #eee;
    border-radius: 3px;
  }
}

.card-doc-link {
  margin-left: auto;
  font-size: 12px;
  color: #999;
  text-decoration: none;
  white-space: nowrap;

  &:hover {
    color: var(--td-brand-color, #0052d9);
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
  color: #999;
}

// ---- 引擎列表 ----
.engine-section {
  margin-top: 0;
}

.engine-grid {
  columns: 2;
  column-gap: 12px;

  @media (max-width: 860px) {
    columns: 1;
  }
}

.engine-card {
  break-inside: avoid;
  margin-bottom: 12px;
  padding: 16px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  background: #fff;
  transition: border-color 0.2s;
  display: flex;
  flex-direction: column;
  gap: 8px;

  &:hover {
    border-color: #c0c4cc;
  }

  &.unavailable {
    background: #fafafa;
  }
}

.card-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.card-name {
  font-size: 15px;
  font-weight: 600;
  color: #333;
  font-family: 'SF Mono', 'Monaco', 'Menlo', monospace;
}

.card-desc {
  font-size: 13px;
  color: #666;
  margin: 0;
  line-height: 1.5;
}

.card-types {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.card-reason {
  font-size: 12px;
  color: #e34d59;
  margin: 0;
}

// ---- 卡片内配置区 ----
.card-config {
  margin-top: 4px;
  padding-top: 12px;
  border-top: 1px dashed #e5e7eb;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.config-item {
  display: flex;
  flex-direction: column;
  gap: 4px;

  label {
    font-size: 12px;
    font-weight: 500;
    color: #555;
  }
}

.config-toggles {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

// ---- 空状态 ----
.empty-state {
  padding: 48px 0;
  text-align: center;

  .empty-text {
    font-size: 14px;
    color: #999;
    margin: 0;
  }
}

// ---- 保存栏 ----
.save-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 24px;
  padding-top: 24px;
  border-top: 1px solid #e5e7eb;
}

.save-msg {
  font-size: 13px;

  &.success {
    color: #52c41a;
  }

  &.error {
    color: #ff4d4f;
  }

  &.hint {
    color: #666;
  }
}

.tag-with-tooltip {
  cursor: help;
}
</style>
