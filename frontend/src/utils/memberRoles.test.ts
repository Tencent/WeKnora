import assert from 'node:assert/strict'
import test from 'node:test'

import { allowedCreateRoles, canCreateAccount, canResetTarget } from './memberRoles.ts'
import type { TenantRole } from '@/api/tenant/members'

/**
 * 覆盖 01-spec AC-1 / AC-2 / AC-9 / AC-11 的前端权限镜像。
 * 口径必须与后端 service.CanCreateRole / CanResetRole 逐格一致（02-plan D3）：
 *   - Owner（含跨租户系统管理员）：建 admin/contributor/viewer；重置 admin/contributor/viewer
 *   - Admin：建 viewer；重置 contributor/viewer
 *   - Contributor / Viewer：无权限
 *   - 本人一律不能重置
 */

const ROLES: TenantRole[] = ['owner', 'admin', 'contributor', 'viewer']

test('allowedCreateRoles matches the AC-1 matrix exactly', () => {
  assert.deepEqual(allowedCreateRoles('owner'), ['admin', 'contributor', 'viewer'])
  assert.deepEqual(allowedCreateRoles('admin'), ['viewer'])
  assert.deepEqual(allowedCreateRoles('contributor'), [])
  assert.deepEqual(allowedCreateRoles('viewer'), [])
})

test('allowedCreateRoles fails closed for missing or unknown actors', () => {
  for (const actor of ['', null, undefined]) {
    assert.deepEqual(allowedCreateRoles(actor as TenantRole), [], `actor=${String(actor)}`)
  }
  assert.deepEqual(allowedCreateRoles('wizard' as TenantRole), [])
})

test('allowedCreateRoles never offers the owner role', () => {
  for (const actor of ROLES) {
    assert.equal(allowedCreateRoles(actor).includes('owner'), false, `${actor} must not create owner`)
  }
})

test('canCreateAccount is true only for owner and admin', () => {
  const want: Record<string, boolean> = {
    owner: true,
    admin: true,
    contributor: false,
    viewer: false,
  }
  for (const actor of ROLES) {
    assert.equal(canCreateAccount(actor), want[actor], `actor=${actor}`)
  }
  for (const actor of ['', null, undefined]) {
    assert.equal(canCreateAccount(actor as TenantRole), false, `actor=${String(actor)}`)
  }
})

test('canResetTarget matches the AC-2 matrix (actor x target)', () => {
  // 行 = 操作者，列 = 目标角色；false 覆盖「同级/上级不可重置」。
  const want: Record<TenantRole, Record<TenantRole, boolean>> = {
    owner: {
      owner: false,
      admin: true,
      contributor: true,
      viewer: true,
    },
    admin: {
      owner: false,
      admin: false,
      contributor: true,
      viewer: true,
    },
    contributor: {
      owner: false,
      admin: false,
      contributor: false,
      viewer: false,
    },
    viewer: {
      owner: false,
      admin: false,
      contributor: false,
      viewer: false,
    },
  }

  for (const actor of ROLES) {
    for (const target of ROLES) {
      const expected = want[actor][target]
      assert.equal(
        canResetTarget(actor, target, false),
        expected,
        `canResetTarget(${actor}, ${target}, isSelf=false)`,
      )
    }
  }
})

test('canResetTarget always rejects self, even when the role would allow it', () => {
  for (const actor of ROLES) {
    for (const target of ROLES) {
      assert.equal(
        canResetTarget(actor, target, true),
        false,
        `canResetTarget(${actor}, ${target}, isSelf=true) must be false`,
      )
    }
  }
})

test('canResetTarget fails closed for missing or unknown roles', () => {
  for (const actor of ['', null, undefined, 'wizard']) {
    for (const target of ROLES) {
      assert.equal(
        canResetTarget(actor as TenantRole, target, false),
        false,
        `actor=${String(actor)} target=${target}`,
      )
    }
  }
  for (const target of ['', null, undefined, 'wizard']) {
    assert.equal(
      canResetTarget('owner', target as TenantRole, false),
      false,
      `target=${String(target)}`,
    )
  }
})
