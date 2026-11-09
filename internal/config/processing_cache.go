package config

import (
	"time"

	"github.com/Tencent/WeKnora/internal/processingcache"
)

// ProcessingCacheConfig controls optional persistent processing-stage reuse.
type ProcessingCacheConfig struct {
	ReadEnabled     bool   `yaml:"read_enabled"      json:"read_enabled"`
	WriteEnabled    bool   `yaml:"write_enabled"     json:"write_enabled"`
	MaxBytes        int    `yaml:"max_bytes"         json:"max_bytes"`
	ParsingMaxBytes int    `yaml:"parsing_max_bytes" json:"parsing_max_bytes"`
	ImageMaxBytes   int    `yaml:"image_max_bytes"   json:"image_max_bytes"`
	RetentionDays   int    `yaml:"retention_days"    json:"retention_days"`
	Epoch           string `yaml:"epoch"             json:"epoch"`
}

// Options keeps retention and payload size bounded even for omitted settings.
func (c *ProcessingCacheConfig) Options() processingcache.Options {
	o := processingcache.Options{MaxBytes: 1 << 20, TTL: 30 * 24 * time.Hour, Epoch: "1"}
	o.StageMaxBytes = map[string]int{"document.parse.v1": 64 << 20, "image.preprocess.v1": 16 << 20}
	if c == nil {
		return o
	}
	o.ReadEnabled, o.WriteEnabled = c.ReadEnabled, c.WriteEnabled
	if c.ParsingMaxBytes > 0 {
		o.StageMaxBytes["document.parse.v1"] = min(c.ParsingMaxBytes, 256<<20)
	}
	if c.ImageMaxBytes > 0 {
		o.StageMaxBytes["image.preprocess.v1"] = min(c.ImageMaxBytes, 64<<20)
	}
	if c.MaxBytes > 0 {
		o.MaxBytes = c.MaxBytes
	}
	if c.RetentionDays > 0 {
		o.TTL = time.Duration(min(c.RetentionDays, 3650)) * 24 * time.Hour
	}
	if c.Epoch != "" {
		o.Epoch = c.Epoch
	}
	return o
}
