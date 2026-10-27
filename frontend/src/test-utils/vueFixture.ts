import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import type { TestContext } from 'node:test'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import * as vue from 'vue'
import ts from 'typescript'

export function change(operation: string, ids: Record<string, string[]> = {}) {
  return { operation, area: operation.split('.')[0], ids }
}

export async function flush() {
  await new Promise<void>(resolve => setImmediate(resolve))
  await vue.nextTick()
}

export function fixture(t: TestContext) {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const window = Object.assign(new EventTarget(), { innerHeight: 900, innerWidth: 1200, setTimeout, clearTimeout })
  const storage = { getItem: () => null, setItem() {}, removeItem() {} }
  const globals = {
    window, CustomEvent, setTimeout, clearTimeout, setInterval, clearInterval,
    localStorage: storage, sessionStorage: storage,
    requestAnimationFrame: (callback: () => void) => setTimeout(callback, 16),
    document: { documentElement: { clientHeight: 900 }, querySelector: () => null },
  }
  function evaluate(source: string, dependencies: Record<string, unknown>, fallback?: (name: string) => unknown) {
    const exports: any = {}
    runInNewContext(ts.transpileModule(source, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText, {
      exports, console, ...globals,
      require(name: string) {
        if (name in dependencies) return dependencies[name]
        if (fallback) return fallback(name)
        throw new Error(`Unexpected dependency: ${name}`)
      },
    })
    return exports
  }
  const dataChange = evaluate(readFileSync(new URL('../utils/assistantDataChange.ts', import.meta.url), 'utf8'), { vue })
  const renderer = vue.createRenderer<object, object>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  function mount(component: any, props = {}) {
    component.render = () => null
    const app = renderer.createApp(component, props)
    const vm: any = app.mount({})
    let mounted = true
    const close = () => { if (mounted) { mounted = false; app.unmount() } }
    t.after(close)
    return { vm, close }
  }
  function component(file: URL, dependencies: Record<string, unknown>, props = {}) {
    const { descriptor } = parse(readFileSync(file, 'utf8'), { filename: file.pathname })
    const script = compileScript(descriptor, { id: 'assistant-refresh-test' }).content
      .replace('__expose();', '')
      .replace('return __returned__', '__expose(__returned__); return __returned__')
    const require = createRequire(file)
    const compiled = evaluate(script, {
      vue, '@/utils/assistantDataChange': dataChange, ...dependencies,
    }, name => {
      if (name.endsWith('.vue')) return { default: {} }
      if (name.startsWith('.')) return require(name)
      assert.fail(`Unexpected component dependency: ${name}`)
    })
    return mount(compiled.default, props)
  }
  return { window, evaluate, mount, component, dataChange, emit: dataChange.emitAssistantDataChange }
}
