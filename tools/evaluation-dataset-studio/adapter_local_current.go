package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const maxRemoteEvaluationResponseSize = 4 << 20

var remoteSensitiveValuePattern = regexp.MustCompile(`(?i)(https?://\S+|(?:sk|ak)-[a-z0-9._-]+|bearer\s+\S+|x-api-key\s*[:=]\s*\S+)`)

type localCurrentAdapter struct{}

type localCurrentEvaluationTask struct {
	ID        string               `json:"id"`
	DatasetID string               `json:"dataset_id"`
	StartTime time.Time            `json:"start_time"`
	Status    EvaluationTaskStatus `json:"status"`
	ErrMsg    string               `json:"err_msg,omitempty"`
	Total     int                  `json:"total,omitempty"`
	Finished  int                  `json:"finished,omitempty"`
}

type localCurrentEvaluationDetail struct {
	Task   localCurrentEvaluationTask `json:"task"`
	Metric *EvaluationMetricResult    `json:"metric,omitempty"`
}

type localCurrentEvaluationEnvelope struct {
	Success bool                         `json:"success"`
	Data    localCurrentEvaluationDetail `json:"data"`
	Message string                       `json:"message,omitempty"`
}

type localCurrentListEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message,omitempty"`
}

type localCurrentKnowledgeBase struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type localCurrentModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
}

var evaluationHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("不允许 WeKnora API 重定向")
	},
}

func (localCurrentAdapter) ID() string { return localCurrentAdapterID }

func (localCurrentAdapter) Capabilities() RemoteCapabilities {
	return RemoteCapabilities{
		DiscoverKnowledgeBases: true,
		ImportKnowledgeChunks:  true,
		DiscoverModels:         true,
		StartEvaluation:        true,
		PollEvaluation:         true,
	}
}

func (localCurrentAdapter) NormalizeBaseURL(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimRight(value, "/"))
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("WeKnora 地址必须是有效的 http 或 https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("WeKnora 地址不能包含用户信息、查询参数或片段")
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		path = "/api/v1"
	} else if !strings.HasSuffix(path, "/api/v1") {
		return "", errors.New("WeKnora 地址路径必须为空或以 /api/v1 结尾")
	}
	parsed.Path = path
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (adapter localCurrentAdapter) DiscoverResources(baseURL, apiKey string) (*EvaluationResources, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("API Key 不能为空")
	}
	normalizedBaseURL, err := adapter.NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	var knowledgeBases []localCurrentKnowledgeBase
	if err := fetchLocalCurrentResourceList(normalizedBaseURL+"/knowledge-bases", apiKey, &knowledgeBases); err != nil {
		return nil, fmt.Errorf("读取知识库失败：%w", err)
	}
	var models []localCurrentModel
	if err := fetchLocalCurrentResourceList(normalizedBaseURL+"/models", apiKey, &models); err != nil {
		return nil, fmt.Errorf("读取模型失败：%w", err)
	}

	resources := &EvaluationResources{
		AdapterID:      adapter.ID(),
		Capabilities:   adapter.Capabilities(),
		BaseURL:        normalizedBaseURL,
		KnowledgeBases: make([]EvaluationResourceOption, 0, len(knowledgeBases)),
		ChatModels:     make([]EvaluationResourceOption, 0),
		RerankModels:   make([]EvaluationResourceOption, 0),
	}
	for _, kb := range knowledgeBases {
		id := strings.TrimSpace(kb.ID)
		if id == "" {
			continue
		}
		resources.KnowledgeBases = append(resources.KnowledgeBases, EvaluationResourceOption{
			ID: id, Name: resourceDisplayName(kb.Name, id),
		})
	}
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		status := strings.TrimSpace(model.Status)
		if id == "" || (status != "" && status != "active") {
			continue
		}
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = model.Name
		}
		option := EvaluationResourceOption{
			ID: id, Name: resourceDisplayName(name, id), Type: model.Type, Status: status,
		}
		switch model.Type {
		case "KnowledgeQA":
			resources.ChatModels = append(resources.ChatModels, option)
		case "Rerank":
			resources.RerankModels = append(resources.RerankModels, option)
		}
	}
	sortEvaluationResourceOptions(resources.KnowledgeBases)
	sortEvaluationResourceOptions(resources.ChatModels)
	sortEvaluationResourceOptions(resources.RerankModels)
	return resources, nil
}

func (adapter localCurrentAdapter) StartEvaluation(input RemoteEvaluationStartInput) (RemoteEvaluationDetail, error) {
	if input.DatasetID == "" {
		input.DatasetID = "default"
	}
	if input.DatasetID != "default" {
		return RemoteEvaluationDetail{}, errors.New("当前 WeKnora 版本仅支持 dataset_id=default")
	}
	body, err := json.Marshal(map[string]string{
		"dataset_id":        input.DatasetID,
		"knowledge_base_id": input.KnowledgeBaseID,
		"chat_id":           input.ChatModelID,
		"rerank_id":         input.RerankModelID,
	})
	if err != nil {
		return RemoteEvaluationDetail{}, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, input.BaseURL+"/evaluation", bytes.NewReader(body))
	if err != nil {
		return RemoteEvaluationDetail{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", input.APIKey)
	return executeLocalCurrentEvaluationRequest(httpReq)
}

func (localCurrentAdapter) PollEvaluation(baseURL, apiKey, taskID string) (RemoteEvaluationDetail, error) {
	endpoint := baseURL + "/evaluation?task_id=" + url.QueryEscape(taskID)
	httpReq, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return RemoteEvaluationDetail{}, err
	}
	httpReq.Header.Set("X-API-Key", strings.TrimSpace(apiKey))
	return executeLocalCurrentEvaluationRequest(httpReq)
}

func (adapter localCurrentAdapter) CheckCompatibility(baseURL, apiKey string) []CompatibilityCheck {
	normalizedBaseURL, err := adapter.NormalizeBaseURL(baseURL)
	if err != nil {
		return []CompatibilityCheck{{
			Name: "API 地址", Status: compatibilityFailed, Message: err.Error(),
		}}
	}
	checks := []CompatibilityCheck{
		probeLocalCurrentJSON(normalizedBaseURL, "/system/capabilities", apiKey, "部署能力", []string{"data.edition", "data.capabilities"}, validateJSONDataObject),
		probeLocalCurrentJSON(normalizedBaseURL, "/system/info", apiKey, "系统版本", []string{"data.version", "data.edition", "data.commit_id", "data.build_time"}, validateJSONDataObject),
		probeLocalCurrentJSON(normalizedBaseURL, "/knowledge-bases", apiKey, "知识库列表", []string{"success", "data[].id", "data[].name"}, validateLocalCurrentList),
		probeLocalCurrentJSON(normalizedBaseURL, "/models", apiKey, "模型列表", []string{"success", "data[].id", "data[].type", "data[].status"}, validateLocalCurrentList),
	}
	checks = append(checks, CompatibilityCheck{
		Name:    "评测契约",
		Status:  compatibilityWarning,
		Message: "Adapter fixture 声明支持启动和查询；预检未调用评测接口，需在费用确认后的端到端验收中验证。",
		Fields:  []string{"dataset_id", "knowledge_base_id", "chat_id", "rerank_id", "task.id", "task.status"},
	})
	return checks
}

func probeLocalCurrentJSON(baseURL, path, apiKey, name string, fields []string, validate func(map[string]any) error) CompatibilityCheck {
	check := CompatibilityCheck{Name: name, Method: http.MethodGet, Path: path, Fields: fields}
	request, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		check.Status = compatibilityFailed
		check.Message = "无法创建只读检查请求"
		return check
	}
	request.Header.Set("X-API-Key", strings.TrimSpace(apiKey))
	response, err := evaluationHTTPClient.Do(request)
	if err != nil {
		check.Status = compatibilityFailed
		check.Message = "无法连接目标端点；报告已省略主机和底层网络错误"
		return check
	}
	defer response.Body.Close()
	check.HTTPStatus = response.StatusCode
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteEvaluationResponseSize+1))
	if err != nil || len(data) > maxRemoteEvaluationResponseSize {
		check.Status = compatibilityFailed
		check.Message = "响应读取失败或超过 4 MiB 限制"
		return check
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		check.Status = compatibilityUnauthorized
		check.Message = "端点存在，但当前 API Key 无权读取"
		return check
	}
	if response.StatusCode == http.StatusNotFound {
		check.Status = compatibilityNotSupported
		check.Message = "目标版本未提供该只读端点"
		return check
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		check.Status = compatibilityFailed
		check.Message = fmt.Sprintf("只读请求失败（HTTP %d）", response.StatusCode)
		return check
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		check.Status = compatibilityIncompatible
		check.Message = "响应不是兼容的 JSON 对象"
		return check
	}
	if err := validate(document); err != nil {
		check.Status = compatibilityIncompatible
		check.Message = err.Error()
		return check
	}
	check.Status = compatibilityPassed
	check.Message = "只读端点可访问，响应结构符合 local-current 契约"
	return check
}

func validateJSONDataObject(document map[string]any) error {
	if _, ok := document["data"].(map[string]any); ok {
		return nil
	}
	return errors.New("响应缺少 data 对象")
}

func validateLocalCurrentList(document map[string]any) error {
	if success, ok := document["success"].(bool); !ok || !success {
		return errors.New("响应缺少 success=true")
	}
	if _, ok := document["data"].([]any); !ok {
		return errors.New("响应中的 data 不是数组")
	}
	return nil
}

func resourceDisplayName(name, id string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return id
}

func sortEvaluationResourceOptions(items []EvaluationResourceOption) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
}

func fetchLocalCurrentResourceList(endpoint, apiKey string, target any) error {
	httpReq, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("X-API-Key", strings.TrimSpace(apiKey))
	response, err := evaluationHTTPClient.Do(httpReq)
	if err != nil {
		return &remoteEvaluationError{Message: "无法连接 WeKnora；已隐藏目标地址和底层网络错误"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteEvaluationResponseSize+1))
	if err != nil {
		return &remoteEvaluationError{Message: "读取 WeKnora 响应失败"}
	}
	if len(data) > maxRemoteEvaluationResponseSize {
		return &remoteEvaluationError{Message: "WeKnora 响应超过 4 MiB 限制"}
	}
	var envelope localCurrentListEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return &remoteEvaluationError{Message: fmt.Sprintf("WeKnora 返回无效 JSON（HTTP %d）", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		return &remoteEvaluationError{Message: localCurrentRemoteErrorMessage(data, response.StatusCode)}
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return &remoteEvaluationError{Message: "WeKnora 响应缺少资源列表"}
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return &remoteEvaluationError{Message: "WeKnora 返回的资源列表格式不兼容"}
	}
	return nil
}

func executeLocalCurrentEvaluationRequest(request *http.Request) (RemoteEvaluationDetail, error) {
	response, err := evaluationHTTPClient.Do(request)
	if err != nil {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: "无法连接 WeKnora；已隐藏目标地址和底层网络错误"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteEvaluationResponseSize+1))
	if err != nil {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: "读取 WeKnora 响应失败"}
	}
	if len(data) > maxRemoteEvaluationResponseSize {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: "WeKnora 响应超过 4 MiB 限制"}
	}
	var envelope localCurrentEvaluationEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: fmt.Sprintf("WeKnora 返回无效 JSON（HTTP %d）", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: localCurrentRemoteErrorMessage(data, response.StatusCode)}
	}
	if strings.TrimSpace(envelope.Data.Task.ID) == "" {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: "WeKnora 响应缺少评测任务 ID"}
	}
	if !envelope.Data.Task.Status.valid() {
		return RemoteEvaluationDetail{}, &remoteEvaluationError{Message: "WeKnora 返回了未知的评测状态"}
	}
	return RemoteEvaluationDetail{
		Task: RemoteEvaluationTask{
			ID:        envelope.Data.Task.ID,
			DatasetID: envelope.Data.Task.DatasetID,
			StartTime: envelope.Data.Task.StartTime,
			Status:    envelope.Data.Task.Status,
			ErrMsg:    envelope.Data.Task.ErrMsg,
			Total:     envelope.Data.Task.Total,
			Finished:  envelope.Data.Task.Finished,
		},
		Metric: envelope.Data.Metric,
	}, nil
}

func localCurrentRemoteErrorMessage(data []byte, status int) string {
	var document struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	message := ""
	if json.Unmarshal(data, &document) == nil {
		message = strings.TrimSpace(document.Message)
		if message == "" && len(document.Error) > 0 && string(document.Error) != "null" {
			var detail struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(document.Error, &detail) == nil {
				message = strings.TrimSpace(detail.Message)
			}
			if message == "" {
				var text string
				if json.Unmarshal(document.Error, &text) == nil {
					message = strings.TrimSpace(text)
				}
			}
		}
	}
	if message == "" {
		return fmt.Sprintf("WeKnora 请求失败（HTTP %d）", status)
	}
	message = strings.Join(strings.Fields(message), " ")
	message = remoteSensitiveValuePattern.ReplaceAllString(message, "[已脱敏]")
	runes := []rune(message)
	if len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return fmt.Sprintf("WeKnora 请求失败（HTTP %d）：%s", status, message)
}

// Compatibility wrapper retained for existing callers and tests. New code should
// resolve an Adapter explicitly.
func normalizeEvaluationBaseURL(value string) (string, error) {
	return localCurrentAdapter{}.NormalizeBaseURL(value)
}
