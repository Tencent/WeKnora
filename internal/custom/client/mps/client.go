// Package mps 腾讯云媒体处理（MPS）智能字幕适配层。
package mps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	mpssdk "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/mps/v20190612"
)

const Provider = "tencent_mps"

type Client struct {
	api     *mpssdk.Client
	cfg     config.MPSConfig
	timeout time.Duration
}

type Segment struct {
	SourceSegmentID string  `json:"source_segment_id"`
	Text            string  `json:"text"`
	StartMs         int     `json:"start_ms"`
	EndMs           int     `json:"end_ms"`
	SpeakerID       string  `json:"speaker_id,omitempty"`
	Confidence      float64 `json:"confidence,omitempty"`
}

type Result struct {
	TaskID       string    `json:"task_id"`
	Segments     []Segment `json:"segments"`
	SubtitlePath string    `json:"subtitle_path,omitempty"`
}

type Task struct {
	TaskID    string
	Status    string
	Progress  int
	ErrorCode string
	ErrorMsg  string
	Result    Result
}

func New(cfg config.MPSConfig) (*Client, error) {
	if strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("tencent mps credentials are not configured")
	}
	cred := common.NewCredential(cfg.SecretID, cfg.SecretKey)
	profileCfg := profile.NewClientProfile()
	profileCfg.HttpProfile = profile.NewHttpProfile()
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint != "" {
		profileCfg.HttpProfile.Endpoint = endpoint
	}
	apiClient, err := mpssdk.NewClient(cred, cfg.Region, profileCfg)
	if err != nil {
		return nil, fmt.Errorf("create mps client: %w", err)
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &Client{api: apiClient, cfg: cfg, timeout: timeout}, nil
}

func (c *Client) CreateTask(ctx context.Context, sourceURL, sessionID string) (string, error) {
	if c == nil || c.api == nil {
		return "", fmt.Errorf("mps client is not configured")
	}
	if strings.TrimSpace(sourceURL) == "" {
		return "", fmt.Errorf("mps source url is empty")
	}
	definition := c.cfg.TemplateID
	if definition == 0 {
		definition = 307
	}
	request := mpssdk.NewProcessMediaRequest()
	request.InputInfo = &mpssdk.MediaInputInfo{
		Type:         common.StringPtr("URL"),
		UrlInputInfo: &mpssdk.UrlInputInfo{Url: common.StringPtr(sourceURL)},
	}
	request.SmartSubtitlesTask = &mpssdk.SmartSubtitlesTaskInput{Definition: common.Uint64Ptr(definition)}
	request.TaskType = common.StringPtr("Online")
	request.SessionId = common.StringPtr(sessionID)
	if bucket := strings.TrimSpace(c.cfg.OutputBucket); bucket != "" {
		region := strings.TrimSpace(c.cfg.OutputRegion)
		if region == "" {
			region = c.cfg.Region
		}
		request.OutputStorage = &mpssdk.TaskOutputStorage{
			Type:             common.StringPtr("COS"),
			CosOutputStorage: &mpssdk.CosOutputStorage{Bucket: common.StringPtr(bucket), Region: common.StringPtr(region)},
		}
		request.OutputDir = common.StringPtr(normalizeOutputDir(c.cfg.OutputDir))
	}
	resp, err := c.api.ProcessMediaWithContext(ctx, request)
	if err != nil {
		return "", fmt.Errorf("mps process media: %w", err)
	}
	if resp == nil || resp.Response == nil || strings.TrimSpace(ptrString(resp.Response.TaskId)) == "" {
		return "", fmt.Errorf("mps process media returned empty task id")
	}
	return ptrString(resp.Response.TaskId), nil
}

func (c *Client) GetTask(ctx context.Context, taskID string) (*Task, error) {
	if c == nil || c.api == nil {
		return nil, fmt.Errorf("mps client is not configured")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("mps task id is empty")
	}
	request := mpssdk.NewDescribeTaskDetailRequest()
	request.TaskId = common.StringPtr(taskID)
	resp, err := c.api.DescribeTaskDetailWithContext(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("mps describe task: %w", err)
	}
	if resp == nil || resp.Response == nil {
		return nil, fmt.Errorf("mps describe task returned empty response")
	}
	return parseTaskDetail(taskID, resp.Response)
}

func parseTaskDetail(taskID string, detail *mpssdk.DescribeTaskDetailResponseParams) (*Task, error) {
	if detail == nil {
		return nil, fmt.Errorf("mps describe task returned empty response")
	}
	task := &Task{TaskID: taskID, Status: strings.ToUpper(ptrString(detail.Status))}
	task.Result.TaskID = taskID
	if detail.WorkflowTask != nil {
		task.Status = strings.ToUpper(ptrString(detail.WorkflowTask.Status))
		if detail.WorkflowTask.ErrCode != nil && *detail.WorkflowTask.ErrCode != 0 {
			task.ErrorCode = fmt.Sprintf("%d", *detail.WorkflowTask.ErrCode)
			task.ErrorMsg = ptrString(detail.WorkflowTask.Message)
		}
		for _, output := range detail.WorkflowTask.SmartSubtitlesTaskResult {
			applySmartSubtitleResult(task, output)
		}
	}
	if detail.ScheduleTask != nil {
		task.Status = strings.ToUpper(ptrString(detail.ScheduleTask.Status))
		if detail.ScheduleTask.ErrCode != nil && *detail.ScheduleTask.ErrCode != 0 {
			task.ErrorCode = fmt.Sprintf("%d", *detail.ScheduleTask.ErrCode)
			task.ErrorMsg = ptrString(detail.ScheduleTask.Message)
		}
		for _, activity := range detail.ScheduleTask.ActivityResultSet {
			if activity == nil || activity.ActivityResItem == nil || activity.ActivityResItem.SmartSubtitlesTask == nil {
				continue
			}
			result := activity.ActivityResItem.SmartSubtitlesTask
			if strings.EqualFold(ptrString(result.Status), "SUCCESS") {
				task.Progress = 100
			}
			if strings.EqualFold(ptrString(result.Status), "FAIL") {
				task.ErrorCode = ptrString(result.ErrCodeExt)
				task.ErrorMsg = ptrString(result.Message)
			}
			for _, output := range result.Output {
				applySmartSubtitleResult(task, output)
			}
		}
	}
	if task.Status == "FINISH" && task.ErrorCode == "" && len(task.Result.Segments) == 0 {
		return task, fmt.Errorf("mps task finished without subtitle segments")
	}
	return task, nil
}

func applySmartSubtitleResult(task *Task, output *mpssdk.SmartSubtitlesResult) {
	if task == nil || output == nil || output.AsrFullTextTask == nil {
		return
	}
	asr := output.AsrFullTextTask
	if asr.Progress != nil {
		task.Progress = int(*asr.Progress)
	}
	if strings.EqualFold(ptrString(asr.Status), "SUCCESS") {
		task.Progress = 100
	}
	if strings.EqualFold(ptrString(asr.Status), "FAIL") {
		task.ErrorCode = ptrString(asr.ErrCodeExt)
		if task.ErrorCode == "" && asr.ErrCode != nil {
			task.ErrorCode = fmt.Sprintf("%d", *asr.ErrCode)
		}
		task.ErrorMsg = ptrString(asr.Message)
	}
	if asr.Output == nil {
		return
	}
	full := asr.Output
	if value := strings.TrimSpace(ptrString(full.SubtitlePath)); value != "" {
		task.Result.SubtitlePath = value
	}
	for _, segment := range full.SegmentSet {
		if segment == nil || strings.TrimSpace(ptrString(segment.Text)) == "" {
			continue
		}
		start, end := 0.0, 0.0
		if segment.StartTimeOffset != nil {
			start = *segment.StartTimeOffset
		}
		if segment.EndTimeOffset != nil {
			end = *segment.EndTimeOffset
		}
		if end <= start {
			continue
		}
		index := len(task.Result.Segments)
		task.Result.Segments = append(task.Result.Segments, Segment{
			SourceSegmentID: fmt.Sprintf("mps:%s:%06d", task.TaskID, index),
			Text:            ptrString(segment.Text), StartMs: int(start * 1000), EndMs: int(end * 1000),
			SpeakerID:  ptrString(segment.SpeakerId),
			Confidence: valueFloat(segment.Confidence),
		})
	}
}

func (c *Client) Timeout() time.Duration { return c.timeout }

func (c *Client) PollInterval() time.Duration {
	interval := time.Duration(c.cfg.PollIntervalSeconds) * time.Second
	if interval <= 0 {
		return 5 * time.Second
	}
	return interval
}

func (r Result) JSON() (string, error) {
	b, err := json.Marshal(r)
	return string(b), err
}

func normalizeOutputDir(value string) string {
	if strings.TrimSpace(value) == "" {
		return "/"
	}
	value = "/" + strings.Trim(strings.TrimSpace(value), "/") + "/"
	return value
}

func valueFloat(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
