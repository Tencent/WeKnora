import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { brandCssVars, deriveBrandScale, isValidHex } from './colors.ts'
import { DEFAULT_BRAND } from './defaults.ts'

/**
 * 天蓝主色（AC-4）与静态兜底色阶（brand.css，T02 裁定）必须一致：
 * 改 colors.ts 的派生算法时，brand.css 里的首屏兜底也要同步，否则会闪一帧旧色。
 */

test('isValidHex accepts #RGB / #RRGGBB and rejects everything else', () => {
  for (const value of ['#abc', '#ABC', '#0EA5E9', '  #0ea5e9  ']) {
    assert.equal(isValidHex(value), true, `${value} should be valid`)
  }
  for (const value of ['red', '#12', '#1234', '#12345', '#1234567', '0EA5E9', '', null, 42, {}, undefined]) {
    assert.equal(isValidHex(value), false, `${String(value)} should be invalid`)
  }
})

test('deriveBrandScale returns all ten TDesign steps for both modes', () => {
  for (const mode of ['light', 'dark'] as const) {
    const scale = deriveBrandScale('#0EA5E9', mode)
    for (let index = 1; index <= 10; index += 1) {
      assert.match(scale[`--td-brand-color-${index}`] ?? '', /^#[0-9a-fA-F]{6}(?:[0-9a-fA-F]{2})?$/, `${mode} step ${index}`)
    }
    assert.equal(Object.keys(scale).length, 10)
  }
})

test('deriveBrandScale keeps the configured primary on the signature step', () => {
  assert.equal(deriveBrandScale('#0EA5E9', 'light')['--td-brand-color-4'], '#0EA5E9')
  assert.equal(deriveBrandScale('#0EA5E9', 'dark')['--td-brand-color-6'], '#0EA5E9')
  // 非法主色回落到默认天蓝，不把坏值写进 DOM。
  assert.equal(deriveBrandScale('nope', 'light')['--td-brand-color-4'], DEFAULT_BRAND.primaryColor)
})

test('brandCssVars maps clickable elements to the darker action color in both modes', () => {
  for (const mode of ['light', 'dark'] as const) {
    const vars = brandCssVars(DEFAULT_BRAND, mode)
    assert.equal(vars['--td-brand-color'], DEFAULT_BRAND.actionColor)
  }
  const light = brandCssVars(DEFAULT_BRAND, 'light')
  assert.equal(light['--brand-main'], DEFAULT_BRAND.primaryColor)
  assert.equal(light['--td-brand-color-light'], light['--td-brand-color-1'])
  assert.match(light['--td-brand-color-focus'] ?? '', /color-mix/)
})

test('brandCssVars falls back to defaults for invalid colors', () => {
  const vars = brandCssVars({ primaryColor: 'oops', actionColor: '#12' }, 'light')
  assert.equal(vars['--td-brand-color'], DEFAULT_BRAND.actionColor)
  assert.equal(vars['--td-brand-color-4'], DEFAULT_BRAND.primaryColor)
})

function extractBlock(source: string, selector: string): string {
  const start = source.indexOf(selector)
  assert.notEqual(start, -1, `selector ${selector} should exist in brand.css`)
  const open = source.indexOf('{', start)
  const close = source.indexOf('}', open)
  return source.slice(open + 1, close)
}

function cssVars(block: string, names: string[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const name of names) {
    const match = block.match(new RegExp(`${name}\\s*:\\s*([^;]+);`))
    if (match) out[name] = match[1]!.trim()
  }
  return out
}

const brandCss = readFileSync(new URL('../assets/theme/brand.css', import.meta.url), 'utf8')
const scaleNames = Array.from({ length: 10 }, (_, index) => `--td-brand-color-${index + 1}`)

test('brand.css static light scale matches colors.ts defaults exactly', () => {
  const css = cssVars(extractBlock(brandCss, ':root:root[data-customer="true"] {'), scaleNames)
  assert.deepEqual(css, deriveBrandScale(DEFAULT_BRAND.primaryColor, 'light'))
})

test('brand.css static dark scale matches colors.ts defaults exactly', () => {
  const css = cssVars(
    extractBlock(brandCss, ':root:root[data-customer="true"][theme-mode="dark"] {'),
    scaleNames,
  )
  assert.deepEqual(css, deriveBrandScale(DEFAULT_BRAND.primaryColor, 'dark'))
})

test('brand.css keeps the default action color and softened tokens', () => {
  const light = extractBlock(brandCss, ':root:root[data-customer="true"] {')
  const vars = cssVars(light, [
    '--td-brand-color',
    '--brand-main',
    '--td-component-stroke',
    '--td-radius-default',
    '--app-radius-lg',
  ])
  assert.equal(vars['--td-brand-color'], DEFAULT_BRAND.actionColor)
  assert.equal(vars['--brand-main'], DEFAULT_BRAND.primaryColor)
  assert.equal(vars['--td-component-stroke'], '#e8ecf2')
  assert.equal(vars['--td-radius-default'], '12px')
  assert.equal(vars['--app-radius-lg'], '14px')
})
