export interface LoadedProtectedFile {
  blob: Blob;
  blobURL: string;
  fileName: string;
}

export const RESOURCE_PREVIEW_EVENT = 'weknora:resource-preview';

export function responseFileName(disposition: string | null, source: string): string {
  let name: string | undefined;
  let encoded: string | undefined;
  const parameters = /(?:^|;)\s*([^\s=;]+)\s*=\s*(?:"((?:[^"\\]|\\.)*)"|([^";]*))(?=\s*(?:;|$))/g;
  for (const [, key, quoted, unquoted] of (disposition || '').matchAll(parameters)) {
    const value = quoted === undefined ? unquoted.trim() : quoted.replace(/\\(["\\])/g, '$1');
    if (key.toLowerCase() === 'filename' && name === undefined) name = value;
    if (key.toLowerCase() === 'filename*' && encoded === undefined) {
      encoded = value.match(/^UTF-8'[^']*'(.+)$/i)?.[1];
    }
  }
  if (encoded) {
    try { name = decodeURIComponent(encoded); } catch { /* use plain filename */ }
  }
  return (name || source.split('/').pop() || 'download').split(/[\\/]/).pop()!
    .replace(/[\x00-\x1f\x7f]/g, '').trim() || 'download';
}

// The browser cannot render an Office document as an <img>. Keep a real
// download link as fallback; the app's preview host intercepts normal clicks.
export function applyProtectedFile(img: HTMLImageElement, file: LoadedProtectedFile, source: string): void {
  const link = img.ownerDocument.createElement('a');
  link.href = file.blobURL;
  link.download = file.fileName;
  link.className = 'protected-resource-card';
  link.textContent = img.alt || file.fileName;
  link.title = file.fileName;
  link.dataset.protectedResource = source;
  img.replaceWith(link);
}
