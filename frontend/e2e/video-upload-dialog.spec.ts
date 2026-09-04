import { expect, test } from '@playwright/test'

test('upload dialog overlay covers the home navigation', async ({ page, baseURL }) => {
  await page.addInitScript(() => {
    localStorage.setItem('weknora_token', 'e2e-upload-dialog-token')
    localStorage.setItem('weknora_user', JSON.stringify({
      id: 'e2e-upload-dialog-user',
      username: 'e2e-user',
      email: 'e2e@example.test',
      tenant_id: '1',
      is_active: true,
      created_at: '2026-01-01T00:00:00.000Z',
      updated_at: '2026-01-01T00:00:00.000Z',
    }))
    localStorage.setItem('weknora_tenant', JSON.stringify({
      id: '1',
      name: 'E2E Space',
      owner_id: 'e2e-upload-dialog-user',
      created_at: '2026-01-01T00:00:00.000Z',
      updated_at: '2026-01-01T00:00:00.000Z',
    }))
  })
  await page.route('**/api/v1/**', route => {
    const pathname = new URL(route.request().url()).pathname
    let response: Record<string, unknown> = { success: true, data: {} }
    if (pathname.endsWith('/auth/me')) {
      response = {
        success: true,
        data: {
          user: {
            id: 'e2e-upload-dialog-user',
            username: 'e2e-user',
            email: 'e2e@example.test',
            tenant_id: 1,
            is_active: true,
          },
          tenant: { id: 1, name: 'E2E Space', owner_id: 'e2e-upload-dialog-user' },
          memberships: [{ tenant_id: 1, tenant_name: 'E2E Space', role: 'owner' }],
          capabilities: { can_create_tenant: false, auto_accept_invitation: false },
        },
      }
    } else if (pathname.endsWith('/system/info')) {
      response = { data: { version: 'e2e', edition: 'standard' } }
    } else if (pathname.endsWith('/system/capabilities')) {
      response = {
        data: {
          edition: 'standard',
          capabilities: { agents: { supported: true }, organizations: { supported: true } },
        },
      }
    }
    return route.fulfill({
    status: 200,
    contentType: 'application/json',
      body: JSON.stringify(response),
    })
  })
  await page.goto(`${baseURL || 'http://127.0.0.1'}/platform/videos`, { waitUntil: 'domcontentloaded' })

  const uploadButton = page.getByRole('button', { name: '上传视频', exact: true }).first()
  await expect(uploadButton).toBeVisible()
  await uploadButton.click()
  await expect(page.locator('.video-upload-dialog')).toBeVisible()

  const layerCheck = await page.evaluate(() => {
    const menu = document.querySelector<HTMLElement>('.aside_box')
    const dialogLayer = [...document.querySelectorAll<HTMLElement>('.t-dialog__ctx--fixed')]
      .find(element => {
        const rect = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden'
      })
    const mask = dialogLayer?.querySelector<HTMLElement>('.t-dialog__mask')
    if (!menu || !dialogLayer || !mask) return null

    const menuRect = menu.getBoundingClientRect()
    const point = {
      x: Math.max(1, menuRect.left + Math.min(20, menuRect.width / 2)),
      y: Math.max(1, menuRect.top + Math.min(80, menuRect.height / 2)),
    }
    const hit = document.elementFromPoint(point.x, point.y)
    const maskRect = mask.getBoundingClientRect()
    return {
      maskRect: {
        x: maskRect.x,
        y: maskRect.y,
        width: maskRect.width,
        height: maskRect.height,
      },
      point,
      hitIsMenu: Boolean(hit && (hit === menu || menu.contains(hit))),
      hitIsDialogLayer: Boolean(hit && dialogLayer.contains(hit)),
      dialogLayerZIndex: Number.parseInt(getComputedStyle(dialogLayer).zIndex, 10),
      menuZIndex: Number.parseInt(getComputedStyle(menu).zIndex, 10),
    }
  })

  expect(layerCheck).not.toBeNull()
  expect(layerCheck?.maskRect.width).toBeGreaterThanOrEqual(1)
  expect(layerCheck?.maskRect.height).toBeGreaterThanOrEqual(1)
  expect(layerCheck?.hitIsMenu).toBe(false)
  expect(layerCheck?.hitIsDialogLayer).toBe(true)
  expect(layerCheck?.dialogLayerZIndex).toBeGreaterThan(layerCheck?.menuZIndex ?? 0)
})
