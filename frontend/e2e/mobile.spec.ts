import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'

for (const width of [360, 390, 430, 768, 1024, 1440]) {
  test(`navigation and knowledge list fit ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockApp(page)
    await page.goto('/platform/knowledge-bases')
    await expect(page.locator('.kb-list-container')).toBeVisible()
    await fitsViewport(page)
    await page.screenshot({ path: `test-results/knowledge-${width}.png` })
    if (width < 768) {
      await expect(page.locator('.platform-navigation')).toBeHidden()
      await page.locator('.mobile-app-header button').first().click()
      await expect(page.locator('.platform-navigation')).toBeVisible()
      await expect(page.locator('.platform-navigation .logo_row')).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(page.locator('.platform-navigation')).toBeHidden()
      await expect(page.locator('.mobile-app-header button').first()).toBeFocused()
      expect(await page.evaluate(() => localStorage.getItem('sidebar_collapsed'))).toBe('true')
    }
  })
}

for (const route of ['agents', 'organizations', 'artifacts', 'toolbox']) {
  test(`mobile ${route} has no horizontal overflow`, async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 780 })
    await mockApp(page)
    await page.goto(`/platform/${route}`)
    await expect(page.locator('.platform-route-outlet')).toBeVisible()
    await fitsViewport(page)
  })
}

test('knowledge cards open documents even when model setup is incomplete', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/knowledge-bases', route => route.fulfill({ json: { success: true, data: [{ id: 'mobile-kb', name: '未配置模型的知识库', type: 'document', tenant_id: 1, knowledge_count: 1 }] } }))
  await page.goto('/platform/knowledge-bases')
  await page.locator('.kb-card').first().click()
  await expect(page).toHaveURL(/\/knowledge-bases\/mobile-kb/)
  await expect(page.locator('.document-header')).toBeVisible()
})

for (const width of [360, 390, 430]) {
  test(`sidebar account popup and settings stay usable at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await mockApp(page)
    await page.goto('/platform/creatChat')
    await page.locator('.mobile-app-header button').first().click()
    await page.locator('[data-guide="user-menu"]').click()
    const popup = page.locator('.user-dropdown')
    await expect(popup).toBeVisible()
    const bounds = await popup.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    await popup.locator('.menu-item').filter({ hasText: '全部设置' }).click()
    await expect(page.locator('.platform-navigation')).toBeHidden()
    const shell = page.locator('.settings-modal-shell')
    await expect(shell).toBeVisible()
    await expect(shell.locator('.settings-sidebar')).toBeVisible()
    await shell.locator('.nav-item').first().click()
    await expect(shell.locator('.settings-content')).toBeVisible()
    await fitsViewport(page)
  })
}
