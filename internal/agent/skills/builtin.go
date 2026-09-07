package skills

import (
	_ "embed"
	"fmt"
)

// InstallerSkillName identifies the platform's built-in installation instructions.
const InstallerSkillName = "skill-installer"

//go:embed builtin/skill-installer/SKILL.md
var installerSkillDocument string

// builtinSource contains platform instructions, not executable image packages.
type builtinSource struct{}

func (builtinSource) DiscoverSkills() ([]*SkillMetadata, error) {
	skill, err := (builtinSource{}).LoadSkillInstructions(InstallerSkillName)
	if err != nil {
		return nil, err
	}
	return []*SkillMetadata{skill.ToMetadata()}, nil
}

func (builtinSource) LoadSkillInstructions(name string) (*Skill, error) {
	if name != InstallerSkillName {
		return nil, fmt.Errorf("unknown built-in skill: %s", name)
	}
	skill, err := ParseSkillFile(installerSkillDocument)
	if err != nil {
		return nil, err
	}
	skill.Loaded = true
	return skill, nil
}

func (b builtinSource) LoadSkillFile(name, relativePath string) (*SkillFile, error) {
	if _, err := b.LoadSkillInstructions(name); err != nil {
		return nil, err
	}
	if relativePath != SkillFileName {
		return nil, fmt.Errorf("built-in skill resource not found: %s", relativePath)
	}
	return &SkillFile{Name: SkillFileName, Content: installerSkillDocument}, nil
}

func (b builtinSource) ListSkillFiles(name string) ([]string, error) {
	if _, err := b.LoadSkillInstructions(name); err != nil {
		return nil, err
	}
	return []string{SkillFileName}, nil
}

func (builtinSource) GetSkillBasePath(name string) (string, error) {
	return "", fmt.Errorf("built-in skill %q uses platform tools and has no shell directory", name)
}

// WithInstaller enables the built-in only when its authenticated tool exists.
// Call before Initialize. Platform instructions take precedence over a package
// with the same name and are independent of the installed-skill whitelist.
func (m *Manager) WithInstaller() *Manager {
	m.installerEnabled = true
	return m
}

// IsBuiltin reports whether a name resolves to enabled platform instructions.
func (m *Manager) IsBuiltin(name string) bool {
	return m.installerEnabled && name == InstallerSkillName
}
