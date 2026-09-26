/** One OCR trigger condition: attribute `prop` equals `is`. */
export interface ImageAttrConditionLike {
  prop: string
  is: string
}

/** Pipeline ids. Both dialogs compare against these instead of literals. */
export const IMAGE_PIPELINE_DEFAULT = 'default'
export const IMAGE_PIPELINE_SMARTOCR = 'smartocr'
/** Spellings a stored config may still carry; normalized, never honoured. */
export const LEGACY_PIPELINE_CAPTION_OCR = 'caption_ocr'
export const LEGACY_PIPELINE_OB_CAP_OCR = 'ob_cap_ocr'

/** The fields of the KB editor that feed image_processing_config. */
export interface ImageProcessingEdits {
  imageAttrsEnabled: boolean
  onUnobserved: boolean
  /** The registry's current default OCR conditions (GET /image-attrs/schema). */
  defaultOn: ImageAttrConditionLike[]
  /** The pipeline picked in the panel; empty means multimodal is off and the fields go away. */
  pipelineId: string
  /** That pipeline's private tunables, keyed by field. */
  pipelineParams: Record<string, unknown>
}

/** The pipeline fields an overrides payload carries, with the empties dropped. */
export interface PipelineFields {
  image_pipeline?: string
  image_pipeline_params?: Record<string, unknown>
}

/**
 * Maps a stored pipeline id onto the id the panel offers. Both renames are
 * folded in here so the KB editor, the upload dialog and the reparse dialog
 * agree on what a saved value means; unknown ids pass through and the caller
 * falls back. Mirrors backend types.NormalizeImagePipelineID.
 */
export function normalizeImagePipelineId(id: string | null | undefined): string {
  if (!id) return ''
  if (id === LEGACY_PIPELINE_CAPTION_OCR) return IMAGE_PIPELINE_DEFAULT
  if (id === LEGACY_PIPELINE_OB_CAP_OCR) return IMAGE_PIPELINE_SMARTOCR
  return id
}

/**
 * Builds the pipeline half of a payload. An empty pick is not a value of its
 * own: the backend resolves from image_attrs_enabled when the field is absent,
 * so the pair is dropped rather than written as an empty pair. The same goes
 * for parameters — a pick with nothing to tune must not send an empty object,
 * which would read as "the caller turned everything off".
 *
 * This is the only place that decides emptiness, so the knowledge base editor
 * (whole-config replacement) and the upload/reparse dialogs (per-task
 * overrides) cannot drift apart on what "no setting" means.
 */
export function buildPipelineFields(
  pipelineId: string,
  pipelineParams: Record<string, unknown> | null | undefined,
): PipelineFields {
  const fields: PipelineFields = {}
  if (pipelineId) fields.image_pipeline = pipelineId
  if (pipelineParams && Object.keys(pipelineParams).length > 0) {
    fields.image_pipeline_params = pipelineParams
  }
  return fields
}

/**
 * Builds the image_processing_config the KB editor saves, or null when nothing
 * changed. The backend replaces the whole object, so every field the editor
 * does not own (model_id, ...) is carried over from the snapshot.
 *
 * The editor only edits the observation switch and on_unobserved, but
 * on_unobserved must travel with a non-empty `on`: the backend adopts a custom
 * OCR clause only when `on` is set. A knowledge base whose `on` was customised
 * through the API keeps it; one without a custom list gets the registry's
 * current default, so it still follows the default as it evolves.
 *
 * The pipeline and its parameters are written too. They are the panel's own
 * settings; the observation switch is kept in step with the pick so a base that
 * was edited through the UI still reads the same way through the older switch.
 */
export function buildImageProcessingConfig(
  snapshot: Record<string, unknown> | null | undefined,
  edits: ImageProcessingEdits,
): Record<string, unknown> | null {
  const snap = snapshot || {}
  const storedOn = (snap.image_actions as { ocr?: { on?: unknown } } | undefined)?.ocr?.on
  const on = Array.isArray(storedOn) && storedOn.length > 0 ? storedOn : edits.defaultOn
  const built: Record<string, unknown> = {
    ...snap,
    image_attrs_enabled: edits.imageAttrsEnabled,
    image_actions: {
      ocr: {
        on,
        on_unobserved: edits.onUnobserved,
      },
    },
  }
  // The pick carries the panel's own answer, so it replaces whatever the
  // snapshot had — including by removing it.
  const pipeline = buildPipelineFields(edits.pipelineId, edits.pipelineParams)
  if (pipeline.image_pipeline) built.image_pipeline = pipeline.image_pipeline
  else delete built.image_pipeline
  if (pipeline.image_pipeline_params) built.image_pipeline_params = pipeline.image_pipeline_params
  else delete built.image_pipeline_params
  return JSON.stringify(built) === JSON.stringify(snap) ? null : built
}

/**
 * Resolves the pipeline id a knowledge base's stored config points at, by the
 * same rule the backend's ResolveImagePipelineID follows: an explicitly stored
 * id wins (a pre-rename "caption_ocr" normalizes to "default", "ob_cap_ocr" to
 * "smartocr"), otherwise the legacy observation switch picks smartocr when on
 * and default otherwise. Bases saved before the selector existed have no
 * config at all and land on default, so the picker always shows a real option.
 */
export function resolveImagePipelineFromKb(
  kb: Record<string, any> | null | undefined,
): string {
  const cfg = kb?.image_processing_config
  const stored = normalizeImagePipelineId((cfg?.image_pipeline as string | undefined) || '')
  if (stored) return stored
  return cfg?.image_attrs_enabled ? IMAGE_PIPELINE_SMARTOCR : IMAGE_PIPELINE_DEFAULT
}
