/**
 * The video section's narration model is its own setting. There is no fallback
 * to the audio section: borrowing asr_config.model_id would make the bill and
 * the transcription behaviour depend on a choice the operator made for audio
 * uploads, and would hide which model the video actually used.
 */
export interface VideoAudioModelConfig {
  modelId?: string | null
}

export function isVideoAudioModelConfigured(
  videoConfig?: VideoAudioModelConfig | null,
): boolean {
  return !!videoConfig?.modelId?.trim()
}

/**
 * Frame descriptions follow the same rule for the vision model: the video
 * section's own vlm_config.model_id must be chosen. Falling back to the image
 * section would let an image-parsing change silently rewrite a video's output,
 * and a knowledge base that never enabled image parsing could not describe a
 * video at all.
 */
export interface VideoFrameModelConfig {
  vlm?: { modelId?: string | null } | null
}

export function isVideoFrameModelConfigured(
  videoConfig?: VideoFrameModelConfig | null,
): boolean {
  return !!videoConfig?.vlm?.modelId?.trim()
}

/**
 * The direct read (video_direct) sends the whole file to a video understanding
 * model, which is a third model with a third role: not the ASR model that
 * transcribes narration, not the VLM that describes sampled frames. Only
 * video_config.video_model_id counts; falling back to either of the other two
 * would hand a chat-style model a video it cannot read.
 */
export interface VideoDirectModelConfig {
  videoModelId?: string | null
}

export function isVideoDirectModelConfigured(
  videoConfig?: VideoDirectModelConfig | null,
): boolean {
  return !!videoConfig?.videoModelId?.trim()
}

/**
 * How the direct read carries the video to the model. `auto` sends files that
 * fit the model provider's inline ceiling base64 inside the request and uploads
 * the rest to the vendor's temporary storage, `base64` never stages the file
 * anywhere, `upload` always stages it, and `url` is for a video the caller
 * already serves publicly.
 */
export type VideoDirectInput = 'auto' | 'base64' | 'url' | 'upload'

const VIDEO_DIRECT_INPUTS: readonly VideoDirectInput[] = ['auto', 'base64', 'url', 'upload']

/**
 * Mirrors the backend's NormalizeVideoDirectInput: an empty value is the decode
 * result of rows written before the field existed and means auto, and an
 * unknown value is a typo the client must never act on, so it also means auto.
 */
export function normalizeVideoDirectInput(mode?: string | null): VideoDirectInput {
  const stored = mode?.trim()
  return VIDEO_DIRECT_INPUTS.includes(stored as VideoDirectInput)
    ? (stored as VideoDirectInput)
    : 'auto'
}

/**
 * Whether this transport can hand the file to the model vendor's temporary
 * storage. The compliance note under the selector keys off it: base64 keeps the
 * bytes inside the request, url means the caller hosts them.
 */
export function videoDirectInputStoresWithVendor(mode?: string | null): boolean {
  const transport = normalizeVideoDirectInput(mode)
  return transport === 'auto' || transport === 'upload'
}

