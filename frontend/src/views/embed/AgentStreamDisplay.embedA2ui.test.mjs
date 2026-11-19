import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

// Source pins for the embed (iframe) A2UI wiring of the agent answer surface.
// Behaviour lives in the component; what this file refuses is the specific
// regressions that created the bug in the first place: the two embeddedMode
// gates that (until 2026-11-08) turned every embed card into raw JSON.
// The ```a2ui FENCE renderer stays gated on purpose — the taught official
// form is <a2ui-json> tags, which markA2uiTags handles; only that path is
// un-gated here.
const source = readFileSync(
  new URL('../chat/components/AgentStreamDisplay.vue', import.meta.url), 'utf8',
)

test('the answer renderer always marks a2ui tag blocks (embed gate removed)', () => {
  assert.ok(
    source.includes('markA2uiTags: markA2uiTagBlocks,'),
    'markA2uiTags must be unconditional again',
  )
  assert.ok(
    !source.includes('markA2uiTags: props.embeddedMode ? undefined : markA2uiTagBlocks'),
    'the embeddedMode renderer gate is back — embed would leak raw a2ui JSON',
  )
})

test('mount no longer returns early on embeddedMode and forwards the action client', () => {
  const m = source.match(/function mountA2uiSurfacesIfDone\(\): void \{([\s\S]*?)\n\}/)
  assert.ok(m, 'mountA2uiSurfacesIfDone not found (rename breaks embed)')
  const body = m[1]
  assert.ok(!body.includes('props.embeddedMode'), 'embed mount gate is back')
  assert.ok(
    body.includes('mountA2uiSurfaces(rootElement.value, a2uiSessionId(), a2uiMountId, embedActionClient.value)'),
    'the mount call must forward the embed action client',
  )
})

test('the embed action client exists and guards on all three channel credentials', () => {
  assert.ok(
    source.includes("from '@/api/embed/action'"),
    'embedActionClient must come from the tiny action module, not the bridge-laden index',
  )
  const m = source.match(/const embedActionClient = computed[\s\S]*?\n\}\);/)
  assert.ok(m, 'embedActionClient computed not found')
  const body = m[0]
  for (const k of ['embedToken', 'embedChannelId', 'embedSessionSig']) {
    assert.ok(body.includes(`props.${k}`), `missing guard on props.${k}`)
  }
  // The session forwarded is the session the card belongs to.
  assert.ok(body.includes('props.sessionId'))
})
