// 用 mermaid 自带的解析器校验文档里所有 ```mermaid 代码块。
// 站点构建不会检查图表语法——语法错的图只在浏览器里报错，
// 所以这里在 CI/本地提前拦住。
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { JSDOM } from 'jsdom'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')

// mermaid 是浏览器库，parse() 需要 DOM 才能初始化。
const dom = new JSDOM('<!DOCTYPE html><body></body>', { pretendToBeVisual: true })
globalThis.window = dom.window
globalThis.document = dom.window.document
// Node 22 的 globalThis.navigator 只有 getter，只能用 defineProperty 覆盖。
Object.defineProperty(globalThis, 'navigator', {
  value: dom.window.navigator,
  configurable: true,
})

const { default: mermaid } = await import('mermaid')
mermaid.initialize({ startOnLoad: false, securityLevel: 'loose' })

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const path = join(dir, name)
    if (statSync(path).isDirectory()) walk(path, out)
    else if (name.endsWith('.md')) out.push(path)
  }
  return out
}

/** 取出每个 mermaid 块及其起始行号，行号用于定位报错。 */
function blocksOf(text) {
  const lines = text.split('\n')
  const blocks = []
  let start = -1
  let buffer = []
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (start === -1 && /^\s*```mermaid\s*$/.test(line)) {
      start = i + 1
      buffer = []
    } else if (start !== -1 && /^\s*```\s*$/.test(line)) {
      blocks.push({ line: start + 1, code: buffer.join('\n') })
      start = -1
    } else if (start !== -1) {
      buffer.push(line)
    }
  }
  return blocks
}

let total = 0
const failures = []

for (const file of walk(root)) {
  for (const block of blocksOf(readFileSync(file, 'utf-8'))) {
    total++
    try {
      await mermaid.parse(block.code)
    } catch (error) {
      failures.push({
        file: relative(root, file),
        line: block.line,
        message: String(error?.message ?? error).split('\n').slice(0, 6).join('\n'),
      })
    }
  }
}

console.log(`检查 ${total} 个 mermaid 图，失败 ${failures.length} 个`)
for (const failure of failures) {
  console.log(`\n--- ${failure.file}:${failure.line}\n${failure.message}`)
}
process.exit(failures.length === 0 ? 0 : 1)
