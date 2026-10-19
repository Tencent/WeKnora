import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { runInNewContext } from 'node:vm'
import * as vue from 'vue'
import * as vueI18n from 'vue-i18n'
import { createI18n } from 'vue-i18n'
import enUS from '../../../i18n/locales/en-US'
import * as files from '../../../utils/files.ts'
import * as referenceSources from '../../../utils/referenceSources.ts'
import * as processingStall from '../../../utils/knowledgeProcessingStall.ts'

/**
 * The document list and card grid are rendered for real here: the SFC is
 * compiled with its template inlined and mounted on a tiny host renderer. The
 * host tree is enough to assert row order and to click a row, which source
 * regexes alone cannot do.
 */

type HostNode = {
  kind: 'element' | 'text' | 'comment'
  tag?: unknown
  text?: string
  props: Record<string, any>
  children: HostNode[]
  parent: HostNode | null
  style?: Record<string, string>
}

function hostElement(tag: unknown): HostNode {
  return { kind: 'element', tag, text: '', props: {}, children: [], parent: null, style: {} } as HostNode
}

const noopComponent = vue.defineComponent({ name: 'Stub', setup: () => () => null })

function evaluateModule(source: string, dependencies: Record<string, unknown>): any {
  const exports: any = {}
  const teleportTarget = hostElement('teleport-target')
  const fakeDocument = {
    querySelector: () => teleportTarget,
    createElement: () => hostElement('detached'),
    body: teleportTarget,
  }
  runInNewContext(
    ts.transpileModule(source, {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText,
    {
      exports,
      console,
      Set,
      document: fakeDocument,
      require(name: string) {
        if (name in dependencies) return dependencies[name]
        throw new Error(`Unexpected dependency: ${name}`)
      },
    },
  )
  return exports
}

const componentCache = new Map<string, any>()

function loadView(file: URL, extraDependencies: Record<string, unknown> = {}): any {
  const key = file.pathname
  const cached = componentCache.get(key)
  if (cached) return cached
  const { descriptor } = parse(readFileSync(file, 'utf8'), { filename: file.pathname })
  const script = compileScript(descriptor, { id: `folder-entry-${key}`, inlineTemplate: true }).content
  const module = evaluateModule(script, {
    vue,
    'vue-i18n': vueI18n,
    '@/utils/files': files,
    '@/utils/referenceSources': referenceSources,
    '@/utils/knowledgeProcessingStall': processingStall,
    './KnowledgeTagPopover.vue': noopComponent,
    './DocumentFileIcon.vue': noopComponent,
    './DocumentActionMenu.vue': noopComponent,
    './FolderPickerMenu.vue': noopComponent,
    '@/components/knowledge-processing-timeline.vue': noopComponent,
    ...extraDependencies,
  })
  componentCache.set(key, module.default)
  return module.default
}

function hostRenderer() {
  const teleportTarget = hostElement('teleport-target')
  return vue.createRenderer<HostNode, HostNode>({
    querySelector: () => teleportTarget,
    createElement: (tag) => hostElement(tag),
    insert(child, parent, anchor) {
      child.parent = parent
      const index = anchor ? parent.children.indexOf(anchor) : -1
      if (index >= 0) parent.children.splice(index, 0, child)
      else parent.children.push(child)
    },
    remove(child) {
      const parent = child.parent
      if (!parent) return
      const index = parent.children.indexOf(child)
      if (index >= 0) parent.children.splice(index, 1)
    },
    createText: (text) => ({ kind: 'text', text, props: {}, children: [], parent: null }),
    createComment: (text) => ({ kind: 'comment', text, props: {}, children: [], parent: null }),
    setText(node, text) {
      node.text = text
    },
    setElementText(node, text) {
      node.children = [{ kind: 'text', text, props: {}, children: [], parent: node }]
    },
    patchProp(el, key, _previous, next) {
      el.props[key] = next
    },
    parentNode: (node) => node.parent,
    nextSibling: (node) => {
      const parent = node.parent
      if (!parent) return null
      const index = parent.children.indexOf(node)
      return parent.children[index + 1] ?? null
    },
  })
}

function mountView(file: URL, props: Record<string, unknown>) {
  const renderer = hostRenderer()
  const app = renderer.createApp(loadView(file), {
    canEdit: false,
    canDownload: false,
    canMutateKnowledge: false,
    moveMenuMode: 'normal',
    moveTargetKbs: [],
    moveTargetsLoading: false,
    moveSelectedTargetName: '',
    moveMode: 'reuse_vectors',
    moveSubmitting: false,
    ...props,
  } as any)
  app.use(createI18n({ legacy: false, locale: 'en-US', messages: { 'en-US': enUS } }))
  for (const name of [
    't-icon',
    't-checkbox',
    't-popup',
    't-tag',
    't-button',
    't-radio',
    't-loading',
    't-tooltip',
    't-skeleton',
    't-input',
  ]) {
    app.component(
      name,
      vue.defineComponent({ name, setup: (_, { slots }) => () => vue.h('span', slots.default?.()) }),
    )
  }
  const root = hostElement('root')
  const vm = app.mount(root) as any
  return {
    root,
    vm,
    unmount: () => app.unmount(),
  }
}

function allElements(node: HostNode): HostNode[] {
  const elements: HostNode[] = []
  const walk = (current: HostNode) => {
    if (current.kind === 'element') elements.push(current)
    current.children.forEach(walk)
  }
  walk(node)
  return elements
}

function classesOf(node: HostNode): string[] {
  return String(node.props.class ?? '').split(/\s+/).filter(Boolean)
}

function textOf(node: HostNode): string {
  if (node.kind === 'text') return node.text ?? ''
  return node.children.map(textOf).join('')
}

const folder = {
  path: '培训材料库/2024使用培训视频',
  name: '2024使用培训视频',
  document_count: 1,
  total_count: 1,
}

const documents = [
  { id: 'doc-1', file_name: '天诛渗透攻防平台用户手册V202403', parse_status: 'completed', file_type: 'pdf' },
  { id: 'doc-2', file_name: '【使用必看】天诛2024新版本', parse_status: 'completed', file_type: 'pdf' },
]

function mountList(overrides: Record<string, unknown> = {}) {
  return mountView(new URL('./DocumentListView.vue', import.meta.url), {
    items: documents,
    subFolders: [folder],
    kbId: 'kb',
    selectedIds: new Set<string>(),
    traceVisibleIds: {},
    ...overrides,
  })
}

function mountCards(overrides: Record<string, unknown> = {}) {
  return mountView(new URL('./DocumentCardView.vue', import.meta.url), {
    items: documents,
    subFolders: [folder],
    kbId: 'kb',
    selectedIds: new Set<string>(),
    batchMode: false,
    traceAvailableById: {},
    ...overrides,
  })
}

test('the list renders one folder row before the document rows', () => {
  const opened: string[] = []
  const view = mountList({ onOpenFolder: (path: string) => opened.push(path) })
  try {
    const rows = allElements(view.root).filter((element) => classesOf(element).includes('doc-list-row'))
    const folderRows = rows.filter((row) => row.props['data-folder-path'] !== undefined)
    const documentRows = rows.filter((row) => row.props['data-folder-path'] === undefined)

    assert.equal(folderRows.length, 1)
    assert.equal(documentRows.length, 2)
    assert.ok(rows.indexOf(folderRows[0]) < rows.indexOf(documentRows[0]), 'folder row comes first')
    assert.equal(folderRows[0].props['data-folder-path'], folder.path)
    assert.match(textOf(folderRows[0]), /2024使用培训视频/)
    // The folder takes no part in batch selection: its checkbox cell stays empty.
    assert.equal(
      allElements(folderRows[0]).filter((element) => classesOf(element).includes('doc-list-check')).length,
      0,
    )

    folderRows[0].props.onClick({ stopPropagation() {} })
    assert.deepEqual(opened, [folder.path])
  } finally {
    view.unmount()
  }
})

test('the card grid renders one folder card before the document cards', () => {
  const opened: string[] = []
  const view = mountCards({ onOpenFolder: (path: string) => opened.push(path) })
  try {
    const cards = allElements(view.root).filter((element) => classesOf(element).includes('knowledge-card'))
    const folderCards = cards.filter((card) => card.props['data-folder-path'] !== undefined)
    const documentCards = cards.filter((card) => card.props['data-folder-path'] === undefined)

    assert.equal(folderCards.length, 1)
    assert.equal(documentCards.length, 2)
    assert.ok(cards.indexOf(folderCards[0]) < cards.indexOf(documentCards[0]), 'folder card comes first')
    assert.equal(folderCards[0].props['data-folder-path'], folder.path)
    assert.match(textOf(folderCards[0]), /2024使用培训视频/)

    folderCards[0].props.onClick({ stopPropagation() {} })
    assert.deepEqual(opened, [folder.path])
  } finally {
    view.unmount()
  }
})

test('no folder rows or cards are rendered when the caller filters them out', () => {
  const list = mountList({ subFolders: [] })
  try {
    const rows = allElements(list.root).filter((element) => classesOf(element).includes('doc-list-row'))
    assert.equal(rows.filter((row) => row.props['data-folder-path'] !== undefined).length, 0)
    assert.equal(rows.length, 2)
  } finally {
    list.unmount()
  }

  const cards = mountCards({ subFolders: [] })
  try {
    const cardElements = allElements(cards.root).filter((element) => classesOf(element).includes('knowledge-card'))
    assert.equal(cardElements.filter((card) => card.props['data-folder-path'] !== undefined).length, 0)
    assert.equal(cardElements.length, 2)
  } finally {
    cards.unmount()
  }
})

test('an absent folder list renders no folder rows and does not crash', () => {
  const list = mountList({ subFolders: undefined })
  try {
    const rows = allElements(list.root).filter((element) => classesOf(element).includes('doc-list-row'))
    assert.equal(rows.length, 2)
  } finally {
    list.unmount()
  }

  const cards = mountCards({ subFolders: undefined })
  try {
    const cardElements = allElements(cards.root).filter((element) => classesOf(element).includes('knowledge-card'))
    assert.equal(cardElements.length, 2)
  } finally {
    cards.unmount()
  }
})
