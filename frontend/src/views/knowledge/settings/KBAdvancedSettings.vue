<template>
  <div class="kb-advanced-settings">
    <div class="section-header">
      <h2>{{ $t('knowledgeEditor.advanced.title') }}</h2>
      <p class="section-description">{{ $t('knowledgeEditor.advanced.description') }}</p>
    </div>

    <div class="settings-group">
      <!-- Question Generation feature -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.advanced.questionGeneration.label') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.advanced.questionGeneration.description') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localQuestionGeneration.enabled"
            @change="handleQuestionGenerationToggle"
            size="large"
          />
        </div>
      </div>

      <!-- Question Generation configuration -->
      <div v-if="localQuestionGeneration.enabled" class="subsection">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.advanced.questionGeneration.countLabel') }}</label>
            <p class="desc">{{ $t('knowledgeEditor.advanced.questionGeneration.countDescription') }}</p>
          </div>
          <div class="setting-control">
            <t-input-number
              v-model="localQuestionGeneration.questionCount"
              :min="1"
              :max="10"
              :step="1"
              theme="normal"
              @change="handleQuestionGenerationChange"
              style="width: 120px;"
            />
          </div>
        </div>
      </div>

      <!-- Multimodal feature -->
      <div class="setting-row">
        <div class="setting-info">
          <label>{{ $t('knowledgeEditor.advanced.multimodal.label') }}</label>
          <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.description') }}</p>
        </div>
        <div class="setting-control">
          <t-switch
            v-model="localMultimodal.enabled"
            @change="handleMultimodalToggle"
            size="large"
          />
        </div>
      </div>

      <!-- Multimodal storage configuration -->
      <div v-if="localMultimodal.enabled" class="subsection">
        <!-- VLLM model -->
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.advanced.multimodal.vllmLabel') }} <span class="required">*</span></label>
            <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.vllmDescription') }}</p>
          </div>
          <div class="setting-control">
            <ModelSelector
              ref="vllmSelectorRef"
              model-type="VLLM"
              :selected-model-id="localMultimodal.vllmModelId"
              :all-models="allModels"
              @update:selected-model-id="handleVLLMChange"
              @add-model="handleAddModel('vllm')"
              :placeholder="$t('knowledgeEditor.advanced.multimodal.vllmPlaceholder')"
            />
          </div>
        </div>

        <div class="subsection-header">
          <h4>{{ $t('knowledgeEditor.advanced.multimodal.storageTitle') }} <span class="required">*</span></h4>
        </div>
        
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ $t('knowledgeEditor.advanced.multimodal.storageTypeLabel') }} <span class="required">*</span></label>
            <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.storageTypeDescription') }}</p>
            <!-- Warning message when MinIO is not enabled -->
            <t-alert
              v-if="!isMinioEnabled"
              theme="warning"
              :message="$t('knowledgeEditor.advanced.multimodal.minioDisabledWarning')"
              style="margin-top: 8px;"
            />
          </div>
          <div class="setting-control">
            <t-radio-group v-model="localMultimodal.storageType" @change="handleStorageTypeChange">
              <t-radio value="minio" :disabled="!isMinioEnabled">
                {{ $t('knowledgeEditor.advanced.multimodal.storageTypeOptions.minio') }}
              </t-radio>
              <t-radio value="cos">{{ $t('knowledgeEditor.advanced.multimodal.storageTypeOptions.cos') }}</t-radio>
            </t-radio-group>
          </div>
        </div>

        <!-- MinIO configuration -->
        <div v-if="localMultimodal.storageType === 'minio'" class="storage-config">
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.minio.bucketLabel') }} <span class="required">*</span></label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.minio.bucketDescription') }}</p>
              <p class="hint">{{ $t('knowledgeEditor.advanced.multimodal.minio.bucketHint') }}</p>
            </div>
            <div class="setting-control bucket-control">
              <t-select
                v-model="localMultimodal.minio.bucketName"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.minio.bucketPlaceholder')"
                :loading="loadingBuckets"
                filterable
                creatable
                @change="handleConfigChange"
                @focus="loadMinioBuckets"
                style="width: 280px;"
              >
                <t-option
                  v-for="bucket in minioBuckets"
                  :key="bucket.name"
                  :value="bucket.name"
                  :label="bucket.name"
                >
                  <div class="bucket-option">
                    <span class="bucket-name">{{ bucket.name }}</span>
                    <t-tag
                      :theme="bucket.policy === 'public' ? 'success' : bucket.policy === 'private' ? 'default' : 'warning'"
                      size="small"
                      variant="light"
                    >
                      {{ $t(`knowledgeEditor.advanced.multimodal.minio.policyLabels.${bucket.policy}`) }}
                    </t-tag>
                  </div>
                </t-option>
              </t-select>
              <t-button
                theme="default"
                variant="outline"
                size="small"
                :loading="loadingBuckets"
                @click="loadMinioBuckets"
                style="margin-left: 8px;"
              >
                <template #icon><refresh-icon /></template>
              </t-button>
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.minio.useSslLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.minio.useSslDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-switch
                v-model="localMultimodal.minio.useSSL"
                @change="handleConfigChange"
                size="large"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.minio.pathPrefixLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.minio.pathPrefixDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.minio.pathPrefix"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.minio.pathPrefixPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>
        </div>

        <!-- COS configuration -->
        <div v-if="localMultimodal.storageType === 'cos'" class="storage-config">
          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.cos.secretIdLabel') }} <span class="required">*</span></label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.cos.secretIdDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.cos.secretId"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.cos.secretIdPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.cos.secretKeyLabel') }} <span class="required">*</span></label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.cos.secretKeyDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.cos.secretKey"
                type="password"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.cos.secretKeyPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.cos.regionLabel') }} <span class="required">*</span></label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.cos.regionDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.cos.region"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.cos.regionPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.cos.bucketLabel') }} <span class="required">*</span></label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.cos.bucketDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.cos.bucketName"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.cos.bucketPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.cos.appIdLabel') }} <span class="required">*</span></label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.cos.appIdDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.cos.appId"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.cos.appIdPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>

          <div class="setting-row">
            <div class="setting-info">
              <label>{{ $t('knowledgeEditor.advanced.multimodal.cos.pathPrefixLabel') }}</label>
              <p class="desc">{{ $t('knowledgeEditor.advanced.multimodal.cos.pathPrefixDescription') }}</p>
            </div>
            <div class="setting-control">
              <t-input
                v-model="localMultimodal.cos.pathPrefix"
                :placeholder="$t('knowledgeEditor.advanced.multimodal.cos.pathPrefixPlaceholder')"
                @change="handleConfigChange"
                style="width: 280px;"
              />
            </div>
          </div>
        </div>
      </div>

    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { RefreshIcon } from 'tdesign-icons-vue-next'
import ModelSelector from '@/components/ModelSelector.vue'
import { useUIStore } from '@/stores/ui'
import { getSystemInfo, listMinioBuckets, type MinioBucketInfo } from '@/api/system'

const uiStore = useUIStore()

interface MultimodalConfig {
  enabled: boolean
  storageType: 'minio' | 'cos'
  vllmModelId?: string
  minio: {
    bucketName: string
    useSSL: boolean
    pathPrefix: string
  }
  cos: {
    secretId: string
    secretKey: string
    region: string
    bucketName: string
    appId: string
    pathPrefix: string
  }
}

interface QuestionGenerationConfig {
  enabled: boolean
  questionCount: number
}

interface Props {
  multimodal: MultimodalConfig
  questionGeneration?: QuestionGenerationConfig
  allModels?: any[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:multimodal': [value: MultimodalConfig]
  'update:questionGeneration': [value: QuestionGenerationConfig]
}>()

const localMultimodal = ref<MultimodalConfig>({ ...props.multimodal })
const localQuestionGeneration = ref<QuestionGenerationConfig>(
  props.questionGeneration || { enabled: false, questionCount: 3 }
)

const vllmSelectorRef = ref()
const isMinioEnabled = ref(false)
const minioBuckets = ref<MinioBucketInfo[]>([])
const loadingBuckets = ref(false)

// Load MinIO buckets
const loadMinioBuckets = async () => {
  if (!isMinioEnabled.value || loadingBuckets.value) return
  
  loadingBuckets.value = true
  try {
    const response = await listMinioBuckets()
    if (response.data?.buckets) {
      minioBuckets.value = response.data.buckets
    }
  } catch (error) {
    console.error('Failed to load MinIO buckets:', error)
  } finally {
    loadingBuckets.value = false
  }
}

// Check system status on mount
onMounted(async () => {
  try {
    const systemInfo = await getSystemInfo()
    
    // Check MinIO status
    isMinioEnabled.value = systemInfo.data?.minio_enabled === true
    
    // If MinIO is not enabled and storage type is minio, switch to cos
    if (!isMinioEnabled.value && localMultimodal.value.storageType === 'minio') {
      localMultimodal.value.storageType = 'cos'
      emit('update:multimodal', localMultimodal.value)
    }
    
    // Load MinIO buckets if enabled
    if (isMinioEnabled.value) {
      loadMinioBuckets()
    }
  } catch (error) {
    console.error('Failed to fetch system info:', error)
    // Default to disabled if we can't fetch the info
    isMinioEnabled.value = false
    // If MinIO status unknown and storage type is minio, switch to cos
    if (localMultimodal.value.storageType === 'minio') {
      localMultimodal.value.storageType = 'cos'
      emit('update:multimodal', localMultimodal.value)
    }
  }
})

// Watch for prop changes
watch(() => props.multimodal, (newVal) => {
  localMultimodal.value = { ...newVal }
}, { deep: true })

watch(() => props.questionGeneration, (newVal) => {
  if (newVal) {
    localQuestionGeneration.value = { ...newVal }
  }
}, { deep: true })

// Handle question generation toggle
const handleQuestionGenerationToggle = () => {
  if (!localQuestionGeneration.value.enabled) {
    localQuestionGeneration.value.questionCount = 3
  }
  emit('update:questionGeneration', localQuestionGeneration.value)
}

// Handle question generation config change
const handleQuestionGenerationChange = () => {
  emit('update:questionGeneration', localQuestionGeneration.value)
}

// Handle multimodal toggle
const handleMultimodalToggle = () => {
  // Reset related configuration when multimodal is disabled
  if (!localMultimodal.value.enabled) {
    localMultimodal.value.vllmModelId = ''
    localMultimodal.value.minio = {
      bucketName: '',
      useSSL: false,
      pathPrefix: ''
    }
    localMultimodal.value.cos = {
      secretId: '',
      secretKey: '',
      region: '',
      bucketName: '',
      appId: '',
      pathPrefix: ''
    }
  }
  emit('update:multimodal', localMultimodal.value)
}

// Handle storage type change
const handleStorageTypeChange = () => {
  // Prevent switching to minio if it's not enabled
  if (localMultimodal.value.storageType === 'minio' && !isMinioEnabled.value) {
    localMultimodal.value.storageType = 'cos'
  }
  emit('update:multimodal', localMultimodal.value)
}

// Handle VLLM model change
const handleVLLMChange = (modelId: string) => {
  localMultimodal.value.vllmModelId = modelId
  emit('update:multimodal', localMultimodal.value)
}

// Navigate to model management when adding models
const handleAddModel = (subSection: string) => {
  uiStore.openSettings('models', subSection)
}

// Handle configuration change
const handleConfigChange = () => {
  emit('update:multimodal', localMultimodal.value)
}

// The allModels prop keeps model options in sync
</script>

<style lang="less" scoped>
.kb-advanced-settings {
  width: 100%;
}

.section-header {
  margin-bottom: 32px;

  h2 {
    font-size: 20px;
    font-weight: 600;
    color: #333333;
    margin: 0 0 8px 0;
  }

  .section-description {
    font-size: 14px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }
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
  border-bottom: 1px solid #e5e7eb;

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
    color: #333333;
    display: block;
    margin-bottom: 4px;
  }

  .desc {
    font-size: 13px;
    color: #666666;
    margin: 0;
    line-height: 1.5;
  }

  .hint {
    font-size: 12px;
    color: #999999;
    margin: 6px 0 0 0;
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

.subsection {
  padding: 16px 20px;
  margin: 12px 0 0 0;
  background: #f8fafb;
  border-radius: 8px;
  border-left: 3px solid #07C05F;
  position: relative;
}

.subsection-header {
  margin: 16px 0 8px 0;
  
  &:first-child {
    margin-top: 0;
  }
  
  h4 {
    font-size: 15px;
    font-weight: 600;
    color: #333333;
    margin: 0;
    padding-left: 8px;
    border-left: 2px solid #07C05F;
    
    .required {
      color: #e34d59;
      margin-left: 4px;
    }
  }
}

.required {
  color: #e34d59;
  margin-left: 2px;
  font-weight: 500;
}

.storage-config {
  margin-top: 8px;
}

.bucket-control {
  display: flex;
  align-items: center;
}

.bucket-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  
  .bucket-name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.config-item {
  background: #ffffff;
  border: 1px solid #e5e7eb;
  border-radius: 6px;
  padding: 16px;
  margin-bottom: 12px;
}

.config-item-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid #f0f0f0;
}

.config-item-title {
  font-size: 14px;
  font-weight: 600;
  color: #333333;
}

.config-item-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.config-field {
  display: flex;
  flex-direction: column;
  gap: 6px;

  label {
    font-size: 13px;
    font-weight: 500;
    color: #555555;
  }
}

.subsection-desc {
  font-size: 13px;
  color: #666666;
  margin: 4px 0 8px 8px;
  line-height: 1.5;
}

</style>

