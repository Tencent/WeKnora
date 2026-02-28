<template>
  <div class="kb-storage-settings">
    <div class="section-header">
      <h2>存储引擎</h2>
      <p class="section-description">
        选择文件存储引擎，影响文档上传存储和文档中图片的存储方式。参数在全局设置中配置。
      </p>
    </div>

    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>加载中...</span>
    </div>

    <div v-else class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>存储引擎</label>
          <p class="desc">选择该知识库使用的存储引擎，需在全局设置中已配置对应引擎。</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localProvider"
            size="medium"
            placeholder="请选择存储引擎"
            style="width: 100%; min-width: 220px;"
            :disabled="props.hasFiles"
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
                <t-tag v-if="opt.disabled" theme="warning" variant="light" size="small">未配置</t-tag>
                <t-tag v-else-if="opt.available === false" theme="danger" variant="light" size="small">不可用</t-tag>
              </span>
            </t-option>
          </t-select>
          <p v-if="props.hasFiles" class="option-hint locked-hint">知识库中已有文件，无法切换存储引擎。如需更换，请先清空知识库中的所有文件。</p>
          <p v-else-if="selectedOption?.desc" class="option-hint">{{ selectedOption.desc }}</p>
          <a v-if="showGoSettings" href="javascript:void(0)" class="go-settings" @click.prevent="goToStorageSettings">去全局设置中配置</a>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { getStorageEngineConfig, getStorageEngineStatus, type StorageEngineStatusItem } from '@/api/system'
import { useUIStore } from '@/stores/ui'

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
      label: 'Local（本地存储）',
      desc: '仅适合单机部署，简单轻量',
      available: statusMap.local !== false,
      disabled: false,
    },
    {
      value: 'minio',
      label: 'MinIO',
      desc: 'S3 兼容，适合内网或私有云',
      available: statusMap.minio,
      disabled: statusMap.minio === false,
    },
    {
      value: 'cos',
      label: '腾讯云 COS',
      desc: '公有云部署，支持 CDN 加速',
      available: statusMap.cos,
      disabled: statusMap.cos === false,
    },
    {
      value: 'tos',
      label: '火山引擎 TOS',
      desc: '火山引擎对象存储，适合公有云部署',
      available: statusMap.tos,
      disabled: statusMap.tos === false,
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
    hasAnyConfig.value = !!(d?.local?.path_prefix || d?.minio?.bucket_name || d?.cos?.bucket_name || d?.tos?.bucket_name)
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
  border-bottom: 1px solid #e5e7eb;
}

.setting-info {
  flex: 1;
  max-width: 65%;
  padding-right: 24px;

  label {
    font-size: 15px;
    font-weight: 500;
    color: #333;
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: #666;
    margin: 0;
    line-height: 1.5;
  }
}

.setting-control {
  flex-shrink: 0;
  min-width: 280px;
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
  color: #999;
  margin: 0;
  line-height: 1.4;

  &.locked-hint {
    color: #e6a23c;
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
