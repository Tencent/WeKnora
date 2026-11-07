import assert from 'node:assert/strict'
import test from 'node:test'
import type { A2UITheme } from 'a2ui-vue'

import { weknoraTheme } from './a2uiTheme.ts'

// A2UITheme.additionalStyles is optional, and re-checking it at every use site in the tests would be noisy; this function "goes red when missing",
// so it doubles as an assertion: weknoraTheme must always hand back an additionalStyles object
// (provideA2UI stores the theme verbatim, so returning undefined would mean every component reads nothing).
function styles(theme: A2UITheme): Record<string, any> {
  if (!theme.additionalStyles) {
    throw new assert.AssertionError({ message: 'weknoraTheme 没有交回 additionalStyles' })
  }
  return theme.additionalStyles
}

// This base is a **copied literal**: transcribed entry by entry from defaultTheme's
// additionalStyles in the a2ui-vue@0.9.5 build output (dist/index-BwIwZ7ky.js, the segment before Card: { "border-br-9" … }).
// Deliberately not importing the library: importing drags 400 KB into the test runtime, and more importantly the assertions would
// share one symbol with the implementation — the day the library changes the purple gradient to something else, the imported "expected value"
// changes with it, the tests stay green while nothing on screen actually gets less ugly (cf. the recorded lesson "a same-source constant pin is vacuous").
const BASE_THEME = {
  additionalStyles: {
    Button: {
      '--n-35': 'var(--n-100)',
      '--n-10': 'var(--n-0)',
      background: 'linear-gradient(135deg, light-dark(#818cf8, #06b6d4) 0%, light-dark(#a78bfa, #3b82f6) 100%)',
      boxShadow: '0 4px 15px rgba(102, 126, 234, 0.4)',
      padding: '12px 28px',
      textTransform: 'uppercase',
    },
    Text: {
      h1: {
        color: 'transparent',
        background: 'linear-gradient(135deg, light-dark(#818cf8, #06b6d4) 0%, light-dark(#a78bfa, #3b82f6) 100%)',
        '-webkit-background-clip': 'text',
        'background-clip': 'text',
        '-webkit-text-fill-color': 'transparent',
      },
      h2: {
        color: 'transparent',
        '-webkit-text-fill-color': 'transparent',
      },
      h3: {
        color: 'transparent',
        '-webkit-text-fill-color': 'transparent',
      },
      h4: {},
      h5: {},
      body: {},
      caption: {},
    },
    Card: {
      background:
        'radial-gradient(circle at top left, light-dark(transparent, rgba(6, 182, 212, 0.15)), transparent 40%)',
    },
    // This entry never triggers in this repo (the agent only produces Card/Column/Row/Text/Button); it stays here to pin down that
    // "weknoraTheme only blanks the three named groups and passes everything else through untouched".
    TextField: { '--p-0': 'light-dark(var(--n-0), #1e293b)' },
  },
  components: {
    Button: { 'color-bgc-p30': true, 'border-br-12': true, 'typography-w-400': true },
    Card: { 'border-br-9': true, 'layout-p-4': true, 'color-bgc-n100': true },
    Column: { 'layout-g-2': true },
  },
  elements: { checkbox: { root: 'label.a2ui-checkbox' } },
  markdown: { allowedTags: ['b', 'i', 'code'] },
}

test('测试夹具本身确实带着要消灭的那些样式（否则后面的置空断言是空的）', () => {
  // The positive assertion anchor on the same fixture: blanking-style assertions must be paired with one stating
  // "the input really carries that thing", otherwise deleting the whole implementation would still be all green.
  assert.match(String(BASE_THEME.additionalStyles.Button.background), /linear-gradient/)
  assert.equal(BASE_THEME.additionalStyles.Button.textTransform, 'uppercase')
  assert.equal(BASE_THEME.additionalStyles.Text.h1['-webkit-text-fill-color'], 'transparent')
  assert.match(String(BASE_THEME.additionalStyles.Card.background), /radial-gradient/)
})

test('Button / Text / Card 的行内样式被清空', () => {
  const theme = styles(weknoraTheme(BASE_THEME))
  assert.deepEqual(theme.Button, {})
  assert.deepEqual(theme.Card, {})
  // Variant keys kept, values cleared: spell out each variant as a literal here, do not share the implementation's list.
  assert.deepEqual(theme.Text, {
    h1: {},
    h2: {},
    h3: {},
    h4: {},
    h5: {},
    body: {},
    caption: {},
  })
})

test('没点名的组（TextField）与 components / elements / markdown 原样保留', () => {
  const full = weknoraTheme(BASE_THEME)
  const theme = styles(full)
  assert.deepEqual(theme.TextField, { '--p-0': 'light-dark(var(--n-0), #1e293b)' })
  assert.deepEqual(full.components.Button, {
    'color-bgc-p30': true,
    'border-br-12': true,
    'typography-w-400': true,
  })
  assert.deepEqual(full.components.Card, {
    'border-br-9': true,
    'layout-p-4': true,
    'color-bgc-n100': true,
  })
  assert.deepEqual(full.elements, { checkbox: { root: 'label.a2ui-checkbox' } })
  assert.deepEqual(full.markdown, { allowedTags: ['b', 'i', 'code'] })
})

test('库以后多给一档（h6）时也会被清掉，不是靠手写名单', () => {
  const withH6 = {
    ...BASE_THEME,
    additionalStyles: {
      ...BASE_THEME.additionalStyles,
      Text: { ...BASE_THEME.additionalStyles.Text, h6: { color: 'transparent' } },
    },
  }
  const text = styles(weknoraTheme(withH6)).Text
  assert.deepEqual(text.h6, {})
  assert.equal('color' in text.h6, false)
  // Anchor: the original seven variants are all still there in the same fixture (the list has not shrunk).
  assert.deepEqual(Object.keys(text).sort(), ['body', 'caption', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6'])
})

test('覆写不改传入的对象', () => {
  weknoraTheme(BASE_THEME)
  // A theme mutated in place on the way in = the one shared defaultTheme would poison the next surface.
  assert.match(String(BASE_THEME.additionalStyles.Button.background), /linear-gradient/)
  assert.equal(BASE_THEME.additionalStyles.Text.h2['-webkit-text-fill-color'], 'transparent')
  assert.match(String(BASE_THEME.additionalStyles.Card.background), /radial-gradient/)
})

test('additionalStyles 缺席时不炸，也不凭空造出被清空的三组', () => {
  const theme = weknoraTheme({ components: { Button: { 'color-bgc-p30': true } } })
  assert.deepEqual(Object.keys(styles(theme)), [])
  assert.deepEqual(theme.components, { Button: { 'color-bgc-p30': true } })
})
