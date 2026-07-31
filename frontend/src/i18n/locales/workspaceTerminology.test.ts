import assert from 'node:assert/strict'
import test from 'node:test'

import enUS from './en-US.ts'
import koKR from './ko-KR.ts'
import ruRU from './ru-RU.ts'
import zhCN from './zh-CN.ts'

type LocaleValue = string | Record<string, unknown> | unknown[]

function collectStrings(value: LocaleValue, path = ''): Array<{ path: string; value: string }> {
  if (typeof value === 'string') return [{ path, value }]
  if (Array.isArray(value)) {
    return value.flatMap((item, index) =>
      collectStrings(item as LocaleValue, `${path}[${index}]`),
    )
  }
  if (value && typeof value === 'object') {
    return Object.entries(value).flatMap(([key, item]) =>
      collectStrings(item as LocaleValue, path ? `${path}.${key}` : key),
    )
  }
  return []
}

function withoutTechnicalTenantTokens(value: string): string {
  return value
    .replace(/\{tenant(?:Id)?\}/g, '')
    .replace(/\btenant_id\b/gi, '')
    .replace(/\btenantless\b/gi, '')
    .replace(/\bX-Tenant-ID\b/g, '')
}

const localeChecks = [
  { name: 'zh-CN', locale: zhCN, forbidden: /租户/ },
  { name: 'en-US', locale: enUS, forbidden: /\btenants?\b/i },
  { name: 'ko-KR', locale: koKR, forbidden: /테넌트/ },
  { name: 'ru-RU', locale: ruRU, forbidden: /(?:тенант|арендатор)/i },
]

test('user-facing locale values use workspace terminology', () => {
  for (const check of localeChecks) {
    const legacyValues = collectStrings(check.locale)
      .filter(({ value }) => check.forbidden.test(withoutTechnicalTenantTokens(value)))
      .map(({ path, value }) => `${check.name}:${path}=${value}`)

    assert.deepEqual(
      legacyValues,
      [],
      `${check.name} still contains user-facing tenant terminology`,
    )
  }
})
