import assert from 'node:assert/strict'
import { Buffer } from 'node:buffer'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

import { build, type Plugin } from 'esbuild'

interface EvaluationModule {
  EVALUATION_EXPORT_TIMEOUT_MS: number
  createEvaluationRequestGate: () => {
    begin: (key: string) => { key: string; generation: number }
    isCurrent: (token: { key: string; generation: number }) => boolean
    invalidate: () => void
  }
  downloadEvaluationArtifact: (
    taskId: string,
    format: 'json' | 'csv',
    options?: { signal?: AbortSignal; timeoutMs?: number },
  ) => Promise<void>
}

interface DownloadCall {
  config: { signal?: AbortSignal; timeout: number }
  url: string
}

const requestMock: Plugin = {
  name: 'evaluation-request-mock',
  setup(builder) {
    builder.onResolve({ filter: /^@\/utils\/request$/ }, args => ({
      path: args.path,
      namespace: 'evaluation-request-test',
    }))
    builder.onLoad({ filter: /.*/, namespace: 'evaluation-request-test' }, () => ({
      contents: `
        export const get = async () => { throw new Error('unexpected get') }
        export const post = async () => { throw new Error('unexpected post') }
        export const put = async () => { throw new Error('unexpected put') }
        export const getDown = async (url, config) => {
          globalThis.__evaluationDownloadCalls.push({ url, config })
          if (config.signal?.aborted) {
            const error = new Error('download canceled')
            error.name = 'AbortError'
            throw error
          }
          return new Blob(['evaluation'])
        }
      `,
      loader: 'js',
    }))
  },
}

let evaluationModulePromise: Promise<EvaluationModule> | undefined

async function loadEvaluationModule(): Promise<EvaluationModule> {
  if (evaluationModulePromise) return evaluationModulePromise
  evaluationModulePromise = (async () => {
    const entry = fileURLToPath(new URL('./index.ts', import.meta.url))
    const result = await build({
      entryPoints: [entry],
      bundle: true,
      format: 'esm',
      logLevel: 'silent',
      platform: 'node',
      plugins: [requestMock],
      target: 'node22',
      write: false,
    })
    const url = `data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`
    return import(url) as Promise<EvaluationModule>
  })()
  return evaluationModulePromise
}

test('request gate accepts only the newest key and generation', async () => {
  const { createEvaluationRequestGate } = await loadEvaluationModule()
  const gate = createEvaluationRequestGate()
  const taskA = gate.begin('task-a')
  const taskB = gate.begin('task-b')

  assert.equal(gate.isCurrent(taskA), false)
  assert.equal(gate.isCurrent(taskB), true)

  const taskBAgain = gate.begin('task-b')
  assert.equal(gate.isCurrent(taskB), false, 'same task id still requires the newest generation')
  assert.equal(gate.isCurrent(taskBAgain), true)
  gate.invalidate()
  assert.equal(gate.isCurrent(taskBAgain), false)
})

test('evaluation export uses its long timeout and forwards the AbortSignal', async () => {
  const {
    EVALUATION_EXPORT_TIMEOUT_MS,
    downloadEvaluationArtifact,
  } = await loadEvaluationModule()
  const calls: DownloadCall[] = []
  ;(globalThis as any).__evaluationDownloadCalls = calls

  let clickedDownload = ''
  const originalDocument = Object.getOwnPropertyDescriptor(globalThis, 'document')
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: {
      createElement: () => ({
        click() {
          clickedDownload = this.download
        },
        download: '',
        href: '',
      }),
    },
  })

  try {
    const controller = new AbortController()
    await downloadEvaluationArtifact('tenant/task', 'csv', { signal: controller.signal })

    assert.equal(calls.length, 1)
    assert.equal(calls[0].url, '/api/v1/evaluation/tasks/tenant%2Ftask/export?format=csv')
    assert.equal(calls[0].config.signal, controller.signal)
    assert.equal(calls[0].config.timeout, EVALUATION_EXPORT_TIMEOUT_MS)
    assert.ok(EVALUATION_EXPORT_TIMEOUT_MS > 30_000)
    assert.equal(clickedDownload, 'evaluation-tenant_task.csv')
  } finally {
    if (originalDocument) Object.defineProperty(globalThis, 'document', originalDocument)
    else Reflect.deleteProperty(globalThis, 'document')
  }
})

test('evaluation export supports an independent timeout and aborts before creating a download', async () => {
  const { downloadEvaluationArtifact } = await loadEvaluationModule()
  const calls: DownloadCall[] = []
  ;(globalThis as any).__evaluationDownloadCalls = calls
  const controller = new AbortController()
  controller.abort()

  await assert.rejects(
    downloadEvaluationArtifact('task-a', 'json', {
      signal: controller.signal,
      timeoutMs: 120_000,
    }),
    (error: Error) => error.name === 'AbortError',
  )
  assert.equal(calls[0].config.signal, controller.signal)
  assert.equal(calls[0].config.timeout, 120_000)
})
