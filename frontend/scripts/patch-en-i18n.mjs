import fs from 'fs'
import path from 'path'
import { createRequire } from 'module'
import { fileURLToPath } from 'url'

const require = createRequire(import.meta.url)
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
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

const fallbacks = {
  'chat.editorOpened': 'Opened in editor',
  'chat.emptyContentWarning': 'Content is empty',
  'chat.imageReadFailed': 'Failed to read image',
  'datasource.resumeFailed': 'Failed to resume data source',
  'error.tenant.createApiKeyFailed': 'Failed to create API key',
  'error.tenant.createFailed': 'Failed to create workspace',
  'error.tenant.deleteApiKeyFailed': 'Failed to delete API key',
  'error.tenant.listApiKeysFailed': 'Failed to list API keys',
  'file.downloadFailed': 'Download failed',
  'input.fileUpload.label': 'Upload file',
  'input.fileUpload.tooLarge': 'File is too large',
  'input.fileUpload.tooMany': 'Too many files',
  'input.fileUpload.tooltip': 'Upload attachment',
  'input.imageUpload.label': 'Upload image',
  'input.imageUpload.tooltip': 'Upload image',
  'input.webSearch.label': 'Web search',
  'knowledge.untitledDocument': 'Untitled document',
  'knowledgeBase.createSessionError': 'Failed to create session',
  'knowledgeBase.createSessionFailed': 'Failed to create session',
  'knowledgeBase.moreOptions': 'More options',
  'knowledgeBase.selectKnowledgeBase': 'Select knowledge base',
  'knowledgeEditor.basic.shareWithDescendantsLabel':
    'Share with subordinate organizations',
  'knowledgeEditor.basic.shareWithDescendantsTip':
    'When enabled, descendant organizations can reference this knowledge base read-only. Off by default.',
  'mcpServiceDialog.shareWithDescendantsLabel': 'Share with subordinate units',
  'mcpServiceDialog.shareWithDescendantsTip':
    'When enabled, descendant org units can read-only use this MCP service. Off by default.',
  'menu.dataCharts': 'Data Charts',
  'menu.orgUnits': 'Organization hierarchy',
  'orgUnit.sectionDescription':
    'Platform organization tree: root units are created by system admins. Members land in the org-named workspace and share it with users in child units.',
  'orgUnit.sectionDescriptionScoped':
    'Manage subordinate organizations from your own unit: add or adjust child units under yourself. Ancestors and peer units are out of scope.',
  'root.kb.created': 'Knowledge base created',
  'settings.parser.checking': 'Checking…',
  'tenantInvitation.columns.orgUnit': 'Organization',
  'tenantInvitation.errors.inviterOrgUnitRequired':
    'Select your current organization before inviting.',
  'tenantInvitation.errors.orgUnitRequired':
    'Select the organization unit for the invitee.',
  'tenantInvitation.orgUnitLabel': 'Organization',
  'tenantInvitation.orgUnitPlaceholder':
    'Own / subordinate (admin role: subordinate only)',
}

const en = loadLocale(fs.readFileSync(enPath, 'utf8'), 'en')
let applied = 0
for (const [key, value] of Object.entries(fallbacks)) {
  if (isLeafMissing(en, key)) {
    deepSet(en, key, value)
    applied += 1
  }
}
fs.writeFileSync(enPath, `export default ${serialize(en, 0)}\n`)

const verify = loadLocale(fs.readFileSync(enPath, 'utf8'), 'en2')
const still = Object.keys(fallbacks).filter((key) => isLeafMissing(verify, key))
console.log(JSON.stringify({ applied, still }, null, 2))
