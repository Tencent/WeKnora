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

      <!-- 与其它 settings 列表同形：左侧 monogram 徽章 + 标题 + 状态徽 + 两行描述。
           整张卡片可点击，打开抽屉配置；当前抽屉对应的卡片获得品牌色描边。 -->
      <div v-else class="engine-cards">
        <!-- 当后端未返回 builtin 引擎项时，仍展示 DocReader 状态卡片 -->
        <button
          v-if="!hasBuiltinEngine"
          type="button"
          class="engine-card engine-card--builtin"
          :class="{ 'engine-card--active': drawerVisible && currentEngine?.Name === 'builtin' }"
          @click="openDrawer({ Name: 'builtin' } as any)"
        >
          <div class="engine-card__badge">{{ engineInitial('builtin') }}</div>
          <div class="engine-card__body">
            <div class="engine-card__header">
              <h3 class="engine-card__title">{{ getEngineDisplayName('builtin') }}</h3>
              <span
                class="engine-card__status"
                :class="connected ? 'engine-card__status--on' : 'engine-card__status--err'"
              >
                <span class="engine-card__status-dot" />
                {{ connected ? $t('settings.parser.connected') : $t('settings.parser.disconnected') }}
              </span>
            </div>
            <p class="engine-card__desc">{{ $t('settings.parser.builtinDesc') }}</p>
          </div>
        </button>

        <button
          v-for="engine in sortedEngines"
          :key="engine.Name"
          type="button"
          class="engine-card"
          :class="[
            `engine-card--${engine.Name}`,
            { 'engine-card--active': drawerVisible && currentEngine?.Name === engine.Name }
          ]"
          @click="openDrawer(engine)"
        >
          <div class="engine-card__badge">{{ engineInitial(engine.Name) }}</div>
          <div class="engine-card__body">
            <div class="engine-card__header">
              <h3 class="engine-card__title">{{ getEngineDisplayName(engine.Name) }}</h3>
              <span v-if="engine.Available" class="engine-card__status engine-card__status--on">
                <span class="engine-card__status-dot" />
                {{ $t('settings.parser.available') }}
              </span>
              <t-tooltip
                v-else-if="engine.UnavailableReason"
                :content="engine.UnavailableReason"
                placement="top"
              >
                <span class="engine-card__status engine-card__status--err engine-card__status--help">
                  <span class="engine-card__status-dot" />
                  {{ $t('settings.parser.unavailable') }}
                </span>
              </t-tooltip>
              <span v-else class="engine-card__status engine-card__status--err">
                <span class="engine-card__status-dot" />
                {{ $t('settings.parser.unavailable') }}
              </span>
            </div>
            <p class="engine-card__desc">{{ getEngineDisplayDesc(engine.Name, engine.Description) }}</p>
          </div>
        </button>
      </div>
    </template>

    <!-- 配置抽屉 — 用 SettingDrawer 包装，保持与 ModelEditorDialog 同款视觉/交互 -->
    <SettingDrawer
      v-model:visible="drawerVisible"
      :title="drawerTitle"
      :class="currentEngine ? `parser-engine-drawer parser-engine-drawer--${currentEngine.Name}` : 'parser-engine-drawer'"
      :hide-footer="!authStore.hasRole('admin') && !needsTestButton"
      :confirm-loading="saving"
      @confirm="onSave"
      @cancel="drawerVisible = false"
    >
      <!--
        Header icon — 与列表卡片同款 monogram 徽章：首字母 + per-engine 配色，
        通过 .parser-engine-drawer--{name} .setting-drawer__header-icon 在
        非 scoped 块里覆盖背景与文字色。Parser 引擎没有真实 logo，所以这
        里只渲染字母；存储引擎那边走的是 logo 图片/mask，pattern 一致。
      -->
      <template v-if="currentEngine" #headerIcon>
        <span class="header-icon__text">{{ engineInitial(currentEngine.Name) }}</span>
      </template>
      <!--
        Subtitle slot: 引擎描述 + 内联文档链接。我们把"参考资料"从一个
        独立 section 收回到头部副标题里 — 一个外链不值得占一整个 section。
      -->
      <template v-if="currentEngine" #subtitle>
        <span>{{ getEngineDisplayDesc(currentEngine.Name, currentEngine.Description) }}</span>
        <a
          v-if="engineDocLink(currentEngine.Name)"
          :href="engineDocLink(currentEngine.Name)"
          target="_blank"
          rel="noopener noreferrer"
          class="doc-link doc-link--inline"
        >
          {{ engineDocLabel(currentEngine.Name) }}
          <t-icon name="link" class="link-icon" />
        </a>
      </template>
      <!--
        Footer-left slot: 测试连接按钮 + 状态文案 — 主操作栏沿底边对齐，
        与 ModelEditorDialog 远程模型抽屉保持一致。仅在引擎有可校验的
        配置/状态时才挂载。
      -->
      <template v-if="needsTestButton" #footer-left>
        <t-button variant="outline" :loading="checking" @click="onCheck">
          <template #icon>
            <t-icon v-if="!checking && saveSuccess && checkMessage" name="check-circle-filled"
              class="status-icon available" />
            <t-icon v-else-if="!checking && checkMessage && !saveSuccess" name="close-circle-filled"
              class="status-icon unavailable" />
          </template>
          {{ checking ? $t('settings.parser.checking', $t('settings.parser.testConnection')) : $t('settings.parser.testConnection') }}
        </t-button>
        <span v-if="checkMessage" :class="['footer-test-message', saveSuccess ? 'success' : 'error']" :title="checkMessage">
          {{ checkMessage }}
        </span>
      </template>

      <div v-if="currentEngine">
        <!--
          Section 1 — 支持文件类型。放在内容开头作为引擎"能干什么"的
          一目了然概览，对所有引擎都有意义；与状态/配置区分开。
        -->
        <section
          v-if="currentEngine.FileTypes && currentEngine.FileTypes.length"
          class="setting-drawer__section"
        >
          <h4 class="setting-drawer__section-title">{{ $t('settings.parser.supportedFileTypes', '支持文件类型') }}</h4>
          <div class="file-types">
            <span v-for="ft in currentEngine.FileTypes" :key="ft" class="file-type-chip">
              {{ ft }}
            </span>
          </div>
        </section>

        <!--
          Section 2 — 状态信息（DocReader 连接 / WeKnoraCloud 凭证）
          只有有内容时才渲染，避免空 section 空底部分隔线。
        -->
        <section
          v-if="currentEngine.Name === 'builtin' || currentEngine.Name === 'weknoracloud'"
          class="setting-drawer__section"
        >
          <h4 class="setting-drawer__section-title">{{ $t('settings.parser.statusSection', '状态信息') }}</h4>

          <!-- builtin: DocReader 连接信息 -->
          <div v-if="currentEngine.Name === 'builtin'" class="docreader-block">
            <div class="status-line">
              <t-tag v-if="connected" theme="success" variant="light" size="small">
                {{ $t('settings.parser.connected') }}
              </t-tag>
              <t-tag v-else theme="danger" variant="light" size="small">
                {{ $t('settings.parser.disconnected') }}
              </t-tag>
              <t-tag theme="default" variant="light" size="small">
                {{ docreaderTransport === 'http' ? 'HTTP' : 'gRPC' }}
              </t-tag>
              <span v-if="docreaderAddrEnv" class="env-hint">
                {{ $t('settings.parser.currentAddr') }}: {{ docreaderAddrEnv }}
              </span>
            </div>
            <p class="form-desc">{{ $t('settings.parser.envVarHint') }}</p>
          </div>

          <!--
            weknoracloud: 凭证状态 — 不再用大块卡片。已配置 / 加载中 / 未配置
            统一用 inline alert：图标 + 一行文案 + 行尾跳转 link，体量
            匹配"一条信息"该有的样子。
          -->
          <template v-if="currentEngine.Name === 'weknoracloud'">
            <div v-if="wkcState === 'configured'" class="inline-alert inline-alert--ok">
              <t-icon name="check-circle-filled" class="inline-alert__icon" />
              <span>{{ $t('settings.weknoraCloud.credentialConfigured') }}</span>
            </div>
            <div v-else-if="wkcState === 'loading'" class="inline-alert">
              <t-icon name="loading" class="inline-alert__icon spinning" />
              <span>{{ $t('settings.weknoraCloud.checkingStatus') }}</span>
            </div>
            <div v-else class="inline-alert inline-alert--warn">
              <t-icon name="error-circle-filled" class="inline-alert__icon" />
              <span class="inline-alert__text">
                <span v-if="wkcState === 'expired'">{{ $t('settings.weknoraCloud.credentialExpired') }}</span>
                <span v-else>{{ $t('settings.weknoraCloud.unconfigured') }}</span>
              </span>
              <a class="inline-alert__action" @click="goToWkcSettings">
                {{ $t('settings.weknoraCloud.goToSettings') }}
                <t-icon name="chevron-right" />
              </a>
            </div>
          </template>
        </section>

        <!-- Section 3 — mineru 自建配置 -->
        <section v-if="currentEngine.Name === 'mineru'" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.parser.configSection', '配置') }}</h4>

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
          <div class="form-item">
            <label class="form-label">vLLM {{ $t('settings.parser.serverUrl') }}</label>
            <t-input
              v-model="config.mineru_vlm_server_url"
              :placeholder="$t('settings.parser.vlmServerUrlPlaceholder')"
              clearable
            />
            <p class="form-desc">{{ $t('settings.parser.vlmServerUrlHint') }}</p>
          </div>
          <div class="form-item">
            <label class="form-label">{{ $t('settings.parser.featuresLabel', '识别选项') }}</label>
            <div class="form-toggles">
              <t-checkbox v-model="config.mineru_enable_formula">{{ $t('settings.parser.formulaRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_enable_table">{{ $t('settings.parser.tableRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_enable_ocr">OCR</t-checkbox>
            </div>
          </div>
          <div class="form-item">
            <label class="form-label">{{ t('settings.parser.language') }}</label>
            <t-input
              v-model="config.mineru_language"
              :placeholder="$t('settings.parser.languagePlaceholder')"
              clearable
            />
          </div>
        </section>

        <!-- Section 3 — mineru_cloud 云 API 配置 -->
        <section v-if="currentEngine.Name === 'mineru_cloud'" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.parser.configSection', '配置') }}</h4>

          <div class="form-item">
            <label class="form-label required">API Key</label>
            <t-input
              v-model="config.mineru_api_key"
              type="password"
              :placeholder="$t('settings.parser.mineruCloudApiKeyPlaceholder')"
              clearable
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </div>
          <div class="form-item">
            <label class="form-label">Model Version</label>
            <t-select v-model="config.mineru_cloud_model" :placeholder="$t('settings.parser.defaultPipeline')" clearable>
              <t-option value="pipeline" label="pipeline" />
              <t-option value="vlm" :label="$t('settings.parser.vlmLabel')" />
              <t-option value="MinerU-HTML" :label="$t('settings.parser.mineruHtmlLabel')" />
            </t-select>
          </div>
          <div class="form-item">
            <label class="form-label">{{ $t('settings.parser.featuresLabel', '识别选项') }}</label>
            <div class="form-toggles">
              <t-checkbox v-model="config.mineru_cloud_enable_formula">{{ $t('settings.parser.formulaRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_cloud_enable_table">{{ $t('settings.parser.tableRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.mineru_cloud_enable_ocr">OCR</t-checkbox>
            </div>
          </div>
          <div class="form-item">
            <label class="form-label">{{ t('settings.parser.language') }}</label>
            <t-input
              v-model="config.mineru_cloud_language"
              :placeholder="$t('settings.parser.languagePlaceholder')"
              clearable
            />
          </div>
        </section>

        <!-- Section 3 — paddleocr_vl 自建配置 -->
        <section v-if="currentEngine.Name === 'paddleocr_vl'" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.parser.configSection', '配置') }}</h4>

          <div class="form-item">
            <label class="form-label required">{{ t('settings.parser.selfHostedEndpoint') }}</label>
            <t-input
              v-model="config.paddleocr_vl_endpoint"
              :placeholder="$t('settings.parser.paddleocrVlEndpointPlaceholder')"
              clearable
            />
            <p class="form-desc">{{ $t('settings.parser.paddleocrVlEndpointHint') }}</p>
          </div>
          <div class="form-item">
            <label class="form-label">{{ $t('settings.parser.featuresLabel', '识别选项') }}</label>
            <div class="form-toggles">
              <t-checkbox v-model="config.paddleocr_vl_use_seal_recognition">{{ $t('settings.parser.sealRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.paddleocr_vl_use_chart_recognition">{{ $t('settings.parser.chartRecognition') }}</t-checkbox>
            </div>
          </div>
        </section>

        <!-- Section 3 — paddleocr_vl_cloud 云 API 配置 -->
        <section v-if="currentEngine.Name === 'paddleocr_vl_cloud'" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ $t('settings.parser.configSection', '配置') }}</h4>

          <div class="form-item">
            <label class="form-label required">Token</label>
            <t-input
              v-model="config.paddleocr_vl_cloud_token"
              type="password"
              :placeholder="$t('settings.parser.paddleocrVlCloudTokenPlaceholder')"
              clearable
            >
              <template #prefix-icon><t-icon name="lock-on" /></template>
            </t-input>
          </div>
          <div class="form-item">
            <label class="form-label">Model</label>
            <t-input
              v-model="config.paddleocr_vl_cloud_model"
              placeholder="PaddleOCR-VL-1.6"
              clearable
            />
          </div>
          <div class="form-item">
            <label class="form-label">{{ $t('settings.parser.featuresLabel', '识别选项') }}</label>
            <div class="form-toggles">
              <t-checkbox v-model="config.paddleocr_vl_cloud_use_seal_recognition">{{ $t('settings.parser.sealRecognition') }}</t-checkbox>
              <t-checkbox v-model="config.paddleocr_vl_cloud_use_chart_recognition">{{ $t('settings.parser.chartRecognition') }}</t-checkbox>
            </div>
          </div>
        </section>
      </div>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
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
const authStore = useAuthStore()

const CONFIGURABLE_ENGINES = new Set(['mineru', 'mineru_cloud', 'paddleocr_vl', 'paddleocr_vl_cloud'])

/** 各解析引擎的项目/官方文档地址 */
const ENGINE_DOC_LINKS: Record<string, string> = {
  weknoracloud: 'https://developers.weixin.qq.com/doc/aispeech/knowledge/atomic_capability/atomic_interface.html',
  markitdown: 'https://github.com/microsoft/markitdown',
  mineru: 'https://github.com/opendatalab/MinerU',
  mineru_cloud: 'https://mineru.net/apiManage/docs',
  paddleocr_vl: 'https://github.com/PaddlePaddle/PaddleOCR',
  paddleocr_vl_cloud: 'https://aistudio.baidu.com/paddleocr',
}

/** 解析引擎配置默认值（与 DocReader/Python 侧一致） */
const DEFAULT_PARSER_CONFIG: ParserEngineConfig = {
  docreader_addr: '',
  docreader_transport: 'grpc',
  mineru_endpoint: '',
  mineru_api_key: '',
  mineru_model: 'pipeline',
  mineru_vlm_server_url: '',
  mineru_enable_formula: true,
  mineru_enable_table: true,
  mineru_enable_ocr: true,
  mineru_language: 'ch',
  mineru_cloud_model: 'pipeline',
  mineru_cloud_enable_formula: true,
  mineru_cloud_enable_table: true,
  mineru_cloud_enable_ocr: true,
  mineru_cloud_language: 'ch',
  paddleocr_vl_endpoint: '',
  paddleocr_vl_use_seal_recognition: true,
  paddleocr_vl_use_chart_recognition: false,
  paddleocr_vl_cloud_token: '',
  paddleocr_vl_cloud_model: 'PaddleOCR-VL-1.6',
  paddleocr_vl_cloud_use_seal_recognition: true,
  paddleocr_vl_cloud_use_chart_recognition: false,
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

// SettingDrawer 头部图标走 #headerIcon 槽（首字母 monogram + per-engine
// 配色，与列表卡片完全一致），不再需要 t-icon name 兜底。

// Whether the footer test-connection button should appear. Engines without
// configurable fields and that aren't the builtin DocReader (whose connection
// status is the whole point of the drawer) skip the test affordance — for
// e.g. simple/markitdown there's nothing to validate beyond presence.
const needsTestButton = computed(() => {
  if (!currentEngine.value) return false
  return hasConfigFields(currentEngine.value.Name) || currentEngine.value.Name === 'builtin'
})

/** 固定展示顺序，未列出的引擎排在末尾按名称排序 */
const ENGINE_ORDER: Record<string, number> = {
  builtin: 0,
  weknoracloud: 1,
  simple: 2,
  markitdown: 3,
  mineru: 4,
  mineru_cloud: 5,
  paddleocr_vl: 6,
  paddleocr_vl_cloud: 7,
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

// 卡片徽章首字母。优先用本地化名称的首字符（覆盖如「内置/简易」等中文场景），
// 兜底回到 engine name；保证英文/中文都能显示一个稳定的可读 monogram。
function engineInitial(engineName: string): string {
  const display = getEngineDisplayName(engineName)
  return (display.trim().charAt(0) || engineName.charAt(0) || '?').toUpperCase()
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
      mineru_vlm_server_url: data?.mineru_vlm_server_url ?? DEFAULT_PARSER_CONFIG.mineru_vlm_server_url ?? '',
      mineru_enable_formula: data?.mineru_enable_formula ?? DEFAULT_PARSER_CONFIG.mineru_enable_formula ?? true,
      mineru_enable_table: data?.mineru_enable_table ?? DEFAULT_PARSER_CONFIG.mineru_enable_table ?? true,
      mineru_enable_ocr: data?.mineru_enable_ocr ?? DEFAULT_PARSER_CONFIG.mineru_enable_ocr ?? true,
      mineru_language: data?.mineru_language ?? DEFAULT_PARSER_CONFIG.mineru_language ?? 'ch',
      mineru_cloud_model: data?.mineru_cloud_model ?? DEFAULT_PARSER_CONFIG.mineru_cloud_model ?? '',
      mineru_cloud_enable_formula: data?.mineru_cloud_enable_formula ?? DEFAULT_PARSER_CONFIG.mineru_cloud_enable_formula ?? true,
      mineru_cloud_enable_table: data?.mineru_cloud_enable_table ?? DEFAULT_PARSER_CONFIG.mineru_cloud_enable_table ?? true,
      mineru_cloud_enable_ocr: data?.mineru_cloud_enable_ocr ?? DEFAULT_PARSER_CONFIG.mineru_cloud_enable_ocr ?? true,
      mineru_cloud_language: data?.mineru_cloud_language ?? DEFAULT_PARSER_CONFIG.mineru_cloud_language ?? 'ch',
      paddleocr_vl_endpoint: data?.paddleocr_vl_endpoint ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_endpoint ?? '',
      paddleocr_vl_use_seal_recognition: data?.paddleocr_vl_use_seal_recognition ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_use_seal_recognition ?? true,
      paddleocr_vl_use_chart_recognition: data?.paddleocr_vl_use_chart_recognition ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_use_chart_recognition ?? false,
      paddleocr_vl_cloud_token: data?.paddleocr_vl_cloud_token ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_cloud_token ?? '',
      paddleocr_vl_cloud_model: data?.paddleocr_vl_cloud_model ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_cloud_model ?? 'PaddleOCR-VL-1.6',
      paddleocr_vl_cloud_use_seal_recognition: data?.paddleocr_vl_cloud_use_seal_recognition ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_cloud_use_seal_recognition ?? true,
      paddleocr_vl_cloud_use_chart_recognition: data?.paddleocr_vl_cloud_use_chart_recognition ?? DEFAULT_PARSER_CONFIG.paddleocr_vl_cloud_use_chart_recognition ?? false,
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
    mineru_vlm_server_url: config.value.mineru_vlm_server_url?.trim() ?? '',
    mineru_enable_formula: config.value.mineru_enable_formula,
    mineru_enable_table: config.value.mineru_enable_table,
    mineru_enable_ocr: config.value.mineru_enable_ocr,
    mineru_language: config.value.mineru_language?.trim() ?? '',
    mineru_cloud_model: config.value.mineru_cloud_model?.trim() ?? '',
    mineru_cloud_enable_formula: config.value.mineru_cloud_enable_formula,
    mineru_cloud_enable_table: config.value.mineru_cloud_enable_table,
    mineru_cloud_enable_ocr: config.value.mineru_cloud_enable_ocr,
    mineru_cloud_language: config.value.mineru_cloud_language?.trim() ?? '',
    paddleocr_vl_endpoint: config.value.paddleocr_vl_endpoint?.trim() ?? '',
    paddleocr_vl_use_seal_recognition: config.value.paddleocr_vl_use_seal_recognition,
    paddleocr_vl_use_chart_recognition: config.value.paddleocr_vl_use_chart_recognition,
    paddleocr_vl_cloud_token: config.value.paddleocr_vl_cloud_token?.trim() ?? '',
    paddleocr_vl_cloud_model: config.value.paddleocr_vl_cloud_model?.trim() ?? '',
    paddleocr_vl_cloud_use_seal_recognition: config.value.paddleocr_vl_cloud_use_seal_recognition,
    paddleocr_vl_cloud_use_chart_recognition: config.value.paddleocr_vl_cloud_use_chart_recognition,
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
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
  margin-top: 24px;
}

// 与 ModelSettings / WebSearchSettings / McpSettings 同形的提供者卡片。
// 这里整张卡是一个 button —— 单击即打开配置抽屉；active 状态用品牌色描边。
.engine-card {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 14px 14px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  text-align: left;
  font: inherit;
  color: inherit;
  cursor: pointer;
  transition: border-color 0.18s ease, box-shadow 0.18s ease, background-color 0.18s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }

  &--active {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-1, rgba(7, 192, 95, 0.06));
  }
}

.engine-card__badge {
  flex-shrink: 0;
  width: 36px;
  height: 36px;
  border-radius: 9px;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-top: 1px;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.02em;
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}

// 解析引擎徽章配色 —— 内置/官方系绿，外部工具按性质各取一色。
.engine-card--builtin .engine-card__badge,
.engine-card--weknoracloud .engine-card__badge {
  background: rgba(7, 192, 95, 0.12);
  color: #07C05F;
}
.engine-card--simple .engine-card__badge {
  background: rgba(70, 70, 70, 0.1);
  color: #464646;
}
.engine-card--markitdown .engine-card__badge {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.engine-card--mineru .engine-card__badge,
.engine-card--mineru_cloud .engine-card__badge,
.engine-card--paddleocr_vl .engine-card__badge,
.engine-card--paddleocr_vl_cloud .engine-card__badge {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}

.engine-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.engine-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.engine-card__title {
  flex: 1;
  min-width: 0;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

// 与 McpSettings 一致的 dot+文字状态徽章。on=绿、err=红、help 用 cursor:help 提示。
.engine-card__status {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 1px 8px 1px 6px;
  font-size: 11px;
  font-weight: 500;
  line-height: 16px;
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);

  &--on {
    color: var(--td-success-color-7, #118053);

    .engine-card__status-dot { background: var(--td-success-color, #118053); }
  }

  &--err {
    color: var(--td-error-color-7, #C93E3E);

    .engine-card__status-dot { background: var(--td-error-color, #C93E3E); }
  }

  &--help {
    cursor: help;
  }
}

.engine-card__status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.engine-card__desc {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  margin: 0;
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

// ---- 抽屉内容 — 与 ModelEditorDialog 同款约定 ----
// .form-item / .form-label / .form-desc / .weknoracloud-hint / .api-test
// 参照 frontend/src/components/ModelEditorDialog.vue 的命名与字号/间距
.form-item {
  margin-bottom: 0;
}

.form-label {
  display: block;
  margin-bottom: 6px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  line-height: 1.4;

  // 与 ModelEditorDialog 一致：必填星号前置
  &.required::before {
    content: '*';
    color: var(--td-error-color);
    margin-right: 4px;
    font-weight: 500;
    line-height: 1;
  }
}

.form-desc {
  margin: 4px 0 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-placeholder);
}

// 输入框统一字号
:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea),
:deep(.t-input-number) {
  width: 100%;
  font-size: 13px;
}

:deep(.t-checkbox) {
  font-size: 13px;

  .t-checkbox__label {
    font-size: 13px;
    color: var(--td-text-color-primary);
  }
}

// ---- DocReader 连接信息（builtin 引擎） ----
.docreader-block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  background: var(--td-bg-color-container-hover);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;

  .form-desc {
    margin-top: 0;
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
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
}

// ---- 文件类型 chip ----
.file-types {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.file-type-chip {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 8px;
  font-size: 11px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-component);
  border-radius: 4px;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  letter-spacing: 0.02em;
}

// ---- Inline alert（替代之前的 .weknoracloud-hint 卡片） ----
// 一行内表达 "状态信号 + 一句话 + 跳转 link"，无外框/无 3px 左边，
// 视觉重量与一行文字相当，section 内不会再被一个独立卡片打断。
.inline-alert {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  flex-wrap: wrap;
}

.inline-alert__icon {
  font-size: 15px;
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
}

.inline-alert--ok .inline-alert__icon {
  color: var(--td-success-color);
}

.inline-alert--warn {
  color: var(--td-text-color-primary);

  .inline-alert__icon {
    color: var(--td-warning-color, #f97316);
  }
}

.inline-alert__text {
  flex: 1 1 auto;
  min-width: 0;
}

// 行尾 link：跟普通 doc-link 一致的主题色，但更紧凑，行内排版
.inline-alert__action {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-brand-color);
  cursor: pointer;
  white-space: nowrap;
  transition: color 0.15s ease;

  &:hover {
    color: var(--td-brand-color-active);
  }

  .t-icon {
    font-size: 14px;
  }
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.spinning {
  animation: spin 1s linear infinite;
}

// ---- 表单切换组（公式/表格/OCR） ----
.form-toggles {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  padding: 8px 0 0;
}

// ---- footer-left 测试连接消息（与 ModelEditorDialog 同款） ----
.footer-test-message {
  font-size: 12px;
  line-height: 1.4;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;

  &.success {
    color: var(--td-brand-color-active);
  }

  &.error {
    color: var(--td-error-color);
  }
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

// ---- 文档外链 ----
.doc-link {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 13px;
  font-weight: 500;
  color: var(--td-brand-color);
  text-decoration: none;
  transition: color 0.15s ease;

  &:hover {
    color: var(--td-brand-color-active);
  }

  .link-icon {
    font-size: 14px;
  }

  // 副标题里的 inline 文档链接：与描述文字平铺一行，体量等同小字
  &--inline {
    margin-left: 6px;
    font-size: 12px;
    font-weight: 500;
    vertical-align: baseline;

    .link-icon {
      font-size: 12px;
    }
  }
}

// ---- Header 图标的首字母 monogram（per-engine 配色见非 scoped 块）----
.header-icon__text {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.02em;
}
</style>

<!--
  Non-scoped block: per-engine header-icon coloring. Same approach as
  StorageEngineSettings — keep these rules global so they always apply
  regardless of whether the drawer panel inherits the parent's scoped
  data attributes. Each rule mirrors the matching .engine-card--{name}
  .engine-card__badge from the scoped block above.
-->
<style lang="less">
.parser-engine-drawer--builtin .setting-drawer__header-icon,
.parser-engine-drawer--weknoracloud .setting-drawer__header-icon {
  background: rgba(7, 192, 95, 0.12);
  color: #07C05F;
}
.parser-engine-drawer--simple .setting-drawer__header-icon {
  background: rgba(70, 70, 70, 0.1);
  color: #464646;
}
.parser-engine-drawer--markitdown .setting-drawer__header-icon {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.parser-engine-drawer--mineru .setting-drawer__header-icon,
.parser-engine-drawer--mineru_cloud .setting-drawer__header-icon,
.parser-engine-drawer--paddleocr_vl .setting-drawer__header-icon,
.parser-engine-drawer--paddleocr_vl_cloud .setting-drawer__header-icon {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
</style>
