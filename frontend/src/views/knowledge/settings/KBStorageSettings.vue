<template>
  <div class="kb-storage-settings">
    <div class="section-header">
      <h2>{{ $t('kbSettings.storage.title') }}</h2>
      <p class="section-description">{{ $t('kbSettings.storage.selectDescription') }}</p>
    </div>
    <div v-if="loading" class="loading-inline"><t-loading size="small" /><span>{{ $t('kbSettings.storage.loading') }}</span></div>
    <div v-else class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('kbSettings.storage.instanceLabel') }}</label>
          <p class="desc">{{ $t('kbSettings.storage.instanceDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-select v-model="localID" style="width:100%;min-width:260px" :disabled="!!props.hasFiles" @change="handleChange">
            <t-option v-for="backend in backends" :key="backend.id" :value="backend.id" :label="backend.name">
              <span class="select-option">
                <span>{{ backend.name }}</span>
                <t-tag theme="primary" variant="light" size="small">{{ backend.provider.toUpperCase() }}</t-tag>
                <t-tag v-if="backend.id === defaultID" variant="light" size="small">{{ $t('kbSettings.storage.defaultTag') }}</t-tag>
              </span>
            </t-option>
          </t-select>
          <p v-if="props.hasFiles" class="option-hint change-warning">{{ $t('kbSettings.storage.migrateHint') }}</p>
          <p v-else-if="selected" class="option-hint">{{ selected.config.endpoint || selected.config.bucket_name || selected.config.path_prefix || $t('kbSettings.storage.localStorage') }}</p>
          <a href="javascript:void(0)" class="go-settings" @click.prevent="goToSettings">{{ $t('kbSettings.storage.manageInstances') }}</a>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { listStorageBackends, type StorageBackend } from '@/api/storage-backend'
import { useUIStore } from '@/stores/ui'

const props = defineProps<{ storageBackendId?: string; storageProvider?: string; hasFiles?: boolean }>()
const emit = defineEmits<{
  'update:storageBackendId': [value: string]
  'update:storageProvider': [value: string]
}>()
const uiStore = useUIStore()
const loading = ref(false), backends = ref<StorageBackend[]>([]), defaultID = ref(''), localID = ref(props.storageBackendId || '')
const selected = computed(() => backends.value.find(item => item.id === localID.value))

function handleChange() {
  emit('update:storageBackendId', localID.value)
  emit('update:storageProvider', selected.value?.provider || props.storageProvider || '')
}
async function load() {
  loading.value = true
  try {
    const response = await listStorageBackends()
    backends.value = (response.data || []).filter(item => item.status === 'active')
    defaultID.value = response.default_storage_backend_id || ''
    if (!localID.value) localID.value = defaultID.value || backends.value[0]?.id || ''
    if (localID.value) handleChange()
  } finally { loading.value = false }
}
function goToSettings() { uiStore.closeKBEditor?.(); uiStore.openSettings?.('storage') }
watch(() => props.storageBackendId, value => { if (value) localID.value = value })
onMounted(load)
</script>

<style scoped lang="less">
.section-header{margin-bottom:20px}.section-header h2{font-size:20px;margin:0 0 6px}.section-description,.desc,.option-hint{color:var(--td-text-color-secondary)}
.setting-row{display:flex;justify-content:space-between;gap:28px}.setting-info{flex:1}.setting-control{width:45%;min-width:300px}.select-option{display:flex;align-items:center;gap:8px}.option-hint{font-size:12px;margin:8px 0}.change-warning{color:var(--td-warning-color)}.go-settings{font-size:13px;color:var(--td-brand-color)}.loading-inline{display:flex;gap:8px}
</style>
