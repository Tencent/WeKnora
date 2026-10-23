// Fallback whitelist used only while the engine registry is still loading
// (uploadSources.ts drops an empty dynamic set). Keep it in sync with the
// backend's supportedImportFileExtensions so a file dropped in that window is
// not rejected by the client and then accepted by the server, or vice versa
// (#4598).
const DEFAULT_VALID_TYPES = new Set([
  "pdf",
  "txt",
  "md",
  "markdown",
  "docx",
  "doc",
  "docm",
  "pptx",
  "ppt",
  "pptm",
  "odt",
  "ods",
  "odp",
  "rtf",
  "epub",
  "xmind",
  "html",
  "htm",
  "mhtml",
  "jpg",
  "jpeg",
  "png",
  "gif",
  "bmp",
  "tiff",
  "webp",
  "csv",
  "xlsx",
  "xls",
  "xlsm",
  "json",
  "mp3",
  "wav",
  "m4a",
  "flac",
  "ogg",
]);

export function shouldRejectKnowledgeFileType(
  filename: string,
  validTypes?: Set<string> | string[],
) {
  const provided = validTypes
    ? validTypes instanceof Set
      ? validTypes
      : new Set(validTypes)
    : undefined;
  const allowed = provided && provided.size > 0 ? provided : DEFAULT_VALID_TYPES;
  const type = filename.substring(filename.lastIndexOf(".") + 1).toLowerCase();

  return !allowed.has(type);
}
