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
                <t-tag v-if="opt.disabled" theme="warning" variant="light" size="small">{{ $t('kbSettings.storage.notConfigured') }}</t-tag>
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
const hasAnyConfig = ref(false)

const engineOptions = computed(() => {
  const statusMap: Record<string, boolean> = {}
  for (const e of engineStatus.value) {
    statusMap[e.name] = e.available
  }
  return [
    {
      value: 'local',
      label: t('kbSettings.storage.engineLocal'),
      desc: t('kbSettings.storage.engineLocalDesc'),
      available: statusMap.local !== false,
      disabled: false,
    },
    {
      value: 'minio',
      label: 'MinIO',
      desc: t('kbSettings.storage.engineMinioDesc'),
      available: statusMap.minio,
      disabled: statusMap.minio === false,
    },
    {
      value: 'cos',
      label: t('kbSettings.storage.engineCos'),
      desc: t('kbSettings.storage.engineCosDesc'),
      available: statusMap.cos,
      disabled: statusMap.cos === false,
    },
    {
      value: 'tos',
      label: t('kbSettings.storage.engineTos'),
      desc: t('kbSettings.storage.engineTosDesc'),
      available: statusMap.tos,
      disabled: statusMap.tos === false,
    },
    {
      value: 's3',
      label: t('kbSettings.storage.engineS3'),
      desc: t('kbSettings.storage.engineS3Desc'),
      available: statusMap.s3,
      disabled: statusMap.s3 === false,
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
    defaultProvider.value = configRes?.data?.default_provider || 'local'
    const d = configRes?.data
    hasAnyConfig.value = !!(d?.local?.path_prefix || d?.minio?.bucket_name || d?.cos?.bucket_name || d?.tos?.bucket_name || d?.s3?.bucket_name)
    if (!localProvider.value || localProvider.value === '') {
      localProvider.value = defaultProvider.value
      emit('update:storageProvider', localProvider.value)
    }
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
