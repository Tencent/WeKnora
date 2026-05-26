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
        <div class="section-subheader">
          <h3>{{ t('vectorStoreSettings.storesTitle') }}</h3>
          <t-button v-if="authStore.hasRole('admin')" theme="primary" variant="outline" size="small" @click="openAddDialog">
            <template #icon><add-icon /></template>
            {{ t('vectorStoreSettings.addStore') }}
          </t-button>
        </div>

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
              { 'store-card--env': store.source === 'env' }
            ]"
          >
            <div class="store-card__main">
              <div class="store-card__badge">{{ engineInitial(store.engine_type) }}</div>
              <div class="store-card__body">
                <div class="store-card__header">
                  <h3 class="store-card__title" :title="store.name">{{ store.name }}</h3>
                  <span v-if="store.source === 'env'" class="store-card__pill">
                    {{ t('vectorStoreSettings.envTag') }}
                  </span>
                  <t-button
                    v-if="authStore.hasRole('admin')"
                    class="store-card__test"
                    theme="default"
                    variant="text"
                    size="small"
                    :loading="testingId === store.id"
                    @click="testExisting(store)"
                  >
                    {{ t('vectorStoreSettings.testConnection') }}
                  </t-button>
                  <t-dropdown
                    v-if="authStore.hasRole('admin') && store.source !== 'env'"
                    :options="storeActions"
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
                <div class="store-card__subtitle">
                  <span class="store-card__type">{{ store.engine_type }}</span>
                  <template v-if="getStoreEndpoint(store)">
                    <span class="store-card__sep">·</span>
                    <span class="store-card__endpoint" :title="getStoreEndpoint(store)">{{ getStoreEndpoint(store) }}</span>
                  </template>
                </div>
              </div>
            </div>
            <div
              v-if="getTestResult(store)"
              :class="['store-card__result', getTestResult(store)!.success ? 'store-card__result--ok' : 'store-card__result--err']"
            >
              <span class="store-card__result-message">{{ getTestResult(store)!.message }}</span>
              <t-icon name="close" size="14px" class="store-card__result-close" @click="clearTestResult(store)" />
            </div>
          </div>
        </div>

        <!-- Empty State -->
        <div v-else class="empty-stores">
          <p>{{ t('vectorStoreSettings.emptyDesc') }}</p>
        </div>
      </div>
    </template>

    <!-- Add/Edit Dialog -->
    <t-dialog
      v-model:visible="showDialog"
      :header="editingStore ? t('vectorStoreSettings.editStore') : t('vectorStoreSettings.addStore')"
      width="580px"
      placement="center"
      :footer="false"
      destroy-on-close
    >
      <div class="dialog-form-container">
        <!-- Edit Mode: immutable info banner + readonly fields -->
        <template v-if="editingStore">
          <div class="immutable-notice">
            <t-icon name="info-circle" size="14px" />
            <span>{{ t('vectorStoreSettings.immutableNotice') }}</span>
          </div>
          <div class="readonly-fields">
            <div class="readonly-row">
              <span class="readonly-label">{{ t('vectorStoreSettings.engineTypeLabel') }}</span>
              <span class="readonly-value">{{ selectedType?.display_name || editingStore.engine_type }}</span>
            </div>
            <template v-if="selectedType">
              <template v-for="field in selectedType.connection_fields" :key="field.name">
                <div v-if="field.sensitive || form.connection_config[field.name]" class="readonly-row">
                  <span class="readonly-label">{{ fieldLabel(field.name) }}</span>
                  <span class="readonly-value">{{ field.sensitive ? '********' : form.connection_config[field.name] }}</span>
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
          <div class="form-divider"></div>
        </template>

        <t-form :data="form" :rules="formRules" label-align="top" @submit="saveStore" class="store-form">
          <div class="form-scroll-area">
          <!-- Create Mode: engine type + connection fields -->
          <template v-if="!editingStore">
            <t-form-item :label="t('vectorStoreSettings.engineTypeLabel')" name="engine_type">
              <t-select v-model="form.engine_type" @change="onEngineTypeChange">
                <t-option
                  v-for="st in storeTypes"
                  :key="st.type"
                  :value="st.type"
                  :label="st.display_name"
                />
              </t-select>
            </t-form-item>
          </template>

          <!-- Name (always editable) -->
          <t-form-item :label="t('vectorStoreSettings.nameLabel')" name="name">
            <t-input v-model="form.name" :placeholder="t('vectorStoreSettings.namePlaceholder')" />
          </t-form-item>

          <!-- Create Mode: connection fields -->
          <template v-if="!editingStore && selectedType">
            <div class="form-divider"></div>
            <div class="form-section-label">{{ t('vectorStoreSettings.connectionInfo') }}</div>

            <template v-for="field in selectedType.connection_fields" :key="field.name">
              <t-form-item
                :label="fieldLabel(field.name)"
                :name="`connection_config.${field.name}`"
              >
                <t-switch
                  v-if="field.type === 'boolean'"
                  v-model="form.connection_config[field.name]"
                />
                <t-input
                  v-else-if="field.type === 'string' && field.sensitive"
                  v-model="form.connection_config[field.name]"
                  type="password"
                  placeholder="********"
                />
                <t-input-number
                  v-else-if="field.type === 'number'"
                  v-model="form.connection_config[field.name]"
                  :placeholder="field.default != null ? String(field.default) : ' '"
                  theme="normal"
                  style="width: 100%;"
                />
                <t-input
                  v-else
                  v-model="form.connection_config[field.name]"
                  :placeholder="field.default?.toString() || ''"
                />
              </t-form-item>
            </template>

            <!-- Advanced: index fields -->
            <template v-if="selectedType.index_fields?.length">
              <div class="form-divider"></div>
              <div class="advanced-toggle" @click="showAdvanced = !showAdvanced">
                <t-icon :name="showAdvanced ? 'chevron-down' : 'chevron-right'" size="14px" />
                <span>{{ t('vectorStoreSettings.advancedIndexConfig') }}</span>
              </div>

              <template v-if="showAdvanced">
                <template v-for="field in selectedType.index_fields" :key="field.name">
                  <t-form-item :label="fieldLabel(field.name)" :name="`index_config.${field.name}`">
                    <t-input-number
                      v-if="field.type === 'number'"
                      v-model="form.index_config[field.name]"
                      :placeholder="field.default?.toString()"
                      :min="1"
                      :max="isReplicaField(field.name) ? 10 : 64"
                      theme="normal"
                      style="width: 100%;"
                    />
                    <t-input
                      v-else
                      v-model="form.index_config[field.name]"
                      :placeholder="field.default?.toString() || ''"
                      :maxlength="128"
                    />
                  </t-form-item>
                </template>
              </template>
            </template>
          </template>

          </div><!-- /.form-scroll-area -->

          <!-- Dialog Footer (outside scroll area) -->
          <div class="dialog-footer">
            <div class="footer-left">
              <t-button
                v-if="!editingStore"
                theme="default"
                variant="outline"
                :loading="testing"
                @click="testFromDialog"
              >
                {{ testing ? t('vectorStoreSettings.testing') : t('vectorStoreSettings.testConnection') }}
              </t-button>
            </div>
            <div class="footer-right">
              <t-button theme="default" variant="base" @click="showDialog = false">{{ t('common.cancel') }}</t-button>
              <t-button theme="primary" type="submit" :loading="saving">{{ t('common.save') }}</t-button>
            </div>
          </div>
        </t-form>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
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
  testVectorStoreById,
  type VectorStoreEntity,
  type VectorStoreTypeInfo,
} from '@/api/vector-store'
import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const authStore = useAuthStore()

// ===== State =====
const stores = ref<VectorStoreEntity[]>([])
const storeTypes = ref<VectorStoreTypeInfo[]>([])
const loading = ref(false)
const showDialog = ref(false)
const editingStore = ref<VectorStoreEntity | null>(null)
const testing = ref(false)
const testingId = ref<string | null>(null)
const saving = ref(false)
const showAdvanced = ref(false)
const testResults = ref<Record<string, { success: boolean; message: string } | null>>({})

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

// ===== Computed =====
const envStores = computed(() => stores.value.filter(s => s.source === 'env'))
const userStores = computed(() => stores.value.filter(s => s.source === 'user'))
const selectedType = computed(() => storeTypes.value.find(st => st.type === form.value.engine_type))

const storeActions = computed(() => [
  { content: t('common.edit'), value: 'edit' },
  { content: t('common.delete'), value: 'delete', theme: 'error' as const },
])

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

const getTestResult = (store: VectorStoreEntity) => {
  return store.id ? testResults.value[store.id] ?? null : null
}

const getStoreEndpoint = (store: VectorStoreEntity): string => {
  const cc = store.connection_config || {}
  return cc.addr || cc.host || ''
}

// 卡片徽章首字母。engine_type 都是英文 ASCII，直接 charAt。
const engineInitial = (engineType: string): string => {
  return (engineType || '?').charAt(0).toUpperCase()
}

const onEngineTypeChange = () => {
  form.value.connection_config = {}
  form.value.index_config = {}
  showAdvanced.value = false
}

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
  showDialog.value = true
}

const editStore = (store: VectorStoreEntity) => {
  editingStore.value = store
  showAdvanced.value = false
  form.value = {
    name: store.name,
    engine_type: store.engine_type,
    connection_config: { ...store.connection_config },
    index_config: { ...store.index_config },
  }
  showDialog.value = true
}

const saveStore = async ({ validateResult, firstError }: any) => {
  if (validateResult !== true && validateResult !== undefined) {
    MessagePlugin.warning(firstError || t('vectorStoreSettings.toasts.errorGeneric'))
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

const clearTestResult = (store: VectorStoreEntity) => {
  if (store.id) {
    const { [store.id]: _, ...rest } = testResults.value
    testResults.value = rest
  }
}

const testExisting = async (store: VectorStoreEntity) => {
  testingId.value = store.id!
  testResults.value = { ...testResults.value, [store.id!]: null }
  try {
    const res = await testVectorStoreById(store.id!)
    testResults.value = {
      ...testResults.value,
      [store.id!]: {
        success: res.success,
        message: res.success
          ? t('vectorStoreSettings.toasts.testSuccess')
          : (res.error || t('vectorStoreSettings.toasts.testFailed')),
      },
    }
  } catch (error: any) {
    testResults.value = {
      ...testResults.value,
      [store.id!]: {
        success: false,
        message: error?.message || t('vectorStoreSettings.toasts.testFailed'),
      },
    }
  } finally {
    testingId.value = null
  }
}

const testFromDialog = async () => {
  testing.value = true
  try {
    const data = {
      engine_type: form.value.engine_type,
      connection_config: { ...form.value.connection_config },
    }
    const res = await testVectorStoreRaw(data)
    if (res.success) {
      MessagePlugin.success(t('vectorStoreSettings.toasts.testSuccess'))
    } else {
      MessagePlugin.error(res.error || t('vectorStoreSettings.toasts.testFailed'))
    }
  } catch (error: any) {
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

.section-subheader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 16px;

  h3 {
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0;
  }
}

.store-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
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

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }

  &--env {
    background: var(--td-bg-color-secondarycontainer);

    &:hover {
      border-color: var(--td-component-stroke);
      box-shadow: none;
    }
  }
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

// 测试按钮做成 text 风格，hover 才显边框；让标题行视觉层级仍由名称主导。
.store-card__test {
  flex-shrink: 0;
  font-size: 12px;
  padding: 0 8px;
  height: 24px;
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
.store-card:focus-within .store-card__more {
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

.store-card__result {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.4;
  word-break: break-word;

  &--ok {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-success-color);
  }

  &--err {
    background: var(--td-error-color-1, rgba(227, 77, 89, 0.1));
    color: var(--td-error-color);
  }
}

.store-card__result-message {
  flex: 1;
  min-width: 0;
}

.store-card__result-close {
  flex-shrink: 0;
  margin-left: 8px;
  cursor: pointer;
  opacity: 0.6;

  &:hover {
    opacity: 1;
  }
}

.empty-stores {
  padding: 48px 32px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  border: 1px dashed var(--td-component-stroke);
  border-radius: 8px;
  font-size: 14px;
}

// Dialog
.dialog-form-container {
  margin-top: 12px;
  display: flex;
  flex-direction: column;
  max-height: 70vh;
}

.store-form {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.form-scroll-area {
  flex: 1;
  overflow-y: auto;
  padding-right: 12px;
}

.immutable-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 10px 14px;
  margin-bottom: 16px;
  background: rgba(7, 192, 95, 0.1);
  border-radius: 6px;
  font-size: 13px;
  line-height: 1.5;
  color: var(--td-brand-color);
  white-space: pre-line;
}

.readonly-fields {
  padding: 10px 14px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  margin-bottom: 16px;
}

.readonly-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 3px 0;
  font-size: 12px;
  line-height: 1.4;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.readonly-label {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  white-space: nowrap;
  min-width: 60px;
}

.readonly-value {
  color: var(--td-text-color-primary);
  font-size: 12px;
  font-family: var(--app-font-family-mono);
  word-break: break-all;
}

.form-divider {
  height: 1px;
  background: var(--td-component-border);
  margin: 20px 0;
}

.form-section-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--td-text-color-secondary);
  margin-bottom: 12px;
}

.advanced-toggle {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  cursor: pointer;
  user-select: none;
  margin-bottom: 12px;

  &:hover {
    color: var(--td-brand-color);
  }
}

.dialog-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-top: 32px;
  padding-top: 20px;
  border-top: 1px solid var(--td-component-border);

  .footer-right {
    display: flex;
    gap: 12px;
  }
}
</style>
