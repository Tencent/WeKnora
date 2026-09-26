<script setup lang="ts">
/**
 * The image-pipeline panel.
 *
 * The list of pipelines and of their fields comes from the backend
 * (GET /api/v1/image-pipelines), so this component names no pipeline and knows
 * no field: adding one there arrives here as a new option and a new control.
 * A field is rendered by its declared type, and its label falls back to the
 * backend's own wording when this locale has nothing for it.
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchImagePipelines, type ImagePipelineField, type ImagePipelineSpec } from '@/api/knowledge-base'

const props = defineProps<{
  /** The selected pipeline id. */
  pipelineId: string
  /** That pipeline's private tunables; a missing key reads the field default. */
  params: Record<string, unknown>
}>()

const emit = defineEmits<{
  'update:pipelineId': [value: string]
  'update:params': [value: Record<string, unknown>]
}>()

const { t, te } = useI18n()

const pipelines = ref<ImagePipelineSpec[]>([])
const loading = ref(false)
const loadError = ref('')

/** The pipeline currently selected, if the registry still carries it. */
const current = computed<ImagePipelineSpec | undefined>(() =>
  pipelines.value.find((pipeline) => pipeline.id === props.pipelineId),
)

/** Its fields, in the order the backend declared them. */
const fields = computed<ImagePipelineField[]>(() => current.value?.fields ?? [])

/**
 * A field's declared default. The backend sends JSON, so an unset key arrives
 * as undefined; the switch below must still read a value rather than a blank.
 */
function defaultValue(field: ImagePipelineField): unknown {
  return field.default ?? false
}

function paramValue(field: ImagePipelineField): unknown {
  const own = props.params?.[field.key]
  return own === undefined || own === null ? defaultValue(field) : own
}

/** i18n overlay key for one field of one pipeline. */
function fieldKey(field: ImagePipelineField, part: 'label' | 'description'): string {
  return `imagePipeline.${current.value?.id ?? ''}.${field.key}.${part}`
}

function fieldLabel(field: ImagePipelineField): string {
  const key = fieldKey(field, 'label')
  return te(key) ? t(key) : field.label || field.key
}

function fieldDescription(field: ImagePipelineField): string {
  const key = fieldKey(field, 'description')
  return te(key) ? t(key) : field.description ?? ''
}

function pipelineName(pipeline: ImagePipelineSpec): string {
  const key = `imagePipeline.${pipeline.id}.name`
  return te(key) ? t(key) : pipeline.name
}

function onPipelineChange(value: string) {
  emit('update:pipelineId', value)
  // A pipeline's parameters are only meaningful to that pipeline. Switching one
  // drops the other's, rather than carrying a stale key across that it silently
  // ignores.
  emit('update:params', {})
}

function onParamChange(key: string, value: unknown) {
  emit('update:params', { ...props.params, [key]: value })
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    pipelines.value = await fetchImagePipelines()
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

onMounted(load)
// A dialog may mount before the KB it edits is loaded, so the selection can
// arrive after the registry it must be validated against.
watch(() => props.pipelineId, () => {
  if (!loading.value && pipelines.value.length === 0) load()
})
</script>

<template>
  <div class="image-pipeline-settings">
    <div v-if="loading" class="image-pipeline-hint">
      {{ $t('knowledgeEditor.advanced.multimodal.imagePipelineLoading') }}
    </div>

    <div v-else-if="loadError" class="image-pipeline-hint image-pipeline-hint--error">
      {{ $t('knowledgeEditor.advanced.multimodal.imagePipelineLoadError') }}：{{ loadError }}
    </div>

    <template v-else>
      <div class="image-pipeline-row">
        <div class="image-pipeline-info">
          <label>{{ $t('knowledgeEditor.advanced.multimodal.imagePipelineLabel') }}</label>
          <p class="desc">
            {{ $t('knowledgeEditor.advanced.multimodal.imagePipelineDescription') }}
          </p>
        </div>
        <div class="image-pipeline-control">
          <t-select
            :value="pipelineId"
            :placeholder="$t('knowledgeEditor.advanced.multimodal.imagePipelinePlaceholder')"
            @change="onPipelineChange"
          >
            <t-option
              v-for="pipeline in pipelines"
              :key="pipeline.id"
              :value="pipeline.id"
              :label="pipelineName(pipeline)"
            />
          </t-select>
        </div>
      </div>

      <!-- One row per field the running pipeline declares. A pipeline with no
           fields shows nothing here, which is the honest rendering of "nothing
           to tune" rather than an empty section. -->
      <div v-for="field in fields" :key="field.key" class="image-pipeline-row">
        <div class="image-pipeline-info">
          <label>{{ fieldLabel(field) }}</label>
          <p v-if="fieldDescription(field)" class="desc">{{ fieldDescription(field) }}</p>
        </div>
        <div class="image-pipeline-control">
          <t-switch
            v-if="field.type === 'bool'"
            :value="Boolean(paramValue(field))"
            size="medium"
            @change="(value: boolean) => onParamChange(field.key, value)"
          />
          <t-select
            v-else-if="field.type === 'enum'"
            :value="String(paramValue(field) ?? '')"
            clearable
            @change="(value: string) => onParamChange(field.key, value)"
          >
            <t-option v-for="opt in field.options ?? []" :key="opt" :value="opt" :label="opt" />
          </t-select>
          <t-input
            v-else
            :value="String(paramValue(field) ?? '')"
            @change="(value: string) => onParamChange(field.key, value)"
          />
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped lang="less">
.image-pipeline-settings {
  display: flex;
  flex-direction: column;
  gap: 12px;

  .image-pipeline-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
  }

  .image-pipeline-info {
    flex: 1 1 auto;
    min-width: 0;

    label {
      font-weight: 500;
    }

    .desc {
      margin: 4px 0 0;
      opacity: 0.75;
      font-size: 12px;
      line-height: 1.5;
    }
  }

  .image-pipeline-control {
    flex: 0 0 200px;
    text-align: right;
  }

  .image-pipeline-hint {
    opacity: 0.75;
    font-size: 13px;

    &--error {
      color: var(--error-color, #d54941);
    }
  }
}
</style>
