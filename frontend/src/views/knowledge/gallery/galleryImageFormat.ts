/** A known non-previewable Office format; ordinary load errors stay generic. */
export function unsupportedGalleryFormat(mimeType: string, rawUrl: string): string {
  const mime = mimeType.split(';')[0].trim().toLowerCase()
  if (['image/emf', 'image/x-emf'].includes(mime)) return 'EMF'
  if (['image/wmf', 'image/x-wmf', 'image/x-win-metafile'].includes(mime)) return 'WMF'
  if (mime.includes('visio') || mime === 'application/vnd.ms-visio') return 'Visio'
  // Prefer the served representation over the original filename after conversion.
  if (mime.startsWith('image/') && mime !== 'image/unknown') return ''
  const extension = rawUrl.split(/[?#]/)[0].match(/\.([a-z0-9]+)$/i)?.[1].toLowerCase()
  if (extension === 'emf') return 'EMF'
  if (extension === 'wmf') return 'WMF'
  if (['vsd', 'vsdx', 'vss', 'vssx', 'vst', 'vstx'].includes(extension || '')) return 'Visio'
  if (mime === 'application/octet-stream' || extension === 'bin') return 'application/octet-stream'
  return ''
}
