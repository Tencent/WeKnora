export interface KnowledgeListQueryParams {
  page: number
  page_size: number
  tag_ids?: string
  keyword?: string
  file_type?: string
  parse_status?: string
  source?: string
  start_time?: string
  end_time?: string
  folder_id?: string | null
}

export interface FolderListQueryOptions {
  all?: boolean
  parentId?: string | null
}

function appendOptionalValue(query: URLSearchParams, key: string, value?: string) {
  if (value) query.append(key, value)
}

export function buildKnowledgeListSearchParams(params: KnowledgeListQueryParams): URLSearchParams {
  const query = new URLSearchParams()
  query.append('page', String(params.page))
  query.append('page_size', String(params.page_size))
  appendOptionalValue(query, 'tag_ids', params.tag_ids)
  appendOptionalValue(query, 'keyword', params.keyword)
  appendOptionalValue(query, 'file_type', params.file_type)
  appendOptionalValue(query, 'parse_status', params.parse_status)
  appendOptionalValue(query, 'source', params.source)
  appendOptionalValue(query, 'start_time', params.start_time)
  appendOptionalValue(query, 'end_time', params.end_time)

  if (params.folder_id === null) {
    query.append('folder_id', '')
  } else if (params.folder_id !== undefined) {
    query.append('folder_id', params.folder_id)
  }
  return query
}
export function buildKnowledgeListScopeKey(
  kbId: string,
  params: KnowledgeListQueryParams,
): string {
  const scopeParams = buildKnowledgeListSearchParams({ ...params, page: 1 })
  return `${kbId}?${scopeParams.toString()}`
}

export function buildFolderListSearchParams(options: FolderListQueryOptions = {}): URLSearchParams {
  const query = new URLSearchParams()
  if (options.all) {
    query.append('all', 'true')
  } else if (typeof options.parentId === 'string') {
    query.append('parent_id', options.parentId)
  }
  return query
}
