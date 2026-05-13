<template>
  <div class="system-info">
    <div class="section-header">
      <h2>{{ $t('system.title') }}</h2>
      <p class="section-description">{{ $t('system.sectionDescription') }}</p>
    </div>

    <!-- Loading state -->
    <div v-if="loading" class="loading-inline">
      <t-loading size="small" />
      <span>{{ $t('system.loadingInfo') }}</span>
    </div>

    <!-- Error state -->
    <div v-else-if="error" class="error-inline">
      <t-alert theme="error" :message="error">
        <template #operation>
          <t-button size="small" @click="loadInfo">{{ $t('system.retry') }}</t-button>
        </template>
      </t-alert>
    </div>

    <!-- Content -->
    <div v-else class="settings-group">
      <!-- System version -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.versionLabel') }}</label>
          <p class="desc">{{ $t('system.versionDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">
              {{ systemInfo?.version || $t('system.unknown') }}
              <t-tag
                v-if="systemInfo?.edition"
                :theme="systemInfo.edition === 'lite' ? 'primary' : 'default'"
                variant="light"
                size="small"
                style="margin-left: 8px;"
              >{{ systemInfo.edition === 'lite' ? 'Lite' : 'Standard' }}</t-tag>
              <span v-if="systemInfo?.commit_id" class="commit-info">
                ({{ systemInfo.commit_id }})
              </span>
          </span>
        </div>
      </div>

      <!-- Frontend version -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.frontendVersionLabel') }}</label>
          <p class="desc">{{ $t('system.frontendVersionDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">
            {{ frontendVersion }}
            <t-tag
              v-if="systemInfo?.version && systemInfo.version !== 'unknown' && frontendVersion !== 'unknown' && systemInfo.version !== frontendVersion"
              theme="warning"
              variant="light"
              size="small"
              style="margin-left: 8px;"
            >{{ $t('system.versionMismatch') }}</t-tag>
          </span>
        </div>
      </div>

      <!-- Build time -->
      <div v-if="systemInfo?.build_time" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.buildTimeLabel') }}</label>
          <p class="desc">{{ $t('system.buildTimeDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ systemInfo.build_time }}</span>
        </div>
      </div>

      <!-- Go version -->
      <div v-if="systemInfo?.go_version" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.goVersionLabel') }}</label>
          <p class="desc">{{ $t('system.goVersionDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ systemInfo.go_version }}</span>
        </div>
      </div>

      <!-- DB Version -->
      <div v-if="systemInfo?.db_version" class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.dbVersionLabel') }}</label>
          <p class="desc">{{ $t('system.dbVersionDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ systemInfo.db_version }}</span>
        </div>
      </div>

      <!-- Keyword Index Engine -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.keywordIndexEngineLabel') }}</label>
          <p class="desc">{{ $t('system.keywordIndexEngineDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ systemInfo?.keyword_index_engine || $t('system.unknown') }}</span>
        </div>
      </div>

      <!-- Vector Store Engine -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.vectorStoreEngineLabel') }}</label>
          <p class="desc">{{ $t('system.vectorStoreEngineDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ systemInfo?.vector_store_engine || $t('system.unknown') }}</span>
        </div>
      </div>

      <!-- Graph Database Engine -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('system.graphDatabaseEngineLabel') }}</label>
          <p class="desc">{{ $t('system.graphDatabaseEngineDescription') }}</p>
        </div>
        <div class="setting-control">
          <span class="info-value">{{ systemInfo?.graph_database_engine || $t('system.unknown') }}</span>
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { getSystemInfo, type SystemInfo } from '@/api/system'
import { useI18n } from 'vue-i18n'

const { t } = useI18n()

// Reactive state
const systemInfo = ref<SystemInfo | null>(null)
const loading = ref(true)
const error = ref('')
const frontendVersion = __FRONTEND_VERSION__

// Methods
const loadInfo = async () => {
  try {
    loading.value = true
    error.value = ''
    
    const systemResponse = await getSystemInfo()
    
    if (systemResponse.data) {
      systemInfo.value = systemResponse.data
    } else {
      error.value = t('system.messages.fetchFailed')
    }
  } catch (err: any) {
    error.value = err?.message || t('system.messages.networkError')
  } finally {
    loading.value = false
  }
}

// Lifecycle
onMounted(() => {
  loadInfo()
})
</script>

<style lang="less" scoped>
.system-info {
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
  gap: 12px;
  padding: 40px 0;
  justify-content: center;
  color: var(--td-text-color-secondary);
  font-size: 14px;
}

.error-inline {
  padding: 20px 0;
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

  .info-value {
    font-size: 14px;
    color: var(--td-text-color-primary);
    text-align: right;
    word-break: break-word;

    .commit-info {
      color: var(--td-text-color-placeholder);
      font-size: 12px;
      margin-left: 6px;
    }
  }
}
</style>
