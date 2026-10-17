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
