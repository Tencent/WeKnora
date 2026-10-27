package service

import "github.com/Tencent/WeKnora/internal/types"

// CanCreateRole 判断操作者能否创建指定租户角色的成员账号。
//
// 规则来自 01-spec AC-1 与 02-plan D3：
//   - owner：可创建 admin / contributor / viewer
//   - admin：只能创建 viewer
//   - contributor / viewer：不能创建任何账号
//   - 所有角色都不能创建 owner（owner 转让另立需求）
//
// 这里只做纯角色判定，不读 context：系统管理员按 owner 处理由调用方
// （handler）在传入前完成，便于单测直接覆盖矩阵本身。
func CanCreateRole(actor, target types.TenantRole) bool {
	switch actor {
	case types.TenantRoleOwner:
		return target == types.TenantRoleAdmin ||
			target == types.TenantRoleContributor ||
			target == types.TenantRoleViewer
	case types.TenantRoleAdmin:
		return target == types.TenantRoleViewer
	default:
		return false
	}
}

// CanResetRole 判断操作者能否重置指定角色成员的密码。
//
// 规则来自 01-spec AC-2 与 02-plan D3：
//   - owner：可重置 admin / contributor / viewer
//   - admin：只能重置 contributor / viewer
//   - contributor / viewer：不能重置任何人
//
// 注意：owner 之间也不能互相重置密码（同级不允许），重置本人由调用方
// 单独判定并返回 400，交给「修改密码」接口。
//
// 与 CanCreateRole 一样只做纯角色判定，不读 context：系统管理员按 owner
// 处理由 handler 在传入前完成。
func CanResetRole(actor, target types.TenantRole) bool {
	switch actor {
	case types.TenantRoleOwner:
		return target == types.TenantRoleAdmin ||
			target == types.TenantRoleContributor ||
			target == types.TenantRoleViewer
	case types.TenantRoleAdmin:
		return target == types.TenantRoleContributor ||
			target == types.TenantRoleViewer
	default:
		return false
	}
}
