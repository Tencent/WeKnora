package datasource

import (
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
	"github.com/Tencent/WeKnora/internal/types"
)

// connectorForm is what the data source editor needs to set one connector up:
// the setup guide and the credential form. These lived in the frontend until
// the editor moved to config schemas; keeping them next to the connector means
// a new connector needs no frontend change.
type connectorForm struct {
	docURL              string
	permissionDocURL    string
	permissionPageURL   string
	requiredPermissions []string
	schema              func() *configschema.Schema
}

// withForm fills a connector's setup guide and credential schema into its
// metadata. Connectors without a form (not implemented yet) are unchanged.
func withForm(meta ConnectorMetadata) ConnectorMetadata {
	form, ok := connectorForms[meta.Type]
	if !ok {
		return meta
	}
	meta.DocURL = form.docURL
	meta.PermissionDocURL = form.permissionDocURL
	meta.PermissionPageURL = form.permissionPageURL
	meta.RequiredPermissions = append([]string(nil), form.requiredPermissions...)
	meta.ConfigSchema = form.schema()
	return meta
}

// Scopes the Feishu / Lark apps must be granted. Lark is Feishu's
// international cloud: same APIs and scope identifiers, separate console.
var (
	feishuWikiPermissions = []string{
		"wiki:wiki:readonly", "drive:drive:readonly", "drive:export:readonly", "docx:document:readonly",
	}
	feishuDrivePermissions = []string{
		"drive:drive:readonly", "drive:export:readonly", "docx:document:readonly",
	}
)

var connectorForms = map[string]connectorForm{
	types.ConnectorTypeFeishu: {
		docURL:              "https://open.feishu.cn/app",
		permissionDocURL:    "https://open.feishu.cn/document/server-docs/docs/wiki-v2/wiki-overview",
		permissionPageURL:   "https://open.feishu.cn/app",
		requiredPermissions: feishuWikiPermissions,
		schema:              func() *configschema.Schema { return feishuSchema("https://open.feishu.cn") },
	},
	types.ConnectorTypeLark: {
		docURL:              "https://open.larksuite.com/app",
		permissionDocURL:    "https://open.larksuite.com/document/server-docs/docs/wiki-v2/wiki-overview",
		permissionPageURL:   "https://open.larksuite.com/app",
		requiredPermissions: feishuWikiPermissions,
		schema:              func() *configschema.Schema { return feishuSchema("https://open.larksuite.com") },
	},
	// Drive mode syncs a folder: no wiki scope needed.
	types.ConnectorTypeFeishuDrive: {
		docURL:              "https://open.feishu.cn/app",
		permissionDocURL:    "https://open.feishu.cn/document/server-docs/docs/drive-v1/file/list",
		permissionPageURL:   "https://open.feishu.cn/app",
		requiredPermissions: feishuDrivePermissions,
		schema:              func() *configschema.Schema { return feishuSchema("https://open.feishu.cn") },
	},
	types.ConnectorTypeLarkDrive: {
		docURL:              "https://open.larksuite.com/app",
		permissionDocURL:    "https://open.larksuite.com/document/server-docs/docs/drive-v1/file/list",
		permissionPageURL:   "https://open.larksuite.com/app",
		requiredPermissions: feishuDrivePermissions,
		schema:              func() *configschema.Schema { return feishuSchema("https://open.larksuite.com") },
	},
	types.ConnectorTypeNotion: {
		docURL: "https://www.notion.so/my-integrations",
		schema: func() *configschema.Schema {
			token := secretField("Integration Token", "datasource.field.integrationToken", "ntn_xxxx", 1)
			return configschema.Object().Set("api_key", token, true)
		},
	},
	types.ConnectorTypeConfluence: {
		docURL:            "https://developer.atlassian.com/cloud/confluence/rest/",
		permissionDocURL:  "https://developer.atlassian.com/cloud/confluence/rest/",
		permissionPageURL: "https://id.atlassian.com/manage-profile/security/api-tokens",
		schema:            confluenceSchema,
	},
	types.ConnectorTypeYuque: {
		docURL:              "https://www.yuque.com/yuque/developer/api",
		permissionDocURL:    "https://www.yuque.com/yuque/developer/api",
		permissionPageURL:   "https://www.yuque.com/settings/tokens",
		requiredPermissions: []string{"repo:read", "doc:read"},
		schema: func() *configschema.Schema {
			return configschema.Object().
				Set("api_token", secretField("API Token", "datasource.field.apiToken", "", 1), true).
				Set("base_url", baseURLField("https://www.yuque.com", 2), false)
		},
	},
	types.ConnectorTypeDingTalk: {
		docURL:            "https://open.dingtalk.com/document/development/knowledge-base-overview",
		permissionDocURL:  "https://open.dingtalk.com/document/development/get-knowledge-base-list",
		permissionPageURL: "https://open-dev.dingtalk.com/",
		requiredPermissions: []string{
			"Wiki.Workspace.Read", "Wiki.Node.Read", "Storage.File.Read",
		},
		schema: func() *configschema.Schema {
			operator := textField("Operator Union ID", "datasource.field.operatorId", "", 3)
			operator.Description = "Reads knowledge bases with this user's permissions."
			operator.I18nKeys["description"] = "datasource.field.operatorIdHint"
			return configschema.Object().
				Set("client_id", textField("Client ID", "datasource.field.clientId", "dingxxxxxxxx", 1), true).
				Set("client_secret", secretField("Client Secret", "datasource.field.clientSecret", "", 2), true).
				Set("operator_id", operator, true)
		},
	},
	// Tencent IMA: OpenAPI with two static headers, no OAuth.
	types.ConnectorTypeIMA: {
		docURL:            "https://ima.qq.com/agent-interface",
		permissionDocURL:  "https://ima.qq.com/agent-interface",
		permissionPageURL: "https://ima.qq.com/agent-interface",
		schema: func() *configschema.Schema {
			return configschema.Object().
				Set("client_id", secretField("IMA ClientID", "datasource.field.imaClientId", "", 1), true).
				Set("api_key", secretField("IMA APIKey", "datasource.field.imaApiKey", "", 2), true).
				Set("base_url", baseURLField("https://ima.qq.com", 3), false)
		},
	},
	types.ConnectorTypeRSS: {
		schema: func() *configschema.Schema {
			// "Name: Value" lines, edited as rows by the headers widget. Not
			// marked secret: the whole credential map is encrypted and never
			// returned, and the widget needs to read back what it wrote.
			headers := &configschema.Schema{
				Type:        configschema.TypeString,
				Title:       "Custom headers (optional)",
				Description: `For private feeds. One per line in "Name: Value" form.`,
				Widget:      "headers",
				I18nKeys: map[string]string{
					"title":       "datasource.field.authHeaders",
					"description": "datasource.field.authHeadersHint",
				},
				Order: 1,
			}
			return configschema.Object().Set("auth_headers", headers, false)
		},
	},
	types.ConnectorTypeGitLab: {
		schema: func() *configschema.Schema {
			baseURL := textField("GitLab URL", "datasource.gitlab.baseUrl", "https://gitlab.example.com", 1)
			return configschema.Object().
				Set("base_url", baseURL, true).
				Set("access_token", secretField("Personal access token", "datasource.gitlab.accessToken", "", 2), true)
		},
	},
}

func feishuSchema(defaultBaseURL string) *configschema.Schema {
	return configschema.Object().
		Set("app_id", textField("App ID", "datasource.field.appId", "cli_xxxx", 1), true).
		Set("app_secret", secretField("App Secret", "datasource.field.appSecret", "", 2), true).
		Set("base_url", baseURLField(defaultBaseURL, 3), false)
}

// confluenceSchema switches the secret with the edition: Server / Data Center
// takes a password, Cloud an API token.
func confluenceSchema() *configschema.Schema {
	edition := &configschema.Schema{
		Type:     configschema.TypeString,
		Title:    "Edition",
		Default:  "server",
		Widget:   "select",
		I18nKeys: map[string]string{"title": "datasource.field.confluenceEdition"},
		OneOf: []*configschema.Schema{
			{
				Const: "server", Title: "Server / Data Center",
				I18nKeys: map[string]string{"title": "datasource.field.confluenceEditionServer"},
			},
			{
				Const: "cloud", Title: "Cloud",
				I18nKeys: map[string]string{"title": "datasource.field.confluenceEditionCloud"},
			},
		},
		Order: 0,
	}
	password := secretField("Server/DC password", "datasource.field.confluencePassword", "Server/DC password", 4)
	password.VisibleIf = map[string]any{"edition": "server"}
	token := secretField("Cloud API token", "datasource.field.confluenceApiToken", "Cloud API token", 5)
	token.VisibleIf = map[string]any{"edition": "cloud"}
	return configschema.Object().
		Set("edition", edition, false).
		Set("base_url", textField(
			"Confluence URL", "datasource.field.confluenceBaseUrl",
			"https://confluence.example.com or https://team.atlassian.net/wiki", 1,
		), true).
		Set("username", textField(
			"Username or email", "datasource.field.confluenceUsername", "name or email", 2,
		), true).
		Set("password", password, true).
		Set("api_token", token, true)
}

// textField is a plain string input. An empty placeholder falls back to the
// generic "Enter value" hint the editor always showed.
func textField(title, titleKey, placeholder string, order int) *configschema.Schema {
	f := &configschema.Schema{
		Type:        configschema.TypeString,
		Title:       title,
		Placeholder: placeholder,
		I18nKeys:    map[string]string{"title": titleKey},
		Order:       order,
	}
	if placeholder == "" {
		f.Placeholder = "Enter value"
		f.I18nKeys["placeholder"] = "credential.inputPlaceholder"
	}
	return f
}

func secretField(title, titleKey, placeholder string, order int) *configschema.Schema {
	f := textField(title, titleKey, placeholder, order)
	f.Secret = true
	f.Widget = "password"
	return f
}

// baseURLField is the optional endpoint override for private deployments and
// reverse proxies.
func baseURLField(defaultURL string, order int) *configschema.Schema {
	f := textField("Base URL (optional)", "datasource.field.baseUrl", defaultURL, order)
	f.Description = "Leave empty to use the default public cloud address."
	f.I18nKeys["description"] = "datasource.field.baseUrlHint"
	return f
}
