import { getFileIcon } from './files'
import { resolveFilePreviewExt } from './filePreview'

/** Shared trusted SVG for Vue file rows and inline Markdown artifact cards. */
export function renderArtifactFileIcon(fileName: string): string {
  const kind = getFileIcon(fileName)
  const ext = resolveFilePreviewExt(fileName)
  // Only this restricted extension label is interpolated, never the filename.
  const label = /^[a-z0-9]{1,4}$/i.test(ext) ? ext.toUpperCase() : 'FILE'
  return `<svg class="artifact-file-icon-svg kind-${kind}" viewBox="0 0 32 38" fill="none" aria-hidden="true">`
    + '<path class="file-sheet" d="M6 1.5h13L28.5 11v22A3.5 3.5 0 0 1 25 36.5H6A3.5 3.5 0 0 1 2.5 33V5A3.5 3.5 0 0 1 6 1.5Z" />'
    + '<path class="file-fold" d="M19 1.5V8a3 3 0 0 0 3 3h6.5" />'
    + '<path class="file-lines" d="M8 14h9M8 18h14" />'
    + '<rect x="5" y="23" width="24" height="11" rx="3" fill="currentColor" />'
    + `<text x="17" y="30.8" text-anchor="middle">${label}</text></svg>`
}
