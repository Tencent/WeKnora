package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var datasetIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var errDatasetNotFound = errors.New("dataset not found")

type projectStore struct {
	root                  string
	projects              string
	backups               string
	trash                 string
	exports               string
	snapshots             string
	evaluations           string
	connections           string
	generationConnections string
	generationJobs        string
	generationCache       string
	settings              string
	mu                    sync.RWMutex
	now                   func() time.Time
}

func newProjectStore(root string) (*projectStore, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	store := &projectStore{
		root:                  absRoot,
		projects:              filepath.Join(absRoot, "projects"),
		backups:               filepath.Join(absRoot, "backups"),
		trash:                 filepath.Join(absRoot, "trash"),
		exports:               filepath.Join(absRoot, "exports"),
		snapshots:             filepath.Join(absRoot, "snapshots"),
		evaluations:           filepath.Join(absRoot, "evaluations"),
		connections:           filepath.Join(absRoot, "connections"),
		generationConnections: filepath.Join(absRoot, "generation-connections"),
		generationJobs:        filepath.Join(absRoot, "generation-jobs"),
		generationCache:       filepath.Join(absRoot, "generation-cache"),
		settings:              filepath.Join(absRoot, "workspace-settings.json"),
		now:                   time.Now,
	}
	for _, dir := range []string{store.projects, store.backups, store.trash, store.exports, store.snapshots, store.evaluations, store.connections, store.generationConnections, store.generationJobs, store.generationCache} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create workspace directory: %w", err)
		}
	}
	return store, nil
}

func validateDatasetID(id string) error {
	if !datasetIDPattern.MatchString(id) {
		return errors.New("数据集 ID 只能包含小写字母、数字和短横线，长度为 1～63")
	}
	return nil
}

func (s *projectStore) projectPath(id string) (string, error) {
	if err := validateDatasetID(id); err != nil {
		return "", err
	}
	return filepath.Join(s.projects, id+".json"), nil
}

func (s *projectStore) list() ([]DatasetSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.projects)
	if err != nil {
		return nil, err
	}
	summaries := make([]DatasetSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		project, err := readProjectFile(filepath.Join(s.projects, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		summaries = append(summaries, summarizeProject(project))
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return summaries, nil
}

func (s *projectStore) create(req createDatasetRequest) (*DatasetProject, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	req.Version = strings.TrimSpace(req.Version)
	if err := validateDatasetID(req.ID); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, errors.New("数据集名称不能为空")
	}
	if req.Version == "" {
		req.Version = "0.1.0"
	}
	settings, err := s.getWorkspaceSettings()
	if err != nil {
		return nil, err
	}
	project := newDatasetProject(req, s.now(), settings)

	s.mu.Lock()
	defer s.mu.Unlock()
	path, _ := s.projectPath(req.ID)
	if _, err := os.Stat(path); err == nil {
		return nil, errors.New("数据集 ID 已存在")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := writeProjectFileAtomic(path, project); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *projectStore) get(id string) (*DatasetProject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	path, err := s.projectPath(id)
	if err != nil {
		return nil, err
	}
	project, err := readProjectFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errDatasetNotFound
	}
	return project, err
}

func (s *projectStore) save(id string, project *DatasetProject) error {
	if project == nil {
		return errors.New("数据集项目不能为空")
	}
	if project.ID != id {
		return errors.New("URL 中的数据集 ID 与项目 ID 不一致")
	}
	path, err := s.projectPath(id)
	if err != nil {
		return err
	}
	normalizeProject(project)
	project.UpdatedAt = s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return errDatasetNotFound
	} else if err != nil {
		return err
	}
	if err := s.backupLocked(path, id); err != nil {
		return err
	}
	return writeProjectFileAtomic(path, project)
}

func (s *projectStore) importProject(project *DatasetProject) error {
	if project == nil {
		return errors.New("数据集项目不能为空")
	}
	if err := validateDatasetID(project.ID); err != nil {
		return err
	}
	normalizeProject(project)
	path, _ := s.projectPath(project.ID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return errors.New("数据集 ID 已存在")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if project.CreatedAt.IsZero() {
		project.CreatedAt = s.now().UTC()
	}
	project.UpdatedAt = s.now().UTC()
	return writeProjectFileAtomic(path, project)
}

func (s *projectStore) delete(id string) error {
	path, err := s.projectPath(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return errDatasetNotFound
	} else if err != nil {
		return err
	}
	target := filepath.Join(s.trash, fmt.Sprintf("%s-%s.json", id, s.now().UTC().Format("20060102T150405.000000000")))
	return os.Rename(path, target)
}

func (s *projectStore) backupLocked(path, id string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	target := filepath.Join(s.backups, id+"-latest.json")
	return writeBytesAtomic(target, data, 0o640)
}

func readProjectFile(path string) (*DatasetProject, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var project DatasetProject
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, err
	}
	normalizeProject(&project)
	return &project, nil
}

func writeProjectFileAtomic(path string, project *DatasetProject) error {
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeBytesAtomic(path, data, 0o640)
}

func writeBytesAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
