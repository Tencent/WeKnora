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

test('organization settings use one outer content scroller and reset it on navigation', () => {
  assert.match(source, /ref="contentWrapperRef" class="content-wrapper"/)
  assert.match(source, /class="data-table-shell members-table-shell"/)
  assert.match(source, /contentWrapperRef\.value\?\.scrollTo\(\{ top: 0, behavior: 'auto' \}\)/)
  assert.match(source, /watch\(\(\) => props\.visible,[\s\S]*?void scrollContentToTop\(\)/)
  assert.match(source, /\}, \{ immediate: true \}\)\s*\n\s*watch\(\(\) => props\.orgId/)
  assert.match(source, /watch\(\(\) => props\.orgId,[\s\S]*?void scrollContentToTop\(\)/)
  assert.match(source, /watch\(currentSection,[\s\S]*?void scrollContentToTop\(\)/)
  assert.match(source, /\.members-table-shell\s*\{[\s\S]*?overflow: visible;[\s\S]*?\.t-table__content/)
})

test('organization settings lock and restore background scrolling', () => {
  assert.match(source, /document\.body\.style\.overflow = 'hidden'/)
  assert.match(source, /document\.body\.style\.overflow = previousBodyOverflow/)
  assert.match(source, /onBeforeUnmount\(\(\) => \{\s*unlockBackgroundScroll\(\)/)
  assert.match(source, /\.settings-overlay\s*\{[\s\S]*?overscroll-behavior: none;/)
  assert.match(source, /\.content-wrapper\s*\{[\s\S]*?overscroll-behavior: contain;/)
})
