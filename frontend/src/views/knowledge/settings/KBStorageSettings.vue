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
import { type StorageEngineStatusItem } from '@/api/system'
import { useUIStore } from '@/stores/ui'
import { useEditorResourcesStore } from '@/stores/editorResources'

const { t } = useI18n()

const props = defineProps<{
  storageProvider: string
  hasFiles?: boolean
}>()

const emit = defineEmits<{
  'update:storageProvider': [value: string]
}>()

const uiStore = useUIStore()
const editorResources = useEditorResourcesStore()
// Keep empty until tenant default_provider is loaded — do not pre-fill 'local'.
const localProvider = ref(props.storageProvider)
const loading = ref(true)
const engineStatus = ref<StorageEngineStatusItem[]>([])
const defaultProvider = ref('local')
const allowedProviders = ref<string[]>([])
const hasAnyConfig = ref(false)

const engineOptions = computed(() => {
  const statusByName = new Map(engineStatus.value.map((engine) => [engine.name, engine]))
  const allowedSet = new Set(allowedProviders.value)
  const hasStatus = engineStatus.value.length > 0
  const hasAllowedList = allowedProviders.value.length > 0
  const resolveState = (name: string, fallbackAvailable = false) => {
    const status = statusByName.get(name)
    const allowed = status?.allowed ?? (hasAllowedList ? allowedSet.has(name) : !hasStatus)
    const available = status?.available ?? fallbackAvailable
    return {
      allowed,
      available,
      disabled: !allowed || !available,
    }
  }

  const local = resolveState('local', true)
  const minio = resolveState('minio')
  const cos = resolveState('cos')
  const tos = resolveState('tos')
  const s3 = resolveState('s3')
  const oss = resolveState('oss')
  const ks3 = resolveState('ks3')
  const obs = resolveState('obs')

  return [
    {
      value: 'local',
      label: t('kbSettings.storage.engineLocal'),
      desc: t('kbSettings.storage.engineLocalDesc'),
      ...local,
    },
    {
      value: 'minio',
      label: 'MinIO',
      desc: t('kbSettings.storage.engineMinioDesc'),
      ...minio,
    },
    {
      value: 'cos',
      label: t('kbSettings.storage.engineCos'),
      desc: t('kbSettings.storage.engineCosDesc'),
      ...cos,
    },
    {
      value: 'tos',
      label: t('kbSettings.storage.engineTos'),
      desc: t('kbSettings.storage.engineTosDesc'),
      ...tos,
    },
    {
      value: 's3',
      label: t('kbSettings.storage.engineS3'),
      desc: t('kbSettings.storage.engineS3Desc'),
      ...s3,
    },
    {
      value: 'oss',
      label: t('kbSettings.storage.engineOss'),
      desc: t('kbSettings.storage.engineOssDesc'),
      ...oss,
    },
    {
      value: 'ks3',
      label: t('kbSettings.storage.engineKs3'),
      desc: t('kbSettings.storage.engineKs3Desc'),
      ...ks3,
    },
    {
      value: 'obs',
      label: t('kbSettings.storage.engineObs'),
      desc: t('kbSettings.storage.engineObsDesc'),
      ...obs,
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
  const resolved = editorResources.resolveUsableStorageProvider(localProvider.value)
  if (resolved === localProvider.value) return
  localProvider.value = resolved
  emit('update:storageProvider', localProvider.value)
}

function goToStorageSettings() {
  uiStore.closeKBEditor?.()
  uiStore.openSettings?.('storage')
}

async function load(force = false) {
  loading.value = true
  try {
    await editorResources.ensureStorageEngine(force)
    const engines = editorResources.storageStatus
    engineStatus.value = engines
    allowedProviders.value = editorResources.storageAllowedProviders
    defaultProvider.value = editorResources.resolveUsableStorageProvider(
      editorResources.storageConfig?.default_provider,
    )
    const d = editorResources.storageConfig
    hasAnyConfig.value = !!(d?.local?.path_prefix || d?.minio?.bucket_name || d?.cos?.bucket_name || d?.tos?.bucket_name || d?.s3?.bucket_name || d?.oss?.bucket_name || d?.ks3?.bucket_name || d?.obs?.bucket_name)
    const parentUnset = !props.storageProvider
    if (parentUnset) {
      localProvider.value = defaultProvider.value
      emit('update:storageProvider', localProvider.value)
    } else {
      localProvider.value = props.storageProvider
    }
    ensureAllowedProvider()
  } catch {
    engineStatus.value = []
  } finally {
    loading.value = false
  }
}

// Sync only when parent sets an explicit provider (edit mode). Create mode leaves
// storageProvider empty until load() applies tenant default_provider.
watch(() => props.storageProvider, (v) => {
  if (v) {
    localProvider.value = v
  }
})

onMounted(load)
</script>

<style lang="less" scoped>
.kb-storage-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 20px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    margin: 0 0 6px 0;
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
  padding: 16px 0;
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
