import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import enUS from '../../../i18n/locales/en-US.ts'
import koKR from '../../../i18n/locales/ko-KR.ts'
import ruRU from '../../../i18n/locales/ru-RU.ts'
import zhCN from '../../../i18n/locales/zh-CN.ts'
import {
  DINGTALK_CONNECTOR_DEF,
  connectorRequiresCredentials,
  connectionValidationMode,
  createLatestRequestGate,
  credentialsForMainPayload,
  hasCandidateCredentialValues,
  mergeLazyResources,
  missingRequiredCredentialField,
  requiresResourceSelection,
  usesCandidateResourcePreview,
} from './datasourceEditorLogic.ts'

test('latest request gate prevents stale deferred responses from committing', async () => {
  const gate = createLatestRequestGate()
  const committed: string[] = []
  let resolveOld!: (value: string) => void
  let resolveNew!: (value: string) => void
  const oldResponse = new Promise<string>((resolve) => { resolveOld = resolve })
  const newResponse = new Promise<string>((resolve) => { resolveNew = resolve })

  const run = async (response: Promise<string>) => {
    const generation = gate.begin()
    const value = await response
    if (gate.isCurrent(generation)) committed.push(value)
  }
  const oldRun = run(oldResponse)
  const newRun = run(newResponse)
  resolveNew('new')
  await newRun
  resolveOld('old')
  await oldRun

  assert.deepEqual(committed, ['new'])
  const active = gate.begin()
  gate.invalidate()
  assert.equal(gate.isCurrent(active), false)
})

test('DingTalk connector exposes required credentials, permissions, and official help links', () => {
  assert.equal(DINGTALK_CONNECTOR_DEF.type, 'dingtalk')
  assert.equal(DINGTALK_CONNECTOR_DEF.available, true)
  assert.deepEqual(
    DINGTALK_CONNECTOR_DEF.fields.map((field) => [field.key, !!field.optional, !!field.secret]),
    [
      ['app_key', false, false],
      ['app_secret', false, true],
      ['operator_id', false, false],
    ],
  )
  assert.deepEqual(DINGTALK_CONNECTOR_DEF.requiredPermissions, [
    'Wiki.Workspace.Read',
    'Wiki.Node.Read',
    'Storage.File.Read',
  ])
  for (const link of [
    DINGTALK_CONNECTOR_DEF.docUrl,
    DINGTALK_CONNECTOR_DEF.permissionDocUrl,
    DINGTALK_CONNECTOR_DEF.permissionPageUrl,
  ]) {
    assert.match(link, /^https:\/\/(?:open|open-dev)\.dingtalk\.com\//)
  }
})

test('DingTalk connection validation requires all identity fields but not base URL', () => {
  const credentials: Record<string, unknown> = {}
  assert.equal(
    missingRequiredCredentialField(DINGTALK_CONNECTOR_DEF, credentials)?.key,
    'app_key',
  )
  credentials.app_key = 'fixture-app-key'
  credentials.app_secret = 'fixture-app-secret'
  assert.equal(
    missingRequiredCredentialField(DINGTALK_CONNECTOR_DEF, credentials)?.key,
    'operator_id',
  )
  credentials.operator_id = 'fixture-operator'
  assert.equal(
    missingRequiredCredentialField(DINGTALK_CONNECTOR_DEF, credentials),
    undefined,
  )
  credentials.operator_id = '   '
  assert.equal(
    missingRequiredCredentialField(DINGTALK_CONNECTOR_DEF, credentials)?.key,
    'operator_id',
  )
})

test('create, edit, and credential replacement use the correct validation and payload paths', () => {
  assert.equal(connectionValidationMode(false, false, false), 'stateless')
  assert.equal(connectionValidationMode(true, true, false), 'stored')
  assert.equal(connectionValidationMode(true, true, true), 'stateless')
  assert.equal(connectionValidationMode(true, false, false), 'stateless')
  assert.equal(usesCandidateResourcePreview(false, false, false), false)
  assert.equal(usesCandidateResourcePreview(true, true, false), false)
  assert.equal(usesCandidateResourcePreview(true, true, true), true)
  assert.equal(usesCandidateResourcePreview(true, false, false), true)
  assert.equal(usesCandidateResourcePreview(true, false, false, false), false)
  assert.equal(usesCandidateResourcePreview(true, false, false, false, true), true)
  assert.equal(hasCandidateCredentialValues({}), false)
  assert.equal(hasCandidateCredentialValues({ auth_headers: '   ' }), false)
  assert.equal(hasCandidateCredentialValues({ auth_headers: 'Authorization: secret' }), true)
  assert.equal(connectorRequiresCredentials(DINGTALK_CONNECTOR_DEF), true)
  assert.equal(connectorRequiresCredentials({
    type: 'rss',
    available: true,
    docUrl: '',
    permissionDocUrl: '',
    permissionPageUrl: '',
    requiredPermissions: [],
    fields: [{
      key: 'auth_headers',
      labelKey: 'datasource.field.authHeaders',
      placeholder: '',
      optional: true,
      fieldType: 'custom_headers',
    }],
  }), false)
  assert.equal(requiresResourceSelection('dingtalk'), true)
  assert.equal(requiresResourceSelection('notion'), false)

  const credentials = {
    app_key: 'fixture-app-key',
    app_secret: 'fixture-app-secret',
    operator_id: 'fixture-operator',
  }
  assert.deepEqual(credentialsForMainPayload(false, credentials), credentials)
  assert.deepEqual(credentialsForMainPayload(true, credentials), {})
  assert.equal(credentials.app_secret, 'fixture-app-secret')
})

test('lazy resource loading appends new children and deduplicates retries', () => {
  const existing = [
    { external_id: 'workspace', name: 'Workspace' },
    { external_id: 'folder', name: 'Folder' },
  ]
  const merged = mergeLazyResources(existing, [
    { external_id: 'folder', name: 'Duplicate Folder' },
    { external_id: 'document', name: 'Document' },
    { external_id: 'document', name: 'Duplicate Document' },
  ])

  assert.deepEqual(merged.map((resource) => resource.external_id), [
    'workspace',
    'folder',
    'document',
  ])
  assert.notEqual(merged, existing)
})

test('all supported locales and icon routing contain DingTalk UI contracts', () => {
  const locales = [
    ['en-US', enUS],
    ['zh-CN', zhCN],
    ['ko-KR', koKR],
    ['ru-RU', ruRU],
  ] as const
  const requiredPaths = [
    'connector.dingtalk',
    'connectorDesc.dingtalk',
    'field.appKey',
    'field.operatorId',
    'field.operatorIdHint',
    'resourceRequired',
    'resourceLoadFailedDesc',
    'noResources_dingtalk',
    'noResourcesDesc_dingtalk',
    'guideStep1_dingtalk',
    'guideStep2_dingtalk',
    'guideStep3_dingtalk',
    'permissionDocLink_dingtalk',
    'selectedScopeCount',
    'createTitleWithType',
    'editTitleWithType',
    'stepDescription.selectType',
    'stepDescription.credentials',
    'stepDescription.resources',
    'stepDescription.strategy',
    'stepProgress',
    'syncMode.incrementalDesc',
    'syncMode.fullDesc',
    'conflict.overwriteDesc',
    'conflict.skipDesc',
    'resourceType.space',
    'resourceType.folder',
    'resourceType.document',
    'prereqBarText_dingtalk',
    'prereqStep1Brief_dingtalk',
    'prereqStep1Desc_dingtalk',
    'prereqStep2Brief_dingtalk',
    'prereqStep2Desc_dingtalk',
    'prereqStep3Brief_dingtalk',
    'prereqStep3Desc_dingtalk',
    'prereqOpenConsole_dingtalk',
    'resumeFailed',
    'syncError.feishu_auth_or_permission',
    'syncError.feishu_rate_limited',
    'syncError.feishu_timeout',
    'syncError.feishu_server_unavailable',
    'syncError.feishu_api_error',
    'syncError.feishu_api_error_generic',
    'syncError.sync_failed',
    'syncError.ingest_failed',
    'syncError.delete_failed',
  ]

  for (const [name, locale] of locales) {
    const datasource = (locale as Record<string, any>).datasource
    for (const path of requiredPaths) {
      const value = path.split('.').reduce<any>((current, key) => current?.[key], datasource)
      assert.equal(typeof value, 'string', `${name} is missing datasource.${path}`)
      assert.notEqual(value.trim(), '', `${name} has an empty datasource.${path}`)
    }
  }

  const sourceDir = dirname(fileURLToPath(import.meta.url))
  const iconSource = readFileSync(resolve(sourceDir, 'datasourceIcons.ts'), 'utf8')
  const typeIconSource = readFileSync(resolve(sourceDir, 'DataSourceTypeIcon.vue'), 'utf8')
  assert.match(iconSource, /dingtalk:\s*dingtalkIcon/)
  assert.match(typeIconSource, /case 'dingtalk':\s+return 'D'/)
})

test('the editor wires stateless tests, stored tests, and credential replacement separately', () => {
  const sourceDir = dirname(fileURLToPath(import.meta.url))
  const dialogSource = readFileSync(resolve(sourceDir, 'DataSourceEditorDialog.vue'), 'utf8')
  const settingsSource = readFileSync(resolve(sourceDir, 'DataSourceSettings.vue'), 'utf8')

  assert.match(dialogSource, /DINGTALK_CONNECTOR_DEF/)
  assert.match(dialogSource, /credentials:\s*validationMode === 'stored'[\s\S]*\?\s*null[\s\S]*candidateCredentialsForRequest\(\)/)
  assert.match(dialogSource, /validateOnly:\s*true/)
  assert.match(dialogSource, /await previewResources\(\{[\s\S]*dataSourceId:\s*isEdit\.value \? tempDsId\.value : undefined/)
  assert.match(dialogSource, /credentialsForPreviewRequest\(\)/)
  assert.match(dialogSource, /await reconfigureDataSource\([\s\S]*candidateCredentialsForRequest\(\)/)
  assert.match(dialogSource, /if \(!isEdit\.value \|\| isCandidateResourcePreview\(\)\)/)
  assert.match(dialogSource, /\(isEdit\.value \|\| resources\.value\.length > 0\)[\s\S]*JSON\.stringify\(settings\)[\s\S]*resetResourcePicker\(true\)/)
  assert.match(dialogSource, /form\.value\.config\.resource_ids = \[\]/)
  assert.doesNotMatch(dialogSource, /status:\s*'paused'/)
  assert.doesNotMatch(dialogSource, /deleteDataSource\(/)
  assert.doesNotMatch(dialogSource, /listResources\(/)
  assert.match(dialogSource, /status:\s*editing \? \(props\.dataSource\?\.status \|\| 'active'\) : 'active'/)
  assert.match(dialogSource, /dialogSessionGate\.isCurrent\(sessionGeneration\)/)
  assert.match(dialogSource, /connectionTestGate\.isCurrent\(requestGeneration\)/)
  assert.match(dialogSource, /if \(!next && submitting\.value\) return/)
  assert.doesNotMatch(dialogSource, /putDataSourceCredentials/)
  assert.match(dialogSource, /selectionBeforeCredentialReplace\.value = \[\.\.\.selectedResourceIds\.value\]/)
  assert.match(dialogSource, /selectedResourceIds\.value = \[\.\.\.selectionBeforeCredentialReplace\.value\]/)
  assert.match(dialogSource, /requiresResourceSelection\(form\.value\.type\)[\s\S]*selectedResourceCount\.value === 0/)
  assert.match(dialogSource, /sib\.external_id !== next && isResourceSelectable\(sib\)/)
  assert.match(dialogSource, /credentialsForMainPayload\(/)
  assert.match(dialogSource, /unsupported:\s*'datasource\.resourceType\.unsupported'/)
  assert.match(dialogSource, /space:\s*'datasource\.resourceType\.space'/)
  assert.match(dialogSource, /folder:\s*'datasource\.resourceType\.folder'/)
  assert.match(dialogSource, /document:\s*'datasource\.resourceType\.document'/)
  assert.match(dialogSource, /selectedResourceIds\.value\.length/)
  assert.match(dialogSource, /resourceLoadError\.value = true/)
  assert.match(dialogSource, /resourceLoadError \? 'error-circle' : 'search'/)
  assert.match(dialogSource, /resourceLoadError \|\|[\s\S]*requiresResourceSelection\(form\.type\)/)
  assert.match(dialogSource, /step\.value === 2 && resourceLoadError\.value/)
  assert.match(dialogSource, /v-else-if="!resourceLoadError && resources\.length > 0"/)
  assert.match(dialogSource, /:aria-disabled="!isResourceSelectable\(r\)"/)
  assert.match(dialogSource, /:aria-label="t\('datasource\.step\.resources'\)"/)
  assert.match(dialogSource, /resourceTabIndex\(r\.external_id\)/)
  assert.match(dialogSource, /tabindex="-1"/)
  assert.match(dialogSource, /\? 'mixed'/)
  assert.match(dialogSource, /@keydown\.enter\.stop/)
  assert.match(dialogSource, /@keydown\.space\.stop/)
  assert.match(dialogSource, /:aria-expanded="expandedResourceIds\.has\(r\.external_id\)"/)
  assert.match(dialogSource, /@keydown\.enter\.prevent\.stop="toggleResource\(r\.external_id\)"/)
  assert.match(dialogSource, /@keydown\.space\.prevent\.stop="toggleResource\(r\.external_id\)"/)
  assert.match(dialogSource, /@keydown\.down\.prevent\.stop="moveResourceFocus\(r\.external_id, 1\)"/)
  assert.match(dialogSource, /@keydown\.right\.prevent\.stop="handleResourceArrowRight\(r\)"/)
  assert.match(dialogSource, /v-native-control/)
  assert.match(dialogSource, /:for="credentialFieldId\(field\.key\)"/)
  assert.match(dialogSource, /var\(--td-brand-color-7, var\(--td-brand-color\)\)/)
  assert.match(dialogSource, /var\(--td-brand-color-8, var\(--td-brand-color-active\)\)/)
  assert.match(dialogSource, /stroke="currentColor"/)
  assert.match(dialogSource, /\.ds-step\.active \.ds-step-num \{[\s\S]*color:\s*var\(--td-bg-color-container\)/)
  assert.match(
    dialogSource,
    /\.ds-setup-guide__toggle:focus-visible \{[\s\S]*outline:\s*2px solid var\(--td-brand-color-7/,
  )
  assert.match(
    dialogSource,
    /\.ds-setup-step::before \{[\s\S]*color:\s*color-mix\(in srgb, #1677ff 60%, var\(--td-text-color-primary\)\)/,
  )
  assert.doesNotMatch(dialogSource, /color:\s*var\(--td-text-color-placeholder\)/)
  assert.match(settingsSource, /var\(--td-brand-color-7, var\(--td-brand-color\)\)/)
  assert.doesNotMatch(settingsSource, /color:\s*var\(--td-text-color-placeholder\)/)
  assert.match(dialogSource, /if \(!isResourceSelectable\(resource\)\) return/)
})
