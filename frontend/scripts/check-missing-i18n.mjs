/**
 * Post-merge i18n health check.
 *
 * Usage:
 *   node frontend/scripts/check-missing-i18n.mjs
 *   node frontend/scripts/check-missing-i18n.mjs --locale=zh-CN,en-US
 *
 * Exit 1 when any listed locale is missing static t()/ $t() keys.
 * Primary product locales are zh-CN + en-US; others are reported as warnings
 * unless passed via --locale.
 */
import fs from 'fs'
import path from 'path'
import { createRequire } from 'module'
import { fileURLToPath } from 'url'

const require = createRequire(import.meta.url)
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')

function parseLocales() {
  const arg = process.argv.find((item) => item.startsWith('--locale='))
  if (!arg) return ['zh-CN', 'en-US']
  return arg
    .slice('--locale='.length)
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function loadLocale(locale) {
  const filePath = path.join(root, 'frontend/src/i18n/locales', `${locale}.ts`)
  if (!fs.existsSync(filePath)) return null
  const tmp = path.join(root, `.tmp-check-${locale}.cjs`)
  fs.writeFileSync(
    tmp,
    fs.readFileSync(filePath, 'utf8').replace(/^export default\s+/, 'module.exports = '),
  )
  try {
    const abs = path.resolve(tmp)
    delete require.cache[abs]
    return require(abs)
  } finally {
    try {
      fs.unlinkSync(tmp)
    } catch {
      /* ignore */
    }
  }
}

function isLeafMissing(obj, dotted) {
  const parts = dotted.split('.')
  let current = obj
  for (const part of parts) {
    if (current == null || typeof current !== 'object' || !(part in current)) {
      return true
    }
    current = current[part]
  }
  return typeof current !== 'string' && typeof current !== 'number'
}

function collectUsedKeys() {
  const keyRe = /(?:\$t|\bt|i18n\.global\.t)\(\s*['"`]([a-zA-Z][\w.]*)['"`]/g
  const keys = new Set()
  function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const fullPath = path.join(dir, entry.name)
      if (entry.isDirectory()) {
        if (entry.name === 'node_modules' || entry.name === 'dist') continue
        walk(fullPath)
        continue
      }
      if (!/\.(vue|ts|js)$/.test(entry.name)) continue
      const text = fs.readFileSync(fullPath, 'utf8')
      let match
      while ((match = keyRe.exec(text))) keys.add(match[1])
    }
  }
  walk(path.join(root, 'frontend/src'))
  return keys
}

const locales = parseLocales()
const usedKeys = collectUsedKeys()
let failed = false

for (const locale of locales) {
  const messages = loadLocale(locale)
  if (!messages) {
    console.error(`[FAIL] locale file missing: ${locale}`)
    failed = true
    continue
  }
  const missing = [...usedKeys]
    .filter((key) => !key.endsWith('.') && isLeafMissing(messages, key))
    .sort()
  if (missing.length) {
    failed = true
    console.error(`[FAIL] ${locale}: ${missing.length} missing keys`)
    for (const key of missing.slice(0, 50)) console.error(`  - ${key}`)
    if (missing.length > 50) {
      console.error(`  … and ${missing.length - 50} more`)
    }
  } else {
    console.log(`[OK] ${locale}: 0 missing (checked ${usedKeys.size} static keys)`)
  }
}

// Soft report for other shipped locales
for (const locale of ['ja-JP', 'ko-KR', 'ru-RU']) {
  if (locales.includes(locale)) continue
  const messages = loadLocale(locale)
  if (!messages) continue
  const missing = [...usedKeys]
    .filter((key) => !key.endsWith('.') && isLeafMissing(messages, key))
    .sort()
  if (missing.length) {
    console.warn(
      `[WARN] ${locale}: ${missing.length} missing (falls back to default locale; sync when touching i18n)`,
    )
  }
}

process.exit(failed ? 1 : 0)
