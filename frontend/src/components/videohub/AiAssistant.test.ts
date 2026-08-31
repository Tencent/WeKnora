import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'

const componentSource = readFileSync(resolve(import.meta.dirname, 'AiAssistant.vue'), 'utf8')

test('video assistant suggestion chips trigger chat as plain buttons', () => {
  assert.match(
    componentSource,
    /<button\s+v-for="item in suggestions"[^>]*type="button"[^>]*@click="send\(item\)"/,
  )
})

test('video assistant input unlocks after chat turn finalizes', () => {
  assert.match(componentSource, /finally\s*\{[^}]*isGenerating\.value\s*=\s*false/s)
  assert.match(componentSource, /finally\s*\{[^}]*streamingAssistantId\.value\s*=\s*''/s)
})
