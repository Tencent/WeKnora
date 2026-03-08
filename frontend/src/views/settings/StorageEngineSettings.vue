<template>
  <div class="storage-engine-settings">
    <div class="section-header">
      <h2>{{ $t('settings.storage.title') }}</h2>
      <p class="section-description">
        {{ $t('settings.storage.description') }}
      </p>
    </div>

    <div v-if="loading" class="loading-state">
      <t-loading size="small" />
      <span>{{ $t('settings.storage.loading') }}</span>
    </div>

    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadAll">{{ $t('settings.storage.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <template v-else>
      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('settings.storage.defaultEngine') }}</label>
            <p class="desc">{{ $t('settings.storage.defaultEngineDesc') }}</p>
          </div>
          <div class="setting-control">
            <t-select v-model="config.default_provider" style="width: 280px;">
              <t-option value="local" :label="$t('settings.storage.engineLocal')" />
              <t-option value="minio" label="MinIO" />
              <t-option value="cos" :label="$t('settings.storage.engineCos')" />
              <t-option value="tos" :label="$t('settings.storage.engineTos')" />
            </t-select>
          </div>
        </div>
      </div>

      <!-- Local -->
      <div class="engine-section" data-model-type="local">
        <div class="engine-header">
          <div class="engine-header-info">
            <div class="engine-title-row">
              <h3>{{ $t('settings.storage.localTitle') }}</h3>
              <t-tag theme="success" variant="light" size="small">{{ $t('settings.storage.available') }}</t-tag>
            </div>
            <p>{{ $t('settings.storage.localDesc') }}</p>
          </div>
        </div>
        <div class="engine-form">
          <div class="form-field">
            <label>{{ $t('settings.storage.pathPrefix') }}</label>
            <t-input
              v-model="config.local.path_prefix"
              :placeholder="$t('settings.storage.pathPrefixPlaceholder')"
              clearable
            />
          </div>
        </div>
      </div>

      <!-- MinIO -->
      <div class="engine-section" data-model-type="minio">
        <div class="engine-header">
          <div class="engine-header-info">
            <div class="engine-title-row">
              <h3>MinIO</h3>
              <t-tag v-if="minioAvailable" theme="success" variant="light" size="small">{{ $t('settings.storage.available') }}</t-tag>
              <t-tag v-else theme="default" variant="light" size="small">{{ $t('settings.storage.needsConfig') }}</t-tag>
            </div>
            <p>{{ $t('settings.storage.minioDesc') }}</p>
          </div>
        </div>

        <div class="mode-selector">
          <div
            :class="['mode-option', { active: config.minio.mode !== 'remote' }]"
            @click="config.minio.mode = 'docker'"
          >
            <span class="mode-label">{{ $t('settings.storage.minioDocker') }}</span>
            <t-tag v-if="minioEnvAvailable" theme="success" variant="light" size="small">{{ $t('settings.storage.detected') }}</t-tag>
            <t-tag v-else theme="default" variant="light" size="small">{{ $t('settings.storage.notDetected') }}</t-tag>
          </div>
          <div
            :class="['mode-option', { active: config.minio.mode === 'remote' }]"
            @click="config.minio.mode = 'remote'"
          >
            <span class="mode-label">{{ $t('settings.storage.minioRemote') }}</span>
          </div>
        </div>

        <!-- Docker mode -->
        <div v-if="config.minio.mode !== 'remote'">
          <div v-if="minioEnvAvailable" class="engine-hint success">
            {{ $t('settings.storage.minioDockerDetected') }}
          </div>
          <div v-else class="engine-hint warning">
            {{ $t('settings.storage.minioDockerNotDetected') }}
          </div>
          <div class="engine-form">
            <div class="form-field">
              <label>{{ $t('settings.storage.bucketName') }}</label>
              <t-select
                v-model="config.minio.bucket_name"
                filterable
                creatable
                :placeholder="$t('settings.storage.bucketSelectPlaceholder')"
                :loading="loadingBuckets"
                :disabled="!minioEnvAvailable"
                @focus="loadMinioBuckets"
              >
                <t-option
                  v-for="b in minioBuckets"
                  :key="b.name"
                  :value="b.name"
                  :label="b.name"
                />
              </t-select>
            </div>
            <div class="form-field form-field--inline">
              <label>Use SSL</label>
              <t-switch v-model="config.minio.use_ssl" size="small" />
            </div>
            <div class="form-field">
              <label>{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input
                v-model="config.minio.path_prefix"
                :placeholder="$t('settings.storage.prefixPlaceholder')"
                clearable
              />
            </div>
          </div>
          <div v-if="minioEnvAvailable" class="test-bar">
            <t-button size="small" variant="outline" :loading="checkingMinio" @click="onCheckMinio">{{ $t('settings.storage.testConnection') }}</t-button>
            <span v-if="minioCheckResult" :class="['test-msg', minioCheckResult.ok ? (minioCheckResult.bucket_created ? 'created' : 'success') : 'error']">
              {{ minioCheckResult.message }}
            </span>
          </div>
        </div>

        <!-- Remote mode -->
        <div v-else>
          <div class="engine-hint">{{ $t('settings.storage.minioRemoteHint') }}</div>
          <div class="engine-form">
            <div class="form-field">
              <label>Endpoint</label>
              <t-input
                v-model="config.minio.endpoint"
                placeholder="e.g. minio.example.com:9000"
                clearable
              />
            </div>
            <div class="form-field">
              <label>Access Key ID</label>
              <t-input
                v-model="config.minio.access_key_id"
                placeholder="MinIO Access Key"
                clearable
              />
            </div>
            <div class="form-field">
              <label>Secret Access Key</label>
              <t-input
                v-model="config.minio.secret_access_key"
                type="password"
                placeholder="MinIO Secret Key"
                clearable
              />
            </div>
            <div class="form-field">
              <label>{{ $t('settings.storage.bucketName') }}</label>
              <t-input
                v-model="config.minio.bucket_name"
                :placeholder="$t('settings.storage.bucketPlaceholder')"
                clearable
              />
            </div>
            <div class="form-field form-field--inline">
              <label>Use SSL</label>
              <t-switch v-model="config.minio.use_ssl" size="small" />
            </div>
            <div class="form-field">
              <label>{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input
                v-model="config.minio.path_prefix"
                :placeholder="$t('settings.storage.prefixPlaceholder')"
                clearable
              />
            </div>
          </div>
          <div class="test-bar">
            <t-button size="small" variant="outline" :loading="checkingMinio" @click="onCheckMinio">{{ $t('settings.storage.testConnection') }}</t-button>
            <span v-if="minioCheckResult" :class="['test-msg', minioCheckResult.ok ? (minioCheckResult.bucket_created ? 'created' : 'success') : 'error']">
              {{ minioCheckResult.message }}
            </span>
          </div>
        </div>
      </div>

      <!-- COS -->
      <div class="engine-section" data-model-type="cos">
        <div class="engine-header">
          <div class="engine-header-info">
            <div class="engine-title-row">
              <h3>{{ $t('settings.storage.cosTitle') }}</h3>
              <t-tag theme="success" variant="light" size="small">{{ $t('settings.storage.configurable') }}</t-tag>
            </div>
            <p>
              {{ $t('settings.storage.cosDesc') }}
              <a class="engine-link" href="https://console.cloud.tencent.com/cos" target="_blank" rel="noopener">{{ $t('settings.storage.console') }} ↗</a>
              <a class="engine-link" href="https://cloud.tencent.com/document/product/436" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }} ↗</a>
            </p>
          </div>
        </div>
        <div class="engine-form">
          <div class="form-field">
            <label>Secret ID</label>
            <t-input
              v-model="config.cos.secret_id"
              :placeholder="$t('settings.storage.cosSecretIdPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>Secret Key</label>
            <t-input
              v-model="config.cos.secret_key"
              type="password"
              :placeholder="$t('settings.storage.cosSecretKeyPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>Region</label>
            <t-input
              v-model="config.cos.region"
              placeholder="e.g. ap-guangzhou"
              clearable
            />
          </div>
          <div class="form-field">
            <label>{{ $t('settings.storage.bucketName') }}</label>
            <t-input
              v-model="config.cos.bucket_name"
              :placeholder="$t('settings.storage.bucketPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>App ID</label>
            <t-input
              v-model="config.cos.app_id"
              :placeholder="$t('settings.storage.cosAppIdPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>{{ $t('settings.storage.pathPrefix') }}</label>
            <t-input
              v-model="config.cos.path_prefix"
              :placeholder="$t('settings.storage.prefixPlaceholder')"
              clearable
            />
          </div>
        </div>
        <div class="test-bar">
          <t-button size="small" variant="outline" :loading="checkingCos" @click="onCheckCos">{{ $t('settings.storage.testConnection') }}</t-button>
          <span v-if="cosCheckResult" :class="['test-msg', cosCheckResult.ok ? 'success' : 'error']">
            {{ cosCheckResult.message }}
          </span>
        </div>
      </div>

      <!-- TOS -->
      <div class="engine-section" data-model-type="tos">
        <div class="engine-header">
          <div class="engine-header-info">
            <div class="engine-title-row">
              <h3>{{ $t('settings.storage.tosTitle') }}</h3>
              <t-tag theme="success" variant="light" size="small">{{ $t('settings.storage.configurable') }}</t-tag>
            </div>
            <p>
              {{ $t('settings.storage.tosDesc') }}
              <a class="engine-link" href="https://console.volcengine.com/tos" target="_blank" rel="noopener">{{ $t('settings.storage.console') }} ↗</a>
              <a class="engine-link" href="https://www.volcengine.com/docs/6349" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }} ↗</a>
            </p>
          </div>
        </div>
        <div class="engine-form">
          <div class="form-field">
            <label>Endpoint</label>
            <t-input
              v-model="config.tos.endpoint"
              placeholder="e.g. https://tos-cn-beijing.volces.com"
              clearable
            />
          </div>
          <div class="form-field">
            <label>Region</label>
            <t-input
              v-model="config.tos.region"
              placeholder="e.g. cn-beijing"
              clearable
            />
          </div>
          <div class="form-field">
            <label>Access Key</label>
            <t-input
              v-model="config.tos.access_key"
              :placeholder="$t('settings.storage.tosAccessKeyPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>Secret Key</label>
            <t-input
              v-model="config.tos.secret_key"
              type="password"
              :placeholder="$t('settings.storage.tosSecretKeyPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>{{ $t('settings.storage.bucketName') }}</label>
            <t-input
              v-model="config.tos.bucket_name"
              :placeholder="$t('settings.storage.bucketPlaceholder')"
              clearable
            />
          </div>
          <div class="form-field">
            <label>{{ $t('settings.storage.pathPrefix') }}</label>
            <t-input
              v-model="config.tos.path_prefix"
              :placeholder="$t('settings.storage.prefixPlaceholder')"
              clearable
            />
          </div>
        </div>
        <div class="test-bar">
          <t-button size="small" variant="outline" :loading="checkingTos" @click="onCheckTos">{{ $t('settings.storage.testConnection') }}</t-button>
          <span v-if="tosCheckResult" :class="['test-msg', tosCheckResult.ok ? 'success' : 'error']">
            {{ tosCheckResult.message }}
          </span>
        </div>
      </div>

      <!-- Save -->
      <div class="save-bar">
        <t-button theme="primary" :loading="saving" @click="onSave">{{ $t('settings.storage.saveConfig') }}</t-button>
        <span v-if="saveMessage" :class="['save-msg', saveSuccess ? 'success' : 'error']">
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
  getStorageEngineConfig,
  updateStorageEngineConfig,
  getStorageEngineStatus,
  listMinioBuckets,
  checkStorageEngine,
  type StorageEngineConfig,
  type MinioBucketInfo,
} from '@/api/system'

const { t } = useI18n()

const defaultConfig = (): StorageEngineConfig => ({
  default_provider: 'local',
  local: { path_prefix: '' },
  minio: { mode: 'docker', endpoint: '', access_key_id: '', secret_access_key: '', bucket_name: '', use_ssl: false, path_prefix: '' },
  cos: {
    secret_id: '',
    secret_key: '',
    region: '',
    bucket_name: '',
    app_id: '',
    path_prefix: '',
  },
  tos: {
    endpoint: '',
    region: '',
    access_key: '',
    secret_key: '',
    bucket_name: '',
    path_prefix: '',
  },
})

const loading = ref(true)
const error = ref('')
const config = ref<StorageEngineConfig>(defaultConfig())
const engineStatus = ref<{ local: boolean; minio: boolean; cos: boolean }>({
  local: true,
  minio: false,
  cos: true,
})
const minioEnvAvailable = ref(false)
const minioBuckets = ref<MinioBucketInfo[]>([])
const loadingBuckets = ref(false)
const saving = ref(false)
const saveMessage = ref('')
const saveSuccess = ref(false)

const checkingMinio = ref(false)
const minioCheckResult = ref<{ ok: boolean; message: string; bucket_created?: boolean } | null>(null)
const checkingCos = ref(false)
const cosCheckResult = ref<{ ok: boolean; message: string } | null>(null)
const checkingTos = ref(false)
const tosCheckResult = ref<{ ok: boolean; message: string } | null>(null)

const minioAvailable = computed(() => {
  if (config.value.minio?.mode === 'remote') {
    return !!(config.value.minio.endpoint && config.value.minio.access_key_id && config.value.minio.secret_access_key)
  }
  return minioEnvAvailable.value
})

async function loadConfig() {
  try {
    const res = await getStorageEngineConfig()
    const d = res?.data
    if (d) {
      config.value = {
        default_provider: d.default_provider || 'local',
        local: d.local ? { path_prefix: d.local.path_prefix || '' } : { path_prefix: '' },
        minio: d.minio
          ? {
              mode: d.minio.mode || 'docker',
              endpoint: d.minio.endpoint || '',
              access_key_id: d.minio.access_key_id || '',
              secret_access_key: d.minio.secret_access_key || '',
              bucket_name: d.minio.bucket_name || '',
              use_ssl: d.minio.use_ssl ?? false,
              path_prefix: d.minio.path_prefix || '',
            }
          : defaultConfig().minio!,
        cos: d.cos
          ? {
              secret_id: d.cos.secret_id || '',
              secret_key: d.cos.secret_key || '',
              region: d.cos.region || '',
              bucket_name: d.cos.bucket_name || '',
              app_id: d.cos.app_id || '',
              path_prefix: d.cos.path_prefix || '',
            }
          : defaultConfig().cos!,
        tos: d.tos
          ? {
              endpoint: d.tos.endpoint || '',
              region: d.tos.region || '',
              access_key: d.tos.access_key || '',
              secret_key: d.tos.secret_key || '',
              bucket_name: d.tos.bucket_name || '',
              path_prefix: d.tos.path_prefix || '',
            }
          : defaultConfig().tos!,
      }
    }
  } catch {
    config.value = defaultConfig()
  }
}

async function loadStatus() {
  try {
    const res = await getStorageEngineStatus()
    const engines = res?.data?.engines ?? []
    const status = { local: true, minio: false, cos: true }
    for (const e of engines) {
      if (e.name === 'local') status.local = e.available
      if (e.name === 'minio') status.minio = e.available
      if (e.name === 'cos') status.cos = e.available
    }
    engineStatus.value = status
    minioEnvAvailable.value = res?.data?.minio_env_available ?? false
  } catch {
    engineStatus.value = { local: true, minio: false, cos: true }
    minioEnvAvailable.value = false
  }
}

async function loadMinioBuckets() {
  if (!minioEnvAvailable.value || loadingBuckets.value) return
  loadingBuckets.value = true
  try {
    const res = await listMinioBuckets()
    if (res?.data?.buckets) {
      minioBuckets.value = res.data.buckets
    }
  } catch {
    minioBuckets.value = []
  } finally {
    loadingBuckets.value = false
  }
}

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([loadConfig(), loadStatus()])
    if (minioEnvAvailable.value) loadMinioBuckets()
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : t('settings.storage.loadFailed')
  } finally {
    loading.value = false
  }
}

function buildPayload(): StorageEngineConfig {
  const mode = config.value.minio?.mode || 'docker'
  return {
    default_provider: config.value.default_provider || 'local',
    local: { path_prefix: (config.value.local?.path_prefix || '').trim() },
    minio: {
      mode,
      endpoint: mode === 'remote' ? (config.value.minio?.endpoint || '').trim() : '',
      access_key_id: mode === 'remote' ? (config.value.minio?.access_key_id || '').trim() : '',
      secret_access_key: mode === 'remote' ? (config.value.minio?.secret_access_key || '').trim() : '',
      bucket_name: (config.value.minio?.bucket_name || '').trim(),
      use_ssl: config.value.minio?.use_ssl ?? false,
      path_prefix: (config.value.minio?.path_prefix || '').trim(),
    },
    cos: {
      secret_id: (config.value.cos?.secret_id || '').trim(),
      secret_key: (config.value.cos?.secret_key || '').trim(),
      region: (config.value.cos?.region || '').trim(),
      bucket_name: (config.value.cos?.bucket_name || '').trim(),
      app_id: (config.value.cos?.app_id || '').trim(),
      path_prefix: (config.value.cos?.path_prefix || '').trim(),
    },
    tos: {
      endpoint: (config.value.tos?.endpoint || '').trim(),
      region: (config.value.tos?.region || '').trim(),
      access_key: (config.value.tos?.access_key || '').trim(),
      secret_key: (config.value.tos?.secret_key || '').trim(),
      bucket_name: (config.value.tos?.bucket_name || '').trim(),
      path_prefix: (config.value.tos?.path_prefix || '').trim(),
    },
  }
}

async function onSave() {
  saving.value = true
  saveMessage.value = ''
  try {
    await updateStorageEngineConfig(buildPayload())
    await loadStatus()
    saveSuccess.value = true
    saveMessage.value = t('settings.storage.saveSuccess')
  } catch (e: unknown) {
    saveSuccess.value = false
    saveMessage.value = e instanceof Error ? e.message : t('settings.storage.saveFailed')
  } finally {
    saving.value = false
  }
}

async function onCheckMinio() {
  checkingMinio.value = true
  minioCheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'minio', minio: payload.minio })
    minioCheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
    // Refresh bucket list if a new bucket was auto-created
    if (res?.data?.bucket_created) {
      loadMinioBuckets()
    }
  } catch (e: unknown) {
    minioCheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingMinio.value = false
  }
}

async function onCheckCos() {
  checkingCos.value = true
  cosCheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'cos', cos: payload.cos })
    cosCheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
  } catch (e: unknown) {
    cosCheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingCos.value = false
  }
}

async function onCheckTos() {
  checkingTos.value = true
  tosCheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'tos', tos: payload.tos })
    tosCheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
  } catch (e: unknown) {
    tosCheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingTos.value = false
  }
}

onMounted(loadAll)
</script>

<style lang="less" scoped>
.storage-engine-settings {
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

.settings-group {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);

  &:last-child {
    border-bottom: none;
  }
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: var(--td-text-color-primary);
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: var(--td-text-color-secondary);
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
  display: flex;
  justify-content: flex-end;
  align-items: center;
}

.engine-section {
  margin-top: 32px;
  padding-top: 32px;
  border-top: 1px solid var(--td-component-stroke);
}

.engine-header {
  margin-bottom: 16px;
}

.engine-header-info {
  .engine-title-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 6px;

    h3 {
      font-size: 17px;
      font-weight: 600;
      color: var(--td-text-color-primary);
      margin: 0;
    }
  }

  p {
    font-size: 13px;
    color: var(--td-text-color-placeholder);
    margin: 0;
    line-height: 1.5;
  }
}

.engine-link {
  color: var(--td-text-color-placeholder);
  text-decoration: none;
  margin-left: 4px;

  &:hover {
    color: var(--td-brand-color);
  }
}

.engine-form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-field {
  display: flex;
  flex-direction: column;
  gap: 6px;

  label {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-secondary)555;
  }

  &--inline {
    flex-direction: row;
    align-items: center;
    gap: 12px;

    label {
      flex-shrink: 0;
    }
  }
}

.mode-selector {
  display: flex;
  gap: 8px;
  margin-bottom: 16px;
}

.mode-option {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  cursor: pointer;
  transition: all 0.2s;
  background: var(--td-bg-color-secondarycontainer);

  &:hover {
    border-color: var(--td-text-color-disabled);
  }

  &.active {
    border-color: var(--td-brand-color);
    background: rgba(7, 192, 95, 0.06);
  }

  .mode-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }
}

.engine-hint {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
  padding: 10px 14px;
  margin-bottom: 16px;
  border-radius: 6px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-stroke);

  &.success {
    color: var(--td-text-color-primary);
    background: var(--td-success-color-light);
    border-color: var(--td-success-color-focus);
  }

  &.warning {
    color: var(--td-text-color-primary);
    background: var(--td-warning-color-light);
    border-color: var(--td-warning-color-focus);
  }
}

.test-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--td-component-stroke);
}

.test-msg {
  font-size: 13px;

  &.success {
    color: var(--td-success-color);
  }

  &.created {
    color: var(--td-warning-color);
  }

  &.error {
    color: var(--td-error-color);
  }
}

.save-bar {
  display: flex;
  align-items: center;
  gap: 12px;
  position: sticky;
  bottom: 0;
  margin-top: 32px;
  padding: 16px 0 4px;
  background: linear-gradient(to bottom, rgba(255, 255, 255, 0) 0%, var(--td-bg-color-container) 12%);
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
}
</style>
