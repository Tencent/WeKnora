import { test, expect } from '@playwright/test'
import { mockApp, fitsViewport } from './fixtures'
test.use({ hasTouch: true })

test('knowledge cards open documents even when model setup is incomplete', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 844 })
  await mockApp(page)
  await page.route('**/api/v1/knowledge-bases', route => route.fulfill({ json: { success: true, data: [{ id: 'mobile-kb', name: '未配置模型的知识库', type: 'document', tenant_id: 1, knowledge_count: 1 }] } }))
  await page.goto('/platform/knowledge-bases')
  await page.locator('.kb-card').first().getByText('未配置模型的知识库', { exact: true }).click()
  await expect(page).toHaveURL(/\/knowledge-bases\/mobile-kb/)
  await expect(page.locator('.document-header')).toBeVisible()
})
