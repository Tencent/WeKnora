package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Loader handles skill discovery and loading from the filesystem
// It implements the Progressive Disclosure pattern by separating
// metadata discovery (Level 1) from instructions loading (Level 2/3)
type Loader struct {
	// skillDirs are the directories to search for skills
	skillDirs []string
	// discoveredSkills caches discovered skill metadata
	discoveredSkills map[string]*Skill
}

// NewLoader creates a new skill loader with the specified search directories
func NewLoader(skillDirs []string) *Loader {
	return &Loader{
		skillDirs:        skillDirs,
		discoveredSkills: make(map[string]*Skill),
	}
}

// DiscoverSkills scans all configured directories for SKILL.md files
// and extracts their metadata (Level 1). This is a lightweight operation
// that only reads the frontmatter of each skill file.
func (l *Loader) DiscoverSkills() ([]*SkillMetadata, error) {
	var allMetadata []*SkillMetadata

	for _, dir := range l.skillDirs {
		metadata, err := l.discoverInDirectory(dir)
		if err != nil {
			// Log warning but continue with other directories
			continue
		}
		allMetadata = append(allMetadata, metadata...)
	}

	return allMetadata, nil
}

// discoverInDirectory scans a single directory for skill subdirectories
func (l *Loader) discoverInDirectory(dir string) ([]*SkillMetadata, error) {
	var metadata []*SkillMetadata

	// Check if directory exists
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // Directory doesn't exist, skip silently
		}
		return nil, fmt.Errorf("failed to access skill directory %s: %w", dir, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	// Read directory entries
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(dir, entry.Name())
		skillFile := filepath.Join(skillPath, SkillFileName)

		// Check if SKILL.md exists
		if _, err := os.Stat(skillFile); os.IsNotExist(err) {
			continue
		}

		// Read and parse SKILL.md
		content, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}

		skill, err := ParseSkillFile(string(content))
		if err != nil {
			continue
		}

		// Set filesystem paths
		skill.BasePath = skillPath
		skill.FilePath = skillFile

		// Cache the skill
		l.discoveredSkills[skill.Name] = skill

		metadata = append(metadata, skill.ToMetadata())
	}

	return metadata, nil
}

// LoadSkillInstructions loads the full instructions of a skill (Level 2)
// Returns the cached skill if already loaded
func (l *Loader) LoadSkillInstructions(skillName string) (*Skill, error) {
	// Check cache first
	if skill, ok := l.discoveredSkills[skillName]; ok {
		if skill.Loaded {
			return skill, nil
		}
	}

	// Search for the skill in all directories
	for _, dir := range l.skillDirs {
		skill, err := l.loadSkillFromDirectory(dir, skillName)
		if err == nil {
			l.discoveredSkills[skillName] = skill
			return skill, nil
		}
	}

	return nil, fmt.Errorf("skill not found: %s", skillName)
}

// loadSkillFromDirectory attempts to load a skill from a specific directory
func (l *Loader) loadSkillFromDirectory(dir, skillName string) (*Skill, error) {
	// First, check if we can find by directory name matching skill name
	skillPath := filepath.Join(dir, skillName)
	skillFile := filepath.Join(skillPath, SkillFileName)

	if _, err := os.Stat(skillFile); err == nil {
		return l.loadSkillFile(skillPath, skillFile)
	}

	// Otherwise, scan all subdirectories to find the skill by name
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillPath := filepath.Join(dir, entry.Name())
		skillFile := filepath.Join(skillPath, SkillFileName)

		if _, err := os.Stat(skillFile); os.IsNotExist(err) {
			continue
		}

		content, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}

		skill, err := ParseSkillFile(string(content))
		if err != nil {
			continue
		}

		if skill.Name == skillName {
			skill.BasePath = skillPath
			skill.FilePath = skillFile
			return skill, nil
		}
	}

	return nil, fmt.Errorf("skill not found in %s: %s", dir, skillName)
}

// loadSkillFile reads and parses a SKILL.md file
func (l *Loader) loadSkillFile(basePath, filePath string) (*Skill, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill file: %w", err)
	}

	skill, err := ParseSkillFile(string(content))
	if err != nil {
		return nil, err
	}

	skill.BasePath = basePath
	skill.FilePath = filePath

	return skill, nil
}

// LoadSkillFile loads an additional file from a skill directory (Level 3)
// The filePath should be relative to the skill's base directory
func (l *Loader) LoadSkillFile(skillName, relativePath string) (*SkillFile, error) {
	// Get the skill first
	skill, ok := l.discoveredSkills[skillName]
	if !ok {
		// Try to load the skill
		var err error
		skill, err = l.LoadSkillInstructions(skillName)
		if err != nil {
			return nil, fmt.Errorf("skill not found: %s", skillName)
		}
	}

	// Validate and resolve the file path
	cleanPath := filepath.Clean(relativePath)

	// Security: prevent path traversal
	if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
		return nil, fmt.Errorf("invalid file path: %s", relativePath)
	}

	fullPath := filepath.Join(skill.BasePath, cleanPath)

	// Verify the file is within the skill directory
	absSkillPath, err := filepath.Abs(skill.BasePath)
	if err != nil {
		return nil, err
	}
	absFilePath, err := filepath.Abs(fullPath)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(absFilePath, absSkillPath) {
		return nil, fmt.Errorf("file path outside skill directory: %s", relativePath)
	}

	// Read the file
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return &SkillFile{
		Name:     relativePath,
		Path:     absFilePath, // Use absolute path for sandbox execution
		Content:  string(content),
		IsScript: IsScript(relativePath),
	}, nil
}

// ListSkillFiles lists all files in a skill directory
func (l *Loader) ListSkillFiles(skillName string) ([]string, error) {
	skill, ok := l.discoveredSkills[skillName]
	if !ok {
		var err error
		skill, err = l.LoadSkillInstructions(skillName)
		if err != nil {
			return nil, fmt.Errorf("skill not found: %s", skillName)
		}
	}

	var files []string

	err := filepath.Walk(skill.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(skill.BasePath, path)
		if err != nil {
			return err
		}

		files = append(files, relPath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list skill files: %w", err)
	}

	return files, nil
}

// GetSkillByName returns a cached skill by name
func (l *Loader) GetSkillByName(name string) (*Skill, bool) {
	skill, ok := l.discoveredSkills[name]
	return skill, ok
}

// GetSkillBasePath returns the base path for a skill (always absolute)
func (l *Loader) GetSkillBasePath(skillName string) (string, error) {
	skill, ok := l.discoveredSkills[skillName]
	if !ok {
		var err error
		skill, err = l.LoadSkillInstructions(skillName)
		if err != nil {
			return "", fmt.Errorf("skill not found: %s", skillName)
		}
	}
	// Return absolute path for consistent sandbox execution
	return filepath.Abs(skill.BasePath)
}

// Reload clears the cache and rediscovers all skills
func (l *Loader) Reload() ([]*SkillMetadata, error) {
	l.discoveredSkills = make(map[string]*Skill)
	return l.DiscoverSkills()
}
