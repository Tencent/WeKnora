package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type evaluationStartRequest struct {
	AdapterID           string `json:"adapter_id,omitempty"`
	ConnectionProfileID string `json:"connection_profile_id,omitempty"`
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key"`
	DatasetID           string `json:"dataset_id,omitempty"`
	KnowledgeBaseID     string `json:"knowledge_base_id"`
	ChatModelID         string `json:"chat_id"`
	RerankModelID       string `json:"rerank_id"`
	ExportID            string `json:"export_id"`
	DatasetDeployed     bool   `json:"dataset_deployed"`
}

type evaluationPollRequest struct {
	APIKey string `json:"api_key"`
}

type evaluationResultImportRequest struct {
	Status            string             `json:"status"`
	Total             int                `json:"total,omitempty"`
	Finished          int                `json:"finished,omitempty"`
	ErrorMessage      string             `json:"error_message,omitempty"`
	RetrievalMetrics  map[string]float64 `json:"retrieval_metrics,omitempty"`
	GenerationMetrics map[string]float64 `json:"generation_metrics,omitempty"`
}

type evaluationResourcesRequest struct {
	AdapterID string `json:"adapter_id,omitempty"`
	BaseURL   string `json:"base_url"`
	APIKey    string `json:"api_key"`
}

type EvaluationResourceOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

type EvaluationResources struct {
	AdapterID      string                     `json:"adapter_id"`
	Capabilities   RemoteCapabilities         `json:"capabilities"`
	BaseURL        string                     `json:"base_url"`
	KnowledgeBases []EvaluationResourceOption `json:"knowledge_bases"`
	ChatModels     []EvaluationResourceOption `json:"chat_models"`
	RerankModels   []EvaluationResourceOption `json:"rerank_models"`
}

type EvaluationMetricResult struct {
	RetrievalMetrics  map[string]float64 `json:"retrieval_metrics"`
	GenerationMetrics map[string]float64 `json:"generation_metrics"`
}

type EvaluationQuestionMetadata struct {
	Category           string            `json:"category,omitempty"`
	Difficulty         string            `json:"difficulty,omitempty"`
	Tags               []string          `json:"tags,omitempty"`
	ReviewState        string            `json:"review_state,omitempty"`
	AnswerKeyPoints    []string          `json:"answer_key_points,omitempty"`
	Answerable         *bool             `json:"answerable,omitempty"`
	ExpectedDocuments  []string          `json:"expected_documents,omitempty"`
	ForbiddenDocuments []string          `json:"forbidden_documents,omitempty"`
	TestRole           string            `json:"test_role,omitempty"`
	RetrievalFilters   map[string]string `json:"retrieval_filters,omitempty"`
	DatasetVersion     string            `json:"dataset_version,omitempty"`
	AnnotationSource   string            `json:"annotation_source,omitempty"`
	GoldSources        []string          `json:"gold_sources,omitempty"`
}

type EvaluationPassageMetadata struct {
	Source      string            `json:"source,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ReviewState string            `json:"review_state,omitempty"`
}

type EvaluationRun struct {
	ID                          string                               `json:"id"`
	AdapterID                   string                               `json:"adapter_id,omitempty"`
	ConnectionProfileID         string                               `json:"connection_profile_id,omitempty"`
	Environment                 string                               `json:"environment,omitempty"`
	ProjectID                   string                               `json:"project_id"`
	DatasetCreatedAt            time.Time                            `json:"dataset_created_at"`
	ExportID                    string                               `json:"export_id"`
	ExportSHA256                string                               `json:"export_sha256,omitempty"`
	ContractProfileID           string                               `json:"contract_profile_id,omitempty"`
	BaseURL                     string                               `json:"base_url"`
	TaskID                      string                               `json:"task_id"`
	DatasetID                   string                               `json:"dataset_id"`
	KnowledgeBaseID             string                               `json:"knowledge_base_id"`
	ChatModelID                 string                               `json:"chat_id"`
	RerankModelID               string                               `json:"rerank_id"`
	CreatedAt                   time.Time                            `json:"created_at"`
	UpdatedAt                   time.Time                            `json:"updated_at"`
	StartTime                   time.Time                            `json:"start_time"`
	Status                      EvaluationTaskStatus                 `json:"status"`
	ErrorMessage                string                               `json:"error_message,omitempty"`
	Total                       int                                  `json:"total,omitempty"`
	Finished                    int                                  `json:"finished,omitempty"`
	Metric                      *EvaluationMetricResult              `json:"metric,omitempty"`
	ResultSource                string                               `json:"result_source,omitempty"`
	ExportQuestionIDs           []int64                              `json:"export_question_ids,omitempty"`
	ExportPassageIDMap          map[int64]int64                      `json:"export_passage_id_map,omitempty"`
	ExternalResultSchemaVersion int                                  `json:"external_result_schema_version,omitempty"`
	ExternalResultFormat        string                               `json:"external_result_format,omitempty"`
	ExternalResultSHA256        string                               `json:"external_result_sha256,omitempty"`
	ExternalItems               []ExternalEvaluationItem             `json:"external_items,omitempty"`
	CorrectionOfRunID           string                               `json:"correction_of_run_id,omitempty"`
	QuestionMetadata            map[int64]EvaluationQuestionMetadata `json:"question_metadata,omitempty"`
	PassageMetadata             map[int64]EvaluationPassageMetadata  `json:"passage_metadata,omitempty"`
}

var evaluationMetricNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{0,63}$`)

func loadEvaluationResources(req evaluationResourcesRequest) (*EvaluationResources, error) {
	adapter, err := resolveRemoteAdapter(req.AdapterID)
	if err != nil {
		return nil, err
	}
	return adapter.DiscoverResources(req.BaseURL, req.APIKey)
}

func (s *projectStore) startEvaluation(projectID string, req evaluationStartRequest) (*EvaluationRun, error) {
	if !req.DatasetDeployed {
		return nil, errors.New("请先确认当前导出包已按目标环境部署说明完成部署")
	}
	req.APIKey = strings.TrimSpace(req.APIKey)
	req.KnowledgeBaseID = strings.TrimSpace(req.KnowledgeBaseID)
	req.ChatModelID = strings.TrimSpace(req.ChatModelID)
	req.RerankModelID = strings.TrimSpace(req.RerankModelID)
	var (
		connectionProfile *ConnectionProfile
		err               error
	)
	if strings.TrimSpace(req.ConnectionProfileID) != "" {
		connectionProfile, err = s.getConnectionProfile(strings.TrimSpace(req.ConnectionProfileID))
		if err != nil {
			return nil, err
		}
		req.AdapterID = connectionProfile.AdapterID
		req.BaseURL = connectionProfile.BaseURL
	}
	adapter, err := resolveRemoteAdapter(req.AdapterID)
	if err != nil {
		return nil, err
	}
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	_, exportItem, err := s.exportHistoryPath(projectID, req.ExportID)
	if err != nil {
		return nil, fmt.Errorf("选择的导出记录无效：%w", err)
	}
	configuredDatasetID := ""
	if connectionProfile != nil {
		configuredDatasetID = connectionProfile.DatasetID
	}
	remoteDatasetID, err := datasetIDForAdapter(adapter.ID(), req.DatasetID, configuredDatasetID)
	if err != nil {
		return nil, err
	}
	if connectionProfile != nil {
		exportDeployment, err := s.updateExportDeployment(projectID, req.ExportID, exportDeploymentRequest{
			ConnectionProfileID: connectionProfile.ID,
			DatasetID:           remoteDatasetID,
			Status:              exportDeploymentDeployed,
		})
		if err != nil {
			return nil, fmt.Errorf("记录数据包部署状态失败：%w", err)
		}
		exportItem = *exportDeployment
	}
	now := s.now().UTC()
	if adapter.ID() == manualExportAdapterID {
		if connectionProfile == nil {
			return nil, errors.New("手工导出模式必须选择一个已保存的环境配置")
		}
		run := &EvaluationRun{
			ID:                  historyID(now),
			AdapterID:           adapter.ID(),
			ConnectionProfileID: connectionProfile.ID,
			Environment:         connectionProfile.Environment,
			ProjectID:           project.ID,
			DatasetCreatedAt:    project.CreatedAt,
			ExportID:            req.ExportID,
			ExportSHA256:        exportItem.SHA256,
			ContractProfileID:   exportItem.ContractProfileID,
			TaskID:              "manual-" + historyID(now),
			DatasetID:           remoteDatasetID,
			KnowledgeBaseID:     "",
			ChatModelID:         "",
			RerankModelID:       "",
			CreatedAt:           now,
			UpdatedAt:           now,
			StartTime:           now,
			Status:              EvaluationTaskPending,
			ResultSource:        "external_pending",
			ExportQuestionIDs:   exportQuestionIDs(exportItem, project),
			ExportPassageIDMap:  exportPassageIDMap(exportItem, project),
			QuestionMetadata:    evaluationQuestionMetadata(project),
			PassageMetadata:     evaluationPassageMetadata(exportItem, project),
		}
		if err := s.writeEvaluationRun(run); err != nil {
			return nil, err
		}
		return run, nil
	}
	if !adapter.Capabilities().StartEvaluation {
		return nil, fmt.Errorf("Adapter %s 不支持启动远程评测", adapter.ID())
	}
	if req.APIKey == "" || req.KnowledgeBaseID == "" || req.ChatModelID == "" || req.RerankModelID == "" {
		return nil, errors.New("API Key、知识库 ID、对话模型 ID 和重排模型 ID 均不能为空")
	}
	baseURL, err := adapter.NormalizeBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	detail, err := adapter.StartEvaluation(RemoteEvaluationStartInput{
		BaseURL:         baseURL,
		APIKey:          req.APIKey,
		DatasetID:       remoteDatasetID,
		KnowledgeBaseID: req.KnowledgeBaseID,
		ChatModelID:     req.ChatModelID,
		RerankModelID:   req.RerankModelID,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(detail.Task.ID) == "" {
		return nil, &remoteEvaluationError{Message: "WeKnora 响应缺少评测任务 ID"}
	}
	run := &EvaluationRun{
		ID:                  historyID(now),
		AdapterID:           adapter.ID(),
		ConnectionProfileID: strings.TrimSpace(req.ConnectionProfileID),
		ProjectID:           project.ID,
		DatasetCreatedAt:    project.CreatedAt,
		ExportID:            req.ExportID,
		ExportSHA256:        exportItem.SHA256,
		ContractProfileID:   exportItem.ContractProfileID,
		BaseURL:             baseURL,
		TaskID:              detail.Task.ID,
		DatasetID:           detail.Task.DatasetID,
		KnowledgeBaseID:     req.KnowledgeBaseID,
		ChatModelID:         req.ChatModelID,
		RerankModelID:       req.RerankModelID,
		CreatedAt:           now,
		UpdatedAt:           now,
		ResultSource:        "remote",
		ExportQuestionIDs:   exportQuestionIDs(exportItem, project),
		ExportPassageIDMap:  exportPassageIDMap(exportItem, project),
		QuestionMetadata:    evaluationQuestionMetadata(project),
		PassageMetadata:     evaluationPassageMetadata(exportItem, project),
	}
	if connectionProfile != nil {
		run.ConnectionProfileID = connectionProfile.ID
		run.Environment = connectionProfile.Environment
	}
	applyRemoteEvaluation(run, detail)
	if run.DatasetID == "" {
		run.DatasetID = remoteDatasetID
	}
	if err := s.writeEvaluationRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

func evaluationQuestionMetadata(project *DatasetProject) map[int64]EvaluationQuestionMetadata {
	passageSources := make(map[int64]string, len(project.Passages))
	for _, passage := range project.Passages {
		passageSources[passage.ID] = strings.TrimSpace(passage.Source)
	}
	result := make(map[int64]EvaluationQuestionMetadata, len(project.Questions))
	for _, question := range project.Questions {
		sources := make([]string, 0, len(question.RelevantPassageIDs))
		seen := map[string]struct{}{}
		for _, passageID := range question.RelevantPassageIDs {
			source := passageSources[passageID]
			if source == "" {
				continue
			}
			if _, exists := seen[source]; exists {
				continue
			}
			seen[source] = struct{}{}
			sources = append(sources, source)
		}
		sort.Strings(sources)
		result[question.ID] = EvaluationQuestionMetadata{
			Category: strings.TrimSpace(question.Category), Difficulty: strings.TrimSpace(question.Difficulty),
			Tags: append([]string(nil), question.Tags...), ReviewState: question.ReviewState,
			AnswerKeyPoints: append([]string(nil), question.AnswerKeyPoints...), Answerable: question.Answerable,
			ExpectedDocuments: append([]string(nil), question.ExpectedDocuments...), ForbiddenDocuments: append([]string(nil), question.ForbiddenDocuments...),
			TestRole: question.TestRole, RetrievalFilters: cloneStringMap(question.RetrievalFilters), DatasetVersion: question.DatasetVersion,
			AnnotationSource: strings.TrimSpace(question.AnnotationSource), GoldSources: sources,
		}
	}
	return result
}

func evaluationPassageMetadata(item ExportHistoryItem, project *DatasetProject) map[int64]EvaluationPassageMetadata {
	byInternalID := make(map[int64]Passage, len(project.Passages))
	for _, passage := range project.Passages {
		byInternalID[passage.ID] = passage
	}
	result := make(map[int64]EvaluationPassageMetadata, len(item.PassageIDMap))
	for exportID, internalID := range exportPassageIDMap(item, project) {
		passage, ok := byInternalID[internalID]
		if !ok {
			continue
		}
		result[exportID] = EvaluationPassageMetadata{Source: passage.Source, Tags: append([]string(nil), passage.Tags...), Metadata: cloneStringMap(passage.Metadata), ReviewState: passage.ReviewState}
	}
	return result
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func exportQuestionIDs(item ExportHistoryItem, project *DatasetProject) []int64 {
	if len(item.QuestionIDs) > 0 {
		return append([]int64(nil), item.QuestionIDs...)
	}
	ids := make([]int64, 0, len(project.Questions))
	for _, question := range project.Questions {
		ids = append(ids, question.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func exportPassageIDMap(item ExportHistoryItem, project *DatasetProject) map[int64]int64 {
	if len(item.PassageIDMap) > 0 {
		result := make(map[int64]int64, len(item.PassageIDMap))
		for exportID, internalID := range item.PassageIDMap {
			result[exportID] = internalID
		}
		return result
	}
	passages := append([]Passage(nil), project.Passages...)
	sort.Slice(passages, func(i, j int) bool { return passages[i].ID < passages[j].ID })
	result := make(map[int64]int64, len(passages))
	for exportID, passage := range passages {
		result[int64(exportID)] = passage.ID
	}
	return result
}

func (s *projectStore) importEvaluationResult(projectID, runID string, req evaluationResultImportRequest) (*EvaluationRun, error) {
	run, err := s.getEvaluationRun(projectID, runID)
	if err != nil {
		return nil, err
	}
	if run.AdapterID != manualExportAdapterID {
		return nil, errors.New("只允许向 manual-export 评测记录导入外部结果")
	}
	if run.Status != EvaluationTaskPending || run.ResultSource != "external_pending" {
		return nil, errors.New("只允许向等待外部结果的手工评测记录导入结果")
	}
	req.Status = strings.TrimSpace(req.Status)
	switch req.Status {
	case "success":
		run.Status = EvaluationTaskSucceeded
	case "failed":
		run.Status = EvaluationTaskFailed
	default:
		return nil, errors.New("外部结果 status 必须是 success 或 failed")
	}
	if req.Total < 0 || req.Finished < 0 || (req.Total > 0 && req.Finished > req.Total) {
		return nil, errors.New("total 和 finished 必须是非负数，且 finished 不能大于 total")
	}
	if err := validateImportedMetrics(req.RetrievalMetrics); err != nil {
		return nil, fmt.Errorf("检索指标无效：%w", err)
	}
	if err := validateImportedMetrics(req.GenerationMetrics); err != nil {
		return nil, fmt.Errorf("生成指标无效：%w", err)
	}
	run.Total = req.Total
	run.Finished = req.Finished
	run.ErrorMessage = strings.TrimSpace(req.ErrorMessage)
	if len(req.RetrievalMetrics) > 0 || len(req.GenerationMetrics) > 0 {
		run.Metric = &EvaluationMetricResult{
			RetrievalMetrics:  req.RetrievalMetrics,
			GenerationMetrics: req.GenerationMetrics,
		}
	} else {
		run.Metric = nil
	}
	run.ResultSource = "external_import"
	run.UpdatedAt = s.now().UTC()
	if err := s.writeEvaluationRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

func validateImportedMetrics(metrics map[string]float64) error {
	if len(metrics) > 100 {
		return errors.New("单类指标不能超过 100 个")
	}
	for name, value := range metrics {
		if !evaluationMetricNamePattern.MatchString(name) {
			return fmt.Errorf("指标名 %q 格式无效", name)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("指标 %s 不是有限数值", name)
		}
	}
	return nil
}

func (s *projectStore) pollEvaluation(projectID, runID string, req evaluationPollRequest) (*EvaluationRun, error) {
	if strings.TrimSpace(req.APIKey) == "" {
		return nil, errors.New("API Key 不能为空")
	}
	run, err := s.getEvaluationRun(projectID, runID)
	if err != nil {
		return nil, err
	}
	adapter, err := resolveRemoteAdapter(run.AdapterID)
	if err != nil {
		return nil, err
	}
	if !adapter.Capabilities().PollEvaluation {
		return nil, fmt.Errorf("Adapter %s 不支持查询远程评测", adapter.ID())
	}
	detail, err := adapter.PollEvaluation(run.BaseURL, req.APIKey, run.TaskID)
	if err != nil {
		return nil, err
	}
	applyRemoteEvaluation(run, detail)
	run.UpdatedAt = s.now().UTC()
	if err := s.writeEvaluationRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *projectStore) listEvaluations(projectID string) ([]EvaluationRun, error) {
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.evaluations, projectID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []EvaluationRun{}, nil
	}
	if err != nil {
		return nil, err
	}
	runs := make([]EvaluationRun, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var run EvaluationRun
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		if !run.DatasetCreatedAt.Equal(project.CreatedAt) {
			continue
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return runs, nil
}

func (s *projectStore) getEvaluationRun(projectID, runID string) (*EvaluationRun, error) {
	if err := validateDatasetID(projectID); err != nil {
		return nil, err
	}
	if err := validateHistoryID(runID); err != nil {
		return nil, err
	}
	project, err := s.get(projectID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, err := os.ReadFile(filepath.Join(s.evaluations, projectID, runID+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("评测记录不存在")
	}
	if err != nil {
		return nil, err
	}
	var run EvaluationRun
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}
	if !run.DatasetCreatedAt.Equal(project.CreatedAt) {
		return nil, errors.New("评测记录属于同名数据集的其他生命周期")
	}
	return &run, nil
}

func (s *projectStore) writeEvaluationRun(run *EvaluationRun) error {
	if run == nil {
		return errors.New("评测记录不能为空")
	}
	if err := validateDatasetID(run.ProjectID); err != nil {
		return err
	}
	if err := validateHistoryID(run.ID); err != nil {
		return err
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.evaluations, run.ProjectID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return writeBytesAtomic(filepath.Join(dir, run.ID+".json"), data, 0o640)
}

func applyRemoteEvaluation(run *EvaluationRun, detail RemoteEvaluationDetail) {
	if detail.Task.ID != "" {
		run.TaskID = detail.Task.ID
	}
	run.Status = detail.Task.Status
	run.ErrorMessage = detail.Task.ErrMsg
	run.Total = detail.Task.Total
	run.Finished = detail.Task.Finished
	run.Metric = detail.Metric
	if !detail.Task.StartTime.IsZero() {
		run.StartTime = detail.Task.StartTime
	}
}
