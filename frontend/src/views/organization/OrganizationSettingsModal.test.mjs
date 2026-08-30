import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('./OrganizationSettingsModal.vue', import.meta.url), 'utf8')
const zhCN = readFileSync(new URL('../../i18n/locales/zh-CN.ts', import.meta.url), 'utf8')
const enUS = readFileSync(new URL('../../i18n/locales/en-US.ts', import.meta.url), 'utf8')

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

test('member roster presents workspace identity instead of representative user identity', () => {
  assert.match(source, /row\.tenant_id === authStore\.currentTenantId/)
  assert.doesNotMatch(source, /row\.user_id === authStore\.currentUserId/)
  assert.match(
    source,
    /return m\.tenant_name \|\| `\$\{t\('organization\.members\.columns\.member'\)\} #\$\{m\.tenant_id\}`/
  )
  assert.match(source, /return m\.username \|\| ''/)
  assert.match(zhCN, /listTitle: '成员空间'/)
  assert.match(zhCN, /成员列表按空间展示，每个空间只显示一行/)
  assert.match(enUS, /listTitle: 'Member workspaces'/)
  assert.match(enUS, /The member list shows one row per workspace/)
})
