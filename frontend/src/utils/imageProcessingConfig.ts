/** One OCR trigger condition: attribute `prop` equals `is`. */
export interface ImageAttrConditionLike {
  prop: string
  is: string
}

/** The fields of the KB editor that feed image_processing_config. */
export interface ImageProcessingEdits {
  imageAttrsEnabled: boolean
  onUnobserved: boolean
  /** The registry's current default OCR conditions (GET /image-attrs/schema). */
  defaultOn: ImageAttrConditionLike[]
  /** The pipeline picked in the panel; empty means "resolve from the switch". */
  pipelineId: string
  /** That pipeline's private tunables, keyed by field. */
  pipelineParams: Record<string, unknown>
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
 * Parameters are stored whole rather than one key at a time: a key the running
 * pipeline no longer declares is dropped when the run reads it, so an old saved
 * value does not have to be pruned here.
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
  // snapshot had — including by removing it. An empty pick is not a value of
  // its own: it asks the backend to resolve from image_attrs_enabled, which it
  // already does when the field is absent, so the pair is dropped rather than
  // written as an empty pair. Dropping it is what makes clearing the pick
  // register as the change it is, instead of leaving the previous pipeline
  // silently in charge.
  if (edits.pipelineId) {
    built.image_pipeline = edits.pipelineId
  } else {
    delete built.image_pipeline
  }
  if (edits.pipelineParams && Object.keys(edits.pipelineParams).length > 0) {
    built.image_pipeline_params = edits.pipelineParams
  } else {
    delete built.image_pipeline_params
  }
  return JSON.stringify(built) === JSON.stringify(snap) ? null : built
}
