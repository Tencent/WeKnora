import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, compileTemplate, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import {
  DINGTALK_DESCRIBE_PARENT_PREFIX,
  DINGTALK_MANUAL_REFERENCE,
  dingtalkManualKind,
  dingtalkManualReference,
  dingtalkManualReferenceId,
  dingtalkResourceHint,
  extractDingTalkId,
} from './dingtalk/dingtalkResources'
import { resourceIconName, resourceTypeLabel, shouldShowResourceType } from './resourcePresentation'

// The editor dialog's side of the DingTalk manual entry: mounting the selector,
// owning the selection the wizard submits, and keeping the tree in step with it.
//
// The selector is the real component — its own behaviour is covered by
// dingtalk/DingTalkManualSelection.test.ts — so what is asserted here is the
// integration: the resources step mounts the selector for DingTalk and for no
// other connector, the selection it writes is the one the final save sends, the
// tree the dialog owns collapses behind the expander while a manual reference
// exists (and is listed only when that expander is opened), and the rows the
// selector describes are merged into that tree.
const require = createRequire(import.meta.url)

const TS_OPTIONS = {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
} as const

// The dialog's template is compiled too: the selector is reached through it, and
// a render function returning nothing would never mount it.
function compileSfc(url: URL, id: string, exposeAll: boolean) {
  const filename = fileURLToPath(url)
  const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
  const setup = compileScript(descriptor, { id })
  let script = setup.content.replace('__expose();', '')
  if (exposeAll) script = script.replace('return __returned__', '__expose(__returned__); return __returned__')
  const template = compileTemplate({
    source: descriptor.template!.content,
    filename,
    id,
    compilerOptions: { mode: 'module', bindingMetadata: setup.bindings },
  }).code
  return {
    script: ts.transpileModule(script, TS_OPTIONS).outputText,
    template: ts.transpileModule(template, TS_OPTIONS).outputText,
  }
}

// SettingDrawer only frames the steps; a stub that renders its default slot is
// what makes the step's own content reachable without a DOM.
const SettingDrawerStub = {
  name: 'SettingDrawer',
  props: ['visible', 'title', 'description'],
  setup(_props: any, { slots }: any) {
    return () => (slots.default ? slots.default() : null)
  },
}

// The reference grammar and the row presentation are shared with the selector,
// and the dialog's tree renders through them: both components are given the real
// modules, so a reference written by one is one the other accepts.
const SHARED_MODULES: Record<string, unknown> = {
  './dingtalk/dingtalkResources': {
    DINGTALK_DESCRIBE_PARENT_PREFIX,
    DINGTALK_MANUAL_REFERENCE,
    dingtalkManualKind,
    dingtalkManualReference,
    dingtalkManualReferenceId,
    dingtalkResourceHint,
    extractDingTalkId,
  },
  // The selector sits next to the grammar module, the dialog one directory up.
  './dingtalkResources': {
    DINGTALK_DESCRIBE_PARENT_PREFIX,
    DINGTALK_MANUAL_REFERENCE,
    dingtalkManualKind,
    dingtalkManualReference,
    dingtalkManualReferenceId,
    dingtalkResourceHint,
    extractDingTalkId,
  },
  './resourcePresentation': { resourceIconName, resourceTypeLabel, shouldShowResourceType },
  '../resourcePresentation': { resourceIconName, resourceTypeLabel, shouldShowResourceType },
}

const child = compileSfc(
  new URL('./dingtalk/DingTalkManualSelection.vue', import.meta.url), 'dingtalk-selector-test', false)
const parent = compileSfc(
  new URL('./DataSourceEditorDialog.vue', import.meta.url), 'datasource-editor-dingtalk-test', true)

const i18nCalls: Array<{ key: string; params?: any }> = []

function run(code: string, modules: Record<string, unknown>) {
  const exports: any = {}
  runInNewContext(code, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') {
        return {
          useI18n: () => ({
            t: (key: string, params?: any) => {
              i18nCalls.push({ key, params })
              return key
            },
          }),
        }
      }
      if (name === 'tdesign-vue-next') return { MessagePlugin: { warning() {}, success() {}, error() {} } }
      if (name in modules) return modules[name]
      return { default: {} }
    },
    URL, console,
  })
  return exports
}

// A renderer needs somewhere to put nodes: Vue's keyed diff asks a node for its
// parent and its next sibling, so a renderer that always answers "nowhere" makes
// the dialog's step content patch forever. This is the smallest node structure
// that answers both truthfully, without a DOM.
interface RenderNode { tag: string; children: RenderNode[]; parent: RenderNode | null; text: string }
function makeNode(tag: string, text = ''): RenderNode {
  return { tag, children: [], parent: null, text }
}

function createTestRenderer() {
  return createRenderer<RenderNode, RenderNode>({
    createElement: (tag: string) => makeNode(tag),
    createText: (text: string) => makeNode('#text', text),
    createComment: (text: string) => makeNode('#comment', text),
    setText(node: RenderNode, text: string) { node.text = text },
    setElementText(node: RenderNode, text: string) { node.text = text },
    insert(node: RenderNode, parent: RenderNode, anchor?: RenderNode | null) {
      node.parent = parent
      const at = anchor ? parent.children.indexOf(anchor) : -1
      if (at >= 0) parent.children.splice(at, 0, node)
      else parent.children.push(node)
    },
    remove(node: RenderNode) {
      const parent = node.parent
      if (parent) {
        const at = parent.children.indexOf(node)
        if (at >= 0) parent.children.splice(at, 1)
      }
      node.parent = null
    },
    parentNode: (node: RenderNode) => node.parent,
    nextSibling: (node: RenderNode) => {
      const parent = node.parent
      if (!parent) return null
      const at = parent.children.indexOf(node)
      return at >= 0 && at + 1 < parent.children.length ? parent.children[at + 1] : null
    },
    patchProp() {},
    querySelector: () => null,
    setScopeId() {},
  })
}

async function fixture({
  resourceIds = [] as string[],
  resources = [] as any[],
  described = {} as Record<string, any>,
  create = false,
  type = 'dingtalk',
} = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  // Resource listings are recorded apart from the data source writes: the tests
  // assert exactly which listings a step makes, and which it deliberately does
  // not.
  const resourceCalls: string[] = []
  const api = {
    async createDataSource(data: any) {
      calls.push({ method: 'createDataSource', args: [JSON.parse(JSON.stringify(data))] })
      return { id: 'temp-one' }
    },
    async updateDataSource(id: string, data: any) {
      calls.push({ method: 'updateDataSource', args: [id, JSON.parse(JSON.stringify(data))] })
    },
    async listResources(_id: string, parentId?: string) {
      resourceCalls.push(parentId ?? '')
      // A describe request asks about one reference and is answered with that
      // reference's own row; an unknown reference yields nothing.
      if (parentId && parentId.startsWith('dingtalk:v1?describe=')) {
        const reference = parentId.slice('dingtalk:v1?describe='.length)
        const row = described[reference]
        return row ? [{ external_id: reference, ...row }] : []
      }
      return resources
    },
    async resolveResourceAncestors() { return { data: { ancestors: [] } } },
    async deleteDataSource() {},
    async triggerSync() {},
    async validateConnection() {},
    async validateCredentials() {},
    async putDataSourceCredentials() {},
    async deleteDataSourceCredentials() {},
  }
  const props = reactive({
    visible: false, kbId: 'kb-one',
    dataSource: create ? null : {
      id: 'source-one', name: 'DingTalk', type,
      credentials: { credentials: { configured: true } },
      config: { resource_ids: resourceIds, settings: {} },
      sync_schedule: '0 0 */6 * * *', sync_mode: 'incremental',
      conflict_strategy: 'overwrite', sync_deletions: true,
    },
  })
  const childModules = { ...SHARED_MODULES, '@/api/datasource': api }
  const selector = run(child.script, childModules).default
  selector.render = run(child.template, childModules).render
  const component = run(parent.script, {
    ...SHARED_MODULES,
    // A default import is wrapped by the compiler's __importDefault helper, so a
    // stubbed module has to say it is one.
    './dingtalk/DingTalkManualSelection.vue': { __esModule: true, default: selector },
    '@/components/settings/SettingDrawer.vue': { __esModule: true, default: SettingDrawerStub },
    // The icon module pulls in asset imports the test runner cannot load; the
    // icons are not what these tests are about.
    './datasourceIcons': { getDatasourceIconUrl: () => '' },
    '@/api/datasource': api,
  }).default
  component.render = run(parent.template, {}).render
  const renderer = createTestRenderer()
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  // Unresolved t-* components are expected: they are elements here, and these
  // tests are about the dialog's own state, not about rendering them.
  app.config.warnHandler = () => {}
  app.mount(makeNode('root'))
  props.visible = true
  await nextTick()
  const vm = instance.value
  if (create) {
    // What the type step leaves behind: the connector the user picked. These
    // tests start on the resources step, so the type is set directly rather than
    // walked through the picker.
    vm.form.type = type
    await nextTick()
  }
  async function enterResourcesStep() {
    // The wizard's own way in: fill the credentials step the way a user would,
    // then let nextStep advance — which is what triggers the resources listing.
    for (const field of vm.displayedCredentialFields) {
      vm.form.config.credentials[field.key] = 'test-value'
    }
    vm.step = 1
    await vm.nextStep()
    await settle()
  }
  return {
    vm, props, calls, resourceCalls, enterResourcesStep,
    selector: () => vm.dingtalkSelection,
    close: () => app.unmount(),
  }
}

// Every promise the fixture's API creates resolves on a microtask, so a handful
// of ticks settles the calls the dialog starts without awaiting them (the step's
// tree listing is fire-and-forget from the user's point of view).
async function settle() {
  for (let i = 0; i < 8; i++) await Promise.resolve()
}

const isDescribeCall = (id: string) => id.startsWith('dingtalk:v1?describe=')
const treeCalls = (f: any) => f.resourceCalls.filter((id: string) => !isDescribeCall(id))
const describeCalls = (f: any) => f.resourceCalls.filter((id: string) => isDescribeCall(id))
const visibleIds = (vm: any) => [...vm.visibleTree].map((row: any) => row.resource.external_id)

// add() drives the manual entry the way the UI does, through the selector the
// dialog mounted.
async function add(f: any, input: string, kind: 'node' | 'base' = 'node') {
  const selector = f.selector()
  assert.ok(selector, 'the DingTalk selector is mounted')
  selector.manualId = input
  selector.manualKind = kind
  await nextTick()
  return selector.applyManualEntry()
}

// The knowledge-base tree is a picker, not a preview: while manual references
// exist it is collapsed behind an explicit expander, so the team's knowledge
// bases are not shown next to one pasted 多维表.
const TEAM_RESOURCES = [
  { external_id: 'ws-1', name: 'Team knowledge base', type: 'workspace', has_children: false },
  { external_id: 'ws-2', name: 'Another team knowledge base', type: 'workspace', has_children: false },
]

test('the resources step mounts the selector for DingTalk and for no other connector', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    assert.equal(f.vm.step, 2)
    assert.ok(f.selector(), 'DingTalk mounts the manual selector')

    // The step exists for every connector, but the selector is DingTalk's: no
    // other type gets the entry block.
    f.vm.form.type = 'notion'
    await nextTick()
    assert.equal(f.selector(), null)
  } finally { f.close() }
})

// A typed id the user forgot to add would otherwise be dropped silently on the
// way to the next step; an unusable one has to keep the wizard where it is.
test('the wizard applies a typed id when it advances, and stops on an unusable one', async () => {
  const f = await fixture({ create: true, resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    assert.equal(f.vm.step, 2)

    await add(f, 'docId456')
    const selector = f.selector()
    selector.manualId = 'docId789'
    await nextTick()
    // Committing is the dialog's call: it asks the selector before advancing.
    assert.equal(selector.hasPendingEntry, true)
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
    assert.deepEqual([...f.vm.selectedResourceIds], [
      'dingtalk:v1?node=docId456',
      'dingtalk:v1?node=docId789',
    ])
    assert.deepEqual([...f.vm.form.config.resource_ids], [
      'dingtalk:v1?node=docId456',
      'dingtalk:v1?node=docId789',
    ])

    // Back on the resources step the selector mounts again, pre-filled from the
    // saved selection. An unusable entry keeps the step where it is, and the
    // selector says why.
    f.vm.step = 2
    await nextTick()
    const refilled = f.selector()
    refilled.manualId = 'https://example.com/not-a-dingtalk-link'
    await nextTick()
    assert.equal(refilled.hasPendingEntry, true)
    await f.vm.nextStep()
    assert.equal(f.vm.step, 2)
    assert.equal(refilled.manualError, 'datasource.dingtalk.manualIdRequired')
  } finally { f.close() }
})

// The selection belongs to the dialog: the wizard submits it, and the tree adds
// to it. One combined array is what the save sends.
test('tree picks and manual references are saved as one resource_ids array', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    f.vm.toggleResource('ws-1')
    await add(f, 'baseId123', 'base')
    await settle()

    assert.deepEqual([...f.vm.selectedResourceIds], ['ws-1', 'dingtalk:v1?base=baseId123'])
    await f.vm.handleSubmit()
    const submitted = f.calls.at(-1)!
    assert.equal(submitted.method, 'updateDataSource')
    assert.deepEqual(submitted.args[1].config.resource_ids, ['ws-1', 'dingtalk:v1?base=baseId123'])
  } finally { f.close() }
})

test('a manual reference collapses the knowledge-base tree, which the expander still opens', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    // No manual reference yet: the tree is exactly the pre-existing picker.
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2'])

    await add(f, 'baseId123', 'base')
    await settle()
    // The manual reference exists, so the tree starts collapsed ...
    assert.equal(f.vm.hasDingTalkManualSelection, true)
    assert.equal(f.vm.dingtalkTreeExpanded, false)
    assert.equal(f.vm.showDingTalkResourceTree, false)

    // ... and the explicit expander brings back the same resources.
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2'])

    // Collapsing again hides it; removing the manual reference restores the
    // tree-only presentation without any expander.
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, false)
    f.selector().removeManualReference('dingtalk:v1?base=baseId123')
    await settle()
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
  } finally { f.close() }
})

test('a refilled manual selection collapses the tree again by default', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    await add(f, 'firstBase', 'base')
    await settle()
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)

    // Removing the last manual reference brings the plain tree back (the
    // expander disappears with it) ...
    f.selector().removeManualReference('dingtalk:v1?base=firstBase')
    await settle()
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)

    // ... and a new manual reference collapses the tree again: that is the
    // default whenever a manual reference is selected.
    await add(f, 'secondBase', 'base')
    await settle()
    assert.equal(f.vm.hasDingTalkManualSelection, true)
    assert.equal(f.vm.dingtalkTreeExpanded, false)
    assert.equal(f.vm.showDingTalkResourceTree, false)
  } finally { f.close() }
})

// The collapsed tree is not fetched at all: a data source whose only selection
// is a pasted id has no tree to fill, so entering the step must not list the
// team knowledge bases. The listing happens when the expander is first opened,
// and only then.
test('a manual selection defers the tree listing until the expander is opened', async () => {
  const f = await fixture({
    resourceIds: ['dingtalk:v1?base=savedBaseId'],
    resources: TEAM_RESOURCES,
    described: { 'dingtalk:v1?base=savedBaseId': { name: 'Project tracker.able', type: 'base' } },
  })
  try {
    await f.enterResourcesStep()
    // Collapsed: the preview is the whole presentation, so no listing ran.
    assert.equal(f.vm.step, 2)
    assert.equal(f.vm.showDingTalkResourceTree, false)
    assert.deepEqual(treeCalls(f), [])
    assert.equal(f.vm.dingtalkTreeLoaded, false)

    // Opening the expander fetches the tree exactly once. The saved manual
    // reference is named by its described row, which joins the tree as an extra
    // root.
    await f.vm.toggleDingTalkResourceTree()
    await settle()
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(treeCalls(f), [''])
    assert.equal(f.vm.dingtalkTreeLoaded, true)
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2', 'dingtalk:v1?base=savedBaseId'])
    assert.equal(f.vm.visibleTree.at(-1).resource.name, 'Project tracker.able')

    // Collapsing and opening again must not fetch a second time.
    await f.vm.toggleDingTalkResourceTree()
    await f.vm.toggleDingTalkResourceTree()
    await settle()
    assert.deepEqual(treeCalls(f), [''])
  } finally { f.close() }
})

// A user who never pastes an id keeps the exact pre-existing step: the tree is
// open on entry and listed once, at the same time as before.
test('a tree-only DingTalk user still gets the tree listed on entry', async () => {
  const f = await fixture({ resourceIds: ['ws-1'], resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(treeCalls(f), [''])
    assert.deepEqual(describeCalls(f), [])
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2'])
  } finally { f.close() }
})

// Removing the last manual reference restores the tree-only presentation, and
// with it the listing that was skipped while the preview replaced it.
test('removing the last manual reference lists the tree it restored', async () => {
  const f = await fixture({
    resourceIds: ['dingtalk:v1?base=baseId123'],
    resources: TEAM_RESOURCES,
    described: { 'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base' } },
  })
  try {
    await f.enterResourcesStep()
    // The only selection is a pasted reference, so the tree was never listed.
    assert.deepEqual(treeCalls(f), [])
    assert.equal(f.vm.dingtalkTreeLoaded, false)
    assert.equal(f.vm.showDingTalkResourceTree, false)

    // Removing it restores the tree-only presentation, and with it the listing
    // that the preview had replaced.
    f.selector().removeManualReference('dingtalk:v1?base=baseId123')
    await settle()
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(treeCalls(f), [''])
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2'])
  } finally { f.close() }
})

// The described row joins the tree, which is what makes an expandable reference
// (a Base, whose wiki children the connector lists) reachable and openable like
// any other node — and it survives a listing that replaces the whole array.
test('a described row is merged into the tree as a root', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base', has_children: true },
    },
  })
  try {
    await f.enterResourcesStep()
    await add(f, 'baseId123', 'base')
    await settle()

    // The described row is visible even though no listing contains it.
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2', 'dingtalk:v1?base=baseId123'])

    // Adding the reference collapsed the tree again; opening it keeps the row.
    assert.equal(f.vm.showDingTalkResourceTree, false)
    await f.vm.toggleDingTalkResourceTree()
    await settle()
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2', 'dingtalk:v1?base=baseId123'])

    // A fresh listing replaces the whole array; the described row is merged back
    // into it rather than dropped.
    await f.vm.loadResources()
    await settle()
    assert.deepEqual(visibleIds(f.vm), ['ws-1', 'ws-2', 'dingtalk:v1?base=baseId123'])
  } finally { f.close() }
})

// A saved manual reference is pre-filled in the selector and described when the
// resources step opens, so the preview is readable without ever opening the
// collapsed tree.
test('a saved manual reference is pre-filled and described on entering the step', async () => {
  const f = await fixture({
    resourceIds: ['dingtalk:v1?node=savedNodeId'],
    resources: TEAM_RESOURCES,
    described: { 'dingtalk:v1?node=savedNodeId': { name: 'Ledger.axls', type: 'document' } },
  })
  try {
    await f.enterResourcesStep()
    const selector = f.selector()
    assert.equal(selector.manualKind, 'node')
    assert.equal(selector.manualId, 'savedNodeId')
    assert.deepEqual(treeCalls(f), [])
    assert.deepEqual(describeCalls(f), ['dingtalk:v1?describe=dingtalk:v1?node=savedNodeId'])
    assert.deepEqual(
      [...selector.selectionRows].map((row: any) => ({ ...row })),
      [{
        id: 'dingtalk:v1?node=savedNodeId', manual: true,
        kind: 'datasource.dingtalk.manualKindNode', label: 'Ledger.axls',
      }],
    )

    // Re-entering the step does not ask again: the answer is cached.
    f.vm.step = 1
    await f.vm.nextStep()
    await settle()
    assert.equal(describeCalls(f).length, 1)
  } finally { f.close() }
})

// The describe call is made against a data source, and the dialog is what
// creates the draft one. Entering the step is where that draft comes from.
test('entering the step creates the draft the describe call is made against', async () => {
  const f = await fixture({ create: true, resources: TEAM_RESOURCES })
  try {
    await f.enterResourcesStep()
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(f.calls.map(call => call.method), ['createDataSource'])
    assert.equal(f.calls[0].args[0].status, 'paused')

    // The selector is handed that draft, which is what its describe call needs.
    await add(f, 'docId456')
    await settle()
    assert.deepEqual(describeCalls(f), ['dingtalk:v1?describe=dingtalk:v1?node=docId456'])
  } finally { f.close() }
})
