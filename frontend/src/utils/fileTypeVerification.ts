const DEFAULT_VALID_TYPES = new Set([
  "pdf",
  "txt",
  "md",
  "docx",
  "doc",
  "pptx",
  "ppt",
  "epub",
  "html",
  "htm",
  "mhtml",
  "jpg",
  "jpeg",
  "png",
  "csv",
  "xlsx",
  "xls",
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
