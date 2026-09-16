import fs from 'fs'
import path from 'path'
import { execSync } from 'child_process'
import { createRequire } from 'module'
import { fileURLToPath } from 'url'

const require = createRequire(import.meta.url)
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
const zhPath = path.join(root, 'frontend/src/i18n/locales/zh-CN.ts')
const enPath = path.join(root, 'frontend/src/i18n/locales/en-US.ts')

function loadLocale(source, label) {
  const tmp = path.join(root, `.tmp-i18n-${label}.cjs`)
  fs.writeFileSync(tmp, source.replace(/^export default\s+/, 'module.exports = '))
  try {
    const abs = path.resolve(tmp)
    delete require.cache[abs]
    return require(abs)
  } finally {
    try {
      fs.unlinkSync(tmp)
    } catch {
      /* ignore */
    }
  }
}

function loadFromGit(ref) {
  const source = execSync(`git show ${ref}:frontend/src/i18n/locales/zh-CN.ts`, {
    cwd: root,
    encoding: 'utf8',
    maxBuffer: 50 * 1024 * 1024,
  })
  return loadLocale(source, ref.replace(/[^a-zA-Z0-9]/g, ''))
}

function deepSet(obj, dotted, value) {
  const parts = dotted.split('.')
  let current = obj
  for (let index = 0; index < parts.length - 1; index += 1) {
    const part = parts[index]
    if (current[part] == null || typeof current[part] !== 'object') {
      current[part] = {}
    }
    current = current[part]
  }
  current[parts[parts.length - 1]] = value
}

function getPath(obj, dotted) {
  return dotted.split('.').reduce((current, part) => {
    if (current && typeof current === 'object') return current[part]
    return undefined
  }, obj)
}

function isLeafMissing(obj, dotted) {
  const value = getPath(obj, dotted)
  return typeof value !== 'string' && typeof value !== 'number'
}

function collectUsedKeys() {
  const keyRe = /(?:\$t|\bt)\(\s*['"`]([a-zA-Z][\w.]*)['"`]/g
  const keys = new Set()
  function walk(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const fullPath = path.join(dir, entry.name)
      if (entry.isDirectory()) {
        if (entry.name === 'node_modules' || entry.name === 'dist') continue
        walk(fullPath)
        continue
      }
      if (!/\.(vue|ts|js)$/.test(entry.name)) continue
      const text = fs.readFileSync(fullPath, 'utf8')
      let match
      while ((match = keyRe.exec(text))) keys.add(match[1])
    }
  }
  walk(path.join(root, 'frontend/src'))
  return keys
}

function serialize(value, indent = 0) {
  const pad = '  '.repeat(indent)
  const nestedPad = '  '.repeat(indent + 1)
  if (typeof value === 'string') {
    const escaped = value
      .replace(/\\/g, '\\\\')
      .replace(/'/g, "\\'")
      .replace(/\n/g, '\\n')
    return `'${escaped}'`
  }
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  if (value === null) return 'null'
  if (Array.isArray(value)) {
    if (!value.length) return '[]'
    return `[\n${value
      .map((item) => `${nestedPad}${serialize(item, indent + 1)}`)
      .join(',\n')}\n${pad}]`
  }
  if (typeof value === 'object') {
    const keys = Object.keys(value)
    if (!keys.length) return '{}'
    const lines = keys.map((key) => {
      const safeKey = /^[a-zA-Z_$][\w$]*$/.test(key) ? key : JSON.stringify(key)
      return `${nestedPad}${safeKey}: ${serialize(value[key], indent + 1)}`
    })
    return `{\n${lines.join(',\n')},\n${pad}}`
  }
  return 'undefined'
}

const refs = [
  'a3c7c6bb',
  '6eeaf0e5',
  'ec4bc8b9',
  '9d56b81a',
  '8354ffd8',
  '143ef262',
]
const history = {}
for (const ref of refs) {
  history[ref] = loadFromGit(ref)
  console.log('loaded', ref)
}

const zh = loadLocale(fs.readFileSync(zhPath, 'utf8'), 'head')
const usedKeys = collectUsedKeys()
const missing = [...usedKeys]
  .filter((key) => !key.endsWith('.') && isLeafMissing(zh, key))
  .sort()

const fallbacks = {
  'chat.editorOpened': '已在编辑器中打开',
  'chat.emptyContentWarning': '内容为空',
  'chat.imageReadFailed': '图片读取失败',
  'datasource.resumeFailed': '恢复数据源失败',
  'error.tenant.createApiKeyFailed': '创建 API Key 失败',
  'error.tenant.createFailed': '创建空间失败',
  'error.tenant.deleteApiKeyFailed': '删除 API Key 失败',
  'error.tenant.listApiKeysFailed': '获取 API Key 列表失败',
  'file.downloadFailed': '下载失败',
  'input.fileUpload.label': '上传文件',
  'input.fileUpload.tooLarge': '文件过大',
  'input.fileUpload.tooMany': '文件数量过多',
  'input.fileUpload.tooltip': '上传附件',
  'input.imageUpload.label': '上传图片',
  'input.imageUpload.tooltip': '上传图片',
  'input.webSearch.label': '联网搜索',
  'knowledge.untitledDocument': '未命名文档',
  'knowledgeBase.createSessionError': '创建会话出错',
  'knowledgeBase.createSessionFailed': '创建会话失败',
  'knowledgeBase.moreOptions': '更多选项',
  'knowledgeBase.selectKnowledgeBase': '选择知识库',
  'root.kb.created': '知识库已创建',
  'settings.parser.checking': '检测中…',
}

let applied = 0
for (const key of missing) {
  let value
  for (const ref of refs) {
    const candidate = getPath(history[ref], key)
    if (typeof candidate === 'string' || typeof candidate === 'number') {
      value = candidate
      break
    }
  }
  if (value == null && fallbacks[key]) value = fallbacks[key]
  if (value == null) continue
  deepSet(zh, key, value)
  applied += 1
}

fs.writeFileSync(zhPath, `export default ${serialize(zh, 0)}\n`)

const verify = loadLocale(fs.readFileSync(zhPath, 'utf8'), 'verify')
const stillMissing = missing.filter((key) => isLeafMissing(verify, key))
console.log(
  JSON.stringify(
    {
      missingBefore: missing.length,
      applied,
      stillMissingCount: stillMissing.length,
      stillMissing,
    },
    null,
    2,
  ),
)

// Also patch en-US for the same recovered Chinese-sourced keys where English
// history lacks them — keep English file from rewriting wholesale; only
// deep-set missing leaves from current en if present in history en.
try {
  const en = loadLocale(fs.readFileSync(enPath, 'utf8'), 'en')
  let enApplied = 0
  for (const key of missing) {
    if (!isLeafMissing(en, key)) continue
    // Prefer English if we can find it in the same git refs' en-US.
    let value
    for (const ref of refs) {
      try {
        const source = execSync(
          `git show ${ref}:frontend/src/i18n/locales/en-US.ts`,
          { cwd: root, encoding: 'utf8', maxBuffer: 50 * 1024 * 1024 },
        )
        const blob = loadLocale(source, `en${ref}`)
        const candidate = getPath(blob, key)
        if (typeof candidate === 'string' || typeof candidate === 'number') {
          value = candidate
          break
        }
      } catch {
        /* ignore missing file in that commit */
      }
    }
    if (value == null) continue
    deepSet(en, key, value)
    enApplied += 1
  }
  if (enApplied > 0) {
    fs.writeFileSync(enPath, `export default ${serialize(en, 0)}\n`)
  }
  console.log('enApplied', enApplied)
} catch (error) {
  console.error('en-US patch skipped:', error.message)
}
