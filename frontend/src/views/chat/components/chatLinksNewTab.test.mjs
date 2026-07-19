import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const referenceDrawer = readFileSync(
  new URL('../../../components/ChatReferencesDrawer.vue', import.meta.url),
  'utf8',
)
const legacyReferences = readFileSync(new URL('./docInfo.vue', import.meta.url), 'utf8')
const agentStream = readFileSync(new URL('./AgentStreamDisplay.vue', import.meta.url), 'utf8')
const chatView = readFileSync(new URL('../index.vue', import.meta.url), 'utf8')

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

test('agent citations recover drawer references from retrieval tool events', () => {
  assert.match(
    agentStream,
    /const getReferencesForDrawer = \([\s\S]*?props\.session\?\.knowledge_references[\s\S]*?props\.session\?\.agentEventStream[\s\S]*?getToolReferenceItems\(event\)/,
  )
  assert.match(
    agentStream,
    /const refs = getReferencesForDrawer\(refsOverride\)[\s\S]*?referencesDrawer\.open\(/,
  )
  assert.match(
    agentStream,
    /chunk_ids: group\.chunks\.map\(\(chunk\) => chunk\.chunk_id\)\.filter\(Boolean\)/,
  )
  assert.equal(
    agentStream.match(/knowledgeReferences: getReferencesForDrawer\(\)|getReferencesForDrawer\(\),/g)?.length,
    3,
  )
  assert.match(
    referenceDrawer,
    /watch\(highlight,[\s\S]*?scrollToHighlight\(\)/,
  )
  assert.equal(
    agentStream.match(/documentTitle: title,[\s\S]*?knowledgeBaseId: kbId,/g)?.length,
    2,
  )
})

test('citation highlighting waits for drawer entry and only scrolls its own body', () => {
  assert.match(referenceDrawer, /@after-enter="handlePanelAfterEnter"/)
  assert.match(referenceDrawer, /if \(!panelEntered\.value\) return/)
  assert.match(referenceDrawer, /container\.scrollTo\(\{ top: Math\.max\(0, nextTop\), behavior: 'smooth' \}\)/)
  assert.doesNotMatch(referenceDrawer, /el\.scrollIntoView\(/)
})

test('references drawer smoothly shifts the chat area while opening', () => {
  assert.match(referenceDrawer, /\.chat-references-panel \{[\s\S]*?position: fixed;/)
  assert.match(chatView, /'has-references-panel': referencesDrawerVisible/)
  assert.match(chatView, /transition: padding-right 0\.3s cubic-bezier\(0\.22, 0\.61, 0\.36, 1\)/)
  assert.match(chatView, /&\.has-references-panel:not\(\.is-embedded\)[\s\S]*?padding-right:\s*420px/)
})
