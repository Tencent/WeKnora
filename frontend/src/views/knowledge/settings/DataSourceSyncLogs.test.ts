import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import { compileScript, parse } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, h, nextTick, reactive, ref } from 'vue'

const require = createRequire(import.meta.url)
const filename = fileURLToPath(new URL('./DataSourceSyncLogs.vue', import.meta.url))
const { descriptor } = parse(readFileSync(filename, 'utf8'), { filename })
const script = compileScript(descriptor, { id: 'datasource-sync-logs-test' }).content
  .replace('__expose();', '')
  .replace('return __returned__', '__expose(__returned__); return __returned__')
const compiled = ts.transpileModule(script, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

const flush = async () => {
  await new Promise(resolve => setImmediate(resolve))
  await nextTick()
}

test('DataSourceSyncLogs surfaces deleted_items with titles and external IDs across sync rounds', async () => {
  const logs = [
    {
      id: 'log-round-3',
      data_source_id: 'ds-feishu',
      status: 'success',
      started_at: '2026-11-18T09:05:00Z',
      finished_at: '2026-11-18T09:05:02Z',
      items_total: 1,
      items_created: 0,
      items_updated: 0,
      items_deleted: 0,
      items_skipped: 1,
      items_failed: 0,
      error_message: '',
      result: {
        deleted_items: [{
          external_id: 'Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
          source_resource_id: '7701598185524874545:Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
          title: '文档 A',
        }],
      },
    },
    {
      id: 'log-round-2',
      data_source_id: 'ds-feishu',
      status: 'success',
      started_at: '2026-11-18T09:00:00Z',
      finished_at: '2026-11-18T09:00:03Z',
      items_total: 2,
      items_created: 0,
      items_updated: 1,
      items_deleted: 1,
      items_skipped: 0,
      items_failed: 0,
      error_message: '',
      result: {
        deleted_items: [{
          external_id: 'Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
          source_resource_id: '7701598185524874545:Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
          title: '',
        }],
      },
    },
  ]

  const props = reactive({ visible: false, dataSourceId: 'ds-feishu', dataSourceName: '飞书知识库' })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/api/datasource') return { async getSyncLogs() { return { data: logs } } }
      return { default: {} }
    },
    Date, Math, JSON,
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
  try {
    props.visible = true
    await flush()
    const vm = instance.value
    assert.equal(vm.logs.length, 2)

    // Round 3 (items_deleted=0, items_skipped=1, deleted_items has title):
    assert.equal(vm.deletedItemsCount(vm.logs[0]), 1)
    assert.equal(
      vm.formatDeletedItem(vm.deletedItems(vm.logs[0])[0]),
      '文档 A (Ut8Gw3GsbiNIAnkNp2kcaZTpnAe)',
    )

    // Round 2 fallback when title is empty: falls back cleanly to external_id.
    assert.equal(vm.deletedItemsCount(vm.logs[1]), 1)
    assert.equal(
      vm.formatDeletedItem(vm.deletedItems(vm.logs[1])[0]),
      'Ut8Gw3GsbiNIAnkNp2kcaZTpnAe',
    )

    // Over-cap (>50) deleted_items with items_deleted=0 preserves true count for deletedItemsMore.
    const manyDeletedLog = {
      ...vm.logs[0],
      items_deleted: 0,
      result: {
        deleted_items: Array.from({ length: 65 }, (_, idx) => ({
          external_id: `tok-${idx}`,
          title: `文档 ${idx}`,
        })),
      },
    }
    assert.equal(vm.deletedItems(manyDeletedLog).length, 50)
    assert.equal(vm.deletedItemsCount(manyDeletedLog), 65)

    // Coded ingest errors format with path-qualified title and fallback message when key is absent.
    assert.equal(
      vm.formatSyncError({
        title: '云盘目录/目录一/源码管理流程.png',
        code: 'prerequisite_not_configured',
        message: '上传图片文件需要设置VLM模型',
      }),
      '云盘目录/目录一/源码管理流程.png — 上传图片文件需要设置VLM模型',
    )
  } finally {
    app.unmount()
  }
})

test('DataSourceSyncLogs ignores stale reset and pagination responses after switching sources', async () => {
  const pending: {
    dataSourceId: string
    offset: number
    resolve: (value: { data: any[] }) => void
  }[] = []
  const props = reactive({ visible: false, dataSourceId: 'source-a', dataSourceName: 'Source A' })
  const exports: any = {}
  runInNewContext(compiled, {
    exports,
    require(name: string) {
      if (name === 'vue') return require('vue')
      if (name === 'vue-i18n') return { useI18n: () => ({ t: (key: string) => key }) }
      if (name === '@/api/datasource') return {
        getSyncLogs(dataSourceId: string, _limit: number, offset: number) {
          return new Promise(resolve => pending.push({ dataSourceId, offset, resolve }))
        },
      }
      return { default: {} }
    },
    Date, Math, JSON,
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
  try {
    props.visible = true
    await nextTick()
    assert.deepEqual(pending.map(({ dataSourceId, offset }) => ({ dataSourceId, offset })), [
      { dataSourceId: 'source-a', offset: 0 },
    ])

    props.visible = false
    props.dataSourceId = 'source-b'
    props.dataSourceName = 'Source B'
    props.visible = true
    await nextTick()
    assert.equal(pending[1].dataSourceId, 'source-b')

    const sourceBFirstPage = Array.from({ length: 50 }, (_, index) => ({
      id: `source-b-${index}`,
      status: 'success',
    }))
    pending[1].resolve({ data: sourceBFirstPage })
    await flush()
    assert.equal(instance.value.logs[0].id, 'source-b-0')
    assert.equal(instance.value.hasMore, true)

    // The slower source A response must not replace source B or its paging state.
    pending[0].resolve({ data: Array.from({ length: 50 }, (_, index) => ({
      id: `source-a-${index}`,
      status: 'success',
    })) })
    await flush()
    assert.equal(instance.value.logs[0].id, 'source-b-0')
    assert.equal(instance.value.hasMore, true)

    instance.value.loadMore()
    await nextTick()
    assert.equal(pending[2].offset, 50)
    assert.equal(instance.value.loadingMore, true)

    props.dataSourceId = 'source-c'
    props.dataSourceName = 'Source C'
    await nextTick()
    assert.equal(instance.value.loadingMore, false)
    assert.equal(instance.value.logs.length, 0)
    assert.equal(instance.value.hasMore, false)
    assert.equal(pending[3].dataSourceId, 'source-c')

    pending[3].resolve({ data: [{ id: 'source-c-0', status: 'success' }] })
    await flush()
    pending[2].resolve({ data: [{ id: 'source-b-50', status: 'success' }] })
    await flush()
    assert.deepEqual(instance.value.logs.map((log: any) => log.id), ['source-c-0'])
    assert.equal(instance.value.hasMore, false)
    assert.equal(instance.value.loading, false)
    assert.equal(instance.value.loadingMore, false)
  } finally {
    app.unmount()
  }
})
