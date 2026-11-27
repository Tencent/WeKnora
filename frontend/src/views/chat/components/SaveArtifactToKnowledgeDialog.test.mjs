import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import test from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { runInNewContext } from 'node:vm'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import * as fileTypeVerification from '../../../utils/fileTypeVerification'
import * as kbPermission from '../../../utils/kbPermission'

const require = createRequire(import.meta.url)
const filename = 'SaveArtifactToKnowledgeDialog.vue'
const source = readFileSync(new URL(filename, import.meta.url), 'utf8')
const { descriptor } = parse(source, { filename })
const script = compileScript(descriptor, { id: 'save-artifact-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const artifactBody = 'Quarterly totals'
const artifact = {
  sessionId: 'session-1',
  messageId: 'message-4',
  index: 7,
  fileName: 'report.txt',
  fileSize: new Blob([artifactBody]).size,
}
const artifactBlob = () => new Blob([artifactBody], { type: 'text/plain' })

const documentKb = (id, overrides = {}) => ({
  id,
  name: id,
  type: 'document',
  tenant_id: 1,
  creator_id: 'user-1',
  embedding_model_id: 'embedding-1',
  summary_model_id: 'summary-1',
  storage_backend_id: 'storage-1',
  ...overrides,
})
const sharedKb = (id, permission) => ({
  knowledge_base: documentKb(id, { tenant_id: 2 }),
  source_tenant_id: 2,
  permission,
})

function fixture(t, {
  sourceArtifact = artifact,
  rawKnowledgeBases = [documentKb('reports')],
  sharedKnowledgeBases = [],
  role = 'contributor',
} = {}) {
  const requests = []
  const uploads = []
  const notices = []
  let closeCount = 0
  const auth = reactive({
    user: { id: 'user-1' },
    effectiveTenantId: 1,
    hasRole: (required) => ['viewer', 'contributor', 'admin', 'owner'].indexOf(role)
      >= ['viewer', 'contributor', 'admin', 'owner'].indexOf(required),
  })
  const organization = {
    sharedKnowledgeBases,
    getKBPermission: id => sharedKnowledgeBases.find(share => share.knowledge_base.id === id)?.permission,
  }
  const messagePlugin = {
    error: value => notices.push(value),
    warning: value => notices.push(value),
  }
  const modules = {
    'vue-i18n': { useI18n: () => ({ t: key => key }) },
    'tdesign-vue-next': { MessagePlugin: messagePlugin },
    '@/api/chat': {
      downloadArtifact(...identity) {
        const request = Promise.withResolvers()
        requests.push({ identity, ...request })
        return request.promise
      },
    },
    '@/stores/auth': { useAuthStore: () => auth },
    '@/stores/chatResources': {
      useChatResourcesStore: () => ({ rawKnowledgeBases, async ensureKnowledgeBases() {} }),
    },
    '@/stores/editorResources': {
      useEditorResourcesStore: () => ({
        parserEngines: [{ Name: 'DocReader', Available: true, FileTypes: ['txt'] }],
        async ensureParserEngines() {},
      }),
    },
    '@/stores/organization': { useOrganizationStore: () => organization },
    '@/stores/uploadTasks': { useUploadTasksStore: () => ({ enqueue: input => uploads.push(input) }) },
    '@/utils': {
      kbFileTypeVerification: (file, _silent, types) => fileTypeVerification.shouldRejectKnowledgeFileType(file.name, types),
    },
    '@/utils/fileTypeVerification': fileTypeVerification,
    '@/utils/kbPermission': kbPermission,
  }
  const exports = {}
  runInNewContext(compiled, {
    exports,
    require: name => modules[name] ?? require(name),
    Blob, File, console,
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref()
  const app = renderer.createApp({
    render: () => h(component, { ref: instance, artifact: sourceArtifact, onClose: () => { closeCount++ } }),
  })
  app.mount({})
  t.after(() => app.unmount())
  return { vm: instance.value, auth, requests, uploads, notices, closeCount: () => closeCount }
}

test('the destination list contains only writable document knowledge bases', async (t) => {
  const f = fixture(t, {
    role: 'admin',
    rawKnowledgeBases: [
      documentKb('local'),
      documentKb('faq', { type: 'faq' }),
      documentKb('temporary', { is_temporary: true }),
      documentKb('agent-only', { my_permission: 'viewer' }),
    ],
    sharedKnowledgeBases: [sharedKb('shared-editor', 'editor'), sharedKb('shared-viewer', 'viewer')],
  })
  await setImmediate()
  assert.equal(f.vm.loading, false)
  assert.deepEqual(Array.from(f.vm.destinations, option => option.value).sort(), ['local', 'shared-editor'])
  f.vm.selectedId = 'shared-viewer'
  await f.vm.confirmSave()
  assert.equal(f.requests.length, 0)
})

test('confirm retrieves the selected artifact and queues its named bytes exactly once', async (t) => {
  const f = fixture(t)
  await setImmediate()
  f.vm.selectedId = 'reports'
  const first = f.vm.confirmSave()
  const repeated = f.vm.confirmSave()
  assert.equal(f.vm.saving, true)
  assert.deepEqual(f.requests.map(request => request.identity), [['session-1', 'message-4', 7]])

  f.requests[0].resolve(artifactBlob())
  await Promise.all([first, repeated])
  assert.equal(f.uploads.length, 1)
  assert.equal(f.uploads[0].kbId, 'reports')
  assert.equal(f.uploads[0].kbName, 'reports')
  assert.equal(f.uploads[0].uploads.length, 1)
  const file = f.uploads[0].uploads[0].file
  assert.ok(file instanceof File)
  assert.equal(file.name, 'report.txt')
  assert.equal(file.type, 'text/plain')
  assert.equal(await file.text(), artifactBody)
  assert.equal(f.closeCount(), 1)
})

test('a failed download leaves the dialog available for retry without queuing a file', async (t) => {
  const f = fixture(t)
  await setImmediate()
  f.vm.selectedId = 'reports'
  const failed = f.vm.confirmSave()
  f.requests[0].reject(new Error('Artifact is unavailable'))
  await failed
  assert.equal(f.vm.saving, false)
  assert.equal(f.closeCount(), 0)
  assert.equal(f.uploads.length, 0)
  assert.deepEqual(f.notices, ['agent.artifactDrawer.downloadFailed'])

  const retry = f.vm.confirmSave()
  f.requests[1].resolve(artifactBlob())
  await retry
  assert.equal(f.uploads.length, 1)
  assert.equal(f.closeCount(), 1)
})

test('changing workspace while the artifact is loading cannot queue it in the new workspace', async (t) => {
  const f = fixture(t)
  await setImmediate()
  f.vm.selectedId = 'reports'
  const pending = f.vm.confirmSave()
  f.auth.effectiveTenantId = 2
  await nextTick()
  f.requests[0].resolve(artifactBlob())
  await pending
  assert.equal(f.uploads.length, 0)
  assert.equal(f.closeCount(), 1)
})

test('unsupported artifacts are rejected before downloading or queuing any bytes', async (t) => {
  const f = fixture(t, { sourceArtifact: { ...artifact, fileName: 'report.zip' } })
  await setImmediate()
  f.vm.selectedId = 'reports'
  await f.vm.confirmSave()
  assert.equal(f.requests.length, 0)
  assert.equal(f.uploads.length, 0)
  assert.equal(f.closeCount(), 0)
})

test('upload readiness follows document knowledge base model and storage requirements', async (t) => {
  const f = fixture(t, {
    rawKnowledgeBases: [
      documentKb('no-summary', { summary_model_id: '' }),
      documentKb('no-storage', { storage_backend_id: '' }),
      documentKb('no-retrieval', {
        embedding_model_id: '',
        indexing_strategy: { vector_enabled: false, keyword_enabled: false },
        storage_backend_id: '',
        storage_provider_config: { provider: 'local' },
      }),
    ],
  })
  await setImmediate()
  f.vm.selectedId = 'no-summary'
  await f.vm.confirmSave()
  f.vm.selectedId = 'no-storage'
  await f.vm.confirmSave()
  assert.deepEqual(f.notices, ['knowledgeBase.notInitialized', 'knowledgeBase.missingStorageEngineUpload'])
  assert.equal(f.requests.length, 0)
  assert.equal(f.uploads.length, 0)

  f.vm.selectedId = 'no-retrieval'
  const ready = f.vm.confirmSave()
  f.requests[0].resolve(artifactBlob())
  await ready
  assert.equal(f.uploads[0].kbId, 'no-retrieval')
})
