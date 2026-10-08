import type { Resource } from '@/api/datasource'

// How a picker row states what a listed resource is. Kept apart from the dialog
// because the selection preview renders the same facts from its own component:
// the two must not be able to disagree about a row's kind or its icon.
export const resourceTypeLabelMap: Record<string, string> = {
  wiki_space: 'datasource.resourceType.wikiSpace',
  doc_category: 'datasource.resourceType.docCategory',
  book: 'datasource.resourceType.book',
  library: 'datasource.resourceType.library',
  // DingTalk's self-addressing references are described by the connector rather
  // than listed by a workspace. The two types are kept apart because they sync
  // different things: a 多维表 ingests all of its tables as one document, while
  // each wiki document under it is a separate selection.
  base: 'datasource.dingtalk.resourceTypeBase',
  base_child: 'datasource.dingtalk.resourceTypeBaseChild',
}

export function resourceTypeLabel(type: string, t: (key: string) => string): string {
  const key = resourceTypeLabelMap[type]
  if (key) return t(key)
  return ''
}

export function shouldShowResourceType(type: string): boolean {
  return !!resourceTypeLabelMap[type]
}

// resourceIconName picks the tree icon for one listed resource.
export function resourceIconName(r: Resource): string {
  // Seafile libraries expand like folders but are the top-level unit a data
  // source binds to, so they keep the root icon.
  if (r.type === 'library') return 'root-list'
  if (r.has_children) return 'folder'
  switch (r.type) {
    case 'wiki_space':
      return 'root-list'
    case 'book':
      return 'book'
    case 'doc_category':
      return 'folder-open'
    default:
      return 'file'
  }
}
