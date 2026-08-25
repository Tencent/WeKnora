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

// VideoIsInitiallyAvailable 返回视频是否已达"初始可用"：核心文件就绪且封面已终态。
// 封面终态 = 已生成封面（thumbnail_url 非空），或初始处理已结束、封面彻底失败后降级为占位图。
func VideoIsInitiallyAvailable(status, fileURL, thumbnailURL string) bool {
	if !VideoIsReadyForHome(status) || strings.TrimSpace(fileURL) == "" {
		return false
	}
	return strings.TrimSpace(thumbnailURL) != "" || VideoIsCoverSettled(status)
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
// 失败记录即使没有可播放文件也要保留，便于前端展示失败原因和提供重试入口。
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
