import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'

const source = readFileSync(new URL('./BackgroundWorkChip.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const compiled = compileScript(descriptor, { id: 'background-work-chip' })
const exports: any = {}
runInNewContext(ts.transpileModule(compiled.content, {
  compilerOptions: { module: ts.ModuleKind.CommonJS },
}).outputText, {
  exports, Date, Set, setInterval, clearInterval,
  require: () => ({ ...vue, onUnmounted() {} }),
})

for (const rejected of [false, true]) {
  test(`cancel button re-enables when request settles (${rejected ? 'failure' : 'success'})`, async () => {
    let finish!: () => void
    let fail!: (reason: Error) => void
    let calls = 0
    const props = vue.reactive({
      items: [{ work_id: 'sj_0123', kind: 'shell', state: 'running', title: 'build' }],
      cancelWork: () => {
        calls++
        return new Promise<void>((resolve, reject) => { finish = resolve; fail = reject })
      },
    })
    const scope = vue.effectScope()
    const state = scope.run(() => exports.default.setup(props, { expose() {}, emit() {} }))
    try {
      const pending = state.onCancel('sj_0123')
      assert.equal(state.cancelling.value.has('sj_0123'), true)
      await state.onCancel('sj_0123')
      assert.equal(calls, 1, 'duplicate clicks are suppressed while pending')
      if (rejected) {
        fail(new Error('HTTP 503'))
        await assert.rejects(pending, /503/)
      } else { finish(); await pending }
      assert.equal(props.items[0].state, 'running', 'poll may still show a running job')
      assert.equal(state.cancelling.value.has('sj_0123'), false)
      const retry = state.onCancel('sj_0123')
      assert.equal(calls, 2)
      finish()
      await retry
    } finally { scope.stop() }
  })
}
