// Command mps-smoke runs one real Tencent MPS smart-subtitle task against a
// local media file. It prints only non-sensitive validation results.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/client/mps"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	mpssdk "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/mps/v20190612"
	cos "github.com/tencentyun/cos-go-sdk-v5"
)

const maxSubtitleBytes = 4 << 20

var subtitleTiming = regexp.MustCompile(`(?m)^\s*\d{1,2}:\d{2}(?::\d{2})?[,.]\d{3}\s+-->\s+\d{1,2}:\d{2}(?::\d{2})?[,.]\d{3}`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "MPS smoke failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	secretID := strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_ID"))
	secretKey := strings.TrimSpace(os.Getenv("TENCENTCLOUD_SECRET_KEY"))
	region := envOr("TENCENTCLOUD_REGION", "ap-guangzhou")
	bucket := strings.TrimSpace(os.Getenv("TENCENTCLOUD_MPS_OUTPUT_BUCKET"))
	mediaPath := strings.TrimSpace(os.Getenv("MPS_SMOKE_FILE"))
	taskID := strings.TrimSpace(os.Getenv("MPS_SMOKE_TASK_ID"))
	if secretID == "" || secretKey == "" {
		return fmt.Errorf("missing TENCENTCLOUD_SECRET_ID or TENCENTCLOUD_SECRET_KEY")
	}
	if bucket == "" {
		return fmt.Errorf("missing TENCENTCLOUD_MPS_OUTPUT_BUCKET")
	}
	if mediaPath == "" && taskID == "" {
		return fmt.Errorf("missing MPS_SMOKE_FILE or MPS_SMOKE_TASK_ID")
	}

	cosClient, err := newCOSClient(bucket, region, secretID, secretKey)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	mpsClient, err := mps.New(config.MPSConfig{
		SecretID: secretID, SecretKey: secretKey, Region: region,
		Endpoint: "mps.tencentcloudapi.com", OutputBucket: bucket,
		OutputRegion: region, OutputDir: "/vidsage-smoke/subtitles/",
		TemplateID: 307, PollIntervalSeconds: 5, TimeoutSeconds: 2100,
	})
	if err != nil {
		return err
	}

	taskStarted := time.Now()
	if taskID == "" {
		stat, err := os.Stat(mediaPath)
		if err != nil {
			return fmt.Errorf("inspect media file: %w", err)
		}
		if !stat.Mode().IsRegular() {
			return fmt.Errorf("media path is not a regular file")
		}
		objectKey := fmt.Sprintf("vidsage-smoke/%s-%s", time.Now().UTC().Format("20060102T150405Z"), filepath.Base(mediaPath))
		uploadStarted := time.Now()
		if _, err := cosClient.Object.PutFromFile(ctx, objectKey, mediaPath, nil); err != nil {
			return fmt.Errorf("upload media to COS: %w", err)
		}
		fmt.Printf("COS upload: ok (bucket=%s, region=%s, object=%s, bytes=%d, elapsed=%s)\n", bucket, region, objectKey, stat.Size(), elapsed(uploadStarted))
		sourceURL, err := cosClient.Object.GetPresignedURL(ctx, http.MethodGet, objectKey, secretID, secretKey, 2*time.Hour, nil)
		if err != nil {
			return fmt.Errorf("sign media URL: %w", err)
		}
		taskID, err = mpsClient.CreateTask(ctx, sourceURL.String(), "vidsage-smoke:"+time.Now().UTC().Format("20060102T150405Z"))
		if err != nil {
			return err
		}
		fmt.Printf("MPS task: created (task_id=%s)\n", taskID)
	} else {
		fmt.Printf("MPS task: resumed (task_id=%s)\n", taskID)
	}

	var result mps.Result
	for {
		task, err := mpsClient.GetTask(ctx, taskID)
		if err != nil {
			printTaskShape(ctx, secretID, secretKey, region, taskID)
			return err
		}
		fmt.Printf("MPS poll: status=%s progress=%d elapsed=%s\n", task.Status, task.Progress, elapsed(taskStarted))
		switch task.Status {
		case "FINISH":
			if task.ErrorCode != "" {
				return fmt.Errorf("task failed: code=%s message=%s", task.ErrorCode, task.ErrorMsg)
			}
			result = task.Result
			goto completed
		case "WAITING", "PROCESSING", "":
		case "FAIL":
			return fmt.Errorf("task failed: code=%s message=%s", task.ErrorCode, task.ErrorMsg)
		default:
			return fmt.Errorf("unknown task status %q", task.Status)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for task: %w", ctx.Err())
		case <-time.After(mpsClient.PollInterval()):
		}
	}

completed:
	if err := validateSegments(result.Segments); err != nil {
		return err
	}
	first, last := result.Segments[0], result.Segments[len(result.Segments)-1]
	fmt.Printf("MPS JSON: ok (segments=%d, first=%d-%dms, last=%d-%dms, elapsed=%s)\n", len(result.Segments), first.StartMs, first.EndMs, last.StartMs, last.EndMs, elapsed(taskStarted))

	if strings.TrimSpace(result.SubtitlePath) == "" {
		return fmt.Errorf("task returned no subtitle path")
	}
	subtitleURL, accessMode, err := readableSubtitleURL(ctx, cosClient, result.SubtitlePath, secretID, secretKey)
	if err != nil {
		return err
	}
	format, cueCount, size, err := inspectSubtitle(ctx, subtitleURL)
	if err != nil {
		return err
	}
	fmt.Printf("MPS subtitle: ok (access=%s, format=%s, cues=%d, bytes=%d)\n", accessMode, format, cueCount, size)
	fmt.Printf("MPS smoke: passed (task_id=%s, total_elapsed=%s)\n", taskID, elapsed(taskStarted))
	return nil
}

func printTaskShape(ctx context.Context, secretID, secretKey, region, taskID string) {
	clientProfile := profile.NewClientProfile()
	clientProfile.HttpProfile.Endpoint = "mps.tencentcloudapi.com"
	client, err := mpssdk.NewClient(common.NewCredential(secretID, secretKey), region, clientProfile)
	if err != nil {
		fmt.Printf("MPS diagnostic: client_error=%v\n", err)
		return
	}
	request := mpssdk.NewDescribeTaskDetailRequest()
	request.TaskId = common.StringPtr(taskID)
	response, err := client.DescribeTaskDetailWithContext(ctx, request)
	if err != nil || response == nil || response.Response == nil {
		fmt.Printf("MPS diagnostic: describe_error=%v\n", err)
		return
	}
	detail := response.Response
	fmt.Printf("MPS diagnostic: root_status=%s schedule_present=%t\n", stringValue(detail.Status), detail.ScheduleTask != nil)
	if detail.ScheduleTask == nil {
		return
	}
	schedule := detail.ScheduleTask
	fmt.Printf("MPS diagnostic: schedule_status=%s schedule_error=%d activities=%d\n", stringValue(schedule.Status), int64Value(schedule.ErrCode), len(schedule.ActivityResultSet))
	for index, activity := range schedule.ActivityResultSet {
		if activity == nil || activity.ActivityResItem == nil {
			fmt.Printf("MPS diagnostic: activity=%d type=%s result_present=false\n", index, stringValue(activityType(activity)))
			continue
		}
		item := activity.ActivityResItem
		fmt.Printf("MPS diagnostic: activity=%d type=%s smart_subtitles=%t recognition=%t analysis=%t\n", index, stringValue(activity.ActivityType), item.SmartSubtitlesTask != nil, item.RecognitionTask != nil, item.AnalysisTask != nil)
		if item.SmartSubtitlesTask == nil {
			continue
		}
		smart := item.SmartSubtitlesTask
		fmt.Printf("MPS diagnostic: smart_status=%s smart_error=%s outputs=%d\n", stringValue(smart.Status), stringValue(smart.ErrCodeExt), len(smart.Output))
		for outputIndex, output := range smart.Output {
			if output == nil {
				fmt.Printf("MPS diagnostic: output=%d present=false\n", outputIndex)
				continue
			}
			fmt.Printf("MPS diagnostic: output=%d type=%s asr=%t ocr=%t translated=%t\n", outputIndex, stringValue(output.Type), output.AsrFullTextTask != nil, output.OcrFullTextTask != nil, output.TransTextTask != nil)
			if output.AsrFullTextTask != nil {
				asr := output.AsrFullTextTask
				segments := 0
				pathPresent := false
				if asr.Output != nil {
					segments = len(asr.Output.SegmentSet)
					pathPresent = strings.TrimSpace(stringValue(asr.Output.SubtitlePath)) != ""
				}
				fmt.Printf("MPS diagnostic: asr_status=%s asr_error=%s output_present=%t segments=%d subtitle_path=%t\n", stringValue(asr.Status), stringValue(asr.ErrCodeExt), asr.Output != nil, segments, pathPresent)
			}
		}
	}
}

func activityType(activity *mpssdk.ActivityResult) *string {
	if activity == nil {
		return nil
	}
	return activity.ActivityType
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func newCOSClient(bucket, region, secretID, secretKey string) (*cos.Client, error) {
	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
	if err != nil {
		return nil, fmt.Errorf("build COS URL: %w", err)
	}
	httpClient := &http.Client{Transport: &cos.AuthorizationTransport{
		SecretID: secretID, SecretKey: secretKey,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
	}}
	return cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, httpClient), nil
}

func validateSegments(segments []mps.Segment) error {
	if len(segments) == 0 {
		return fmt.Errorf("MPS JSON contains no segments")
	}
	previousStart := -1
	seen := make(map[string]struct{}, len(segments))
	for index, segment := range segments {
		if strings.TrimSpace(segment.SourceSegmentID) == "" || strings.TrimSpace(segment.Text) == "" {
			return fmt.Errorf("segment %d lacks stable ID or text", index)
		}
		if segment.StartMs < 0 || segment.EndMs <= segment.StartMs || segment.StartMs < previousStart {
			return fmt.Errorf("segment %d has invalid timing %d-%dms", index, segment.StartMs, segment.EndMs)
		}
		if _, exists := seen[segment.SourceSegmentID]; exists {
			return fmt.Errorf("segment %d has duplicate stable ID", index)
		}
		seen[segment.SourceSegmentID] = struct{}{}
		previousStart = segment.StartMs
	}
	return nil
}

func readableSubtitleURL(ctx context.Context, client *cos.Client, rawURL, secretID, secretKey string) (string, string, error) {
	if err := checkURL(ctx, rawURL); err == nil {
		return rawURL, "direct", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || strings.TrimSpace(parsed.Path) == "" {
		return "", "", fmt.Errorf("subtitle URL is invalid or inaccessible")
	}
	objectKey := strings.TrimPrefix(parsed.EscapedPath(), "/")
	objectKey, err = url.PathUnescape(objectKey)
	if err != nil {
		return "", "", fmt.Errorf("decode subtitle object path: %w", err)
	}
	signed, err := client.Object.GetPresignedURL(ctx, http.MethodGet, objectKey, secretID, secretKey, 30*time.Minute, nil)
	if err != nil {
		return "", "", fmt.Errorf("sign subtitle URL: %w", err)
	}
	if err := checkURL(ctx, signed.String()); err != nil {
		return "", "", fmt.Errorf("subtitle is inaccessible directly and with COS signature: %w", err)
	}
	return signed.String(), "signed", nil
}

func checkURL(ctx context.Context, value string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func inspectSubtitle(ctx context.Context, value string) (string, int, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
	if err != nil {
		return "", 0, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, 0, fmt.Errorf("download subtitle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, 0, fmt.Errorf("download subtitle: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSubtitleBytes+1))
	if err != nil {
		return "", 0, 0, fmt.Errorf("read subtitle: %w", err)
	}
	if len(body) > maxSubtitleBytes {
		return "", 0, 0, fmt.Errorf("subtitle exceeds %d bytes", maxSubtitleBytes)
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	format := "SRT"
	if strings.HasPrefix(strings.TrimSpace(text), "WEBVTT") {
		format = "WebVTT"
	}
	count := len(subtitleTiming.FindAllStringIndex(text, -1))
	if count == 0 {
		return "", 0, len(body), fmt.Errorf("subtitle has no parseable timed cues")
	}
	return format, count, len(body), nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func elapsed(start time.Time) time.Duration {
	return time.Since(start).Round(time.Millisecond)
}
