package api

import (
	"context"
	"strconv"

	"github.com/Tencent/WeKnora/internal/types"
)

// HeaderVariableNames 列出自定义请求头支持插值的动态变量。
//
// 这些变量在每次请求发出前从当前请求上下文取值，所以同一份模型配置
// 可以给不同用户打上不同的身份头——这是静态 header 做不到的。
var HeaderVariableNames = []string{
	"user_id",
	"user_name",
	"user_email",
	"tenant_id",
	"tenant_name",
	"session_id",
	"request_id",
}

// HeaderVariables 从请求上下文收集动态请求头可用的变量取值。
//
// 取不到的变量不会出现在结果里，调用方据此知道哪些占位符会展开为空。
//
// user_name / user_email 来自完整用户对象，因此只有解析出了真实账号的
// 请求才有它们：API Key、IM 机器人、嵌入访客这些主体没有用户行，只有
// user_id 可用。
func HeaderVariables(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	vars := make(map[string]string, len(HeaderVariableNames))
	if uid, ok := types.UserIDFromContext(ctx); ok {
		vars["user_id"] = uid
	}
	if user, ok := types.UserFromContext(ctx); ok {
		vars["user_name"] = user.Username
		vars["user_email"] = user.Email
	}
	if tenantID, ok := types.TenantIDFromContext(ctx); ok {
		vars["tenant_id"] = strconv.FormatUint(tenantID, 10)
	}
	if tenant, ok := types.TenantInfoFromContext(ctx); ok {
		vars["tenant_name"] = tenant.Name
	}
	if sessionID, ok := types.SessionIDFromContext(ctx); ok {
		vars["session_id"] = sessionID
	}
	if requestID, ok := types.RequestIDFromContext(ctx); ok {
		vars["request_id"] = requestID
	}
	return vars
}
