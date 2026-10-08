import assert from 'node:assert/strict'
import test from 'node:test'
import { countResourcesByCategory, hasToolboxCategory } from './toolboxCategories'

test('tab counts include untagged resources in the total and count shared tags independently', () => {
  const a = { id: 'a', name: 'Contracts' }, b = { id: 'b', name: 'Customers' }
  assert.deepEqual(countResourcesByCategory([
    { categories: [a, b] }, { categories: [a] }, {},
  ]), { '': 3, a: 2, b: 1 })
  assert.deepEqual(countResourcesByCategory([]), { '': 0 })
})
import enUS from '../i18n/locales/en-US'
import zhCN from '../i18n/locales/zh-CN'

test('an empty selection includes categorized and uncategorized resources', () => {
  assert.equal(hasToolboxCategory(undefined, ''), true)
  assert.equal(hasToolboxCategory([{ id: 'a', name: 'Contracts' }], ''), true)
})

test('a resource can match any one of its tags', () => {
  const categories = [
    { id: 'a', name: 'Contracts' },
    { id: 'b', name: 'Customer relations' },
  ]
  assert.equal(hasToolboxCategory(categories, 'a'), true)
  assert.equal(hasToolboxCategory(categories, 'b'), true)
  assert.equal(hasToolboxCategory(categories, 'c'), false)
})

test('toolbox labels use Tags and 标签 consistently for the main actions', () => {
  for (const [messages, expected] of [
    [enUS.toolboxCategories, ['Tags', 'All tags', 'Manage tags', 'Edit tags']],
    [zhCN.toolboxCategories, ['标签', '全部标签', '管理标签', '设置标签']],
  ] as const) {
    assert.deepEqual([messages.label, messages.all, messages.manage, messages.assign], expected)
  }
})
