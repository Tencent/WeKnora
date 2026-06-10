<template>
  <div class="vectorstore-settings">
    <div class="section-header">
      <h2>{{ t('vectorStoreSettings.title') }}</h2>
      <p class="section-description">{{ t('vectorStoreSettings.description') }}</p>
    </div>

    <!-- Loading -->
    <div v-if="loading" class="loading-container">
      <t-loading size="small" />
    </div>

    <template v-else>
      <div class="settings-group">
        <h3 class="list-section-title">{{ t('vectorStoreSettings.storesTitle') }}</h3>

        <!-- 与其它 settings 列表同形：左侧 engine 徽章 + 标题 + env pill + 副标题 + 测试动作。
             env 来源是只读的 (engine_type / connection_config 由 .env 写入），所以没有更多菜单；
             user 来源沿用三点菜单的编辑 / 删除入口；测试结果作为卡片底部的彩色条出现。 -->
        <div v-if="stores.length > 0" class="store-grid">
          <div
            v-for="store in [...envStores, ...userStores]"
            :key="store.id"
            class="store-card"
            :class="[
              `store-card--${store.engine_type}`,
              {
                'store-card--env': store.source === 'env',
                'store-card--clickable': isStoreCardClickable(store),
              },
            ]"
            :role="isStoreCardClickable(store) ? 'button' : undefined"
            :tabindex="isStoreCardClickable(store) ? 0 : undefined"
            @click="onStoreCardClick($event, store)"
            @keydown.enter="onStoreCardClick($event, store)"
          >
            <div class="store-card__main">
              <div
                class="store-card__badge"
                :class="badgeClass(store.engine_type)"
                :style="badgeStyle(store.engine_type)"
                :aria-label="store.engine_type"
              >
                <img
                  v-if="resolveLogo(store.engine_type)?.mode === 'color'"
                  :src="resolveLogo(store.engine_type)!.url"
                  :alt="store.engine_type"
                  class="store-card__badge-img"
                />
                <template v-else-if="!resolveLogo(store.engine_type)">{{ engineInitial(store.engine_type) }}</template>
              </div>
              <div class="store-card__body">
                <div class="store-card__header">
                  <h3 class="store-card__title" :title="store.name">{{ store.name }}</h3>
                  <span v-if="store.source === 'env'" class="store-card__pill">
                    {{ t('vectorStoreSettings.envTag') }}
                  </span>
                  <!--
                    测试连接已挪到编辑抽屉的 footer，外层菜单不再有"测试"入口。
                    env 来源（.env 写入）也不需要 dropdown — 没有可执行的动作。
                  -->
                  <div
                    v-if="authStore.hasRole('admin') && storeActionsFor(store).length > 0"
                    class="store-card__actions"
                    @click.stop
                  >
                    <t-dropdown
                      :options="storeActionsFor(store)"
                      placement="bottom-right"
                      attach="body"
                      trigger="click"
                      @click="(action: any) => handleAction(action, store)"
                    >
                      <t-button variant="text" shape="square" size="small" class="store-card__more">
                        <t-icon name="ellipsis" />
                      </t-button>
                    </t-dropdown>
                  </div>
                </div>
                <div class="store-card__subtitle">
                  <span class="store-card__type">{{ store.engine_type }}</span>
                  <template v-if="getStoreEndpoint(store)">
                    <span class="store-card__sep">·</span>
                    <span class="store-card__endpoint" :title="getStoreEndpoint(store)">{{ getStoreEndpoint(store) }}</span>
                  </template>
                </div>
              </div>
            </div>
          </div>
          <button
            v-if="authStore.hasRole('admin')"
            type="button"
            class="store-card store-card--add"
            @click="openAddDialog"
          >
            <span class="store-card--add__icon" aria-hidden="true">
              <add-icon />
            </span>
            <span class="store-card--add__label">{{ t('vectorStoreSettings.addStore') }}</span>
          </button>
        </div>

        <!-- Empty State -->
        <div v-else class="empty-stores">
          <t-empty :description="t('vectorStoreSettings.emptyDesc')">
            <t-button v-if="authStore.hasRole('admin')" theme="primary" variant="outline" size="small"
              @click="openAddDialog">
              <template #icon><add-icon /></template>
              {{ t('vectorStoreSettings.addStore') }}
            </t-button>
          </t-empty>
        </div>
      </div>
    </template>

    <!-- Add/Edit Drawer — 与 ModelEditorDialog/Storage/Parser/WebSearch 同款 -->
    <SettingDrawer
      v-model:visible="showDialog"
      :title="editingStore ? t('vectorStoreSettings.editStore') : t('vectorStoreSettings.addStore')"
      :class="drawerClass"
      :confirm-loading="saving"
      @confirm="onDrawerConfirm"
      @cancel="showDialog = false"
    >
      <!--
        Header icon — 与列表 .store-card__badge 同款 logo/mono/fallback。
        per-engine 配色由非 scoped 块的 .vectorstore-drawer--{engine} 注入。
      -->
      <template v-if="form.engine_type" #headerIcon>
        <img
          v-if="drawerLogo?.mode === 'color'"
          :src="drawerLogo.url"
          :alt="form.engine_type"
          class="header-icon__img"
        />
        <span
          v-else-if="drawerLogo?.mode === 'mono'"
          class="header-icon__mono"
          :style="drawerLogoStyle"
        />
        <span v-else class="header-icon__text">{{ engineInitial(form.engine_type) }}</span>
      </template>

      <!-- 副标题：engine display_name -->
      <template v-if="selectedType" #subtitle>
        <span>{{ selectedType.display_name || form.engine_type }}</span>
      </template>

      <!--
        Test connection (footer-left). create 模式：实时验证当前表单的连接信息；
        edit 模式：用存储的连接配置（连接配置在编辑模式不可改 — engine 是 immutable）。
        始终显示按钮，由 canTestConnection 控制 disabled。
      -->
      <template #footer-left>
        <t-button
          variant="outline"
          :loading="testing"
          :disabled="!canTestConnection"
          @click="onDrawerTest"
        >
          <template #icon>
            <t-icon
              v-if="!testing && lastTestOk === true"
              name="check-circle-filled"
              class="status-icon available"
            />
            <t-icon
              v-else-if="!testing && lastTestOk === false"
              name="close-circle-filled"
              class="status-icon unavailable"
            />
          </template>
          {{ testing ? t('vectorStoreSettings.testing') : t('vectorStoreSettings.testConnection') }}
        </t-button>
      </template>

      <t-form ref="formRef" :data="form" :rules="formRules" label-align="top" class="store-form">
        <!--
          Edit 模式特殊提示：engine_type / connection_config / index_config
          创建后不可改，仅 name 可编辑。用 inline-alert 而不是大块 banner，
          视觉与其他抽屉的提示一致。
        -->
        <section v-if="editingStore" class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">{{ t('vectorStoreSettings.basicSection', '基本信息') }}</h4>

          <div class="inline-alert inline-alert--info">
            <t-icon name="info-circle-filled" class="inline-alert__icon" />
            <span class="inline-alert__text">{{ t('vectorStoreSettings.immutableNotice') }}</span>
          </div>

          <div class="form-item">
            <label class="form-label required">{{ t('vectorStoreSettings.nameLabel') }}</label>
            <t-input v-model="form.name" :placeholder="t('vectorStoreSettings.namePlaceholder')" />
          </div>

          <!-- 只读字段以 inline list 展示（轻量 readonly 行） -->
          <div class="readonly-fields">
            <div class="readonly-row">
              <span class="readonly-label">{{ t('vectorStoreSettings.engineTypeLabel') }}</span>
              <span class="readonly-value">{{ selectedType?.display_name || editingStore.engine_type }}</span>
            </div>
            <template v-if="selectedType">
              <template v-for="field in selectedType.connection_fields" :key="field.name">
                <div v-if="field.sensitive || form.connection_config[field.name]" class="readonly-row">
                  <span class="readonly-label">{{ fieldLabel(field.name) }}</span>
                  <span class="readonly-value">
                    {{ field.sensitive ? '********' : form.connection_config[field.name] }}
                  </span>
                </div>
              </template>
            </template>
            <template v-if="selectedType?.index_fields?.length">
              <template v-for="field in selectedType.index_fields" :key="field.name">
                <div v-if="form.index_config[field.name]" class="readonly-row">
                  <span class="readonly-label">{{ fieldLabel(field.name) }}</span>
                  <span class="readonly-value">{{ form.index_config[field.name] }}</span>
                </div>
              </template>
            </template>
          </div>
        </section>

        <!-- Create 模式：基本信息 + 连接配置 + 高级索引 三段 -->
        <template v-else>
          <!-- Section 1 — 基本信息：engine 类型 + 名称 -->
          <section class="setting-drawer__section">
            <h4 class="setting-drawer__section-title">{{ t('vectorStoreSettings.basicSection', '基本信息') }}</h4>

            <div class="form-item">
              <label class="form-label required">{{ t('vectorStoreSettings.engineTypeLabel') }}</label>
              <t-select v-model="form.engine_type" @change="onEngineTypeChange">
                <t-option
                  v-for="st in storeTypes"
                  :key="st.type"
                  :value="st.type"
                  :label="st.display_name"
                />
              </t-select>
            </div>

            <div class="form-item">
              <label class="form-label required">{{ t('vectorStoreSettings.nameLabel') }}</label>
              <t-input v-model="form.name" :placeholder="t('vectorStoreSettings.namePlaceholder')" />
            </div>
          </section>

          <!-- Section 2 — 连接配置（engine type 决定具体字段） -->
          <section v-if="selectedType" class="setting-drawer__section">
            <h4 class="setting-drawer__section-title">{{ t('vectorStoreSettings.connectionInfo') }}</h4>

            <div
              v-for="field in selectedType.connection_fields"
              :key="field.name"
              class="form-item"
            >
              <label
                class="form-label"
                :class="{ required: field.required }"
              >{{ fieldLabel(field.name) }}</label>

              <!-- boolean 字段：switch + 行内描述 / TLS 警告 -->
              <template v-if="field.type === 'boolean'">
                <div class="vision-toggle">
                  <t-switch v-model="form.connection_config[field.name]" />
                </div>
                <p
                  v-if="field.name === 'insecure_skip_verify' && form.connection_config[field.name]"
                  class="form-desc form-desc--warn"
                >
                  {{ t('vectorStoreSettings.insecureSkipVerifyWarning') }}
                </p>
              </template>

              <!-- 敏感字段（password / api key 等）：lock prefix + password -->
              <t-input
                v-else-if="field.type === 'string' && field.sensitive"
                v-model="form.connection_config[field.name]"
                type="password"
                placeholder="********"
              >
                <template #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>

              <!-- 数字字段：用 t-input + type=number，与 MCP 高级配置同款；无单位提示 -->
              <t-input
                v-else-if="field.type === 'number'"
                v-model="connectionNumberTextProxy[field.name].value"
                type="number"
                :placeholder="field.default != null ? String(field.default) : ' '"
                class="number-input"
              />

              <!-- 普通字符串 -->
              <t-input
                v-else
                v-model="form.connection_config[field.name]"
                :placeholder="field.default?.toString() || ''"
              />
            </div>
          </section>

          <!-- Section 3 — 高级索引（仅 selectedType 有 index_fields 时显示） -->
          <section v-if="selectedType?.index_fields?.length" class="setting-drawer__section">
            <h4 class="setting-drawer__section-title">{{ t('vectorStoreSettings.advancedIndexConfig') }}</h4>

            <!-- 折叠/展开开关：保留之前的可选展示行为，但样式更轻量 -->
            <button
              type="button"
              class="advanced-toggle"
              @click="showAdvanced = !showAdvanced"
            >
              <t-icon :name="showAdvanced ? 'chevron-down' : 'chevron-right'" />
              <span>{{ showAdvanced ? t('common.collapse', '收起') : t('common.expand', '展开') }}</span>
            </button>

            <template v-if="showAdvanced">
              <div
                v-for="field in selectedType.index_fields"
                :key="field.name"
                class="form-item"
              >
                <label class="form-label">{{ fieldLabel(field.name) }}</label>

                <!-- 枚举 → 下拉 -->
                <t-select
                  v-if="field.enum && field.enum.length"
                  v-model="form.index_config[field.name]"
                  :placeholder="field.default?.toString() || ''"
                >
                  <t-option v-for="opt in field.enum" :key="opt" :value="opt" :label="opt" />
                </t-select>

                <!-- 数字 → number input -->
                <t-input
                  v-else-if="field.type === 'number'"
                  v-model="indexNumberTextProxy[field.name].value"
                  type="number"
                  :placeholder="field.default?.toString()"
                  :min="field.min ?? 1"
                  :max="field.max ?? (isReplicaField(field.name) ? 10 : 64)"
                  class="number-input"
                />

                <!-- 字符串 -->
                <t-input
                  v-else
                  v-model="form.index_config[field.name]"
                  :placeholder="field.default?.toString() || ''"
                  :maxlength="128"
                />
              </div>
            </template>
          </section>
        </template>
      </t-form>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch, type WritableComputedRef } from 'vue'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { AddIcon } from 'tdesign-icons-vue-next'
import {
  listVectorStores,
  listVectorStoreTypes,
  createVectorStore,
  updateVectorStore,
  deleteVectorStore as deleteVectorStoreAPI,
  testVectorStoreRaw,
  type VectorStoreEntity,
  type VectorStoreTypeInfo,
} from '@/api/vector-store'
import { useAuthStore } from '@/stores/auth'
import { providerLogo } from './providerLogos'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'

const { t } = useI18n()
const authStore = useAuthStore()

// ===== State =====
const stores = ref<VectorStoreEntity[]>([])
const storeTypes = ref<VectorStoreTypeInfo[]>([])
const loading = ref(false)
const showDialog = ref(false)
const editingStore = ref<VectorStoreEntity | null>(null)
const testing = ref(false)
const saving = ref(false)
const showAdvanced = ref(false)
const formRef = ref<any>()

const form = ref<{
  name: string
  engine_type: string
  connection_config: Record<string, any>
  index_config: Record<string, any>
}>({
  name: '',
  engine_type: '',
  connection_config: {},
  index_config: {},
})

// Tri-state hint icon next to the test button: null=neutral, true=just
// succeeded, false=just failed. Cleared when the user changes any
// connection-relevant field so a stale ✓/✗ doesn't follow a config the
// user is still editing.
const lastTestOk = ref<boolean | null>(null)

watch(
  () => [form.value.engine_type, form.value.connection_config],
  () => { lastTestOk.value = null },
  { deep: true },
)

// ===== Computed =====
const envStores = computed(() => stores.value.filter(s => s.source === 'env'))
const userStores = computed(() => stores.value.filter(s => s.source === 'user'))
const selectedType = computed(() => storeTypes.value.find(st => st.type === form.value.engine_type))

// Drawer header logo — 与列表 .store-card__badge 同源（providerLogo()），让
// 列表卡 → 抽屉 hand-off 视觉连贯。
const drawerLogo = computed(() => {
  if (!form.value.engine_type) return null
  return providerLogo('vectorstore', form.value.engine_type)
})

const drawerLogoStyle = computed((): Record<string, string> => {
  const logo = drawerLogo.value
  if (!logo || logo.mode !== 'mono') return {}
  return { '--logo-url': `url("${logo.url}")` }
})

// per-engine class on drawer for non-scoped header-icon coloring rules.
const drawerClass = computed(() => {
  return form.value.engine_type
    ? `vectorstore-drawer vectorstore-drawer--${form.value.engine_type}`
    : 'vectorstore-drawer'
})

// 测试连接是否可点。create 模式：必须填全所有 required 连接字段；
// edit 模式：engine 不可改、连接配置只读，禁用测试（要重新建条目，不在抽屉里测）。
const canTestConnection = computed(() => {
  if (editingStore.value) return false
  const st = selectedType.value
  if (!st) return false
  for (const f of st.connection_fields) {
    if (!f.required) continue
    const v = form.value.connection_config[f.name]
    if (v == null || v === '' || (typeof v === 'string' && v.trim() === '')) return false
  }
  return true
})

// Per-store dropdown options. env 来源由 .env 写入，UI 不允许 edit / delete；
// 测试连接已挪到编辑抽屉的 footer，外层菜单不再露出"测试"项。env 来源没有
// 编辑/删除入口 → 整个 dropdown 都不需要展示。
const storeActionsFor = (store: VectorStoreEntity) => {
  if (store.source === 'env') return []
  return [
    { content: t('common.edit'), value: 'edit' },
    { content: t('common.delete'), value: 'delete', theme: 'error' as const },
  ]
}

const formRules = computed(() => {
  const rules: Record<string, any[]> = {
    name: [{ required: true, message: t('vectorStoreSettings.validation.nameRequired') }],
  }
  if (!editingStore.value) {
    rules.engine_type = [{ required: true, message: t('vectorStoreSettings.validation.engineTypeRequired') }]
    if (selectedType.value) {
      for (const field of selectedType.value.connection_fields) {
        if (field.required) {
          rules[`connection_config.${field.name}`] = [
            { required: true, message: t('vectorStoreSettings.validation.fieldRequired', { field: fieldLabel(field.name) }) },
          ]
        }
      }
      // Index name/collection string fields: pattern validation (optional — empty is allowed)
      for (const field of (selectedType.value.index_fields || [])) {
        if (field.type === 'string') {
          rules[`index_config.${field.name}`] = [
            {
              validator: (val: string) => !val || indexNamePattern.test(val),
              message: t('vectorStoreSettings.validation.indexNamePattern'),
              trigger: 'blur',
            },
          ]
        }
      }
    }
  }
  return rules
})

// Index/collection name pattern: must start with letter, alphanumeric + _ + - only, max 128
const indexNamePattern = /^[a-zA-Z][a-zA-Z0-9_-]{0,127}$/

// ===== Methods =====
const fieldLabel = (name: string): string => {
  const key = `vectorStoreSettings.fields.${name}`
  const translated = t(key)
  // If i18n key not found, vue-i18n returns the key itself — fall back to field name
  return translated === key ? name : translated
}

// Distinguish replica fields (max 10) from shard fields (max 64) for input bounds
const replicaFieldNames = ['number_of_replicas', 'replication_factor', 'replica_number']
const isReplicaField = (name: string): boolean => replicaFieldNames.includes(name)

const getStoreEndpoint = (store: VectorStoreEntity): string => {
  const cc = store.connection_config || {}
  return cc.addr || cc.host || ''
}

// 卡片徽章首字母。engine_type 都是英文 ASCII，直接 charAt。
const engineInitial = (engineType: string): string => {
  return (engineType || '?').charAt(0).toUpperCase()
}

// 当 engine 有 logo 资源时，把 SVG URL 透传给 CSS（::before 用 mask-image
// 渲染），并把卡片底色切回中性白；没有 logo 时返回空对象，沿用每个 engine
// 的品牌色 monogram 样式。color 模式不需要 mask 染色，所以 url 不上报。
const resolveLogo = (engineType: string) => providerLogo('vectorstore', engineType)

const badgeClass = (engineType: string) => {
  const m = resolveLogo(engineType)?.mode
  return {
    'store-card__badge--logo': !!m,
    'store-card__badge--color': m === 'color',
    'store-card__badge--mono': m === 'mono',
  }
}

const badgeStyle = (engineType: string): Record<string, string> => {
  const logo = resolveLogo(engineType)
  return logo?.mode === 'mono' ? { '--logo-url': `url("${logo.url}")` } : {}
}

const onEngineTypeChange = () => {
  form.value.connection_config = {}
  form.value.index_config = {}
  showAdvanced.value = false
  // Drop cached number-text proxies so a switch to a different engine
  // doesn't keep stale entries pointing at the old field set.
  for (const k of Object.keys(connectionNumberText)) delete connectionNumberText[k]
  for (const k of Object.keys(indexNumberText)) delete indexNumberText[k]
}

// ---- Number-input text proxies (lazy per field name) ----
// type=number 输入会因为 v-model 把空字符串 coerce 成 0 / NaN，导致
// "用户清空 → 自动塞回 0" 的烦躁交互。我们用 WritableComputedRef 包一层：
// 读取时把数字转成字符串展示；写入时空串 → 删除字段（让 placeholder 显示
// 出来），非空 → 转 int。Proxy 按字段名按需创建并缓存，避免重复 computed。
const connectionNumberText: Record<string, WritableComputedRef<string>> = {}
const indexNumberText: Record<string, WritableComputedRef<string>> = {}

function ensureNumberProxy(
  bag: Record<string, WritableComputedRef<string>>,
  store: Record<string, any>,
  key: string,
): WritableComputedRef<string> {
  if (bag[key]) return bag[key]
  bag[key] = computed<string>({
    get: () => {
      const v = store[key]
      return v == null || v === '' ? '' : String(v)
    },
    set: (raw: string) => {
      const s = String(raw ?? '').trim()
      if (!s) {
        delete store[key]
        return
      }
      const n = Number(s)
      store[key] = Number.isFinite(n) ? n : s
    },
  })
  return bag[key]
}

// Vue templates can't call ensureNumberProxy on every render without the
// keys multiplying — wrap in a Proxy so `connectionNumberText[name].value`
// from the template lazily creates the proxy on first read.
const connectionNumberTextProxy = new Proxy(connectionNumberText, {
  get: (target, name: string) => ensureNumberProxy(target, form.value.connection_config, name),
})
const indexNumberTextProxy = new Proxy(indexNumberText, {
  get: (target, name: string) => ensureNumberProxy(target, form.value.index_config, name),
})

const loadStores = async () => {
  try {
    const response = await listVectorStores()
    if (response.data && Array.isArray(response.data)) {
      stores.value = response.data
    }
  } catch (error) {
    console.error('Failed to load vector stores:', error)
  }
}

const loadStoreTypes = async () => {
  try {
    storeTypes.value = await listVectorStoreTypes()
  } catch (error) {
    console.error('Failed to load vector store types:', error)
  }
}

const openAddDialog = () => {
  editingStore.value = null
  showAdvanced.value = false
  form.value = {
    name: '',
    engine_type: storeTypes.value[0]?.type || '',
    connection_config: {},
    index_config: {},
  }
  lastTestOk.value = null
  showDialog.value = true
}

// env 来源由 .env 注入，与列表菜单一致：不可点击编辑
const isStoreCardClickable = (store: VectorStoreEntity) =>
  authStore.hasRole('admin') && store.source !== 'env'

const onStoreCardClick = (event: Event, store: VectorStoreEntity) => {
  if (!isStoreCardClickable(store)) return
  if (event.type === 'keydown') {
    const ke = event as KeyboardEvent
    if (ke.key !== 'Enter' && ke.key !== ' ') return
    ke.preventDefault()
  }
  const target = event.target as HTMLElement | null
  if (target?.closest('.store-card__actions')) return
  editStore(store)
}

const editStore = (store: VectorStoreEntity) => {
  if (store.source === 'env') {
    return
  }
  editingStore.value = store
  showAdvanced.value = false
  form.value = {
    name: store.name,
    engine_type: store.engine_type,
    connection_config: { ...store.connection_config },
    index_config: { ...store.index_config },
  }
  lastTestOk.value = null
  showDialog.value = true
}

// SettingDrawer 的"保存"按钮触发：手动校验后写后端。
// edit 模式只能改 name；create 模式提交完整 connection / index 配置。
const onDrawerConfirm = async () => {
  const result = await formRef.value?.validate()
  if (result !== true && result !== undefined) {
    // 取第一条错误展示
    const firstError =
      typeof result === 'object'
        ? Object.values(result).map((errs: any) => Array.isArray(errs) ? errs[0]?.message : '').find(Boolean)
        : ''
    MessagePlugin.warning(firstError || (t('vectorStoreSettings.toasts.errorGeneric') as string))
    return
  }

  saving.value = true
  try {
    if (editingStore.value) {
      await updateVectorStore(editingStore.value.id!, { name: form.value.name.trim() })
      MessagePlugin.success(t('vectorStoreSettings.toasts.storeUpdated'))
    } else {
      const data: Partial<VectorStoreEntity> = {
        name: form.value.name.trim(),
        engine_type: form.value.engine_type,
        connection_config: { ...form.value.connection_config },
        index_config: showAdvanced.value ? { ...form.value.index_config } : {},
      }
      await createVectorStore(data)
      MessagePlugin.success(t('vectorStoreSettings.toasts.storeCreated'))
    }
    showDialog.value = false
    await loadStores()
  } catch (error: any) {
    const msg = error?.message || t('vectorStoreSettings.toasts.errorGeneric')
    if (msg.toLowerCase().includes('already exists') || msg.toLowerCase().includes('duplicate')) {
      MessagePlugin.error(t('vectorStoreSettings.toasts.duplicateName'))
    } else {
      MessagePlugin.error(msg)
    }
  } finally {
    saving.value = false
  }
}

const handleAction = (action: { value: string }, store: VectorStoreEntity) => {
  // test 已挪到抽屉，外层菜单不再处理 'test' 值。
  if (action.value === 'edit') {
    editStore(store)
  } else if (action.value === 'delete') {
    confirmDelete(store)
  }
}

const confirmDelete = (store: VectorStoreEntity) => {
  const dialog = DialogPlugin.confirm({
    header: t('vectorStoreSettings.deleteConfirm'),
    confirmBtn: t('common.delete'),
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      try {
        await deleteVectorStoreAPI(store.id!)
        MessagePlugin.success(t('vectorStoreSettings.toasts.storeDeleted'))
        await loadStores()
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('vectorStoreSettings.toasts.errorGeneric'))
      }
      dialog.destroy()
    },
  })
}

// 测试连接（在抽屉内触发）。create 模式下用当前表单数据，调
// /test/raw 端点。edit 模式按钮 disabled，所以这里只处理 create 路径。
const onDrawerTest = async () => {
  if (editingStore.value) return
  testing.value = true
  try {
    const data = {
      engine_type: form.value.engine_type,
      connection_config: { ...form.value.connection_config },
    }
    const res = await testVectorStoreRaw(data)
    lastTestOk.value = !!res.success
    if (res.success) {
      MessagePlugin.success(t('vectorStoreSettings.toasts.testSuccess'))
    } else {
      MessagePlugin.error(res.error || t('vectorStoreSettings.toasts.testFailed'))
    }
  } catch (error: any) {
    lastTestOk.value = false
    MessagePlugin.error(error?.message || t('vectorStoreSettings.toasts.testFailed'))
  } finally {
    testing.value = false
  }
}

// ===== Init =====
onMounted(async () => {
  loading.value = true
  try {
    await Promise.all([loadStoreTypes(), loadStores()])
  } finally {
    loading.value = false
  }
})
</script>

<style lang="less" scoped>
.vectorstore-settings {
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
    margin: 0;
    line-height: 1.5;
  }
}

.loading-container {
  display: flex;
  justify-content: center;
  padding: 48px 0;
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.list-section-title {
  font-size: 16px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 16px 0;
}

.store-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;

  .store-card--add {
    width: 100%;
    height: 100%;
  }
}

// 与 Parser / Storage / Model 等同形：徽章 + 三段式。env 来源走 secondaryContainer
// 底色暗示只读；test 按钮做成 text 模式，避免在标题行抢眼。
.store-card {
  display: flex;
  flex-direction: column;
  padding: 14px 14px 14px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;

  &--env {
    background: var(--td-bg-color-secondarycontainer);
  }

  &--clickable {
    cursor: pointer;

    &:hover {
      border-color: var(--td-brand-color-3, var(--td-brand-color));
      box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
  }

  &--env:not(.store-card--clickable):hover {
    border-color: var(--td-component-stroke);
    box-shadow: none;
  }

  &--add {
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 68px;
    border-style: dashed;
    background: transparent;
    color: var(--td-text-color-placeholder);
    cursor: pointer;
    font: inherit;
    text-align: center;

    &:hover,
    &:focus-visible {
      color: var(--td-brand-color);
      border-color: var(--td-brand-color);
      background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
      box-shadow: none;
    }

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }

    &__icon {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      border-radius: 8px;
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
      color: var(--td-brand-color);
      font-size: 18px;
    }

    &__label {
      font-size: 13px;
      font-weight: 500;
      line-height: 1.4;
    }
  }
}

.store-card__actions {
  flex-shrink: 0;
}

.store-card__main {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  min-width: 0;
}

.store-card__badge {
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

// 真实品牌 logo 的渲染：保留每个 engine 类的 color 作为品牌色，
// 把背景换成中性白 + 细边框；用 ::before mask-image 把单色 SVG 染成 currentColor。
// 选择器叠了一层 .store-card 是为了胜过 `.store-card--<engine> .store-card__badge`
// 那条更具体的品牌底色规则。
.store-card .store-card__badge--logo {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.store-card .store-card__badge--mono::before {
  content: '';
  width: 22px;
  height: 22px;
  background-color: currentColor;
  -webkit-mask-image: var(--logo-url);
  -webkit-mask-position: center;
  -webkit-mask-repeat: no-repeat;
  -webkit-mask-size: contain;
  mask-image: var(--logo-url);
  mask-position: center;
  mask-repeat: no-repeat;
  mask-size: contain;
}

.store-card__badge-img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  display: block;
}

// 各 vector engine 配色（覆盖 11 类常见后端，未列出的回落到默认蓝）
.store-card--qdrant .store-card__badge {
  background: rgba(225, 38, 38, 0.12);
  color: #E12626;
}
.store-card--milvus .store-card__badge {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.store-card--weaviate .store-card__badge {
  background: rgba(7, 192, 95, 0.12);
  color: #07A050;
}
.store-card--elasticsearch .store-card__badge,
.store-card--elasticfaiss .store-card__badge {
  background: rgba(255, 153, 0, 0.12);
  color: #D97706;
}
.store-card--postgres .store-card__badge {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}
.store-card--opensearch .store-card__badge {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
.store-card--infinity .store-card__badge {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
.store-card--tencent_vectordb .store-card__badge {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}
.store-card--doris .store-card__badge {
  background: rgba(255, 90, 0, 0.12);
  color: #E55A00;
}
.store-card--sqlite .store-card__badge {
  background: rgba(70, 70, 70, 0.1);
  color: #464646;
}

.store-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.store-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.store-card__title {
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

.store-card__pill {
  flex-shrink: 0;
  padding: 1px 6px;
  font-size: 11px;
  font-weight: 500;
  line-height: 16px;
  border-radius: 3px;
  color: var(--td-warning-color-7, #B85C00);
  background: var(--td-warning-color-1, #FEF3E6);
}

.store-card__more {
  flex-shrink: 0;
  color: var(--td-text-color-placeholder);
  padding: 2px;
  opacity: 0;
  transition: opacity 0.15s ease;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.store-card:hover .store-card__more,
.store-card:focus-within .store-card__more,
.store-card__actions:focus-within .store-card__more {
  opacity: 1;
}

.store-card__subtitle {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--td-text-color-secondary);
  min-width: 0;
}

.store-card__type {
  font-weight: 500;
}

.store-card__sep {
  color: var(--td-text-color-placeholder);
}

.store-card__endpoint {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}


.empty-stores {
  padding: 64px 0;
  text-align: center;

  :deep(.t-empty__description) {
    font-size: 14px;
    color: var(--td-text-color-placeholder);
    margin-bottom: 16px;
  }
}

// ---- 抽屉内容 — 与 ModelEditorDialog 同款约定 ----
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

  &--inline { margin: 0; }

  // TLS 警告等"危险确认"用红字
  &--warn { color: var(--td-error-color); }
}

:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea) {
  width: 100%;
  font-size: 13px;
}

// 隐藏 t-form 默认 form-item 容器 — 走自定义 .form-item / .form-label
:deep(.t-form) .t-form-item {
  display: none;
}

.vision-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
}

// ---- inline alert（替代之前的 .immutable-notice 大块横幅） ----
.inline-alert {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  flex-wrap: wrap;

  white-space: pre-line;

  &__icon {
    font-size: 15px;
    flex-shrink: 0;
    color: var(--td-text-color-placeholder);
  }

  &__text {
    flex: 1 1 auto;
    min-width: 0;
  }

  &--info {
    color: var(--td-text-color-primary);

    .inline-alert__icon { color: var(--td-brand-color); }
  }
}

// ---- 编辑模式只读字段列表（保持原有视觉，但去掉外框，紧贴 alert 下方）----
.readonly-fields {
  padding: 10px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 8px;
}

.readonly-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 4px 0;
  font-size: 12px;
  line-height: 1.4;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child { border-bottom: none; }
}

.readonly-label {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  white-space: nowrap;
  min-width: 80px;
}

.readonly-value {
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  word-break: break-all;
}

// ---- 高级索引展开/收起按钮 ----
.advanced-toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 0;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  background: transparent;
  border: none;
  font-family: inherit;
  cursor: pointer;
  user-select: none;
  align-self: flex-start;

  &:hover { color: var(--td-brand-color); }

  .t-icon { font-size: 14px; }
}

// ---- Number input：去原生 spinner（与 MCP 高级配置同款）----
.number-input {
  :deep(input::-webkit-outer-spin-button),
  :deep(input::-webkit-inner-spin-button) {
    -webkit-appearance: none;
    appearance: none;
    margin: 0;
  }

  :deep(input[type="number"]) {
    -moz-appearance: textfield;
    appearance: textfield;
  }
}

// ---- Header 图标徽章 ----
.header-icon__img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  display: block;
}

.header-icon__mono {
  display: inline-block;
  width: 22px;
  height: 22px;
  background-color: currentColor;
  -webkit-mask-image: var(--logo-url);
  -webkit-mask-position: center;
  -webkit-mask-repeat: no-repeat;
  -webkit-mask-size: contain;
  mask-image: var(--logo-url);
  mask-position: center;
  mask-repeat: no-repeat;
  mask-size: contain;
}

.header-icon__text {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

// ---- footer-left 测试按钮的状态 icon ----
.status-icon {
  font-size: 16px;
  flex-shrink: 0;

  &.available { color: var(--td-brand-color); }
  &.unavailable { color: var(--td-error-color); }
}
</style>

<!--
  Non-scoped block: per-engine header-icon coloring + color-logo background
  tweak. Same pattern as Storage/Parser/WebSearch drawers — these rules
  must be global so they reach the t-drawer panel even if its scoped
  data-attribute is dropped in some builds. Each rule mirrors the matching
  .store-card--{engine} .store-card__badge from the scoped block above so
  list-card → drawer hand-off stays visually continuous.
-->
<style lang="less">
// 彩色 logo 时给 header-icon 容器一个白底 + 1px 边
.vectorstore-drawer .setting-drawer__header-icon:has(.header-icon__img) {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.vectorstore-drawer--qdrant .setting-drawer__header-icon {
  background: rgba(225, 38, 38, 0.12);
  color: #E12626;
}
.vectorstore-drawer--milvus .setting-drawer__header-icon {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.vectorstore-drawer--weaviate .setting-drawer__header-icon {
  background: rgba(7, 192, 95, 0.12);
  color: #07A050;
}
.vectorstore-drawer--elasticsearch .setting-drawer__header-icon,
.vectorstore-drawer--elasticfaiss .setting-drawer__header-icon {
  background: rgba(255, 153, 0, 0.12);
  color: #D97706;
}
.vectorstore-drawer--postgres .setting-drawer__header-icon {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}
.vectorstore-drawer--opensearch .setting-drawer__header-icon {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
.vectorstore-drawer--infinity .setting-drawer__header-icon {
  background: rgba(98, 53, 187, 0.12);
  color: #6235BB;
}
.vectorstore-drawer--tencent_vectordb .setting-drawer__header-icon {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}
.vectorstore-drawer--doris .setting-drawer__header-icon {
  background: rgba(255, 90, 0, 0.12);
  color: #E55A00;
}
.vectorstore-drawer--sqlite .setting-drawer__header-icon {
  background: rgba(70, 70, 70, 0.1);
  color: #464646;
}
</style>
