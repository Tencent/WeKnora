import { del, get } from '@/utils/request'

/**
 * One file in the artifact library: the latest version of a file a skill
 * generated in one of the caller's sessions. Download it through
 * downloadArtifact(session_id, message_id, index) from '@/api/chat', which
 * re-checks session ownership server side.
 */
export interface ArtifactLibraryItem {
  session_id: string
  session_title: string
  message_id: string
  index: number
  /** `resource://<handle>` when the deployment runs a resource catalog. */
  handle?: string
  file_name: string
  file_type: string
  file_size: number
  source_path: string
  created_at: string
  /** How many times this file was (re)generated in its session. */
  version_count: number
}

export interface ArtifactLibraryParams {
  keyword?: string
  /** Extensions such as ".pdf"; empty means every type. */
  fileTypes?: string[]
  page?: number
  pageSize?: number
}

export interface ArtifactLibraryPage {
  success: boolean
  data: ArtifactLibraryItem[]
  total: number
  page: number
  page_size: number
}

export function listArtifactLibrary(params: ArtifactLibraryParams = {}) {
  const query: Record<string, string | number> = {}
  const keyword = params.keyword?.trim()
  if (keyword) query.keyword = keyword
  if (params.fileTypes?.length) query.file_types = params.fileTypes.join(',')
  if (params.page) query.page = params.page
  if (params.pageSize) query.page_size = params.pageSize
  return get<ArtifactLibraryPage>('/api/v1/artifacts', { params: query })
}

export interface ArtifactDeleteResult {
  success: boolean
  data: { file_name: string; deleted: number }
}

/**
 * Deletes a file from the artifact library, along with every earlier version of
 * it in the same session — a library row is the file, not one regeneration of
 * it, so leaving the older versions behind would leave the row in place.
 *
 * The stored bytes are reclaimed unless something else still references them,
 * so this is not reversible. Callers confirm with the user first.
 */
export function deleteArtifactLibraryItem(item: ArtifactLibraryItem) {
  const query = new URLSearchParams({
    session_id: item.session_id,
    message_id: item.message_id,
    index: String(item.index),
  })
  return del<ArtifactDeleteResult>(`/api/v1/artifacts?${query.toString()}`)
}
