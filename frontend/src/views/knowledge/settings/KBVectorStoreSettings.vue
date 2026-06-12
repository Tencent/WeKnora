<template>
  <div class="kb-vector-store-settings">
    <div class="section-header">
      <h2>{{ $t('kbSettings.vectorStore.title') }}</h2>
      <p class="section-description">{{ $t('kbSettings.vectorStore.description') }}</p>
    </div>

    <div v-if="props.mode === 'create' && loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('kbSettings.vectorStore.loading') }}</span>
    </div>

    <!-- CREATE mode: dropdown -->
    <div v-else-if="props.mode === 'create'" class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('kbSettings.vectorStore.engineLabel') }}</label>
          <p class="desc">{{ $t('kbSettings.vectorStore.engineDesc') }}</p>
        </div>
        <div class="setting-control">
          <t-select
            v-model="localVectorStoreId"
            size="medium"
            :placeholder="$t('kbSettings.vectorStore.systemDefault')"
            :clearable="true"
            style="width: 100%; min-width: 220px;"
            @change="handleChange"
          >
            <t-option :value="''" :label="$t('kbSettings.vectorStore.systemDefault')">
              <span class="select-option">
                <span>{{ $t('kbSettings.vectorStore.systemDefault') }}</span>
                <t-tag
                  v-if="envEngineType"
                  theme="primary"
                  variant="light"
                  size="small"
                >
                  {{ envEngineType }}
                </t-tag>
              </span>
            </t-option>
            <t-option
              v-for="s in userStores"
              :key="s.id"
              :value="s.id || ''"
              :label="s.name"
            >
              <span class="select-option">
                <span>{{ s.name }}</span>
                <t-tag theme="success" variant="light" size="small">
                  {{ s.engine_type }}
                </t-tag>
              </span>
            </t-option>
          </t-select>
          <p class="option-hint">{{ $t('kbSettings.vectorStore.immutableHint') }}</p>
          <a
            href="javascript:void(0)"
            class="go-settings"
            @click.prevent="goToVectorStoreSettings"
          >
            {{ $t('kbSettings.vectorStore.goGlobalSettings') }}
          </a>
        </div>
      </div>
    </div>

    <!-- EDIT mode: read-only display -->
    <div v-else class="settings-group">
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('kbSettings.vectorStore.boundLabel') }}</label>
          <p class="desc">{{ $t('kbSettings.vectorStore.immutableEdit') }}</p>
        </div>
        <div class="setting-control">
          <VectorStoreBadge
            :source="props.boundSource"
            :name="props.boundName"
            :engine-type="props.boundEngineType"
            :status="props.boundStatus"
          />
          <p
            v-if="props.boundStatus === 'unavailable'"
            class="option-hint change-warning"
          >
            {{ $t('kbSettings.vectorStore.unavailableHint') }}
          </p>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useUIStore } from '@/stores/ui'
import { listVectorStores, type VectorStoreEntity } from '@/api/vector-store'
import type { VectorStoreSource, VectorStoreStatus } from '@/api/knowledge-base'
import VectorStoreBadge from '@/components/VectorStoreBadge.vue'

const props = defineProps<{
  mode: 'create' | 'edit'
  // create mode — current selection (empty string for env-default)
  vectorStoreId?: string
  // edit mode — bound store info already on the KB
  boundSource?: VectorStoreSource
  boundName?: string
  boundEngineType?: string
  boundStatus?: VectorStoreStatus
}>()

const emit = defineEmits<{
  (e: 'update:vectorStoreId', id: string): void
}>()

const { t } = useI18n()
const uiStore = useUIStore()

const loading = ref(false)
const allStores = ref<VectorStoreEntity[]>([])
const localVectorStoreId = ref<string>(props.vectorStoreId || '')

// Only show user-defined stores in the dropdown. The env store is
// surfaced via the explicit "System default" entry at the top of the
// list; including it twice would confuse users about which one is the
// fallback path.
const userStores = computed(() => allStores.value.filter((s) => s.source === 'user'))

// Engine type for the env store, shown as a tag next to the "System
// default" label so users know which storage backend handles unbound
// KBs (e.g. "postgres"). When the env-store entry is missing or its
// engine type is not populated, the tag is hidden entirely rather than
// showing a placeholder.
const envEngineType = computed(() => {
  const envStore = allStores.value.find((s) => s.source === 'env')
  return envStore?.engine_type || ''
})

watch(
  () => props.vectorStoreId,
  (v) => {
    localVectorStoreId.value = v || ''
  },
)

const handleChange = (val: string | undefined) => {
  // t-select's clear emits undefined; normalize to empty string so the
  // parent treats both as "use system default".
  emit('update:vectorStoreId', val || '')
}

// Open the global Settings panel directly on the Vector Stores
// section. This follows the same pattern as the other KB editor "go
// to settings" links (parser, storage, models): it talks to the UI
// store rather than navigating via the router, so the host editor
// modal stays mounted and can be returned to once the user closes the
// settings panel.
const goToVectorStoreSettings = () => {
  uiStore.openSettings('vectorstore')
}

onMounted(async () => {
  if (props.mode !== 'create') return
  loading.value = true
  try {
    const resp = await listVectorStores()
    if (resp.success) allStores.value = resp.data || []
  } catch (e) {
    // Graceful degradation: if vector-store listing fails the dropdown
    // simply renders only the "System default" entry, which is exactly
    // the legacy behavior. The KB editor remains usable.
    console.warn('[KBVectorStoreSettings] failed to load vector stores', e)
  } finally {
    loading.value = false
  }
})
</script>

<style lang="less" scoped>
.kb-vector-store-settings {
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
  font-size: 13px;
  color: var(--td-text-color-secondary);
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

  &:last-child {
    border-bottom: none;
  }
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
