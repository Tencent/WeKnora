package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

const (
	maxSourceImportItems = 1000
	sourcePageSize       = 100
)

type sourceImportRequest struct {
	ProfileID       string   `json:"profile_id"`
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	KnowledgeID     string   `json:"knowledge_id"`
	ChunkIDs        []string `json:"chunk_ids"`
	ChangedPolicy   string   `json:"changed_policy,omitempty"`
}

type sourceImportPreviewItem struct {
	ChunkID           string `json:"chunk_id"`
	ChunkIndex        int    `json:"chunk_index,omitempty"`
	ContentPreview    string `json:"content_preview,omitempty"`
	ContentSHA256     string `json:"content_sha256,omitempty"`
	Status            string `json:"status"`
	Message           string `json:"message,omitempty"`
	ExistingPassageID int64  `json:"existing_passage_id,omitempty"`
}

type sourceImportSummary struct {
	Selected   int `json:"selected"`
	Importable int `json:"importable"`
	Existing   int `json:"existing"`
	Changed    int `json:"changed"`
	Invalid    int `json:"invalid"`
	Imported   int `json:"imported,omitempty"`
	Skipped    int `json:"skipped,omitempty"`
}

type sourceImportPreview struct {
	ProfileID       string                    `json:"profile_id"`
	KnowledgeBaseID string                    `json:"knowledge_base_id"`
	KnowledgeID     string                    `json:"knowledge_id"`
	DocumentName    string                    `json:"document_name"`
	ChangedPolicy   string                    `json:"changed_policy"`
	Summary         sourceImportSummary       `json:"summary"`
	Items           []sourceImportPreviewItem `json:"items"`
	chunks          map[string]SourceChunk
}

type sourceImportResult struct {
	Project            *DatasetProject     `json:"project"`
	Summary            sourceImportSummary `json:"summary"`
	ImportedPassageIDs []int64             `json:"imported_passage_ids"`
}

func contentSHA256(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeSourceImportRequest(req *sourceImportRequest) error {
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	req.KnowledgeBaseID = strings.TrimSpace(req.KnowledgeBaseID)
	req.KnowledgeID = strings.TrimSpace(req.KnowledgeID)
	req.ChangedPolicy = strings.TrimSpace(req.ChangedPolicy)
	if req.ChangedPolicy == "" {
		req.ChangedPolicy = "skip"
	}
	if req.ChangedPolicy != "skip" && req.ChangedPolicy != "new" {
		return errors.New("内容变化策略只能是 skip 或 new")
	}
	if req.ProfileID == "" || req.KnowledgeBaseID == "" || req.KnowledgeID == "" {
		return errors.New("环境配置、知识库和文档不能为空")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(req.ChunkIDs))
	for _, raw := range req.ChunkIDs {
		id := strings.TrimSpace(raw)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	req.ChunkIDs = ids
	if len(ids) == 0 {
		return errors.New("请至少选择一个分块")
	}
	if len(ids) > maxSourceImportItems {
		return fmt.Errorf("单次最多导入 %d 个分块", maxSourceImportItems)
	}
	return nil
}

func (s *projectStore) chunkSourceProfile(id string) (*ConnectionProfile, ChunkSourceAdapter, error) {
	profile, err := s.getConnectionProfile(strings.TrimSpace(id))
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(profile.APIKey) == "" {
		return nil, nil, errors.New("环境配置缺少已保存的 API Key")
	}
	adapter, err := resolveRemoteAdapter(profile.AdapterID)
	if err != nil {
		return nil, nil, err
	}
	if !adapter.Capabilities().ImportKnowledgeChunks {
		return nil, nil, errors.New("当前 Adapter 不支持知识库分块读取")
	}
	source, ok := adapter.(ChunkSourceAdapter)
	if !ok {
		return nil, nil, errors.New("当前 Adapter 未实现知识库分块读取契约")
	}
	return profile, source, nil
}

func ensureProductionChunkSourceCompatible(profile *ConnectionProfile, adapter ChunkSourceAdapter, knowledgeBaseID string) error {
	if profile.Environment != "production" {
		return nil
	}
	for _, check := range adapter.CheckChunkSourceCompatibility(profile.BaseURL, profile.APIKey, knowledgeBaseID) {
		if strings.HasPrefix(check.Name, "分块数据源：") && check.Status != compatibilityPassed {
			return fmt.Errorf("生产环境分块契约未通过：%s（%s）", check.Name, compatibilityStatusLabel(check.Status))
		}
	}
	return nil
}

func compatibilityStatusLabel(status string) string {
	switch status {
	case compatibilityUnauthorized:
		return "无权限"
	case compatibilityNotSupported:
		return "不支持"
	case compatibilityIncompatible:
		return "结构不兼容"
	case compatibilityFailed:
		return "请求失败"
	case compatibilityWarning:
		return "未完成验证"
	default:
		return status
	}
}

func sourceDocumentName(item SourceKnowledge) string {
	if name := strings.TrimSpace(item.FileName); name != "" {
		return name
	}
	if name := strings.TrimSpace(item.Title); name != "" {
		return name
	}
	return item.ID
}

func findSourceKnowledge(adapter ChunkSourceAdapter, profile *ConnectionProfile, knowledgeBaseID, knowledgeID string) (SourceKnowledge, error) {
	for page := 1; page <= maxSourceImportItems/sourcePageSize; page++ {
		result, err := adapter.ListSourceKnowledge(profile.BaseURL, profile.APIKey, knowledgeBaseID, SourcePageRequest{Page: page, PageSize: sourcePageSize})
		if err != nil {
			return SourceKnowledge{}, err
		}
		for _, item := range result.Items {
			if item.ID == knowledgeID {
				if item.ParseStatus != "" && item.ParseStatus != "completed" {
					return SourceKnowledge{}, fmt.Errorf("文档尚未解析完成（%s）", item.ParseStatus)
				}
				if item.EnableStatus != "" && item.EnableStatus != "enabled" {
					return SourceKnowledge{}, fmt.Errorf("文档当前不可用（%s）", item.EnableStatus)
				}
				return item, nil
			}
		}
		if len(result.Items) < sourcePageSize || (result.Total > 0 && page*sourcePageSize >= result.Total) {
			break
		}
	}
	return SourceKnowledge{}, errors.New("所选文档不存在或超过可读取范围")
}

func fetchSelectedSourceChunks(adapter ChunkSourceAdapter, profile *ConnectionProfile, knowledgeID string, wanted map[string]bool) (map[string]SourceChunk, error) {
	result := make(map[string]SourceChunk, len(wanted))
	for page := 1; page <= maxSourceImportItems/sourcePageSize && len(result) < len(wanted); page++ {
		batch, err := adapter.ListSourceChunks(profile.BaseURL, profile.APIKey, knowledgeID, SourcePageRequest{Page: page, PageSize: sourcePageSize})
		if err != nil {
			return nil, err
		}
		for _, item := range batch.Items {
			if wanted[item.ID] {
				result[item.ID] = item
			}
		}
		if len(batch.Items) < sourcePageSize || (batch.Total > 0 && page*sourcePageSize >= batch.Total) {
			break
		}
	}
	return result, nil
}

func previewText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len([]rune(value)) <= 160 {
		return value
	}
	return string([]rune(value)[:160]) + "…"
}

func buildSourceImportPreview(project *DatasetProject, req sourceImportRequest, document SourceKnowledge, chunks map[string]SourceChunk) *sourceImportPreview {
	preview := &sourceImportPreview{
		ProfileID: req.ProfileID, KnowledgeBaseID: req.KnowledgeBaseID, KnowledgeID: req.KnowledgeID,
		DocumentName: sourceDocumentName(document), ChangedPolicy: req.ChangedPolicy,
		Items: make([]sourceImportPreviewItem, 0, len(req.ChunkIDs)), chunks: chunks,
	}
	for _, chunkID := range req.ChunkIDs {
		item := sourceImportPreviewItem{ChunkID: chunkID}
		chunk, ok := chunks[chunkID]
		if !ok {
			item.Status, item.Message = "invalid", "目标环境未返回该分块"
			preview.Summary.Invalid++
			preview.Items = append(preview.Items, item)
			continue
		}
		item.ChunkIndex = chunk.ChunkIndex
		item.ContentPreview = previewText(chunk.Content)
		item.ContentSHA256 = contentSHA256(chunk.Content)
		if strings.TrimSpace(chunk.Content) == "" {
			item.Status, item.Message = "invalid", "分块正文为空"
			preview.Summary.Invalid++
		} else if !chunk.IsEnabled {
			item.Status, item.Message = "invalid", "分块已禁用"
			preview.Summary.Invalid++
		} else if chunk.ChunkType != "" && chunk.ChunkType != "text" {
			item.Status, item.Message = "invalid", "P0 仅导入文本分块"
			preview.Summary.Invalid++
		} else {
			for _, passage := range project.Passages {
				metadata := passage.Metadata
				if metadata["source_type"] != "weknora-chunk" || metadata["source_profile_id"] != req.ProfileID || metadata["source_chunk_id"] != chunkID {
					continue
				}
				item.ExistingPassageID = passage.ID
				if metadata["source_content_sha256"] == item.ContentSHA256 {
					item.Status, item.Message = "existing", "相同来源和内容已导入"
					preview.Summary.Existing++
				} else {
					item.Status, item.Message = "changed", "相同分块 ID 的正文已变化"
					preview.Summary.Changed++
					if req.ChangedPolicy == "new" {
						preview.Summary.Importable++
					}
				}
				break
			}
			if item.Status == "" {
				item.Status = "new"
				preview.Summary.Importable++
			}
		}
		preview.Items = append(preview.Items, item)
	}
	preview.Summary.Selected = len(req.ChunkIDs)
	return preview
}

func (s *projectStore) previewSourceImport(datasetID string, req sourceImportRequest) (*sourceImportPreview, error) {
	if err := normalizeSourceImportRequest(&req); err != nil {
		return nil, err
	}
	project, err := s.get(datasetID)
	if err != nil {
		return nil, err
	}
	profile, adapter, err := s.chunkSourceProfile(req.ProfileID)
	if err != nil {
		return nil, err
	}
	if err := ensureProductionChunkSourceCompatible(profile, adapter, req.KnowledgeBaseID); err != nil {
		return nil, err
	}
	document, err := findSourceKnowledge(adapter, profile, req.KnowledgeBaseID, req.KnowledgeID)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(req.ChunkIDs))
	for _, id := range req.ChunkIDs {
		wanted[id] = true
	}
	chunks, err := fetchSelectedSourceChunks(adapter, profile, req.KnowledgeID, wanted)
	if err != nil {
		return nil, err
	}
	return buildSourceImportPreview(project, req, document, chunks), nil
}

func (s *projectStore) importSourceChunks(datasetID string, req sourceImportRequest) (*sourceImportResult, error) {
	if err := normalizeSourceImportRequest(&req); err != nil {
		return nil, err
	}
	preview, err := s.previewSourceImport(datasetID, req)
	if err != nil {
		return nil, err
	}
	if preview.Summary.Importable == 0 {
		return nil, errors.New("没有可导入的新语料")
	}
	path, err := s.projectPath(datasetID)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	project, err := readProjectFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errDatasetNotFound
	}
	if err != nil {
		return nil, err
	}
	// Reclassify under the write lock so a concurrent import cannot create a duplicate.
	document := SourceKnowledge{ID: preview.KnowledgeID, FileName: preview.DocumentName}
	lockedPreview := buildSourceImportPreview(project, req, document, preview.chunks)
	importedIDs := make([]int64, 0, lockedPreview.Summary.Importable)
	for _, item := range lockedPreview.Items {
		if item.Status != "new" && !(item.Status == "changed" && req.ChangedPolicy == "new") {
			continue
		}
		chunk := preview.chunks[item.ChunkID]
		passageID := project.NextPassageID
		project.NextPassageID++
		project.Passages = append(project.Passages, Passage{
			ID: passageID, Text: chunk.Content,
			Source:      preview.DocumentName + fmt.Sprintf(" / 分块 %d", chunk.ChunkIndex),
			ReviewState: "draft",
			Metadata: map[string]string{
				"source_type": "weknora-chunk", "source_profile_id": req.ProfileID,
				"source_knowledge_base_id": req.KnowledgeBaseID, "source_knowledge_id": req.KnowledgeID,
				"source_chunk_id": chunk.ID, "source_document_name": preview.DocumentName,
				"source_chunk_index": fmt.Sprint(chunk.ChunkIndex), "source_content_sha256": item.ContentSHA256,
				"imported_at": now.Format(time.RFC3339),
			},
		})
		importedIDs = append(importedIDs, passageID)
	}
	project.UpdatedAt = now
	if err := s.backupLocked(path, datasetID); err != nil {
		return nil, err
	}
	if err := writeProjectFileAtomic(path, project); err != nil {
		return nil, err
	}
	lockedPreview.Summary.Imported = len(importedIDs)
	lockedPreview.Summary.Skipped = lockedPreview.Summary.Selected - len(importedIDs)
	sort.Slice(importedIDs, func(i, j int) bool { return importedIDs[i] < importedIDs[j] })
	return &sourceImportResult{Project: project, Summary: lockedPreview.Summary, ImportedPassageIDs: importedIDs}, nil
}
