package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	localCurrentAdapterID = "local-current"
	manualExportAdapterID = "manual-export"
)

type RemoteCapabilities struct {
	DiscoverKnowledgeBases bool `json:"discover_knowledge_bases"`
	ImportKnowledgeChunks  bool `json:"import_knowledge_chunks"`
	DiscoverModels         bool `json:"discover_models"`
	StartEvaluation        bool `json:"start_evaluation"`
	PollEvaluation         bool `json:"poll_evaluation"`
	CancelEvaluation       bool `json:"cancel_evaluation"`
	QuestionLevelResults   bool `json:"question_level_results"`
	UploadDataset          bool `json:"upload_dataset"`
	ConfigurableDatasetID  bool `json:"configurable_dataset_id"`
}

type SourcePageRequest struct {
	Page     int
	PageSize int
}

type SourcePage[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type SourceKnowledgeBase struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SourceKnowledge struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Title           string `json:"title"`
	FileName        string `json:"file_name"`
	ParseStatus     string `json:"parse_status"`
	EnableStatus    string `json:"enable_status"`
}

type SourceChunk struct {
	ID              string `json:"id"`
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Content         string `json:"content"`
	ChunkIndex      int    `json:"chunk_index"`
	IsEnabled       bool   `json:"is_enabled"`
	ChunkType       string `json:"chunk_type"`
	IndexStatus     string `json:"index_status"`
}

type ChunkSourceAdapter interface {
	ListSourceKnowledgeBases(baseURL, apiKey string, page SourcePageRequest) (SourcePage[SourceKnowledgeBase], error)
	ListSourceKnowledge(baseURL, apiKey, knowledgeBaseID string, page SourcePageRequest) (SourcePage[SourceKnowledge], error)
	ListSourceChunks(baseURL, apiKey, knowledgeID string, page SourcePageRequest) (SourcePage[SourceChunk], error)
	CheckChunkSourceCompatibility(baseURL, apiKey, knowledgeBaseID string) []CompatibilityCheck
}

type EvaluationTaskStatus int

const (
	EvaluationTaskPending EvaluationTaskStatus = iota
	EvaluationTaskRunning
	EvaluationTaskSucceeded
	EvaluationTaskFailed
)

func (s EvaluationTaskStatus) valid() bool {
	return s >= EvaluationTaskPending && s <= EvaluationTaskFailed
}

type RemoteEvaluationTask struct {
	ID        string
	DatasetID string
	StartTime time.Time
	Status    EvaluationTaskStatus
	ErrMsg    string
	Total     int
	Finished  int
}

type RemoteEvaluationDetail struct {
	Task   RemoteEvaluationTask
	Metric *EvaluationMetricResult
}

type RemoteEvaluationStartInput struct {
	BaseURL         string
	APIKey          string
	DatasetID       string
	KnowledgeBaseID string
	ChatModelID     string
	RerankModelID   string
}

type RemoteAdapter interface {
	ID() string
	Capabilities() RemoteCapabilities
	NormalizeBaseURL(string) (string, error)
	DiscoverResources(baseURL, apiKey string) (*EvaluationResources, error)
	StartEvaluation(RemoteEvaluationStartInput) (RemoteEvaluationDetail, error)
	PollEvaluation(baseURL, apiKey, taskID string) (RemoteEvaluationDetail, error)
	CheckCompatibility(baseURL, apiKey string) []CompatibilityCheck
}

type remoteEvaluationError struct {
	Message string
}

func (e *remoteEvaluationError) Error() string { return e.Message }

func resolveRemoteAdapter(adapterID string) (RemoteAdapter, error) {
	switch strings.TrimSpace(adapterID) {
	case "", localCurrentAdapterID:
		return localCurrentAdapter{}, nil
	case manualExportAdapterID:
		return manualExportAdapter{}, nil
	default:
		return nil, fmt.Errorf("未知的 WeKnora Adapter：%s", adapterID)
	}
}

func datasetIDForAdapter(adapterID, requested, configured string) (string, error) {
	adapter, err := resolveRemoteAdapter(adapterID)
	if err != nil {
		return "", err
	}
	datasetID := strings.TrimSpace(requested)
	if datasetID == "" {
		datasetID = strings.TrimSpace(configured)
	}
	if adapter.ID() == localCurrentAdapterID {
		if datasetID == "" {
			datasetID = "default"
		}
		if datasetID != "default" {
			return "", errors.New("local-current Adapter 仅支持 dataset_id=default")
		}
		return datasetID, nil
	}
	if datasetID == "" {
		return "", errors.New("dataset ID 不能为空")
	}
	return datasetID, nil
}

type manualExportAdapter struct{}

func (manualExportAdapter) ID() string { return manualExportAdapterID }

func (manualExportAdapter) Capabilities() RemoteCapabilities { return RemoteCapabilities{} }

func (manualExportAdapter) NormalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return "", errors.New("手工导出模式不访问远程 WeKnora 地址")
}

func (manualExportAdapter) DiscoverResources(string, string) (*EvaluationResources, error) {
	return nil, errors.New("手工导出模式不支持读取远程资源")
}

func (manualExportAdapter) StartEvaluation(RemoteEvaluationStartInput) (RemoteEvaluationDetail, error) {
	return RemoteEvaluationDetail{}, errors.New("手工导出模式不支持启动远程评测")
}

func (manualExportAdapter) PollEvaluation(string, string, string) (RemoteEvaluationDetail, error) {
	return RemoteEvaluationDetail{}, errors.New("手工导出模式不支持查询远程评测")
}

func (manualExportAdapter) CheckCompatibility(string, string) []CompatibilityCheck {
	return []CompatibilityCheck{
		{
			Name:    "手工导出模式",
			Status:  compatibilityNotSupported,
			Message: "不访问远程接口；请按生产部署说明人工上传数据包并记录 dataset ID。",
		},
	}
}
