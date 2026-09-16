import assert from 'node:assert/strict'
import { Buffer } from 'node:buffer'
import { readFile } from 'node:fs/promises'
import { dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

import { compileScript, parse } from '@vue/compiler-sfc'
import { build, type Plugin } from 'esbuild'

interface Deferred<T> {
  promise: Promise<T>
  resolve: (value: T) => void
}

interface EvaluationTaskStub {
  id: string
  labels: string[]
}

interface HumanRatingRevisionStub {
  rubric_key: string
  rubric_version: string
  score: number
}

interface HumanRatingPanelStub {
  open: boolean
  loading: boolean
  saving: boolean
  loaded: boolean
  items: HumanRatingRevisionStub[]
  score: number
  comment: string
  error: string
}

interface WorkbenchBindings {
  activeTaskId: { value: string }
  applyFilters: () => void
  baselineTaskId: { value: string }
  comparisonLoading: { value: boolean }
  comparisonResult: { value: { runs: Array<{ task_id: string }> } | null }
  detail: { value: { task: EvaluationTaskStub } | null }
  detailLoading: { value: boolean }
  questionLoading: { value: boolean }
  filters: { datasetId: string }
  ratingPanel: (sampleIndex: number) => HumanRatingPanelStub
  labelDraft: { value: string }
  listLoading: { value: boolean }
  loadTasks: (append?: boolean) => Promise<void>
  onComparisonCheckbox: (taskId: string, event: { target: { checked: boolean } }) => void
  openTask: (task: EvaluationTaskStub) => Promise<void>
  questions: { value: Array<{ qid: string }> }
  saveLabels: () => Promise<void>
  saveHumanRating: (sampleIndex: number) => Promise<void>
  savingLabels: { value: boolean }
  selectedTaskIds: { value: string[] }
  tasks: { value: EvaluationTaskStub[] }
  toggleHumanRatings: (sampleIndex: number) => Promise<void>
  runComparison: () => Promise<void>
  onTaskCreated: (task: EvaluationTaskStub) => Promise<void>
}

interface WorkbenchComponent {
  setup: (
    props: Record<string, never>,
    context: { expose: () => void },
  ) => WorkbenchBindings
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(done => {
    resolve = done
  })
  return { promise, resolve }
}

async function flushMicrotasks() {
  await Promise.resolve()
  await Promise.resolve()
}

const componentDependencies: Plugin = {
  name: 'evaluation-workbench-test-dependencies',
  setup(builder) {
    const modules: Record<string, string> = {
      vue: `
        export const defineComponent = value => value
        export const ref = value => ({ value })
        export const reactive = value => value
        export const computed = getter => ({ get value() { return getter() } })
        export const onMounted = callback => globalThis.__evaluationLifecycle.mounted.push(callback)
        export const onBeforeUnmount = callback => globalThis.__evaluationLifecycle.beforeUnmount.push(callback)
        export const watch = (source, callback) => globalThis.__evaluationLifecycle.watchers.push(callback)
      `,
      'tdesign-vue-next': `
        export const MessagePlugin = {
          error: value => globalThis.__evaluationMessages.push(['error', value]),
          success: value => globalThis.__evaluationMessages.push(['success', value]),
          warning: value => globalThis.__evaluationMessages.push(['warning', value]),
        }
      `,
      'vue-i18n': `export const useI18n = () => ({ t: key => key })`,
      '@lucide/vue': ['ChevronDown', 'CircleAlert', 'Coins', 'Crosshair', 'Database', 'Download', 'ExternalLink', 'FlaskConical', 'LoaderCircle', 'MessageSquareText', 'Plus', 'RefreshCw', 'ScanLine', 'SlidersHorizontal', 'Timer'].map(name => `export const ${name} = {};`).join('\n'),
      '@/stores/auth': `export const useAuthStore = () => ({ hasRole: () => true })`,
      '@/api/evaluation': `
        export const EVALUATION_STATUS = {
          pending: 0, running: 1, success: 2, failed: 3,
          timedOut: 4, interrupted: 5, canceled: 6,
        }
        export const createEvaluationRequestGate = () => {
          let generation = 0
          let currentKey = ''
          return {
            begin(key) {
              currentKey = key
              return { key, generation: ++generation }
            },
            isCurrent(token) {
              return token.key === currentKey && token.generation === generation
            },
            invalidate() {
              currentKey = ''
              generation += 1
            },
          }
        }
        const api = () => globalThis.__evaluationWorkbenchApi
        export const listEvaluationTasks = (...args) => api().listTasks(...args)
        export const getEvaluationDetail = (...args) => api().getDetail(...args)
        export const listEvaluationQuestions = (...args) => api().listQuestions(...args)
        export const listEvaluationHumanRatings = (...args) => api().listRatings(...args)
        export const appendEvaluationHumanRating = (...args) => api().appendRating(...args)
        export const compareEvaluationTasks = (...args) => api().compare(...args)
        export const downloadEvaluationArtifact = (...args) => api().download(...args)
        export const replaceEvaluationLabels = (...args) => api().replaceLabels(...args)
      `,
    }

    builder.onResolve({
      filter: /^(?:vue|tdesign-vue-next|vue-i18n|@lucide\/vue|@\/api\/evaluation|@\/stores\/auth)$/,
    }, args => ({ path: args.path, namespace: 'evaluation-workbench-test' }))
    builder.onLoad({ filter: /.*/, namespace: 'evaluation-workbench-test' }, args => ({
      contents: modules[args.path],
      loader: 'js',
    }))
    builder.onResolve({ filter: /Evaluation(?:Create|Datasets)Drawer\.vue$/ }, args => ({ path: args.path, namespace: 'evaluation-child' }))
    builder.onLoad({ filter: /.*/, namespace: 'evaluation-child' }, () => ({ contents: 'export default {}', loader: 'js' }))
  },
}

let componentPromise: Promise<WorkbenchComponent> | undefined

async function loadWorkbenchComponent(): Promise<WorkbenchComponent> {
  if (componentPromise) return componentPromise
  componentPromise = (async () => {
    const filename = fileURLToPath(new URL('./EvaluationWorkbench.vue', import.meta.url))
    const source = await readFile(filename, 'utf8')
    const descriptor = parse(source, { filename }).descriptor
    const compiled = compileScript(descriptor, { id: 'evaluation-workbench-test' })
    const result = await build({
      bundle: true,
      format: 'esm',
      logLevel: 'silent',
      platform: 'node',
      plugins: [componentDependencies],
      stdin: {
        contents: compiled.content,
        loader: 'ts',
        resolveDir: dirname(filename),
        sourcefile: filename,
      },
      target: 'node22',
      write: false,
    })
    const url = `data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`
    const module = await import(url) as { default: WorkbenchComponent }
    return module.default
  })()
  return componentPromise
}

function installWorkbenchApi(overrides: Record<string, (...args: any[]) => any>) {
  ;(globalThis as any).__evaluationLifecycle = { mounted: [], beforeUnmount: [], watchers: [] }
  ;(globalThis as any).__evaluationMessages = []
  ;(globalThis as any).__evaluationWorkbenchApi = {
    appendRating: async () => ({}),
    compare: async () => ({ runs: [], parameters: [], metrics: [] }),
    download: async () => undefined,
    getDetail: async (taskId: string) => ({ task: { id: taskId, labels: [] } }),
    listQuestions: async () => ({ items: [], next_cursor: '' }),
    listRatings: async () => [],
    listTasks: async () => ({ items: [], next_cursor: '' }),
    replaceLabels: async (_taskId: string, labels: string[]) => labels,
    ...overrides,
  }
}

test('a new task-list request starts during an older in-flight request and wins the state race', async () => {
  const first = deferred<{ items: EvaluationTaskStub[]; next_cursor: string }>()
  const second = deferred<{ items: EvaluationTaskStub[]; next_cursor: string }>()
  const calls: Array<{ datasetId: string }> = []
  installWorkbenchApi({
    listTasks: (filters: { datasetId: string }) => {
      calls.push({ datasetId: filters.datasetId })
      return calls.length === 1 ? first.promise : second.promise
    },
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })

  workbench.filters.datasetId = 'dataset-a'
  const requestA = workbench.loadTasks(false)
  workbench.filters.datasetId = 'dataset-b'
  const requestB = workbench.loadTasks(false)

  assert.deepEqual(calls, [{ datasetId: 'dataset-a' }, { datasetId: 'dataset-b' }])
  second.resolve({ items: [{ id: 'task-b', labels: [] }], next_cursor: '' })
  await requestB
  first.resolve({ items: [{ id: 'task-a', labels: [] }], next_cursor: '' })
  await requestA

  assert.deepEqual(workbench.tasks.value.map(task => task.id), ['task-b'])
  assert.equal(workbench.listLoading.value, false)
})

test('question responses are scoped by task id and generation when task B opens during task A', async () => {
  const questionsA = deferred<{ items: Array<{ qid: string }>; next_cursor: string }>()
  const questionsB = deferred<{ items: Array<{ qid: string }>; next_cursor: string }>()
  const requestedTaskIds: string[] = []
  installWorkbenchApi({
    listQuestions: (taskId: string) => {
      requestedTaskIds.push(taskId)
      return taskId === 'task-a' ? questionsA.promise : questionsB.promise
    },
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })

  await workbench.openTask({ id: 'task-a', labels: [] })
  await workbench.openTask({ id: 'task-b', labels: [] })
  assert.deepEqual(requestedTaskIds, ['task-a', 'task-b'])

  questionsB.resolve({ items: [{ qid: 'question-b' }], next_cursor: '' })
  await flushMicrotasks()
  questionsA.resolve({ items: [{ qid: 'question-a' }], next_cursor: '' })
  await flushMicrotasks()

  assert.equal(workbench.activeTaskId.value, 'task-b')
  assert.deepEqual(workbench.questions.value.map(question => question.qid), ['question-b'])
})

test('task A label response cannot overwrite an already loaded task B', async () => {
  const saveA = deferred<string[]>()
  installWorkbenchApi({
    replaceLabels: (taskId: string) => {
      assert.equal(taskId, 'task-a')
      return saveA.promise
    },
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })
  const taskA = { id: 'task-a', labels: ['a-old'] }
  const taskB = { id: 'task-b', labels: ['b-current'] }
  workbench.tasks.value = [taskA, taskB]

  await workbench.openTask(taskA)
  workbench.labelDraft.value = 'a-saved'
  const pendingSave = workbench.saveLabels()
  assert.equal(workbench.savingLabels.value, true)
  await workbench.openTask(taskB)
  assert.equal(workbench.savingLabels.value, false)

  saveA.resolve(['a-saved'])
  await pendingSave

  assert.equal(workbench.detail.value?.task.id, 'task-b')
  assert.deepEqual(workbench.detail.value?.task.labels, ['b-current'])
  assert.equal(workbench.labelDraft.value, 'b-current')
  assert.deepEqual(taskA.labels, ['a-saved'])
  assert.deepEqual(taskB.labels, ['b-current'])
  assert.deepEqual((globalThis as any).__evaluationMessages, [])
})

test('task A label response remains scoped while task B detail is loading', async () => {
  const detailB = deferred<{ task: EvaluationTaskStub }>()
  const saveA = deferred<string[]>()
  installWorkbenchApi({
    getDetail: (taskId: string) => taskId === 'task-b'
      ? detailB.promise
      : Promise.resolve({ task: { id: taskId, labels: ['server-a'] } }),
    replaceLabels: () => saveA.promise,
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })
  const taskA = { id: 'task-a', labels: ['a-old'] }
  const taskB = { id: 'task-b', labels: ['b-current'] }
  workbench.tasks.value = [taskA, taskB]

  await workbench.openTask(taskA)
  workbench.labelDraft.value = 'a-saved'
  const pendingSave = workbench.saveLabels()
  const pendingOpenB = workbench.openTask(taskB)
  assert.equal(workbench.detail.value, null)

  saveA.resolve(['a-saved'])
  await pendingSave
  assert.equal(workbench.detail.value, null)
  assert.deepEqual(taskA.labels, ['a-saved'])

	detailB.resolve({ task: { id: 'task-b', labels: ['server-b'] } })
	await pendingOpenB
	const loadedDetail = Reflect.get(workbench.detail, 'value') as { task: EvaluationTaskStub } | null
	assert.equal(loadedDetail?.task.id, 'task-b')
	assert.deepEqual(loadedDetail?.task.labels, ['b-current'])
  assert.equal(workbench.labelDraft.value, 'b-current')
  assert.equal(workbench.savingLabels.value, false)
})

test('comparison responses remain bound to the selected runs and baseline', async () => {
  const comparisonAB = deferred<{ runs: Array<{ task_id: string }>; parameters: never[]; metrics: never[] }>()
  const comparisonAC = deferred<{ runs: Array<{ task_id: string }>; parameters: never[]; metrics: never[] }>()
  const calls: Array<{ task_ids: string[]; baseline_task_id: string }> = []
  installWorkbenchApi({
    compare: (request: { task_ids: string[]; baseline_task_id: string }) => {
      calls.push(request)
      return calls.length === 1 ? comparisonAB.promise : comparisonAC.promise
    },
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })
  workbench.selectedTaskIds.value = ['task-a', 'task-b']
  workbench.baselineTaskId.value = 'task-a'

  const pendingAB = workbench.runComparison()
  workbench.onComparisonCheckbox('task-b', { target: { checked: false } })
  workbench.onComparisonCheckbox('task-c', { target: { checked: true } })
  const pendingAC = workbench.runComparison()
  assert.deepEqual(calls, [
    { task_ids: ['task-a', 'task-b'], baseline_task_id: 'task-a' },
    { task_ids: ['task-a', 'task-c'], baseline_task_id: 'task-a' },
  ])

  comparisonAB.resolve({ runs: [{ task_id: 'task-a' }, { task_id: 'task-b' }], parameters: [], metrics: [] })
  await pendingAB
  assert.equal(workbench.comparisonResult.value, null)
  assert.equal(workbench.comparisonLoading.value, true)

  comparisonAC.resolve({ runs: [{ task_id: 'task-a' }, { task_id: 'task-c' }], parameters: [], metrics: [] })
  await pendingAC
  const completedComparison = Reflect.get(workbench.comparisonResult, 'value') as {
    runs: Array<{ task_id: string }>
  } | null
  assert.deepEqual(completedComparison?.runs.map(run => run.task_id), ['task-a', 'task-c'])
  assert.equal(workbench.comparisonLoading.value, false)
})

test('human rating submits one holistic answer-quality score with concise anchors', async () => {
  let submitted: unknown
  installWorkbenchApi({
    appendRating: async (taskId: string, sampleIndex: number, request: Record<string, unknown>) => {
      submitted = { taskId, sampleIndex, request }
      return {
        id: 'rating-1', tenant_id: 1, task_id: taskId, sample_index: sampleIndex,
        revision: 1, rater_id: 'admin', created_at: '2026-09-01T00:00:00Z',
        ...request,
      }
    },
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })
  workbench.activeTaskId.value = 'task-a'
  const panel = workbench.ratingPanel(4)
  panel.score = 4
  panel.comment = 'Minor wording issue'

  await workbench.saveHumanRating(4)

  assert.deepEqual(submitted, {
    taskId: 'task-a',
    sampleIndex: 4,
    request: {
      rubric_key: 'answer-quality',
      rubric_version: '1.1.0',
      rubric_snapshot: {
        title: 'Overall answer quality',
        dimension: 'overall_answer_quality',
        considerations: ['correctness', 'relevance', 'grounding'],
        scale: {
          1: 'Incorrect or unsupported',
          2: 'Major quality issues',
          3: 'Acceptable with notable issues',
          4: 'Strong with minor issues',
          5: 'Correct, relevant, and well grounded',
        },
      },
      score: 4,
      comment: 'Minor wording issue',
    },
  })
})

test('an older rubric revision stays in history without prefilling the current score', async () => {
  installWorkbenchApi({
    listRatings: async () => [{
      id: 'rating-old', tenant_id: 1, task_id: 'task-a', sample_index: 4,
      revision: 2, rater_id: 'admin', rubric_key: 'answer-quality',
      rubric_version: '1.0.0', rubric_snapshot: {}, score: 5,
      created_at: '2026-08-31T00:00:00Z',
    }],
  })
  const component = await loadWorkbenchComponent()
  const workbench = component.setup({}, { expose() {} })
  workbench.activeTaskId.value = 'task-a'

  await workbench.toggleHumanRatings(4)

  const panel = workbench.ratingPanel(4)
  assert.equal(panel.items[0]?.rubric_version, '1.0.0')
  assert.equal(panel.score, 3)
})

test('a newly created evaluation refreshes the task list and opens its detail', async () => {
  const created = { id: 'created-task', labels: [] }
  const listed: string[] = [], details: string[] = []
  installWorkbenchApi({
    listTasks: async (filters: { datasetId: string }) => { listed.push(filters.datasetId); return { items: [created], next_cursor: '' } },
    getDetail: async (id: string) => { details.push(id); return { task: created } },
  })
  const workbench = (await loadWorkbenchComponent()).setup({}, { expose() {} })
  workbench.filters.datasetId = 'unrelated-filter'
  await workbench.onTaskCreated(created)
  assert.deepEqual(listed, [''])
  assert.deepEqual(details, ['created-task'])
  assert.equal(workbench.activeTaskId.value, 'created-task')
  assert.equal(workbench.detail.value?.task.id, 'created-task')
  assert.deepEqual(workbench.tasks.value.map(task => task.id), ['created-task'])
})

test('a tenant switch invalidates a previous task-list response and clears selected runs', async () => {
  const delayed = deferred<{ items: EvaluationTaskStub[]; next_cursor: string }>()
  let calls = 0
  installWorkbenchApi({ listTasks: () => ++calls === 1 ? delayed.promise : Promise.resolve({ items: [{ id: 'new-space-task', labels: [] }], next_cursor: '' }) })
  const workbench = (await loadWorkbenchComponent()).setup({}, { expose() {} })
  const pending = workbench.loadTasks()
  workbench.selectedTaskIds.value = ['old-task']
  ;(globalThis as any).__evaluationLifecycle.watchers[0]()
  await flushMicrotasks()
  delayed.resolve({ items: [{ id: 'old-space-task', labels: [] }], next_cursor: '' })
  await pending
  assert.deepEqual(workbench.tasks.value.map(task => task.id), ['new-space-task'])
  assert.deepEqual(workbench.selectedTaskIds.value, [])
  assert.equal(workbench.detail.value, null)
})

test('applying a filter clears the opened task detail and a late detail response cannot restore it', async () => {
  const detailA = deferred<{ task: EvaluationTaskStub }>()
  installWorkbenchApi({
    getDetail: () => detailA.promise,
    listTasks: async () => ({ items: [{ id: 'task-a', labels: [] }], next_cursor: '' }),
  })
  const workbench = (await loadWorkbenchComponent()).setup({}, { expose() {} })

  const opening = workbench.openTask({ id: 'task-a', labels: [] })
  detailA.resolve({ task: { id: 'task-a', labels: [] } })
  await opening
  assert.equal(workbench.activeTaskId.value, 'task-a')
  assert.equal(workbench.detail.value?.task.id, 'task-a')

  workbench.filters.datasetId = 'dataset-without-runs'
  workbench.applyFilters()
  assert.equal(workbench.activeTaskId.value, '')
  assert.equal(workbench.detail.value, null)
  assert.deepEqual(workbench.questions.value, [])

  // A detail response that was already in flight when the filter applied
  // must not restore the stale detail afterwards.
  const staleDetail = deferred<{ task: EvaluationTaskStub }>()
  installWorkbenchApi({
    getDetail: () => staleDetail.promise,
    listTasks: async () => ({ items: [], next_cursor: '' }),
  })
  const workbench2 = (await loadWorkbenchComponent()).setup({}, { expose() {} })
  const opening2 = workbench2.openTask({ id: 'task-x', labels: [] })
  assert.equal(workbench2.detailLoading.value, true)
  workbench2.applyFilters()
  assert.equal(workbench2.detailLoading.value, false)
  staleDetail.resolve({ task: { id: 'task-x', labels: [] } })
  await opening2
  assert.equal(workbench2.activeTaskId.value, '')
  assert.equal(workbench2.detail.value, null)
  assert.equal(workbench2.detailLoading.value, false)
})

test('applying filters releases question loading and rejects a late question page', async () => {
  const pending = deferred<{ items: Array<{ qid: string }>; next_cursor: string }>()
  installWorkbenchApi({ listQuestions: () => pending.promise })
  const workbench = (await loadWorkbenchComponent()).setup({}, { expose() {} })
  await workbench.openTask({ id: 'task-a', labels: [] })
  assert.equal(workbench.questionLoading.value, true)

  workbench.applyFilters()
  assert.equal(workbench.questionLoading.value, false)
  pending.resolve({ items: [{ qid: 'stale-question' }], next_cursor: 'stale-cursor' })
  await flushMicrotasks()

  assert.equal(workbench.activeTaskId.value, '')
  assert.deepEqual(workbench.questions.value, [])
  assert.equal(workbench.questionLoading.value, false)
})
