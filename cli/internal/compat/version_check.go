// Package compat probes server `/system/info` and decides client-server
// version skew level. Used by `weknora doctor` 的 server_version 检查项。
//
// 24h TTL state file cache, three-tier compat semantics
// (major-mismatch=hard / minor-mismatch=soft / equal=ok).
package compat

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/Tencent/WeKnora/client"
)

// Info is the cached server version snapshot.
type Info struct {
	ServerVersion string            `yaml:"server_version"`
	EngineInfo    map[string]string `yaml:"engine_info,omitempty"`
	ProbedAt      time.Time         `yaml:"probed_at"`
}

// ProbeService is the narrow SDK surface compat depends on (testability via
// fake; mirrors v0.0 service interface pattern).
type ProbeService interface {
	GetSystemInfo(ctx context.Context) (*sdk.SystemInfo, error)
}

// Probe calls server /system/info and returns a fresh Info snapshot.
func Probe(ctx context.Context, c ProbeService) (*Info, error) {
	resp, err := c.GetSystemInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("probe server: %w", err)
	}
	return &Info{
		ServerVersion: resp.Version,
		ProbedAt:      time.Now(),
	}, nil
}
