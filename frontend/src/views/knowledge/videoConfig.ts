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
