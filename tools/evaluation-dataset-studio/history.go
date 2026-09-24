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
	"time"
)

const historyIDLayout = "20060102T150405.000000000Z"

var historyIDPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z$`)

type SnapshotSummary struct {
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	Version       string    `json:"version"`
	PassageCount  int       `json:"passage_count"`
	QuestionCount int       `json:"question_count"`
}

type ExportHistoryItem struct {
	ID                  string          `json:"id"`
	FileName            string          `json:"file_name"`
	CreatedAt           time.Time       `json:"created_at"`
	DatasetCreatedAt    time.Time       `json:"dataset_created_at"`
	Size                int64           `json:"size"`
	SHA256              string          `json:"sha256,omitempty"`
	ContractProfileID   string          `json:"contract_profile_id,omitempty"`
	QuestionCount       int             `json:"question_count"`
	PassageCount        int             `json:"passage_count"`
	QrelCount           int             `json:"qrel_count"`
	QuestionIDs         []int64         `json:"question_ids,omitempty"`
	PassageIDMap        map[int64]int64 `json:"passage_id_map,omitempty"`
	ConnectionProfileID string          `json:"connection_profile_id,omitempty"`
	Environment         string          `json:"environment,omitempty"`
	AdapterID           string          `json:"adapter_id,omitempty"`
	DatasetID           string          `json:"dataset_id,omitempty"`
	DeploymentStatus    string          `json:"deployment_status"`
	DeployedAt          *time.Time      `json:"deployed_at,omitempty"`
}

const (
	exportDeploymentUnknown     = "unknown"
	exportDeploymentNotDeployed = "not_deployed"
	exportDeploymentDeployed    = "deployed"
)

type exportDeploymentRequest struct {
	ConnectionProfileID string `json:"connection_profile_id"`
	DatasetID           string `json:"dataset_id,omitempty"`
	Status              string `json:"status"`
}

func historyID(now time.Time) string {
	return now.UTC().Format(historyIDLayout)
}

func validateHistoryID(id string) error {
	if !historyIDPattern.MatchString(id) {
		return errors.New("历史记录 ID 格式无效")
	}
	return nil
}

func (s *projectStore) createSnapshot(datasetID string) (SnapshotSummary, error) {
	projectPath, err := s.projectPath(datasetID)
	if err != nil {
		return SnapshotSummary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	project, err := readProjectFile(projectPath)
	if errors.Is(err, fs.ErrNotExist) {
		return SnapshotSummary{}, errDatasetNotFound
	}
	if err != nil {
		return SnapshotSummary{}, err
	}
	createdAt := s.now().UTC()
	id := historyID(createdAt)
	dir := filepath.Join(s.snapshots, datasetID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return SnapshotSummary{}, err
	}
	target := filepath.Join(dir, id+".json")
	if _, err := os.Stat(target); err == nil {
		return SnapshotSummary{}, errors.New("同一时刻的快照已存在")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return SnapshotSummary{}, err
	}
	if err := writeProjectFileAtomic(target, project); err != nil {
		return SnapshotSummary{}, err
	}
	return snapshotSummary(id, createdAt, project), nil
}

func (s *projectStore) listSnapshots(datasetID string) ([]SnapshotSummary, error) {
	projectPath, err := s.projectPath(datasetID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	currentProject, err := readProjectFile(projectPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errDatasetNotFound
	} else if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.snapshots, datasetID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []SnapshotSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]SnapshotSummary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		createdAt, err := time.Parse(historyIDLayout, id)
		if err != nil {
			continue
		}
		project, err := readProjectFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("读取快照 %s 失败：%w", id, err)
		}
		if !project.CreatedAt.Equal(currentProject.CreatedAt) {
			continue
		}
		items = append(items, snapshotSummary(id, createdAt, project))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *projectStore) restoreSnapshot(datasetID, snapshotID string) (*DatasetProject, error) {
	if err := validateDatasetID(datasetID); err != nil {
		return nil, err
	}
	if err := validateHistoryID(snapshotID); err != nil {
		return nil, err
	}
	projectPath, _ := s.projectPath(datasetID)
	snapshotPath := filepath.Join(s.snapshots, datasetID, snapshotID+".json")
	s.mu.Lock()
	defer s.mu.Unlock()
	currentProject, err := readProjectFile(projectPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errDatasetNotFound
	} else if err != nil {
		return nil, err
	}
	project, err := readProjectFile(snapshotPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("快照不存在")
	}
	if err != nil {
		return nil, err
	}
	if project.ID != datasetID {
		return nil, errors.New("快照的数据集 ID 不匹配")
	}
	if !project.CreatedAt.Equal(currentProject.CreatedAt) {
		return nil, errors.New("快照属于同名数据集的其他生命周期")
	}
	if err := s.backupLocked(projectPath, datasetID); err != nil {
		return nil, err
	}
	project.UpdatedAt = s.now().UTC()
	if err := writeProjectFileAtomic(projectPath, project); err != nil {
		return nil, err
	}
	return project, nil
}

func snapshotSummary(id string, createdAt time.Time, project *DatasetProject) SnapshotSummary {
	return SnapshotSummary{
		ID:            id,
		CreatedAt:     createdAt,
		Version:       project.Version,
		PassageCount:  len(project.Passages),
		QuestionCount: len(project.Questions),
	}
}

func (s *projectStore) listExports(datasetID string) ([]ExportHistoryItem, error) {
	projectPath, err := s.projectPath(datasetID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	currentProject, err := readProjectFile(projectPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errDatasetNotFound
	} else if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.exports, datasetID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []ExportHistoryItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]ExportHistoryItem, 0, len(entries)/2)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var item ExportHistoryItem
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("读取导出记录 %s 失败：%w", entry.Name(), err)
		}
		if err := validateHistoryID(item.ID); err != nil {
			continue
		}
		if !item.DatasetCreatedAt.Equal(currentProject.CreatedAt) {
			continue
		}
		if err := backfillExportHistoryItem(&item, filepath.Join(dir, item.ID+".zip")); err != nil {
			return nil, fmt.Errorf("补齐导出记录 %s 失败：%w", entry.Name(), err)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (s *projectStore) exportHistoryPath(datasetID, exportID string) (string, ExportHistoryItem, error) {
	if err := validateDatasetID(datasetID); err != nil {
		return "", ExportHistoryItem{}, err
	}
	if err := validateHistoryID(exportID); err != nil {
		return "", ExportHistoryItem{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, err := readProjectFile(filepath.Join(s.projects, datasetID+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ExportHistoryItem{}, errDatasetNotFound
	}
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	dir := filepath.Join(s.exports, datasetID)
	metadata, err := os.ReadFile(filepath.Join(dir, exportID+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", ExportHistoryItem{}, errors.New("导出记录不存在")
	}
	if err != nil {
		return "", ExportHistoryItem{}, err
	}
	var item ExportHistoryItem
	if err := json.Unmarshal(metadata, &item); err != nil {
		return "", ExportHistoryItem{}, err
	}
	if !item.DatasetCreatedAt.Equal(project.CreatedAt) {
		return "", ExportHistoryItem{}, errors.New("导出记录属于同名数据集的其他生命周期")
	}
	path := filepath.Join(dir, exportID+".zip")
	if _, err := os.Stat(path); err != nil {
		return "", ExportHistoryItem{}, err
	}
	if err := backfillExportHistoryItem(&item, path); err != nil {
		return "", ExportHistoryItem{}, err
	}
	return path, item, nil
}

func backfillExportHistoryItem(item *ExportHistoryItem, zipPath string) error {
	if item.ContractProfileID == "" {
		item.ContractProfileID = weKnoraFiveParquetProfileID
	}
	if item.DeploymentStatus == "" {
		item.DeploymentStatus = exportDeploymentUnknown
	}
	if item.SHA256 == "" {
		digest, err := sha256File(zipPath)
		if err != nil {
			return err
		}
		item.SHA256 = digest
	}
	return nil
}

func (s *projectStore) updateExportDeployment(datasetID, exportID string, req exportDeploymentRequest) (*ExportHistoryItem, error) {
	path, item, err := s.exportHistoryPath(datasetID, exportID)
	if err != nil {
		return nil, err
	}
	req.Status = strings.TrimSpace(req.Status)
	if req.Status != exportDeploymentDeployed && req.Status != exportDeploymentNotDeployed {
		return nil, errors.New("部署状态必须是 deployed 或 not_deployed")
	}
	if req.Status == exportDeploymentDeployed {
		profile, err := s.getConnectionProfile(strings.TrimSpace(req.ConnectionProfileID))
		if err != nil {
			return nil, err
		}
		datasetRemoteID, err := datasetIDForAdapter(profile.AdapterID, req.DatasetID, profile.DatasetID)
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		item.ConnectionProfileID = profile.ID
		item.Environment = profile.Environment
		item.AdapterID = profile.AdapterID
		item.DatasetID = datasetRemoteID
		item.DeploymentStatus = exportDeploymentDeployed
		item.DeployedAt = &now
	} else {
		item.DeploymentStatus = exportDeploymentNotDeployed
		item.DeployedAt = nil
	}
	metadata, err := json.MarshalIndent(item, "", "  ")
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeBytesAtomic(filepath.Join(filepath.Dir(path), exportID+".json"), append(metadata, '\n'), 0o640); err != nil {
		return nil, err
	}
	return &item, nil
}
