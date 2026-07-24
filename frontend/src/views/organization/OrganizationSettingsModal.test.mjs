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
