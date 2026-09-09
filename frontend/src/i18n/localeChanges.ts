import { execFileSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { collectLocaleMessages } from './localeKeyAudit.ts'
import enUS from './locales/en-US.ts'

/** Also detect wording changes under existing keys, which key-parity audits cannot catch. */
export function compareLocaleMessages(before: unknown, after: unknown) {
  const oldMessages = new Map(collectLocaleMessages(before).map(({ path, value }) => [path, value]))
  const newMessages = new Map(collectLocaleMessages(after).map(({ path, value }) => [path, value]))
  return {
    added: [...newMessages.keys()].filter(key => !oldMessages.has(key)).sort(),
    removed: [...oldMessages.keys()].filter(key => !newMessages.has(key)).sort(),
    changed: [...newMessages.keys()].filter(key => oldMessages.has(key) && oldMessages.get(key) !== newMessages.get(key)).sort(),
  }
}

async function main() {
  const ref = process.argv[2]
  if (!ref || process.argv.length !== 3) throw new Error('Usage: npm run review-i18n-changes -- <previous-upstream-ref>')
  const cwd = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
  const sha = execFileSync('git', ['rev-parse', '--verify', '--end-of-options', `${ref}^{commit}`], { cwd, encoding: 'utf8' }).trim()
  const source = execFileSync('git', ['show', `${sha}:frontend/src/i18n/locales/en-US.ts`], { cwd, encoding: 'utf8' })
  const dir = mkdtempSync(join(tmpdir(), 'weknora-i18n-review-'))
  try {
    const path = join(dir, 'en-US.ts')
    writeFileSync(path, source, 'utf8')
    const previous = await import(pathToFileURL(path).href)
    const changes = compareLocaleMessages(previous.default, enUS)
    console.log(JSON.stringify({ since: sha, ...changes }, null, 2))
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(error instanceof Error ? error.message : String(error)); process.exitCode = 1 })
}
