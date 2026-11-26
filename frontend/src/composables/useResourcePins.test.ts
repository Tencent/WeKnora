import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'

const source = readFileSync(new URL('./useResourcePins.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const flush = () => new Promise(resolve => setImmediate(resolve))
const rows = (id: string, type = 'kb') => ({ success: true, data: [{ resource_type: type, resource_id: id }] })

function fixture() {
  const auth = vue.reactive({ effectiveTenantId: 'a', user: { id: 'user-a' } })
  const reads: Array<{ tenant: string; type: string; resolve: (value: any) => void; reject: (error: Error) => void }> = []
  const writes: Array<ReturnType<typeof deferred<void>>> = []
  const exports: any = {}
  const scopes: vue.EffectScope[] = []
  // Retain the real detached scope semantics while cleaning up each test's
  // module-level watcher afterward.
  const trackedVue = { ...vue, effectScope(detached?: boolean) { const scope = vue.effectScope(detached); scopes.push(scope); return scope } }
  runInNewContext(compiled, {
    exports, console: { warn() {} },
    require(name: string) {
      if (name === 'vue') return trackedVue
      if (name === '@/stores/auth') return { useAuthStore: () => auth }
      if (name === './preferenceStorage') return { safeGetItem: () => null, safeSetItem() {}, readUserId: () => auth.user.id }
      if (name === '@/api/user-favorites') return {
        listFavorites(type: string) { const call = deferred<any>(); reads.push({ ...call, tenant: auth.effectiveTenantId, type }); return call.promise },
        addFavorite() { const call = deferred<void>(); writes.push(call); return call.promise },
        removeFavorite() { const call = deferred<void>(); writes.push(call); return call.promise },
      }
      throw new Error(`unexpected import ${name}`)
    },
  })
  const mount = () => {
    const scope = vue.effectScope()
    scopes.push(scope)
    const pins = scope.run(() => exports.useResourcePins())!
    return { pins, unmount: () => scope.stop() }
  }
  return { auth, reads, writes, mount, dispose: () => scopes.forEach(scope => scope.stop()) }
}

test('switching workspaces while reads are pending starts fresh reads and ignores old completions', async () => {
  const f = fixture()
  try {
    const { pins } = f.mount()
    assert.equal(f.reads.length, 2)
    f.auth.effectiveTenantId = 'b'
    await vue.nextTick()
    assert.equal(f.reads.length, 4, 'workspace B must not join workspace A requests')
    f.reads[2].resolve(rows('b-kb'))
    f.reads[3].resolve(rows('b-agent', 'agent'))
    await flush()
    f.reads[0].resolve(rows('a-kb'))
    f.reads[1].reject(new Error('late workspace A failure'))
    await flush()
    assert.equal(pins.isFavorite('kb', 'b-kb'), true)
    assert.equal(pins.isFavorite('agent', 'b-agent'), true)
    assert.equal(pins.isFavorite('kb', 'a-kb'), false)
  } finally { f.dispose() }
})

test('the shared workspace watcher survives the first list view unmounting', async () => {
  const f = fixture()
  try {
    const first = f.mount()
    f.reads[0].resolve(rows('a-kb'))
    f.reads[1].resolve(rows('a-agent', 'agent'))
    await flush()
    first.unmount()
    const second = f.mount()
    f.auth.effectiveTenantId = 'b'
    await vue.nextTick()
    assert.equal(f.reads.length, 4)
    assert.equal(second.pins.isFavorite('kb', 'a-kb'), false)
    f.reads[2].resolve(rows('b-kb'))
    f.reads[3].resolve(rows('b-agent', 'agent'))
    await flush()
    assert.equal(second.pins.isFavorite('kb', 'b-kb'), true)
  } finally { f.dispose() }
})

test('changing users within the same workspace invalidates cached favorites', async () => {
  const f = fixture()
  try {
    const { pins } = f.mount()
    f.reads[0].resolve(rows('old-user-kb'))
    f.reads[1].resolve(rows('old-user-agent', 'agent'))
    await flush()
    f.auth.user.id = 'user-b'
    await vue.nextTick()
    assert.equal(f.reads.length, 4)
    assert.equal(pins.favorites.value.length, 0)
  } finally { f.dispose() }
})

for (const remove of [true, false]) {
  test(`late ${remove ? 'remove' : 'add'} rollback cannot change the new workspace favorites`, async () => {
    const f = fixture()
    try {
      const { pins } = f.mount()
      f.reads[0].resolve(remove ? rows('shared-id') : { success: true, data: [] })
      f.reads[1].resolve({ success: true, data: [] })
      await flush()
      const toggling = pins.toggleFavorite('kb', 'shared-id')
      f.auth.effectiveTenantId = 'b'
      await vue.nextTick()
      assert.equal(f.reads.length, 4)
      f.reads[2].resolve(remove ? { success: true, data: [] } : rows('shared-id'))
      f.reads[3].resolve({ success: true, data: [] })
      await flush()
      f.writes[0].reject(new Error('old mutation failed'))
      await toggling
      assert.equal(pins.isFavorite('kb', 'shared-id'), !remove)
    } finally { f.dispose() }
  })
}

test('multiple list views still share a request in the same workspace', async () => {
  const f = fixture()
  try {
    const first = f.mount()
    const second = f.mount()
    assert.equal(f.reads.length, 2)
    f.reads[0].resolve(rows('a-kb'))
    f.reads[1].resolve(rows('a-agent', 'agent'))
    await flush()
    assert.equal(first.pins.isFavorite('kb', 'a-kb'), true)
    assert.equal(second.pins.isFavorite('agent', 'a-agent'), true)
  } finally { f.dispose() }
})

test('an old read completion cannot release the newer workspace single-flight guard', async () => {
  const f = fixture()
  try {
    f.mount()
    f.auth.effectiveTenantId = 'b'
    await vue.nextTick()
    assert.equal(f.reads.length, 4)
    f.reads[0].resolve(rows('a-kb'))
    f.reads[1].resolve(rows('a-agent', 'agent'))
    await flush()
    f.mount()
    assert.equal(f.reads.length, 4, 'new views join the still-pending B requests')
    f.reads[2].resolve(rows('b-kb'))
    f.reads[3].resolve(rows('b-agent', 'agent'))
    await flush()
  } finally { f.dispose() }
})

for (const remove of [true, false]) {
  test(`a failed ${remove ? 'remove' : 'add'} still rolls back within its own workspace`, async () => {
    const f = fixture()
    try {
      const { pins } = f.mount()
      f.reads[0].resolve(remove ? rows('a-kb') : { success: true, data: [] })
      f.reads[1].resolve({ success: true, data: [] })
      await flush()
      const toggling = pins.toggleFavorite('kb', 'a-kb')
      assert.equal(pins.isFavorite('kb', 'a-kb'), !remove)
      f.writes[0].reject(new Error('request failed'))
      await toggling
      assert.equal(pins.isFavorite('kb', 'a-kb'), remove)
    } finally { f.dispose() }
  })
}
