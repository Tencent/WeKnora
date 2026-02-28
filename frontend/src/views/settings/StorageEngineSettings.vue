<template>
  <div class="storage-engine-settings">
    <div class="section-header">
      <h2>存储引擎</h2>
      <p class="section-description">
        配置文档与图片的存储方式。此处设置各引擎参数，知识库中仅选择使用哪个引擎。存储引擎会影响文档上传存储以及文档内图片的存储。
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
      <div class="engine-section">
        <div class="section-title-row">
          <div class="section-title-info">
            <h3>引擎配置</h3>
            <p>为每种存储引擎配置参数，知识库创建或编辑时选择使用哪个引擎。</p>
          </div>
        </div>

        <div class="engine-list">
          <!-- Local -->
          <div class="engine-block">
            <div class="block-header">
              <span class="block-name">Local（本地存储）</span>
              <t-tag theme="success" variant="light" size="small">可用</t-tag>
            </div>
            <p class="block-desc">使用服务器本地文件系统存储文件，仅适合单机部署。</p>
            <div class="block-config">
              <div class="config-item">
                <label>路径前缀（可选）</label>
                <t-input
                  v-model="config.local.path_prefix"
                  size="small"
                  placeholder="如 weknora/images"
                  clearable
                />
              </div>
            </div>
          </div>

          <!-- MinIO -->
          <div class="engine-block">
            <div class="block-header">
              <span class="block-name">MinIO</span>
              <t-tag v-if="minioAvailable" theme="success" variant="light" size="small">可用</t-tag>
              <t-tag v-else theme="default" variant="light" size="small">需要配置</t-tag>
            </div>
            <p class="block-desc">S3 兼容的自托管对象存储，适合内网和私有云部署。</p>

            <div class="minio-mode-tabs">
              <div
                :class="['mode-tab', { active: config.minio.mode !== 'remote' }]"
                @click="config.minio.mode = 'docker'"
              >
                <span class="tab-label">Docker 部署</span>
                <t-tag v-if="minioEnvAvailable" theme="success" variant="light" size="small">已检测</t-tag>
                <t-tag v-else theme="default" variant="light" size="small">未检测到</t-tag>
              </div>
              <div
                :class="['mode-tab', { active: config.minio.mode === 'remote' }]"
                @click="config.minio.mode = 'remote'"
              >
                <span class="tab-label">远程 MinIO</span>
              </div>
            </div>

            <!-- Docker mode -->
            <div v-if="config.minio.mode !== 'remote'" class="minio-mode-content">
              <p v-if="minioEnvAvailable" class="mode-hint success">
                已检测到 Docker 部署的 MinIO 环境变量，连接信息（Endpoint、Access Key）由环境变量提供，无需手动填写。
              </p>
              <p v-else class="mode-hint warning">
                未检测到 MinIO 环境变量（MINIO_ENDPOINT、MINIO_ACCESS_KEY_ID、MINIO_SECRET_ACCESS_KEY），请确认 Docker Compose 配置正确。
              </p>
              <div class="block-config">
                <div class="config-item">
                  <label>Bucket 名称</label>
                  <t-select
                    v-model="config.minio.bucket_name"
                    size="small"
                    filterable
                    creatable
                    placeholder="选择或输入 Bucket"
                    :loading="loadingBuckets"
                    :disabled="!minioEnvAvailable"
                    @focus="loadMinioBuckets"
                    style="width: 100%;"
                  >
                    <t-option
                      v-for="b in minioBuckets"
                      :key="b.name"
                      :value="b.name"
                      :label="b.name"
                    />
                  </t-select>
                </div>
                <div class="config-item config-item--inline">
                  <label>Use SSL</label>
                  <t-switch v-model="config.minio.use_ssl" size="small" />
                </div>
                <div class="config-item">
                  <label>路径前缀（可选）</label>
                  <t-input
                    v-model="config.minio.path_prefix"
                    size="small"
                    placeholder="如 weknora"
                    clearable
                  />
                </div>
              </div>
              <div v-if="minioEnvAvailable" class="check-bar">
                <t-button size="small" variant="outline" :loading="checkingMinio" @click="onCheckMinio">测试连接</t-button>
                <span v-if="minioCheckResult" :class="['check-msg', minioCheckResult.ok ? 'success' : 'error']">
                  {{ minioCheckResult.message }}
                </span>
              </div>
            </div>

            <!-- Remote mode -->
            <div v-else class="minio-mode-content">
              <p class="mode-hint">连接到远程 MinIO 服务，需要手动填写连接信息。</p>
              <div class="block-config">
                <div class="config-item">
                  <label>Endpoint</label>
                  <t-input
                    v-model="config.minio.endpoint"
                    size="small"
                    placeholder="如 minio.example.com:9000"
                    clearable
                  />
                </div>
                <div class="config-item">
                  <label>Access Key ID</label>
                  <t-input
                    v-model="config.minio.access_key_id"
                    size="small"
                    placeholder="MinIO Access Key"
                    clearable
                  />
                </div>
                <div class="config-item">
                  <label>Secret Access Key</label>
                  <t-input
                    v-model="config.minio.secret_access_key"
                    size="small"
                    type="password"
                    placeholder="MinIO Secret Key"
                    clearable
                  />
                </div>
                <div class="config-item">
                  <label>Bucket 名称</label>
                  <t-input
                    v-model="config.minio.bucket_name"
                    size="small"
                    placeholder="存储桶名称"
                    clearable
                  />
                </div>
                <div class="config-item config-item--inline">
                  <label>Use SSL</label>
                  <t-switch v-model="config.minio.use_ssl" size="small" />
                </div>
                <div class="config-item">
                  <label>路径前缀（可选）</label>
                  <t-input
                    v-model="config.minio.path_prefix"
                    size="small"
                    placeholder="如 weknora"
                    clearable
                  />
                </div>
              </div>
              <div class="check-bar">
                <t-button size="small" variant="outline" :loading="checkingMinio" @click="onCheckMinio">测试连接</t-button>
                <span v-if="minioCheckResult" :class="['check-msg', minioCheckResult.ok ? 'success' : 'error']">
                  {{ minioCheckResult.message }}
                </span>
              </div>
            </div>
          </div>

          <!-- COS -->
          <div class="engine-block">
            <div class="block-header">
              <span class="block-name">腾讯云 COS</span>
              <t-tag theme="success" variant="light" size="small">可配置</t-tag>
            </div>
            <p class="block-desc">腾讯云对象存储服务，适合公有云部署，支持 CDN 加速。<a class="engine-link" href="https://console.cloud.tencent.com/cos" target="_blank" rel="noopener">控制台</a> · <a class="engine-link" href="https://cloud.tencent.com/document/product/436" target="_blank" rel="noopener">文档</a></p>
            <div class="block-config">
              <div class="config-item">
                <label>Secret ID</label>
                <t-input
                  v-model="config.cos.secret_id"
                  size="small"
                  placeholder="腾讯云 API 密钥 SecretId"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Secret Key</label>
                <t-input
                  v-model="config.cos.secret_key"
                  size="small"
                  type="password"
                  placeholder="腾讯云 API 密钥 SecretKey"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Region</label>
                <t-input
                  v-model="config.cos.region"
                  size="small"
                  placeholder="如 ap-guangzhou"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Bucket 名称</label>
                <t-input
                  v-model="config.cos.bucket_name"
                  size="small"
                  placeholder="存储桶名称"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>App ID</label>
                <t-input
                  v-model="config.cos.app_id"
                  size="small"
                  placeholder="腾讯云账号 AppID"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>路径前缀（可选）</label>
                <t-input
                  v-model="config.cos.path_prefix"
                  size="small"
                  placeholder="如 weknora"
                  clearable
                />
              </div>
            </div>
            <div class="check-bar">
              <t-button size="small" variant="outline" :loading="checkingCos" @click="onCheckCos">测试连接</t-button>
              <span v-if="cosCheckResult" :class="['check-msg', cosCheckResult.ok ? 'success' : 'error']">
                {{ cosCheckResult.message }}
              </span>
            </div>
          </div>

          <!-- TOS -->
          <div class="engine-block">
            <div class="block-header">
              <span class="block-name">火山引擎 TOS</span>
              <t-tag theme="success" variant="light" size="small">可配置</t-tag>
            </div>
            <p class="block-desc">火山引擎对象存储服务（TOS），适合公有云部署。<a class="engine-link" href="https://console.volcengine.com/tos" target="_blank" rel="noopener">控制台</a> · <a class="engine-link" href="https://www.volcengine.com/docs/6349" target="_blank" rel="noopener">文档</a></p>
            <div class="block-config">
              <div class="config-item">
                <label>Endpoint</label>
                <t-input
                  v-model="config.tos.endpoint"
                  size="small"
                  placeholder="如 https://tos-cn-beijing.volces.com"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Region</label>
                <t-input
                  v-model="config.tos.region"
                  size="small"
                  placeholder="如 cn-beijing"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Access Key</label>
                <t-input
                  v-model="config.tos.access_key"
                  size="small"
                  placeholder="火山引擎 Access Key"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Secret Key</label>
                <t-input
                  v-model="config.tos.secret_key"
                  size="small"
                  type="password"
                  placeholder="火山引擎 Secret Key"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>Bucket 名称</label>
                <t-input
                  v-model="config.tos.bucket_name"
                  size="small"
                  placeholder="存储桶名称"
                  clearable
                />
              </div>
              <div class="config-item">
                <label>路径前缀（可选）</label>
                <t-input
                  v-model="config.tos.path_prefix"
                  size="small"
                  placeholder="如 weknora"
                  clearable
                />
              </div>
            </div>
            <div class="check-bar">
              <t-button size="small" variant="outline" :loading="checkingTos" @click="onCheckTos">测试连接</t-button>
              <span v-if="tosCheckResult" :class="['check-msg', tosCheckResult.ok ? 'success' : 'error']">
                {{ tosCheckResult.message }}
              </span>
            </div>
          </div>
        </div>
      </div>

      <div class="default-and-save">
        <div class="default-row">
          <label>默认引擎</label>
          <t-select
            v-model="config.default_provider"
            size="small"
            style="width: 200px;"
          >
            <t-option value="local" label="Local（本地）" />
            <t-option value="minio" label="MinIO" />
            <t-option value="cos" label="腾讯云 COS" />
            <t-option value="tos" label="火山引擎 TOS" />
          </t-select>
          <span class="hint">新建知识库时默认选用的存储引擎</span>
        </div>
        <div class="save-bar">
          <t-button theme="primary" :loading="saving" @click="onSave">保存配置</t-button>
          <span v-if="saveMessage" :class="['save-msg', saveSuccess ? 'success' : 'error']">
            {{ saveMessage }}
          </span>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import {
  getStorageEngineConfig,
  updateStorageEngineConfig,
  getStorageEngineStatus,
  listMinioBuckets,
  checkStorageEngine,
  type StorageEngineConfig,
  type MinioBucketInfo,
} from '@/api/system'

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
const minioCheckResult = ref<{ ok: boolean; message: string } | null>(null)
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
    error.value = e instanceof Error ? e.message : '加载失败'
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
    saveMessage.value = '保存成功'
  } catch (e: unknown) {
    saveSuccess.value = false
    saveMessage.value = e instanceof Error ? e.message : '保存失败'
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
    minioCheckResult.value = res?.data ?? { ok: false, message: '未知错误' }
  } catch (e: unknown) {
    minioCheckResult.value = { ok: false, message: e instanceof Error ? e.message : '请求失败' }
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
    cosCheckResult.value = res?.data ?? { ok: false, message: '未知错误' }
  } catch (e: unknown) {
    cosCheckResult.value = { ok: false, message: e instanceof Error ? e.message : '请求失败' }
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
    tosCheckResult.value = res?.data ?? { ok: false, message: '未知错误' }
  } catch (e: unknown) {
    tosCheckResult.value = { ok: false, message: e instanceof Error ? e.message : '请求失败' }
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

.loading-state,
.error-inline {
  padding: 24px 0;
}

.loading-state {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #999;
}

.engine-section {
  margin-top: 0;
}

.section-title-row {
  margin-bottom: 20px;

  h3 {
    font-size: 17px;
    font-weight: 600;
    color: #333;
    margin: 0 0 6px 0;
  }

  p {
    font-size: 13px;
    color: #999;
    margin: 0;
    line-height: 1.5;
  }
}

.engine-list {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.engine-block {
  padding: 20px;
  border: 1px solid #e5e7eb;
  border-radius: 10px;
  background: #fff;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.block-header {
  display: flex;
  align-items: center;
  gap: 10px;
}

.block-name {
  font-size: 16px;
  font-weight: 600;
  color: #333;
}

.block-desc {
  font-size: 13px;
  color: #666;
  margin: 0;
  line-height: 1.5;
}

.engine-link {
  color: var(--td-brand-color, #0052d9);
  text-decoration: none;
  margin-left: 4px;

  &:hover {
    text-decoration: underline;
  }
}

.block-config {
  margin-top: 6px;
  padding-top: 14px;
  border-top: 1px dashed #e5e7eb;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: 14px 24px;
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

  &--inline {
    flex-direction: row;
    align-items: center;
    gap: 10px;

    label {
      flex-shrink: 0;
    }
  }
}

// ---- MinIO mode tabs ----
.minio-mode-tabs {
  display: flex;
  gap: 8px;
  margin-top: 4px;
}

.mode-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  border: 1px solid #e5e7eb;
  border-radius: 8px;
  cursor: pointer;
  transition: all 0.15s;
  background: #fafafa;

  &:hover {
    border-color: #c0c4cc;
  }

  &.active {
    border-color: var(--td-brand-color, #0052d9);
    background: var(--td-brand-color-light, #f0f5ff);
  }

  .tab-label {
    font-size: 13px;
    font-weight: 500;
    color: #333;
  }
}

.minio-mode-content {
  margin-top: 4px;
}

.mode-hint {
  font-size: 13px;
  color: #666;
  margin: 0 0 4px 0;
  line-height: 1.5;

  &.success {
    color: #52c41a;
  }

  &.warning {
    color: #e6a23c;
  }
}

.default-and-save {
  margin-top: 24px;
  padding-top: 24px;
  border-top: 1px solid #e5e7eb;
}

.default-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;

  label {
    font-size: 14px;
    font-weight: 500;
    color: #333;
  }

  .hint {
    font-size: 13px;
    color: #999;
  }
}

.save-bar {
  display: flex;
  align-items: center;
  gap: 12px;
}

.save-msg {
  font-size: 13px;

  &.success {
    color: #52c41a;
  }

  &.error {
    color: #ff4d4f;
  }
}

.check-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
  padding-top: 12px;
  border-top: 1px dashed #f0f0f0;
}

.check-msg {
  font-size: 13px;

  &.success {
    color: #52c41a;
  }

  &.error {
    color: #ff4d4f;
  }
}
</style>
