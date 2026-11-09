package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type localCurrentSourceEnvelope[T any] struct {
	Success  bool `json:"success"`
	Data     []T  `json:"data"`
	Total    int  `json:"total"`
	Page     int  `json:"page"`
	PageSize int  `json:"page_size"`
}

type sourceReadError struct {
	HTTPStatus int
	Kind       string
	Message    string
}

func (e *sourceReadError) Error() string { return e.Message }

func normalizeSourcePage(page SourcePageRequest) SourcePageRequest {
	if page.Page < 1 {
		page.Page = 1
	}
	if page.PageSize < 1 {
		page.PageSize = 20
	}
	if page.PageSize > 100 {
		page.PageSize = 100
	}
	return page
}

func sourceEndpoint(baseURL, path string, page SourcePageRequest) string {
	query := url.Values{}
	query.Set("page", fmt.Sprint(page.Page))
	query.Set("page_size", fmt.Sprint(page.PageSize))
	return strings.TrimRight(baseURL, "/") + path + "?" + query.Encode()
}

func fetchLocalCurrentSourcePage[T any](endpoint, apiKey string, validate func([]T) error) (SourcePage[T], error) {
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return SourcePage[T]{}, &sourceReadError{Kind: compatibilityFailed, Message: "无法创建只读请求"}
	}
	request.Header.Set("X-API-Key", strings.TrimSpace(apiKey))
	response, err := evaluationHTTPClient.Do(request)
	if err != nil {
		return SourcePage[T]{}, &sourceReadError{Kind: compatibilityFailed, Message: "无法连接目标端点；已隐藏主机和底层网络错误"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteEvaluationResponseSize+1))
	if err != nil || len(data) > maxRemoteEvaluationResponseSize {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityFailed, Message: "响应读取失败或超过 4 MiB 限制"}
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityUnauthorized, Message: "端点存在，但当前 API Key 无权读取"}
	}
	if response.StatusCode == http.StatusNotFound {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityNotSupported, Message: "目标版本未提供该只读端点"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityFailed, Message: fmt.Sprintf("只读请求失败（HTTP %d）", response.StatusCode)}
	}
	var envelope localCurrentSourceEnvelope[T]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityIncompatible, Message: "响应不是兼容的 JSON 对象"}
	}
	if !envelope.Success || envelope.Data == nil {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityIncompatible, Message: "响应缺少 success=true 或 data 数组"}
	}
	if err := validate(envelope.Data); err != nil {
		return SourcePage[T]{}, &sourceReadError{HTTPStatus: response.StatusCode, Kind: compatibilityIncompatible, Message: err.Error()}
	}
	result := SourcePage[T]{Items: envelope.Data, Total: envelope.Total, Page: envelope.Page, PageSize: envelope.PageSize}
	if result.Page == 0 {
		result.Page = 1
	}
	if result.PageSize == 0 {
		result.PageSize = len(result.Items)
	}
	if result.Total == 0 {
		result.Total = len(result.Items)
	}
	return result, nil
}

func validateSourceKnowledgeBases(items []SourceKnowledgeBase) error {
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return errors.New("知识库列表存在缺少 id 的记录")
		}
	}
	return nil
}

func validateSourceKnowledge(items []SourceKnowledge) error {
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return errors.New("文档列表存在缺少 id 的记录")
		}
	}
	return nil
}

func validateSourceChunks(items []SourceChunk) error {
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return errors.New("分块列表存在缺少 id 的记录")
		}
		if strings.TrimSpace(item.Content) == "" {
			return errors.New("分块列表存在缺少 content 文本的记录")
		}
	}
	return nil
}

func (adapter localCurrentAdapter) ListSourceKnowledgeBases(baseURL, apiKey string, page SourcePageRequest) (SourcePage[SourceKnowledgeBase], error) {
	normalized, err := adapter.NormalizeBaseURL(baseURL)
	if err != nil {
		return SourcePage[SourceKnowledgeBase]{}, err
	}
	page = normalizeSourcePage(page)
	return fetchLocalCurrentSourcePage(sourceEndpoint(normalized, "/knowledge-bases", page), apiKey, validateSourceKnowledgeBases)
}

func (adapter localCurrentAdapter) ListSourceKnowledge(baseURL, apiKey, knowledgeBaseID string, page SourcePageRequest) (SourcePage[SourceKnowledge], error) {
	normalized, err := adapter.NormalizeBaseURL(baseURL)
	if err != nil {
		return SourcePage[SourceKnowledge]{}, err
	}
	if strings.TrimSpace(knowledgeBaseID) == "" {
		return SourcePage[SourceKnowledge]{}, errors.New("知识库 ID 不能为空")
	}
	page = normalizeSourcePage(page)
	path := "/knowledge-bases/" + url.PathEscape(strings.TrimSpace(knowledgeBaseID)) + "/knowledge"
	return fetchLocalCurrentSourcePage(sourceEndpoint(normalized, path, page), apiKey, validateSourceKnowledge)
}

func (adapter localCurrentAdapter) ListSourceChunks(baseURL, apiKey, knowledgeID string, page SourcePageRequest) (SourcePage[SourceChunk], error) {
	normalized, err := adapter.NormalizeBaseURL(baseURL)
	if err != nil {
		return SourcePage[SourceChunk]{}, err
	}
	if strings.TrimSpace(knowledgeID) == "" {
		return SourcePage[SourceChunk]{}, errors.New("文档 ID 不能为空")
	}
	page = normalizeSourcePage(page)
	path := "/chunks/" + url.PathEscape(strings.TrimSpace(knowledgeID))
	return fetchLocalCurrentSourcePage(sourceEndpoint(normalized, path, page), apiKey, validateSourceChunks)
}

func sourceCompatibilityCheck(name, path string, err error) CompatibilityCheck {
	check := CompatibilityCheck{Name: name, Method: http.MethodGet, Path: path}
	if err == nil {
		check.Status = compatibilityPassed
		check.Message = "只读端点可访问，响应结构符合 local-current 契约"
		return check
	}
	var sourceErr *sourceReadError
	if errors.As(err, &sourceErr) {
		check.Status = sourceErr.Kind
		check.HTTPStatus = sourceErr.HTTPStatus
		check.Message = sourceErr.Message
		return check
	}
	check.Status = compatibilityFailed
	check.Message = err.Error()
	return check
}

func (adapter localCurrentAdapter) CheckChunkSourceCompatibility(baseURL, apiKey, configuredKnowledgeBaseID string) []CompatibilityCheck {
	page := SourcePageRequest{Page: 1, PageSize: 1}
	knowledgeBases, kbErr := adapter.ListSourceKnowledgeBases(baseURL, apiKey, page)
	checks := []CompatibilityCheck{sourceCompatibilityCheck("分块数据源：知识库", "/knowledge-bases?page=1&page_size=1", kbErr)}
	if kbErr != nil {
		return append(checks,
			CompatibilityCheck{Name: "分块数据源：文档", Status: compatibilityWarning, Message: "知识库读取未通过，未继续检查文档端点"},
			CompatibilityCheck{Name: "分块数据源：分块", Status: compatibilityWarning, Message: "知识库读取未通过，未继续检查分块端点"},
		)
	}
	kbID := strings.TrimSpace(configuredKnowledgeBaseID)
	if kbID == "" && len(knowledgeBases.Items) > 0 {
		kbID = knowledgeBases.Items[0].ID
	}
	if kbID == "" {
		return append(checks,
			CompatibilityCheck{Name: "分块数据源：文档", Status: compatibilityWarning, Message: "当前没有可用于抽样检查的知识库"},
			CompatibilityCheck{Name: "分块数据源：分块", Status: compatibilityWarning, Message: "当前没有可用于抽样检查的文档"},
		)
	}
	knowledgePath := "/knowledge-bases/{kb_id}/knowledge?page=1&page_size=1"
	knowledge, knowledgeErr := adapter.ListSourceKnowledge(baseURL, apiKey, kbID, page)
	checks = append(checks, sourceCompatibilityCheck("分块数据源：文档", knowledgePath, knowledgeErr))
	if knowledgeErr != nil || len(knowledge.Items) == 0 {
		message := "文档读取未通过，未继续检查分块端点"
		if knowledgeErr == nil {
			message = "当前知识库没有可用于抽样检查的文档"
		}
		return append(checks, CompatibilityCheck{Name: "分块数据源：分块", Status: compatibilityWarning, Message: message})
	}
	chunkPath := "/chunks/{knowledge_id}?page=1&page_size=1"
	_, chunkErr := adapter.ListSourceChunks(baseURL, apiKey, knowledge.Items[0].ID, page)
	return append(checks, sourceCompatibilityCheck("分块数据源：分块", chunkPath, chunkErr))
}
