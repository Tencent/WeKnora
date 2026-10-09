import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'
import * as resourcePresentation from '../resourcePresentation'
import * as dingtalkResources from './dingtalkResources'

// The DingTalk manual entry (钉钉链接或 ID) and the preview of what the data
// source will sync. A personal-space document and a multi-dimensional table are
// readable by id but never appear in the lazy-load tree, so the user pastes a
// link or an id and picks which of the two it is; the preview then has to show
// the real selection — manual references and tree picks together — because the
// tree cannot show the manual part at all.
//
// The dialog owns the selection and the tree, so these tests drive this
// component on its own: they pin what it writes back (the model), what it asks
// the dialog to do (describe rows, expand a row, remove a tree pick), and what
// it renders from the two facts it reads (which rows exist, which children an
// expanded row has). The parent's own step handling is covered by
// DataSourceEditorDialog.dingtalk.test.ts.
const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./DingTalkManualSelection.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
// The component exposes its own surface (defineExpose), so the empty
// compiler-generated expose() call is dropped rather than replaced: the test
// drives exactly the API the dialog drives.
const script = compileScript(descriptor, { id: 'dingtalk-manual-selection-test' }).content
  .replace('__expose();', '')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

// A lazy tree of two roots, plus the reference grammar the component shares with
// the dialog: both modules are the real ones, so a reference this component
// writes is a reference the dialog and the connector agree on.
const STUB_MODULES: Record<string, unknown> = {
  '../resourcePresentation': resourcePresentation,
  './dingtalkResources': dingtalkResources,
}

interface FixtureOptions {
  resourceIds?: string[]
  resources?: any[]
  described?: Record<string, any>
  describeError?: boolean
  childResources?: Record<string, any[]>
  dataSourceId?: string
  savedReference?: string
}

// The dialog's side of the contract: it indexes children by parent, owns the
// expansion state, and lists one level on demand. Reproduced here so the
// component is exercised against the same facts the dialog hands it.
function buildChildrenMap(resources: any[]): Map<string, any[]> {
  const map = new Map<string, any[]>()
  for (const r of resources) {
    if (!r.parent_id) continue
    const siblings = map.get(r.parent_id)
    if (siblings) siblings.push(r)
    else map.set(r.parent_id, [r])
  }
  return map
}

async function fixture({
  resourceIds = [] as string[],
  resources = [] as any[],
  described = {} as Record<string, any>,
  describeError = false,
  childResources = {} as Record<string, any[]>,
  dataSourceId = 'temp-one',
  savedReference = '',
}: FixtureOptions = {}) {
  // Describe requests and child listings are recorded apart: the component
  // makes exactly one kind of call itself (a describe), and never the root
  // listing the dialog owns.
  const resourceCalls: string[] = []
  // Every t() call is recorded with its parameters, so a test can assert what a
  // row actually says even though the mock returns the key itself.
  const i18nCalls: Array<{ key: string; params?: any }> = []
  const api = {
    async listResources(_id: string, parentId?: string) {
      resourceCalls.push(parentId ?? '')
      // A describe request asks about one reference and is answered with that
      // reference's own row (never with children); an unknown reference or a
      // failure yields nothing, which the component must survive.
      if (parentId && parentId.startsWith('dingtalk:v1?describe=')) {
        if (describeError) throw new Error('describe failed')
        const reference = parentId.slice('dingtalk:v1?describe='.length)
        const row = described[reference]
        return row ? [{ external_id: reference, ...row }] : []
      }
      if (parentId && childResources[parentId]) return childResources[parentId]
      return []
    },
  }
  const state = reactive({
    selected: [...resourceIds],
    resources: [...resources],
    childrenMap: buildChildrenMap(resources),
    expandedResourceIds: new Set<string>(),
    loadingChildrenIds: new Set<string>(),
    dataSourceId,
    loading: false,
    savedReference,
  })
  // What the component asked the dialog to do.
  const describedReports: Array<Record<string, any>> = []
  const expandRequests: string[] = []
  function onUpdateSelected(ids: string[]) { state.selected = [...ids] }
  function onDescribed(rows: Record<string, any>) { describedReports.push({ ...rows }) }
  // The dialog's toggleExpand: it flips the state, then lazily lists one level
  // through the same endpoint the tree uses.
  async function onExpand(id: string) {
    expandRequests.push(id)
    const next = new Set(state.expandedResourceIds)
    if (next.has(id)) {
      next.delete(id)
      state.expandedResourceIds = next
      return
    }
    next.add(id)
    state.expandedResourceIds = next
    if (state.childrenMap.has(id)) return
    state.loadingChildrenIds = new Set(state.loadingChildrenIds).add(id)
    const res: any = await api.listResources(state.dataSourceId, id)
    const children: any[] = res?.data || res || []
    if (children.length > 0) {
      const known = new Set(state.resources.map(r => r.external_id))
      const missing = children.filter(child => !known.has(child.external_id))
      if (missing.length > 0) {
        state.resources = [...state.resources, ...missing]
        state.childrenMap = buildChildrenMap(state.resources)
      }
    }
    const settled = new Set(state.loadingChildrenIds)
    settled.delete(id)
    state.loadingChildrenIds = settled
  }
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
      if (name in STUB_MODULES) return STUB_MODULES[name]
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
  const app = renderer.createApp({
    render: () => h(component, {
      selectedResourceIds: state.selected,
      'onUpdate:selectedResourceIds': onUpdateSelected,
      resources: state.resources,
      childrenMap: state.childrenMap,
      expandedResourceIds: state.expandedResourceIds,
      loadingChildrenIds: state.loadingChildrenIds,
      dataSourceId: state.dataSourceId,
      loading: state.loading,
      savedReference: state.savedReference,
      onDescribed,
      onExpand,
      ref: instance,
    }),
  })
  app.mount({})
  await nextTick()
  return {
    vm: instance.value, state, api, resourceCalls, i18nCalls, describedReports, expandRequests,
    close: () => app.unmount(),
  }
}

// The fixture's API resolves on microtasks only, so a handful of ticks settles
// the calls the component starts without awaiting them (the name lookup is
// fire-and-forget from the user's point of view).
async function settle() {
  for (let i = 0; i < 8; i++) await Promise.resolve()
}

const describeCalls = (f: any) => f.resourceCalls.filter((id: string) => id.startsWith('dingtalk:v1?describe='))
// Listings the dialog makes for a node (an expansion), apart from the describe
// requests this component makes itself.
const treeCalls = (f: any) => f.resourceCalls.filter((id: string) => !id.startsWith('dingtalk:v1?describe='))

// add() uses the manual entry the way the UI does: type a link (or an id) into
// the field, choose the kind, press Add.
async function add(vm: any, input: string, kind: 'node' | 'base' = 'node') {
  vm.manualId = input
  vm.manualKind = kind
  await nextTick()
  return vm.applyManualEntry()
}

// Rows are rebuilt as host objects: the component runs in a vm realm, so its
// objects do not compare reference-equal with host literals.
function previewRows(vm: any) {
  return [...vm.selectionRows].map((row: any) => ({ ...row }))
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
    assert.equal(f.vm.manualId, 'node_7f3a9c2e5b1d4a8c6e0f')
    assert.equal(f.vm.manualError, '')
    assert.deepEqual([...f.state.selected], ['dingtalk:v1?node=node_7f3a9c2e5b1d4a8c6e0f'])
  } finally { f.close() }
})

test('a bare id is accepted and committed as the chosen kind', async () => {
  const f = await fixture()
  try {
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.deepEqual([...f.state.selected], ['dingtalk:v1?node=docId456'])
    // The entry stays in the field, so the dialog can re-apply it when the
    // wizard advances without duplicating anything.
    assert.equal(f.vm.manualId, 'docId456')
    assert.equal(f.vm.hasPendingEntry, true)
  } finally { f.close() }
})

test('the type selector decides between node= and base= for identical links', async () => {
  const f = await fixture()
  try {
    // A Base link and a document link have the same shape, so only the selector
    // can tell them apart.
    const url = 'https://alidocs.dingtalk.com/i/nodes/sharedId123'
    assert.equal(await add(f.vm, url, 'base'), true)
    assert.deepEqual([...f.state.selected], ['dingtalk:v1?base=sharedId123'])
    assert.equal(await add(f.vm, url, 'node'), true)
    assert.deepEqual([...f.state.selected], [
      'dingtalk:v1?base=sharedId123',
      'dingtalk:v1?node=sharedId123',
    ])
    assert.deepEqual([...f.vm.manualReferences], [
      'dingtalk:v1?base=sharedId123',
      'dingtalk:v1?node=sharedId123',
    ])
  } finally { f.close() }
})

test('an unusable entry shows an inline error and selects nothing', async () => {
  const f = await fixture()
  try {
    const ok = await add(f.vm, 'https://example.com/not-a-dingtalk-link')
    assert.equal(ok, false)
    assert.equal(f.vm.manualError, 'datasource.dingtalk.manualIdRequired')
    assert.deepEqual([...f.state.selected], [])
    assert.equal(f.vm.hasPendingEntry, true)
  } finally { f.close() }
})

test('a committed entry stays in the field, a rejected one keeps what was typed', async () => {
  const f = await fixture()
  try {
    assert.equal(await add(f.vm, 'docId456'), true)
    // Committed: the field keeps the id it added, so the entry just made stays
    // readable (and reusable) instead of disappearing.
    assert.equal(f.vm.manualId, 'docId456')

    // Rejected: the unusable text stays in the field, where it can be corrected,
    // rather than being cleared and forcing a retype.
    assert.equal(await add(f.vm, 'https://example.com/not-a-dingtalk-link'), false)
    assert.equal(f.vm.manualId, 'https://example.com/not-a-dingtalk-link')
    assert.equal(f.vm.manualError, 'datasource.dingtalk.manualIdRequired')

    // A link is committed through the same path: it stays in the field as the
    // bare id it carried.
    assert.equal(
      await add(f.vm, 'https://alidocs.dingtalk.com/i/nodes/linkId789?utm_scene=team_space'),
      true,
    )
    assert.equal(f.vm.manualId, 'linkId789')
    assert.deepEqual([...f.state.selected], [
      'dingtalk:v1?node=docId456',
      'dingtalk:v1?node=linkId789',
    ])
  } finally { f.close() }
})

// The field keeps a committed entry, so the same value can be committed again
// (by pressing Add, or by the dialog re-applying it when the wizard advances)
// without the selection growing a second copy of it.
test('a retained entry is added exactly once, however often it is committed', async () => {
  const f = await fixture()
  try {
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.deepEqual([...f.state.selected], ['dingtalk:v1?node=docId456'])
    assert.equal(f.vm.manualReferences.length, 1)
    assert.equal(previewRows(f.vm).length, 1)
  } finally { f.close() }
})

test('a manual entry is added alongside tree selections and stays removable', async () => {
  const f = await fixture({ resourceIds: ['legacy-workspace'] })
  try {
    assert.equal(await add(f.vm, 'docId456'), true)
    assert.deepEqual([...f.state.selected], ['legacy-workspace', 'dingtalk:v1?node=docId456'])
    // Tree ids never match the manual pattern, so only the manual one is listed
    // and only it can be removed.
    assert.deepEqual([...f.vm.manualReferences], ['dingtalk:v1?node=docId456'])
    f.vm.removeManualReference('dingtalk:v1?node=docId456')
    assert.deepEqual([...f.state.selected], ['legacy-workspace'])
  } finally { f.close() }
})

test('a saved manual reference pre-fills the entry', async () => {
  const f = await fixture({ savedReference: 'dingtalk:v1?base=savedBaseId' })
  try {
    assert.equal(f.vm.manualKind, 'base')
    assert.equal(f.vm.manualId, 'savedBaseId')
  } finally { f.close() }
})

const PREVIEW_RESOURCES = [
  { external_id: 'ws-1', name: 'Team knowledge base', type: 'wiki_space', has_children: false },
  { external_id: 'ws-2', name: 'Another team knowledge base', type: 'workspace', has_children: false },
]

test('the preview lists manual references and tree picks together', async () => {
  const f = await fixture({ resources: PREVIEW_RESOURCES })
  try {
    await add(f.vm, 'baseId123', 'base')
    // Manual-only selection: one row naming the kind and the id.
    assert.deepEqual(previewRows(f.vm), [
      { id: 'dingtalk:v1?base=baseId123', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'baseId123' },
    ])

    f.state.selected = ['dingtalk:v1?base=baseId123', 'ws-1', 'ws-2']
    await nextTick()
    // The preview is the FULL selection: the tree picks join the manual row,
    // named from the rows the dialog listed, with a generic kind when the
    // connector has no type label for the node.
    assert.deepEqual(previewRows(f.vm), [
      { id: 'dingtalk:v1?base=baseId123', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'baseId123' },
      { id: 'ws-1', manual: false, kind: 'datasource.resourceType.wikiSpace', label: 'Team knowledge base' },
      { id: 'ws-2', manual: false, kind: 'datasource.dingtalk.selectionTreeKind', label: 'Another team knowledge base' },
    ])

    // Removing a tree row from the preview writes the selection back.
    f.vm.removeTreeSelectionRow('ws-1')
    assert.deepEqual([...f.state.selected], ['dingtalk:v1?base=baseId123', 'ws-2'])
    assert.equal(previewRows(f.vm).length, 2)
  } finally { f.close() }
})

test('a saved tree selection the tree cannot show is still previewed', async () => {
  // DingTalk content that cannot be enumerated (personal-space nodes, Bases) is
  // exactly what the tree fails to reveal; the preview must not depend on the
  // node being present in the rows the dialog listed.
  const f = await fixture({
    resourceIds: ['dingtalk:v1?base=savedBaseId', 'missing-node-id'],
    resources: PREVIEW_RESOURCES,
  })
  try {
    assert.deepEqual(previewRows(f.vm), [
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
    assert.equal(dingtalkResources.dingtalkManualKind('dingtalk:v1?base=baseId123'), 'base')
    assert.equal(dingtalkResources.dingtalkManualReferenceId('dingtalk:v1?base=baseId123'), 'baseId123')

    f.vm.removeManualReference('dingtalk:v1?base=baseId123')
    assert.deepEqual([...f.state.selected], ['dingtalk:v1?node=docId456'])
    assert.deepEqual(previewRows(f.vm), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'docId456' },
    ])
  } finally { f.close() }
})

// A manual reference is described by id, and the name that comes back is the one
// the sync will title the item with — never the raw id the user pasted.
test('a manual reference is described and shown by name', async () => {
  const f = await fixture({
    resources: PREVIEW_RESOURCES,
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
    assert.deepEqual(previewRows(f.vm), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'Ledger.axls' },
      { id: 'dingtalk:v1?base=baseId123', manual: true, kind: 'datasource.dingtalk.manualKindBase', label: 'Project tracker.able' },
    ])
    // The id is still the row's identity (and its hover title), so a name that
    // is wrong can never make the row ambiguous.
    assert.deepEqual(previewRows(f.vm).map((row: any) => row.id),
      ['dingtalk:v1?node=docId456', 'dingtalk:v1?base=baseId123'])
    // The described rows are reported to the dialog, which merges them into the
    // tree so an expandable reference has a row there.
    assert.deepEqual(Object.keys(f.describedReports.at(-1)!).sort(), [
      'dingtalk:v1?base=baseId123', 'dingtalk:v1?node=docId456',
    ])
  } finally { f.close() }
})

// A Base row says what selecting it syncs. Its wiki children are ordinary
// selectable documents, so without the line the expansion would read as the
// Base's contents — which the base= reference does not sync.
test('only a Base row explains what it syncs', async () => {
  const f = await fixture({
    described: {
      'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base', description: 'Checklist, Details' },
    },
  })
  try {
    assert.equal(
      dingtalkResources.dingtalkResourceHint(
        { type: 'base', description: 'Checklist, Details' } as any, (k: string) => k),
      'datasource.dingtalk.resourceHintBase',
    )
    // A Base whose tables could not be listed still explains itself.
    assert.equal(
      dingtalkResources.dingtalkResourceHint(
        { type: 'base', description: '' } as any, (k: string) => k),
      'datasource.dingtalk.resourceHintBase',
    )
    // Every other row stays exactly as it was: no extra line.
    for (const resource of [
      { type: 'document', description: 'Checklist' },
      { type: 'wiki_space', description: '' },
      { type: 'base_child', description: '' },
      { type: 'folder' },
    ]) {
      assert.equal(
        dingtalkResources.dingtalkResourceHint(resource as any, (k: string) => k), '',
        resource.type,
      )
    }

    await add(f.vm, 'baseId123', 'base')
    await settle()
    assert.equal(f.vm.rowHint(previewRows(f.vm)[0]), 'datasource.dingtalk.resourceHintBase')
  } finally { f.close() }
})

// A preview that only names the selection is not a preview of what will sync:
// the tables a Base ingests are named under its row, in the row, and are
// readable without expanding anything — the documents the row can expand are a
// different thing entirely.
test('a Base preview row names the tables it will sync', async () => {
  const f = await fixture({
    resources: PREVIEW_RESOURCES,
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

    const row = previewRows(f.vm)[0]
    assert.equal(row.label, 'Project tracker.able')
    assert.equal(f.vm.rowHint(row), 'datasource.dingtalk.resourceHintBase')
    // The line names each table and how many there are — the whole list, not a
    // prefix of it, because the list is the point of the line.
    const tableList = f.i18nCalls
      .filter(call => call.key === 'datasource.dingtalk.resourceHintBaseTableList')
      .at(-1)!
    assert.equal(tableList.params.count, 3)
    assert.equal(tableList.params.tables, 'Checklist · Details · Timeline')

    // The names come from the connector's own list (notableTableNames joins
    // with ", "); the picker only re-separates them for display.
    assert.deepEqual(dingtalkResources.dingtalkTableNames('Checklist, Details'), ['Checklist', 'Details'])
    assert.deepEqual(dingtalkResources.dingtalkTableNames(''), [])
    assert.deepEqual(dingtalkResources.dingtalkTableNames(' Checklist ,, Details '), ['Checklist', 'Details'])
  } finally { f.close() }
})

test('a Base whose tables could not be listed still says what it syncs', async () => {
  const f = await fixture({
    resources: PREVIEW_RESOURCES,
    described: {
      'dingtalk:v1?base=baseId123': { name: 'Project tracker.able', type: 'base', description: '' },
    },
  })
  try {
    await add(f.vm, 'baseId123', 'base')
    await settle()

    assert.equal(f.vm.rowHint(previewRows(f.vm)[0]), 'datasource.dingtalk.resourceHintBase')
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
test('expanding a Base preview row lists its documents through the dialog once', async () => {
  const baseRef = 'dingtalk:v1?base=baseId123'
  const f = await fixture({
    resources: PREVIEW_RESOURCES,
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
    const row = previewRows(f.vm)[0]
    assert.equal(f.vm.rowExpandable(row), true)
    assert.equal(f.vm.rowExpanded(row.id), false)
    assert.deepEqual([...f.vm.rowChildren(row.id)], [])

    // The disclosure control asks the dialog to open the row; the dialog is what
    // lists one level, through the call the tree makes when a node is expanded.
    f.vm.toggleRow(row.id)
    await settle()
    assert.deepEqual(f.expandRequests, [baseRef])
    assert.equal(f.vm.rowExpanded(row.id), true)
    assert.equal(f.vm.rowLoading(row.id), false)
    assert.deepEqual(
      [...f.vm.rowChildren(row.id)].map((child: any) => child.name),
      ['Appendix.adoc', 'Notes.adoc'],
    )
    // Each child is a knowledge-base document that syncs on its own: the type
    // label is what keeps them apart from the tables the row names above.
    assert.deepEqual(
      [...f.vm.rowChildren(row.id)].map((child: any) => child.type),
      ['base_child', 'base_child'],
    )
    assert.equal(resourcePresentation.resourceTypeLabel('base_child', (k: string) => k),
      'datasource.dingtalk.resourceTypeBaseChild')

    // Listing the children is looking, not selecting: the base= reference is
    // still the whole selection.
    assert.deepEqual([...f.state.selected], [baseRef])

    // Collapsing and expanding again reuses the children already loaded: the
    // listing is not repeated.
    f.vm.toggleRow(row.id)
    await settle()
    assert.equal(f.vm.rowExpanded(row.id), false)
    f.vm.toggleRow(row.id)
    await settle()
    assert.deepEqual(f.expandRequests, [baseRef, baseRef, baseRef])
    assert.equal(treeCalls(f).filter((id: string) => id === baseRef).length, 1)
  } finally { f.close() }
})

test('expanding a folder node preview row lists the children under it', async () => {
  const folderRef = 'dingtalk:v1?node=folderId123'
  const f = await fixture({
    resources: PREVIEW_RESOURCES,
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
    const row = previewRows(f.vm)[0]
    assert.equal(f.vm.rowExpandable(row), true)

    f.vm.toggleRow(row.id)
    await settle()
    assert.deepEqual(treeCalls(f), [folderRef])
    assert.deepEqual(
      [...f.vm.rowChildren(row.id)].map((child: any) => child.name),
      ['Plan.adoc'],
    )
  } finally { f.close() }
})

test('a preview row without children offers no disclosure control', async () => {
  const docRef = 'dingtalk:v1?node=docId456'
  const folderRef = 'dingtalk:v1?node=folderId123'
  const f = await fixture({
    resourceIds: ['missing-node-id'],
    described: {
      [docRef]: { name: 'Ledger.axls', type: 'document' },
      [folderRef]: { name: 'My documents', type: 'folder', has_children: true },
    },
  })
  try {
    await add(f.vm, 'docId456', 'node')
    await add(f.vm, 'folderId123', 'node')
    await settle()

    const [missingRow, docRow, folderRow] = previewRows(f.vm)
    // A plain document is a leaf: nothing to expand, so no control.
    assert.equal(f.vm.rowExpandable(docRow), false)
    // A folder is expandable ...
    assert.equal(f.vm.rowExpandable(folderRow), true)
    // ... and a saved selection the tree cannot show has no children fact at
    // all, so it gets no control either.
    assert.equal(f.vm.rowExpandable(missingRow), false)
    // None of them was expanded, so nothing was listed.
    assert.deepEqual(f.expandRequests, [])
    assert.deepEqual(treeCalls(f), [])
  } finally { f.close() }
})

// A name lookup is never a reason for the step to fail: whatever the connector
// answers (nothing, or an error), the row keeps the id the user pasted.
test('an unresolved name falls back to the id', async () => {
  const noRow = await fixture({ resources: PREVIEW_RESOURCES })
  try {
    await add(noRow.vm, 'docId456', 'node')
    await settle()
    assert.deepEqual(previewRows(noRow.vm), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'docId456' },
    ])
  } finally { noRow.close() }

  const failure = await fixture({ resources: PREVIEW_RESOURCES, describeError: true })
  try {
    await add(failure.vm, 'docId456', 'node')
    await settle()
    assert.deepEqual(previewRows(failure.vm), [
      { id: 'dingtalk:v1?node=docId456', manual: true, kind: 'datasource.dingtalk.manualKindNode', label: 'docId456' },
    ])
    // The selection is untouched by the failed lookup.
    assert.deepEqual([...failure.state.selected], ['dingtalk:v1?node=docId456'])
  } finally { failure.close() }
})

// The describe call needs a data source, and the dialog creates the draft for
// it when the resources step opens. A saved manual reference can therefore ask
// for its name before that draft exists — the answer must not be lost, or the
// row would keep the raw id for good.
test('a saved manual reference is described as soon as the dialog has a draft', async () => {
  const f = await fixture({
    resourceIds: ['dingtalk:v1?node=savedNodeId'],
    resources: PREVIEW_RESOURCES,
    described: { 'dingtalk:v1?node=savedNodeId': { name: 'Ledger.axls', type: 'document' } },
    dataSourceId: '',
  })
  try {
    await settle()
    // No draft: nothing is sent, and the row keeps the id.
    assert.deepEqual(describeCalls(f), [])
    assert.equal(previewRows(f.vm)[0].label, 'savedNodeId')

    // The dialog hands the draft over; the pending names are resolved then.
    f.state.dataSourceId = 'temp-one'
    await nextTick()
    await settle()
    assert.deepEqual(describeCalls(f), ['dingtalk:v1?describe=dingtalk:v1?node=savedNodeId'])
    assert.equal(previewRows(f.vm)[0].label, 'Ledger.axls')

    // Asking again does not repeat the call: the answer is cached.
    await f.vm.resolveManualNames()
    await settle()
    assert.equal(describeCalls(f).length, 1)
  } finally { f.close() }
})

test('a described row is reported to the dialog exactly once per reference', async () => {
  const f = await fixture({
    resources: PREVIEW_RESOURCES,
    described: { 'dingtalk:v1?node=docId456': { name: 'Ledger.axls', type: 'document' } },
  })
  try {
    await add(f.vm, 'docId456', 'node')
    await settle()
    assert.equal(f.describedReports.length, 1)
    assert.deepEqual(Object.keys(f.describedReports[0]), ['dingtalk:v1?node=docId456'])

    // Removing the reference tells the dialog the row is gone with it: a row
    // left behind would sit in the tree unchecked, as if still selected.
    f.vm.removeManualReference('dingtalk:v1?node=docId456')
    assert.deepEqual(f.describedReports.at(-1), {})
    assert.deepEqual([...f.state.selected], [])
  } finally { f.close() }
})
