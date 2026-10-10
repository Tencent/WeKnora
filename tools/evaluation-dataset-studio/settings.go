package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const workspaceSettingsSchemaVersion = 1

type WorkspaceSettings struct {
	SchemaVersion              int    `json:"schema_version"`
	DefaultGenerationProfileID string `json:"default_generation_profile_id,omitempty"`
	DefaultEvaluationProfileID string `json:"default_evaluation_profile_id,omitempty"`
}

type ConfigurationReferences struct {
	Type             string   `json:"type"`
	ID               string   `json:"id"`
	DatasetIDs       []string `json:"dataset_ids"`
	WorkspaceDefault bool     `json:"workspace_default"`
}

func normalizeWorkspaceSettings(settings *WorkspaceSettings) error {
	if settings == nil {
		return errors.New("workspace 设置不能为空")
	}
	settings.SchemaVersion = workspaceSettingsSchemaVersion
	settings.DefaultGenerationProfileID = strings.TrimSpace(settings.DefaultGenerationProfileID)
	settings.DefaultEvaluationProfileID = strings.TrimSpace(settings.DefaultEvaluationProfileID)
	for _, id := range []string{settings.DefaultGenerationProfileID, settings.DefaultEvaluationProfileID} {
		if id != "" {
			if err := validateDatasetID(id); err != nil {
				return errors.New("默认配置 ID 格式无效")
			}
		}
	}
	return nil
}

func (s *projectStore) getWorkspaceSettings() (WorkspaceSettings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(s.settings)
	if errors.Is(err, fs.ErrNotExist) {
		return WorkspaceSettings{SchemaVersion: workspaceSettingsSchemaVersion}, nil
	}
	if err != nil {
		return WorkspaceSettings{}, err
	}
	var settings WorkspaceSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return WorkspaceSettings{}, err
	}
	if err := normalizeWorkspaceSettings(&settings); err != nil {
		return WorkspaceSettings{}, err
	}
	return settings, nil
}

func (s *projectStore) saveWorkspaceSettings(settings WorkspaceSettings) (WorkspaceSettings, error) {
	if err := normalizeWorkspaceSettings(&settings); err != nil {
		return WorkspaceSettings{}, err
	}
	if settings.DefaultGenerationProfileID != "" {
		if _, err := os.Stat(filepath.Join(s.generationConnections, settings.DefaultGenerationProfileID+".json")); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return WorkspaceSettings{}, errors.New("默认生成模型配置不存在")
			}
			return WorkspaceSettings{}, err
		}
	}
	if settings.DefaultEvaluationProfileID != "" {
		path, _ := s.connectionProfilePath(settings.DefaultEvaluationProfileID)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return WorkspaceSettings{}, errors.New("默认 WeKnora 环境配置不存在")
			}
			return WorkspaceSettings{}, err
		}
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return WorkspaceSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeBytesAtomic(s.settings, append(data, '\n'), 0o600); err != nil {
		return WorkspaceSettings{}, err
	}
	return settings, nil
}

func (s *projectStore) configurationReferences(kind, id string) (ConfigurationReferences, error) {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if kind != "generation" && kind != "evaluation" {
		return ConfigurationReferences{}, errors.New("配置类型必须是 generation 或 evaluation")
	}
	if err := validateDatasetID(id); err != nil {
		return ConfigurationReferences{}, errors.New("配置 ID 格式无效")
	}
	settings, err := s.getWorkspaceSettings()
	if err != nil {
		return ConfigurationReferences{}, err
	}
	refs := ConfigurationReferences{Type: kind, ID: id}
	if kind == "generation" {
		refs.WorkspaceDefault = settings.DefaultGenerationProfileID == id
	} else {
		refs.WorkspaceDefault = settings.DefaultEvaluationProfileID == id
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.projects)
	if err != nil {
		return ConfigurationReferences{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		project, err := readProjectFile(filepath.Join(s.projects, entry.Name()))
		if err != nil {
			return ConfigurationReferences{}, err
		}
		if (kind == "generation" && project.GenerationProfileID == id) || (kind == "evaluation" && project.EvaluationProfileID == id) {
			refs.DatasetIDs = append(refs.DatasetIDs, project.ID)
		}
	}
	sort.Strings(refs.DatasetIDs)
	return refs, nil
}

func (s *projectStore) ensureConfigurationNotReferenced(kind, id string) error {
	refs, err := s.configurationReferences(kind, id)
	if err != nil {
		return err
	}
	if !refs.WorkspaceDefault && len(refs.DatasetIDs) == 0 {
		return nil
	}
	parts := make([]string, 0, 2)
	if refs.WorkspaceDefault {
		parts = append(parts, "workspace 默认配置")
	}
	if len(refs.DatasetIDs) > 0 {
		parts = append(parts, "数据集 "+strings.Join(refs.DatasetIDs, "、"))
	}
	return errors.New("配置仍被引用：" + strings.Join(parts, "；") + "。请先解除引用或设置替代配置")
}
