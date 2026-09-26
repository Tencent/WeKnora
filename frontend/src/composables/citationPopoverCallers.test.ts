import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'
import * as vue from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'

// The popover watches its cache-scope getter, so Vue evaluates it during
// setup; callers must not read props that <script setup> has not declared yet.
const srcDir = fileURLToPath(new URL('..', import.meta.url))
const composables = ['useCitationPopover', 'useChatCitationPopover', 'useEmbedCitationPopover']
const callPattern = new RegExp(`\\b(${composables.join('|')})\\(`)

const transpile = (source: string) => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function vueFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return vueFiles(path)
    return entry.name.endsWith('.vue') ? [path] : []
  })
}

// Every import outside Vue and the popover chain is replaced by this inert,
// infinitely callable value so only setup ordering is exercised.
const stub: any = new Proxy(function () {}, {
  get(_, key) {
    if (key === Symbol.toPrimitive) return () => ''
    if (key === Symbol.iterator) return function* () {}
    if (key === 'then') return undefined
    if (key === '__esModule') return true
    return stub
  },
  apply: () => stub,
  construct: () => stub,
})

function loadComposable(name: string) {
  const modules: Record<string, any> = {}
  const load = (file: string): any => {
    if (modules[file]) return modules[file]
    const exports = modules[file] = {}
    runInNewContext(transpile(readFileSync(join(srcDir, 'composables', `${file}.ts`), 'utf8')), {
      exports,
      require: (path: string) => {
        if (path === 'vue') return vue
        if (path.startsWith('./')) return load(path.slice(2))
        return stub
      },
    })
    return exports
  }
  return load(name)
}

function runSetup(file: string, props: Record<string, unknown>) {
  const { descriptor } = parse(readFileSync(file, 'utf8'), { filename: file })
  const script = compileScript(descriptor, { id: 'citation-popover-caller', inlineTemplate: false })
  const exports: Record<string, any> = {}
  runInNewContext(transpile(script.content), {
    exports, console,
    require: (path: string) => {
      if (path === 'vue') return vue
      const name = composables.find(c => path.endsWith(`/composables/${c}`))
      return name ? loadComposable(name) : stub
    },
  })
  const scope = vue.effectScope()
  const warn = console.warn
  console.warn = () => {} // lifecycle hooks warn without a component instance
  try {
    scope.run(() => exports.default.setup(vue.shallowReactive(props), {
      expose() {}, emit() {}, attrs: {}, slots: {},
    }))
  } finally {
    console.warn = warn
    scope.stop()
  }
}

const callers = vueFiles(srcDir).filter(file => callPattern.test(readFileSync(file, 'utf8')))

test('citation popover callers are discovered', () => {
  assert.ok(callers.length > 0)
})

for (const file of callers) {
  test(`${relative(srcDir, file)}: citation popover options can be read during setup`, () => {
    assert.doesNotThrow(() => runSetup(file, {
      content: '', session: {}, sessionId: 'session-1', userQuery: '',
      embedChannelId: '', embedToken: '',
    }))
  })
}
