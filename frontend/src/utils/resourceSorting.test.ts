import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_RESOURCE_SORT,
  RESOURCE_SORT_OPTIONS,
  getResourceSortOption,
  sortResourceItems,
  sortResourcesWithinGroups,
  type ResourceSortAccessors,
} from './resourceSorting'

interface Item {
  id: string
  group: 'mine' | 'shared'
  name?: string
  created_at?: string
  updated_at?: string
}

const accessors: ResourceSortAccessors<Item> = {
  getName: item => item.name,
  getCreatedAt: item => item.created_at,
  getUpdatedAt: item => item.updated_at,
}

const items: Item[] = [
  { id: 'old-z', group: 'mine', name: 'Zulu 10', created_at: '2024-01-01', updated_at: '2024-03-01' },
  { id: 'new-a', group: 'mine', name: 'Alpha 2', created_at: '2024-02-01', updated_at: '2024-04-01' },
  { id: 'middle-a', group: 'mine', name: 'Alpha 11', created_at: '2024-01-15', updated_at: '2024-03-15' },
]

test('资源排序默认使用更新时间倒序并提供三组共六个选项', () => {
  assert.equal(DEFAULT_RESOURCE_SORT, 'updated_desc')
  assert.deepEqual(
    RESOURCE_SORT_OPTIONS.map(({ value, sortBy, sortOrder }) => [value, sortBy, sortOrder]),
    [
      ['updated_desc', 'updated_at', 'desc'],
      ['updated_asc', 'updated_at', 'asc'],
      ['created_desc', 'created_at', 'desc'],
      ['created_asc', 'created_at', 'asc'],
      ['name_asc', 'name', 'asc'],
      ['name_desc', 'name', 'desc'],
    ],
  )
  assert.deepEqual(
    sortResourceItems(items, DEFAULT_RESOURCE_SORT, accessors).map(item => item.id),
    ['new-a', 'middle-a', 'old-z'],
  )
})

test('未知资源排序值会安全回退到默认选项', () => {
  assert.equal(getResourceSortOption('invalid' as never).value, DEFAULT_RESOURCE_SORT)
})

test('资源排序支持创建时间和名称的双向排序', () => {
  assert.deepEqual(
    sortResourceItems(items, 'created_asc', accessors).map(item => item.id),
    ['old-z', 'middle-a', 'new-a'],
  )
  assert.deepEqual(
    sortResourceItems(items, 'name_asc', accessors).map(item => item.id),
    ['new-a', 'middle-a', 'old-z'],
  )
  assert.deepEqual(
    sortResourceItems(items, 'name_desc', accessors).map(item => item.id),
    ['old-z', 'middle-a', 'new-a'],
  )
})

test('排序保留固定分组顺序，只调整组内条目', () => {
  const mixed: Item[] = [
    { ...items[0], group: 'shared' },
    { ...items[1], group: 'mine' },
    { ...items[2], group: 'shared' },
  ]
  assert.deepEqual(
    sortResourcesWithinGroups(mixed, 'updated_desc', item => item.group, ['mine', 'shared'], accessors)
      .map(item => `${item.group}:${item.id}`),
    ['mine:new-a', 'shared:middle-a', 'shared:old-z'],
  )
})

test('缺少排序字段的旧数据始终排在末尾，值相同时保持原顺序', () => {
  const legacy: Item[] = [
    { id: 'first', group: 'mine', name: 'Same', updated_at: '2024-01-01' },
    { id: 'missing', group: 'mine' },
    { id: 'second', group: 'mine', name: 'same', updated_at: '2024-01-01' },
  ]
  assert.deepEqual(
    sortResourceItems(legacy, 'updated_asc', accessors).map(item => item.id),
    ['first', 'second', 'missing'],
  )
  assert.deepEqual(
    sortResourceItems(legacy, 'name_desc', accessors).map(item => item.id),
    ['first', 'second', 'missing'],
  )
})
