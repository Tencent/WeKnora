import { fileSizeVerification, kbFileTypeVerification } from '@/utils'

export const UPLOAD_VIDEO_EXTENSIONS = ['mp4', 'mov', 'avi', 'mkv', 'webm', 'wmv', 'flv']

/**
 * The slice of a knowledge-base detail this file needs to decide whether the
 * upload menu may offer videos. The upload call itself re-checks everything on
 * the server; these helpers only keep the menu from hiding a supported format.
 */
export interface VideoParsingKbInfo {
  video_config?: {
    enabled?: boolean
    mode?: string
    model_id?: string
    // The direct read's own video model, independent of the ASR and frame
    // models above.
    video_model_id?: string
    // The video section owns its frame-description model; the knowledge base's
    // top-level vlm_config (image processing) never makes a video parseable.
    vlm_config?: {
      enabled?: boolean
      model_id?: string
    } | null
  } | null
  vlm_config?: {
    enabled?: boolean
    model_id?: string
    model_name?: string
    base_url?: string
  } | null
}

const VIDEO_PARSE_MODES = ['audio_only', 'keyframes', 'audio_and_keyframes', 'video_direct'] as const
type VideoParseMode = (typeof VIDEO_PARSE_MODES)[number]

// Rows saved before the mode existed decode to an empty value; the backend
// normalises both that and an unknown mode to audio_only, so mirror it.
function normalizeVideoParseMode(mode?: string): VideoParseMode {
  const trimmed = mode?.trim()
  return VIDEO_PARSE_MODES.includes(trimmed as VideoParseMode)
    ? (trimmed as VideoParseMode)
    : 'audio_only'
}

/**
 * Whether the knowledge base opted into video parsing. The switch, not the
 * browser, is the source of truth: off keeps the historic "videos are skipped"
 * behaviour, on lets the file reach the backend gate.
 */
export function isVideoParsingEnabled(kbInfo?: VideoParsingKbInfo | null): boolean {
  return kbInfo?.video_config?.enabled === true
}

/**
 * Best-effort mirror of the server's import gate: the model the selected mode
 * needs (ASR for narration, VLM for frames, a video model for the direct read)
 * must be configured or the upload is rejected with a 400. Used for an early
 * warning only — a stale or partial detail must never block the upload the
 * server would accept.
 */
export function isVideoParsingReady(kbInfo?: VideoParsingKbInfo | null): boolean {
  if (!isVideoParsingEnabled(kbInfo)) return false
  const mode = normalizeVideoParseMode(kbInfo?.video_config?.mode)
  const usesAudio = mode === 'audio_only' || mode === 'audio_and_keyframes'
  const usesFrames = mode === 'keyframes' || mode === 'audio_and_keyframes'
  const usesDirect = mode === 'video_direct'
  // The video's own ASR model is the only one that counts: the audio section's
  // model never makes a video parseable, exactly like the backend.
  const audioReady = usesAudio && !!kbInfo?.video_config?.model_id?.trim()
  // Frames use the video section's own vision model. The image section's
  // vlm_config is a different setting and must not make a video look ready:
  // the server would archive it. `enabled` mirrors VLMConfig.IsEnabled, which
  // requires both the flag and a model id.
  const frameVLM = kbInfo?.video_config?.vlm_config
  const framesReady =
    usesFrames && frameVLM?.enabled === true && !!frameVLM.model_id?.trim()
  // The direct read is satisfied only by the video section's own video model;
  // the ASR and frame models are its fallback, not its prerequisite.
  const directReady = usesDirect && !!kbInfo?.video_config?.video_model_id?.trim()
  return audioReady || framesReady || directReady
}

export function getUploadFileExt(file: File): string {
  const dot = file.name.lastIndexOf('.')
  if (dot < 0) return ''
  return file.name.substring(dot + 1).toLowerCase()
}

export function getUploadFileKey(file: File): string {
  const path = (file as File & { webkitRelativePath?: string }).webkitRelativePath || ''
  return `${path || file.name}\0${file.size}`
}

export interface FilterUploadFilesOptions {
  supportedFileTypes?: Set<string> | string[]
  fromFolder?: boolean
  multiFile?: boolean
  /**
   * Admit video files instead of filtering them out. The caller derives it from
   * the knowledge base's `video_config.enabled`; the backend still decides
   * whether the video can be parsed (a missing ASR/VLM model is a 400).
   */
  allowVideo?: boolean
}

export interface FilterUploadFilesResult {
  validFiles: File[]
  skippedCount: number
  videoFilteredCount: number
  hiddenFileCount: number
}

export function filterUploadFiles(
  files: FileList | File[],
  options: FilterUploadFilesOptions = {},
): FilterUploadFilesResult {
  const list = Array.from(files)
  const dynamicTypesRaw = options.supportedFileTypes
    ? options.supportedFileTypes instanceof Set
      ? options.supportedFileTypes
      : new Set(options.supportedFileTypes)
    : undefined
  // An empty set means the parser-engine list hasn't loaded yet (race with the
  // async fetch on mount). Treat it as "unknown" and fall back to the default
  // whitelist instead of rejecting every file as unsupported.
  const dynamicTypes = dynamicTypesRaw && dynamicTypesRaw.size > 0 ? dynamicTypesRaw : undefined

  const validFiles: File[] = []
  let skippedCount = 0
  let videoFilteredCount = 0
  let hiddenFileCount = 0
  const multiFile = options.multiFile ?? list.length > 1
  const allowVideo = options.allowVideo === true

  for (const file of list) {
    if (options.fromFolder) {
      const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
      if (relativePath.split('/').some(part => part.startsWith('.'))) {
        hiddenFileCount++
        continue
      }
    }

    const fileExt = getUploadFileExt(file)
    if (UPLOAD_VIDEO_EXTENSIONS.includes(fileExt)) {
      if (!allowVideo) {
        videoFilteredCount++
        continue
      }
      // Video parsing is a knowledge-base switch, not a parser-engine rule: the
      // engine registry never lists video extensions, so bypass that whitelist
      // (the backend admits the family whenever the switch and its model are
      // ready) and keep only the shared size guard.
      if (fileSizeVerification(file, multiFile)) {
        skippedCount++
        continue
      }
      validFiles.push(file)
      continue
    }

    if (kbFileTypeVerification(file, multiFile, dynamicTypes)) {
      skippedCount++
      continue
    }

    validFiles.push(file)
  }

  return { validFiles, skippedCount, videoFilteredCount, hiddenFileCount }
}
