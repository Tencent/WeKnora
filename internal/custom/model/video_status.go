package model

import "strings"

const (
	VideoStatusUploading    = "uploading"
	VideoStatusUploaded     = "uploaded"
	VideoStatusInitializing = "initializing"
	VideoStatusReady        = "ready"
	VideoStatusProcessing   = "processing"
	VideoStatusCompleted    = "completed"
	VideoStatusFailed       = "failed"
)

// VideoIsReadyForHome 返回视频是否已经进入可以出现在首页的状态。
// 文件是否已经合并完成由 VideoIsInitiallyAvailable 额外判断。
func VideoIsReadyForHome(status string) bool {
	switch status {
	case VideoStatusUploaded, VideoStatusInitializing, VideoStatusReady, VideoStatusProcessing, VideoStatusCompleted:
		return true
	default:
		return false
	}
}

// VideoIsInitiallyAvailable 返回视频是否已完成上传并可在产品中使用。
// 封面和时长属于异步增强信息，不应阻塞视频播放或列表展示。
func VideoIsInitiallyAvailable(status, fileURL, thumbnailURL string) bool {
	if !VideoIsReadyForHome(status) || strings.TrimSpace(fileURL) == "" {
		return false
	}
	return true
}

// VideoIsPlayable 是对业务语义更明确的别名：只要核心视频文件已合并即可播放。
// thumbnailURL 保留在参数中兼容旧调用方，封面生成失败不影响播放。
func VideoIsPlayable(status, fileURL, thumbnailURL string) bool {
	return VideoIsInitiallyAvailable(status, fileURL, thumbnailURL)
}

// VideoIsCoverSettled 返回无封面视频的初始处理是否已结束（成功进入后续流程或彻底失败降级）。
// uploaded / initializing 表示封面仍在生成中，不算结束。
func VideoIsCoverSettled(status string) bool {
	switch status {
	case VideoStatusReady, VideoStatusProcessing, VideoStatusCompleted:
		return true
	default:
		return false
	}
}

// VideoIsVisibleInList 返回视频是否应出现在列表中。
// 文件合并完成后立即展示，封面和时长由后台异步补齐。
func VideoIsVisibleInList(status, fileURL, thumbnailURL string) bool {
	return VideoIsInitiallyAvailable(status, fileURL, thumbnailURL) || status == VideoStatusFailed
}

// VideoInitiallyAvailableStatuses 返回初始可用状态集合，供数据库查询复用。
func VideoInitiallyAvailableStatuses() []string {
	return []string{
		VideoStatusUploaded,
		VideoStatusInitializing,
		VideoStatusReady,
		VideoStatusProcessing,
		VideoStatusCompleted,
	}
}

// VideoCoverSettledStatuses 返回"无封面也视为可用"的状态集合，供数据库查询复用。
func VideoCoverSettledStatuses() []string {
	return []string{
		VideoStatusReady,
		VideoStatusProcessing,
		VideoStatusCompleted,
	}
}
