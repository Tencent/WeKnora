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
  /**
   * The knowledge base's own OCR fallback clause. It is the value every
   * pipeline starts from, no longer a control: the panel edits the selected
   * pipeline's own copy in that pipeline's slot, and this field is carried
   * over so a base that customised it through the API keeps it.
   */
  onUnobserved: boolean
  /** The registry's current default OCR conditions (GET /image-attrs/schema). */
  defaultOn: ImageAttrConditionLike[]
  /** The pipeline picked in the panel; empty means multimodal is off and the fields go away. */
  pipelineId: string
  /**
   * Every pipeline's private tunables, each stored under the slot of the
   * pipeline that owns it — see pipelineParamKey. Switching the pick therefore
   * finds the other pipeline's own answers rather than inheriting this one's.
   */
  pipelineParams: Record<string, unknown>
}

/** The preprocess switch's values, mirroring the backend's constants. */
export const PREPROCESS_MODE_OFF = 'off'
export const PREPROCESS_MODE_AUTO = 'auto'
export const PREPROCESS_MODE_ALWAYS = 'always'

/**
 * The key the preprocessing action declares its page-straightening switch
 * under. The action owns the key; a pipeline that schedules the action offers
 * the control, and the value is stored in that pipeline's slot. Mirrors
 * types.PreprocessModeKey on the backend.
 */
export const PREPROCESS_MODE_KEY = 'preprocess.mode'

/**
 * The key the smart pipeline declares its OCR fallback clause under. Pipeline
 * private: it decides whether OCR is scheduled at all, which is that
 * pipeline's own orchestration decision. Mirrors
 * service.ImagePipelineKeyOCROnUnobserved.
 */
export const OCR_ON_UNOBSERVED_KEY = 'ocr_on_unobserved'

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
    case PREPROCESS_MODE_ALWAYS:
      return PREPROCESS_MODE_ALWAYS
    default:
      return PREPROCESS_MODE_AUTO
  }
}

/**
 * The key one pipeline-private setting is stored under. Mirrors the backend's
 * types.SlotKey: the pipeline that owns the setting namespaces it, unless the
 * key already carries that namespace (an action scoped to one pipeline, whose
 * id is "<pipeline>.<action>"), in which case it is left alone so the id is
 * never doubled.
 *
 * Panel and run must agree on this or a saved switch is never read back, which
 * is the whole reason it is a function here and in Go rather than a spelling
 * both sides repeat.
 */
export function pipelineParamKey(pipelineId: string, key: string): string {
  if (!pipelineId || key.startsWith(`${pipelineId}.`)) return key
  return `${pipelineId}.${key}`
}

/**
 * Reads one pipeline-private setting the way the backend's
 * types.ImagePipelineParam does: the running pipeline's slot first, then the
 * bare key, which is how a config written before the slots existed still
 * reads. `undefined` means neither carries a value, and the caller falls back
 * to the field's declared default.
 */
export function readPipelineParam(
  params: Record<string, unknown> | null | undefined,
  pipelineId: string,
  key: string,
): unknown {
  if (!params) return undefined
  if (pipelineId) {
    const slot = pipelineParamKey(pipelineId, key)
    const own = params[slot]
    if (own !== undefined && own !== null) return own
    // The key already carries this pipeline's namespace; the bare lookup below
    // would read the same entry twice.
    if (slot === key) return undefined
  }
  const bare = params[key]
  return bare === undefined || bare === null ? undefined : bare
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
  if (!pipelineId) return {}
  const fields: PipelineFields = { image_pipeline: pipelineId }
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
 * The editor edits the observation switch, the pick and its parameters. The
 * OCR clause travels with a non-empty `on`: the backend adopts a custom clause
 * only when `on` is set. A knowledge base whose `on` was customised through the
 * API keeps it; one without a custom list gets the registry's current default,
 * so it still follows the default as it evolves. The fallback clause beside it
 * is carried over rather than recomputed — the panel edits the selected
 * pipeline's own copy in that pipeline's slot now, so this key stays the
 * default every pipeline starts from.
 *
 * The pipeline and its parameters are written as one unit. image_vector_enabled
 * is written only when it is on or was already stored: a knowledge base that
 * never touched the switch reads as off on the backend, so writing `false` into
 * it would be a change for nothing.
 */
export function buildImageProcessingConfig(
  snapshot: Record<string, unknown> | null | undefined,
  edits: ImageProcessingEdits,
): Record<string, unknown> | null {
  const snap = snapshot || {}
  const storedOCR = (snap.image_actions as { ocr?: Record<string, unknown> } | undefined)?.ocr
  const storedOn = storedOCR?.on
  const on = Array.isArray(storedOn) && storedOn.length > 0 ? storedOn : edits.defaultOn
  const built: Record<string, unknown> = {
    ...snap,
    image_attrs_enabled: edits.imageAttrsEnabled,
    image_actions: {
      ...(snap.image_actions as Record<string, unknown> | undefined),
      ocr: { ...storedOCR, on, on_unobserved: edits.onUnobserved },
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
  return JSON.stringify(built) === JSON.stringify(snap) ? null : built
}

/**
 * The pipeline parameters a stored config amounts to, with the two settings
 * that predate the slots folded in from where they used to live.
 *
 * This is a read-time migration, not a rewrite: the backend resolves the same
 * way (types.ResolvePreprocessMode, service.resolveImageActions), so a
 * knowledge base that stored the straightening mode or the OCR fallback clause
 * before they moved into the pipeline's slot shows the value it actually runs
 * with. Without it the panel would draw those controls at their declared
 * default, and the next save would write that default into the slot and shadow
 * the setting the base was using.
 */
export function pipelineParamsFromKb(
  kb: Record<string, any> | null | undefined,
): Record<string, unknown> {
  const cfg = kb?.image_processing_config
  const params = { ...((cfg?.image_pipeline_params as Record<string, unknown>) ?? {}) }
  const pipelineId = resolveImagePipelineFromKb(kb)
  if (!pipelineId) return params
  if (readPipelineParam(params, pipelineId, PREPROCESS_MODE_KEY) === undefined && cfg?.preprocess_mode != null) {
    params[pipelineParamKey(pipelineId, PREPROCESS_MODE_KEY)] = normalizePreprocessMode(cfg.preprocess_mode)
  }
  const legacyUnobserved = cfg?.image_actions?.ocr?.on_unobserved
  if (
    typeof legacyUnobserved === 'boolean' &&
    readPipelineParam(params, pipelineId, OCR_ON_UNOBSERVED_KEY) === undefined
  ) {
    params[pipelineParamKey(pipelineId, OCR_ON_UNOBSERVED_KEY)] = legacyUnobserved
  }
  return params
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
