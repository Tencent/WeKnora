import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(
  new URL('./SandboxConfigEditorDrawer.vue', import.meta.url), 'utf8')

test('network policy lives in the runtime step, right below the runtime config', () => {
  const runtimeSections = source.indexOf("currentStepKey === 'runtime'")
  const networkSection = source.indexOf('settings.sandbox.sectionNetwork')
  const envSection = source.indexOf('settings.sandbox.sectionEnvironment')

  assert.ok(runtimeSections !== -1, 'runtime step must exist')
  assert.ok(networkSection !== -1, 'network policy section must exist')
  assert.ok(
    networkSection > runtimeSections && networkSection < envSection,
    'network policy must sit between the runtime config and the env vars',
  )
  assert.ok(
    !source.includes("currentStepKey === 'network'"),
    'network policy must not become a fourth wizard step',
  )
})

test('runtime numbers are laid out two per row with per-field tips', () => {
  const runtimeBlock = source.slice(
    source.indexOf('settings.sandbox.sectionRuntime'),
    source.indexOf('settings.sandbox.sectionNetwork'),
  )
  assert.ok(
    runtimeBlock.includes('form-grid form-grid--two'),
    'timeouts must be compressed into the two-column grid',
  )
  // Each number keeps its own sentence; the previous layout put those in
  // block paragraphs, which is what made the step so tall.
  assert.ok(
    runtimeBlock.includes(":tips=\"$t('settings.sandbox.httpTimeoutHelp')\""),
    'http timeout keeps its own explanation, now inline',
  )
  assert.ok(
    runtimeBlock.includes(":tips=\"$t('settings.sandbox.sandboxTtlHelp')\""),
    'sandbox TTL keeps its own explanation, now inline',
  )
  for (const key of [
    'dockerCpuLimitHelp',
    'dockerMemoryLimitHelp',
    'dockerPidsLimitHelp',
  ]) {
    assert.ok(
      runtimeBlock.includes(`:tips="$t('settings.sandbox.${key}')"`),
      `${key} must remain attached to its compressed field`,
    )
  }
})

test('docker network mode moved into the network policy section', () => {
  const networkBlock = source.slice(source.indexOf('settings.sandbox.sectionNetwork'))
  assert.ok(
    networkBlock.includes('settings.sandbox.dockerNetworkMode'),
    'the docker bridge/none selector belongs with the other network controls',
  )
  const runtimeBlock = source.slice(
    source.indexOf('settings.sandbox.sectionRuntime'),
    source.indexOf('settings.sandbox.sectionNetwork'),
  )
  assert.ok(
    !runtimeBlock.includes('settings.sandbox.dockerNetworkMode'),
    'and must no longer be duplicated in the runtime config',
  )
})

test('inbound defaults to credential-required and egress to allowed', () => {
  assert.match(
    source,
    /const allowPublicInbound = ref\(false\)/,
    'inbound closed is the default',
  )
  assert.match(
    source,
    /const denyEgressByDefault = ref\(false\)/,
    'egress allowed is the default',
  )
})

test('injected header values are password inputs and survive an untouched edit', () => {
  const payloadBlock = source.slice(
    source.indexOf('function collectNetworkPolicy'),
    source.indexOf('function close'),
  )
  const templateBlock = source.slice(0, source.indexOf('<script setup'))
  assert.match(
    source,
    /<t-input v-model="inject\.secret" type="password"/,
    'Cube injected credentials must not be rendered in clear text',
  )
  assert.match(
    source,
    /<t-input v-model="header\.value" type="password"/,
    'E2B injected credentials must not be rendered in clear text',
  )
  assert.match(
    payloadBlock,
    /isStoredNetworkSecretRecoverable\(\s*inject,\s*inject\.originalRuleName,\s*inject\.originalHeader,\s*rule\.name,\s*inject\.header,\s*\)[\s\S]*?inject\.secret === ''[\s\S]*?\? secretPlaceholder/,
    'Cube payload preservation must use the shared recoverability decision',
  )
  assert.match(
    payloadBlock,
    /isStoredNetworkSecretRecoverable\(\s*header,\s*header\.originalHost,\s*header\.originalName,\s*rule\.host,\s*header\.name,\s*\)[\s\S]*?header\.value === ''[\s\S]*?\? secretPlaceholder/,
    'E2B payload preservation must use the shared recoverability decision',
  )
  assert.match(
    templateBlock,
    /isStoredNetworkSecretRecoverable\(\s*inject,\s*inject\.originalRuleName,\s*inject\.originalHeader,\s*rule\.name,\s*inject\.header,\s*\)[\s\S]*?\$t\('settings\.sandbox\.secretKeepHint'\)/,
    'Cube configured placeholder must use the shared recoverability decision',
  )
  assert.match(
    templateBlock,
    /isStoredNetworkSecretRecoverable\(\s*header,\s*header\.originalHost,\s*header\.originalName,\s*rule\.host,\s*header\.name,\s*\)[\s\S]*?\$t\('settings\.sandbox\.secretKeepHint'\)/,
    'E2B configured placeholder must use the shared recoverability decision',
  )
})

test('stored network secret recoverability requires the loaded identity', () => {
  const helper = source.match(
    /function isStoredNetworkSecretRecoverable\([\s\S]*?\): boolean \{\n([\s\S]*?)\n\}/,
  )
  assert.ok(helper, 'the shared recoverability helper must exist')
  const isRecoverable = new Function(
    'row',
    'originalParentIdentity',
    'originalChildIdentity',
    'currentParentIdentity',
    'currentChildIdentity',
    helper[1],
  )
  const storedRow = { stored: true }

  assert.equal(
    isRecoverable(storedRow, 'rule-a', 'Authorization', 'rule-a', 'Authorization'),
    true,
    'a stored row under its loaded identity keeps the placeholder',
  )
  assert.equal(
    isRecoverable(storedRow, 'rule-a', 'Authorization', 'rule-b', 'Authorization'),
    false,
    'a renamed row must request a new value',
  )
})

test('domain allow without deny-all is warned about in place', () => {
  const classifierBlock = source.slice(
    source.indexOf('const domainAllowNeedsDenyAll'),
    source.indexOf('const inFlightFromSkills'),
  )
  assert.match(
    source,
    /<t-alert v-if="domainAllowNeedsDenyAll"[\s\S]*?:message="\$t\('settings\.sandbox\.domainAllowNeedsDenyAll'\)"/,
    'the in-place warning alert must remain rendered',
  )
  assert.match(
    classifierBlock,
    /if \(denyEgressByDefault\.value\) return false/,
    'deny-by-default must suppress the warning',
  )
  assert.match(
    classifierBlock,
    /denyOutRows\.value\.some\(\(row\) => denyOutRowCoversAllIPv4\(row\)\)\) return false/,
    'an explicit deny-all row must suppress the warning',
  )
  assert.match(
    source,
    /function denyOutRowCoversAllIPv4\(row: string\): boolean \{[\s\S]*\\\/0\$/,
    'any IPv4 /0 CIDR must count as deny-all, matching the backend validator',
  )
  assert.match(
    classifierBlock,
    /return !\/\^\[0-9\.\/\]\+\$\/\.test\(value\)/,
    'only non-IP/CIDR allow rows must trigger the warning',
  )

  const helper = source.match(
    /function denyOutRowCoversAllIPv4\(row: string\): boolean \{\n([\s\S]*?)\n\}/,
  )
  assert.ok(helper, 'the deny-all classifier must exist')
  const coversAll = new Function('row', helper[1])
  assert.equal(coversAll('0.0.0.0/0'), true)
  assert.equal(coversAll('  1.2.3.4/0  '), true)
  assert.equal(coversAll('10.0.0.0/8'), false)
  assert.equal(coversAll('0.0.0.0/32'), false)
})

test('docker payload omits hidden allow and deny lists', () => {
  const payloadBlock = source.slice(
    source.indexOf('function collectNetworkPolicy'),
    source.indexOf('function close'),
  )
  assert.match(
    payloadBlock,
    /if \(backend\.value === 'docker'\) \{\s*return policy/,
    'Docker must not persist Cube/E2B radios or allow/deny rows',
  )
  assert.match(
    payloadBlock,
    /if \(backend\.value !== 'docker'\) \{[^}]*policy\.allow_out = allowOut[^}]*policy\.deny_out = denyOut[^}]*\}/,
    'Docker must not send allow/deny rows that its form hides',
  )
})

test('docker hides inbound and egress radios that it cannot honour', () => {
  const networkBlock = source.slice(
    source.indexOf('settings.sandbox.sectionNetwork'),
    source.indexOf('settings.sandbox.sectionEnvironment'),
  )
  assert.match(
    networkBlock,
    /v-if="backend !== 'docker'"[\s\S]*settings\.sandbox\.inboundAccess[\s\S]*settings\.sandbox\.egressDefault/,
    'inbound and egress radios must not render for Docker',
  )
  assert.match(
    source,
    /function selectBackend[\s\S]*crossingDocker[\s\S]*allowPublicInbound\.value = false/,
    'switching across Docker must not carry a public-inbound flag into Cube/E2B',
  )
})

test('cube L7 rules collapse to a name bar', () => {
  const templateBlock = source.slice(0, source.indexOf('<script setup'))
  const cubeBlock = templateBlock.slice(templateBlock.indexOf('settings.sandbox.cubeL7Rules'))
  const e2bBlock = cubeBlock.slice(cubeBlock.indexOf('settings.sandbox.e2bHostRules'))
  const cubeRulesBlock = cubeBlock.slice(0, cubeBlock.indexOf('settings.sandbox.e2bHostRules'))

  assert.match(
    cubeRulesBlock,
    /class="net-rule net-rule--collapsible"/,
    'Cube L7 rules use the compact collapsible card, not the always-open padding',
  )
  assert.match(
    cubeRulesBlock,
    /class="net-rule__bar"/,
    'each Cube L7 rule must render a compact name bar',
  )
  assert.match(
    cubeRulesBlock,
    /rule\.expanded \? 'chevron-down' : 'chevron-right'/,
    'the left control must expand and collapse the rule',
  )
  assert.match(
    cubeRulesBlock,
    /v-if="rule\.expanded"/,
    'rule details must stay hidden until the bar is expanded',
  )
  assert.equal(
    cubeRulesBlock.includes("settings.sandbox.removeRule"),
    false,
    'the expanded body must not grow with a second delete control',
  )
  assert.match(
    e2bBlock,
    /class="net-rule net-rule--collapsible"/,
    'E2B host rules use the same compact collapsible card',
  )
  assert.match(
    e2bBlock,
    /class="net-rule__bar"/,
    'each E2B host rule must render a compact name bar',
  )
  assert.match(
    e2bBlock,
    /v-if="rule\.expanded"/,
    'E2B rule details must stay hidden until the bar is expanded',
  )
  assert.equal(
    e2bBlock.includes("settings.sandbox.removeRule"),
    false,
    'E2B expanded body must not grow with a second delete control',
  )
  assert.match(
    source,
    /for \(const rule of cubeRules\.value\) rule\.expanded = false/,
    'adding a Cube rule must collapse the others so the list stays a stack of name bars',
  )
  assert.match(
    source,
    /for \(const rule of e2bHostRules\.value\) rule\.expanded = false/,
    'adding an E2B rule must collapse the others so the list stays a stack of name bars',
  )
  assert.match(
    source,
    /expanded: true, inject: \[\]/,
    'the Cube rule just added stays open so it can be filled in',
  )
  assert.match(
    source,
    /host: '', expanded: true, headers: \[\]/,
    'the E2B rule just added stays open so it can be filled in',
  )
  const cubeLoadBlock = source.slice(
    source.indexOf('cubeRules.value = (net.cube_rules || []).map'),
    source.indexOf('e2bHostRules.value = (net.e2b_host_rules || []).map'),
  )
  const e2bLoadBlock = source.slice(
    source.indexOf('e2bHostRules.value = (net.e2b_host_rules || []).map'),
    source.indexOf('checkResult.value = null'),
  )
  assert.match(
    cubeLoadBlock,
    /expanded: false/,
    'Cube rules loaded from a saved config start collapsed',
  )
  assert.match(
    e2bLoadBlock,
    /expanded: false/,
    'E2B rules loaded from a saved config start collapsed',
  )
})

test('deny rules omit hidden injected secrets', () => {
  const payloadBlock = source.slice(
    source.indexOf('function collectNetworkPolicy'),
    source.indexOf('function close'),
  )
  assert.match(
    payloadBlock,
    /inject: rule\.deny[\s\S]*?\? undefined[\s\S]*?: rule\.inject/,
    'a deny rule must not send inject entries hidden by the form',
  )
})

test('policy-restricted egress does not claim full outbound verification', () => {
  assert.match(
    source,
    /EGRESS_RESTRICTED_REASON = 'egress_restricted_by_policy'/,
    'the hint must recognize the skip reason the check API returns',
  )
  assert.match(
    source,
    /settings\.sandbox\.checkScopePolicyRestricted/,
    'deny-by-default must not reuse the "outbound was verified" copy',
  )
  const hintBlock = source.slice(
    source.indexOf('const checkScopeHint = computed'),
    source.indexOf('function checkDetail'),
  )
  const restrictedAt = hintBlock.indexOf('checkScopePolicyRestricted')
  const fullAt = hintBlock.indexOf('checkScopeFull')
  assert.ok(
    restrictedAt !== -1 && fullAt !== -1 && restrictedAt < fullAt,
    'policy-restricted copy must win over the full-verification claim',
  )
})
