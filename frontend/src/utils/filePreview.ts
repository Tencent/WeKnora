/**
 * Shared file-preview type resolution used by DocumentPreview, chat
 * attachments, and skill-generated artifacts.
 *
 * Unknown extensions are not immediately rejected: callers can fetch a
 * short byte prefix and run sniffPreview() so skill outputs without a
 * conventional suffix (or with a misleading one) still open when the
 * content is HTML, SVG, JSON-like text, PDF, images, audio, or video.
 */

export type FilePreviewKind =
  | 'pdf'
  | 'docx'
  | 'pptx'
  | 'image'
  | 'excel'
  | 'text'
  | 'markdown'
  | 'html'
  | 'audio'
  | 'video'
  | 'mermaid'
  | 'unsupported'

export type FilePreviewSniff = {
  kind: FilePreviewKind
  ext: string
}

const KIND_BY_EXT: Record<string, FilePreviewKind> = {}

function register(kind: FilePreviewKind, exts: string[]) {
  for (const ext of exts) {
    KIND_BY_EXT[ext] = kind
  }
}

register('pdf', ['pdf'])
register('docx', ['docx', 'doc'])
register('pptx', ['pptx', 'ppt'])
register('image', [
  'jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tiff', 'tif', 'svg', 'svgz',
  'ico', 'avif', 'apng', 'jfif', 'pjpeg', 'pjp', 'heic', 'heif',
])
register('excel', ['xlsx', 'xls', 'csv', 'tsv', 'tab'])
register('markdown', ['md', 'markdown', 'mdown', 'mkd', 'mdx'])
register('html', ['html', 'htm', 'xhtml'])
register('audio', ['mp3', 'wav', 'm4a', 'flac', 'ogg', 'oga', 'aac', 'opus', 'weba'])
register('video', ['mp4', 'webm', 'ogv', 'm4v', 'mov'])
register('mermaid', ['mmd', 'mermaid'])
register('text', [
  'txt', 'text', 'log', 'out', 'err',
  'json', 'jsonc', 'json5', 'geojson', 'ipynb', 'jsonl', 'ndjson', 'har',
  'xml', 'xsl', 'xsd', 'dtd',
  'css', 'scss', 'less', 'sass', 'styl',
  'js', 'mjs', 'cjs', 'jsx', 'ts', 'tsx', 'mts', 'cts',
  'vue', 'svelte', 'astro',
  'py', 'pyi', 'pyw', 'java', 'kt', 'kts', 'scala', 'groovy', 'gradle',
  'go', 'rs', 'rb', 'php', 'cs', 'fs', 'fsx',
  'c', 'h', 'cc', 'cpp', 'cxx', 'hpp', 'hxx', 'hh',
  'swift', 'mm',
  'sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd',
  'yaml', 'yml', 'toml', 'ini', 'cfg', 'conf', 'config', 'env', 'properties',
  'editorconfig', 'gitignore', 'dockerignore', 'npmignore', 'gitattributes',
  'sql', 'graphql', 'gql', 'proto', 'prisma',
  'r', 'lua', 'pl', 'pm', 'tcl',
  'dart', 'ex', 'exs', 'erl', 'hrl', 'hs', 'lhs', 'ml', 'mli',
  'clj', 'cljs', 'cljc', 'lisp', 'el', 'scm', 'rkt',
  'vim', 'diff', 'patch',
  'makefile', 'mk', 'cmake', 'dockerfile',
  'tf', 'hcl', 'nix', 'zig', 'nim',
  'asm', 's',
  'rst', 'adoc', 'asciidoc', 'org', 'tex', 'latex',
  'lock', 'sum', 'mod',
])

const MIME_BY_EXT: Record<string, string> = {
  pdf: 'application/pdf',
  docx: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  doc: 'application/msword',
  pptx: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  ppt: 'application/vnd.ms-powerpoint',
  xlsx: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
  xls: 'application/vnd.ms-excel',
  csv: 'text/csv',
  tsv: 'text/tab-separated-values',
  tab: 'text/tab-separated-values',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  png: 'image/png',
  gif: 'image/gif',
  bmp: 'image/bmp',
  webp: 'image/webp',
  tiff: 'image/tiff',
  tif: 'image/tiff',
  svg: 'image/svg+xml',
  svgz: 'image/svg+xml',
  ico: 'image/x-icon',
  avif: 'image/avif',
  apng: 'image/apng',
  heic: 'image/heic',
  heif: 'image/heif',
  txt: 'text/plain',
  md: 'text/markdown',
  markdown: 'text/markdown',
  json: 'application/json',
  geojson: 'application/geo+json',
  xml: 'application/xml',
  html: 'text/html',
  htm: 'text/html',
  xhtml: 'application/xhtml+xml',
  css: 'text/css',
  js: 'text/javascript',
  mjs: 'text/javascript',
  ts: 'text/typescript',
  py: 'text/x-python',
  java: 'text/x-java',
  go: 'text/x-go',
  mp3: 'audio/mpeg',
  wav: 'audio/wav',
  m4a: 'audio/mp4',
  flac: 'audio/flac',
  ogg: 'audio/ogg',
  oga: 'audio/ogg',
  aac: 'audio/aac',
  opus: 'audio/opus',
  mp4: 'video/mp4',
  webm: 'video/webm',
  ogv: 'video/ogg',
  m4v: 'video/mp4',
  mov: 'video/quicktime',
  mmd: 'text/plain',
  mermaid: 'text/plain',
}

const HIGHLIGHT_LANG_BY_EXT: Record<string, string> = {
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  mts: 'typescript',
  cts: 'typescript',
  py: 'python',
  pyi: 'python',
  rb: 'ruby',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  fish: 'bash',
  yml: 'yaml',
  md: 'markdown',
  mdx: 'markdown',
  rs: 'rust',
  kt: 'kotlin',
  kts: 'kotlin',
  pl: 'perl',
  pm: 'perl',
  conf: 'ini',
  cfg: 'ini',
  env: 'ini',
  properties: 'ini',
  editorconfig: 'ini',
  log: 'plaintext',
  vue: 'xml',
  svelte: 'xml',
  astro: 'xml',
  html: 'xml',
  htm: 'xml',
  xhtml: 'xml',
  svg: 'xml',
  less: 'css',
  scss: 'scss',
  sass: 'scss',
  ipynb: 'json',
  geojson: 'json',
  jsonc: 'json',
  json5: 'json',
  jsonl: 'json',
  ndjson: 'json',
  har: 'json',
  proto: 'protobuf',
  gql: 'graphql',
  ps1: 'powershell',
  bat: 'dos',
  cmd: 'dos',
  dockerfile: 'dockerfile',
  makefile: 'makefile',
  cmake: 'cmake',
  tf: 'hcl',
  hcl: 'hcl',
  dart: 'dart',
  ex: 'elixir',
  exs: 'elixir',
  erl: 'erlang',
  hs: 'haskell',
  fs: 'fsharp',
  clj: 'clojure',
  lisp: 'lisp',
  vim: 'vim',
  diff: 'diff',
  patch: 'diff',
  tex: 'latex',
  prisma: 'javascript',
  mmd: 'plaintext',
  mermaid: 'plaintext',
}

const JSON_PRETTY_EXTS = new Set(['json', 'jsonc', 'geojson', 'ipynb', 'har'])

const DEFAULT_EXT_BY_KIND: Record<FilePreviewKind, string> = {
  pdf: 'pdf',
  docx: 'docx',
  pptx: 'pptx',
  image: 'png',
  excel: 'csv',
  text: 'txt',
  markdown: 'md',
  html: 'html',
  audio: 'mp3',
  video: 'mp4',
  mermaid: 'mmd',
  unsupported: '',
}

const SNIFF_BYTES = 16384

/** Strip a leading dot and lowercase. Prefers an explicit type over the filename suffix. */
export function resolveFilePreviewExt(fileName?: string, fileType?: string): string {
  const normalizedType = String(fileType || '')
    .trim()
    .replace(/^\./, '')
    .toLowerCase()
  if (normalizedType) return normalizedType
  const name = String(fileName || '')
  const dot = name.lastIndexOf('.')
  if (dot < 0 || dot === name.length - 1) return ''
  return name.slice(dot + 1).toLowerCase()
}

export function resolvePreviewKind(ext: string): FilePreviewKind {
  if (!ext) return 'unsupported'
  return KIND_BY_EXT[ext.toLowerCase()] || 'unsupported'
}

export function isKnownPreviewableExt(ext: string): boolean {
  return resolvePreviewKind(ext) !== 'unsupported'
}

export function isKnownPreviewableFile(fileName?: string, fileType?: string): boolean {
  return isKnownPreviewableExt(resolveFilePreviewExt(fileName, fileType))
}

export function getPreviewMimeType(ext: string): string {
  return MIME_BY_EXT[ext?.toLowerCase()] || 'application/octet-stream'
}

export function getHighlightLang(ext: string): string {
  const lower = ext?.toLowerCase() || ''
  return HIGHLIGHT_LANG_BY_EXT[lower] || lower
}

export function shouldPrettyPrintJson(ext: string): boolean {
  return JSON_PRETTY_EXTS.has(ext?.toLowerCase())
}

export function prettyPrintJson(text: string): string {
  const trimmed = text.trim()
  if (!trimmed) return text
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2)
  } catch {
    return text
  }
}

/**
 * True when `bytes` is well-formed UTF-8. A truncated trailing sequence at
 * the end of a prefix sample is tolerated so sniffing a slice does not
 * reject valid text files.
 */
export function isValidUTF8(bytes: Uint8Array, allowTruncatedEnd = false): boolean {
  for (let i = 0; i < bytes.length;) {
    const b = bytes[i]
    let remaining = 0
    if (b <= 0x7F) {
      remaining = 0
    } else if ((b & 0xE0) === 0xC0) {
      remaining = 1
    } else if ((b & 0xF0) === 0xE0) {
      remaining = 2
    } else if ((b & 0xF8) === 0xF0) {
      remaining = 3
    } else {
      return false
    }
    if (i + remaining >= bytes.length) {
      return allowTruncatedEnd
    }
    for (let j = 1; j <= remaining; j++) {
      if ((bytes[i + j] & 0xC0) !== 0x80) return false
    }
    i += 1 + remaining
  }
  return true
}

function asciiTag(bytes: Uint8Array, offset: number, length: number): string {
  if (offset + length > bytes.length) return ''
  let out = ''
  for (let i = 0; i < length; i++) {
    out += String.fromCharCode(bytes[offset + i])
  }
  return out
}

function looksLikeMermaid(head: string): boolean {
  const line = head.split(/\r?\n/, 1)[0]?.trim() || ''
  if (/^(graph|flowchart)\s+[A-Za-z-]+\b/i.test(line)) return true
  return /^(sequenceDiagram|classDiagram|erDiagram|gantt|pie\b|mindmap|gitGraph|stateDiagram|journey|C4Context|requirementDiagram|quadrantChart|sankey-beta|xychart-beta|block-beta|timeline)\b/i.test(line)
}

function sniffFromMagic(bytes: Uint8Array): FilePreviewSniff | null {
  if (bytes.length >= 4 && asciiTag(bytes, 0, 4) === '%PDF') {
    return { kind: 'pdf', ext: 'pdf' }
  }
  if (bytes.length >= 8 && bytes[0] === 0x89 && asciiTag(bytes, 1, 3) === 'PNG') {
    return { kind: 'image', ext: 'png' }
  }
  if (bytes.length >= 3 && bytes[0] === 0xFF && bytes[1] === 0xD8 && bytes[2] === 0xFF) {
    return { kind: 'image', ext: 'jpg' }
  }
  if (bytes.length >= 6 && asciiTag(bytes, 0, 3) === 'GIF') {
    return { kind: 'image', ext: 'gif' }
  }
  if (bytes.length >= 12 && asciiTag(bytes, 0, 4) === 'RIFF') {
    const tag = asciiTag(bytes, 8, 4)
    if (tag === 'WEBP') return { kind: 'image', ext: 'webp' }
    if (tag === 'WAVE') return { kind: 'audio', ext: 'wav' }
    if (tag === 'AVI ') return { kind: 'video', ext: 'avi' }
  }
  if (bytes.length >= 8 && asciiTag(bytes, 4, 4) === 'ftyp') {
    return { kind: 'video', ext: 'mp4' }
  }
  if (bytes.length >= 4 && bytes[0] === 0x1A && bytes[1] === 0x45 && bytes[2] === 0xDF && bytes[3] === 0xA3) {
    return { kind: 'video', ext: 'webm' }
  }
  if (bytes.length >= 3 && asciiTag(bytes, 0, 3) === 'ID3') {
    return { kind: 'audio', ext: 'mp3' }
  }
  if (bytes.length >= 4 && bytes[0] === 0x66 && bytes[1] === 0x4C && bytes[2] === 0x61 && bytes[3] === 0x43) {
    return { kind: 'audio', ext: 'flac' }
  }
  return null
}

/**
 * Infer a preview kind from a byte prefix. Used when the filename has no
 * known extension, or when callers want a second opinion on unlabeled
 * skill output.
 */
export function sniffPreview(bytes: Uint8Array): FilePreviewSniff {
  if (!bytes || bytes.length === 0) {
    return { kind: 'text', ext: 'txt' }
  }
  const magic = sniffFromMagic(bytes)
  if (magic) return magic

  const sample = bytes.subarray(0, Math.min(bytes.length, SNIFF_BYTES))
  if (sample.includes(0)) {
    return { kind: 'unsupported', ext: '' }
  }
  if (!isValidUTF8(sample, true)) {
    return { kind: 'unsupported', ext: '' }
  }

  const head = new TextDecoder('utf-8').decode(sample).replace(/^\uFEFF/, '').trimStart()
  const headLower = head.slice(0, 512).toLowerCase()
  if (
    headLower.startsWith('<!doctype html') ||
    headLower.startsWith('<html') ||
    /^<head[\s>]/i.test(head) ||
    /^<body[\s>]/i.test(head)
  ) {
    return { kind: 'html', ext: 'html' }
  }
  if (/^<\?xml\b/i.test(head) && /<svg[\s>]/i.test(head.slice(0, 2048))) {
    return { kind: 'image', ext: 'svg' }
  }
  if (/^<svg[\s>]/i.test(head)) {
    return { kind: 'image', ext: 'svg' }
  }
  if (looksLikeMermaid(head)) {
    return { kind: 'mermaid', ext: 'mmd' }
  }
  if (
    (head.startsWith('{') || head.startsWith('[')) &&
    /"cells"\s*:/.test(head) &&
    /"nbformat"\s*:/.test(head)
  ) {
    return { kind: 'text', ext: 'ipynb' }
  }
  return { kind: 'text', ext: DEFAULT_EXT_BY_KIND.text }
}

export { SNIFF_BYTES as FILE_PREVIEW_SNIFF_BYTES }
