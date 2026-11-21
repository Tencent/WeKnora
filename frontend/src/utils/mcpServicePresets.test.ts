import assert from 'node:assert/strict'
import test from 'node:test'
import {
  MCP_SERVICE_PRESETS, filterMcpPresets, mcpPresetDraft, mcpPresetBearer, mcpPresetPlatformLink,
} from './mcpServicePresets.ts'

test('service-name discovery finds the independent integration and preserves other choices', () => {
  for (const term of ['Suno', 'Seedance', 'Happy Horse', 'GPT Image', 'Nano Banana']) {
    const matches = filterMcpPresets(term)
    assert.equal(matches.length, 1, term)
    assert.match(matches[0]!.name, /by Ace Data Cloud$/)
  }
  assert.equal(filterMcpPresets('video').length, 2)
  assert.equal(filterMcpPresets('图片').length, 2)
  assert.equal(filterMcpPresets('suno video').length, 0)
  assert.equal(filterMcpPresets('').length, 5)
})

test('presets contain only reviewed public endpoints and create private-key drafts', () => {
  assert.equal(new Set(MCP_SERVICE_PRESETS.map(p => p.id)).size, 5)
  for (const preset of MCP_SERVICE_PRESETS) {
    const draft = mcpPresetDraft(preset, 'en-US')
    assert.equal(new URL(draft.url).protocol, 'https:')
    assert.match(new URL(draft.url).hostname, /\.mcp\.acedata\.cloud$/)
    assert.equal(new URL(draft.url).search, '')
    assert.equal(draft.transport_type, 'http-streamable')
    assert.deepEqual(draft.auth_config, { auth_type: 'api_key', api_key_header: 'Authorization', api_key: '' })
    assert.match(draft.usage_instructions, /billable/)
    assert.match(draft.usage_instructions, /task ID/)
    assert.match(mcpPresetDraft(preset, 'zh-TW').usage_instructions, /计费/)
  }
})

test('credential values are normalized once without creating a placeholder credential', () => {
  assert.equal(mcpPresetBearer(''), '')
  assert.equal(mcpPresetBearer('   '), '')
  assert.equal(mcpPresetBearer('  test-key  '), 'Bearer test-key')
  assert.equal(mcpPresetBearer('Bearer test-key'), 'Bearer test-key')
  assert.equal(mcpPresetBearer('bearer   test-key'), 'Bearer test-key')
})

test('onboarding links preserve pricing and carry the accepted four attribution fields', () => {
  for (const preset of MCP_SERVICE_PRESETS) {
    for (const placement of ['key', 'pricing'] as const) {
      const url = new URL(mcpPresetPlatformLink(preset, placement))
      assert.equal(url.hostname, 'platform.acedata.cloud')
      assert.equal(url.searchParams.get('utm_source'), 'opensource')
      assert.equal(url.searchParams.get('utm_medium'), 'referral')
      assert.equal(url.searchParams.get('utm_campaign'), 'evergreen')
      assert.match(url.searchParams.get('utm_content')!, /^weknora_mcp_[a-z0-9_]+_(key|pricing)$/)
      if (placement === 'pricing') assert.equal(url.pathname, '/models')
      else assert.equal(url.pathname, '/console/applications')
    }
  }
})
