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
