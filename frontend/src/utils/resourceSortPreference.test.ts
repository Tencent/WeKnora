import assert from 'node:assert/strict'
import test from 'node:test'

// Ways this can go wrong:
//   1. nothing stored yet -> must fall back to the default sort
//   2. a stored value that is no longer a valid option (removed option,
//      hand-edited storage) -> must fall back to the default, not leak through
//   3. a saved choice is not read back on the next visit
//   4. two accounts sharing a browser inherit each other's choice
//   5. the knowledge base, agent and space lists overwrite each other's choice
//   6. storage is unavailable (private mode, disabled, quota) -> must not throw

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
  assert.equal(loadResourceSortPreference('knowledge_bases'), DEFAULT_RESOURCE_SORT)
})

test('reads back the saved sort on the next visit', () => {
  saveResourceSortPreference('knowledge_bases', 'name_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases'), 'name_asc')
})

test('ignores stored values that are not a known sort option', () => {
  for (const raw of ['', 'name', 'NAME_ASC', 'updated_desc ', '{"value":"name_asc"}']) {
    store.set('WeKnora_u1_resource_sort_knowledge_bases', raw)
    assert.equal(loadResourceSortPreference('knowledge_bases'), DEFAULT_RESOURCE_SORT, `raw=${JSON.stringify(raw)}`)
  }
})

test('keeps each list independent', () => {
  saveResourceSortPreference('knowledge_bases', 'name_asc')
  saveResourceSortPreference('agents', 'created_asc')
  assert.equal(loadResourceSortPreference('knowledge_bases'), 'name_asc')
  assert.equal(loadResourceSortPreference('agents'), 'created_asc')
  assert.equal(loadResourceSortPreference('organizations'), DEFAULT_RESOURCE_SORT)
})

test('does not share the choice between accounts on the same browser', () => {
  saveResourceSortPreference('knowledge_bases', 'name_desc')
  loginAs('u2')
  assert.equal(loadResourceSortPreference('knowledge_bases'), DEFAULT_RESOURCE_SORT)
  loginAs('u1')
  assert.equal(loadResourceSortPreference('knowledge_bases'), 'name_desc')
})

test('degrades to the default when storage is unavailable', () => {
  saveResourceSortPreference('knowledge_bases', 'name_asc')
  storageBroken = true
  const warn = console.warn
  console.warn = () => {}
  try {
    assert.equal(loadResourceSortPreference('knowledge_bases'), DEFAULT_RESOURCE_SORT)
    assert.doesNotThrow(() => saveResourceSortPreference('knowledge_bases', 'created_desc'))
  } finally {
    console.warn = warn
  }
})
