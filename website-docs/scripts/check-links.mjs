// 校验文档之间的相对链接。拆分章节时最容易留下指向已移动内容的死链，
// 而 vitepress build 不会因为死链失败。
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join, relative, dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (name === 'node_modules' || name.startsWith('.')) continue
    const path = join(dir, name)
    if (statSync(path).isDirectory()) walk(path, out)
    else if (name.endsWith('.md')) out.push(path)
  }
  return out
}

const files = walk(root)
const failures = []
let checked = 0

for (const file of files) {
  const lines = readFileSync(file, 'utf-8').split('\n')
  lines.forEach((line, index) => {
    for (const match of line.matchAll(/\[[^\]]*\]\(([^)\s]+)\)/g)) {
      const target = match[1]
      if (/^(https?:|#|mailto:)/.test(target)) continue
      const [path] = target.split('#')
      if (!path || !path.endsWith('.md')) continue
      checked++
      if (!existsSync(resolve(dirname(file), path))) {
        failures.push({ file: relative(root, file), line: index + 1, target })
      }
    }
  })
}

console.log(`检查 ${files.length} 篇文档的 ${checked} 个内部链接，失效 ${failures.length} 个`)
for (const failure of failures) {
  console.log(`  ${failure.file}:${failure.line} -> ${failure.target}`)
}
process.exit(failures.length === 0 ? 0 : 1)
