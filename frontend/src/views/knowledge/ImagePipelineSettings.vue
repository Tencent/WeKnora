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
import {
  fetchImagePipelines,
  validateImagePipelines,
  type ImagePipelineField,
  type ImagePipelineSpec,
  type ImagePipelineViolation,
} from '@/api/knowledge-base'

const props = defineProps<{
  /** The selected pipeline id. */
  pipelineId: string
  /** That pipeline's private tunables; a missing key reads the field default. */
  params: Record<string, unknown>
}>()

const emit = defineEmits<{
  'update:pipelineId': [value: string]
  'update:params': [value: Record<string, unknown>]
  /** Turned on while the current pick is configured to do nothing at all. */
  'update:invalid': [value: boolean]
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

// Every rule the selected pipeline breaks, reported by the backend and in the
// backend's own terms: each violation carries the i18n key of its own wording
// and the name of the control to point at. Nothing here decides what is
// forbidden — the rules travel with the spec, so adding one to a pipeline needs
// no change to this file, and a pipeline whose actions are not the user's to
// switch off declares none and can never land in this list.
const violations = ref<ImagePipelineViolation[]>([])
const validationFailed = ref(false)

const invalid = computed<boolean>(() => violations.value.length > 0)

watch(invalid, (value) => emit('update:invalid', value), { immediate: true })

/** The debounce keeps a dribbled change from posting on every keystroke. */
let validationTimer: ReturnType<typeof setTimeout> | undefined

function scheduleValidation() {
  if (validationTimer) clearTimeout(validationTimer)
  validationTimer = setTimeout(() => void validate(), 300)
}

async function validate() {
  const spec = current.value
  if (!spec) {
    violations.value = []
    return
  }
  try {
    const result = await validateImagePipelines(spec.id, props.params ?? {})
    violations.value = result.violations ?? []
    validationFailed.value = false
  } catch (error) {
    // An unreachable check is not a verdict. The panel must not block saving on
    // a network hiccup, and the save path asks the backend again anyway.
    violations.value = []
    validationFailed.value = true
    console.error('[ImagePipelineSettings] validation request failed', error)
  }
}

watch(() => [props.pipelineId, props.params], scheduleValidation)

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

/** How the selected pipeline works, shown under the pick. */
function pipelineDescription(pipeline: ImagePipelineSpec): string {
  const key = `imagePipeline.${pipeline.id}.description`
  return te(key) ? t(key) : pipeline.description ?? ''
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
  // The registry arrived, so the current pick can now be checked against it;
  // before that there was no rule to check it against.
  scheduleValidation()
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
      <p v-if="current" class="image-pipeline-desc">{{ pipelineDescription(current) }}</p>

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

      <!-- Every broken rule, listed rather than summarised: each line names what
           is wrong and, through the field it points at, which control to turn
           back on. Sits at the foot of the panel, next to the button the user
           has to press to lose it. -->
      <ul v-if="invalid" class="image-pipeline-violations">
        <li
          v-for="violation in violations"
          :key="`${violation.message_key}:${violation.field ?? ''}`"
          class="image-pipeline-desc image-pipeline-desc--error"
        >
          {{ $t(violation.message_key) || violation.message_key }}
        </li>
      </ul>
      <p v-else-if="validationFailed" class="image-pipeline-desc">
        {{ $t('knowledgeEditor.advanced.multimodal.imagePipelineValidateError') }}
      </p>
    </template>
  </div>
</template>

<style scoped lang="less">
.image-pipeline-settings {
  display: flex;
  flex-direction: column;
  gap: 12px;

  // The section description above ends here; a wider gap marks where the
  // controls begin, matching the rhythm of the other setting rows.
  margin-top: 4px;

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
      font-size: var(--app-text-sm);
      line-height: 1.5;
    }
  }

  .image-pipeline-control {
    flex: 0 0 200px;
    text-align: right;
  }

  .image-pipeline-desc {
    margin: -4px 0 0;
    opacity: 0.75;
    font-size: var(--app-text-sm);
    line-height: 1.5;

    &--error {
      margin: 0;
      opacity: 1;
      color: var(--error-color, #d54941);
    }
  }

  .image-pipeline-hint {
    opacity: 0.75;
    font-size: var(--app-text-md);

    &--error {
      color: var(--error-color, #d54941);
    }
  }

  // One line per broken rule, kept compact so that several of them still read
  // as a footnote to the button rather than as a second form.
  .image-pipeline-violations {
    margin: 0;
    padding-left: 1em;
    list-style: disc;
  }
}
</style>
