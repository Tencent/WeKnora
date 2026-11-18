import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'

import i18n, {
  SUPPORTED_MAIN_LOCALES,
  initI18n,
  isLocaleLoaded,
  loadLocaleMessages,
  normalizeMainLocale,
  setI18nLanguage,
} from './index.ts'
import {
  LOCALE_BUNDLES,
  collectLocaleKeys,
  loadLocaleBundlesFromDisk,
  regeneratePrunedLocaleFiles,
  writePrunedLocaleFiles,
  type LocaleName,
} from './localeKeyAudit.ts'
import { DOMAIN_BY_NAMESPACE, LOCALE_DOMAINS } from './locales/domains.ts'

const I18N_DIR = dirname(fileURLToPath(import.meta.url))

test('i18n entry uses dynamic locale imports without static locale bundling', () => {
  const source = readFileSync(join(I18N_DIR, 'index.ts'), 'utf8')
  assert.equal(
    /^\s*import\s+[\w*{}\s,]+\s+from\s+['"]\.\/locales\//m.test(source),
    false,
    'src/i18n/index.ts must not statically import ./locales/* modules',
  )
  for (const locale of SUPPORTED_MAIN_LOCALES) {
    assert.match(
      source,
      new RegExp(`import\\(['"]\\./locales/${locale}\\.ts['"]\\)`),
      `missing dynamic import for ${locale}`,
    )
  }
})

test('lazy i18n loads active locale on demand and switches languages cleanly', async () => {
  assert.equal(isLocaleLoaded('ja-JP'), false)

  const initial = await initI18n()
  assert.equal(initial, 'zh-CN')
  assert.equal(isLocaleLoaded('zh-CN'), true)
  assert.equal(i18n.global.t('common.close'), '关闭')

  const switched = await setI18nLanguage('en-US')
  assert.equal(switched, 'en-US')
  assert.equal(i18n.global.locale.value, 'en-US')
  assert.equal(isLocaleLoaded('en-US'), true)
  assert.equal(i18n.global.t('common.close'), 'Close')

  // Concurrent loads for the same locale share one promise and resolve cleanly.
  const [koA, koB] = await Promise.all([
    loadLocaleMessages('ko'),
    loadLocaleMessages('ko-KR'),
  ])
  assert.equal(koA, 'ko-KR')
  assert.equal(koB, 'ko-KR')
  await setI18nLanguage('ko-KR')
  assert.equal(i18n.global.t('common.close'), '닫기')

  await setI18nLanguage('zh-CN')
})

test('embed i18n synchronizes shared utility i18n synchronously and merges full active locale', async () => {
  const embedMod = await import('./embed.ts')

  embedMod.applyEmbedLocale('ru-RU')
  assert.equal(i18n.global.locale.value, 'ru-RU')
  // Seeded EMBED_MESSAGES are available immediately before awaiting dynamic chunk load.
  assert.equal(
    i18n.global.t('embedPublish.copyCode'),
    embedMod.EMBED_MESSAGES['ru-RU'].embedPublish.copyCode,
  )

  await loadLocaleMessages('ru-RU')
  // Shared markdown/mermaid/tool/error utilities read from @/i18n on /embed/ too,
  // and full native locale bundles upgrade English-fallback embed seeds (ko-KR / ru-RU).
  assert.notEqual(i18n.global.t('mermaid.diagram'), 'mermaid.diagram')
  assert.notEqual(i18n.global.t('tools.searchKnowledge'), 'tools.searchKnowledge')
  assert.equal(
    i18n.global.t('embedPublish.copyCode'),
    (LOCALE_BUNDLES['ru-RU'].embedPublish as Record<string, string>).copyCode,
  )
  assert.equal(i18n.global.t('embedPublish.copyCode'), 'Копировать код')

  embedMod.applyEmbedLocale('ko-KR')
  await loadLocaleMessages('ko-KR')
  assert.equal(i18n.global.t('embedPublish.copyCode'), '코드 복사')

  await setI18nLanguage('zh-CN')
})

test('normalizeMainLocale resolves BCP-47 tags and Traditional Chinese variants', () => {
  assert.equal(normalizeMainLocale('zh-CN'), 'zh-CN')
  assert.equal(normalizeMainLocale('zh'), 'zh-CN')
  assert.equal(normalizeMainLocale('zh-TW'), 'zh-TW')
  assert.equal(normalizeMainLocale('zh-HK'), 'zh-TW')
  assert.equal(normalizeMainLocale('zh-Hant-TW'), 'zh-TW')
  assert.equal(normalizeMainLocale('en-GB'), 'en-US')
  assert.equal(normalizeMainLocale('ja'), 'ja-JP')
  assert.equal(normalizeMainLocale('ko'), 'ko-KR')
  assert.equal(normalizeMainLocale('ru'), 'ru-RU')
  assert.equal(normalizeMainLocale('unknown-tag'), 'zh-CN')
  assert.equal(normalizeMainLocale(''), 'zh-CN')
})

test('domain-split locale layout has disjoint namespaces and round-trips losslessly', async () => {
  const localesDir = join(I18N_DIR, 'locales')

  const unmapped = Object.keys(LOCALE_BUNDLES['en-US']).filter(
    (ns) => !(ns in DOMAIN_BY_NAMESPACE),
  )
  assert.deepEqual(unmapped, [], `unmapped top-level namespaces: ${unmapped.join(', ')}`)

  for (const locale of SUPPORTED_MAIN_LOCALES) {
    const seen = new Set<string>()
    for (const domain of LOCALE_DOMAINS) {
      const domainPath = join(localesDir, locale, `${domain}.ts`)
      assert.ok(existsSync(domainPath), `missing ${locale}/${domain}.ts`)
      const mod = await import(`./locales/${locale}/${domain}.ts`)
      for (const ns of Object.keys(mod.default as Record<string, unknown>)) {
        assert.equal(seen.has(ns), false, `duplicate namespace ${ns} in ${locale}/${domain}.ts`)
        seen.add(ns)
      }
    }
  }

  const tmpDir = mkdtempSync(join(tmpdir(), 'weknora-i18n-roundtrip-'))
  try {
    writePrunedLocaleFiles(
      LOCALE_BUNDLES as unknown as Record<LocaleName, Record<string, unknown>>,
      tmpDir,
    )
    const reloaded = await loadLocaleBundlesFromDisk(tmpDir)
    for (const [locale, expected] of Object.entries(LOCALE_BUNDLES)) {
      assert.deepEqual(reloaded[locale as LocaleName], expected, `${locale} round-trip mismatch`)
    }

    const prunedResult = await regeneratePrunedLocaleFiles(tmpDir)
    assert.equal(prunedResult.before, collectLocaleKeys(LOCALE_BUNDLES['en-US']).size)
    assert.ok(prunedResult.after > 0 && prunedResult.after <= prunedResult.before)
  } finally {
    rmSync(tmpDir, { recursive: true, force: true })
  }
})
