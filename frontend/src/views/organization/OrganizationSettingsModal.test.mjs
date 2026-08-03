import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./OrganizationSettingsModal.vue', import.meta.url), 'utf8')

test('reviewing join requests goes through the store and refreshes modal data', () => {
  assert.match(
    source,
    /const refreshOrganizationAfterReview = async \(\) => \{\s*await Promise\.all\(\[\s*fetchOrgDetail\(\),\s*fetchMembers\(\)\s*\]\)\s*\}/
  )

  assert.match(source, /orgStore\.reviewOrganizationJoinRequest\(/)
  const refreshCalls = source.match(/await refreshOrganizationAfterReview\(\)/g) ?? []
  assert.equal(refreshCalls.length, 2)
})

test('space settings labels every KB source and protects every agent-carried KB from removal', () => {
  assert.match(source, /listOrganizationSharedKnowledgeBases\(props\.orgId\)/)
  assert.match(source, /toSettingsKnowledgeBaseRows\(directShares, spaceKbRes\.data\)/)
  assert.match(source, /class="knowledge-base-source">\{\{ knowledgeBaseSourceLabel\(row\) \}\}/)
  assert.match(source, /organization\.settings\.directKbSource/)
  assert.match(source, /organization\.settings\.agentKbSource/)
  assert.match(source, /organization\.settings\.directAndAgentKbSource/)
  assert.match(source, /v-if="isAdmin && !hasAgentSources\(row\)"/)
  assert.match(source, /if \(!props\.orgId \|\| hasAgentSources\(share\)\) return/)
})

test('shared KB permission guidance is visible without hover', () => {
  assert.match(source, /class="section-description shared-kb-permission-tip">\s*\{\{ \$t\('organization\.settings\.permissionCalcFormula'\) \}\}/)
  const sharedKbHeader = source.slice(source.indexOf('<!-- 共享知识库 -->'), source.indexOf('<!-- 共享智能体 -->'))
  assert.doesNotMatch(sharedKbHeader, /trigger="hover"/)
})
