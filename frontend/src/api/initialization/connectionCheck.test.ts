import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import test from 'node:test'
import ts from 'typescript'

const filename = fileURLToPath(new URL('./index.ts', import.meta.url))
const compiled = ts.transpileModule(readFileSync(filename, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

for (const modelType of ['VLLM', 'KnowledgeQA', undefined] as const) {
  test(`remote connection timeout: ${modelType ?? 'legacy chat'}`, async () => {
    const requests: Array<{ url: string; data: unknown; config?: { timeout: number } }> = []
    const exports: Record<string, any> = {}
    runInNewContext(compiled, {
      exports,
      require(name: string) {
        if (name === '../../utils/request') return {
          post(url: string, data: unknown, config?: { timeout: number }) {
            requests.push({ url, data, config })
            return Promise.resolve({ data: { available: true } })
          },
        }
        if (name === '../../utils/api-base') return { getApiBaseUrl: () => '' }
        if (name === '@/i18n') return { default: { global: { t: (key: string) => key } } }
        throw new Error(`Unexpected import: ${name}`)
      },
    })

    const payload = { modelName: 'test-model', baseUrl: 'https://example.com/v1', modelType }
    const result = await exports.checkRemoteModel(payload)

    assert.equal(result.available, true)
    assert.equal(requests.length, 1)
    assert.equal(requests[0].url, '/api/v1/initialization/remote/check')
    assert.equal(requests[0].data, payload)
    if (modelType === 'VLLM') {
      assert.equal(requests[0].config?.timeout, 65_000)
    } else {
      assert.equal(requests[0].config, undefined, 'chat requests must retain the default timeout')
    }
  })
}
