import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const referenceDrawer = readFileSync(
  new URL('../../../components/ChatReferencesDrawer.vue', import.meta.url),
  'utf8',
)
const legacyReferences = readFileSync(new URL('./docInfo.vue', import.meta.url), 'utf8')
const agentStream = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')

test('reference document links open in a new tab', () => {
  assert.match(
    referenceDrawer,
    /:href="getDocumentHref\(item\)"[\s\S]*?target="_blank"[\s\S]*?rel="noopener noreferrer"/,
  )
  assert.match(
    legacyReferences,
    /:href="getDocumentHref\(group\)"[\s\S]*?target="_blank"[\s\S]*?rel="noopener noreferrer"/,
  )
})

test('wiki drawer navigation and citation fallbacks open in a new tab', () => {
  assert.match(
    agentStream,
    /:href="wikiGraphHref"[\s\S]*?target="_blank"[\s\S]*?rel="noopener noreferrer"/,
  )
  assert.match(agentStream, /window\.open\(href, '_blank', 'noopener,noreferrer'\)/)
  assert.doesNotMatch(agentStream, /router\.push\(/)
})
