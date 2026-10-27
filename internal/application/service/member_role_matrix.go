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
