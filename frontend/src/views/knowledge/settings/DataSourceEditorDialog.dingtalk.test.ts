import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

// Manual DingTalk entry (钉钉链接或 ID): a personal-space document and a
// multi-dimensional table are readable by id but never appear in the lazy-load
// tree, so the user pastes a link or an id and picks which of the two it is.
// These tests pin the resource_ids the dialog writes, the inline error it
// shows, and the way the step presents the selection: a preview of everything
// that will sync (manual references and tree picks together) with the picker
// demoted behind an expander that says it adds to the preview. The tree-only
// path is untouched.
const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./DataSourceEditorDialog.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'datasource-editor-dingtalk-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

async function fixture({
  resourceIds = [] as string[],
  resources = [] as any[],
  described = {} as Record<string, any>,
  describeError = false,
  childResources = {} as Record<string, any[]>,
  create = false,
  createFails = false,
} = {}) {
  const calls: Array<{ method: string; args: any[] }> = []
  // Resource calls are recorded separately from create/update calls: the
  // tests below assert exactly which listings a step makes (and which it
  // deliberately does not), while the pre-existing assertions count only the
  // data source writes.
  const resourceCalls: string[] = []
  // Every t() call is recorded with its parameters, so a test can assert what a
  // row actually says even though the mock returns the key itself.
  const i18nCalls: Array<{ key: string; params?: any }> = []
  // The first attempt fails when the test asks for it, so a test can tell a
  // remembered failure apart from a retried one.
  let createFailures = createFails ? 1 : 0
  const api = {
    async createDataSource(data: any) {
      calls.push({ method: 'createDataSource', args: [data] })
      if (createFailures > 0) {
        createFailures--
        throw new Error('create failed')
      }
      return { id: 'temp-one' }
    },
    async updateDataSource(id: string, data: any) {
      calls.push({ method: 'updateDataSource', args: [id, JSON.parse(JSON.stringify(data))] })
    },
    async listResources(_id: string, parentId?: string) {
      resourceCalls.push(parentId ?? '')
      // A describe request asks about one reference and is answered with that
      // reference's own row (never with children); an unknown reference or a
      // failure yields nothing, which the dialog must survive.
      if (parentId && parentId.startsWith('dingtalk:v1?describe=')) {
        if (describeError) throw new Error('describe failed')
        const reference = parentId.slice('dingtalk:v1?describe='.length)
        const row = described[reference]
        return row ? [{ external_id: reference, ...row }] : []
      }
      // A parent the test gave its own children is answered with them — the
      // lazy expansion of one node — while every other listing keeps returning
      // the single root listing the pre-existing tests rely on.
      if (parentId && childResources[parentId]) return childResources[parentId]
      return resources
    },
    async resolveResourceAncestors() { return { data: { ancestors: [] } } },
    async deleteDataSource() {},
    async triggerSync() {},
    async validateConnection() {},
    async validateCredentials() {},
  }
  const props = reactive({
    visible: false, kbId: 'kb-one',
    // create: a brand-new data source, so no draft exists yet and the dialog
    // has to create the one the picker lists through.
    dataSource: create ? null : {
      id: 'source-one', name: 'DingTalk', type: 'dingtalk',
      credentials: { credentials: { configured: true } },
      config: { resource_ids: resourceIds, settings: {} },
      sync_schedule: '0 0 */6 * * *', sync_mode: 'incremental',
      conflict_strategy: 'overwrite', sync_deletions: true,
    },
  })
  const exports: any = {}
  runInNewContext(compiled, {
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
      if (name === '@/api/datasource') return api
      return { default: {} }
    },
    URL, console,
  })
  const component = exports.default
  component.render = () => null
  const renderer = createRenderer<any, any>({
    createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    insert() {}, remove() {}, setElementText() {}, setText() {}, patchProp() {},
    parentNode: () => null, nextSibling: () => null,
  })
  const instance = ref<any>()
  const app = renderer.createApp({ render: () => h(component, { ...props, ref: instance }) })
  app.mount({})
  props.visible = true
  await nextTick()
  const vm = instance.value
  vm.step = 2
  return { vm, props, calls, resourceCalls, i18nCalls, close: () => app.unmount() }
}

// The fixture's API resolves on microtasks only, so a handful of ticks settles
// the calls the component starts without awaiting them (the tree listing and
// the name lookup are both fire-and-forget from the user's point of view).
async function settle() {
  for (let i = 0; i < 8; i++) await Promise.resolve()
}

// enterResourcesStep walks the wizard the way the dialog does: the resources
// step is entered with nextStep, which is where the tree listing (and the name
// lookup) is triggered.
async function enterResourcesStep(vm: any) {
  vm.step = 1
  await vm.nextStep()
  await settle()
}

const isDescribeCall = (parentId: string) => parentId.startsWith('dingtalk:v1?describe=')
const treeCalls = (f: any) => f.resourceCalls.filter((id: string) => !isDescribeCall(id))
const describeCalls = (f: any) => f.resourceCalls.filter((id: string) => isDescribeCall(id))

// add() uses the manual entry the way the UI does: type a link (or an id) into
// the field, choose the kind, press Add.
async function add(vm: any, input: string, kind: 'node' | 'base' = 'node') {
  vm.dingtalkManualId = input
  vm.dingtalkManualKind = kind
  await nextTick()
  return vm.applyDingTalkManualEntry()
}

test('a pasted DingTalk document link becomes a bare node reference', async () => {
  const f = await fixture()
  try {
    const ok = await add(
      f.vm,
      'https://alidocs.dingtalk.com/i/nodes/node_7f3a9c2e5b1d4a8c6e0f?utm_scene=team_space',
    )
    assert.equal(ok, true)
    // The link is normalized into the stored reference, and the field keeps
    // that bare id: the box shows exactly what was added.
    assert.equal(f.vm.dingtalkManualId, 'node_7f3a9c2e5b1d4a8c6e0f')
    assert.equal(f.vm.dingtalkManualError, '')
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?node=node_7f3a9c2e5b1d4a8c6e0f'])
    assert.deepEqual([...f.vm.form.config.resource_ids], ['dingtalk:v1?node=node_7f3a9c2e5b1d4a8c6e0f'])
  } finally { f.close() }
})

test('a bare id is accepted, and a typed id is applied when leaving the step', async () => {
  const f = await fixture()
  try {
    const ok = await add(f.vm, 'docId456')
    assert.equal(ok, true)
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?node=docId456'])

    // Typed but not added: advancing the wizard must not drop it silently.
    f.vm.dingtalkManualId = 'docId789'
    await nextTick()
    await f.vm.nextStep()
    assert.deepEqual([...f.vm.selectedResourceIds], [
      'dingtalk:v1?node=docId456',
      'dingtalk:v1?node=docId789',
    ])
    assert.equal(f.vm.step, 3)
  } finally { f.close() }
})

test('the type selector decides between node= and base= for identical links', async () => {
  const f = await fixture()
  try {
    // A Base link and a document link have the same shape, so only the selector
    // can tell them apart.
    const url = 'https://alidocs.dingtalk.com/i/nodes/sharedId123'
    assert.equal(await add(f.vm, url, 'base'), true)
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?base=sharedId123'])
    assert.equal(await add(f.vm, url, 'node'), true)
    assert.deepEqual([...f.vm.selectedResourceIds], [
      'dingtalk:v1?base=sharedId123',
      'dingtalk:v1?node=sharedId123',
    ])
    assert.deepEqual([...f.vm.dingtalkManualReferences], [
      'dingtalk:v1?base=sharedId123',
      'dingtalk:v1?node=sharedId123',
    ])
    // Manual ids are not in the tree, so they carry no check state; the count
    // must still report them as selected.
    assert.equal(f.vm.selectedResourceCount, 2)
  } finally { f.close() }
})

test('an unusable entry shows an inline error and selects nothing', async () => {
  const f = await fixture()
  try {
    const ok = await add(f.vm, 'https://example.com/not-a-dingtalk-link')
    assert.equal(ok, false)
    assert.equal(f.vm.dingtalkManualError, 'datasource.dingtalk.manualIdRequired')
    assert.deepEqual([...f.vm.selectedResourceIds], [])
    assert.deepEqual([...f.vm.form.config.resource_ids], [])
  } finally { f.close() }
})

test('a committed entry stays in the field, a rejected one keeps what was typed', async () => {
  const f = await fixture()
  try {
    assert.equal(await add(f.vm, 'docId456'), true)
    // Committed: the field keeps the id it added, so the entry just made stays
    // readable (and reusable) instead of disappearing.
    assert.equal(f.vm.dingtalkManualId, 'docId456')

    // Rejected: the unusable text stays in the field, where it can be corrected,
    // rather than being cleared and forcing a retype.
    assert.equal(await add(f.vm, 'https://example.com/not-a-dingtalk-link'), false)
    assert.equal(f.vm.dingtalkManualId, 'https://example.com/not-a-dingtalk-link')
    assert.equal(f.vm.dingtalkManualError, 'datasource.dingtalk.manualIdRequired')

    // A link is committed through the same path: it stays in the field as the
    // bare id it carried.
    assert.equal(
      await add(f.vm, 'https://alidocs.dingtalk.com/i/nodes/linkId789?utm_scene=team_space'),
      true,
    )
    assert.equal(f.vm.dingtalkManualId, 'linkId789')
    assert.deepEqual([...f.vm.selectedResourceIds], [
      'dingtalk:v1?node=docId456',
      'dingtalk:v1?node=linkId789',
    ])
  } finally { f.close() }
})

// The field keeps a committed entry, so the same value can be committed again
// (by pressing Add, or by leaving the step) without the selection — or the
// resource_ids the final submit sends — growing a second copy of it.
test('a retained entry is added exactly once, however often it is committed', async () => {
  const f = await fixture()
  try {
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.equal(f.vm.dingtalkManualId, 'docId456')

    // Pressing Add again with the same value adds nothing new.
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?node=docId456'])
    assert.deepEqual([...f.vm.form.config.resource_ids], ['dingtalk:v1?node=docId456'])
    assert.equal(f.vm.selectedResourceCount, 1)

    // Leaving the step re-applies the retained value through the same path; the
    // selection still holds it once and exactly one preview row is shown.
    assert.equal(f.vm.dingtalkManualReferences.length, 1)
    await f.vm.nextStep()
    assert.equal(f.vm.step, 3)
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?node=docId456'])
    assert.deepEqual([...f.vm.form.config.resource_ids], ['dingtalk:v1?node=docId456'])
    assert.equal(previewRows(f).length, 1)
  } finally { f.close() }
})

test('a manual entry is added alongside tree selections and stays removable', async () => {
  const f = await fixture()
  try {
    f.vm.selectedResourceIds = ['legacy-workspace']
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.deepEqual([...f.vm.selectedResourceIds], ['legacy-workspace', 'dingtalk:v1?node=docId456'])
    // Tree ids never match the manual pattern, so only the manual one is listed
    // and only it can be removed.
    assert.deepEqual([...f.vm.dingtalkManualReferences], ['dingtalk:v1?node=docId456'])
    f.vm.removeDingTalkManualReference('dingtalk:v1?node=docId456')
    assert.deepEqual([...f.vm.selectedResourceIds], ['legacy-workspace'])
    assert.deepEqual([...f.vm.form.config.resource_ids], ['legacy-workspace'])
  } finally { f.close() }
})

test('a saved manual reference is pre-filled and submitted unchanged', async () => {
  const f = await fixture({ resourceIds: ['dingtalk:v1?base=savedBaseId'] })
  try {
    assert.equal(f.vm.dingtalkManualKind, 'base')
    assert.equal(f.vm.dingtalkManualId, 'savedBaseId')
    assert.deepEqual([...f.vm.dingtalkManualReferences], ['dingtalk:v1?base=savedBaseId'])
    assert.equal(f.vm.selectedResourceCount, 1)

    await add(f.vm, 'savedNodeId', 'node')
    await f.vm.handleSubmit()
    assert.equal(f.calls.length, 1)
    assert.deepEqual(f.calls[0].args[1].config.resource_ids, [
      'dingtalk:v1?base=savedBaseId',
      'dingtalk:v1?node=savedNodeId',
    ])
  } finally { f.close() }
})

// The knowledge-base tree is a picker, not a preview: while manual references
// exist it is collapsed behind an explicit expander, so eight unrelated team
// knowledge bases are not shown next to one pasted 多维表. Expanding it must
// still work exactly as before.
const TEAM_RESOURCES = [
  { external_id: 'ws-1', name: 'Team knowledge base', type: 'workspace', has_children: false },
  { external_id: 'ws-2', name: 'Another team knowledge base', type: 'workspace', has_children: false },
]

test('a manual reference collapses the knowledge-base tree, which the expander still opens', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.vm.loadResources()
    // No manual reference yet: the tree is exactly the pre-existing picker.
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])

    assert.equal(await add(f.vm, 'baseId123', 'base'), true)
    // The manual reference exists, so the tree starts collapsed ...
    assert.equal(f.vm.hasDingTalkManualSelection, true)
    assert.equal(f.vm.dingtalkTreeExpanded, false)
    assert.equal(f.vm.showDingTalkResourceTree, false)

    // ... and the explicit expander brings back the same resources.
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])

    // Collapsing again hides it; removing the manual reference restores the
    // tree-only presentation without any expander.
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, false)
    f.vm.removeDingTalkManualReference('dingtalk:v1?base=baseId123')
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
  } finally { f.close() }
})

test('a refilled manual selection collapses the tree again by default', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.vm.loadResources()
    await add(f.vm, 'firstBase', 'base')
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)

    // Removing the last manual reference brings the plain tree back (the
    // expander disappears with it) ...
    f.vm.removeDingTalkManualReference('dingtalk:v1?base=firstBase')
    assert.equal(f.vm.showDingTalkResourceTree, true)

    // ... and a new manual reference collapses the tree again: that is the
    // default whenever a manual reference is selected.
    await add(f.vm, 'secondBase', 'base')
    assert.equal(f.vm.dingtalkTreeExpanded, false)
    assert.equal(f.vm.showDingTalkResourceTree, false)
  } finally { f.close() }
})

test('an explicit expand survives further manual entries', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.vm.loadResources()
    await add(f.vm, 'firstBase', 'base')
    f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)

    // Adding to the manual selection never collapses a tree the user opened on
    // purpose: the two paths stay additive, not either/or.
    await add(f.vm, 'secondBase', 'base')
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.equal(f.vm.dingtalkManualReferences.length, 2)
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])
  } finally { f.close() }
})

// The selection area is a PREVIEW of what this data source will sync, not a
// view of the tree: manual references and tree picks are listed together, and
// the picker itself is demoted behind the expander.
const PREVIEW_RESOURCES = [
  { external_id: 'ws-1', name: 'Team knowledge base', type: 'wiki_space', has_children: false },
  { external_id: 'ws-2', name: 'Another team knowledge base', type: 'workspace', has_children: false },
]

// Rows are rebuilt as host objects: the component runs in a vm realm, so its
// objects do not compare reference-equal with host literals.
function previewRows(f: any) {
  return [...f.vm.dingtalkSelectionRows].map((row: any) => ({ ...row }))
}

test('the selection area previews manual references and tree picks together', async () => {
  const f = await fixture({ resources: PREVIEW_RESOURCES })
  try {
    await f.vm.loadResources()
    await add(f.vm, 'baseId123', 'base')
    // Manual-only selection: one row naming the kind and the id.
    assert.deepEqual(previewRows(f), [
      { id: 'dingtalk:v1?base=baseId123', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'baseId123' },
    ])

    f.vm.toggleResource('ws-1')
    f.vm.toggleResource('ws-2')
    // The preview is the FULL selection: the tree picks join the manual row,
    // named from the loaded tree, with a generic kind when the connector has no
    // type label for the node.
    assert.deepEqual(previewRows(f), [
      { id: 'dingtalk:v1?base=baseId123', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'baseId123' },
      { id: 'ws-1', manual: false, kind: 'datasource.resourceType.wikiSpace', label: 'Team knowledge base' },
      { id: 'ws-2', manual: false, kind: 'datasource.dingtalk.selectionTreeKind', label: 'Another team knowledge base' },
    ])

    // Removing a tree row from the preview unchecks it in the tree too.
    f.vm.removeTreeSelectionRow('ws-1')
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?base=baseId123', 'ws-2'])
    assert.equal(f.vm.selectedResourceCount, 2)
    assert.equal(previewRows(f).length, 2)
  } finally { f.close() }
})

test('a saved tree selection the tree cannot show is still previewed', async () => {
  // DingTalk content that cannot be enumerated (personal-space nodes, Bases) is
  // exactly what the tree fails to reveal; the preview must not depend on the
  // node being present in `resources`.
  const f = await fixture({
    resourceIds: ['dingtalk:v1?base=savedBaseId', 'missing-node-id'],
    resources: PREVIEW_RESOURCES,
  })
  try {
    await f.vm.loadResources()
    assert.equal(f.vm.showDingTalkResourceTree, false)
    assert.deepEqual(previewRows(f), [
      { id: 'dingtalk:v1?base=savedBaseId', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'savedBaseId' },
      { id: 'missing-node-id', manual: false, kind: 'datasource.dingtalk.selectionTreeKind', label: 'missing-node-id' },
    ])
  } finally { f.close() }
})

test('manual references stay removable from the preview', async () => {
  const f = await fixture()
  try {
    await add(f.vm, 'baseId123', 'base')
    await add(f.vm, 'docId456', 'node')
    assert.equal(f.vm.dingtalkManualKindLabel('dingtalk:v1?base=baseId123'), 'datasource.dingtalk.manualKindBase')
    assert.equal(f.vm.dingtalkManualKindLabel('dingtalk:v1?node=docId456'), 'datasource.dingtalk.manualKindNode')
    assert.equal(f.vm.dingtalkManualReferenceId('dingtalk:v1?base=baseId123'), 'baseId123')

    f.vm.removeDingTalkManualReference('dingtalk:v1?base=baseId123')
    assert.deepEqual([...f.vm.selectedResourceIds], ['dingtalk:v1?node=docId456'])
    assert.deepEqual([...f.vm.form.config.resource_ids], ['dingtalk:v1?node=docId456'])
    assert.deepEqual(previewRows(f), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'docId456' },
    ])
  } finally { f.close() }
})

test('tree picks and manual references stay additive in one resource_ids array', async () => {
  const f = await fixture({ resources: TEAM_RESOURCES })
  try {
    await f.vm.loadResources()
    f.vm.toggleResource('ws-1')
    assert.equal(await add(f.vm, 'baseId123', 'base'), true)
    // Picking from the tree is not an alternative to the manual id: both are
    // kept, and one combined array is submitted.
    assert.deepEqual([...f.vm.selectedResourceIds], ['ws-1', 'dingtalk:v1?base=baseId123'])
    assert.equal(f.vm.selectedResourceCount, 2)

    await f.vm.handleSubmit()
    const submitted = f.calls.at(-1)!
    assert.equal(submitted.method, 'updateDataSource')
    assert.deepEqual(submitted.args[1].config.resource_ids, ['ws-1', 'dingtalk:v1?base=baseId123'])
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
    await enterResourcesStep(f.vm)
    // Collapsed: the preview is the whole presentation, so no listing ran —
    // not the root listing, and no attempt to expand anything.
    assert.equal(f.vm.step, 2)
    assert.equal(f.vm.showDingTalkResourceTree, false)
    assert.deepEqual(treeCalls(f), [])
    assert.equal(f.vm.dingtalkTreeLoaded, false)

    // Opening the expander fetches the tree exactly once, and the saved tree
    // selection would be revealed as before (there is none here). The saved
    // manual reference is named by its described row, which joins the tree as
    // an extra root.
    await f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(treeCalls(f), [''])
    assert.equal(f.vm.dingtalkTreeLoaded, true)
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id),
      ['ws-1', 'ws-2', 'dingtalk:v1?base=savedBaseId'])
    assert.equal(f.vm.visibleTree.at(-1).resource.name, 'Project tracker.able')

    // Collapsing and opening again must not fetch a second time.
    await f.vm.toggleDingTalkResourceTree()
    assert.deepEqual(treeCalls(f), [''])
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
    await enterResourcesStep(f.vm)
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(treeCalls(f), [''])
    assert.deepEqual(describeCalls(f), [])
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])
  } finally { f.close() }
})

// Removing the last manual reference restores the tree-only presentation, and
// with it the listing that was skipped while the preview replaced the tree.
test('removing the last manual reference lists the tree it restored', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: { 'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base' } },
  })
  try {
    await add(f.vm, 'baseId123', 'base')
    await settle()
    assert.deepEqual(treeCalls(f), [])

    f.vm.removeDingTalkManualReference('dingtalk:v1?base=baseId123')
    await settle()
    assert.equal(f.vm.hasDingTalkManualSelection, false)
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual(treeCalls(f), [''])
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])
    // The described row went with the reference it described.
    assert.deepEqual(Object.keys(f.vm.dingtalkManualRows), [])
  } finally { f.close() }
})

// A manual reference is described by id, and the name that comes back is the
// one the sync will title the item with — never the raw id the user pasted.
test('a manual reference is described and shown by name', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      'dingtalk:v1?node=docId456': { name: 'Ledger.axls', type: 'document', description: '' },
      'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base', description: 'Checklist, Details' },
    },
  })
  try {
    assert.equal(await add(f.vm, 'docId456', 'node'), true)
    assert.equal(await add(f.vm, 'baseId123', 'base'), true)
    await settle()

    // One describe call per reference, sent as the picker-only describe form of
    // the reference itself — not the root listing and not an expansion.
    assert.deepEqual(describeCalls(f).sort(), [
      'dingtalk:v1?describe=dingtalk:v1?base=baseId123',
      'dingtalk:v1?describe=dingtalk:v1?node=docId456',
    ])
    assert.deepEqual(treeCalls(f), [])
    assert.deepEqual(previewRows(f), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'Ledger.axls' },
      { id: 'dingtalk:v1?base=baseId123', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'Project tracker.able' },
    ])
    // The id is still the row's identity (and its hover title), so a name that
    // is wrong can never make the row ambiguous.
    assert.deepEqual(previewRows(f).map((row: any) => row.id),
      ['dingtalk:v1?node=docId456', 'dingtalk:v1?base=baseId123'])
    // A described selection has a row in the tree, so it is counted there — not
    // once there and once again as a manual id.
    assert.equal(f.vm.selectedResourceCount, 2)
  } finally { f.close() }
})

// The described row joins the tree, which is what makes an expandable
// reference (a Base, whose wiki children the connector lists) reachable and
// openable like any other node.
test('a described row is merged into the tree as a root', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base', has_children: true },
    },
  })
  try {
    await f.vm.loadResources()
    await add(f.vm, 'baseId123', 'base')
    await settle()

    // The described row is visible even though no listing contains it.
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id),
      ['ws-1', 'ws-2', 'dingtalk:v1?base=baseId123'])

    // Adding the reference collapsed the tree again; opening it keeps the row.
    assert.equal(f.vm.showDingTalkResourceTree, false)
    await f.vm.toggleDingTalkResourceTree()
    assert.equal(f.vm.showDingTalkResourceTree, true)
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id),
      ['ws-1', 'ws-2', 'dingtalk:v1?base=baseId123'])

    // A fresh listing replaces the whole array; the described row is merged
    // back into it rather than dropped.
    await f.vm.loadResources()
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id),
      ['ws-1', 'ws-2', 'dingtalk:v1?base=baseId123'])
  } finally { f.close() }
})

// A Base row says what selecting it syncs. Its wiki children are ordinary
// selectable documents, so without the line the expansion would read as the
// Base's contents — which the base= reference does not sync.
test('a Base row explains what it syncs, and its children do not', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base', description: 'Checklist, Details' },
    },
  })
  try {
    assert.equal(f.vm.resourceHint({ type: 'base', description: 'Checklist, Details' }),
      'datasource.dingtalk.resourceHintBase')
    // A Base whose tables could not be listed still explains itself.
    assert.equal(f.vm.resourceHint({ type: 'base', description: '' }),
      'datasource.dingtalk.resourceHintBase')
    // Every other row stays exactly as it was: no extra line.
    assert.equal(f.vm.resourceHint({ type: 'document', description: 'Checklist' }), '')
    assert.equal(f.vm.resourceHint({ type: 'wiki_space', description: '' }), '')
    assert.equal(f.vm.resourceHint({ type: 'base_child', description: '' }), '')
    assert.equal(f.vm.resourceHint({ type: 'folder' }), '')
  } finally { f.close() }
})

// A preview that only names the selection is not a preview of what will sync:
// the tables a Base ingests are named under its row, in the row, and are
// readable without expanding anything — the documents the row can expand are a
// different thing entirely.
test('a Base preview row names the tables it will sync', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      'dingtalk:v1?base=baseId123': {
        name: 'Project tracker.able', type: 'base',
        description: 'Checklist, Details, Timeline',
      },
    },
  })
  try {
    await add(f.vm, 'baseId123', 'base')
    await settle()

    const row = previewRows(f)[0]
    assert.equal(row.label, 'Project tracker.able')
    assert.equal(f.vm.dingtalkSelectionRowHint(row), 'datasource.dingtalk.resourceHintBase')
    // The line names each table and how many there are — the whole list, not a
    // prefix of it, because the list is the point of the line.
    const tableList = f.i18nCalls
      .filter(call => call.key === 'datasource.dingtalk.resourceHintBaseTableList')
      .at(-1)!
    assert.equal(tableList.params.count, 3)
    assert.equal(tableList.params.tables, 'Checklist · Details · Timeline')

    // The names come from the connector's own list (notableTableNames joins
    // with ", "); the picker only re-separates them for display.
    assert.deepEqual([...f.vm.dingtalkTableNames('Checklist, Details')], ['Checklist', 'Details'])
    assert.deepEqual([...f.vm.dingtalkTableNames('')], [])
    assert.deepEqual([...f.vm.dingtalkTableNames(' Checklist ,, Details ')], ['Checklist', 'Details'])
  } finally { f.close() }
})

test('a Base whose tables could not be listed still says what it syncs', async () => {
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      'dingtalk:v1?base=baseId123': {
        name: 'Project tracker.able', type: 'base', description: '',
      },
    },
  })
  try {
    await add(f.vm, 'baseId123', 'base')
    await settle()

    const row = previewRows(f)[0]
    assert.equal(f.vm.dingtalkSelectionRowHint(row), 'datasource.dingtalk.resourceHintBase')
    // No count is invented for tables the connector never listed: the row says
    // "all tables" instead of "0 tables".
    assert.equal(
      f.i18nCalls.some(call => call.key === 'datasource.dingtalk.resourceHintBaseTableList'),
      false,
    )
    assert.equal(
      f.i18nCalls.some(call => call.key === 'datasource.dingtalk.resourceHintBaseAllTables'),
      true,
    )
  } finally { f.close() }
})

// The row that has children can be opened in place, through the same lazy
// listing the tree uses (listResources(parent_id=<ref>)), so the preview shows
// what is inside a selection instead of only its name.
test('expanding a Base preview row lists its knowledge-base documents once', async () => {
  const baseRef = 'dingtalk:v1?base=baseId123'
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      [baseRef]: {
        name: 'Project tracker.able', type: 'base',
        description: 'Checklist, Details', has_children: true,
      },
    },
    childResources: {
      [baseRef]: [
        {
          external_id: 'dingtalk:v1?node=appendix', name: 'Appendix.adoc',
          type: 'base_child', parent_id: baseRef,
        },
        {
          external_id: 'dingtalk:v1?node=notes', name: 'Notes.adoc',
          type: 'base_child', parent_id: baseRef,
        },
      ],
    },
  })
  try {
    await add(f.vm, 'baseId123', 'base')
    await settle()
    const row = previewRows(f)[0]
    assert.equal(f.vm.dingtalkSelectionRowExpandable(row), true)
    assert.equal(f.vm.dingtalkSelectionRowExpanded(row.id), false)
    assert.deepEqual([...f.vm.dingtalkSelectionRowChildren(row.id)], [])

    f.vm.toggleExpand(row.id)
    await settle()
    // Exactly one listing, addressed by the reference itself: the call the tree
    // makes when a node is expanded, not a second loading path.
    assert.deepEqual(treeCalls(f), [baseRef])
    assert.equal(f.vm.dingtalkSelectionRowExpanded(row.id), true)
    assert.deepEqual(
      [...f.vm.dingtalkSelectionRowChildren(row.id)].map((child: any) => child.name),
      ['Appendix.adoc', 'Notes.adoc'],
    )
    // Each child is a knowledge-base document that syncs on its own: the type
    // label is what keeps them apart from the tables the row names above.
    assert.deepEqual(
      [...f.vm.dingtalkSelectionRowChildren(row.id)].map((child: any) => child.type),
      ['base_child', 'base_child'],
    )
    assert.equal(f.vm.resourceTypeLabel('base_child'), 'datasource.dingtalk.resourceTypeBaseChild')

    // Listing the children is looking, not selecting: the base= reference is
    // still the whole selection.
    assert.deepEqual([...f.vm.selectedResourceIds], [baseRef])
    assert.deepEqual([...f.vm.form.config.resource_ids], [baseRef])

    // Collapsing and expanding again reuses the children already loaded: the
    // listing is not repeated.
    f.vm.toggleExpand(row.id)
    assert.equal(f.vm.dingtalkSelectionRowExpanded(row.id), false)
    f.vm.toggleExpand(row.id)
    await settle()
    assert.deepEqual(treeCalls(f), [baseRef])
  } finally { f.close() }
})

test('expanding a folder node preview row lists the children under it', async () => {
  const folderRef = 'dingtalk:v1?node=folderId123'
  const f = await fixture({
    resources: TEAM_RESOURCES,
    described: {
      [folderRef]: { name: 'My documents', type: 'folder', has_children: true },
    },
    childResources: {
      [folderRef]: [
        {
          external_id: 'dingtalk:v1?node=doc1', name: 'Plan.adoc',
          type: 'document', parent_id: folderRef,
        },
      ],
    },
  })
  try {
    await add(f.vm, 'folderId123', 'node')
    await settle()
    const row = previewRows(f)[0]
    assert.equal(f.vm.dingtalkSelectionRowExpandable(row), true)

    f.vm.toggleExpand(row.id)
    await settle()
    assert.deepEqual(treeCalls(f), [folderRef])
    assert.deepEqual(
      [...f.vm.dingtalkSelectionRowChildren(row.id)].map((child: any) => child.name),
      ['Plan.adoc'],
    )
  } finally { f.close() }
})

// A row with no children offers no disclosure control at all: an expander there
// would promise content that does not exist.
test('a preview row without children offers no disclosure control', async () => {
  const docRef = 'dingtalk:v1?node=docId456'
  const folderRef = 'dingtalk:v1?node=folderId123'
  const f = await fixture({
    resourceIds: ['missing-node-id'],
    resources: TEAM_RESOURCES,
    described: {
      [docRef]: { name: 'Ledger.axls', type: 'document' },
      [folderRef]: { name: 'My documents', type: 'folder', has_children: true },
    },
  })
  try {
    await add(f.vm, 'docId456', 'node')
    await add(f.vm, 'folderId123', 'node')
    await settle()

    const [missingRow, docRow, folderRow] = previewRows(f)
    // A plain document is a leaf: nothing to expand, so no control.
    assert.equal(f.vm.dingtalkSelectionRowExpandable(docRow), false)
    // A folder is expandable ...
    assert.equal(f.vm.dingtalkSelectionRowExpandable(folderRow), true)
    // ... and a saved selection the tree cannot show has no children fact at
    // all, so it gets no control either.
    assert.equal(f.vm.dingtalkSelectionRowExpandable(missingRow), false)
    // None of them was expanded, so nothing was listed.
    assert.deepEqual(treeCalls(f), [])
  } finally { f.close() }
})

// A name lookup is never a reason for the step to fail: whatever the connector
// answers (nothing, or an error), the row keeps the id the user pasted.
test('an unresolved name falls back to the id', async () => {
  const noRow = await fixture({ resources: TEAM_RESOURCES })
  try {
    await add(noRow.vm, 'docId456', 'node')
    await settle()
    assert.deepEqual(previewRows(noRow), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'docId456' },
    ])
    assert.deepEqual(treeCalls(noRow), [])
  } finally { noRow.close() }

  const failure = await fixture({ resources: TEAM_RESOURCES, describeError: true })
  try {
    await add(failure.vm, 'docId456', 'node')
    await settle()
    assert.deepEqual(previewRows(failure), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'docId456' },
    ])
    // A failed lookup leaves the selection untouched and submits as before.
    await failure.vm.handleSubmit()
    assert.deepEqual(failure.calls.at(-1)!.args[1].config.resource_ids, ['dingtalk:v1?node=docId456'])
  } finally { failure.close() }
})

// A saved manual reference is described when the resources step opens, so the
// preview is readable without ever opening the collapsed tree.
test('a saved manual reference is described on entering the step', async () => {
  const f = await fixture({
    resourceIds: ['dingtalk:v1?node=savedNodeId'],
    resources: TEAM_RESOURCES,
    described: { 'dingtalk:v1?node=savedNodeId': { name: 'Ledger.axls', type: 'document' } },
  })
  try {
    await enterResourcesStep(f.vm)
    assert.deepEqual(treeCalls(f), [])
    assert.deepEqual(describeCalls(f), ['dingtalk:v1?describe=dingtalk:v1?node=savedNodeId'])
    assert.deepEqual(previewRows(f), [
      { id: 'dingtalk:v1?node=savedNodeId', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'Ledger.axls' },
    ])
    // Re-entering the step does not ask again: the answer is cached.
    f.vm.step = 1
    await f.vm.nextStep()
    await settle()
    assert.equal(describeCalls(f).length, 1)
  } finally { f.close() }
})

// The draft the picker lists through is created on first use. The create
// endpoint rejects a request without a knowledge base or a registered connector
// type before the connector is ever reached, so such a request is certain to
// fail and must never be sent; and a skipped attempt must not be remembered as
// a failure, or a later one with a real type could never create the draft.
test('a load that cannot create a draft sends nothing and does not poison the draft', async () => {
  const f = await fixture({ create: true, resources: TEAM_RESOURCES })
  try {
    // Create mode, no connector type picked yet: the form cannot produce a data
    // source, so no create and no listing is sent, and nothing is reported.
    assert.equal(f.vm.form.type, '')
    await f.vm.loadResources()
    assert.equal(f.calls.length, 0)
    assert.deepEqual(treeCalls(f), [])
    assert.equal(f.vm.tempDsId, '')
    assert.equal(f.vm.loadingResources, false)

    // Whitespace is as unusable as empty.
    f.vm.form.type = '   '
    await f.vm.loadResources()
    assert.equal(f.calls.length, 0)
    assert.deepEqual(treeCalls(f), [])

    // The Drive root loader creates the same paused draft for its own listing,
    // so the same guard covers it.
    f.vm.driveFolderToken = 'folderTokenSynthetic'
    await f.vm.loadDriveRoot()
    assert.equal(f.calls.length, 0)
    assert.deepEqual(treeCalls(f), [])

    // The knowledge base is the other half of the contract. (The prop reaches
    // the component on the next render tick, exactly as it does in the app.)
    f.vm.form.type = 'dingtalk'
    f.props.kbId = '  '
    await nextTick()
    await f.vm.loadResources()
    assert.equal(f.calls.length, 0)
    assert.deepEqual(treeCalls(f), [])
    assert.equal(f.vm.loadingResources, false)
    f.props.kbId = 'kb-one'
    await nextTick()

    // With a real type the same call creates exactly one paused draft and lists
    // through it, exactly as before.
    await f.vm.loadResources()
    const creates = f.calls.filter(call => call.method === 'createDataSource')
    assert.equal(creates.length, 1)
    assert.equal(creates[0].args[0].type, 'dingtalk')
    assert.equal(creates[0].args[0].knowledge_base_id, 'kb-one')
    assert.equal(creates[0].args[0].status, 'paused')
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(treeCalls(f), [''])
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])

    // The draft is remembered: a second load reuses it (in create mode the form
    // is kept in step through the update endpoint) instead of creating a row.
    await f.vm.loadResources()
    assert.equal(f.calls.filter(call => call.method === 'createDataSource').length, 1)
    assert.equal(f.calls.filter(call => call.method === 'updateDataSource').length, 1)
  } finally { f.close() }
})

// A create that fails is a failure of that attempt, not of the dialog: the
// failed promise must not be remembered, or every later attempt would be handed
// the same rejection instead of creating the draft.
test('a failed draft creation is not remembered and is retried', async () => {
  const f = await fixture({ create: true, resources: TEAM_RESOURCES, createFails: true })
  try {
    f.vm.form.type = 'dingtalk'
    await f.vm.loadResources()
    assert.equal(f.calls.filter(call => call.method === 'createDataSource').length, 1)
    assert.equal(f.vm.tempDsId, '')
    // No draft means nothing to list through: the failure is reported, and no
    // listing is sent against an id that does not exist.
    assert.deepEqual(treeCalls(f), [])
    assert.equal(f.vm.loadingResources, false)

    // The next attempt creates the draft for real and lists through it.
    await f.vm.loadResources()
    assert.equal(f.calls.filter(call => call.method === 'createDataSource').length, 2)
    assert.equal(f.vm.tempDsId, 'temp-one')
    assert.deepEqual(treeCalls(f), [''])
    assert.deepEqual([...f.vm.visibleTree].map((r: any) => r.resource.external_id), ['ws-1', 'ws-2'])
  } finally { f.close() }
})
