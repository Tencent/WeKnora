// Package skills provides Agent Skills functionality following Claude's Progressive Disclosure pattern.
// Skills are modular capabilities that extend the agent's functionality through instruction files.
package skills

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill validation constants following Claude's specification
const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
	SkillFileName        = "SKILL.md"
)

// Reserved words that cannot be used in skill names
var reservedWords = []string{"anthropic", "claude"}

// namePattern validates skill names: unicode letters, numbers only
var namePattern = regexp.MustCompile(`^[\p{L}\p{N}-]+$`)

// xmlTagPattern detects XML tags in content
var xmlTagPattern = regexp.MustCompile(`<[^>]+>`)

// Skill represents a loaded skill with its metadata and content
// It follows the Progressive Disclosure pattern:
// - Level 1 (Metadata): Name and Description are always loaded
// - Level 2 (Instructions): The main body of SKILL.md, loaded on demand
// - Level 3 (Resources): Additional files in the skill directory, loaded as needed
type Skill struct {
	// Metadata (Level 1) - always loaded
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// Filesystem information
	BasePath string // Absolute path to the skill directory
	FilePath string // Absolute path to SKILL.md

	// Instructions (Level 2) - loaded on demand
	Instructions string // The main body of SKILL.md (after frontmatter)
	Loaded       bool   // Whether Level 2 instructions have been loaded
}

// SkillMetadata represents the minimal metadata for system prompt injection (Level 1)
// This is the lightweight representation used during skill discovery
type SkillMetadata struct {
	Name        string
	Description string
	BasePath    string // Path to skill directory for later loading
}

// SkillFile represents an additional file within a skill directory (Level 3)
type SkillFile struct {
	Name     string // Filename (e.g., "FORMS.md", "scripts/validate.py")
	Path     string // Absolute path to the file
	Content  string // File content
	IsScript bool   // Whether this is an executable script
}

// Validate checks if the skill metadata is valid according to Claude's specification
func (s *Skill) Validate() error {
	// Validate name
	if s.Name == "" {
		return errors.New("skill name is required")
	}
	if len(s.Name) > MaxNameLength {
		return fmt.Errorf("skill name exceeds maximum length of %d characters", MaxNameLength)
	}
	if !namePattern.MatchString(s.Name) {
		return errors.New("skill name must contain only lowercase letters, numbers, and hyphens")
	}
	for _, reserved := range reservedWords {
		if strings.Contains(s.Name, reserved) {
			return fmt.Errorf("skill name cannot contain reserved word: %s", reserved)
		}
	}
	if xmlTagPattern.MatchString(s.Name) {
		return errors.New("skill name cannot contain XML tags")
	}

	// Validate description
	if s.Description == "" {
		return errors.New("skill description is required")
	}
	if len(s.Description) > MaxDescriptionLength {
		return fmt.Errorf("skill description exceeds maximum length of %d characters", MaxDescriptionLength)
	}
	if xmlTagPattern.MatchString(s.Description) {
		return errors.New("skill description cannot contain XML tags")
	}

	return nil
}

// ToMetadata converts a Skill to its lightweight metadata representation
func (s *Skill) ToMetadata() *SkillMetadata {
	return &SkillMetadata{
		Name:        s.Name,
		Description: s.Description,
		BasePath:    s.BasePath,
	}
}

// ParseSkillFile parses a SKILL.md file content and extracts metadata and body
// It handles YAML frontmatter enclosed in --- delimiters
func ParseSkillFile(content string) (*Skill, error) {
	skill := &Skill{}

	// Check for YAML frontmatter
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return nil, errors.New("SKILL.md must start with YAML frontmatter (---)")
	}

	// Find the end of frontmatter
	scanner := bufio.NewScanner(strings.NewReader(content))
	var frontmatterLines []string
	var bodyLines []string
	inFrontmatter := false
	frontmatterEnded := false

	for scanner.Scan() {
		line := scanner.Text()

		if !inFrontmatter && !frontmatterEnded && strings.TrimSpace(line) == "---" {
			inFrontmatter = true
			continue
		}

		if inFrontmatter && strings.TrimSpace(line) == "---" {
			inFrontmatter = false
			frontmatterEnded = true
			continue
		}

		if inFrontmatter {
			frontmatterLines = append(frontmatterLines, line)
		} else if frontmatterEnded {
			bodyLines = append(bodyLines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading SKILL.md: %w", err)
	}

	if !frontmatterEnded {
		return nil, errors.New("SKILL.md frontmatter is not properly closed with ---")
	}

	// Parse YAML frontmatter
	frontmatter := strings.Join(frontmatterLines, "\n")
	if err := yaml.Unmarshal([]byte(frontmatter), skill); err != nil {
		return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	// Set body instructions
	skill.Instructions = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	skill.Loaded = true

	// Validate
	if err := skill.Validate(); err != nil {
		return nil, fmt.Errorf("skill validation failed: %w", err)
	}

	return skill, nil
}

// ParseSkillMetadata parses only the metadata from a SKILL.md file content
// This is a lightweight operation for skill discovery (Level 1 only)
func ParseSkillMetadata(content string) (*SkillMetadata, error) {
	skill, err := ParseSkillFile(content)
	if err != nil {
		return nil, err
	}
	return skill.ToMetadata(), nil
}

// IsScript checks if a file path represents an executable script
func IsScript(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	scriptExtensions := map[string]bool{
		".py":   true,
		".sh":   true,
		".bash": true,
		".js":   true,
		".ts":   true,
		".rb":   true,
		".pl":   true,
		".php":  true,
	}
	return scriptExtensions[ext]
}

// GetScriptLanguage returns the language/interpreter for a script file
func GetScriptLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	languages := map[string]string{
		".py":   "python",
		".sh":   "bash",
		".bash": "bash",
		".js":   "node",
		".ts":   "ts-node",
		".rb":   "ruby",
		".pl":   "perl",
		".php":  "php",
	}
	if lang, ok := languages[ext]; ok {
		return lang
	}
	return "unknown"
}
