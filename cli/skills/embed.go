// Package skills embeds the bundled Agent Skills into the CLI binary so
// `weknora skills install` can write them out without a source checkout
// (replacing the manual symlink-from-checkout MVP). The markdown under each
// skill dir is the single source of truth — embedding it keeps the shipped
// binary and the docs in lockstep (the parity tests guard the command/flag
// vocabulary the skills reference).
package skills

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed all:weknora-shared all:weknora-rag-search
var fsys embed.FS

// Skill is one bundled skill: its directory name, the description parsed from
// its SKILL.md frontmatter, and the relative file paths it contains.
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
}

// FS exposes the embedded skill tree for callers that want raw access.
func FS() fs.FS { return fsys }

// List returns the bundled skills (top-level dirs that contain a SKILL.md),
// sorted by name.
func List() ([]Skill, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := skillFiles(e.Name())
		if err != nil {
			return nil, err
		}
		if !hasSkillMD(files) {
			continue
		}
		skills = append(skills, Skill{
			Name:        e.Name(),
			Description: descriptionOf(e.Name()),
			Files:       files,
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// skillFiles returns every file path under a skill dir (relative to the FS
// root, e.g. "weknora-shared/SKILL.md"), sorted.
func skillFiles(dir string) ([]string, error) {
	var files []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func hasSkillMD(files []string) bool {
	for _, f := range files {
		if path.Base(f) == "SKILL.md" {
			return true
		}
	}
	return false
}

// descriptionOf reads the `description:` line from a skill's SKILL.md YAML
// frontmatter. Best-effort: returns "" if absent or unreadable.
func descriptionOf(dir string) string {
	b, err := fs.ReadFile(fsys, path.Join(dir, "SKILL.md"))
	if err != nil {
		return ""
	}
	inFrontmatter := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if t == "---" {
			if inFrontmatter {
				break // end of frontmatter
			}
			inFrontmatter = true
			continue
		}
		if inFrontmatter && strings.HasPrefix(t, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(t, "description:"))
		}
	}
	return ""
}

// ReadFile returns the bytes of one embedded file path (as reported in
// Skill.Files), for `skills install` to write to disk.
func ReadFile(p string) ([]byte, error) { return fsys.ReadFile(p) }
