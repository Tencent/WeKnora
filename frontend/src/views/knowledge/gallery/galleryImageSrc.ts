import {
  buildProtectedFileRequest,
  isProviderFileURL,
  resolveProtectedFileAccess,
  type ProtectedFileRequest,
} from '../../../utils/protectedFileAccess.ts'

/**
 * The proxy request that loads one gallery image, or null when the browser can
 * render the URL as is.
 *
 * Stored images are storage handles (local://, minio://, cos://, s3://,
 * storage://<backend>/..., resource://) that no browser can open; they go
 * through the knowledge-base file proxy, which is also what authorises a
 * viewer of a KB shared from another tenant. The scheme list is the one
 * protectedFileAccess owns, so the gallery cannot drift from chat and wiki.
 */
export function galleryImageRequest(rawUrl: string, kbId: string): ProtectedFileRequest | null {
  if (!rawUrl || !isProviderFileURL(rawUrl)) return null
  return buildProtectedFileRequest(rawUrl, resolveProtectedFileAccess({ mode: 'knowledgeBase', kbId }))
}
