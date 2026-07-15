<template>
  <div class="storage-backend-settings">
    <div class="section-header">
      <div class="section-header__top">
        <div>
          <h2>{{ t('settings.storage.title') }}</h2>
          <p class="section-description">管理文件与图片使用的存储实例；同一种类型可以配置多个实例。</p>
        </div>
      </div>
    </div>

    <t-loading :loading="loading" size="small" class="backend-list-loading">
      <t-empty
        v-if="!loading && backends.length === 0 && !authStore.hasRole('admin')"
        description="尚未配置存储实例"
      />
      <div v-else-if="!loading" class="backend-grid">
        <div
          v-for="backend in backends"
          :key="backend.id"
          class="backend-card"
          :class="[
            `backend-card--${backend.provider}`,
            { 'backend-card--clickable': canEdit(backend) },
          ]"
          :role="canEdit(backend) ? 'button' : undefined"
          :tabindex="canEdit(backend) ? 0 : undefined"
          @click="onCardClick($event, backend)"
          @keydown.enter="onCardClick($event, backend)"
        >
          <div
            class="backend-card__badge"
            :class="badgeClass(backend.provider)"
            :style="badgeStyle(backend.provider)"
            :aria-label="backend.provider"
          >
            <img
              v-if="resolveLogo(backend.provider)?.mode === 'color'"
              :src="resolveLogo(backend.provider)!.url"
              :alt="backend.provider"
              class="backend-card__badge-img"
            />
            <template v-else-if="!resolveLogo(backend.provider)">{{ providerInitial(backend.provider) }}</template>
          </div>
          <div class="backend-card__body">
            <div class="backend-card__header">
              <h3 class="backend-card__title">{{ backend.name }}</h3>
              <t-tag v-if="backend.id === defaultID" theme="primary" variant="light" size="small">默认</t-tag>
              <div v-if="hasActions(backend)" class="backend-card__actions" @click.stop>
                <t-dropdown
                  :options="getBackendOptions(backend)"
                  placement="bottom-right"
                  attach="body"
                  trigger="click"
                  @click="(data: any) => handleMenuAction(data.value, backend)"
                >
                  <t-button variant="text" shape="square" size="small" class="backend-card__action-btn">
                    <t-icon name="ellipsis" />
                  </t-button>
                </t-dropdown>
              </div>
            </div>
            <p class="backend-card__subtitle">
              <span>{{ backend.provider.toUpperCase() }}</span>
              <template v-if="backendMeta(backend)">
                <span class="backend-card__sep">·</span>
                <span class="backend-card__meta">{{ backendMeta(backend) }}</span>
              </template>
            </p>
          </div>
        </div>

        <button
          v-if="authStore.hasRole('admin')"
          type="button"
          class="backend-card backend-card--add"
          @click="openCreate"
        >
          <span class="backend-card--add__icon" aria-hidden="true">
            <add-icon />
          </span>
          <span class="backend-card--add__label">添加存储实例</span>
        </button>
      </div>
    </t-loading>

    <SettingDrawer
      v-model:visible="visible"
      :title="editing ? '编辑存储实例' : '添加存储实例'"
      :class="`storage-backend-drawer storage-backend-drawer--${form.provider}`"
      :confirm-loading="saving"
      @confirm="save"
      @cancel="visible = false"
    >
      <template #headerIcon>
        <img
          v-if="currentLogo?.mode === 'color'"
          :src="currentLogo.url"
          :alt="form.provider"
          class="header-icon__img"
        />
        <span
          v-else-if="currentLogo?.mode === 'mono'"
          class="header-icon__mono"
          :style="monoLogoStyle"
        />
        <span v-else class="header-icon__text">{{ providerInitial(form.provider) }}</span>
      </template>
      <template #subtitle>
        <span>{{ editing ? '修改此存储实例的连接配置。' : '为文件与图片新增一个存储实例。' }}</span>
      </template>

      <t-form :data="form" layout="vertical">
        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">基本信息</h4>
          <div class="form-item">
            <label class="form-label required">名称</label>
            <t-input v-model="form.name" placeholder="例如：生产 COS、归档 COS" clearable />
          </div>
          <div class="form-item">
            <label class="form-label required">存储类型</label>
            <t-select v-model="form.provider" :disabled="!!editing" @change="resetConfig">
              <t-option
                v-for="provider in providers"
                :key="provider"
                :value="provider"
                :label="provider.toUpperCase()"
              />
            </t-select>
          </div>
          <div v-if="form.provider === 'minio'" class="form-item">
            <label class="form-label">部署模式</label>
            <div class="source-options" role="radiogroup">
              <button
                type="button"
                class="source-option"
                :class="{ 'is-active': form.config.mode !== 'docker' }"
                :disabled="!!editing"
                @click="form.config.mode = 'remote'"
              >
                <t-icon name="cloud" class="source-option__icon" />
                <span class="source-option__label">远程实例</span>
              </button>
              <button
                type="button"
                class="source-option"
                :class="{ 'is-active': form.config.mode === 'docker' }"
                :disabled="!!editing"
                @click="form.config.mode = 'docker'"
              >
                <t-icon name="server" class="source-option__icon" />
                <span class="source-option__label">环境变量</span>
              </button>
            </div>
          </div>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">连接配置</h4>
          <div v-if="needsEndpoint" class="form-item">
            <label class="form-label required">Endpoint</label>
            <t-input
              v-model="form.config.endpoint"
              :disabled="!!editing"
              :placeholder="form.provider === 'minio' ? 'storage.example.com:9000' : 'https://storage.example.com'"
              clearable
            />
          </div>
          <div v-if="needsRegion" class="form-item">
            <label class="form-label required">Region</label>
            <t-input v-model="form.config.region" :disabled="!!editing" clearable />
          </div>
          <template v-if="needsCredentials">
            <div class="form-item">
              <label class="form-label required">Access Key / Secret ID</label>
              <t-input v-model="form.config.access_key_id" placeholder="***" clearable>
                <template #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>
            </div>
            <div class="form-item">
              <label class="form-label required">Secret Key</label>
              <t-input v-model="form.config.secret_access_key" type="password" placeholder="***" clearable>
                <template #prefix-icon><t-icon name="lock-on" /></template>
              </t-input>
            </div>
          </template>
          <div v-if="form.provider !== 'local'" class="form-item">
            <label class="form-label required">Bucket</label>
            <t-input v-model="form.config.bucket_name" :disabled="!!editing" clearable />
          </div>
          <div v-if="form.provider === 'cos'" class="form-item">
            <label class="form-label">App ID</label>
            <t-input v-model="form.config.app_id" :disabled="!!editing" placeholder="可选" clearable />
          </div>
        </section>

        <section class="setting-drawer__section">
          <h4 class="setting-drawer__section-title">高级选项</h4>
          <div class="form-item">
            <label class="form-label">路径前缀</label>
            <t-input v-model="form.config.path_prefix" :disabled="!!editing" placeholder="weknora/" clearable />
          </div>
          <div v-if="form.provider === 'minio'" class="form-item">
            <div class="vision-toggle">
              <t-switch v-model="form.config.use_ssl" />
              <span class="form-desc form-desc--inline">使用 HTTPS 访问 MinIO</span>
            </div>
          </div>
          <div v-if="form.provider === 's3'" class="form-item">
            <div class="vision-toggle">
              <t-switch v-model="form.config.force_path_style" />
              <span class="form-desc form-desc--inline">使用 Path Style</span>
            </div>
          </div>
          <div v-if="form.provider === 'oss'" class="form-item">
            <div class="vision-toggle">
              <t-switch v-model="form.config.use_temp_bucket" />
              <span class="form-desc form-desc--inline">使用临时桶</span>
            </div>
          </div>
          <template v-if="['cos', 'tos'].includes(form.provider) || (form.provider === 'oss' && form.config.use_temp_bucket)">
            <div class="form-item">
              <label class="form-label">临时桶</label>
              <t-input v-model="form.config.temp_bucket_name" placeholder="可选，用于临时文件" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">临时桶 Region</label>
              <t-input v-model="form.config.temp_region" placeholder="留空时使用主 Region" clearable />
            </div>
          </template>
        </section>
      </t-form>

      <template #footer-left>
        <t-button variant="outline" :loading="testing" @click="testRaw">
          <template #icon>
            <t-icon v-if="!testing && rawTestResult === 'ok'" name="check-circle-filled" class="status-icon available" />
            <t-icon v-else-if="!testing && rawTestResult === 'error'" name="close-circle-filled" class="status-icon unavailable" />
          </template>
          测试连接
        </t-button>
      </template>
    </SettingDrawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { AddIcon } from 'tdesign-icons-vue-next'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import { providerLogo } from './providerLogos'
import {
  createStorageBackend, deleteStorageBackend, listStorageBackends, listStorageBackendTypes,
  setDefaultStorageBackend, testStorageBackend, testStorageBackendByID, updateStorageBackend,
  type StorageBackend, type StorageBackendConfig,
} from '@/api/storage-backend'

const { t } = useI18n()
const authStore = useAuthStore()
const loading = ref(false), saving = ref(false), testing = ref(false), visible = ref(false)
const backends = ref<StorageBackend[]>([]), providers = ref<string[]>([]), defaultID = ref('')
const editing = ref<StorageBackend | null>(null)
const rawTestResult = ref<'ok' | 'error' | null>(null)
const blankConfig = (): StorageBackendConfig => ({ mode: 'remote', endpoint: '', region: '', access_key_id: '', secret_access_key: '', bucket_name: '', path_prefix: '', use_ssl: true })
const form = reactive<{ name: string; provider: string; config: StorageBackendConfig }>({ name: '', provider: 'local', config: blankConfig() })
const needsEndpoint = computed(() => !['local', 'cos'].includes(form.provider) && !(form.provider === 'minio' && form.config.mode === 'docker'))
const needsRegion = computed(() => !['local', 'minio'].includes(form.provider))
const needsCredentials = computed(() => form.provider !== 'local' && !(form.provider === 'minio' && form.config.mode === 'docker'))

const resolveLogo = (provider: string) => providerLogo('storage', provider)
const providerInitial = (provider: string) => (provider || '?').trim().charAt(0).toUpperCase() || '?'
const badgeClass = (provider: string) => {
  const mode = resolveLogo(provider)?.mode
  return {
    'backend-card__badge--logo': !!mode,
    'backend-card__badge--color': mode === 'color',
    'backend-card__badge--mono': mode === 'mono',
  }
}
const badgeStyle = (provider: string): Record<string, string> => {
  const logo = resolveLogo(provider)
  return logo?.mode === 'mono' ? { '--logo-url': `url("${logo.url}")` } : {}
}

const currentLogo = computed(() => providerLogo('storage', form.provider))
const monoLogoStyle = computed((): Record<string, string> => {
  const logo = currentLogo.value
  if (!logo || logo.mode !== 'mono') return {}
  return { '--logo-url': `url("${logo.url}")` }
})

function backendMeta(backend: StorageBackend): string {
  return backend.config.endpoint || backend.config.bucket_name || backend.config.path_prefix || '本地存储'
}

const canEdit = (backend: StorageBackend) => authStore.hasRole('admin') && backend.source !== 'env'
const canDelete = (backend: StorageBackend) => authStore.hasRole('admin') && backend.source !== 'env' && !backend.legacy_alias
const canSetDefault = (backend: StorageBackend) => backend.id !== defaultID.value && authStore.hasRole('admin')
// 测试连接对所有可见用户开放，因此每张卡至少有一个动作。
const hasActions = (_backend: StorageBackend) => true

function getBackendOptions(backend: StorageBackend) {
  const options: { content: string; value: string; theme?: string }[] = []
  options.push({ content: '测试连接', value: 'test' })
  if (canSetDefault(backend)) options.push({ content: '设为默认', value: 'default' })
  if (canEdit(backend)) options.push({ content: '编辑', value: 'edit' })
  if (canDelete(backend)) options.push({ content: '删除', value: 'delete', theme: 'error' })
  return options
}

function handleMenuAction(value: string, backend: StorageBackend) {
  if (value === 'test') testSaved(backend)
  else if (value === 'default') makeDefault(backend)
  else if (value === 'edit') openEdit(backend)
  else if (value === 'delete') remove(backend)
}

function onCardClick(event: Event, backend: StorageBackend) {
  if (!canEdit(backend)) return
  const target = event.target as HTMLElement | null
  if (target?.closest('.backend-card__actions')) return
  openEdit(backend)
}

async function load() {
  loading.value = true
  try {
    const [list, types] = await Promise.all([listStorageBackends(), listStorageBackendTypes()])
    backends.value = list.data || []; defaultID.value = list.default_storage_backend_id || ''; providers.value = types.data || []
  } finally { loading.value = false }
}
function resetConfig() { form.config = blankConfig(); rawTestResult.value = null }
function openCreate() { editing.value = null; form.name = ''; form.provider = providers.value[0] || 'local'; form.config = blankConfig(); rawTestResult.value = null; visible.value = true }
function openEdit(backend: StorageBackend) { editing.value = backend; form.name = backend.name; form.provider = backend.provider; form.config = { ...blankConfig(), ...backend.config }; rawTestResult.value = null; visible.value = true }
async function testRaw() {
  testing.value = true
  rawTestResult.value = null
  try {
    const r: any = editing.value ? await testStorageBackendByID(editing.value.id) : await testStorageBackend(form)
    if (r.success) { rawTestResult.value = 'ok'; MessagePlugin.success('连接成功') }
    else { rawTestResult.value = 'error'; MessagePlugin.error(r.error || '连接失败') }
  } finally { testing.value = false }
}
async function testSaved(backend: StorageBackend) { const r: any = await testStorageBackendByID(backend.id); r.success ? MessagePlugin.success('连接成功') : MessagePlugin.error(r.error || '连接失败') }
async function save() {
  if (!form.name.trim()) { MessagePlugin.warning('请输入名称'); return }
  saving.value = true
  try {
    const payload = { name: form.name.trim(), provider: form.provider, config: { ...form.config } }
    if (editing.value) await updateStorageBackend(editing.value.id, payload); else await createStorageBackend(payload)
    MessagePlugin.success('保存成功'); visible.value = false; await load()
  } catch (e: any) { MessagePlugin.error(e?.message || '保存失败') } finally { saving.value = false }
}
async function makeDefault(backend: StorageBackend) { await setDefaultStorageBackend(backend.id); defaultID.value = backend.id; MessagePlugin.success('默认存储已更新') }
function remove(backend: StorageBackend) {
  const dialog = DialogPlugin.confirm({ header: '删除存储实例', body: `确定删除“${backend.name}”吗？`, onConfirm: async () => { dialog.destroy(); try { await deleteStorageBackend(backend.id); await load(); MessagePlugin.success('已删除') } catch (e: any) { MessagePlugin.error(e?.message || '删除失败') } }, onCancel: () => dialog.destroy() })
}
onMounted(load)
</script>

<style scoped lang="less">
.storage-backend-settings {
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

.section-header__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}

.backend-list-loading {
  min-height: 120px;
}

.backend-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;

  .backend-card--add {
    width: 100%;
    height: 100%;
  }
}

.backend-card {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 10px;
  background: var(--td-bg-color-container);
  transition: border-color 0.18s ease, box-shadow 0.18s ease;
  min-width: 0;

  &:hover {
    border-color: var(--td-brand-color-3, var(--td-brand-color));
    box-shadow: 0 4px 14px rgba(15, 23, 42, 0.06);
  }

  &--clickable {
    cursor: pointer;

    &:focus-visible {
      outline: 2px solid var(--td-brand-color);
      outline-offset: 2px;
    }
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

.backend-card__badge {
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

.backend-card .backend-card__badge--logo {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.backend-card .backend-card__badge--mono::before {
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

.backend-card__badge-img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  display: block;
}

.backend-card--local .backend-card__badge { background: rgba(70, 70, 70, 0.1); color: #464646; }
.backend-card--minio .backend-card__badge { background: rgba(225, 38, 38, 0.12); color: #C0382B; }
.backend-card--cos .backend-card__badge { background: rgba(0, 82, 217, 0.1); color: #0052D9; }
.backend-card--tos .backend-card__badge { background: rgba(0, 137, 255, 0.12); color: #0089FF; }
.backend-card--s3 .backend-card__badge { background: rgba(255, 153, 0, 0.12); color: #D97706; }
.backend-card--oss .backend-card__badge { background: rgba(255, 90, 0, 0.12); color: #E55A00; }
.backend-card--ks3 .backend-card__badge { background: rgba(7, 192, 95, 0.12); color: #07A050; }
.backend-card--obs .backend-card__badge { background: rgba(206, 17, 38, 0.1); color: #CE1126; }

.backend-card__body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 2px;
}

.backend-card__header {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.backend-card__title {
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

.backend-card__subtitle {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--td-text-color-secondary);
  display: flex;
  align-items: center;
  min-width: 0;
}

.backend-card__sep {
  margin: 0 6px;
  color: var(--td-text-color-placeholder);
  flex-shrink: 0;
}

.backend-card__meta {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.backend-card__actions {
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.backend-card__action-btn {
  flex-shrink: 0;
  padding: 2px;
  color: var(--td-text-color-placeholder);
  opacity: 0;
  transition: opacity 0.15s ease;

  &:hover,
  &:focus-visible {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.backend-card:hover .backend-card__action-btn,
.backend-card:focus-within .backend-card__action-btn {
  opacity: 1;
}

// ---- 抽屉头部图标 ----
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

// ---- 抽屉表单 ----
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

  &--inline {
    margin: 0;
  }
}

:deep(.t-input),
:deep(.t-select),
:deep(.t-textarea) {
  width: 100%;
  font-size: 13px;
}

.vision-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
}

// ---- MinIO 部署模式 pill segmented ----
.source-options {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px;
  background: var(--td-bg-color-component);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
}

.source-option {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 12px;
  height: 28px;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 6px;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1;
  transition: all 0.15s ease;

  &:disabled {
    cursor: not-allowed;
    opacity: 0.6;
  }

  &:hover:not(.is-active):not(:disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &.is-active {
    background: var(--td-bg-color-container);
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    font-weight: 500;
    box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  }
}

.source-option__icon {
  font-size: 14px;
  flex-shrink: 0;
}

.source-option__label {
  white-space: nowrap;
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
</style>

<!--
  Non-scoped: drawer header icon coloring per provider, mirroring the list
  card badge colors. Namespaced under .storage-backend-drawer--{id}.
-->
<style lang="less">
.storage-backend-drawer .setting-drawer__header-icon:has(.header-icon__img) {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.storage-backend-drawer--local .setting-drawer__header-icon { background: rgba(70, 70, 70, 0.1); color: #464646; }
.storage-backend-drawer--minio .setting-drawer__header-icon { background: rgba(225, 38, 38, 0.12); color: #C0382B; }
.storage-backend-drawer--cos .setting-drawer__header-icon { background: rgba(0, 82, 217, 0.1); color: #0052D9; }
.storage-backend-drawer--tos .setting-drawer__header-icon { background: rgba(0, 137, 255, 0.12); color: #0089FF; }
.storage-backend-drawer--s3 .setting-drawer__header-icon { background: rgba(255, 153, 0, 0.12); color: #D97706; }
.storage-backend-drawer--oss .setting-drawer__header-icon { background: rgba(255, 90, 0, 0.12); color: #E55A00; }
.storage-backend-drawer--ks3 .setting-drawer__header-icon { background: rgba(7, 192, 95, 0.12); color: #07A050; }
.storage-backend-drawer--obs .setting-drawer__header-icon { background: rgba(206, 17, 38, 0.1); color: #CE1126; }
</style>
