<template>
  <div class="kb-storage-settings">
    <div class="section-header">
      <h2>{{ $t('kbSettings.storage.title') }}</h2>
      <p class="section-description">
        {{ $t('kbSettings.storage.description') }}
      </p>
    </div>

    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('kbSettings.storage.loading') }}</span>
    </div>

    <div v-else class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('kbSettings.storage.engineLabel') }}</label>
          <p class="desc">{{ $t('kbSettings.storage.engineDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localProvider"
            size="medium"
            :placeholder="$t('kbSettings.storage.selectPlaceholder')"
            style="width: 100%; min-width: 220px;"
            @change="handleChange"
          >
            <t-option
              v-for="opt in engineOptions"
              :key="opt.value"
              :value="opt.value"
              :label="opt.label"
              :disabled="opt.disabled"
            >
              <span class="select-option">
                <span>{{ opt.label }}</span>
                 <t-tag v-if="opt.disabled && opt.allowed === false" theme="danger" variant="light" size="small">{{ $t('kbSettings.storage.unavailable') }}</t-tag>
                 <t-tag v-else-if="opt.disabled" theme="warning" variant="light" size="small">{{ $t('kbSettings.storage.notConfigured') }}</t-tag>
                <t-tag v-else-if="opt.available === false" theme="danger" variant="light" size="small">{{ $t('kbSettings.storage.unavailable') }}</t-tag>
              </span>
            </t-option>
          </t-select>
          <p v-if="props.hasFiles" class="option-hint change-warning">{{ $t('kbSettings.storage.changeWarning') }}</p>
          <p v-else-if="selectedOption?.desc" class="option-hint">{{ selectedOption.desc }}</p>
          <a v-if="showGoSettings" href="javascript:void(0)" class="go-settings" @click.prevent="goToStorageSettings">{{ $t('kbSettings.storage.goGlobalSettings') }}</a>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { getStorageEngineConfig, getStorageEngineStatus, type StorageEngineStatusItem } from '@/api/system'
import { useUIStore } from '@/stores/ui'

const { t } = useI18n()

const props = defineProps<{
  storageProvider: string
  hasFiles?: boolean
}>()

const emit = defineEmits<{
  'update:storageProvider': [value: string]
}>()

const uiStore = useUIStore()
const localProvider = ref(props.storageProvider || 'local')
const loading = ref(true)
const engineStatus = ref<StorageEngineStatusItem[]>([])
const defaultProvider = ref('local')
const allowedProviders = ref<string[]>([])
const hasAnyConfig = ref(false)

const engineOptions = computed(() => {
  const statusMap: Record<string, boolean> = {}
  const allowedMap: Record<string, boolean> = {}
  for (const e of engineStatus.value) {
    statusMap[e.name] = e.available
    allowedMap[e.name] = e.allowed !== false
  }
  return [
    {
      value: 'local',
      label: t('kbSettings.storage.engineLocal'),
      desc: t('kbSettings.storage.engineLocalDesc'),
      allowed: allowedMap.local !== false,
      available: statusMap.local !== false,
      disabled: allowedMap.local === false,
    },
    {
      value: 'minio',
      label: 'MinIO',
      desc: t('kbSettings.storage.engineMinioDesc'),
      allowed: allowedMap.minio !== false,
      available: statusMap.minio,
      disabled: allowedMap.minio === false || statusMap.minio === false,
    },
    {
      value: 'cos',
      label: t('kbSettings.storage.engineCos'),
      desc: t('kbSettings.storage.engineCosDesc'),
      allowed: allowedMap.cos !== false,
      available: statusMap.cos,
      disabled: allowedMap.cos === false || statusMap.cos === false,
    },
    {
      value: 'tos',
      label: t('kbSettings.storage.engineTos'),
      desc: t('kbSettings.storage.engineTosDesc'),
      allowed: allowedMap.tos !== false,
      available: statusMap.tos,
      disabled: allowedMap.tos === false || statusMap.tos === false,
    },
    {
      value: 's3',
      label: t('kbSettings.storage.engineS3'),
      desc: t('kbSettings.storage.engineS3Desc'),
      allowed: allowedMap.s3 !== false,
      available: statusMap.s3,
      disabled: allowedMap.s3 === false || statusMap.s3 === false,
    },
    {
      value: 'oss',
      label: t('kbSettings.storage.engineOss'),
      desc: t('kbSettings.storage.engineOssDesc'),
      allowed: allowedMap.oss !== false,
      available: statusMap.oss,
      disabled: allowedMap.oss === false || statusMap.oss === false,
    },
    {
      value: 'ks3',
      label: t('kbSettings.storage.engineKs3'),
      desc: t('kbSettings.storage.engineKs3Desc'),
      allowed: allowedMap.ks3 !== false,
      available: statusMap.ks3,
      disabled: allowedMap.ks3 === false || statusMap.ks3 === false,
    },
  ]
})

const showGoSettings = computed(() =>
  engineOptions.value.some(o => o.disabled)
)

const selectedOption = computed(() =>
  engineOptions.value.find(o => o.value === localProvider.value)
)

function handleChange() {
  emit('update:storageProvider', localProvider.value)
}

function ensureAllowedProvider() {
  const current = engineOptions.value.find(o => o.value === localProvider.value && !o.disabled)
  if (current) return
  const fallback = engineOptions.value.find(o => !o.disabled)?.value || defaultProvider.value || 'local'
  localProvider.value = fallback
  emit('update:storageProvider', localProvider.value)
}

function goToStorageSettings() {
  uiStore.closeKBEditor?.()
  uiStore.openSettings?.('storage')
}

async function load() {
  loading.value = true
  try {
    const [configRes, statusRes] = await Promise.all([
      getStorageEngineConfig(),
      getStorageEngineStatus(),
    ])
    const engines = statusRes?.data?.engines ?? []
    engineStatus.value = engines
    allowedProviders.value = statusRes?.data?.allowed_providers ?? []
    defaultProvider.value = configRes?.data?.default_provider || 'local'
    const d = configRes?.data
    hasAnyConfig.value = !!(d?.local?.path_prefix || d?.minio?.bucket_name || d?.cos?.bucket_name || d?.tos?.bucket_name || d?.s3?.bucket_name || d?.oss?.bucket_name || d?.ks3?.bucket_name)
    if (!localProvider.value || localProvider.value === '') {
      localProvider.value = defaultProvider.value
      emit('update:storageProvider', localProvider.value)
    }
    ensureAllowedProvider()
  } catch {
    engineStatus.value = []
  } finally {
    loading.value = false
  }
}

watch(() => props.storageProvider, (v) => {
  localProvider.value = v || defaultProvider.value || 'local'
}, { immediate: true })

onMounted(load)
</script>

<style lang="less" scoped>
.kb-storage-settings {
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

.loading-inline {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 16px 0;
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.setting-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 20px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

.setting-info {
  flex: 0 0 40%;
  max-width: 40%;
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
  flex: 0 0 55%;
  max-width: 55%;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
}

.select-option {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.option-hint {
  font-size: 12px;
  color: var(--td-text-color-placeholder);
  margin: 0;
  line-height: 1.4;

  &.locked-hint {
    color: var(--td-warning-color);
  }

  &.change-warning {
    color: var(--td-warning-color);
  }
}

.go-settings {
  font-size: 13px;
  color: var(--td-brand-color, #0052d9);
  margin-top: 8px;
  text-decoration: none;

  &:hover {
    text-decoration: underline;
  }
}
</style>
