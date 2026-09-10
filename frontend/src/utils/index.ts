import { MessagePlugin } from "tdesign-vue-next";
import i18n from '@/i18n';
import { shouldRejectKnowledgeFileType } from "./fileTypeVerification";

// 声明全局运行时配置类型
declare global {
  interface Window {
    __RUNTIME_CONFIG__?: {
      MAX_FILE_SIZE_MB?: number;
      MAX_SKILL_BUNDLE_SIZE_MB?: number;
      DEFAULT_LOCALE?: string;
      CHAT_ATTACHMENT_EXTRA_EXTENSIONS?: string;
    };
  }
}

function positiveMegabytes(value: unknown, fallback: number): number {
  const n = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

// 从运行时配置获取最大文件大小(MB)，支持 Docker 环境动态配置
// 优先级：运行时配置 > 构建时环境变量 > 默认值 50MB
export const MAX_FILE_SIZE_MB = positiveMegabytes(
  window.__RUNTIME_CONFIG__?.MAX_FILE_SIZE_MB ?? import.meta.env.VITE_MAX_FILE_SIZE_MB,
  50,
)
export const MAX_FILE_SIZE_BYTES = MAX_FILE_SIZE_MB * 1024 * 1024

// Skill zips are larger than knowledge files (GitHub zipballs, ppt-master).
// Never below the knowledge cap; ceiling matches the Go uncompressed archive cap.
export const MAX_SKILL_BUNDLE_SIZE_MB = Math.min(
  512,
  Math.max(
    positiveMegabytes(
      window.__RUNTIME_CONFIG__?.MAX_SKILL_BUNDLE_SIZE_MB
        ?? import.meta.env.VITE_MAX_SKILL_BUNDLE_SIZE_MB,
      256,
    ),
    MAX_FILE_SIZE_MB,
  ),
)
export const MAX_SKILL_BUNDLE_SIZE_BYTES = MAX_SKILL_BUNDLE_SIZE_MB * 1024 * 1024

// Executable formats the backend refuses even when listed in the extra
// extensions config (chat attachments land in agent sandboxes). Mirrored from
// temporaryDocumentBlockedExtensions in temporary_document.go — filtering here
// keeps the picker from offering files the upload would only reject.
const CHAT_ATTACHMENT_BLOCKED_EXTENSIONS = new Set([
  '.exe', '.dll', '.so', '.dylib', '.msi',
  '.com', '.scr', '.bat', '.cmd', '.jar',
  '.ps1', '.vbs', '.hta',
])

// Deploy-time extra chat attachment extensions (comma-separated, e.g.
// ".msg,.seq,.ab1"; the frontend entrypoint mirrors
// WEKNORA_CHAT_ATTACHMENT_EXTRA_EXTENSIONS into the runtime config). Files
// with these extensions are accepted by the chat attachment picker and
// uploaded as raw attachments: the backend stages them into the agent sandbox
// instead of parsing, so skills / MCP tools read the originals directly.
export const CHAT_ATTACHMENT_EXTRA_EXTENSIONS = parseChatAttachmentExtraExtensions(
  window.__RUNTIME_CONFIG__?.CHAT_ATTACHMENT_EXTRA_EXTENSIONS
    ?? import.meta.env.VITE_CHAT_ATTACHMENT_EXTRA_EXTENSIONS,
)

function parseChatAttachmentExtraExtensions(raw: string | undefined | null): string[] {
  if (!raw) return []
  return [...new Set(
    raw
      .split(',')
      .map(ext => ext.trim().toLowerCase())
      .filter(Boolean)
      .map(ext => (ext.startsWith('.') ? ext : `.${ext}`))
      .filter(ext => !CHAT_ATTACHMENT_BLOCKED_EXTENSIONS.has(ext)),
  )]
}

export function generateRandomString(length: number) {
  let result = "";
  const characters =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  const charactersLength = characters.length;
  for (let i = 0; i < length; i++) {
    result += characters.charAt(Math.floor(Math.random() * charactersLength));
  }
  return result;
}

export function formatStringDate(date: any) {
  let data = new Date(date);
  let year = data.getFullYear();
  let month = String(data.getMonth() + 1).padStart(2, '0');
  let day = String(data.getDate()).padStart(2, '0');
  let hour = String(data.getHours()).padStart(2, '0');
  let minute = String(data.getMinutes()).padStart(2, '0');
  let second = String(data.getSeconds()).padStart(2, '0');
  return (
    year + "-" + month + "-" + day + " " + hour + ":" + minute + ":" + second
  );
}
/** Returns true when the file exceeds the deploy-time upload limit. */
export function fileSizeVerification(file: Pick<File, 'size'>, silent = false) {
  if (file.size <= MAX_FILE_SIZE_BYTES) return false;
  if (!silent) {
    MessagePlugin.error(i18n.global.t('error.fileSizeExceeded', { size: MAX_FILE_SIZE_MB }));
  }
  return true;
}

/**
 * Returns true when the file should be **rejected**.
 * @param validTypes - override the default extension whitelist with a dynamic set (e.g. from engine registry).
 */
export function kbFileTypeVerification(file: any, silent = false, validTypes?: Set<string> | string[]) {
  if (shouldRejectKnowledgeFileType(file.name, validTypes)) {
    if (!silent) {
      MessagePlugin.error(i18n.global.t('error.unsupportedFileType'));
    }
    return true;
  }
  return fileSizeVerification(file, silent);
}
