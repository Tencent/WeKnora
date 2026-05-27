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
            <t-select
              v-model="config.default_provider"
              style="width: 280px;"
              :placeholder="$t('settings.storage.defaultEngine')"
              :disabled="!hasAllowedProviders"
              @change="onSaveDefaultEngine"
            >
              <t-option
                v-for="opt in providerOptions"
                :key="opt.value"
                :value="opt.value"
                :label="opt.label"
                :disabled="!opt.allowed"
              />
            </t-select>
            <span v-if="saveMessage && !drawerVisible" :class="['save-msg', saveSuccess ? 'success' : 'error']" style="margin-left: 12px;">
              {{ saveMessage }}
            </span>
          </div>
        </div>
      </div>

      <!-- 与其它 settings 列表同形：左侧 monogram 徽章 + 标题 + 状态徽 + 描述。
           整张卡是一个 button，单击打开配置抽屉；当前抽屉对应的卡获得品牌色描边。
           原本 8 张手写卡片由统一的 STORAGE_PROVIDERS 数组驱动，把状态判定收敛到
           providerStatus()，新增 provider 时只需在数组里加一项 + 翻译键即可。 -->
      <div class="engine-cards">
        <button
          v-for="provider in STORAGE_PROVIDERS"
          v-show="isProviderAllowed(provider.id)"
          :key="provider.id"
          type="button"
          class="engine-card"
          :class="[
            `engine-card--${provider.id}`,
            { 'engine-card--active': drawerVisible && currentEngine === provider.id }
          ]"
          @click="openDrawer(provider.id)"
        >
          <div
            class="engine-card__badge"
            :class="badgeClass(provider.id)"
            :style="badgeStyle(provider.id)"
            :aria-label="provider.id"
          >
            <img
              v-if="resolveLogo(provider.id)?.mode === 'color'"
              :src="resolveLogo(provider.id)!.url"
              :alt="provider.id"
              class="engine-card__badge-img"
            />
            <template v-else-if="!resolveLogo(provider.id)">{{ providerInitial(provider.id) }}</template>
          </div>
          <div class="engine-card__body">
            <div class="engine-card__header">
              <h3 class="engine-card__title">{{ providerTitle(provider.id) }}</h3>
              <span
                class="engine-card__status"
                :class="`engine-card__status--${providerStatus(provider.id).kind}`"
              >
                <span class="engine-card__status-dot" />
                {{ providerStatus(provider.id).label }}
              </span>
            </div>
            <p class="engine-card__desc">{{ $t(`settings.storage.${provider.id}Desc`) }}</p>
          </div>
        </button>
      </div>
    </template>

    <t-drawer
      v-model:visible="drawerVisible"
      :header="drawerTitle"
      size="500px"
      :footer="true"
      @confirm="onSave"
    >
      <div class="drawer-content">
        <template v-if="currentEngine === 'local'">
          <div class="engine-info-block">
            <p class="engine-desc">{{ $t('settings.storage.localDesc') }}</p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input
                v-model="config.local.path_prefix"
                :placeholder="$t('settings.storage.pathPrefixPlaceholder')"
                clearable
              />
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 'minio'">
          <div class="engine-info-block">
            <p class="engine-desc">{{ $t('settings.storage.minioDesc') }}</p>
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

          <div v-if="config.minio.mode !== 'remote'">
            <div v-if="minioEnvAvailable" class="engine-hint success">
              {{ $t('settings.storage.minioDockerDetected') }}
            </div>
            <div v-else class="engine-hint warning">
              {{ $t('settings.storage.minioDockerNotDetected') }}
            </div>
            <div class="engine-form">
              <div class="form-item">
                <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
                <t-input
                  v-model="config.minio.bucket_name"
                  :placeholder="$t('settings.storage.bucketPlaceholder')"
                  :disabled="!minioEnvAvailable"
                  clearable
                />
              </div>
              <div class="form-item form-item--inline">
                <label class="form-label">Use SSL</label>
                <t-switch v-model="config.minio.use_ssl" size="small" />
              </div>
              <div class="form-item">
                <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
                <t-input
                  v-model="config.minio.path_prefix"
                  :placeholder="$t('settings.storage.prefixPlaceholder')"
                  clearable
                />
              </div>
            </div>
          </div>

          <div v-else>
            <div class="engine-hint">{{ $t('settings.storage.minioRemoteHint') }}</div>
            <div class="engine-form">
              <div class="form-item">
                <label class="form-label">Endpoint</label>
                <t-input v-model="config.minio.endpoint" placeholder="e.g. minio.example.com:9000" clearable />
              </div>
              <div class="form-item">
                <label class="form-label">Access Key ID</label>
                <t-input v-model="config.minio.access_key_id" placeholder="MinIO Access Key" clearable />
              </div>
              <div class="form-item">
                <label class="form-label">Secret Access Key</label>
                <t-input v-model="config.minio.secret_access_key" type="password" placeholder="MinIO Secret Key" clearable />
              </div>
              <div class="form-item">
                <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
                <t-input v-model="config.minio.bucket_name" :placeholder="$t('settings.storage.bucketPlaceholder')" clearable />
              </div>
              <div class="form-item form-item--inline">
                <label class="form-label">Use SSL</label>
                <t-switch v-model="config.minio.use_ssl" size="small" />
              </div>
              <div class="form-item">
                <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
                <t-input v-model="config.minio.path_prefix" :placeholder="$t('settings.storage.prefixPlaceholder')" clearable />
              </div>
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 'cos'">
          <div class="engine-info-block">
            <p class="engine-desc">
              {{ $t('settings.storage.cosDesc') }}
              <a class="doc-link" href="https://console.cloud.tencent.com/cos" target="_blank" rel="noopener">{{ $t('settings.storage.console') }}<t-icon name="link" class="link-icon" /></a>
              <a class="doc-link" href="https://cloud.tencent.com/document/product/436" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }}<t-icon name="link" class="link-icon" /></a>
            </p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">Secret ID</label>
              <t-input v-model="config.cos.secret_id" :placeholder="$t('settings.storage.cosSecretIdPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Secret Key</label>
              <t-input v-model="config.cos.secret_key" type="password" :placeholder="$t('settings.storage.cosSecretKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Region</label>
              <t-input v-model="config.cos.region" placeholder="e.g. ap-guangzhou" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
              <t-input v-model="config.cos.bucket_name" :placeholder="$t('settings.storage.bucketPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">App ID</label>
              <t-input v-model="config.cos.app_id" :placeholder="$t('settings.storage.cosAppIdPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input v-model="config.cos.path_prefix" :placeholder="$t('settings.storage.prefixPlaceholder')" clearable />
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 'tos'">
          <div class="engine-info-block">
            <p class="engine-desc">
              {{ $t('settings.storage.tosDesc') }}
              <a class="doc-link" href="https://console.volcengine.com/tos" target="_blank" rel="noopener">{{ $t('settings.storage.console') }}<t-icon name="link" class="link-icon" /></a>
              <a class="doc-link" href="https://www.volcengine.com/docs/6349" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }}<t-icon name="link" class="link-icon" /></a>
            </p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">Endpoint</label>
              <t-input v-model="config.tos.endpoint" placeholder="e.g. https://tos-cn-beijing.volces.com" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Region</label>
              <t-input v-model="config.tos.region" placeholder="e.g. cn-beijing" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Access Key</label>
              <t-input v-model="config.tos.access_key" :placeholder="$t('settings.storage.tosAccessKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Secret Key</label>
              <t-input v-model="config.tos.secret_key" type="password" :placeholder="$t('settings.storage.tosSecretKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
              <t-input v-model="config.tos.bucket_name" :placeholder="$t('settings.storage.bucketPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input v-model="config.tos.path_prefix" :placeholder="$t('settings.storage.prefixPlaceholder')" clearable />
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 's3'">
          <div class="engine-info-block">
            <p class="engine-desc">
              {{ $t('settings.storage.s3Desc') }}
              <a class="doc-link" href="https://aws.amazon.com/s3/" target="_blank" rel="noopener">{{ $t('settings.storage.console') }}<t-icon name="link" class="link-icon" /></a>
              <a class="doc-link" href="https://docs.aws.amazon.com/s3/" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }}<t-icon name="link" class="link-icon" /></a>
            </p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">Endpoint</label>
              <t-input v-model="config.s3.endpoint" placeholder="e.g. https://s3.amazonaws.com" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Region</label>
              <t-input v-model="config.s3.region" placeholder="e.g. us-east-1" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Access Key</label>
              <t-input v-model="config.s3.access_key" :placeholder="$t('settings.storage.s3AccessKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Secret Key</label>
              <t-input v-model="config.s3.secret_key" type="password" :placeholder="$t('settings.storage.s3SecretKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
              <t-input v-model="config.s3.bucket_name" :placeholder="$t('settings.storage.bucketPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input v-model="config.s3.path_prefix" :placeholder="$t('settings.storage.prefixPlaceholder')" clearable />
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 'oss'">
          <div class="engine-info-block">
            <p class="engine-desc">
              {{ $t('settings.storage.ossDesc') }}
              <a class="doc-link" href="https://oss.console.aliyun.com/" target="_blank" rel="noopener">{{ $t('settings.storage.console') }}<t-icon name="link" class="link-icon" /></a>
              <a class="doc-link" href="https://help.aliyun.com/zh/oss/" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }}<t-icon name="link" class="link-icon" /></a>
            </p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">Endpoint</label>
              <t-input v-model="config.oss.endpoint" placeholder="e.g. https://oss-cn-hangzhou.aliyuncs.com" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Region</label>
              <t-input v-model="config.oss.region" placeholder="e.g. cn-hangzhou" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Access Key</label>
              <t-input v-model="config.oss.access_key" :placeholder="$t('settings.storage.ossAccessKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">Secret Key</label>
              <t-input v-model="config.oss.secret_key" type="password" :placeholder="$t('settings.storage.ossSecretKeyPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
              <t-input v-model="config.oss.bucket_name" :placeholder="$t('settings.storage.bucketPlaceholder')" clearable />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input v-model="config.oss.path_prefix" :placeholder="$t('settings.storage.prefixPlaceholder')" clearable />
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 'ks3'">
          <div class="engine-info-block">
            <p class="engine-desc">
              {{ $t('settings.storage.ks3Desc') }}
            </p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">Endpoint</label>
              <t-input
                v-model="config.ks3.endpoint"
                :placeholder="$t('settings.storage.ks3EndpointPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">Region</label>
              <t-input
                v-model="config.ks3.region"
                :placeholder="$t('settings.storage.ks3RegionPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">Access Key</label>
              <t-input
                v-model="config.ks3.access_key"
                :placeholder="$t('settings.storage.ks3AccessKeyPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">Secret Key</label>
              <t-input
                v-model="config.ks3.secret_key"
                type="password"
                :placeholder="$t('settings.storage.ks3SecretKeyPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
              <t-input
                v-model="config.ks3.bucket_name"
                :placeholder="$t('settings.storage.bucketPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input
                v-model="config.ks3.path_prefix"
                :placeholder="$t('settings.storage.prefixPlaceholder')"
                clearable
              />
            </div>
          </div>
        </template>

        <template v-else-if="currentEngine === 'obs'">
          <div class="engine-info-block">
            <p class="engine-desc">
              {{ $t('settings.storage.obsDesc') }}
              <a class="engine-link" href="https://obs.huaweicloud.com/" target="_blank" rel="noopener">{{ $t('settings.storage.console') }} ↗</a>
              <a class="engine-link" href="https://support.huaweicloud.com/obs/" target="_blank" rel="noopener">{{ $t('settings.storage.docs') }} ↗</a>
            </p>
          </div>
          <div class="engine-form">
            <div class="form-item">
              <label class="form-label">Endpoint</label>
              <t-input
                v-model="config.obs.endpoint"
                :placeholder="$t('settings.storage.obsEndpointPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">Region</label>
              <t-input
                v-model="config.obs.region"
                :placeholder="$t('settings.storage.obsRegionPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">Access Key</label>
              <t-input
                v-model="config.obs.access_key"
                :placeholder="$t('settings.storage.obsAccessKeyPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">Secret Key</label>
              <t-input
                v-model="config.obs.secret_key"
                type="password"
                :placeholder="$t('settings.storage.obsSecretKeyPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.bucketName') }}</label>
              <t-input
                v-model="config.obs.bucket_name"
                :placeholder="$t('settings.storage.bucketPlaceholder')"
                clearable
              />
            </div>
            <div class="form-item">
              <label class="form-label">{{ $t('settings.storage.pathPrefix') }}</label>
              <t-input
                v-model="config.obs.path_prefix"
                :placeholder="$t('settings.storage.prefixPlaceholder')"
                clearable
              />
            </div>
          </div>
        </template>

        <div class="form-item" v-if="currentEngine && currentEngine !== 'local'">
          <label class="form-label">{{ $t('settings.storage.testConnection') }}</label>
          <div class="api-test-section">
            <t-button variant="outline" :loading="currentCheckState.loading" @click="currentCheckState.onCheck">
              {{ $t('settings.storage.testConnection') }}
            </t-button>
            <span v-if="currentCheckState.result" :class="['test-message', currentCheckState.result.ok ? (currentCheckState.result.bucket_created ? 'created' : 'success') : 'error']">
              {{ currentCheckState.result.message }}
            </span>
          </div>
        </div>
      </div>

      <template #footer>
        <div class="drawer-footer-actions">
          <t-button theme="default" variant="outline" @click="drawerVisible = false">{{ $t('common.cancel') }}</t-button>
          <t-button v-if="authStore.hasRole('admin')" theme="primary" :loading="saving" @click="onSave">{{ $t('common.save') }}</t-button>
        </div>
      </template>
    </t-drawer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  checkStorageEngine,
  getStorageEngineConfig,
  getStorageEngineStatus,
  updateStorageEngineConfig,
  type StorageEngineConfig,
} from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import { providerLogo } from './providerLogos'

const { t } = useI18n()
const authStore = useAuthStore()

const defaultConfig = (): StorageEngineConfig => ({
  default_provider: 'local',
  local: { path_prefix: '' },
  minio: { mode: 'docker', endpoint: '', access_key_id: '', secret_access_key: '', bucket_name: '', use_ssl: false, path_prefix: '' },
  cos: { secret_id: '', secret_key: '', region: '', bucket_name: '', app_id: '', path_prefix: '' },
  tos: { endpoint: '', region: '', access_key: '', secret_key: '', bucket_name: '', path_prefix: '' },
  s3: { endpoint: '', region: '', access_key: '', secret_key: '', bucket_name: '', path_prefix: '' },
  oss: {
    endpoint: '',
    region: '',
    access_key: '',
    secret_key: '',
    bucket_name: '',
    path_prefix: '',
    use_temp_bucket: false,
    temp_bucket_name: '',
    temp_region: '',
  },
  ks3: {
    endpoint: '',
    region: '',
    access_key: '',
    secret_key: '',
    bucket_name: '',
    path_prefix: '',
  },
  obs: {
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
const allowedProviders = ref<string[] | null>(null)
const engineStatus = ref<{ local: boolean; minio: boolean; cos: boolean }>({ local: true, minio: false, cos: true })
const minioEnvAvailable = ref(false)
const saving = ref(false)
const saveMessage = ref('')
const saveSuccess = ref(false)

const checkingMinio = ref(false)
const minioCheckResult = ref<{ ok: boolean; message: string; bucket_created?: boolean } | null>(null)
const checkingCos = ref(false)
const cosCheckResult = ref<{ ok: boolean; message: string } | null>(null)
const checkingTos = ref(false)
const tosCheckResult = ref<{ ok: boolean; message: string } | null>(null)
const checkingS3 = ref(false)
const s3CheckResult = ref<{ ok: boolean; message: string } | null>(null)
const checkingOss = ref(false)
const ossCheckResult = ref<{ ok: boolean; message: string } | null>(null)
const checkingKs3 = ref(false)
const ks3CheckResult = ref<{ ok: boolean; message: string } | null>(null)
const checkingObs = ref(false)
const obsCheckResult = ref<{ ok: boolean; message: string } | null>(null)

const drawerVisible = ref(false)
const currentEngine = ref<string | null>(null)

const providerOptions = computed(() => [
  { value: 'local', label: t('settings.storage.engineLocal'), allowed: isProviderAllowed('local') },
  { value: 'minio', label: 'MinIO', allowed: isProviderAllowed('minio') },
  { value: 'cos', label: t('settings.storage.engineCos'), allowed: isProviderAllowed('cos') },
  { value: 'tos', label: t('settings.storage.engineTos'), allowed: isProviderAllowed('tos') },
  { value: 's3', label: 'AWS S3', allowed: isProviderAllowed('s3') },
  { value: 'oss', label: t('settings.storage.engineOss'), allowed: isProviderAllowed('oss') },
  { value: 'ks3', label: t('settings.storage.engineKs3'), allowed: isProviderAllowed('ks3') },
  { value: 'obs', label: t('settings.storage.engineObs'), allowed: isProviderAllowed('obs') },
])

const hasAllowedProviders = computed(() => (allowedProviders.value?.length ?? 0) > 0)

const currentCheckState = computed(() => {
  switch (currentEngine.value) {
    case 'minio':
      return { loading: checkingMinio.value, result: minioCheckResult.value, onCheck: onCheckMinio }
    case 'cos':
      return { loading: checkingCos.value, result: cosCheckResult.value, onCheck: onCheckCos }
    case 'tos':
      return { loading: checkingTos.value, result: tosCheckResult.value, onCheck: onCheckTos }
    case 's3':
      return { loading: checkingS3.value, result: s3CheckResult.value, onCheck: onCheckS3 }
    case 'oss':
      return { loading: checkingOss.value, result: ossCheckResult.value, onCheck: onCheckOss }
    case 'ks3':
      return { loading: checkingKs3.value, result: ks3CheckResult.value, onCheck: onCheckKs3 }
    case 'obs':
      return { loading: checkingObs.value, result: obsCheckResult.value, onCheck: onCheckObs }
    default:
      return { loading: false, result: null, onCheck: () => undefined }
  }
})

const drawerTitle = computed(() => {
  if (!currentEngine.value) return ''
  const titles: Record<string, string> = {
    local: t('settings.storage.localTitle'),
    minio: 'MinIO',
    cos: t('settings.storage.cosTitle'),
    tos: t('settings.storage.tosTitle'),
    s3: t('settings.storage.s3Title'),
    oss: t('settings.storage.ossTitle'),
    ks3: t('settings.storage.ks3Title'),
    obs: t('settings.storage.obsTitle'),
  }
  return titles[currentEngine.value] || currentEngine.value
})

const minioAvailable = computed(() => {
  if (config.value.minio?.mode === 'remote') {
    return !!(config.value.minio.endpoint && config.value.minio.access_key_id && config.value.minio.secret_access_key)
  }
  return minioEnvAvailable.value
})

// Single source-of-truth for the cards列 + 状态/标题查询。新增 provider 时
// 在数组里加一项 + 翻译键即可，模板 v-for 自动跟进。
type StorageProviderId = 'local' | 'minio' | 'cos' | 'tos' | 's3' | 'oss' | 'ks3' | 'obs'
const STORAGE_PROVIDERS: { id: StorageProviderId }[] = [
  { id: 'local' },
  { id: 'minio' },
  { id: 'cos' },
  { id: 'tos' },
  { id: 's3' },
  { id: 'oss' },
  { id: 'ks3' },
  { id: 'obs' },
]

const providerTitle = (id: StorageProviderId): string => {
  if (id === 'minio') return 'MinIO'
  if (id === 's3') return 'AWS S3'
  return t(`settings.storage.${id}Title`)
}

const providerInitial = (id: StorageProviderId): string => {
  return providerTitle(id).trim().charAt(0).toUpperCase() || '?'
}

// 见 VectorStoreSettings 的同名注释：返回 --logo-url 给 ::before 用 mask 渲染。
const resolveLogo = (id: StorageProviderId) => providerLogo('storage', id)

const badgeClass = (id: StorageProviderId) => {
  const m = resolveLogo(id)?.mode
  return {
    'engine-card__badge--logo': !!m,
    'engine-card__badge--color': m === 'color',
    'engine-card__badge--mono': m === 'mono',
  }
}

const badgeStyle = (id: StorageProviderId): Record<string, string> => {
  const logo = resolveLogo(id)
  return logo?.mode === 'mono' ? { '--logo-url': `url("${logo.url}")` } : {}
}

const providerStatus = (id: StorageProviderId): { kind: 'on' | 'off'; label: string } => {
  if (id === 'minio' && !minioAvailable.value) {
    return { kind: 'off', label: t('settings.storage.needsConfig') }
  }
  if (id === 'local' || id === 'minio') {
    return { kind: 'on', label: t('settings.storage.available') }
  }
  return { kind: 'on', label: t('settings.storage.configurable') }
}

function isProviderAllowed(provider: string) {
  if (allowedProviders.value === null) return true
  return allowedProviders.value.includes(provider)
}

function ensureAllowedDefaultProvider() {
  if (isProviderAllowed(config.value.default_provider)) return
  config.value.default_provider = allowedProviders.value?.[0] || 'local'
}

function openDrawer(engine: string) {
  if (!isProviderAllowed(engine)) return
  currentEngine.value = engine
  drawerVisible.value = true
  saveMessage.value = ''
  minioCheckResult.value = null
  cosCheckResult.value = null
  tosCheckResult.value = null
  s3CheckResult.value = null
  ossCheckResult.value = null
  ks3CheckResult.value = null
  obsCheckResult.value = null
}

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
          : defaultConfig().minio,
        cos: d.cos
          ? {
              secret_id: d.cos.secret_id || '',
              secret_key: d.cos.secret_key || '',
              region: d.cos.region || '',
              bucket_name: d.cos.bucket_name || '',
              app_id: d.cos.app_id || '',
              path_prefix: d.cos.path_prefix || '',
            }
          : defaultConfig().cos,
        tos: d.tos
          ? {
              endpoint: d.tos.endpoint || '',
              region: d.tos.region || '',
              access_key: d.tos.access_key || '',
              secret_key: d.tos.secret_key || '',
              bucket_name: d.tos.bucket_name || '',
              path_prefix: d.tos.path_prefix || '',
            }
          : defaultConfig().tos,
        s3: d.s3
          ? {
              endpoint: d.s3.endpoint || '',
              region: d.s3.region || '',
              access_key: d.s3.access_key || '',
              secret_key: d.s3.secret_key || '',
              bucket_name: d.s3.bucket_name || '',
              path_prefix: d.s3.path_prefix || '',
            }
          : defaultConfig().s3,
        oss: d.oss
          ? {
              endpoint: d.oss.endpoint || '',
              region: d.oss.region || '',
              access_key: d.oss.access_key || '',
              secret_key: d.oss.secret_key || '',
              bucket_name: d.oss.bucket_name || '',
              path_prefix: d.oss.path_prefix || '',
              use_temp_bucket: d.oss.use_temp_bucket ?? false,
              temp_bucket_name: d.oss.temp_bucket_name || '',
              temp_region: d.oss.temp_region || '',
            }
          : defaultConfig().oss,
        ks3: d.ks3
          ? {
              endpoint: d.ks3.endpoint || '',
              region: d.ks3.region || '',
              access_key: d.ks3.access_key || '',
              secret_key: d.ks3.secret_key || '',
              bucket_name: d.ks3.bucket_name || '',
              path_prefix: d.ks3.path_prefix || '',
            }
          : defaultConfig().ks3,
        obs: d.obs
          ? {
              endpoint: d.obs.endpoint || '',
              region: d.obs.region || '',
              access_key: d.obs.access_key || '',
              secret_key: d.obs.secret_key || '',
              bucket_name: d.obs.bucket_name || '',
              path_prefix: d.obs.path_prefix || '',
            }
          : defaultConfig().obs,
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
    allowedProviders.value = res?.data?.allowed_providers?.length
      ? res.data.allowed_providers
      : engines.filter(e => e.allowed !== false).map(e => e.name)
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
    allowedProviders.value = ['local', 'minio', 'cos', 'tos', 's3', 'oss']
    minioEnvAvailable.value = false
  }
}

async function loadAll() {
  loading.value = true
  error.value = ''
  try {
    await Promise.all([loadConfig(), loadStatus()])
    ensureAllowedDefaultProvider()
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
    s3: {
      endpoint: (config.value.s3?.endpoint || '').trim(),
      region: (config.value.s3?.region || '').trim(),
      access_key: (config.value.s3?.access_key || '').trim(),
      secret_key: (config.value.s3?.secret_key || '').trim(),
      bucket_name: (config.value.s3?.bucket_name || '').trim(),
      path_prefix: (config.value.s3?.path_prefix || '').trim(),
    },
    oss: {
      endpoint: (config.value.oss?.endpoint || '').trim(),
      region: (config.value.oss?.region || '').trim(),
      access_key: (config.value.oss?.access_key || '').trim(),
      secret_key: (config.value.oss?.secret_key || '').trim(),
      bucket_name: (config.value.oss?.bucket_name || '').trim(),
      path_prefix: (config.value.oss?.path_prefix || '').trim(),
      use_temp_bucket: config.value.oss?.use_temp_bucket ?? false,
      temp_bucket_name: (config.value.oss?.temp_bucket_name || '').trim(),
      temp_region: (config.value.oss?.temp_region || '').trim(),
    },
    ks3: {
      endpoint: (config.value.ks3?.endpoint || '').trim(),
      region: (config.value.ks3?.region || '').trim(),
      access_key: (config.value.ks3?.access_key || '').trim(),
      secret_key: (config.value.ks3?.secret_key || '').trim(),
      bucket_name: (config.value.ks3?.bucket_name || '').trim(),
      path_prefix: (config.value.ks3?.path_prefix || '').trim(),
    },
    obs: {
      endpoint: (config.value.obs?.endpoint || '').trim(),
      region: (config.value.obs?.region || '').trim(),
      access_key: (config.value.obs?.access_key || '').trim(),
      secret_key: (config.value.obs?.secret_key || '').trim(),
      bucket_name: (config.value.obs?.bucket_name || '').trim(),
      path_prefix: (config.value.obs?.path_prefix || '').trim(),
    },
  }
}

async function onSave() {
  saving.value = true
  saveMessage.value = ''
  try {
    ensureAllowedDefaultProvider()
    await updateStorageEngineConfig(buildPayload())
    await loadStatus()
    ensureAllowedDefaultProvider()
    saveSuccess.value = true
    saveMessage.value = t('settings.storage.saveSuccess')
    drawerVisible.value = false
  } catch (e: unknown) {
    saveSuccess.value = false
    saveMessage.value = e instanceof Error ? e.message : t('settings.storage.saveFailed')
  } finally {
    saving.value = false
  }
}

async function onSaveDefaultEngine() {
  await onSave()
}

async function onCheckMinio() {
  checkingMinio.value = true
  minioCheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'minio', minio: payload.minio })
    minioCheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
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

async function onCheckS3() {
  checkingS3.value = true
  s3CheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 's3', s3: payload.s3 })
    s3CheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
  } catch (e: unknown) {
    s3CheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingS3.value = false
  }
}

async function onCheckOss() {
  checkingOss.value = true
  ossCheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'oss', oss: payload.oss })
    ossCheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
  } catch (e: unknown) {
    ossCheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingOss.value = false
  }
}

async function onCheckKs3() {
  checkingKs3.value = true
  ks3CheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'ks3', ks3: payload.ks3 })
    ks3CheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
  } catch (e: unknown) {
    ks3CheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingKs3.value = false
  }
}

async function onCheckObs() {
  checkingObs.value = true
  obsCheckResult.value = null
  try {
    const payload = buildPayload()
    const res = await checkStorageEngine({ provider: 'obs', obs: payload.obs })
    obsCheckResult.value = res?.data ?? { ok: false, message: t('settings.storage.unknownError') }
  } catch (e: unknown) {
    obsCheckResult.value = { ok: false, message: e instanceof Error ? e.message : t('settings.storage.requestFailed') }
  } finally {
    checkingObs.value = false
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

.engine-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
  margin-top: 24px;
}

// 与 Parser / Model / WebSearch / Mcp 一致的卡片样式 —— 整张是 button，
// 单击打开抽屉；active 是「当前正在编辑」的语义而不是「默认引擎」。
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

// 真实品牌 logo：白底 + 细边，logo 用 mask-image 染成 currentColor（沿用品牌色）。
// 多套一层 .engine-card 以胜过 `.engine-card--<id> .engine-card__badge` 的具体规则。
.engine-card .engine-card__badge--logo {
  background: var(--td-bg-color-container, #fff);
  box-shadow: inset 0 0 0 1px var(--td-component-stroke);
}

.engine-card .engine-card__badge--mono::before {
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

.engine-card__badge-img {
  width: 24px;
  height: 24px;
  object-fit: contain;
  display: block;
}

// 各对象存储徽章配色 —— 和 LOGO 主色对齐，但走低饱和版以维持 settings 整体调性。
.engine-card--local .engine-card__badge {
  background: rgba(70, 70, 70, 0.1);
  color: #464646;
}
.engine-card--minio .engine-card__badge {
  background: rgba(225, 38, 38, 0.12);
  color: #C0382B;
}
.engine-card--cos .engine-card__badge {
  background: rgba(0, 82, 217, 0.1);
  color: #0052D9;
}
.engine-card--tos .engine-card__badge {
  background: rgba(0, 137, 255, 0.12);
  color: #0089FF;
}
.engine-card--s3 .engine-card__badge {
  background: rgba(255, 153, 0, 0.12);
  color: #D97706;
}
.engine-card--oss .engine-card__badge {
  background: rgba(255, 90, 0, 0.12);
  color: #E55A00;
}
.engine-card--ks3 .engine-card__badge {
  background: rgba(7, 192, 95, 0.12);
  color: #07A050;
}
.engine-card--obs .engine-card__badge {
  background: rgba(206, 17, 38, 0.1);
  color: #CE1126;
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

  &--off {
    color: var(--td-text-color-placeholder);

    .engine-card__status-dot { background: var(--td-gray-color-5); }
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

.engine-info-block .doc-link {
  margin-left: 4px;
  font-size: 13px;
}

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

.form-item--inline {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;

  .form-label {
    margin-bottom: 0;
    flex-shrink: 0;
  }
}

.mode-selector {
  display: flex;
  gap: 8px;
  margin-bottom: 20px;
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

.drawer-actions {
  display: flex;
  align-items: center;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px dashed var(--td-component-stroke);
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

    &.created {
      color: var(--td-warning-color);
    }
  }

  :deep(.t-button) {
    min-width: 88px;
    height: 32px;
    font-size: 13px;
    border-radius: 6px;
    flex-shrink: 0;
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
