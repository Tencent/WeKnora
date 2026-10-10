package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const compatibilityReportSchemaVersion = "1"

const (
	compatibilityPassed       = "passed"
	compatibilityWarning      = "warning"
	compatibilityFailed       = "failed"
	compatibilityUnauthorized = "unauthorized"
	compatibilityIncompatible = "incompatible"
	compatibilityNotSupported = "not_supported"
)

type ConnectionProfile struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Environment     string    `json:"environment"`
	BaseURL         string    `json:"base_url,omitempty"`
	APIKey          string    `json:"api_key,omitempty"`
	AdapterID       string    `json:"adapter_id"`
	DatasetID       string    `json:"dataset_id,omitempty"`
	KnowledgeBaseID string    `json:"knowledge_base_id,omitempty"`
	ChatModelID     string    `json:"chat_model_id,omitempty"`
	RerankModelID   string    `json:"rerank_model_id,omitempty"`
	DeploymentNotes string    `json:"deployment_notes,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type compatibilityCheckRequest struct {
	ProfileID string `json:"profile_id"`
	APIKey    string `json:"api_key"`
}

type CompatibilityCheck struct {
	Name       string   `json:"name"`
	Method     string   `json:"method,omitempty"`
	Path       string   `json:"path,omitempty"`
	Status     string   `json:"status"`
	HTTPStatus int      `json:"http_status,omitempty"`
	Fields     []string `json:"fields,omitempty"`
	Message    string   `json:"message"`
}

type CompatibilityReport struct {
	SchemaVersion string               `json:"schema_version"`
	Environment   string               `json:"environment"`
	AdapterID     string               `json:"adapter_id"`
	TargetScheme  string               `json:"target_scheme,omitempty"`
	TargetAPIPath string               `json:"target_api_path,omitempty"`
	CheckedAt     time.Time            `json:"checked_at"`
	Capabilities  RemoteCapabilities   `json:"capabilities"`
	Checks        []CompatibilityCheck `json:"checks"`
	Summary       CompatibilitySummary `json:"summary"`
}

type CompatibilitySummary struct {
	Passed       int `json:"passed"`
	Warnings     int `json:"warnings"`
	Failed       int `json:"failed"`
	Unauthorized int `json:"unauthorized"`
	Incompatible int `json:"incompatible"`
	NotSupported int `json:"not_supported"`
}

func (s *projectStore) connectionProfilePath(id string) (string, error) {
	if err := validateDatasetID(id); err != nil {
		return "", errors.New(strings.NewReplacer("数据集 ID", "连接配置 ID").Replace(err.Error()))
	}
	return filepath.Join(s.connections, id+".json"), nil
}

func normalizeConnectionProfile(profile *ConnectionProfile) error {
	if profile == nil {
		return errors.New("连接配置不能为空")
	}
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Environment = strings.TrimSpace(profile.Environment)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.AdapterID = strings.TrimSpace(profile.AdapterID)
	profile.DatasetID = strings.TrimSpace(profile.DatasetID)
	profile.KnowledgeBaseID = strings.TrimSpace(profile.KnowledgeBaseID)
	profile.ChatModelID = strings.TrimSpace(profile.ChatModelID)
	profile.RerankModelID = strings.TrimSpace(profile.RerankModelID)
	profile.DeploymentNotes = strings.TrimSpace(profile.DeploymentNotes)
	if profile.Name == "" {
		return errors.New("连接配置名称不能为空")
	}
	if profile.Environment != "local" && profile.Environment != "staging" && profile.Environment != "production" {
		return errors.New("环境类型必须是 local、staging 或 production")
	}
	// A production profile without an explicit adapter must stay offline. This
	// prevents older or non-UI clients from accidentally inheriting the
	// local-current remote adapter as the registry default.
	if profile.Environment == "production" && profile.AdapterID == "" {
		profile.AdapterID = manualExportAdapterID
	}
	adapter, err := resolveRemoteAdapter(profile.AdapterID)
	if err != nil {
		return err
	}
	profile.AdapterID = adapter.ID()
	if adapter.ID() == manualExportAdapterID {
		profile.BaseURL = ""
		profile.APIKey = ""
		profile.KnowledgeBaseID = ""
		profile.ChatModelID = ""
		profile.RerankModelID = ""
		return nil
	}
	profile.BaseURL, err = adapter.NormalizeBaseURL(profile.BaseURL)
	if err != nil {
		return err
	}
	parsed, _ := url.Parse(profile.BaseURL)
	if parsed.Scheme == "http" && (profile.Environment != "local" || !isLoopbackHost(parsed.Host)) {
		return errors.New("只有 local 环境的 loopback 地址允许使用 HTTP，其他环境必须使用 HTTPS")
	}
	return nil
}

func (s *projectStore) listConnectionProfiles() ([]ConnectionProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(s.connections)
	if err != nil {
		return nil, err
	}
	profiles := make([]ConnectionProfile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.connections, entry.Name()))
		if err != nil {
			return nil, err
		}
		var profile ConnectionProfile
		if err := json.Unmarshal(data, &profile); err != nil {
			return nil, fmt.Errorf("读取连接配置 %s：%w", entry.Name(), err)
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Environment == profiles[j].Environment {
			return profiles[i].Name < profiles[j].Name
		}
		return profiles[i].Environment < profiles[j].Environment
	})
	return profiles, nil
}

func (s *projectStore) saveConnectionProfile(id string, profile *ConnectionProfile) (*ConnectionProfile, error) {
	if profile == nil {
		return nil, errors.New("连接配置不能为空")
	}
	if id != "" && strings.TrimSpace(id) != strings.TrimSpace(profile.ID) {
		return nil, errors.New("URL 中的连接配置 ID 与请求体不一致")
	}
	path, err := s.connectionProfilePath(profile.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, err := os.ReadFile(path); err == nil {
		var existing ConnectionProfile
		if json.Unmarshal(data, &existing) == nil {
			profile.CreatedAt = existing.CreatedAt
			// An omitted/blank key means "keep the saved secret" when editing
			// an existing remote profile. Clearing a key must be explicit by
			// deleting the profile; manual-export always clears it below.
			if strings.TrimSpace(profile.APIKey) == "" {
				profile.APIKey = existing.APIKey
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := normalizeConnectionProfile(profile); err != nil {
		return nil, err
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return nil, err
	}
	// Connection profiles may contain an API key, so keep them readable only by
	// the current OS user. Dataset projects and evaluation records never receive
	// this value.
	if err := writeBytesAtomic(path, append(data, '\n'), 0o600); err != nil {
		return nil, err
	}
	copy := *profile
	return &copy, nil
}

func (s *projectStore) getConnectionProfile(id string) (*ConnectionProfile, error) {
	path, err := s.connectionProfilePath(id)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("连接配置不存在")
	}
	if err != nil {
		return nil, err
	}
	var profile ConnectionProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *projectStore) deleteConnectionProfile(id string) error {
	if err := s.ensureConfigurationNotReferenced("evaluation", id); err != nil {
		return err
	}
	path, err := s.connectionProfilePath(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(path); errors.Is(err, fs.ErrNotExist) {
		return errors.New("连接配置不存在")
	} else {
		return err
	}
}

func (s *projectStore) checkCompatibility(req compatibilityCheckRequest) (*CompatibilityReport, error) {
	profile, err := s.getConnectionProfile(strings.TrimSpace(req.ProfileID))
	if err != nil {
		return nil, err
	}
	adapter, err := resolveRemoteAdapter(profile.AdapterID)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(profile.APIKey)
	}
	if adapter.ID() != manualExportAdapterID && apiKey == "" {
		return nil, errors.New("API Key 不能为空")
	}
	parsed, _ := url.Parse(profile.BaseURL)
	report := &CompatibilityReport{
		SchemaVersion: compatibilityReportSchemaVersion,
		Environment:   profile.Environment,
		AdapterID:     adapter.ID(),
		TargetScheme:  parsed.Scheme,
		TargetAPIPath: parsed.Path,
		CheckedAt:     s.now().UTC(),
		Capabilities:  adapter.Capabilities(),
		Checks:        adapter.CheckCompatibility(profile.BaseURL, apiKey),
	}
	if sourceAdapter, ok := adapter.(ChunkSourceAdapter); ok {
		report.Checks = append(report.Checks, sourceAdapter.CheckChunkSourceCompatibility(profile.BaseURL, apiKey, profile.KnowledgeBaseID)...)
	}
	for _, check := range report.Checks {
		switch check.Status {
		case compatibilityPassed:
			report.Summary.Passed++
		case compatibilityWarning:
			report.Summary.Warnings++
		case compatibilityFailed:
			report.Summary.Failed++
		case compatibilityUnauthorized:
			report.Summary.Unauthorized++
		case compatibilityIncompatible:
			report.Summary.Incompatible++
		case compatibilityNotSupported:
			report.Summary.NotSupported++
		}
	}
	return report, nil
}
