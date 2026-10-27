<script setup lang="ts">
/**
 * The image-pipeline panel.
 *
 * The list of pipelines and of their fields comes from the backend
 * (GET /api/v1/image-pipelines), so this component names no pipeline and knows
 * no field: adding one there arrives here as a new option and a new control.
 * A field is rendered by its declared type, and its label falls back to the
 * backend's own wording when this locale has nothing for it.
 *
 * The panel renders those fields in the order the pipeline declared them, and
 * that order is part of what a pipeline decides about itself. Nothing here may
 * sort, group or reorder them — see the note above the field loop.
 */
import { computed, onBeforeUnmount, onMounted, ref, useSlots, watch } from 'vue'
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
  /**
   * Every pipeline's private tunables, stored under the slot of the pipeline
   * that owns each one. A missing key reads the field's declared default, so a
   * pipeline the operator has never configured still renders real switches.
   */
  params: Record<string, unknown>
}>()

const emit = defineEmits<{
  'update:pipelineId': [value: string]
  'update:params': [value: Record<string, unknown>]
  /** Turned on while the current pick is configured to do nothing at all. */
  'update:invalid': [value: boolean]
}>()

import { pipelineParamKey, readPipelineParam } from '@/utils/imageProcessingConfig'

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
 * The run of the panel a field belongs to. A pipeline declares its fields in
 * three runs — standing switches, advanced switches, notes — and the empty
 * group is the first of them, so a standing switch says nothing about groups.
 */
function fieldGroup(field: ImagePipelineField): string {
  return field.group || 'switch'
}

// Each run keeps the order it was declared in: filter preserves it, and that is
// the whole reason nothing here may re-sort (see the note in the template).
const standingFields = computed(() => fields.value.filter((f) => fieldGroup(f) === 'switch'))
const advancedFields = computed(() => fields.value.filter((f) => fieldGroup(f) === 'advanced'))
const noteFields = computed(() => fields.value.filter((f) => fieldGroup(f) === 'note'))

// Both folded runs start closed: a pipeline declares them to keep them out of
// the way rather than to lengthen the panel.
const advancedOpen = ref(false)
const notesOpen = ref(false)

// The notes run also carries whatever the host hands over through the slot, and
// a pipeline that declares no note of its own must still show that — the smart
// pipeline's attribute dictionary is the case it exists for.
const slots = useSlots()
const hasNotesSlot = computed(() => Boolean(slots.notes))

/**
 * The wording of one value of an enum field. A pipeline that wants its options
 * read as words declares them under the field's own overlay path, so a fourth
 * mode needs no frontend change; without one the stored value is shown as it is.
 */
function enumOptionLabel(field: ImagePipelineField, option: string): string {
  const key = `${fieldKey(field, 'options')}.${option}`
  return te(key) ? t(key) : option
}

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
let validationGeneration = 0
let disposed = false

function scheduleValidation() {
  if (disposed) return
  // Invalidate in-flight responses as soon as the inputs change, including
  // the interval before the next debounced request starts.
  validationGeneration++
  violations.value = []
  validationFailed.value = false
  if (validationTimer) clearTimeout(validationTimer)
  validationTimer = setTimeout(() => void validate(), 300)
}

async function validate() {
  if (disposed) return
  const generation = ++validationGeneration
  const spec = current.value
  if (!spec) {
    violations.value = []
    return
  }
  try {
    const result = await validateImagePipelines(spec.id, props.params ?? {})
    if (generation !== validationGeneration) return
    violations.value = result.violations ?? []
    validationFailed.value = false
  } catch (error) {
    if (generation !== validationGeneration) return
    // An unreachable check is not a verdict. The panel must not block saving on
    // a network hiccup, and the save path asks the backend again anyway.
    violations.value = []
    validationFailed.value = true
    console.error('[ImagePipelineSettings] validation request failed', error)
  }
}

watch(() => [props.pipelineId, props.params], scheduleValidation)

onBeforeUnmount(() => {
  disposed = true
  validationGeneration++
  if (validationTimer) clearTimeout(validationTimer)
})

/**
 * A field's declared default. The backend sends JSON, so an unset key arrives
 * as undefined; the switch below must still read a value rather than a blank.
 */
function defaultValue(field: ImagePipelineField): unknown {
  return field.default ?? false
}

function paramValue(field: ImagePipelineField): unknown {
  const own = readPipelineParam(props.params, current.value?.id ?? '', field.key)
  return own === undefined || own === null ? defaultValue(field) : own
}

/** i18n overlay key for one field of one pipeline. */
function fieldKey(field: ImagePipelineField, part: 'label' | 'description' | 'options'): string {
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
  // The parameters stay: they are stored per pipeline, so switching back finds
  // the choice that was made here last time. Clearing them used to lose it.
  emit('update:pipelineId', value)
}

function onParamChange(key: string, value: unknown) {
  // Stored under the pipeline's own slot, so two pipelines that declare the same
  // field key never read each other's value, and switching back keeps both.
  emit('update:params', { ...props.params, [pipelineParamKey(current.value?.id ?? '', key)]: value })
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
      <!-- Section 1 of 3: which pipeline, and how it differs from the others. -->
      <div class="image-pipeline-section">
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
      </div>

      <div v-if="standingFields.length" class="image-pipeline-divider" />

      <!-- Section 2 of 3: the standing switches, each with its own description
           beside it. Section 3 (the observation attributes) belongs to the host
           dialog, which is where the attribute registry is read.

           The pipeline declares its fields in three runs — standing switches,
           advanced switches, notes — and this panel renders them as three
           places: here, the folded "advanced" block below, and the folded
           "notes" block under that. A run the pipeline does not declare renders
           nothing at all: no rows, no fold title, and no rule of its own, so a
           panel made of one switch looks like one switch.

           ==================== DO NOT REORDER THESE ROWS ====================
           Every row below is a field the SELECTED PIPELINE declared, and the
           order of its declaration is the order of this panel. What a panel
           shows, and in what order, is decided in the pipeline's own Go file
           (internal/application/service/image_pipeline_<id>.go, method Fields)
           — not here. This component is a renderer and must stay one: it
           cannot tell a tunable of the describe call from a tunable of the
           read call, so any sorting or grouping it attempted would be a guess.

           An earlier version of this file regrouped the fields by key prefix —
           "caption.* first, then ocr.*" — which read well for the smart
           pipeline and silently scrambled the manual one, whose switches and
           the thinking tunables that belong to them alternate. If a panel's
           order looks wrong, fix the declaration. See the Fields() doc on the
           imagePipeline interface for the full contract.
           =================================================================== -->
      <!-- Every declared field is a setting of the selected pipeline, stored in
           that pipeline's own slot — including the ones the pipeline carries in
           from the action that owns them. There is no field this panel reads
           from somewhere else, which is what keeps the value it draws and the
           value the run reads the same value. -->
      <div v-if="standingFields.length" class="image-pipeline-section">
        <div v-for="field in standingFields" :key="field.key" class="image-pipeline-row">
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
              <t-option v-for="opt in field.options ?? []" :key="opt" :value="opt" :label="enumOptionLabel(field, opt)" />
            </t-select>
            <t-input
              v-else
              :value="String(paramValue(field) ?? '')"
              @change="(value: string) => onParamChange(field.key, value)"
            />
          </div>
        </div>
      </div>

      <!-- The advanced switches, folded away. The toggle is a real button, so
           the title opens it as well as the arrow. -->
      <template v-if="advancedFields.length">
        <div class="image-pipeline-section">
          <button type="button" class="advanced-toggle" @click="advancedOpen = !advancedOpen">
            <t-icon name="chevron-right" class="toggle-arrow" :class="{ open: advancedOpen }" />
            <span>{{ $t('knowledgeEditor.advanced.multimodal.advancedSettingsLabel') }}</span>
          </button>
          <div v-if="advancedOpen" class="advanced-section">
            <div v-for="field in advancedFields" :key="field.key" class="image-pipeline-row">
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
                  <t-option v-for="opt in field.options ?? []" :key="opt" :value="opt" :label="enumOptionLabel(field, opt)" />
                </t-select>
                <t-input
                  v-else
                  :value="String(paramValue(field) ?? '')"
                  @change="(value: string) => onParamChange(field.key, value)"
                />
              </div>
            </div>
          </div>
        </div>
      </template>

      <!-- What the pipeline wants said about itself, folded away with it — plus
           whatever the host folds in here through the slot. For the smart
           pipeline that is the attribute dictionary: it explains what the OCR
           policy reads, it comes from the attribute registry rather than from
           the pipeline, and it belongs in the same place a reader looks for
           this pipeline's explanation. -->
      <template v-if="noteFields.length || hasNotesSlot">
        <div class="image-pipeline-section">
          <button type="button" class="advanced-toggle" @click="notesOpen = !notesOpen">
            <t-icon name="chevron-right" class="toggle-arrow" :class="{ open: notesOpen }" />
            <span>{{ $t('knowledgeEditor.advanced.multimodal.notesLabel') }}</span>
          </button>
          <div v-if="notesOpen" class="advanced-section">
            <div v-for="note in noteFields" :key="note.key" class="image-pipeline-note">
              <label>{{ fieldLabel(note) }}</label>
              <p v-if="fieldDescription(note)" class="desc">{{ fieldDescription(note) }}</p>
            </div>
            <slot name="notes" />
          </div>
        </div>
      </template>

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
// Sections 1 and 2 of the panel: a rule goes between the sections and never
// inside one, so a section reads as a block rather than as a series of rows
// separated by lines. Section 3 (the observation attributes) lives in the host
// dialog, which is where the attribute registry is read, and brings its own
// separating line with it. A folded run the pipeline does not declare draws no
// rule either — see the template.
.image-pipeline-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.image-pipeline-divider {
  margin: 0;
  border: 0;
  border-top: 1px solid var(--td-component-border);
}
// The folded runs follow the chunking panel's advanced options exactly — same
// button, same arrow, same collapse — so "advanced" looks and behaves the same
// wherever a knowledge base shows it. The whole row is the button, which is why
// the title opens it as well as the arrow.
.advanced-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 0;
  margin: 0;
  background: transparent;
  border: none;
  cursor: pointer;
  font-size: var(--app-text-base);
  font-weight: 500;
  color: var(--td-text-color-secondary);
  user-select: none;

  &:hover {
    color: var(--td-text-color-primary);
  }
}

.toggle-arrow {
  font-size: var(--app-text-xl);
  transition: transform var(--app-motion-fast) ease;

  &.open {
    transform: rotate(90deg);
  }
}

.advanced-section {
  margin-top: 4px;
}

// One declared note: a title and the prose under it, worded like a setting
// row's label and description so it reads as part of the same panel.
.image-pipeline-note {
  label {
    display: block;
    font-weight: 500;
    color: var(--td-text-color-primary);
  }

  .desc {
    margin: 4px 0 0;
    opacity: 0.75;
    font-size: var(--app-text-sm);
    line-height: 1.5;
  }
}
</style>
