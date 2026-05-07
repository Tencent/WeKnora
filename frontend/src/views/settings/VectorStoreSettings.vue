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
          <t-button theme="primary" variant="outline" size="small" @click="openAddDialog">
            <template #icon><add-icon /></template>
            {{ t('vectorStoreSettings.addStore') }}
          </t-button>
        </div>

        <!-- Store List -->
        <div v-if="stores.length > 0" class="store-list">
          <!-- Env Stores -->
          <div v-for="store in envStores" :key="store.id" class="store-item store-item--env">
            <div class="item-row">
              <div class="item-info">
                <div class="item-header">
                  <t-icon name="lock-on" size="14px" />
                  <span class="item-name">{{ store.name }}</span>
                  <t-tag theme="warning" size="small" variant="light">{{ t('vectorStoreSettings.envTag') }}</t-tag>
                  <t-tag size="small" variant="outline">{{ store.engine_type }}</t-tag>
                </div>
              </div>
              <div class="item-actions">
                <t-button
                  theme="default" variant="outline" size="small"
                  :loading="testingId === store.id"
                  @click="testExisting(store)"
                >
                  {{ t('vectorStoreSettings.testConnection') }}
                </t-button>
                <t-button theme="default" variant="text" size="small" shape="square" style="visibility: hidden;">
                  <template #icon><t-icon name="more" /></template>
                </t-button>
              </div>
            </div>
            <div v-if="getTestResult(store)" :class="['test-result-bar', getTestResult(store)!.success ? 'success' : 'error']">
              <span class="test-result-message">{{ getTestResult(store)!.message }}</span>
              <t-icon name="close" size="14px" class="test-result-close" @click="clearTestResult(store)" />
            </div>
          </div>

          <!-- User Stores -->
          <div v-for="store in userStores" :key="store.id" class="store-item">
            <div class="item-row">
              <div class="item-info">
                <div class="item-header">
                  <span class="item-name">{{ store.name }}</span>
                  <t-tag size="small" variant="outline">{{ store.engine_type }}</t-tag>
                </div>
                <div class="item-desc">{{ getStoreEndpoint(store) }}</div>
              </div>
              <div class="item-actions">
                <t-button
                  theme="default" variant="outline" size="small"
                  :loading="testingId === store.id"
                  @click="testExisting(store)"
                >
                  {{ t('vectorStoreSettings.testConnection') }}
                </t-button>
                <t-dropdown :options="storeActions" trigger="click" @click="(action: any) => handleAction(action, store)">
                  <t-button theme="default" variant="text" size="small" shape="square">
                    <template #icon><t-icon name="more" /></template>
                  </t-button>
                </t-dropdown>
              </div>
            </div>
            <div v-if="getTestResult(store)" :class="['test-result-bar', getTestResult(store)!.success ? 'success' : 'error']">
              <span class="test-result-message">{{ getTestResult(store)!.message }}</span>
              <t-icon name="close" size="14px" class="test-result-close" @click="clearTestResult(store)" />
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

const { t } = useI18n()

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

.store-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.store-item {
  display: flex;
  flex-direction: column;
  padding: 14px 16px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  transition: all 0.2s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }

  &--env {
    background: var(--td-bg-color-secondarycontainer);

    &:hover {
      border-color: var(--td-component-stroke);
    }
  }
}

.item-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
  flex: 1;
}

.item-header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.item-name {
  font-size: 14px;
  font-weight: 500;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-desc {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.item-actions {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-shrink: 0;
  margin-left: 12px;
}

.item-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
}

.test-result-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.4;
  word-break: break-word;

  &.success {
    background: rgba(7, 192, 95, 0.1);
    color: var(--td-success-color);
  }

  &.error {
    background: var(--td-error-color-1, rgba(227, 77, 89, 0.1));
    color: var(--td-error-color);
  }
}

.test-result-message {
  flex: 1;
  min-width: 0;
}

.test-result-close {
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
