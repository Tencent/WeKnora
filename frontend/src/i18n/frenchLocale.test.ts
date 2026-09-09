import assert from 'node:assert/strict'
import { test } from 'node:test'
import dayjs from 'dayjs'
import { createI18n } from 'vue-i18n'

import enUS from './locales/en-US.ts'
import frFR from './locales/fr-FR.ts'
import { EMBED_MESSAGES } from './embed.ts'
import { collectLocaleMessages, diffLocaleKeys, collectLocaleKeys, findAllLocaleMessageCompileErrors } from './localeKeyAudit.ts'
import frTDesign from './tdesign/fr-FR.ts'

/** Keep named/list parameters and escaped literals intact, including {'{{'}. */
function interpolationTokens(message: string): string[] {
  return (message.match(/\{(?:'[^']*'|[A-Za-z_]\w*|\d+)\}/g) || []).sort()
}

test('the interpolation check detects dropped/renamed parameters and handles escaped braces', () => {
  assert.deepEqual(interpolationTokens("{'{{'}query{'}}'}: {name}, {count}, {0}"), ["{'{{'}", "{'}}'}", '{0}', '{count}', '{name}'].sort())
  assert.notDeepEqual(interpolationTokens('{count} files'), interpolationTokens('{total} fichiers'))
  assert.notDeepEqual(interpolationTokens('{count} files'), interpolationTokens('Des fichiers'))
})

for (const [surface, english, french] of [
  ['main', enUS, frFR],
  ['embed', EMBED_MESSAGES['en-US'], EMBED_MESSAGES['fr-FR']],
] as const) {
  test(`French ${surface} messages retain every interpolation parameter and escaped literal`, () => {
    const source = new Map(collectLocaleMessages(english).map(({ path, value }) => [path, value]))
    const failures: string[] = []
    for (const { path, value } of collectLocaleMessages(french)) {
      if (!source.has(path)) continue
      const expected = interpolationTokens(source.get(path)!)
      const actual = interpolationTokens(value)
      if (JSON.stringify(expected) !== JSON.stringify(actual)) {
        failures.push(`${path}: ${expected.join(', ')} -> ${actual.join(', ')}`)
      }
    }
    assert.deepEqual(failures, [], failures.join('\n'))
  })
}

test('every runtime embed bundle has the English keys and compiles', () => {
  const reference = collectLocaleKeys(EMBED_MESSAGES['en-US'])
  for (const [locale, bundle] of Object.entries(EMBED_MESSAGES)) {
    assert.deepEqual(diffLocaleKeys(reference, collectLocaleKeys(bundle)), { missing: [], extra: [] }, locale)
  }
  assert.deepEqual(findAllLocaleMessageCompileErrors(EMBED_MESSAGES), [])
})

test('French messages render names and literal prompt parameters', () => {
  const i18n = createI18n({ legacy: false, locale: 'fr-FR', messages: { 'fr-FR': frFR } })
  assert.equal(i18n.global.t('auth.oidcLoginWithProvider', { provider: 'Example' }), 'Se connecter avec Example')
  assert.match(i18n.global.t('agent.editor.queryMissingInFallback'), /\{\{query\}\}/)
  assert.match(i18n.global.t('agent.editor.queryMissingInRewrite'), /\{\{query\}\}/)
})

test('French TDesign dates start on Monday and format day/month/year', () => {
  assert.equal(frTDesign.datePicker?.firstDayOfWeek, 1)
  assert.equal(frTDesign.datePicker?.dayjsLocale, 'fr')
  assert.equal(dayjs('2026-07-14').locale('fr').format(frTDesign.datePicker?.format), '14/07/2026')
  assert.equal(dayjs('2026-07-14').locale('fr').format('MMMM'), 'juillet')
  assert.equal(frTDesign.dialog?.cancel, 'Annuler')
  assert.match(frTDesign.form?.errorMessage?.required as string, /\$\{name\}/)
})
