import assert from 'node:assert/strict'
import test from 'node:test'

// Ways this can go wrong:
//   1. nothing stored yet -> must fall back to the default sort
//   2. a stored value that is no longer a valid option (removed option,
//      hand-edited storage) -> must fall back to the default, not leak through
//   3. a saved choice is not read back on the next visit
//   4. a choice made in one workspace shows up after switching to another
//   5. the workspace id is not known yet -> must not read or write a key
//      shared by every workspace
//   6. two accounts sharing a browser inherit each other's choice
//   7. the knowledge base, agent and space lists overwrite each other's choice
//   8. storage is unavailable (private mode, disabled, quota) -> must not throw

const store = new Map<string, string>()
let storageBroken = false
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  value: {
    getItem(key: string) {
      if (storageBroken) throw new Error('storage disabled')
      return store.has(key) ? store.get(key)! : null
    },
    setItem(key: string, value: string) {
      if (storageBroken) throw new Error('storage disabled')
      store.set(key, String(value))
    },
    removeItem(key: string) {
      if (storageBroken) throw new Error('storage disabled')
      store.delete(key)
    },
  },
})

const { DEFAULT_RESOURCE_SORT } = await import('./resourceSorting')
const { loadResourceSortPreference, saveResourceSortPreference } = await import('./resourceSortPreference')

function loginAs(userId: string) {
  store.set('weknora_user', JSON.stringify({ id: userId }))
}

test.beforeEach(() => {
  store.clear()
  storageBroken = false
  loginAs('u1')
})

test('falls back to the default sort when nothing is stored', () => {
  assert.equal(loadResourceSortPreference('knowledge_bases', 10), DEFAULT_RESOURCE_SORT)
})

test('reads back the saved sort on the next visit', () => {
  saveResourceSortPreference('knowledge_bases', 10, 'name_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases', 10), 'name_asc')
})

test('ignores stored values that are not a known sort option', () => {
  for (const raw of ['', 'name', 'NAME_ASC', 'updated_desc ', '{"value":"name_asc"}']) {
    store.set('WeKnora_u1_t10_resource_sort_knowledge_bases', raw)
    assert.equal(loadResourceSortPreference('knowledge_bases', 10), DEFAULT_RESOURCE_SORT, `raw=${JSON.stringify(raw)}`)
  }
})

test('remembers the choice per workspace', () => {
  saveResourceSortPreference('knowledge_bases', 10, 'name_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases', 20), DEFAULT_RESOURCE_SORT)
  saveResourceSortPreference('knowledge_bases', 20, 'created_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases', 10), 'name_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases', 20), 'created_asc')
})

test('does nothing until the workspace is known', () => {
  for (const tenantId of [null, undefined, 0]) {
    saveResourceSortPreference('knowledge_bases', tenantId, 'name_asc')
    assert.deepEqual([...store.keys()], ['weknora_user'], `tenantId=${tenantId}`)
    assert.equal(loadResourceSortPreference('knowledge_bases', tenantId), DEFAULT_RESOURCE_SORT)
  }
})

test('keeps each list independent', () => {
  saveResourceSortPreference('knowledge_bases', 10, 'name_asc')
  saveResourceSortPreference('agents', 10, 'created_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases', 10), 'name_asc')
  assert.equal(loadResourceSortPreference('agents', 10), 'created_asc')
  assert.equal(loadResourceSortPreference('organizations', 10), DEFAULT_RESOURCE_SORT)
})

test('does not share the choice between accounts on the same browser', () => {
  saveResourceSortPreference('knowledge_bases', 10, 'name_desc')
  loginAs('u2')
  assert.equal(loadResourceSortPreference('knowledge_bases', 10), DEFAULT_RESOURCE_SORT)
  loginAs('u1')
  assert.equal(loadResourceSortPreference('knowledge_bases', 10), 'name_desc')
})

test('degrades to the default when storage is unavailable', () => {
  saveResourceSortPreference('knowledge_bases', 10, 'name_asc')
  storageBroken = true
  const warn = console.warn
  console.warn = () => {}
  try {
    assert.equal(loadResourceSortPreference('knowledge_bases', 10), DEFAULT_RESOURCE_SORT)
    assert.doesNotThrow(() => saveResourceSortPreference('knowledge_bases', 10, 'created_desc'))
  } finally {
    console.warn = warn
  }
})
