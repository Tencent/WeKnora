package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Summary contains a summary of a skill
type Summary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "preloaded", "installed", "url"
}

// Doc contains a doc of a skill
type Doc struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// FullSkill contains a full skill
type FullSkill struct {
	Summary      Summary `json:"summary"`
	Body         string  `json:"body"`
	Docs         []Doc   `json:"docs"`
	Instructions string  `json:"instructions"`
	BasePath     string  `json:"base_path"`
}

// Repository provides access to skills
type Repository interface {
	// Summaries returns all available skill summaries
	Summaries() []Summary
	// Get returns a full skill by name
	Get(name string) (*FullSkill, error)
	// Path returns the directory path containing the specified skill
	Path(name string) (string, error)
}

// RefreshableRepository supports refreshing the skill index
type RefreshableRepository interface {
	Repository
	Refresh() error
}

// InstallableRepository supports installing and uninstalling skills
type InstallableRepository interface {
	Repository
	// Install installs a skill from the specified path
	Install(name string, sourcePath string) error
	// Uninstall uninstalls the specified skill
	Uninstall(name string) error
	// InstallDir returns the directory where skills are installed
	InstallDir() string
}

// FSRepository is a repository that scans a filesystem for skills
type FSRepository struct {
	roots []string
	mu    sync.RWMutex
	// name -> directory containing SKILL.md
	index map[string]indexEntry
}

// indexEntry stores skill index information
type indexEntry struct {
	Dir    string // directory path
	Source string // source identifier
}

// NewFSRepository creates an FSRepository that scans the specified roots
func NewFSRepository(roots ...string) (*FSRepository, error) {
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		resolved = append(resolved, root)
	}
	index, err := scanRoots(resolved)
	if err != nil {
		return nil, err
	}
	return &FSRepository{
		roots: resolved,
		index: index,
	}, nil
}

// Refresh refreshes the skill index
func (r *FSRepository) Refresh() error {
	index, err := scanRoots(r.roots)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.index = index
	r.mu.Unlock()
	return nil
}

// AddRoot adds a root to the repository
func (r *FSRepository) AddRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	r.mu.Lock()
	r.roots = append(r.roots, root)
	r.mu.Unlock()
	return r.Refresh()
}

// Path returns the directory path containing the specified skill
func (r *FSRepository) Path(name string) (string, error) {
	r.mu.RLock()
	entry, ok := r.index[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	return entry.Dir, nil
}

func scanRoots(roots []string) (map[string]indexEntry, error) {
	index := map[string]indexEntry{}
	seen := map[string]struct{}{}
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != "" {
			root = resolved
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}

		source := "preloaded"
		if strings.Contains(root, "installed") || strings.Contains(root, "user") {
			source = "installed"
		}

		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			sf := filepath.Join(p, SkillFileName)
			st, err2 := os.Stat(sf)
			if err2 != nil || st.IsDir() {
				return nil
			}
			content, err3 := os.ReadFile(sf)
			if err3 != nil {
				return nil
			}
			skill, err4 := ParseSkillFile(string(content))
			if err4 != nil {
				return nil
			}
			name := strings.TrimSpace(skill.Name)
			if name == "" {
				name = filepath.Base(p)
			}
			if strings.TrimSpace(name) == "" {
				return nil
			}
			// 记录首次出现的；后续的忽略
			if _, ok := index[name]; !ok {
				index[name] = indexEntry{Dir: p, Source: source}
			}
			return nil
		})
	}
	return index, nil
}

// Summaries returns all available skill summaries
func (r *FSRepository) Summaries() []Summary {
	r.mu.RLock()
	indexCopy := make(map[string]indexEntry, len(r.index))
	for name, entry := range r.index {
		indexCopy[name] = entry
	}
	r.mu.RUnlock()

	out := make([]Summary, 0, len(indexCopy))
	for name, entry := range indexCopy {
		sf := filepath.Join(entry.Dir, SkillFileName)
		content, err := os.ReadFile(sf)
		if err != nil {
			continue
		}
		skill, err := ParseSkillFile(string(content))
		if err != nil {
			continue
		}
		s := Summary{
			Name:        skill.Name,
			Description: skill.Description,
			Source:      entry.Source,
		}
		if s.Name == "" {
			s.Name = name
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns the full skill
func (r *FSRepository) Get(name string) (*FullSkill, error) {
	r.mu.RLock()
	entry, ok := r.index[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	sf := filepath.Join(entry.Dir, SkillFileName)
	content, err := os.ReadFile(sf)
	if err != nil {
		return nil, err
	}
	skill, err := ParseSkillFile(string(content))
	if err != nil {
		return nil, err
	}
	sum := Summary{
		Name:        skill.Name,
		Description: skill.Description,
		Source:      entry.Source,
	}
	if sum.Name == "" {
		sum.Name = name
	}
	docs := readDocs(entry.Dir)
	return &FullSkill{
		Summary:      sum,
		Body:         skill.Instructions,
		Docs:         docs,
		Instructions: skill.Instructions,
		BasePath:     entry.Dir,
	}, nil
}

// readDocs reads all doc files in the directory
func readDocs(dir string) []Doc {
	var docs []Doc
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), SkillFileName) {
			return nil
		}
		if !isDocFile(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		docs = append(docs, Doc{
			Path:    filepath.ToSlash(rel),
			Content: string(b),
		})
		return nil
	})
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Path < docs[j].Path
	})
	return docs
}

func isDocFile(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".txt")
}
