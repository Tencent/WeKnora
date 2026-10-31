package service

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// marketplaceManifests is the catalog WeKnora reads, in the order a directory
// that contains more than one of them is resolved. The first one that parses
// into at least one installable plugin wins. A symlink or an unreadable
// higher-priority file does not hide the next catalog.
var marketplaceManifests = []string{
	".weknora-plugin/marketplace.json",
	".claude-plugin/marketplace.json",
	".cursor-plugin/marketplace.json",
	".github/plugin/marketplace.json",
	".agents/plugins/marketplace.json",
}

// pluginManifests is the per-plugin manifest, in the same channel order.
// A root plugin.json is the Agent Plugins / Codex portable manifest.
var pluginManifests = []string{
	".weknora-plugin/plugin.json",
	".claude-plugin/plugin.json",
	".cursor-plugin/plugin.json",
	".codex-plugin/plugin.json",
	"plugin.json",
}

const geminiExtensionManifest = "gemini-extension.json"

// remotePluginSources are marketplace entry sources whose files are not in
// this archive. They are skipped and reported, not fetched.
var remotePluginSources = map[string]bool{
	"github": true, "url": true, "git": true, "git-subdir": true,
	"npm": true, "archive": true, "command": true,
}

type manifestHit struct {
	priority int
	relPath  string
	prefix   string
	depth    int
}

type pluginSkillDir struct {
	name  string
	files map[string][]byte
}

type rawMarketplaceFile struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Version     string                 `json:"version"`
	Owner       types.MarketplaceOwner `json:"owner"`
	Metadata    struct {
		Description string `json:"description"`
		Version     string `json:"version"`
		PluginRoot  string `json:"pluginRoot"`
	} `json:"metadata"`
	Interface struct {
		DisplayName string `json:"displayName"`
	} `json:"interface"`
	Plugins []rawMarketplacePlugin `json:"plugins"`
}

type rawMarketplacePlugin struct {
	Name        string          `json:"name"`
	Source      json.RawMessage `json:"source"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	Category    string          `json:"category"`
	Keywords    []string        `json:"keywords"`
}

type rawPluginSource struct {
	Source  string `json:"source"`
	Path    string `json:"path"`
	Repo    string `json:"repo"`
	URL     string `json:"url"`
	Package string `json:"package"`
}

func findMarketplaceManifest(files map[string][]byte) (relPath, prefix string, ok bool) {
	hits := marketplaceManifestCandidates(files)
	if len(hits) == 0 {
		return "", "", false
	}
	return hits[0].relPath, hits[0].prefix, true
}

// marketplaceManifestCandidates lists every catalog in the archive. Shallowest
// directory first, then channel order, then lexical path.
func marketplaceManifestCandidates(files map[string][]byte) []manifestHit {
	var hits []manifestHit
	for name := range files {
		for priority, rel := range marketplaceManifests {
			hitPrefix, matched := manifestPrefix(name, rel)
			if !matched {
				continue
			}
			hits = append(hits, manifestHit{
				priority: priority, relPath: rel, prefix: hitPrefix, depth: pathDepth(hitPrefix),
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return manifestBetter(hits[i], hits[j]) })
	return hits
}

func manifestPrefix(name, rel string) (string, bool) {
	if name == rel {
		return "", true
	}
	suffix := "/" + rel
	if strings.HasSuffix(name, suffix) {
		return strings.TrimSuffix(name, suffix), true
	}
	return "", false
}

func pathDepth(prefix string) int {
	if prefix == "" {
		return 0
	}
	return strings.Count(prefix, "/") + 1
}

// manifestBetter prefers the shallowest directory, then the channel order,
// then the lexical path. A WeKnora manifest nested inside a Claude
// marketplace does not hide the marketplace the archive is actually for.
func manifestBetter(a, b manifestHit) bool {
	if a.depth != b.depth {
		return a.depth < b.depth
	}
	if a.priority != b.priority {
		return a.priority < b.priority
	}
	return a.prefix < b.prefix
}

func stripMarketplacePrefix(files map[string][]byte, prefix string) map[string][]byte {
	if prefix == "" {
		return files
	}
	prefixSlash := prefix + "/"
	out := make(map[string][]byte, len(files))
	for name, content := range files {
		if strings.HasPrefix(name, prefixSlash) {
			out[strings.TrimPrefix(name, prefixSlash)] = content
		}
	}
	return out
}

func parseMarketplaceCatalog(files map[string][]byte, relPath string) (*ParsedMarketplace, error) {
	rawBytes, ok := files[relPath]
	if !ok {
		return nil, fmt.Errorf("%w: %s is missing", ErrSkillBundleInvalid, relPath)
	}
	var raw rawMarketplaceFile
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return nil, fmt.Errorf("%w: cannot parse %s: %v", ErrSkillBundleInvalid, relPath, err)
	}
	manifest, entries, warnings, err := normalizeMarketplaceCatalog(raw)
	if err != nil {
		return nil, err
	}
	return parseMarketplaceEntries(files, manifest, entries, warnings)
}

func normalizeMarketplaceCatalog(
	raw rawMarketplaceFile,
) (types.MarketplaceManifest, []types.MarketplacePluginEntry, []string, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		return types.MarketplaceManifest{}, nil, nil, fmt.Errorf(
			"%w: marketplace name cannot be empty",
			ErrSkillBundleInvalid,
		)
	}
	description := strings.TrimSpace(raw.Metadata.Description)
	if description == "" {
		description = strings.TrimSpace(raw.Description)
	}
	if description == "" {
		description = strings.TrimSpace(raw.Interface.DisplayName)
	}
	version := strings.TrimSpace(raw.Metadata.Version)
	if version == "" {
		version = strings.TrimSpace(raw.Version)
	}
	manifest := types.MarketplaceManifest{
		Name:  name,
		Owner: raw.Owner,
		Metadata: types.MarketplaceMetadata{
			Description: description,
			Version:     version,
		},
	}
	var entries []types.MarketplacePluginEntry
	var warnings []string
	for _, plugin := range raw.Plugins {
		pluginName := strings.TrimSpace(plugin.Name)
		if pluginName == "" {
			warnings = append(warnings, "跳过一个未命名的插件条目")
			continue
		}
		local, remoteDetail, remote, err := classifyPluginSource(plugin.Source, raw.Metadata.PluginRoot)
		if err != nil {
			return types.MarketplaceManifest{}, nil, nil, fmt.Errorf(
				"%w: plugin %q: %v",
				ErrSkillBundleInvalid,
				pluginName,
				err,
			)
		}
		if remote {
			warnings = append(warnings, fmt.Sprintf("跳过插件 %q：来源指向仓库外（%s），压缩包中没有这个插件的文件", pluginName, remoteDetail))
			continue
		}
		entries = append(entries, types.MarketplacePluginEntry{
			Name:        pluginName,
			Source:      local,
			Description: plugin.Description,
			Version:     plugin.Version,
			Category:    plugin.Category,
			Keywords:    plugin.Keywords,
		})
	}
	manifest.Plugins = entries
	return manifest, entries, warnings, nil
}

func classifyPluginSource(raw json.RawMessage, pluginRoot string) (local, detail string, remote bool, err error) {
	raw = bytesTrim(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", "未填写 source", true, nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", "", false, err
		}
		if strings.TrimSpace(text) == "" {
			return "", "未填写 source", true, nil
		}
		local, err = cleanLocalPluginSource(text, pluginRoot)
		return local, "", false, err
	}
	var obj rawPluginSource
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", "", false, fmt.Errorf("source 无法解析: %v", err)
	}
	kind := strings.ToLower(strings.TrimSpace(obj.Source))
	if remotePluginSources[kind] || (kind != "" && kind != "local" && strings.TrimSpace(obj.Path) == "") {
		return "", remoteSourceDetail(obj), true, nil
	}
	local, err = cleanLocalPluginSource(obj.Path, pluginRoot)
	return local, "", false, err
}

func remoteSourceDetail(obj rawPluginSource) string {
	kind := strings.TrimSpace(obj.Source)
	if kind == "" {
		kind = "remote"
	}
	switch {
	case strings.TrimSpace(obj.Repo) != "":
		return kind + " " + strings.TrimSpace(obj.Repo)
	case strings.TrimSpace(obj.URL) != "":
		return kind + " " + strings.TrimSpace(obj.URL)
	case strings.TrimSpace(obj.Package) != "":
		return kind + " " + strings.TrimSpace(obj.Package)
	default:
		return kind
	}
}

func cleanLocalPluginSource(source, pluginRoot string) (string, error) {
	source = strings.TrimSpace(strings.ReplaceAll(source, "\\", "/"))
	if strings.Contains(source, "..") {
		return "", fmt.Errorf("source %q escapes the marketplace", source)
	}
	source = strings.TrimPrefix(source, "./")
	root := strings.Trim(strings.TrimSpace(strings.ReplaceAll(pluginRoot, "\\", "/")), "/")
	root = strings.TrimPrefix(root, "./")
	if root != "" && source != "" && !strings.Contains(source, "/") {
		source = root + "/" + source
	}
	if source == "" || source == "." {
		return ".", nil
	}
	cleaned := path.Clean(source)
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("source %q escapes the marketplace", source)
	}
	return cleaned, nil
}

func parseMarketplaceEntries(
	files map[string][]byte,
	manifest types.MarketplaceManifest,
	entries []types.MarketplacePluginEntry,
	warnings []string,
) (*ParsedMarketplace, error) {
	parsed := &ParsedMarketplace{
		Manifest: manifest,
		RawFiles: files,
		Plugins:  make(map[string]*ParsedPlugin, len(entries)),
		Warnings: warnings,
	}
	kept := make([]types.MarketplacePluginEntry, 0, len(entries))
	for _, entry := range entries {
		plugin, err := parsePluginFromFiles(files, entry)
		if err != nil {
			return nil, err
		}
		if plugin.SkillCount == 0 && plugin.MCPCount == 0 {
			parsed.Warnings = append(parsed.Warnings, skipPluginWithoutPayload(plugin))
			continue
		}
		if _, dup := parsed.Plugins[plugin.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate plugin name %q", ErrSkillBundleInvalid, plugin.Name)
		}
		parsed.Plugins[plugin.Name] = plugin
		parsed.Order = append(parsed.Order, plugin.Name)
		entry.Name = plugin.Name
		kept = append(kept, entry)
	}
	parsed.Manifest.Plugins = kept
	if len(parsed.Plugins) == 0 {
		return nil, emptyMarketplaceError(parsed.Warnings)
	}
	return parsed, nil
}

// skipPluginWithoutPayload reports a plugin whose archive has neither an
// installable skill nor an HTTP/SSE MCP server. Slash commands, hooks and
// stdio-only servers fall here.
func skipPluginWithoutPayload(plugin *ParsedPlugin) string {
	if len(plugin.MCPWarnings) == 0 {
		return fmt.Sprintf("跳过插件 %q：没有可安装的技能或 MCP", plugin.Name)
	}
	return fmt.Sprintf("跳过插件 %q：没有可安装的技能或 MCP（%s）", plugin.Name, strings.Join(plugin.MCPWarnings, "；"))
}

func emptyMarketplaceError(warnings []string) error {
	if len(warnings) == 0 {
		return fmt.Errorf("%w: 没有找到可导入的插件", ErrSkillBundleInvalid)
	}
	return fmt.Errorf("%w: 没有可导入的插件：%s", ErrSkillBundleInvalid, strings.Join(warnings, "；"))
}

// parseLoosePluginBundle accepts an archive that has no marketplace.json.
// The root is one plugin when it carries a plugin manifest, skills, or MCP
// config. Otherwise each immediate subdirectory that does is one plugin.
func parseLoosePluginBundle(files map[string][]byte) (*ParsedMarketplace, error) {
	root := files
	wrapper := ""
	if top, ok := singleTopDir(files); ok {
		root = stripMarketplacePrefix(files, top)
		wrapper = top
	}
	var entries []types.MarketplacePluginEntry
	var warnings []string
	if directoryIsPlugin(root, "") {
		name := pluginNameAt(root, "")
		if name == "" {
			name = wrapper
		}
		if err := validatePluginName(name); err != nil {
			return nil, fmt.Errorf("%w: plugin name %q: %v", ErrSkillBundleInvalid, name, err)
		}
		entries = append(entries, types.MarketplacePluginEntry{Name: name, Source: "."})
	} else {
		children := immediateChildDirs(root)
		sort.Strings(children)
		for _, child := range children {
			if !directoryIsPlugin(root, child) {
				continue
			}
			name := pluginNameAt(root, child)
			if name == "" {
				name = child
			}
			if err := validatePluginName(name); err != nil {
				warnings = append(warnings, fmt.Sprintf("跳过目录 %q：%v", child, err))
				continue
			}
			entries = append(entries, types.MarketplacePluginEntry{Name: name, Source: child})
		}
	}
	if len(entries) == 0 {
		if len(warnings) > 0 {
			return nil, emptyMarketplaceError(warnings)
		}
		return nil, fmt.Errorf(
			"%w: 没有找到插件市场清单或插件。支持 .weknora-plugin、.claude-plugin、"+
				".cursor-plugin、.github/plugin、.agents/plugins 的 marketplace.json，"+
				"或带 plugin.json / skills 的插件目录",
			ErrSkillBundleInvalid,
		)
	}
	marketName := wrapper
	if len(entries) == 1 {
		marketName = entries[0].Name
	}
	if strings.TrimSpace(marketName) == "" {
		marketName = "plugins"
	}
	manifest := types.MarketplaceManifest{
		Name:    marketName,
		Plugins: entries,
	}
	return parseMarketplaceEntries(root, manifest, entries, warnings)
}

func singleTopDir(files map[string][]byte) (string, bool) {
	var top string
	for name := range files {
		name = path.Clean(name)
		if name == "." || !strings.Contains(name, "/") {
			return "", false
		}
		seg := name[:strings.Index(name, "/")]
		if top == "" {
			top = seg
			continue
		}
		if seg != top {
			return "", false
		}
	}
	return top, top != ""
}

func immediateChildDirs(files map[string][]byte) []string {
	seen := map[string]struct{}{}
	var names []string
	for name := range files {
		name = path.Clean(name)
		if !strings.Contains(name, "/") {
			continue
		}
		seg := name[:strings.Index(name, "/")]
		if _, ok := seen[seg]; ok || strings.HasPrefix(seg, ".") {
			continue
		}
		seen[seg] = struct{}{}
		names = append(names, seg)
	}
	return names
}

func directoryIsPlugin(files map[string][]byte, dir string) bool {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	for _, rel := range pluginManifests {
		if _, ok := files[prefix+rel]; ok {
			return true
		}
	}
	if _, ok := files[prefix+geminiExtensionManifest]; ok {
		return true
	}
	if _, ok := files[prefix+"mcp.json"]; ok {
		return true
	}
	if _, ok := files[prefix+".mcp.json"]; ok {
		return true
	}
	if _, ok := files[prefix+"SKILL.md"]; ok {
		return true
	}
	skillsPrefix := prefix + "skills/"
	for name := range files {
		if strings.HasPrefix(name, skillsPrefix) && strings.HasSuffix(name, "/SKILL.md") {
			return true
		}
	}
	return false
}

func pluginNameAt(files map[string][]byte, dir string) string {
	manifest, _, err := loadPluginManifest(files, dirPrefix(dir))
	if err != nil || manifest == nil {
		return ""
	}
	return strings.TrimSpace(manifest.Name)
}

func dirPrefix(dir string) string {
	if dir == "" || dir == "." {
		return ""
	}
	return strings.Trim(dir, "/") + "/"
}

func loadPluginManifest(files map[string][]byte, dirPrefix string) (*types.PluginManifest, bool, error) {
	for _, rel := range pluginManifests {
		full := dirPrefix + rel
		body, ok := files[full]
		if !ok {
			continue
		}
		manifest := &types.PluginManifest{}
		if err := json.Unmarshal(body, manifest); err != nil {
			return nil, false, fmt.Errorf("%w: cannot parse %s: %v", ErrSkillBundleInvalid, full, err)
		}
		return manifest, true, nil
	}
	if body, ok := files[dirPrefix+geminiExtensionManifest]; ok {
		manifest, err := manifestFromGemini(body, dirPrefix+geminiExtensionManifest)
		if err != nil {
			return nil, false, err
		}
		return manifest, true, nil
	}
	return nil, false, nil
}

type geminiExtensionFile struct {
	Name        string                                `json:"name"`
	Version     string                                `json:"version"`
	Description string                                `json:"description"`
	MCPServers  map[string]types.PluginMCPServerEntry `json:"mcpServers"`
}

func manifestFromGemini(body []byte, path string) (*types.PluginManifest, error) {
	var ext geminiExtensionFile
	if err := json.Unmarshal(body, &ext); err != nil {
		return nil, fmt.Errorf("%w: cannot parse %s: %v", ErrSkillBundleInvalid, path, err)
	}
	return &types.PluginManifest{
		Name:        ext.Name,
		Version:     ext.Version,
		Description: ext.Description,
		MCPServers:  types.PluginMCPConfig{Inline: ext.MCPServers},
	}, nil
}

func collectPluginMCPServers(
	files map[string][]byte, dirPrefix, pluginName string, decl types.PluginMCPConfig,
) (map[string]types.PluginMCPServerEntry, []string, []string, error) {
	merged := map[string]types.PluginMCPServerEntry{}
	var loadErr error
	readFile := func(rel string) {
		if loadErr != nil || rel == "" {
			return
		}
		body, ok := files[dirPrefix+rel]
		if !ok {
			return
		}
		var cfg types.PluginMCPServersConfig
		if err := json.Unmarshal(body, &cfg); err != nil {
			loadErr = fmt.Errorf("%w: cannot parse %s: %v", ErrSkillBundleInvalid, dirPrefix+rel, err)
			return
		}
		for name, server := range cfg.MCPServers {
			merged[name] = server
		}
	}
	readFile(".mcp.json")
	readFile("mcp.json")
	for _, extra := range decl.Paths {
		rel, err := cleanLocalPluginSource(extra, "")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%w: plugin %q: %v", ErrSkillBundleInvalid, pluginName, err)
		}
		if rel == "." {
			continue
		}
		body, ok := files[dirPrefix+rel]
		if !ok {
			return nil, nil, nil, fmt.Errorf("%w: plugin %q: MCP 配置 %q 不在插件目录中", ErrSkillBundleInvalid, pluginName, rel)
		}
		var cfg types.PluginMCPServersConfig
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: cannot parse %s: %v", ErrSkillBundleInvalid, dirPrefix+rel, err)
		}
		for name, server := range cfg.MCPServers {
			merged[name] = server
		}
	}
	if loadErr != nil {
		return nil, nil, nil, loadErr
	}
	for name, server := range decl.Inline {
		merged[name] = server
	}

	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)

	kept := make(map[string]types.PluginMCPServerEntry, len(names))
	var warnings []string
	var headerVars []string
	for _, name := range names {
		server := merged[name]
		accepted, warning, vars, err := acceptPluginMCPServer(pluginName, name, server)
		if err != nil {
			return nil, nil, nil, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
			continue
		}
		kept[name] = accepted
		headerVars = append(headerVars, vars...)
	}
	return kept, warnings, headerVars, nil
}

func acceptPluginMCPServer(
	pluginName, serverName string,
	server types.PluginMCPServerEntry,
) (types.PluginMCPServerEntry, string, []string, error) {
	if mcpServerIsStdio(server) {
		return types.PluginMCPServerEntry{}, fmt.Sprintf(
			"跳过 MCP 服务 %q: WeKnora 已禁用 stdio transport",
			serverName,
		), nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(server.Type)) {
	case "", "http", "streamable-http", "streamable_http", "http-streamable":
		server.Type = "http"
	case "sse":
		server.Type = "sse"
	default:
		if strings.TrimSpace(server.URL) == "" {
			return types.PluginMCPServerEntry{}, fmt.Sprintf(
				"跳过 MCP 服务 %q: 不支持的传输 %q",
				serverName,
				server.Type,
			), nil, nil
		}
		server.Type = "http"
	}
	if strings.TrimSpace(server.URL) == "" {
		return types.PluginMCPServerEntry{}, fmt.Sprintf("跳过 MCP 服务 %q: 没有 URL", serverName), nil, nil
	}
	if strings.Contains(server.URL, "${") {
		return types.PluginMCPServerEntry{}, "", nil, fmt.Errorf(
			"%w: plugin %q: MCP 服务 %q 的 URL 中不能使用变量占位符",
			ErrSkillBundleInvalid,
			pluginName,
			serverName,
		)
	}
	var vars []string
	varHeaders := 0
	for key, value := range server.Headers {
		matches := varRegex.FindAllStringSubmatch(value, -1)
		if len(matches) == 0 {
			continue
		}
		varHeaders++
		if varHeaders > 1 {
			return types.PluginMCPServerEntry{}, "", nil, fmt.Errorf(
				"%w: plugin %q: MCP 服务 %q 最多支持一个带 ${VAR} 的 header",
				ErrSkillBundleInvalid,
				pluginName,
				serverName,
			)
		}
		if len(matches) > 1 {
			return types.PluginMCPServerEntry{}, "", nil, fmt.Errorf(
				"%w: plugin %q: MCP 服务 %q 的 header %q 中包含多个变量占位符",
				ErrSkillBundleInvalid,
				pluginName,
				serverName,
				key,
			)
		}
		varName := matches[0][1]
		exact := "${" + varName + "}"
		bearer := "Bearer ${" + varName + "}"
		if value != exact && (!strings.EqualFold(key, "Authorization") || value != bearer) {
			return types.PluginMCPServerEntry{}, "", nil, fmt.Errorf(
				"%w: plugin %q: MCP 服务 %q 的 header %q 格式不支持 (仅支持 Bearer ${VAR} 或纯 ${VAR})",
				ErrSkillBundleInvalid,
				pluginName,
				serverName,
				key,
			)
		}
		vars = append(vars, varName)
	}
	return server, "", vars, nil
}

func mcpServerIsStdio(server types.PluginMCPServerEntry) bool {
	return strings.EqualFold(server.Type, "stdio") || strings.TrimSpace(server.Command) != "" || len(server.Args) > 0
}

func collectPluginSkillDirs(
	files map[string][]byte,
	dirPrefix, pluginName string,
	extra types.PluginPathList,
) ([]pluginSkillDir, error) {
	var dirs []pluginSkillDir
	seen := map[string]bool{}
	add := func(name, absDir string) {
		if name == "" || name == "." || seen[absDir] {
			return
		}
		collected := skillFilesUnder(files, absDir)
		if _, ok := collected["SKILL.md"]; !ok {
			return
		}
		seen[absDir] = true
		dirs = append(dirs, pluginSkillDir{name: name, files: collected})
	}
	scanParent := func(parent string) {
		prefix := parent
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		children := map[string]struct{}{}
		for name := range files {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			rel := strings.TrimPrefix(name, prefix)
			parts := strings.SplitN(rel, "/", 2)
			if len(parts) == 2 && parts[0] != "" {
				children[parts[0]] = struct{}{}
			}
		}
		names := make([]string, 0, len(children))
		for name := range children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			add(name, strings.TrimSuffix(prefix, "/")+"/"+name)
		}
	}

	defaultSkills := joinPluginPath(dirPrefix, "skills")
	if _, ok := files[defaultSkills+"/SKILL.md"]; ok {
		add("skills", defaultSkills)
	} else {
		scanParent(defaultSkills)
	}
	for _, extraPath := range extra {
		rel, err := cleanLocalPluginSource(extraPath, "")
		if err != nil {
			return nil, err
		}
		if rel == "." {
			add(pluginName, strings.TrimSuffix(dirPrefix, "/"))
			continue
		}
		abs := joinPluginPath(dirPrefix, rel)
		if _, ok := files[abs+"/SKILL.md"]; ok {
			add(path.Base(rel), abs)
		} else {
			scanParent(abs)
		}
	}
	if len(dirs) == 0 {
		root := strings.TrimSuffix(dirPrefix, "/")
		name := pluginName
		if base := path.Base(root); root != "" && base != "." {
			name = base
		}
		add(name, root)
	}
	return dirs, nil
}

func joinPluginPath(dirPrefix, rel string) string {
	dir := strings.TrimSuffix(dirPrefix, "/")
	rel = strings.TrimPrefix(rel, "/")
	if dir == "" {
		return rel
	}
	if rel == "" {
		return dir
	}
	return dir + "/" + rel
}

// skillFilesUnder copies one skill directory. An empty dir is the archive root.
func skillFilesUnder(files map[string][]byte, dir string) map[string][]byte {
	if dir == "." {
		dir = ""
	}
	prefix := ""
	if dir != "" {
		prefix = strings.TrimSuffix(dir, "/") + "/"
	}
	out := map[string][]byte{}
	for name, content := range files {
		rel := name
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			rel = strings.TrimPrefix(name, prefix)
		}
		if rel == "" || rel == "." {
			continue
		}
		copied := append([]byte(nil), content...)
		out[rel] = copied
	}
	return out
}

func bytesTrim(raw []byte) []byte {
	return []byte(strings.TrimSpace(string(raw)))
}
