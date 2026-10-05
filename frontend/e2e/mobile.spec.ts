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

