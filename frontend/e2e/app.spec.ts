import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'

test('@desktop desktop authenticated knowledge home boots', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApp(page)
  await page.goto('/platform/knowledge-bases')
  await expect(page.locator('.kb-card').first()).toBeVisible()
  await fitsViewport(page)
})
