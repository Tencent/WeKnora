package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dingtalkConnector "github.com/Tencent/WeKnora/internal/datasource/connector/dingtalk"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const dingtalkExportDownloadLimit = 100 * 1024 * 1024

type dataSourceExportIngestor interface {
	IngestFetchedItem(ctx context.Context, dataSourceID string, item *types.FetchedItem) error
}

type DingTalkExportService struct {
	taskRepo   interfaces.DingTalkExportTaskRepository
	ingestor   dataSourceExportIngestor
	httpClient *http.Client
}

func NewDingTalkExportService(
	taskRepo interfaces.DingTalkExportTaskRepository,
	dataSourceService interfaces.DataSourceService,
) interfaces.DingTalkExportService {
	return &DingTalkExportService{
		taskRepo: taskRepo,
		ingestor: dataSourceService,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (s *DingTalkExportService) HandleExportFinishEvent(ctx context.Context, payload []byte) error {
	event, err := dingtalkConnector.ParseExportFinishEvent(payload)
	if err != nil {
		return err
	}

	task, err := s.taskRepo.FindByTaskID(ctx, event.TaskID)
	if err != nil {
		return err
	}
	if task.DentryUUID != "" && task.DentryUUID != event.DentryUUID {
		return fmt.Errorf("dingtalk export event dentry uuid mismatch: task=%s event=%s", task.DentryUUID, event.DentryUUID)
	}
	if task.Status == types.DingTalkExportTaskStatusSucceeded {
		return nil
	}

	if !event.Success {
		return s.taskRepo.MarkFailed(ctx, event.TaskID, event.EventID, event.ErrorCode, event.ErrorMessage)
	}
	if strings.TrimSpace(event.URL) == "" {
		return fmt.Errorf("dingtalk export event %s has empty download url", event.EventID)
	}

	content, err := s.downloadExport(ctx, event.URL)
	if err != nil {
		return err
	}
	item := dingtalkExportFetchedItem(task, event, content)
	if err := s.ingestor.IngestFetchedItem(ctx, task.DataSourceID, item); err != nil {
		return err
	}
	return s.taskRepo.MarkSucceeded(ctx, event.TaskID, event.EventID, event.URL)
}

func (s *DingTalkExportService) downloadExport(ctx context.Context, endpoint string) ([]byte, error) {
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download dingtalk export status=%d body=%s", resp.StatusCode, string(body))
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, dingtalkExportDownloadLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read dingtalk export: %w", err)
	}
	if len(content) > dingtalkExportDownloadLimit {
		return nil, fmt.Errorf("dingtalk export exceeds %d bytes", dingtalkExportDownloadLimit)
	}
	return content, nil
}

func dingtalkExportFetchedItem(
	task *types.DingTalkExportTask,
	event *dingtalkConnector.ExportFinishEvent,
	content []byte,
) *types.FetchedItem {
	title := firstNonEmptyString(event.Name, task.Title)
	fileName := task.FileName
	if strings.TrimSpace(fileName) == "" {
		fileName = exportMarkdownFileName(title)
	}
	return &types.FetchedItem{
		ExternalID:       task.ExternalID,
		Title:            title,
		Content:          content,
		ContentType:      "text/markdown",
		FileName:         fileName,
		URL:              task.SourceURL,
		SourceResourceID: task.SourceResourceID,
		Metadata: map[string]string{
			"channel":         types.ChannelDingtalk,
			"workspace_id":    task.WorkspaceID,
			"node_id":         task.NodeID,
			"dentry_uuid":     task.DentryUUID,
			"fetcher":         "export",
			"fidelity":        "official_markdown_export",
			"export_task_id":  event.TaskID,
			"export_event_id": event.EventID,
			"target_format":   firstNonEmptyString(event.Format, "markdown"),
			"extension":       event.Extension,
		},
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func exportMarkdownFileName(title string) string {
	name := firstNonEmptyString(title, "dingtalk-export")
	lower := strings.ToLower(name)
	for _, ext := range []string{".md", ".markdown", ".adoc", ".asciidoc", ".txt", ".doc", ".docx"} {
		if strings.HasSuffix(lower, ext) {
			name = strings.TrimSpace(name[:len(name)-len(ext)])
			break
		}
	}
	if name == "" {
		name = "dingtalk-export"
	}
	return name + ".md"
}
