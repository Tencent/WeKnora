import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { DEFAULT_BRAND } from '../brand/defaults.ts'
import { productLineStorageKey, resolveLine } from '../brand/productLines.ts'

/**
 * 产线偏好（AC-19）与「切换只改界面」（AC-18 / C7）。
 *
 * productLine store 通过 `@/` 别名引用 auth / brand，`tsx --test` 默认不解析
 * tsconfig.app 的 paths，所以这里沿用 settingsNavigation.test.ts 的做法：
 * 抽出真实 store 定义，用假的 auth store 和真实 Vue 响应式在 vm 里跑。
 */

const LINE_1 = DEFAULT_BRAND.productLines[0]!.id
const LINE_2 = DEFAULT_BRAND.productLines[1]!.id

const storeSource = readFileSync(new URL('./productLine.ts', import.meta.url), 'utf8')
const storeAst = ts.createSourceFile('productLine.ts', storeSource, ts.ScriptTarget.Latest, true)
const storeStatement = storeAst.statements.find(
  (statement) =>
    ts.isVariableStatement(statement) &&
    statement.declarationList.declarations.some(
      (declaration) => ts.isIdentifier(declaration.name) && declaration.name.text === 'useProductLineStore',
    ),
)
assert.ok(storeStatement, 'productLine.ts must define useProductLineStore')
const compiledStore = ts.transpileModule(
  storeStatement.getText(storeAst).replace(/^export\s+/, ''),
  { compilerOptions: { target: ts.ScriptTarget.ES2022 } },
).outputText

interface StorageHarness {
  data: Map<string, string>
  writes: Array<{ key: string; value: string }>
  restore: () => void
}

function installStorage(initial: Record<string, string> = {}): StorageHarness {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const data = new Map(Object.entries(initial))
  const writes: Array<{ key: string; value: string }> = []
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => data.get(key) ?? null,
      setItem: (key: string, value: string) => {
        writes.push({ key, value })
        data.set(key, value)
      },
      removeItem: (key: string) => {
        data.delete(key)
      },
    },
  })
  return {
    data,
    writes,
    restore: () => {
      if (original) Object.defineProperty(globalThis, 'localStorage', original)
      else Reflect.deleteProperty(globalThis, 'localStorage')
    },
  }
}

function createStore(userId = 'user-1'): {
  store: {
    currentLineId: { value: string }
    currentLine: { value: unknown }
    lines: { value: Array<{ id: string; name: string }> }
    selectLine: (id: string) => void
  }
  authState: { user: { id: string } | null }
} {
  const authState = reactive<{ user: { id: string } | null }>({ user: userId ? { id: userId } : null })
  const fakeAuthStore = {
    get currentUserId() {
      return authState.user?.id || ''
    },
  }
  const defineStore = (_id: string, setup: () => unknown) => setup
  const store = runInNewContext(`${compiledStore}\nuseProductLineStore()`, {
    localStorage: (globalThis as { localStorage?: unknown }).localStorage,
    // 显式传入宿主 console，让 M10 的 catch 告警能被测试捕获（vm 自带的 console 不共享实例）。
    console,
    computed,
    ref,
    watch,
    defineStore,
    brandConfig: { productLines: DEFAULT_BRAND.productLines },
    productLineStorageKey,
    resolveLine,
    useAuthStore: () => fakeAuthStore,
  }) as ProductLineStoreShape
  return { store, authState }
}

interface ProductLineStoreShape {
  currentLineId: { value: string }
  currentLine: { value: { name: string } | null }
  lines: { value: Array<{ id: string; name: string }> }
  selectLine: (id: string) => void
}

test('first visit defaults to the first product line', () => {
  const storage = installStorage()
  try {
    const { store } = createStore()
    assert.equal(store.currentLineId.value, LINE_1)
    assert.equal((store.currentLine.value as { name: string }).name, DEFAULT_BRAND.productLines[0]!.name)
    assert.deepEqual(store.lines.value.map((line) => line.id), [LINE_1, LINE_2])
  } finally {
    storage.restore()
  }
})

test('stored preference is read from the per-user key', () => {
  const key = productLineStorageKey('user-1')
  const storage = installStorage({ [key]: LINE_2 })
  try {
    const { store } = createStore('user-1')
    assert.equal(store.currentLineId.value, LINE_2)
  } finally {
    storage.restore()
  }
})

test('selectLine persists under the user key and never touches other storage keys', () => {
  const key = productLineStorageKey('user-1')
  const storage = installStorage()
  try {
    const { store } = createStore('user-1')
    storage.writes.length = 0
    store.selectLine(LINE_2)
    assert.equal(store.currentLineId.value, LINE_2)
    assert.deepEqual(storage.writes, [{ key, value: LINE_2 }])
    for (const write of storage.writes) {
      assert.equal(write.key.startsWith('weknora_customer_line:'), true)
    }
  } finally {
    storage.restore()
  }
})

test('invalid stored value falls back to the first line and is repaired', () => {
  const key = productLineStorageKey('user-1')
  const storage = installStorage({ [key]: 'deleted-line' })
  try {
    const { store } = createStore('user-1')
    assert.equal(store.currentLineId.value, LINE_1)
    assert.equal(storage.data.get(key), LINE_1, 'bad value should be overwritten with the fallback')
  } finally {
    storage.restore()
  }
})

test('selectLine falls back to the first line for an unknown id (documented behaviour)', () => {
  const storage = installStorage()
  try {
    const { store } = createStore('user-1')
    store.selectLine(LINE_2)
    store.selectLine('does-not-exist')
    assert.equal(store.currentLineId.value, LINE_1)
  } finally {
    storage.restore()
  }
})

test('switching accounts rehydrates from the new user key and does not leak preferences', async () => {
  const storage = installStorage({
    [productLineStorageKey('user-1')]: LINE_2,
    [productLineStorageKey('user-2')]: LINE_1,
  })
  try {
    const { store, authState } = createStore('user-1')
    assert.equal(store.currentLineId.value, LINE_2)

    authState.user = { id: 'user-2' }
    await nextTick()
    assert.equal(store.currentLineId.value, LINE_1, 'user-2 keeps their own preference')

    authState.user = { id: 'user-3' }
    await nextTick()
    assert.equal(store.currentLineId.value, LINE_1, 'a brand-new user starts on the first line')
  } finally {
    storage.restore()
  }
})

test('logged-out store reads nothing and writes nothing', () => {
  const storage = installStorage()
  try {
    const { store } = createStore('')
    assert.equal(store.currentLineId.value, LINE_1)
    storage.writes.length = 0
    store.selectLine(LINE_2)
    assert.deepEqual(storage.writes, [], 'no shared key without a user id')
    assert.equal(store.currentLineId.value, LINE_2)
  } finally {
    storage.restore()
  }
})

test('productLine store is UI-only: no selectAgent/toggleAgent/session or KB switching', () => {
  assert.doesNotMatch(storeSource, /selectAgent\s*\(/)
  assert.doesNotMatch(storeSource, /toggleAgent\s*\(/)
  assert.doesNotMatch(storeSource, /selectedKnowledgeBases|selectedFiles|vectorstore|vector-store/i)
  assert.doesNotMatch(storeSource, /createSession|rebuildSession|resetSession/)
})

test('setItem failures (private mode / quota) keep the in-memory selection working (M10)', () => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const originalWarn = console.warn
  const warnings: string[] = []
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: () => null,
      setItem: () => {
        throw new Error('QuotaExceededError')
      },
    },
  })
  console.warn = (...args: unknown[]) => {
    warnings.push(args.map((arg) => String(arg)).join(' '))
  }
  try {
    const { store } = createStore('user-1')
    assert.doesNotThrow(() => store.selectLine(LINE_2))
    assert.equal(store.currentLineId.value, LINE_2, 'memory state still switches when persistence fails')
    assert.ok(warnings.some((line) => line.includes('保存产线偏好失败')))
  } finally {
    console.warn = originalWarn
    if (original) Object.defineProperty(globalThis, 'localStorage', original)
    else Reflect.deleteProperty(globalThis, 'localStorage')
  }
})

test('productLineStorageKey is namespaced per user', () => {
  assert.equal(productLineStorageKey('abc'), 'weknora_customer_line:abc')
  assert.notEqual(productLineStorageKey('abc'), productLineStorageKey('def'))
})
