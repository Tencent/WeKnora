import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(
  new URL('./SandboxConfigEditorDrawer.vue', import.meta.url), 'utf8')

test('connection step has no desktop-image switch', () => {
  const connection = source.slice(
    source.indexOf("currentStepKey === 'connection' && isRemoteBackend"),
    source.indexOf("currentStepKey === 'connection' && !isRemoteBackend"),
  )
  assert.doesNotMatch(
    connection,
    /desktopEnabled/,
    'desktop_enabled is derived from the template the admin picks, not a connection-step switch',
  )
})

test('desktop templates appear in the catalog like CLI, without a sibling create offer', () => {
  const template = source.slice(
    source.indexOf("currentStepKey === 'template'"),
    source.indexOf("currentStepKey === 'runtime'"),
  )
  assert.ok(template.includes('canCreateStandard'), 'CLI create offer remains a fallback if ensure failed')
  assert.doesNotMatch(
    template,
    /canCreateDesktop|createDesktopTemplate|weknoraDesktopTemplate/,
    'desktop must not have a separate create row; listing ensures it from the Hub image',
  )
  assert.ok(
    template.includes('item.desktop'),
    'listed cards still mark the desktop image so picking one can set desktop_enabled',
  )
})

test('listing Cube/E2B templates ensures the published desktop image', () => {
  assert.match(
    source,
    /ensure_desktop:\s*opts\.ensureDesktop \|\| \(ensureFirstParty && !opts\.replaceDesktop\)/,
    'catalog load must send ensure_desktop so a missing weknora-desktop is built from Hub',
  )
  assert.match(
    source,
    /const ensureFirstParty = isRemoteBackend\.value/,
    'Docker has no desktop catalog; only Cube/E2B auto-ensure first-party templates',
  )
})

test('saving records desktop_enabled from the selected catalog card', () => {
  assert.match(
    source,
    /function collectedDesktopEnabled/,
    'derivation must be a named helper so snapshot vs catalog-miss cannot drift',
  )
  assert.match(
    source,
    /templatesLoaded\.value && !retargetFrozen\.value/,
    'an unmatched catalog ID must not keep a stale desktop_enabled from a previous save',
  )
  assert.match(
    source,
    /effectiveRecord\.value\?\.config\?\.desktop_enabled/,
    'a skill-snapshot UUID is not a catalog card; keep the stored desktop_enabled bit',
  )
})
