import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

// A Vue API call written with a TypeScript generic — e.g. ref<string>('')
// — is fine inside a lang="ts" script block, but silently corrupts a plain-JS
// <script> block: esbuild's JS parser reads the generic as comparison
// operators (ref < string > ''), so the build succeeds and the damage only
// surfaces at runtime, where the mis-evaluated expression throws and the
// component fails to mount (observed as: chat answers stream, persist, and
// render completely blank). vue-tsc does not flag plain-JS blocks strictly
// enough to catch this either, so nothing between the editor and the browser
// console reports it. This test pins the exact pattern.

const GENERIC_CALL_RE = /\b(?:ref|shallowRef|customRef|computed|reactive|inject|provide|defineProps|defineEmits|defineExpose|withDefaults)\s*<\s*[A-Za-z_(\[]/g

function findTsGenericCalls(code) {
  return [...code.matchAll(GENERIC_CALL_RE)].map((m) => m[0])
}

function* walkVueFiles(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === 'node_modules' || entry.name === 'dist') continue
    const full = join(dir, entry.name)
    if (entry.isDirectory()) yield* walkVueFiles(full)
    else if (entry.name.endsWith('.vue')) yield full
  }
}

test('plain-JS script blocks contain no TypeScript generic calls', () => {
  const findings = []
  for (const file of walkVueFiles(dirname(fileURLToPath(import.meta.url)))) {
    const source = readFileSync(file, 'utf8')
    for (const m of source.matchAll(/<script([^>]*)>([\s\S]*?)<\/script>/g)) {
      if (/(^|\s)lang\s*=\s*["']?ts["']?/.test(m[1])) continue
      const bodyOffset = m.index + m[0].indexOf(m[2])
      for (const hit of m[2].matchAll(GENERIC_CALL_RE)) {
        const line = source.slice(0, bodyOffset + hit.index).split('\n').length
        findings.push(`${file}:${line}: ${hit[0]}`)
      }
    }
  }
  assert.deepEqual(
    findings,
    [],
    `TypeScript generic calls in plain-JS script blocks (esbuild misparses them as comparisons — build passes, runtime fails):\n${findings.join('\n')}`,
  )
})

test('detector catches the regression shape and ignores ordinary JS', () => {
  assert.equal(findTsGenericCalls("const myRating = ref<string>('')").length, 1)
  assert.equal(findTsGenericCalls('const visible = ref(false)').length, 0)
  assert.equal(findTsGenericCalls('const total = a < b && c > d').length, 0)
  assert.equal(findTsGenericCalls('// treat the payload as any[] when debugging').length, 0)
  assert.equal(findTsGenericCalls('const list = computed(() => items.value)').length, 0)
})
