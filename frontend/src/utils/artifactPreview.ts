import type { ArtifactKind, ArtifactMeta } from '../api/chat'
import { resolveFilePreviewExt, resolvePreviewKind } from './filePreview'
import { getFileIcon } from './files'

type Artifact = Pick<ArtifactMeta, 'file_name'> & Partial<Pick<ArtifactMeta, 'file_type' | 'kind'>>
const ICONS: Record<ArtifactKind, string> = {
  presentation: 'file-powerpoint', web_page: 'code', spreadsheet: 'file-excel',
  document: 'file-word', image: 'image', text: 'file', other: 'file',
}

export function resolveArtifactPreview(item: Artifact, restricted = false) {
  const declared = resolveFilePreviewExt(item.file_name, item.file_type)
  const filename = resolveFilePreviewExt(item.file_name)
  const ext = resolvePreviewKind(declared) !== 'unsupported' ? declared : filename || declared
  const previewKind = resolvePreviewKind(ext)
  const inferred: ArtifactKind = previewKind === 'pptx' || ['ppt', 'odp'].includes(ext) ? 'presentation'
    : previewKind === 'html' ? 'web_page'
    : previewKind === 'excel' ? 'spreadsheet'
    : ['pdf', 'docx', 'markdown'].includes(previewKind) || ext === 'doc' ? 'document'
    : previewKind === 'image' ? 'image'
    : ['text', 'mermaid'].includes(previewKind) ? 'text' : 'other'
  const hasKind = !!item.kind && Object.hasOwn(ICONS, item.kind)
  const kind = hasKind ? item.kind! : inferred
  // Category is presentation metadata, not permission to execute HTML. Only the
  // restricted renderer may use a category as a hint for an unknown extension.
  const hintedExt = restricted && previewKind === 'unsupported'
    ? kind === 'web_page' ? 'html' : kind === 'text' ? 'txt' : ext
    : ext
  return { kind, ext: hintedExt, icon: hasKind ? ICONS[kind] : getFileIcon(item.file_name) }
}
