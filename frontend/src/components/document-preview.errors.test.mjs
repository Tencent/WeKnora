import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import vm from 'node:vm'

test('renderer mount failure becomes a visible preview error instead of a blank page', () => {
  const source = readFileSync(new URL('./document-preview.vue', import.meta.url), 'utf8')
  const hook = source.slice(source.indexOf('onErrorCaptured((err)'), source.indexOf("const previewType ="))
  assert.ok(hook.startsWith('onErrorCaptured('))
  const error = { value: '' }, loading = { value: true }
  let handler
  vm.runInNewContext(hook, {
    onErrorCaptured: callback => { handler = callback }, error, loading,
    t: key => key, console: { error() {} },
  })
  assert.equal(handler(new TypeError('Cannot read properties of null')), false)
  assert.equal(error.value, 'preview.loadFailed')
  assert.equal(loading.value, false)
})
