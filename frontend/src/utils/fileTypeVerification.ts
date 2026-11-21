import type { ParserEngineInfo } from '@/api/system';
import type { ParserEngineRule } from '@/types/knowledgeProcess';

const DEFAULT_VALID_TYPES = new Set([
  "pdf",
  "txt",
  "md",
  "docx",
  "doc",
  "pptx",
  "ppt",
  "epub",
  "xmind",
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

export function supportedKnowledgeUploadTypes(
  engines: ParserEngineInfo[],
  rules: ParserEngineRule[] = [],
): Set<string> {
  const engineByType = new Map<string, string>();
  for (const rule of rules) {
    for (const type of rule.file_types) engineByType.set(type, rule.engine);
  }
  const availableNames = new Set(engines.filter(engine => engine.Available !== false).map(engine => engine.Name));
  const supported = new Set<string>();
  for (const engine of engines) {
    for (const type of engine.FileTypes || []) {
      if (availableNames.has(engineByType.get(type) || engine.Name)) supported.add(type);
    }
  }
  return supported;
}
