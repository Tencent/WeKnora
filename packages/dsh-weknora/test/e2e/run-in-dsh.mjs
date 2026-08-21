#!/usr/bin/env node
/**
 * End-to-end check inside a real DeepSeek Harness install: install this package
 * into a throwaway dsh profile with `dsh plugin add`, boot the headless profile
 * against a fake OpenAI-compatible model and a mock WeKnora backend, and assert
 * that the harness agent loop actually called the tools and answered from what
 * they returned.
 *
 * Two scenarios run, because both are documented paths:
 *   A. the bundle's shipped `cordis.patch.yml`, configured only by environment
 *      variables — what a user gets straight after `dsh plugin add`.
 *   B. a profile patch that overrides the row, including a renamed tool prefix,
 *      which also proves configuration reaches the plugin.
 *
 * Usage:
 *   node test/e2e/run-in-dsh.mjs                 # installs dsh into a cache dir
 *   DSH_BIN=/path/to/dsh node test/e2e/run-in-dsh.mjs
 *   DSH_E2E_LOG=/tmp/transcript.log node test/e2e/run-in-dsh.mjs
 *
 * Environment:
 *   DSH_PACKAGE_SPEC  npm spec for the harness (default `@deepseek-ai/dsh@latest`)
 *   DSH_INSTALL_DIR   where to keep that install (default `<tmp>/dsh-e2e-install`)
 *   DSH_E2E_KEEP_HOME keep the throwaway `DSH_HOME` for inspection
 */

import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { startFakeModel } from './fake-model.mjs'
import { startMockWeknora } from '../helpers/mock-weknora.mjs'

const packageDir = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const installDir = process.env.DSH_INSTALL_DIR ?? join(tmpdir(), 'dsh-e2e-install')
const dshSpec = process.env.DSH_PACKAGE_SPEC ?? '@deepseek-ai/dsh@latest'
const API_KEY = 'e2e-api-key'
const QUESTION = 'WeKnora 的默认检索阈值是多少？请查知识库后回答。'
const transcript = []

function log(line) {
  transcript.push(line)
  process.stdout.write(`${line}\n`)
}

/** Run a command to completion, capturing both streams. */
function run(command, args, options = {}) {
  return new Promise((resolveRun, rejectRun) => {
    const child = spawn(command, args, {
      cwd: options.cwd ?? packageDir,
      env: { ...process.env, ...options.env },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    let stdout = ''
    let stderr = ''
    child.stdout.on('data', part => {
      stdout += part
    })
    child.stderr.on('data', part => {
      stderr += part
    })
    child.on('error', rejectRun)
    child.on('close', code => resolveRun({ code, stdout, stderr }))
  })
}

/** Resolve a dsh launcher, installing one from npm when none was supplied. */
async function resolveDsh() {
  if (process.env.DSH_BIN !== undefined) return process.env.DSH_BIN
  const binary = join(installDir, 'node_modules', '.bin', 'dsh')
  if (existsSync(binary)) {
    log(`# reusing the dsh install at ${installDir}`)
    return binary
  }
  log(`# installing ${dshSpec} into ${installDir} (first run only)`)
  await mkdir(installDir, { recursive: true })
  await writeFile(join(installDir, 'package.json'), JSON.stringify({ name: 'dsh-e2e-install', private: true }, null, 2))
  const install = await run('npm', ['install', '--no-audit', '--no-fund', dshSpec], { cwd: installDir })
  if (install.code !== 0) throw new Error(`npm install ${dshSpec} failed:\n${install.stderr}`)
  return binary
}

/** The two rows that make dsh talk to the fake model instead of DeepSeek. */
function modelPatchRows(modelUrl) {
  return [
    {
      id: 'agent-default-model',
      config: { provider: 'weknora-e2e', model: 'weknora-e2e-model' },
    },
    {
      id: 'llm-pi-ai',
      config: {
        providers: {
          'weknora-e2e': {
            displayName: 'WeKnora e2e gateway',
            apiKeyEnv: 'WEKNORA_E2E_MODEL_KEY',
            api: 'openai-completions',
            baseURL: modelUrl,
            models: [{
              id: 'weknora-e2e-model',
              name: 'WeKnora e2e model',
              contextWindow: 65536,
              maxTokens: 4096,
            }],
          },
        },
      },
    },
  ]
}

/**
 * Boot the headless profile once and assert the whole path: the harness offered
 * the tools, the tools reached WeKnora, the retrieved passages reached the
 * model, and the printed answer is grounded in them.
 */
async function scenario(options) {
  const { label, dsh, home, env, patch, prefix, weknora } = options
  const model = await startFakeModel({ searchTool: `${prefix}_search`, readTool: `${prefix}_read_document` })
  const requestsBefore = weknora.requests.length
  try {
    await writeFile(
      join(home, 'profiles', 'headless', 'cordis.patch.yml'),
      JSON.stringify([...patch, ...modelPatchRows(model.url)], null, 2),
    )

    const dump = await run(dsh, ['--profile', 'headless', '--dump-config'], { env })
    assert.equal(dump.code, 0, `--dump-config must succeed:\n${dump.stderr}`)
    assert.match(dump.stdout, /name: ["']?@wxg-prc-cpg\/dsh-weknora/, 'the composed tree must mount the plugin')

    log(`\n=== ${label} ===`)
    log(`$ dsh --profile headless "${QUESTION}"`)
    const boot = await run(dsh, ['--profile', 'headless', QUESTION], { env })
    log(`--- stdout ---\n${boot.stdout.trim()}`)
    if (boot.stderr.trim() !== '') log(`--- stderr ---\n${boot.stderr.trim()}`)
    assert.equal(boot.code, 0, 'the headless run must exit 0')

    const offered = (model.requests.at(0)?.offeredTools ?? []).filter(name => name.startsWith(`${prefix}_`))
    log(`# tools offered to the model: ${offered.join(', ')}`)
    for (const suffix of ['search', 'read_document', 'ask', 'list_knowledge_bases']) {
      assert.ok(offered.includes(`${prefix}_${suffix}`), `${prefix}_${suffix} must appear in the model's tool schemas`)
    }

    const paths = weknora.requests.slice(requestsBefore).map(request => request.path)
    log(`# WeKnora received: ${paths.join(', ')}`)
    assert.ok(paths.includes('/api/v1/knowledge-search'), 'search must reach the retrieval endpoint')
    assert.ok(paths.some(path => path.startsWith('/api/v1/chunks/')), 'read_document must reach the chunk endpoint')
    assert.equal(
      weknora.requests.slice(requestsBefore).every(request => request.headers['x-api-key'] === API_KEY),
      true,
      'every call must carry the configured API key',
    )

    const toolResults = model.requests.flatMap(request => request.toolResults).join('\n')
    assert.match(toolResults, /vector_threshold 是 0\.5/, 'the model must see the retrieved passage verbatim')
    assert.match(toolResults, /knowledge_id: doc-retrieval-pipeline/, 'search hits must carry a usable knowledge_id')
    assert.match(boot.stdout, /0\.5/, 'the printed answer must carry the retrieved threshold')

    log(`# PASS (${label}): ${prefix}_search and ${prefix}_read_document ran inside dsh against WeKnora, `
      + 'and the answer came from the retrieved passages.')
  } finally {
    await model.close()
  }
}

async function main() {
  if (!existsSync(join(packageDir, 'dist', 'index.js'))) {
    throw new Error('build the package first: npm run build')
  }

  const weknora = await startMockWeknora({ apiKey: API_KEY })
  const home = await mkdtemp(join(tmpdir(), 'dsh-weknora-home-'))
  const dsh = await resolveDsh()
  const env = {
    DSH_HOME: home,
    WEKNORA_E2E_MODEL_KEY: 'not-a-real-key',
    WEKNORA_BASE_URL: weknora.url,
    WEKNORA_API_KEY: API_KEY,
    WEKNORA_KNOWLEDGE_BASE_IDS: 'kb-product',
  }

  log(`# mock WeKnora   ${weknora.url}`)
  log(`# DSH_HOME       ${home}`)
  log(`# dsh launcher   ${dsh}`)

  try {
    // Install this package into a fresh profile exactly as a user would.
    const install = await run(dsh, ['plugin', '--profile', 'headless', 'add', packageDir], { env })
    log(`\n$ dsh plugin --profile headless add ${packageDir}\n${install.stdout.trim()}\n${install.stderr.trim()}`)
    assert.equal(install.code, 0, 'dsh plugin add must succeed')

    const manifest = JSON.parse(await readFile(join(home, 'profiles', 'headless', 'package.json'), 'utf8'))
    log(`# profile bundles: ${JSON.stringify(manifest.dsh.profile.bundles)}`)
    assert.ok(
      manifest.dsh.profile.bundles.includes('@wxg-prc-cpg/dsh-weknora'),
      'the bundle must join the profile layer list',
    )

    // A. The shipped bundle patch, configured only by environment variables.
    await scenario({ label: 'A · shipped bundle defaults from env', dsh, home, env, weknora, patch: [], prefix: 'weknora' })

    // B. A profile override, which also renames the tools.
    await scenario({
      label: 'B · profile override with a renamed tool prefix',
      dsh,
      home,
      env,
      weknora,
      prefix: 'kb',
      patch: [{
        id: 'weknora',
        config: {
          baseUrl: weknora.url,
          apiKey: API_KEY,
          knowledgeBaseIds: ['kb-product'],
          toolPrefix: 'kb',
          maxResults: 3,
          maxChunkChars: 400,
        },
      }],
    })

    log('\n# ALL SCENARIOS PASSED')
  } finally {
    await weknora.close()
    const logPath = process.env.DSH_E2E_LOG
    if (logPath !== undefined) await writeFile(logPath, `${transcript.join('\n')}\n`)
    if (process.env.DSH_E2E_KEEP_HOME === undefined) await rm(home, { recursive: true, force: true })
  }
}

await main()
