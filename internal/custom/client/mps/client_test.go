package mps

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	mpssdk "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/mps/v20190612"
)

func TestParseTaskDetailMapsSmartSubtitleSegments(t *testing.T) {
	segment := &mpssdk.SmartSubtitleTaskAsrFullTextSegmentItem{
		Text: common.StringPtr("第一段"), StartTimeOffset: common.Float64Ptr(1.25), EndTimeOffset: common.Float64Ptr(2.75), SpeakerId: common.StringPtr("1"),
	}
	fullText := &mpssdk.SmartSubtitleTaskAsrFullTextResult{
		Output: &mpssdk.SmartSubtitleTaskAsrFullTextResultOutput{
			SubtitlePath: common.StringPtr("https://cos.example/subtitle.vtt"), SegmentSet: []*mpssdk.SmartSubtitleTaskAsrFullTextSegmentItem{segment},
		},
	}
	smartSubtitle := &mpssdk.ScheduleSmartSubtitleTaskResult{
		Status: common.StringPtr("SUCCESS"), Output: []*mpssdk.SmartSubtitlesResult{{AsrFullTextTask: fullText}},
	}
	detail := &mpssdk.DescribeTaskDetailResponseParams{
		Status: common.StringPtr("FINISH"),
		ScheduleTask: &mpssdk.ScheduleTask{
			Status: common.StringPtr("FINISH"), ActivityResultSet: []*mpssdk.ActivityResult{{ActivityResItem: &mpssdk.ActivityResItem{SmartSubtitlesTask: smartSubtitle}}},
		},
	}

	task, err := parseTaskDetail("task-1", detail)
	require.NoError(t, err)
	require.Equal(t, "FINISH", task.Status)
	require.Equal(t, 100, task.Progress)
	require.Equal(t, "https://cos.example/subtitle.vtt", task.Result.SubtitlePath)
	require.Equal(t, []Segment{{SourceSegmentID: "mps:task-1:000000", Text: "第一段", StartMs: 1250, EndMs: 2750, SpeakerID: "1"}}, task.Result.Segments)
}

func TestParseTaskDetailMapsWorkflowSmartSubtitleSegments(t *testing.T) {
	detail := &mpssdk.DescribeTaskDetailResponseParams{
		TaskType: common.StringPtr("WorkflowTask"), Status: common.StringPtr("FINISH"),
		WorkflowTask: &mpssdk.WorkflowTask{
			Status: common.StringPtr("FINISH"),
			SmartSubtitlesTaskResult: []*mpssdk.SmartSubtitlesResult{{
				Type: common.StringPtr("AsrFullTextRecognition"),
				AsrFullTextTask: &mpssdk.SmartSubtitleTaskAsrFullTextResult{
					Status: common.StringPtr("SUCCESS"), Progress: common.Uint64Ptr(100),
					Output: &mpssdk.SmartSubtitleTaskAsrFullTextResultOutput{
						SubtitlePath: common.StringPtr("https://cos.example/subtitle.vtt"),
						SegmentSet: []*mpssdk.SmartSubtitleTaskAsrFullTextSegmentItem{{
							Text: common.StringPtr("工作流结果"), StartTimeOffset: common.Float64Ptr(3.5), EndTimeOffset: common.Float64Ptr(5.25),
						}},
					},
				},
			}},
		},
	}

	task, err := parseTaskDetail("workflow-task", detail)
	require.NoError(t, err)
	require.Equal(t, "FINISH", task.Status)
	require.Equal(t, 100, task.Progress)
	require.Equal(t, "https://cos.example/subtitle.vtt", task.Result.SubtitlePath)
	require.Equal(t, []Segment{{SourceSegmentID: "mps:workflow-task:000000", Text: "工作流结果", StartMs: 3500, EndMs: 5250}}, task.Result.Segments)
}

func TestParseTaskDetailRejectsFinishedEmptyResult(t *testing.T) {
	_, err := parseTaskDetail("task-1", &mpssdk.DescribeTaskDetailResponseParams{Status: common.StringPtr("FINISH")})
	require.ErrorContains(t, err, "without subtitle segments")
}

func TestParseTaskDetailPreservesNestedASRError(t *testing.T) {
	detail := &mpssdk.DescribeTaskDetailResponseParams{
		Status: common.StringPtr("FINISH"),
		ScheduleTask: &mpssdk.ScheduleTask{
			Status: common.StringPtr("FINISH"),
			ActivityResultSet: []*mpssdk.ActivityResult{{ActivityResItem: &mpssdk.ActivityResItem{
				SmartSubtitlesTask: &mpssdk.ScheduleSmartSubtitleTaskResult{
					Status: common.StringPtr("SUCCESS"),
					Output: []*mpssdk.SmartSubtitlesResult{{AsrFullTextTask: &mpssdk.SmartSubtitleTaskAsrFullTextResult{
						Status: common.StringPtr("FAIL"), ErrCodeExt: common.StringPtr("InternalError.Asr"), Message: common.StringPtr("ASR unavailable"),
					}}},
				},
			}}},
		},
	}

	task, err := parseTaskDetail("task-1", detail)
	require.NoError(t, err)
	require.Equal(t, "InternalError.Asr", task.ErrorCode)
	require.Equal(t, "ASR unavailable", task.ErrorMsg)
}

func TestNormalizeOutputDir(t *testing.T) {
	require.Equal(t, "/subtitles/", normalizeOutputDir("subtitles"))
	require.Equal(t, "/", normalizeOutputDir(""))
}
