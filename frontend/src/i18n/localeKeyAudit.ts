import { execSync } from 'node:child_process'
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

import { compile, type CompileError } from '@intlify/core-base'

import {
  FLAT_KEY_BAG_PATHS,
  KB_ACTIVITY_DETAIL_VALUES,
  KB_ACTIVITY_I18N_ROOTS,
  KB_ACTIVITY_OUTCOMES,
  REGISTERED_AUDIT_ACTION_ENTRIES,
} from './auditActionRegistry.ts'
import { AUDIT_ACTION_LOCALE_DEFAULTS, getAuditActionLocaleDefault } from './auditActionLocaleDefaults.ts'
import { writeLocaleModule } from './localeSerialize.ts'

import enUS from './locales/en-US.ts'
import koKR from './locales/ko-KR.ts'
import ruRU from './locales/ru-RU.ts'
import zhCN from './locales/zh-CN.ts'

export const LOCALE_BUNDLES = {
  'en-US': enUS,
  'zh-CN': zhCN,
  'ko-KR': koKR,
  'ru-RU': ruRU,
} as const

export type LocaleName = keyof typeof LOCALE_BUNDLES

const SOURCE_ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SOURCE_EXTENSIONS = /\.(vue|ts|js|mjs)$/
const TEST_FILE_PATTERN = /\.test\.(ts|mjs|js)$/
const I18N_SOURCE_HINTS = ['$t(', 'i18n.global.t(', "t('", 't("', ".t('", '.t("'] as const
const STATIC_KEY_PATTERNS = [
  /\$t\(\s*['"]([^'"]+)['"]/g,
  /i18n\.global\.t\(\s*['"]([^'"]+)['"]/g,
  /(?<![.\w])t\(\s*['"]([^'"]+)['"]/g,
  /(?<![.\w])te\(\s*['"]([^'"]+)['"]/g,
] as const
const CONCAT_KEY_PATTERNS = [
  /\$t\(\s*['"]([^'"]+)['"]\s*\+/g,
  /i18n\.global\.t\(\s*['"]([^'"]+)['"]\s*\+/g,
  /(?<![.\w])t\(\s*['"]([^'"]+)['"]\s*\+/g,
] as const
const TEMPLATE_KEY_PATTERNS = [
  /\$t\(\s*`([^`]+)`/g,
  /i18n\.global\.t\(\s*`([^`]+)`/g,
  /(?<![.\w])t\(\s*`([^`]+)`/g,
  /(?<![.\w])te\(\s*`([^`]+)`/g,
  /globalSettingsText\(\s*`([^`]+)`/g,
] as const
const METADATA_KEY_PATTERNS = [
  /\b(?:labelKey|hintKey|titleKey|i18nKey)\s*:\s*['"]([^'"]+)['"]/g,
  /\bstepI18nPrefix\s*:\s*['"]([^'"]+)['"]/g,
] as const
const STEP_PREFIX_PATTERNS = [
  /step-i18n-prefix=["']([^"']+)["']/g,
] as const
const TM_KEY_PATTERNS = [
  /(?<![.\w])tm\(\s*['"]([^'"]+)['"]/g,
] as const

const EXTRA_PREFIXES = [
  'system.globalSettings.listConfirm.',
  'system.globalSettings.admins.confirm.',
  'system.globalSettings.keyLabels.',
  'system.globalSettings.keyDescriptions.',
  'system.globalSettings.enumLabels.',
  'system.globalSettings.audit.action.',
  'system.globalSettings.audit.actorRole.',
  'knowledgeEditor.activity.detailFields.',
  'knowledgeEditor.activity.detailValues.',
  'knowledgeEditor.activity.outcomes.',
  'knowledgeEditor.activity.actions.',
  'knowledgeEditor.activity.targets.',
  'tenantMember.audit.action.',
  'newUserGuide.steps.',
  'contextualGuide.kbList.steps.',
  'contextualGuide.kbCreate.steps.',
  'contextualGuide.tenantModels.steps.',
  'contextualGuide.tenantModels.stepsAgent.',
  'contextualGuide.kbDetail.steps.',
  'contextualGuide.chat.steps.',
  'contextualGuide.agentList.steps.',
  'contextualGuide.agentCreate.steps.',
  'kbSettings.parser.engines.',
  'model.editor.description.',
  'integrations.tabs.',
  'knowledgeStages.stage.',
  'knowledgeStages.status.',
  'system.globalSettings.runtime.pools.',
  'system.globalSettings.runtime.poolDescriptions.',
  'system.globalSettings.runtime.queueNames.',
  'system.globalSettings.runtime.queueDescriptions.',
  'system.globalSettings.runtime.tasks.taskTypes.',
  'organization.role.',
  'inviteRegister.',
  'modelSettings.builtinModels.',
  'datasource.stepDescription.',
  'datasource.syncError.',
] as const

/** Keys that must survive pruning even when static analysis misses them. */
export const CRITICAL_LOCALE_KEYS = [
  'newUserGuide.steps.welcome.title',
  'contextualGuide.kbList.steps.create.title',
  'tenantMember.audit.action.rbac.member_added',
  'system.globalSettings.audit.action.system.setting_changed',
  'knowledgeEditor.activity.targets.knowledge_base',
  'modelSettings.builtinModels.descriptionAdmin',
  'model.editor.description.chat',
  'model.editor.description.asr',
  'inviteRegister.emailPlaceholder',
  'knowledgeList.sections.tenantOthers',
  'agent.sections.tenantOthers',
] as const

function addPrefix(usage: I18nUsage, prefix: string): void {
  if (!prefix) return
  usage.prefixes.add(prefix.endsWith('.') ? prefix : `${prefix}.`)
}

function buildI18nLiteralPattern(): RegExp {
  const namespaces = Object.keys(enUS as Record<string, unknown>)
    .map((name) => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('|')
  // Only match object-literal values (e.g. labelKey: 'foo.bar'), not Vue bindings like :name="agent.name".
  return new RegExp(`(?:[:,(]\\s*)['"]((?:${namespaces})\\.[^'"]+)['"]`, 'g')
}

export type I18nUsage = {
  staticKeys: Set<string>
  prefixes: Set<string>
  segmentPatterns: string[][]
}

/** Flatten a locale object into dot-path keys without recursive flatMap allocations. */
export function collectLocaleKeys(root: unknown): Set<string> {
  const keys = new Set<string>()
  const stack: Array<{ value: unknown; path: string }> = [{ value: root, path: '' }]

  while (stack.length > 0) {
    const current = stack.pop()!
    const { value, path } = current

    if (typeof value === 'string') {
      if (path) keys.add(path)
      continue
    }

    if (Array.isArray(value)) {
      for (let index = value.length - 1; index >= 0; index -= 1) {
        stack.push({ value: value[index], path: `${path}[${index}]` })
      }
      continue
    }

    if (!value || typeof value !== 'object') continue

    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      const nextPath = path ? `${path}.${key}` : key
      if (typeof child === 'string') keys.add(nextPath)
      else stack.push({ value: child, path: nextPath })
    }
  }

  return keys
}

function isAuditableSourceFile(path: string): boolean {
  if (!SOURCE_EXTENSIONS.test(path)) return false
  if (path.includes('/i18n/locales/')) return false
  if (TEST_FILE_PATTERN.test(path)) return false
  return true
}

function listSourceFiles(dir: string, results: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name !== 'node_modules') listSourceFiles(path, results)
      continue
    }
    if (isAuditableSourceFile(path)) results.push(path)
  }
  return results
}

function isStaticTranslationKey(key: string): boolean {
  if (!key || key.endsWith('.')) return false
  return /^[\w.-]+$/.test(key)
}

const KNOWN_LOCALE_NAMESPACES = new Set(Object.keys(enUS as Record<string, unknown>))
const INDIRECT_TERNARY_IN_T_RE = /\$t\(\s*[\s\S]*?\? ['"]([^'"]+)['"]\s*:\s*['"]([^'"]+)['"]/g
const INDIRECT_VAR_T_RE = /\$t\(([a-zA-Z_][\w]*)\)/g

function addIndirectTranslationKeys(content: string, usage: I18nUsage): void {
  INDIRECT_TERNARY_IN_T_RE.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = INDIRECT_TERNARY_IN_T_RE.exec(content)) !== null) {
    for (const key of [match[1], match[2]]) {
      if (KNOWN_LOCALE_NAMESPACES.has(key.split('.')[0]) && isStaticTranslationKey(key)) {
        usage.staticKeys.add(key)
      }
    }
  }

  const variableNames = new Set<string>()
  INDIRECT_VAR_T_RE.lastIndex = 0
  while ((match = INDIRECT_VAR_T_RE.exec(content)) !== null) {
    variableNames.add(match[1])
  }

  for (const variableName of variableNames) {
    const computedPattern = new RegExp(
      `${escapeRegExp(variableName)}\\s*=\\s*computed\\(\\(\\)\\s*=>[\\s\\S]*?\\n\\)`,
      'm',
    )
    const block = content.match(computedPattern)?.[0]
    if (!block) continue

    const ternaryPattern = /\? ['"]([^'"]+)['"]\s*:\s*['"]([^'"]+)['"]/g
    ternaryPattern.lastIndex = 0
    while ((match = ternaryPattern.exec(block)) !== null) {
      for (const key of [match[1], match[2]]) {
        if (KNOWN_LOCALE_NAMESPACES.has(key.split('.')[0]) && isStaticTranslationKey(key)) {
          usage.staticKeys.add(key)
        }
      }
    }
  }
}

let cachedSourceFiles: string[] | undefined

function getSourceFiles(rootDir = SOURCE_ROOT): string[] {
  if (!cachedSourceFiles) cachedSourceFiles = listSourceFiles(rootDir)
  return cachedSourceFiles
}

/** Collect literal i18n keys from app source (excludes locale bundles and tests). */
export function collectStaticI18nKeysFromSources(rootDir = SOURCE_ROOT): Set<string> {
  return collectI18nUsageFromSources(rootDir).staticKeys
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function addTemplatePattern(usage: I18nUsage, template: string): void {
  const trimmed = template.trim()
  if (!trimmed || !trimmed.includes('${')) return

  const parts = trimmed.split(/\$\{[^}]+\}/)
  if (parts.length === 1) {
    usage.prefixes.add(parts[0])
    return
  }

  if (parts.length === 2 && parts[1] === '') {
    usage.prefixes.add(parts[0])
    return
  }

  usage.segmentPatterns.push(parts)
}

export function collectI18nUsageFromSources(rootDir = SOURCE_ROOT): I18nUsage {
  const usage: I18nUsage = {
    staticKeys: new Set<string>(),
    prefixes: new Set<string>(EXTRA_PREFIXES),
    segmentPatterns: [],
  }
  const i18nLiteralPattern = buildI18nLiteralPattern()

  for (const file of getSourceFiles(rootDir)) {
    const content = readFileSync(file, 'utf8')

    for (const pattern of STATIC_KEY_PATTERNS) {
      pattern.lastIndex = 0
      let match: RegExpExecArray | null
      while ((match = pattern.exec(content)) !== null) {
        const key = match[1]
        if (isStaticTranslationKey(key)) usage.staticKeys.add(key)
      }
    }

    for (const pattern of METADATA_KEY_PATTERNS) {
      pattern.lastIndex = 0
      let match: RegExpExecArray | null
      while ((match = pattern.exec(content)) !== null) {
        const key = match[1]
        if (key.includes('.steps')) addPrefix(usage, key)
        else if (isStaticTranslationKey(key)) usage.staticKeys.add(key)
      }
    }

    for (const pattern of STEP_PREFIX_PATTERNS) {
      pattern.lastIndex = 0
      let match: RegExpExecArray | null
      while ((match = pattern.exec(content)) !== null) {
        addPrefix(usage, match[1])
      }
    }

    for (const pattern of TM_KEY_PATTERNS) {
      pattern.lastIndex = 0
      let match: RegExpExecArray | null
      while ((match = pattern.exec(content)) !== null) {
        addPrefix(usage, match[1])
      }
    }

    i18nLiteralPattern.lastIndex = 0
    let literalMatch: RegExpExecArray | null
    while ((literalMatch = i18nLiteralPattern.exec(content)) !== null) {
      const key = literalMatch[1]
      if (isStaticTranslationKey(key)) usage.staticKeys.add(key)
    }

    for (const pattern of CONCAT_KEY_PATTERNS) {
      pattern.lastIndex = 0
      let match: RegExpExecArray | null
      while ((match = pattern.exec(content)) !== null) {
        const prefix = match[1]
        if (prefix) usage.prefixes.add(prefix)
      }
    }

    for (const pattern of TEMPLATE_KEY_PATTERNS) {
      pattern.lastIndex = 0
      let match: RegExpExecArray | null
      while ((match = pattern.exec(content)) !== null) {
        addTemplatePattern(usage, match[1])
      }
    }

    addIndirectTranslationKeys(content, usage)
  }

  return usage
}

export function isLocaleKeyUsed(key: string, usage: I18nUsage): boolean {
  if (usage.staticKeys.has(key)) return true

  for (const prefix of usage.prefixes) {
    if (key.startsWith(prefix)) return true
  }

  for (const parts of usage.segmentPatterns) {
    let pattern = '^'
    for (let index = 0; index < parts.length; index += 1) {
      pattern += escapeRegExp(parts[index])
      if (index < parts.length - 1) pattern += '[^.]+'
    }
    pattern += '$'
    if (new RegExp(pattern).test(key)) return true
  }

  return false
}

export function collectReferencedLocaleKeys(root: unknown, usage: I18nUsage): Set<string> {
  const referenced = new Set<string>()
  for (const key of collectLocaleKeys(root)) {
    if (isLocaleKeyUsed(key, usage)) referenced.add(key)
  }
  return referenced
}

export function pruneLocaleTree(root: unknown, usage: I18nUsage, path = ''): unknown {
  if (typeof root === 'string') {
    return path && isLocaleKeyUsed(path, usage) ? root : undefined
  }

  if (Array.isArray(root)) {
    const pruned = root
      .map((item, index) => pruneLocaleTree(item, usage, `${path}[${index}]`))
      .filter((item) => item !== undefined)
    return pruned.length > 0 ? pruned : undefined
  }

  if (!root || typeof root !== 'object') return undefined

  const result: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(root as Record<string, unknown>)) {
    const nextPath = path ? `${path}.${key}` : key
    const pruned = pruneLocaleTree(value, usage, nextPath)
    if (pruned !== undefined) result[key] = pruned
  }

  return Object.keys(result).length > 0 ? result : undefined
}

export function diffLocaleKeys(reference: Set<string>, target: Set<string>): {
  missing: string[]
  extra: string[]
} {
  const missing: string[] = []
  const extra: string[] = []

  for (const key of reference) {
    if (!target.has(key)) missing.push(key)
  }
  for (const key of target) {
    if (!reference.has(key)) extra.push(key)
  }

  missing.sort()
  extra.sort()
  return { missing, extra }
}

export function findUsedKeysMissingInLocales(
  usedKeys: Iterable<string>,
  localeKeysByName: Record<LocaleName, Set<string>>,
): string[] {
  const failures: string[] = []

  for (const key of usedKeys) {
    const missingIn = (Object.keys(localeKeysByName) as LocaleName[]).filter(
      (localeName) => !localeKeysByName[localeName].has(key),
    )
    if (missingIn.length > 0) failures.push(`${key} -> missing in ${missingIn.join(', ')}`)
  }

  failures.sort()
  return failures
}

export type LocaleMessageEntry = {
  path: string
  value: string
}

export type LocaleMessageCompileError = {
  path: string
  value: string
  message: string
}

const LOCALE_MESSAGE_COMPILE_OPTIONS = {
  warnHtmlMessage: false,
} as const

/** Flatten a locale object into dot-path message entries. */
export function collectLocaleMessages(root: unknown, path = ''): LocaleMessageEntry[] {
  if (typeof root === 'string') {
    return path ? [{ path, value: root }] : []
  }

  if (Array.isArray(root)) {
    return root.flatMap((item, index) => collectLocaleMessages(item, `${path}[${index}]`))
  }

  if (!root || typeof root !== 'object') return []

  return Object.entries(root as Record<string, unknown>).flatMap(([key, value]) => {
    const nextPath = path ? `${path}.${key}` : key
    return collectLocaleMessages(value, nextPath)
  })
}

/** Compile a locale message with the same vue-i18n rules used at runtime. */
export function compileLocaleMessage(
  value: string,
  context: { key: string; locale: string } = { key: 'audit', locale: 'en-US' },
): void {
  compile(value, {
    ...LOCALE_MESSAGE_COMPILE_OPTIONS,
    key: context.key,
    locale: context.locale,
    onError: (error: CompileError) => {
      throw error
    },
  })
}

export function findLocaleMessageCompileErrors(
  root: unknown,
  localeName = 'locale',
): LocaleMessageCompileError[] {
  const failures: LocaleMessageCompileError[] = []

  for (const { path, value } of collectLocaleMessages(root)) {
    try {
      compileLocaleMessage(value, { key: path, locale: localeName })
    } catch (error) {
      failures.push({
        path: `${localeName}:${path}`,
        value,
        message: error instanceof Error ? error.message : String(error),
      })
    }
  }

  failures.sort((left, right) => left.path.localeCompare(right.path))
  return failures
}

export function findAllLocaleMessageCompileErrors(
  bundles: Record<string, unknown>,
): LocaleMessageCompileError[] {
  return Object.entries(bundles).flatMap(([localeName, bundle]) =>
    findLocaleMessageCompileErrors(bundle, localeName),
  )
}

type LocaleTree = Record<string, unknown>

const LOCALE_ORDER: LocaleName[] = ['en-US', 'zh-CN', 'ko-KR', 'ru-RU']
const LOCALES_DIR = join(dirname(fileURLToPath(import.meta.url)), 'locales')

function getLocaleValueAtPathParts(current: unknown, parts: string[]): unknown {
  if (parts.length === 0) return current
  if (current == null || typeof current !== 'object' || Array.isArray(current)) return undefined

  const object = current as Record<string, unknown>
  for (let length = parts.length; length >= 1; length -= 1) {
    const key = parts.slice(0, length).join('.')
    if (!Object.prototype.hasOwnProperty.call(object, key)) continue

    const next = object[key]
    if (length === parts.length) return next
    return getLocaleValueAtPathParts(next, parts.slice(length))
  }

  return undefined
}

function setLocaleValueAtPathParts(current: unknown, parts: string[], value: string): void {
  if (parts.length === 0) return

  const object = current as Record<string, unknown>
  for (let length = parts.length; length >= 1; length -= 1) {
    const key = parts.slice(0, length).join('.')
    if (!Object.prototype.hasOwnProperty.call(object, key) && length > 1) continue

    if (length === parts.length) {
      object[key] = value
      return
    }

    let child = object[key]
    if (child == null || typeof child !== 'object' || Array.isArray(child)) {
      child = {}
      object[key] = child
    }
    setLocaleValueAtPathParts(child, parts.slice(length), value)
    return
  }

  const [head, ...tail] = parts
  let child = object[head]
  if (child == null || typeof child !== 'object' || Array.isArray(child)) {
    child = {}
    object[head] = child
  }
  setLocaleValueAtPathParts(child, tail, value)
}

export function getLocaleValueAtPath(root: unknown, path: string): unknown {
  return getLocaleValueAtPathParts(root, path.split('.'))
}

export function setLocaleValueAtPath(root: LocaleTree, path: string, value: string): void {
  if (!path) return
  if (setLocaleFlatBagValueIfApplicable(root, path, value)) return
  setLocaleValueAtPathParts(root, path.split('.'), value)
}

/** Write flat keys (e.g. `kb.created`) under audit action bags without nesting. */
function setLocaleFlatBagValueIfApplicable(root: LocaleTree, path: string, value: string): boolean {
  for (const bagPath of FLAT_KEY_BAG_PATHS) {
    const prefix = `${bagPath}.`
    if (!path.startsWith(prefix)) continue

    const actionKey = path.slice(prefix.length)
    if (!actionKey) return false

    const bagParts = bagPath.split('.')
    let node: Record<string, unknown> = root
    for (const part of bagParts) {
      let child = node[part]
      if (child == null || typeof child !== 'object' || Array.isArray(child)) {
        child = {}
        node[part] = child
      }
      node = child as Record<string, unknown>
    }
    node[actionKey] = value
    return true
  }
  return false
}

export function mergeLocaleKeysFromSource(
  target: LocaleTree,
  source: unknown,
  keys: Iterable<string>,
): void {
  for (const key of keys) {
    if (collectLocaleKeys(target).has(key)) continue
    const value =
      getLocaleValueAtPath(source, key) ??
      getAuditActionLocaleDefault(key) ??
      AUDIT_ACTION_LOCALE_DEFAULTS[key]
    if (typeof value === 'string') setLocaleValueAtPath(target, key, value)
  }
}

export function collectRequiredLocaleKeys(usage: I18nUsage, referenceBundle: unknown): Set<string> {
  const required = new Set([
    ...collectReferencedLocaleKeys(referenceBundle, usage),
    ...usage.staticKeys,
    ...CRITICAL_LOCALE_KEYS,
  ])

  for (const { root, actions } of REGISTERED_AUDIT_ACTION_ENTRIES) {
    for (const action of actions) required.add(`${root}.${action}`)
  }
  for (const outcome of KB_ACTIVITY_OUTCOMES) {
    required.add(`${KB_ACTIVITY_I18N_ROOTS.outcomes}.${outcome}`)
  }
  for (const value of KB_ACTIVITY_DETAIL_VALUES) {
    required.add(`${KB_ACTIVITY_I18N_ROOTS.detailValues}.${value}`)
  }

  return required
}

export function alignLocaleBundleToReferenceKeys(
  referenceKeys: Set<string>,
  localeFull: LocaleTree,
  fallbackFull: LocaleTree,
): LocaleTree {
  const aligned: LocaleTree = {}
  for (const key of referenceKeys) {
    const value = getLocaleValueAtPath(localeFull, key) ?? getLocaleValueAtPath(fallbackFull, key)
    if (typeof value === 'string') setLocaleValueAtPath(aligned, key, value)
  }
  return aligned
}

export function rebuildPrunedLocales(
  fullBundles: Record<LocaleName, LocaleTree>,
  usage = collectI18nUsageFromSources(),
): Record<LocaleName, LocaleTree> {
  const requiredKeys = collectRequiredLocaleKeys(usage, fullBundles['en-US'])
  const enFull = structuredClone(fullBundles['en-US'])
  const enPruned = pruneLocaleTree(enFull, usage)
  const enTree: LocaleTree =
    enPruned && typeof enPruned === 'object' && !Array.isArray(enPruned)
      ? (enPruned as LocaleTree)
      : {}
  mergeLocaleKeysFromSource(enTree, fullBundles['en-US'], requiredKeys)
  const referenceKeys = collectLocaleKeys(enTree)

  const rebuilt = { 'en-US': enTree } as Record<LocaleName, LocaleTree>
  for (const localeName of LOCALE_ORDER) {
    if (localeName === 'en-US') continue
    rebuilt[localeName] = alignLocaleBundleToReferenceKeys(
      referenceKeys,
      fullBundles[localeName],
      fullBundles['en-US'],
    )
  }

  return rebuilt
}

export function writePrunedLocaleFiles(
  bundles: Record<LocaleName, LocaleTree>,
  localesDir = LOCALES_DIR,
): void {
  for (const localeName of LOCALE_ORDER) {
    writeLocaleModule(join(localesDir, `${localeName}.ts`), bundles[localeName])
  }
}

const REPO_ROOT = join(dirname(fileURLToPath(import.meta.url)), '../../..')

export async function loadLocaleBundlesFromDisk(
  localesDir = LOCALES_DIR,
): Promise<Record<LocaleName, LocaleTree>> {
  const bundles = {} as Record<LocaleName, LocaleTree>
  for (const localeName of LOCALE_ORDER) {
    const filePath = join(localesDir, `${localeName}.ts`)
    const mod = await import(`${pathToFileURL(filePath).href}?disk=${Date.now()}`)
    bundles[localeName] = structuredClone(mod.default) as LocaleTree
  }
  return bundles
}

export async function loadFullLocaleBundlesFromGit(
  ref = 'HEAD',
): Promise<Record<LocaleName, LocaleTree>> {
  const tempDir = mkdtempSync(join(tmpdir(), 'weknora-locales-'))
  try {
    const bundles = {} as Record<LocaleName, LocaleTree>
    for (const localeName of LOCALE_ORDER) {
      const relativePath = `frontend/src/i18n/locales/${localeName}.ts`
      const content = execSync(`git show ${ref}:${relativePath}`, {
        cwd: REPO_ROOT,
        encoding: 'utf8',
      })
      const filePath = join(tempDir, `${localeName}.ts`)
      writeFileSync(filePath, content, 'utf8')
      const mod = await import(pathToFileURL(filePath).href)
      bundles[localeName] = structuredClone(mod.default) as LocaleTree
    }
    return bundles
  } finally {
    rmSync(tempDir, { recursive: true, force: true })
  }
}

export async function regeneratePrunedLocaleFiles(localesDir = LOCALES_DIR): Promise<{
  before: number
  after: number
}> {
  const usage = collectI18nUsageFromSources()
  const sourceBundles = await loadLocaleBundlesFromDisk(localesDir)
  const before = collectLocaleKeys(sourceBundles['en-US']).size
  const rebuilt = rebuildPrunedLocales(sourceBundles, usage)
  writePrunedLocaleFiles(rebuilt, localesDir)
  return {
    before,
    after: collectLocaleKeys(rebuilt['en-US']).size,
  }
}
