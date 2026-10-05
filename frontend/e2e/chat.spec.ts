import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'

test('mobile composer expands tools and keeps draft after viewport change', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/creatChat')
  await expect(page.locator('.control-bar')).toBeVisible()
  await page.locator('.mobile-composer-more').click()
  await expect(page.locator('.control-left')).toBeVisible()
  await page.locator('.mobile-composer-more').click()
  await expect(page.locator('.control-left')).toBeHidden()
  const input = page.locator('[data-guide="chat-input"] textarea')
  await input.fill('庄子的逍遥是什么意思？')
  await page.setViewportSize({ width: 390, height: 420 })
  await expect(input).toHaveValue('庄子的逍遥是什么意思？')
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
  await fitsViewport(page)
})

test('landscape and enlarged text keep navigation and composer reachable', async ({ page }) => {
  await mockApp(page)
  await page.setViewportSize({ width: 740, height: 390 })
  await page.goto('/platform/creatChat')
  await page.evaluate(() => { document.documentElement.style.zoom = '1.2'; window.dispatchEvent(new Event('resize')) })
  await expect(page.locator('.mobile-app-header')).toBeVisible()
  await expect(page.locator('[data-guide="chat-send"]')).toBeInViewport()
  await fitsViewport(page)
})


test('desktop composer keeps tools visible without a mobile toggle', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApp(page)
  await page.goto('/platform/creatChat')
  await expect(page.locator('.control-left')).toBeVisible()
  await expect(page.locator('.mobile-composer-more')).toBeHidden()
  await fitsViewport(page)
})
