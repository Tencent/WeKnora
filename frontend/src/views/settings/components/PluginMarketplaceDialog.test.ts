import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import JSZip from 'jszip'
import { createRenderer, nextTick } from 'vue'
import * as pluginUtils from '../../../utils/pluginMarketplace'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./PluginMarketplaceDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'plugin-marketplace-dialog-test' }).content
  .replace(/__expose\([^;]*\);/g, '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText

async function fixture() {
  const uploads: Array<{ file: Blob; filename?: string }> = []
  const sources: string[] = []
  const errors: string[] = []
  const added: unknown[] = []
  const exports: any = {}
  runInNewContext(compiled, {
    exports, console: { error() {} },
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { success() {}, warning() {}, error: (m: string) => errors.push(m) } }
      if (name === '@/utils/pluginMarketplace') return pluginUtils
      if (name === '@/api/plugin') return {
        addPluginMarketplaceFromFile: async (file: Blob, name?: string) => {
          uploads.push({ file, filename: name })
          return { data: { id: 'mp', name: 'acme-plugins' } }
        },
        addPluginMarketplaceFromSource: async (source: string) => {
          sources.push(source)
          return { data: { id: 'mp', name: 'acme-plugins' } }
        },
      }
      return { default: {} }
    },
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const app = renderer.createApp(component, { visible: true, onAdded: (mp: unknown) => added.push(mp) })
  const vm: any = app.mount({})
  await nextTick()
  return { vm, uploads, sources, errors, added, close: () => app.unmount() }
}

function pickEvent(files: Array<{ name: string; webkitRelativePath?: string; content?: string }>) {
  const input = {
    value: 'C:\\fakepath',
    files: files.map((f) => Object.assign(new Blob([f.content ?? '']), { name: f.name, webkitRelativePath: f.webkitRelativePath ?? '' })),
  }
  return { target: input }
}

test('a folder without marketplace.json is rejected before upload', async () => {
  const f = await fixture()
  try {
    f.vm.onFolderPicked(pickEvent([{ name: 'README.md', webkitRelativePath: 'acme/README.md' }]))
    assert.equal(f.errors.length, 1)
    assert.equal(f.vm.canSubmit, false)
  } finally { f.close() }
})

test('a picked folder is zipped in the browser and uploaded as <folder>.zip', async () => {
  const f = await fixture()
  try {
    f.vm.onFolderPicked(pickEvent([
      { name: 'marketplace.json', webkitRelativePath: 'acme/.weknora-plugin/marketplace.json', content: '{"name":"acme-plugins"}' },
      { name: '.mcp.json', webkitRelativePath: 'acme/plugins/calcom/.mcp.json', content: '{}' },
    ]))
    assert.equal(f.vm.folderName, 'acme')
    assert.equal(f.vm.canSubmit, true)
    await f.vm.submit()
    assert.equal(f.uploads.length, 1)
    assert.equal(f.uploads[0]!.filename, 'acme.zip')
    const zip = await JSZip.loadAsync(await f.uploads[0]!.file.arrayBuffer())
    assert.ok(zip.file('acme/.weknora-plugin/marketplace.json'))
    assert.ok(zip.file('acme/plugins/calcom/.mcp.json'))
    assert.equal(f.added.length, 1)
  } finally { f.close() }
})

test('archives are uploaded as picked and other formats are refused', async () => {
  const f = await fixture()
  try {
    f.vm.mode = 'archive'
    f.vm.onArchivePicked(pickEvent([{ name: 'acme.rar' }]))
    assert.equal(f.vm.archive, null)
    assert.equal(f.errors.length, 1)

    f.vm.onArchivePicked(pickEvent([{ name: 'acme-plugins.tar.gz' }]))
    assert.equal(f.vm.canSubmit, true)
    await f.vm.submit()
    assert.equal(f.uploads.length, 1)
    assert.equal(f.uploads[0]!.filename, undefined, 'the original file name is kept')
  } finally { f.close() }
})

test('git sources are validated and trimmed', async () => {
  const f = await fixture()
  try {
    f.vm.mode = 'git'
    f.vm.source = 'not a source'
    assert.equal(f.vm.canSubmit, false)
    await f.vm.submit()
    assert.equal(f.sources.length, 0)
    assert.equal(f.errors.length, 1)

    f.vm.source = '  acme/plugins  '
    assert.equal(f.vm.canSubmit, true)
    await f.vm.submit()
    assert.deepEqual(f.sources, ['acme/plugins'])
  } finally { f.close() }
})
