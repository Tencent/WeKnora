import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const listSource = readFileSync(new URL('./OrganizationList.vue', import.meta.url), 'utf8')
const settingsSource = readFileSync(new URL('./OrganizationSettingsModal.vue', import.meta.url), 'utf8')
const kbShareSource = readFileSync(
  new URL('../knowledge/settings/KBShareSettings.vue', import.meta.url),
  'utf8'
)
const agentShareSource = readFileSync(
  new URL('../../components/AgentShareSettings.vue', import.meta.url),
  'utf8'
)
const storeSource = readFileSync(
  new URL('../../stores/organization.ts', import.meta.url),
  'utf8'
)

test('organization list directly derives cards from the store', () => {
  assert.match(
    listSource,
    /const organizations = computed<OrgWithUI\[\]>\(\(\) => orgStore\.organizations\)/
  )
  assert.doesNotMatch(listSource, /organizations\.value = newOrgs\.map/)
})

test('join flows and searchable cache are coordinated by the store', () => {
  assert.match(listSource, /orgStore\.join\(inviteCode\.value\)/)
  assert.match(listSource, /orgStore\.joinById\(/)
  assert.match(listSource, /orgStore\.fetchSearchableOrganizations\(/)
  assert.doesNotMatch(listSource, /const searchCache = ref/)
})

test('organization settings writes use store actions', () => {
  for (const action of [
    'updateOrganization',
    'reviewOrganizationJoinRequest',
    'inviteOrganizationMember',
    'requestOrganizationRoleUpgrade',
    'unshareKnowledgeBase',
    'unshareAgent'
  ]) {
    assert.match(settingsSource, new RegExp(`orgStore\\.${action}\\(`))
  }
})

test('knowledge base and agent share editors use store actions', () => {
  assert.match(kbShareSource, /orgStore\.shareKnowledgeBase\(/)
  assert.match(kbShareSource, /orgStore\.unshareKnowledgeBase\(/)
  assert.match(kbShareSource, /orgStore\.changeKnowledgeBaseSharePermission\(/)
  assert.match(agentShareSource, /orgStore\.shareAgent\(/)
  assert.match(agentShareSource, /orgStore\.unshareAgent\(/)
})

test('store invalidates all affected organization caches after sharing', () => {
  assert.match(
    storeSource,
    /invalidateOrganizationData\(\{ sharedKnowledgeBases: true \}\)/
  )
  assert.match(
    storeSource,
    /invalidateOrganizationData\(\{ sharedAgents: true, sharedKnowledgeBases: true \}\)/
  )
  assert.match(storeSource, /adjustOrganizationResourceCount\(/)
})
