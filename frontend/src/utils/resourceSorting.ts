export type ResourceSortValue =
  | 'updated_desc'
  | 'updated_asc'
  | 'created_desc'
  | 'created_asc'
  | 'name_asc'
  | 'name_desc'

export type ResourceSortField = 'updated_at' | 'created_at' | 'name'

export interface ResourceSortOption {
  value: ResourceSortValue
  sortBy: ResourceSortField
  sortOrder: 'asc' | 'desc'
  labelKey:
    | 'recentlyUpdated'
    | 'earliestUpdated'
    | 'recentlyCreated'
    | 'earliestCreated'
    | 'nameAscending'
    | 'nameDescending'
}

export interface ResourceSortAccessors<T> {
  getName: (item: T) => string | null | undefined
  getUpdatedAt: (item: T) => string | number | Date | null | undefined
  getCreatedAt: (item: T) => string | number | Date | null | undefined
}

export const DEFAULT_RESOURCE_SORT: ResourceSortValue = 'updated_desc'

export const RESOURCE_SORT_OPTIONS: readonly ResourceSortOption[] = [
  { value: 'updated_desc', sortBy: 'updated_at', sortOrder: 'desc', labelKey: 'recentlyUpdated' },
  { value: 'updated_asc', sortBy: 'updated_at', sortOrder: 'asc', labelKey: 'earliestUpdated' },
  { value: 'created_desc', sortBy: 'created_at', sortOrder: 'desc', labelKey: 'recentlyCreated' },
  { value: 'created_asc', sortBy: 'created_at', sortOrder: 'asc', labelKey: 'earliestCreated' },
  { value: 'name_asc', sortBy: 'name', sortOrder: 'asc', labelKey: 'nameAscending' },
  { value: 'name_desc', sortBy: 'name', sortOrder: 'desc', labelKey: 'nameDescending' },
]

export function getResourceSortOption(value: ResourceSortValue): ResourceSortOption {
  return RESOURCE_SORT_OPTIONS.find(option => option.value === value) ?? RESOURCE_SORT_OPTIONS[0]
}

function parseTimestamp(value: string | number | Date | null | undefined): number | null {
  if (value === null || value === undefined || value === '') return null
  const timestamp = value instanceof Date
    ? value.getTime()
    : typeof value === 'number'
      ? value
      : Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

function compareNullable<T>(
  left: T | null,
  right: T | null,
  direction: 1 | -1,
  compare: (a: T, b: T) => number,
): number {
  // 缺少时间或名称的旧数据始终排在末尾，避免切换升序后突然跑到最前面。
  if (left === null && right === null) return 0
  if (left === null) return 1
  if (right === null) return -1
  return compare(left, right) * direction
}

export function sortResourceItems<T>(
  items: readonly T[],
  value: ResourceSortValue,
  accessors: ResourceSortAccessors<T>,
): T[] {
  const option = getResourceSortOption(value)
  const direction: 1 | -1 = option.sortOrder === 'asc' ? 1 : -1
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

  return items
    .map((item, index) => ({ item, index }))
    .sort((left, right) => {
      let result = 0
      if (option.sortBy === 'name') {
        const leftName = accessors.getName(left.item)?.trim() || null
        const rightName = accessors.getName(right.item)?.trim() || null
        result = compareNullable(leftName, rightName, direction, (a, b) => collator.compare(a, b))
      } else {
        const getter = option.sortBy === 'updated_at'
          ? accessors.getUpdatedAt
          : accessors.getCreatedAt
        result = compareNullable(
          parseTimestamp(getter(left.item)),
          parseTimestamp(getter(right.item)),
          direction,
          (a, b) => a - b,
        )
      }
      return result || left.index - right.index
    })
    .map(entry => entry.item)
}

export function sortResourcesWithinGroups<T, TGroup extends string>(
  items: readonly T[],
  value: ResourceSortValue,
  getGroup: (item: T) => TGroup,
  groupOrder: readonly TGroup[],
  accessors: ResourceSortAccessors<T>,
): T[] {
  const groups = new Map<TGroup, T[]>()
  for (const group of groupOrder) groups.set(group, [])
  for (const item of items) {
    const group = getGroup(item)
    const bucket = groups.get(group)
    if (bucket) bucket.push(item)
    else groups.set(group, [item])
  }

  return [...groups.values()].flatMap(group => sortResourceItems(group, value, accessors))
}
