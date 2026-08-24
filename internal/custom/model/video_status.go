package model

const (
	VideoStatusUploading    = "uploading"
	VideoStatusUploaded     = "uploaded"
	VideoStatusInitializing = "initializing"
	VideoStatusReady        = "ready"
	VideoStatusProcessing   = "processing"
	VideoStatusCompleted    = "completed"
	VideoStatusFailed       = "failed"
)

// VideoIsReadyForHome 返回视频是否已经进入初始可用状态。
func VideoIsReadyForHome(status string) bool {
	return status == VideoStatusReady || status == VideoStatusProcessing || status == VideoStatusCompleted
}
