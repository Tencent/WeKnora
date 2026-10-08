import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

for (const width of [360, 390, 430, 768, 1024, 1440]) {
 test(`navigation overlay and desktop preference at ${width}px`, async ({ page }) => {
  await page.setViewportSize({ width, height: 844 }); await mockApp(page)
  await page.goto('/platform/knowledge-bases')
  if (width < 768) {
   await expect(page.locator('.platform-navigation')).toBeHidden()
   await page.locator('.mobile-app-header button').first().click()
   await expect(page.locator('.platform-navigation .logo_row')).toBeVisible()
   await page.keyboard.press('Escape')
   await expect(page.locator('.platform-navigation')).toBeHidden()
   await expect(page.locator('.mobile-app-header button').first()).toBeFocused()
   await page.locator('.mobile-app-header button').first().click()
   await page.locator('[data-guide="nav-creatChat"]').click()
   await expect(page).toHaveURL(/creatChat/)
   await expect(page.locator('.platform-navigation')).toBeHidden()
  } else {
   await expect(page.locator('.mobile-app-header')).toBeHidden()
   await expect(page.locator('.platform-navigation')).toBeVisible()
   await expect(page.locator('.aside_box')).toHaveClass(/aside_box--collapsed/)
  }
  expect(await page.evaluate(() => localStorage.getItem('sidebar_collapsed'))).toBe('true')
 })
}
