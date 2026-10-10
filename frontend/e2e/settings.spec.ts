import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
// Phone projects use isMobile:true; desktop cases run in a separate project.


test('mobile settings directory, detail and back restore directory focus', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/settings')
  const shell = page.locator('.settings-modal-shell')
  await expect(shell).toBeVisible()
  expect(await page.evaluate(() => innerWidth)).toBe(390)
  const sidebar = (await shell.locator('.settings-sidebar').boundingBox())!
  for (const item of await shell.locator('.nav-item').all()) {
    const bounds = (await item.boundingBox())!
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(sidebar.x + sidebar.width)
  }
  await shell.locator('.nav-item').first().click()
  await expect(shell.locator('.settings-content')).toBeVisible()
  await expect(shell.locator('.settings-sidebar')).toBeHidden()
  await shell.locator('.mobile-settings-back').click()
  await expect(shell.locator('.settings-sidebar')).toBeVisible()
})


test('knowledge settings preserve edits across directory navigation and save', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockApp(page)
  await page.goto('/platform/knowledge-bases/mobile-kb')
  await page.getByRole('button', { name: '设置', exact: true }).click()
  const shell = page.locator('.settings-modal-shell')
  await shell.locator('.nav-item').first().click()
  const name = shell.locator('[data-guide="kb-create-name"] input')
  await name.fill('庄子手机编辑测试')
  await shell.locator('.mobile-settings-back').click()
  await expect(shell.locator('.nav-item').first()).toBeFocused()
  await shell.locator('.nav-item').first().click()
  await expect(name).toHaveValue('庄子手机编辑测试')
  const save = shell.locator('.settings-footer-actions button').last()
  await expect(save).toBeInViewport()
  const update = page.waitForRequest(request => request.url().endsWith('/knowledge-bases/mobile-kb') && request.method() === 'PUT')
  await save.click()
  expect((await update).postDataJSON().name).toBe('庄子手机编辑测试')
  await expect(shell).toBeHidden()
})

test('@desktop desktop settings keep the existing two-column layout', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockApp(page)
  await page.goto('/platform/settings')
  const shell = page.locator('.settings-modal-shell')
  await expect(shell.locator('.settings-sidebar')).toBeVisible()
  await expect(shell.locator('.settings-content')).toBeVisible()
  await expect(shell.locator('.mobile-settings-back')).toBeHidden()
})
