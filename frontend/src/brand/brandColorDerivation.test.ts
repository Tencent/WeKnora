import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import test from 'node:test'
import { brandCssVars, buildLoginGradient, deriveActionColor } from './colors.ts'

/**
 * H3 品牌色联动（AC-1 / AC-4 / C5）：
 * - primaryColor 一处即可带动交互色 actionColor；
 * - 登录渐变不再写死天蓝，改为按主色生成并写入 --customer-login-gradient；
 * - 客户激活菜单图标用内联 SVG + currentColor，不再依赖蓝色图片资源。
 */

const brandCss = readFileSync(new URL('../assets/theme/brand.css', import.meta.url), 'utf8')
const loginVue = readFileSync(new URL('../views/auth/Login.vue', import.meta.url), 'utf8')
const menuVue = readFileSync(new URL('../components/menu.vue', import.meta.url), 'utf8')

test('default primary derives exactly #0284C7 and preserves contrast', () => {
  assert.equal(deriveActionColor('#0EA5E9'), '#0284C7')
  assert.equal(brandCssVars({ primaryColor: '#0EA5E9' }, 'light')['--td-brand-color'], '#0284C7')
  assert.equal(brandCssVars({ primaryColor: '#0EA5E9' }, 'dark')['--td-brand-color'], '#0284C7')
})

test('changing primaryColor changes the derived action color without touching actionColor', () => {
  const vars = brandCssVars({ primaryColor: '#7C3AED' }, 'light')
  assert.equal(vars['--td-brand-color'], deriveActionColor('#7C3AED'))
  assert.notEqual(vars['--td-brand-color'], '#0284C7')
  assert.equal(vars['--brand-main'], '#7C3AED')
})

test('explicit actionColor still overrides the derivation', () => {
  const vars = brandCssVars({ primaryColor: '#7C3AED', actionColor: '#111111' }, 'light')
  assert.equal(vars['--td-brand-color'], '#111111')
})

test('--customer-login-gradient follows primaryColor in both modes', () => {
  const lightDefault = brandCssVars({ primaryColor: '#0EA5E9' }, 'light')['--customer-login-gradient']!
  const lightCustom = brandCssVars({ primaryColor: '#7C3AED' }, 'light')['--customer-login-gradient']!
  const darkCustom = brandCssVars({ primaryColor: '#7C3AED' }, 'dark')['--customer-login-gradient']!
  assert.match(lightDefault, /^linear-gradient\(/)
  assert.ok(lightDefault.includes('#0EA5E9'))
  assert.ok(lightCustom.includes('#7C3AED'))
  assert.notEqual(lightDefault, lightCustom)
  assert.notEqual(lightCustom, darkCustom)
  assert.equal(lightCustom, buildLoginGradient('#7C3AED', 'light'))
})

test('brand.css login background uses the runtime variable with no literal sky-blue', () => {
  const loginSection = brandCss.slice(brandCss.indexOf('/* 登录页背景。'))
  assert.ok(loginSection.length > 0, 'brand.css must keep the customer login background block')
  assert.match(loginSection, /var\(\s*--customer-login-gradient,/)
  assert.match(loginSection, /var\(--td-brand-color-4\)/)
  // 登录渐变的兜底也不能再写死旧的天蓝色值。
  assert.doesNotMatch(loginSection, /#0EA5E9/i)
  assert.doesNotMatch(loginSection, /#38bdf8/i)
})

test('Login.vue no longer hardcodes a gradient of its own', () => {
  assert.doesNotMatch(loginVue, /linear-gradient/i)
  assert.doesNotMatch(loginVue, /#07c05f/i)
  assert.match(loginVue, /背景渐变由 brand.css/)
})

test('customer active menu icon is inline currentColor, and the blue asset is gone', () => {
  assert.equal(
    existsSync(new URL('../assets/img/prefixIcon-blue.svg', import.meta.url)),
    false,
    'prefixIcon-blue.svg must be deleted (H3)',
  )
  assert.doesNotMatch(menuVue, /prefixIcon-blue\.svg/)
  assert.match(menuVue, /prefixIcon === 'customer-active'/)
  assert.match(menuVue, /fill="currentColor"/)
  assert.match(menuVue, /isCustomer\.value \? 'customer-active' : 'prefixIcon-green\.svg'/)
})
