import assert from 'node:assert/strict'
import test from 'node:test'

import { createI18n } from 'vue-i18n'

import type { ImageAttrSchema, ImageAttrSpec } from '@/api/knowledge-base'
import enUS from '../i18n/locales/en-US.ts'
import jaJP from '../i18n/locales/ja-JP.ts'
import koKR from '../i18n/locales/ko-KR.ts'
import ruRU from '../i18n/locales/ru-RU.ts'
import zhCN from '../i18n/locales/zh-CN.ts'
import { imageAttrConditionDisplay, imageAttrDisplay, imageAttrKeyBase } from './imageAttrDisplay.ts'

const SPEC: ImageAttrSpec = {
  name: 'contain.text',
  type: 'extent',
  values: [
    { value: 'none', label: 'no text at all', description: 'the image carries no text at all' },
    { value: 'sparse', label: 'a few words', description: 'a logo, a road sign, a single label' },
    { value: 'block', label: 'a block of body text', description: 'a screenshot, a table, a document page' },
  ],
  question: '',
  label: 'Text in the image',
  description: 'How much body text the picture carries.',
}

const PRESENCE: ImageAttrSpec = {
  name: 'contain.data_visual',
  type: 'presence',
  question: '',
  label: 'Data visual',
  description: 'Whether the picture conveys data as a chart.',
  values: [
    { value: 'true', label: 'yes' },
    { value: 'false', label: 'no' },
  ],
}

const SCHEMA: ImageAttrSchema = {
  version: 'attrs/2',
  prompt: 'observe/1',
  attributes: [SPEC, PRESENCE],
  default_actions: {
    ocr: {
      on: [
        { prop: 'contain.text', is: 'block' },
        { prop: 'contain.data_visual', is: 'true' },
      ],
      on_unobserved: true,
    },
  },
}

/** No translations at all: the registry's own wording has to carry the panel. */
const noI18n = { t: (key: string) => key, te: () => false }

test('falls back to the registry wording when nothing is translated', () => {
  const display = imageAttrDisplay(SPEC, noI18n.t, noI18n.te)

  assert.equal(display.name, 'contain.text')
  assert.equal(display.label, 'Text in the image')
  assert.equal(display.description, 'How much body text the picture carries.')
  assert.deepEqual(
    display.values.map((value) => value.value),
    ['none', 'sparse', 'block'],
  )
  assert.equal(display.values[2].label, 'a block of body text')
})

test('prefers the translation overlay, keyed by attribute name', () => {
  // Keys escape the dots in the attribute name (see imageAttrKeyBase).
  const zh: Record<string, string> = {
    'imageAttr.contain_text.label': '图中文字量',
    'imageAttr.contain_text.values.block.label': '成段正文',
    'imageAttr.contain_text.values.block.description': '成段正文 —— 截图、表格或文档页面',
  }
  const display = imageAttrDisplay(SPEC, (key) => zh[key] ?? key, (key) => key in zh)

  assert.equal(display.label, '图中文字量')
  // Untranslated pieces keep the registry wording instead of exposing the key.
  assert.equal(display.description, 'How much body text the picture carries.')
  assert.equal(display.values[0].label, 'no text at all')
  assert.equal(display.values[2].label, '成段正文')
  // The same split applies to a value's longer text.
  assert.equal(display.values[2].description, '成段正文 —— 截图、表格或文档页面')
  assert.equal(display.values[0].description, 'the image carries no text at all')
})

test('never shows a blank label when the registry omits display text', () => {
  const bare: ImageAttrSpec = {
    ...SPEC,
    label: '',
    description: undefined,
    values: [
      { value: 'none', label: '', description: '' },
      { value: 'sparse', label: '', description: '' },
      { value: 'block', label: '', description: '' },
    ],
  }
  const display = imageAttrDisplay(bare, noI18n.t, noI18n.te)

  assert.equal(display.label, 'contain.text')
  assert.equal(display.description, '')
  assert.deepEqual(
    display.values.map((value) => value.label),
    ['none', 'sparse', 'block'],
  )
})

test('a presence attribute without an explicit value list offers true/false', () => {
  const display = imageAttrDisplay(PRESENCE, noI18n.t, noI18n.te)

  assert.deepEqual(
    display.values.map((value) => value.value),
    ['true', 'false'],
  )
  assert.equal(display.values[0].label, 'yes')
})

test('renders a policy condition in words next to its raw pair', () => {
  const zh: Record<string, string> = {
    'imageAttr.contain_text.label': '图中文字量',
    'imageAttr.contain_text.values.block.label': '成段正文',
  }
  const condition = imageAttrConditionDisplay(
    { prop: 'contain.text', is: 'block' },
    SCHEMA,
    (key) => zh[key] ?? key,
    (key) => key in zh,
  )

  assert.equal(condition.label, '图中文字量 = 成段正文')
  assert.equal(condition.raw, 'contain.text = block')
})

test('an unknown attribute or value degrades to its raw form', () => {
  const unknownProp = imageAttrConditionDisplay(
    { prop: 'contain.mystery', is: 'x' },
    SCHEMA,
    noI18n.t,
    noI18n.te,
  )
  assert.equal(unknownProp.label, 'contain.mystery = x')
  assert.equal(unknownProp.raw, 'contain.mystery = x')

  const unknownValue = imageAttrConditionDisplay(
    { prop: 'contain.text', is: 'huge' },
    SCHEMA,
    noI18n.t,
    noI18n.te,
  )
  assert.equal(unknownValue.label, 'Text in the image = huge')
})

// ---------------------------------------------------------------------------
// The real bundles, resolved by the real vue-i18n.
//
// The faked translators above answer a flat key lookup, so they would happily
// "translate" a key the real bundle can never resolve — which is exactly how a
// literal `'contain.text'` message key slipped through once (vue-i18n walks a
// key segment by segment on the dots, so it looked for `imageAttr` → `contain`).
// These tests therefore use createI18n, the same resolver the app runs.
// ---------------------------------------------------------------------------

// Each bundle is wired with a literal locale key, the same shape the app's own
// createI18n call uses, so the messages argument keeps a checkable type.
const MESSAGES_BY_LOCALE = {
  'zh-CN': { 'zh-CN': zhCN },
  'en-US': { 'en-US': enUS },
  'ja-JP': { 'ja-JP': jaJP },
  'ko-KR': { 'ko-KR': koKR },
  'ru-RU': { 'ru-RU': ruRU },
}

type TestLocale = keyof typeof MESSAGES_BY_LOCALE

const LOCALES = Object.keys(MESSAGES_BY_LOCALE) as TestLocale[]

function translator(locale: TestLocale) {
  const i18n = createI18n({
    legacy: false,
    locale,
    fallbackLocale: locale,
    warnHtmlMessage: false,
    messages: MESSAGES_BY_LOCALE[locale],
  })
  const global = i18n.global as unknown as {
    t: (key: string) => string
    te: (key: string) => boolean
  }
  return {
    t: (key: string) => global.t(key),
    te: (key: string) => global.te(key),
  }
}

test('every shipped locale translates every attribute key', () => {
  for (const locale of LOCALES) {
    const { te, t } = translator(locale)

    for (const spec of SCHEMA.attributes) {
      const base = imageAttrKeyBase(spec.name)
      const keys = [
        `${base}.label`,
        `${base}.description`,
        ...imageAttrDisplay(spec, t, te).values.map(
          (value) => `${base}.values.${value.value}.label`,
        ),
      ]
      for (const key of keys) {
        assert.equal(te(key), true, `${locale}: ${key} does not resolve`)
        assert.ok(t(key).length > 0, `${locale}: ${key} is empty`)
      }
    }
  }
})

test('every shipped locale translates every OCR condition key', () => {
  for (const locale of LOCALES) {
    const { te } = translator(locale)

    for (const cond of SCHEMA.default_actions.ocr.on) {
      const base = imageAttrKeyBase(cond.prop)
      assert.equal(te(`${base}.label`), true, `${locale}: ${base}.label does not resolve`)
      assert.equal(
        te(`${base}.values.${cond.is}.label`),
        true,
        `${locale}: ${base}.values.${cond.is}.label does not resolve`,
      )
    }
  }
})

test('the panel actually renders the translated text, not the registry fallback', () => {
  const { t, te } = translator('zh-CN')

  for (const spec of SCHEMA.attributes) {
    const display = imageAttrDisplay(spec, t, te)

    assert.match(display.label, /[\u4e00-\u9fa5]/, `${spec.name}: label is not Chinese`)
    assert.match(display.description, /[\u4e00-\u9fa5]/, `${spec.name}: description is not Chinese`)
    for (const value of display.values) {
      assert.match(
        value.label,
        /[\u4e00-\u9fa5]/,
        `${spec.name}.${value.value}: value is not Chinese`,
      )
    }
  }

  for (const cond of SCHEMA.default_actions.ocr.on) {
    const display = imageAttrConditionDisplay(cond, SCHEMA, t, te)
    assert.match(display.label, /[\u4e00-\u9fa5]/, `${cond.prop}=${cond.is}: condition is not Chinese`)
    // The raw pair stays alongside, so the panel still lines up with the trace.
    assert.equal(display.raw, `${cond.prop} = ${cond.is}`)
  }
})
