package main

import "strings"

// Messages the form shows, in the user's language (call.Locale). Errors
// that quote Jira stay in Jira's words.
type message struct{ en, zh string }

var (
	msgConnectAccount = message{"Connect an Atlassian account first.", "请先连接 Atlassian 账号。"}
	msgChooseSite     = message{"Choose a site.", "请选择站点。"}
	msgSiteGone       = message{"The account cannot reach this site any more.", "该账号已无法访问此站点。"}
	msgTokenFields    = message{"Enter the site URL, email and API token first.", "请先填写站点地址、邮箱和 API 令牌。"}
	msgUnknownAuth    = message{"Unknown sign-in method.", "未知的登录方式。"}
	msgNoProjects     = message{"Select at least one project to sync.", "请至少选择一个要同步的项目。"}
	msgToolsNotSetUp  = message{
		"Jira is not connected for this workspace: an admin sets it up in Settings → Plugins → Jira.",
		"本空间尚未连接 Jira：请管理员在「设置 → 插件 → Jira」中配置。",
	}
)

func say(locale string, m message) string {
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		return m.zh
	}
	return m.en
}
