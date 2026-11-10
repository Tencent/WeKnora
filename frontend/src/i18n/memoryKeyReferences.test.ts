import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { test } from 'node:test'

import { LOCALE_BUNDLES, collectLocaleKeys, type LocaleName } from './localeKeyAudit.ts'

// The repository-wide audit compares locales against each other: it catches a
// key present in en-US but missing in ko-KR. It cannot catch a key that is
// referenced in code and missing from *every* locale, because it starts from
// the keys that already exist.
//
// That gap is not hypothetical — the memory settings shipped a reference to
// `memoryWorkspaceSettings.instructionsLabel` while it existed in no locale,
// and every check stayed green while the UI would have rendered the raw key.
//
// This guards the memory namespaces specifically. There are pre-existing
// violations elsewhere in the app; widening this check means fixing those
// first, which is a separate change.
const GUARDED_PREFIXES = ['memorySettings.', 'memoryWorkspaceSettings.', 'chat.memory']

const STATIC_KEY_PATTERN = /\$?\bt\(\s*['"]([a-zA-Z][\w]*(?:\.[\w]+)+)['"]/g

function collectStaticKeys(dir: string, found = new Set<string>()): Set<string> {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const target = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      collectStaticKeys(target, found)
      continue
    }
    if (!/\.(vue|ts)$/.test(entry.name) || entry.name.endsWith('.test.ts')) continue
    for (const match of fs.readFileSync(target, 'utf8').matchAll(STATIC_KEY_PATTERN)) {
      found.add(match[1])
    }
  }
  return found
}

test('memory i18n keys referenced in code exist in every locale', () => {
  const referenced = [...collectStaticKeys(path.join(import.meta.dirname, '..'))].filter((key) =>
    GUARDED_PREFIXES.some((prefix) => key.startsWith(prefix)),
  )
  assert.ok(referenced.length > 0, 'no memory i18n keys were found; has the scan pattern drifted?')

  const failures: string[] = []
  for (const [localeName, bundle] of Object.entries(LOCALE_BUNDLES) as Array<[LocaleName, unknown]>) {
    const keys = collectLocaleKeys(bundle)
    for (const key of referenced) {
      if (!keys.has(key)) failures.push(`${localeName}: missing ${key}`)
    }
  }
  assert.deepEqual(failures, [], failures.slice(0, 20).join('\n'))
})

// Kind and origin labels are looked up dynamically, so the scan above cannot
// see them and a renamed kind would silently render a raw key in the chat.
test('dynamic memory kind and origin labels exist in every locale', () => {
  const dynamic = [
    ...['profile', 'preference', 'fact', 'task'].map((kind) => `memorySettings.kinds.${kind}`),
    ...['explicit', 'extracted', 'manual'].map((origin) => `memorySettings.origins.${origin}`),
  ]
  const failures: string[] = []
  for (const [localeName, bundle] of Object.entries(LOCALE_BUNDLES) as Array<[LocaleName, unknown]>) {
    const keys = collectLocaleKeys(bundle)
    for (const key of dynamic) {
      if (!keys.has(key)) failures.push(`${localeName}: missing ${key}`)
    }
  }
  assert.deepEqual(failures, [], failures.join('\n'))
})
