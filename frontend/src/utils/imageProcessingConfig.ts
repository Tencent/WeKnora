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
  /** Index and recall images by their own vectors (off unless opted in). */
  imageVectorEnabled: boolean
  onUnobserved: boolean
  /** The registry's current default OCR conditions (GET /image-attrs/schema). */
  defaultOn: ImageAttrConditionLike[]
  /** The pipeline picked in the panel; empty means multimodal is off and the fields go away. */
  pipelineId: string
  /** That pipeline's private tunables, keyed by field. */
  pipelineParams: Record<string, unknown>
  /**
   * The page-straightening switch: 'auto' (observe geometry, then decide),
   * 'off' (never) or 'force' (always). Mirrors backend preprocess modes.
   */
  preprocessMode: string
}

/** The preprocess switch's values, mirroring the backend's constants. */
export const PREPROCESS_MODE_OFF = 'off'
export const PREPROCESS_MODE_AUTO = 'auto'
export const PREPROCESS_MODE_FORCE = 'force'

/**
 * The field key the smart pipeline declares for the page-straightening switch.
 * It is declared as a field so the panel draws it at the point that pipeline's
 * run uses it, but its value is not a pipeline param: the panel binds it to the
 * knowledge base's preprocess mode instead of to params. Mirrors
 * smartFieldKeyPreprocessMode on the backend — the two must agree, or the
 * switch silently becomes an ordinary, ignored param.
 */
export const PREPROCESS_MODE_FIELD_KEY = 'preprocess_mode'

/**
 * The field key the smart pipeline declares for the OCR policy's unobserved
 * clause. Like PREPROCESS_MODE_FIELD_KEY, its value is a knowledge base setting
 * rather than a pipeline param, and the two must agree with the backend's
 * imageFieldKeyOCROnUnobserved.
 */
export const OCR_ON_UNOBSERVED_FIELD_KEY = 'ocr.on_unobserved'

/**
 * Maps a stored preprocess mode onto the one the panel offers. Anything
 * unrecognised — including the empty string a knowledge base saved before the
 * field existed carries — resolves to auto, the backend's own default, so the
 * panel never shows a blank choice.
 */
export function normalizePreprocessMode(mode: string | null | undefined): string {
  switch ((mode || '').trim().toLowerCase()) {
    case PREPROCESS_MODE_OFF:
      return PREPROCESS_MODE_OFF
    case PREPROCESS_MODE_FORCE:
      return PREPROCESS_MODE_FORCE
    default:
      return PREPROCESS_MODE_AUTO
  }
}

/** The pipeline fields an overrides payload carries, with the empties dropped. */
export interface PipelineFields {
  image_pipeline?: string
  image_pipeline_params?: Record<string, unknown>
  /**
   * Per-task override of the page-straightening switch. Only sent when it
   * differs from the knowledge base's own value, so an untouched dialog does
   * not pin a document to a setting the KB may later change.
   */
  preprocess_mode?: string
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
  preprocessMode?: string | null,
): PipelineFields {
  if (!pipelineId) return {}
  const fields: PipelineFields = {}
  if (pipelineId) fields.image_pipeline = pipelineId
  if (pipelineParams && Object.keys(pipelineParams).length > 0) {
    fields.image_pipeline_params = pipelineParams
  }
  // The preprocess switch follows the same rule as the pick above: what the
  // dialog shows is what it sends, so the document is re-parsed with the
  // settings the operator just looked at. How a document's own setting and the
  // knowledge base's default interact is decided elsewhere (see
  // applyOverridesToState), not here.
  if (preprocessMode) fields.preprocess_mode = normalizePreprocessMode(preprocessMode)
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
 * image_vector_enabled is written only when it is on or was already stored:
 * a knowledge base that never touched the switch reads as off on the
 * backend, so writing `false` into it would be a change for nothing.
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
  if (edits.imageVectorEnabled || 'image_vector_enabled' in snap) {
    built.image_vector_enabled = edits.imageVectorEnabled
  }
  // Same reasoning as the vector switch: auto is what the backend already
  // assumes, so a knowledge base that never touched this select reads the same
  // way whether the key is absent or says "auto". Writing it only when it
  // carries information keeps an untouched base's stored config untouched.
  const preprocessMode = normalizePreprocessMode(edits.preprocessMode)
  if (preprocessMode !== PREPROCESS_MODE_AUTO || 'preprocess_mode' in snap) {
    built.preprocess_mode = preprocessMode
  }
  return JSON.stringify(built) === JSON.stringify(snap) ? null : built
}

/**
 * Reads the preprocess switch out of a stored config, normalized the way the
 * panel shows it. Absent means auto.
 */
export function resolvePreprocessModeFromKb(
  kb: Record<string, any> | null | undefined,
): string {
  return normalizePreprocessMode(kb?.image_processing_config?.preprocess_mode)
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
