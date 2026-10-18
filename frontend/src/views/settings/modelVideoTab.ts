/**
 * The model settings page's 「视频」 tab is a real model type, not a filter.
 *
 * A video model is a row of its own (`type: "Video"`): it carries its own
 * protocol, credentials and catalog entry, and the knowledge pipeline picks it
 * separately from the ASR and frame-VLM models. The tab used to be a view over
 * `capabilities.input` on chat rows, which could never create or edit a model
 * and stayed empty whenever no saved chat row declared video input.
 *
 * These helpers keep the tab list, the tab counting and the type check on one
 * definition, so a card cannot be counted as one type and edited as another.
 */

/** Model types the settings page renders one tab each, in display order. */
export const MODEL_SETTINGS_TAB_TYPES = [
  'chat',
  'embedding',
  'rerank',
  'vllm',
  'asr',
  'video',
] as const

export type ModelSettingsTabType = typeof MODEL_SETTINGS_TAB_TYPES[number]

/** The backend spelling of the video model type. */
export const VIDEO_MODEL_TYPE = 'Video'

/** The frontend spelling used by the tab list and the model editor. */
export const VIDEO_MODEL_EDITOR_TYPE = 'video'

export interface TypedModel {
  type?: string | null
}

/** Whether a saved model row is a video model (backend spelling). */
export function isVideoModelType(type?: string | null): boolean {
  return (type || '').trim().toLowerCase() === VIDEO_MODEL_TYPE.toLowerCase()
}

/** How many models carry one frontend type (the tab's count). */
export function countModelsOfType(
  models: ReadonlyArray<{ _modelType?: string | null }>,
  type: string,
): number {
  return models.filter(model => model._modelType === type).length
}

/** How many saved models the 「视频」 tab shows. */
export function videoModelCount(models: readonly TypedModel[]): number {
  return models.filter(model => isVideoModelType(model.type)).length
}
