import assert from 'node:assert/strict'
import { Buffer } from 'node:buffer'
import { readFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import { build } from 'esbuild'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

let component: any
async function setup(overrides: Record<string, (...args: any[]) => any> = {}, admin = true) {
  const state = (globalThis as any).__modelUsageTest = {
    watches: [] as any[], unmount: [] as (() => void)[], messages: [] as string[],
    api: { listModelUsage: async () => ({ items: [] }), listModelPrices: async () => [], putModelPrice: async () => ({}), ...overrides },
  }
  if (!component) {
    const filename = fileURLToPath(new URL('./ModelUsageDrawer.vue', import.meta.url))
    const { descriptor } = parse(await readFile(filename, 'utf8'), { filename })
    const compiled = compileScript(descriptor, { id: 'model-usage-test' })
    const modules: Record<string, string> = {
      vue: `export const defineComponent = value => value; export const h = () => ({});
        export const ref = value => ({value});
        export const computed = getter => ({get value() {return typeof getter === 'function' ? getter() : getter.get()}});
        export const watch = (source, callback, options) => globalThis.__modelUsageTest.watches.push({source, callback, options});
        export const onBeforeUnmount = callback => globalThis.__modelUsageTest.unmount.push(callback);`,
      'vue-i18n': `export const useI18n = () => ({t: key => key});`,
      'tdesign-vue-next': `export const MessagePlugin = {error: value => globalThis.__modelUsageTest.messages.push(value), success: value => globalThis.__modelUsageTest.messages.push(value)};`,
      '@/components/settings/SettingDrawer.vue': 'export default {};',
      '@lucide/vue': ['ChartNoAxesCombined', 'Database', 'Layers3', 'LoaderCircle', 'Plus', 'RefreshCw', 'X'].map(name => `export const ${name} = {};`).join('\n'),
      '@/api/model': ['listModelUsage', 'listModelPrices', 'putModelPrice'].map(name => `export const ${name} = (...args) => globalThis.__modelUsageTest.api.${name}(...args);`).join('\n'),
    }
    const result = await build({
      bundle: true, format: 'esm', platform: 'node', write: false, logLevel: 'silent', target: 'node22',
      stdin: { contents: compiled.content, loader: 'ts', resolveDir: dirname(filename), sourcefile: filename },
      plugins: [{ name: 'model-usage-mocks', setup(builder) {
        builder.onResolve({filter: /.*/}, args => args.path in modules ? {path: args.path, namespace: 'mock'} : undefined)
        builder.onLoad({filter: /.*/, namespace: 'mock'}, args => ({contents: modules[args.path], loader: 'js'}))
      }}],
    })
    component = (await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`)).default
  }
  const props = {visible: true, canEditPricing: admin, models: [{id: 'a', name: 'Model A'}, {id: 'b', name: 'Model B'}]}
  const vm = component.setup(props, {expose() {}, emit() {}})
  return {vm, props, state}
}

test('model and local custom dates are sent to the usage API; invalid ranges send no request', async () => {
  const calls: any[] = []
  const {vm} = await setup({listModelUsage: async query => {calls.push(query); return {items: []}}})
  vm.usageModelId.value = 'b'
  vm.windowDays.value = 0
  vm.rangeFrom.value = '2026-09-01T08:30'
  vm.rangeTo.value = '2026-09-02T08:30'
  await vm.loadUsage()
  assert.deepEqual(calls, [{modelIds: ['b'], from: new Date('2026-09-01T08:30').toISOString(), to: new Date('2026-09-02T08:30').toISOString()}])
  vm.rangeTo.value = vm.rangeFrom.value
  await vm.loadUsage()
  assert.equal(calls.length, 1)
  assert.match(vm.usageError.value, /invalidRange/)
  assert.equal(vm.loading.value, false)
})

test('late usage responses cannot replace newer rows or dismiss their loading state', async () => {
  const first = deferred<any>(), second = deferred<any>()
  let calls = 0
  const {vm} = await setup({listModelUsage: () => ++calls === 1 ? first.promise : second.promise})
  const a = vm.loadUsage(), b = vm.loadUsage()
  first.resolve({items: [{model_id: 'a'}]})
  await a
  assert.deepEqual(vm.rows.value, [])
  assert.equal(vm.loading.value, true)
  second.resolve({items: [{model_id: 'b'}]})
  await b
  assert.equal(vm.rows.value[0].model_id, 'b')
})

test('stale usage failures are silent and current failures clear previous rows', async () => {
  const first = deferred<any>()
  let calls = 0
  const {vm, state} = await setup({listModelUsage: () => ++calls === 1 ? first.promise : Promise.resolve({items: [{model_id: 'b'}]})})
  const a = vm.loadUsage()
  await vm.loadUsage()
  first.reject(new Error('stale'))
  await a
  assert.equal(vm.usageError.value, '')
  assert.equal(vm.rows.value[0].model_id, 'b')
  state.api.listModelUsage = async () => {throw new Error('current')}
  await vm.loadUsage()
  assert.deepEqual(vm.rows.value, [])
  assert.equal(vm.usageError.value, 'current')
  assert.deepEqual(state.messages, [])
})

test('price history follows the newest model and clears when the model is empty', async () => {
  const first = deferred<any>()
  const {vm} = await setup({listModelPrices: id => id === 'a' ? first.promise : Promise.resolve([{id: 'price-b'}])})
  vm.priceModelId.value = 'a'
  const a = vm.loadPrices()
  vm.priceModelId.value = 'b'
  await vm.loadPrices()
  first.resolve([{id: 'price-a'}])
  await a
  assert.equal(vm.prices.value[0].id, 'price-b')
  vm.priceModelId.value = ''
  await vm.loadPrices()
  assert.deepEqual(vm.prices.value, [])
})

test('close and unmount invalidate in-flight usage and price history requests', async () => {
  for (const action of ['close', 'unmount']) {
    const usage = deferred<any>(), prices = deferred<any>()
    const {vm, props, state} = await setup({listModelUsage: () => usage.promise, listModelPrices: () => prices.promise})
    vm.priceModelId.value = 'a'
    const a = vm.loadUsage(), b = vm.loadPrices()
    if (action === 'close') {props.visible = false; state.watches[0].callback(false)}
    else state.unmount.forEach(callback => callback())
    usage.resolve({items: [{model_id: 'a'}]}); prices.resolve([{id: 'price-a'}])
    await Promise.all([a, b])
    assert.deepEqual(vm.rows.value, [])
    assert.deepEqual(vm.prices.value, [])
    assert.equal(vm.loading.value, false)
  }
})

test('zero prices are valid; missing, negative, nonfinite, unsafe amounts and reversed dates are rejected', async () => {
  const {vm} = await setup()
  vm.priceModelId.value = 'a'
  vm.inputPrice.value = 0; vm.outputPrice.value = 0
  assert.equal(vm.canSavePrice.value, true)
  for (const invalid of ['', -1, Number.NaN, Number.POSITIVE_INFINITY, 1e15]) {
    vm.inputPrice.value = invalid
    assert.equal(vm.canSavePrice.value, false)
  }
  vm.inputPrice.value = 0
  vm.validTo.value = vm.validFrom.value
  assert.equal(vm.canSavePrice.value, false)
  vm.validTo.value = ''; vm.currency.value = '123'
  assert.equal(vm.canSavePrice.value, false)
})

test('local effective time round-trips without a timezone shift and duplicate saves are blocked', async () => {
  const saved = deferred<any>(), calls: any[] = []
  const {vm} = await setup({putModelPrice: (...args) => {calls.push(args); return saved.promise}})
  assert.ok(Math.abs(new Date(vm.validFrom.value).getTime() - Date.now()) < 60000)
  vm.priceModelId.value = 'b'; vm.inputPrice.value = 1.234567; vm.outputPrice.value = 0; vm.currency.value = 'usd'
  const saving = vm.savePrice()
  await vm.savePrice()
  assert.equal(calls.length, 1)
  assert.equal(calls[0][0], 'b')
  assert.equal(calls[0][1].input_microunits_per_million, 1234567)
  assert.equal(calls[0][1].currency, 'USD')
  assert.equal(calls[0][1].valid_from, new Date(vm.validFrom.value).toISOString())
  saved.resolve({})
  await saving
  assert.equal(vm.savingPrice.value, false)
})

test('read-only users do not request or mutate model pricing', async () => {
  let calls = 0
  const {vm, state} = await setup({listModelPrices: async () => {calls++; return []}, putModelPrice: async () => {calls++}}, false)
  state.watches[0].callback(true)
  vm.priceModelId.value = 'a'; vm.inputPrice.value = 0; vm.outputPrice.value = 0
  await vm.loadPrices(); await vm.savePrice()
  assert.equal(calls, 0)
  assert.equal(state.watches[0].options.immediate, true)
})

test('cache prices preserve unknown, free and priced fields without inventing a zero', async () => {
  const calls: any[] = []
  const {vm} = await setup({putModelPrice: async (...args) => {calls.push(args); return {}}})
  vm.priceModelId.value = 'a'; vm.inputPrice.value = 1; vm.outputPrice.value = 2
  await vm.savePrice()
  assert.equal(calls[0][1].cache_pricing, undefined)
  vm.cacheReadPrice.value = 0
  vm.cacheWrite5mPrice.value = 1.25
  await vm.savePrice()
  assert.deepEqual(calls[1][1].cache_pricing, {
    version: 1, read_microunits_per_million: 0,
    write_5m_microunits_per_million: 1250000,
    write_1h_microunits_per_million: undefined,
  })
  for (const invalid of [-1, NaN, Infinity, 1e15]) {
    vm.cacheWrite1hPrice.value = invalid
    assert.equal(vm.canSavePrice.value, false)
    await vm.savePrice()
  }
  assert.equal(calls.length, 2)
})

test('historical usage remains readable after its model is absent from the active model list', async () => {
  const {vm} = await setup({listModelUsage: async () => ({items: [{model_id: 'deleted-model', call_count: 2}]})})
  await vm.loadUsage()
  assert.equal(vm.rows.value[0].model_id, 'deleted-model')
  // The raw identifier stays readable and is marked as lacking a known name.
  assert.equal(vm.modelName('deleted-model'), 'deleted-model · modelSettings.observability.nameUnavailable')
})
