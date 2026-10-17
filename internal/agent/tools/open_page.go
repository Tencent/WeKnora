package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
)

// ToolOpenPage offers the user a link to a WeKnora page, optionally with a
// form filled in for them to finish and save.
const ToolOpenPage = "open_page"

const (
	maxOpenPageNoteRunes  = 200
	maxOpenPageNameRunes  = 255
	maxOpenPageUsageRunes = 2000
	maxOpenPageURLBytes   = 2048
)

// AgentEditorSections are the agent editor tabs a link may open.
var AgentEditorSections = []string{
	"basic", "prompts", "model", "multimodal", "conversation", "suggestions",
	"tools", "mcp", "skills", "knowledge", "websearch", "retrieval", "share",
}

// Pages without a resource of their own: the settings dialog at a section,
// the toolbox at a tool, and the lists in the sidebar.
const (
	openPageSettings       = "settings"
	openPageToolbox        = "toolbox"
	openPageAgents         = "agents"
	openPageKnowledgeBases = "knowledge_bases"
	openPageOrganizations  = "organizations"
)

var openPages = []string{
	types.PageKnowledgeBase, types.PageWiki, types.PageAgentEditor, types.PageModelSettings, types.PageMCPServices,
	openPageSettings, openPageToolbox, openPageAgents, openPageKnowledgeBases, openPageOrganizations,
}

type openPageSection struct{ zh, en string }

// SettingsSections are the settings dialog's sections, keyed as the client's
// navigation keys them, with the names it shows.
var SettingsSections = map[string]openPageSection{
	"general":               {"常规设置", "General Settings"},
	"userprofile":           {"用户信息", "User Profile"},
	"assistant":             {"助手", "Assistant"},
	"mymemory":              {"我的记忆", "My memory"},
	"envvars":               {"沙箱密钥", "Sandbox secrets"},
	"tenant":                {"空间信息", "Workspace Info"},
	"members":               {"成员管理", "Members"},
	"chathistory":           {"消息管理", "Message Management"},
	"memory":                {"长期记忆", "Long-term memory"},
	"models":                {"模型管理", "Model Management"},
	"ollama":                {"Ollama", "Ollama"},
	"weknoracloud":          {"WeKnora Cloud", "WeKnora Cloud"},
	"websearch":             {"网络搜索", "Web Search"},
	"vectorstore":           {"向量数据库引擎", "Vector DB Engine"},
	"parser":                {"解析引擎", "Parser Engine"},
	"storage":               {"存储引擎", "Storage Engine"},
	"sandbox":               {"沙箱配置", "Sandbox Config"},
	"system":                {"版本信息", "Version Info"},
	"system-global":         {"系统设置", "System Settings"},
	"model-catalog":         {"模型目录", "Model catalog"},
	"runtime-queues":        {"任务队列", "Task Queues"},
	"platform-api-keys":     {"平台 API Key", "Platform API Keys"},
	"system-audit-log":      {"审计日志", "Audit log"},
	"integration-im":        {"IM 集成", "IM Integration"},
	"integration-embed":     {"网页嵌入", "Web Embed"},
	"integration-api":       {"API 集成", "API Integration"},
	"integration-mcpserver": {"MCP Server", "MCP Server"},
	"integration-cli":       {"CLI", "CLI"},
	"integration-chrome":    {"Chrome 插件", "Chrome Extension"},
	"integration-claw":      {"Claw Skill", "Claw Skill"},
}

// SystemSettingsPanels are the tabs inside settings section system-global.
// "other" is conditional on the page and is reached with select_tab instead.
var SystemSettingsPanels = map[string]openPageSection{
	"access":   {"注册与访问", "Registration and access"},
	"users":    {"用户管理", "User management"},
	"tenant":   {"空间默认值", "Workspace defaults"},
	"runtime":  {"运行与并发", "Runtime and concurrency"},
	"security": {"网络安全", "Network security"},
}

// ToolboxSections are the toolbox's tools.
var ToolboxSections = map[string]openPageSection{
	"skills":            {"技能管理", "Skill Management"},
	"mcp":               {"MCP 服务", "MCP services"},
	"browserconnection": {"浏览器连接", "Browser connection"},
}

func sectionKeys(sections map[string]openPageSection) string {
	keys := make([]string, 0, len(sections))
	for key := range sections {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return strings.Join(keys, ", ")
}

var (
	openPageModelTypes   = []string{"chat", "embedding", "rerank", "vllm", "asr"}
	openPageModelSources = []string{"remote", "local"}
	openPageTransports   = []string{"sse", "http-streamable"}
	openPageAuthTypes    = []string{"", "api_key", "oauth"}
)

// OpenPageTool checks that what it links to exists and is visible to the
// user, then hands the link to the client. It changes nothing.
type OpenPageTool struct {
	BaseTool
	rt OperationsRuntime
	// actor opens the page in the user's browser for navigate; without it
	// every link is a button.
	actor approval.PageActor
}

// NewOpenPageTool creates open_page; target reads go through rt.
func NewOpenPageTool(rt OperationsRuntime) *OpenPageTool {
	return &OpenPageTool{
		BaseTool: NewBaseTool(ToolOpenPage, openPageDescription, json.RawMessage(openPageSchema)),
		rt:       rt,
	}
}

// WithPageActor lets navigate open the page in the user's browser.
func (t *OpenPageTool) WithPageActor(actor approval.PageActor) *OpenPageTool {
	t.actor = actor
	return t
}

const openPageDescription = "" +
	"Show the user a button that opens a WeKnora page: a knowledge base or one of its documents, " +
	"a Wiki page, an agent's editor at a given tab, the model settings, the MCP services, any section " +
	"of the settings dialog (personal preferences, profile, workspace, engines, integrations, system), " +
	"a toolbox tool, or the agent, knowledge base or shared space lists. settings section system-global " +
	"also takes panel, so a tab such as user management opens directly instead of registration and access. " +
	"Once the user opens it, " +
	"read_page can read what it shows. " +
	"It can open an existing model or MCP service for editing, or a new one with the form filled in " +
	"from model_draft or mcp_service_draft. Use it whenever the next step is the user's on one of these " +
	"pages: to look at what you created, changed or found (a knowledge base, a document, a Wiki page, " +
	"an agent), to finish something themselves, or when they ask where something is, instead of " +
	"describing the way there. Above all use it when the step needs an API key, token or password: " +
	"never ask for those in chat, and the drafts have no field for them. Offer one button per target, " +
	"once the work it points to is done, and none for the page the user already has open. The user " +
	"reviews any form and saves it; nothing is saved by this tool, and the page only opens when they " +
	"click. Name what they still need to fill in. " +
	"When you need to look at a page yourself to answer (a setting's current value, what a section shows), " +
	"set navigate: the page opens for the user now and its content comes back in the result, so you can " +
	"answer in the same turn. The user can go back from the card. If their page has unsaved changes it is " +
	"not left and they get the button instead. Not with a draft."

const openPageSchema = `{
  "type": "object",
  "properties": {
    "page": {
      "type": "string",
      "enum": ["knowledge_base", "wiki", "agent_editor", "settings_models", "mcp_services",
        "settings", "toolbox", "agents", "knowledge_bases", "organizations"],
      "description": "Which page to open. ` +
	`For a model or MCP service use settings_models or mcp_services, which can open one."
    },
    "knowledge_base_id": {"type": "string", "description": "For knowledge_base and wiki."},
    "knowledge_id": {"type": "string", "description": "knowledge_base only: open this document."},
    "wiki_slug": {"type": "string", "description": "wiki only: open this page; omit for the Wiki home."},
    "agent_id": {"type": "string", "description": "agent_editor: the agent to edit."},
    "section": {
      "type": "string",
      "description": "The part of the page to open. ` +
	`agent_editor: basic, prompts, model, multimodal, conversation, suggestions, tools, mcp, skills, ` +
	`knowledge, websearch, retrieval, share. ` +
	`settings: general (personal preferences: language, theme, font), userprofile, assistant, mymemory, ` +
	`envvars, tenant, members, chathistory, memory, models, ollama, weknoracloud, websearch, vectorstore, ` +
	`parser, storage, sandbox, system (version), system-global, model-catalog, runtime-queues, ` +
	`platform-api-keys, system-audit-log, integration-im, integration-embed, integration-api, ` +
	`integration-mcpserver, integration-cli, integration-chrome, integration-claw; ` +
	`a section the user may not see opens their first one. toolbox: skills, mcp, browserconnection."
    },
    "panel": {
      "type": "string",
      "enum": ["access", "users", "tenant", "runtime", "security"],
      "description": "settings with section system-global only: which tab to open. users is user management ` +
	`(accounts, enable or disable, reset password — the user still confirms and types the password). ` +
	`access is registration and access, the tab shown when panel is omitted. tenant, runtime, security ` +
	`are the other tabs."
    },
    "model_id": {"type": "string", "description": "settings_models only: open this model for editing."},
    "model_draft": {
      "type": "object",
      "description": "settings_models only: a new model, filled in. The user adds the API key.",
      "properties": {
        "type": {"type": "string", "enum": ["chat", "embedding", "rerank", "vllm", "asr"]},
        "source": {"type": "string", "enum": ["remote", "local"]},
        "provider": {
          "type": "string",
          "description": "Provider id from model.list_providers, e.g. openai, aliyun, generic. ` +
	`Prefer the catalog provider over generic."
        },
        "model_name": {
          "type": "string",
          "description": "The model name the provider expects: a model_name from model.get_provider when it lists one."
        },
        "display_name": {"type": "string"},
        "base_url": {"type": "string", "description": "http(s) endpoint, when the provider needs one."}
      },
      "required": ["type"],
      "additionalProperties": false
    },
    "mcp_service_id": {"type": "string", "description": "mcp_services only: open this service for editing."},
    "mcp_service_draft": {
      "type": "object",
      "description": "mcp_services only: a new remote MCP service, filled in. The user adds any key or signs in.",
      "properties": {
        "name": {"type": "string"},
        "usage_instructions": {"type": "string", "description": "When agents should use this service."},
        "transport_type": {"type": "string", "enum": ["sse", "http-streamable"]},
        "url": {"type": "string", "description": "http(s) endpoint of the service."},
        "auth_type": {
          "type": "string",
          "enum": ["", "api_key", "oauth"],
          "description": "How it authenticates; empty for none."
        },
        "api_key_header": {"type": "string", "description": "api_key only: the header that carries the key."}
      },
      "required": ["name", "transport_type", "url"],
      "additionalProperties": false
    },
    "note": {"type": "string", "description": "One short line on the button card: what to check or fill in."},
    "navigate": {
      "type": "boolean",
      "description": "Open the page now and return what it shows, instead of a button. ` +
	`Only to look at it yourself; never with a draft."
    }
  },
  "required": ["page"],
  "additionalProperties": false
}`

// OpenPageModelDraft is a new model's form, without credentials.
type OpenPageModelDraft struct {
	Type        string `json:"type"`
	Source      string `json:"source,omitempty"`
	Provider    string `json:"provider,omitempty"`
	ModelName   string `json:"model_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	BaseURL     string `json:"base_url,omitempty"`
}

// OpenPageMCPServiceDraft is a new MCP service's form, without credentials.
type OpenPageMCPServiceDraft struct {
	Name              string `json:"name"`
	UsageInstructions string `json:"usage_instructions,omitempty"`
	TransportType     string `json:"transport_type"`
	URL               string `json:"url"`
	AuthType          string `json:"auth_type,omitempty"`
	APIKeyHeader      string `json:"api_key_header,omitempty"`
}

type openPageInput struct {
	Page            string                   `json:"page"`
	KnowledgeBaseID string                   `json:"knowledge_base_id"`
	KnowledgeID     string                   `json:"knowledge_id"`
	WikiSlug        string                   `json:"wiki_slug"`
	AgentID         string                   `json:"agent_id"`
	Section         string                   `json:"section"`
	Panel           string                   `json:"panel"`
	ModelID         string                   `json:"model_id"`
	ModelDraft      *OpenPageModelDraft      `json:"model_draft"`
	MCPServiceID    string                   `json:"mcp_service_id"`
	MCPServiceDraft *OpenPageMCPServiceDraft `json:"mcp_service_draft"`
	Note            string                   `json:"note"`
	Navigate        bool                     `json:"navigate"`
}

// OpenPageLink is what the client needs to render the button and open the
// page. Labels come from the target reads, not from the model.
type OpenPageLink struct {
	DisplayType       string                   `json:"display_type"`
	Page              string                   `json:"page"`
	Label             string                   `json:"label"`
	Detail            string                   `json:"detail,omitempty"`
	Note              string                   `json:"note,omitempty"`
	KnowledgeBaseID   string                   `json:"knowledge_base_id,omitempty"`
	KnowledgeID       string                   `json:"knowledge_id,omitempty"`
	WikiSlug          string                   `json:"wiki_slug,omitempty"`
	AgentID           string                   `json:"agent_id,omitempty"`
	Section           string                   `json:"section,omitempty"`
	Panel             string                   `json:"panel,omitempty"`
	ModelID           string                   `json:"model_id,omitempty"`
	ModelDraft        *OpenPageModelDraft      `json:"model_draft,omitempty"`
	MCPServiceID      string                   `json:"mcp_service_id,omitempty"`
	MCPServiceDraft   *OpenPageMCPServiceDraft `json:"mcp_service_draft,omitempty"`
	KnowledgeBaseName string                   `json:"knowledge_base_name,omitempty"`
	// Navigated: the page was opened for the user; ReturnPath is where they were.
	Navigated  bool   `json:"navigated,omitempty"`
	ReturnPath string `json:"return_path,omitempty"`
}

func parseOpenPageInput(args json.RawMessage) (openPageInput, error) {
	var input openPageInput
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return input, fmt.Errorf("%v. Drafts take no API keys, tokens or passwords: the user enters "+
				"those on the page", err)
		}
		return input, fmt.Errorf("invalid arguments: %v", err)
	}
	trim := func(fields ...*string) {
		for _, f := range fields {
			*f = strings.TrimSpace(*f)
		}
	}
	trim(&input.Page, &input.KnowledgeBaseID, &input.KnowledgeID, &input.WikiSlug, &input.AgentID,
		&input.Section, &input.Panel, &input.ModelID, &input.MCPServiceID, &input.Note)
	if d := input.ModelDraft; d != nil {
		trim(&d.Type, &d.Source, &d.Provider, &d.ModelName, &d.DisplayName, &d.BaseURL)
	}
	if d := input.MCPServiceDraft; d != nil {
		trim(&d.Name, &d.UsageInstructions, &d.TransportType, &d.URL, &d.AuthType, &d.APIKeyHeader)
	}
	if utf8.RuneCountInString(input.Note) > maxOpenPageNoteRunes {
		input.Note = truncateRunes(input.Note, maxOpenPageNoteRunes)
	}
	return input, validateOpenPageInput(input)
}

func validateOpenPageInput(in openPageInput) error {
	if in.Navigate && (in.ModelDraft != nil || in.MCPServiceDraft != nil) {
		return errors.New("a form with a draft opens only from its button, for the user to review; drop navigate")
	}
	only := func(allowed ...string) error {
		set := map[string]bool{
			"knowledge_base_id": in.KnowledgeBaseID != "", "knowledge_id": in.KnowledgeID != "",
			"wiki_slug": in.WikiSlug != "", "agent_id": in.AgentID != "", "section": in.Section != "",
			"panel": in.Panel != "", "model_id": in.ModelID != "", "model_draft": in.ModelDraft != nil,
			"mcp_service_id": in.MCPServiceID != "", "mcp_service_draft": in.MCPServiceDraft != nil,
		}
		for name, present := range set {
			if present && !slices.Contains(allowed, name) {
				return fmt.Errorf("%s does not apply to page %s", name, in.Page)
			}
		}
		return nil
	}
	switch in.Page {
	case types.PageKnowledgeBase, types.PageWiki:
		if in.KnowledgeBaseID == "" {
			return fmt.Errorf("knowledge_base_id is required for page %s", in.Page)
		}
		if in.Page == types.PageWiki {
			return only("knowledge_base_id", "wiki_slug")
		}
		return only("knowledge_base_id", "knowledge_id")
	case types.PageAgentEditor:
		if in.AgentID == "" {
			return errors.New("agent_id is required for page agent_editor")
		}
		if in.Section != "" && !slices.Contains(AgentEditorSections, in.Section) {
			return fmt.Errorf("section must be one of %s", strings.Join(AgentEditorSections, ", "))
		}
		return only("agent_id", "section")
	case types.PageModelSettings:
		if in.ModelID != "" && in.ModelDraft != nil {
			return errors.New("give model_id to edit a model or model_draft to add one, not both")
		}
		if err := only("model_id", "model_draft"); err != nil {
			return err
		}
		return validateModelDraft(in.ModelDraft)
	case types.PageMCPServices:
		if in.MCPServiceID != "" && in.MCPServiceDraft != nil {
			return errors.New("give mcp_service_id to edit a service or mcp_service_draft to add one, not both")
		}
		if err := only("mcp_service_id", "mcp_service_draft"); err != nil {
			return err
		}
		return validateMCPServiceDraft(in.MCPServiceDraft)
	case openPageSettings, openPageToolbox:
		sections := SettingsSections
		if in.Page == openPageToolbox {
			sections = ToolboxSections
		}
		if _, ok := sections[in.Section]; in.Section != "" && !ok {
			return fmt.Errorf("section for page %s must be one of %s", in.Page, sectionKeys(sections))
		}
		if in.Page == openPageSettings {
			if err := validateSystemSettingsPanel(in); err != nil {
				return err
			}
			return only("section", "panel")
		}
		return only("section")
	case openPageAgents, openPageKnowledgeBases, openPageOrganizations:
		return only()
	default:
		return fmt.Errorf("page must be one of %s", strings.Join(openPages, ", "))
	}
}

func validateSystemSettingsPanel(in openPageInput) error {
	if in.Panel == "" {
		return nil
	}
	if in.Section != "system-global" {
		return errors.New("panel applies only to settings section system-global")
	}
	if _, ok := SystemSettingsPanels[in.Panel]; !ok {
		return fmt.Errorf("panel must be one of %s", sectionKeys(SystemSettingsPanels))
	}
	return nil
}

func validateModelDraft(d *OpenPageModelDraft) error {
	if d == nil {
		return nil
	}
	if !slices.Contains(openPageModelTypes, d.Type) {
		return fmt.Errorf("model_draft.type must be one of %s", strings.Join(openPageModelTypes, ", "))
	}
	if d.Source != "" && !slices.Contains(openPageModelSources, d.Source) {
		return errors.New("model_draft.source must be remote or local")
	}
	for name, value := range map[string]string{
		"provider": d.Provider, "model_name": d.ModelName, "display_name": d.DisplayName,
	} {
		if utf8.RuneCountInString(value) > maxOpenPageNameRunes {
			return fmt.Errorf("model_draft.%s must be at most %d characters", name, maxOpenPageNameRunes)
		}
	}
	if d.BaseURL != "" {
		return validateDraftURL("model_draft.base_url", d.BaseURL)
	}
	return nil
}

func validateMCPServiceDraft(d *OpenPageMCPServiceDraft) error {
	if d == nil {
		return nil
	}
	if d.Name == "" || utf8.RuneCountInString(d.Name) > maxOpenPageNameRunes {
		return fmt.Errorf("mcp_service_draft.name is required, at most %d characters", maxOpenPageNameRunes)
	}
	if utf8.RuneCountInString(d.UsageInstructions) > maxOpenPageUsageRunes {
		return fmt.Errorf("mcp_service_draft.usage_instructions must be at most %d characters", maxOpenPageUsageRunes)
	}
	if !slices.Contains(openPageTransports, d.TransportType) {
		return errors.New("mcp_service_draft.transport_type must be sse or http-streamable; " +
			"local (stdio) services are set up by the user")
	}
	if !slices.Contains(openPageAuthTypes, d.AuthType) {
		return errors.New("mcp_service_draft.auth_type must be empty, api_key or oauth")
	}
	if d.APIKeyHeader != "" && (d.AuthType != "api_key" || utf8.RuneCountInString(d.APIKeyHeader) > 128) {
		return errors.New("mcp_service_draft.api_key_header applies to auth_type api_key only, " +
			"at most 128 characters")
	}
	return validateDraftURL("mcp_service_draft.url", d.URL)
}

// validateDraftURL accepts only an http(s) URL without embedded credentials:
// the user sees the host on the card and in the form.
func validateDraftURL(field, raw string) error {
	if len(raw) > maxOpenPageURLBytes {
		return fmt.Errorf("%s is too long", field)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an http(s) URL", field)
	}
	if u.User != nil {
		return fmt.Errorf("%s must not carry a user name or password", field)
	}
	return nil
}

// Execute checks the target and returns the link for the client.
func (t *OpenPageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	input, err := parseOpenPageInput(args)
	if err != nil {
		return operationFailure(err.Error()), nil
	}
	if (input.Page == types.PageModelSettings && (input.ModelID != "" || input.ModelDraft != nil)) ||
		(input.Page == types.PageMCPServices && (input.MCPServiceID != "" || input.MCPServiceDraft != nil)) {
		if t.rt.Access.EnforceRole && !t.rt.Access.Role.HasPermission(types.TenantRoleAdmin) {
			return operationFailure("only workspace admins can add or edit models and MCP services; " +
				"tell the user to ask an admin"), nil
		}
	}
	link, blocked := t.resolve(ctx, input)
	if blocked != nil {
		return blocked, nil
	}
	button := fmt.Sprintf("The user now sees a button to open %s. Nothing was opened or saved yet: "+
		"they open it, review the form and save it themselves.", link.Label)
	if !input.Navigate {
		return openPageResult(link, button), nil
	}
	return t.navigate(ctx, link, button), nil
}

// navigate opens the link in the user's browser and returns what the page
// shows. Whatever stops it, the user still gets the button.
func (t *OpenPageTool) navigate(ctx context.Context, link *OpenPageLink, button string) *types.ToolResult {
	res, failure := requestPageAction(ctx, t.actor, types.PageActionPromptRequest{
		Action: types.PageActionNavigate,
		Target: &types.PageNavigateTarget{
			Page: link.Page, KnowledgeBaseID: link.KnowledgeBaseID, KnowledgeID: link.KnowledgeID,
			WikiSlug: link.WikiSlug, AgentID: link.AgentID, Section: link.Section, Panel: link.Panel,
			ModelID: link.ModelID, MCPServiceID: link.MCPServiceID,
		},
	})
	switch {
	case failure != nil:
		return openPageResult(link, "The page could not be opened for the user ("+failure.Error+"). "+button)
	case res.Response.Stayed:
		return openPageResult(link, "The user's page has unsaved changes, so it was not left. "+button+
			" Ask them to open it and tell you when it is open; then read it with read_page.")
	case res.Response.Error != "":
		return openPageResult(link, "The page could not be opened for the user ("+res.Response.Error+"). "+button)
	}
	link.Navigated, link.ReturnPath = true, res.Response.ReturnPath
	return openPageResult(link, fmt.Sprintf("Opened %s for the user; they can go back from the card. "+
		"Nothing was saved. It shows:\n", link.Label)+quotePageSnapshot(res.Response.Location, res.Response.Snapshot))
}

func openPageResult(link *OpenPageLink, output string) *types.ToolResult {
	data := map[string]any{}
	raw, _ := json.Marshal(link)
	_ = json.Unmarshal(raw, &data)
	return &types.ToolResult{Success: true, Output: output, Data: data}
}

func (t *OpenPageTool) resolve(ctx context.Context, in openPageInput) (*OpenPageLink, *types.ToolResult) {
	link := &OpenPageLink{DisplayType: ToolOpenPage, Page: in.Page, Note: in.Note}
	zh := strings.HasPrefix(types.LanguageFromContextOrDefault(ctx), "zh")
	pick := func(zhText, enText string) string {
		if zh {
			return zhText
		}
		return enText
	}
	switch in.Page {
	case types.PageKnowledgeBase, types.PageWiki:
		kb, blocked := t.read(ctx, "knowledge_base.get", map[string]string{"knowledge_base_id": in.KnowledgeBaseID})
		if blocked != nil {
			return nil, blocked
		}
		link.KnowledgeBaseID = in.KnowledgeBaseID
		link.KnowledgeBaseName = kb.Label("name")
		link.Label = firstNonEmpty(link.KnowledgeBaseName, in.KnowledgeBaseID)
		if in.KnowledgeID != "" {
			doc, blocked := t.read(ctx, "knowledge.get", map[string]string{"knowledge_id": in.KnowledgeID})
			if blocked != nil {
				return nil, blocked
			}
			if doc.Label("knowledge_base_id") != in.KnowledgeBaseID {
				return nil, operationFailure("knowledge_id is not a document of that knowledge base")
			}
			link.KnowledgeID = in.KnowledgeID
			link.Detail = firstNonEmpty(doc.Label("title"), doc.Label("file_name"), in.KnowledgeID)
		}
		if in.Page == types.PageWiki {
			link.Detail = pick("Wiki 首页", "Wiki home")
			if in.WikiSlug != "" {
				page, blocked := t.read(ctx, "wiki.get_page", map[string]string{
					"knowledge_base_id": in.KnowledgeBaseID, "slug": in.WikiSlug,
				})
				if blocked != nil {
					return nil, blocked
				}
				link.WikiSlug = in.WikiSlug
				link.Detail = firstNonEmpty(page.Label("title"), in.WikiSlug)
			}
		}
	case types.PageAgentEditor:
		agent, blocked := t.read(ctx, "agent.get", map[string]string{"agent_id": in.AgentID})
		if blocked != nil {
			return nil, blocked
		}
		link.AgentID, link.Section = in.AgentID, in.Section
		link.Label = firstNonEmpty(agent.Label("name"), in.AgentID)
	case types.PageModelSettings:
		link.Label = pick("模型管理", "Models")
		if in.ModelID != "" {
			model, blocked := t.read(ctx, "model.get", map[string]string{"model_id": in.ModelID})
			if blocked != nil {
				return nil, blocked
			}
			link.ModelID = in.ModelID
			link.Detail = firstNonEmpty(model.Label("display_name"), model.Label("name"), in.ModelID)
		} else if d := in.ModelDraft; d != nil {
			link.ModelDraft = d
			link.Detail = pick("新模型：", "New model: ") + firstNonEmpty(d.DisplayName, d.ModelName, d.Type)
		}
	case types.PageMCPServices:
		link.Label = pick("MCP 服务", "MCP services")
		if in.MCPServiceID != "" {
			svc, blocked := t.read(ctx, "mcp_service.get", map[string]string{"mcp_service_id": in.MCPServiceID})
			if blocked != nil {
				return nil, blocked
			}
			link.MCPServiceID = in.MCPServiceID
			link.Detail = firstNonEmpty(svc.Label("name"), in.MCPServiceID)
		} else if d := in.MCPServiceDraft; d != nil {
			link.MCPServiceDraft = d
			link.Detail = pick("新服务：", "New service: ") + d.Name
		}
	case openPageSettings:
		link.Label = pick("设置", "Settings")
		if s, ok := SettingsSections[in.Section]; ok {
			link.Section, link.Detail = in.Section, pick(s.zh, s.en)
		}
		if p, ok := SystemSettingsPanels[in.Panel]; ok {
			link.Panel = in.Panel
			link.Detail = strings.TrimPrefix(link.Detail+" / "+pick(p.zh, p.en), " / ")
		}
	case openPageToolbox:
		link.Label = pick("工具箱", "Toolbox")
		if s, ok := ToolboxSections[in.Section]; ok {
			link.Section, link.Detail = in.Section, pick(s.zh, s.en)
		}
	case openPageAgents:
		link.Label = pick("智能体", "Agents")
	case openPageKnowledgeBases:
		link.Label = pick("知识库", "Knowledge bases")
	case openPageOrganizations:
		link.Label = pick("共享空间", "Shared spaces")
	}
	return link, nil
}

// read runs a catalog read as the user, so a link never points at something
// they cannot open.
func (t *OpenPageTool) read(
	ctx context.Context, id string, params map[string]string,
) (operations.Projected, *types.ToolResult) {
	op, ok := t.rt.Catalog.Get(id)
	if !ok || t.rt.Dispatcher == nil {
		return operations.Projected{}, operationFailure(fmt.Sprintf("%s is unavailable", id))
	}
	raw := make(map[string]json.RawMessage, len(params))
	for k, v := range params {
		raw[k], _ = json.Marshal(v)
	}
	call, err := op.Build(operations.SelectParams(op, raw))
	if err != nil {
		return operations.Projected{}, operationFailure(fmt.Sprintf("invalid target for %s: %v", id, err))
	}
	resp, err := t.rt.Dispatcher.Dispatch(ctx, t.rt.Identity, call)
	if err != nil {
		return operations.Projected{}, operationFailure(fmt.Sprintf("%s could not be run: %v", id, err))
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return operations.Projected{}, operationFailure(fmt.Sprintf(
			"the target was not found or is not visible to the user (%s, HTTP %d): %s",
			id, resp.Status, operationErrorMessage(resp.Status, resp.Body)))
	}
	projected, err := op.Project(resp.Body)
	if err != nil {
		return operations.Projected{}, operationFailure(fmt.Sprintf("%s returned an unexpected response", id))
	}
	return projected, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
