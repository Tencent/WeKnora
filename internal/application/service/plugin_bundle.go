package service

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	varRegex = regexp.MustCompile(`\$\{([A-Za-z0-9_]+)\}`)
	// pluginNamePattern keeps a plugin name usable as one URL path segment
	// and as the prefix of skill names. MCP services are prefixed with the
	// marketplace name instead.
	// Dots stay in the installed name (acme.tools). ".." is rejected below.
	pluginNamePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_.-]*$`)
)

// maxPluginNameLength leaves room for "__<skill>" within skills.MaxNameLength.
const maxPluginNameLength = 48

// validatePluginName rejects names that would break routes or make the
// "<plugin>__<resource>" prefix ambiguous.
func validatePluginName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("cannot be empty")
	case len(name) > maxPluginNameLength:
		return fmt.Errorf("longer than %d bytes", maxPluginNameLength)
	case !pluginNamePattern.MatchString(name):
		return fmt.Errorf(
			"must start with a letter or digit and contain only letters, digits, hyphens, underscores and dots",
		)
	case strings.Contains(name, ".."):
		return fmt.Errorf("must not contain \"..\"")
	case strings.Contains(name, "__"):
		return fmt.Errorf("must not contain \"__\"")
	}
	return nil
}

// ParsedMarketplace contains the decoded marketplace manifest, normalized raw files, and parsed plugins.
type ParsedMarketplace struct {
	Manifest types.MarketplaceManifest
	RawFiles map[string][]byte
	Plugins  map[string]*ParsedPlugin
	// Order lists plugin names in marketplace.json order.
	Order []string
	// Warnings explains entries that were left out, such as a plugin whose
	// source points at another repository.
	Warnings []string
}

// ParsedPlugin represents one plugin extracted from a marketplace bundle.
type ParsedPlugin struct {
	Name            string
	Source          string
	Description     string
	Version         string
	Category        string
	Keywords        []string
	Manifest        *types.PluginManifest
	HasPluginJSON   bool
	MCPServers      map[string]types.PluginMCPServerEntry
	MCPWarnings     []string
	Skills          []ParsedPluginSkill
	SkillCount      int
	MCPCount        int
	RequiresSandbox bool
	EnvDeclarations []types.PluginEnvVar
}

// ParsedPluginSkill represents a repackaged skill with the prefixed name.
type ParsedPluginSkill struct {
	OriginalName string
	PrefixedName string
	Archive      []byte // Re-packed zip with SKILL.md at root
	Bundle       *SkillBundle
}

// UnpackMarketplaceArchive extracts a zip or tar.gz archive into a normalized in-memory file map.
func UnpackMarketplaceArchive(archive []byte) (map[string][]byte, error) {
	var raw map[string][]byte
	var err error

	if len(archive) >= 2 && archive[0] == 0x1f && archive[1] == 0x8b {
		raw, err = unpackTarGzArchive(archive)
	} else {
		raw, err = unpackZipArchive(archive)
		if err != nil {
			// Fallback: check if uncompressed tar
			var tarErr error
			raw, tarErr = unpackTarGzArchive(archive)
			if tarErr == nil {
				err = nil
			}
		}
	}
	if err != nil {
		return nil, err
	}
	return normalizeMarketplaceFiles(raw)
}

func unpackTarGzArchive(archive []byte) (map[string][]byte, error) {
	var tr *tar.Reader
	if len(archive) >= 2 && archive[0] == 0x1f && archive[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, fmt.Errorf("%w: invalid gzip: %v", ErrSkillBundleInvalid, err)
		}
		defer func() { _ = gr.Close() }()
		tr = tar.NewReader(gr)
	} else {
		tr = tar.NewReader(bytes.NewReader(archive))
	}

	raw := make(map[string][]byte)
	var totalBytes int64
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: failed to read tar entry: %v", ErrSkillBundleInvalid, err)
		}
		if header.Typeflag == tar.TypeDir || header.Typeflag == tar.TypeSymlink {
			continue
		}
		// NUL is the pre-ustar regular-file flag (formerly tar.TypeRegA).
		if header.Typeflag != tar.TypeReg && header.Typeflag != 0 {
			return nil, fmt.Errorf("%w: entry %q is not a regular file", ErrSkillBundleInvalid, header.Name)
		}

		cleanPath := path.Clean(header.Name)
		if cleanPath == "." || strings.HasPrefix(cleanPath, "..") || path.IsAbs(cleanPath) ||
			strings.Contains(cleanPath, "../") {
			return nil, fmt.Errorf("%w: entry %q escapes the archive root", ErrSkillBundleInvalid, header.Name)
		}

		if header.Size > maxSkillBundleFileBytes {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, header.Name)
		}
		if totalBytes+header.Size > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}

		buf, err := io.ReadAll(io.LimitReader(tr, maxSkillBundleFileBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read entry %q: %v", ErrSkillBundleInvalid, header.Name, err)
		}
		if int64(len(buf)) > maxSkillBundleFileBytes {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, header.Name)
		}
		totalBytes += int64(len(buf))
		if totalBytes > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}

		raw[cleanPath] = buf
		if len(raw) > maxSkillBundleFiles {
			return nil, fmt.Errorf("%w: archive holds more than %d files", ErrSkillBundleInvalid, maxSkillBundleFiles)
		}
	}
	return raw, nil
}

func unpackZipArchive(archive []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a readable zip archive: %v", ErrSkillBundleInvalid, err)
	}
	if len(reader.File) > maxSkillBundleZipEntries {
		return nil, fmt.Errorf(
			"%w: archive has more than %d zip entries",
			ErrSkillBundleInvalid,
			maxSkillBundleZipEntries,
		)
	}

	raw := make(map[string][]byte)
	var totalBytes int64
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		cleanPath := path.Clean(entry.Name)
		if cleanPath == "." || strings.HasPrefix(cleanPath, "..") || path.IsAbs(cleanPath) ||
			strings.Contains(cleanPath, "../") {
			return nil, fmt.Errorf("%w: entry %q escapes the archive root", ErrSkillBundleInvalid, entry.Name)
		}
		// A marketplace file is often a symlink to another channel's catalog
		// (GitHub Copilot links .claude-plugin/marketplace.json at the Copilot
		// file). Skip the link and let the next catalog be tried. Never follow
		// it: the target can point outside the archive.
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			continue
		}
		size := entry.FileInfo().Size()
		if size > maxSkillBundleFileBytes {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, entry.Name)
		}
		if totalBytes+size > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}

		rc, err := entry.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read %q: %v", ErrSkillBundleInvalid, entry.Name, err)
		}
		content, err := io.ReadAll(io.LimitReader(rc, maxSkillBundleFileBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read %q: %v", ErrSkillBundleInvalid, entry.Name, err)
		}
		if int64(len(content)) > maxSkillBundleFileBytes {
			return nil, fmt.Errorf("%w: entry %q is too large", ErrSkillBundleInvalid, entry.Name)
		}
		totalBytes += int64(len(content))
		if totalBytes > maxSkillBundleTotalBytes {
			return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
		}
		raw[cleanPath] = content
		if len(raw) > maxSkillBundleFiles {
			return nil, fmt.Errorf("%w: archive holds more than %d files", ErrSkillBundleInvalid, maxSkillBundleFiles)
		}
	}
	return raw, nil
}

func normalizeMarketplaceFiles(raw map[string][]byte) (map[string][]byte, error) {
	_, prefix, ok := findMarketplaceManifest(raw)
	if !ok {
		// A plugin directory without a catalog is parsed as-is. A single
		// wrapping folder is removed later, once the marketplace name can
		// still be taken from that folder.
		return raw, nil
	}
	return stripMarketplacePrefix(raw, prefix), nil
}

// ParsePluginMarketplace parses a marketplace archive into WeKnora plugins.
// It accepts WeKnora, Claude, Cursor, Copilot and Codex catalogs, and a
// directory that is itself one plugin or a single level of plugins.
func ParsePluginMarketplace(files map[string][]byte) (*ParsedMarketplace, error) {
	hits := marketplaceManifestCandidates(files)
	if len(hits) == 0 {
		return parseLoosePluginBundle(files)
	}
	var skipped []string
	for _, hit := range hits {
		normalized := stripMarketplacePrefix(files, hit.prefix)
		parsed, err := parseMarketplaceCatalog(normalized, hit.relPath)
		if err == nil {
			if len(skipped) > 0 {
				parsed.Warnings = append(skipped, parsed.Warnings...)
			}
			return parsed, nil
		}
		label := hit.relPath
		if hit.prefix != "" {
			label = hit.prefix + "/" + hit.relPath
		}
		reason := strings.TrimPrefix(err.Error(), ErrSkillBundleInvalid.Error()+": ")
		skipped = append(skipped, fmt.Sprintf("跳过清单 %s：%s", label, reason))
	}
	return nil, fmt.Errorf("%w: %s", ErrSkillBundleInvalid, strings.Join(skipped, "；"))
}

func parsePluginFromFiles(files map[string][]byte, entry types.MarketplacePluginEntry) (*ParsedPlugin, error) {
	cleanSource := path.Clean(strings.TrimPrefix(strings.TrimSpace(entry.Source), "./"))
	if cleanSource == "." {
		cleanSource = ""
	}

	pluginDirPrefix := cleanSource
	if pluginDirPrefix != "" {
		pluginDirPrefix += "/"
	}

	pluginManifest, hasPluginJSON, err := loadPluginManifest(files, pluginDirPrefix)
	if err != nil {
		return nil, err
	}

	pluginName := entry.Name
	if pluginManifest != nil && strings.TrimSpace(pluginManifest.Name) != "" {
		pluginName = strings.TrimSpace(pluginManifest.Name)
	}
	if err := validatePluginName(pluginName); err != nil {
		return nil, fmt.Errorf("%w: plugin name %q: %v", ErrSkillBundleInvalid, pluginName, err)
	}

	// Validate env declarations in plugin.json
	declaredEnvMap := make(map[string]bool)
	var envDeclarations []types.PluginEnvVar
	if pluginManifest != nil && len(pluginManifest.Env) > 0 {
		for _, e := range pluginManifest.Env {
			name := strings.TrimSpace(e.Name)
			if err := validateUserEnvName(name); err != nil {
				return nil, fmt.Errorf(
					"%w: plugin %q: invalid env declaration %q: %v",
					ErrSkillBundleInvalid,
					pluginName,
					name,
					err,
				)
			}
			if declaredEnvMap[name] {
				return nil, fmt.Errorf(
					"%w: plugin %q: duplicate env declaration %q",
					ErrSkillBundleInvalid,
					pluginName,
					name,
				)
			}
			declaredEnvMap[name] = true
			envDeclarations = append(envDeclarations, types.PluginEnvVar{
				Name:        name,
				Description: e.Description,
				Required:    e.Required,
			})
		}
	}

	var mcpDecl types.PluginMCPConfig
	var skillPaths types.PluginPathList
	if pluginManifest != nil {
		mcpDecl = pluginManifest.MCPServers
		skillPaths = pluginManifest.Skills
	}
	mcpServers, mcpWarnings, headerVars, err := collectPluginMCPServers(files, pluginDirPrefix, pluginName, mcpDecl)
	if err != nil {
		return nil, err
	}
	inferred := make([]string, 0)
	seenInferred := map[string]bool{}
	for _, varName := range headerVars {
		if declaredEnvMap[varName] || seenInferred[varName] {
			continue
		}
		seenInferred[varName] = true
		inferred = append(inferred, varName)
	}
	sort.Strings(inferred)
	for _, varName := range inferred {
		if err := validateUserEnvName(varName); err != nil {
			return nil, fmt.Errorf(
				"%w: plugin %q: MCP 请求头中的 ${%s} 不能作为环境变量: %v",
				ErrSkillBundleInvalid,
				pluginName,
				varName,
				err,
			)
		}
		declaredEnvMap[varName] = true
		envDeclarations = append(envDeclarations, types.PluginEnvVar{
			Name:        varName,
			Description: "由 MCP 请求头中的 ${" + varName + "} 自动识别",
			Required:    true,
		})
	}

	skillDirs, err := collectPluginSkillDirs(files, pluginDirPrefix, pluginName, skillPaths)
	if err != nil {
		return nil, fmt.Errorf("%w: plugin %q: %v", ErrSkillBundleInvalid, pluginName, err)
	}
	var parsedSkills []ParsedPluginSkill
	for _, skillDir := range skillDirs {
		subDir := skillDir.name
		skillFiles := skillDir.files
		skillMD, ok := skillFiles["SKILL.md"]
		if !ok {
			continue
		}

		prefixedName := fmt.Sprintf("%s__%s", pluginName, subDir)
		if len(prefixedName) > skills.MaxNameLength {
			return nil, fmt.Errorf("%w: 技能前缀名称 %q 超过最大长度 %d", ErrSkillBundleInvalid, prefixedName, skills.MaxNameLength)
		}

		rewrittenMD, err := rewriteSkillFrontmatterName(skillMD, prefixedName)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: plugin %q: rewrite SKILL.md for %s failed: %v",
				ErrSkillBundleInvalid,
				pluginName,
				subDir,
				err,
			)
		}
		skillFiles["SKILL.md"] = rewrittenMD

		zipArchive, err := zipSkillFiles(skillFiles)
		if err != nil {
			return nil, fmt.Errorf("%w: repackage skill %s failed: %v", ErrSkillBundleInvalid, prefixedName, err)
		}

		bundle, err := ParseSkillBundle(zipArchive)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: validate repackaged skill %s failed: %v",
				ErrSkillBundleInvalid,
				prefixedName,
				err,
			)
		}

		parsedSkills = append(parsedSkills, ParsedPluginSkill{
			OriginalName: subDir,
			PrefixedName: prefixedName,
			Archive:      zipArchive,
			Bundle:       bundle,
		})
	}

	// Sort skills deterministically by PrefixedName
	sort.Slice(parsedSkills, func(i, j int) bool {
		return parsedSkills[i].PrefixedName < parsedSkills[j].PrefixedName
	})

	version := strings.TrimSpace(entry.Version)
	if pluginManifest != nil && strings.TrimSpace(pluginManifest.Version) != "" {
		version = strings.TrimSpace(pluginManifest.Version)
	}
	desc := entry.Description
	if pluginManifest != nil && pluginManifest.Description != "" {
		desc = pluginManifest.Description
	}
	keywords := entry.Keywords
	if len(keywords) == 0 && pluginManifest != nil {
		keywords = pluginManifest.Keywords
	}

	return &ParsedPlugin{
		Name:            pluginName,
		Source:          entry.Source,
		Description:     desc,
		Version:         version,
		Category:        entry.Category,
		Keywords:        keywords,
		Manifest:        pluginManifest,
		HasPluginJSON:   hasPluginJSON,
		MCPServers:      mcpServers,
		MCPWarnings:     mcpWarnings,
		Skills:          parsedSkills,
		SkillCount:      len(parsedSkills),
		MCPCount:        len(mcpServers),
		RequiresSandbox: len(parsedSkills) > 0,
		EnvDeclarations: envDeclarations,
	}, nil
}

func rewriteSkillFrontmatterName(content []byte, newName string) ([]byte, error) {
	lines := strings.Split(string(content), "\n")
	frontmatterStart := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			frontmatterStart = i
			break
		}
		if trimmed != "" {
			break
		}
	}
	if frontmatterStart < 0 {
		return nil, fmt.Errorf("SKILL.md missing frontmatter")
	}

	frontmatterEnd := -1
	for i := frontmatterStart + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			frontmatterEnd = i
			break
		}
	}
	if frontmatterEnd < 0 {
		return nil, fmt.Errorf("SKILL.md missing closing frontmatter ---")
	}

	nameReplaced := false
	var newLines []string
	newLines = append(newLines, lines[:frontmatterStart+1]...)
	for i := frontmatterStart + 1; i < frontmatterEnd; i++ {
		line := lines[i]
		// Only the top-level key: an indented "name:" belongs to a nested map.
		if strings.HasPrefix(line, "name:") {
			newLines = append(newLines, fmt.Sprintf("name: %s", newName))
			nameReplaced = true
		} else {
			newLines = append(newLines, line)
		}
	}
	if !nameReplaced {
		newLines = append(newLines, fmt.Sprintf("name: %s", newName))
	}
	newLines = append(newLines, lines[frontmatterEnd:]...)
	return []byte(strings.Join(newLines, "\n")), nil
}
