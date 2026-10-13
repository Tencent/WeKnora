package handler

import (
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/im"
	"io"
	"os"
	"time"
)

type OperationsHost struct {
	CPUCount             *int     `json:"cpu_count,omitempty"`
	Load1m               *float64 `json:"load_1m,omitempty"`
	MemoryTotalBytes     *uint64  `json:"memory_total_bytes,omitempty"`
	MemoryAvailableBytes *uint64  `json:"memory_available_bytes,omitempty"`
}
type OperationsDisk struct {
	Name           string   `json:"name"`
	Available      bool     `json:"available"`
	UsedPercent    *float64 `json:"used_percent,omitempty"`
	AvailableBytes *uint64  `json:"available_bytes,omitempty"`
	TotalBytes     *uint64  `json:"total_bytes,omitempty"`
}
type OperationsBackup struct {
	Name          string `json:"name"`
	Available     bool   `json:"available"`
	SnapshotCount uint64 `json:"snapshot_count"`
	CompletedAt   *int64 `json:"completed_at,omitempty"`
	Stale         bool   `json:"stale"`
}
type OperationsLog struct {
	Name     string  `json:"name"`
	Bytes    *uint64 `json:"bytes,omitempty"`
	MaxSize  string  `json:"max_size,omitempty"`
	MaxFiles string  `json:"max_files,omitempty"`
	Status   string  `json:"status,omitempty"`
	Health   string  `json:"health,omitempty"`
}
type OperationsService struct {
	Name            string `json:"name"`
	Available       bool   `json:"available"`
	Result          string `json:"result,omitempty"`
	Active          string `json:"active,omitempty"`
	ExitStatus      *int   `json:"exit_status,omitempty"`
	RunningRequired bool   `json:"running_required"`
}

// OperationsStatus contains aggregate telemetry from an optional external collector.
type OperationsStatus struct {
	Available   bool                `json:"available"`
	Stale       bool                `json:"stale"`
	CollectedAt int64               `json:"collected_at,omitempty"`
	Host        *OperationsHost     `json:"host,omitempty"`
	Disks       []OperationsDisk    `json:"disks,omitempty"`
	Backups     []OperationsBackup  `json:"backups,omitempty"`
	Logs        []OperationsLog     `json:"logs,omitempty"`
	Services    []OperationsService `json:"services,omitempty"`
}

func readOperationsStatus(path string, now time.Time) OperationsStatus {
	if path == "" {
		return OperationsStatus{}
	}
	// Reject pipes/devices before opening: this request must not wait for a producer.
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 128*1024 {
		return OperationsStatus{}
	}
	f, err := os.Open(path)
	if err != nil {
		return OperationsStatus{}
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 128*1024+1))
	var status OperationsStatus
	if err != nil || len(b) > 128*1024 || json.Unmarshal(b, &status) != nil || status.CollectedAt <= 0 || status.CollectedAt > now.Unix()+60 {
		return OperationsStatus{}
	}
	status.Available = true
	status.Stale = now.Unix()-status.CollectedAt > 600
	return status
}

func (h *SystemHandler) BindIMRuntime(service *im.Service) {
	h.imRuntime = service.RuntimeStatus
}
